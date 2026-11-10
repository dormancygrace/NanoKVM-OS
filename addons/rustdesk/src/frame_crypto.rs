// SPDX-License-Identifier: AGPL-3.0-only
//! The frame cipher: XSalsa20-Poly1305 with a detached tag (NaCl secretbox),
//! in the caller's payload buffer, byte for byte what `crypto_secretbox`
//! produces.
//!
//! Written in plain Rust for the one 64-bit core this runs on: Salsa20 keeps
//! its 16 state words in registers and XORs whole words, Poly1305 works in
//! radix 2^64 with 128-bit products. The measurements are in the README.
use crypto_secretbox::{aead::Error, Key, Nonce, Tag};
use zeroize::{Zeroize, Zeroizing};

const SIGMA: [u32; 4] = [0x61707865, 0x3320646e, 0x79622d32, 0x6b206574];

pub struct FrameCipher([u32; 8]);
impl FrameCipher {
    pub fn new(key: &Key) -> Self {
        Self(core::array::from_fn(|i| le32(key, 4 * i)))
    }
    pub fn encrypt(&self, nonce: &Nonce, buffer: &mut [u8]) -> Result<Tag, Error> {
        let mut state = self.state(nonce);
        let first = Zeroizing::new(block(&state));
        xor_stream(&mut state, &first, buffer);
        Ok(tag(&first, buffer).into())
    }
    // The tag is checked first; a frame with a wrong tag is left as it was.
    pub fn decrypt(&self, nonce: &Nonce, buffer: &mut [u8], expected: &Tag) -> Result<(), Error> {
        let mut state = self.state(nonce);
        let first = Zeroizing::new(block(&state));
        let actual = tag(&first, buffer);
        // One comparison of the whole tag, so the time does not tell where
        // it differs.
        if (le64(&actual, 0) ^ le64(expected, 0)) | (le64(&actual, 8) ^ le64(expected, 8)) != 0 {
            return Err(Error);
        }
        xor_stream(&mut state, &first, buffer);
        Ok(())
    }
    // The Salsa20 state at block 0. XSalsa20: HSalsa20 of the key and the
    // first 16 bytes of the nonce is the key, its last 8 bytes the nonce.
    // Wiped after the frame, like the first block with the Poly1305 key.
    fn state(&self, nonce: &Nonce) -> Zeroizing<[u32; 16]> {
        let k = &self.0;
        let n: [u32; 6] = core::array::from_fn(|i| le32(nonce, 4 * i));
        let h = rounds(&[
            SIGMA[0], k[0], k[1], k[2], k[3], SIGMA[1], n[0], n[1], n[2], n[3], SIGMA[2], k[4],
            k[5], k[6], k[7], SIGMA[3],
        ]);
        Zeroizing::new([
            SIGMA[0], h[0], h[5], h[10], h[15], SIGMA[1], n[4], n[5], 0, 0, SIGMA[2], h[6], h[7],
            h[8], h[9], SIGMA[3],
        ])
    }
}
impl Drop for FrameCipher {
    fn drop(&mut self) {
        self.0.zeroize();
    }
}

fn le32(bytes: &[u8], at: usize) -> u32 {
    u32::from_le_bytes(bytes[at..at + 4].try_into().unwrap())
}

fn le64(bytes: &[u8], at: usize) -> u64 {
    u64::from_le_bytes(bytes[at..at + 8].try_into().unwrap())
}

// The 20 Salsa20 rounds, without the final addition of the input.
#[inline(always)]
fn rounds(state: &[u32; 16]) -> [u32; 16] {
    let [mut x0, mut x1, mut x2, mut x3, mut x4, mut x5, mut x6, mut x7, mut x8, mut x9, mut x10, mut x11, mut x12, mut x13, mut x14, mut x15] =
        *state;
    macro_rules! quarter {
        ($a:ident, $b:ident, $c:ident, $d:ident) => {
            $b ^= $a.wrapping_add($d).rotate_left(7);
            $c ^= $b.wrapping_add($a).rotate_left(9);
            $d ^= $c.wrapping_add($b).rotate_left(13);
            $a ^= $d.wrapping_add($c).rotate_left(18);
        };
    }
    for _ in 0..10 {
        quarter!(x0, x4, x8, x12);
        quarter!(x5, x9, x13, x1);
        quarter!(x10, x14, x2, x6);
        quarter!(x15, x3, x7, x11);
        quarter!(x0, x1, x2, x3);
        quarter!(x5, x6, x7, x4);
        quarter!(x10, x11, x8, x9);
        quarter!(x15, x12, x13, x14);
    }
    [
        x0, x1, x2, x3, x4, x5, x6, x7, x8, x9, x10, x11, x12, x13, x14, x15,
    ]
}

// One key stream block.
#[inline(always)]
fn block(state: &[u32; 16]) -> [u8; 64] {
    let x = rounds(state);
    let mut out = [0; 64];
    for i in 0..16 {
        out[4 * i..4 * i + 4].copy_from_slice(&x[i].wrapping_add(state[i]).to_le_bytes());
    }
    out
}

// The first half of block 0 is the Poly1305 key; its second half covers the
// first 32 bytes of the frame, blocks 1, 2, ... the rest. A frame is at most
// 8 MiB (MAX_PACKET_LENGTH), so the low word of the block counter is enough.
fn xor_stream(state: &mut [u32; 16], first: &[u8; 64], buffer: &mut [u8]) {
    for (byte, key) in buffer.iter_mut().zip(&first[32..]) {
        *byte ^= key;
    }
    if buffer.len() <= 32 {
        return;
    }
    let (blocks, rest) = buffer[32..].as_chunks_mut::<64>();
    for chunk in blocks {
        state[8] += 1;
        let x = rounds(state);
        for i in 0..8 {
            let low = x[2 * i].wrapping_add(state[2 * i]) as u64;
            let high = x[2 * i + 1].wrapping_add(state[2 * i + 1]) as u64;
            let word: &mut [u8; 8] = (&mut chunk[8 * i..8 * i + 8]).try_into().unwrap();
            *word = (u64::from_le_bytes(*word) ^ (low | high << 32)).to_le_bytes();
        }
    }
    if !rest.is_empty() {
        state[8] += 1;
        for (byte, key) in rest.iter_mut().zip(&block(state)) {
            *byte ^= key;
        }
    }
}

// Poly1305 of the message under the key in the first 32 bytes of `key`.
// h = h0 + h1 2^64 + h2 2^128 with a small h2; p = 2^130 - 5.
fn tag(key: &[u8; 64], message: &[u8]) -> [u8; 16] {
    let r0 = le64(key, 0) & 0x0fff_fffc_0fff_ffff;
    let r1 = le64(key, 8) & 0x0fff_fffc_0fff_fffc;
    let s1 = r1 + (r1 >> 2); // r1 * 5 / 4, exact: 2^130 = 5 mod p
    let (mut h0, mut h1, mut h2) = (0u64, 0u64, 0u64);
    // h = (h + m + high 2^128) r mod p, partly reduced.
    let mut absorb = |m0: u64, m1: u64, high: u64| {
        let t = h0 as u128 + m0 as u128;
        let a0 = t as u64;
        let t = h1 as u128 + m1 as u128 + (t >> 64);
        let a1 = t as u64;
        let a2 = h2 + high + (t >> 64) as u64;
        let d0 = a0 as u128 * r0 as u128 + a1 as u128 * s1 as u128;
        let d1 = a0 as u128 * r1 as u128
            + a1 as u128 * r0 as u128
            + (a2 * s1) as u128
            + (d0 >> 64);
        let d2 = a2 * r0 + (d1 >> 64) as u64;
        // Fold the bits from 2^130 up: times 5.
        let t = d0 as u64 as u128 + ((d2 >> 2) + (d2 & !3)) as u128;
        h0 = t as u64;
        let t = d1 as u64 as u128 + (t >> 64);
        h1 = t as u64;
        h2 = (d2 & 3) + (t >> 64) as u64;
    };
    let (blocks, rest) = message.as_chunks::<16>();
    for chunk in blocks {
        absorb(le64(chunk, 0), le64(chunk, 8), 1);
    }
    if !rest.is_empty() {
        let mut last = [0; 16];
        last[..rest.len()].copy_from_slice(rest);
        last[rest.len()] = 1;
        absorb(le64(&last, 0), le64(&last, 8), 0);
    }
    // h mod p: h + 5 reaches 2^130 exactly when h >= p, and then
    // h - p = h + 5 - 2^130. Selected without a branch.
    let t = h0 as u128 + 5;
    let g0 = t as u64;
    let t = h1 as u128 + (t >> 64);
    let g1 = t as u64;
    let take = 0u64.wrapping_sub((h2 + (t >> 64) as u64) >> 2);
    let h0 = (h0 & !take) | (g0 & take);
    let h1 = (h1 & !take) | (g1 & take);
    let t = h0 as u128 + le64(key, 16) as u128;
    let low = t as u64;
    let high = h1
        .wrapping_add(le64(key, 24))
        .wrapping_add((t >> 64) as u64);
    let mut out = [0; 16];
    out[..8].copy_from_slice(&low.to_le_bytes());
    out[8..].copy_from_slice(&high.to_le_bytes());
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crypto_secretbox::{
        aead::{AeadInPlace, KeyInit},
        XSalsa20Poly1305,
    };

    // The reference cipher gives the same bytes at every length around the
    // key stream block edges (the frame starts at byte 32 of block 0), and
    // a changed tag or frame is refused and left alone.
    #[test]
    fn equals_crypto_secretbox_and_refuses_changes() {
        let mut seed = 0x1234_5678_9abc_def0u64;
        let mut next = move || {
            seed ^= seed << 13;
            seed ^= seed >> 7;
            seed ^= seed << 17;
            seed as u8
        };
        for length in (0..420).chain([1400, 4097, 70001]) {
            let key = Key::from(core::array::from_fn(|_| next()));
            let nonce = Nonce::from(core::array::from_fn(|_| next()));
            let plain: Vec<u8> = (0..length).map(|_| next()).collect();
            let mut expected = plain.clone();
            let expected_tag = XSalsa20Poly1305::new(&key)
                .encrypt_in_place_detached(&nonce, b"", &mut expected)
                .unwrap();
            let cipher = FrameCipher::new(&key);
            let mut frame = plain.clone();
            let tag = cipher.encrypt(&nonce, &mut frame).unwrap();
            assert!(frame == expected && tag == expected_tag, "length {length}");
            let mut wrong = tag;
            wrong[length % 16] ^= 1 << (length % 8);
            assert!(cipher.decrypt(&nonce, &mut frame, &wrong).is_err());
            if length > 0 {
                frame[length / 2] ^= 0x80;
                assert!(cipher.decrypt(&nonce, &mut frame, &tag).is_err());
                frame[length / 2] ^= 0x80;
            }
            assert!(frame == expected, "a refused frame changed, length {length}");
            cipher.decrypt(&nonce, &mut frame, &tag).unwrap();
            assert!(frame == plain, "length {length}");
        }
    }
}
