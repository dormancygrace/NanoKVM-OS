//! Bounded HID paste with native layout tables and cancellable manual priority.
use crate::{
    api::{error, ok},
    controlmode::Mode,
    hid_reports::{parse, Frame, Kind, Report},
    inputcontrol::ManualSession,
    paste_layout,
    request_cancel::Cancellation,
    sessions::Principal,
    Error, Runtime,
};
use axum::response::Response;
use serde_json::Value;
use std::time::{Duration, Instant};
const DELAY: Duration = Duration::from_millis(30);
const LIMIT: Duration = Duration::from_secs(25);
// Go's 25 seconds is a key-count budget. Allow IO/scheduler overhead at
// the valid 833-key boundary, while still draining below the 30s mode limit.
const DRAIN_LIMIT: Duration = Duration::from_secs(28);
const MAX_RUNES: usize = (LIMIT.as_millis() / DELAY.as_millis()) as usize;
fn report(modifier: u8, code: u8) -> Report {
    match parse(&[1, modifier, 0, code, 0, 0, 0, 0, 0]).unwrap() {
        Frame::Report(report) => report,
        _ => unreachable!(),
    }
}
struct Plan {
    keys: Vec<[u8; 2]>,
}
impl Plan {
    fn new(content: &str, language: &str) -> Result<Self, (&'static str, i32)> {
        if content.len() > MAX_RUNES * 4 || content.chars().count() > MAX_RUNES {
            return Err(("content too long", -2));
        }
        let layout = paste_layout::layout(language);
        let mut keys = Vec::new();
        for c in content.chars() {
            if let Ok(index) = layout.binary_search_by_key(&c, |row| row.0) {
                let (_, key, follow) = layout[index];
                keys.push(key);
                if let Some(key) = follow {
                    keys.push(key);
                }
            }
        }
        if keys.len() > MAX_RUNES {
            return Err(("paste duration exceeds 25s", -2));
        }
        Ok(Self { keys })
    }
}
pub(crate) fn handle(
    runtime: &Runtime,
    parameters: Result<Value, Error>,
    principal: Principal,
    lease: &str,
    cancel: &Cancellation,
) -> Response {
    let Ok(v) = parameters else {
        return error(-1, "invalid arguments");
    };
    let Some(content) = v["content"].as_str().filter(|s| !s.is_empty()) else {
        return error(-1, "invalid arguments");
    };
    let plan = match Plan::new(content, v["langue"].as_str().unwrap_or("")) {
        Ok(plan) => plan,
        Err((message, code)) => return error(code, message),
    };
    // Concurrent pastes otherwise interleave characters on the same keyboard.
    let Ok(_paste) = runtime.paste.try_lock() else {
        return error(-3, "HID control is busy");
    };
    let Some(generation) = runtime.input.http_generation(lease) else {
        return error(-3, "HID paste failed");
    };
    if cancel.cancelled() || !principal.valid(runtime) {
        return error(-3, "HID paste failed");
    }
    let manual = ManualSession::new(runtime.control.clone(), runtime.coordinator.clone());
    let allow = |mode| mode != Mode::Picoclaw || runtime.pico_lock.owner().is_empty();
    let outer = manual.reserve(Kind::Keyboard, false, true, Duration::from_secs(2), allow);
    let Ok(outer) = outer else {
        return error(-3, "HID control is busy");
    };
    let deadline = Instant::now() + DRAIN_LIMIT;
    let valid = || -> Result<(), Error> {
        if cancel.cancelled()
            || !principal.valid(runtime)
            || !runtime.input.allows_http_at(lease, generation)
            || Instant::now() >= deadline
        {
            return Err("paste cancelled".into());
        }
        let status = runtime.control.status()?;
        if status.transitioning || !allow(status.mode) {
            return Err("manual input is blocked".into());
        }
        Ok(())
    };
    let result = (|| -> Result<(), Error> {
        valid()?;
        for [modifier, code] in plan.keys {
            valid()?;
            let reservation =
                manual.reserve(Kind::Keyboard, false, true, Duration::from_secs(2), allow)?;
            let result = runtime.input.synchronized(|| {
                valid()?;
                if !reservation.execute(|| {
                    // Ownership cannot transfer between this press and release.
                    // Finish physical cleanup inside the input transition lock.
                    let down = runtime.hid.write(&report(modifier, code));
                    let up = runtime.hid.write(&report(0, 0));
                    match (down, up) {
                        (Ok(()), Ok(())) => Ok(()),
                        (Err(error), _) | (_, Err(error)) => {
                            let _ = runtime.hid.release_all();
                            Err(error)
                        }
                    }
                })? {
                    return Err("manual paste was revoked".into());
                }
                Ok(())
            });
            result?;
            runtime.jiggler.update();
            cancel.wait(DELAY.min(deadline.saturating_duration_since(Instant::now())))?;
        }
        valid()
    })();
    // Cleanup is authorized independently of account expiry, but a transferred
    // lease must never clear the new owner's keys. Each stroke above is neutral.
    let final_release = runtime.input.synchronized(|| {
        outer
            .execute(|| {
                if runtime.input.allows_http_at(lease, generation) {
                    runtime.hid.write(&report(0, 0))
                } else {
                    Ok(())
                }
            })
            .map(|_| ())
    });
    if let Err(cause) = manual.revoke(true, || Ok(())) {
        eprintln!("paste session cleanup failed: {cause}");
    }
    let result = match (result, final_release) {
        (Ok(()), result) | (result, Ok(())) => result,
        (Err(cause), Err(cleanup)) => {
            Err(format!("{cause}; final key-up failed: {cleanup}").into())
        }
    };
    match result {
        Ok(()) => ok(Value::Null),
        Err(cause) => {
            eprintln!("HID paste failed: {cause}");
            error(-3, "HID paste failed")
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn layouts_skip_unknown_runes_and_bound_dead_key_duration() {
        assert_eq!(
            Plan::new("aA\n😀", "").unwrap().keys,
            [[0, 4], [2, 4], [0, 40]]
        );
        assert_eq!(
            Plan::new("áÜ^", "es").unwrap().keys,
            [[0, 52], [0, 4], [2, 52], [2, 24], [2, 47], [0, 44]]
        );
        assert!(Plan::new(&"á".repeat(417), "es").is_err());
        assert!(Plan::new(&"😀".repeat(834), "").is_err());
        assert_eq!(
            Plan::new("text", "unknown").unwrap().keys,
            Plan::new("text", "").unwrap().keys
        );
        assert!(DELAY * MAX_RUNES as u32 <= LIMIT);
    }
}
