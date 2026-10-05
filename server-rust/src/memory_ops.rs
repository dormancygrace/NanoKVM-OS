//! Swap/video mutations delegate to the firmware's validated, flocked helpers.
use crate::{
    api::{error, ok},
    fsroot,
    memory_command::{Action, Board, SwapKind, VideoMode},
    memory_status, request_cancel, systemops, update_lock, Error, Runtime,
};
use axum::response::Response;
use serde_json::{json, Value};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex, MutexGuard,
    },
    time::Duration,
};
pub struct Manager {
    root: PathBuf,
    commands: Arc<dyn systemops::Executor>,
    mutation: Mutex<()>,
    stopped: AtomicBool,
}
impl Manager {
    pub fn new(root: PathBuf, commands: Arc<dyn systemops::Executor>) -> Arc<Self> {
        Arc::new(Self {
            root,
            commands,
            mutation: Mutex::new(()),
            stopped: AtomicBool::new(false),
        })
    }
    fn cancelled(&self, cancelled: &dyn Fn() -> bool) -> bool {
        self.stopped.load(Ordering::Acquire) || cancelled()
    }
    fn lock(&self, cancelled: &dyn Fn() -> bool) -> Result<MutexGuard<'_, ()>, Error> {
        loop {
            if self.cancelled(cancelled) {
                return Err("request cancelled".into());
            }
            match self.mutation.try_lock() {
                Ok(lock) => return Ok(lock),
                Err(std::sync::TryLockError::Poisoned(_)) => {
                    return Err("memory configuration unavailable".into())
                }
                Err(std::sync::TryLockError::WouldBlock) => {
                    std::thread::sleep(Duration::from_millis(10))
                }
            }
        }
    }
    pub fn configure(&self, action: Action, cancelled: &dyn Fn() -> bool) -> Result<(), Error> {
        if !matches!(action, Action::Configure { .. }) || !action.valid() {
            return Err("invalid swap type or size".into());
        }
        let _lock = self.lock(cancelled)?;
        let output = self.commands.memory(action, Duration::from_secs(120), &|| {
            self.cancelled(cancelled)
        });
        match output {
            Ok(out) if out.success => Ok(()),
            Ok(out) => Err(out.swap_error().into()),
            Err(_) => Err("Failed to change swap".into()),
        }
    }
    pub fn select_video(&self, mode: VideoMode, cancelled: &dyn Fn() -> bool) -> Result<(), Error> {
        if self.cancelled(cancelled) {
            return Err("request cancelled".into());
        }
        let _update = update_lock::Lock::acquire(&self.root)?;
        let _mutation = self.lock(cancelled)?;
        if !memory_status::video(&self.root).available {
            return Err("Install the kernel package with both video memory modes first".into());
        }
        let board = fsroot::resolve(
            &self.root,
            Path::new("/sys/firmware/devicetree/base/sipeed,board-revision"),
            false,
        )
        .and_then(|p| Ok(fs::read(p)?))
        .map_err(|_| "Board profile unavailable")?;
        let text = crate::json_text::text(&board);
        let board = Board::parse(text.trim_matches(['\0', ' ', '\r', '\n']))
            .ok_or("Board profile unavailable")?;
        let output = self.commands.memory(
            Action::Video { board, mode },
            Duration::from_secs(90),
            &|| self.cancelled(cancelled),
        );
        match output {
            Ok(out) if out.success => Ok(()),
            Ok(out) => Err(out.video_error().into()),
            Err(_) => Err("Failed to select video memory mode: ".into()),
        }
    }
    pub fn maintenance(&self, cancelled: &dyn Fn() -> bool) -> Result<(), Error> {
        if self.cancelled(cancelled) {
            return Ok(());
        }
        let read = |path: &str| {
            fsroot::resolve(&self.root, Path::new(path), false)
                .ok()
                .and_then(|p| fs::read(p).ok())
                .unwrap_or_default()
        };
        if !memory_status::recompress_enabled(&crate::json_text::text(&read(
            "/etc/kvm/memory.conf",
        ))) {
            return Ok(());
        }
        if !memory_status::active_swaps(&crate::json_text::text(&read("/proc/swaps")))
            .get("/dev/zram0")
            .is_some_and(|s| s.enabled)
        {
            return Ok(());
        }
        if self.cancelled(cancelled) {
            return Ok(());
        }
        let output = self
            .commands
            .memory(Action::Recompress, Duration::from_secs(15), &|| {
                self.cancelled(cancelled)
            })?;
        if output.success {
            Ok(())
        } else {
            Err(format!("device command failed: {}", output.maintenance_error()).into())
        }
    }
    pub fn start(self: &Arc<Self>) -> Option<tokio::task::JoinHandle<()>> {
        let handle = tokio::runtime::Handle::try_current().ok()?;
        let weak = Arc::downgrade(self);
        Some(handle.spawn(async move {
            let period = Duration::from_secs(15);
            let mut ticks = tokio::time::interval_at(tokio::time::Instant::now() + period, period);
            ticks.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            let mut warnings = Warnings::default();
            loop {
                ticks.tick().await;
                let Some(manager) = weak.upgrade() else { break };
                if manager.stopped.load(Ordering::Acquire) {
                    break;
                }
                let job = manager.clone();
                let result = tokio::task::spawn_blocking(move || job.maintenance(&|| false)).await;
                if manager.stopped.load(Ordering::Acquire) {
                    break;
                }
                let message = match result {
                    Ok(Ok(())) => None,
                    Ok(Err(e)) => Some(e.to_string()),
                    Err(e) => Some(e.to_string()),
                };
                if warnings.changed(message.as_deref()) {
                    eprintln!("ZRAM recompression skipped/failed: {}", message.unwrap());
                }
                drop(manager);
            }
        }))
    }
    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
    }
}
#[derive(Default)]
struct Warnings {
    last: Option<String>,
}
impl Warnings {
    fn changed(&mut self, error: Option<&str>) -> bool {
        let changed = error.is_some() && self.last.as_deref() != error;
        self.last = error.map(str::to_owned);
        changed
    }
}
fn request(value: Result<Value, Error>) -> Option<Action> {
    let value = value.ok()?;
    let kind = match value["kind"].as_str().unwrap_or("") {
        "zram" => SwapKind::Zram,
        "sd" => SwapKind::Sd,
        _ => return None,
    };
    let action = Action::Configure {
        kind,
        enabled: value["enabled"].as_bool().unwrap_or(false),
        size: value["sizeMiB"].as_i64().unwrap_or(0),
        recompress: value["recompress"].as_bool(),
    };
    action.valid().then_some(action)
}
pub(crate) fn swap(
    s: &Runtime,
    value: Result<Value, Error>,
    cancelled: &request_cancel::Cancellation,
) -> Response {
    let Some(action) = request(value) else {
        return error(-1, "Invalid swap type or size");
    };
    if let Err(e) = s.memory.configure(action, &|| cancelled.cancelled()) {
        return error(-2, &e.to_string());
    }
    memory_status::get(s)
}
pub(crate) fn video(
    s: &Runtime,
    value: Result<Value, Error>,
    cancelled: &request_cancel::Cancellation,
) -> Response {
    let mode = value
        .ok()
        .and_then(|v| VideoMode::parse(v["mode"].as_str().unwrap_or("")));
    let Some(mode) = mode else {
        return error(-1, "Invalid video memory mode");
    };
    if let Err(e) = s.memory.select_video(mode, &|| cancelled.cancelled()) {
        return error(-2, &e.to_string());
    }
    memory_status::get(s)
}
pub(crate) fn legacy_get(s: &Runtime) -> Response {
    match memory_status::read_status(&s.root) {
        Ok(status) => ok(json!({"size":if status.sd.enabled{status.sd.size_mib}else{0}})),
        Err(_) => error(-1, "Failed to read swap state"),
    }
}
pub(crate) fn legacy_set(
    s: &Runtime,
    value: Result<Value, Error>,
    cancelled: &request_cancel::Cancellation,
) -> Response {
    let Ok(value) = value else {
        return error(-1, "Invalid arguments");
    };
    let size = value["size"].as_i64().unwrap_or(0);
    let action = Action::Configure {
        kind: SwapKind::Sd,
        enabled: size != 0,
        size: if size == 0 { 256 } else { size },
        recompress: None,
    };
    match s.memory.configure(action, &|| cancelled.cancelled()) {
        Ok(()) => ok(Value::Null),
        Err(e) => error(-2, &e.to_string()),
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn repeated_warnings_reset_after_success() {
        let mut warnings = Warnings::default();
        assert!(warnings.changed(Some("first")));
        assert!(!warnings.changed(Some("first")));
        assert!(warnings.changed(Some("second")));
        assert!(!warnings.changed(None));
        assert!(warnings.changed(Some("second")));
    }
}
