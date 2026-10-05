//! SetGpioReq uint64 and error text, verified with actual Gin and validator.
use crate::Error;
use axum::http::{HeaderMap, Method};
use serde::{
    de::{MapAccess, Visitor},
    Deserialize, Deserializer,
};
use serde_json::value::RawValue;
use std::fmt;
#[derive(Default, Debug)]
pub(crate) struct Request {
    pub kind: String,
    pub duration: u64,
    error: Option<String>,
}
fn type_name(raw: &str, number: bool) -> String {
    match raw.as_bytes()[0] {
        b'"' => "string".into(),
        b'{' => "object".into(),
        b'[' => "array".into(),
        b't' | b'f' => "bool".into(),
        _ if number => format!("number {raw}"),
        _ => "number".into(),
    }
}
struct Object;
impl<'de> Visitor<'de> for Object {
    type Value = Request;
    fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str("GPIO request")
    }
    fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> Result<Request, A::Error> {
        let mut result = Request::default();
        while let Some(key) = map.next_key::<String>()? {
            let raw = map.next_value::<Box<RawValue>>()?;
            let raw = raw.get();
            if raw == "null" {
                continue;
            }
            let error = if crate::binding::same_name(&key, "Type") {
                match serde_json::from_str::<String>(raw) {Ok(value)=>{result.kind=value;None}, Err(_)=>Some(format!("json: cannot unmarshal {} into Go struct field SetGpioReq.Type of type string",type_name(raw,false)))}
            } else if crate::binding::same_name(&key, "Duration") {
                match raw.parse::<u64>() {Ok(value) if raw.bytes().all(|b| b.is_ascii_digit())=>{result.duration=value;None}, _=>Some(format!("json: cannot unmarshal {} into Go struct field SetGpioReq.Duration of type uint",type_name(raw,true)))}
            } else {
                None
            };
            if result.error.is_none() {
                result.error = error;
            }
        }
        Ok(result)
    }
}
pub(crate) fn parse(
    headers: &HeaderMap,
    body: &[u8],
    query: Option<&str>,
) -> Result<Request, Error> {
    let mut result = if headers
        .get("content-type")
        .and_then(|h| h.to_str().ok())
        .is_some_and(|h| h.split(';').next() == Some("application/json"))
    {
        if body
            .iter()
            .all(|b| matches!(b, b' ' | b'\n' | b'\r' | b'\t'))
        {
            return Err("EOF".into());
        }
        crate::json_syntax::first_value(body)?;
        // Gin invokes Decoder.Decode once, so it accepts a trailing value.
        let mut parser = serde_json::Deserializer::from_slice(body);
        let raw = Box::<RawValue>::deserialize(&mut parser).map_err(|error| {
            if error.is_eof() {
                "unexpected EOF".to_owned()
            } else {
                error.to_string()
            }
        })?;
        match raw.get().as_bytes()[0] {
            b'{' => {
                let mut parser = serde_json::Deserializer::from_str(raw.get());
                parser.deserialize_map(Object)?
            }
            b'n' => Request::default(),
            _ => {
                return Err(format!(
                    "json: cannot unmarshal {} into Go value of type proto.SetGpioReq",
                    type_name(raw.get(), false)
                )
                .into())
            }
        }
    } else {
        let values = crate::form_binding::parameters(headers, body, &Method::POST, query)?;
        let mut result = Request::default();
        if let Some((_, value)) = values.iter().find(|(key, _)| key == "Type") {
            result.kind = value.clone();
        }
        if let Some((_, value)) = values.iter().find(|(key, _)| key == "Duration") {
            let value = value.trim();
            let value = if value.is_empty() { "0" } else { value };
            if !value.bytes().all(|b| b.is_ascii_digit()) {
                return Err(format!("strconv.ParseUint: parsing {value:?}: invalid syntax").into());
            }
            result.duration = value
                .parse()
                .map_err(|_| format!("strconv.ParseUint: parsing {value:?}: value out of range"))?;
        }
        result
    };
    if let Some(error) = result.error.take() {
        return Err(error.into());
    }
    if result.kind.is_empty() {
        return Err(
            "Key: 'SetGpioReq.Type' Error:Field validation for 'Type' failed on the 'required' tag"
                .into(),
        );
    }
    Ok(result)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn actual_gin_uint_and_validator_oracle() {
        let oracle: serde_json::Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/gpio-go-oracle.json"
        ))
        .unwrap();
        for case in oracle["cases"].as_array().unwrap() {
            let mut headers = HeaderMap::new();
            headers.insert(
                "content-type",
                case["contentType"].as_str().unwrap().parse().unwrap(),
            );
            let result = parse(
                &headers,
                case["body"].as_str().unwrap().as_bytes(),
                Some(case["query"].as_str().unwrap()),
            );
            let error = result
                .as_ref()
                .err()
                .map(ToString::to_string)
                .unwrap_or_default();
            assert_eq!(error, case["error"], "{}", case["name"]);
            if let Ok(result) = result {
                assert_eq!(result.kind, case["type"]);
                assert_eq!(result.duration, case["duration"]);
            }
        }
    }
}
