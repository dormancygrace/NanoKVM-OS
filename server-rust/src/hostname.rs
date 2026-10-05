//! Hostname validation and exact /etc/hosts token replacement.
use crate::{
    api::{error, ok},
    fsroot,
    store::atomic_write,
    systemops::Action,
    Error, Runtime,
};
use axum::response::Response;
use serde_json::{json, Value};
use std::{fs, path::Path, time::Duration};
fn validate(name: &str) -> Result<(), Error> {
    if name.is_empty() || name.len() > 253 {
        return Err("hostname must be 1-253 characters".into());
    }
    if name.split('.').any(|label| {
        label.is_empty()
            || label.len() > 63
            || !label.as_bytes()[0].is_ascii_alphanumeric()
            || !label.as_bytes()[label.len() - 1].is_ascii_alphanumeric()
            || label
                .bytes()
                .any(|byte| !byte.is_ascii_alphanumeric() && byte != b'-')
    }) {
        return Err(
            "hostname labels may contain only letters, digits and inner hyphens (1-63 characters)"
                .into(),
        );
    }
    Ok(())
}
// Go strings.Fields preserves invalid UTF-8 bytes while recognizing Unicode
// whitespace. Keep byte ranges rather than rewriting the whole text as UTF-8.
fn fields(bytes: &[u8]) -> Vec<std::ops::Range<usize>> {
    let mut ranges = Vec::new();
    let mut start = None;
    let mut at = 0;
    for chunk in bytes.utf8_chunks() {
        for c in chunk.valid().chars() {
            if c.is_whitespace() {
                if let Some(start) = start.take() {
                    ranges.push(start..at);
                }
            } else if start.is_none() {
                start = Some(at);
            }
            at += c.len_utf8();
        }
        if !chunk.invalid().is_empty() {
            if start.is_none() {
                start = Some(at);
            }
            at += chunk.invalid().len();
        }
    }
    if let Some(start) = start {
        ranges.push(start..at);
    }
    ranges
}
fn trim(bytes: &[u8]) -> &[u8] {
    let ranges = fields(bytes);
    match (ranges.first(), ranges.last()) {
        (Some(first), Some(last)) => &bytes[first.start..last.end],
        _ => &[],
    }
}
fn replace_hosts(hosts: &[u8], old: &[u8], new: &[u8]) -> Vec<u8> {
    if old.is_empty() {
        return hosts.into();
    }
    let mut output = Vec::with_capacity(hosts.len());
    for (index, line) in hosts.split(|b| *b == b'\n').enumerate() {
        if index > 0 {
            output.push(b'\n');
        }
        let (body, comment) = match line.iter().position(|b| *b == b'#') {
            Some(at) => (&line[..at], Some(&line[at + 1..])),
            None => (line, None),
        };
        let ranges = fields(body);
        if ranges.len() < 2
            || !ranges
                .iter()
                .skip(1)
                .any(|range| &body[range.clone()] == old)
        {
            output.extend_from_slice(line);
            continue;
        }
        for (index, range) in ranges.into_iter().enumerate() {
            if index > 0 {
                output.push(b'\t');
            }
            let field = &body[range];
            output.extend_from_slice(if index > 0 && field == old {
                new
            } else {
                field
            });
        }
        if let Some(comment) = comment {
            output.extend_from_slice(b"\t#");
            output.extend_from_slice(comment);
        }
    }
    output
}
pub(crate) fn get(runtime: &Runtime) -> Response {
    let data = fsroot::resolve(&runtime.root, Path::new("/etc/hostname"), false)
        .and_then(|path| fs::read(path).map_err(Into::into));
    match data {
        Ok(data) => ok(json!({"hostname":crate::json_text::text(&data).replace('\n',"")})),
        Err(_) => error(-1, "read Hostname failed"),
    }
}
pub(crate) fn set(runtime: &Runtime, parameters: Result<Value, Error>) -> Response {
    let Ok(v) = parameters else {
        return error(-1, "invalid arguments");
    };
    let Some(name) = v["hostname"].as_str().filter(|name| !name.is_empty()) else {
        return error(-1, "invalid arguments");
    };
    if let Err(cause) = validate(name) {
        return error(-1, &cause.to_string());
    }
    let Ok(_settings) = runtime.system_settings.lock() else {
        return error(-2, "failed to write data");
    };
    let etc = match fsroot::resolve(&runtime.root, Path::new("/etc/hostname"), false) {
        Ok(path) => path,
        Err(_) => return error(-1, "read Hostname failed"),
    };
    let old = match fs::read(&etc) {
        Ok(data) => trim(&data).to_vec(),
        Err(_) => return error(-1, "read Hostname failed"),
    };
    if old != name.as_bytes() {
        let hosts = match fsroot::resolve(&runtime.root, Path::new("/etc/hosts"), false) {
            Ok(path) => path,
            Err(_) => return error(-1, "read Hosts failed"),
        };
        let contents = match fs::read(&hosts) {
            Ok(data) => data,
            Err(_) => return error(-1, "read Hosts failed"),
        };
        if atomic_write(
            &hosts,
            &replace_hosts(&contents, &old, name.as_bytes()),
            0o644,
        )
        .is_err()
        {
            return error(-2, "failed to write data");
        }
    }
    let boot = match fsroot::resolve(&runtime.root, Path::new("/boot/hostname"), true) {
        Ok(path) => path,
        Err(_) => return error(-2, "failed to write data"),
    };
    if atomic_write(&boot, name.as_bytes(), 0o644).is_err() {
        return error(-2, "failed to write data");
    }
    if atomic_write(&etc, name.as_bytes(), 0o644).is_err() {
        return error(-3, "failed to write data");
    }
    // Persistent success is unchanged by a runtime hostname-command failure.
    if let Err(cause) = runtime
        .commands
        .run(Action::ApplyHostname, Duration::from_secs(5))
    {
        eprintln!("apply hostname failed: {cause}");
    }
    ok(Value::Null)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn exact_baseline_hostname_validation_and_hosts_replacement() {
        let oracle: Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/hostname-go-oracle.json"
        ))
        .unwrap();
        for case in oracle["names"].as_array().unwrap() {
            assert_eq!(
                validate(case["name"].as_str().unwrap())
                    .err()
                    .map(|e| e.to_string())
                    .unwrap_or_default(),
                case["error"]
            );
        }
        use base64::Engine;
        let b64 = base64::engine::general_purpose::STANDARD;
        for case in oracle["rawHosts"].as_array().unwrap() {
            let input = b64.decode(case["input"].as_str().unwrap()).unwrap();
            let old = b64.decode(case["old"].as_str().unwrap()).unwrap();
            let new = b64.decode(case["new"].as_str().unwrap()).unwrap();
            let output = b64.decode(case["output"].as_str().unwrap()).unwrap();
            assert_eq!(replace_hosts(&input, &old, &new), output);
        }
        for case in oracle["hosts"].as_array().unwrap() {
            assert_eq!(
                replace_hosts(
                    case["input"].as_str().unwrap().as_bytes(),
                    case["old"].as_str().unwrap().as_bytes(),
                    case["new"].as_str().unwrap().as_bytes()
                ),
                case["output"].as_str().unwrap().as_bytes()
            );
        }
    }
}
