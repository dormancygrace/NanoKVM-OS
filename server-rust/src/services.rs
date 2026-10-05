//! Fixed service actions and persistent mDNS/SSH policy.
use crate::{
    api::{error, ok},
    crypto, fsroot, request_cancel, store,
    systemops::Action,
    Error, Runtime,
};
use axum::response::Response;
use serde_json::{json, Value};
use std::{fs, path::Path, time::Duration};
fn exists(s: &Runtime, path: &str) -> bool {
    fsroot::resolve(&s.root, Path::new(path), false).is_ok()
}
fn parent(s: &Runtime, path: &str) -> Result<std::path::PathBuf, Error> {
    let path = Path::new(path);
    Ok(
        fsroot::resolve(&s.root, path.parent().ok_or("missing parent")?, false)?
            .join(path.file_name().ok_or("missing name")?),
    )
}
fn remove(s: &Runtime, path: &str) -> Result<(), Error> {
    match fs::remove_file(parent(s, path)?) {
        Ok(()) => Ok(()),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(e.into()),
    }
}
pub(crate) fn mdns(
    s: &Runtime,
    enabled: bool,
    cancelled: &request_cancel::Cancellation,
) -> Response {
    let _settings = match settings(s, cancelled) {
        Ok(lock) => lock,
        Err(_) => return error(-1, "failed to change mDNS state"),
    };
    if cancelled.cancelled() {
        return error(-1, "failed to change mDNS state");
    }
    let alpine = exists(s, "/etc/alpine-release");
    let action = if alpine {
        if enabled {
            Action::AlpineMdnsStart
        } else {
            Action::AlpineMdnsStop
        }
    } else if enabled {
        Action::LegacyMdnsStart
    } else {
        Action::LegacyMdnsStop
    };
    let message = if alpine {
        "failed to change mDNS state"
    } else if enabled {
        "failed to enable mdns"
    } else {
        "failed to disable mdns"
    };
    if !alpine && s.info.mdns_enabled() == enabled {
        return ok(Value::Null);
    }
    let operation = (|| -> Result<(), Error> {
        s.commands.check(action)?;
        if alpine {
            if enabled {
                remove(s, "/etc/kvm/mdns_disabled")?;
            } else {
                store::atomic_write(&parent(s, "/etc/kvm/mdns_disabled")?, b"1\n", 0o600)?;
            }
        } else if enabled {
            let source = fsroot::resolve(
                &s.root,
                Path::new("/kvmapp/system/init.d/S50avahi-daemon"),
                false,
            )?;
            let bytes = fs::read(&source)?;
            let permissions = fs::metadata(source)?.permissions();
            let destination = parent(s, "/etc/init.d/S50avahi-daemon")?;
            use std::os::unix::fs::PermissionsExt;
            store::atomic_write(&destination, &bytes, permissions.mode() & 0o777)?;
        }
        s.commands
            .run_cancel(action, Duration::from_secs(15), &|| cancelled.cancelled())?;
        if !alpine && !enabled {
            let _ = remove(s, "/run/avahi-daemon/pid");
            let _ = remove(s, "/etc/init.d/S50avahi-daemon");
        }
        Ok(())
    })();
    match operation {
        Ok(()) => ok(Value::Null),
        Err(_) => error(-1, message),
    }
}
pub(crate) fn ssh_get(s: &Runtime) -> Response {
    let enabled = match fsroot::resolve(&s.root, Path::new("/etc/kvm/ssh_stop"), false) {
        Ok(_) => false,
        Err(e) => e
            .downcast_ref::<std::io::Error>()
            .is_some_and(|e| e.kind() == std::io::ErrorKind::NotFound),
    };
    ok(json!({"enabled":enabled}))
}
pub(crate) fn ssh_set(
    s: &Runtime,
    enabled: bool,
    value: Result<Value, Error>,
    cancelled: &request_cancel::Cancellation,
) -> Response {
    let _settings = match settings(s, cancelled) {
        Ok(lock) => lock,
        Err(_) => return error(-1, "operation failed"),
    };
    if !enabled {
        return match s
            .commands
            .run_cancel(Action::SshDisable, Duration::from_secs(15), &|| {
                cancelled.cancelled()
            }) {
            Ok(()) => ok(Value::Null),
            Err(_) => error(-1, "operation failed"),
        };
    }
    let Ok(value) = value else {
        return error(-1, "a new root password is required");
    };
    let Some(encrypted) = value["password"].as_str().filter(|p| !p.is_empty()) else {
        return error(-1, "a new root password is required");
    };
    let password = match crypto::decrypt(encrypted) {
        Ok(p) if !p.trim().eq_ignore_ascii_case("root") && !p.contains(['\r', '\n', '\0']) => p,
        _ => return error(-1, "invalid root password"),
    };
    if let Err(e) = store::valid_password(&password) {
        return error(-1, &e.to_string());
    }

    if s.commands
        .password(&password, Duration::from_secs(10), &|| {
            cancelled.cancelled()
        })
        .is_err()
    {
        return error(-1, "could not set root password");
    }
    match s
        .commands
        .run_cancel(Action::SshEnable, Duration::from_secs(15), &|| {
            cancelled.cancelled()
        }) {
        Ok(()) => ok(Value::Null),
        Err(_) => error(-1, "operation failed"),
    }
}

pub(crate) fn settings<'a>(
    s: &'a Runtime,
    cancelled: &request_cancel::Cancellation,
) -> Result<std::sync::MutexGuard<'a, ()>, Error> {
    loop {
        if cancelled.cancelled() || s.stopping.load(std::sync::atomic::Ordering::Acquire) {
            return Err("request cancelled".into());
        }
        match s.system_settings.try_lock() {
            Ok(lock) => return Ok(lock),
            Err(std::sync::TryLockError::Poisoned(_)) => {
                return Err("system settings unavailable".into())
            }
            Err(std::sync::TryLockError::WouldBlock) => {
                std::thread::sleep(Duration::from_millis(10))
            }
        }
    }
}
