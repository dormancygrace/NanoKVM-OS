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
use rand::{distr::Alphanumeric, RngExt};
use sha2::{Digest, Sha256};
use tokio::{
    io::{AsyncRead, AsyncWrite, BufReader},
    net::{TcpListener, TcpStream, UnixStream},
    sync::{mpsc, watch, OwnedSemaphorePermit, Semaphore},
    task::JoinSet,
    time,
};

use crate::{
    config::Config,
    framing::{
        read_message_within, write_message, FrameReader, FrameWriter, SessionKey,
        MAX_LOGIN_PACKET_LENGTH,
    },
    identity::RustDeskIdentity,
    input::InputState,
    nanokvm::{self, Codec, HidClient, Identity, MediaFrame, MediaSubscriber, VideoInfo},
    protocol::{
        login_response, message, video_frame, DisplayInfo, EncodedVideoFrame, EncodedVideoFrames,
        Features, Hash, IdPk, KeyEvent, LoginRequest, LoginResponse, Message, MouseEvent, PeerInfo,
        SignedId, SupportedEncoding, VideoFrame,
    },
    rendezvous,
};

use prost::Message as _;

const LOGIN_TIMEOUT: Duration = Duration::from_secs(60);
// A controller answers the signed ID as soon as it reaches the relay, which
// may lag behind the device on a slow link. Only the password dialog needs
// the login timeout.
const HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(20);
const MAX_PASSWORD_ATTEMPTS: usize = 5;
const MAX_EMPTY_PROBES: usize = 3;
const EMPTY_PROBE_DELAY: Duration = Duration::from_millis(300);
const MEDIA_TIMEOUT: Duration = Duration::from_secs(5);
const INPUT_FLUSH_INTERVAL: Duration = Duration::from_millis(16);
// A connected viewer keeps its temporary-password reconnect grace alive;
// refreshing it once a second is plenty for a 30 s grace.
const SESSION_TOUCH_INTERVAL: Duration = Duration::from_secs(1);
const MOUSE_TYPE_MASK: i32 = 0x07;
const MOUSE_TYPE_MOVE: i32 = 0;
const MOUSE_TYPE_MOVE_RELATIVE: i32 = 5;
// Relative movement of one flush window, per axis: 32000 points a second,
// at most five HID reports. A larger value, such as i32::MAX from a broken or hostile
// client, would otherwise become millions of HID reports.
const MAX_RELATIVE_MOVE: i64 = 512;

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
    pub(crate) audio_allowed: bool,
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
    pub(crate) fn new(max_clients: usize) -> Self {
        Self {
            input_allowed: true,
            audio_allowed: true,
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
    pub(crate) fn with_permissions(
        &self,
        permissions: Option<&crate::protocol::ControlPermissions>,
    ) -> Self {
        let mut limits = self.clone();
        limits.input_allowed = self.input_allowed && permissions.map_or(true, |p| p.allows(0));
        limits.audio_allowed = self.audio_allowed && permissions.map_or(true, |p| p.allows(4));
        limits
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
                x: i64::from(event.x).clamp(-MAX_RELATIVE_MOVE, MAX_RELATIVE_MOVE),
                y: i64::from(event.y).clamp(-MAX_RELATIVE_MOVE, MAX_RELATIVE_MOVE),
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
                *x = x
                    .saturating_add(i64::from(event.x))
                    .clamp(-MAX_RELATIVE_MOVE, MAX_RELATIVE_MOVE);
                *y = y
                    .saturating_add(i64::from(event.y))
                    .clamp(-MAX_RELATIVE_MOVE, MAX_RELATIVE_MOVE);
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

// The first INPUT_FLUSH_INTERVAL boundary after `now` on the grid that
// starts at `grid`.
fn next_flush(grid: time::Instant, now: time::Instant) -> time::Instant {
    let interval = INPUT_FLUSH_INTERVAL.as_nanos();
    let into_frame = now.saturating_duration_since(grid).as_nanos() % interval;
    now + Duration::from_nanos((interval - into_frame) as u64)
}

// Keep a due timer ready until the pending batch is consumed. Other select
// branches may finish after its deadline; they must not postpone that batch.
fn arm_pending_flush(
    mut timer: std::pin::Pin<&mut time::Sleep>,
    grid: time::Instant,
    pending: bool,
    armed: &mut bool,
) {
    if pending && !*armed {
        timer.as_mut().reset(next_flush(grid, time::Instant::now()));
    }
    *armed = pending;
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
        let rendezvous_app = Arc::clone(&identity);
        let rendezvous_identity = Arc::clone(&rustdesk_identity);
        let rendezvous_shutdown = shutdown_receiver.clone();
        let rendezvous_limits = limits.clone();
        sessions.spawn(async move {
            rendezvous::run(
                rendezvous_config,
                rendezvous_app,
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
    // The status file only feeds the web UI. A failed write must not stop the
    // endpoint; it is logged once per kind of failure, not every tick.
    let mut status_error: Option<io::ErrorKind> = None;
    eprintln!("RustDesk direct-IP endpoint listening on {address}");
    loop {
        tokio::select! {
            _ = status_tick.tick() => {
                crate::connlog::flush();
                let count = limits.sessions.lock().unwrap_or_else(|p| p.into_inner()).clients.len();
                match crate::status::write(&rustdesk_identity.id, count) {
                    Ok(()) => {
                        if status_error.take().is_some() {
                            eprintln!("RustDesk status file is written again");
                        }
                    }
                    Err(error) => {
                        if status_error.replace(error.kind()) != Some(error.kind()) {
                            eprintln!("RustDesk status file not written: {error}");
                        }
                    }
                }
            }
            signal = shutdown_signal() => {
                signal?;
                break;
            }
            accepted = listener.accept() => {
                let (stream, peer) = accepted?;
                let Some(pending_permit) = limits.try_pending() else {
                    crate::connlog::connection_log!("rejecting RustDesk client {peer}: too many pending handshakes");
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
                            crate::connlog::connection_log!("RustDesk client {peer}: {error}");
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
    // Viewer input arrives as many small packets; the buffer reads a length
    // prefix and its payload, often several packets, in one call. Large
    // packets bypass it.
    let mut reader = FrameReader::new(BufReader::new(reader), session_key.clone());
    reader.set_max_length(MAX_LOGIN_PACKET_LENGTH);
    let mut writer = FrameWriter::new(writer, session_key);
    let hash = Hash {
        salt: rustdesk_identity.password_salt.clone(),
        challenge: random_string(6),
    };
    write_union(&mut writer, message::Union::Hash(hash.clone())).await?;

    let login_deadline = time::Instant::now() + LOGIN_TIMEOUT;
    let mut password_attempts = 0;
    let mut empty_probes = 0;
    let (login, session_permit) = loop {
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
            crate::connlog::connection_log!("rejecting RustDesk client {peer}: expected LoginRequest");
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
            crate::connlog::connection_log!(
                "rejecting RustDesk client {peer} (id {:?}): unsupported {session_type} session",
                login.my_id
            );
            send_error(&mut writer, "Unsupported session type").await?;
            return Ok(());
        }
        // The official client first sends an empty password to ask the server
        // whether authentication is required. Keep the connection and hash
        // challenge alive so the password dialog can submit another request.
        // Empty probes do not consume an attempt or slow down the answer, but
        // a connection gets only a few, and the overall deadline still
        // prevents them from holding a pending slot indefinitely.
        let admission = {
            let mut credentials = limits.credentials.lock().unwrap_or_else(|p| p.into_inner());
            let credentials =
                credentials.get_or_insert_with(|| crate::auth::Credentials::new(&config));
            if let Some(verified) = credentials.verify(&hash, &login)? {
                match limits.try_session(viewer_key(peer, is_relay, &login.my_id)) {
                    Some(permit) => {
                        credentials.admitted(&login, verified)?;
                        Ok(permit)
                    }
                    None => Err(None),
                }
            } else if login.password.is_empty() {
                Err(Some(EMPTY_PROBE_DELAY))
            } else {
                Err(Some(credentials.failed()))
            }
        };
        let delay = match admission {
            Ok(permit) => break (login, permit),
            Err(None) => {
                send_error(&mut writer, "Maximum viewers reached").await?;
                return Ok(());
            }
            Err(Some(delay)) => delay,
        };
        if login.password.is_empty() {
            empty_probes += 1;
            if empty_probes > MAX_EMPTY_PROBES {
                crate::connlog::connection_log!(
                    "rejecting RustDesk client {peer} (id {:?}): too many empty password probes",
                    login.my_id
                );
                return Ok(());
            }
        } else {
            password_attempts += 1;
        }
        let authentication_error = if login.password.is_empty() {
            "Empty Password"
        } else {
            "Wrong Password"
        };
        crate::connlog::connection_log!(
            "RustDesk client {peer} (id {:?}) needs password authentication ({} bytes)",
            login.my_id,
            login.password.len()
        );
        // The wait ends at the login deadline at the latest: it must not
        // extend how long the connection holds its pending permit.
        tokio::select! {
            _ = time::sleep_until((time::Instant::now() + delay).min(login_deadline)) => {}
            result = shutdown.changed() => {
                let _ = result;
                return Ok(());
            }
        }
        // The pending permit was held throughout; a delay past the login
        // deadline ends the connection.
        if time::Instant::now() >= login_deadline {
            return Ok(());
        }
        send_error(&mut writer, authentication_error).await?;
        if password_attempts >= MAX_PASSWORD_ATTEMPTS {
            eprintln!(
                "rejecting RustDesk client {peer} (id {:?}): too many wrong password attempts",
                login.my_id
            );
            return Ok(());
        }
    };
    // A controller sends only input, options and pings; nothing it sends
    // comes near the login limit. A larger packet of repeated empty fields
    // would decode into far more memory than its size.
    reader.set_max_length(MAX_LOGIN_PACKET_LENGTH);

    let video_info = time::timeout(
        MEDIA_TIMEOUT,
        nanokvm::query_video_info(&config.media_socket, &identity),
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
    // Pin the negotiated codec for this session. A shared setting change must
    // reconnect instead of sending H.265 frames to an H.264-only decoder.
    let video_profile = nanokvm::VideoProfile {
        codec: video_info.codec,
    };

    write_union(
        &mut writer,
        message::Union::LoginResponse(LoginResponse {
            union: Some(login_response::Union::PeerInfo(peer_info(&video_info))),
            enable_trusted_devices: false,
        }),
    )
    .await?;

    // Re-establish negotiated input permission on every login. Mobile clients
    // can retain the denial sent after an earlier HID failure across reconnects.
    write_union(
        &mut writer,
        message::Union::Misc(crate::protocol::Misc {
            union: Some(crate::protocol::misc::Union::PermissionInfo(
                crate::protocol::PermissionInfo {
                    permission: 0,
                    enabled: limits.input_allowed,
                },
            )),
        }),
    )
    .await?;

    drop(pending_permit);
    eprintln!(
        "RustDesk client {peer} login accepted (id {:?}, secure {is_relay})",
        login.my_id
    );
    let media = time::timeout(
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
        // next frame boundary (about 8 ms on average), not a full frame. The
        // timer is armed only while movement is pending, so an idle session
        // does not wake every frame.
        let mut heartbeat = time::interval(Duration::from_secs(2));
        heartbeat.tick().await;
        let flush_grid = time::Instant::now();
        let flush_timer = time::sleep_until(flush_grid + INPUT_FLUSH_INTERVAL);
        tokio::pin!(flush_timer);
        let mut flush_armed = false;
        let result = loop {
            arm_pending_flush(
                flush_timer.as_mut(),
                flush_grid,
                pending_move.is_some(),
                &mut flush_armed,
            );
            let event = tokio::select! {
                event = input_receiver.recv() => event,
                _ = heartbeat.tick() => {
                    if enabled { active=true; if let Err(error) = input.heartbeat().await { break Err(error); } }
                    continue;
                },
                _ = &mut flush_timer, if pending_move.is_some() => {
                    flush_armed = false;
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
                                flush_armed = false;
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
        // Report loss of input immediately. Release reports may themselves
        // time out when USB is off; viewing must remain independent of them.
        drop(input_receiver);
        let _ = input_result_sender.send(result).await;
        if active {
            input.release_all().await;
        }
    });
    // AsyncReadExt::read_exact is not cancellation-safe. Reading a media frame
    // directly inside the session select would lose a partially consumed
    // header whenever a client input message won the race. Keep the media read
    // in its own task and pass only complete frames to the session loop.
    let (media_sender, mut media_receiver) = mpsc::channel(1);
    let media_task = tokio::spawn(forward_media(media, media_sender));
    // FrameReader::read also uses read_exact internally and has the same
    // cancellation constraint. Keep it alive across media sends so a partial
    // RustDesk packet cannot be mistaken for the next packet.
    let (incoming_sender, mut incoming_receiver) = mpsc::channel(16);
    let can_audio = limits.audio_allowed && config.audio_enabled;
    let (audio_desired, audio_setting) =
        watch::channel(can_audio && login.option.as_ref().map_or(true, |o| o.disable_audio != 2));
    let audio_current = audio_setting.clone();
    let (audio_task, mut audio_state) =
        crate::audio::start(config.audio_socket.clone(), can_audio, audio_setting);
    let incoming_limits = limits.clone();
    let incoming_session = crate::auth::Credentials::session(&login);
    let incoming_task = tokio::spawn(async move {
        let mut enabled = input_allowed;
        // Admission has just refreshed the reconnect grace.
        let mut last_touch = time::Instant::now();
        loop {
            // A stalled/half-open peer releases HID and its viewer slot. The
            // whole stream ends on timeout, so partial reads are never reused.
            let message = time::timeout(Duration::from_secs(30), reader.read::<Message>())
                .await
                .unwrap_or_else(|_| {
                    Err(io::Error::new(io::ErrorKind::TimedOut, "peer idle timeout"))
                });
            if let Some(session) = incoming_session.as_ref() {
                if message.is_ok() && last_touch.elapsed() >= SESSION_TOUCH_INTERVAL {
                    last_touch = time::Instant::now();
                    if let Some(credentials) = incoming_limits
                        .credentials
                        .lock()
                        .unwrap_or_else(|p| p.into_inner())
                        .as_mut()
                    {
                        credentials.touch(session);
                    }
                }
            }
            match message {
                Ok(Message {
                    union:
                        Some(message::Union::Misc(crate::protocol::Misc {
                            union: Some(crate::protocol::misc::Union::Option(option)),
                        })),
                }) => {
                    if matches!(option.disable_audio, 1 | 2) {
                        let next = can_audio && option.disable_audio == 1;
                        audio_desired.send_if_modified(|value| {
                            if *value == next {
                                false
                            } else {
                                *value = next;
                                true
                            }
                        });
                    }
                    if matches!(option.disable_keyboard, 1 | 2) {
                        enabled = can_control && option.disable_keyboard == 1;
                        if input_sender
                            .send(InputEvent::Enabled(enabled))
                            .await
                            .is_err()
                        {
                            enabled = false;
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
                        enabled = false;
                    }
                }
                Ok(Message {
                    union: Some(message::Union::KeyEvent(event)),
                }) => {
                    if enabled && input_sender.send(InputEvent::Key(event)).await.is_err() {
                        enabled = false;
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
    let mut audio_updates_open = true;
    let mut audio_permission = None;
    let mut audio_generation = 0;
    let mut input_updates_open = true;
    let result = loop {
        tokio::select! {
            changed=audio_state.changed(), if audio_updates_open => {
                if changed.is_err(){audio_updates_open=false;continue;}
                let state=audio_state.borrow_and_update().clone();
                if let Some(available)=state.available {
                    if audio_permission!=Some(available) {
                        let permission=crate::protocol::PermissionInfo{permission:3,enabled:available};
                        if let Err(error)=write_union(&mut writer,message::Union::Misc(crate::protocol::Misc{union:Some(crate::protocol::misc::Union::PermissionInfo(permission))})).await{break Err(error);}
                        audio_permission=Some(available);
                    }
                }
                if state.available==Some(true) && *audio_current.borrow() && state.generation!=0 {
                    if audio_generation!=state.generation {
                        let format=crate::protocol::AudioFormat{sample_rate:crate::audio::SAMPLE_RATE,channels:crate::audio::CHANNELS};
                        if let Err(error)=write_union(&mut writer,message::Union::Misc(crate::protocol::Misc{union:Some(crate::protocol::misc::Union::AudioFormat(format))})).await{break Err(error);}
                        audio_generation=state.generation;
                    }
                    if let Some((received,data))=state.packet {
                        if received.elapsed()<=crate::audio::MAX_AGE {
                            if let Err(error)=write_union(&mut writer,message::Union::AudioFrame(crate::protocol::AudioFrame{data})).await{break Err(error);}
                        }
                    }
                }
            }
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
            input = input_result_receiver.recv(), if input_updates_open => {
                input_updates_open = false;
                if let Some(Err(error)) = input {
                    eprintln!("RustDesk input unavailable for {peer}; continuing video: {error}");
                    let permission=crate::protocol::PermissionInfo{permission:0,enabled:false};
                    if let Err(error)=write_union(&mut writer,message::Union::Misc(crate::protocol::Misc{union:Some(crate::protocol::misc::Union::PermissionInfo(permission))})).await{break Err(error);}
                }
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
    audio_task.abort();
    let _ = audio_task.await;
    let _ = input_shutdown_sender.send(true);
    retire_input(input_task, session_permit);
    result
}

// The session ends at once, but its viewer slot stays taken until the input
// task has ended: that task releases held keys and buttons and closes the
// HID lease last, and a viewer admitted before would find the lease still
// held and lose its input. The task is never aborted, which could strand
// held reports; each HID request has its own timeout, and the clamped
// pointer movement bounds how many remain.
fn retire_input(input_task: tokio::task::JoinHandle<()>, permit: SessionPermit) {
    tokio::spawn(async move {
        let _ = input_task.await;
        drop(permit);
    });
}

// The bridge drops a subscriber that stops reading for 2 s, which ended the
// session of any viewer slower than the encoder. A frame the session writer
// is not ready for within MEDIA_GRACE is dropped here instead, and so are
// the frames after it until a keyframe, so the bridge socket keeps being
// drained. A keyframe is requested when that starts, and again every
// second while it lasts, in case the requested one was dropped too.
const MEDIA_GRACE: Duration = Duration::from_millis(250);
const KEYFRAME_REQUEST_INTERVAL: Duration = Duration::from_secs(1);

async fn forward_media(
    mut media: MediaSubscriber,
    sender: mpsc::Sender<io::Result<MediaFrame>>,
) {
    let mut dropping = false;
    let mut requested: Option<time::Instant> = None;
    loop {
        let frame = match media.read_frame().await {
            Ok(frame) => frame,
            Err(error) => {
                let _ = sender.send(Err(error)).await;
                return;
            }
        };
        if !dropping || frame.keyframe {
            match sender.send_timeout(Ok(frame), MEDIA_GRACE).await {
                Ok(()) => {
                    dropping = false;
                    requested = None;
                    continue;
                }
                Err(mpsc::error::SendTimeoutError::Timeout(_)) => dropping = true,
                Err(mpsc::error::SendTimeoutError::Closed(_)) => return,
            }
        }
        if requested.is_none_or(|at| at.elapsed() >= KEYFRAME_REQUEST_INTERVAL) {
            requested = Some(time::Instant::now());
            if let Err(error) = media.request_key_frame().await {
                let _ = sender.send(Err(error)).await;
                return;
            }
        }
    }
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
    rand::rng()
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
    rand::rng().fill(&mut ephemeral_secret);
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

    let response: Message = time::timeout(
        HANDSHAKE_TIMEOUT,
        read_message_within(stream, MAX_LOGIN_PACKET_LENGTH),
    )
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
        nanokvm::{Codec, Identity, MediaFrame, VideoInfo},
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

    #[tokio::test]
    async fn the_viewer_slot_waits_for_the_input_cleanup() {
        let limits = ClientLimits::new(1);
        let permit = limits.try_session("id:old".to_owned()).unwrap();
        let (release, released) = oneshot::channel::<()>();
        // The old session's input task, still closing its HID lease.
        let input_task = tokio::spawn(async move {
            let _ = released.await;
        });
        super::retire_input(input_task, permit);
        time::sleep(Duration::from_millis(50)).await;
        assert!(
            limits.try_session("id:new".to_owned()).is_none(),
            "a new viewer was admitted while the old one still held HID"
        );
        release.send(()).unwrap();
        let deadline = time::Instant::now() + Duration::from_secs(2);
        let admitted = loop {
            if let Some(permit) = limits.try_session("id:new".to_owned()) {
                break Some(permit);
            }
            if time::Instant::now() > deadline {
                break None;
            }
            time::sleep(Duration::from_millis(10)).await;
        };
        assert!(admitted.is_some(), "the slot was not freed after the cleanup");
    }

    #[tokio::test(start_paused = true)]
    async fn pointer_flush_stays_due_after_competing_work() {
        let grid = time::Instant::now();
        let timer = time::sleep_until(grid);
        tokio::pin!(timer);
        let mut armed = false;
        super::arm_pending_flush(timer.as_mut(), grid, true, &mut armed);
        let deadline = timer.deadline();
        assert_eq!(deadline - grid, super::INPUT_FLUSH_INTERVAL);
        // A heartbeat or another ready input branch held the worker past the
        // frame boundary. Re-entering select must deliver this batch now.
        time::advance(Duration::from_millis(20)).await;
        super::arm_pending_flush(timer.as_mut(), grid, true, &mut armed);
        assert_eq!(timer.deadline(), deadline);
        time::timeout(Duration::ZERO, timer.as_mut()).await.unwrap();
    }

    #[tokio::test(start_paused = true)]
    async fn pointer_flush_rearms_after_discard_or_consumption() {
        let grid = time::Instant::now();
        let timer = time::sleep_until(grid);
        tokio::pin!(timer);
        let mut armed = false;
        for _ in 0..2 {
            super::arm_pending_flush(timer.as_mut(), grid, false, &mut armed);
            time::advance(Duration::from_millis(101)).await;
            super::arm_pending_flush(timer.as_mut(), grid, true, &mut armed);
            let delay = timer.deadline() - time::Instant::now();
            assert!(delay > Duration::ZERO && delay <= super::INPUT_FLUSH_INTERVAL);
            time::advance(delay).await;
            timer.as_mut().await;
        }
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
    fn pointer_limiter_bounds_relative_displacement() {
        let event = |x, y| MouseEvent {
            mask: 5,
            x,
            y,
            modifiers: Vec::new(),
        };
        let mut pending = PendingPointerMove::new(event(i32::MAX, i32::MIN)).unwrap();
        for _ in 0..4 {
            pending.merge(event(i32::MAX, i32::MIN)).unwrap();
        }
        let PendingPointerMove::Relative { mut x, mut y, .. } = pending else {
            panic!("relative movement changed pointer mode");
        };
        assert_eq!((x, y), (super::MAX_RELATIVE_MOVE, -super::MAX_RELATIVE_MOVE));
        let mut chunks = 0;
        while x != 0 || y != 0 {
            take_relative_chunk(&mut x, &mut y);
            chunks += 1;
        }
        assert_eq!(chunks, 5);
    }

    #[test]
    fn rendezvous_permissions_cannot_override_local_denials() {
        let mut limits = ClientLimits::new(1);
        limits.input_allowed = false;
        limits.audio_allowed = false;
        for permissions in [
            None,
            Some(crate::protocol::ControlPermissions {
                permissions: 2 | (2 << 8),
            }),
        ] {
            let resolved = limits.with_permissions(permissions.as_ref());
            assert!(!resolved.input_allowed && !resolved.audio_allowed);
        }
    }

    #[test]
    fn verifies_rustdesk_password_hash() {
        let hash = Hash {
            salt: "salt".to_owned(),
            challenge: "challenge".to_owned(),
        };
        let mut first = Sha256::new();
        first.update(b"rustdesk-test");
        first.update(hash.salt.as_bytes());
        let mut second = Sha256::new();
        second.update(first.finalize());
        second.update(hash.challenge.as_bytes());
        let login = LoginRequest {
            password: second.finalize().to_vec(),
            ..Default::default()
        };
        assert!(verify_password("rustdesk-test", &hash, &login));
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
    async fn bridges_login_video_and_hid_with_mock_app() {
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
    #[tokio::test]
    async fn authenticated_audio_mutes_resumes_and_never_overrides_server_denial() {
        for (server_allowed, configured_audio) in [(true, true), (false, true), (true, false)] {
            let allowed = server_allowed && configured_audio;
            let directory = temporary_directory();
            std::fs::create_dir_all(&directory).unwrap();
            let media_path = directory.join("media.sock");
            let audio_path = directory.join("audio.sock");
            let media_listener = UnixListener::bind(&media_path).unwrap();
            let audio_listener = UnixListener::bind(&audio_path).unwrap();
            let media_task = tokio::spawn(async move {
                let (mut info, _) = media_listener.accept().await.unwrap();
                read_subscription(&mut info).await;
                write_media_frame(&mut info, &[], true).await;
                let (mut encoded, _) = media_listener.accept().await.unwrap();
                read_subscription(&mut encoded).await;
                loop {
                    write_media_frame(&mut encoded, &[0, 0, 0, 1, 0x65, 0x88], true).await;
                    time::sleep(Duration::from_millis(20)).await;
                }
            });
            let (captures, mut capture_events) = mpsc::channel(8);
            let audio_task = tokio::spawn(async move {
                loop {
                    let (mut stream, _) = audio_listener.accept().await.unwrap();
                    let mut line = String::new();
                    BufReader::new(&mut stream)
                        .read_line(&mut line)
                        .await
                        .unwrap();
                    let command: serde_json::Value = serde_json::from_str(&line).unwrap();
                    let mut header = [0; 16];
                    header[..4].copy_from_slice(b"OKAF");
                    header[4] = 1;
                    header[5] = 1;
                    header[6] = 2;
                    header[8..12].copy_from_slice(&48000u32.to_be_bytes());
                    stream.write_all(&header).await.unwrap();
                    if command["audio"] == "opus" {
                        captures.send(true).await.unwrap();
                        let captures = captures.clone();
                        tokio::spawn(async move {
                            header[12..14].copy_from_slice(&3u16.to_be_bytes());
                            loop {
                                tokio::select! {
                                    _ = stream.read_u8() => break,
                                    _ = time::sleep(Duration::from_millis(20)) => {
                                        if stream.write_all(&header).await.is_err() || stream.write_all(&[0xfc, 0xff, 0xfe]).await.is_err() { break; }
                                    }
                                }
                            }
                            let _ = captures.send(false).await;
                        });
                    }
                }
            });
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let mut client = TcpStream::connect(listener.local_addr().unwrap())
                .await
                .unwrap();
            let (stream, peer) = listener.accept().await.unwrap();
            let config = Arc::new(Config {
                password: "audio-fixture-password".into(),
                audio_enabled: configured_audio,
                media_socket: media_path.to_string_lossy().into_owned(),
                audio_socket: audio_path.to_string_lossy().into_owned(),
                ..Config::default()
            });
            let identity = Arc::new(Identity {
                extension_id: "rustdesk".into(),
                token: "a".repeat(64),
            });
            let rustdesk_identity =
                Arc::new(RustDeskIdentity::load_from(&directory.join("identity")).unwrap());
            let (_shutdown, shutdown) = watch::channel(false);
            let mut limits = ClientLimits::new(1);
            limits.audio_allowed = server_allowed;
            let permit = limits.try_pending().unwrap();
            let server = tokio::spawn(serve(
                stream,
                peer,
                config,
                identity,
                rustdesk_identity,
                false,
                shutdown,
                limits,
                permit,
            ));
            let Some(message::Union::Hash(hash)) =
                read_message::<_, Message>(&mut client).await.unwrap().union
            else {
                panic!("missing challenge");
            };
            write_message(
                &mut client,
                &Message {
                    union: Some(message::Union::LoginRequest(LoginRequest {
                        my_id: "audio-test-controller".into(),
                        my_name: "Audio qualification".into(),
                        password: password_hash("audio-fixture-password", &hash),
                        option: Some(OptionMessage {
                            disable_keyboard: 2,
                            ..Default::default()
                        }),
                        ..Default::default()
                    })),
                },
            )
            .await
            .unwrap();
            let mut format_seen = false;
            time::timeout(Duration::from_secs(2), async {
                loop {
                    match read_message::<_, Message>(&mut client).await.unwrap().union {
                        Some(message::Union::Misc(crate::protocol::Misc {
                            union: Some(crate::protocol::misc::Union::AudioFormat(f)),
                        })) => {
                            assert_eq!((f.sample_rate, f.channels), (48000, 2));
                            format_seen = true;
                        }
                        Some(message::Union::AudioFrame(f)) => {
                            assert!(allowed && format_seen);
                            assert_eq!(&f.data[..], &[0xfc, 0xff, 0xfe]);
                            break;
                        }
                        Some(message::Union::Misc(crate::protocol::Misc {
                            union: Some(crate::protocol::misc::Union::PermissionInfo(p)),
                        })) if !allowed && p.permission == 3 => {
                            assert_eq!((p.permission, p.enabled), (3, false));
                            break;
                        }
                        _ => {}
                    }
                }
            })
            .await
            .unwrap();
            if allowed {
                assert_eq!(capture_events.recv().await, Some(true));
            }
            // Both mute and a denied client's explicit unmute use the normal options.
            write_message(
                &mut client,
                &Message {
                    union: Some(message::Union::Misc(crate::protocol::Misc {
                        union: Some(crate::protocol::misc::Union::Option(OptionMessage {
                            disable_audio: if allowed { 2 } else { 1 },
                            ..Default::default()
                        })),
                    })),
                },
            )
            .await
            .unwrap();
            if allowed {
                assert_eq!(
                    time::timeout(Duration::from_secs(1), capture_events.recv())
                        .await
                        .unwrap(),
                    Some(false)
                );
            }
            write_message(
                &mut client,
                &Message {
                    union: Some(message::Union::TestDelay(crate::protocol::TestDelay {
                        time: 42,
                        from_client: true,
                        ..Default::default()
                    })),
                },
            )
            .await
            .unwrap();
            time::timeout(Duration::from_secs(1), async {
                loop { if matches!(read_message::<_, Message>(&mut client).await.unwrap().union, Some(message::Union::TestDelay(d)) if d.time == 42 && !d.from_client) { break; } }
            }).await.unwrap();
            let mut video = 0;
            while video < 3 {
                match read_message::<_, Message>(&mut client).await.unwrap().union {
                    Some(message::Union::VideoFrame(_)) => video += 1,
                    Some(message::Union::AudioFrame(_)) => {
                        panic!("audio sent while muted or denied")
                    }
                    _ => {}
                }
            }
            if allowed {
                write_message(
                    &mut client,
                    &Message {
                        union: Some(message::Union::Misc(crate::protocol::Misc {
                            union: Some(crate::protocol::misc::Union::Option(OptionMessage {
                                disable_audio: 1,
                                ..Default::default()
                            })),
                        })),
                    },
                )
                .await
                .unwrap();
                assert_eq!(
                    time::timeout(Duration::from_secs(2), capture_events.recv())
                        .await
                        .unwrap(),
                    Some(true)
                );
                format_seen = false;
                time::timeout(Duration::from_secs(1), async {
                    loop {
                        match read_message::<_, Message>(&mut client).await.unwrap().union {
                            Some(message::Union::Misc(crate::protocol::Misc {
                                union: Some(crate::protocol::misc::Union::AudioFormat(_)),
                            })) => format_seen = true,
                            Some(message::Union::AudioFrame(_)) => {
                                assert!(format_seen);
                                break;
                            }
                            _ => {}
                        }
                    }
                })
                .await
                .unwrap();
            } else {
                assert!(
                    time::timeout(Duration::from_millis(100), capture_events.recv())
                        .await
                        .is_err()
                );
            }
            drop(client);
            time::timeout(Duration::from_secs(1), server)
                .await
                .unwrap()
                .unwrap()
                .unwrap();
            if allowed {
                assert_eq!(
                    time::timeout(Duration::from_secs(1), capture_events.recv())
                        .await
                        .unwrap(),
                    Some(false)
                );
            }
            media_task.abort();
            let _ = media_task.await;
            audio_task.abort();
            let _ = audio_task.await;
            std::fs::remove_dir_all(directory).unwrap();
        }
    }
    #[tokio::test]
    async fn hid_rejection_disables_input_without_ending_video_or_peer_messages() {
        for status in [409, 504] {
            bridge_mock_with_hid_failure(true, false, false, Some(status)).await;
        }
    }
    async fn bridge_mock_with_options(can_control: bool, initially_disabled: bool, toggle: bool) {
        bridge_mock_with_hid_failure(can_control, initially_disabled, toggle, None).await;
    }
    async fn bridge_mock_with_hid_failure(
        can_control: bool,
        initially_disabled: bool,
        toggle: bool,
        failure_status: Option<u16>,
    ) {
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
        let (fourth_frame_sender, fourth_frame_receiver) = oneshot::channel();
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
            if failure_status.is_some() {
                let _ = fourth_frame_receiver.await;
                write_media_frame(&mut encoded, &[0, 0, 0, 1, 0x41, 0xbb], false).await;
            }
            let _ = media_done_receiver.await;
        });
        let (hid_response_sender, hid_response_receiver) = oneshot::channel();
        let (hid_sender, mut hid_receiver) = mpsc::channel(6);
        let hid_task = tokio::spawn(async move {
            let mut hid_response_receiver = Some(hid_response_receiver);
            for request_index in 0..6 {
                let (mut stream, _) = hid_listener.accept().await.unwrap();
                let request = crate::nanokvm::read_test_http_request(&mut stream).await;
                hid_sender.send(request).await.unwrap();
                if request_index == 0 {
                    let _ = hid_response_receiver.take().unwrap().await;
                }
                let status = if request_index == 0 {
                    failure_status.unwrap_or(204)
                } else {
                    204
                };
                let response = format!("HTTP/1.1 {status} Test\r\nConnection: close\r\n\r\n");
                stream.write_all(response.as_bytes()).await.unwrap();
            }
        });

        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let client_connection = tokio::spawn(TcpStream::connect(address));
        let (server_stream, peer) = listener.accept().await.unwrap();
        let mut client = client_connection.await.unwrap().unwrap();
        let config = Arc::new(Config {
            service_enabled: true,
            codec: "h265".to_owned(), // A stale preference must not override H.264 metadata.
            password: "rustdesk-test".to_owned(),
            media_socket: media_path.to_string_lossy().into_owned(),
            audio_socket: directory
                .join("absent-audio.sock")
                .to_string_lossy()
                .into_owned(),
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
            password: password_hash("rustdesk-test", &hash),
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
        let keyboard_permission: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(
            keyboard_permission.union,
            Some(message::Union::Misc(crate::protocol::Misc {
                union: Some(crate::protocol::misc::Union::PermissionInfo(
                    crate::protocol::PermissionInfo { permission: 0, enabled }
                ))
            })) if enabled == can_control
        ));
        let permission: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(
            permission.union,
            Some(message::Union::Misc(crate::protocol::Misc {
                union: Some(crate::protocol::misc::Union::PermissionInfo(
                    crate::protocol::PermissionInfo {
                        permission: 3,
                        enabled: false
                    }
                ))
            }))
        ));
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
        // The reply is a network-reader barrier: both moves and the button
        // event are queued before the first HID response is released. A fixed
        // sleep can release it early on a loaded runner and expose the
        // intermediate (200, 100) move instead of the coalesced final position.
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
        let barrier: Message = time::timeout(Duration::from_secs(2), read_message(&mut client))
            .await
            .expect("network reader stalled while the HID response was pending")
            .unwrap();
        assert!(matches!(
            barrier.union,
            Some(message::Union::TestDelay(TestDelay {
                time: 789,
                from_client: false,
                ..
            }))
        ));
        let _ = third_frame_sender.send(());
        let video: Message = time::timeout(Duration::from_secs(2), read_message(&mut client))
            .await
            .expect("video stalled while the HID response was pending")
            .unwrap();
        assert!(matches!(video.union, Some(message::Union::VideoFrame(_))));
        let _ = hid_response_sender.send(());
        if failure_status.is_some() {
            let permission: Message =
                time::timeout(Duration::from_secs(2), read_message(&mut client))
                    .await
                    .unwrap()
                    .unwrap();
            assert!(matches!(
                permission.union,
                Some(message::Union::Misc(crate::protocol::Misc {
                    union: Some(crate::protocol::misc::Union::PermissionInfo(
                        crate::protocol::PermissionInfo {
                            permission: 0,
                            enabled: false
                        }
                    ))
                }))
            ));
            // A closed input worker must not close the network reader when the
            // client sends another move or tries enabling input again.
            for union in [
                message::Union::MouseEvent(MouseEvent {
                    mask: 0,
                    x: 400,
                    y: 200,
                    ..Default::default()
                }),
                message::Union::Misc(crate::protocol::Misc {
                    union: Some(crate::protocol::misc::Union::Option(OptionMessage {
                        disable_keyboard: 1,
                        ..Default::default()
                    })),
                }),
                message::Union::MouseEvent(MouseEvent {
                    mask: 0,
                    x: 500,
                    y: 250,
                    ..Default::default()
                }),
                message::Union::KeyEvent(crate::protocol::KeyEvent::default()),
                message::Union::TestDelay(TestDelay {
                    time: 987,
                    from_client: true,
                    ..Default::default()
                }),
            ] {
                write_message(&mut client, &Message { union: Some(union) })
                    .await
                    .unwrap();
            }
            let reply: Message = time::timeout(Duration::from_secs(2), read_message(&mut client))
                .await
                .unwrap()
                .unwrap();
            assert!(matches!(
                reply.union,
                Some(message::Union::TestDelay(TestDelay { time: 987, .. }))
            ));
            let _ = fourth_frame_sender.send(());
            let video: Message = time::timeout(Duration::from_secs(2), read_message(&mut client))
                .await
                .unwrap()
                .unwrap();
            assert!(matches!(video.union, Some(message::Union::VideoFrame(_))));
        }

        if input_allowed && failure_status.is_none() {
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
        if input_allowed && failure_status.is_none() {
            for _ in 0..3 {
                time::timeout(Duration::from_secs(2), hid_receiver.recv())
                    .await
                    .unwrap()
                    .unwrap();
            }
            hid_task.await.unwrap();
        } else {
            if failure_status.is_none() {
                assert!(time::timeout(
                    Duration::from_millis(if initially_disabled { 2200 } else { 200 }),
                    hid_receiver.recv()
                )
                .await
                .is_err());
            }
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

    fn login_error(message: Message) -> Option<String> {
        match message.union {
            Some(message::Union::LoginResponse(LoginResponse {
                union: Some(login_response::Union::Error(error)),
                ..
            })) => Some(error),
            _ => None,
        }
    }

    // A connection to serve() without media; the login is all it gets to.
    async fn login_connection(
        config: &Arc<Config>,
        limits: &ClientLimits,
        rustdesk_identity: &Arc<RustDeskIdentity>,
        shutdown: watch::Receiver<bool>,
    ) -> (TcpStream, Hash, tokio::task::JoinHandle<std::io::Result<()>>) {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let client = tokio::spawn(TcpStream::connect(listener.local_addr().unwrap()));
        let (server_stream, peer) = listener.accept().await.unwrap();
        let mut client = client.await.unwrap().unwrap();
        let pending = limits.try_pending().unwrap();
        let server = tokio::spawn(serve(
            server_stream,
            peer,
            Arc::clone(config),
            Arc::new(Identity {
                extension_id: "rustdesk".to_owned(),
                token: "a".repeat(64),
            }),
            Arc::clone(rustdesk_identity),
            false,
            shutdown,
            limits.clone(),
            pending,
        ));
        let challenge: Message = read_message(&mut client).await.unwrap();
        let Some(message::Union::Hash(hash)) = challenge.union else {
            panic!("server did not send a hash challenge");
        };
        (client, hash, server)
    }

    async fn send_login(client: &mut TcpStream, password: Vec<u8>) {
        let login = LoginRequest {
            password,
            my_id: "test-client".to_owned(),
            my_name: "NanoKVM test".to_owned(),
            version: crate::upstream::version().to_owned(),
            ..Default::default()
        };
        write_message(
            client,
            &Message {
                union: Some(message::Union::LoginRequest(login)),
            },
        )
        .await
        .unwrap();
    }

    #[tokio::test]
    async fn wrong_passwords_wait_while_a_correct_one_is_admitted_at_once() {
        let directory = temporary_directory();
        std::fs::create_dir_all(&directory).unwrap();
        let config = Arc::new(Config {
            password: "rustdesk-test".to_owned(),
            media_socket: directory.join("absent.sock").to_string_lossy().into_owned(),
            ..Config::default()
        });
        let rustdesk_identity =
            Arc::new(RustDeskIdentity::load_from(&directory.join("rustdesk-state")).unwrap());
        let (shutdown_sender, shutdown) = watch::channel(false);
        let limits = ClientLimits::new(1);
        let seed = |limits: &ClientLimits| {
            let mut credentials = crate::auth::Credentials::new(&config);
            for _ in 0..20 {
                credentials.failed();
            }
            *limits.credentials.lock().unwrap() = Some(credentials);
        };

        // After many failures the owner's password still gets in at once:
        // the next step, the video query, is what answers.
        seed(&limits);
        let (mut client, hash, server) =
            login_connection(&config, &limits, &rustdesk_identity, shutdown.clone()).await;
        send_login(&mut client, password_hash("rustdesk-test", &hash)).await;
        let response = time::timeout(Duration::from_secs(5), read_message::<_, Message>(&mut client))
            .await
            .expect("a correct password must not wait")
            .unwrap();
        assert!(login_error(response).unwrap().starts_with("NanoKVM video unavailable"));
        server.await.unwrap().unwrap();

        // A wrong one waits, holding its pending permit, until shutdown.
        seed(&limits);
        let (mut client, _, server) =
            login_connection(&config, &limits, &rustdesk_identity, shutdown.clone()).await;
        send_login(&mut client, vec![0; 32]).await;
        assert!(
            time::timeout(Duration::from_secs(1), read_message::<_, Message>(&mut client))
                .await
                .is_err()
        );
        assert_eq!(limits.pending.available_permits(), 3);
        shutdown_sender.send(true).unwrap();
        time::timeout(Duration::from_secs(5), server)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        assert_eq!(limits.pending.available_permits(), 4);
        std::fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test(start_paused = true)]
    async fn the_failure_delay_ends_at_the_login_deadline() {
        let directory = temporary_directory();
        std::fs::create_dir_all(&directory).unwrap();
        let config = Arc::new(Config {
            password: "rustdesk-test".to_owned(),
            ..Config::default()
        });
        let rustdesk_identity =
            Arc::new(RustDeskIdentity::load_from(&directory.join("rustdesk-state")).unwrap());
        let (_shutdown_sender, shutdown) = watch::channel(false);
        let limits = ClientLimits::new(1);
        let mut credentials = crate::auth::Credentials::new(&config);
        for _ in 0..20 {
            credentials.failed();
        }
        *limits.credentials.lock().unwrap() = Some(credentials);
        let (mut client, server_stream) = tokio::io::duplex(64 << 10);
        let started = time::Instant::now();
        let pending = limits.try_pending().unwrap();
        let server = tokio::spawn(super::serve_io(
            server_stream,
            "127.0.0.1:1".parse().unwrap(),
            Arc::clone(&config),
            Arc::new(Identity {
                extension_id: "rustdesk".to_owned(),
                token: "a".repeat(64),
            }),
            Arc::clone(&rustdesk_identity),
            None,
            shutdown,
            limits.clone(),
            pending,
        ));
        let challenge: Message = read_message(&mut client).await.unwrap();
        assert!(matches!(challenge.union, Some(message::Union::Hash(_))));
        // A wrong password a second before the deadline, with the delay at
        // its cap of a minute.
        time::sleep_until(started + super::LOGIN_TIMEOUT - Duration::from_secs(1)).await;
        let login = LoginRequest {
            password: vec![0; 32],
            my_id: "test-client".to_owned(),
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
        time::timeout_at(started + super::LOGIN_TIMEOUT + Duration::from_millis(100), server)
            .await
            .expect("the wait outlived the login deadline")
            .unwrap()
            .unwrap();
        assert_eq!(limits.pending.available_permits(), 4);
        std::fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test]
    async fn a_connection_gets_only_a_few_empty_password_probes() {
        let directory = temporary_directory();
        std::fs::create_dir_all(&directory).unwrap();
        let config = Arc::new(Config {
            password: "rustdesk-test".to_owned(),
            ..Config::default()
        });
        let rustdesk_identity =
            Arc::new(RustDeskIdentity::load_from(&directory.join("rustdesk-state")).unwrap());
        let (_shutdown_sender, shutdown) = watch::channel(false);
        let limits = ClientLimits::new(1);
        let (mut client, _, server) =
            login_connection(&config, &limits, &rustdesk_identity, shutdown).await;
        for _ in 0..super::MAX_EMPTY_PROBES {
            send_login(&mut client, Vec::new()).await;
            let response: Message = read_message(&mut client).await.unwrap();
            assert_eq!(login_error(response).as_deref(), Some("Empty Password"));
        }
        send_login(&mut client, Vec::new()).await;
        assert!(read_message::<_, Message>(&mut client).await.is_err());
        server.await.unwrap().unwrap();
        assert_eq!(limits.pending.available_permits(), 4);
        std::fs::remove_dir_all(directory).unwrap();
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

    #[tokio::test]
    async fn a_slow_viewer_drops_frames_until_a_keyframe_instead_of_stalling() {
        let directory = temporary_directory();
        std::fs::create_dir_all(&directory).unwrap();
        let path = directory.join("media.sock");
        let listener = UnixListener::bind(&path).unwrap();
        let identity = Identity {
            extension_id: "rustdesk".to_owned(),
            token: "a".repeat(64),
        };
        let profile = crate::nanokvm::VideoProfile { codec: Codec::H264 };
        let (subscriber, accepted) = tokio::join!(
            crate::nanokvm::MediaSubscriber::connect(path.to_str().unwrap(), &identity, "encoded", Some(&profile)),
            listener.accept()
        );
        let (mut bridge, _) = accepted.unwrap();
        read_subscription(&mut bridge).await;
        let (sender, mut receiver) = mpsc::channel(1);
        let forwarder = tokio::spawn(super::forward_media(subscriber.unwrap(), sender));
        let frame = |marker: u8| [0, 0, 0, 1, 0x65, marker];
        async fn keyframe_request(bridge: &mut tokio::net::UnixStream, wait: Duration) -> bool {
            let mut line = Vec::new();
            let read = time::timeout(wait, async {
                loop {
                    let byte = bridge.read_u8().await.unwrap();
                    line.push(byte);
                    if byte == b'\n' {
                        break;
                    }
                }
            })
            .await;
            if read.is_err() {
                return false;
            }
            assert_eq!(line, b"{\"request\":\"keyframe\"}\n");
            true
        }

        // Nobody reads: the first frame waits, the rest are dropped while
        // the bridge keeps being drained, with one keyframe request. A
        // keyframe dropped right after it asks for no other yet.
        write_media_frame(&mut bridge, &frame(1), true).await;
        for marker in 2..40 {
            write_media_frame(&mut bridge, &frame(marker), marker == 20).await;
        }
        assert!(keyframe_request(&mut bridge, Duration::from_secs(5)).await);
        assert!(!keyframe_request(&mut bridge, Duration::from_millis(200)).await);
        // Past the grace of the last waiting keyframe.
        time::sleep(super::MEDIA_GRACE * 2).await;
        let first = receiver.recv().await.unwrap().unwrap();
        assert_eq!(first.payload[5], 1);
        // Caught up: frames resume at the next keyframe.
        write_media_frame(&mut bridge, &frame(40), false).await;
        write_media_frame(&mut bridge, &frame(41), true).await;
        let resumed = receiver.recv().await.unwrap().unwrap();
        assert!(resumed.keyframe);
        assert_eq!(resumed.payload[5], 41);
        write_media_frame(&mut bridge, &frame(42), false).await;
        assert_eq!(receiver.recv().await.unwrap().unwrap().payload[5], 42);
        // A requested keyframe that never comes is asked for again a second
        // later, while deltas keep being dropped.
        while keyframe_request(&mut bridge, Duration::from_millis(50)).await {}
        write_media_frame(&mut bridge, &frame(50), true).await;
        write_media_frame(&mut bridge, &frame(51), false).await;
        assert!(keyframe_request(&mut bridge, Duration::from_secs(5)).await);
        write_media_frame(&mut bridge, &frame(52), false).await;
        assert!(!keyframe_request(&mut bridge, Duration::from_millis(200)).await);
        time::sleep(super::KEYFRAME_REQUEST_INTERVAL).await;
        write_media_frame(&mut bridge, &frame(53), false).await;
        assert!(keyframe_request(&mut bridge, Duration::from_secs(5)).await);
        assert_eq!(receiver.recv().await.unwrap().unwrap().payload[5], 50);
        // A closed bridge still reaches the session as an error.
        drop(bridge);
        assert!(receiver.recv().await.unwrap().is_err());
        forwarder.await.unwrap();
        std::fs::remove_dir_all(directory).unwrap();
    }

    async fn read_subscription(stream: &mut tokio::net::UnixStream) {
        let mut line = String::new();
        BufReader::new(stream).read_line(&mut line).await.unwrap();
        let request: serde_json::Value = serde_json::from_str(&line).unwrap();
        assert_eq!(request["version"], 1);
        if request["video"] == "info" {
            assert!(
                request["codec"].is_null(),
                "metadata must follow the device codec"
            );
        } else {
            assert_eq!(
                request["codec"], "h264",
                "stream must pin the codec returned by metadata"
            );
        }
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
        std::env::temp_dir().join(format!("nanokvm-rustdesk-{}-{unique}", std::process::id()))
    }
}
