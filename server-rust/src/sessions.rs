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
#[derive(Default)]
pub(crate) struct Registry(Mutex<HashMap<u64, Session>>);
impl Registry {
    pub fn register(&self, id: u64, username: &str) -> Result<watch::Receiver<bool>, Error> {
        let (cancel, receiver) = watch::channel(false);
        let mut sessions = self.0.lock().map_err(|_| "session registry unavailable")?;
        sessions.insert(
            id,
            Session {
                username: username.into(),
                cancel,
            },
        );
        Ok(receiver)
    }
    pub fn remove(&self, id: u64) {
        if let Ok(mut sessions) = self.0.lock() {
            sessions.remove(&id);
        }
    }
    pub fn revoke(&self, username: &str) -> Vec<u64> {
        let Ok(mut sessions) = self.0.lock() else {
            return Vec::new();
        };
        let ids: Vec<_> = sessions
            .iter()
            .filter(|(_, s)| s.username == username)
            .map(|(id, _)| *id)
            .collect();
        for id in &ids {
            if let Some(session) = sessions.remove(id) {
                session.cancel.send_replace(true);
            }
        }
        ids
    }
    pub fn revoke_all(&self) -> Vec<u64> {
        let Ok(mut sessions) = self.0.lock() else {
            return Vec::new();
        };
        sessions
            .drain()
            .map(|(id, session)| {
                session.cancel.send_replace(true);
                id
            })
            .collect()
    }
}
