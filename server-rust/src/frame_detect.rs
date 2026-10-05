//! Shared frame detector intent and owned restoration after temporary pauses.
use crate::{
    api, media_session, native_capture::Request, native_capture_actor::Actor,
    request_cancel::Cancellation, Error, Runtime,
};
use axum::{
    body::Bytes,
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
};
use serde_json::Value;
use std::{
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex, MutexGuard,
    },
    time::{Duration, Instant},
};
use tokio::sync::Notify;
const NATIVE_TIMEOUT: Duration = Duration::from_secs(2);
struct State {
    desired: u8,
    version: u64,
    temporary: Option<u64>,
    pending: Option<(u64, u8)>,
    running: bool,
    completed: u64,
    error: Option<String>,
}
pub struct Manager {
    actor: Option<Actor>,
    state: Mutex<State>,
    control: Mutex<()>,
    stopping: AtomicBool,
    wake: Notify,
    #[cfg(test)]
    pub(crate) fixture_delays: Mutex<Option<Vec<i64>>>,
}
impl Manager {
    pub fn new(actor: Option<Actor>) -> Arc<Self> {
        Arc::new(Self {
            actor,
            state: Mutex::new(State {
                desired: 60,
                version: 0,
                temporary: None,
                pending: None,
                running: false,
                completed: 0,
                error: None,
            }),
            control: Mutex::new(()),
            stopping: AtomicBool::new(false),
            wake: Notify::new(),
            #[cfg(test)]
            fixture_delays: Mutex::new(None),
        })
    }
    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|error| error.into_inner())
    }
    fn admission(&self, cancelled: &dyn Fn() -> bool) -> Result<MutexGuard<'_, ()>, Error> {
        loop {
            if cancelled() || self.stopping.load(Ordering::Acquire) {
                return Err("frame detector request cancelled".into());
            }
            match self.control.try_lock() {
                Ok(guard) => return Ok(guard),
                Err(std::sync::TryLockError::Poisoned(_)) => {
                    return Err("frame detector control unavailable".into())
                }
                Err(std::sync::TryLockError::WouldBlock) => {
                    std::thread::sleep(Duration::from_millis(5))
                }
            }
        }
    }
    fn native(&self, value: u8) -> Result<(), Error> {
        let actor = self
            .actor
            .as_ref()
            .ok_or("native frame detector backend is not linked")?;
        if actor.stopped() {
            return Err("native frame detector backend stopped".into());
        }
        // Once admitted, complete this short bounded native command. Dropping
        // an in-flight actor call could poison the shared capture owner.
        let result = actor.call_blocking(
            Request::FrameDetect(value),
            Instant::now(),
            NATIVE_TIMEOUT,
            &|| false,
        )?;
        if result.status != 0 {
            return Err("native frame detector command failed".into());
        }
        Ok(())
    }
    pub(crate) fn set(&self, enabled: bool, cancelled: &Cancellation) -> Result<(), Error> {
        let _control = self.admission(&|| cancelled.cancelled())?;
        {
            self.lock()
                .version
                .checked_add(1)
                .ok_or("frame detector revision exhausted")?;
        }
        let value = if enabled { 60 } else { 0 };
        self.native(value)?;
        let mut state = self.lock();
        state.version = state
            .version
            .checked_add(1)
            .ok_or("frame detector revision exhausted")?;
        state.desired = value;
        state.temporary = None;
        state.pending = None;
        state.completed = state.version;
        state.error = None;
        drop(state);
        self.wake.notify_waiters();
        Ok(())
    }
    fn begin(self: &Arc<Self>, cancelled: &Cancellation) -> Result<Temporary, Error> {
        let _control = self.admission(&|| cancelled.cancelled())?;
        {
            self.lock()
                .version
                .checked_add(1)
                .ok_or("frame detector revision exhausted")?;
        }
        self.native(0)?;
        let mut state = self.lock();
        let version = state
            .version
            .checked_add(1)
            .ok_or("frame detector revision exhausted")?;
        state.version = version;
        state.temporary = Some(version);
        state.pending = None;
        state.completed = version;
        state.error = None;
        drop(state);
        self.wake.notify_waiters();
        Ok(Temporary {
            manager: self.clone(),
            version: Some(version),
        })
    }
    fn release(self: &Arc<Self>, version: u64) -> Option<u64> {
        let mut state = self.lock();
        if self.stopping.load(Ordering::Acquire) || state.temporary != Some(version) {
            return None;
        }
        let next = state.version.checked_add(1)?;
        state.version = next;
        state.temporary = None;
        state.pending = Some((next, state.desired));
        if !state.running {
            let Ok(handle) = tokio::runtime::Handle::try_current() else {
                state.error = Some("frame detector cleanup runtime unavailable".into());
                state.completed = next;
                state.pending = None;
                self.wake.notify_waiters();
                return Some(next);
            };
            state.running = true;
            let manager = self.clone();
            handle.spawn(async move {
                loop {
                    let next = { manager.lock().pending.take() };
                    let Some((version, value)) = next else {
                        let mut state = manager.lock();
                        // A newer release can arrive between the two locks.
                        if state.pending.is_some() {
                            continue;
                        }
                        state.running = false;
                        drop(state);
                        manager.wake.notify_waiters();
                        break;
                    };
                    let copy = manager.clone();
                    let result = tokio::task::spawn_blocking(move || {
                        let _control = copy.admission(&|| false)?;
                        if copy.lock().version != version {
                            return Ok::<_, Error>(());
                        }
                        copy.native(value)
                    })
                    .await;
                    let mut state = manager.lock();
                    if state.completed < version {
                        state.completed = version;
                        state.error = match result {
                            Ok(Ok(())) => None,
                            Ok(Err(error)) => Some(error.to_string()),
                            Err(error) => Some(error.to_string()),
                        };
                    }
                    drop(state);
                    manager.wake.notify_waiters();
                }
            });
        }
        Some(next)
    }
    async fn restored(&self, version: u64) -> Result<(), Error> {
        loop {
            let wake = self.wake.notified();
            tokio::pin!(wake);
            wake.as_mut().enable();
            {
                let state = self.lock();
                if self.stopping.load(Ordering::Acquire) {
                    return Err("frame detector stopped".into());
                }
                if state.completed >= version {
                    return state
                        .error
                        .clone()
                        .map_or(Ok(()), |error| Err(error.into()));
                }
            }
            wake.await;
        }
    }
    async fn delay(&self, nanos: i64) {
        #[cfg(test)]
        {
            let mut fixture = self.fixture_delays.lock().unwrap();
            if let Some(delays) = fixture.as_mut() {
                delays.push(nanos);
                return;
            }
        }
        if nanos > 0 {
            tokio::time::sleep(Duration::from_nanos(nanos as u64)).await;
        }
    }
    pub fn stop(&self) {
        self.stopping.store(true, Ordering::Release);
        let mut state = self.lock();
        state.temporary = None;
        state.pending = None;
        drop(state);
        self.wake.notify_waiters();
    }
    pub async fn join(&self) {
        loop {
            let wake = self.wake.notified();
            tokio::pin!(wake);
            wake.as_mut().enable();
            if !self.lock().running {
                return;
            }
            wake.await;
        }
    }
}
struct Temporary {
    manager: Arc<Manager>,
    version: Option<u64>,
}
impl Temporary {
    async fn finish(mut self) -> Result<(), Error> {
        let version = self
            .version
            .take()
            .and_then(|version| self.manager.release(version));
        if let Some(version) = version {
            self.manager.restored(version).await
        } else {
            Ok(())
        }
    }
}
impl Drop for Temporary {
    fn drop(&mut self) {
        if let Some(version) = self.version.take() {
            self.manager.release(version);
        }
    }
}
fn pause_nanos(seconds: i64) -> i64 {
    if seconds > 0 {
        seconds.wrapping_mul(1_000_000_000)
    } else {
        10_000_000_000
    }
}
pub(crate) fn update(
    runtime: &Runtime,
    parsed: Result<Value, Error>,
    cancelled: &Cancellation,
) -> Response {
    let Ok(parsed) = parsed else {
        return api::error(-1, "invalid parameters");
    };
    let enabled = parsed["enabled"].as_bool().unwrap_or(false);
    match runtime.frame_detect.set(enabled, cancelled) {
        Ok(()) => api::ok(Value::Null),
        Err(_) => api::error(-2, "cannot update frame detect"),
    }
}
pub(crate) async fn temporary(
    runtime: Arc<Runtime>,
    headers: HeaderMap,
    body: Bytes,
    query: Option<String>,
) -> Response {
    let principal = match media_session::authenticate(&runtime, &headers).await {
        Ok(principal) => principal,
        Err(response) => return *response,
    };
    let Ok(_slot) = runtime.socket_slots.clone().try_acquire_owned() else {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    };
    let Ok((_registration, mut revoked)) = media_session::Registration::new(&runtime, &principal)
    else {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    };
    let Ok(permit) = runtime.jobs.clone().try_acquire_owned() else {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    };
    let cancelled = Arc::new(Cancellation::default());
    let _guard = crate::request_cancel::Guard(cancelled.clone());
    let manager = runtime.frame_detect.clone();
    let who = principal.clone();
    let copy = runtime.clone();
    let started = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        if cancelled.cancelled() || !who.valid(&copy) {
            return Err(Box::new(api::unauthorized()));
        }
        let parsed = api::frame_detect_params(&headers, &body, query.as_deref())
            .map_err(|_| Box::new(api::error(-1, "invalid parameters")))?;
        let nanos = pause_nanos(parsed["duration"].as_i64().unwrap_or(0));
        let temporary = manager
            .begin(&cancelled)
            .map_err(|_| Box::new(api::error(-2, "cannot stop frame detect")))?;
        Ok((temporary, nanos))
    });
    let (temporary, nanos) = tokio::select! {biased;
        _=media_session::cancelled(&mut revoked)=>return api::unauthorized(),
        _=media_session::expired(&principal)=>return api::unauthorized(),
        result=started=>match result{Ok(Ok(value))=>value,Ok(Err(response))=>return *response,Err(_)=>return StatusCode::INTERNAL_SERVER_ERROR.into_response()},
    };
    tokio::select! {biased;
        _=media_session::cancelled(&mut revoked)=>return api::unauthorized(),
        _=media_session::expired(&principal)=>return api::unauthorized(),
        _=runtime.frame_detect.delay(nanos)=>{},
    }
    match temporary.finish().await {
        Ok(()) => api::ok(Value::Null),
        Err(_) => api::error(-2, "cannot restore frame detect"),
    }
}

#[cfg(all(test, feature = "native-fixture"))]
#[path = "frame_detect_tests.rs"]
mod tests;
