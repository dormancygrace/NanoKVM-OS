//! Auth and input lease are checked at entry and throughout a long ATX pulse.
use crate::{
    api::{error, ok},
    gpio_binding,
    request_cancel::Cancellation,
    sessions::Principal,
    Runtime,
};
use axum::{http::HeaderMap, response::Response};
use serde_json::Value;
use std::{sync::atomic::Ordering, time::Duration};
pub(crate) fn set(
    runtime: &Runtime,
    headers: &HeaderMap,
    body: &[u8],
    query: Option<&str>,
    principal: Principal,
    cancel: &Cancellation,
) -> Response {
    let req = match gpio_binding::parse(headers, body, query) {
        Ok(req) => req,
        Err(cause) => return error(-1, &format!("invalid arguments: {cause}")),
    };
    let device = match req.kind.as_str() {
        "power" => &runtime.hardware.power,
        "reset" => &runtime.hardware.reset,
        _ => return error(-2, &format!("invalid power event: {}", req.kind)),
    };
    if req.duration > 60000 {
        return error(-1, "ATX duration must not exceed 60000 ms");
    }
    let lease = headers
        .get("x-nanokvm-input-lease")
        .and_then(|h| h.to_str().ok())
        .unwrap_or("");
    let Some(generation) = runtime.input.http_generation(lease) else {
        return error(-4, "another session holds input control");
    };
    let duration = Duration::from_millis(if req.duration == 0 { 800 } else { req.duration });
    let result = runtime.atx.pulse(device, duration, || {
        if cancel.cancelled()
            || runtime.stopping.load(Ordering::Acquire)
            || !principal.valid(runtime)
            || !runtime.input.allows_http_at(lease, generation)
        {
            Err("context canceled".into())
        } else {
            Ok(())
        }
    });
    match result {
        Ok(()) => ok(Value::Null),
        Err(cause) => error(-3, &format!("operation failed: {cause}")),
    }
}
pub(crate) fn get(runtime: &Runtime, cancel: &Cancellation) -> Response {
    match runtime.atx_leds.current(|| cancel.cancelled()) {
        Ok(status) => ok(serde_json::to_value(status).unwrap()),
        Err(cause) => error(-2, &format!("failed to read ATX LEDs: {cause}")),
    }
}
