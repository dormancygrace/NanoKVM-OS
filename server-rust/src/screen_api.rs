//! Screen HTTP contract; execution is on the bounded API blocking pool.
use crate::{
    api::{error, ok},
    monitor,
    request_cancel::Cancellation,
    screen::{read_video_value, supports_qhd},
    screen_store, Error, Runtime,
};
use axum::response::Response;
use serde_json::{json, Value};
use std::sync::{MutexGuard, TryLockError};

fn lock<'a>(s: &'a Runtime, cancelled: &Cancellation) -> Result<MutexGuard<'a, ()>, Error> {
    loop {
        if cancelled.cancelled() || s.stopping.load(std::sync::atomic::Ordering::Acquire) {
            return Err("screen operation cancelled".into());
        }
        match s.screen.updates.try_lock() {
            Ok(guard) => return Ok(guard),
            Err(TryLockError::Poisoned(_)) => return Err("screen state unavailable".into()),
            Err(TryLockError::WouldBlock) => {
                cancelled.wait(std::time::Duration::from_millis(10))?
            }
        }
    }
}
pub(crate) fn get(s: &Runtime, cancelled: &Cancellation) -> Response {
    let result = (|| -> Result<Value, Error> {
        let _guard = lock(s, cancelled)?;
        let current = s.screen.snapshot()?;
        let active = s.monitor.backend.video_status()?;
        let profile = s.monitor.status_cancellable(&s.root, &|| {
            cancelled.cancelled() || s.stopping.load(std::sync::atomic::Ordering::Acquire)
        })?;
        let mut data = json!({
            "width":current.width,"height":current.height,"fps":current.fps,
            "quality":current.quality,"bitRate":current.bit_rate,"gop":current.gop,
            "mjpegChroma":current.mjpeg_chroma,"mjpegChromaActive":active.mjpeg_chroma,"mjpegChromaFallback":active.chroma_fallback,
            "gopMode":current.gop_mode,"gopModeActive":active.gop_mode,"gopModeRestartRequired":current.gop_mode != active.gop_mode,
            "monitor":read_video_value(&s.root,"/etc/kvm/monitor_resolution"),
            "monitorRequiresPowerCycle":profile.requires_power_cycle,"monitorPowerCyclePending":profile.power_cycle_pending,
            "monitorHighRefreshSupported":profile.high_refresh_supported,"monitorSupported":profile.supported,"qhdSupported":supports_qhd(&s.root),
            "portrait":profile.portrait,"portraitSupported":profile.portrait_supported,"portraitResolution":profile.portrait_resolution,"portraitMaxSupported":profile.portrait_max_supported,
            "effectiveFps":s.screen.capture_screen()?.fps
        });
        for (key, file) in [
            ("inputWidth", "width"),
            ("inputHeight", "height"),
            ("outputWidth", "stream_width"),
            ("outputHeight", "stream_height"),
            ("mjpegOutputWidth", "mjpeg_width"),
            ("mjpegOutputHeight", "mjpeg_height"),
            ("videoOutputWidth", "video_width"),
            ("videoOutputHeight", "video_height"),
            ("measuredFps", "now_fps"),
        ] {
            data[key] = read_video_value(&s.root, &format!("/run/nanokvm/{file}")).into();
        }
        Ok(data)
    })();
    match result {
        Ok(data) => ok(data),
        Err(_) => error(-2, "read screen failed"),
    }
}
pub(crate) fn set(s: &Runtime, parsed: Result<Value, Error>, cancelled: &Cancellation) -> Response {
    let Ok(req) = parsed else {
        return error(-1, "invalid arguments");
    };
    let Some(key) = req["type"].as_str().filter(|v| !v.is_empty()) else {
        return error(-1, "invalid arguments");
    };
    let mut value = req["value"].as_i64().unwrap_or(0);
    let confirm = req["confirmPowerCycle"].as_bool().unwrap_or(false);
    let Ok(_guard) = lock(s, cancelled) else {
        return error(-2, "update screen failed");
    };
    match key {
        "mjpeg_chroma" => {
            if value != 420 && value != 422 {
                return error(-1, "MJPEG chroma must be 420 or 422");
            }
            let Ok(previous) = s.screen.snapshot() else {
                return error(-2, "update screen failed");
            };
            if s.monitor.backend.set_mjpeg_chroma(value as u16).is_err() {
                return error(-4, "cannot apply MJPEG chroma");
            }
            if save(s, key, &value.to_string()).is_err() {
                if let Err(e) = s.monitor.backend.set_mjpeg_chroma(previous.mjpeg_chroma) {
                    eprintln!("restore MJPEG chroma failed: {e}");
                }
                return error(-2, "update screen failed");
            }
            if s.screen.set(key, value).is_err() {
                return error(-2, "update screen failed");
            }
            let Ok(active) = s.monitor.backend.video_status() else {
                return error(-2, "read screen failed");
            };
            return ok(
                json!({"mjpegChroma":value,"mjpegChromaActive":active.mjpeg_chroma,"mjpegChromaFallback":active.chroma_fallback}),
            );
        }
        "monitor_power_cycle_ack" => {
            if !confirm {
                return error(-1, "power cycle confirmation required");
            }
            return profile_response(
                s.monitor.acknowledge_power_cycle_cancellable(&s.root, &|| {
                    cancelled.cancelled() || s.stopping.load(std::sync::atomic::Ordering::Acquire)
                }),
            );
        }
        "portrait" => {
            if value != 0 && value != 1 {
                return error(-1, "portrait must be 0 or 1");
            }
            return profile_response(s.monitor.apply_portrait_cancellable(
                &s.root,
                value == 1,
                &|| cancelled.cancelled() || s.stopping.load(std::sync::atomic::Ordering::Acquire),
            ));
        }
        "portrait_resolution" => {
            if ![1280, 1920, 2304, 2560].contains(&value) {
                return error(-1, "unsupported portrait monitor resolution");
            }
            let Ok(status) = s.monitor.status_cancellable(&s.root, &|| {
                cancelled.cancelled() || s.stopping.load(std::sync::atomic::Ordering::Acquire)
            }) else {
                return error(-4, "monitor state unavailable");
            };
            if value == 2560 && !status.portrait_max_supported {
                return error(-3, "maximum portrait monitor profile is unavailable");
            }
            if value != 2560 && !status.portrait_supported {
                return error(-3, "portrait monitor profile is unavailable");
            }
            return profile_response(s.monitor.apply_portrait_resolution_cancellable(
                &s.root,
                value as u16,
                &|| cancelled.cancelled() || s.stopping.load(std::sync::atomic::Ordering::Acquire),
            ));
        }
        "monitor" => {
            if monitor::requires_power_cycle(&s.root) && !confirm {
                return error(
                    -5,
                    "Physical power cycle required after EDID programming; confirm before writing",
                );
            }
            if ![0, 720, 1080, 1440].contains(&value) {
                return error(-1, "unsupported monitor profile");
            }
            if value == 1440 && !supports_qhd(&s.root) {
                return error(-3, "QHD requires at least 62 MiB of ION memory");
            }
            return profile_response(s.monitor.apply_resolution_cancellable(
                &s.root,
                value as u16,
                &|| cancelled.cancelled() || s.stopping.load(std::sync::atomic::Ordering::Acquire),
            ));
        }
        "resolution" if ![0, 600, 720, 1080, 1440].contains(&value) => {
            return error(-1, "unsupported stream limit")
        }
        "fps" if !(10..=120).contains(&value) => {
            return error(-1, "FPS must be between 10 and 120")
        }
        "quality" if !(1..=20000).contains(&value) => {
            return error(-1, "quality must be 1-100, or a bitrate up to 20000 kbit/s")
        }
        "gop_mode" if value != 0 && value != 1 => {
            return error(-1, "GOP mode must be NormalP or SmartP")
        }
        "type" => {
            let codec = match value {
                0 => "mjpeg",
                1 => "h264",
                2 => "h265",
                _ => return error(-1, "stream type must be MJPEG, H.264, or H.265"),
            };
            if save(s, key, codec).is_err() {
                return error(-2, "update screen failed");
            }
            return ok(Value::Null);
        }
        "gop" => {
            if !(1..=100).contains(&value) {
                value = 30;
            }
            if s.monitor.backend.set_gop(value as u8).is_err() {
                return error(-2, "update screen failed");
            }
        }
        "resolution" | "fps" | "quality" | "gop_mode" => {
            if save(s, key, &value.to_string()).is_err() {
                return error(-2, "update screen failed");
            }
        }
        _ => return error(-1, "unknown screen setting"),
    }
    if s.screen.set(key, value).is_err() {
        return error(-2, "update screen failed");
    }
    if key == "fps" {
        return ok(json!({"fps":value}));
    }
    if key == "gop_mode" {
        let Ok(active) = s.monitor.backend.video_status() else {
            return error(-2, "read screen failed");
        };
        return ok(
            json!({"gopMode":value,"gopModeActive":active.gop_mode,"gopModeRestartRequired":value != i64::from(active.gop_mode)}),
        );
    }
    ok(Value::Null)
}
fn profile_response(result: Result<(), Error>) -> Response {
    match result {
        Ok(()) => ok(Value::Null),
        Err(e) => error(-4, &e.to_string()),
    }
}
fn save(s: &Runtime, key: &str, value: &str) -> Result<(), Error> {
    let name = match key {
        "quality" => "qlty",
        "resolution" => "res",
        "fps" | "type" | "gop_mode" | "mjpeg_chroma" => key,
        _ => return Err("invalid screen setting".into()),
    };
    screen_store::write(
        &s.root,
        &format!("/kvmapp/kvm/{name}"),
        value.as_bytes(),
        0o666,
        true,
    )
}
