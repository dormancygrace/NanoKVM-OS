//! Shared Direct transport: one video subscription and bounded viewer queues.
use crate::{
    hdmi, media_session,
    media_status::{Mode, Statuses},
    sessions::Principal,
    stream_api,
    video_source::{Conflict, EncoderConfig, Source, Subscription},
    Error, Runtime,
};
use axum::{
    body::Bytes,
    extract::ws::{
        rejection::WebSocketUpgradeRejection, CloseFrame, Message, WebSocket, WebSocketUpgrade,
    },
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
};
use futures_util::{stream::SplitSink, SinkExt, StreamExt};
use std::{
    collections::{BTreeMap, VecDeque},
    net::SocketAddr,
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc, Mutex, MutexGuard,
    },
    time::Duration,
};
use tokio::{
    sync::{watch, Notify, OwnedSemaphorePermit},
    time::{timeout, Instant},
};

#[cfg(test)]
#[path = "direct_queue_tests.rs"]
mod queue_tests;
#[cfg(all(test, feature = "native-fixture"))]
#[path = "direct_socket_tests.rs"]
mod socket_tests;

const MAX_FRAMES: usize = 8;
const MAX_BYTES: usize = 2 * 1024 * 1024;
const WRITE: Duration = Duration::from_secs(2);
#[derive(Clone)]
struct Outbound {
    key: bool,
    timestamp: i64,
    payload: Bytes,
}
struct Queue {
    frames: VecDeque<Outbound>,
    bytes: usize,
    in_flight: VecDeque<i64>,
    window: Option<usize>,
    waiting: bool,
    closed: bool,
}
impl Queue {
    fn new(window: Option<usize>) -> Self {
        Self {
            frames: VecDeque::with_capacity(MAX_FRAMES),
            bytes: 0,
            in_flight: VecDeque::with_capacity(8),
            window: window.map(|window| window.clamp(1, 8)),
            waiting: true,
            closed: false,
        }
    }
    fn clear(&mut self) {
        self.frames.clear();
        self.bytes = 0;
    }
    fn can_advance(&self) -> bool {
        !self.closed
            && (self.window.is_none()
                || self.waiting
                || (self.frames.len() < MAX_FRAMES && self.bytes < MAX_BYTES))
    }
    fn offer(&mut self, frame: Outbound) -> bool {
        if self.closed {
            return false;
        }
        if frame.key {
            self.clear();
            if frame.payload.len() > MAX_BYTES {
                self.waiting = true;
                return false;
            }
            self.waiting = false;
        } else {
            if self.waiting {
                return false;
            }
            if self.frames.len() >= MAX_FRAMES || self.bytes + frame.payload.len() > MAX_BYTES {
                self.clear();
                self.waiting = true;
                return false;
            }
        }
        self.bytes += frame.payload.len();
        self.frames.push_back(frame);
        true
    }
    fn pop(&mut self) -> Option<Outbound> {
        if self.closed
            || self
                .window
                .is_some_and(|window| self.in_flight.len() >= window)
        {
            return None;
        }
        let frame = self.frames.pop_front()?;
        self.bytes -= frame.payload.len();
        if self.window.is_some() {
            self.in_flight.push_back(frame.timestamp);
        }
        Some(frame)
    }
    fn acknowledge(&mut self, timestamp: i64) -> bool {
        let mut changed = false;
        while self
            .in_flight
            .front()
            .is_some_and(|pending| *pending <= timestamp)
        {
            self.in_flight.pop_front();
            changed = true;
        }
        changed
    }
    fn resync(&mut self) {
        self.clear();
        self.in_flight.clear();
        self.waiting = true;
    }
    fn discontinuity(&mut self) {
        self.clear();
        self.waiting = true;
    }
    fn close(&mut self) {
        self.closed = true;
        self.clear();
        self.in_flight.clear();
    }
}
struct Entry {
    id: u64,
    queue: Mutex<Queue>,
    wake: Notify,
    reason: Mutex<Option<&'static str>>,
}
impl Entry {
    fn new(id: u64, window: Option<usize>) -> Arc<Self> {
        Arc::new(Self {
            id,
            queue: Mutex::new(Queue::new(window)),
            wake: Notify::new(),
            reason: Mutex::new(None),
        })
    }
    fn lock(&self) -> MutexGuard<'_, Queue> {
        self.queue.lock().unwrap_or_else(|error| error.into_inner())
    }
    fn close(&self, reason: Option<&'static str>) {
        if let Some(reason) = reason {
            *self
                .reason
                .lock()
                .unwrap_or_else(|error| error.into_inner()) = Some(reason);
        }
        self.lock().close();
        self.wake.notify_waiters();
    }
    fn offer(&self, frame: Outbound) {
        if self.lock().offer(frame) {
            self.wake.notify_one();
        }
    }
    async fn next(&self) -> Option<Outbound> {
        loop {
            let wake = self.wake.notified();
            tokio::pin!(wake);
            wake.as_mut().enable();
            {
                let mut queue = self.lock();
                if queue.closed {
                    return None;
                }
                if let Some(frame) = queue.pop() {
                    return Some(frame);
                }
            }
            wake.await;
        }
    }
    fn control(&self, data: &[u8], source: &Source) {
        match data {
            [2, rest @ ..] if rest.len() == 8 => {
                // Go reinterprets uint64 bits as int64. A negative ACK must
                // not accidentally release positive in-flight timestamps.
                let timestamp = i64::from_le_bytes(rest.try_into().expect("ACK length"));
                if self.lock().acknowledge(timestamp) {
                    self.wake.notify_one();
                }
            }
            [3] => {
                self.lock().resync();
                source.request_keyframe();
            }
            _ => {}
        }
    }
}
struct Session {
    config: EncoderConfig,
    subscription: Subscription,
    done: AtomicBool,
    finished: Notify,
}
struct State {
    session: Option<Arc<Session>>,
    entries: BTreeMap<u64, Arc<Entry>>,
    snapshot: Arc<[Arc<Entry>]>,
    sequence: u64,
    version: u64,
}
impl State {
    fn refresh(&mut self) {
        self.snapshot = self.entries.values().cloned().collect();
    }
}
#[derive(Default)]
struct Publication {
    latest: Option<(usize, u64)>,
    running: bool,
}
pub struct Group {
    source: Arc<Source>,
    hdmi: Arc<hdmi::Manager>,
    statuses: Arc<Statuses>,
    state: Mutex<State>,
    admission: tokio::sync::Mutex<()>,
    stopping: AtomicBool,
    active: AtomicUsize,
    idle: Notify,
    publication: Mutex<Publication>,
}
struct Viewer {
    group: Arc<Group>,
    session: Arc<Session>,
    entry: Arc<Entry>,
}
impl Drop for Viewer {
    fn drop(&mut self) {
        self.entry.close(None);
        self.group.remove(&self.session, self.entry.id);
    }
}
impl Group {
    pub fn new(
        source: Arc<Source>,
        hdmi: Arc<hdmi::Manager>,
        statuses: Arc<Statuses>,
    ) -> Arc<Self> {
        Arc::new(Self {
            source,
            hdmi,
            statuses,
            state: Mutex::new(State {
                session: None,
                entries: BTreeMap::new(),
                snapshot: Arc::from([]),
                sequence: 0,
                version: 0,
            }),
            admission: tokio::sync::Mutex::new(()),
            stopping: AtomicBool::new(false),
            active: AtomicUsize::new(0),
            idle: Notify::new(),
            publication: Mutex::new(Publication::default()),
        })
    }
    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|error| error.into_inner())
    }
    fn publish(self: &Arc<Self>, count: usize, version: u64) {
        let Ok(handle) = tokio::runtime::Handle::try_current() else {
            return;
        };
        let mut state = self
            .publication
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        if state.latest.is_none_or(|(_, latest)| version >= latest) {
            state.latest = Some((count, version));
        }
        if state.running {
            return;
        }
        state.running = true;
        self.active.fetch_add(1, Ordering::AcqRel);
        let group = self.clone();
        handle.spawn(async move {
            loop {
                let next = {
                    let mut state = group
                        .publication
                        .lock()
                        .unwrap_or_else(|error| error.into_inner());
                    match state.latest.take() {
                        Some(next) => Some(next),
                        None => {
                            state.running = false;
                            None
                        }
                    }
                };
                let Some((count, version)) = next else {
                    break;
                };
                if !group.stopping.load(Ordering::Acquire) {
                    let hdmi = group.hdmi.clone();
                    if let Ok(Err(error)) = tokio::task::spawn_blocking(move || {
                        hdmi.update_viewers("direct", count as i64, version)
                    })
                    .await
                    {
                        if !group.stopping.load(Ordering::Acquire) {
                            eprintln!("Direct viewer update failed: {error}");
                        }
                    }
                }
            }
            group.active.fetch_sub(1, Ordering::AcqRel);
            group.idle.notify_waiters();
        });
    }
    async fn add(
        self: &Arc<Self>,
        config: EncoderConfig,
        window: Option<usize>,
    ) -> Result<Viewer, Error> {
        let _admission = self.admission.lock().await;
        if self.stopping.load(Ordering::Acquire) {
            return Err("Direct video stopped".into());
        }
        let existing = {
            let state = self.lock();
            if let Some(session) = &state.session {
                if session.config != config {
                    return Err(Box::new(Conflict {
                        active: session.config,
                        requested: config,
                    }));
                }
            }
            state.session.clone()
        };
        let start = existing.is_none();
        let session = if let Some(session) = existing {
            session
        } else {
            Arc::new(Session {
                config,
                subscription: self.source.subscribe(Some(config)).await?,
                done: AtomicBool::new(false),
                finished: Notify::new(),
            })
        };
        let mut state = self.lock();
        if self.stopping.load(Ordering::Acquire) {
            return Err("Direct video stopped".into());
        }
        let id = state
            .sequence
            .checked_add(1)
            .ok_or("Direct viewer sequence exhausted")?;
        let version = state
            .version
            .checked_add(1)
            .ok_or("Direct viewer revision exhausted")?;
        let entry = Entry::new(id, window);
        state.sequence = id;
        state.version = version;
        state.session = Some(session.clone());
        state.entries.insert(id, entry.clone());
        state.refresh();
        let count = state.entries.len();
        drop(state);
        self.source.request_keyframe(); // New decoders also need IDR under smart GOP.
        if start {
            self.active.fetch_add(1, Ordering::AcqRel);
            let group = self.clone();
            let session = session.clone();
            tokio::spawn(async move {
                group.run(session).await;
            });
        }
        self.publish(count, version);
        Ok(Viewer {
            group: self.clone(),
            session,
            entry,
        })
    }
    fn remove(self: &Arc<Self>, session: &Arc<Session>, id: u64) {
        let mut state = self.lock();
        if state
            .session
            .as_ref()
            .is_none_or(|current| !Arc::ptr_eq(current, session))
            || state.entries.remove(&id).is_none()
        {
            return;
        }
        state.refresh();
        state.version = state.version.saturating_add(1);
        let version = state.version;
        let count = state.entries.len();
        if count == 0 {
            state.session = None;
            session.subscription.close();
        }
        drop(state);
        self.publish(count, version);
    }
    fn snapshot(&self, session: &Arc<Session>) -> Arc<[Arc<Entry>]> {
        let state = self.lock();
        if state
            .session
            .as_ref()
            .is_some_and(|current| Arc::ptr_eq(current, session))
        {
            state.snapshot.clone()
        } else {
            Arc::from([])
        }
    }
    async fn run(self: Arc<Self>, session: Arc<Session>) {
        while let Some(frame) = session.subscription.next().await {
            let clients = self.snapshot(&session);
            if clients.is_empty() {
                break;
            }
            if !clients.iter().any(|client| client.lock().can_advance()) {
                for client in clients.iter() {
                    client.lock().discontinuity();
                }
                continue;
            }
            self.statuses.update(Mode::Direct, frame.result);
            let Some(storage) = frame.storage else {
                continue;
            };
            if frame.result < 0 || storage.data().is_empty() {
                continue;
            }
            let outbound = Outbound {
                key: frame.result == 3,
                timestamp: frame.timestamp,
                payload: storage.packet_bytes(),
            };
            for client in clients.iter() {
                client.offer(outbound.clone());
            }
            tokio::task::yield_now().await;
        }
        if !self.source.available() && !self.stopping.load(Ordering::Acquire) {
            self.statuses.update(Mode::Direct, -1);
        }
        let mut state = self.lock();
        if state
            .session
            .as_ref()
            .is_some_and(|current| Arc::ptr_eq(current, &session))
        {
            for entry in state.entries.values() {
                entry.close(Some("encoder-reconfigured"));
            }
            state.entries.clear();
            state.refresh();
            state.session = None;
            state.version = state.version.saturating_add(1);
            let version = state.version;
            drop(state);
            self.publish(0, version);
        }
        session.done.store(true, Ordering::Release);
        session.finished.notify_waiters();
        self.active.fetch_sub(1, Ordering::AcqRel);
        self.idle.notify_waiters();
    }
    pub fn stop(&self) {
        self.stopping.store(true, Ordering::Release);
        let mut state = self.lock();
        for entry in state.entries.values() {
            entry.close(None);
        }
        state.entries.clear();
        state.refresh();
        if let Some(session) = &state.session {
            session.subscription.close();
        }
    }
    pub async fn join(&self) {
        loop {
            let idle = self.idle.notified();
            tokio::pin!(idle);
            idle.as_mut().enable();
            if self.active.load(Ordering::Acquire) == 0 {
                return;
            }
            idle.await;
        }
    }
}
pub(crate) async fn connect(
    runtime: Arc<Runtime>,
    peer: SocketAddr,
    headers: HeaderMap,
    upgrade: Result<WebSocketUpgrade, WebSocketUpgradeRejection>,
    raw_query: &str,
    legacy: bool,
) -> Response {
    let principal = match media_session::authenticate(&runtime, &headers).await {
        Ok(value) => value,
        Err(response) => return *response,
    };
    let config = if legacy {
        EncoderConfig::legacy()
    } else {
        match stream_api::encoder_query(raw_query, EncoderConfig::default()) {
            Ok(config) => config,
            Err(error) => return (StatusCode::BAD_REQUEST, error).into_response(),
        }
    };
    if !crate::ws_origin::allowed(&runtime.config, &headers, peer.ip()) {
        return (
            StatusCode::FORBIDDEN,
            "websocket: request origin not allowed",
        )
            .into_response();
    }
    let upgrade = match upgrade {
        Ok(upgrade) => upgrade,
        Err(error) => return error.into_response(),
    };
    let Ok(slot) = runtime.socket_slots.clone().try_acquire_owned() else {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    };
    let flow = stream_api::flow_window(raw_query);
    upgrade
        .max_message_size(64)
        .max_frame_size(64)
        .write_buffer_size(256 * 1024)
        .max_write_buffer_size(MAX_BYTES + 256 * 1024 + 1024)
        .on_upgrade(move |socket| run_socket(socket, runtime, principal, slot, config, flow))
}
async fn send(
    sink: &mut SplitSink<WebSocket, Message>,
    message: Message,
    revoked: &mut watch::Receiver<bool>,
    principal: &Principal,
) -> bool {
    if media_session::expiry_delay(principal).is_zero() || *revoked.borrow() {
        return false;
    }
    tokio::select! {
        biased;
        _=media_session::cancelled(revoked)=>false,
        _=tokio::time::sleep(media_session::expiry_delay(principal))=>false,
        result=timeout(WRITE,sink.send(message))=>matches!(result,Ok(Ok(()))),
    }
}
async fn run_socket(
    socket: WebSocket,
    runtime: Arc<Runtime>,
    principal: Principal,
    _slot: OwnedSemaphorePermit,
    config: EncoderConfig,
    window: Option<usize>,
) {
    let Ok((_registration, mut revoked)) = media_session::Registration::new(&runtime, &principal)
    else {
        return;
    };
    let (mut sink, mut incoming) = socket.split();
    let who = principal.clone();
    let copy = runtime.clone();
    let valid = tokio::select! {
        biased;
        _ = media_session::cancelled(&mut revoked) => false,
        _ = tokio::time::sleep(media_session::expiry_delay(&principal)) => false,
        result = tokio::task::spawn_blocking(move || who.valid(&copy)) => result.unwrap_or(false),
    };
    let mut session_revoked = !valid;
    let joined = if valid && !media_session::expiry_delay(&principal).is_zero() {
        tokio::select! {biased;
            _=media_session::cancelled(&mut revoked)=>{session_revoked=true;Err("session expired or revoked".into())},
            _=tokio::time::sleep(media_session::expiry_delay(&principal))=>{session_revoked=true;Err("session expired or revoked".into())},
            result=runtime.direct.add(config,window)=>result,
        }
    } else {
        Err("session expired or revoked".into())
    };
    let viewer = match joined {
        Ok(viewer) => viewer,
        Err(error) => {
            let _ = timeout(
                WRITE,
                sink.send(Message::Close(Some(CloseFrame {
                    code: if session_revoked { 4401 } else { 1008 },
                    reason: error.to_string().into(),
                }))),
            )
            .await;
            return;
        }
    };
    let mut reason = None;
    let mut code = 1000;
    let mut heartbeat = Instant::now() + Duration::from_secs(30);
    let mut ping = tokio::time::interval(Duration::from_secs(15));
    ping.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    ping.tick().await;
    let mut check = tokio::time::interval(Duration::from_secs(1));
    check.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {biased;
            _=media_session::cancelled(&mut revoked)=>{session_revoked=true;break;},
            _=tokio::time::sleep(media_session::expiry_delay(&principal))=>{session_revoked=true;break;},
            _=tokio::time::sleep_until(heartbeat)=>break,
            _=ping.tick()=>{if !send(&mut sink,Message::Ping(Bytes::new()),&mut revoked,&principal).await {break;}},
            _=check.tick()=>{
                if media_session::expiry_delay(&principal).is_zero() {session_revoked=true;break;}
                if let Ok(permit)=runtime.jobs.clone().try_acquire_owned() {
                    let who=principal.clone();let copy=runtime.clone();
                    let valid = tokio::select! { biased;
                        _=media_session::cancelled(&mut revoked)=>false,
                        _=tokio::time::sleep(media_session::expiry_delay(&principal))=>false,
                        result=tokio::task::spawn_blocking(move || {let _permit=permit;who.valid(&copy)})=>result.unwrap_or(false),
                    };
                    if !valid {session_revoked=true;break;}
                }
            },
            event=incoming.next()=>{
                match event {
                    Some(Ok(message))=>{
                        heartbeat=Instant::now()+Duration::from_secs(30);
                        match message {
                            Message::Binary(data)=>viewer.entry.control(&data,&runtime.video),
                            Message::Close(_)=>break,
                            Message::Ping(data)=>if !send(&mut sink,Message::Pong(data),&mut revoked,&principal).await {break;},
                            _=>{},
                        }
                    }
                    Some(Err(error))=>{
                        if error.into_inner().downcast_ref::<tokio_tungstenite::tungstenite::Error>().is_some_and(|error|matches!(error,tokio_tungstenite::tungstenite::Error::Capacity(_))) {code=1009;reason=Some("message too big");}
                        break;
                    }
                    None=>break,
                }
            },
            frame=viewer.entry.next()=>{
                let Some(frame)=frame else {reason = *viewer.entry.reason.lock().unwrap_or_else(|error|error.into_inner());if reason.is_some() {code=1008;}break;};
                if !send(&mut sink,Message::Binary(frame.payload),&mut revoked,&principal).await {break;}
            }
        }
    }
    session_revoked |= *revoked.borrow()
        || media_session::expiry_delay(&principal).is_zero()
        || runtime.stopping.load(Ordering::Acquire);
    if session_revoked {
        code = 4401;
        reason = Some("session expired or revoked");
    }
    drop(viewer);
    let _ = timeout(
        WRITE,
        sink.send(Message::Close(Some(CloseFrame {
            code,
            reason: reason.unwrap_or("").into(),
        }))),
    )
    .await;
}
