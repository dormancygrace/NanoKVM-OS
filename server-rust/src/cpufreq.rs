//! Qualified SG2002 cpufreq policy, verified readback and boot-safe persistence.
use crate::{
    api::{error, ok},
    fsroot,
    store::atomic_write,
    Error, Runtime,
};
use axum::response::Response;
use serde::Serialize;
use serde_json::Value;
use std::{
    fs,
    io::Write,
    os::unix::fs::OpenOptionsExt,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};
const POLICY: &str = "/sys/devices/system/cpu/cpufreq/policy0";
const PREF: &str = "/etc/kvm/cpufreq";
const RUN_PREF: &str = "/run/nanokvm-cpufreq";
pub trait Backend: Send + Sync {
    fn read(&self, path: &str) -> Result<Vec<u8>, Error>;
    fn write(&self, path: &str, data: &[u8]) -> Result<(), Error>;
    fn entries(&self, path: &str) -> Vec<String>;
    fn atomic_preference(&self, path: &Path, data: &[u8], mode: u32) -> Result<(), Error> {
        atomic_write(path, data, mode)
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
    fn read(&self, path: &str) -> Result<Vec<u8>, Error> {
        Ok(fs::read(fsroot::resolve(
            &self.root,
            Path::new(path),
            false,
        )?)?)
    }
    fn write(&self, path: &str, data: &[u8]) -> Result<(), Error> {
        let path = fsroot::resolve(&self.root, Path::new(path), false)?;
        let mut file = fs::OpenOptions::new()
            .write(true)
            .truncate(true)
            .custom_flags(libc::O_CLOEXEC | libc::O_NOFOLLOW | libc::O_NONBLOCK)
            .open(path)?;
        file.write_all(data)?;
        Ok(())
    }
    fn entries(&self, path: &str) -> Vec<String> {
        let result = (|| -> Result<Vec<String>, Error> {
            let path = fsroot::resolve(&self.root, Path::new(path), false)?;
            let mut names = fs::read_dir(path)?
                .filter_map(Result::ok)
                .filter_map(|entry| entry.file_name().into_string().ok())
                .collect::<Vec<_>>();
            names.sort();
            Ok(names)
        })();
        result.unwrap_or_default()
    }
}
#[derive(Debug, Serialize, PartialEq, Eq)]
pub struct Status {
    pub supported: bool,
    pub throttled: bool,
    pub running: i64,
    pub target: i64,
    pub options: Option<Vec<i64>>,
}
fn valid(target: i64) -> bool {
    [850, 1000, 1050, 1075, 1100, 1125, 1150].contains(&target)
}
pub struct Manager {
    root: PathBuf,
    backend: Arc<dyn Backend>,
    gate: Mutex<()>,
}
impl Manager {
    pub fn new(root: PathBuf, backend: Arc<dyn Backend>) -> Self {
        Self {
            root,
            backend,
            gate: Mutex::new(()),
        }
    }
    fn read(&self, path: &str) -> Result<String, Error> {
        let bytes = self.backend.read(path)?;
        Ok(crate::json_text::text(&bytes).trim().to_owned())
    }
    fn policy(&self, name: &str) -> Result<String, Error> {
        self.read(&format!("{POLICY}/{name}"))
    }
    fn options(&self) -> Option<Vec<i64>> {
        let available = match self.policy("scaling_available_frequencies") {
            Ok(text) => text,
            Err(_) => return Some(vec![850, 1000]),
        };
        let values = available
            .split_whitespace()
            .filter_map(|value| value.parse::<i64>().ok())
            .filter(|value| value % 1000 == 0 && valid(value / 1000))
            .map(|value| value / 1000)
            .collect::<Vec<_>>();
        (!values.is_empty()).then_some(values)
    }
    fn thermal(&self) -> bool {
        let root = "/sys/class/thermal";
        for zone in self
            .backend
            .entries(root)
            .into_iter()
            .filter(|name| name.starts_with("thermal_zone"))
        {
            let zone = format!("{root}/{zone}");
            if self.read(&format!("{zone}/type")).ok().as_deref() != Some("sg2002-cpu") {
                continue;
            }
            for device in self.backend.entries(&zone).into_iter().filter(|name| {
                name.strip_prefix("cdev")
                    .is_some_and(|suffix| suffix.as_bytes().first().is_some_and(u8::is_ascii_digit))
            }) {
                let device = format!("{zone}/{device}");
                if self.read(&format!("{device}/type")).ok().as_deref() != Some("cpufreq-cpu0") {
                    continue;
                }
                if self
                    .read(&format!("{device}/cur_state"))
                    .ok()
                    .and_then(|state| state.parse::<i64>().ok())
                    .is_some_and(|state| state > 0)
                {
                    return true;
                }
            }
        }
        false
    }
    fn state(&self) -> Status {
        let mut state = Status {
            supported: false,
            throttled: false,
            running: 0,
            target: 1000,
            options: Some(Vec::new()),
        };
        if let Ok(text) = self.read(PREF) {
            if let Ok(value) = text.parse::<i64>() {
                if [850, 1000].contains(&value) {
                    state.target = value;
                }
            }
        }
        if let Ok(text) = self.read(RUN_PREF) {
            if let Ok(value) = text.parse::<i64>() {
                if valid(value) {
                    state.target = value;
                }
            }
        }
        if self.policy("scaling_driver").ok().as_deref() != Some("sg2002-cpufreq") {
            return state;
        }
        let Some(khz) = self
            .policy("cpuinfo_cur_freq")
            .ok()
            .and_then(|value| value.parse::<i64>().ok())
            .filter(|khz| *khz > 0)
        else {
            return state;
        };
        state.running = khz / 1000;
        state.throttled = self.thermal();
        state.supported = true;
        state.options = self.options();
        state
    }
    pub fn status(&self) -> Result<Status, Error> {
        let _guard = self.gate.lock().map_err(|_| "CPU frequency unavailable")?;
        Ok(self.state())
    }
    fn program(&self, target: i64) -> Result<(), Error> {
        if !self
            .options()
            .is_some_and(|options| options.contains(&target))
        {
            return Err("unsupported CPU frequency".into());
        }
        let minimum = if self.policy("cpuinfo_min_freq").ok().as_deref() == Some("600000") {
            600000
        } else {
            850000
        };
        let ceiling = target.max(1000) * 1000;
        for (name, value) in [
            ("scaling_governor", "userspace".into()),
            ("scaling_min_freq", minimum.to_string()),
            ("scaling_max_freq", ceiling.to_string()),
            ("scaling_setspeed", (target * 1000).to_string()),
            ("scaling_max_freq", (target * 1000).to_string()),
        ] {
            self.backend
                .write(&format!("{POLICY}/{name}"), value.as_bytes())
                .map_err(|cause| format!("{name}: {cause}"))?;
            if matches!(name, "scaling_min_freq" | "scaling_max_freq") {
                let end = Instant::now() + Duration::from_secs(2);
                loop {
                    let actual = self.policy(name)?;
                    if actual == value
                        || (name == "scaling_max_freq" && actual == "850000" && self.thermal())
                    {
                        break;
                    }
                    if Instant::now() >= end {
                        return Err(format!("{name} did not apply: {actual}").into());
                    }
                    std::thread::sleep(Duration::from_millis(10));
                }
            }
        }
        let state = self.state();
        if !state.supported
            || (state.running != target && !(state.throttled && state.running == 850))
        {
            return Err(format!(
                "CPU clock readback is {} MHz, requested {target}",
                state.running
            )
            .into());
        }
        Ok(())
    }
    fn persist(&self, target: i64) -> Result<(), Error> {
        // A failed request must not leave a new boot cap after restoring its clock.
        use std::os::unix::fs::PermissionsExt;
        let mut files = Vec::new();
        for (path, value) in [(PREF, target.min(1000)), (RUN_PREF, target)] {
            let path = fsroot::resolve(&self.root, Path::new(path), true)?;
            let old = match fs::metadata(&path) {
                Ok(meta) if meta.is_file() => {
                    Some((fs::read(&path)?, meta.permissions().mode() & 0o7777))
                }
                Ok(_) => return Err("CPU frequency preference is not a regular file".into()),
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
                Err(error) => return Err(error.into()),
            };
            files.push((path, format!("{value}\n"), old));
        }
        for (index, (path, data, _)) in files.iter().enumerate() {
            if let Err(cause) = self.backend.atomic_preference(path, data.as_bytes(), 0o600) {
                let cleanup = files[..=index]
                    .iter()
                    .rev()
                    .map(|(path, _, old)| match old {
                        Some((bytes, mode)) => self.backend.atomic_preference(path, bytes, *mode),
                        None => match fs::remove_file(path) {
                            Ok(()) => fs::File::open(path.parent().unwrap())
                                .and_then(|file| file.sync_all())
                                .map_err(Into::into),
                            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
                            Err(error) => Err(error.into()),
                        },
                    });
                return crate::gpio::joined(Err(cause), cleanup);
            }
        }
        Ok(())
    }
    pub fn apply(&self, target: i64, persist: bool) -> Result<(), Error> {
        let _guard = self.gate.lock().map_err(|_| "CPU frequency unavailable")?;
        let before = self.state();
        if !before.supported {
            return Err("qualified CPU frequency driver is unavailable".into());
        }
        let result = self.program(target).and_then(|()| {
            if persist {
                self.persist(target)
            } else {
                Ok(())
            }
        });
        let restore = if before.throttled {
            before.target
        } else {
            before.running
        };
        if result.is_err() && valid(restore) {
            let cleanup = self
                .program(restore)
                .map_err(|cause| format!("restore CPU clock: {cause}").into());
            return crate::gpio::joined(result, [cleanup]);
        }
        result
    }
    pub fn apply_saved(&self) -> Result<(), Error> {
        let status = self.status()?;
        if status.supported {
            self.apply(status.target, false)
        } else {
            Ok(())
        }
    }
}
pub(crate) fn get(runtime: &Runtime) -> Response {
    match runtime.cpu.status() {
        Ok(status) => ok(serde_json::to_value(status).unwrap()),
        Err(cause) => error(-2, &cause.to_string()),
    }
}
pub(crate) fn set(runtime: &Runtime, parameters: Result<Value, Error>) -> Response {
    let Ok(v) = parameters else {
        return error(-1, "invalid CPU frequency");
    };
    let target = v["target"].as_i64().unwrap_or(0);
    if !valid(target) {
        return error(-1, "invalid CPU frequency");
    }
    match runtime.cpu.apply(target, true) {
        Ok(()) => ok(Value::Null),
        Err(cause) => error(-2, &cause.to_string()),
    }
}
