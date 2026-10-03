// SPDX-License-Identifier: AGPL-3.0-only
use crate::{
    config::{Config, PasswordMode},
    protocol::{Hash, LoginRequest},
    temporary_password,
};
use std::{
    collections::HashMap,
    io,
    path::PathBuf,
    time::{Duration, Instant},
};

const RECONNECT_GRACE: Duration = Duration::from_secs(30);
const MAX_RECENT_SESSIONS: usize = 32;
type Session = (String, String, u64);

pub(crate) struct Credentials {
    current: String,
    temporary: bool,
    path: PathBuf,
    failures: usize,
    recent: HashMap<Session, (String, Instant)>,
}
pub(crate) struct Verified {
    password: String,
    fresh: bool,
}

impl Credentials {
    pub fn new(config: &Config) -> Self {
        Self {
            current: config.password.clone(),
            temporary: config.password_mode == PasswordMode::Temporary,
            path: PathBuf::from(temporary_password::PATH),
            failures: 0,
            recent: HashMap::new(),
        }
    }
    fn key(login: &LoginRequest) -> Option<Session> {
        if login.my_id.is_empty()
            || login.session_id == 0
            || login.my_id.len() > 128
            || login.my_name.len() > 256
        {
            return None;
        }
        Some((login.my_id.clone(), login.my_name.clone(), login.session_id))
    }
    pub fn verify(&mut self, hash: &Hash, login: &LoginRequest) -> io::Result<Option<Verified>> {
        self.recent
            .retain(|_, (_, last)| last.elapsed() < RECONNECT_GRACE);
        if self.temporary {
            if let Some((password, _)) = Self::key(login).and_then(|key| self.recent.get(&key)) {
                if crate::server::verify_password(password, hash, login) {
                    return Ok(Some(Verified {
                        password: password.clone(),
                        fresh: false,
                    }));
                }
            }
        }
        if crate::server::verify_password(&self.current, hash, login) {
            self.failures = 0;
            return Ok(Some(Verified {
                password: self.current.clone(),
                fresh: self.temporary,
            }));
        }
        // Empty password probes are normal before the client's password dialog.
        if self.temporary && !login.password.is_empty() {
            self.failures += 1;
            if self.failures >= 10 {
                self.rotate()?;
            }
        }
        Ok(None)
    }
    fn rotate(&mut self) -> io::Result<()> {
        let next = temporary_password::generate()?;
        temporary_password::replace(&self.path, &next)?;
        self.current = next;
        self.failures = 0;
        Ok(())
    }
    pub fn admitted(&mut self, login: &LoginRequest, verified: Verified) -> io::Result<()> {
        if !self.temporary {
            return Ok(());
        }
        // A current password rotates only once a viewer slot has been granted.
        if verified.fresh {
            self.rotate()?;
        }
        if let Some(key) = Self::key(login) {
            if self.recent.len() >= MAX_RECENT_SESSIONS && !self.recent.contains_key(&key) {
                if let Some(oldest) = self
                    .recent
                    .iter()
                    .min_by_key(|(_, (_, time))| *time)
                    .map(|(key, _)| key.clone())
                {
                    self.recent.remove(&oldest);
                }
            }
            self.recent.insert(key, (verified.password, Instant::now()));
        }
        Ok(())
    }
    pub fn touch(&mut self, login: &LoginRequest) {
        if let Some(entry) = Self::key(login).and_then(|key| self.recent.get_mut(&key)) {
            entry.1 = Instant::now();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use sha2::{Digest, Sha256};
    fn login(password: &str, hash: &Hash, id: &str, session: u64) -> LoginRequest {
        let first = Sha256::digest([password.as_bytes(), hash.salt.as_bytes()].concat());
        let second = Sha256::digest([first.as_slice(), hash.challenge.as_bytes()].concat());
        LoginRequest {
            my_id: id.into(),
            my_name: "Friend".into(),
            session_id: session,
            password: second.to_vec(),
            ..Default::default()
        }
    }
    #[test]
    fn rotation_keeps_only_authenticated_session_reconnect_and_current_password() {
        let path = std::env::temp_dir().join(format!("nanokvm-auth-{}", std::process::id()));
        let config = Config {
            password: "ORIGINALPASS".into(),
            password_mode: PasswordMode::Temporary,
            ..Default::default()
        };
        let mut state = Credentials::new(&config);
        state.path = path.clone();
        let hash = Hash {
            salt: "salt".into(),
            challenge: "first".into(),
        };
        let first = login("ORIGINALPASS", &hash, "friend", 42);
        let verified = state.verify(&hash, &first).unwrap().unwrap();
        state.admitted(&first, verified).unwrap();
        let rotated = state.current.clone();
        assert_ne!(rotated, "ORIGINALPASS");
        assert_eq!(std::fs::read_to_string(&path).unwrap(), rotated);
        let next_hash = Hash {
            salt: "salt".into(),
            challenge: "new-challenge".into(),
        };
        let reconnect = login("ORIGINALPASS", &next_hash, "friend", 42);
        let verified = state.verify(&next_hash, &reconnect).unwrap().unwrap();
        state.admitted(&reconnect, verified).unwrap();
        assert_eq!(state.current, rotated);
        assert!(state
            .verify(&next_hash, &login("ORIGINALPASS", &next_hash, "other", 42))
            .unwrap()
            .is_none());
        assert!(state
            .verify(&next_hash, &login("ORIGINALPASS", &next_hash, "friend", 43))
            .unwrap()
            .is_none());
        state
            .recent
            .values_mut()
            .for_each(|(_, last)| *last = Instant::now() - RECONNECT_GRACE);
        assert!(state.verify(&next_hash, &reconnect).unwrap().is_none());
        assert!(state
            .verify(&next_hash, &login(&rotated, &next_hash, "new", 99))
            .unwrap()
            .is_some());
        std::fs::remove_file(path).unwrap();
    }
    #[test]
    fn permanent_credentials_do_not_rotate_or_create_reconnect_exceptions() {
        let mut state = Credentials::new(&Config {
            password: "permanent-password".into(),
            ..Default::default()
        });
        let hash = Hash {
            salt: "salt".into(),
            challenge: "challenge".into(),
        };
        let req = login("permanent-password", &hash, "friend", 42);
        let verified = state.verify(&hash, &req).unwrap().unwrap();
        state.admitted(&req, verified).unwrap();
        assert_eq!(state.current, "permanent-password");
        assert!(state.recent.is_empty());
    }
}
