//! Bounded capture snapshots and the firmware's shared three-second FPS counter.
use crate::{screen_store, Error};
use serde::Serialize;
use std::{
    collections::BTreeMap,
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, AtomicI32, Ordering},
        Arc, Mutex,
    },
    time::Duration,
};
use tokio::sync::watch;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Mode {
    Direct,
    H264,
    Mjpeg,
}
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CaptureStatus {
    pub ok: bool,
    pub result: i32,
    pub message: &'static str,
    pub mode: Mode,
    pub severity: &'static str,
    pub updated_at: String,
}
impl CaptureStatus {
    fn new(mode: Mode, result: i32, updated_at: String) -> Self {
        let (message, severity) = match result {
            -7 => ("HDMI input resolution error", "error"),
            -6 => ("Unsupported HDMI resolution", "error"),
            -5 => ("Retrieving image", "warning"),
            -4 => ("Changing image resolution", "warning"),
            -3 => ("Image buffer full", "error"),
            -2 => ("Encoder error", "error"),
            -1 => ("No image captured", "error"),
            value if value < 0 => ("Capture failed", "error"),
            _ => ("", ""),
        };
        Self {
            ok: result >= 0,
            result,
            message,
            mode,
            severity,
            updated_at,
        }
    }
    fn same_public(&self, other: &Self) -> bool {
        (self.ok && other.ok)
            || (self.ok == other.ok && self.result == other.result && self.mode == other.mode)
    }
}
pub type Snapshot = Arc<BTreeMap<Mode, CaptureStatus>>;
pub struct Statuses {
    updates: watch::Sender<Snapshot>,
    state: Mutex<Snapshot>,
}
impl Statuses {
    pub fn new() -> Arc<Self> {
        let state = Arc::new(BTreeMap::new());
        let (updates, _) = watch::channel(state.clone());
        Arc::new(Self {
            updates,
            state: Mutex::new(state),
        })
    }
    pub fn subscribe(&self) -> watch::Receiver<Snapshot> {
        self.updates.subscribe()
    }
    pub fn snapshot(&self) -> Snapshot {
        self.updates.borrow().clone()
    }
    pub fn update(&self, mode: Mode, result: i32) {
        // Avoid formatting the same public success/error on every capture.
        if self
            .updates
            .borrow()
            .get(&mode)
            .is_some_and(|last| (last.ok && result >= 0) || (!last.ok && last.result == result))
        {
            return;
        }
        self.update_at(mode, result, local_timestamp());
    }
    fn update_at(&self, mode: Mode, result: i32, timestamp: String) {
        let next = CaptureStatus::new(mode, result, timestamp);
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.get(&mode).is_some_and(|last| last.same_public(&next)) {
            return;
        }
        Arc::make_mut(&mut state).insert(mode, next);
        self.updates.send_replace(state.clone());
    }
}
fn local_timestamp() -> String {
    let timestamp = jiff::Timestamp::now();
    let seconds = timestamp.as_second() as libc::time_t;
    let mut local = std::mem::MaybeUninit::<libc::tm>::uninit();
    if unsafe { libc::localtime_r(&seconds, local.as_mut_ptr()) }.is_null() {
        return timestamp.to_string();
    }
    let local = unsafe { local.assume_init() };
    let mut result = format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}",
        local.tm_year + 1900,
        local.tm_mon + 1,
        local.tm_mday,
        local.tm_hour,
        local.tm_min,
        local.tm_sec
    );
    let nanos = timestamp.subsec_nanosecond();
    if nanos != 0 {
        result.push('.');
        result.push_str(format!("{nanos:09}").trim_end_matches('0'));
    }
    let offset = local.tm_gmtoff;
    if offset == 0 {
        result.push('Z');
    } else {
        let absolute = offset.unsigned_abs();
        result.push_str(&format!(
            "{}{:02}:{:02}",
            if offset < 0 { '-' } else { '+' },
            absolute / 3600,
            (absolute % 3600) / 60
        ));
    }
    result
}
pub struct FrameRate {
    root: PathBuf,
    count: AtomicI32,
    fps: AtomicI32,
    started: AtomicBool,
    stopped: AtomicBool,
    task: Mutex<Option<tokio::task::JoinHandle<()>>>,
    io: Mutex<()>,
}
impl FrameRate {
    pub fn new(root: PathBuf) -> Arc<Self> {
        Arc::new(Self {
            root,
            count: AtomicI32::new(0),
            fps: AtomicI32::new(0),
            started: AtomicBool::new(false),
            stopped: AtomicBool::new(false),
            task: Mutex::new(None),
            io: Mutex::new(()),
        })
    }
    pub fn update(self: &Arc<Self>) {
        if self.stopped.load(Ordering::Acquire) {
            return;
        }
        self.count.fetch_add(1, Ordering::AcqRel);
        let Ok(handle) = tokio::runtime::Handle::try_current() else {
            return;
        };
        let mut task = self.task.lock().unwrap_or_else(|error| error.into_inner());
        if self.stopped.load(Ordering::Acquire) || self.started.swap(true, Ordering::AcqRel) {
            return;
        }
        let weak = Arc::downgrade(self);
        *task = Some(handle.spawn(async move {
            let mut timer = tokio::time::interval(Duration::from_secs(3));
            timer.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            timer.tick().await;
            loop {
                timer.tick().await;
                let Some(counter) = weak.upgrade() else {
                    break;
                };
                if counter.stopped.load(Ordering::Acquire) {
                    break;
                }
                let _ = tokio::task::spawn_blocking(move || {
                    if let Err(error) = counter.publish() {
                        eprintln!("capture FPS publication failed: {error}");
                    }
                })
                .await;
            }
        }));
    }
    fn publish(&self) -> Result<(), Error> {
        let _io = self
            .io
            .lock()
            .map_err(|_| "FPS publication lock unavailable")?;
        if self.stopped.load(Ordering::Acquire) {
            return Ok(());
        }
        let fps = self.count.swap(0, Ordering::AcqRel) / 3;
        self.fps.store(fps, Ordering::Release);
        screen_store::write(
            &self.root,
            "/run/nanokvm/now_fps",
            fps.to_string().as_bytes(),
            0o666,
            true,
        )
    }
    pub fn fps(&self) -> i32 {
        self.fps.load(Ordering::Acquire)
    }
    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
        if let Some(task) = self
            .task
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .take()
        {
            task.abort();
        }
        drop(self.io.lock().unwrap_or_else(|error| error.into_inner()));
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::{symlink, PermissionsExt};
    #[test]
    fn complete_go_status_sequence_keeps_sorted_latest_and_deduplicates() {
        let oracle: serde_json::Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/direct-contract-go-oracle.json"
        ))
        .unwrap();
        let statuses = Statuses::new();
        let mut receiver = statuses.subscribe();
        let mut notifications = 0;
        for step in oracle["captureStatuses"].as_array().unwrap() {
            let mode = match step["mode"].as_str().unwrap() {
                "direct" => Mode::Direct,
                "h264" => Mode::H264,
                "mjpeg" => Mode::Mjpeg,
                _ => panic!(),
            };
            statuses.update_at(
                mode,
                step["result"].as_i64().unwrap() as i32,
                step["timestamp"].as_str().unwrap().into(),
            );
            if receiver.has_changed().unwrap() {
                notifications += 1;
                receiver.borrow_and_update();
            }
            assert_eq!(notifications, step["notifications"].as_u64().unwrap());
            assert_eq!(
                serde_json::to_value(statuses.snapshot().values().collect::<Vec<_>>()).unwrap(),
                step["latest"]
            );
        }
        assert_eq!(statuses.snapshot().len(), 3);
        assert!(local_timestamp().parse::<jiff::Timestamp>().is_ok());
    }
    #[tokio::test]
    async fn fps_real_three_second_timer_preserves_mode_and_stops_owned_publication() {
        let root = tempfile::tempdir().unwrap();
        let dir = root.path().join("run/nanokvm");
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("now_fps");
        std::fs::write(&path, "old").unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o640)).unwrap();
        let counter = FrameRate::new(root.path().to_owned());
        for _ in 0..12 {
            counter.update();
        }
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "old");
        tokio::time::timeout(Duration::from_secs(5), async {
            while counter.fps() != 4 {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "4");
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o640
        );
        counter.stop();
        counter.update();
        counter.publish().unwrap();
        assert_eq!(std::fs::read_to_string(path).unwrap(), "4");
    }
    #[test]
    fn fps_is_confined_and_does_not_create_missing_firmware_parent() {
        let root = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let counter = FrameRate::new(root.path().to_owned());
        assert!(counter.publish().is_err());
        std::fs::create_dir(root.path().join("run")).unwrap();
        symlink(outside.path(), root.path().join("run/nanokvm")).unwrap();
        assert!(counter.publish().is_err());
        assert!(!outside.path().join("now_fps").exists());
    }
}
