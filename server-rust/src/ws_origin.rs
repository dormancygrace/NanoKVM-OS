//! Match the browser origin against the externally visible, trusted address.
use crate::config::Config;
use axum::http::{uri::Authority, HeaderMap, Uri};
use std::net::IpAddr;

fn authority(value: &str) -> Option<Authority> {
    if value.is_empty()
        || value
            .chars()
            .any(|c| c.is_whitespace() || "/?#@\\,".contains(c))
    {
        return None;
    }
    value.parse().ok()
}
fn origin(value: &str) -> Option<(Authority, String)> {
    let value = value.trim();
    if value.contains(['?', '#', '\\']) {
        return None;
    }
    let uri: Uri = value.parse().ok()?;
    let scheme = uri.scheme_str()?.to_ascii_lowercase();
    if !matches!(scheme.as_str(), "http" | "https") || !matches!(uri.path(), "" | "/") {
        return None;
    }
    Some((authority(uri.authority()?.as_str())?, scheme))
}
fn matches(a: &Authority, scheme: &str, b: &Authority, other_scheme: &str) -> bool {
    let default = if scheme == "https" { "443" } else { "80" };
    scheme == other_scheme
        && a.host().eq_ignore_ascii_case(b.host())
        && a.port()
            .map(|p| p.as_str().to_owned())
            .unwrap_or_else(|| default.into())
            == b.port()
                .map(|p| p.as_str().to_owned())
                .unwrap_or_else(|| default.into())
}
fn single(headers: &HeaderMap, name: &str) -> Option<String> {
    let mut values = headers.get_all(name).iter();
    let first = values.next()?.to_str().ok()?.trim();
    if values.next().is_some() || first.contains(',') {
        return None;
    }
    Some(first.into())
}
pub(crate) fn allowed(config: &Config, headers: &HeaderMap, peer: IpAddr) -> bool {
    let value = match single(headers, "origin") {
        Some(value) if value.is_empty() => return true,
        Some(value) if !value.is_empty() => value,
        _ if !headers.contains_key("origin")
            || headers
                .get("origin")
                .is_some_and(|h| h.as_bytes().is_empty()) =>
        {
            return true
        }
        _ => return false,
    };
    let Some((source, scheme)) = origin(&value) else {
        return false;
    };
    let mut target = single(headers, "host").and_then(|h| authority(&h));
    let mut protocol = if config.proto == "https" {
        "https".into()
    } else {
        "http".into()
    };
    let trusted = config.security.trusted_proxies.iter().any(|p| {
        let p = p.trim();
        p.parse::<ipnet::IpNet>().is_ok_and(|n| n.contains(&peer))
            || p.parse::<IpAddr>().is_ok_and(|ip| ip == peer)
    });
    if trusted {
        if let Some(host) = single(headers, "x-forwarded-host").and_then(|h| authority(&h)) {
            target = Some(host);
        }
        if let Some(proto) = single(headers, "x-forwarded-proto")
            .map(|p| p.to_ascii_lowercase())
            .filter(|p| matches!(p.as_str(), "http" | "https"))
        {
            protocol = proto;
        }
    }
    target.is_some_and(|host| matches(&source, &scheme, &host, &protocol))
        || config
            .security
            .allowed_origins
            .iter()
            .filter_map(|o| origin(o))
            .any(|(host, proto)| matches(&source, &scheme, &host, &proto))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn proxy_origin_default_ports_ipv6_and_explicit_allowlist() {
        let mut config = Config {
            proto: "http".into(),
            ..Default::default()
        };
        config.security.trusted_proxies.clear();
        let peer = "127.0.0.1".parse().unwrap();
        let mut headers = HeaderMap::new();
        headers.insert("host", "[::1]:80".parse().unwrap());
        assert!(allowed(&config, &headers, peer));
        headers.insert("origin", "http://[::1]".parse().unwrap());
        assert!(allowed(&config, &headers, peer));
        headers.insert("origin", "https://kvm.example".parse().unwrap());
        headers.insert("x-forwarded-host", "kvm.example".parse().unwrap());
        headers.insert("x-forwarded-proto", "https".parse().unwrap());
        assert!(!allowed(&config, &headers, peer));
        config.security.trusted_proxies.push("127.0.0.0/8".into());
        assert!(allowed(&config, &headers, peer));
        headers.insert(
            "x-forwarded-host",
            "kvm.example,evil.example".parse().unwrap(),
        );
        assert!(!allowed(&config, &headers, peer));
        config
            .security
            .allowed_origins
            .push("https://kvm.example/".into());
        assert!(allowed(&config, &headers, peer));
        for bad in [
            "null",
            "http://user@[::1]",
            "http://[::1]/path",
            "http://[::1]#fragment",
            "http://[::1]?query",
            "ws://[::1]",
        ] {
            headers.insert("origin", bad.parse().unwrap());
            assert!(!allowed(&config, &headers, peer), "{bad}");
        }
    }
}
