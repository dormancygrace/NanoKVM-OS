//! USB/EDID monitor association. Native audio/capture hooks are explicit;
//! unavailable hardware is never simulated by the default runtime backend.
use crate::{fsroot, store::atomic_write, Error};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::Mutex,
};
pub trait Backend: Send + Sync {
    fn stop_audio(&self) -> Result<(), Error>;
    fn apply_monitor_profile(&self, path: &Path) -> Result<(), Error>;
}
pub struct Unavailable;
impl Backend for Unavailable {
    fn stop_audio(&self) -> Result<(), Error> {
        Err("native audio backend is not linked".into())
    }
    fn apply_monitor_profile(&self, _: &Path) -> Result<(), Error> {
        Err("native monitor backend is not linked".into())
    }
}
pub struct Monitor {
    pub(crate) backend: std::sync::Arc<dyn Backend>,
    lock: Mutex<()>,
}
impl Monitor {
    pub fn new(backend: std::sync::Arc<dyn Backend>) -> Self {
        Self {
            backend,
            lock: Mutex::new(()),
        }
    }
    pub fn apply_pointer(&self, root: &Path, enabled: bool) -> Result<(), Error> {
        let _guard = self.lock.lock().map_err(|_| "monitor state unavailable")?;
        let path = fsroot::resolve(root, &saved_profile(root), false)?;
        if !enabled {
            return self.backend.apply_monitor_profile(&path);
        }
        if !pointer_supported(root) {
            return Err(
                "Windows pointer requires ContainerID kernel support and live EDID programming"
                    .into(),
            );
        }
        let id = container_id(root)?;
        let data = decorate(&fs::read(path)?, &id)?;
        let directory = fsroot::resolve(root, Path::new("/run"), false)?;
        let mut random = [0u8; 16];
        getrandom::fill(&mut random)?;
        let name = random
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>();
        let temporary = directory.join(format!("nanokvm-pointer-{name}.bin"));
        atomic_write(&temporary, &data, 0o600)?;
        let result = self.backend.apply_monitor_profile(&temporary);
        let _ = fs::remove_file(temporary);
        result
    }
}
fn text(root: &Path, path: &str) -> String {
    fsroot::resolve(root, Path::new(path), false)
        .and_then(|path| Ok(fs::read_to_string(path)?))
        .unwrap_or_default()
        .trim()
        .to_owned()
}
pub fn pointer_supported(root: &Path) -> bool {
    let board = text(root, "/etc/kvm/hw");
    let chip = text(root, "/etc/kvm/hdmi_version");
    board == "pcie"
        && chip == "ux"
        && fsroot::resolve(
            root,
            Path::new("/sys/kernel/config/usb_gadget/g0/os_desc/container_id"),
            false,
        )
        .is_ok()
}
fn saved_profile(root: &Path) -> PathBuf {
    let board = text(root, "/etc/kvm/hw");
    let cube = board == "alpha" || board == "beta";
    let height = text(root, "/etc/kvm/monitor_resolution")
        .parse::<u16>()
        .ok()
        .filter(|h| [0, 600, 720, 1080, 1440].contains(h))
        .unwrap_or(0);
    let portrait = ["1", "true", "yes", "on"].contains(
        &text(root, "/etc/kvm/monitor_portrait")
            .to_lowercase()
            .as_str(),
    );
    let name = if portrait {
        match text(root, "/etc/kvm/monitor_portrait_resolution")
            .parse::<u16>()
            .unwrap_or(1920)
        {
            1280 => "NanoKVM-portrait-720x1280.bin".to_owned(),
            2304 => "NanoKVM-portrait-1296x2304.bin".to_owned(),
            2560 => "NanoKVM-portrait-1440x2560.bin".to_owned(),
            _ => "NanoKVM-portrait-1080x1920.bin".to_owned(),
        }
    } else if cube {
        format!(
            "NanoKVM-cube-monitor-{}.bin",
            if height == 0 { 1080 } else { height }
        )
    } else if height != 0 {
        format!("NanoKVM-monitor-{height}.bin")
    } else {
        let ion = fsroot::resolve(
            root,
            Path::new("/proc/device-tree/reserved-memory/ion/size"),
            false,
        )
        .and_then(|path| Ok(fs::read(path)?))
        .unwrap_or_default();
        if ion.len() == 4
            && u32::from_be_bytes(ion.as_slice().try_into().unwrap()) >= 62 * 1024 * 1024
        {
            "NanoKVM-final-video-profiles.bin".to_owned()
        } else {
            "NanoKVM-stock.bin".to_owned()
        }
    };
    Path::new("/usr/share/nanokvm/edid").join(name)
}
pub fn parse_container_id(text: &str) -> Result<[u8; 16], Error> {
    let bytes = text.trim().as_bytes();
    if bytes.len() != 36 || [8, 13, 18, 23].into_iter().any(|i| bytes[i] != b'-') {
        return Err("invalid USB ContainerID".into());
    }
    let mut id = [0u8; 16];
    let hex = |b: u8| -> Result<u8, Error> {
        match b {
            b'0'..=b'9' => Ok(b - b'0'),
            b'a'..=b'f' => Ok(b - b'a' + 10),
            b'A'..=b'F' => Ok(b - b'A' + 10),
            _ => Err("invalid USB ContainerID".into()),
        }
    };
    let raw: Vec<_> = bytes.iter().copied().filter(|b| *b != b'-').collect();
    if raw.len() != 32 {
        return Err("invalid USB ContainerID".into());
    }
    for (n, pair) in raw.as_chunks::<2>().0.iter().enumerate() {
        id[n] = (hex(pair[0])? << 4) | hex(pair[1])?;
    }
    if id == [0; 16] {
        return Err("invalid USB ContainerID".into());
    }
    Ok(id)
}
fn container_id(root: &Path) -> Result<[u8; 16], Error> {
    let path = fsroot::resolve(root, Path::new("/etc/kvm/usb_container_id"), true)?;
    let data = match fs::read_to_string(&path) {
        Ok(data) => data,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            let mut id = [0u8; 16];
            getrandom::fill(&mut id)?;
            id[6] = (id[6] & 15) | 0x40;
            id[8] = (id[8] & 63) | 0x80;
            let hex = id.iter().map(|b| format!("{b:02x}")).collect::<String>();
            let data = format!(
                "{}-{}-{}-{}-{}\n",
                &hex[..8],
                &hex[8..12],
                &hex[12..16],
                &hex[16..20],
                &hex[20..]
            );
            atomic_write(&path, data.as_bytes(), 0o600)?;
            data
        }
        Err(error) => return Err(error.into()),
    };
    parse_container_id(&data)
}
pub fn decorate(original: &[u8], id: &[u8; 16]) -> Result<Vec<u8>, Error> {
    if original.len() != 256
        || original[..8] != [0, 255, 255, 255, 255, 255, 255, 0]
        || original[126] != 1
        || original[128] != 2
        || original[129] != 3
    {
        return Err("unsupported EDID layout for Windows pointer".into());
    }
    for block in original.as_chunks::<128>().0 {
        if block.iter().fold(0u8, |sum, b| sum.wrapping_add(*b)) != 0 {
            return Err("invalid EDID checksum".into());
        }
    }
    let end = original[130] as usize;
    if !(4..=127).contains(&end) {
        return Err("invalid CTA boundary".into());
    }
    let mut data = original.to_vec();
    let mut blocks = Vec::new();
    let mut timings = Vec::new();
    let mut pos = 132;
    while pos < 128 + end {
        let size = 1 + usize::from(original[pos] & 31);
        if pos + size > 128 + end {
            return Err("invalid CTA data block".into());
        }
        let block = &original[pos..pos + size];
        if !(block[0] >> 5 == 3 && block.len() >= 4 && block[1..4] == [0x5c, 0x12, 0xca]) {
            blocks.extend_from_slice(block);
        }
        pos += size;
    }
    blocks.extend_from_slice(&[0x75, 0x5c, 0x12, 0xca, 3, 0x42]);
    blocks.extend_from_slice(id);
    pos = 128 + end;
    while pos + 18 <= 255 {
        let timing = &original[pos..pos + 18];
        if timing[0] == 0 && timing[1] == 0 {
            break;
        }
        if 4 + blocks.len() + timings.len() + 18 <= 127 || timing != &original[54..72] {
            timings.extend_from_slice(timing);
        }
        pos += 18;
    }
    if 4 + blocks.len() + timings.len() > 127 {
        if let Some(index) = timings
            .as_chunks::<18>()
            .0
            .iter()
            .position(|timing| timing == &original[54..72])
        {
            timings.drain(index * 18..index * 18 + 18);
        }
    }
    for pos in [72, 90, 108] {
        if 4 + blocks.len() + timings.len() <= 127 {
            break;
        }
        if data[pos] == 0
            && data[pos + 1] == 0
            && [0x10, 0xff].contains(&data[pos + 3])
            && timings.len() >= 18
        {
            data[pos..pos + 18].copy_from_slice(&timings[timings.len() - 18..]);
            timings.truncate(timings.len() - 18);
        }
    }
    if 4 + blocks.len() + timings.len() > 127 {
        return Err("EDID has no room for monitor association without losing timings".into());
    }
    data[128..].fill(0);
    data[128] = 2;
    data[129] = 3;
    data[130] = (4 + blocks.len()) as u8;
    data[131] = original[131] & 0xf0;
    data[132..132 + blocks.len()].copy_from_slice(&blocks);
    data[132 + blocks.len()..132 + blocks.len() + timings.len()].copy_from_slice(&timings);
    for offset in [0, 128] {
        data[offset + 127] = 0u8.wrapping_sub(
            data[offset..offset + 127]
                .iter()
                .fold(0u8, |sum, b| sum.wrapping_add(*b)),
        );
    }
    Ok(data)
}
