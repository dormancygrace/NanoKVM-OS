//! Persisted jiggler settings and one inactivity worker. Background moves use
//! the addon lane, retain cleanup through cancellation, and never displace HID.
use crate::{
    api::{error, ok},
    controlmode::Mode,
    hid_reports::{self, Frame, Report},
    inputcontrol::OperationKind,
    store::atomic_write,
    Error, Runtime,
};
use axum::{http::Method, response::Response};
use serde_json::{json, Value};
use std::{
    fs,
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    time::Duration,
};
use tokio::{sync::watch, time::Instant};
const INTERVAL: Duration = Duration::from_secs(15);
struct State {
    enabled: bool,
    mode: String,
    last: Instant,
}
pub struct Jiggler {
    path: PathBuf,
    state: Mutex<State>,
    settings: Mutex<()>,
    changes: watch::Sender<()>,
    started: AtomicBool,
    stopped: AtomicBool,
}
impl Jiggler {
    pub fn load(path: PathBuf) -> Arc<Self> {
        let (enabled, mode) = match fs::read(&path) {
            Ok(bytes) => {
                let mode = String::from_utf8_lossy(&bytes).replace('\n', "");
                (
                    true,
                    if mode.is_empty() {
                        "relative".into()
                    } else {
                        mode
                    },
                )
            }
            Err(_) => (false, "relative".into()),
        };
        let (changes, _) = watch::channel(());
        Arc::new(Self {
            path,
            settings: Mutex::new(()),
            state: Mutex::new(State {
                enabled,
                mode,
                last: Instant::now(),
            }),
            changes,
            started: AtomicBool::new(false),
            stopped: AtomicBool::new(false),
        })
    }
    pub fn status(&self) -> Result<Value, Error> {
        let state = self.state.lock().map_err(|_| "jiggler state unavailable")?;
        Ok(json!({"enabled":state.enabled,"mode":state.mode}))
    }
    pub fn configure(&self, enabled: bool, mode: &str) -> Result<(), Error> {
        let _settings = self
            .settings
            .lock()
            .map_err(|_| "jiggler settings unavailable")?;
        if self.stopped.load(Ordering::Acquire) {
            return Err("jiggler is stopped".into());
        }
        if enabled {
            atomic_write(&self.path, mode.as_bytes(), 0o644)?;
        } else {
            fs::remove_file(&self.path)?;
        }
        // Async snapshots/activity updates must never wait for a filesystem sync.
        let mut state = self.state.lock().map_err(|_| "jiggler state unavailable")?;
        if enabled {
            if !state.enabled {
                state.last = Instant::now();
            }
            state.enabled = true;
            state.mode = mode.into();
        } else {
            // Preserve Go's default mode after disable.
            state.enabled = false;
            state.mode = "relative".into();
        }
        self.changes.send_replace(());
        Ok(())
    }
    pub fn update(&self) {
        if let Ok(mut state) = self.state.lock() {
            if state.enabled {
                state.last = Instant::now();
            }
        }
    }
    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
        self.changes.send_replace(());
    }
    pub fn start(runtime: &Arc<Runtime>) {
        let Ok(handle) = tokio::runtime::Handle::try_current() else {
            return;
        };
        let service = &runtime.jiggler;
        if service
            .started
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .is_err()
        {
            return;
        }
        service.update();
        let mut changes = service.changes.subscribe();
        let weak = Arc::downgrade(runtime);
        handle.spawn(async move {
            let mut tick = tokio::time::interval_at(Instant::now() + INTERVAL, INTERVAL);
            tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            let mut previously_enabled = false;
            loop {
                let Some(runtime) = weak.upgrade() else {
                    break;
                };
                if runtime.jiggler.stopped.load(Ordering::Acquire) {
                    break;
                }
                let enabled = runtime
                    .jiggler
                    .state
                    .lock()
                    .map(|s| s.enabled)
                    .unwrap_or(false);
                drop(runtime);
                if enabled && !previously_enabled {
                    tick.reset();
                }
                previously_enabled = enabled;
                tokio::select! {
                    result = changes.changed() => { if result.is_err() { break; } },
                    _ = tick.tick(), if enabled => {
                        let Some(runtime) = weak.upgrade() else { break; };
                        let inactive = runtime.jiggler.state.lock().is_ok_and(|s| s.enabled && s.last.elapsed() > INTERVAL);
                        if inactive {
                            if let Err(error) = move_once(runtime.clone()).await { eprintln!("jiggler move failed: {error}"); }
                            runtime.jiggler.update();
                        }
                    }
                }
            }
        });
    }
}
fn reports(mode: &str) -> (Report, Report) {
    let (first, last) = if mode == "absolute" {
        (
            vec![2, 0, 0, 0x3f, 0, 0x3f, 0, 0],
            vec![2, 0, 0xff, 0x3f, 0xff, 0x3f, 0, 0],
        )
    } else {
        (vec![2, 0, 10, 10, 0, 0], vec![2, 0, 0xf6, 0xf6, 0, 0])
    };
    let report = |bytes: Vec<u8>| match hid_reports::parse(&bytes).unwrap() {
        Frame::Report(report) => report,
        _ => unreachable!(),
    };
    (report(first), report(last))
}
async fn write(runtime: Arc<Runtime>, report: Report) -> Result<(), Error> {
    tokio::task::spawn_blocking(move || {
        runtime.input.synchronized(|| {
            if runtime.stopping.load(Ordering::Acquire) {
                return Err("runtime is stopping".into());
            }
            runtime.hid.write(&report)
        })
    })
    .await?
}
/// Trusted runtime/test adapter, not an HTTP endpoint. Returns false when busy.
pub async fn move_once(runtime: Arc<Runtime>) -> Result<bool, Error> {
    let Ok(admission) = runtime.control_jobs.clone().try_acquire_owned() else {
        return Ok(false);
    };
    let mut changes = runtime.jiggler.changes.subscribe();
    let copy = runtime.clone();
    let ready = tokio::task::spawn_blocking(move || -> Result<_, Error> {
        let _admission = admission;
        let state = copy
            .jiggler
            .state
            .lock()
            .map_err(|_| "jiggler state unavailable")?;
        if !state.enabled
            || copy.jiggler.stopped.load(Ordering::Acquire)
            || copy.stopping.load(Ordering::Acquire)
        {
            return Err("jiggler inactive".into());
        }
        let mode = state.mode.clone();
        drop(state);
        let (status, activity) = copy.control.acquire(None)?;
        if status.mode == Mode::Picoclaw && !copy.pico_lock.owner().is_empty() {
            return Err("PicoClaw session holds control".into());
        }
        let operation = copy.coordinator.begin(OperationKind::Hid)?;
        Ok((mode, activity, operation))
    })
    .await?;
    let Ok((mode, _activity, mut operation)) = ready else {
        return Ok(false);
    };
    let Ok(_worker) = runtime.hid_jobs.clone().try_acquire_owned() else {
        return Ok(false);
    };
    if operation.cause().is_some() || runtime.jiggler.stopped.load(Ordering::Acquire) {
        return Ok(false);
    }
    let (first, last) = reports(&mode);
    write(runtime.clone(), first).await?;
    tokio::select! {
        _ = operation.cancelled() => {},
        _ = changes.changed() => {},
        _ = tokio::time::sleep(Duration::from_millis(100)) => {},
    }
    // Manual admission waits for this compensation and operation drop.
    write(runtime, last).await?;
    Ok(true)
}
pub(crate) fn handle(
    runtime: &Runtime,
    method: &Method,
    parameters: Result<Value, Error>,
) -> Response {
    if method == Method::GET {
        return runtime
            .jiggler
            .status()
            .map(ok)
            .unwrap_or_else(|_| error(-1, "get mouse jiggler failed"));
    }
    let Ok(parameters) = parameters else {
        return error(-1, "invalid arguments");
    };
    let enabled = parameters["enabled"].as_bool().unwrap_or(false);
    let mode = parameters["mode"].as_str().unwrap_or("");
    match runtime.jiggler.configure(enabled, mode) {
        Ok(()) => ok(Value::Null),
        Err(_) => error(-2, "operation failed"),
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn configuration_empty_modes_disable_failure_and_persisted_newlines_match_go() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("mouse-jiggler");
        let jiggler = Jiggler::load(path.clone());
        assert_eq!(
            jiggler.status().unwrap(),
            json!({"enabled":false,"mode":"relative"})
        );
        assert!(jiggler.configure(false, "").is_err());
        jiggler.configure(true, "absolute").unwrap();
        assert_eq!(fs::read(&path).unwrap(), b"absolute");
        jiggler.configure(true, "").unwrap();
        assert_eq!(jiggler.status().unwrap(), json!({"enabled":true,"mode":""}));
        assert_eq!(
            Jiggler::load(path.clone()).status().unwrap(),
            json!({"enabled":true,"mode":"relative"})
        );
        fs::write(&path, "abso\nlute\n").unwrap();
        assert_eq!(
            Jiggler::load(path).status().unwrap(),
            json!({"enabled":true,"mode":"absolute"})
        );
        jiggler.configure(false, "").unwrap();
        assert_eq!(
            jiggler.status().unwrap(),
            json!({"enabled":false,"mode":"relative"})
        );
        jiggler.stop();
        assert!(jiggler.configure(true, "relative").is_err());
    }
}
