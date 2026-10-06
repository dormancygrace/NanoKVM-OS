// RustDesk 1.5.0 secure rendezvous signaling. SPDX-License-Identifier: AGPL-3.0-only
use crate::{
    config::Config,
    framing::{read_message, write_message, FrameReader, FrameWriter, SessionKey},
    protocol::{rendezvous_message, KeyExchange, KxParams, RendezvousMessage},
};
use base64::{engine::general_purpose::STANDARD, Engine};
use crypto_box::{aead::Aead, Nonce, PublicKey, SalsaBox, SecretKey};
use ed25519_dalek::{Signature, VerifyingKey};
use prost::Message as _;
use rand::{rngs::OsRng, RngCore};
use std::{io, time::Duration};
use tokio::{
    io::{ReadHalf, WriteHalf},
    net::TcpStream,
    time,
};

pub struct Signal {
    // Keep the read half alive even for a one-way candidate connection.
    pub _reader: FrameReader<ReadHalf<TcpStream>>,
    pub writer: FrameWriter<WriteHalf<TcpStream>>,
}
fn invalid(message: &str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message)
}
fn verify_signed(signed: &[u8], key: &VerifyingKey) -> io::Result<Vec<u8>> {
    if signed.len() < 64 {
        return Err(invalid("missing rendezvous signature"));
    }
    let signature = Signature::from_slice(&signed[..64])
        .map_err(|_| invalid("invalid rendezvous signature"))?;
    key.verify_strict(&signed[64..], &signature)
        .map_err(|_| invalid("rendezvous signature mismatch"))?;
    Ok(signed[64..].to_vec())
}
pub fn verified_exchange(ex: &KeyExchange, key: &VerifyingKey) -> io::Result<[u8; 32]> {
    if ex.keys.len() != 1 {
        return Err(invalid("invalid rendezvous key exchange"));
    }
    let pk: [u8; 32] = verify_signed(&ex.keys[0], key)?
        .try_into()
        .map_err(|_| invalid("invalid rendezvous public key length"))?;
    if pk[31] & 0x80 != 0 || !ex.signed_params.is_empty() {
        let signed = verify_signed(&ex.signed_params, key)?;
        let params = signed
            .strip_prefix(b"rdkx-params")
            .ok_or_else(|| invalid("invalid KX signature domain"))?;
        let params =
            KxParams::decode(params).map_err(|_| invalid("invalid signed KX parameters"))?;
        if params.pk != pk || params.version != ex.version {
            return Err(invalid("key exchange parameters do not match signature"));
        }
    }
    Ok(pk)
}
pub async fn connect(config: &Config) -> io::Result<Signal> {
    let address = crate::rendezvous::server_address(&config.rendezvous_server, 21116)?;
    let stream = time::timeout(Duration::from_secs(8), TcpStream::connect(address))
        .await
        .map_err(|_| {
            io::Error::new(io::ErrorKind::TimedOut, "rendezvous TCP connect timed out")
        })??;
    secure(stream, &config.server_key).await
}
pub async fn secure(mut stream: TcpStream, server_key: &str) -> io::Result<Signal> {
    stream.set_nodelay(true)?;
    let key: [u8; 32] = STANDARD
        .decode(server_key)
        .map_err(|_| invalid("invalid rendezvous signing key"))?
        .try_into()
        .map_err(|_| invalid("invalid rendezvous signing key length"))?;
    let verifier =
        VerifyingKey::from_bytes(&key).map_err(|_| invalid("invalid rendezvous signing key"))?;
    // Fail closed. SDP and ICE are never sent on the legacy plaintext path.
    let first: RendezvousMessage = time::timeout(Duration::from_secs(3), read_message(&mut stream))
        .await
        .map_err(|_| {
            io::Error::new(
                io::ErrorKind::TimedOut,
                "rendezvous did not secure signaling",
            )
        })??;
    let Some(rendezvous_message::Union::KeyExchange(ex)) = first.union else {
        return Err(invalid("rendezvous did not provide a key exchange"));
    };
    let signed_pk = verified_exchange(&ex, &verifier)?;
    let picked = ex.version.min(1);
    let mut seed = [0u8; 32];
    OsRng.fill_bytes(&mut seed);
    let secret = SecretKey::from(seed);
    let public = secret.public_key();
    let mut symmetric = [0u8; 32];
    OsRng.fill_bytes(&mut symmetric);
    // The signed high bit is a marker; X25519 ignores it.
    let mut x25519_pk = signed_pk;
    x25519_pk[31] &= 0x7f;
    let sealed = SalsaBox::new(&PublicKey::from(x25519_pk), &secret)
        .encrypt(Nonce::from_slice(&[0u8; 24]), symmetric.as_slice())
        .map_err(|_| invalid("rendezvous key encryption failed"))?;
    let response = RendezvousMessage {
        union: Some(rendezvous_message::Union::KeyExchange(KeyExchange {
            keys: vec![public.as_bytes().to_vec(), sealed],
            version: picked,
            signed_params: Vec::new(),
        })),
    };
    time::timeout(
        Duration::from_secs(3),
        write_message(&mut stream, &response),
    )
    .await
    .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "rendezvous key exchange timed out"))??;
    let keys = SessionKey::negotiated(
        symmetric,
        public.as_bytes(),
        &signed_pk,
        ex.version,
        picked,
        true,
    )?;
    let (reader, writer) = tokio::io::split(stream);
    Ok(Signal {
        _reader: FrameReader::new(reader, Some(keys.clone())),
        writer: FrameWriter::new(writer, Some(keys)),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::{Signer, SigningKey};
    #[test]
    fn signed_parameters_bind_key_version_domain_and_require_marker_signature() {
        let identity = SigningKey::from_bytes(&[9; 32]);
        let sign = |data: &[u8]| [identity.sign(data).to_bytes().as_slice(), data].concat();
        let mut pk = [17u8; 32];
        pk[31] |= 0x80;
        let params = KxParams {
            pk: pk.to_vec(),
            version: 1,
        }
        .encode_to_vec();
        let mut ex = KeyExchange {
            keys: vec![sign(&pk)],
            version: 1,
            signed_params: sign(&[b"rdkx-params".as_slice(), &params].concat()),
        };
        assert_eq!(
            verified_exchange(&ex, &identity.verifying_key()).unwrap(),
            pk
        );
        ex.version = 0;
        assert!(verified_exchange(&ex, &identity.verifying_key()).is_err());
        ex.version = 1;
        ex.signed_params.clear();
        assert!(verified_exchange(&ex, &identity.verifying_key()).is_err());
        ex.signed_params = sign(&[b"wrong-domain".as_slice(), &params].concat());
        assert!(verified_exchange(&ex, &identity.verifying_key()).is_err());
    }

    #[tokio::test]
    async fn secure_signaling_roundtrips_v0_and_v1_with_signed_parameters() {
        use ed25519_dalek::{Signer, SigningKey};
        use tokio::net::TcpListener;
        for advertised in [0, 1] {
            let identity = SigningKey::from_bytes(&[9; 32]);
            let key = STANDARD.encode(identity.verifying_key().as_bytes());
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let address = listener.local_addr().unwrap();
            let responder = tokio::spawn(async move {
                let (mut stream, _) = listener.accept().await.unwrap();
                let secret = SecretKey::from([7; 32]);
                let mut raw = *secret.public_key().as_bytes();
                // New hbbs marks its signed ephemeral key to require the version signature.
                if advertised == 1 {
                    raw[31] |= 0x80;
                }
                let sign =
                    |bytes: &[u8]| [identity.sign(bytes).to_bytes().as_slice(), bytes].concat();
                let params = KxParams {
                    pk: raw.to_vec(),
                    version: advertised,
                }
                .encode_to_vec();
                let ex = KeyExchange {
                    keys: vec![sign(&raw)],
                    version: advertised,
                    signed_params: if advertised == 1 {
                        sign(&[b"rdkx-params".as_slice(), &params].concat())
                    } else {
                        Vec::new()
                    },
                };
                write_message(
                    &mut stream,
                    &RendezvousMessage {
                        union: Some(rendezvous_message::Union::KeyExchange(ex)),
                    },
                )
                .await
                .unwrap();
                let reply: RendezvousMessage = read_message(&mut stream).await.unwrap();
                let Some(rendezvous_message::Union::KeyExchange(reply)) = reply.union else {
                    panic!("no KX")
                };
                let initiator: [u8; 32] = reply.keys[0].clone().try_into().unwrap();
                let master = SalsaBox::new(&PublicKey::from(initiator), &secret)
                    .decrypt(Nonce::from_slice(&[0; 24]), reply.keys[1].as_slice())
                    .unwrap();
                let keys = SessionKey::negotiated(
                    master.try_into().unwrap(),
                    &initiator,
                    &raw,
                    advertised,
                    reply.version,
                    false,
                )
                .unwrap();
                let (r, w) = tokio::io::split(stream);
                let mut reader = FrameReader::new(r, Some(keys.clone()));
                let mut writer = FrameWriter::new(w, Some(keys));
                let message: RendezvousMessage = reader.read().await.unwrap();
                writer.write(&message).await.unwrap();
            });
            let mut signal = secure(TcpStream::connect(address).await.unwrap(), &key)
                .await
                .unwrap();
            let message = RendezvousMessage {
                union: Some(rendezvous_message::Union::IceCandidate(
                    crate::protocol::IceCandidate {
                        id: "peer".into(),
                        socket_addr: vec![1, 2, 3],
                        session_key: "certificate".into(),
                        candidate: "test ICE".into(),
                    },
                )),
            };
            signal.writer.write(&message).await.unwrap();
            assert_eq!(
                signal._reader.read::<RendezvousMessage>().await.unwrap(),
                message
            );
            responder.await.unwrap();
        }
    }
}
