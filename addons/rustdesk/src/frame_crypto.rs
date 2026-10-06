// SPDX-License-Identifier: AGPL-3.0-only
use crypto_secretbox::{
    aead::{AeadInPlace, Error, KeyInit},
    Key, Nonce, Tag, XSalsa20Poly1305,
};

// Keep the existing RustCrypto cipher; reuse the caller's payload buffer.
pub struct FrameCipher(XSalsa20Poly1305);
impl FrameCipher {
    pub fn new(key: &Key) -> Self {
        Self(XSalsa20Poly1305::new(key))
    }
    pub fn encrypt(&self, nonce: &Nonce, buffer: &mut [u8]) -> Result<Tag, Error> {
        self.0.encrypt_in_place_detached(nonce, b"", buffer)
    }
    pub fn decrypt(&self, nonce: &Nonce, buffer: &mut [u8], tag: &Tag) -> Result<(), Error> {
        self.0.decrypt_in_place_detached(nonce, b"", buffer, tag)
    }
}
