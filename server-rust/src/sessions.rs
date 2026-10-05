//! Active sockets are revoked synchronously after a committed account change.
use crate::{store::User, Error, Runtime};
use std::{collections::HashMap, sync::Mutex};
use tokio::sync::watch;

#[derive(Clone)]
pub(crate) struct Principal {
    pub user: User,
    pub expires: Option<u64>,
}
impl Principal {
    pub fn valid(&self, runtime: &Runtime) -> bool {
        if runtime.stopping.load(std::sync::atomic::Ordering::Acquire) {
            return false;
        }
        if self.expires.is_none() && runtime.config.authentication == "disable" {
            return true;
        }
        let Ok(now) = crate::api::now() else {
            return false;
        };
        if self.expires.is_none_or(|expiry| expiry <= now) {
            return false;
        }
        runtime.store.get(&self.user.username).is_ok_and(|user| {
            user.enabled
                && !user.must_change_password
                && user.token_version == self.user.token_version
        })
    }
}
struct Session {
    username: String,
    cancel: watch::Sender<bool>,
}
#[derive(Clone, Copy, PartialEq, Eq, Hash)]
enum Key {
    Input(u64),
    Media(u64),
}
#[derive(Default)]
struct RegistryState {
    sessions: HashMap<Key, Session>,
    sequence: u64,
}
#[derive(Default)]
pub(crate) struct Registry(Mutex<RegistryState>);
impl Registry {
    pub fn register(&self, id: u64, username: &str) -> Result<watch::Receiver<bool>, Error> {
        let (cancel, receiver) = watch::channel(false);
        let mut state = self.0.lock().map_err(|_| "session registry unavailable")?;
        if state.sessions.contains_key(&Key::Input(id)) {
            return Err("input session already registered".into());
        }
        state.sessions.insert(
            Key::Input(id),
            Session {
                username: username.into(),
                cancel,
            },
        );
        Ok(receiver)
    }
    pub fn register_media(&self, username: &str) -> Result<(u64, watch::Receiver<bool>), Error> {
        let (cancel, receiver) = watch::channel(false);
        let mut state = self.0.lock().map_err(|_| "session registry unavailable")?;
        let id = state
            .sequence
            .checked_add(1)
            .ok_or("media session sequence exhausted")?;
        state.sequence = id;
        state.sessions.insert(
            Key::Media(id),
            Session {
                username: username.into(),
                cancel,
            },
        );
        Ok((id, receiver))
    }
    pub fn remove(&self, id: u64) {
        if let Ok(mut state) = self.0.lock() {
            state.sessions.remove(&Key::Input(id));
        }
    }
    pub fn remove_media(&self, id: u64) {
        if let Ok(mut state) = self.0.lock() {
            state.sessions.remove(&Key::Media(id));
        }
    }
    pub fn revoke(&self, username: &str) -> Vec<u64> {
        let Ok(mut state) = self.0.lock() else {
            return Vec::new();
        };
        let keys: Vec<_> = state
            .sessions
            .iter()
            .filter(|(_, session)| session.username == username)
            .map(|(key, _)| *key)
            .collect();
        let mut input = Vec::new();
        for key in keys {
            if let Some(session) = state.sessions.remove(&key) {
                session.cancel.send_replace(true);
                if let Key::Input(id) = key {
                    input.push(id);
                }
            }
        }
        input
    }
    pub fn revoke_all(&self) -> Vec<u64> {
        let Ok(mut state) = self.0.lock() else {
            return Vec::new();
        };
        let mut input = Vec::new();
        for (key, session) in state.sessions.drain() {
            session.cancel.send_replace(true);
            if let Key::Input(id) = key {
                input.push(id);
            }
        }
        input
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn media_and_input_ids_are_independent_and_revoke_returns_only_input() {
        let registry = Registry::default();
        let input = registry.register(1, "alice").unwrap();
        let (media_id, media) = registry.register_media("alice").unwrap();
        assert_eq!(media_id, 1);
        let other = registry.register(2, "bob").unwrap();
        assert_eq!(registry.revoke("alice"), vec![1]);
        assert!(*input.borrow() && *media.borrow());
        assert!(!*other.borrow());
        assert_eq!(registry.revoke_all(), vec![2]);
        assert!(*other.borrow());
    }
    #[test]
    fn media_removal_never_removes_input_with_same_number() {
        let registry = Registry::default();
        let input = registry.register(1, "alice").unwrap();
        let (id, media) = registry.register_media("bob").unwrap();
        registry.remove_media(id);
        assert!(media.has_changed().is_err());
        assert_eq!(registry.revoke("alice"), vec![1]);
        assert!(*input.borrow());
        assert!(registry.register(7, "alice").is_ok());
        assert!(registry.register(7, "alice").is_err());
    }
}
