//! Legacy-compatible time preferences, system TZif and reversible daemon updates.
use crate::{
    api::{error, ok},
    fsroot,
    systemops::{Action, Executor},
    Error, Runtime,
};
use axum::response::Response;
use serde::Serialize;
use serde_json::{json, Value};
use std::{
    fs,
    net::IpAddr,
    os::unix::fs::{DirBuilderExt, PermissionsExt},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, SystemTime, UNIX_EPOCH},
};
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct Config {
    pub servers: Option<Vec<String>>,
    pub timezone: String,
    pub format: String,
}
impl Config {
    pub fn defaults() -> Self {
        Self {
            servers: Some(
                (0..4)
                    .map(|index| format!("{index}.pool.ntp.org"))
                    .collect(),
            ),
            timezone: "UTC".into(),
            format: "24".into(),
        }
    }
    fn from_value(value: &Value, mut config: Self) -> Self {
        if let Some(v) = value.get("servers") {
            config.servers = v.as_array().map(|values| {
                values
                    .iter()
                    .map(|v| v.as_str().unwrap_or("").to_owned())
                    .collect()
            });
        }
        if let Some(v) = value["timezone"].as_str() {
            config.timezone = v.into();
        }
        if let Some(v) = value["format"].as_str() {
            config.format = v.into();
        }
        config
    }
}
pub trait Backend: Send + Sync {
    fn restart(&self, chrony: bool) -> Result<(), Error>;
    fn synchronized(&self, chrony: bool) -> Result<bool, Error>;
    fn atomic_preference(&self, path: &Path, data: &[u8], mode: u32) -> Result<(), Error> {
        crate::preferences::write(path, data, mode)
    }
    fn now_millis(&self) -> i64 {
        unix_millis(SystemTime::now())
    }
}
pub struct Native {
    root: PathBuf,
    commands: Arc<dyn Executor>,
}
impl Native {
    pub fn new(root: PathBuf, commands: Arc<dyn Executor>) -> Self {
        Self { root, commands }
    }
}
impl Backend for Native {
    fn restart(&self, chrony: bool) -> Result<(), Error> {
        self.commands.run(
            if chrony {
                Action::ChronyRestart
            } else {
                Action::NtpRestart
            },
            Duration::from_secs(12),
        )
    }
    fn synchronized(&self, chrony: bool) -> Result<bool, Error> {
        if self.root != Path::new("/") {
            return Err("time synchronization unavailable in isolated root".into());
        }
        if chrony {
            let output = self
                .commands
                .output(Action::ChronyTracking, Duration::from_secs(2))?;
            crate::time_sync::parse_chrony(&crate::json_text::text(&output))
        } else {
            self.commands.check(Action::NtpStatus)?;
            crate::time_sync::ntp_at("127.0.0.1:123".parse().unwrap(), Duration::from_millis(750))
        }
    }
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
    fn read_file(&self, path: &str) -> Result<Vec<u8>, Error> {
        Ok(fs::read(fsroot::resolve(
            &self.root,
            Path::new(path),
            false,
        )?)?)
    }
    pub fn zones(&self) -> Vec<String> {
        let mut names = vec!["UTC".into()];
        if let Ok(data) = self.read_file("/usr/share/zoneinfo/zone.tab") {
            for line in crate::json_text::text(&data).split('\n') {
                let fields: Vec<_> = line.split_whitespace().collect();
                if fields.len() >= 3 && !fields[0].starts_with('#') {
                    names.push(fields[2].into());
                }
            }
        }
        names.sort();
        names.dedup();
        names
    }
    pub fn zone_data(&self, name: &str) -> Result<Vec<u8>, Error> {
        if name.is_empty()
            || name.contains('\\')
            || name.starts_with('/')
            || matches!(name, "." | "..")
            || (name != "."
                && name
                    .split('/')
                    .any(|part| part.is_empty() || matches!(part, "." | "..")))
        {
            return Err("unknown time zone".into());
        }
        let bytes = self
            .read_file(&format!("/usr/share/zoneinfo/{name}"))
            .map_err(|_| "unknown time zone; install tzdata")?;
        jiff::tz::TimeZone::tzif(name, &bytes).map_err(|_| "invalid time zone")?;
        Ok(bytes)
    }
    pub fn validate(&self, config: &Config) -> Result<(), Error> {
        if !matches!(config.format.as_str(), "24" | "12") {
            return Err("time format must be 24 or 12".into());
        }
        self.zone_data(&config.timezone)?;
        let servers = config.servers.as_deref().unwrap_or(&[]);
        if !(1..=6).contains(&servers.len()) {
            return Err("choose between 1 and 6 NTP servers".into());
        }
        let mut seen = std::collections::HashSet::new();
        for server in servers {
            if server.is_empty() || server.len() > 253 || server.trim() != server {
                return Err("invalid NTP server".into());
            }
            let host = server.strip_suffix('.').unwrap_or(server);
            if server.parse::<IpAddr>().is_err()
                && !host.split('.').all(|label| {
                    let bytes = label.as_bytes();
                    (1..=63).contains(&bytes.len())
                        && bytes[0].is_ascii_alphanumeric()
                        && bytes[bytes.len() - 1].is_ascii_alphanumeric()
                        && bytes
                            .iter()
                            .all(|b| b.is_ascii_alphanumeric() || *b == b'-')
                })
            {
                return Err("NTP server must be a hostname or IP address".into());
            }
            if !seen.insert(host.to_lowercase()) {
                return Err("duplicate NTP server".into());
            }
        }
        Ok(())
    }
    pub fn chrony(&self) -> bool {
        fsroot::resolve(&self.root, Path::new("/etc/chrony.conf"), false)
            .and_then(|path| fs::metadata(path).map_err(Into::into))
            .is_ok()
    }
    fn daemon(&self) -> &'static str {
        if self.chrony() {
            "/etc/chrony.conf"
        } else {
            "/etc/ntp.conf"
        }
    }
    fn read_locked(&self) -> Result<Config, Error> {
        let mut config = Config::defaults();
        match self.read_file("/etc/kvm/date-time.json") {
            Ok(bytes) => {
                let value =
                    crate::binding::json_complete(&bytes, &["servers", "timezone", "format"])?;
                config = Config::from_value(&value, config);
                self.validate(&config)?;
                return Ok(config);
            }
            Err(error)
                if error
                    .downcast_ref::<std::io::Error>()
                    .is_some_and(|error| error.kind() == std::io::ErrorKind::NotFound) => {}
            Err(error) => return Err(error),
        }
        if let Ok(bytes) = self.read_file("/etc/timezone") {
            let name = crate::json_text::text(&bytes);
            let name = name.trim();
            if self.zone_data(name).is_ok() {
                config.timezone = name.into();
            }
        }
        if let Ok(bytes) = self.read_file(self.daemon()) {
            let text = crate::json_text::text(&bytes);
            let servers: Vec<String> = text
                .split('\n')
                .filter_map(|line| {
                    let fields: Vec<_> = line.split_whitespace().collect();
                    (fields.len() >= 2 && matches!(fields[0], "server" | "pool"))
                        .then(|| fields[1].into())
                })
                .collect();
            if !servers.is_empty() {
                config.servers = Some(servers);
            }
        }
        Ok(config)
    }
    pub fn read(&self) -> Result<Config, Error> {
        let _guard = self.gate.lock().map_err(|_| "time settings unavailable")?;
        self.read_locked()
    }
    fn atomic_path(&self, name: &str) -> Result<PathBuf, Error> {
        let name = Path::new(name);
        let parent = name.parent().ok_or("invalid time preference path")?;
        if parent == Path::new("/etc/kvm") {
            let path = fsroot::resolve(&self.root, parent, true)?;
            if !path.exists() {
                fs::DirBuilder::new().mode(0o755).create(&path)?;
                fs::set_permissions(&path, fs::Permissions::from_mode(0o755))?;
            }
        }
        Ok(fsroot::resolve(&self.root, parent, false)?
            .join(name.file_name().ok_or("invalid time preference path")?))
    }
    fn rollback(
        &self,
        files: &[(PathBuf, Vec<u8>, crate::preferences::Saved)],
    ) -> Result<(), Error> {
        crate::gpio::joined(
            Ok(()),
            files.iter().rev().map(|(path, _, saved)| {
                crate::preferences::restore(path, saved, |path, bytes, mode| {
                    self.backend.atomic_preference(path, bytes, mode)
                })
            }),
        )
    }
    pub fn save(&self, config: &Config) -> Result<(), Error> {
        self.validate(config)?;
        let _guard = self.gate.lock().map_err(|_| "time settings unavailable")?;
        let old = self.read_locked()?;
        let tz = self.zone_data(&config.timezone)?;
        let chrony = self.chrony();
        let daemon = self.daemon();
        let old_daemon = match self.read_file(daemon) {
            Ok(bytes) => bytes,
            Err(error)
                if error
                    .downcast_ref::<std::io::Error>()
                    .is_some_and(|e| e.kind() == std::io::ErrorKind::NotFound) =>
            {
                Vec::new()
            }
            Err(error) => return Err(error),
        };
        let data = serde_json::to_string_pretty(config)?
            .replace('&', "\\u0026")
            .replace('<', "\\u003c")
            .replace('>', "\\u003e")
            .replace('\u{2028}', "\\u2028")
            .replace('\u{2029}', "\\u2029")
            + "\n";
        let mut planned = vec![
            ("/etc/localtime", tz),
            (
                "/etc/timezone",
                format!("{}\n", config.timezone).into_bytes(),
            ),
            ("/etc/kvm/date-time.json", data.into_bytes()),
        ];
        let restart = old.servers != config.servers;
        if restart {
            planned.push((
                daemon,
                server_config(
                    &old_daemon,
                    config.servers.as_deref().unwrap_or(&[]),
                    chrony,
                ),
            ));
        }
        let mut files = Vec::new();
        for (path, bytes) in planned {
            let path = self.atomic_path(path)?;
            let saved = crate::preferences::snapshot(&path)?;
            files.push((path, bytes, saved));
        }
        for (index, (path, bytes, _)) in files.iter().enumerate() {
            if let Err(error) = self.backend.atomic_preference(path, bytes, 0o644) {
                return crate::gpio::joined(Err(error), [self.rollback(&files[..=index])]);
            }
        }
        if restart {
            if let Err(error) = self.backend.restart(chrony) {
                return crate::gpio::joined(
                    Err(format!("NTP restart failed: {error}").into()),
                    [self.rollback(&files), self.backend.restart(chrony)],
                );
            }
        }
        Ok(())
    }
    pub fn status(&self) -> Result<Value, Error> {
        let config = self.read()?;
        let chrony = self.chrony();
        let synchronized = self.backend.synchronized(chrony).unwrap_or(false);
        Ok(
            json!({"config":config,"zones":self.zones(),"now":self.backend.now_millis(),"synchronized":synchronized,"daemon":if chrony{"chrony"}else{"ntpd"}}),
        )
    }
}
pub fn server_config(old: &[u8], servers: &[String], chrony: bool) -> Vec<u8> {
    let mut result = Vec::new();
    for server in servers {
        result.extend_from_slice(format!("server {server} iburst\n").as_bytes());
    }
    let old = if old.is_empty() {
        if chrony {
            b"driftfile /var/lib/chrony/drift\nmakestep 1.0 3\nrtcsync\nport 0\ncmdport 0\nbindcmdaddress /run/chrony/chronyd.sock\n".as_slice()
        } else {
            b"restrict default nomodify nopeer noquery limited kod\nrestrict 127.0.0.1\nrestrict [::1]\n"
        }
    } else {
        old
    };
    let mut end = old.len();
    while end > 0 && old[end - 1] == b'\n' {
        end -= 1;
    }
    for line in old[..end].split(|b| *b == b'\n') {
        let text = crate::json_text::text(line);
        let first = text.split_whitespace().next();
        if matches!(first, Some("server" | "pool")) {
            continue;
        }
        result.extend_from_slice(line);
        result.push(b'\n');
    }
    result
}
pub(crate) fn get(runtime: &Runtime) -> Response {
    match runtime.time.status() {
        Ok(value) => ok(value),
        Err(_) => error(-1, "Cannot read date and time settings"),
    }
}
pub(crate) fn set(runtime: &Runtime, parameters: Result<Value, Error>) -> Response {
    let Ok(value) = parameters else {
        return error(-1, "Invalid date and time settings");
    };
    let config = Config::from_value(
        &value,
        Config {
            servers: None,
            timezone: String::new(),
            format: String::new(),
        },
    );
    if let Err(cause) = runtime.time.validate(&config) {
        return error(-1, &cause.to_string());
    }
    match runtime.time.save(&config){Ok(())=>get(runtime),Err(_)=>error(-2,"Cannot apply date and time settings; previous configuration was restored where possible")}
}

fn unix_millis(now: SystemTime) -> i64 {
    match now.duration_since(UNIX_EPOCH) {
        Ok(value) => value.as_millis().min(i64::MAX as u128) as i64,
        Err(error) => {
            let value = error.duration();
            let millis = value
                .as_millis()
                .saturating_add(u128::from(value.subsec_nanos() % 1_000_000 != 0));
            if millis > i64::MAX as u128 {
                i64::MIN
            } else {
                -(millis as i64)
            }
        }
    }
}
#[cfg(test)]
mod clock_tests {
    use super::*;
    #[test]
    fn milliseconds_before_epoch_round_down_like_go_without_setting_clock() {
        let oracle: Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/time-go-oracle.json"
        ))
        .unwrap();
        for case in oracle["millis"].as_array().unwrap() {
            let nanos = case["nanos"].as_i64().unwrap();
            let duration = Duration::from_nanos(nanos.unsigned_abs());
            let time = if nanos < 0 {
                UNIX_EPOCH - duration
            } else {
                UNIX_EPOCH + duration
            };
            assert_eq!(
                unix_millis(time),
                case["millis"].as_i64().unwrap(),
                "{nanos}"
            );
        }
    }
}
