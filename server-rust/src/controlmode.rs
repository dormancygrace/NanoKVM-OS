//! Persisted AI mode and bounded activity drain. Blocking methods run only on
//! admitted workers; leases keep the selected mode stable during operations.
use crate::{store::atomic_write, Error};
use std::{
    fs,
    path::PathBuf,
    sync::{Arc, Condvar, Mutex},
    time::{Duration, Instant, SystemTime},
};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Mode {
    Off,
    Mcp,
    Picoclaw,
}
impl Mode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Off => "off",
            Self::Mcp => "mcp",
            Self::Picoclaw => "picoclaw",
        }
    }
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "off" => Some(Self::Off),
            "mcp" => Some(Self::Mcp),
            "picoclaw" => Some(Self::Picoclaw),
            _ => None,
        }
    }
}
#[derive(Clone, Debug)]
pub struct Status {
    pub mode: Mode,
    pub transitioning: bool,
    pub last_error: String,
    pub changed_at: SystemTime,
}
struct State {
    status: Status,
    loaded: bool,
    fingerprint: Option<(u64, SystemTime)>,
}
#[derive(Default)]
struct Activity {
    active: usize,
    exclusive: bool,
}
#[derive(Default)]
struct Gate {
    state: Mutex<Activity>,
    changed: Condvar,
}
pub struct Lease {
    gate: Arc<Gate>,
    exclusive: bool,
}
impl Drop for Lease {
    fn drop(&mut self) {
        if let Ok(mut activity) = self.gate.state.lock() {
            if self.exclusive {
                activity.exclusive = false;
            } else {
                activity.active = activity.active.saturating_sub(1);
            }
            self.gate.changed.notify_all();
        }
    }
}
impl Gate {
    fn shared(self: &Arc<Self>) -> Result<Lease, Error> {
        let mut activity = self
            .state
            .lock()
            .map_err(|_| "control activity unavailable")?;
        while activity.exclusive {
            activity = self
                .changed
                .wait(activity)
                .map_err(|_| "control activity unavailable")?;
        }
        activity.active = activity
            .active
            .checked_add(1)
            .ok_or("control activity exhausted")?;
        Ok(Lease {
            gate: self.clone(),
            exclusive: false,
        })
    }
    fn exclusive(self: &Arc<Self>, timeout: Duration) -> Result<Lease, Error> {
        let deadline = Instant::now()
            .checked_add(timeout)
            .ok_or("invalid control activity timeout")?;
        let mut activity = self
            .state
            .lock()
            .map_err(|_| "control activity unavailable")?;
        loop {
            if !activity.exclusive && activity.active == 0 {
                activity.exclusive = true;
                return Ok(Lease {
                    gate: self.clone(),
                    exclusive: true,
                });
            }
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err("timed out waiting for active control operations".into());
            }
            activity = self
                .changed
                .wait_timeout(activity, remaining)
                .map_err(|_| "control activity unavailable")?
                .0;
        }
    }
}
pub struct Manager {
    path: PathBuf,
    default: Mode,
    state: Mutex<State>,
    transition: Mutex<()>,
    gate: Arc<Gate>,
    wait: Duration,
}
struct Transition<'a>(&'a Manager);
impl Drop for Transition<'_> {
    fn drop(&mut self) {
        if let Ok(mut state) = self.0.state.lock() {
            state.status.transitioning = false;
        }
    }
}
fn hook(operation: impl FnOnce() -> Result<(), Error>) -> Result<(), Error> {
    std::panic::catch_unwind(std::panic::AssertUnwindSafe(operation))
        .unwrap_or_else(|_| Err("AI mode transition hook panicked".into()))
}
impl Manager {
    pub fn new(path: PathBuf, default: Mode) -> Arc<Self> {
        Self::with_timeout(path, default, Duration::from_secs(30))
    }
    pub fn with_timeout(path: PathBuf, default: Mode, wait: Duration) -> Arc<Self> {
        Arc::new(Self {
            path,
            default,
            state: Mutex::new(State {
                status: Status {
                    mode: default,
                    transitioning: false,
                    last_error: String::new(),
                    changed_at: SystemTime::UNIX_EPOCH,
                },
                loaded: false,
                fingerprint: None,
            }),
            transition: Mutex::new(()),
            gate: Arc::new(Gate::default()),
            wait: if wait.is_zero() {
                Duration::from_secs(30)
            } else {
                wait
            },
        })
    }
    pub fn status(&self) -> Result<Status, Error> {
        let mut state = self.state.lock().map_err(|_| "AI mode unavailable")?;
        if state.loaded && state.status.transitioning {
            return Ok(state.status.clone());
        }
        let fingerprint = match fs::metadata(&self.path) {
            Ok(info) => Some((info.len(), info.modified()?)),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
            Err(error) => return Err(format!("stat AI control mode: {error}").into()),
        };
        if state.loaded && state.fingerprint == fingerprint {
            return Ok(state.status.clone());
        }
        match fs::read(&self.path) {
            Ok(bytes) => {
                let value = String::from_utf8_lossy(&bytes);
                match Mode::parse(value.trim()) {
                    Some(mode) => {
                        state.status.mode = mode;
                        state.status.last_error.clear();
                    }
                    None => {
                        state.status.mode = Mode::Off;
                        state.status.last_error =
                            format!("invalid AI control mode {:?}", value.trim());
                    }
                }
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                state.status.mode = self.default;
            }
            Err(error) => return Err(format!("read AI control mode: {error}").into()),
        }
        if state.status.changed_at == SystemTime::UNIX_EPOCH {
            state.status.changed_at = SystemTime::now();
        }
        if let Some((_, modified)) = fingerprint {
            if modified > state.status.changed_at {
                state.status.changed_at = modified;
            }
        }
        state.fingerprint = fingerprint;
        state.loaded = true;
        Ok(state.status.clone())
    }
    pub fn current(&self) -> Mode {
        self.status().map(|s| s.mode).unwrap_or(Mode::Off)
    }
    pub fn acquire(self: &Arc<Self>, expected: Option<Mode>) -> Result<(Status, Lease), Error> {
        let verify = |status: &Status| -> Result<(), Error> {
            if status.transitioning || expected.is_some_and(|mode| mode != status.mode) {
                return Err("AI control mode conflict".into());
            }
            Ok(())
        };
        verify(&self.status()?)?;
        let lease = self.gate.shared()?;
        let status = self.status()?;
        verify(&status)?;
        Ok((status, lease))
    }
    fn save(&self, mode: Mode) -> Result<(), Error> {
        let mut state = self.state.lock().map_err(|_| "AI mode unavailable")?;
        atomic_write(&self.path, format!("{}\n", mode.as_str()).as_bytes(), 0o600)?;
        let info = fs::metadata(&self.path)?;
        state.fingerprint = Some((info.len(), info.modified()?));
        state.status.mode = mode;
        state.status.changed_at = SystemTime::now();
        state.loaded = true;
        Ok(())
    }
    pub fn switch(
        &self,
        expected: Option<Mode>,
        next: Mode,
        preempt: impl FnOnce() -> Result<(), Error>,
        cleanup: impl FnOnce() -> Result<(), Error>,
    ) -> Result<bool, Error> {
        let _serial = self
            .transition
            .lock()
            .map_err(|_| "AI transition unavailable")?;
        let status = self.status()?;
        if expected.is_some_and(|mode| mode != status.mode) {
            return Ok(false);
        }
        if next == status.mode {
            return Ok(true);
        }
        self.state
            .lock()
            .map_err(|_| "AI mode unavailable")?
            .status
            .transitioning = true;
        let _reset = Transition(self);
        let result = (|| -> Result<(), Error> {
            hook(preempt)?;
            let _activity = self.gate.exclusive(self.wait)?;
            if let Err(error) = hook(cleanup) {
                return match self.save(Mode::Off) {
                    Ok(()) => Err(error),
                    Err(rollback) => Err(format!("{error}; rollback failed: {rollback}").into()),
                };
            }
            self.save(next)
        })();
        self.state
            .lock()
            .map_err(|_| "AI mode unavailable")?
            .status
            .last_error = result
            .as_ref()
            .err()
            .map(ToString::to_string)
            .unwrap_or_default();
        result.map(|_| true)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{os::unix::fs::PermissionsExt, sync::mpsc, thread};
    #[test]
    fn persisted_modes_reload_invalid_state_and_permissions_match_go() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("mode");
        let manager = Manager::new(path.clone(), Mode::Picoclaw);
        assert_eq!(manager.current(), Mode::Picoclaw);
        assert!(!manager
            .switch(
                Some(Mode::Mcp),
                Mode::Off,
                || panic!("unmatched preempt"),
                || Ok(())
            )
            .unwrap());
        fs::write(&path, [0xff, b'\n']).unwrap();
        assert_eq!(manager.current(), Mode::Off);
        assert!(!manager.status().unwrap().last_error.is_empty());
        fs::write(&path, "invalid\n").unwrap();
        assert_eq!(manager.current(), Mode::Off);
        assert!(manager.status().unwrap().last_error.contains("invalid"));
        manager
            .switch(None, Mode::Mcp, || Ok(()), || Ok(()))
            .unwrap();
        assert_eq!(fs::read_to_string(&path).unwrap(), "mcp\n");
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        fs::write(&path, "picoclaw\n").unwrap();
        assert_eq!(manager.current(), Mode::Picoclaw);
    }
    #[test]
    fn preempt_precedes_drain_cleanup_and_new_operations_cannot_enter_transition() {
        let temp = tempfile::tempdir().unwrap();
        let manager = Manager::new(temp.path().join("mode"), Mode::Mcp);
        let (_, lease) = manager.acquire(Some(Mode::Mcp)).unwrap();
        let (preempted, saw_preempt) = mpsc::channel();
        let (cleaned, saw_cleanup) = mpsc::channel();
        let copy = manager.clone();
        let worker = thread::spawn(move || {
            copy.switch(
                None,
                Mode::Picoclaw,
                || {
                    preempted.send(()).unwrap();
                    Ok(())
                },
                || {
                    cleaned.send(()).unwrap();
                    Ok(())
                },
            )
        });
        saw_preempt.recv_timeout(Duration::from_secs(2)).unwrap();
        assert!(manager.acquire(None).is_err());
        assert!(saw_cleanup.try_recv().is_err());
        drop(lease);
        assert!(worker.join().unwrap().unwrap());
        saw_cleanup.recv_timeout(Duration::from_secs(2)).unwrap();
        assert_eq!(manager.current(), Mode::Picoclaw);
    }
    #[test]
    fn timeout_clears_transition_and_cleanup_failure_or_panic_fails_closed() {
        let temp = tempfile::tempdir().unwrap();
        let manager = Manager::with_timeout(
            temp.path().join("mode"),
            Mode::Mcp,
            Duration::from_millis(10),
        );
        let (_, lease) = manager.acquire(None).unwrap();
        assert!(manager
            .switch(
                None,
                Mode::Picoclaw,
                || Ok(()),
                || panic!("cleanup before drain")
            )
            .unwrap_err()
            .to_string()
            .contains("timed out"));
        assert!(!manager.status().unwrap().transitioning && manager.current() == Mode::Mcp);
        drop(lease);
        assert!(manager
            .switch(
                None,
                Mode::Picoclaw,
                || Ok(()),
                || Err("HID release failed".into())
            )
            .is_err());
        assert_eq!(manager.current(), Mode::Off);
        assert!(!manager.status().unwrap().transitioning);
        assert!(manager
            .switch(None, Mode::Mcp, || panic!("failed preempt"), || Ok(()))
            .is_err());
        assert_eq!(manager.current(), Mode::Off);
        manager
            .switch(None, Mode::Mcp, || Ok(()), || Ok(()))
            .unwrap();
        assert_eq!(manager.current(), Mode::Mcp);
    }
}
