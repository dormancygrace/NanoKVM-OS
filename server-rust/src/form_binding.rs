//! Go net/http urlencoded/query binding; JSON binding stays in binding.rs.
use crate::Error;
use axum::http::{HeaderMap, Method};
pub(crate) fn parse_query(data: &[u8]) -> Result<Vec<(String, String)>, Error> {
    // url.ParseQuery rejects raw semicolons and malformed percent escapes,
    // including in unknown fields, instead of silently normalizing them.
    if data.contains(&b';') {
        return Err("invalid semicolon separator in query".into());
    }
    let mut i = 0;
    while i < data.len() {
        if data[i] == b'%' {
            if data.get(i + 1).is_none_or(|b| !b.is_ascii_hexdigit())
                || data.get(i + 2).is_none_or(|b| !b.is_ascii_hexdigit())
            {
                return Err("invalid URL escape".into());
            }
            i += 3;
        } else {
            i += 1;
        }
    }
    Ok(serde_urlencoded::from_bytes(data)?)
}
pub(crate) fn parameters(
    headers: &HeaderMap,
    body: &[u8],
    method: &Method,
    query: Option<&str>,
) -> Result<Vec<(String, String)>, Error> {
    let mut values = Vec::new();
    if matches!(*method, Method::POST | Method::PUT | Method::PATCH) {
        let value = headers
            .get("content-type")
            .map(|value| value.to_str())
            .transpose()?
            .unwrap_or("application/octet-stream");
        let value = if value.is_empty() {
            "application/octet-stream"
        } else {
            value.trim()
        };
        // Go allows a final empty parameter separator.
        let value = value.strip_suffix(';').map(str::trim_end).unwrap_or(value);
        // Go's shared media/disposition parser accepts an opaque token without
        // a slash (e.g. "inline"). Validate its MIME tokens, then reuse the
        // maintained parser for parameters while retaining the body decision.
        let (essence, parameters) = value.split_once(';').map_or((value, ""), |(essence, _)| {
            (essence, &value[essence.len()..])
        });
        let parts: Vec<_> = essence.trim().split('/').collect();
        if parts.len() > 2
            || parts.iter().any(|part| {
                part.is_empty()
                    || part.bytes().any(|byte| {
                        byte <= b' ' || byte >= 127 || b"()<>@,;:\\\"[]?=".contains(&byte)
                    })
            })
        {
            return Err("invalid media type".into());
        }
        let urlencoded = essence
            .trim()
            .eq_ignore_ascii_case("application/x-www-form-urlencoded");
        let normalized = format!("application/octet-stream{parameters}");
        let media = mediatype::MediaType::parse(&normalized)?;
        // Go permits equal duplicate parameters, and rejects unequal values.
        let mut seen = std::collections::BTreeMap::new();
        for (name, value) in media.params.iter() {
            let name = name.as_str().to_ascii_lowercase();
            if let Some(previous) = seen.insert(name, value) {
                if previous != value {
                    return Err("duplicate media parameter".into());
                }
            }
        }
        if urlencoded {
            values = parse_query(body)?;
        }
    }
    if let Some(query) = query {
        values.extend(parse_query(query.as_bytes())?);
    }
    Ok(values)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn body_query_method_and_media_rules_match_actual_go() {
        let oracle: serde_json::Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/form-go-oracle.json"
        ))
        .unwrap();
        for case in oracle["cases"].as_array().unwrap() {
            let mut headers = HeaderMap::new();
            let ct = case["contentType"].as_str().unwrap();
            if !ct.is_empty() {
                headers.insert("content-type", ct.parse().unwrap());
            }
            let result = parameters(
                &headers,
                case["body"].as_str().unwrap().as_bytes(),
                &case["method"].as_str().unwrap().parse().unwrap(),
                Some(case["query"].as_str().unwrap()),
            );
            assert_eq!(
                result.is_err(),
                case["error"].as_bool().unwrap(),
                "{}",
                case["name"]
            );
            if let Ok(values) = result {
                let mut grouped = std::collections::BTreeMap::<String, Vec<String>>::new();
                for (key, value) in values {
                    grouped.entry(key).or_default().push(value);
                }
                assert_eq!(
                    serde_json::to_value(grouped).unwrap(),
                    case["values"],
                    "{}",
                    case["name"]
                );
            }
        }
    }
}
