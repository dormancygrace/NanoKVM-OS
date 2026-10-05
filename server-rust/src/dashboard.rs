//! Read-only Linux dashboard telemetry and request-independent CPU intervals.
use crate::{api::ok, fsroot, sysinfo, Error, Runtime};
use serde::Serialize;
use std::{
    collections::BTreeSet,
    fs,
    path::{Path, PathBuf},
    sync::{Arc, Mutex, Weak},
    time::{Duration, Instant, SystemTime},
};
#[derive(Clone, Copy, Debug, Serialize)]
pub struct CPU {
    pub total: u64,
    pub idle: u64,
}
#[derive(Clone, Debug, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Storage {
    pub path: String,
    pub available: bool,
    pub total: u64,
    pub free: u64,
    pub used: u64,
    pub read_only: bool,
}
#[derive(Clone, Debug, Serialize)]
pub struct Interface {
    pub kind: String,
    pub name: String,
    pub mac: String,
    pub mtu: i64,
    pub up: bool,
    pub enabled: bool,
    pub connected: bool,
    pub wireless: bool,
    pub addresses: Vec<String>,
    pub received: Option<u64>,
    pub sent: Option<u64>,
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub now: i64,
    pub hostname: String,
    pub kernel: String,
    pub architecture: String,
    pub cores: usize,
    pub uptime: Option<f64>,
    pub cpu_usage: Option<f64>,
    pub cpu: Option<CPU>,
    pub load: Option<Vec<String>>,
    pub cpu_frequency: Option<i64>,
    pub temperature: Option<f64>,
    pub storage: Vec<Storage>,
    pub interfaces: Vec<Interface>,
}
#[derive(Clone, Copy, Debug)]
pub struct Filesystem {
    pub blocks: u64,
    pub available: u64,
    pub free: u64,
    pub block_size: i64,
    pub flags: u64,
}
pub trait Backend: Send + Sync {
    fn architecture(&self) -> String;
    fn cores(&self) -> usize;
    fn storage(&self, path: &str) -> Result<Filesystem, Error>;
    fn interfaces(&self) -> Result<Vec<sysinfo::TelemetryInterface>, Error>;
}
pub struct Native {
    root: PathBuf,
    network: Arc<dyn sysinfo::Interfaces>,
}
impl Native {
    pub fn new(root: PathBuf, network: Arc<dyn sysinfo::Interfaces>) -> Self {
        Self { root, network }
    }
}
impl Backend for Native {
    fn architecture(&self) -> String {
        match std::env::consts::ARCH {
            "x86_64" => "amd64",
            "x86" => "386",
            "aarch64" => "arm64",
            value => value,
        }
        .into()
    }
    fn cores(&self) -> usize {
        if self.root != Path::new("/") {
            return 0;
        }
        let mut mask = vec![0u8; 8192];
        if unsafe { libc::sched_getaffinity(0, mask.len(), mask.as_mut_ptr().cast()) } == 0 {
            return mask
                .iter()
                .map(|b| b.count_ones() as usize)
                .sum::<usize>()
                .max(1);
        }
        let cores = unsafe { libc::sysconf(libc::_SC_NPROCESSORS_ONLN) };
        usize::try_from(cores).unwrap_or(1).max(1)
    }
    fn storage(&self, path: &str) -> Result<Filesystem, Error> {
        if self.root != Path::new("/") {
            return Err("filesystem statistics unavailable in isolated root".into());
        }
        if !["/", "/boot", "/data"].contains(&path) {
            return Err("invalid dashboard storage path".into());
        }
        use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};
        let path = std::ffi::CString::new(path)?;
        let raw = unsafe { libc::open(path.as_ptr(), libc::O_PATH | libc::O_CLOEXEC) };
        if raw < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        let file = unsafe { OwnedFd::from_raw_fd(raw) };
        let mut value: libc::statfs = unsafe { std::mem::zeroed() };
        let mut flags: libc::statvfs = unsafe { std::mem::zeroed() };
        if unsafe { libc::fstatfs(file.as_raw_fd(), &mut value) } != 0
            || unsafe { libc::fstatvfs(file.as_raw_fd(), &mut flags) } != 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok(Filesystem {
            blocks: value.f_blocks,
            available: value.f_bavail,
            free: value.f_bfree,
            block_size: i64::from_ne_bytes(value.f_bsize.to_ne_bytes()),
            flags: flags.f_flag,
        })
    }
    fn interfaces(&self) -> Result<Vec<sysinfo::TelemetryInterface>, Error> {
        if self.root != Path::new("/") {
            return Err("network telemetry unavailable in isolated root".into());
        }
        self.network.telemetry()
    }
}
#[derive(Default)]
pub struct Sampler {
    previous: Option<CPU>,
    usage: Option<f64>,
    sampled: Option<Instant>,
}
impl Sampler {
    pub fn update(&mut self, next: Option<CPU>, now: Instant) {
        self.usage = None;
        if let (Some(next), Some(last)) = (next, self.previous) {
            if next.total > last.total && next.idle >= last.idle {
                let total = next.total - last.total;
                let idle = next.idle - last.idle;
                if idle <= total {
                    self.usage = Some((total - idle) as f64 * 100.0 / total as f64)
                }
            }
        }
        self.previous = next;
        self.sampled = Some(now);
    }
    pub fn value(&self, now: Instant) -> Option<f64> {
        if now.saturating_duration_since(self.sampled?) > Duration::from_secs(3) {
            None
        } else {
            self.usage
        }
    }
}
fn unsigned(value: &str) -> Option<u64> {
    (!value.is_empty() && value.bytes().all(|b| b.is_ascii_digit()))
        .then(|| value.parse().ok())
        .flatten()
}
pub fn parse_cpu(text: &str) -> Option<CPU> {
    let fields = text
        .split('\n')
        .next()?
        .split_whitespace()
        .collect::<Vec<_>>();
    if fields.len() < 5 || fields[0] != "cpu" {
        return None;
    }
    let mut cpu = CPU { total: 0, idle: 0 };
    for (index, field) in fields[1..fields.len().min(9)].iter().enumerate() {
        let n = unsigned(field)?;
        cpu.total = cpu.total.wrapping_add(n);
        if index == 3 || index == 4 {
            cpu.idle = cpu.idle.wrapping_add(n)
        }
    }
    Some(cpu)
}
pub fn storage(path: &str, mounted: bool, value: Option<Filesystem>) -> Storage {
    let mut result = Storage {
        path: path.into(),
        ..Storage::default()
    };
    if !mounted {
        return result;
    }
    if let Some(value) = value.filter(|value| value.blocks > 0) {
        let size = value.block_size as u64;
        result.available = true;
        result.total = value.blocks.wrapping_mul(size);
        result.free = value.available.wrapping_mul(size);
        result.used = (value.blocks - value.free.min(value.blocks)).wrapping_mul(size);
        result.read_only = value.flags & libc::ST_RDONLY != 0;
    }
    result
}
pub struct Manager {
    root: PathBuf,
    backend: Arc<dyn Backend>,
    sampler: Mutex<Sampler>,
}
impl Manager {
    pub fn new(root: PathBuf, backend: Arc<dyn Backend>) -> Arc<Self> {
        let manager = Arc::new(Self {
            root,
            backend,
            sampler: Mutex::new(Sampler::default()),
        });
        manager.sample();
        manager
    }
    fn path(&self, path: &str) -> Result<PathBuf, Error> {
        fsroot::resolve(&self.root, Path::new(path), false)
    }
    fn text(&self, path: &str) -> String {
        self.path(path)
            .ok()
            .and_then(|p| fs::read(p).ok())
            .map(|bytes| crate::json_text::text(&bytes).trim().to_owned())
            .unwrap_or_default()
    }
    fn exists(&self, path: &str) -> bool {
        self.path(path).is_ok_and(|p| p.exists())
    }
    fn dirs(&self, path: &str, prefix: &str) -> Vec<String> {
        let mut names = self
            .path(path)
            .ok()
            .and_then(|p| fs::read_dir(p).ok())
            .into_iter()
            .flatten()
            .filter_map(Result::ok)
            .filter_map(|entry| entry.file_name().into_string().ok())
            .filter(|name| name.starts_with(prefix))
            .map(|name| format!("{path}/{name}"))
            .collect::<Vec<_>>();
        names.sort();
        names
    }
    pub fn temperature(&self) -> Option<f64> {
        let mut paths = Vec::new();
        for dir in self.dirs("/sys/class/hwmon", "hwmon") {
            if self.text(&format!("{dir}/name")) == "sg2002" {
                let mut readings = self
                    .path(&dir)
                    .ok()
                    .and_then(|p| fs::read_dir(p).ok())
                    .into_iter()
                    .flatten()
                    .filter_map(Result::ok)
                    .filter_map(|entry| entry.file_name().into_string().ok())
                    .filter(|name| name.starts_with("temp") && name.ends_with("_input"))
                    .map(|name| format!("{dir}/{name}"))
                    .collect::<Vec<_>>();
                readings.sort();
                paths.extend(readings)
            }
        }
        if paths.is_empty() {
            for dir in self.dirs("/sys/class/thermal", "thermal_zone") {
                let kind = self.text(&format!("{dir}/type")).to_lowercase();
                if ["soc", "cpu", "sg2002"]
                    .iter()
                    .any(|name| kind.contains(name))
                {
                    paths.push(format!("{dir}/temp"))
                }
            }
        }
        paths
            .into_iter()
            .filter_map(|path| self.text(&path).parse::<i64>().ok())
            .filter(|value| (-40000..=150000).contains(value))
            .map(|value| value as f64 / 1000.0)
            .max_by(f64::total_cmp)
    }
    pub fn frequency(&self) -> Option<i64> {
        self.text("/sys/devices/system/cpu/cpufreq/policy0/cpuinfo_cur_freq")
            .parse::<i64>()
            .ok()
            .filter(|n| *n > 0)
            .map(|n| n / 1000)
    }
    pub fn sample(&self) {
        if let Ok(mut sampler) = self.sampler.lock() {
            sampler.update(parse_cpu(&self.text("/proc/stat")), Instant::now())
        }
    }
    pub fn start(self: &Arc<Self>) -> Option<tokio::task::JoinHandle<()>> {
        let handle = tokio::runtime::Handle::try_current().ok()?;
        let weak = Arc::downgrade(self);
        Some(handle.spawn(async move { Self::worker(weak).await }))
    }
    async fn worker(weak: Weak<Self>) {
        let mut ticks = tokio::time::interval_at(
            tokio::time::Instant::now() + Duration::from_secs(1),
            Duration::from_secs(1),
        );
        ticks.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        loop {
            ticks.tick().await;
            let Some(manager) = weak.upgrade() else { break };
            if tokio::task::spawn_blocking(move || manager.sample())
                .await
                .is_err()
            {
                break;
            }
        }
    }
    pub fn read_at(&self, now: i64, instant: Instant) -> Status {
        let uptime = self
            .text("/proc/uptime")
            .split_whitespace()
            .next()
            .and_then(|n| n.parse::<f64>().ok())
            .filter(|n| n.is_finite() && *n >= 0.0);
        let fields = self
            .text("/proc/loadavg")
            .split_whitespace()
            .take(3)
            .map(str::to_owned)
            .collect::<Vec<_>>();
        let load = (fields.len() == 3).then_some(fields);
        let mounts = self
            .text("/proc/mounts")
            .lines()
            .filter_map(|line| line.split_whitespace().nth(1))
            .map(str::to_owned)
            .collect::<BTreeSet<_>>();
        let storage = ["/", "/boot", "/data"]
            .into_iter()
            .map(|path| {
                let mounted = mounts.contains(path);
                storage(
                    path,
                    mounted,
                    if mounted {
                        self.backend.storage(path).ok()
                    } else {
                        None
                    },
                )
            })
            .collect();
        let interfaces = self
            .backend
            .interfaces()
            .unwrap_or_default()
            .into_iter()
            .filter(|iface| !iface.loopback)
            .map(|iface| {
                let base = format!("/sys/class/net/{}", iface.interface.name);
                let name = iface.interface.name;
                let enabled = if name == "eth0" || name.starts_with("eth0.") {
                    !self.exists("/boot/eth.disabled")
                } else if name == "wlan0" {
                    !self.exists("/etc/kvm/wifi.disabled")
                } else {
                    true
                };
                Interface {
                    wireless: self.exists(&format!("{base}/wireless")) || name.starts_with("wl"),
                    connected: iface.interface.running
                        && self.text(&format!("{base}/carrier")) != "0",
                    received: unsigned(&self.text(&format!("{base}/statistics/rx_bytes"))),
                    sent: unsigned(&self.text(&format!("{base}/statistics/tx_bytes"))),
                    kind: iface.kind,
                    name,
                    mac: iface.mac,
                    mtu: iface.mtu,
                    up: iface.interface.up,
                    enabled,
                    addresses: iface.addresses,
                }
            })
            .collect();
        Status {
            now,
            hostname: self.text("/proc/sys/kernel/hostname"),
            kernel: self.text("/proc/sys/kernel/osrelease"),
            architecture: self.backend.architecture(),
            cores: self.backend.cores(),
            uptime,
            cpu_usage: self.sampler.lock().ok().and_then(|s| s.value(instant)),
            cpu: parse_cpu(&self.text("/proc/stat")),
            load,
            cpu_frequency: self.frequency(),
            temperature: self.temperature(),
            storage,
            interfaces,
        }
    }
    pub fn read(&self) -> Status {
        self.read_at(
            crate::timeconfig::unix_millis(SystemTime::now()),
            Instant::now(),
        )
    }
}
pub(crate) fn get(runtime: &Runtime) -> axum::response::Response {
    ok(
        serde_json::json!({"system":runtime.dashboard.read(),"memory":crate::memory_status::read_status(&runtime.root).ok(),"hardware":runtime.hardware.version,"application":runtime.info.application(),"image":runtime.info.image()}),
    )
}
