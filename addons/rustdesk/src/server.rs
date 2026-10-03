// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::{
    collections::HashMap,
    io,
    net::SocketAddr,
    sync::{Arc, Mutex},
    time::Duration,
};

use crypto_box::{
    aead::Aead as _, Nonce as BoxNonce, PublicKey as BoxPublicKey, SalsaBox,
    SecretKey as BoxSecretKey,
};
use rand::{distributions::Alphanumeric, Rng};
use sha2::{Digest, Sha256};
use tokio::{
    io::{AsyncRead, AsyncWrite},
    net::{TcpListener, TcpStream, UnixStream},
    sync::{mpsc, watch, OwnedSemaphorePermit, Semaphore},
    task::JoinSet,
    time,
};

use crate::{
    config::Config,
    framing::{read_message, write_message, FrameReader, FrameWriter, SessionKey},
    identity::RustDeskIdentity,
    input::InputState,
    onekvm::{self, Codec, HidClient, Identity, MediaFrame, MediaSubscriber, VideoInfo},
    protocol::{
        login_response, message, video_frame, DisplayInfo, EncodedVideoFrame, EncodedVideoFrames,
        Features, Hash, IdPk, KeyEvent, LoginRequest, LoginResponse, Message, MouseEvent, PeerInfo,
        SignedId, SupportedEncoding, VideoFrame,
    },
    rendezvous,
};

use prost::Message as _;

const LOGIN_TIMEOUT: Duration = Duration::from_secs(60);
const MAX_PASSWORD_ATTEMPTS: usize = 5;
const MEDIA_TIMEOUT: Duration = Duration::from_secs(5);
const INPUT_FLUSH_INTERVAL: Duration = Duration::from_millis(16);
const MOUSE_TYPE_MASK: i32 = 0x07;
const MOUSE_TYPE_MOVE: i32 = 0;
const MOUSE_TYPE_MOVE_RELATIVE: i32 = 5;

fn is_peer_disconnect(error: &io::Error) -> bool {
    matches!(
        error.kind(),
        io::ErrorKind::UnexpectedEof
            | io::ErrorKind::ConnectionReset
            | io::ErrorKind::ConnectionAborted
            | io::ErrorKind::BrokenPipe
    )
}

enum InputEvent {
    Enabled(bool),
    Mouse(MouseEvent),
    Key(KeyEvent),
}

#[derive(Clone)]
pub(crate) struct ClientLimits {
    pub(crate) input_allowed: bool,
    sessions: Arc<Mutex<SessionRegistry>>,
    pending: Arc<Semaphore>,
    credentials: Arc<Mutex<Option<crate::auth::Credentials>>>,
}

struct SessionRegistry {
    max_clients: usize,
    clients: HashMap<String, usize>,
}

struct SessionPermit {
    registry: Arc<Mutex<SessionRegistry>>,
    client: String,
}

impl Drop for SessionPermit {
    fn drop(&mut self) {
        let mut registry = self
            .registry
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        if let Some(references) = registry.clients.get_mut(&self.client) {
            *references -= 1;
            if *references == 0 {
                registry.clients.remove(&self.client);
            }
        }
    }
}

impl ClientLimits {
    fn new(max_clients: usize) -> Self {
        Self {
            input_allowed: true,
            sessions: Arc::new(Mutex::new(SessionRegistry {
                max_clients,
                clients: HashMap::new(),
            })),
            // RustDesk races direct, hole-punch, and relay candidates for one
            // logical connection. Keep those short-lived handshakes separate
            // from the configured limit, which applies to authenticated
            // sessions. The pending cap still bounds unauthenticated work.
            pending: Arc::new(Semaphore::new(max_clients.saturating_mul(2).max(4))),
            credentials: Arc::new(Mutex::new(None)),
        }
    }

    pub(crate) fn try_pending(&self) -> Option<OwnedSemaphorePermit> {
        Arc::clone(&self.pending).try_acquire_owned().ok()
    }

    fn try_session(&self, client: String) -> Option<SessionPermit> {
        let mut registry = self
            .sessions
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        if let Some(references) = registry.clients.get_mut(&client) {
            if *references >= 4 {
                return None;
            }
            *references += 1;
        } else if registry.clients.len() < registry.max_clients {
            registry.clients.insert(client.clone(), 1);
        } else {
            return None;
        }
        drop(registry);
        Some(SessionPermit {
            registry: Arc::clone(&self.sessions),
            client,
        })
    }
}

enum PendingPointerMove {
    Absolute(MouseEvent),
    Relative {
        x: i64,
        y: i64,
        template: MouseEvent,
    },
}

impl PendingPointerMove {
    fn new(event: MouseEvent) -> Result<Self, MouseEvent> {
        match event.mask & MOUSE_TYPE_MASK {
            MOUSE_TYPE_MOVE => Ok(Self::Absolute(event)),
            MOUSE_TYPE_MOVE_RELATIVE => Ok(Self::Relative {
                x: i64::from(event.x),
                y: i64::from(event.y),
                template: event,
            }),
            _ => Err(event),
        }
    }

    fn merge(&mut self, event: MouseEvent) -> Result<(), MouseEvent> {
        match (self, event.mask & MOUSE_TYPE_MASK) {
            (Self::Absolute(pending), MOUSE_TYPE_MOVE) => {
                *pending = event;
                Ok(())
            }
            (Self::Relative { x, y, template }, MOUSE_TYPE_MOVE_RELATIVE) => {
                *x = x.saturating_add(i64::from(event.x));
                *y = y.saturating_add(i64::from(event.y));
                *template = event;
                Ok(())
            }
            _ => Err(event),
        }
    }

    async fn flush(self, input: &mut InputState) -> io::Result<()> {
        match self {
            Self::Absolute(event) => input.handle_mouse(event).await,
            Self::Relative {
                mut x,
                mut y,
                mut template,
            } => {
                // Match the web client limiter: preserve the complete relative
                // displacement and split it into HID-safe signed-byte chunks.
                // A zero movement is still sent so InputState can learn that
                // the RustDesk client switched to relative pointer mode.
                let mut first = true;
                while first || x != 0 || y != 0 {
                    first = false;
                    let (chunk_x, chunk_y) = take_relative_chunk(&mut x, &mut y);
                    template.x = chunk_x;
                    template.y = chunk_y;
                    input.handle_mouse(template.clone()).await?;
                }
                Ok(())
            }
        }
    }
}

fn take_relative_chunk(x: &mut i64, y: &mut i64) -> (i32, i32) {
    let chunk_x = (*x).clamp(-127, 127) as i32;
    let chunk_y = (*y).clamp(-127, 127) as i32;
    *x -= i64::from(chunk_x);
    *y -= i64::from(chunk_y);
    (chunk_x, chunk_y)
}

pub async fn run(
    config: Config,
    identity: Identity,
    rustdesk_identity: RustDeskIdentity,
) -> io::Result<()> {
    let address = SocketAddr::new(
        config
            .listen_address
            .parse()
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidInput, error))?,
        config.port,
    );
    let listener = TcpListener::bind(address).await?;
    let limits = ClientLimits::new(config.max_clients);
    let config = Arc::new(config);
    let identity = Arc::new(identity);
    let rustdesk_identity = Arc::new(rustdesk_identity);
    let (shutdown_sender, shutdown_receiver) = watch::channel(false);
    let mut sessions = JoinSet::new();

    if !config.rendezvous_server.trim().is_empty() {
        let rendezvous_config = Arc::clone(&config);
        let rendezvous_onekvm = Arc::clone(&identity);
        let rendezvous_identity = Arc::clone(&rustdesk_identity);
        let rendezvous_shutdown = shutdown_receiver.clone();
        let rendezvous_limits = limits.clone();
        sessions.spawn(async move {
            rendezvous::run(
                rendezvous_config,
                rendezvous_onekvm,
                rendezvous_identity,
                rendezvous_shutdown,
                rendezvous_limits,
            )
            .await
        });
        eprintln!(
            "RustDesk ID {} registering with {}",
            rustdesk_identity.id, config.rendezvous_server
        );
    } else {
        eprintln!(
            "RustDesk ID {} is available locally; ID server is not configured",
            rustdesk_identity.id
        );
    }

    let _status_guard = crate::status::Guard;
    let mut status_tick = time::interval(Duration::from_secs(2));
    eprintln!("RustDesk direct-IP endpoint listening on {address}");
    loop {
        tokio::select! {
            _ = status_tick.tick() => {
                let count = limits.sessions.lock().unwrap_or_else(|p| p.into_inner()).clients.len();
                crate::status::write(&rustdesk_identity.id, count)?;
            }
            signal = shutdown_signal() => {
                signal?;
                break;
            }
            accepted = listener.accept() => {
                let (stream, peer) = accepted?;
                let Some(pending_permit) = limits.try_pending() else {
                    eprintln!("rejecting RustDesk client {peer}: too many pending handshakes");
                    drop(stream);
                    continue;
                };
                let config = Arc::clone(&config);
                let identity = Arc::clone(&identity);
                let rustdesk_identity = Arc::clone(&rustdesk_identity);
                let shutdown = shutdown_receiver.clone();
                let limits = limits.clone();
                sessions.spawn(async move {
                    if let Err(error) = serve(stream, peer, config, identity, rustdesk_identity, false, shutdown, limits, pending_permit).await {
                        if !is_peer_disconnect(&error) {
                            eprintln!("RustDesk client {peer}: {error}");
                        }
                    }
                    Ok(())
                });
            }
            completed = sessions.join_next(), if !sessions.is_empty() => {
                if let Some(result) = completed {
                    match result {
                        Err(error) => eprintln!("RustDesk session task failed: {error}"),
                        Ok(Err(error)) => eprintln!("RustDesk background task failed: {error}"),
                        Ok(Ok(())) => {}
                    }
                }
            }
        }
    }

    let _ = shutdown_sender.send(true);
    if time::timeout(Duration::from_secs(3), async {
        while sessions.join_next().await.is_some() {}
    })
    .await
    .is_err()
    {
        sessions.abort_all();
    }
    Ok(())
}

#[allow(clippy::too_many_arguments)]
pub(crate) async fn serve(
    stream: TcpStream,
    peer: SocketAddr,
    config: Arc<Config>,
    identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    secure: bool,
    shutdown: watch::Receiver<bool>,
    limits: ClientLimits,
    pending_permit: OwnedSemaphorePermit,
) -> io::Result<()> {
    stream.set_nodelay(true)?;
    serve_io(
        stream,
        peer,
        config,
        identity,
        rustdesk_identity,
        secure.then_some(String::new()),
        shutdown,
        limits,
        pending_permit,
    )
    .await
}

#[allow(clippy::too_many_arguments)]
pub(crate) async fn serve_webrtc(
    stream: UnixStream,
    fingerprint: String,
    peer: SocketAddr,
    config: Arc<Config>,
    identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    shutdown: watch::Receiver<bool>,
    limits: ClientLimits,
    pending_permit: OwnedSemaphorePermit,
) -> io::Result<()> {
    if fingerprint.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "missing DTLS identity",
        ));
    }
    serve_io(
        stream,
        peer,
        config,
        identity,
        rustdesk_identity,
        Some(fingerprint),
        shutdown,
        limits,
        pending_permit,
    )
    .await
}

#[allow(clippy::too_many_arguments)]
async fn serve_io<S: AsyncRead + AsyncWrite + Unpin + Send + 'static>(
    mut stream: S,
    peer: SocketAddr,
    config: Arc<Config>,
    identity: Arc<Identity>,
    rustdesk_identity: Arc<RustDeskIdentity>,
    secure: Option<String>,
    mut shutdown: watch::Receiver<bool>,
    limits: ClientLimits,
    pending_permit: OwnedSemaphorePermit,
) -> io::Result<()> {
    let is_relay = secure.is_some();
    let session_key = match secure {
        Some(fingerprint) => {
            identity_handshake(&mut stream, &rustdesk_identity, &fingerprint).await?
        }
        None => None,
    };
    let (reader, writer) = tokio::io::split(stream);
    let mut reader = FrameReader::new(reader, session_key.clone());
    let mut writer = FrameWriter::new(writer, session_key);
    let hash = Hash {
        salt: rustdesk_identity.password_salt.clone(),
        challenge: random_string(6),
    };
    write_union(&mut writer, message::Union::Hash(hash.clone())).await?;

    let login_deadline = time::Instant::now() + LOGIN_TIMEOUT;
    let mut password_attempts = 0;
    let (login, _session_permit) = loop {
        let login: Message = tokio::select! {
            result = time::timeout_at(login_deadline, reader.read()) => {
                result.map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "login timed out"))??
            }
            result = shutdown.changed() => {
                let _ = result;
                return Ok(());
            }
        };
        let Some(message::Union::LoginRequest(login)) = login.union else {
            eprintln!("rejecting RustDesk client {peer}: expected LoginRequest");
            send_error(&mut writer, "Expected LoginRequest").await?;
            return Ok(());
        };
        if login.union.is_some() {
            let session_type = match &login.union {
                Some(crate::protocol::login_request::Union::FileTransfer(_)) => "file transfer",
                Some(crate::protocol::login_request::Union::PortForward(_)) => "port forward",
                Some(crate::protocol::login_request::Union::ViewCamera(_)) => "view camera",
                Some(crate::protocol::login_request::Union::Terminal(_)) => "terminal",
                None => "desktop",
            };
            eprintln!(
                "rejecting RustDesk client {peer} (id {:?}): unsupported {session_type} session",
                login.my_id
            );
            send_error(&mut writer, "Unsupported session type").await?;
            return Ok(());
        }
        let admission = {
            let mut credentials = limits.credentials.lock().unwrap_or_else(|p| p.into_inner());
            let credentials =
                credentials.get_or_insert_with(|| crate::auth::Credentials::new(&config));
            if let Some(verified) = credentials.verify(&hash, &login)? {
                match limits.try_session(viewer_key(peer, is_relay, &login.my_id)) {
                    Some(permit) => {
                        credentials.admitted(&login, verified)?;
                        Some(Ok(permit))
                    }
                    None => Some(Err(())),
                }
            } else {
                None
            }
        };
        match admission {
            Some(Ok(permit)) => break (login, permit),
            Some(Err(())) => {
                send_error(&mut writer, "Maximum viewers reached").await?;
                return Ok(());
            }
            None => {}
        }

        // The official client first sends an empty password to ask the server
        // whether authentication is required. Keep the connection and hash
        // challenge alive so the password dialog can submit another request.
        // Empty probes do not consume an attempt, but the overall deadline
        // still prevents them from holding a pending slot indefinitely.
        if !login.password.is_empty() {
            password_attempts += 1;
        }
        let authentication_error = if login.password.is_empty() {
            "Empty Password"
        } else {
            "Wrong Password"
        };
        eprintln!(
            "RustDesk client {peer} (id {:?}) needs password authentication ({} bytes)",
            login.my_id,
            login.password.len()
        );
        time::sleep(Duration::from_millis(300)).await;
        send_error(&mut writer, authentication_error).await?;
        if password_attempts >= MAX_PASSWORD_ATTEMPTS {
            eprintln!(
                "rejecting RustDesk client {peer} (id {:?}): too many wrong password attempts",
                login.my_id
            );
            return Ok(());
        }
    };

    let video_profile = onekvm::VideoProfile {
        codec: onekvm::parse_codec(&config.codec)?,
    };
    let video_info = time::timeout(
        MEDIA_TIMEOUT,
        onekvm::query_video_info(&config.media_socket, &identity, &video_profile),
    )
    .await
    .unwrap_or_else(|_| {
        Err(io::Error::new(
            io::ErrorKind::TimedOut,
            "video metadata timed out",
        ))
    });
    let mut video_info = match video_info {
        Ok(info) => info,
        Err(error) => {
            send_error(&mut writer, &format!("NanoKVM video unavailable: {error}")).await?;
            return Ok(());
        }
    };
    if video_info.codec == Codec::Mjpeg {
        send_error(&mut writer, "NanoKVM video codec must be H.264 or H.265").await?;
        return Ok(());
    }
    if !client_supports_codec(&login, video_info.codec) {
        send_error(
            &mut writer,
            &format!(
                "RustDesk client does not support NanoKVM {} video",
                codec_name(video_info.codec)
            ),
        )
        .await?;
        return Ok(());
    }
    video_info.fps = video_info.fps.min(config.fps);

    write_union(
        &mut writer,
        message::Union::LoginResponse(LoginResponse {
            union: Some(login_response::Union::PeerInfo(peer_info(&video_info))),
            enable_trusted_devices: false,
        }),
    )
    .await?;

    drop(pending_permit);
    eprintln!(
        "RustDesk client {peer} login accepted (id {:?}, secure {is_relay})",
        login.my_id
    );
    let mut media = time::timeout(
        MEDIA_TIMEOUT,
        MediaSubscriber::connect(
            &config.media_socket,
            &identity,
            "encoded",
            Some(&video_profile),
        ),
    )
    .await
    .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "video subscription timed out"))??;
    let hid = HidClient::new(config.admin_socket.clone(), (*identity).clone());
    let mut input = InputState::new(hid, video_info.width, video_info.height);
    // HID requests use a separate Unix HTTP exchange for every event. Process
    // them serially in their own task so a slow HID response cannot stall the
    // video writer. Pointer movement follows the web UI limiter: at most one
    // flush starts per 16 ms window, absolute moves keep the newest position,
    // and relative moves accumulate without losing displacement. Keyboard,
    // button, and wheel events flush pending movement before being handled.
    let can_control = limits.input_allowed;
    let input_allowed = can_control
        && login
            .option
            .as_ref()
            .map(|o| matches!(o.disable_keyboard, 0 | 1))
            .unwrap_or(true);
    let (input_sender, mut input_receiver) = mpsc::channel(64);
    let (input_result_sender, mut input_result_receiver) = mpsc::channel(1);
    let (input_shutdown_sender, mut input_shutdown) = watch::channel(false);
    let input_task = tokio::spawn(async move {
        let mut enabled = input_allowed;
        let mut active = false;
        let mut pending_move: Option<PendingPointerMove> = None;
        // A fixed cadence mirrors requestAnimationFrame more closely than
        // sleeping 16 ms after the first event: movement waits only until the
        // next frame boundary (about 8 ms on average), not a full frame.
        let mut heartbeat = time::interval(Duration::from_secs(2));
        heartbeat.tick().await;
        let mut flush_tick = time::interval_at(
            time::Instant::now() + INPUT_FLUSH_INTERVAL,
            INPUT_FLUSH_INTERVAL,
        );
        flush_tick.set_missed_tick_behavior(time::MissedTickBehavior::Skip);
        let result = loop {
            let event = tokio::select! {
                event = input_receiver.recv() => event,
                _ = heartbeat.tick() => {
                    if enabled { active=true; if let Err(error) = input.heartbeat().await { break Err(error); } }
                    continue;
                },
                _ = flush_tick.tick() => {
                    if let Some(movement) = pending_move.take() {
                        active=true;
                        if let Err(error) = movement.flush(&mut input).await {
                            break Err(error);
                        }
                    }
                    continue;
                }
                result = input_shutdown.changed() => {
                    let _ = result;
                    break Ok(());
                }
            };

            let Some(event) = event else {
                if let Some(movement) = pending_move.take() {
                    active = true;
                    if let Err(error) = movement.flush(&mut input).await {
                        break Err(error);
                    }
                }
                break Ok(());
            };

            match event {
                InputEvent::Enabled(next) => {
                    if !next {
                        pending_move = None;
                        if active {
                            input.release_all().await;
                            active = false;
                        }
                    }
                    enabled = next;
                }
                InputEvent::Mouse(_) | InputEvent::Key(_) if !enabled => {}
                InputEvent::Mouse(event) => match PendingPointerMove::new(event) {
                    Ok(movement) => {
                        if let Some(pending) = pending_move.as_mut() {
                            let event = match movement {
                                PendingPointerMove::Absolute(event) => event,
                                PendingPointerMove::Relative { template, .. } => template,
                            };
                            if let Err(event) = pending.merge(event) {
                                if let Some(movement) = pending_move.take() {
                                    active = true;
                                    if let Err(error) = movement.flush(&mut input).await {
                                        break Err(error);
                                    }
                                }
                                pending_move = Some(
                                    PendingPointerMove::new(event)
                                        .expect("pointer movement was already classified"),
                                );
                            }
                        } else {
                            pending_move = Some(movement);
                        }
                    }
                    Err(event) => {
                        if let Some(movement) = pending_move.take() {
                            active = true;
                            if let Err(error) = movement.flush(&mut input).await {
                                break Err(error);
                            }
                        }
                        active = true;
                        if let Err(error) = input.handle_mouse(event).await {
                            break Err(error);
                        }
                    }
                },
                InputEvent::Key(event) => {
                    if let Some(movement) = pending_move.take() {
                        active = true;
                        if let Err(error) = movement.flush(&mut input).await {
                            break Err(error);
                        }
                    }
                    active = true;
                    if let Err(error) = input.handle_key(event).await {
                        break Err(error);
                    }
                }
            }
        };
        if active {
            input.release_all().await;
        }
        let _ = input_result_sender.send(result).await;
    });
    // AsyncReadExt::read_exact is not cancellation-safe. Reading a media frame
    // directly inside the session select would lose a partially consumed
    // header whenever a client input message won the race. Keep the media read
    // in its own task and pass only complete frames to the session loop.
    let (media_sender, mut media_receiver) = mpsc::channel(1);
    let media_task = tokio::spawn(async move {
        loop {
            let frame = media.read_frame().await;
            let failed = frame.is_err();
            if media_sender.send(frame).await.is_err() || failed {
                break;
            }
        }
    });
    // FrameReader::read also uses read_exact internally and has the same
    // cancellation constraint. Keep it alive across media sends so a partial
    // RustDesk packet cannot be mistaken for the next packet.
    let (incoming_sender, mut incoming_receiver) = mpsc::channel(16);
    let incoming_limits = limits.clone();
    let incoming_login = login.clone();
    let incoming_task = tokio::spawn(async move {
        let mut enabled = input_allowed;
        loop {
            // A stalled/half-open peer releases HID and its viewer slot. The
            // whole stream ends on timeout, so partial reads are never reused.
            let message = time::timeout(Duration::from_secs(30), reader.read::<Message>())
                .await
                .unwrap_or_else(|_| {
                    Err(io::Error::new(io::ErrorKind::TimedOut, "peer idle timeout"))
                });
            if message.is_ok() {
                if let Some(credentials) = incoming_limits
                    .credentials
                    .lock()
                    .unwrap_or_else(|p| p.into_inner())
                    .as_mut()
                {
                    credentials.touch(&incoming_login);
                }
            }
            match message {
                Ok(Message {
                    union:
                        Some(message::Union::Misc(crate::protocol::Misc {
                            union: Some(crate::protocol::misc::Union::Option(option)),
                        })),
                }) => {
                    if matches!(option.disable_keyboard, 1 | 2) {
                        enabled = can_control && option.disable_keyboard == 1;
                        if input_sender
                            .send(InputEvent::Enabled(enabled))
                            .await
                            .is_err()
                        {
                            break;
                        }
                    }
                }
                Ok(Message {
                    union:
                        Some(message::Union::Misc(crate::protocol::Misc {
                            union: Some(crate::protocol::misc::Union::CloseReason(_)),
                        })),
                }) => break,
                Ok(Message {
                    union: Some(message::Union::MouseEvent(event)),
                }) => {
                    if enabled && input_sender.send(InputEvent::Mouse(event)).await.is_err() {
                        break;
                    }
                }
                Ok(Message {
                    union: Some(message::Union::KeyEvent(event)),
                }) => {
                    if enabled && input_sender.send(InputEvent::Key(event)).await.is_err() {
                        break;
                    }
                }
                message => {
                    let failed = message.is_err();
                    if incoming_sender.send(message).await.is_err() || failed {
                        break;
                    }
                }
            }
        }
    });
    eprintln!(
        "RustDesk client {peer} authenticated ({}x{} {} @ {} FPS)",
        video_info.width,
        video_info.height,
        codec_name(video_info.codec),
        video_info.fps
    );

    let mut first_video_frame = true;
    let mut peer_tick = time::interval(Duration::from_secs(10));
    peer_tick.tick().await;
    let result = loop {
        tokio::select! {
            _ = peer_tick.tick() => {
                let delay = crate::protocol::TestDelay { time: std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_millis() as i64,
                    from_client: false, ..Default::default() };
                if let Err(error) = write_union(&mut writer, message::Union::TestDelay(delay)).await {
                    break Err(error);
                }
            }
            incoming = incoming_receiver.recv() => {
                match incoming {
                    Some(Ok(Message { union: Some(message::Union::TestDelay(mut delay)) })) => {
                        if delay.from_client {
                            delay.from_client = false;
                            if let Err(error) = write_union(&mut writer, message::Union::TestDelay(delay)).await {
                                break Err(error);
                            }
                        }
                    }
                    Some(Ok(_)) => {}
                    Some(Err(error)) if is_peer_disconnect(&error) => break Ok(()),
                    Some(Err(error)) => break Err(error),
                    None => break Ok(()),
                }
            }
            input = input_result_receiver.recv() => {
                break input.unwrap_or_else(|| {
                    Err(io::Error::new(io::ErrorKind::BrokenPipe, "RustDesk input task ended"))
                });
            }
            frame = media_receiver.recv() => {
                let frame = match frame {
                    Some(Ok(frame)) => frame,
                    Some(Err(error)) => break Err(error),
                    None => break Err(io::Error::new(io::ErrorKind::UnexpectedEof, "NanoKVM media stream ended")),
                };
                let frame_codec = frame.codec;
                let frame_length = frame.payload.len();
                let Some(video) = rustdesk_video_frame(frame) else {
                    continue;
                };
                if first_video_frame {
                    eprintln!(
                        "RustDesk client {peer} sending first {} video frame ({frame_length} bytes)",
                        codec_name(frame_codec)
                    );
                }
                if let Err(error) = write_union(&mut writer, message::Union::VideoFrame(video)).await {
                    break Err(error);
                }
                if first_video_frame {
                    eprintln!("RustDesk client {peer} sent first video frame");
                    first_video_frame = false;
                }
            }
            result = shutdown.changed() => {
                let _ = result;
                break Ok(());
            }
        }
    };
    incoming_task.abort();
    let _ = incoming_task.await;
    media_task.abort();
    let _ = media_task.await;
    let _ = input_shutdown_sender.send(true);
    let _ = input_task.await;
    result
}

fn viewer_key(peer: std::net::SocketAddr, secure: bool, client_id: &str) -> String {
    if !client_id.is_empty() {
        format!("id:{client_id}")
    } else if secure {
        // Empty-ID relay clients cannot be deduplicated across connections.
        format!("relay:{peer}")
    } else {
        format!("direct:{}", peer.ip())
    }
}

fn peer_info(video: &VideoInfo) -> PeerInfo {
    PeerInfo {
        username: "NanoKVM".to_owned(),
        hostname: "NanoKVM".to_owned(),
        // This endpoint controls an external KVM target, not the Linux system
        // running NanoKVM. RustDesk disables relative mouse mode for peers that
        // advertise Linux, so use the actual endpoint type here.
        platform: "NanoKVM".to_owned(),
        displays: vec![DisplayInfo {
            x: 0,
            y: 0,
            width: i32::from(video.width),
            height: i32::from(video.height),
            name: "KVM capture".to_owned(),
            online: true,
            // The HDMI capture contains the target cursor, but it arrives with
            // the video pipeline latency. Advertising it as the only cursor
            // makes RustDesk hide the immediate local pointer. Keep the local
            // pointer visible for responsive KVM control.
            cursor_embedded: false,
            scale: 1.0,
        }],
        current_display: 0,
        sas_enabled: false,
        version: crate::upstream::version().to_owned(),
        features: Some(Features {
            privacy_mode: false,
            terminal: false,
        }),
        encoding: Some(SupportedEncoding {
            h264: video.codec == Codec::H264,
            h265: video.codec == Codec::H265,
            vp8: false,
            av1: false,
            i444: None,
        }),
        platform_additions: "{}".to_owned(),
    }
}

fn client_supports_codec(login: &LoginRequest, codec: Codec) -> bool {
    let supported = login
        .option
        .as_ref()
        .and_then(|option| option.supported_decoding.as_ref());
    match codec {
        // Older RustDesk clients predate SupportedDecoding but can decode
        // H.264, so preserve the existing compatibility fallback.
        Codec::H264 => supported.map_or(true, |ability| ability.ability_h264 > 0),
        // H.265 is optional and must be explicitly advertised by the client.
        Codec::H265 => supported.is_some_and(|ability| ability.ability_h265 > 0),
        Codec::Mjpeg => false,
    }
}

fn codec_name(codec: Codec) -> &'static str {
    match codec {
        Codec::H264 => "H.264",
        Codec::H265 => "H.265",
        Codec::Mjpeg => "MJPEG",
    }
}

fn rustdesk_video_frame(frame: MediaFrame) -> Option<VideoFrame> {
    if frame.payload.is_empty() {
        return None;
    }
    let frames = EncodedVideoFrames {
        frames: vec![EncodedVideoFrame {
            data: frame.payload,
            key: frame.keyframe,
            pts: frame.pts_usec / 1_000,
        }],
    };
    let union = match frame.codec {
        Codec::H264 => video_frame::Union::H264s(frames),
        Codec::H265 => video_frame::Union::H265s(frames),
        Codec::Mjpeg => return None,
    };
    Some(VideoFrame {
        union: Some(union),
        display: 0,
    })
}

pub(crate) fn verify_password(password: &str, hash: &Hash, login: &LoginRequest) -> bool {
    let mut first = Sha256::new();
    first.update(password.as_bytes());
    first.update(hash.salt.as_bytes());
    let first = first.finalize();
    let mut second = Sha256::new();
    second.update(first);
    second.update(hash.challenge.as_bytes());
    constant_time_eq(second.finalize().as_slice(), &login.password)
}

fn constant_time_eq(expected: &[u8], actual: &[u8]) -> bool {
    let mut difference = expected.len() ^ actual.len();
    for (index, expected_byte) in expected.iter().enumerate() {
        difference |= usize::from(*expected_byte ^ actual.get(index).copied().unwrap_or(0));
    }
    difference == 0
}

fn random_string(length: usize) -> String {
    rand::thread_rng()
        .sample_iter(Alphanumeric)
        .take(length)
        .map(char::from)
        .collect()
}

async fn send_error<W: AsyncWrite + Unpin>(
    writer: &mut FrameWriter<W>,
    error: &str,
) -> io::Result<()> {
    write_union(
        writer,
        message::Union::LoginResponse(LoginResponse {
            union: Some(login_response::Union::Error(error.to_owned())),
            enable_trusted_devices: false,
        }),
    )
    .await
}

async fn write_union<W: AsyncWrite + Unpin>(
    writer: &mut FrameWriter<W>,
    union: message::Union,
) -> io::Result<()> {
    writer.write(&Message { union: Some(union) }).await
}

#[cfg(test)]
async fn secure_handshake(
    stream: &mut TcpStream,
    identity: &RustDeskIdentity,
) -> io::Result<SessionKey> {
    identity_handshake(stream, identity, "")
        .await?
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "missing TCP session key"))
}

async fn identity_handshake<S: AsyncRead + AsyncWrite + Unpin>(
    stream: &mut S,
    identity: &RustDeskIdentity,
    fingerprint: &str,
) -> io::Result<Option<SessionKey>> {
    let mut ephemeral_secret = [0_u8; 32];
    rand::thread_rng().fill(&mut ephemeral_secret);
    let ephemeral_secret = BoxSecretKey::from(ephemeral_secret);
    let ephemeral_public = ephemeral_secret.public_key();
    let id_pk = IdPk {
        id: identity.id.clone(),
        pk: ephemeral_public.as_bytes().to_vec(),
        dtls_fingerprint: fingerprint.to_owned(),
        kx_version: if fingerprint.is_empty() { 1 } else { 0 },
    }
    .encode_to_vec();
    write_message(
        stream,
        &Message {
            union: Some(message::Union::SignedId(SignedId {
                id: identity.sign(&id_pk),
            })),
        },
    )
    .await?;

    let response: Message = time::timeout(LOGIN_TIMEOUT, read_message(stream))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "secure handshake timed out"))??;
    let Some(message::Union::PublicKey(public_key)) = response.union else {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "secure handshake expected PublicKey",
        ));
    };
    // Old controllers select v0; 1.5 controllers select the advertised v1.
    // Reject any version above the signed offer before deriving frame keys.
    let advertised = if fingerprint.is_empty() { 1 } else { 0 };
    if public_key.kx_version > advertised {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "peer selected a key exchange version not offered",
        ));
    }
    let peer_public: [u8; 32] = public_key
        .asymmetric_value
        .try_into()
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid peer public key"))?;
    let cipher = SalsaBox::new(&BoxPublicKey::from(peer_public), &ephemeral_secret);
    let symmetric = cipher
        .decrypt(
            BoxNonce::from_slice(&[0_u8; 24]),
            public_key.symmetric_value.as_slice(),
        )
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "session key decryption failed"))?;
    let symmetric: [u8; 32] = symmetric
        .try_into()
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid symmetric session key"))?;
    if !fingerprint.is_empty() {
        return Ok(None);
    }
    SessionKey::negotiated(
        symmetric,
        &peer_public,
        ephemeral_public.as_bytes(),
        advertised,
        public_key.kx_version,
        false,
    )
    .map(Some)
}

#[cfg(unix)]
async fn shutdown_signal() -> io::Result<()> {
    let mut terminate = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())?;
    tokio::select! {
        result = tokio::signal::ctrl_c() => result,
        _ = terminate.recv() => Ok(()),
    }
}

#[cfg(not(unix))]
async fn shutdown_signal() -> io::Result<()> {
    tokio::signal::ctrl_c().await
}

#[cfg(test)]
mod tests {
    use std::{
        path::PathBuf,
        sync::Arc,
        time::{Duration, SystemTime, UNIX_EPOCH},
    };

    use prost::Message as _;
    use sha2::{Digest, Sha256};
    use tokio::{
        io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader},
        net::{TcpListener, TcpStream, UnixListener},
        sync::{mpsc, oneshot, watch},
        time,
    };

    use crate::{
        config::Config,
        framing::{read_message, write_message, FrameReader, FrameWriter},
        identity::RustDeskIdentity,
        onekvm::{Codec, Identity, MediaFrame, VideoInfo},
        protocol::{
            login_response, message, Hash, LoginRequest, LoginResponse, Message, MouseEvent,
            OptionMessage, SupportedDecoding, TestDelay,
        },
    };

    use super::{
        client_supports_codec, peer_info, rustdesk_video_frame, serve, take_relative_chunk,
        verify_password, viewer_key, ClientLimits, PendingPointerMove,
    };

    #[tokio::test]
    async fn secure_handshake_supports_v0_v1_and_rejects_unoffered_versions() {
        use crate::protocol::{IdPk, PublicKey};
        use crypto_box::{aead::Aead as _, Nonce, SalsaBox, SecretKey};

        for selected in [0, 1, 2] {
            let root = temporary_directory();
            let identity = RustDeskIdentity::load_from(&root).unwrap();
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let address = listener.local_addr().unwrap();
            let server = tokio::spawn(async move {
                let (mut stream, _) = listener.accept().await.unwrap();
                let key = super::secure_handshake(&mut stream, &identity).await?;
                let mut writer = FrameWriter::new(stream, Some(key));
                writer
                    .write(&Message {
                        union: Some(message::Union::Hash(Hash {
                            salt: "salt".into(),
                            challenge: "challenge".into(),
                        })),
                    })
                    .await
            });
            let mut stream = TcpStream::connect(address).await.unwrap();
            let response: Message = read_message(&mut stream).await.unwrap();
            let Some(message::Union::SignedId(signed)) = response.union else {
                panic!("missing signed identity")
            };
            let id = IdPk::decode(&signed.id[64..]).unwrap();
            let peer_public: [u8; 32] = id.pk.try_into().unwrap();
            let secret = SecretKey::from([17_u8; 32]);
            let cipher = SalsaBox::new(&crypto_box::PublicKey::from(peer_public), &secret);
            let session_bytes = [29_u8; 32];
            let sealed = cipher
                .encrypt(Nonce::from_slice(&[0_u8; 24]), session_bytes.as_slice())
                .unwrap();
            write_message(
                &mut stream,
                &Message {
                    union: Some(message::Union::PublicKey(PublicKey {
                        asymmetric_value: secret.public_key().as_bytes().to_vec(),
                        symmetric_value: sealed,
                        kx_version: selected,
                    })),
                },
            )
            .await
            .unwrap();
            assert_eq!(id.kx_version, 1);
            if selected <= 1 {
                let key = crate::framing::SessionKey::negotiated(
                    session_bytes,
                    secret.public_key().as_bytes(),
                    &peer_public,
                    id.kx_version,
                    selected,
                    true,
                )
                .unwrap();
                let mut reader = FrameReader::new(stream, Some(key));
                let response: Message = reader.read().await.unwrap();
                assert!(matches!(response.union, Some(message::Union::Hash(_))));
                assert!(server.await.unwrap().is_ok());
            } else {
                let error = server.await.unwrap().unwrap_err();
                assert_eq!(error.kind(), std::io::ErrorKind::InvalidData);
                assert!(error.to_string().contains("not offered"));
            }
            std::fs::remove_dir_all(root).unwrap();
        }
    }

    #[tokio::test]
    async fn dtls_identity_is_signed_and_does_not_add_secretbox_framing() {
        use crate::protocol::{IdPk, PublicKey};
        use crypto_box::{aead::Aead as _, Nonce, SalsaBox, SecretKey};
        let root = temporary_directory();
        let identity = RustDeskIdentity::load_from(&root).unwrap();
        let (mut server, mut client) = tokio::net::UnixStream::pair().unwrap();
        let fingerprint = "sha-256 ".to_owned() + &vec!["AB"; 32].join(":");
        let expected = fingerprint.clone();
        let responder = tokio::spawn(async move {
            let key = super::identity_handshake(&mut server, &identity, &fingerprint)
                .await
                .unwrap();
            assert!(key.is_none());
            write_message(
                &mut server,
                &Message {
                    union: Some(message::Union::Hash(Hash {
                        salt: "s".into(),
                        challenge: "c".into(),
                    })),
                },
            )
            .await
            .unwrap();
        });
        let signed: Message = read_message(&mut client).await.unwrap();
        let Some(message::Union::SignedId(signed)) = signed.union else {
            panic!("unsigned DTLS identity")
        };
        let id = IdPk::decode(&signed.id[64..]).unwrap();
        assert_eq!(id.dtls_fingerprint, expected);
        assert_eq!(id.kx_version, 0);
        let secret = SecretKey::from([33; 32]);
        let pk: [u8; 32] = id.pk.try_into().unwrap();
        let sealed = SalsaBox::new(&crypto_box::PublicKey::from(pk), &secret)
            .encrypt(Nonce::from_slice(&[0; 24]), [7u8; 32].as_slice())
            .unwrap();
        write_message(
            &mut client,
            &Message {
                union: Some(message::Union::PublicKey(PublicKey {
                    asymmetric_value: secret.public_key().as_bytes().to_vec(),
                    symmetric_value: sealed,
                    kx_version: 0,
                })),
            },
        )
        .await
        .unwrap();
        let hash: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(hash.union, Some(message::Union::Hash(_))));
        responder.await.unwrap();
        std::fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn relay_viewers_are_not_collapsed_by_relay_ip() {
        let peer = "192.0.2.1:21117".parse().unwrap();
        let limits = ClientLimits::new(1);
        let first = viewer_key(peer, true, "viewer-a");
        let _permit = limits.try_session(first.clone()).unwrap();
        assert!(limits
            .try_session(viewer_key(peer, true, "viewer-b"))
            .is_none());
        let _candidate = limits.try_session(first.clone()).unwrap();
        let _candidate2 = limits.try_session(first.clone()).unwrap();
        let _candidate3 = limits.try_session(first.clone()).unwrap();
        assert!(limits.try_session(first).is_none());
        assert_ne!(
            viewer_key(peer, true, ""),
            viewer_key("192.0.2.1:21118".parse().unwrap(), true, "")
        );
    }

    #[test]
    fn pending_handshakes_and_duplicate_paths_share_one_client_slot() {
        let limits = ClientLimits::new(1);
        let _first_pending = limits.try_pending().unwrap();
        let _second_pending = limits.try_pending().unwrap();
        let first_path = limits.try_session("id:client-a".to_owned()).unwrap();
        let second_path = limits.try_session("id:client-a".to_owned()).unwrap();
        assert!(limits.try_session("id:client-b".to_owned()).is_none());
        drop(first_path);
        assert!(limits.try_session("id:client-b".to_owned()).is_none());
        drop(second_path);
        assert!(limits.try_session("id:client-b".to_owned()).is_some());
    }

    #[test]
    fn pointer_limiter_keeps_latest_absolute_position() {
        let mut pending = PendingPointerMove::new(MouseEvent {
            mask: 0,
            x: 100,
            y: 50,
            modifiers: Vec::new(),
        })
        .unwrap();
        pending
            .merge(MouseEvent {
                mask: 0,
                x: 200,
                y: 100,
                modifiers: Vec::new(),
            })
            .unwrap();
        pending
            .merge(MouseEvent {
                mask: 0,
                x: 300,
                y: 150,
                modifiers: Vec::new(),
            })
            .unwrap();

        let PendingPointerMove::Absolute(event) = pending else {
            panic!("absolute movement changed pointer mode");
        };
        assert_eq!((event.x, event.y), (300, 150));
    }

    #[test]
    fn pointer_limiter_preserves_and_chunks_relative_displacement() {
        let mut pending = PendingPointerMove::new(MouseEvent {
            mask: 5,
            x: 90,
            y: -100,
            modifiers: Vec::new(),
        })
        .unwrap();
        pending
            .merge(MouseEvent {
                mask: 5,
                x: 100,
                y: -80,
                modifiers: Vec::new(),
            })
            .unwrap();

        let PendingPointerMove::Relative { mut x, mut y, .. } = pending else {
            panic!("relative movement changed pointer mode");
        };
        let mut chunks = Vec::new();
        while x != 0 || y != 0 {
            chunks.push(take_relative_chunk(&mut x, &mut y));
        }
        assert_eq!(chunks, vec![(127, -127), (63, -53)]);
        assert_eq!(
            chunks
                .iter()
                .fold((0, 0), |sum, chunk| (sum.0 + chunk.0, sum.1 + chunk.1)),
            (190, -180)
        );
    }

    #[test]
    fn verifies_rustdesk_password_hash() {
        let hash = Hash {
            salt: "salt".to_owned(),
            challenge: "challenge".to_owned(),
        };
        let mut first = Sha256::new();
        first.update(b"onekvm-test");
        first.update(hash.salt.as_bytes());
        let mut second = Sha256::new();
        second.update(first.finalize());
        second.update(hash.challenge.as_bytes());
        let login = LoginRequest {
            password: second.finalize().to_vec(),
            ..Default::default()
        };
        assert!(verify_password("onekvm-test", &hash, &login));
        assert!(!verify_password("wrong-password", &hash, &login));
    }

    #[test]
    fn negotiates_and_wraps_h264_and_h265_video() {
        let legacy_login = LoginRequest::default();
        assert!(client_supports_codec(&legacy_login, Codec::H264));
        assert!(!client_supports_codec(&legacy_login, Codec::H265));

        let modern_login = LoginRequest {
            option: Some(OptionMessage {
                supported_decoding: Some(SupportedDecoding {
                    ability_h264: 1,
                    ability_h265: 1,
                    ..Default::default()
                }),
                ..Default::default()
            }),
            ..Default::default()
        };
        assert!(client_supports_codec(&modern_login, Codec::H264));
        assert!(client_supports_codec(&modern_login, Codec::H265));

        for codec in [Codec::H264, Codec::H265] {
            let info = peer_info(&VideoInfo {
                codec,
                width: 1920,
                height: 1080,
                fps: 60,
            });
            let encoding = info.encoding.unwrap();
            assert_eq!(encoding.h264, codec == Codec::H264);
            assert_eq!(encoding.h265, codec == Codec::H265);

            let video = rustdesk_video_frame(MediaFrame {
                sequence: 1,
                pts_usec: 1_234_000,
                duration_usec: 16_667,
                width: 1920,
                height: 1080,
                codec,
                keyframe: true,
                payload: vec![0, 0, 0, 1, 0x26].into(),
            })
            .unwrap();
            match (codec, video.union) {
                (Codec::H264, Some(message)) => {
                    assert!(matches!(
                        message,
                        crate::protocol::video_frame::Union::H264s(_)
                    ))
                }
                (Codec::H265, Some(message)) => {
                    assert!(matches!(
                        message,
                        crate::protocol::video_frame::Union::H265s(_)
                    ))
                }
                _ => panic!("video frame used the wrong RustDesk codec variant"),
            }
        }
    }

    #[tokio::test]
    async fn bridges_login_video_and_hid_with_mock_onekvm() {
        bridge_mock(true).await;
    }
    #[tokio::test]
    async fn view_only_session_streams_video_without_any_hid_or_lease_requests() {
        bridge_mock(false).await;
    }
    async fn bridge_mock(input_allowed: bool) {
        bridge_mock_with_options(input_allowed, false, false).await;
    }
    #[tokio::test]
    async fn client_view_only_at_login_never_claims_hid() {
        bridge_mock_with_options(true, true, false).await;
    }
    #[tokio::test]
    async fn client_can_enable_input_then_release_it_without_ending_video() {
        bridge_mock_with_options(true, true, true).await;
    }
    async fn bridge_mock_with_options(can_control: bool, initially_disabled: bool, toggle: bool) {
        let input_allowed = can_control && (!initially_disabled || toggle);
        let directory = temporary_directory();
        std::fs::create_dir_all(&directory).unwrap();
        let media_path = directory.join("media.sock");
        let hid_path = directory.join("control.sock");
        let media_listener = UnixListener::bind(&media_path).unwrap();
        let hid_listener = UnixListener::bind(&hid_path).unwrap();
        let (media_done_sender, media_done_receiver) = oneshot::channel();
        let (partial_header_sender, partial_header_receiver) = oneshot::channel();
        let (continue_frame_sender, continue_frame_receiver) = oneshot::channel();
        let (second_frame_sender, second_frame_receiver) = oneshot::channel();
        let (third_frame_sender, third_frame_receiver) = oneshot::channel();
        let media_task = tokio::spawn(async move {
            let (mut info, _) = media_listener.accept().await.unwrap();
            read_subscription(&mut info).await;
            write_media_frame(&mut info, &[], true).await;
            let (mut encoded, _) = media_listener.accept().await.unwrap();
            read_subscription(&mut encoded).await;
            let payload = [0, 0, 0, 1, 0x65, 0x88];
            let header = media_frame_header(&payload, true);
            encoded.write_all(&header[..8]).await.unwrap();
            let _ = partial_header_sender.send(());
            let _ = continue_frame_receiver.await;
            encoded.write_all(&header[8..]).await.unwrap();
            encoded.write_all(&payload).await.unwrap();
            let _ = second_frame_receiver.await;
            write_media_frame(&mut encoded, &[0, 0, 0, 1, 0x41, 0x99], false).await;
            let _ = third_frame_receiver.await;
            write_media_frame(&mut encoded, &[0, 0, 0, 1, 0x41, 0xaa], false).await;
            let _ = media_done_receiver.await;
        });
        let (hid_response_sender, hid_response_receiver) = oneshot::channel();
        let (hid_sender, mut hid_receiver) = mpsc::channel(6);
        let hid_task = tokio::spawn(async move {
            let mut hid_response_receiver = Some(hid_response_receiver);
            for request_index in 0..6 {
                let (mut stream, _) = hid_listener.accept().await.unwrap();
                let mut request = Vec::new();
                stream.read_to_end(&mut request).await.unwrap();
                hid_sender.send(request).await.unwrap();
                if request_index == 0 {
                    let _ = hid_response_receiver.take().unwrap().await;
                }
                stream
                    .write_all(b"HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
                    .await
                    .unwrap();
            }
        });

        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let client_connection = tokio::spawn(TcpStream::connect(address));
        let (server_stream, peer) = listener.accept().await.unwrap();
        let mut client = client_connection.await.unwrap().unwrap();
        let config = Arc::new(Config {
            service_enabled: true,
            password: "onekvm-test".to_owned(),
            media_socket: media_path.to_string_lossy().into_owned(),
            admin_socket: hid_path.to_string_lossy().into_owned(),
            ..Config::default()
        });
        let identity = Arc::new(Identity {
            extension_id: "rustdesk".to_owned(),
            token: "a".repeat(64),
        });
        let (_shutdown_sender, shutdown_receiver) = watch::channel(false);
        let mut limits = ClientLimits::new(1);
        limits.input_allowed = can_control;
        let pending_permit = limits.try_pending().unwrap();
        let rustdesk_identity =
            Arc::new(RustDeskIdentity::load_from(&directory.join("rustdesk-state")).unwrap());
        let server_task = tokio::spawn(serve(
            server_stream,
            peer,
            config,
            identity,
            rustdesk_identity,
            false,
            shutdown_receiver,
            limits,
            pending_permit,
        ));

        let challenge: Message = read_message(&mut client).await.unwrap();
        let Some(message::Union::Hash(hash)) = challenge.union else {
            panic!("server did not send a hash challenge");
        };
        write_message(
            &mut client,
            &Message {
                union: Some(message::Union::LoginRequest(LoginRequest {
                    my_id: "test-client".to_owned(),
                    my_name: "NanoKVM test".to_owned(),
                    version: crate::upstream::version().to_owned(),
                    my_platform: "Linux".to_owned(),
                    ..Default::default()
                })),
            },
        )
        .await
        .unwrap();
        let response: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(
            response.union,
            Some(message::Union::LoginResponse(LoginResponse {
                union: Some(login_response::Union::Error(ref error)),
                ..
            })) if error == "Empty Password"
        ));

        write_message(
            &mut client,
            &Message {
                union: Some(message::Union::LoginRequest(LoginRequest {
                    password: vec![0; 32],
                    my_id: "test-client".to_owned(),
                    my_name: "NanoKVM test".to_owned(),
                    version: crate::upstream::version().to_owned(),
                    my_platform: "Linux".to_owned(),
                    ..Default::default()
                })),
            },
        )
        .await
        .unwrap();
        let response: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(
            response.union,
            Some(message::Union::LoginResponse(LoginResponse {
                union: Some(login_response::Union::Error(ref error)),
                ..
            })) if error == "Wrong Password"
        ));

        let login = LoginRequest {
            option: Some(OptionMessage {
                disable_keyboard: if initially_disabled { 2 } else { 0 },
                ..Default::default()
            }),
            password: password_hash("onekvm-test", &hash),
            my_id: "test-client".to_owned(),
            my_name: "NanoKVM test".to_owned(),
            version: crate::upstream::version().to_owned(),
            my_platform: "Linux".to_owned(),
            ..Default::default()
        };
        write_message(
            &mut client,
            &Message {
                union: Some(message::Union::LoginRequest(login)),
            },
        )
        .await
        .unwrap();
        let response: Message = read_message(&mut client).await.unwrap();
        let Some(message::Union::LoginResponse(response)) = response.union else {
            panic!("server did not send LoginResponse");
        };
        let Some(login_response::Union::PeerInfo(peer_info)) = response.union else {
            panic!("server did not send peer info");
        };
        assert_eq!(peer_info.platform, "NanoKVM");
        assert!(!peer_info.displays[0].cursor_embedded);
        partial_header_receiver.await.unwrap();
        time::sleep(Duration::from_millis(10)).await;
        write_message(
            &mut client,
            &Message {
                union: Some(message::Union::TestDelay(TestDelay {
                    time: 123,
                    from_client: true,
                    ..Default::default()
                })),
            },
        )
        .await
        .unwrap();
        let delay: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(
            delay.union,
            Some(message::Union::TestDelay(TestDelay {
                time: 123,
                from_client: false,
                ..
            }))
        ));
        let _ = continue_frame_sender.send(());
        let video: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(video.union, Some(message::Union::VideoFrame(_))));

        let partial_delay = Message {
            union: Some(message::Union::TestDelay(TestDelay {
                time: 456,
                from_client: true,
                ..Default::default()
            })),
        };
        let payload = partial_delay.encode_to_vec();
        assert!(payload.len() <= 0x3f);
        let mut framed = vec![(payload.len() as u8) << 2];
        framed.extend_from_slice(&payload);
        client.write_all(&framed[..3]).await.unwrap();
        time::sleep(Duration::from_millis(10)).await;
        let _ = second_frame_sender.send(());
        let video: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(video.union, Some(message::Union::VideoFrame(_))));
        client.write_all(&framed[3..]).await.unwrap();
        let delay: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(
            delay.union,
            Some(message::Union::TestDelay(TestDelay {
                time: 456,
                from_client: false,
                ..
            }))
        ));

        if !can_control {
            write_message(
                &mut client,
                &Message {
                    union: Some(message::Union::Misc(crate::protocol::Misc {
                        union: Some(crate::protocol::misc::Union::Option(OptionMessage {
                            disable_keyboard: 1,
                            ..Default::default()
                        })),
                    })),
                },
            )
            .await
            .unwrap();
        }
        if toggle {
            assert!(
                time::timeout(Duration::from_millis(80), hid_receiver.recv())
                    .await
                    .is_err()
            );
            write_message(
                &mut client,
                &Message {
                    union: Some(message::Union::Misc(crate::protocol::Misc {
                        union: Some(crate::protocol::misc::Union::Option(OptionMessage {
                            disable_keyboard: 1,
                            ..Default::default()
                        })),
                    })),
                },
            )
            .await
            .unwrap();
        }
        write_message(
            &mut client,
            &Message {
                union: Some(message::Union::MouseEvent(MouseEvent {
                    mask: 0,
                    x: 100,
                    y: 50,
                    modifiers: Vec::new(),
                })),
            },
        )
        .await
        .unwrap();
        if input_allowed {
            let request = time::timeout(Duration::from_secs(2), hid_receiver.recv())
                .await
                .unwrap()
                .unwrap();
            assert!(request.starts_with(b"POST /api/hid/mouse/absolute HTTP/1.1\r\n"));
        }
        for (x, y) in [(200, 100), (300, 150)] {
            write_message(
                &mut client,
                &Message {
                    union: Some(message::Union::MouseEvent(MouseEvent {
                        mask: 0,
                        x,
                        y,
                        modifiers: Vec::new(),
                    })),
                },
            )
            .await
            .unwrap();
        }
        write_message(
            &mut client,
            &Message {
                union: Some(message::Union::MouseEvent(MouseEvent {
                    mask: 9,
                    x: 300,
                    y: 150,
                    modifiers: Vec::new(),
                })),
            },
        )
        .await
        .unwrap();
        time::sleep(Duration::from_millis(10)).await;
        let _ = third_frame_sender.send(());
        let video: Message = time::timeout(Duration::from_secs(2), read_message(&mut client))
            .await
            .expect("video stalled while the HID response was pending")
            .unwrap();
        assert!(matches!(video.union, Some(message::Union::VideoFrame(_))));
        let _ = hid_response_sender.send(());
        if input_allowed {
            let request = time::timeout(Duration::from_secs(2), hid_receiver.recv())
                .await
                .unwrap()
                .unwrap();
            let body_offset = request
                .windows(4)
                .position(|window| window == b"\r\n\r\n")
                .unwrap()
                + 4;
            let body: serde_json::Value = serde_json::from_slice(&request[body_offset..]).unwrap();
            assert_eq!(body["x"], (300_u32 * 32767) / 1919);
            assert_eq!(body["y"], (150_u32 * 32767) / 1079);
            assert_eq!(body["buttons"], 0);
            let request = time::timeout(Duration::from_secs(2), hid_receiver.recv())
                .await
                .unwrap()
                .unwrap();
            let body_offset = request
                .windows(4)
                .position(|window| window == b"\r\n\r\n")
                .unwrap()
                + 4;
            let body: serde_json::Value = serde_json::from_slice(&request[body_offset..]).unwrap();
            assert_eq!(body["x"], (300_u32 * 32767) / 1919);
            assert_eq!(body["y"], (150_u32 * 32767) / 1079);
            assert_eq!(body["buttons"], 1);
        }
        if toggle {
            write_message(
                &mut client,
                &Message {
                    union: Some(message::Union::Misc(crate::protocol::Misc {
                        union: Some(crate::protocol::misc::Union::Option(OptionMessage {
                            disable_keyboard: 2,
                            ..Default::default()
                        })),
                    })),
                },
            )
            .await
            .unwrap();
            // A disabled-input session still responds to its heartbeat.
            write_message(
                &mut client,
                &Message {
                    union: Some(message::Union::TestDelay(TestDelay {
                        time: 789,
                        from_client: true,
                        ..Default::default()
                    })),
                },
            )
            .await
            .unwrap();
            let response: Message = read_message(&mut client).await.unwrap();
            assert!(matches!(
                response.union,
                Some(message::Union::TestDelay(TestDelay { time: 789, .. }))
            ));
        }
        drop(client);
        server_task.await.unwrap().unwrap();
        if input_allowed {
            for _ in 0..3 {
                time::timeout(Duration::from_secs(2), hid_receiver.recv())
                    .await
                    .unwrap()
                    .unwrap();
            }
            hid_task.await.unwrap();
        } else {
            assert!(time::timeout(
                Duration::from_millis(if initially_disabled { 2200 } else { 200 }),
                hid_receiver.recv()
            )
            .await
            .is_err());
            hid_task.abort();
            let _ = hid_task.await;
        }
        let _ = media_done_sender.send(());
        media_task.await.unwrap();
        std::fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test]
    #[ignore = "requires a running target extension"]
    async fn direct_ip_media_survives_concurrent_control_messages() {
        let address = std::env::var("RUSTDESK_TEST_DIRECT").unwrap();
        let password = std::env::var("RUSTDESK_TEST_PASSWORD").unwrap();
        let mut stream = TcpStream::connect(address).await.unwrap();
        let challenge: Message = read_message(&mut stream).await.unwrap();
        let Some(message::Union::Hash(hash)) = challenge.union else {
            panic!("target did not send a hash challenge");
        };
        write_message(
            &mut stream,
            &Message {
                union: Some(message::Union::LoginRequest(LoginRequest {
                    password: password_hash(&password, &hash),
                    my_id: "media-cancellation-probe".to_owned(),
                    my_name: "NanoKVM media cancellation probe".to_owned(),
                    version: crate::upstream::version().to_owned(),
                    my_platform: "Linux".to_owned(),
                    option: Some(OptionMessage {
                        supported_decoding: Some(SupportedDecoding {
                            ability_h264: 1,
                            ability_h265: 1,
                            ..Default::default()
                        }),
                        ..Default::default()
                    }),
                    ..Default::default()
                })),
            },
        )
        .await
        .unwrap();
        let response: Message = read_message(&mut stream).await.unwrap();
        assert!(matches!(
            response.union,
            Some(message::Union::LoginResponse(response))
                if matches!(response.union, Some(login_response::Union::PeerInfo(_)))
        ));

        let (read_half, write_half) = stream.into_split();
        let mut reader = FrameReader::new(read_half, None);
        let mut writer = FrameWriter::new(write_half, None);
        let writer_task = tokio::spawn(async move {
            writer
                .write(&Message {
                    union: Some(message::Union::MouseEvent(MouseEvent {
                        mask: 0,
                        x: 960,
                        y: 540,
                        modifiers: Vec::new(),
                    })),
                })
                .await
                .unwrap();
            for sequence in 0..1_000_i64 {
                writer
                    .write(&Message {
                        union: Some(message::Union::TestDelay(TestDelay {
                            time: sequence,
                            from_client: true,
                            ..Default::default()
                        })),
                    })
                    .await
                    .unwrap();
                time::sleep(Duration::from_millis(5)).await;
            }
        });
        let frames = time::timeout(Duration::from_secs(20), async {
            let mut frames = 0;
            while frames < 120 {
                let message: Message = reader.read().await.unwrap();
                if matches!(message.union, Some(message::Union::VideoFrame(_))) {
                    frames += 1;
                }
            }
            frames
        })
        .await
        .expect("target did not sustain the video stream");
        writer_task.abort();
        let _ = writer_task.await;
        assert_eq!(frames, 120);
    }

    fn password_hash(password: &str, hash: &Hash) -> Vec<u8> {
        let mut first = Sha256::new();
        first.update(password.as_bytes());
        first.update(hash.salt.as_bytes());
        let mut second = Sha256::new();
        second.update(first.finalize());
        second.update(hash.challenge.as_bytes());
        second.finalize().to_vec()
    }

    async fn read_subscription(stream: &mut tokio::net::UnixStream) {
        let mut line = String::new();
        BufReader::new(stream).read_line(&mut line).await.unwrap();
        assert!(line.contains("\"version\":1"));
    }

    async fn write_media_frame(
        stream: &mut tokio::net::UnixStream,
        payload: &[u8],
        keyframe: bool,
    ) {
        let header = media_frame_header(payload, keyframe);
        stream.write_all(&header).await.unwrap();
        stream.write_all(payload).await.unwrap();
    }

    fn media_frame_header(payload: &[u8], keyframe: bool) -> [u8; 40] {
        let mut header = [0_u8; 40];
        header[..4].copy_from_slice(b"OKVF");
        header[4] = 1;
        header[5] = 1;
        header[6] = u8::from(keyframe);
        header[16..24].copy_from_slice(&1_000_000_i64.to_be_bytes());
        header[24..28].copy_from_slice(&16_667_u32.to_be_bytes());
        header[28..30].copy_from_slice(&1920_u16.to_be_bytes());
        header[30..32].copy_from_slice(&1080_u16.to_be_bytes());
        header[32..36].copy_from_slice(&(payload.len() as u32).to_be_bytes());
        header
    }

    fn temporary_directory() -> PathBuf {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        std::env::temp_dir().join(format!("onekvm-rustdesk-{}-{unique}", std::process::id()))
    }
}
