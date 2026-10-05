//! Durable HDMI intent and serialized capture demand. Native calls remain
//! explicit through the shared media backend; no vendor library is loaded here.
use crate::{fsroot, monitor::Backend, store::atomic_write, Error};
use serde_json::{json, Value};
use std::{
    collections::BTreeMap,
    fs,
    io::Read,
    os::unix::fs::PermissionsExt,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Condvar, Mutex, MutexGuard,
    },
    time::{Duration, Instant},
};

const WARMUP: Duration = Duration::from_secs(1);
const RESET: Duration = Duration::from_secs(1);
const MAX_TIMEOUT: i64 = 10080;

#[derive(Default)]
struct Demand {
    sources: BTreeMap<String, (usize, u64)>,
    viewers: usize,
    leases: usize,
    ready_at: Option<Instant>,
    fresh: bool,
}
impl Demand {
    fn update(&mut self, source: &str, count: i64, version: u64) -> Result<bool, Error> {
        if self
            .sources
            .get(source)
            .is_some_and(|(_, current)| version <= *current)
        {
            return Ok(false);
        }
        // Only internal streamers supply source names. Keep the bookkeeping
        // bounded even if a future transport accidentally uses session IDs.
        if source.len() > 128 || (!self.sources.contains_key(source) && self.sources.len() >= 64) {
            return Err("too many HDMI viewer sources".into());
        }
        let count = usize::try_from(count.max(0))?;
        let previous = self.sources.get(source).map_or(0, |(count, _)| *count);
        let total = self
            .viewers
            .checked_sub(previous)
            .and_then(|n| n.checked_add(count))
            .ok_or("HDMI viewer count overflow")?;
        self.sources.insert(source.to_owned(), (count, version));
        self.viewers = total;
        Ok(true)
    }
    fn next_version(&self, source: &str) -> u64 {
        self.sources
            .get(source)
            .map_or(0, |(_, version)| *version)
            .wrapping_add(1)
    }
    fn acquire(&mut self) -> Result<(), Error> {
        self.leases = self
            .leases
            .checked_add(1)
            .ok_or("HDMI lease count overflow")?;
        Ok(())
    }
    fn release(&mut self) -> bool {
        if self.leases == 0 {
            return false;
        }
        self.leases -= 1;
        true
    }
    fn has_demand(&self) -> bool {
        self.viewers > 0 || self.leases > 0
    }
    fn warm(&mut self, ready_at: Instant) {
        self.ready_at = Some(ready_at);
        self.fresh = true;
    }
    fn clear(&mut self) {
        self.ready_at = None;
        self.fresh = false;
    }
    fn claim(&mut self) -> bool {
        std::mem::take(&mut self.fresh)
    }
}
#[derive(Default)]
struct State {
    demand: Demand,
    running: Option<bool>,
    idle_stopped: bool,
    idle_at: Option<Instant>,
    epoch: u64,
    resetting: bool,
    read_busy: bool,
}
pub struct Manager {
    root: PathBuf,
    backend: Arc<dyn Backend>,
    state: Mutex<State>,
    changed: Condvar,
    stopped: AtomicBool,
}
impl Manager {
    pub fn new(root: PathBuf, backend: Arc<dyn Backend>) -> Arc<Self> {
        Arc::new(Self {
            root,
            backend,
            state: Mutex::new(State::default()),
            changed: Condvar::new(),
            stopped: AtomicBool::new(false),
        })
    }
    fn lock(&self) -> Result<MutexGuard<'_, State>, Error> {
        self.state
            .lock()
            .map_err(|_| "HDMI state unavailable".into())
    }
    fn active(&self) -> Result<(), Error> {
        if self.stopped.load(Ordering::Acquire) {
            Err("HDMI capture is stopping".into())
        } else {
            Ok(())
        }
    }
    fn path(&self, name: &str) -> Result<PathBuf, Error> {
        Ok(fsroot::resolve(&self.root, Path::new("/etc/kvm"), false)?.join(name))
    }
    fn enabled(&self) -> Result<bool, Error> {
        // A dangling/invalid disable marker is still an administrator's
        // disable intent. Read errors never silently grant permission.
        match fs::symlink_metadata(self.path("hdmi_disable")?) {
            Ok(_) => Ok(false),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(true),
            Err(error) => Err(error.into()),
        }
    }
    fn timeout(&self) -> u32 {
        let read = || -> Result<u32, Error> {
            let path = fsroot::resolve(&self.root, Path::new("/etc/kvm/hdmi_idle_timeout"), false)?;
            let mut data = String::new();
            fs::File::open(path)?.take(129).read_to_string(&mut data)?;
            if data.len() > 128 {
                return Err("invalid HDMI idle timeout".into());
            }
            let value = data.trim().parse::<i64>()?;
            if !(0..=MAX_TIMEOUT).contains(&value) {
                return Err("invalid HDMI idle timeout".into());
            }
            Ok(u32::try_from(value)?)
        };
        read().unwrap_or(0)
    }
    fn persist_enabled(&self, enabled: bool) -> Result<(), Error> {
        let path = self.path("hdmi_disable")?;
        let metadata = match fs::symlink_metadata(&path) {
            Ok(metadata) if metadata.is_file() => Some(metadata),
            Ok(_) => return Err("HDMI disable marker is not a regular file".into()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
            Err(error) => return Err(error.into()),
        };
        if enabled {
            if metadata.is_some() {
                fs::remove_file(&path)?;
                fs::File::open(path.parent().ok_or("missing HDMI settings directory")?)?
                    .sync_all()?;
            }
        } else if metadata.is_none() {
            atomic_write(&path, b"", 0o644)?;
        }
        Ok(())
    }
    fn schedule(&self, state: &mut State, now: Instant) -> Result<(), Error> {
        state.idle_at = None;
        if state.demand.has_demand()
            || state.idle_stopped
            || state.running != Some(true)
            || state.resetting
            || !self.enabled()?
        {
            return Ok(());
        }
        let minutes = self.timeout();
        if minutes > 0 {
            state.idle_at = Some(now + Duration::from_secs(u64::from(minutes) * 60));
        }
        Ok(())
    }
    fn apply(&self, state: &mut State, enabled: bool) -> Result<(), Error> {
        self.active()?;
        state.idle_at = None;
        state.demand.clear();
        state.running = None;
        self.backend.set_hdmi(enabled)?;
        state.running = Some(enabled);
        if enabled {
            state.demand.warm(Instant::now() + WARMUP);
        }
        self.changed.notify_all();
        Ok(())
    }
    fn reconcile(&self, state: &mut State) -> Result<(), Error> {
        state.idle_at = None;
        if state.demand.has_demand() {
            if state.idle_stopped && self.enabled()? && !state.resetting {
                self.apply(state, true)?;
                state.idle_stopped = false;
            }
            return Ok(());
        }
        self.schedule(state, Instant::now())
    }
    /// Called once after a real native backend has been initialized.
    pub fn initialize(&self) -> Result<(), Error> {
        let mut state = self.lock()?;
        self.active()?;
        let enabled = self.enabled()?;
        state.epoch = state.epoch.wrapping_add(1);
        state.resetting = false;
        state.idle_stopped = false;
        self.apply(&mut state, enabled)?;
        self.schedule(&mut state, Instant::now())
    }
    pub fn snapshot(&self) -> Result<Value, Error> {
        let state = self.lock()?;
        self.active()?;
        let enabled = self.enabled()?;
        let signal = enabled && self.backend.has_hdmi_signal()?;
        Ok(json!({"enabled":enabled,"viewerCount":state.demand.viewers,
            "signal":signal,"idleTimeout":self.timeout()}))
    }
    pub fn set_enabled(&self, enabled: bool) -> Result<(), Error> {
        let mut state = self.lock()?;
        self.active()?;
        self.persist_enabled(enabled)?;
        state.epoch = state.epoch.wrapping_add(1);
        state.resetting = false;
        state.idle_stopped = false;
        self.apply(&mut state, enabled)?;
        self.schedule(&mut state, Instant::now())
    }
    pub fn set_timeout(&self, minutes: i64) -> Result<(), Error> {
        if !(0..=MAX_TIMEOUT).contains(&minutes) {
            return Err("invalid HDMI idle timeout".into());
        }
        let mut state = self.lock()?;
        self.active()?;
        let path = self.path("hdmi_idle_timeout")?;
        let mode = match fs::symlink_metadata(&path) {
            Ok(metadata) if metadata.is_file() => metadata.permissions().mode() & 0o7777,
            Ok(_) => return Err("HDMI idle timeout is not a regular file".into()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => 0o644,
            Err(error) => return Err(error.into()),
        };
        atomic_write(&path, minutes.to_string().as_bytes(), mode)?;
        state.idle_at = None;
        if state.idle_stopped && self.enabled()? {
            self.apply(&mut state, true)?;
            state.idle_stopped = false;
        }
        self.schedule(&mut state, Instant::now())
    }
    pub fn reset(&self, cancelled: &dyn Fn() -> bool) -> Result<(), Error> {
        if cancelled() {
            return Err("request cancelled".into());
        }
        let ticket = {
            let mut state = self.lock()?;
            self.active()?;
            if !self.enabled()? {
                return Err("HDMI capture is disabled".into());
            }
            if cancelled() {
                return Err("request cancelled".into());
            }
            state.epoch = state.epoch.wrapping_add(1);
            state.idle_stopped = false;
            state.resetting = true;
            if let Err(error) = self.apply(&mut state, false) {
                state.resetting = false;
                return Err(error);
            }
            state.epoch
        };
        let deadline = Instant::now() + RESET;
        while Instant::now() < deadline && !cancelled() && !self.stopped.load(Ordering::Acquire) {
            std::thread::sleep(
                deadline
                    .saturating_duration_since(Instant::now())
                    .min(Duration::from_millis(10)),
            );
        }
        let mut state = self.lock()?;
        // A newer administrator action/reset owns the result. Neither normal
        // completion nor cancellation may overwrite that durable decision.
        if state.epoch == ticket && !self.stopped.load(Ordering::Acquire) {
            state.resetting = false;
            if self.enabled()? {
                self.apply(&mut state, true)?;
                self.schedule(&mut state, Instant::now())?;
            }
        }
        self.changed.notify_all();
        if cancelled() {
            Err("request cancelled".into())
        } else {
            self.active()
        }
    }
    pub fn update_viewers(&self, source: &str, count: i64, version: u64) -> Result<bool, Error> {
        let mut state = self.lock()?;
        self.active()?;
        if !state.demand.update(source, count, version)? {
            return Ok(false);
        }
        self.reconcile(&mut state)?;
        Ok(true)
    }
    pub fn set_viewers(&self, source: &str, count: i64) -> Result<(), Error> {
        let mut state = self.lock()?;
        self.active()?;
        let version = state.demand.next_version(source);
        if state.demand.update(source, count, version)? {
            self.reconcile(&mut state)?;
        }
        Ok(())
    }
    pub fn acquire(self: &Arc<Self>) -> Result<Lease, Error> {
        let mut state = self.lock()?;
        self.active()?;
        if !self.enabled()? {
            return Err("HDMI capture is disabled".into());
        }
        state.demand.acquire()?;
        if let Err(error) = self.reconcile(&mut state) {
            state.demand.release();
            return Err(error);
        }
        if state.running != Some(true) && !state.resetting {
            state.demand.release();
            return Err("HDMI capture is not initialized".into());
        }
        Ok(Lease {
            manager: self.clone(),
        })
    }
    pub fn acquire_read(
        self: &Arc<Self>,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<ReadLease, Error> {
        let lease = self.acquire()?;
        let mut state = self.lock()?;
        loop {
            self.active()?;
            if cancelled() {
                return Err("request cancelled".into());
            }
            if !self.enabled()? {
                return Err("HDMI capture is disabled".into());
            }
            if !state.resetting && state.running != Some(true) {
                return Err("HDMI capture is unavailable".into());
            }
            let now = Instant::now();
            let wait = state
                .demand
                .ready_at
                .map_or(Duration::ZERO, |ready| ready.saturating_duration_since(now));
            if !state.resetting && wait.is_zero() && !state.read_busy {
                state.read_busy = true;
                return Ok(ReadLease {
                    lease,
                    epoch: state.epoch,
                });
            }
            let duration = if wait.is_zero() {
                Duration::from_millis(25)
            } else {
                wait.min(Duration::from_millis(25))
            };
            state = self
                .changed
                .wait_timeout(state, duration)
                .map_err(|_| "HDMI state unavailable")?
                .0;
        }
    }
    fn tick(&self, now: Instant) -> Result<(), Error> {
        let mut state = self.lock()?;
        self.active()?;
        if !state.idle_at.is_some_and(|deadline| deadline <= now) {
            return Ok(());
        }
        state.idle_at = None;
        if state.demand.has_demand() || state.resetting || !self.enabled()? {
            return Ok(());
        }
        self.apply(&mut state, false)?;
        state.idle_stopped = true;
        Ok(())
    }
    pub fn start(self: &Arc<Self>) -> Option<tokio::task::JoinHandle<()>> {
        let handle = tokio::runtime::Handle::try_current().ok()?;
        let weak = Arc::downgrade(self);
        Some(handle.spawn(async move {
            loop {
                tokio::time::sleep(Duration::from_millis(250)).await;
                let Some(manager) = weak.upgrade() else {
                    break;
                };
                if manager.stopped.load(Ordering::Acquire) {
                    break;
                }
                let _ = tokio::task::spawn_blocking(move || {
                    if let Err(error) = manager.tick(Instant::now()) {
                        if !manager.stopped.load(Ordering::Acquire) {
                            eprintln!("HDMI idle update failed: {error}");
                        }
                    }
                })
                .await;
            }
        }))
    }
    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
        self.changed.notify_all();
    }
}
pub struct Lease {
    manager: Arc<Manager>,
}
impl Drop for Lease {
    fn drop(&mut self) {
        let Ok(mut state) = self.manager.lock() else {
            return;
        };
        if state.demand.release() && !self.manager.stopped.load(Ordering::Acquire) {
            if let Err(error) = self.manager.reconcile(&mut state) {
                eprintln!("HDMI lease release failed: {error}");
            }
        }
        self.manager.changed.notify_all();
    }
}
pub struct ReadLease {
    lease: Lease,
    epoch: u64,
}
impl ReadLease {
    /// A reader claims only after a successful native read. Failed reads leave
    /// the one fresh-frame discard available to the next successful reader.
    pub fn claim_fresh(&self) -> Result<bool, Error> {
        let manager = &self.lease.manager;
        let mut state = manager.lock()?;
        manager.active()?;
        if state.epoch != self.epoch
            || state.resetting
            || state.running != Some(true)
            || !manager.enabled()?
        {
            return Err("HDMI capture changed during read".into());
        }
        Ok(state.demand.claim())
    }
}
impl Drop for ReadLease {
    fn drop(&mut self) {
        if let Ok(mut state) = self.lease.manager.lock() {
            state.read_busy = false;
        }
        self.lease.manager.changed.notify_all();
    }
}

pub(crate) fn get(s: &crate::Runtime) -> axum::response::Response {
    match s.hdmi.snapshot() {
        Ok(value) => crate::api::ok(value),
        Err(error) => {
            eprintln!("HDMI state failed: {error}");
            crate::api::error(-2, "HDMI operation failed")
        }
    }
}
pub(crate) fn handle(
    s: &crate::Runtime,
    path: &str,
    parsed: Result<Value, Error>,
    cancelled: &crate::request_cancel::Cancellation,
) -> axum::response::Response {
    if cancelled.cancelled() {
        return crate::api::error(-2, "HDMI operation failed");
    }
    let result = match path {
        "/api/vm/hdmi/enable" => s.hdmi.set_enabled(true),
        "/api/vm/hdmi/disable" => s.hdmi.set_enabled(false),
        "/api/vm/hdmi/reset" => s.hdmi.reset(&|| cancelled.cancelled()),
        "/api/vm/hdmi/timeout" => {
            let Ok(value) = parsed else {
                return crate::api::error(-1, "invalid arguments");
            };
            let minutes = value["minutes"].as_i64().unwrap_or(0);
            if !(0..=MAX_TIMEOUT).contains(&minutes) {
                return crate::api::error(-1, "invalid arguments");
            }
            s.hdmi.set_timeout(minutes)
        }
        _ => unreachable!(),
    };
    match result {
        Ok(()) => crate::api::ok(Value::Null),
        Err(error) if error.to_string() == "HDMI capture is disabled" => {
            crate::api::error(-2, "HDMI capture is disabled")
        }
        Err(error) => {
            eprintln!("HDMI mutation failed: {error}");
            crate::api::error(-2, "HDMI operation failed")
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{os::unix::fs::symlink, sync::atomic::AtomicUsize};
    #[derive(Default)]
    struct Fake {
        calls: Mutex<Vec<bool>>,
        signal_calls: AtomicUsize,
        fail: AtomicBool,
        slow_off: AtomicBool,
    }
    impl Backend for Fake {
        fn stop_audio(&self) -> Result<(), Error> {
            Ok(())
        }
        fn apply_monitor_profile(&self, _: &Path) -> Result<(), Error> {
            Ok(())
        }
        fn set_hdmi(&self, enabled: bool) -> Result<(), Error> {
            self.calls.lock().unwrap().push(enabled);
            if !enabled && self.slow_off.load(Ordering::Acquire) {
                std::thread::sleep(Duration::from_millis(150));
            }
            if self.fail.load(Ordering::Acquire) {
                Err("fixture native failure".into())
            } else {
                Ok(())
            }
        }
        fn has_hdmi_signal(&self) -> Result<bool, Error> {
            self.signal_calls.fetch_add(1, Ordering::AcqRel);
            Ok(true)
        }
    }
    fn fixture() -> (tempfile::TempDir, Arc<Fake>, Arc<Manager>) {
        let temp = tempfile::tempdir().unwrap();
        fs::create_dir_all(temp.path().join("etc/kvm")).unwrap();
        let fake = Arc::new(Fake::default());
        let manager = Manager::new(temp.path().to_owned(), fake.clone());
        (temp, fake, manager)
    }
    fn wait_for(fake: &Fake, count: usize) {
        let deadline = Instant::now() + Duration::from_secs(3);
        while fake.calls.lock().unwrap().len() < count {
            assert!(
                Instant::now() < deadline,
                "native fixture call did not arrive"
            );
            std::thread::sleep(Duration::from_millis(1));
        }
    }
    #[test]
    fn actual_go_demand_transitions_revisions_leases_and_first_fresh_claim() {
        let oracle: Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/hdmi-go-oracle.json"
        ))
        .unwrap();
        let mut demand = Demand::default();
        for row in oracle["state"].as_array().unwrap() {
            let step = &row["step"];
            let accepted = match step["Op"].as_str().unwrap() {
                "viewer" => demand
                    .update(
                        step["Source"].as_str().unwrap(),
                        step["Count"].as_i64().unwrap(),
                        step["Version"].as_u64().unwrap(),
                    )
                    .unwrap(),
                "lease" => {
                    demand.acquire().unwrap();
                    false
                }
                "release" => demand.release(),
                "warm" => {
                    demand.warm(Instant::now());
                    false
                }
                "clear" => {
                    demand.clear();
                    false
                }
                "claim" => demand.claim(),
                _ => unreachable!(),
            };
            assert_eq!(
                json!({"step":step,"accepted":accepted,"viewers":demand.viewers,
                "leases":demand.leases,"demand":demand.has_demand(),"fresh":demand.fresh,
                "warming":demand.ready_at.is_some(),"nextA":demand.next_version("a")}),
                *row
            );
        }
    }
    #[test]
    fn source_and_counter_admission_failures_do_not_corrupt_existing_demand() {
        let mut demand = Demand::default();
        demand.update("a", i64::MAX, 1).unwrap();
        demand.update("b", i64::MAX, 1).unwrap();
        assert!(demand.update("c", 2, 1).is_err());
        assert!(!demand.sources.contains_key("c"));
        assert_eq!(demand.viewers, usize::MAX - 1);
        demand.leases = usize::MAX;
        assert!(demand.acquire().is_err());
        assert_eq!(demand.leases, usize::MAX);
        for i in 2..64 {
            demand.update(&format!("s{i}"), 0, 1).unwrap();
        }
        assert!(demand.update("overflow", 0, 1).is_err());
        assert!(demand.update(&"x".repeat(129), 0, 1).is_err());
        assert!(demand.update("a", 0, 2).unwrap());
    }
    #[test]
    fn saved_intent_and_timeout_initialize_capture_without_fabricating_signal() {
        let (temp, fake, manager) = fixture();
        fs::write(temp.path().join("etc/kvm/hdmi_idle_timeout"), " 10\n").unwrap();
        manager.initialize().unwrap();
        assert_eq!(*fake.calls.lock().unwrap(), [true]);
        let state = manager.lock().unwrap();
        assert!(state.demand.fresh);
        assert!(state.demand.ready_at.unwrap() > Instant::now());
        assert!(state.idle_at.unwrap() > Instant::now() + Duration::from_secs(599));
        drop(state);
        assert_eq!(
            manager.snapshot().unwrap(),
            json!({"enabled":true,"viewerCount":0,"signal":true,"idleTimeout":10})
        );
        manager.set_enabled(false).unwrap();
        let calls = fake.signal_calls.load(Ordering::Acquire);
        assert_eq!(manager.snapshot().unwrap()["signal"], false);
        assert_eq!(fake.signal_calls.load(Ordering::Acquire), calls);
        manager.initialize().unwrap();
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, false]);
        assert!(manager.acquire().is_err());
        let state = manager.lock().unwrap();
        assert!(!state.demand.fresh && state.demand.ready_at.is_none() && state.idle_at.is_none());
    }
    #[test]
    fn idle_stop_and_resume_use_authoritative_revisions_and_current_demand() {
        let (_temp, fake, manager) = fixture();
        manager.set_timeout(1).unwrap();
        manager.initialize().unwrap();
        let expired = manager.lock().unwrap().idle_at.unwrap();
        manager.update_viewers("direct", 1, 2).unwrap();
        assert!(!manager.update_viewers("direct", 0, 1).unwrap());
        manager.tick(expired + Duration::from_secs(1)).unwrap();
        assert_eq!(*fake.calls.lock().unwrap(), [true]);
        manager.update_viewers("direct", 0, 3).unwrap();
        let deadline = manager.lock().unwrap().idle_at.unwrap();
        manager.tick(deadline).unwrap();
        assert!(manager.lock().unwrap().idle_stopped);
        manager.update_viewers("mjpeg", 2, 1).unwrap();
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, true]);
        assert!(manager.lock().unwrap().demand.fresh);
        manager.update_viewers("mjpeg", -1, 2).unwrap();
        let deadline = manager.lock().unwrap().idle_at.unwrap();
        manager.set_enabled(false).unwrap();
        manager.tick(deadline).unwrap();
        manager.update_viewers("mjpeg", 1, 3).unwrap();
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, true, false]);
    }
    #[test]
    fn raii_leases_cancel_idle_and_resume_only_until_the_last_owner_releases() {
        let (_temp, fake, manager) = fixture();
        manager.set_timeout(1).unwrap();
        manager.initialize().unwrap();
        let a = manager.acquire().unwrap();
        let b = manager.acquire().unwrap();
        assert!(manager.lock().unwrap().idle_at.is_none());
        drop(a);
        assert_eq!(manager.lock().unwrap().demand.leases, 1);
        drop(b);
        let deadline = manager.lock().unwrap().idle_at.unwrap();
        manager.tick(deadline).unwrap();
        let a = manager.acquire().unwrap();
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, true]);
        manager.stop();
        drop(a);
        assert_eq!(manager.lock().unwrap().demand.leases, 0);
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, true]);
        assert!(manager.acquire().is_err());
    }
    #[test]
    fn read_warmup_slot_cancellation_and_failed_reads_preserve_one_fresh_claim() {
        let (_temp, _fake, manager) = fixture();
        manager.initialize().unwrap();
        let began = Instant::now();
        let read = manager.acquire_read(&|| false).unwrap();
        assert!(began.elapsed() >= Duration::from_millis(950));
        let other = manager.clone();
        let cancel = Arc::new(AtomicBool::new(false));
        let c = cancel.clone();
        let queued =
            std::thread::spawn(move || other.acquire_read(&|| c.load(Ordering::Acquire)).is_err());
        let deadline = Instant::now() + Duration::from_secs(3);
        while manager.lock().unwrap().demand.leases < 2 {
            assert!(Instant::now() < deadline);
            std::thread::sleep(Duration::from_millis(1));
        }
        cancel.store(true, Ordering::Release);
        assert!(queued.join().unwrap());
        drop(read); // Failed native read: deliberately do not claim.
        let read = manager.acquire_read(&|| false).unwrap();
        assert!(read.claim_fresh().unwrap());
        assert!(!read.claim_fresh().unwrap());
        manager.set_enabled(false).unwrap();
        assert!(read.claim_fresh().is_err());
        drop(read);
        assert_eq!(manager.lock().unwrap().demand.leases, 0);
        assert!(!manager.lock().unwrap().read_busy);
    }
    #[test]
    fn administrator_disable_wins_over_an_inflight_reset() {
        let (_temp, fake, manager) = fixture();
        manager.initialize().unwrap();
        let other = manager.clone();
        let reset = std::thread::spawn(move || other.reset(&|| false));
        wait_for(&fake, 2);
        manager.set_enabled(false).unwrap();
        reset.join().unwrap().unwrap();
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, false]);
        assert_eq!(manager.snapshot().unwrap()["enabled"], false);
    }
    #[test]
    fn cancelled_reset_restores_its_own_capture_and_stopped_reset_never_resumes() {
        let (_temp, fake, manager) = fixture();
        manager.initialize().unwrap();
        let cancelled = Arc::new(AtomicBool::new(false));
        let c = cancelled.clone();
        let other = manager.clone();
        let reset = std::thread::spawn(move || other.reset(&|| c.load(Ordering::Acquire)));
        wait_for(&fake, 2);
        cancelled.store(true, Ordering::Release);
        assert!(reset.join().unwrap().is_err());
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, true]);
        let other = manager.clone();
        let reset = std::thread::spawn(move || other.reset(&|| false));
        wait_for(&fake, 4);
        manager.stop();
        assert!(reset.join().unwrap().is_err());
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, true, false]);
    }
    #[test]
    fn persistent_failures_and_invalid_markers_never_send_native_commands() {
        let (temp, fake, manager) = fixture();
        let marker = temp.path().join("etc/kvm/hdmi_disable");
        fs::create_dir(&marker).unwrap();
        assert!(manager.set_enabled(true).is_err());
        assert!(manager.set_enabled(false).is_err());
        assert_eq!(manager.snapshot().unwrap()["enabled"], false);
        fs::remove_dir(&marker).unwrap();
        symlink("/missing", &marker).unwrap();
        assert_eq!(manager.snapshot().unwrap()["enabled"], false);
        assert!(manager.reset(&|| false).is_err());
        assert!(manager.set_enabled(true).is_err());
        let timeout = temp.path().join("etc/kvm/hdmi_idle_timeout");
        fs::create_dir(&timeout).unwrap();
        assert!(manager.set_timeout(10).is_err());
        assert!(fake.calls.lock().unwrap().is_empty());
        fs::remove_dir(&timeout).unwrap();
        fs::write(&timeout, "1").unwrap();
        fs::set_permissions(&timeout, fs::Permissions::from_mode(0o640)).unwrap();
        manager.set_timeout(2).unwrap();
        assert_eq!(
            fs::metadata(&timeout).unwrap().permissions().mode() & 0o777,
            0o640
        );
        assert_eq!(fs::read(&timeout).unwrap(), b"2");
    }
    #[test]
    fn failed_native_command_retains_intent_without_marking_a_ready_frame() {
        let (temp, fake, manager) = fixture();
        fake.fail.store(true, Ordering::Release);
        assert!(manager.set_enabled(false).is_err());
        assert!(temp.path().join("etc/kvm/hdmi_disable").exists());
        assert!(manager.set_enabled(true).is_err());
        assert!(!temp.path().join("etc/kvm/hdmi_disable").exists());
        let state = manager.lock().unwrap();
        assert!(
            state.running.is_none()
                && state.demand.ready_at.is_none()
                && !state.demand.fresh
                && state.idle_at.is_none()
        );
        drop(state);
        assert!(manager.acquire().is_err());
    }
    #[test]
    fn changing_timeout_resumes_an_idle_capture_and_reschedules_from_now() {
        let (_temp, fake, manager) = fixture();
        manager.set_timeout(1).unwrap();
        manager.initialize().unwrap();
        let deadline = manager.lock().unwrap().idle_at.unwrap();
        manager.tick(deadline).unwrap();
        manager.set_timeout(0).unwrap();
        assert_eq!(*fake.calls.lock().unwrap(), [true, false, true]);
        assert!(manager.lock().unwrap().idle_at.is_none());
        manager.set_timeout(2).unwrap();
        assert!(
            manager.lock().unwrap().idle_at.unwrap() > Instant::now() + Duration::from_secs(119)
        );
    }
    #[tokio::test(flavor = "current_thread")]
    async fn idle_worker_keeps_blocking_backend_off_the_event_loop_and_has_weak_ownership() {
        let (_temp, fake, manager) = fixture();
        manager.initialize().unwrap();
        fake.slow_off.store(true, Ordering::Release);
        manager.lock().unwrap().idle_at = Some(Instant::now());
        let task = manager.start().unwrap();
        let mut progress = 0;
        tokio::time::timeout(Duration::from_secs(3), async {
            while fake.calls.lock().unwrap().len() < 2 {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
            while !manager
                .state
                .try_lock()
                .is_ok_and(|state| state.idle_stopped)
            {
                progress += 1;
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        assert!(progress >= 3, "native delay blocked async event loop");
        let weak = Arc::downgrade(&manager);
        drop(manager);
        tokio::time::timeout(Duration::from_secs(3), task)
            .await
            .unwrap()
            .unwrap();
        assert!(weak.upgrade().is_none());
    }
}
