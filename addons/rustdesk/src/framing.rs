// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::io;

use crypto_secretbox::{
    aead::{Aead, KeyInit},
    Key, Nonce, XSalsa20Poly1305,
};
use prost::Message as ProstMessage;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

const MAX_PACKET_LENGTH: usize = 8 << 20;

#[derive(Clone)]
pub struct SessionKey([u8; 32]);

impl SessionKey {
    pub fn new(bytes: [u8; 32]) -> Self {
        Self(bytes)
    }
}

pub struct FrameReader<R> {
    inner: R,
    cipher: Option<XSalsa20Poly1305>,
    sequence: u64,
}

pub struct FrameWriter<W> {
    inner: W,
    cipher: Option<XSalsa20Poly1305>,
    sequence: u64,
}

impl<R> FrameReader<R>
where
    R: AsyncRead + Unpin,
{
    pub fn new(inner: R, key: Option<SessionKey>) -> Self {
        Self {
            inner,
            cipher: key.map(|key| XSalsa20Poly1305::new(Key::from_slice(&key.0))),
            sequence: 0,
        }
    }

    pub async fn read<M>(&mut self) -> io::Result<M>
    where
        M: ProstMessage + Default,
    {
        let mut payload = read_payload(&mut self.inner).await?;
        if let Some(cipher) = &self.cipher {
            self.sequence = self.sequence.wrapping_add(1);
            payload = cipher
                .decrypt(&nonce(self.sequence), payload.as_slice())
                .map_err(|_| {
                    io::Error::new(io::ErrorKind::InvalidData, "frame decryption failed")
                })?;
        }
        M::decode(payload.as_slice())
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))
    }
}

impl<W> FrameWriter<W>
where
    W: AsyncWrite + Unpin,
{
    pub fn new(inner: W, key: Option<SessionKey>) -> Self {
        Self {
            inner,
            cipher: key.map(|key| XSalsa20Poly1305::new(Key::from_slice(&key.0))),
            sequence: 0,
        }
    }

    pub async fn write<M>(&mut self, message: &M) -> io::Result<()>
    where
        M: ProstMessage,
    {
        let mut payload = message.encode_to_vec();
        if let Some(cipher) = &self.cipher {
            self.sequence = self.sequence.wrapping_add(1);
            payload = cipher
                .encrypt(&nonce(self.sequence), payload.as_slice())
                .map_err(|_| {
                    io::Error::new(io::ErrorKind::InvalidData, "frame encryption failed")
                })?;
        }
        write_payload(&mut self.inner, &payload).await
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
    let length = read_length(reader).await?;
    if length > MAX_PACKET_LENGTH {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "packet is too large",
        ));
    }
    let mut payload = vec![0; length];
    reader.read_exact(&mut payload).await?;
    Ok(payload)
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
}
