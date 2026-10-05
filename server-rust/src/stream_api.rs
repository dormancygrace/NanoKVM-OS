use crate::{
    api,
    request_cancel::Cancellation,
    screen_store,
    video_source::{Codec, EncoderConfig},
    Error, Runtime,
};
use axum::{
    http::StatusCode,
    response::{IntoResponse, Response},
};
use serde_json::{json, Value};
use std::{fmt::Write, time::Duration};

pub(crate) fn state(runtime: &Runtime) -> Response {
    let (active, selected, codec) = runtime.video.encoder_state();
    let mut response = api::ok(json!({"active":active,"selected":selected,"codec":codec}));
    response
        .headers_mut()
        .insert("cache-control", "no-store".parse().unwrap());
    response
}
pub(crate) fn select(
    runtime: &Runtime,
    role: &str,
    parsed: Result<Value, Error>,
    cancelled: &Cancellation,
) -> Response {
    if role != "admin" {
        return StatusCode::FORBIDDEN.into_response();
    }
    let Some(codec) = parsed
        .ok()
        .and_then(|value| value["codec"].as_str().and_then(Codec::parse))
    else {
        return api::error(-1, "codec must be h264 or h265");
    };
    let _guard = loop {
        if cancelled.cancelled() {
            return api::error(-2, "cannot save encoder selection");
        }
        match runtime.encoder_settings.try_lock() {
            Ok(guard) => break guard,
            Err(std::sync::TryLockError::Poisoned(_)) => {
                return api::error(-2, "cannot save encoder selection")
            }
            Err(std::sync::TryLockError::WouldBlock) => {
                std::thread::sleep(Duration::from_millis(10))
            }
        }
    };
    if screen_store::write(
        &runtime.root,
        "/etc/kvm/encoder_codec",
        format!("{codec}\n").as_bytes(),
        0o600,
        false,
    )
    .is_err()
    {
        return api::error(-2, "cannot save encoder selection");
    }
    runtime.video.select(EncoderConfig { codec });
    api::ok(Value::Null)
}

fn decode(value: &[u8]) -> Option<Vec<u8>> {
    let mut result = Vec::with_capacity(value.len());
    let mut offset = 0;
    while offset < value.len() {
        match value[offset] {
            b'+' => result.push(b' '),
            b'%' => {
                let hi = char::from(*value.get(offset + 1)?).to_digit(16)?;
                let lo = char::from(*value.get(offset + 2)?).to_digit(16)?;
                result.push((hi * 16 + lo) as u8);
                offset += 2;
            }
            byte => result.push(byte),
        }
        offset += 1;
    }
    Some(result)
}
/// URL.Query keeps valid pairs when ParseQuery also reports bad fields.
fn query(raw: &str) -> Vec<(Vec<u8>, Vec<u8>)> {
    raw.as_bytes()
        .split(|byte| *byte == b'&')
        .filter(|field| !field.is_empty() && !field.contains(&b';'))
        .filter_map(|field| {
            let position = field.iter().position(|byte| *byte == b'=');
            let (key, value) = position.map_or((field, &b""[..]), |position| {
                (&field[..position], &field[position + 1..])
            });
            Some((decode(key)?, decode(value)?))
        })
        .collect()
}
fn first<'a>(values: &'a [(Vec<u8>, Vec<u8>)], key: &[u8]) -> &'a [u8] {
    values
        .iter()
        .find(|(name, _)| name == key)
        .map_or(&[], |(_, value)| value)
}
pub(crate) fn encoder_query(raw: &str, fallback: EncoderConfig) -> Result<EncoderConfig, String> {
    let values = query(raw);
    let codec = first(&values, b"codec");
    let codec = if codec.is_empty() {
        fallback.codec
    } else {
        match codec {
            b"h264" => Codec::H264,
            b"h265" => Codec::H265,
            value => {
                return Err(format!(
                    "invalid codec {}: expected h264 or h265",
                    quote(value)
                ))
            }
        }
    };
    let rc = first(&values, b"rc");
    if !rc.is_empty() && rc != b"cbr" {
        return Err(format!(
            "invalid rate control {}: only cbr is supported",
            quote(rc)
        ));
    }
    Ok(EncoderConfig { codec })
}
pub(crate) fn flow_window(raw: &str) -> Option<usize> {
    let values = query(raw);
    let value = std::str::from_utf8(first(&values, b"flow"))
        .ok()?
        .parse::<i64>()
        .ok()?;
    if value > 0 {
        Some(value.min(8) as usize)
    } else {
        None
    }
}
fn quote(value: &[u8]) -> String {
    fn character(result: &mut String, c: char) {
        match c {
            '\\' => result.push_str("\\\\"),
            '"' => result.push_str("\\\""),
            '\x07' => result.push_str("\\a"),
            '\x08' => result.push_str("\\b"),
            '\x0c' => result.push_str("\\f"),
            '\n' => result.push_str("\\n"),
            '\r' => result.push_str("\\r"),
            '\t' => result.push_str("\\t"),
            '\x0b' => result.push_str("\\v"),
            c => {
                let value = u32::from(c);
                let printable = crate::go_printable::PRINTABLE
                    .binary_search_by(|&(lower, upper)| {
                        if upper < value {
                            std::cmp::Ordering::Less
                        } else if lower > value {
                            std::cmp::Ordering::Greater
                        } else {
                            std::cmp::Ordering::Equal
                        }
                    })
                    .is_ok();
                if printable {
                    result.push(c);
                } else if value < 0x20 || value == 0x7f {
                    write!(result, "\\x{value:02x}").unwrap();
                } else if value <= 0xffff {
                    write!(result, "\\u{value:04x}").unwrap();
                } else {
                    write!(result, "\\U{value:08x}").unwrap();
                }
            }
        }
    }
    let mut result = String::from("\"");
    let mut rest = value;
    while !rest.is_empty() {
        match std::str::from_utf8(rest) {
            Ok(text) => {
                for c in text.chars() {
                    character(&mut result, c);
                }
                break;
            }
            Err(error) => {
                let valid = error.valid_up_to();
                for c in std::str::from_utf8(&rest[..valid])
                    .expect("validated UTF8 prefix")
                    .chars()
                {
                    character(&mut result, c);
                }
                let invalid = error.error_len().unwrap_or(rest.len() - valid);
                for byte in &rest[valid..valid + invalid] {
                    write!(result, "\\x{byte:02x}").unwrap();
                }
                rest = &rest[valid + invalid..];
            }
        }
    }
    result.push('"');
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    use sha2::{Digest, Sha256};
    fn oracle() -> Value {
        serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/direct-contract-go-oracle.json"
        ))
        .unwrap()
    }
    #[test]
    fn direct_query_config_and_signed_flow_match_actual_go() {
        let oracle = oracle();
        for case in oracle["config"].as_array().unwrap() {
            let raw = case["query"].as_str().unwrap();
            let fallback = EncoderConfig {
                codec: Codec::parse(case["fallback"].as_str().unwrap()).unwrap(),
            };
            let result = encoder_query(raw, fallback);
            let (codec, error) = match result {
                Ok(config) => (config.codec.to_string(), String::new()),
                Err(error) => (String::new(), error),
            };
            assert_eq!(codec, case["codec"].as_str().unwrap(), "{raw}");
            assert_eq!(error, case["error"].as_str().unwrap(), "{raw}");
            assert_eq!(
                flow_window(raw).unwrap_or(0) as u64,
                case["flow"].as_u64().unwrap(),
                "{raw}"
            );
        }
    }
    #[test]
    fn every_unicode_scalar_go_quote_digest_and_invalid_bytes_match() {
        let mut digest = Sha256::new();
        let mut buffer = [0; 4];
        for scalar in 0..=0x10ffff {
            if let Some(c) = char::from_u32(scalar) {
                digest.update(quote(c.encode_utf8(&mut buffer).as_bytes()).as_bytes());
            }
        }
        assert_eq!(
            digest
                .finalize()
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect::<String>(),
            oracle()["allScalarQuotedSHA256"].as_str().unwrap()
        );
        assert_eq!(
            quote(&[0xff, 0xc2, 0xa0, 0xc0, 0x80]),
            r#""\xff\u00a0\xc0\x80""#
        );
    }
}
