// RustDesk 1.5 classic direct TCP rendezvous. SPDX-License-Identifier: AGPL-3.0-only
use crate::{
    config::Config,
    identity::RustDeskIdentity,
    onekvm::Identity,
    protocol::{rendezvous_message, LocalAddr, NatType, PunchHoleSent, RendezvousMessage},
    rendezvous, server,
};
use std::{
    io,
    net::SocketAddr,
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};
use tokio::{
    net::{lookup_host, TcpSocket, TcpStream},
    sync::watch,
    time,
};

const SETUP: Duration = Duration::from_secs(8);
fn socket(addr: SocketAddr) -> io::Result<TcpSocket> {
    let s = if addr.is_ipv4() {
        TcpSocket::new_v4()?
    } else {
        TcpSocket::new_v6()?
    };
    s.set_reuseaddr(true)?;
    #[cfg(target_os = "linux")]
    s.set_reuseport(true)?;
    Ok(s)
}
async fn connect_local(
    peer: SocketAddr,
    local: SocketAddr,
    timeout: Duration,
) -> io::Result<TcpStream> {
    let s = socket(local)?;
    s.bind(local)?;
    time::timeout(timeout, s.connect(peer))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "TCP connect timed out"))?
}
pub(crate) fn encode_address(addr: SocketAddr) -> Vec<u8> {
    // Canonical hbb_common AddrMangle encoding; this is obfuscation, not a secret.
    match addr {
        SocketAddr::V4(a) => {
            let t = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or_default()
                .as_micros() as u32 as u128;
            let ip = u32::from_le_bytes(a.ip().octets()) as u128;
            let n = ((ip + t) << 49) | (t << 17) | (u128::from(a.port()) + (t & 0xffff));
            let b = n.to_le_bytes();
            let end = b.iter().rposition(|x| *x != 0).map_or(1, |i| i + 1);
            b[..end].to_vec()
        }
        SocketAddr::V6(a) => {
            let mut b = a.ip().octets().to_vec();
            b.extend_from_slice(&a.port().to_le_bytes());
            b
        }
    }
}
#[derive(Clone)]
pub(crate) struct Request {
    pub peer: Vec<u8>,
    pub peer_v6: Vec<u8>,
    pub relay: String,
    pub lan: bool,
}
#[allow(clippy::too_many_arguments)]
pub(crate) fn spawn(
    request: Request,
    config: Arc<Config>,
    onekvm: Arc<Identity>,
    rd: Arc<RustDeskIdentity>,
    mut shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
) {
    // Acquire before spawning: the cap includes network setup, listeners and authentication.
    let Some(pending) = limits.try_pending() else {
        return;
    };
    tokio::spawn(async move {
        if *shutdown.borrow() {
            return;
        }
        let connection = tokio::select! {
         r=time::timeout(Duration::from_secs(20),establish(&request,&config,&rd))=>r.unwrap_or_else(|_|Err(io::Error::new(io::ErrorKind::TimedOut,"direct TCP setup expired"))),
         _=shutdown.changed()=>return,
        };
        match connection {
            Ok((stream, peer)) => {
                if let Err(error) = server::serve(
                    stream, peer, config, onekvm, rd, true, shutdown, limits, pending,
                )
                .await
                {
                    eprintln!("RustDesk direct TCP ended: {error}")
                }
            }
            Err(error) => {
                drop(pending);
                if *shutdown.borrow() {
                    return;
                }
                eprintln!("RustDesk direct TCP unavailable: {error}; using relay");
                rendezvous::spawn_fallback_relay(
                    request.peer,
                    request.peer_v6,
                    request.relay,
                    config,
                    onekvm,
                    rd,
                    shutdown,
                    limits,
                );
            }
        }
    });
}
async fn establish(
    request: &Request,
    config: &Config,
    rd: &RustDeskIdentity,
) -> io::Result<(TcpStream, SocketAddr)> {
    let peer = rendezvous::decode_address(&request.peer);
    if peer.port() == 0 || peer.ip().is_unspecified() || peer.ip().is_multicast() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid direct peer",
        ));
    }
    let hbbs = rendezvous::server_address(&config.rendezvous_server, 21116)?;
    let remote = time::timeout(SETUP, lookup_host(hbbs))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "ID resolution timed out"))??
        .find(|a| a.is_ipv4() == peer.is_ipv4())
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "no matching ID address family"))?;
    let initial = if remote.is_ipv4() {
        SocketAddr::from(([0, 0, 0, 0], 0))
    } else {
        "[::]:0".parse().unwrap()
    };
    let channel = socket(initial)?;
    channel.bind(initial)?;
    let mut channel = time::timeout(SETUP, channel.connect(remote))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "ID connect timed out"))??;
    let local = channel.local_addr()?;
    let listen = socket(local)?;
    listen.bind(local)?;
    let listener = listen.listen(2)?;
    let relay = rendezvous::relay_address(config, &request.relay);
    let message = if request.lan {
        RendezvousMessage {
            union: Some(rendezvous_message::Union::LocalAddr(LocalAddr {
                socket_addr: request.peer.clone(),
                local_addr: encode_address(local),
                relay_server: relay,
                id: rd.id.clone(),
                version: crate::upstream::version().into(),
                socket_addr_v6: Vec::new(),
            })),
        }
    } else {
        RendezvousMessage {
            union: Some(rendezvous_message::Union::PunchHoleSent(PunchHoleSent {
                socket_addr: request.peer.clone(),
                id: rd.id.clone(),
                relay_server: relay,
                nat_type: NatType::Unknown as i32,
                version: crate::upstream::version().into(),
                upnp_port: 0,
                socket_addr_v6: Vec::new(),
                webrtc_sdp_answer: String::new(),
            })),
        }
    };
    // Open the outward NAT mapping before announcing it. A successful crossing is
    // the actual connection and must be retained rather than reset as a probe.
    let crossed = if request.lan {
        None
    } else {
        connect_local(peer, local, Duration::from_millis(30))
            .await
            .ok()
    };
    time::timeout(SETUP, crate::framing::write_message(&mut channel, &message))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "ID response timed out"))??;
    drop(channel);
    if let Some(stream) = crossed {
        return Ok((stream, peer));
    }
    let until = time::Instant::now() + SETUP;
    let accept = async {
        loop {
            match listener.accept().await {
                Ok(v) => return Ok(v),
                Err(_) => time::sleep(Duration::from_millis(100)).await,
            }
        }
    };
    let outbound = async {
        if request.lan {
            return std::future::pending::<io::Result<(TcpStream, SocketAddr)>>().await;
        }
        let mut delay = Duration::from_millis(100);
        loop {
            time::sleep(delay).await;
            if let Ok(stream) = connect_local(peer, local, Duration::from_millis(250)).await {
                return Ok((stream, peer));
            }
            delay = (delay * 2).min(Duration::from_millis(800));
        }
    };
    time::timeout_at(until, async {
        tokio::select! {biased;r=outbound=>r,r=accept=>r}
    })
    .await
    .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "direct TCP window expired"))?
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn canonical_address_roundtrip() {
        for a in ["127.0.0.1:5555", "192.0.2.9:21118", "[2001:db8::1]:9000"] {
            let a: SocketAddr = a.parse().unwrap();
            assert_eq!(rendezvous::decode_address(&encode_address(a)), a)
        }
    }
    #[tokio::test]
    async fn reused_id_port_accepts_a_direct_peer() {
        let hbbs = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = hbbs.local_addr().unwrap();
        let expected = "127.0.0.1:3456".parse().unwrap();
        let req = Request {
            peer: encode_address(expected),
            peer_v6: Vec::new(),
            relay: String::new(),
            lan: true,
        };
        let config = Config {
            rendezvous_server: address.to_string(),
            ..Config::default()
        };
        let root = std::env::temp_dir().join(format!(
            "rd-direct-{}-{}",
            std::process::id(),
            rand::random::<u64>()
        ));
        let id = RustDeskIdentity::load_from(&root).unwrap();
        std::fs::remove_dir_all(&root).unwrap();
        let client = tokio::spawn(async move {
            let (mut stream, _) = hbbs.accept().await.unwrap();
            let msg: RendezvousMessage = crate::framing::read_message(&mut stream).await.unwrap();
            let Some(rendezvous_message::Union::LocalAddr(addr)) = msg.union else {
                panic!("missing LocalAddr")
            };
            assert_eq!(rendezvous::decode_address(&addr.socket_addr), expected);
            let mut peer = TcpStream::connect(rendezvous::decode_address(&addr.local_addr))
                .await
                .unwrap();
            tokio::io::AsyncWriteExt::write_all(&mut peer, b"direct")
                .await
                .unwrap();
        });
        let (mut stream, _) = establish(&req, &config, &id).await.unwrap();
        let mut data = [0; 6];
        tokio::io::AsyncReadExt::read_exact(&mut stream, &mut data)
            .await
            .unwrap();
        assert_eq!(&data, b"direct");
        client.await.unwrap();
    }
    #[tokio::test]
    async fn successful_outbound_crossing_is_kept_and_not_reset() {
        let hbbs = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let peer_listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let peer_addr = peer_listener.local_addr().unwrap();
        let request = Request {
            peer: encode_address(peer_addr),
            peer_v6: Vec::new(),
            relay: String::new(),
            lan: false,
        };
        let config = Config {
            rendezvous_server: hbbs.local_addr().unwrap().to_string(),
            ..Config::default()
        };
        let root = std::env::temp_dir().join(format!("rd-direct-cross-{}", rand::random::<u64>()));
        let id = RustDeskIdentity::load_from(&root).unwrap();
        std::fs::remove_dir_all(&root).unwrap();
        let client = tokio::spawn(async move {
            let (mut signal, _) = hbbs.accept().await.unwrap();
            let (mut connection, _) = peer_listener.accept().await.unwrap();
            let msg: RendezvousMessage = crate::framing::read_message(&mut signal).await.unwrap();
            let Some(rendezvous_message::Union::PunchHoleSent(sent)) = msg.union else {
                panic!("missing TCP punch")
            };
            assert!(sent.webrtc_sdp_answer.is_empty());
            assert_eq!(sent.nat_type, NatType::Unknown as i32);
            tokio::io::AsyncWriteExt::write_all(&mut connection, b"kept")
                .await
                .unwrap();
        });
        let (mut stream, peer) =
            time::timeout(Duration::from_secs(2), establish(&request, &config, &id))
                .await
                .unwrap()
                .unwrap();
        assert_eq!(peer, peer_addr);
        let mut bytes = [0; 4];
        tokio::io::AsyncReadExt::read_exact(&mut stream, &mut bytes)
            .await
            .unwrap();
        assert_eq!(&bytes, b"kept");
        client.await.unwrap();
    }
    #[tokio::test]
    #[ignore = "requires an explicitly configured own NanoKVM target and coordinated device slot"]
    async fn own_device_direct_tcp_encrypted_video() {
        use crate::{
            framing::{read_message, write_message, FrameReader, FrameWriter, SessionKey},
            protocol::{self, login_response, message, video_frame},
        };
        use base64::{engine::general_purpose::STANDARD, Engine as _};
        use crypto_box::{aead::Aead as _, Nonce, PublicKey as BoxPublic, SalsaBox, SecretKey};
        use ed25519_dalek::{Signature, Verifier, VerifyingKey};
        use prost::Message as _;
        use rand::RngCore;
        use sha2::{Digest, Sha256};
        let target = std::env::var("RUSTDESK_TEST_ID").unwrap();
        let password = std::env::var("RUSTDESK_TEST_PASSWORD").unwrap();
        let server_key = std::env::var("RUSTDESK_TEST_KEY").unwrap();
        let public: [u8; 32] = STANDARD
            .decode(std::env::var("RUSTDESK_TEST_PUBLIC_KEY").unwrap())
            .unwrap()
            .try_into()
            .unwrap();
        let verifier = VerifyingKey::from_bytes(&public).unwrap();
        let hbbs = std::env::var("RUSTDESK_TEST_HBBS").unwrap();
        let remote = lookup_host(&hbbs)
            .await
            .unwrap()
            .find(|a| a.is_ipv4())
            .unwrap();
        let initial = SocketAddr::from(([0, 0, 0, 0], 0));
        let tcp = socket(initial).unwrap();
        tcp.bind(initial).unwrap();
        let channel = time::timeout(SETUP, tcp.connect(remote))
            .await
            .unwrap()
            .unwrap();
        let local = channel.local_addr().unwrap();
        let mut signal = crate::signaling::secure(channel, &server_key)
            .await
            .unwrap();
        signal
            .writer
            .write(&RendezvousMessage {
                union: Some(rendezvous_message::Union::PunchHoleRequest(
                    protocol::PunchHoleRequest {
                        id: target.clone(),
                        nat_type: NatType::Unknown as i32,
                        licence_key: server_key,
                        conn_type: protocol::ConnType::DefaultConn as i32,
                        version: crate::upstream::version().into(),
                        ..Default::default()
                    },
                )),
            })
            .await
            .unwrap();
        let reply: RendezvousMessage = time::timeout(SETUP, signal._reader.read())
            .await
            .unwrap()
            .unwrap();
        let Some(rendezvous_message::Union::PunchHoleResponse(reply)) = reply.union else {
            panic!("target did not offer direct TCP")
        };
        let peer = rendezvous::decode_address(&reply.socket_addr);
        assert_ne!(peer.port(), 0);
        assert!(!reply.is_udp);
        assert!(reply.webrtc_sdp_answer.is_empty());
        drop(signal);
        let mut channel = connect_local(peer, local, SETUP).await.unwrap();
        channel.set_nodelay(true).unwrap();
        let first: protocol::Message = time::timeout(SETUP, read_message(&mut channel))
            .await
            .unwrap()
            .unwrap();
        let Some(message::Union::SignedId(signed)) = first.union else {
            panic!("missing signed TCP identity")
        };
        let signature = Signature::from_slice(&signed.id[..64]).unwrap();
        verifier.verify(&signed.id[64..], &signature).unwrap();
        let identity = protocol::IdPk::decode(&signed.id[64..]).unwrap();
        assert_eq!(identity.id, target);
        assert_eq!(identity.kx_version, 1);
        let remote_key: [u8; 32] = identity.pk.try_into().unwrap();
        let mut seed = [0; 32];
        let mut symmetric = [0; 32];
        rand::thread_rng().fill_bytes(&mut seed);
        rand::thread_rng().fill_bytes(&mut symmetric);
        let secret = SecretKey::from(seed);
        let public = secret.public_key();
        let sealed = SalsaBox::new(&BoxPublic::from(remote_key), &secret)
            .encrypt(Nonce::from_slice(&[0; 24]), symmetric.as_slice())
            .unwrap();
        write_message(
            &mut channel,
            &protocol::Message {
                union: Some(message::Union::PublicKey(protocol::PublicKey {
                    asymmetric_value: public.as_bytes().to_vec(),
                    symmetric_value: sealed,
                    kx_version: 1,
                })),
            },
        )
        .await
        .unwrap();
        let key =
            SessionKey::negotiated(symmetric, public.as_bytes(), &remote_key, 1, 1, true).unwrap();
        let (read, write) = channel.into_split();
        let mut read = FrameReader::new(read, Some(key.clone()));
        let mut write = FrameWriter::new(write, Some(key));
        let challenge: protocol::Message = read.read().await.unwrap();
        let Some(message::Union::Hash(hash)) = challenge.union else {
            panic!("missing encrypted challenge")
        };
        let mut first = Sha256::new();
        first.update(password);
        first.update(hash.salt);
        let mut second = Sha256::new();
        second.update(first.finalize());
        second.update(hash.challenge);
        write
            .write(&protocol::Message {
                union: Some(message::Union::LoginRequest(protocol::LoginRequest {
                    password: second.finalize().to_vec(),
                    my_id: "bounded-direct-probe".into(),
                    my_name: "NanoKVM direct qualification".into(),
                    version: crate::upstream::version().into(),
                    session_id: rand::random::<u64>(),
                    option: Some(protocol::OptionMessage {
                        disable_keyboard: 2,
                        supported_decoding: Some(protocol::SupportedDecoding {
                            ability_h264: 1,
                            ..Default::default()
                        }),
                        ..Default::default()
                    }),
                    ..Default::default()
                })),
            })
            .await
            .unwrap();
        let login: protocol::Message = read.read().await.unwrap();
        let Some(message::Union::LoginResponse(response)) = login.union else {
            panic!("no login response")
        };
        assert!(matches!(
            response.union,
            Some(login_response::Union::PeerInfo(_))
        ));
        let start = time::Instant::now();
        let until = start + Duration::from_secs(20);
        let mut frames = 0;
        let mut bytes = 0;
        loop {
            let msg: protocol::Message = match time::timeout_at(until, read.read()).await {
                Ok(Ok(m)) => m,
                Err(_) => break,
                Ok(Err(e)) => panic!("direct stream failed: {e}"),
            };
            match msg.union {
                Some(message::Union::VideoFrame(video)) => {
                    let Some(video_frame::Union::H264s(data)) = video.union else {
                        panic!("not H264")
                    };
                    frames += data.frames.len();
                    bytes += data.frames.iter().map(|f| f.data.len()).sum::<usize>();
                }
                Some(message::Union::TestDelay(delay)) => {
                    write
                        .write(&protocol::Message {
                            union: Some(message::Union::TestDelay(delay)),
                        })
                        .await
                        .unwrap();
                }
                _ => {}
            }
        }
        assert!(frames > 100);
        eprintln!(
            "DIRECT_QUALIFICATION {}",
            serde_json::json!({"transport":"encrypted direct TCP","kx_version":1,"view_only":true,"seconds":20,"video_frames":frames,"received_frame_rate":frames as f64/20.0,"video_bytes":bytes})
        );
    }
}
