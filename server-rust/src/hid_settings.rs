//! Persisted HID settings and read-only composition inspection.
//! Gadget rebinding is separate stage 3 work.
use crate::{
    api::{error, ok, pending},
    store::atomic_write,
    Error, Runtime,
};
use axum::{http::Method, response::Response};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{fs, io::ErrorKind, path::Path};

#[derive(Serialize, Deserialize)]
struct Key {
    #[serde(default, deserialize_with = "null_string")]
    code: String,
    #[serde(default, deserialize_with = "null_string")]
    label: String,
}
fn null_string<'de, D: serde::Deserializer<'de>>(d: D) -> Result<String, D::Error> {
    Ok(Option::<String>::deserialize(d)?.unwrap_or_default())
}
#[derive(Serialize, Deserialize)]
struct Shortcut {
    #[serde(default, deserialize_with = "null_string")]
    id: String,
    #[serde(default)]
    keys: Option<Vec<Key>>,
}
#[derive(Default, Serialize, Deserialize)]
struct Shortcuts {
    #[serde(default)]
    shortcuts: Option<Vec<Shortcut>>,
}
fn load(path: &Path) -> Result<Shortcuts, Error> {
    match fs::read(path) {
        Ok(data) => Ok(serde_json::from_slice::<Option<Shortcuts>>(&data)
            .map(Option::unwrap_or_default)
            .unwrap_or(Shortcuts {
                shortcuts: Some(vec![]),
            })),
        Err(e) if e.kind() == ErrorKind::NotFound => Ok(Shortcuts {
            shortcuts: Some(vec![]),
        }),
        Err(e) => Err(e.into()),
    }
}
fn new_id() -> Result<String, Error> {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes)?;
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    let h = bytes.iter().map(|b| format!("{b:02x}")).collect::<String>();
    Ok(format!(
        "{}-{}-{}-{}-{}",
        &h[..8],
        &h[8..12],
        &h[12..16],
        &h[16..20],
        &h[20..]
    ))
}
fn mode(root: &Path) -> Result<&'static str, Error> {
    let gadget = root.join("sys/kernel/config/usb_gadget/g0");
    let flag = fs::read_to_string(gadget.join("bcdDevice"))?;
    match flag.trim() {
        "0x0510" | "0x0511" | "0x0710" | "0x0711" => Ok("normal"),
        "0x0623" | "0x0624" | "0x0720" | "0x0721" => {
            for entry in fs::read_dir(gadget.join("configs/c.1"))? {
                let entry = entry?;
                if !entry.file_type()?.is_symlink() {
                    continue;
                }
                let target = fs::read_link(entry.path())?;
                let name = target
                    .file_name()
                    .and_then(|s| s.to_str())
                    .ok_or("invalid USB function")?;
                if ["rndis.", "ncm.", "mass_storage.", "uac1."]
                    .iter()
                    .any(|p| name.starts_with(p))
                {
                    return Ok("normal");
                }
            }
            Ok("hid-only")
        }
        _ => Err("invalid HID mode flag".into()),
    }
}
pub fn handle(
    s: &Runtime,
    method: &Method,
    path: &str,
    parameters: Result<Value, Error>,
) -> Response {
    let _guard = match s.hid_settings.lock() {
        Ok(guard) => guard,
        Err(_) => {
            return (
                axum::http::StatusCode::INTERNAL_SERVER_ERROR,
                "HID settings unavailable",
            )
                .into_response()
        }
    };
    use axum::response::IntoResponse;
    let shortcuts_path = s.root.join("etc/kvm/shortcuts.json");
    let leader = s.root.join("etc/kvm/leader-key");
    match (method.as_str(), path) {
        ("GET", "/api/hid/leds") => {
            let leds = s.hid.leds();
            leds.refresh();
            ok(serde_json::to_value(leds.snapshot(true)).unwrap())
        }
        ("GET", "/api/hid/mode") => match mode(&s.root) {
            Ok(value) => ok(json!({"mode":value})),
            Err(_) => error(-1, "get HID mode failed"),
        },
        ("GET", "/api/hid/input-status") => {
            let boot = s.root.join("boot");
            let all = boot.join("disable_hid").exists();
            let keyboard = !(all || boot.join("usb.disable_keyboard").exists());
            let relative = !(all || boot.join("usb.disable_relative").exists());
            let absolute = !(all || boot.join("usb.disable_absolute").exists());
            ok(
                json!({"available":keyboard || relative || absolute, "keyboard":keyboard, "relative":relative,
                "absolute":absolute, "pointerProfile":if boot.join("usb.pointer_windows").exists(){"windows"}else{"default"}}),
            )
        }
        ("GET", "/api/hid/shortcuts") => match load(&shortcuts_path) {
            Ok(shortcuts) => ok(json!({"shortcuts":shortcuts.shortcuts})),
            Err(_) => error(-1, "get shortcuts failed"),
        },
        ("POST", "/api/hid/shortcut") => {
            let keys = parameters.and_then(|v| {
                if !v["keys"].is_array() {
                    return Err("invalid arguments".into());
                }
                serde_json::from_value::<Vec<Key>>(v["keys"].clone()).map_err(Into::into)
            });
            let Ok(keys) = keys else {
                return error(-1, "invalid arguments");
            };
            let add = || -> Result<(), Error> {
                let mut shortcuts = load(&shortcuts_path)?;
                shortcuts
                    .shortcuts
                    .get_or_insert_with(Vec::new)
                    .push(Shortcut {
                        id: new_id()?,
                        keys: Some(keys),
                    });
                atomic_write(&shortcuts_path, &serde_json::to_vec(&shortcuts)?, 0o644)
            };
            match add() {
                Ok(()) => ok(Value::Null),
                Err(_) => error(-2, "add shortcut failed"),
            }
        }
        ("DELETE", "/api/hid/shortcut") => {
            let Ok(parameters) = parameters else {
                return error(-1, "invalid arguments");
            };
            let Some(id) = parameters["id"].as_str().filter(|id| !id.is_empty()) else {
                return error(-1, "invalid arguments");
            };
            let delete = || -> Result<(), Error> {
                let mut shortcuts = load(&shortcuts_path)?;
                let entries = shortcuts.shortcuts.get_or_insert_with(Vec::new);
                let before = entries.len();
                entries.retain(|item| item.id != id);
                if entries.len() == before {
                    return Err("shortcut not found".into());
                }
                atomic_write(&shortcuts_path, &serde_json::to_vec(&shortcuts)?, 0o644)
            };
            match delete() {
                Ok(()) => ok(Value::Null),
                Err(_) => error(-2, "delete shortcut failed"),
            }
        }
        ("GET", "/api/hid/shortcut/leader-key") => match fs::read_to_string(&leader) {
            Ok(key) => ok(json!({"key":key.replace('\n', "")})),
            Err(e) if e.kind() == ErrorKind::NotFound => ok(json!({"key":""})),
            Err(_) => error(-1, "read leader key failed"),
        },
        ("POST", "/api/hid/shortcut/leader-key") => {
            let Ok(parameters) = parameters else {
                return error(-1, "invalid arguments");
            };
            let key = match parameters.get("key") {
                None | Some(Value::Null) => "",
                Some(Value::String(key)) => key,
                _ => return error(-1, "invalid arguments"),
            };
            if key.is_empty() {
                if let Err(e) = fs::remove_file(leader) {
                    if e.kind() != ErrorKind::NotFound {
                        return error(-2, "reset failed");
                    }
                }
            } else if atomic_write(&leader, key.as_bytes(), 0o644).is_err() {
                return error(-3, "write failed");
            }
            ok(Value::Null)
        }
        _ => pending(),
    }
}
