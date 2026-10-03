// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::{
    io,
    net::{IpAddr, Ipv4Addr, SocketAddr, SocketAddrV4},
    sync::Arc,
    time::Duration,
};

use prost::Message as _;
use rand::{distributions::Alphanumeric, Rng};
use tokio::{
    net::{lookup_host, TcpStream, UdpSocket},
    sync::watch,
    time,
};

use crate::{
    config::Config,
    framing::write_message,
    identity::RustDeskIdentity,
    onekvm::Identity,
    protocol::{
        relay_response, rendezvous_message, ConnType, RegisterPeer, RegisterPk, RegisterPkResult,
        RelayResponse, RendezvousMessage, RequestRelay,
    },
    server,
};

const RENDEZVOUS_PORT: u16 = 21116;
const RELAY_PORT: u16 = 21117;
const REGISTER_INTERVAL: Duration = Duration::from_secs(20);
const RETRY_INTERVAL: Duration = Duration::from_secs(3);
const CONNECT_TIMEOUT: Duration = Duration::from_secs(8);

pub async fn run(
    config: Arc<Config>,
    onekvm_identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    mut shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
) -> io::Result<()> {
    let webrtc = crate::webrtc::Manager::default();
    loop {
        let result = run_once(
            Arc::clone(&config),
            Arc::clone(&onekvm_identity),
            Arc::clone(&rustdesk_identity),
            shutdown.clone(),
            limits.clone(),
            webrtc.clone(),
        )
        .await;
        if *shutdown.borrow() {
            return Ok(());
        }
        if let Err(error) = result {
            eprintln!(
                "RustDesk ID server {}: {error}; retrying",
                config.rendezvous_server
            );
        }
        tokio::select! {
            _ = time::sleep(RETRY_INTERVAL) => {}
            _ = shutdown.changed() => return Ok(()),
        }
    }
}

async fn run_once(
    config: Arc<Config>,
    onekvm_identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    mut shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
    webrtc: crate::webrtc::Manager,
) -> io::Result<()> {
    let hbbs = server_address(&config.rendezvous_server, RENDEZVOUS_PORT)?;
    let remote = lookup_host(&hbbs)
        .await?
        .next()
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "ID server did not resolve"))?;
    let bind = if remote.is_ipv4() {
        "0.0.0.0:0"
    } else {
        "[::]:0"
    };
    let socket = UdpSocket::bind(bind).await?;
    socket.connect(remote).await?;
    crate::status::disconnected();
    let mut registered = false;
    let mut last_ack = time::Instant::now();
    let mut timer = time::interval(RETRY_INTERVAL);
    timer.set_missed_tick_behavior(time::MissedTickBehavior::Delay);
    let mut receive_buffer = vec![0_u8; 64 * 1024];

    loop {
        tokio::select! {
            _ = timer.tick() => {
                if registered && last_ack.elapsed() > Duration::from_secs(60) {
                    registered = false; crate::status::disconnected();
                }
                let message = if registered {
                    RendezvousMessage {
                        union: Some(rendezvous_message::Union::RegisterPeer(RegisterPeer {
                            id: rustdesk_identity.id.clone(),
                            serial: 0,
                        })),
                    }
                } else {
                    RendezvousMessage {
                        union: Some(rendezvous_message::Union::RegisterPk(RegisterPk {
                            id: rustdesk_identity.id.clone(),
                            uuid: rustdesk_identity.uuid.clone(),
                            pk: rustdesk_identity.public_key().to_vec(),
                            old_id: String::new(),
                            no_register_device: false,
                        })),
                    }
                };
                socket.send(&message.encode_to_vec()).await?;
            }
            received = socket.recv(&mut receive_buffer) => {
                let length = received?;
                let message = RendezvousMessage::decode(&receive_buffer[..length])
                    .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
                match message.union {
                    Some(rendezvous_message::Union::RegisterPkResponse(response)) => {
                        match RegisterPkResult::try_from(response.result) {
                            Ok(RegisterPkResult::Ok) => {
                                if !registered {
                                    eprintln!("RustDesk ID {} registered with {hbbs}", rustdesk_identity.id);
                                }
                                registered = true;
                                last_ack = time::Instant::now();
                                crate::status::registered();
                                timer = time::interval(REGISTER_INTERVAL);
                            }
                            result => {
                                registered = false;
                                crate::status::disconnected();
                                eprintln!("RustDesk ID registration rejected by {hbbs}: {result:?}");
                            }
                        }
                    }
                    Some(rendezvous_message::Union::RegisterPeerResponse(response)) => {
                        if response.request_pk {
                            registered = false;
                            crate::status::disconnected();
                            timer = time::interval(RETRY_INTERVAL);
                        } else if registered {
                            last_ack = time::Instant::now();
                            crate::status::registered();
                        }
                    }
                    Some(rendezvous_message::Union::RequestRelay(request)) => {
                        let mut limits = limits.clone();
                        limits.input_allowed = request.control_permissions.as_ref().map(|p|p.permissions & 1 != 0).unwrap_or(true);
                        spawn_requested_relay(
                            request,
                            Arc::clone(&config),
                            Arc::clone(&onekvm_identity),
                            Arc::clone(&rustdesk_identity),
                            shutdown.clone(),
                            limits.clone(),
                        );
                    }
                    Some(rendezvous_message::Union::IceCandidate(candidate)) => {
                        webrtc.candidate(candidate);
                    }
                    Some(rendezvous_message::Union::PunchHole(request)) => {
                        let mut limits = limits.clone();
                        limits.input_allowed = request.control_permissions.as_ref().map(|p|p.permissions & 1 != 0).unwrap_or(true);
                        if request.webrtc_sdp_offer.is_empty() {
                            spawn_fallback_relay(request.socket_addr, request.socket_addr_v6, request.relay_server,
                                Arc::clone(&config), Arc::clone(&onekvm_identity), Arc::clone(&rustdesk_identity),
                                shutdown.clone(), limits.clone());
                        } else {
                            let manager = webrtc.clone();
                            let config = Arc::clone(&config);
                            let onekvm = Arc::clone(&onekvm_identity);
                            let rd = Arc::clone(&rustdesk_identity);
                            let shutdown = shutdown.clone();
                            let limits = limits.clone();
                            tokio::spawn(async move {
                                if let Err(error) = manager.answer(&request, Arc::clone(&config), Arc::clone(&onekvm),
                                    Arc::clone(&rd), shutdown.clone(), limits.clone()).await {
                                    eprintln!("RustDesk WebRTC unavailable: {error}; using relay");
                                    spawn_fallback_relay(request.socket_addr, request.socket_addr_v6, request.relay_server,
                                        config, onekvm, rd, shutdown, limits);
                                }
                            });
                        }
                    }
                    Some(rendezvous_message::Union::FetchLocalAddr(request)) => {
                        spawn_fallback_relay(
                            request.socket_addr,
                            request.socket_addr_v6,
                            request.relay_server,
                            Arc::clone(&config),
                            Arc::clone(&onekvm_identity),
                            Arc::clone(&rustdesk_identity),
                            shutdown.clone(),
                            limits.clone(),
                        );
                    }
                    _ => {}
                }
            }
            _ = shutdown.changed() => return Ok(()),
        }
    }
}

fn spawn_requested_relay(
    request: RequestRelay,
    config: Arc<Config>,
    onekvm_identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
) {
    tokio::spawn(async move {
        if request.conn_type != ConnType::DefaultConn as i32 {
            eprintln!(
                "rejecting unsupported RustDesk relay session type {}",
                request.conn_type
            );
            return;
        }
        let relay = relay_address(&config, &request.relay_server);
        let peer = decode_address(&request.socket_addr);
        let result = async {
            let hbbs = server_address(&config.rendezvous_server, RENDEZVOUS_PORT)?;
            let response = RendezvousMessage {
                union: Some(rendezvous_message::Union::RelayResponse(RelayResponse {
                    socket_addr: request.socket_addr,
                    uuid: String::new(),
                    relay_server: String::new(),
                    union: None,
                    refuse_reason: String::new(),
                    version: crate::upstream::version().to_owned(),
                    feedback: 0,
                    socket_addr_v6: Vec::new(),
                    upnp_port: 0,
                    webrtc_sdp_answer: String::new(),
                })),
            };
            let mut stream = time::timeout(CONNECT_TIMEOUT, TcpStream::connect(&hbbs))
                .await
                .map_err(|_| {
                    io::Error::new(io::ErrorKind::TimedOut, "ID server connect timed out")
                })??;
            write_message(&mut stream, &response).await?;
            connect_relay(
                relay,
                request.uuid,
                peer,
                request.secure,
                config,
                onekvm_identity,
                rustdesk_identity,
                shutdown,
                limits,
            )
            .await
        }
        .await;
        if let Err(error) = result {
            eprintln!("RustDesk requested relay failed: {error}");
        }
    });
}

#[allow(clippy::too_many_arguments)]
fn spawn_fallback_relay(
    socket_addr: Vec<u8>,
    socket_addr_v6: Vec<u8>,
    offered_relay: String,
    config: Arc<Config>,
    onekvm_identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
) {
    tokio::spawn(async move {
        let uuid: String = rand::thread_rng()
            .sample_iter(Alphanumeric)
            .take(32)
            .map(char::from)
            .collect();
        let relay = relay_address(&config, &offered_relay);
        let peer = decode_address(&socket_addr);
        let response = RendezvousMessage {
            union: Some(rendezvous_message::Union::RelayResponse(RelayResponse {
                socket_addr,
                uuid: uuid.clone(),
                relay_server: relay.clone(),
                union: Some(relay_response::Union::Id(rustdesk_identity.id.clone())),
                refuse_reason: String::new(),
                version: crate::upstream::version().to_owned(),
                feedback: 0,
                socket_addr_v6,
                upnp_port: 0,
                webrtc_sdp_answer: String::new(),
            })),
        };
        let hbbs = match server_address(&config.rendezvous_server, RENDEZVOUS_PORT) {
            Ok(address) => address,
            Err(error) => {
                eprintln!("RustDesk fallback relay response failed: {error}");
                return;
            }
        };
        let result = async {
            let mut stream = time::timeout(CONNECT_TIMEOUT, TcpStream::connect(&hbbs))
                .await
                .map_err(|_| {
                    io::Error::new(io::ErrorKind::TimedOut, "ID server connect timed out")
                })??;
            write_message(&mut stream, &response).await?;
            connect_relay(
                relay,
                uuid,
                peer,
                true,
                config,
                onekvm_identity,
                rustdesk_identity,
                shutdown,
                limits,
            )
            .await
        }
        .await;
        if let Err(error) = result {
            eprintln!("RustDesk fallback relay failed: {error}");
        }
    });
}

#[allow(clippy::too_many_arguments)]
async fn connect_relay(
    relay: String,
    uuid: String,
    peer: SocketAddr,
    secure: bool,
    config: Arc<Config>,
    onekvm_identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
) -> io::Result<()> {
    let pending_permit = limits
        .try_pending()
        .ok_or_else(|| io::Error::new(io::ErrorKind::WouldBlock, "too many pending handshakes"))?;
    let mut stream = time::timeout(CONNECT_TIMEOUT, TcpStream::connect(&relay))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "relay connect timed out"))??;
    write_message(
        &mut stream,
        &RendezvousMessage {
            union: Some(rendezvous_message::Union::RequestRelay(RequestRelay {
                id: String::new(),
                uuid,
                socket_addr: Vec::new(),
                relay_server: String::new(),
                secure: false,
                licence_key: config.server_key.clone(),
                conn_type: ConnType::DefaultConn as i32,
                token: String::new(),
                ..Default::default()
            })),
        },
    )
    .await?;
    eprintln!("RustDesk client {peer} connected through relay {relay}");
    server::serve(
        stream,
        peer,
        config,
        onekvm_identity,
        rustdesk_identity,
        secure,
        shutdown,
        limits,
        pending_permit,
    )
    .await
}

fn relay_address(config: &Config, offered: &str) -> String {
    let configured = config.relay_server.trim();
    if !configured.is_empty() {
        return server_address(configured, RELAY_PORT).unwrap_or_else(|_| configured.to_owned());
    }
    if !offered.trim().is_empty() {
        return server_address(offered.trim(), RELAY_PORT).unwrap_or_else(|_| offered.to_owned());
    }
    increment_port(&config.rendezvous_server).unwrap_or_else(|| {
        server_address(&config.rendezvous_server, RELAY_PORT)
            .unwrap_or_else(|_| config.rendezvous_server.clone())
    })
}

fn increment_port(value: &str) -> Option<String> {
    let address = server_address(value, RENDEZVOUS_PORT).ok()?;
    if let Ok(mut socket) = address.parse::<SocketAddr>() {
        socket.set_port(socket.port().checked_add(1)?);
        return Some(socket.to_string());
    }
    let (host, port) = address.rsplit_once(':')?;
    let port = port.parse::<u16>().ok()?.checked_add(1)?;
    Some(format!("{host}:{port}"))
}

pub(crate) fn server_address(value: &str, default_port: u16) -> io::Result<String> {
    let value = value.trim();
    if value.is_empty() || value.contains("//") || value.chars().any(char::is_whitespace) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "server must be a hostname or IP address with an optional port",
        ));
    }
    if value.parse::<SocketAddr>().is_ok() {
        return Ok(value.to_owned());
    }
    if value.starts_with('[') && value.ends_with(']') {
        return Ok(format!("{value}:{default_port}"));
    }
    if value.matches(':').count() > 1 {
        return Ok(format!("[{value}]:{default_port}"));
    }
    if let Some((host, port)) = value.rsplit_once(':') {
        if !host.is_empty() && port.parse::<u16>().is_ok() {
            return Ok(value.to_owned());
        }
    }
    Ok(format!("{value}:{default_port}"))
}

pub(crate) fn decode_address(bytes: &[u8]) -> SocketAddr {
    if bytes.len() == 18 {
        let mut ip = [0_u8; 16];
        ip.copy_from_slice(&bytes[..16]);
        let port = u16::from_le_bytes([bytes[16], bytes[17]]);
        return SocketAddr::new(IpAddr::from(ip), port);
    }
    if bytes.len() > 16 {
        return SocketAddr::from(([0, 0, 0, 0], 0));
    }
    let mut padded = [0_u8; 16];
    padded[..bytes.len()].copy_from_slice(bytes);
    let number = u128::from_le_bytes(padded);
    let timestamp = (number >> 17) & u128::from(u32::MAX);
    let encoded_ip = number >> 49;
    let encoded_port = number & 0xFF_FFFF;
    let Some(ip) = encoded_ip.checked_sub(timestamp) else {
        return SocketAddr::from(([0, 0, 0, 0], 0));
    };
    let Some(port) = encoded_port.checked_sub(timestamp & 0xFFFF) else {
        return SocketAddr::from(([0, 0, 0, 0], 0));
    };
    SocketAddr::V4(SocketAddrV4::new(
        Ipv4Addr::from((ip as u32).to_le_bytes()),
        port as u16,
    ))
}

#[cfg(test)]
mod tests {
    use std::{net::SocketAddr, time::Duration};

    use base64::{engine::general_purpose::STANDARD, Engine as _};
    use crypto_box::{
        aead::Aead as _, Nonce as BoxNonce, PublicKey as BoxPublicKey, SalsaBox,
        SecretKey as BoxSecretKey,
    };
    use ed25519_dalek::{Signature, Verifier, VerifyingKey};
    use prost::Message as _;
    use rand::RngCore;
    use sha2::{Digest, Sha256};
    use tokio::time;

    use crate::{
        framing::{read_message, write_message, FrameReader, FrameWriter, SessionKey},
        identity::RustDeskIdentity,
        protocol::{
            login_response, message, rendezvous_message, video_frame, ConnType, IdPk, LoginRequest,
            Message, PublicKey, RendezvousMessage, RequestRelay,
        },
    };

    use super::{decode_address, server_address, CONNECT_TIMEOUT};

    #[test]
    fn adds_default_server_ports() {
        assert_eq!(
            server_address("hbbs.example", 21116).unwrap(),
            "hbbs.example:21116"
        );
        assert_eq!(
            server_address("hbbs.example:22000", 21116).unwrap(),
            "hbbs.example:22000"
        );
        assert_eq!(
            server_address("2001:db8::1", 21116).unwrap(),
            "[2001:db8::1]:21116"
        );
    }

    #[test]
    fn decodes_rustdesk_mangled_ipv4_address() {
        let expected: SocketAddr = "192.0.2.5:45000".parse().unwrap();
        let timestamp = 0x1234_5678_u128;
        let ip = u128::from(u32::from_le_bytes([192, 0, 2, 5]));
        let encoded = ((ip + timestamp) << 49)
            | (timestamp << 17)
            | (u128::from(expected.port()) + (timestamp & 0xffff));
        let bytes = encoded.to_le_bytes();
        assert_eq!(decode_address(&bytes[..13]), expected);
    }

    #[tokio::test]
    #[ignore = "requires official hbbs/hbbr and a running target extension"]
    async fn official_hbbs_hbbr_secure_interop() {
        let hbbs = std::env::var("RUSTDESK_TEST_HBBS").unwrap();
        let relay_server = std::env::var("RUSTDESK_TEST_RELAY").unwrap();
        let target_id = std::env::var("RUSTDESK_TEST_ID").unwrap();
        let configured_key = std::env::var("RUSTDESK_TEST_KEY").unwrap();
        let public_key = if let Ok(encoded) = std::env::var("RUSTDESK_TEST_PUBLIC_KEY") {
            STANDARD.decode(encoded).unwrap().try_into().unwrap()
        } else {
            let target_data = std::env::var("RUSTDESK_TEST_DATA").unwrap();
            let target_identity = RustDeskIdentity::load_from(target_data.as_ref()).unwrap();
            assert_eq!(target_identity.id, target_id);
            target_identity.public_key()
        };
        let device_key = VerifyingKey::from_bytes(&public_key).unwrap();
        let uuid = format!("{:032x}", rand::random::<u128>());
        let mut hbbs_stream = time::timeout(CONNECT_TIMEOUT, tokio::net::TcpStream::connect(&hbbs))
            .await
            .unwrap()
            .unwrap();
        write_message(
            &mut hbbs_stream,
            &RendezvousMessage {
                union: Some(rendezvous_message::Union::RequestRelay(RequestRelay {
                    id: target_id.clone(),
                    uuid: uuid.clone(),
                    socket_addr: Vec::new(),
                    relay_server: relay_server.clone(),
                    secure: true,
                    licence_key: configured_key.clone(),
                    conn_type: ConnType::DefaultConn as i32,
                    token: String::new(),
                    ..Default::default()
                })),
            },
        )
        .await
        .unwrap();
        let relay = time::timeout(CONNECT_TIMEOUT, async {
            loop {
                let response: RendezvousMessage = read_message(&mut hbbs_stream).await.unwrap();
                if let Some(rendezvous_message::Union::RelayResponse(relay)) = response.union {
                    break relay;
                }
            }
        })
        .await
        .expect("hbbs did not approve the relay request");
        assert!(relay.refuse_reason.is_empty(), "{}", relay.refuse_reason);

        let mut stream = time::timeout(
            CONNECT_TIMEOUT,
            tokio::net::TcpStream::connect(&relay_server),
        )
        .await
        .unwrap()
        .unwrap();
        write_message(
            &mut stream,
            &RendezvousMessage {
                union: Some(rendezvous_message::Union::RequestRelay(RequestRelay {
                    id: target_id.clone(),
                    uuid,
                    socket_addr: Vec::new(),
                    relay_server: String::new(),
                    secure: false,
                    licence_key: configured_key,
                    conn_type: ConnType::DefaultConn as i32,
                    token: String::new(),
                    ..Default::default()
                })),
            },
        )
        .await
        .unwrap();

        let signed: Message = time::timeout(CONNECT_TIMEOUT, read_message(&mut stream))
            .await
            .unwrap()
            .unwrap();
        let Some(message::Union::SignedId(signed)) = signed.union else {
            panic!("target did not start the secure handshake");
        };
        let ephemeral = IdPk::decode(open_signed(&signed.id, &device_key).as_slice()).unwrap();
        assert_eq!(ephemeral.id, target_id);

        let mut secret_bytes = [0_u8; 32];
        let mut session_bytes = [0_u8; 32];
        rand::thread_rng().fill_bytes(&mut secret_bytes);
        rand::thread_rng().fill_bytes(&mut session_bytes);
        let secret = BoxSecretKey::from(secret_bytes);
        let public = secret.public_key();
        let remote_public: [u8; 32] = ephemeral.pk.try_into().unwrap();
        let cipher = SalsaBox::new(&BoxPublicKey::from(remote_public), &secret);
        let sealed = cipher
            .encrypt(BoxNonce::from_slice(&[0_u8; 24]), session_bytes.as_slice())
            .unwrap();
        write_message(
            &mut stream,
            &Message {
                union: Some(message::Union::PublicKey(PublicKey {
                    asymmetric_value: public.as_bytes().to_vec(),
                    symmetric_value: sealed,
                    kx_version: 0,
                })),
            },
        )
        .await
        .unwrap();

        let (reader, writer) = stream.into_split();
        let key = SessionKey::new(session_bytes);
        let mut reader = FrameReader::new(reader, Some(key.clone()));
        let mut writer = FrameWriter::new(writer, Some(key));
        let challenge: Message = reader.read().await.unwrap();
        let Some(message::Union::Hash(hash)) = challenge.union else {
            panic!("encrypted Hash challenge was not received");
        };
        let password = std::env::var("RUSTDESK_TEST_PASSWORD").ok();
        let mut first = Sha256::new();
        first.update(
            password
                .as_deref()
                .unwrap_or("deliberately-wrong")
                .as_bytes(),
        );
        first.update(hash.salt.as_bytes());
        let mut second = Sha256::new();
        second.update(first.finalize());
        second.update(hash.challenge.as_bytes());
        writer
            .write(&Message {
                union: Some(message::Union::LoginRequest(LoginRequest {
                    password: second.finalize().to_vec(),
                    my_id: "interop-probe".to_owned(),
                    my_name: "OneKVM interop probe".to_owned(),
                    version: crate::upstream::version().to_owned(),
                    my_platform: "Linux".to_owned(),
                    ..Default::default()
                })),
            })
            .await
            .unwrap();
        let response: Message = reader.read().await.unwrap();
        let Some(message::Union::LoginResponse(response)) = response.union else {
            panic!("encrypted LoginResponse was not received");
        };
        if password.is_some() {
            let Some(login_response::Union::PeerInfo(peer)) = response.union else {
                panic!("target rejected the configured test password");
            };
            assert!(!peer.displays.is_empty());
            assert!(peer.displays[0].width > 0 && peer.displays[0].height > 0);

            let video: Message = time::timeout(Duration::from_secs(10), reader.read())
                .await
                .expect("target did not send a video frame")
                .unwrap();
            let Some(message::Union::VideoFrame(video)) = video.union else {
                panic!("target did not send VideoFrame after successful login");
            };
            let Some(video_frame::Union::H264s(frames)) = video.union else {
                panic!("target did not send H.264 video");
            };
            assert!(!frames.frames.is_empty());
            assert!(frames.frames.iter().all(|frame| !frame.data.is_empty()));
        } else {
            assert!(matches!(
                response.union,
                Some(login_response::Union::Error(error)) if error == "Wrong Password"
            ));
        }
    }

    fn open_signed(signed: &[u8], key: &VerifyingKey) -> Vec<u8> {
        assert!(signed.len() >= 64);
        let signature = Signature::from_slice(&signed[..64]).unwrap();
        key.verify(&signed[64..], &signature).unwrap();
        signed[64..].to_vec()
    }
}
