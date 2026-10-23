// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::{
    io,
    net::{IpAddr, Ipv4Addr, SocketAddr, SocketAddrV4},
    sync::{Arc, Mutex},
    time::Duration,
};

use prost::Message as _;
use rand::{distr::Alphanumeric, RngExt};
use tokio::{
    net::{lookup_host, TcpStream, UdpSocket},
    sync::watch,
    time,
};

use crate::{
    config::Config,
    framing::write_message,
    identity::RustDeskIdentity,
    nanokvm::Identity,
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
const MAX_RETRY_INTERVAL: Duration = Duration::from_secs(60);
const CONNECT_TIMEOUT: Duration = Duration::from_secs(8);

pub async fn run(
    config: Arc<Config>,
    app_identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    mut shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
) -> io::Result<()> {
    let webrtc = crate::webrtc::Manager::default();
    // An unreachable ID server (no network, wrong name) is retried with a
    // growing delay, and the same failure is logged once, not every retry.
    let mut delay = RETRY_INTERVAL;
    let mut last_error = String::new();
    loop {
        let started = time::Instant::now();
        let result = run_once(
            Arc::clone(&config),
            Arc::clone(&app_identity),
            Arc::clone(&rustdesk_identity),
            shutdown.clone(),
            limits.clone(),
            webrtc.clone(),
        )
        .await;
        if *shutdown.borrow() {
            return Ok(());
        }
        // A connection that lasted counts as recovered: start over.
        if started.elapsed() >= MAX_RETRY_INTERVAL {
            delay = RETRY_INTERVAL;
            last_error.clear();
        }
        if let Err(error) = result {
            let error = error.to_string();
            if error != last_error {
                eprintln!(
                    "RustDesk ID server {}: {error}; retrying",
                    config.rendezvous_server
                );
                last_error = error;
            }
        }
        tokio::select! {
            _ = time::sleep(delay) => {}
            _ = shutdown.changed() => return Ok(()),
        }
        delay = (delay * 2).min(MAX_RETRY_INTERVAL);
    }
}

async fn run_once(
    config: Arc<Config>,
    app_identity: Arc<Identity>,
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
    note_rendezvous(&config, remote);
    let bind = if remote.is_ipv4() {
        "0.0.0.0:0"
    } else {
        "[::]:0"
    };
    let socket = UdpSocket::bind(bind).await?;
    socket.connect(remote).await?;
    crate::status::disconnected();
    let mut registered = false;
    let mut rejection = String::new();
    let mut last_ack = time::Instant::now();
    let mut timer = time::interval(RETRY_INTERVAL);
    timer.set_missed_tick_behavior(time::MissedTickBehavior::Delay);
    let mut receive_buffer = vec![0_u8; 64 * 1024];
    let mut malformed_logged = false;

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
                // A datagram that does not decode, spoofed or damaged, is
                // dropped; it must not end the registration.
                let Ok(message) = RendezvousMessage::decode(&receive_buffer[..length]) else {
                    if !malformed_logged {
                        eprintln!("RustDesk ID server {hbbs}: ignoring a malformed message");
                        malformed_logged = true;
                    }
                    continue;
                };
                match message.union {
                    Some(rendezvous_message::Union::RegisterPkResponse(response)) => {
                        match RegisterPkResult::try_from(response.result) {
                            Ok(RegisterPkResult::Ok) => {
                                if !registered {
                                    eprintln!("RustDesk ID {} registered with {hbbs}", rustdesk_identity.id);
                                }
                                registered = true;
                                rejection.clear();
                                last_ack = time::Instant::now();
                                crate::status::registered();
                                timer = time::interval(REGISTER_INTERVAL);
                            }
                            result => {
                                registered = false;
                                crate::status::disconnected();
                                // Every retry gets the same answer; log it once.
                                let reason = format!("{result:?}");
                                if reason != rejection {
                                    eprintln!("RustDesk ID registration rejected by {hbbs}: {reason}");
                                    rejection = reason;
                                }
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
                        let limits=limits.with_permissions(request.control_permissions.as_ref());
                        spawn_requested_relay(
                            request,
                            Arc::clone(&config),
                            Arc::clone(&app_identity),
                            Arc::clone(&rustdesk_identity),
                            shutdown.clone(),
                            limits.clone(),
                        );
                    }
                    Some(rendezvous_message::Union::IceCandidate(candidate)) => {
                        webrtc.candidate(candidate);
                    }
                    Some(rendezvous_message::Union::PunchHole(request)) => {
                        let limits=limits.with_permissions(request.control_permissions.as_ref());
                        if request.webrtc_sdp_offer.is_empty() || !config.webrtc_enabled {
                            if !request.force_relay                                 && request.nat_type != crate::protocol::NatType::Symmetric as i32
                                && request.webrtc_sdp_offer.is_empty()
                            {
                                crate::direct::spawn(crate::direct::Request {peer:request.socket_addr, peer_v6:request.socket_addr_v6,relay:request.relay_server,lan:false},Arc::clone(&config),Arc::clone(&app_identity),Arc::clone(&rustdesk_identity),shutdown.clone(),limits.clone());
                                continue;
                            }
                            spawn_fallback_relay(request.socket_addr, request.socket_addr_v6, request.relay_server,
                                Arc::clone(&config), Arc::clone(&app_identity), Arc::clone(&rustdesk_identity),
                                shutdown.clone(), limits.clone());
                        } else {
                            let manager = webrtc.clone();
                            let config = Arc::clone(&config);
                            let app = Arc::clone(&app_identity);
                            let rd = Arc::clone(&rustdesk_identity);
                            let shutdown = shutdown.clone();
                            let limits = limits.clone();
                            tokio::spawn(async move {
                                if let Err(error) = manager.answer(&request, Arc::clone(&config), Arc::clone(&app),
                                    Arc::clone(&rd), shutdown.clone(), limits.clone()).await {
                                    if *shutdown.borrow(){return}
                                    eprintln!("RustDesk WebRTC unavailable: {error}; using relay");
                                    spawn_fallback_relay(request.socket_addr, request.socket_addr_v6, request.relay_server,
                                        config, app, rd, shutdown, limits);
                                }
                            });
                        }
                    }
                    Some(rendezvous_message::Union::FetchLocalAddr(request)) => {
                        let limits=limits.with_permissions(request.control_permissions.as_ref());
                        crate::direct::spawn(crate::direct::Request{peer:request.socket_addr,peer_v6:request.socket_addr_v6,relay:request.relay_server,lan:true},Arc::clone(&config),Arc::clone(&app_identity),Arc::clone(&rustdesk_identity),shutdown.clone(),limits);
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
    app_identity: Arc<Identity>,
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
                app_identity,
                rustdesk_identity,
                shutdown,
                limits,
            )
            .await
        }
        .await;
        if let Err(error) = result {
            crate::connlog::connection_log!("RustDesk requested relay failed: {error}");
        }
    });
}

#[allow(clippy::too_many_arguments)]
pub(crate) fn spawn_fallback_relay(
    socket_addr: Vec<u8>,
    socket_addr_v6: Vec<u8>,
    offered_relay: String,
    config: Arc<Config>,
    app_identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
) {
    tokio::spawn(async move {
        let uuid: String = rand::rng()
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
                app_identity,
                rustdesk_identity,
                shutdown,
                limits,
            )
            .await
        }
        .await;
        if let Err(error) = result {
            crate::connlog::connection_log!("RustDesk fallback relay failed: {error}");
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
    app_identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    shutdown: watch::Receiver<bool>,
    limits: server::ClientLimits,
) -> io::Result<()> {
    if !valid_uuid(&uuid) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "relay request has an invalid session ID",
        ));
    }
    let pending_permit = limits
        .try_pending()
        .ok_or_else(|| io::Error::new(io::ErrorKind::WouldBlock, "too many pending handshakes"))?;
    let target = time::timeout(CONNECT_TIMEOUT, relay_target(&config, &relay))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "relay lookup timed out"))??;
    // Each address in turn, as for a name, within one timeout.
    let mut stream = time::timeout(CONNECT_TIMEOUT, TcpStream::connect(target.as_slice()))
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
    crate::connlog::connection_log!("RustDesk client {peer} connected through relay {relay}");
    server::serve(
        stream,
        peer,
        config,
        app_identity,
        rustdesk_identity,
        secure,
        shutdown,
        limits,
        pending_permit,
    )
    .await
}

// The relay and session ID come from the ID server's message, which only
// relays what a controller asked for. Without checks, anyone who knows the
// device ID could make it connect to any host and port, also on its LAN or
// to itself, and send a chosen session ID there.
fn valid_uuid(uuid: &str) -> bool {
    !uuid.is_empty()
        && uuid.len() <= 64
        && uuid
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
}

fn address_host(address: &str) -> &str {
    let host = address
        .rsplit_once(':')
        .filter(|(host, port)| !host.is_empty() && port.parse::<u16>().is_ok())
        .map_or(address, |(host, _)| host);
    host.trim_start_matches('[').trim_end_matches(']')
}

// Not reachable on the Internet: this host, private and shared networks,
// link-local, documentation, benchmark, multicast and reserved ranges.
pub(crate) fn is_public(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(ip) => {
            let [a, b, c, _] = ip.octets();
            !(ip.is_unspecified()
                || ip.is_loopback()
                || ip.is_private()
                || ip.is_link_local()
                || ip.is_broadcast()
                || ip.is_multicast()
                || ip.is_documentation()
                || a == 0
                || a >= 240
                || (a == 100 && (64..128).contains(&b))
                || (a == 192 && b == 0 && c == 0)
                || (a == 198 && (18..20).contains(&b)))
        }
        IpAddr::V6(ip) => {
            if let Some(ip) = ip.to_ipv4_mapped() {
                return is_public(IpAddr::V4(ip));
            }
            let first = ip.segments()[0];
            !(ip.is_unspecified()
                || ip.is_loopback()
                || ip.is_multicast()
                || ip.is_unique_local()
                || ip.is_unicast_link_local()
                || (first & 0xffc0) == 0xfec0
                || (first == 0x2001 && ip.segments()[1] == 0x0db8)
                || first == 0x0064)
        }
    }
}

// The names the configuration gives, and an ID server that is itself on a
// private network, may lead anywhere; any other relay must be on the
// Internet. The checked addresses are the ones connected to, in order.
async fn relay_target(config: &Config, relay: &str) -> io::Result<Vec<SocketAddr>> {
    let addresses: Vec<SocketAddr> = lookup_host(relay).await?.collect();
    check_relay(config, relay, addresses).await
}

async fn check_relay(
    config: &Config,
    relay: &str,
    addresses: Vec<SocketAddr>,
) -> io::Result<Vec<SocketAddr>> {
    if addresses.is_empty() {
        return Err(io::Error::new(io::ErrorKind::NotFound, "relay did not resolve"));
    }
    let host = address_host(relay);
    let configured = [config.relay_server.trim(), config.rendezvous_server.trim()]
        .into_iter()
        .filter(|value| !value.is_empty())
        .filter_map(|value| server_address(value, RENDEZVOUS_PORT).ok())
        .any(|value| address_host(&value).eq_ignore_ascii_case(host));
    if configured || addresses.iter().all(|address| is_public(address.ip())) {
        return Ok(addresses);
    }
    if rendezvous_is_private(config).await {
        return Ok(addresses);
    }
    Err(io::Error::new(
        io::ErrorKind::PermissionDenied,
        format!("refusing relay {relay}: not on the Internet"),
    ))
}

// Whether the ID server is on a private network, as its registration last
// resolved it: refused relays must not cost a DNS query each.
static RENDEZVOUS_PRIVATE: Mutex<Option<(String, bool)>> = Mutex::new(None);

fn note_rendezvous(config: &Config, remote: SocketAddr) {
    *RENDEZVOUS_PRIVATE
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner()) =
        Some((config.rendezvous_server.clone(), !is_public(remote.ip())));
}

async fn rendezvous_is_private(config: &Config) -> bool {
    if let Some((server, private)) = RENDEZVOUS_PRIVATE
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
        .as_ref()
    {
        if *server == config.rendezvous_server {
            return *private;
        }
    }
    let Ok(hbbs) = server_address(&config.rendezvous_server, RENDEZVOUS_PORT) else {
        return false;
    };
    let Ok(Some(remote)) = lookup_host(hbbs).await.map(|mut addresses| addresses.next()) else {
        return false;
    };
    note_rendezvous(config, remote);
    !is_public(remote.ip())
}

pub(crate) fn relay_address(config: &Config, offered: &str) -> String {
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
    use rand::Rng;
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
        rand::rng().fill_bytes(&mut secret_bytes);
        rand::rng().fill_bytes(&mut session_bytes);
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
                    my_name: "NanoKVM interop probe".to_owned(),
                    version: crate::upstream::version().to_owned(),
                    my_platform: "Linux".to_owned(),
                    option: Some(crate::protocol::OptionMessage {
                        disable_keyboard: 2,
                        disable_audio: 2,
                        supported_decoding: Some(crate::protocol::SupportedDecoding {
                            ability_h264: 1,
                            ability_h265: 1,
                            ..Default::default()
                        }),
                        ..Default::default()
                    }),
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

            let keepalive = tokio::spawn(async move {
                for sequence in 0..40_i64 {
                    writer
                        .write(&Message {
                            union: Some(message::Union::TestDelay(crate::protocol::TestDelay {
                                time: sequence,
                                from_client: true,
                                ..Default::default()
                            })),
                        })
                        .await
                        .unwrap();
                    time::sleep(Duration::from_millis(500)).await;
                }
            });
            let video = time::timeout(Duration::from_secs(20), async {
                let mut count = 0;
                while count < 120 {
                    let message: Message = reader
                        .read()
                        .await
                        .unwrap_or_else(|error| panic!("video ended after {count} messages: {error}"));
                    if let Some(message::Union::VideoFrame(video)) = message.union {
                        let frames = match video.union {
                            Some(video_frame::Union::H264s(frames))
                            | Some(video_frame::Union::H265s(frames)) => frames,
                            _ => panic!("target did not send a supported HDMI codec"),
                        };
                        assert!(!frames.frames.is_empty());
                        assert!(frames.frames.iter().all(|frame| !frame.data.is_empty()));
                        count += 1;
                    }
                }
            })
            .await;
            keepalive.abort();
            let _ = keepalive.await;
            video.expect("target did not sustain 120 encrypted HDMI video messages");
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

    #[test]
    fn only_internet_addresses_are_public() {
        for private in [
            "0.0.0.0", "127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.1.1",
            "100.64.0.1", "192.0.0.8", "192.0.2.1", "198.18.0.1", "224.0.0.1", "240.0.0.1",
            "255.255.255.255", "::", "::1", "fc00::1", "fd12::1", "fe80::1", "fec0::1",
            "2001:db8::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::1",
        ] {
            assert!(!super::is_public(private.parse().unwrap()), "{private}");
        }
        for public in ["1.1.1.1", "8.8.8.8", "100.128.0.1", "2606:4700::1111", "::ffff:8.8.8.8"] {
            assert!(super::is_public(public.parse().unwrap()), "{public}");
        }
    }

    #[test]
    fn relay_session_ids_are_short_tokens() {
        assert!(super::valid_uuid("8d1f3c1e-0d8b-4c5e-9f3b-1b2c3d4e5f60"));
        assert!(super::valid_uuid(&"a".repeat(64)));
        for invalid in ["", "\r\nCONFIG SET dir /tmp\r\n", "a b", "../x"] {
            assert!(!super::valid_uuid(invalid), "{invalid:?}");
        }
        assert!(!super::valid_uuid(&"a".repeat(65)));
    }

    #[tokio::test]
    async fn an_offered_relay_off_the_internet_is_refused() {
        let official = crate::config::Config {
            rendezvous_server: "1.1.1.1".into(),
            ..Default::default()
        };
        for refused in ["192.168.1.1:6379", "127.0.0.1:6379", "[::1]:11211", "10.0.0.5:21117"] {
            let relay = super::relay_address(&official, refused);
            let error = super::relay_target(&official, &relay).await.unwrap_err();
            assert_eq!(error.kind(), std::io::ErrorKind::PermissionDenied, "{refused}");
        }
        for allowed in ["8.8.8.8:21117", "1.1.1.1:21117", "1.1.1.1"] {
            let relay = super::relay_address(&official, allowed);
            assert!(super::relay_target(&official, &relay).await.is_ok(), "{allowed}");
        }
        // A configured relay, and an ID server on the LAN, may be private.
        let configured = crate::config::Config {
            rendezvous_server: "1.1.1.1".into(),
            relay_server: "10.0.0.2".into(),
            ..Default::default()
        };
        let relay = super::relay_address(&configured, "192.168.1.1:6379");
        assert_eq!(
            super::relay_target(&configured, &relay).await.unwrap(),
            vec!["10.0.0.2:21117".parse::<SocketAddr>().unwrap()]
        );
        let lan = crate::config::Config {
            rendezvous_server: "192.168.1.10".into(),
            ..Default::default()
        };
        let relay = super::relay_address(&lan, "192.168.1.11:21117");
        assert!(super::relay_target(&lan, &relay).await.is_ok());
    }

    #[tokio::test]
    async fn a_malformed_datagram_does_not_end_the_registration() {
        let hbbs = tokio::net::UdpSocket::bind("127.0.0.1:0").await.unwrap();
        let directory = std::env::temp_dir().join(format!(
            "nanokvm-rendezvous-{}-{}",
            std::process::id(),
            rand::random::<u64>()
        ));
        std::fs::create_dir_all(&directory).unwrap();
        let config = std::sync::Arc::new(crate::config::Config {
            rendezvous_server: hbbs.local_addr().unwrap().to_string(),
            ..Default::default()
        });
        let identity = std::sync::Arc::new(crate::nanokvm::Identity {
            extension_id: "rustdesk".to_owned(),
            token: "a".repeat(64),
        });
        let rustdesk_identity =
            std::sync::Arc::new(RustDeskIdentity::load_from(&directory.join("state")).unwrap());
        let (shutdown_sender, shutdown) = tokio::sync::watch::channel(false);
        let device = tokio::spawn(super::run_once(
            config,
            identity,
            rustdesk_identity,
            shutdown,
            crate::server::ClientLimits::new(1),
            crate::webrtc::Manager::default(),
        ));
        let mut buffer = [0_u8; 2048];
        let (_, device_address) = time::timeout(Duration::from_secs(5), hbbs.recv_from(&mut buffer))
            .await
            .unwrap()
            .unwrap();
        // A length-delimited field whose length runs past the datagram.
        hbbs.send_to(&[0x0a, 0xff], device_address).await.unwrap();
        time::sleep(Duration::from_millis(300)).await;
        assert!(!device.is_finished());
        shutdown_sender.send(true).unwrap();
        time::timeout(Duration::from_secs(5), device)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        std::fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test]
    async fn a_relay_session_id_that_is_not_a_token_is_never_sent() {
        let relay = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = relay.local_addr().unwrap().to_string();
        let directory = std::env::temp_dir().join(format!(
            "nanokvm-relay-uuid-{}-{}",
            std::process::id(),
            rand::random::<u64>()
        ));
        std::fs::create_dir_all(&directory).unwrap();
        let config = std::sync::Arc::new(crate::config::Config {
            rendezvous_server: address.clone(),
            ..Default::default()
        });
        let (_shutdown_sender, shutdown) = tokio::sync::watch::channel(false);
        let result = time::timeout(
            Duration::from_secs(2),
            super::connect_relay(
                address,
                "x\r\nFLUSHALL\r\n".to_owned(),
                "127.0.0.1:1".parse().unwrap(),
                false,
                config,
                std::sync::Arc::new(crate::nanokvm::Identity {
                    extension_id: "rustdesk".to_owned(),
                    token: "a".repeat(64),
                }),
                std::sync::Arc::new(RustDeskIdentity::load_from(&directory.join("state")).unwrap()),
                shutdown,
                crate::server::ClientLimits::new(1),
            ),
        )
        .await
        .expect("an invalid session ID must fail at once");
        assert_eq!(result.unwrap_err().kind(), std::io::ErrorKind::InvalidData);
        assert!(time::timeout(Duration::from_millis(100), relay.accept()).await.is_err());
        std::fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test]
    async fn a_relay_name_keeps_all_its_checked_addresses() {
        let official = crate::config::Config {
            rendezvous_server: "1.1.1.1".into(),
            ..Default::default()
        };
        let addresses = |list: &[&str]| -> Vec<SocketAddr> {
            list.iter().map(|address| address.parse().unwrap()).collect()
        };
        let both = addresses(&["8.8.8.8:21117", "[2606:4700::1111]:21117"]);
        assert_eq!(
            super::check_relay(&official, "relay.example:21117", both.clone())
                .await
                .unwrap(),
            both
        );
        // One private answer among public ones refuses the name.
        let mixed = addresses(&["8.8.8.8:21117", "192.168.1.1:21117"]);
        let error = super::check_relay(&official, "relay.example:21117", mixed)
            .await
            .unwrap_err();
        assert_eq!(error.kind(), std::io::ErrorKind::PermissionDenied);
        assert_eq!(
            super::check_relay(&official, "relay.example:21117", Vec::new())
                .await
                .unwrap_err()
                .kind(),
            std::io::ErrorKind::NotFound
        );
    }

    #[tokio::test]
    async fn the_next_checked_address_is_tried_when_one_refuses() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        // Nothing listens on 127.0.0.2 at that port.
        let target: Vec<SocketAddr> = vec![
            format!("127.0.0.2:{port}").parse().unwrap(),
            format!("127.0.0.1:{port}").parse().unwrap(),
        ];
        let (connected, accepted) = tokio::join!(
            tokio::net::TcpStream::connect(target.as_slice()),
            listener.accept()
        );
        assert_eq!(connected.unwrap().peer_addr().unwrap(), target[1]);
        accepted.unwrap();
    }
}
