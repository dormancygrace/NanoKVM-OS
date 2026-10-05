//! Gin default route selection uses one URL-path decode and trailing-slash aliases.
use crate::{json_text, Error};
use axum::{
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
};
pub(crate) fn decode(raw: &str) -> Result<Vec<u8>, Error> {
    let mut result = Vec::with_capacity(raw.len());
    let bytes = raw.as_bytes();
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' {
            let digit = |b: u8| match b {
                b'0'..=b'9' => Some(b - b'0'),
                b'a'..=b'f' => Some(b - b'a' + 10),
                b'A'..=b'F' => Some(b - b'A' + 10),
                _ => None,
            };
            let a = bytes
                .get(index + 1)
                .copied()
                .and_then(digit)
                .ok_or("invalid URL escape")?;
            let b = bytes
                .get(index + 2)
                .copied()
                .and_then(digit)
                .ok_or("invalid URL escape")?;
            result.push(a * 16 + b);
            index += 3;
        } else {
            result.push(bytes[index]);
            index += 1;
        }
    }
    Ok(result)
}
pub(crate) fn not_found() -> Response {
    (
        StatusCode::NOT_FOUND,
        [("content-type", "text/plain")],
        "404 page not found",
    )
        .into_response()
}
pub(crate) fn text(path: &[u8]) -> String {
    json_text::text(path).into_owned()
}
pub(crate) fn slash(path: &[u8]) -> Option<Vec<u8>> {
    if path == b"/" || path.is_empty() {
        None
    } else if path.ends_with(b"/") {
        Some(path[..path.len() - 1].to_vec())
    } else {
        let mut candidate = path.to_vec();
        candidate.push(b'/');
        Some(candidate)
    }
}
fn escape(path: &[u8]) -> String {
    let mut result = String::new();
    const HEX: &[u8] = b"0123456789ABCDEF";
    for &byte in path {
        if byte.is_ascii_alphanumeric() || b"-_.~$&+,/:;=@".contains(&byte) {
            result.push(char::from(byte));
        } else {
            result.push('%');
            result.push(char::from(HEX[usize::from(byte >> 4)]));
            result.push(char::from(HEX[usize::from(byte & 15)]));
        }
    }
    result
}
pub(crate) fn redirect(
    method: &str,
    path: &[u8],
    query: Option<&str>,
    headers: &HeaderMap,
) -> Response {
    let mut path = path.to_vec();
    // Accept local proxy prefixes only; join once and keep the location local.
    if let Some(prefix) = headers
        .get("x-forwarded-prefix")
        .and_then(|value| value.to_str().ok())
    {
        if prefix.starts_with('/')
            && !prefix.starts_with("//")
            && prefix
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b"/-".contains(&b))
        {
            let prefix = prefix.trim_end_matches('/');
            if !prefix.is_empty() {
                let mut prefixed = prefix.as_bytes().to_vec();
                prefixed.push(b'/');
                prefixed.extend_from_slice(path.strip_prefix(b"/").unwrap_or(&path));
                path = prefixed;
            }
        }
    }
    let mut location = escape(&path);
    if let Some(query) = query {
        location.push('?');
        for b in query.bytes() {
            if b.is_ascii() {
                location.push(char::from(b));
            } else {
                location.push_str(&format!("%{b:02X}"));
            }
        }
    }
    let status = if method == "GET" {
        StatusCode::MOVED_PERMANENTLY
    } else {
        StatusCode::TEMPORARY_REDIRECT
    };
    let mut response = if method == "GET" {
        let href = location
            .replace('&', "&amp;")
            .replace('<', "&lt;")
            .replace('>', "&gt;")
            .replace('"', "&#34;")
            .replace('\'', "&#39;");
        (
            status,
            format!("<a href=\"{href}\">Moved Permanently</a>.\n\n"),
        )
            .into_response()
    } else {
        status.into_response()
    };
    if method == "GET" || method == "HEAD" {
        response
            .headers_mut()
            .insert("content-type", "text/html; charset=utf-8".parse().unwrap());
    }
    match location.parse() {
        Ok(value) => {
            response.headers_mut().insert("location", value);
            response
        }
        Err(_) => StatusCode::BAD_REQUEST.into_response(),
    }
}

#[derive(Clone)]
pub(crate) struct DecodedPath(pub Vec<u8>);
