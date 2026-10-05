//! encoding/json legacy string semantics: replace invalid bytes individually,
//! and replace unpaired UTF-16 escapes without altering escaped backslashes.
use std::borrow::Cow;
pub(crate) fn text(bytes: &[u8]) -> Cow<'_, str> {
    if let Ok(text) = std::str::from_utf8(bytes) {
        return Cow::Borrowed(text);
    }
    let mut output = String::with_capacity(bytes.len());
    for chunk in bytes.utf8_chunks() {
        output.push_str(chunk.valid());
        for _ in chunk.invalid() {
            output.push('\u{fffd}');
        }
    }
    Cow::Owned(output)
}
fn hex(bytes: &[u8]) -> Option<u16> {
    if bytes.len() != 4 {
        return None;
    }
    let mut value = 0;
    for byte in bytes {
        value = value * 16 + char::from(*byte).to_digit(16)? as u16;
    }
    Some(value)
}
pub(crate) fn normalize(data: &[u8]) -> Cow<'_, [u8]> {
    let decoded = text(data);
    if !decoded.as_bytes().windows(2).any(|part| part == b"\\u") {
        return match decoded {
            Cow::Borrowed(text) => Cow::Borrowed(text.as_bytes()),
            Cow::Owned(text) => Cow::Owned(text.into_bytes()),
        };
    }
    let bytes = decoded.as_bytes();
    let mut output = Vec::with_capacity(bytes.len());
    let mut at = 0;
    let mut string = false;
    while at < bytes.len() {
        let byte = bytes[at];
        if byte == b'"' {
            string = !string;
            output.push(byte);
            at += 1;
            continue;
        }
        if string && byte == b'\\' && at + 1 < bytes.len() {
            if bytes[at + 1] == b'u' {
                if let Some(code) = bytes.get(at + 2..at + 6).and_then(hex) {
                    if (0xd800..=0xdbff).contains(&code)
                        && bytes.get(at + 6..at + 8) == Some(b"\\u")
                        && bytes
                            .get(at + 8..at + 12)
                            .and_then(hex)
                            .is_some_and(|low| (0xdc00..=0xdfff).contains(&low))
                    {
                        output.extend_from_slice(&bytes[at..at + 12]);
                        at += 12;
                        continue;
                    }
                    if (0xd800..=0xdfff).contains(&code) {
                        output.extend_from_slice(b"\\ufffd");
                        at += 6;
                        continue;
                    }
                    output.extend_from_slice(&bytes[at..at + 6]);
                    at += 6;
                    continue;
                }
            }
            output.extend_from_slice(&bytes[at..at + 2]);
            at += 2;
            continue;
        }
        output.push(byte);
        at += 1;
    }
    Cow::Owned(output)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn invalid_utf8_and_utf16_match_actual_baseline_json_strings() {
        use base64::Engine;
        let cases: serde_json::Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/json-text-go-oracle.json"
        ))
        .unwrap();
        for case in cases.as_array().unwrap() {
            let input = base64::engine::general_purpose::STANDARD
                .decode(case["input"].as_str().unwrap())
                .unwrap();
            let result = crate::binding::json(&input, &["title"], false);
            assert_eq!(
                result.is_err(),
                case["error"].as_bool().unwrap(),
                "{}",
                case["name"]
            );
            if let Ok(v) = result {
                assert_eq!(
                    v["title"].as_str().unwrap_or(""),
                    case["title"],
                    "{}",
                    case["name"]
                );
            }
        }
        assert_eq!(text(&[0xe1, 0x80, 0xff]), "\u{fffd}\u{fffd}\u{fffd}");
    }
}
