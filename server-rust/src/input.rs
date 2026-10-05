//! Socket-specific input ownership. Transport and HID workers consume tickets;
//! the transition lock orders report execution before revocation cleanup.
use crate::Error;
use ctutils::CtEq;
use serde::Serialize;
use std::{
    collections::HashMap,
    sync::{Arc, Mutex, MutexGuard},
};
use tokio::sync::watch;

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct ControlStatus {
    pub enabled: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub lease: Option<String>,
}
pub type ClientId = u64;
#[derive(Clone, Copy)]
pub struct Ticket {
    client: ClientId,
    generation: u64,
}
struct Client {
    lease: String,
    view_only: bool,
    status: watch::Sender<ControlStatus>,
    cleanup: Arc<dyn Fn(bool) + Send + Sync>,
}
struct External {
    lease: String,
    release: Arc<dyn Fn() + Send + Sync>,
}
#[derive(Default)]
struct State {
    clients: HashMap<ClientId, Client>,
    next: u64,
    generation: u64,
    owner: Option<ClientId>,
    external: Option<External>,
    cleanup: bool,
}
pub struct Hub {
    transition: Mutex<()>,
    state: Mutex<State>,
    release_reports: Arc<dyn Fn() + Send + Sync>,
}
fn lease() -> Result<String, Error> {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes)?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}
fn equal(a: &str, b: &str) -> bool {
    a.as_bytes().ct_eq(b.as_bytes()).into()
}
impl Hub {
    pub fn new(release_reports: impl Fn() + Send + Sync + 'static) -> Self {
        Self {
            transition: Mutex::new(()),
            state: Mutex::new(State::default()),
            release_reports: Arc::new(release_reports),
        }
    }
    fn state(&self) -> Result<MutexGuard<'_, State>, Error> {
        self.state
            .lock()
            .map_err(|_| "input ownership unavailable".into())
    }
    fn transition(&self) -> Result<MutexGuard<'_, ()>, Error> {
        self.transition
            .lock()
            .map_err(|_| "input transition unavailable".into())
    }
    fn publish(s: &State) {
        for (id, client) in &s.clients {
            let enabled = s.owner == Some(*id) && !s.cleanup;
            client.status.send_replace(ControlStatus {
                enabled,
                lease: enabled.then(|| client.lease.clone()),
            });
        }
    }
    pub fn join(&self) -> Result<(ClientId, watch::Receiver<ControlStatus>), Error> {
        self.join_with_cleanup(|_| {})
    }
    pub fn join_with_cleanup(
        &self,
        cleanup: impl Fn(bool) + Send + Sync + 'static,
    ) -> Result<(ClientId, watch::Receiver<ControlStatus>), Error> {
        let new_lease = lease()?;
        let _transition = self.transition()?;
        let mut state = self.state()?;
        state.next = state
            .next
            .checked_add(1)
            .ok_or("input client identifiers exhausted")?;
        let id = state.next;
        let (status, rx) = watch::channel(ControlStatus {
            enabled: false,
            lease: None,
        });
        state.clients.insert(
            id,
            Client {
                lease: new_lease,
                view_only: false,
                status,
                cleanup: Arc::new(cleanup),
            },
        );
        if state.owner.is_none() && state.external.is_none() && !state.cleanup {
            state.owner = Some(id);
        }
        Self::publish(&state);
        Ok((id, rx))
    }
    pub fn set_control(&self, id: ClientId, enabled: bool) -> Result<(), Error> {
        let _transition = self.transition()?;
        let mut state = self.state()?;
        let Some(client) = state.clients.get_mut(&id) else {
            return Ok(());
        };
        client.view_only = !enabled;
        if !enabled && state.owner != Some(id) {
            Self::publish(&state);
            return Ok(());
        }
        if enabled && state.owner == Some(id) {
            Self::publish(&state);
            return Ok(());
        }
        let previous = state.owner.take();
        let cleanup =
            previous.and_then(|id| state.clients.get(&id).map(|client| client.cleanup.clone()));
        let external = if enabled { state.external.take() } else { None };
        state.generation = state
            .generation
            .checked_add(1)
            .ok_or("input generation exhausted")?;
        state.cleanup = true;
        Self::publish(&state);
        drop(state);
        if let Some(external) = external {
            (external.release)();
        }
        if let Some(cleanup) = cleanup {
            cleanup(false);
        }
        if previous.is_some() {
            (self.release_reports)();
        }
        let mut state = self.state()?;
        state.cleanup = false;
        state.owner = enabled.then_some(id);
        Self::publish(&state);
        Ok(())
    }
    pub fn leave(&self, id: ClientId) -> Result<(), Error> {
        let _transition = self.transition()?;
        let mut state = self.state()?;
        let Some(client) = state.clients.remove(&id) else {
            return Ok(());
        };
        let owner = state.owner == Some(id);
        if owner {
            state.owner = None;
            state.generation = state
                .generation
                .checked_add(1)
                .ok_or("input generation exhausted")?;
            state.cleanup = true;
            Self::publish(&state);
        }
        drop(state);
        (client.cleanup)(true);
        if owner {
            (self.release_reports)();
        }
        state = self.state()?;
        if owner {
            state.cleanup = false;
        }
        if state.owner.is_none() && state.external.is_none() && state.clients.len() == 1 {
            state.owner = state
                .clients
                .iter()
                .find(|(_, c)| !c.view_only)
                .map(|(id, _)| *id);
        }
        Self::publish(&state);
        Ok(())
    }
    pub fn ticket(&self, id: ClientId) -> Option<Ticket> {
        let state = self.state().ok()?;
        (state.owner == Some(id) && !state.cleanup).then_some(Ticket {
            client: id,
            generation: state.generation,
        })
    }
    /// Serialize trusted background/lifecycle IO with browser ownership changes.
    /// The operation must not reenter this hub.
    /// Pause browser tickets, invalidate every queued/manual generation and
    /// release held input before privileged gadget lifecycle operations.
    pub fn reconfigure(&self, operation: impl FnOnce() -> Result<(), Error>) -> Result<(), Error> {
        let _transition = self.transition()?;
        let mut state = self.state()?;
        state.generation = state
            .generation
            .checked_add(1)
            .ok_or("input generation exhausted")?;
        state.cleanup = true;
        Self::publish(&state);
        let cleanup: Vec<_> = state
            .clients
            .values()
            .map(|client| client.cleanup.clone())
            .collect();
        let external = state.external.take();
        drop(state);
        if let Some(external) = external {
            (external.release)();
        }
        for cleanup in cleanup {
            cleanup(false);
        }
        let result = operation();
        let mut state = self.state()?;
        state.cleanup = false;
        Self::publish(&state);
        result
    }
    pub fn synchronized<T>(
        &self,
        operation: impl FnOnce() -> Result<T, Error>,
    ) -> Result<T, Error> {
        let _transition = self.transition()?;
        operation()
    }
    pub fn execute(
        &self,
        ticket: Ticket,
        operation: impl FnOnce() -> Result<(), Error>,
    ) -> Result<bool, Error> {
        let _transition = self.transition()?;
        let state = self.state()?;
        if state.cleanup
            || state.owner != Some(ticket.client)
            || state.generation != ticket.generation
        {
            return Ok(false);
        }
        drop(state);
        if let Err(error) = operation() {
            // Cancel reports already queued behind a failed device write.
            let mut state = self.state()?;
            state.generation = state
                .generation
                .checked_add(1)
                .ok_or("input generation exhausted")?;
            return Err(error);
        }
        Ok(true)
    }
    pub fn allows_http(&self, lease: &str) -> bool {
        let Ok(state) = self.state() else {
            return false;
        };
        if state.cleanup {
            return false;
        }
        let owner = state
            .external
            .as_ref()
            .map(|e| e.lease.as_str())
            .or_else(|| {
                state
                    .owner
                    .and_then(|id| state.clients.get(&id))
                    .map(|c| c.lease.as_str())
            });
        owner.is_none_or(|owner| !lease.is_empty() && equal(owner, lease))
    }
    pub fn acquire_external(
        &self,
        lease: String,
        release: impl Fn() + Send + Sync + 'static,
    ) -> Result<bool, Error> {
        if lease.is_empty() {
            return Ok(false);
        }
        let _transition = self.transition()?;
        let mut state = self.state()?;
        if state.owner.is_some() || state.external.is_some() || state.cleanup {
            return Ok(false);
        }
        state.external = Some(External {
            lease,
            release: Arc::new(release),
        });
        Ok(true)
    }
    pub fn release_external(&self, lease: &str) -> Result<(), Error> {
        // May be called by the trusted external cleanup callback. It does not
        // acquire the transition lock and cannot grant a browser ownership.
        let mut state = self.state()?;
        if state
            .external
            .as_ref()
            .is_some_and(|e| equal(&e.lease, lease))
        {
            state.external = None;
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};
    #[test]
    fn ownership_leases_view_only_and_stale_queue_tickets() {
        let released = Arc::new(AtomicUsize::new(0));
        let copy = released.clone();
        let hub = Hub::new(move || {
            copy.fetch_add(1, Ordering::SeqCst);
        });
        assert!(hub.allows_http(""));
        let (a, sa) = hub.join().unwrap();
        let (b, sb) = hub.join().unwrap();
        let lease_a = sa.borrow().lease.clone().unwrap();
        assert!(sa.borrow().enabled && !sb.borrow().enabled && sb.borrow().lease.is_none());
        assert!(hub.allows_http(&lease_a) && !hub.allows_http(""));
        let stale = hub.ticket(a).unwrap();
        hub.set_control(b, true).unwrap();
        assert_eq!(released.load(Ordering::SeqCst), 1);
        assert!(!hub
            .execute(stale, || panic!("stale report executed"))
            .unwrap());
        assert!(!hub.allows_http(&lease_a) && hub.allows_http(sb.borrow().lease.as_ref().unwrap()));
        hub.set_control(a, false).unwrap();
        hub.leave(b).unwrap();
        assert!(!sa.borrow().enabled && hub.ticket(a).is_none());
        hub.set_control(a, true).unwrap();
        assert!(hub.execute(hub.ticket(a).unwrap(), || Ok(())).unwrap());
    }
    #[test]
    fn promotion_waits_for_one_viewer_and_external_cleanup_finishes_first() {
        let hub = Arc::new(Hub::new(|| {}));
        let weak = Arc::downgrade(&hub);
        let cleaned = Arc::new(AtomicUsize::new(0));
        let copy = cleaned.clone();
        assert!(hub
            .acquire_external("external-test".into(), move || {
                let hub = weak.upgrade().unwrap();
                assert!(!hub.allows_http("external-test") && !hub.allows_http(""));
                hub.release_external("external-test").unwrap();
                copy.fetch_add(1, Ordering::SeqCst);
            })
            .unwrap());
        let (a, sa) = hub.join().unwrap();
        let (b, sb) = hub.join().unwrap();
        let (c, sc) = hub.join().unwrap();
        assert!(!sa.borrow().enabled && hub.allows_http("external-test"));
        hub.set_control(a, true).unwrap();
        assert_eq!(cleaned.load(Ordering::SeqCst), 1);
        hub.leave(a).unwrap();
        assert!(!sb.borrow().enabled && !sc.borrow().enabled);
        hub.leave(b).unwrap();
        assert!(sc.borrow().enabled && hub.ticket(c).is_some());
    }
}
