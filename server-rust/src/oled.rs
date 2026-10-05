//! Persistent OLED sleep preference; the native display process consumes it.
use crate::{
    api::{error, ok},
    fsroot,
    store::atomic_write,
    Error, Runtime,
};
use axum::response::Response;
use serde_json::{json, Value};
use std::{fs, os::unix::fs::PermissionsExt, path::Path};
pub(crate) fn get(runtime: &Runtime) -> Response {
    if fsroot::resolve(&runtime.root, Path::new("/etc/kvm/oled_exist"), false)
        .and_then(|path| fs::metadata(path).map_err(Into::into))
        .is_err()
    {
        return ok(json!({"exist":false,"sleep":0}));
    }
    let contents = fsroot::resolve(&runtime.root, Path::new("/etc/kvm/oled_sleep"), false)
        .and_then(|path| fs::read(path).map_err(Into::into));
    let Ok(contents) = contents else {
        return ok(json!({"exist":true,"sleep":0}));
    };
    match String::from_utf8_lossy(&contents).trim().parse::<i64>() {
        Ok(sleep) => ok(json!({"exist":true,"sleep":sleep})),
        Err(_) => error(-1, "failed to parse OLED config"),
    }
}
pub(crate) fn set(runtime: &Runtime, parameters: Result<Value, Error>) -> Response {
    let Ok(v) = parameters else {
        return error(-1, "invalid arguments");
    };
    let sleep = v["sleep"].as_i64().unwrap_or(0);
    if ![-1, 0, 15, 30, 60, 180, 300, 600, 1800, 3600].contains(&sleep) {
        return error(-1, "invalid OLED sleep duration");
    }
    let Ok(_settings) = runtime.system_settings.lock() else {
        return error(-2, "failed to write data");
    };
    let Ok(path) = fsroot::resolve(&runtime.root, Path::new("/etc/kvm/oled_sleep"), true) else {
        return error(-2, "failed to write data");
    };
    let mode = match fs::metadata(&path) {
        Ok(meta) if meta.is_file() => meta.permissions().mode() & 0o7777,
        Ok(_) => return error(-2, "failed to write data"),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => 0o644,
        Err(_) => return error(-2, "failed to write data"),
    };
    match atomic_write(&path, sleep.to_string().as_bytes(), mode) {
        Ok(()) => ok(Value::Null),
        Err(_) => error(-2, "failed to write data"),
    }
}
