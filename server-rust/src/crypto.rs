use crate::Error;
use aes::cipher::{block_padding::Pkcs7, BlockModeDecrypt, KeyIvInit};
use base64::{
    engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD},
    Engine,
};
use hmac::{Hmac, KeyInit, Mac};
use md5::{Digest, Md5};
use serde::{Deserialize, Serialize};
use sha2::Sha256;

// Wire compatibility with CryptoJS. TLS remains the confidentiality boundary.
pub fn decrypt(value: &str) -> Result<String, Error> {
    fn raw(value: &str) -> Result<String, Error> {
        if value.is_empty() {
            return Ok(String::new());
        }
        let bytes = STANDARD.decode(value)?;
        if bytes.len() < 32 || &bytes[..8] != b"Salted__" || (bytes.len() - 16) % 16 != 0 {
            return Err("invalid encrypted password".into());
        }
        let mut key = Vec::with_capacity(48);
        let mut last = Vec::new();
        while key.len() < 48 {
            let mut hash = Md5::new();
            hash.update(&last);
            hash.update(b"nanokvm-sipeed-2024");
            hash.update(&bytes[8..16]);
            last = hash.finalize().to_vec();
            key.extend_from_slice(&last);
        }
        let plain = cbc::Decryptor::<aes::Aes256>::new_from_slices(&key[..32], &key[32..48])
            .map_err(|_| "invalid encrypted password")?
            .decrypt_padded_vec::<Pkcs7>(&bytes[16..])
            .map_err(|_| "invalid encrypted password")?;
        Ok(String::from_utf8(plain)?)
    }
    raw(value).or_else(|_| {
        let unescaped = percent_encoding::percent_decode_str(&value.replace('+', " "))
            .decode_utf8()?
            .into_owned();
        if unescaped == value {
            return Err("invalid encrypted password".into());
        }
        raw(&unescaped)
    })
}

#[derive(Clone, Serialize, Deserialize)]
pub struct Claims {
    pub username: String,
    #[serde(rename = "tokenVersion")]
    pub token_version: u64,
    pub sub: String,
    pub exp: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub iat: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub nbf: Option<u64>,
}

pub fn sign(claims: &Claims, secret: &str) -> Result<String, Error> {
    let header = URL_SAFE_NO_PAD.encode(br#"{"alg":"HS256","typ":"JWT"}"#);
    let payload = URL_SAFE_NO_PAD.encode(serde_json::to_vec(claims)?);
    let data = format!("{header}.{payload}");
    let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes())?;
    mac.update(data.as_bytes());
    Ok(format!(
        "{data}.{}",
        URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes())
    ))
}

pub fn verify(token: &str, secret: &str, now: u64) -> Result<Claims, Error> {
    let parts: Vec<_> = token.split('.').collect();
    if parts.len() != 3 {
        return Err("invalid session".into());
    }
    let header: serde_json::Value = serde_json::from_slice(&URL_SAFE_NO_PAD.decode(parts[0])?)?;
    if header["alg"] != "HS256" {
        return Err("invalid signing method".into());
    }
    let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes())?;
    mac.update(format!("{}.{}", parts[0], parts[1]).as_bytes());
    mac.verify_slice(&URL_SAFE_NO_PAD.decode(parts[2])?)?;
    let claims: Claims = serde_json::from_slice(&URL_SAFE_NO_PAD.decode(parts[1])?)?;
    if claims.username.is_empty()
        || claims.username != claims.sub
        || claims.token_version == 0
        || claims.exp <= now
        || claims.nbf.is_some_and(|nbf| nbf > now)
    {
        return Err("invalid token claims".into());
    }
    Ok(claims)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn openssl_and_percent_encoded_go_fixtures() {
        let raw = "U2FsdGVkX18zLUxaLNGy7jL96oMO4tq6wDYwVzUMO3XfTY2Zy/ipO4LDEqtBT+fx";
        assert_eq!(decrypt(raw).unwrap(), "operator-password");
        assert_eq!(
            decrypt(&raw.replace('/', "%2F").replace('+', "%2B")).unwrap(),
            "operator-password"
        );
        for bad in [
            "not base64!",
            "U2FsdGVkX18=",
            "U2FsdGVkX18AAAAAAAAAAA==",
            "%zz",
        ] {
            assert!(decrypt(bad).is_err());
        }
    }
    #[test]
    fn jwt_expiry_signature_and_identity_are_required() {
        let mut c = Claims {
            username: "admin".into(),
            sub: "admin".into(),
            token_version: 3,
            iat: Some(100),
            exp: 200,
            nbf: None,
        };
        let t = sign(&c, "test-secret").unwrap();
        assert!(verify(&t, "test-secret", 199).is_ok());
        assert!(verify(&t, "test-secret", 200).is_err());
        assert!(verify(&t, "other", 100).is_err());
        c.sub = "someone-else".into();
        assert!(verify(&sign(&c, "test-secret").unwrap(), "test-secret", 100).is_err());
    }
}
