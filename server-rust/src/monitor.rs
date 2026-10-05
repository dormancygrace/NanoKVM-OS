//! Shared monitor/portrait/USB association operations. All profile transactions
//! use one lock; native audio/capture effects remain an explicit backend boundary.
use crate::{
    fsroot,
    screen::{read_video_value, supports_qhd},
    screen_store,
    store::atomic_write,
    Error,
};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::Mutex,
};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct VideoStatus {
    pub gop_mode: u8,
    pub mjpeg_chroma: u16,
    pub chroma_fallback: String,
}
pub trait Backend: Send + Sync {
    fn capture_actor(&self) -> Option<crate::native_capture_actor::Actor> {
        None
    }
    fn stop(&self) {}
    fn join(&self) -> Result<(), Error> {
        Ok(())
    }
    fn set_hdmi(&self, _: bool) -> Result<(), Error> {
        Err("native HDMI backend is not linked".into())
    }
    fn has_hdmi_signal(&self) -> Result<bool, Error> {
        Err("native HDMI signal backend is not linked".into())
    }
    fn set_gop(&self, _: u8) -> Result<(), Error> {
        Err("native GOP backend is not linked".into())
    }
    fn set_mjpeg_chroma(&self, _: u16) -> Result<(), Error> {
        Err("native MJPEG chroma backend is not linked".into())
    }
    fn video_status(&self) -> Result<VideoStatus, Error> {
        Err("native video status backend is not linked".into())
    }
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
#[derive(Clone, Copy, Debug)]
pub struct ProfileStatus {
    pub supported: bool,
    pub requires_power_cycle: bool,
    pub power_cycle_pending: bool,
    pub high_refresh_supported: bool,
    pub portrait: bool,
    pub portrait_supported: bool,
    pub portrait_resolution: u16,
    pub portrait_max_supported: bool,
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
    fn guard(&self, cancelled: &dyn Fn() -> bool) -> Result<std::sync::MutexGuard<'_, ()>, Error> {
        loop {
            if cancelled() {
                return Err("monitor operation cancelled".into());
            }
            match self.lock.try_lock() {
                Ok(guard) => return Ok(guard),
                Err(std::sync::TryLockError::Poisoned(_)) => {
                    return Err("monitor state unavailable".into())
                }
                Err(std::sync::TryLockError::WouldBlock) => {
                    std::thread::sleep(std::time::Duration::from_millis(10))
                }
            }
        }
    }
    pub fn status(&self, root: &Path) -> Result<ProfileStatus, Error> {
        self.status_cancellable(root, &|| false)
    }
    pub(crate) fn status_cancellable(
        &self,
        root: &Path,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<ProfileStatus, Error> {
        let _guard = self.guard(cancelled)?;
        Ok(ProfileStatus {
            supported: profile_supported(root),
            requires_power_cycle: requires_power_cycle(root),
            power_cycle_pending: requires_power_cycle(root)
                && exists(root, "/etc/kvm/monitor_power_cycle_pending"),
            high_refresh_supported: high_refresh_supported(root),
            portrait: portrait_enabled(root),
            portrait_supported: portrait_supported(root),
            portrait_resolution: saved_portrait_resolution(root),
            portrait_max_supported: portrait_max_supported(root),
        })
    }
    pub fn acknowledge_power_cycle(&self, root: &Path) -> Result<(), Error> {
        self.acknowledge_power_cycle_cancellable(root, &|| false)
    }
    pub(crate) fn acknowledge_power_cycle_cancellable(
        &self,
        root: &Path,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        let _guard = self.guard(cancelled)?;
        let logical = "/etc/kvm/monitor_power_cycle_pending";
        let directory = fsroot::resolve(root, Path::new("/etc/kvm"), false)?;
        let path = directory.join("monitor_power_cycle_pending");
        // os.Remove also accepts an empty directory; use remove_dir only for
        // that retained acknowledgement operation, never for setting writes.
        let result = if fs::symlink_metadata(&path).is_ok_and(|m| m.is_dir()) {
            fs::remove_dir(&path)
        } else {
            fs::remove_file(&path)
        };
        match result {
            Ok(()) => {
                fs::File::open(path.parent().ok_or("missing monitor directory")?)?.sync_all()?;
                Ok(())
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(error) => Err(screen_store::file_error("remove", logical, &error.into()).into()),
        }
    }
    pub fn apply_resolution(&self, root: &Path, height: u16) -> Result<(), Error> {
        self.apply_resolution_cancellable(root, height, &|| false)
    }
    pub(crate) fn apply_resolution_cancellable(
        &self,
        root: &Path,
        height: u16,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        if requires_power_cycle(root) && ![0, 720, 1080].contains(&height) {
            return Err("Cube EDID profiles are limited to 720p/1080p at 60 Hz".into());
        }
        let _guard = self.guard(cancelled)?;
        if ![0, 600, 720, 1080, 1440].contains(&height) {
            return Err("unsupported monitor resolution".into());
        }
        require_hardware(root)?;
        let path = if portrait_enabled(root) {
            let resolution = saved_portrait_resolution(root);
            if !portrait_resolution_supported(root, resolution) {
                return Err("portrait monitor profile is unavailable".into());
            }
            portrait_profile(resolution)
        } else {
            landscape_profile(root, height)
        };
        self.apply_profile_locked(root, &path)?;
        screen_store::write(
            root,
            "/etc/kvm/monitor_resolution",
            height.to_string().as_bytes(),
            0o600,
            true,
        )
        .map_err(|error| {
            format!(
                "monitor changed, but saving its setting failed: {}",
                screen_store::file_error("open", "/etc/kvm/monitor_resolution", &error)
            )
            .into()
        })
    }
    pub fn apply_portrait(&self, root: &Path, enabled: bool) -> Result<(), Error> {
        self.apply_portrait_cancellable(root, enabled, &|| false)
    }
    pub(crate) fn apply_portrait_cancellable(
        &self,
        root: &Path,
        enabled: bool,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        let _guard = self.guard(cancelled)?;
        require_hardware(root)?;
        let resolution = saved_portrait_resolution(root);
        if enabled && !portrait_resolution_supported(root, resolution) {
            return Err("portrait monitor profile is unavailable".into());
        }
        let path = if enabled {
            portrait_profile(resolution)
        } else {
            landscape_profile(root, saved_monitor_resolution(root))
        };
        self.apply_profile_locked(root, &path)?;
        screen_store::write(
            root,
            "/etc/kvm/monitor_portrait",
            if enabled { b"1\n" } else { b"0\n" },
            0o600,
            false,
        )
        .map_err(|error| {
            format!(
                "monitor changed, but saving portrait setting failed: {}",
                screen_store::file_error("rename", "/etc/kvm/monitor_portrait", &error)
            )
            .into()
        })
    }
    pub fn apply_portrait_resolution(&self, root: &Path, resolution: u16) -> Result<(), Error> {
        self.apply_portrait_resolution_cancellable(root, resolution, &|| false)
    }
    pub(crate) fn apply_portrait_resolution_cancellable(
        &self,
        root: &Path,
        resolution: u16,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        let _guard = self.guard(cancelled)?;
        if ![1280, 1920, 2304, 2560].contains(&resolution) {
            return Err("unsupported portrait monitor resolution".into());
        }
        if !portrait_resolution_supported(root, resolution) {
            return Err("portrait monitor profile is unavailable".into());
        }
        if portrait_enabled(root) {
            self.apply_profile_locked(root, &portrait_profile(resolution))?;
        }
        screen_store::write(
            root,
            "/etc/kvm/monitor_portrait_resolution",
            format!("{resolution}\n").as_bytes(),
            0o600,
            false,
        )
        .map_err(|error| {
            format!(
                "portrait resolution changed, but saving its setting failed: {}",
                screen_store::file_error("rename", "/etc/kvm/monitor_portrait_resolution", &error)
            )
            .into()
        })
    }
    fn apply_profile_locked(&self, root: &Path, logical: &Path) -> Result<(), Error> {
        let logical_text = logical.to_string_lossy();
        let path = fsroot::resolve(root, logical, false).map_err(|error| {
            format!(
                "monitor profile is unavailable: {}",
                screen_store::file_error("stat", &logical_text, &error)
            )
        })?;
        let metadata = fs::metadata(&path).map_err(|error| {
            format!(
                "monitor profile is unavailable: {}",
                screen_store::file_error("stat", &logical_text, &error.into())
            )
        })?;
        if !metadata.is_file() {
            return Err(format!(
                "monitor profile is unavailable: {logical_text} is not a regular file"
            )
            .into());
        }
        self.apply_pointer_profile_locked(root, &path, exists(root, "/boot/usb.pointer_windows"))
    }
    pub fn apply_pointer(&self, root: &Path, enabled: bool) -> Result<(), Error> {
        let _guard = self.lock.lock().map_err(|_| "monitor state unavailable")?;
        let logical = if portrait_enabled(root) {
            portrait_profile(saved_portrait_resolution(root))
        } else {
            landscape_profile(root, saved_monitor_resolution(root))
        };
        let path = fsroot::resolve(root, &logical, false)?;
        self.apply_pointer_profile_locked(root, &path, enabled)
    }
    fn apply_pointer_profile_locked(
        &self,
        root: &Path,
        path: &Path,
        enabled: bool,
    ) -> Result<(), Error> {
        if !enabled {
            return self.backend.apply_monitor_profile(path);
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
        let name: String = random.iter().map(|b| format!("{b:02x}")).collect();
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
fn exists(root: &Path, path: &str) -> bool {
    fsroot::resolve(root, Path::new(path), false).is_ok()
}
pub fn requires_power_cycle(root: &Path) -> bool {
    matches!(text(root, "/etc/kvm/hw").as_str(), "alpha" | "beta")
}
pub fn profile_supported(root: &Path) -> bool {
    let board = text(root, "/etc/kvm/hw");
    let chip = text(root, "/etc/kvm/hdmi_version");
    (board == "pcie" && chip == "ux")
        || (matches!(board.as_str(), "alpha" | "beta") && matches!(chip.as_str(), "c" | "ux" | "d"))
}
fn require_hardware(root: &Path) -> Result<(), Error> {
    if profile_supported(root) {
        Ok(())
    } else {
        Err("EDID programming is unsupported on this board/HDMI chip".into())
    }
}
pub fn high_refresh_supported(root: &Path) -> bool {
    profile_supported(root) && !requires_power_cycle(root)
}
pub fn pointer_supported(root: &Path) -> bool {
    high_refresh_supported(root)
        && exists(
            root,
            "/sys/kernel/config/usb_gadget/g0/os_desc/container_id",
        )
}
fn portrait_enabled(root: &Path) -> bool {
    ["1", "true", "yes", "on"].contains(
        &text(root, "/etc/kvm/monitor_portrait")
            .to_lowercase()
            .as_str(),
    )
}
fn regular_profile(root: &Path, path: &Path) -> bool {
    fsroot::resolve(root, path, false)
        .and_then(|path| Ok(fs::metadata(path)?))
        .is_ok_and(|m| m.is_file())
}
fn portrait_supported(root: &Path) -> bool {
    high_refresh_supported(root)
        && text(
            root,
            "/sys/module/cv181x_vi/parameters/yuv_bypass_aligned_stride",
        ) == "Y"
        && supports_qhd(root)
        && regular_profile(root, &portrait_profile(1920))
}
fn portrait_max_supported(root: &Path) -> bool {
    let ion = fsroot::resolve(
        root,
        Path::new("/proc/device-tree/reserved-memory/ion/size"),
        false,
    )
    .and_then(|path| Ok(fs::read(path)?))
    .unwrap_or_default();
    high_refresh_supported(root)
        && ion.len() == 4
        && u32::from_be_bytes(ion.as_slice().try_into().unwrap()) >= 64 * 1024 * 1024
        && regular_profile(root, &portrait_profile(2560))
}
fn portrait_resolution_supported(root: &Path, resolution: u16) -> bool {
    match resolution {
        2560 => portrait_max_supported(root),
        1280 | 1920 | 2304 => {
            portrait_supported(root) && regular_profile(root, &portrait_profile(resolution))
        }
        _ => false,
    }
}
fn saved_monitor_resolution(root: &Path) -> u16 {
    u16::try_from(read_video_value(root, "/etc/kvm/monitor_resolution"))
        .ok()
        .filter(|v| [0, 600, 720, 1080, 1440].contains(v))
        .unwrap_or(0)
}
fn saved_portrait_resolution(root: &Path) -> u16 {
    u16::try_from(read_video_value(
        root,
        "/etc/kvm/monitor_portrait_resolution",
    ))
    .ok()
    .filter(|v| [1280, 1920, 2304, 2560].contains(v))
    .unwrap_or(1920)
}
fn portrait_profile(resolution: u16) -> PathBuf {
    Path::new("/usr/share/nanokvm/edid").join(match resolution {
        1280 => "NanoKVM-portrait-720x1280.bin",
        2304 => "NanoKVM-portrait-1296x2304.bin",
        2560 => "NanoKVM-portrait-1440x2560.bin",
        _ => "NanoKVM-portrait-1080x1920.bin",
    })
}
fn landscape_profile(root: &Path, height: u16) -> PathBuf {
    let name = if requires_power_cycle(root) {
        format!(
            "NanoKVM-cube-monitor-{}.bin",
            if height == 0 { 1080 } else { height }
        )
    } else if height != 0 {
        format!("NanoKVM-monitor-{height}.bin")
    } else if supports_qhd(root) {
        "NanoKVM-final-video-profiles.bin".to_owned()
    } else {
        "NanoKVM-stock.bin".to_owned()
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
