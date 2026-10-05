//! Linux GPIO-v2 and legacy sysfs ATX. Every acquired output is released.
use crate::Error;
use std::{
    fs::{self, File, OpenOptions},
    io::{Read, Seek, SeekFrom, Write},
    os::{
        fd::{AsRawFd, FromRawFd, IntoRawFd, OwnedFd},
        unix::fs::OpenOptionsExt,
    },
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};
pub trait Line: Send {
    fn set(&mut self, active: bool) -> Result<(), Error>;
    fn get(&mut self) -> Result<bool, Error>;
    fn close(&mut self) -> Result<(), Error>;
}
pub trait Backend: Send + Sync {
    fn open(&self, device: &str, output: bool) -> Result<Box<dyn Line>, Error>;
}
// repr(C) follows the matched kernel UAPI, including 8-byte aligned unions.
#[repr(C)]
#[derive(Default)]
struct ChipInfo {
    name: [u8; 32],
    label: [u8; 32],
    lines: u32,
}
#[repr(C)]
#[derive(Default)]
struct Values {
    bits: u64,
    mask: u64,
}
#[repr(C)]
#[derive(Default, Clone, Copy)]
struct Attribute {
    id: u32,
    padding: u32,
    value: u64,
}
#[repr(C)]
#[derive(Default, Clone, Copy)]
struct ConfigAttribute {
    attr: Attribute,
    mask: u64,
}
#[repr(C)]
#[derive(Default)]
struct Config {
    flags: u64,
    num_attrs: u32,
    padding: [u32; 5],
    attrs: [ConfigAttribute; 10],
}
#[repr(C)]
struct Request {
    offsets: [u32; 64],
    consumer: [u8; 32],
    config: Config,
    num_lines: u32,
    event_buffer_size: u32,
    padding: [u32; 5],
    fd: i32,
}
const CHIP_INFO: libc::c_ulong = 0x8044b401;
const GET_LINE: libc::c_ulong = 0xc250b407;
const GET_VALUES: libc::c_ulong = 0xc010b40e;
const SET_VALUES: libc::c_ulong = 0xc010b40f;
fn request(offset: u32, output: bool) -> Request {
    let mut result = Request {
        offsets: [0; 64],
        consumer: [0; 32],
        config: Config::default(),
        num_lines: 1,
        event_buffer_size: 0,
        padding: [0; 5],
        fd: -1,
    };
    result.offsets[0] = offset;
    result.consumer[..11].copy_from_slice(b"nanokvm-atx");
    result.config.flags = if output { 8 } else { 4 };
    if output {
        result.config.num_attrs = 1;
        result.config.attrs[0] = ConfigAttribute {
            attr: Attribute {
                id: 2,
                padding: 0,
                value: 0,
            },
            mask: 1,
        };
    }
    result
}
fn ioctl<T>(fd: &impl AsRawFd, command: libc::c_ulong, value: &mut T) -> Result<(), Error> {
    // The ioctl size and T layout are proven against C and x/sys fixtures.
    if unsafe { libc::ioctl(fd.as_raw_fd(), command as _, value as *mut T) } < 0 {
        Err(std::io::Error::last_os_error().into())
    } else {
        Ok(())
    }
}
pub struct Native {
    root: PathBuf,
}
impl Native {
    pub fn new(root: PathBuf) -> Self {
        Self { root }
    }
}
impl Backend for Native {
    fn open(&self, device: &str, output: bool) -> Result<Box<dyn Line>, Error> {
        if !device.starts_with("gpio-v2:") {
            if !device.starts_with("/sys/class/gpio/gpio") || !device.ends_with("/value") {
                return Err(format!("invalid GPIO device {device:?}").into());
            }
            let path = crate::fsroot::resolve(&self.root, Path::new(device), false)?;
            // Pin an existing descriptor. Do not recreate missing sysfs nodes.
            let file = OpenOptions::new()
                .read(!output)
                .write(output)
                .custom_flags(libc::O_CLOEXEC | libc::O_NOFOLLOW | libc::O_NONBLOCK)
                .open(path)?;
            return Ok(Box::new(Sysfs { file: Some(file) }));
        }
        let parts: Vec<_> = device.split(':').collect();
        if parts.len() != 3 || parts[1].is_empty() {
            return Err(format!("invalid GPIO-v2 device {device:?}").into());
        }
        let offset = parts[2].parse::<u32>()?;
        if !parts[2].bytes().all(|b| b.is_ascii_digit()) {
            return Err("invalid GPIO-v2 offset".into());
        }
        // An isolated server must never issue a GPIO request on its host.
        if self.root != Path::new("/") {
            return Err("GPIO-v2 unavailable in isolated root".into());
        }
        let mut paths = fs::read_dir("/dev")?
            .filter_map(Result::ok)
            .map(|e| e.path())
            .filter(|p| {
                p.file_name()
                    .is_some_and(|n| n.as_encoded_bytes().starts_with(b"gpiochip"))
            })
            .collect::<Vec<_>>();
        paths.sort();
        for path in paths {
            let chip = OpenOptions::new()
                .read(true)
                .custom_flags(libc::O_CLOEXEC | libc::O_NOFOLLOW)
                .open(&path)?;
            let mut info = ChipInfo::default();
            ioctl(&chip, CHIP_INFO, &mut info)?;
            let end = info
                .label
                .iter()
                .position(|b| *b == 0)
                .unwrap_or(info.label.len());
            if &info.label[..end] != parts[1].as_bytes() {
                continue;
            }
            if offset >= info.lines {
                return Err("GPIO line outside controller".into());
            }
            let mut req = request(offset, output);
            ioctl(&chip, GET_LINE, &mut req).map_err(|e| format!("request {device}: {e}"))?;
            if req.fd < 0 {
                return Err("GPIO request returned invalid descriptor".into());
            }
            // Kernel returns ownership of this fd; chip drops independently.
            let fd = unsafe { OwnedFd::from_raw_fd(req.fd) };
            return Ok(Box::new(Cdev { fd: Some(fd) }));
        }
        Err(format!("GPIO controller {:?} not found", parts[1]).into())
    }
}
fn close_fd(fd: impl IntoRawFd) -> Result<(), Error> {
    // On Linux the fd is consumed even on EINTR; never retry close.
    if unsafe { libc::close(fd.into_raw_fd()) } < 0 {
        Err(std::io::Error::last_os_error().into())
    } else {
        Ok(())
    }
}
struct Cdev {
    fd: Option<OwnedFd>,
}
impl Line for Cdev {
    fn set(&mut self, active: bool) -> Result<(), Error> {
        ioctl(
            self.fd.as_ref().ok_or("GPIO line closed")?,
            SET_VALUES,
            &mut Values {
                bits: u64::from(active),
                mask: 1,
            },
        )
    }
    fn get(&mut self) -> Result<bool, Error> {
        let mut values = Values { bits: 0, mask: 1 };
        ioctl(
            self.fd.as_ref().ok_or("GPIO line closed")?,
            GET_VALUES,
            &mut values,
        )?;
        Ok(values.bits & 1 != 0)
    }
    fn close(&mut self) -> Result<(), Error> {
        self.fd.take().map_or(Ok(()), close_fd)
    }
}
struct Sysfs {
    file: Option<File>,
}
impl Line for Sysfs {
    fn set(&mut self, active: bool) -> Result<(), Error> {
        let file = self.file.as_mut().ok_or("GPIO line closed")?;
        file.seek(SeekFrom::Start(0))?;
        file.write_all(if active { b"1" } else { b"0" })?;
        Ok(())
    }
    fn get(&mut self) -> Result<bool, Error> {
        let file = self.file.as_mut().ok_or("GPIO line closed")?;
        file.seek(SeekFrom::Start(0))?;
        let mut bytes = Vec::new();
        file.take(64).read_to_end(&mut bytes)?;
        match String::from_utf8_lossy(&bytes).trim() {
            "0" => Ok(false),
            "1" => Ok(true),
            _ => Err(format!("invalid GPIO value {:?}", String::from_utf8_lossy(&bytes)).into()),
        }
    }
    fn close(&mut self) -> Result<(), Error> {
        self.file.take().map_or(Ok(()), close_fd)
    }
}
pub(crate) fn joined(
    result: Result<(), Error>,
    cleanup: impl IntoIterator<Item = Result<(), Error>>,
) -> Result<(), Error> {
    let errors = std::iter::once(result)
        .chain(cleanup)
        .filter_map(Result::err)
        .map(|e| e.to_string())
        .collect::<Vec<_>>();
    if errors.is_empty() {
        Ok(())
    } else {
        Err(errors.join("\n").into())
    }
}
struct Output {
    line: Option<Box<dyn Line>>,
}
impl Output {
    fn finish(&mut self, result: Result<(), Error>) -> Result<(), Error> {
        let Some(mut line) = self.line.take() else {
            return result;
        };
        joined(result, [line.set(false), line.close()])
    }
}
impl Drop for Output {
    fn drop(&mut self) {
        if let Err(error) = self.finish(Ok(())) {
            eprintln!("ATX cleanup failed: {error}");
        }
    }
}
pub struct Controller {
    backend: Arc<dyn Backend>,
    gate: Mutex<()>,
}
impl Controller {
    pub fn new(backend: Arc<dyn Backend>) -> Self {
        Self {
            backend,
            gate: Mutex::new(()),
        }
    }
    pub fn pulse(
        &self,
        device: &str,
        duration: Duration,
        valid: impl Fn() -> Result<(), Error>,
    ) -> Result<(), Error> {
        if duration.is_zero() || duration > Duration::from_secs(60) {
            return Err("ATX duration must be between 1 ns and 60 seconds".into());
        }
        let _guard = match self.gate.try_lock() {
            Ok(guard) => guard,
            Err(std::sync::TryLockError::Poisoned(error)) => error.into_inner(),
            Err(std::sync::TryLockError::WouldBlock) => {
                return Err("another ATX pulse is in progress".into())
            }
        };
        valid()?;
        let mut output = Output {
            line: Some(self.backend.open(device, true)?),
        };
        let result = (|| {
            valid()?;
            output.line.as_mut().unwrap().set(true)?;
            let end = Instant::now() + duration;
            loop {
                valid()?;
                let now = Instant::now();
                if now >= end {
                    return Ok(());
                }
                std::thread::sleep((end - now).min(Duration::from_millis(20)));
            }
        })();
        output.finish(result)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn uapi_sizes_offsets_commands_and_atomic_inactive_output_match_c_and_go() {
        let oracle: serde_json::Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/gpio-go-oracle.json"
        ))
        .unwrap();
        let abi = &oracle["abi"];
        for (name, value) in [
            ("chipSize", size_of::<ChipInfo>() as u64),
            ("valuesSize", size_of::<Values>() as u64),
            ("configSize", size_of::<Config>() as u64),
            ("requestSize", size_of::<Request>() as u64),
            ("configOffset", std::mem::offset_of!(Request, config) as u64),
            ("fdOffset", std::mem::offset_of!(Request, fd) as u64),
            ("chipInfo", CHIP_INFO),
            ("getLine", GET_LINE),
            ("getValues", GET_VALUES),
            ("setValues", SET_VALUES),
        ] {
            assert_eq!(abi[name], value, "{name}");
        }
        assert_eq!(align_of::<Request>(), 8);
        let output = request(27, true);
        assert_eq!(output.offsets[0], 27);
        assert_eq!(output.num_lines, 1);
        assert_eq!(output.config.flags, 8);
        assert_eq!(output.config.num_attrs, 1);
        assert_eq!(output.config.attrs[0].attr.id, 2);
        assert_eq!(output.config.attrs[0].attr.value, 0);
        assert_eq!(output.config.attrs[0].mask, 1);
        assert_eq!(output.config.padding, [0; 5]);
        assert_eq!(output.padding, [0; 5]);
        assert_eq!(request(23, false).config.flags, 4);
        assert_eq!(request(23, false).config.num_attrs, 0);
    }
}
