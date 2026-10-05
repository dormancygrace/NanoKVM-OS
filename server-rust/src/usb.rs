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
    let manual = ManualSession::new(runtime.control.clone(), runtime.coordinator.clone());
    let reservation = manual.reserve(Kind::Relative, false, true, Duration::from_secs(2), |_| {
        true
    });
    let Ok(reservation) = reservation else {
        return error(-1, "HID control is busy");
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
        Err(_) => error(-1, "failed to reset hid"),
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
