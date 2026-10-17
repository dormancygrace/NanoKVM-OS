// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::io;

use crate::frame_crypto::FrameCipher;
use crypto_secretbox::{Key, Nonce, Tag, XSalsa20Poly1305};
use prost::Message as ProstMessage;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

pub(crate) const MAX_PACKET_LENGTH: usize = 8 << 20;
// Before login a peer sends only its public key and a LoginRequest, a few
// hundred bytes each. The limit keeps unauthenticated peers from making the
// server allocate full-size packets.
pub(crate) const MAX_LOGIN_PACKET_LENGTH: usize = 64 << 10;
const MAX_RETAINED_BUFFER: usize = 1 << 20;
// Room for the longest length prefix in front of a frame, so the prefix and
// the payload go out in one write.
const HEADER_ROOM: usize = 4;

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
    max_length: usize,
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
            max_length: MAX_PACKET_LENGTH,
        }
    }

    // Packets longer than this are rejected before anything is allocated.
    pub fn set_max_length(&mut self, max_length: usize) {
        self.max_length = max_length.min(MAX_PACKET_LENGTH);
    }

    pub async fn read<M>(&mut self) -> io::Result<M>
    where
        M: ProstMessage + Default,
    {
        let result = self.read_plaintext().await.and_then(|offset| {
            M::decode(&self.buffer[offset..])
                .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))
        });
        release_oversized_buffer(&mut self.buffer);
        result
    }

    // Reads and decrypts one packet into the buffer and returns where the
    // plaintext starts.
    async fn read_plaintext(&mut self) -> io::Result<usize> {
        read_payload_into(&mut self.inner, &mut self.buffer, self.max_length).await?;
        let Some(cipher) = &self.cipher else {
            return Ok(0);
        };
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
        let offset = XSalsa20Poly1305::TAG_SIZE;
        cipher
            .decrypt(&nonce(self.sequence), &mut self.buffer[offset..], &tag)
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "frame decryption failed"))?;
        Ok(offset)
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
        let tag_length = if self.cipher.is_some() {
            XSalsa20Poly1305::TAG_SIZE
        } else {
            0
        };
        let offset = HEADER_ROOM + tag_length;
        let length = message
            .encoded_len()
            .checked_add(tag_length)
            .filter(|&length| length <= MAX_PACKET_LENGTH)
            .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "packet is too large"))?;
        self.buffer.reserve(HEADER_ROOM + length);
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
            self.buffer[HEADER_ROOM..offset].copy_from_slice(&tag);
        }
        let result = write_frame(&mut self.inner, &mut self.buffer).await;
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
    read_message_within(reader, MAX_PACKET_LENGTH).await
}

// read_message for an unauthenticated peer: longer packets are rejected
// before anything is allocated.
pub async fn read_message_within<R, M>(reader: &mut R, max_length: usize) -> io::Result<M>
where
    R: AsyncRead + Unpin,
    M: ProstMessage + Default,
{
    let payload = read_payload(reader, max_length).await?;
    M::decode(payload.as_slice()).map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))
}

pub async fn write_message<W, M>(writer: &mut W, message: &M) -> io::Result<()>
where
    W: AsyncWrite + Unpin,
    M: ProstMessage,
{
    let mut frame = Vec::with_capacity(HEADER_ROOM + message.encoded_len());
    frame.resize(HEADER_ROOM, 0);
    message
        .encode(&mut frame)
        .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
    write_frame(writer, &mut frame).await
}

async fn read_payload<R: AsyncRead + Unpin>(
    reader: &mut R,
    max_length: usize,
) -> io::Result<Vec<u8>> {
    let mut payload = Vec::new();
    read_payload_into(reader, &mut payload, max_length).await?;
    Ok(payload)
}

async fn read_payload_into<R: AsyncRead + Unpin>(
    reader: &mut R,
    payload: &mut Vec<u8>,
    max_length: usize,
) -> io::Result<()> {
    let length = read_length(reader).await?;
    if length > max_length {
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

// Writes a frame whose payload starts after HEADER_ROOM unused bytes: the
// length prefix goes into the end of that room, and the prefix and the
// payload leave in one write.
async fn write_frame<W: AsyncWrite + Unpin>(writer: &mut W, frame: &mut [u8]) -> io::Result<()> {
    let (encoded, bytes) = encoded_length(frame.len() - HEADER_ROOM)?;
    let start = HEADER_ROOM - bytes;
    frame[start..HEADER_ROOM].copy_from_slice(&encoded.to_le_bytes()[..bytes]);
    writer.write_all(&frame[start..]).await?;
    writer.flush().await
}

#[cfg(test)]
async fn write_payload<W: AsyncWrite + Unpin>(writer: &mut W, payload: &[u8]) -> io::Result<()> {
    let mut frame = vec![0; HEADER_ROOM];
    frame.extend_from_slice(payload);
    write_frame(writer, &mut frame).await
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
            let actual = super::read_payload(&mut right, super::MAX_PACKET_LENGTH)
                .await
                .unwrap();
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

    // Records every write call separately.
    #[derive(Default)]
    struct Writes(Vec<Vec<u8>>);

    impl tokio::io::AsyncWrite for Writes {
        fn poll_write(
            mut self: std::pin::Pin<&mut Self>,
            _: &mut std::task::Context<'_>,
            buf: &[u8],
        ) -> std::task::Poll<std::io::Result<usize>> {
            self.0.push(buf.to_vec());
            std::task::Poll::Ready(Ok(buf.len()))
        }
        fn poll_flush(
            self: std::pin::Pin<&mut Self>,
            _: &mut std::task::Context<'_>,
        ) -> std::task::Poll<std::io::Result<()>> {
            std::task::Poll::Ready(Ok(()))
        }
        fn poll_shutdown(
            self: std::pin::Pin<&mut Self>,
            _: &mut std::task::Context<'_>,
        ) -> std::task::Poll<std::io::Result<()>> {
            std::task::Poll::Ready(Ok(()))
        }
    }

    #[tokio::test]
    async fn length_prefix_and_payload_leave_in_one_write() {
        use prost::Message as _;
        // Payload sizes on both sides of each length-prefix width.
        for size in [0, 50, 60, 70, 0x3ff0, 0x4010, 0x3ffff0, 0x400010] {
            let message = Message {
                union: Some(message::Union::Hash(Hash {
                    salt: "x".repeat(size),
                    challenge: String::new(),
                })),
            };
            for key in [None, Some(super::SessionKey::new([5; 32]))] {
                let encrypted = key.is_some();
                let mut writer = super::FrameWriter::new(Writes::default(), key.clone());
                writer.write(&message).await.unwrap();
                writer.write(&message).await.unwrap();
                let writes = &writer.inner.0;
                assert_eq!(writes.len(), 2, "size={size} encrypted={encrypted}");
                let wire = writes.concat();
                let mut reader = super::FrameReader::new(wire.as_slice(), key);
                for _ in 0..2 {
                    assert_eq!(reader.read::<Message>().await.unwrap(), message);
                }
            }
            let mut writes = Writes::default();
            write_message(&mut writes, &message).await.unwrap();
            assert_eq!(writes.0.len(), 1, "size={size}");
            let mut wire = writes.0[0].as_slice();
            let payload = super::read_payload(&mut wire, super::MAX_PACKET_LENGTH)
                .await
                .unwrap();
            assert_eq!(payload, message.encode_to_vec());
            assert!(wire.is_empty());
        }
    }

    #[tokio::test]
    async fn login_limit_rejects_a_long_packet_before_reading_it() {
        use std::time::Duration;
        use tokio::io::AsyncWriteExt;
        // A 1 MiB length prefix (3 bytes, low bits 2) and no payload: the
        // read must fail at once instead of waiting for the payload.
        let header = (((1_usize << 20) << 2) | 2).to_le_bytes();
        let (mut left, right) = duplex(64);
        left.write_all(&header[..3]).await.unwrap();
        let mut reader = super::FrameReader::new(right, None);
        reader.set_max_length(super::MAX_LOGIN_PACKET_LENGTH);
        let error = tokio::time::timeout(Duration::from_secs(5), reader.read::<Message>())
            .await
            .expect("an over-limit packet must be rejected without reading it")
            .unwrap_err();
        assert_eq!(error.kind(), std::io::ErrorKind::InvalidData);
        assert_eq!(reader.buffer.capacity(), 0);

        let (mut left, mut right) = duplex(64);
        left.write_all(&header[..3]).await.unwrap();
        let error = tokio::time::timeout(
            Duration::from_secs(5),
            super::read_message_within::<_, Message>(&mut right, super::MAX_LOGIN_PACKET_LENGTH),
        )
        .await
        .expect("an over-limit packet must be rejected without reading it")
        .unwrap_err();
        assert_eq!(error.kind(), std::io::ErrorKind::InvalidData);

        // A packet within the limit still reads.
        let expected = Message {
            union: Some(message::Union::Hash(Hash {
                salt: "s".repeat(1000),
                challenge: "c".to_owned(),
            })),
        };
        let (mut left, right) = duplex(4096);
        write_message(&mut left, &expected).await.unwrap();
        let mut reader = super::FrameReader::new(right, None);
        reader.set_max_length(super::MAX_LOGIN_PACKET_LENGTH);
        assert_eq!(reader.read::<Message>().await.unwrap(), expected);
    }
}
