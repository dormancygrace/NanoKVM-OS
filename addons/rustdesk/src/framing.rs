// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::io;

use crate::frame_crypto::FrameCipher;
use crypto_secretbox::{Key, Nonce, Tag, XSalsa20Poly1305};
use prost::Message as ProstMessage;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

const MAX_PACKET_LENGTH: usize = 8 << 20;
const MAX_RETAINED_BUFFER: usize = 1 << 20;

#[derive(Clone)]
pub struct SessionKey {
    send: [u8; 32],
    receive: [u8; 32],
}

impl SessionKey {
    pub fn new(bytes: [u8; 32]) -> Self {
        Self {
            send: bytes,
            receive: bytes,
        }
    }

    // RustDesk 1.5 KX v1: keyed BLAKE2b-256 with the exact upstream
    // domain, direction, little-endian versions and both ephemeral keys.
    pub fn negotiated(
        bytes: [u8; 32],
        initiator: &[u8; 32],
        responder: &[u8; 32],
        advertised: u32,
        picked: u32,
        is_initiator: bool,
    ) -> io::Result<Self> {
        if picked > advertised || picked > 1 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "key exchange version not offered",
            ));
        }
        if picked == 0 {
            return Ok(Self::new(bytes));
        }
        use blake2::{
            digest::{consts::U32, KeyInit, Mac},
            Blake2bMac,
        };
        let derive = |direction: u8| {
            let mut hash = <Blake2bMac<U32> as KeyInit>::new_from_slice(&bytes)
                .expect("a 32-byte BLAKE2 key is valid");
            for part in [
                b"rdkx-spl".as_slice(),
                &[direction],
                &advertised.to_le_bytes(),
                &picked.to_le_bytes(),
                initiator.as_slice(),
                responder.as_slice(),
            ] {
                Mac::update(&mut hash, part);
            }
            let output = hash.finalize().into_bytes();
            let mut key = [0u8; 32];
            key.copy_from_slice(&output);
            key
        };
        let (initiator_key, responder_key) = (derive(1), derive(2));
        Ok(if is_initiator {
            Self {
                send: initiator_key,
                receive: responder_key,
            }
        } else {
            Self {
                send: responder_key,
                receive: initiator_key,
            }
        })
    }
}

pub struct FrameReader<R> {
    inner: R,
    cipher: Option<FrameCipher>,
    sequence: u64,
    buffer: Vec<u8>,
}

pub struct FrameWriter<W> {
    inner: W,
    cipher: Option<FrameCipher>,
    sequence: u64,
    buffer: Vec<u8>,
}

impl<R> FrameReader<R>
where
    R: AsyncRead + Unpin,
{
    pub fn new(inner: R, key: Option<SessionKey>) -> Self {
        Self {
            inner,
            cipher: key.map(|key| FrameCipher::new(Key::from_slice(&key.receive))),
            sequence: 0,
            buffer: Vec::new(),
        }
    }

    pub async fn read<M>(&mut self) -> io::Result<M>
    where
        M: ProstMessage + Default,
    {
        read_payload_into(&mut self.inner, &mut self.buffer).await?;
        let mut offset = 0;
        if let Some(cipher) = &self.cipher {
            self.sequence = self.sequence.checked_add(1).ok_or_else(|| {
                io::Error::new(
                    io::ErrorKind::InvalidData,
                    "session nonce counter exhausted",
                )
            })?;
            if self.buffer.len() < XSalsa20Poly1305::TAG_SIZE {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    "frame decryption failed",
                ));
            }
            let tag = Tag::clone_from_slice(&self.buffer[..XSalsa20Poly1305::TAG_SIZE]);
            offset = XSalsa20Poly1305::TAG_SIZE;
            cipher
                .decrypt(&nonce(self.sequence), &mut self.buffer[offset..], &tag)
                .map_err(|_| {
                    io::Error::new(io::ErrorKind::InvalidData, "frame decryption failed")
                })?;
        }
        let result = M::decode(&self.buffer[offset..])
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error));
        release_oversized_buffer(&mut self.buffer);
        result
    }
}

impl<W> FrameWriter<W>
where
    W: AsyncWrite + Unpin,
{
    pub fn new(inner: W, key: Option<SessionKey>) -> Self {
        Self {
            inner,
            cipher: key.map(|key| FrameCipher::new(Key::from_slice(&key.send))),
            sequence: 0,
            buffer: Vec::new(),
        }
    }

    pub async fn write<M>(&mut self, message: &M) -> io::Result<()>
    where
        M: ProstMessage,
    {
        self.buffer.clear();
        let offset = if self.cipher.is_some() {
            XSalsa20Poly1305::TAG_SIZE
        } else {
            0
        };
        let length = message
            .encoded_len()
            .checked_add(offset)
            .filter(|&length| length <= MAX_PACKET_LENGTH)
            .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "packet is too large"))?;
        self.buffer.reserve(length);
        self.buffer.resize(offset, 0);
        message
            .encode(&mut self.buffer)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        if let Some(cipher) = &self.cipher {
            self.sequence = self.sequence.checked_add(1).ok_or_else(|| {
                io::Error::new(
                    io::ErrorKind::InvalidData,
                    "session nonce counter exhausted",
                )
            })?;
            let tag = cipher
                .encrypt(&nonce(self.sequence), &mut self.buffer[offset..])
                .map_err(|_| {
                    io::Error::new(io::ErrorKind::InvalidData, "frame encryption failed")
                })?;
            self.buffer[..offset].copy_from_slice(&tag);
        }
        let result = write_payload(&mut self.inner, &self.buffer).await;
        release_oversized_buffer(&mut self.buffer);
        result
    }
}

fn nonce(sequence: u64) -> Nonce {
    let mut bytes = [0_u8; 24];
    bytes[..8].copy_from_slice(&sequence.to_le_bytes());
    *Nonce::from_slice(&bytes)
}

pub async fn read_message<R, M>(reader: &mut R) -> io::Result<M>
where
    R: AsyncRead + Unpin,
    M: ProstMessage + Default,
{
    let payload = read_payload(reader).await?;
    M::decode(payload.as_slice()).map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))
}

pub async fn write_message<W, M>(writer: &mut W, message: &M) -> io::Result<()>
where
    W: AsyncWrite + Unpin,
    M: ProstMessage,
{
    let payload = message.encode_to_vec();
    write_payload(writer, &payload).await
}

async fn read_payload<R: AsyncRead + Unpin>(reader: &mut R) -> io::Result<Vec<u8>> {
    let mut payload = Vec::new();
    read_payload_into(reader, &mut payload).await?;
    Ok(payload)
}

async fn read_payload_into<R: AsyncRead + Unpin>(
    reader: &mut R,
    payload: &mut Vec<u8>,
) -> io::Result<()> {
    let length = read_length(reader).await?;
    if length > MAX_PACKET_LENGTH {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "packet is too large",
        ));
    }
    payload.resize(length, 0);
    reader.read_exact(payload).await?;
    Ok(())
}

fn release_oversized_buffer(buffer: &mut Vec<u8>) {
    if buffer.capacity() > MAX_RETAINED_BUFFER {
        *buffer = Vec::new();
    }
}

async fn write_payload<W: AsyncWrite + Unpin>(writer: &mut W, payload: &[u8]) -> io::Result<()> {
    write_length(writer, payload.len()).await?;
    writer.write_all(payload).await?;
    writer.flush().await
}

async fn read_length<R: AsyncRead + Unpin>(reader: &mut R) -> io::Result<usize> {
    let first = reader.read_u8().await?;
    let header_length = usize::from((first & 0x03) + 1);
    let mut encoded = usize::from(first);
    for offset in 1..header_length {
        encoded |= usize::from(reader.read_u8().await?) << (offset * 8);
    }
    Ok(encoded >> 2)
}

async fn write_length<W: AsyncWrite + Unpin>(writer: &mut W, length: usize) -> io::Result<()> {
    let (encoded, bytes) = encoded_length(length)?;
    writer.write_all(&encoded.to_le_bytes()[..bytes]).await
}

fn encoded_length(length: usize) -> io::Result<(usize, usize)> {
    let result = if length <= 0x3f {
        (length << 2, 1)
    } else if length <= 0x3fff {
        ((length << 2) | 1, 2)
    } else if length <= 0x3fffff {
        ((length << 2) | 2, 3)
    } else if length <= 0x3fffffff {
        ((length << 2) | 3, 4)
    } else {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "packet is too large",
        ));
    };
    Ok(result)
}

#[cfg(test)]
mod tests {
    use tokio::io::duplex;

    use crate::protocol::{message, Hash, Message};

    use super::{read_message, write_message};

    #[test]
    fn kx_v1_matches_independent_blake2b_vectors_and_splits_directions() {
        let mut bytes = [0u8; 32];
        for (i, b) in bytes.iter_mut().enumerate() {
            *b = i as u8;
        }
        let init = super::SessionKey::negotiated(bytes, &[17; 32], &[23; 32], 1, 1, true).unwrap();
        let resp = super::SessionKey::negotiated(bytes, &[17; 32], &[23; 32], 1, 1, false).unwrap();
        assert_eq!(
            init.send,
            [
                223, 213, 146, 56, 121, 34, 202, 87, 204, 216, 12, 124, 127, 27, 197, 16, 253, 101,
                105, 202, 239, 150, 57, 26, 160, 163, 123, 86, 92, 207, 172, 42
            ]
        );
        assert_eq!(
            init.receive,
            [
                160, 72, 144, 162, 135, 42, 191, 237, 99, 185, 240, 210, 224, 18, 46, 88, 138, 128,
                175, 105, 203, 208, 172, 226, 198, 164, 186, 32, 218, 163, 41, 175
            ]
        );
        assert_eq!(init.send, resp.receive);
        assert_eq!(init.receive, resp.send);
        assert_ne!(init.send, init.receive);
        let altered =
            super::SessionKey::negotiated(bytes, &[18; 32], &[23; 32], 1, 1, true).unwrap();
        assert_ne!(init.send, altered.send);
        assert!(super::SessionKey::negotiated(bytes, &[17; 32], &[23; 32], 1, 2, true).is_err());
    }

    #[tokio::test]
    async fn encrypted_counter_exhaustion_cannot_reuse_a_nonce() {
        let (left, _right) = duplex(1024);
        let mut writer = super::FrameWriter::new(left, Some(super::SessionKey::new([9; 32])));
        writer.sequence = u64::MAX;
        let error = writer.write(&Message::default()).await.unwrap_err();
        assert_eq!(error.kind(), std::io::ErrorKind::InvalidData);
    }
    #[tokio::test]
    async fn frame_round_trip() {
        let (mut left, mut right) = duplex(1024);
        let expected = Message {
            union: Some(message::Union::Hash(Hash {
                salt: "salt".to_owned(),
                challenge: "challenge".to_owned(),
            })),
        };
        write_message(&mut left, &expected).await.unwrap();
        let actual: Message = read_message(&mut right).await.unwrap();
        assert_eq!(actual, expected);
    }
    #[tokio::test]
    async fn detached_frames_are_byte_identical_to_existing_secretbox() {
        use crypto_secretbox::aead::{Aead, KeyInit};
        use prost::Message as _;
        for size in [
            0, 1, 15, 16, 31, 32, 63, 64, 255, 256, 511, 512, 513, 1420, 16384, 1048593,
        ] {
            let message = Message {
                union: Some(message::Union::Hash(Hash {
                    salt: "x".repeat(size),
                    challenge: "wire reference".to_owned(),
                })),
            };
            let (left, mut right) = duplex(size + 512);
            let mut writer =
                super::FrameWriter::new(left, Some(super::SessionKey::new([0x37; 32])));
            writer.write(&message).await.unwrap();
            let actual = super::read_payload(&mut right).await.unwrap();
            let reference = crypto_secretbox::XSalsa20Poly1305::new((&[0x37; 32]).into())
                .encrypt(&super::nonce(1), message.encode_to_vec().as_slice())
                .unwrap();
            assert_eq!(actual, reference, "size={size}");
            if size > super::MAX_RETAINED_BUFFER {
                assert_eq!(writer.buffer.capacity(), 0);
            }
            let (mut left, right) = duplex(size + 512);
            super::write_payload(&mut left, &reference).await.unwrap();
            let mut reader =
                super::FrameReader::new(right, Some(super::SessionKey::new([0x37; 32])));
            let decoded: Message = reader.read().await.unwrap();
            assert_eq!(decoded, message);
            if size > super::MAX_RETAINED_BUFFER {
                assert_eq!(reader.buffer.capacity(), 0);
            }
        }
    }

    #[tokio::test]
    async fn framed_bad_tag_and_short_ciphertext_remain_rejected() {
        for payload in [vec![0; 15], vec![0x93; 256]] {
            let (mut left, right) = duplex(512);
            super::write_payload(&mut left, &payload).await.unwrap();
            let mut reader = super::FrameReader::new(right, Some(super::SessionKey::new([3; 32])));
            assert_eq!(
                reader.read::<Message>().await.unwrap_err().kind(),
                std::io::ErrorKind::InvalidData
            );
        }
    }
}
