use std::{
    collections::HashMap,
    net::IpAddr,
    time::{Duration, Instant},
};

struct Attempt {
    failures: u32,
    last: Instant,
    until: Option<Instant>,
}
#[derive(Default)]
pub struct Lockout {
    entries: HashMap<String, Attempt>,
}
pub const LOCKED: &str = "Account locked due to too many failed attempts, please try again later";
pub const SATURATED: &str = "Too many failed login attempts, please try again later";
fn key(ip: IpAddr) -> String {
    match ip {
        IpAddr::V6(v) if v.to_ipv4_mapped().is_none() => {
            let mut b = v.octets();
            b[8..].fill(0);
            format!("{}/64", std::net::Ipv6Addr::from(b))
        }
        IpAddr::V6(v) => v.to_ipv4_mapped().unwrap().to_string(),
        IpAddr::V4(v) => v.to_string(),
    }
}
impl Lockout {
    pub fn check(&mut self, ip: IpAddr, duration: i64, now: Instant) -> Option<&'static str> {
        if duration <= 0 {
            return None;
        }
        if let Some(a) = self.entries.get_mut(&key(ip)) {
            if a.until.is_some_and(|t| t > now) {
                return Some(LOCKED);
            }
            if a.until.is_some() {
                a.failures = 0;
                a.until = None;
            }
        } else if self.entries.len() >= 3000
            && !self
                .entries
                .values()
                .any(|a| a.until.is_none_or(|t| t <= now))
        {
            return Some(SATURATED);
        }
        None
    }
    pub fn failure(
        &mut self,
        ip: IpAddr,
        duration: i64,
        maximum: i64,
        now: Instant,
    ) -> Option<&'static str> {
        if duration <= 0 {
            return None;
        }
        let key = key(ip);
        if !self.entries.contains_key(&key) && self.entries.len() >= 3000 {
            self.entries.retain(|_, a| {
                !a.until.is_some_and(|t| t <= now)
                    && !(a.until.is_none()
                        && now.duration_since(a.last) > Duration::from_secs(1800))
            });
            if self.entries.len() >= 3000 {
                let oldest = self
                    .entries
                    .iter()
                    .filter(|(_, a)| a.until.is_none())
                    .min_by_key(|(_, a)| a.last)
                    .map(|(k, _)| k.clone());
                if let Some(k) = oldest {
                    self.entries.remove(&k);
                } else {
                    return Some(SATURATED);
                }
            }
        }
        let a = self.entries.entry(key).or_insert(Attempt {
            failures: 0,
            last: now,
            until: None,
        });
        if now.duration_since(a.last) > Duration::from_secs(duration as u64) {
            a.failures = 0;
        }
        a.failures = a.failures.saturating_add(1);
        a.last = now;
        if i64::from(a.failures) >= maximum {
            a.until = Some(now + Duration::from_secs(duration as u64));
        }
        None
    }
    pub fn clear(&mut self, ip: IpAddr) {
        self.entries.remove(&key(ip));
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn ipv6_rotation_and_expired_lockout() {
        let mut l = Lockout::default();
        let now = Instant::now();
        l.failure("2001:db8::1".parse().unwrap(), 300, 1, now);
        assert_eq!(
            l.check("2001:db8::2".parse().unwrap(), 300, now),
            Some(LOCKED)
        );
        assert_eq!(l.check("2001:db8:0:1::1".parse().unwrap(), 300, now), None);
        assert_eq!(
            l.check(
                "2001:db8::2".parse().unwrap(),
                300,
                now + Duration::from_secs(301)
            ),
            None
        );
    }
    #[test]
    fn saturation_never_discards_active_lockouts() {
        let mut l = Lockout::default();
        let now = Instant::now();
        for n in 1..=3000 {
            l.failure(IpAddr::V4(std::net::Ipv4Addr::from(n)), 300, 1, now);
        }
        assert_eq!(
            l.check("192.0.2.1".parse().unwrap(), 300, now),
            Some(SATURATED)
        );
        assert_eq!(l.check("0.0.0.1".parse().unwrap(), 300, now), Some(LOCKED));
    }
}
