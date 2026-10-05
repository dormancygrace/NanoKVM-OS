//! Go struct JSON binding: case-insensitive field matches, wire-order updates,
//! type errors even when a later duplicate is valid, and field-specific nulls.
use serde::{
    de::{self, DeserializeSeed, MapAccess, SeqAccess, Visitor},
    Deserializer,
};
use serde_json::{Map, Value};
use std::fmt;
pub(crate) fn same_name(key: &str, canonical: &str) -> bool {
    // Go Unicode SimpleFold has these two non-ASCII equivalents of ASCII
    // letters. All implemented struct field names themselves are ASCII.
    key.chars()
        .map(|c| match c {
            '\u{017f}' => 'S',
            '\u{212a}' => 'K',
            c => c.to_ascii_uppercase(),
        })
        .eq(canonical.chars().map(|c| c.to_ascii_uppercase()))
}
#[derive(Clone, Copy)]
struct Object {
    fields: &'static [&'static str],
    nullable: bool,
    empty_null: bool,
}
impl<'de> DeserializeSeed<'de> for Object {
    type Value = Value;
    fn deserialize<D: Deserializer<'de>>(self, deserializer: D) -> Result<Value, D::Error> {
        deserializer.deserialize_any(self)
    }
}
impl<'de> Visitor<'de> for Object {
    type Value = Value;
    fn expecting(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
        formatter.write_str("JSON request object")
    }
    fn visit_unit<E: de::Error>(self) -> Result<Value, E> {
        Ok(if self.empty_null {
            Value::Object(Map::new())
        } else {
            Value::Null
        })
    }
    fn visit_map<A: MapAccess<'de>>(self, mut object: A) -> Result<Value, A::Error> {
        let mut values = Map::new();
        while let Some(key) = object.next_key::<String>()? {
            let Some(&canonical) = self
                .fields
                .iter()
                .find(|canonical| same_name(&key, canonical))
            else {
                // Go ignores unknown struct fields, including their contents.
                let _ = object.next_value::<de::IgnoredAny>()?;
                continue;
            };
            let value = match canonical {
                "keys" => object.next_value_seed(Keys)?,
                "sleep" | "target" => {
                    let raw = object.next_value::<Box<serde_json::value::RawValue>>()?;
                    if raw.get() == "null" {
                        continue;
                    }
                    Value::from(raw.get().parse::<i64>().map_err(de::Error::custom)?)
                }
                "enabled" | "keyboard" | "relative" | "absolute" | "network" | "disk"
                | "serial" | "audio" => match object.next_value::<Option<bool>>()? {
                    Some(value) => Value::Bool(value),
                    None if self.nullable || canonical != "enabled" => Value::Null,
                    None => continue,
                },
                _ => match object.next_value::<Option<String>>()? {
                    Some(value) => value.into(),
                    None if self.nullable => Value::Null,
                    // null does not overwrite a nonpointer Go string.
                    None => continue,
                },
            };
            values.insert(canonical.into(), value);
        }
        Ok(Value::Object(values))
    }
}
struct Keys;
impl<'de> DeserializeSeed<'de> for Keys {
    type Value = Value;
    fn deserialize<D: Deserializer<'de>>(self, deserializer: D) -> Result<Value, D::Error> {
        deserializer.deserialize_any(self)
    }
}
impl<'de> Visitor<'de> for Keys {
    type Value = Value;
    fn expecting(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
        formatter.write_str("shortcut key array")
    }
    fn visit_unit<E: de::Error>(self) -> Result<Value, E> {
        Ok(Value::Null)
    }
    fn visit_seq<A: SeqAccess<'de>>(self, mut sequence: A) -> Result<Value, A::Error> {
        let mut values = Vec::new();
        while let Some(value) = sequence.next_element_seed(Object {
            fields: &["code", "label"],
            nullable: false,
            empty_null: true,
        })? {
            values.push(value);
        }
        Ok(Value::Array(values))
    }
}
pub(crate) fn json(
    data: &[u8],
    fields: &'static [&'static str],
    nullable: bool,
) -> Result<Value, serde_json::Error> {
    crate::json_syntax::first_value(data).map_err(<serde_json::Error as de::Error>::custom)?;
    let normalized = crate::json_text::normalize(data);
    let mut parser = serde_json::Deserializer::from_slice(&normalized);
    let value = Object {
        fields,
        nullable,
        empty_null: false,
    }
    .deserialize(&mut parser)?;
    // Gin JSON binding decodes the first JSON value only.
    Ok(value)
}
pub(crate) fn json_key(data: &[u8]) -> Result<Value, serde_json::Error> {
    crate::json_syntax::first_value(data).map_err(<serde_json::Error as de::Error>::custom)?;
    let normalized = crate::json_text::normalize(data);
    let mut parser = serde_json::Deserializer::from_slice(&normalized);
    let value = Object {
        fields: &["code", "label"],
        nullable: false,
        empty_null: true,
    }
    .deserialize(&mut parser)?;
    parser.end()?;
    Ok(value)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn case_order_nulls_types_and_unknown_fields_match_go_struct_binding() {
        assert_eq!(json(br#"{"username":"first","USERNAME":"last","username":null,"KEYS":[{"CoDe":"first","CODE":"last","LABEL":"a"},null],"unknown":{"PASSWORD":7},"customKey":18446744073709551615}"#, &["username","keys"], false).unwrap(),
            serde_json::json!({"username":"last","keys":[{"code":"last","label":"a"},{}]}));
        assert_eq!(
            json(
                br#"{"USERNAME":"first","username":null}"#,
                &["username"],
                true
            )
            .unwrap()["username"],
            Value::Null
        );
        assert_eq!(
            json(br#"{"enabled":true,"ENABLED":null}"#, &["enabled"], false).unwrap()["enabled"],
            true
        );
        assert!(json(br#"{"username":7,"USERNAME":"last"}"#, &["username"], false).is_err());
        assert!(json(br#"{"username":"x"} true"#, &["username"], false).is_ok());
        assert!(json(
            br#"{"username":"x","unknown":1e10000}"#,
            &["username"],
            false
        )
        .is_ok());
        assert!(json(b"[]", &["title"], false).is_err());
        assert_eq!(json(b"null", &["title"], false).unwrap(), Value::Null);
        assert_eq!(
            json(
                "{\"uſername\":\"viewer\",\"Keys\":[]}".as_bytes(),
                &["username", "keys"],
                false
            )
            .unwrap(),
            serde_json::json!({"username":"viewer","keys":[]})
        );
    }
}

#[cfg(test)]
mod signed_oracle_tests {
    use super::*;
    #[test]
    fn actual_baseline_signed_json_binding() {
        let oracle: Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/signed-go-oracle.json"
        ))
        .unwrap();
        for case in oracle.as_array().unwrap() {
            let result = json(case["body"].as_str().unwrap().as_bytes(), &["sleep"], false);
            assert_eq!(
                result.is_err(),
                case["error"].as_bool().unwrap(),
                "{}",
                case["name"]
            );
            if let Ok(v) = result {
                assert_eq!(
                    v["sleep"].as_i64().unwrap_or(0),
                    case["sleep"],
                    "{}",
                    case["name"]
                );
            }
        }
    }
}
