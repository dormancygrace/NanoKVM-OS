//! Drop of an HTTP handler cancels its long blocking work, including paste.
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Condvar, Mutex,
};
#[derive(Default)]
pub(crate) struct Cancellation {
    cancelled: AtomicBool,
    wake: Condvar,
    state: Mutex<()>,
}
impl Cancellation {
    pub fn cancelled(&self) -> bool {
        self.cancelled.load(Ordering::Acquire)
    }
    pub fn cancel(&self) {
        let _state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        self.cancelled.store(true, Ordering::Release);
        self.wake.notify_all();
    }
    pub fn wait(&self, duration: std::time::Duration) -> Result<(), crate::Error> {
        let state = self
            .state
            .lock()
            .map_err(|_| "request cancellation unavailable")?;
        if self.cancelled() {
            return Err("request cancelled".into());
        }
        let _state = self
            .wake
            .wait_timeout_while(state, duration, |_| !self.cancelled())
            .map_err(|_| "request cancellation unavailable")?;
        if self.cancelled() {
            Err("request cancelled".into())
        } else {
            Ok(())
        }
    }
}
pub(crate) struct Guard(pub Arc<Cancellation>);
impl Drop for Guard {
    fn drop(&mut self) {
        self.0.cancel();
    }
}
