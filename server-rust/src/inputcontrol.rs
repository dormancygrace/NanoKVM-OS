//! Manual reservations and the cancellable addon lane. Lock order for writes,
//! completion and revocation is write -> session -> coordinator. Mode leases
//! survive held reports and are dropped only after cleanup has finished.
use crate::{
    controlmode::{Lease, Manager, Mode},
    hid_reports::Kind,
    Error,
};
use std::{
    sync::{Arc, Condvar, Mutex},
    time::{Duration, Instant},
};
use tokio::sync::watch;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum OperationKind {
    Hid,
    ReadOnly,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Cause {
    Cancelled,
    ManualPreempted,
    ModeChanged,
}
struct Active {
    id: u64,
    kind: OperationKind,
    cancel: watch::Sender<Option<Cause>>,
}
#[derive(Default)]
struct Lane {
    active: Option<Active>,
    next: u64,
    manual: usize,
    until: Option<Instant>,
}
type Clock = Arc<dyn Fn() -> Instant + Send + Sync>;
pub struct Coordinator {
    state: Mutex<Lane>,
    changed: Condvar,
    write: Mutex<()>,
    cooldown: Duration,
    clock: Clock,
}
pub struct Operation {
    coordinator: Arc<Coordinator>,
    id: u64,
    cancelled: watch::Receiver<Option<Cause>>,
}
impl Operation {
    pub fn cause(&self) -> Option<Cause> {
        *self.cancelled.borrow()
    }
    pub async fn cancelled(&mut self) -> Cause {
        loop {
            if let Some(cause) = *self.cancelled.borrow_and_update() {
                return cause;
            }
            if self.cancelled.changed().await.is_err() {
                return Cause::Cancelled;
            }
        }
    }
}
impl Drop for Operation {
    fn drop(&mut self) {
        if let Ok(mut state) = self.coordinator.state.lock() {
            if state
                .active
                .as_ref()
                .is_some_and(|active| active.id == self.id)
            {
                let active = state.active.take().unwrap();
                active.cancel.send_if_modified(|cause| {
                    if cause.is_none() {
                        *cause = Some(Cause::Cancelled);
                        true
                    } else {
                        false
                    }
                });
                self.coordinator.changed.notify_all();
            }
        }
    }
}
impl Coordinator {
    pub fn new() -> Arc<Self> {
        Self::with_cooldown(Duration::from_secs(2))
    }
    pub fn with_cooldown(cooldown: Duration) -> Arc<Self> {
        Self::with_clock(cooldown, Arc::new(Instant::now))
    }
    fn with_clock(cooldown: Duration, clock: Clock) -> Arc<Self> {
        Arc::new(Self {
            state: Mutex::new(Lane::default()),
            changed: Condvar::new(),
            write: Mutex::new(()),
            cooldown,
            clock,
        })
    }
    /// Caller must hold its mode/activity lease until this operation is dropped.
    pub fn begin(self: &Arc<Self>, kind: OperationKind) -> Result<Operation, Error> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| "input coordinator unavailable")?;
        if state.active.is_some() {
            return Err("MCP remote control is busy".into());
        }
        if kind == OperationKind::Hid
            && (state.manual > 0 || state.until.is_some_and(|t| (self.clock)() < t))
        {
            return Err("manual control is active".into());
        }
        state.next = state
            .next
            .checked_add(1)
            .ok_or("input operation identifiers exhausted")?;
        let id = state.next;
        let (cancel, cancelled) = watch::channel(None);
        state.active = Some(Active { id, kind, cancel });
        Ok(Operation {
            coordinator: self.clone(),
            id,
            cancelled,
        })
    }
    pub fn cancel(&self, cause: Cause) {
        if let Ok(state) = self.state.lock() {
            if let Some(active) = &state.active {
                active.cancel.send_if_modified(|value| {
                    if value.is_none() {
                        *value = Some(cause);
                        true
                    } else {
                        false
                    }
                });
            }
        }
    }
    fn begin_manual(&self, wait: Duration) -> Result<(), Error> {
        let deadline = Instant::now()
            .checked_add(wait)
            .ok_or("invalid manual preemption timeout")?;
        let mut state = self
            .state
            .lock()
            .map_err(|_| "input coordinator unavailable")?;
        state.manual = state
            .manual
            .checked_add(1)
            .ok_or("manual sessions exhausted")?;
        let preempted = state
            .active
            .as_ref()
            .filter(|active| active.kind == OperationKind::Hid)
            .map(|active| {
                active.cancel.send_if_modified(|cause| {
                    if cause.is_none() {
                        *cause = Some(Cause::ManualPreempted);
                        true
                    } else {
                        false
                    }
                });
                active.id
            });
        while preempted
            .is_some_and(|id| state.active.as_ref().is_some_and(|active| active.id == id))
        {
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                state.manual -= 1;
                return Err("timed out waiting for MCP input cleanup".into());
            }
            state = self
                .changed
                .wait_timeout(state, remaining)
                .map_err(|_| "input coordinator unavailable")?
                .0;
        }
        Ok(())
    }
    fn end_manual(&self, cooldown: bool) {
        if let Ok(mut state) = self.state.lock() {
            state.manual = state.manual.saturating_sub(1);
            if cooldown && state.manual == 0 {
                state.until = (self.clock)().checked_add(self.cooldown);
            }
        }
    }
}

fn index(kind: Kind) -> usize {
    match kind {
        Kind::Keyboard => 0,
        Kind::Relative => 1,
        Kind::Absolute => 2,
    }
}
#[derive(Default)]
struct ManualState {
    active: bool,
    closed: bool,
    revoking: bool,
    generation: u64,
    pending: usize,
    held: [bool; 3],
    pending_held: [usize; 3],
    cooldown: bool,
    lease: Option<Lease>,
}
pub struct ManualSession {
    control: Arc<Manager>,
    coordinator: Arc<Coordinator>,
    state: Mutex<ManualState>,
}
pub struct Reservation {
    session: Arc<ManualSession>,
    generation: u64,
    kind: Kind,
    held: bool,
    cooldown: bool,
    completed: bool,
}
impl ManualSession {
    pub fn new(control: Arc<Manager>, coordinator: Arc<Coordinator>) -> Arc<Self> {
        Arc::new(Self {
            control,
            coordinator,
            state: Mutex::new(ManualState::default()),
        })
    }
    fn reservation(
        self: &Arc<Self>,
        state: &mut ManualState,
        kind: Kind,
        held: bool,
        cooldown: bool,
    ) -> Result<Reservation, Error> {
        let pending = state
            .pending
            .checked_add(1)
            .ok_or("manual report reservations exhausted")?;
        let pending_held = state.pending_held[index(kind)]
            .checked_add(usize::from(held))
            .ok_or("manual held reservations exhausted")?;
        state.pending = pending;
        state.pending_held[index(kind)] = pending_held;
        let release = !held
            && match kind {
                Kind::Keyboard => state.held[0] || state.pending_held[0] > 0,
                Kind::Relative | Kind::Absolute => {
                    state.held[1]
                        || state.held[2]
                        || state.pending_held[1] > 0
                        || state.pending_held[2] > 0
                }
            };
        Ok(Reservation {
            session: self.clone(),
            generation: state.generation,
            kind,
            held,
            cooldown: cooldown || release,
            completed: false,
        })
    }
    pub fn reserve(
        self: &Arc<Self>,
        kind: Kind,
        held: bool,
        cooldown: bool,
        wait: Duration,
        allow: impl Fn(Mode) -> bool,
    ) -> Result<Reservation, Error> {
        let (generation, active) = {
            let state = self
                .state
                .lock()
                .map_err(|_| "manual input session unavailable")?;
            if state.closed || state.revoking {
                return Err("manual input session is closed".into());
            }
            (state.generation, state.active)
        };
        if active {
            // This can stat/read the mode file. Keep queue Drop and async state
            // readers independent of its filesystem latency.
            let status = self.control.status()?;
            let permitted = !status.transitioning && allow(status.mode);
            let mut state = self
                .state
                .lock()
                .map_err(|_| "manual input session unavailable")?;
            if state.closed || state.revoking || state.generation != generation {
                return Err("manual input is blocked".into());
            }
            if state.active {
                let release = !held
                    && match kind {
                        Kind::Keyboard => state.held[0] || state.pending_held[0] > 0,
                        Kind::Relative | Kind::Absolute => {
                            state.held[1]
                                || state.held[2]
                                || state.pending_held[1] > 0
                                || state.pending_held[2] > 0
                        }
                    };
                if !permitted && !release {
                    return Err("manual input is blocked".into());
                }
                return self.reservation(&mut state, kind, held, cooldown);
            }
        }
        let (status, lease) = self.control.acquire(None)?;
        if !allow(status.mode) {
            return Err("manual input is blocked".into());
        }
        self.coordinator.begin_manual(wait)?;
        let mut state = self
            .state
            .lock()
            .map_err(|_| "manual input session unavailable")?;
        // A transfer may have revoked this client while it waited for MCP cleanup.
        if state.closed || state.revoking || state.generation != generation {
            self.coordinator.end_manual(false);
            return Err("manual input is blocked".into());
        }
        if state.active {
            self.coordinator.end_manual(false);
        } else {
            state.generation = match state.generation.checked_add(1) {
                Some(value) => value,
                None => {
                    self.coordinator.end_manual(false);
                    return Err("manual input generations exhausted".into());
                }
            };
            state.active = true;
            state.lease = Some(lease);
        }
        self.reservation(&mut state, kind, held, cooldown)
    }
    fn finish_idle(&self, state: &mut ManualState) {
        if state.active && state.pending == 0 && !state.held.iter().any(|held| *held) {
            state.active = false;
            state.lease.take();
            self.coordinator.end_manual(state.cooldown);
            state.cooldown = false;
        }
    }
    /// The cleanup closure may perform device IO but must not reenter a session.
    /// Keeping the lease through cleanup prevents mode changes from racing releases.
    pub fn revoke(
        &self,
        close: bool,
        cleanup: impl FnOnce() -> Result<(), Error>,
    ) -> Result<(), Error> {
        let _write = self
            .coordinator
            .write
            .lock()
            .map_err(|_| "manual input write unavailable")?;
        let mut state = self
            .state
            .lock()
            .map_err(|_| "manual input session unavailable")?;
        if close && state.closed {
            return Ok(());
        }
        state.closed |= close;
        state.revoking = true;
        state.generation = state
            .generation
            .checked_add(1)
            .ok_or("manual input generations exhausted")?;
        state.pending = 0;
        state.pending_held = [0; 3];
        let active = state.active;
        drop(state);
        let result = if active { cleanup() } else { Ok(()) };
        let mut state = self
            .state
            .lock()
            .map_err(|_| "manual input session unavailable")?;
        state.held = [false; 3];
        state.cooldown = false;
        state.revoking = false;
        state.lease.take();
        if state.active {
            state.active = false;
            self.coordinator.end_manual(close);
        }
        result
    }
}
impl Reservation {
    /// Generation validation, write, and completion use the same lock as revoke.
    /// Pointer workers release the other pointer kind before this callback returns.
    pub fn execute(mut self, write: impl FnOnce() -> Result<(), Error>) -> Result<bool, Error> {
        let session = self.session.clone();
        let _write = session
            .coordinator
            .write
            .lock()
            .map_err(|_| "manual input write unavailable")?;
        let state = session
            .state
            .lock()
            .map_err(|_| "manual input session unavailable")?;
        if !state.active || state.closed || state.generation != self.generation {
            self.completed = true;
            return Ok(false);
        }
        drop(state);
        let result = write();
        let mut state = session
            .state
            .lock()
            .map_err(|_| "manual input session unavailable")?;
        if result.is_ok() && self.kind != Kind::Keyboard {
            state.held[3 - index(self.kind)] = false;
        }
        self.complete_locked(&session, &mut state, Some(result.is_ok()));
        result.map(|_| true)
    }
    fn complete_locked(
        &mut self,
        session: &ManualSession,
        state: &mut ManualState,
        success: Option<bool>,
    ) {
        self.completed = true;
        if !state.active || state.generation != self.generation {
            return;
        }
        state.pending = state.pending.saturating_sub(1);
        if self.held {
            state.pending_held[index(self.kind)] =
                state.pending_held[index(self.kind)].saturating_sub(1);
        }
        state.cooldown |= self.cooldown;
        match success {
            Some(true) => state.held[index(self.kind)] = self.held,
            Some(false) => state.held = [false; 3],
            // A queued report that never ran cannot clear actual held keys.
            None => {}
        }
        session.finish_idle(state);
    }
}
impl Drop for Reservation {
    fn drop(&mut self) {
        if self.completed {
            return;
        }
        let session = self.session.clone();
        // No IO/write-lane wait in Drop: queue cancellation can run on Tokio.
        if let Ok(mut state) = session.state.lock() {
            self.complete_locked(&session, &mut state, None);
        };
    }
}
impl Drop for ManualSession {
    fn drop(&mut self) {
        if let Ok(state) = self.state.get_mut() {
            if state.active {
                self.coordinator.end_manual(true);
            }
        }
    }
}

/// PicoClaw's 30-minute session lock. Old cleanup must match the current owner.
pub struct PicoLock {
    state: Mutex<Option<PicoOwner>>,
    duration: Duration,
    clock: Clock,
}
struct PicoOwner {
    id: String,
    acquired: Instant,
    expires: Instant,
}
impl Default for PicoLock {
    fn default() -> Self {
        Self::with_duration(Duration::from_secs(30 * 60))
    }
}
impl PicoLock {
    pub fn with_duration(duration: Duration) -> Self {
        Self::with_clock(duration, Arc::new(Instant::now))
    }
    fn with_clock(duration: Duration, clock: Clock) -> Self {
        Self {
            state: Mutex::new(None),
            duration,
            clock,
        }
    }
    fn expire(&self, state: &mut Option<PicoOwner>) {
        if state
            .as_ref()
            .is_some_and(|owner| (self.clock)() >= owner.expires)
        {
            *state = None;
        }
    }
    pub fn acquire(&self, id: &str) -> Result<bool, Error> {
        if id.is_empty() {
            return Err("invalid PicoClaw session".into());
        }
        let mut state = self
            .state
            .lock()
            .map_err(|_| "PicoClaw session lock unavailable")?;
        self.expire(&mut state);
        let now = (self.clock)();
        let expires = now
            .checked_add(self.duration)
            .ok_or("invalid PicoClaw lock timeout")?;
        let acquired = state.is_none();
        if let Some(owner) = state.as_ref() {
            if owner.id != id {
                return Err("another PicoClaw session is running".into());
            }
        }
        let acquired_at = state.as_ref().map_or(now, |owner| owner.acquired);
        *state = Some(PicoOwner {
            id: id.into(),
            acquired: acquired_at,
            expires,
        });
        Ok(acquired)
    }
    pub fn renew(&self, id: &str) -> bool {
        let Ok(mut state) = self.state.lock() else {
            return false;
        };
        if let Some(owner) = state
            .as_mut()
            .filter(|owner| !id.is_empty() && owner.id == id)
        {
            if let Some(expires) = (self.clock)().checked_add(self.duration) {
                owner.expires = expires;
                return true;
            }
        }
        false
    }
    pub fn owner(&self) -> String {
        let Ok(mut state) = self.state.lock() else {
            return "lock unavailable".into();
        };
        self.expire(&mut state);
        state
            .as_ref()
            .map(|owner| owner.id.clone())
            .unwrap_or_default()
    }
    pub fn release_owned(&self, id: &str) -> bool {
        let Ok(mut state) = self.state.lock() else {
            return false;
        };
        self.expire(&mut state);
        if id.is_empty() || !state.as_ref().is_some_and(|owner| owner.id == id) {
            return false;
        }
        *state = None;
        true
    }
    pub fn release(&self, id: &str) -> bool {
        let Ok(mut state) = self.state.lock() else {
            return false;
        };
        if !id.is_empty() && state.as_ref().is_some_and(|owner| owner.id != id) {
            return false;
        }
        *state = None;
        true
    }
    pub fn force_takeover(&self, id: &str) -> Result<(), Error> {
        let now = (self.clock)();
        let expires = now
            .checked_add(self.duration)
            .ok_or("invalid PicoClaw lock timeout")?;
        *self
            .state
            .lock()
            .map_err(|_| "PicoClaw session lock unavailable")? =
            (!id.is_empty()).then(|| PicoOwner {
                id: id.into(),
                acquired: now,
                expires,
            });
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{sync::mpsc, thread};
    #[derive(Clone)]
    struct TestClock(Arc<Mutex<Instant>>);
    impl TestClock {
        fn new() -> Self {
            Self(Arc::new(Mutex::new(Instant::now())))
        }
        fn source(&self) -> Clock {
            let copy = self.clone();
            Arc::new(move || *copy.0.lock().unwrap())
        }
        fn advance(&self, duration: Duration) {
            let mut time = self.0.lock().unwrap();
            *time += duration;
        }
    }
    fn setup(
        cooldown: Duration,
    ) -> (
        tempfile::TempDir,
        Arc<Manager>,
        Arc<Coordinator>,
        Arc<ManualSession>,
        TestClock,
    ) {
        let temp = tempfile::tempdir().unwrap();
        let mode = Manager::new(temp.path().join("mode"), Mode::Mcp);
        let clock = TestClock::new();
        let lane = Coordinator::with_clock(cooldown, clock.source());
        let manual = ManualSession::new(mode.clone(), lane.clone());
        (temp, mode, lane, manual, clock)
    }
    fn reserve(manual: &Arc<ManualSession>, kind: Kind, held: bool, cooldown: bool) -> Reservation {
        manual
            .reserve(kind, held, cooldown, Duration::from_secs(1), |_| true)
            .unwrap()
    }
    #[test]
    fn manual_preempts_hid_but_read_only_runs_during_held_keys() {
        let (_temp, mode, lane, manual, clock) = setup(Duration::from_millis(10));
        let operation = lane.begin(OperationKind::Hid).unwrap();
        assert!(lane.begin(OperationKind::ReadOnly).is_err());
        let copy = manual.clone();
        let worker = thread::spawn(move || reserve(&copy, Kind::Keyboard, true, true));
        let deadline = Instant::now() + Duration::from_secs(1);
        while operation.cause().is_none() {
            assert!(Instant::now() < deadline);
            thread::yield_now();
        }
        assert_eq!(operation.cause(), Some(Cause::ManualPreempted));
        assert!(!worker.is_finished());
        drop(operation);
        worker.join().unwrap().execute(|| Ok(())).unwrap();
        assert_eq!(mode.current(), Mode::Mcp);
        assert!(lane.begin(OperationKind::Hid).is_err());
        let read = lane.begin(OperationKind::ReadOnly).unwrap();
        reserve(&manual, Kind::Keyboard, false, true)
            .execute(|| Ok(()))
            .unwrap();
        assert_eq!(read.cause(), None);
        drop(read);
        assert!(lane.begin(OperationKind::Hid).is_err());
        clock.advance(Duration::from_millis(15));
        lane.begin(OperationKind::Hid).unwrap();
    }
    #[test]
    fn preemption_timeout_and_revocation_during_wait_do_not_leak_activity() {
        let (_temp, mode, lane, manual, _clock) = setup(Duration::ZERO);
        let operation = lane.begin(OperationKind::Hid).unwrap();
        assert!(manual
            .reserve(Kind::Keyboard, true, true, Duration::from_millis(5), |_| {
                true
            })
            .is_err());
        assert_eq!(lane.state.lock().unwrap().manual, 0);
        let copy = manual.clone();
        let worker = thread::spawn(move || {
            copy.reserve(Kind::Keyboard, true, true, Duration::from_secs(1), |_| true)
        });
        let deadline = Instant::now() + Duration::from_secs(1);
        while lane.state.lock().unwrap().manual == 0 {
            assert!(Instant::now() < deadline);
            thread::yield_now();
        }
        manual.revoke(false, || Ok(())).unwrap();
        drop(operation);
        assert!(worker.join().unwrap().is_err());
        assert_eq!(lane.state.lock().unwrap().manual, 0);
        mode.switch(None, Mode::Off, || Ok(()), || Ok(())).unwrap();
    }
    #[test]
    fn releases_during_mode_transition_drain_held_activity() {
        let (_temp, mode, lane, manual, _clock) = setup(Duration::ZERO);
        reserve(&manual, Kind::Keyboard, true, true)
            .execute(|| Ok(()))
            .unwrap();
        let (preempt, saw_preempt) = mpsc::channel();
        let copy = mode.clone();
        let switch = thread::spawn(move || {
            copy.switch(
                None,
                Mode::Picoclaw,
                || {
                    preempt.send(()).unwrap();
                    Ok(())
                },
                || Ok(()),
            )
        });
        saw_preempt.recv_timeout(Duration::from_secs(1)).unwrap();
        assert!(manual
            .reserve(Kind::Keyboard, true, true, Duration::ZERO, |_| true)
            .is_err());
        manual
            .reserve(Kind::Keyboard, false, false, Duration::ZERO, |_| false)
            .unwrap()
            .execute(|| Ok(()))
            .unwrap();
        switch.join().unwrap().unwrap();
        assert_eq!(mode.current(), Mode::Picoclaw);
        lane.begin(OperationKind::Hid).unwrap();
    }
    #[test]
    fn pointer_motion_pending_cooldown_and_pointer_switch_reset_match() {
        let (_temp, _mode, lane, manual, clock) = setup(Duration::from_millis(10));
        reserve(&manual, Kind::Relative, false, false)
            .execute(|| Ok(()))
            .unwrap();
        lane.begin(OperationKind::Hid).unwrap();
        let click = reserve(&manual, Kind::Relative, true, true);
        let movement = reserve(&manual, Kind::Relative, false, false);
        click.execute(|| Ok(())).unwrap();
        movement.execute(|| Ok(())).unwrap();
        assert!(lane.begin(OperationKind::Hid).is_err());
        clock.advance(Duration::from_millis(15));
        reserve(&manual, Kind::Relative, true, true)
            .execute(|| Ok(()))
            .unwrap();
        reserve(&manual, Kind::Absolute, false, false)
            .execute(|| Ok(()))
            .unwrap();
        assert!(!manual.state.lock().unwrap().active);
        assert!(lane.begin(OperationKind::Hid).is_err());
        manual.revoke(false, || Ok(())).unwrap();
        clock.advance(Duration::from_millis(15));
        lane.begin(OperationKind::Hid).unwrap();
    }
    #[test]
    fn revocation_serializes_with_io_and_invalidates_queued_reservations() {
        let (_temp, mode, lane, manual, _clock) = setup(Duration::ZERO);
        let report = reserve(&manual, Kind::Keyboard, true, true);
        let stale = reserve(&manual, Kind::Keyboard, true, true);
        let (started, saw_started) = mpsc::channel();
        let (finish, may_finish) = mpsc::channel();
        let worker = thread::spawn(move || {
            report.execute(|| {
                started.send(()).unwrap();
                may_finish.recv().unwrap();
                Ok(())
            })
        });
        saw_started.recv_timeout(Duration::from_secs(1)).unwrap();
        let (cleaned, saw_cleaned) = mpsc::channel();
        let copy = manual.clone();
        let revoke = thread::spawn(move || {
            copy.revoke(false, || {
                cleaned.send(()).unwrap();
                Ok(())
            })
        });
        assert!(saw_cleaned.recv_timeout(Duration::from_millis(10)).is_err());
        finish.send(()).unwrap();
        worker.join().unwrap().unwrap();
        revoke.join().unwrap().unwrap();
        assert!(!stale
            .execute(|| panic!("old generation wrote HID"))
            .unwrap());
        assert_eq!(lane.state.lock().unwrap().manual, 0);
        mode.switch(None, Mode::Off, || Ok(()), || Ok(())).unwrap();
        reserve(&manual, Kind::Keyboard, false, true)
            .execute(|| Err("write failed".into()))
            .unwrap_err();
        assert_eq!(lane.state.lock().unwrap().manual, 0);
    }
    #[test]
    fn cancelled_queue_keeps_actual_held_keys_and_pending_release_drains_transition() {
        let (_temp, mode, lane, manual, _clock) = setup(Duration::ZERO);
        reserve(&manual, Kind::Keyboard, true, true)
            .execute(|| Ok(()))
            .unwrap();
        drop(reserve(&manual, Kind::Keyboard, false, true));
        assert!(lane.begin(OperationKind::Hid).is_err());
        reserve(&manual, Kind::Keyboard, false, true)
            .execute(|| Ok(()))
            .unwrap();
        lane.begin(OperationKind::Hid).unwrap();
        let queued = reserve(&manual, Kind::Keyboard, true, true);
        let (preempt, saw_preempt) = mpsc::channel();
        let copy = mode.clone();
        let switch = thread::spawn(move || {
            copy.switch(
                None,
                Mode::Off,
                || {
                    preempt.send(()).unwrap();
                    Ok(())
                },
                || Ok(()),
            )
        });
        saw_preempt.recv_timeout(Duration::from_secs(1)).unwrap();
        let release = manual
            .reserve(Kind::Keyboard, false, true, Duration::ZERO, |_| false)
            .unwrap();
        queued.execute(|| Ok(())).unwrap();
        release.execute(|| Ok(())).unwrap();
        switch.join().unwrap().unwrap();
        assert_eq!(mode.current(), Mode::Off);
    }
    #[test]
    fn unowned_viewer_cleanup_cannot_release_another_clients_reports() {
        let (_temp, _mode, lane, manual, _clock) = setup(Duration::ZERO);
        let viewer = ManualSession::new(manual.control.clone(), lane.clone());
        reserve(&manual, Kind::Keyboard, true, true)
            .execute(|| Ok(()))
            .unwrap();
        viewer
            .revoke(true, || panic!("viewer released owner HID"))
            .unwrap();
        assert!(lane.begin(OperationKind::Hid).is_err());
        manual.revoke(true, || Ok(())).unwrap();
        lane.begin(OperationKind::Hid).unwrap();
    }
    #[test]
    fn picoclaw_renew_takeover_and_expired_owner_cleanup() {
        let clock = TestClock::new();
        let lock = PicoLock::with_clock(Duration::from_millis(10), clock.source());
        assert!(lock.acquire("").is_err());
        assert!(lock.acquire("one").unwrap());
        assert!(!lock.acquire("one").unwrap());
        assert!(lock.acquire("two").is_err());
        assert!(lock.renew("one"));
        lock.force_takeover("two").unwrap();
        assert!(!lock.release_owned("one"));
        assert_eq!(lock.owner(), "two");
        clock.advance(Duration::from_millis(15));
        assert!(!lock.release_owned("two"));
        assert_eq!(lock.owner(), "");
        assert!(lock.acquire("three").unwrap());
        assert!(!lock.release("two"));
        assert!(lock.release(""));
    }
}
