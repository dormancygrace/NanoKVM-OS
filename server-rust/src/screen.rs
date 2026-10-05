//! Immutable screen snapshots and retained firmware capture-rate policy.
//! Storage and status reads are confined to a virtual root; no capture starts.
use crate::{fsroot, Error};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::{Mutex, RwLock},
    time::{Duration, Instant},
};

const FILES: [(&str, &str); 5] = [
    ("fps", "/kvmapp/kvm/fps"),
    ("gop_mode", "/kvmapp/kvm/gop_mode"),
    ("mjpeg_chroma", "/kvmapp/kvm/mjpeg_chroma"),
    ("quality", "/kvmapp/kvm/qlty"),
    ("resolution", "/kvmapp/kvm/res"),
];
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Screen {
    pub width: u16,
    pub height: u16,
    pub fps: i64,
    pub quality: u16,
    pub bit_rate: u16,
    pub gop: u8,
    pub gop_mode: u8,
    pub mjpeg_chroma: u16,
}
impl Default for Screen {
    fn default() -> Self {
        Self {
            width: 0,
            height: 0,
            fps: 50,
            quality: 80,
            bit_rate: 3000,
            gop: 30,
            gop_mode: 1,
            mjpeg_chroma: 422,
        }
    }
}
fn resolution(height: u16) -> Option<u16> {
    match height {
        0 => Some(0),
        600 => Some(800),
        720 => Some(1280),
        1080 => Some(1920),
        1440 => Some(2560),
        _ => None,
    }
}
impl Screen {
    /// Core publication preserves Go's ignore/clamp rules. HTTP mutation
    /// validation and durable writes are separate operations.
    pub fn set(&mut self, key: &str, value: i64) {
        match key {
            "resolution" => {
                if let Ok(height) = u16::try_from(value) {
                    if let Some(width) = resolution(height) {
                        self.width = width;
                        self.height = height;
                    }
                }
            }
            "quality" if (1..=100).contains(&value) => self.quality = value as u16,
            "quality" if (101..=20000).contains(&value) => self.bit_rate = value as u16,
            "fps" => self.fps = value.clamp(10, 120),
            "gop" if (1..=100).contains(&value) => self.gop = value as u8,
            "gop_mode" if value == 0 || value == 1 => self.gop_mode = value as u8,
            "mjpeg_chroma" if value == 420 || value == 422 => self.mjpeg_chroma = value as u16,
            _ => {}
        }
    }
    /// Go CheckScreen normalizes known quality/bitrate presets before a new
    /// streamer starts; it does not normalize every field on every snapshot.
    pub fn check(&mut self) {
        if self.mjpeg_chroma != 420 && self.mjpeg_chroma != 422 {
            self.mjpeg_chroma = 422;
        }
        if resolution(self.height).is_none() {
            self.width = 1920;
            self.height = 1080;
        }
        if ![100, 80, 60, 50].contains(&self.quality) {
            self.quality = 80;
        }
        if ![20000, 15000, 10000, 5000, 3000, 2000, 1000].contains(&self.bit_rate) {
            self.bit_rate = 3000;
        }
        if self.gop_mode != 0 && self.gop_mode != 1 {
            self.gop_mode = 1;
        }
    }
    pub fn for_capture(self, input_width: i64, input_height: i64) -> Self {
        let mut next = self;
        let limit = capture_rate_limit(input_width, input_height).min(capture_rate_limit(
            i64::from(self.width),
            i64::from(self.height),
        ));
        next.fps = next.fps.min(limit);
        next
    }
}
pub fn capture_rate_limit(width: i64, height: i64) -> i64 {
    if width <= 0 || height <= 0 {
        return 120;
    }
    let longer = width.max(height);
    let shorter = width.min(height);
    if longer <= 1280 && shorter <= 720 {
        120
    } else if longer <= 1920 && shorter <= 1088 {
        75
    } else {
        50
    }
}
pub fn is_fhd_class(width: i64, height: i64) -> bool {
    if width <= 0 || height <= 0 {
        return false;
    }
    // Retained native portrait alignment is intentionally orientation-specific.
    if width == 1088 && height == 1920 {
        return true;
    }
    width.max(height) <= 1920 && width.min(height) <= 1080
}
fn read(root: &Path, path: &str) -> Result<Vec<u8>, Error> {
    Ok(fs::read(fsroot::resolve(root, Path::new(path), false)?)?)
}
pub fn supports_qhd(root: &Path) -> bool {
    let Ok(data) = read(root, "/proc/device-tree/reserved-memory/ion/size") else {
        return false;
    };
    let Ok(bytes) = <[u8; 4]>::try_from(data.as_slice()) else {
        return false;
    };
    u32::from_be_bytes(bytes) >= 62 * 1024 * 1024
}
// ReadVideoValue discards strconv.Atoi's error. ParseInt saturates range
// failures, including a uint64 overflow before a later invalid character.
// Syntax before that overflow returns zero. This is distinct from strict
// stored-setting parsing, which ignores every Atoi error.
fn status_number(data: &[u8]) -> i64 {
    let text = String::from_utf8_lossy(data);
    let bytes = text.trim().as_bytes();
    if bytes.is_empty() {
        return 0;
    }
    let (negative, digits) = match bytes[0] {
        b'-' => (true, &bytes[1..]),
        b'+' => (false, &bytes[1..]),
        _ => (false, bytes),
    };
    if digits.is_empty() {
        return 0;
    }
    let saturated = if negative { i64::MIN } else { i64::MAX };
    let mut magnitude = 0u64;
    for &digit in digits {
        if !digit.is_ascii_digit() {
            return 0;
        }
        let Some(next) = magnitude
            .checked_mul(10)
            .and_then(|n| n.checked_add(u64::from(digit - b'0')))
        else {
            return saturated;
        };
        magnitude = next;
    }
    if negative {
        if magnitude >= 1u64 << 63 {
            i64::MIN
        } else {
            -(magnitude as i64)
        }
    } else if magnitude > i64::MAX as u64 {
        i64::MAX
    } else {
        magnitude as i64
    }
}
pub fn read_video_value(root: &Path, path: &str) -> i64 {
    read(root, path).map_or(0, |data| status_number(&data))
}
#[derive(Default)]
struct Timing {
    expires: Option<Instant>,
    width: i64,
    height: i64,
}
pub struct Manager {
    root: PathBuf,
    screen: RwLock<Screen>,
    timing: Mutex<Timing>,
}
impl Manager {
    pub fn load(root: &Path) -> Result<Self, Error> {
        Self::load_with_default_chroma(
            root,
            std::env::var_os("NANOKVM_MJPEG_422").is_some_and(|value| value == "0"),
        )
    }
    pub fn load_with_default_chroma(root: &Path, prefer_420: bool) -> Result<Self, Error> {
        let root = root.canonicalize()?;
        let mut screen = Screen::default();
        if prefer_420 {
            screen.mjpeg_chroma = 420;
        }
        for (key, path) in FILES {
            if let Ok(data) = read(&root, path) {
                if let Some(value) = std::str::from_utf8(&data)
                    .ok()
                    .and_then(|text| text.trim().parse::<i64>().ok())
                {
                    screen.set(key, value);
                }
            }
        }
        screen.check();
        if screen.height > 1080 && !supports_qhd(&root) {
            screen.width = 1920;
            screen.height = 1080;
        }
        Ok(Self {
            root,
            screen: RwLock::new(screen),
            timing: Mutex::new(Timing::default()),
        })
    }
    pub fn snapshot(&self) -> Result<Screen, Error> {
        Ok(*self.screen.read().map_err(|_| "screen state unavailable")?)
    }
    pub fn set(&self, key: &str, value: i64) -> Result<(), Error> {
        self.screen
            .write()
            .map_err(|_| "screen state unavailable")?
            .set(key, value);
        Ok(())
    }
    pub fn check(&self) -> Result<(), Error> {
        self.screen
            .write()
            .map_err(|_| "screen state unavailable")?
            .check();
        Ok(())
    }
    pub fn capture_screen(&self) -> Result<Screen, Error> {
        self.capture_with_clock(&Instant::now)
    }
    fn capture_with_clock(&self, clock: &dyn Fn() -> Instant) -> Result<Screen, Error> {
        let screen = self.snapshot()?;
        let mut timing = self
            .timing
            .lock()
            .map_err(|_| "capture timing unavailable")?;
        let now = clock();
        if timing.expires.is_none_or(|deadline| now > deadline) {
            timing.width = read_video_value(&self.root, "/run/nanokvm/width");
            timing.height = read_video_value(&self.root, "/run/nanokvm/height");
            timing.expires = Some(clock() + Duration::from_secs(1));
        }
        Ok(screen.for_capture(timing.width, timing.height))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::{json, Value};
    use std::{
        os::unix::fs::symlink,
        sync::{Arc, Barrier},
    };
    fn value(screen: Screen) -> Value {
        json!({"Width":screen.width,"Height":screen.height,"FPS":screen.fps,
            "Quality":screen.quality,"BitRate":screen.bit_rate,"GOP":screen.gop,
            "GOPMode":screen.gop_mode,"MjpegChroma":screen.mjpeg_chroma})
    }
    fn oracle() -> Value {
        serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/screen-state-go-oracle.json"
        ))
        .unwrap()
    }
    fn roots(root: &Path) {
        for dir in [
            "kvmapp/kvm",
            "proc/device-tree/reserved-memory/ion",
            "run/nanokvm",
            "etc/kvm",
        ] {
            fs::create_dir_all(root.join(dir)).unwrap();
        }
    }
    #[test]
    fn actual_go_boot_publication_effective_rate_and_status_number_cases() {
        for row in oracle()["cases"].as_array().unwrap() {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path();
            roots(root);
            let c = &row["case"];
            for (name, data) in c["Files"].as_object().unwrap() {
                fs::write(root.join("kvmapp/kvm").join(name), data.as_str().unwrap()).unwrap();
            }
            let ion = c["Ion"].as_str().unwrap();
            if !ion.is_empty() {
                let bytes: Vec<u8> = ion
                    .as_bytes()
                    .as_chunks::<2>()
                    .0
                    .iter()
                    .map(|pair| u8::from_str_radix(std::str::from_utf8(pair).unwrap(), 16).unwrap())
                    .collect();
                fs::write(
                    root.join("proc/device-tree/reserved-memory/ion/size"),
                    bytes,
                )
                .unwrap();
            }
            for (name, key) in [("width", "InputWidth"), ("height", "InputHeight")] {
                if let Some(text) = c[key].as_str().filter(|s| !s.is_empty()) {
                    fs::write(root.join("run/nanokvm").join(name), text).unwrap();
                }
            }
            let manager = Manager::load_with_default_chroma(root, c["Env"] == "0").unwrap();
            assert_eq!(
                value(manager.snapshot().unwrap()),
                row["initial"],
                "{}",
                c["Name"]
            );
            assert_eq!(
                value(manager.capture_screen().unwrap()),
                row["capture"],
                "{}",
                c["Name"]
            );
            assert_eq!(
                value(manager.snapshot().unwrap()),
                row["savedAfterCapture"],
                "{}",
                c["Name"]
            );
            assert_eq!(
                supports_qhd(root),
                row["qhd"].as_bool().unwrap(),
                "{}",
                c["Name"]
            );
            for (name, key) in [("width", "inputWidth"), ("height", "inputHeight")] {
                assert_eq!(
                    read_video_value(root, &format!("/run/nanokvm/{name}")),
                    row[key].as_i64().unwrap(),
                    "{}",
                    c["Name"]
                );
            }
            for published in row["published"].as_array().unwrap() {
                let step = &published["step"];
                if step["Key"] == "check" {
                    manager.check().unwrap();
                } else {
                    manager
                        .set(
                            step["Key"].as_str().unwrap(),
                            step["Value"].as_i64().unwrap(),
                        )
                        .unwrap();
                }
                assert_eq!(
                    value(manager.snapshot().unwrap()),
                    published["screen"],
                    "{}: {}",
                    c["Name"],
                    step
                );
            }
        }
    }
    #[test]
    fn actual_go_orientation_rate_and_fhd_class_matrix() {
        for row in oracle()["rates"].as_array().unwrap() {
            let w = row["width"].as_i64().unwrap();
            let h = row["height"].as_i64().unwrap();
            assert_eq!(
                capture_rate_limit(w, h),
                row["limit"].as_i64().unwrap(),
                "{w}x{h}"
            );
            assert_eq!(is_fhd_class(w, h), row["fhd"].as_bool().unwrap(), "{w}x{h}");
        }
    }
    #[test]
    fn actual_go_check_normalizes_only_retained_fields() {
        for row in oracle()["checks"].as_array().unwrap() {
            let raw = &row["input"];
            let mut screen = Screen {
                width: raw["Width"].as_u64().unwrap() as u16,
                height: raw["Height"].as_u64().unwrap() as u16,
                fps: raw["FPS"].as_i64().unwrap(),
                quality: raw["Quality"].as_u64().unwrap() as u16,
                bit_rate: raw["BitRate"].as_u64().unwrap() as u16,
                gop: raw["GOP"].as_u64().unwrap() as u8,
                gop_mode: raw["GOPMode"].as_u64().unwrap() as u8,
                mjpeg_chroma: raw["MjpegChroma"].as_u64().unwrap() as u16,
            };
            screen.check();
            assert_eq!(value(screen), row["output"]);
        }
    }
    #[test]
    fn cached_input_and_live_output_limits_never_change_the_saved_fps() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        roots(root);
        fs::write(root.join("kvmapp/kvm/fps"), "120").unwrap();
        fs::write(root.join("run/nanokvm/width"), "1280").unwrap();
        fs::write(root.join("run/nanokvm/height"), "720").unwrap();
        let manager = Manager::load_with_default_chroma(root, false).unwrap();
        let base = Instant::now();
        assert_eq!(manager.capture_with_clock(&|| base).unwrap().fps, 120);
        fs::write(root.join("run/nanokvm/width"), "2560").unwrap();
        fs::write(root.join("run/nanokvm/height"), "1440").unwrap();
        assert_eq!(
            manager
                .capture_with_clock(&|| base + Duration::from_millis(500))
                .unwrap()
                .fps,
            120
        );
        manager.set("resolution", 1440).unwrap();
        assert_eq!(
            manager
                .capture_with_clock(&|| base + Duration::from_millis(600))
                .unwrap()
                .fps,
            50
        );
        manager.set("resolution", 0).unwrap();
        assert_eq!(
            manager
                .capture_with_clock(&|| base + Duration::from_millis(700))
                .unwrap()
                .fps,
            120
        );
        assert_eq!(
            manager
                .capture_with_clock(&|| base + Duration::from_millis(1001))
                .unwrap()
                .fps,
            50
        );
        fs::write(root.join("run/nanokvm/width"), "1088").unwrap();
        fs::write(root.join("run/nanokvm/height"), "1920").unwrap();
        assert_eq!(
            manager
                .capture_with_clock(&|| base + Duration::from_millis(2002))
                .unwrap()
                .fps,
            75
        );
        assert_eq!(manager.snapshot().unwrap().fps, 120);
        assert_eq!(fs::read(root.join("kvmapp/kvm/fps")).unwrap(), b"120");
    }
    #[test]
    fn concurrent_publication_never_exposes_a_mixed_resolution_snapshot() {
        let temp = tempfile::tempdir().unwrap();
        roots(temp.path());
        let manager = Arc::new(Manager::load_with_default_chroma(temp.path(), false).unwrap());
        let barrier = Arc::new(Barrier::new(10));
        let mut threads = vec![];
        for _ in 0..8 {
            let m = manager.clone();
            let b = barrier.clone();
            threads.push(std::thread::spawn(move || {
                b.wait();
                for _ in 0..2000 {
                    let screen = m.snapshot().unwrap();
                    assert_eq!(resolution(screen.height), Some(screen.width));
                }
            }));
        }
        let m = manager.clone();
        let b = barrier.clone();
        threads.push(std::thread::spawn(move || {
            b.wait();
            for _ in 0..2000 {
                m.set("resolution", 720).unwrap();
                m.set("resolution", 1440).unwrap();
            }
        }));
        barrier.wait();
        for thread in threads {
            thread.join().unwrap();
        }
    }
    #[test]
    fn absolute_firmware_links_are_confined_and_failed_reads_keep_defaults() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        roots(root);
        fs::write(root.join("etc/kvm/fps"), "120").unwrap();
        symlink("/etc/kvm/fps", root.join("kvmapp/kvm/fps")).unwrap();
        fs::write(root.join("run/nanokvm/real_width"), "1920").unwrap();
        symlink("/run/nanokvm/real_width", root.join("run/nanokvm/width")).unwrap();
        fs::write(root.join("run/nanokvm/height"), "1080").unwrap();
        let manager = Manager::load_with_default_chroma(root, false).unwrap();
        assert_eq!(manager.snapshot().unwrap().fps, 120);
        assert_eq!(manager.capture_screen().unwrap().fps, 75);
        fs::remove_file(root.join("kvmapp/kvm/fps")).unwrap();
        symlink("../../../outside", root.join("kvmapp/kvm/fps")).unwrap();
        assert_eq!(
            Manager::load_with_default_chroma(root, false)
                .unwrap()
                .snapshot()
                .unwrap()
                .fps,
            50
        );
    }
}
