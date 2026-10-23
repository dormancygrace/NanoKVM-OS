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
// Wrong passwords from every source count together: relayed viewers all
// share the relay's address. The answer to a wrong password waits longer
// after each one, which bounds guessing by the pending-handshake permits
// held meanwhile, while a correct password is admitted at once. A rotation
// or lockout instead would let anyone who knows the ID lock the owner out.
const FAILURE_DELAY: Duration = Duration::from_millis(300);
const FREE_FAILURES: u32 = 5;
// No longer than the login timeout: a connection that waits for its answer
// holds a pending permit, and an attacker should not get to hold them all
// for longer per wrong password than before.
const MAX_FAILURE_DELAY: Duration = Duration::from_secs(60);
const FAILURE_RESET: Duration = Duration::from_secs(15 * 60);
pub(crate) type Session = (String, String, u64);

pub(crate) struct Credentials {
    current: String,
    temporary: bool,
    path: PathBuf,
    failures: u32,
    last_failure: Option<Instant>,
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
            last_failure: None,
            recent: HashMap::new(),
        }
    }
    // The reconnect key of a login; the login of a connection never changes,
    // so a session computes it once.
    pub fn session(login: &LoginRequest) -> Option<Session> {
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
            if let Some((password, _)) = Self::session(login).and_then(|key| self.recent.get(&key))
            {
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
            self.last_failure = None;
            crate::status::password_failures(0);
            return Ok(Some(Verified {
                password: self.current.clone(),
                fresh: self.temporary,
            }));
        }
        Ok(None)
    }
    // Records a wrong password and says how long its answer waits.
    pub fn failed(&mut self) -> Duration {
        self.failed_at(Instant::now())
    }
    fn failed_at(&mut self, now: Instant) -> Duration {
        if self
            .last_failure
            .is_some_and(|last| now.saturating_duration_since(last) >= FAILURE_RESET)
        {
            self.failures = 0;
        }
        self.failures = self.failures.saturating_add(1);
        self.last_failure = Some(now);
        crate::status::password_failures(self.failures);
        let doublings = self.failures.saturating_sub(FREE_FAILURES).min(16);
        FAILURE_DELAY
            .saturating_mul(1 << doublings)
            .min(MAX_FAILURE_DELAY)
    }
    fn rotate(&mut self) -> io::Result<()> {
        let next = temporary_password::generate()?;
        temporary_password::replace(&self.path, &next)?;
        self.current = next;
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
        if let Some(key) = Self::session(login) {
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
    pub fn touch(&mut self, session: &Session) {
        if let Some(entry) = self.recent.get_mut(session) {
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
    fn wrong_passwords_slow_down_but_never_rotate_or_lock_out() {
        let config = Config {
            password: "ORIGINALPASS".into(),
            password_mode: PasswordMode::Temporary,
            ..Default::default()
        };
        let mut state = Credentials::new(&config);
        state.path = std::env::temp_dir().join(format!(
            "nanokvm-auth-never-written-{}",
            std::process::id()
        ));
        let hash = Hash {
            salt: "salt".into(),
            challenge: "challenge".into(),
        };
        let start = Instant::now();
        let mut delays = Vec::new();
        for _ in 0..40 {
            assert!(state
                .verify(&hash, &login("WRONG", &hash, "attacker", 1))
                .unwrap()
                .is_none());
            delays.push(state.failed_at(start));
        }
        assert!(delays[..5].iter().all(|delay| *delay == FAILURE_DELAY));
        assert_eq!(delays[5], FAILURE_DELAY * 2);
        assert!(delays.windows(2).all(|pair| pair[0] <= pair[1]));
        assert_eq!(delays[39], MAX_FAILURE_DELAY);
        // The password the owner reads stays valid and is admitted at once.
        assert_eq!(state.current, "ORIGINALPASS");
        assert!(!state.path.exists());
        assert!(state
            .verify(&hash, &login("ORIGINALPASS", &hash, "owner", 2))
            .unwrap()
            .is_some());
        assert_eq!(state.failed_at(start), FAILURE_DELAY);
        // A quiet period forgets earlier failures too.
        for _ in 0..20 {
            state.failed_at(start);
        }
        assert_eq!(state.failed_at(start + FAILURE_RESET), FAILURE_DELAY);
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
