//! Persisted loopback-only internal credential, independent of browser auth.
use crate::{config, fsroot, store::atomic_write, Error};
use axum::http::HeaderMap;
use base64::{engine::general_purpose::URL_SAFE, Engine};
use ctutils::CtEq;
use std::{fs, net::IpAddr, path::Path};
pub struct Token(Vec<u8>);
impl Token {
    pub fn load(root: &Path) -> Result<Self, Error> {
        let path = config::rooted(root, "/etc/kvm/.picoclaw_internal_token")?;
        let path = fsroot::resolve(root, &path, true)?;
        let token = match fs::read(&path) {
            Ok(data) => match std::str::from_utf8(&data) {
                Ok(value) => value.trim().as_bytes().to_vec(),
                Err(_) => data,
            },
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Vec::new(),
            Err(error) => return Err(error.into()),
        };
        if !token.is_empty() {
            return Ok(Self(token));
        }
        let mut bytes = [0u8; 64];
        getrandom::fill(&mut bytes)?;
        let token = URL_SAFE.encode(bytes);
        atomic_write(&path, format!("{token}\n").as_bytes(), 0o600)?;
        Ok(Self(token.into_bytes()))
    }
    pub fn allowed(&self, peer: IpAddr, headers: &HeaderMap) -> bool {
        let loopback = match peer {
            IpAddr::V4(ip) => ip.is_loopback(),
            IpAddr::V6(ip) => ip
                .to_ipv4_mapped()
                .map_or_else(|| ip.is_loopback(), |ip| ip.is_loopback()),
        };
        loopback
            && headers
                .get("x-nanokvm-internal-token")
                .is_some_and(|value| bool::from(value.as_bytes().ct_eq(self.0.as_slice())))
    }
}
pub fn http_path(path: &str) -> bool {
    matches!(
        path,
        "/api/internal/usb/recover"
            | "/api/picoclaw/mcp"
            | "/api/picoclaw/runtime/session"
            | "/api/picoclaw/screenshot"
            | "/api/picoclaw/actions"
            | "/api/picoclaw/load-image"
    )
}
#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    #[test]
    fn persisted_generated_private_token_is_loopback_only_and_not_forwardable() {
        let temp = tempfile::tempdir().unwrap();
        fs::create_dir_all(temp.path().join("etc/kvm")).unwrap();
        let path = temp.path().join("etc/kvm/.picoclaw_internal_token");
        let first = Token::load(temp.path()).unwrap();
        assert_eq!(first.0.len(), 88);
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        let second = Token::load(temp.path()).unwrap();
        assert_eq!(first.0, second.0);
        let mut headers = HeaderMap::new();
        headers.insert(
            "x-nanokvm-internal-token",
            first.0.as_slice().try_into().unwrap(),
        );
        assert!(first.allowed("127.0.0.1".parse().unwrap(), &headers));
        assert!(first.allowed("::1".parse().unwrap(), &headers));
        assert!(first.allowed("::ffff:127.0.0.1".parse().unwrap(), &headers));
        headers.insert("x-forwarded-for", "127.0.0.1".parse().unwrap());
        assert!(!first.allowed("192.0.2.1".parse().unwrap(), &headers));
        headers.insert("x-nanokvm-internal-token", "wrong".parse().unwrap());
        assert!(!first.allowed("127.0.0.1".parse().unwrap(), &headers));
        fs::write(&path, "  existing secret\n").unwrap();
        assert_eq!(Token::load(temp.path()).unwrap().0, b"existing secret");
        // Runtime cache follows Go: file changes take effect after restart.
        assert_eq!(first.0, second.0);
    }
}
