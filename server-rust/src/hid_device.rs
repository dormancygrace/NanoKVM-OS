//! Shared HID descriptors. All callers serialize with the ownership transition.
//! Device files are opened without creation; each write has a 50 ms deadline.
use crate::{
    config,
    hid_reports::{self, Kind, Report},
    Error,
};
use std::{
    fs::{self, File, OpenOptions},
    os::unix::fs::{FileTypeExt, OpenOptionsExt},
    path::PathBuf,
    sync::Mutex,
    time::{Duration, Instant},
};

#[derive(Default)]
struct State {
    files: [Option<File>; 3],
    held: [Option<Report>; 3],
    windows_pointer: bool,
    led_capable: bool,
    led_retry: Option<Instant>,
}
pub struct Devices {
    root: PathBuf,
    state: Mutex<State>,
    leds: std::sync::Arc<crate::leds::Leds>,
}
fn index(kind: Kind) -> usize {
    match kind {
        Kind::Keyboard => 0,
        Kind::Relative => 1,
        Kind::Absolute => 2,
    }
}
impl Devices {
    pub fn new(root: PathBuf) -> Self {
        let leds = crate::leds::Leds::new(root.clone());
        Self {
            root,
            state: Mutex::new(State::default()),
            leds,
        }
    }
    pub fn leds(&self) -> std::sync::Arc<crate::leds::Leds> {
        self.leds.clone()
    }
    fn open_kind(&self, state: &mut State, kind: Kind) -> Result<(), Error> {
        let i = index(kind);
        if self.disabled(kind) {
            state.files[i] = None;
            if i == 0 {
                let _ = self.leds.replace_reader(None);
            }
            return Ok(());
        }
        if state.files[i].is_none() {
            if i == 2 {
                state.windows_pointer = self.windows();
            }
            let path = self.path(["/dev/hidg0", "/dev/hidg1", "/dev/hidg2"][i])?;
            let file = OpenOptions::new()
                .write(true)
                .custom_flags(libc::O_NONBLOCK | libc::O_NOFOLLOW | libc::O_CLOEXEC)
                .open(path)?;
            if i == 0 {
                state.led_capable = file.metadata()?.file_type().is_char_device();
                state.led_retry = None;
            }
            state.files[i] = Some(file);
        }
        if i == 0
            && state.led_capable
            && self.leds.missing_reader()
            && state
                .led_retry
                .is_none_or(|deadline| Instant::now() >= deadline)
        {
            state.led_retry = Some(Instant::now() + Duration::from_secs(2));
            let result = (|| -> Result<(), Error> {
                let path = self.path("/dev/hidg0")?;
                let file = OpenOptions::new()
                    .read(true)
                    .custom_flags(libc::O_NONBLOCK | libc::O_NOFOLLOW | libc::O_CLOEXEC)
                    .open(path)?;
                self.leds.replace_reader(Some(file))
            })();
            if let Err(error) = result {
                eprintln!("keyboard LED reader open failed: {error}");
            }
        }
        Ok(())
    }
    pub fn open(&self) -> Result<(), Error> {
        self.leds.refresh();
        let mut state = self.state.lock().map_err(|_| "HID state unavailable")?;
        let mut first = None;
        for kind in [Kind::Keyboard, Kind::Relative, Kind::Absolute] {
            if let Err(error) = self.open_kind(&mut state, kind) {
                if first.is_none() {
                    first = Some(error);
                }
            }
        }
        first.map_or(Ok(()), Err)
    }
    fn path(&self, path: &str) -> Result<PathBuf, Error> {
        let path = config::rooted(&self.root, path)?.canonicalize()?;
        if !path.starts_with(&self.root) {
            return Err("HID path outside runtime root".into());
        }
        Ok(path)
    }
    fn disabled(&self, kind: Kind) -> bool {
        [
            "disable_hid",
            match kind {
                Kind::Keyboard => "usb.disable_keyboard",
                Kind::Relative => "usb.disable_relative",
                Kind::Absolute => "usb.disable_absolute",
            },
        ]
        .iter()
        .any(|marker| self.root.join("boot").join(marker).exists())
    }
    fn windows(&self) -> bool {
        self.path("/sys/kernel/config/usb_gadget/g0/functions/hid.GS2/report_desc")
            .ok()
            .and_then(|path| fs::read(path).ok())
            .is_some_and(|bytes| bytes.starts_with(&[5, 0x0d]))
    }
    fn emit(&self, state: &mut State, report: &Report) -> Result<(), Error> {
        let i = index(report.kind());
        if self.disabled(report.kind()) {
            state.files[i] = None;
            if i == 0 {
                let _ = self.leds.replace_reader(None);
            }
            return Ok(());
        }
        self.open_kind(state, report.kind())?;
        for packet in report.packets(state.windows_pointer) {
            if let Err(error) = hid_reports::write_bounded(
                state.files[i].as_mut().unwrap(),
                &packet,
                Duration::from_millis(50),
            ) {
                state.files[i] = None;
                if i == 0 {
                    let _ = self.leds.replace_reader(None);
                }
                return Err(error.into());
            }
        }
        Ok(())
    }
    fn release_kind(&self, state: &mut State, i: usize) -> Result<(), Error> {
        if let Some(report) = state.held[i].clone() {
            self.emit(state, &report.released())?;
            state.held[i] = None;
        }
        Ok(())
    }
    pub fn write(&self, report: &Report) -> Result<(), Error> {
        let mut state = self.state.lock().map_err(|_| "HID state unavailable")?;
        let i = index(report.kind());
        if i > 0 {
            self.release_kind(&mut state, 3 - i)?;
        }
        if let Err(error) = self.emit(&mut state, report) {
            // A descriptor may have accepted a partial Windows packet before
            // failing. Remember the attempted buttons/coordinates for cleanup.
            if report.held() {
                state.held[i] = Some(report.clone());
            }
            let _ = self.release_kind(&mut state, i);
            return Err(error);
        }
        state.held[i] = report.held().then(|| report.clone());
        Ok(())
    }
    pub fn release_all(&self) -> Result<(), Error> {
        let mut state = self.state.lock().map_err(|_| "HID state unavailable")?;
        let mut first = None;
        for i in 0..3 {
            if let Err(error) = self.release_kind(&mut state, i) {
                if first.is_none() {
                    first = Some(error);
                }
            }
        }
        first.map_or(Ok(()), Err)
    }
    pub fn close(&self) -> Result<(), Error> {
        self.leds.refresh();
        let release = self.release_all();
        self.state
            .lock()
            .map_err(|_| "HID state unavailable")?
            .files = Default::default();
        let _ = self.leds.replace_reader(None);
        release
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn report(bytes: &[u8]) -> Report {
        match hid_reports::parse(bytes).unwrap() {
            hid_reports::Frame::Report(report) => report,
            _ => panic!("report"),
        }
    }
    #[test]
    fn descriptor_reports_release_before_pointer_switch_and_confine_devices() {
        let temp = tempfile::tempdir().unwrap();
        fs::create_dir(temp.path().join("dev")).unwrap();
        for i in 0..3 {
            fs::write(temp.path().join(format!("dev/hidg{i}")), []).unwrap();
        }
        let devices = Devices::new(temp.path().canonicalize().unwrap());
        devices
            .write(&report(&[1, 0, 0, 4, 0, 0, 0, 0, 0]))
            .unwrap();
        devices.write(&report(&[2, 1, 0, 0, 0, 0])).unwrap();
        devices.write(&report(&[2, 1, 1, 2, 3, 4, 0, 0])).unwrap();
        devices.release_all().unwrap();
        assert_eq!(
            fs::read(temp.path().join("dev/hidg0")).unwrap(),
            [vec![0, 0, 4, 0, 0, 0, 0, 0], vec![0; 8]].concat()
        );
        assert_eq!(
            fs::read(temp.path().join("dev/hidg1")).unwrap(),
            [vec![1, 0, 0, 0, 0], vec![0; 5]].concat()
        );
        assert_eq!(
            fs::read(temp.path().join("dev/hidg2")).unwrap(),
            [vec![1, 1, 2, 3, 4, 0, 0], vec![0, 1, 2, 3, 4, 0, 0]].concat()
        );
        devices.close().unwrap();
        let outside = tempfile::NamedTempFile::new().unwrap();
        fs::remove_file(temp.path().join("dev/hidg0")).unwrap();
        std::os::unix::fs::symlink(outside.path(), temp.path().join("dev/hidg0")).unwrap();
        assert!(devices
            .write(&report(&[1, 0, 0, 4, 0, 0, 0, 0, 0]))
            .is_err());
        assert_eq!(outside.as_file().metadata().unwrap().len(), 0);
    }
    #[test]
    fn disabled_function_noop_and_windows_pen_cleanup() {
        let temp = tempfile::tempdir().unwrap();
        fs::create_dir(temp.path().join("boot")).unwrap();
        fs::write(temp.path().join("boot/usb.disable_keyboard"), []).unwrap();
        let devices = Devices::new(temp.path().canonicalize().unwrap());
        devices
            .write(&report(&[1, 0, 0, 4, 0, 0, 0, 0, 0]))
            .unwrap();
        devices.release_all().unwrap();
        fs::create_dir(temp.path().join("dev")).unwrap();
        fs::write(temp.path().join("dev/hidg2"), []).unwrap();
        let descriptor = temp
            .path()
            .join("sys/kernel/config/usb_gadget/g0/functions/hid.GS2");
        fs::create_dir_all(&descriptor).unwrap();
        fs::write(descriptor.join("report_desc"), [5, 0x0d]).unwrap();
        devices.write(&report(&[2, 3, 1, 2, 3, 4, 5, 6])).unwrap();
        devices.release_all().unwrap();
        assert_eq!(
            fs::read(temp.path().join("dev/hidg2")).unwrap(),
            [
                vec![1, 7, 1, 2, 3, 4, 0, 4],
                vec![2, 0, 0, 0, 5, 6],
                vec![1, 4, 1, 2, 3, 4, 0, 0],
                vec![2, 0, 0, 0, 0, 0]
            ]
            .concat()
        );
    }
}
