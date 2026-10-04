use axum::{
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    Json,
};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::{fs, path::Path};

fn digest(data: &[u8]) -> String {
    Sha256::digest(data)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
pub fn status(root: &Path) -> Response {
    let directory = root.join("etc/kvm/branding");
    let c: Value = fs::read(directory.join("config.json"))
        .ok()
        .and_then(|v| serde_json::from_slice(&v).ok())
        .unwrap_or(Value::Null);
    let logo_enabled = c["logoCustom"].as_bool().unwrap_or(c["style"] == "custom");
    let favicon_enabled = c["faviconCustom"]
        .as_bool()
        .unwrap_or(!c["faviconRevision"].as_str().unwrap_or("").is_empty());
    let asset = |name: &str, configured: &str, enabled: bool| -> (String, bool) {
        if !enabled {
            return (String::new(), false);
        }
        match fs::read(directory.join(name)) {
            Ok(data) => (
                if configured.is_empty() {
                    digest(&data)
                } else {
                    configured.into()
                },
                true,
            ),
            Err(_) => (String::new(), false),
        }
    };
    let mut logo_revision = c["logoRevision"].as_str().unwrap_or("");
    if c["logoCustom"].is_null() && logo_enabled && logo_revision.is_empty() {
        logo_revision = c["revision"].as_str().unwrap_or("");
    }
    let (logo, custom_logo) = asset("logo.png", logo_revision, logo_enabled);
    let (favicon, custom_favicon) = asset(
        "favicon.png",
        c["faviconRevision"].as_str().unwrap_or(""),
        favicon_enabled,
    );
    let color = c["buttonColor"]
        .as_str()
        .unwrap_or("")
        .trim()
        .to_uppercase();
    let custom_color = color.len() == 7
        && color.starts_with('#')
        && color[1..].bytes().all(|b| b.is_ascii_hexdigit());
    let banner = fs::read_to_string(root.join("etc/kvm/banner-style"))
        .unwrap_or_default()
        .trim()
        .to_lowercase();
    let data = json!({"logoRevision":logo,"faviconRevision":favicon,"customLogoAvailable":custom_logo,"customFaviconAvailable":custom_favicon,
        "buttonColor":if custom_color{color.as_str()}else{"#45E9A0"},"customButtonColor":custom_color,"bannerStyle":if banner=="rainbow"{"rainbow"}else{"default"},
        "title":fs::read_to_string(root.join("etc/kvm/web-title")).unwrap_or_default().trim(),
        "style":if custom_logo{"custom"}else{"connection"},"revision":logo,"customAvailable":custom_logo});
    let mut r = Json(json!({"code":0,"msg":"success","data":data})).into_response();
    r.headers_mut()
        .insert("cache-control", "no-store".parse().unwrap());
    r
}
pub fn image(root: &Path, name: &str, headers: &HeaderMap) -> Response {
    let Ok(data) = fs::read(root.join("etc/kvm/branding").join(name)) else {
        return StatusCode::NOT_FOUND.into_response();
    };
    let etag = format!("\"{}\"", digest(&data));
    let mut r = if headers
        .get("if-none-match")
        .is_some_and(|v| v == etag.as_str())
    {
        StatusCode::NOT_MODIFIED.into_response()
    } else {
        ([(axum::http::header::CONTENT_TYPE, "image/png")], data).into_response()
    };
    r.headers_mut().insert("etag", etag.parse().unwrap());
    r.headers_mut()
        .insert("cache-control", "no-cache".parse().unwrap());
    r.headers_mut()
        .insert("x-content-type-options", "nosniff".parse().unwrap());
    r
}
