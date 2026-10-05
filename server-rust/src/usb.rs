//! USB profile install/reset. Firmware commands are fixed and injected for
//! isolated fixtures; held/queued input is invalidated before gadget changes.
use crate::{
    api::{error, ok},
    fsroot,
    hid_reports::Kind,
    hid_settings,
    inputcontrol::ManualSession,
    store::atomic_write,
    systemops::Action,
    Error, Runtime,
};
use axum::response::Response;
use serde_json::Value;
use std::{fs, os::unix::fs::PermissionsExt, path::Path, time::Duration};
pub fn install_mode(root: &Path, mode: &str) -> Result<(), Error> {
    let source = match mode {
        "normal" => "/kvmapp/system/init.d/S03usbdev",
        "hid-only" => "/kvmapp/system/init.d/S03usbhid",
        _ => return Err("invalid HID mode".into()),
    };
    let source = fsroot::resolve(root, Path::new(source), false)?;
    let directory = fsroot::resolve(root, Path::new("/etc/init.d"), false)?;
    let candidate = directory.join("S03usbdev");
    // Go rejects a dangling compatibility link rather than creating its target.
    let missing = match fs::symlink_metadata(&candidate) {
        Ok(info) => !info.file_type().is_symlink(),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => true,
        Err(error) => return Err(error.into()),
    };
    let destination = fsroot::resolve(root, &candidate, missing)?;
    let bytes = fs::read(&source)?;
    let permissions = fs::metadata(source)?.permissions().mode() & 0o7777;
    atomic_write(&destination, &bytes, permissions)
}
pub(crate) fn set_mode(runtime: &Runtime, parameters: Result<Value, Error>) -> Response {
    let Ok(parameters) = parameters else {
        return error(-1, "invalid arguments");
    };
    let Some(mode) = parameters["mode"].as_str().filter(|mode| !mode.is_empty()) else {
        return error(-1, "invalid arguments");
    };
    if mode != "normal" && mode != "hid-only" {
        return error(-2, "invalid arguments");
    }
    if hid_settings::mode(&runtime.root).is_ok_and(|current| mode == current) {
        return ok(Value::Null);
    }
    let manual = ManualSession::new(runtime.control.clone(), runtime.coordinator.clone());
    let Ok(reservation) =
        manual.reserve(Kind::Relative, false, true, Duration::from_secs(2), |_| {
            true
        })
    else {
        return error(-3, "operation failed");
    };
    let result = runtime.input.reconfigure(|| {
        reservation
            .execute(|| {
                runtime
                    .hid
                    .reconfigure(false, || install_mode(&runtime.root, mode))
            })
            .map(|_| ())
    });
    if let Err(error) = manual.revoke(true, || runtime.hid.release_all()) {
        eprintln!("profile cleanup failed: {error}");
    }
    match result.and_then(|()| runtime.schedule_reboot()) {
        Ok(()) => ok(Value::Null),
        Err(_) => error(-3, "operation failed"),
    }
}
pub(crate) fn reset(runtime: &Runtime) -> Response {
    reset_response(runtime, false)
}
pub(crate) fn recover(runtime: &Runtime) -> Response {
    let Ok(_guard) = runtime.hid_settings.lock() else {
        return error(-1, "failed to recover usb");
    };
    reset_response(runtime, true)
}
fn reset_response(runtime: &Runtime, internal: bool) -> Response {
    let manual = ManualSession::new(runtime.control.clone(), runtime.coordinator.clone());
    let reservation = manual.reserve(Kind::Relative, false, true, Duration::from_secs(2), |_| {
        true
    });
    let Ok(reservation) = reservation else {
        return error(
            -1,
            if internal {
                "failed to recover usb"
            } else {
                "HID control is busy"
            },
        );
    };
    let result = runtime.input.reconfigure(|| {
        reservation
            .execute(|| {
                runtime.hid.reconfigure(true, || {
                    runtime
                        .commands
                        .run(Action::UsbPhyRestart, Duration::from_secs(10))
                })
            })
            .map(|_| ())
    });
    if let Err(error) = manual.revoke(true, || runtime.hid.release_all()) {
        eprintln!("reset cleanup failed: {error}");
    }
    match result {
        Ok(()) => ok(Value::Null),
        Err(_) => error(
            -1,
            if internal {
                "failed to recover usb"
            } else {
                "failed to reset hid"
            },
        ),
    }
}
pub(crate) fn composition_response(
    runtime: &Runtime,
    method: &axum::http::Method,
    parameters: Result<Value, Error>,
) -> Response {
    use crate::{composition::Composition, monitor};
    let Ok(_guard) = runtime.hid_settings.lock() else {
        return error(-3, "failed to apply USB composition");
    };
    let current = Composition::read(&runtime.root);
    if method == axum::http::Method::GET {
        return ok(current.response(monitor::pointer_supported(&runtime.root)));
    }
    let replacing = method == axum::http::Method::PUT;
    let invalid = if replacing {
        "invalid composition"
    } else {
        "invalid argument"
    };
    let Ok(v) = parameters else {
        return error(-1, invalid);
    };
    let candidate = if replacing {
        let Ok(mut candidate) = Composition::from_parameters(&v) else {
            return error(-1, invalid);
        };
        if v["pointerProfile"].as_str().unwrap_or("").is_empty() {
            candidate.windows_pointer = current.windows_pointer;
        }
        candidate
    } else {
        let Some(device) = v["device"].as_str().filter(|s| !s.is_empty()) else {
            return error(-1, invalid);
        };
        if !["network", "disk", "serial", "audio"].contains(&device) {
            return error(-2, "invalid arguments");
        }
        current.toggle(device)
    };
    if let Err(error) = candidate.validate() {
        return crate::api::error(-4, &error.to_string());
    }
    if replacing && v["revision"].as_str() != Some(current.revision().as_str()) {
        return error(-5, "USB composition changed; refresh and try again");
    }
    if let Err(cause) = apply_composition(runtime, current, candidate) {
        eprintln!("apply USB composition: {cause}");
        return error(
            -3,
            if replacing {
                "failed to apply USB composition"
            } else {
                "operation failed"
            },
        );
    }
    if replacing {
        ok(Composition::read(&runtime.root).response(monitor::pointer_supported(&runtime.root)))
    } else {
        ok(serde_json::json!({"on":candidate.enabled(v["device"].as_str().unwrap())}))
    }
}
fn apply_composition(
    runtime: &Runtime,
    current: crate::composition::Composition,
    candidate: crate::composition::Composition,
) -> Result<(), Error> {
    use crate::composition::{self, Transaction};
    use std::cell::Cell;
    candidate.validate()?;
    if current == candidate {
        return Ok(());
    }
    let manual = ManualSession::new(runtime.control.clone(), runtime.coordinator.clone());
    let reservation =
        manual.reserve(Kind::Relative, false, true, Duration::from_secs(2), |_| {
            true
        })?;
    let result = runtime.input.reconfigure(|| {
        reservation
            .execute(|| {
                runtime.monitor.backend.stop_audio()?;
                if candidate.serial && !current.serial {
                    composition::retire_getty(&runtime.root, runtime.commands.as_ref())?;
                }
                let pointer_changed = current.windows_pointer != candidate.windows_pointer;
                if pointer_changed {
                    if candidate.windows_pointer
                        && !crate::monitor::pointer_supported(&runtime.root)
                    {
                        return Err(
                            "Windows pointer is unavailable: update kernel and check EDID support"
                                .into(),
                        );
                    }
                    if let Err(cause) = runtime
                        .monitor
                        .apply_pointer(&runtime.root, candidate.windows_pointer)
                    {
                        return Err(join_restore(
                            cause,
                            runtime
                                .monitor
                                .apply_pointer(&runtime.root, current.windows_pointer),
                        ));
                    }
                }
                let committed = Cell::new(false);
                let transaction = Transaction {
                    root: &runtime.root,
                    install: &|mode| install_mode(&runtime.root, mode.name()),
                    run: &|action| runtime.commands.run(action, Duration::from_secs(20)),
                    verify: &|state| composition::verify(&runtime.root, state),
                };
                let result = runtime.hid.reconfigure(false, || {
                    transaction.apply(current, candidate)?;
                    committed.set(true);
                    Ok(())
                });
                if let Err(cause) = result {
                    // A later descriptor-open failure must not revert only the EDID
                    // while leaving a successfully committed gadget on its new profile.
                    if pointer_changed && !committed.get() {
                        return Err(join_restore(
                            cause,
                            runtime
                                .monitor
                                .apply_pointer(&runtime.root, current.windows_pointer),
                        ));
                    }
                    return Err(cause);
                }
                Ok(())
            })
            .map(|_| ())
    });
    if let Err(cause) = manual.revoke(true, || runtime.hid.release_all()) {
        eprintln!("USB composition input cleanup failed: {cause}");
    }
    result
}
fn join_restore(cause: Error, restore: Result<(), Error>) -> Error {
    match restore {
        Ok(()) => cause,
        Err(error) => format!("{cause}; restoring monitor association also failed: {error}").into(),
    }
}
/// Used by RustDesk startup after native media wiring. Preserve extra functions
/// and pointer association, and verify even an otherwise idempotent request.
pub fn ensure_remote_access(runtime: &Runtime, audio: bool) -> Result<(), Error> {
    let _guard = runtime
        .hid_settings
        .lock()
        .map_err(|_| "USB settings unavailable")?;
    let current = crate::composition::Composition::read(&runtime.root);
    let candidate = current.remote_access_defaults(audio);
    candidate.validate()?;
    if current == candidate {
        crate::composition::verify(&runtime.root, candidate)
    } else {
        apply_composition(runtime, current, candidate)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;
    #[test]
    fn installing_profile_preserves_alpine_absolute_link_and_source_permissions() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        for path in [
            "kvmapp/system/init.d",
            "etc/init.d",
            "usr/libexec/nanokvm/legacy",
        ] {
            fs::create_dir_all(root.join(path)).unwrap();
        }
        let source = root.join("kvmapp/system/init.d/S03usbhid");
        fs::write(&source, "new HID script").unwrap();
        fs::set_permissions(&source, fs::Permissions::from_mode(0o751)).unwrap();
        let destination = root.join("usr/libexec/nanokvm/legacy/S03usbdev");
        fs::write(&destination, "old").unwrap();
        let link = root.join("etc/init.d/S03usbdev");
        symlink("/usr/libexec/nanokvm/legacy/S03usbdev", &link).unwrap();
        install_mode(root, "hid-only").unwrap();
        assert_eq!(fs::read(&destination).unwrap(), b"new HID script");
        assert_eq!(
            fs::read_link(&link).unwrap(),
            Path::new("/usr/libexec/nanokvm/legacy/S03usbdev")
        );
        assert_eq!(
            fs::metadata(&destination).unwrap().permissions().mode() & 0o777,
            0o751
        );
        fs::remove_file(&destination).unwrap();
        assert!(install_mode(root, "hid-only").is_err());
        assert!(!destination.exists());
        assert!(install_mode(root, "invalid").is_err());
    }
}
