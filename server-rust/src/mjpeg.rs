//! One native MJPEG reader, immutable frames, and one latest slot per viewer.
use crate::{
    api, hdmi, media_session,
    media_status::{FrameRate, Mode, Statuses},
    native_capture::Request as NativeRequest,
    native_capture_actor::Actor,
    native_frame::{Frame as NativeFrame, FrameCopy},
    screen,
    transport::WriteControl,
    Error, Runtime,
};
use axum::{
    body::{Body, Bytes},
    http::{header, HeaderMap, HeaderValue, Response as HttpResponse, StatusCode},
    response::{IntoResponse, Response},
};
use http_body::{Body as HttpBody, Frame};
use std::{
    collections::{BTreeMap, VecDeque},
    future::Future,
    io,
    pin::Pin,
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc, Mutex, MutexGuard,
    },
    task::{Context, Poll},
    time::{Duration, Instant as NativeInstant, SystemTime},
};
use tokio::{
    sync::{Notify, OwnedSemaphorePermit},
    time::Instant,
};

const PART: &[u8] = b"--frame\r\nContent-Type: image/jpeg\r\n\r\n";
const NEXT: &[u8] = b"\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n";
const WRITE: Duration = Duration::from_secs(5);
const REFRESH: Duration = Duration::from_secs(5);
const CAPTURE: Duration = Duration::from_secs(2);

fn same_image(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    const PREFIX: &[u8] = &[0xff, 0xd8, 0xff, 0xe9, 0, 4];
    if a.len() > 8 && a.starts_with(PREFIX) && b.starts_with(PREFIX) {
        a[8..] == b[8..]
    } else {
        a == b
    }
}
#[derive(Default)]
struct Queue {
    frame: Option<Bytes>,
    closed: bool,
    generation: u64,
    offered: Option<Instant>,
}
struct Entry {
    id: u64,
    queue: Mutex<Queue>,
    wake: Notify,
}
impl Entry {
    fn new(id: u64) -> Arc<Self> {
        Arc::new(Self {
            id,
            queue: Mutex::new(Queue::default()),
            wake: Notify::new(),
        })
    }
    fn lock(&self) -> MutexGuard<'_, Queue> {
        self.queue.lock().unwrap_or_else(|e| e.into_inner())
    }
    fn ready(&self) -> bool {
        let state = self.lock();
        !state.closed && state.frame.is_none()
    }
    fn close(&self) {
        let mut state = self.lock();
        state.closed = true;
        state.frame = None;
        drop(state);
        self.wake.notify_waiters();
    }
    async fn next(&self) -> Option<Bytes> {
        loop {
            let changed = self.wake.notified();
            tokio::pin!(changed);
            changed.as_mut().enable();
            {
                let mut state = self.lock();
                if state.closed {
                    return None;
                }
                if let Some(frame) = state.frame.take() {
                    return Some(frame);
                }
            }
            changed.await;
        }
    }
}
#[derive(Default)]
struct Delivery {
    last: Option<Bytes>,
    generation: u64,
}
impl Delivery {
    fn offer(&mut self, clients: &[Arc<Entry>], data: Bytes, now: Instant) -> bool {
        if !same_image(self.last.as_deref().unwrap_or(&[]), &data) {
            self.last = Some(data.clone());
            self.generation = self.generation.wrapping_add(1);
        }
        let mut sent = false;
        for client in clients {
            let mut queue = client.lock();
            if queue.closed {
                continue;
            }
            if queue.generation != self.generation
                || queue
                    .offered
                    .is_none_or(|last| now.saturating_duration_since(last) >= REFRESH)
            {
                queue.frame = Some(data.clone());
                queue.generation = self.generation;
                queue.offered = Some(now);
                sent = true;
                drop(queue);
                client.wake.notify_one();
            }
        }
        sent
    }
}
struct Session {
    stop: AtomicBool,
    changed: Notify,
    done: AtomicBool,
    finished: Notify,
    period: Duration,
}
impl Session {
    fn stop(&self) {
        self.stop.store(true, Ordering::Release);
        self.changed.notify_waiters();
    }
    async fn finished(&self) {
        loop {
            let done = self.finished.notified();
            tokio::pin!(done);
            done.as_mut().enable();
            if self.done.load(Ordering::Acquire) {
                return;
            }
            done.await;
        }
    }
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
struct Cached {
    frame: Arc<NativeFrame>,
    width: u16,
    height: u16,
    captured_at: SystemTime,
}
#[derive(Default)]
struct Cache {
    refs: usize,
    latest: Option<Cached>,
}
pub struct LatestFrame {
    pub data: FrameCopy,
    pub width: u16,
    pub height: u16,
    pub captured_at: SystemTime,
}
pub struct CacheLease {
    group: Arc<Group>,
}
impl Drop for CacheLease {
    fn drop(&mut self) {
        let mut cache = self.group.cache.lock().unwrap_or_else(|e| e.into_inner());
        cache.refs -= 1;
        if cache.refs == 0 {
            cache.latest = None;
        }
    }
}
#[derive(Default)]
struct Publication {
    latest: Option<(usize, u64)>,
    running: bool,
}
pub struct Group {
    actor: Option<Actor>,
    hdmi: Arc<hdmi::Manager>,
    screen: Arc<screen::Manager>,
    statuses: Arc<Statuses>,
    rate: Arc<FrameRate>,
    state: Mutex<State>,
    cache: Mutex<Cache>,
    publication: Mutex<Publication>,
    admission: tokio::sync::Mutex<()>,
    stopping: AtomicBool,
    active: AtomicUsize,
    idle: Notify,
}
struct OwnedRun(Arc<Group>);
impl Drop for OwnedRun {
    fn drop(&mut self) {
        self.0.active.fetch_sub(1, Ordering::AcqRel);
        self.0.idle.notify_waiters();
    }
}
struct Viewer {
    group: Arc<Group>,
    session: Arc<Session>,
    entry: Arc<Entry>,
}
impl Drop for Viewer {
    fn drop(&mut self) {
        self.entry.close();
        self.group.remove(&self.session, self.entry.id);
    }
}
impl Group {
    pub fn new(
        actor: Option<Actor>,
        hdmi: Arc<hdmi::Manager>,
        screen: Arc<screen::Manager>,
        statuses: Arc<Statuses>,
        rate: Arc<FrameRate>,
    ) -> Arc<Self> {
        Arc::new(Self {
            actor,
            hdmi,
            screen,
            statuses,
            rate,
            state: Mutex::new(State {
                session: None,
                entries: BTreeMap::new(),
                snapshot: Arc::from([]),
                sequence: 0,
                version: 0,
            }),
            cache: Mutex::new(Cache::default()),
            publication: Mutex::new(Publication::default()),
            admission: tokio::sync::Mutex::new(()),
            stopping: AtomicBool::new(false),
            active: AtomicUsize::new(0),
            idle: Notify::new(),
        })
    }
    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }
    fn own(self: &Arc<Self>) -> OwnedRun {
        self.active.fetch_add(1, Ordering::AcqRel);
        OwnedRun(self.clone())
    }
    pub fn enable_cache(self: &Arc<Self>) -> Result<CacheLease, Error> {
        let mut cache = self.cache.lock().unwrap_or_else(|e| e.into_inner());
        if self.stopping.load(Ordering::Acquire) {
            return Err("MJPEG stopped".into());
        }
        cache.refs = cache
            .refs
            .checked_add(1)
            .ok_or("MJPEG cache references exhausted")?;
        Ok(CacheLease {
            group: self.clone(),
        })
    }
    pub fn latest_frame(&self) -> Result<Option<LatestFrame>, Error> {
        let cache = self.cache.lock().unwrap_or_else(|e| e.into_inner());
        if cache.refs == 0 {
            return Ok(None);
        }
        let Some(latest) = &cache.latest else {
            return Ok(None);
        };
        Ok(Some(LatestFrame {
            data: latest.frame.copy_data()?,
            width: latest.width,
            height: latest.height,
            captured_at: latest.captured_at,
        }))
    }
    fn cache_enabled(&self) -> bool {
        self.cache.lock().unwrap_or_else(|e| e.into_inner()).refs > 0
    }
    fn cache_frame(&self, frame: Arc<NativeFrame>, screen: screen::Screen) {
        let mut cache = self.cache.lock().unwrap_or_else(|e| e.into_inner());
        if cache.refs > 0 && !self.stopping.load(Ordering::Acquire) {
            cache.latest = Some(Cached {
                frame,
                width: screen.width,
                height: screen.height,
                captured_at: SystemTime::now(),
            });
        }
    }
    fn publish(self: &Arc<Self>, count: usize, version: u64) {
        let Ok(handle) = tokio::runtime::Handle::try_current() else {
            return;
        };
        let mut state = self.publication.lock().unwrap_or_else(|e| e.into_inner());
        if state.latest.is_none_or(|(_, latest)| version >= latest) {
            state.latest = Some((count, version));
        }
        if state.running {
            return;
        }
        state.running = true;
        let owned = self.own();
        let group = self.clone();
        handle.spawn(async move {
            let _owned = owned;
            loop {
                let next = {
                    let mut state = group.publication.lock().unwrap_or_else(|e| e.into_inner());
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
                        hdmi.update_viewers("mjpeg", count as i64, version)
                    })
                    .await
                    {
                        if !group.stopping.load(Ordering::Acquire) {
                            eprintln!("MJPEG viewer update failed: {error}");
                        }
                    }
                }
            }
        });
    }
    async fn add(self: &Arc<Self>) -> Result<Viewer, Error> {
        let _admission = self.admission.lock().await;
        if self.actor.as_ref().is_none_or(Actor::stopped) {
            return Err("native MJPEG backend is unavailable".into());
        }
        loop {
            if self.stopping.load(Ordering::Acquire) {
                return Err("MJPEG stopped".into());
            }
            let retiring = self
                .lock()
                .session
                .as_ref()
                .filter(|session| session.stop.load(Ordering::Acquire))
                .cloned();
            if let Some(session) = retiring {
                session.finished().await;
            } else {
                break;
            }
        }
        let start = self.lock().session.is_none();
        let new_session = if start {
            let screen = self.screen.clone();
            let screen = tokio::task::spawn_blocking(move || {
                screen.check()?;
                screen.capture_screen()
            })
            .await??;
            Some(Arc::new(Session {
                stop: AtomicBool::new(false),
                changed: Notify::new(),
                done: AtomicBool::new(false),
                finished: Notify::new(),
                period: period(screen.fps),
            }))
        } else {
            None
        };
        let mut state = self.lock();
        if self.stopping.load(Ordering::Acquire) {
            return Err("MJPEG stopped".into());
        }
        let session = new_session
            .or_else(|| state.session.clone())
            .ok_or("MJPEG session unavailable")?;
        let id = state
            .sequence
            .checked_add(1)
            .ok_or("MJPEG viewer sequence exhausted")?;
        let version = state
            .version
            .checked_add(1)
            .ok_or("MJPEG viewer revision exhausted")?;
        let entry = Entry::new(id);
        state.sequence = id;
        state.version = version;
        state.session = Some(session.clone());
        state.entries.insert(id, entry.clone());
        state.refresh();
        let count = state.entries.len();
        drop(state);
        if start {
            let owned = self.own();
            let group = self.clone();
            let session = session.clone();
            tokio::spawn(async move {
                let _owned = owned;
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
        let count = state.entries.len();
        let version = state.version;
        if count == 0 {
            session.stop();
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
        let origin = NativeInstant::now();
        let mut current_period = session.period;
        let mut tick = tokio::time::interval_at(Instant::now() + current_period, current_period);
        tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut delivery = Delivery::default();
        loop {
            let changed = session.changed.notified();
            tokio::pin!(changed);
            changed.as_mut().enable();
            if session.stop.load(Ordering::Acquire) {
                break;
            }
            tokio::select! { biased; _ = changed => continue, _ = tick.tick() => {} }
            let clients = self.snapshot(&session);
            if clients.is_empty() {
                break;
            }
            let capture = self.cache_enabled() || clients.iter().any(|client| client.ready());
            let group = self.clone();
            let current = session.clone();
            let captured = tokio::task::spawn_blocking(move || {
                let screen = group.screen.capture_screen()?;
                if !capture {
                    return Ok((None, screen, false));
                }
                let lease = group
                    .hdmi
                    .acquire_read(&|| current.stop.load(Ordering::Acquire))?;
                if current.stop.load(Ordering::Acquire) {
                    return Err("MJPEG retired before capture".into());
                }
                let actor = group
                    .actor
                    .as_ref()
                    .ok_or("native MJPEG backend is unavailable")?;
                // Dispatched reads finish bounded before the last viewer retires.
                let outcome = actor.call_blocking(
                    NativeRequest::Mjpeg {
                        width: screen.width,
                        height: screen.height,
                        quality: screen.quality,
                    },
                    origin,
                    CAPTURE,
                    &|| false,
                )?;
                let fresh = if outcome.frame.is_some() {
                    lease.claim_fresh()?
                } else {
                    false
                };
                Ok::<_, Error>((Some(outcome), screen, fresh))
            })
            .await;
            if session.stop.load(Ordering::Acquire) {
                break;
            }
            let (outcome, screen, fresh) = match captured {
                Ok(Ok(captured)) => captured,
                Ok(Err(error)) => {
                    eprintln!("native MJPEG capture failed: {error}");
                    self.statuses.update(Mode::Mjpeg, -1);
                    delivery.last = None;
                    if self.actor.as_ref().is_none_or(Actor::stopped) {
                        break;
                    }
                    continue;
                }
                Err(error) => {
                    eprintln!("MJPEG capture task failed: {error}");
                    break;
                }
            };
            let next_period = period(screen.fps);
            if next_period != current_period {
                current_period = next_period;
                tick = tokio::time::interval_at(Instant::now() + current_period, current_period);
                tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            }
            let Some(outcome) = outcome else {
                continue;
            };
            self.statuses.update(Mode::Mjpeg, outcome.status);
            if outcome.status < 0 || outcome.status == 5 {
                delivery.last = None;
                continue;
            }
            let Some(frame) = outcome.frame else {
                delivery.last = None;
                continue;
            };
            if frame.data().is_empty() {
                delivery.last = None;
                continue;
            }
            if fresh {
                delivery.last = None;
                continue;
            }
            self.cache_frame(frame.clone(), screen);
            if delivery.offer(&clients, frame.data_bytes(), Instant::now()) {
                self.rate.update();
            }
            tokio::task::yield_now().await;
        }
        let mut state = self.lock();
        if state
            .session
            .as_ref()
            .is_some_and(|current| Arc::ptr_eq(current, &session))
        {
            for entry in state.entries.values() {
                entry.close();
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
    }
    pub fn stop(&self) {
        self.stopping.store(true, Ordering::Release);
        let mut state = self.lock();
        for entry in state.entries.values() {
            entry.close();
        }
        state.entries.clear();
        state.refresh();
        if let Some(session) = &state.session {
            session.stop();
        }
        self.cache.lock().unwrap_or_else(|e| e.into_inner()).latest = None;
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
fn period(fps: i64) -> Duration {
    Duration::from_secs(1)
        / u32::try_from(fps.max(1))
            .unwrap_or(u32::MAX)
            .min(1_000_000_000)
}

struct BodyGuard {
    viewer: Viewer,
    _registration: media_session::Registration,
    _slot: OwnedSemaphorePermit,
    control: WriteControl,
    watchdog: tokio::task::JoinHandle<()>,
}
impl Drop for BodyGuard {
    fn drop(&mut self) {
        self.watchdog.abort();
        self.control.abort();
    }
}
type NextFrame = Pin<Box<dyn Future<Output = Option<Bytes>> + Send>>;
type Flush = Pin<Box<dyn Future<Output = io::Result<()>> + Send>>;
fn multipart(first: bool, data: Bytes) -> VecDeque<Bytes> {
    let mut chunks = VecDeque::with_capacity(3);
    if first {
        chunks.push_back(Bytes::from_static(PART));
    }
    chunks.push_back(data);
    chunks.push_back(Bytes::from_static(NEXT));
    chunks
}
struct StreamBody {
    ended: bool,
    guard: BodyGuard,
    first: bool,
    next: Option<NextFrame>,
    chunks: VecDeque<Bytes>,
    epoch: Option<u64>,
    flush: Option<Flush>,
}
impl HttpBody for StreamBody {
    type Data = Bytes;
    type Error = io::Error;
    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, io::Error>>> {
        if self.ended {
            return Poll::Ready(None);
        }
        loop {
            if let Some(chunk) = self.chunks.pop_front() {
                return Poll::Ready(Some(Ok(Frame::data(chunk))));
            }
            if let Some(epoch) = self.epoch.take() {
                self.guard.control.finish(epoch);
                let control = self.guard.control.clone();
                self.flush = Some(Box::pin(async move { control.flushed(epoch).await }));
            }
            if let Some(flush) = &mut self.flush {
                match flush.as_mut().poll(cx) {
                    Poll::Pending => return Poll::Pending,
                    Poll::Ready(Err(error)) => {
                        self.ended = true;
                        return Poll::Ready(Some(Err(error)));
                    }
                    Poll::Ready(Ok(())) => self.flush = None,
                }
            }
            if self.next.is_none() {
                let entry = self.guard.viewer.entry.clone();
                self.next = Some(Box::pin(async move { entry.next().await }));
            }
            let data = match self.next.as_mut().unwrap().as_mut().poll(cx) {
                Poll::Pending => return Poll::Pending,
                Poll::Ready(None) => {
                    self.ended = true;
                    return Poll::Ready(None);
                }
                Poll::Ready(Some(data)) => data,
            };
            self.next = None;
            let epoch = match self.guard.control.begin(WRITE) {
                Ok(epoch) => epoch,
                Err(error) => {
                    self.ended = true;
                    return Poll::Ready(Some(Err(error)));
                }
            };
            self.epoch = Some(epoch);
            self.chunks = multipart(self.first, data);
            self.first = false;
        }
    }
}
pub(crate) async fn connect(
    runtime: Arc<Runtime>,
    headers: HeaderMap,
    control: Option<WriteControl>,
) -> Response {
    let principal = match media_session::authenticate(&runtime, &headers).await {
        Ok(principal) => principal,
        Err(response) => return *response,
    };
    let Some(control) = control else {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            "MJPEG write transport unavailable",
        )
            .into_response();
    };
    let Ok(slot) = runtime.socket_slots.clone().try_acquire_owned() else {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    };
    let Ok((registration, mut revoked)) = media_session::Registration::new(&runtime, &principal)
    else {
        return api::unauthorized();
    };
    let copy = runtime.clone();
    let who = principal.clone();
    let valid = tokio::select! { biased;
        _ = media_session::cancelled(&mut revoked) => false,
        _ = media_session::expired(&principal) => false,
        result = tokio::task::spawn_blocking(move || who.valid(&copy)) => result.unwrap_or(false),
    };
    if !valid {
        return api::unauthorized();
    }
    let viewer = tokio::select! { biased;
        _ = media_session::cancelled(&mut revoked) => return api::unauthorized(),
        _ = media_session::expired(&principal) => return api::unauthorized(),
        result = runtime.mjpeg.add() => match result { Ok(viewer) => viewer, Err(error) => return (StatusCode::SERVICE_UNAVAILABLE, error.to_string()).into_response() },
    };
    let entry = viewer.entry.clone();
    let cancel = control.clone();
    let group = viewer.group.clone();
    let owned = group.own();
    let watchdog = tokio::spawn(async move {
        let _owned = owned;
        let mut check = tokio::time::interval(Duration::from_secs(1));
        check.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        loop {
            tokio::select! { biased;
                _ = media_session::cancelled(&mut revoked) => break,
                _ = media_session::expired(&principal) => break,
                _ = cancel.cancelled() => break,
                _ = runtime.transport_shutdown.stopped() => break,
                _ = check.tick() => {
                    if let Ok(permit) = runtime.jobs.clone().try_acquire_owned() {
                        let who = principal.clone(); let copy = runtime.clone();
                        let valid = tokio::select! { biased;
                            _ = media_session::cancelled(&mut revoked) => false,
                            _ = media_session::expired(&principal) => false,
                            _ = cancel.cancelled() => false,
                            result = tokio::task::spawn_blocking(move || { let _permit = permit; who.valid(&copy) }) => result.unwrap_or(false),
                        };
                        if !valid { break; }
                    }
                }
            }
        }
        entry.close();
        cancel.abort();
    });
    let date = server_date();
    HttpResponse::builder()
        .header(
            header::CONTENT_TYPE,
            "multipart/x-mixed-replace; boundary=frame",
        )
        .header(header::CACHE_CONTROL, "no-cache")
        .header(header::CONNECTION, "keep-alive")
        .header(header::PRAGMA, "no-cache")
        .header("x-server-date", date)
        .body(Body::new(StreamBody {
            ended: false,
            guard: BodyGuard {
                viewer,
                _registration: registration,
                _slot: slot,
                control,
                watchdog,
            },
            first: true,
            next: None,
            chunks: VecDeque::with_capacity(3),
            epoch: None,
            flush: None,
        }))
        .unwrap()
}
fn server_date() -> HeaderValue {
    let now = SystemTime::now();
    let seconds = now
        .duration_since(SystemTime::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as libc::time_t;
    let mut local = std::mem::MaybeUninit::<libc::tm>::uninit();
    if unsafe { libc::localtime_r(&seconds, local.as_mut_ptr()) }.is_null() {
        return HeaderValue::from_str(&httpdate::fmt_http_date(now).replace("GMT", "UTC")).unwrap();
    }
    let local = unsafe { local.assume_init() };
    let zone = if local.tm_zone.is_null() {
        "UTC".into()
    } else {
        unsafe { std::ffi::CStr::from_ptr(local.tm_zone) }.to_string_lossy()
    };
    let days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
    let months = [
        "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
    ];
    HeaderValue::from_str(&format!(
        "{}, {:02} {} {:04} {:02}:{:02}:{:02} {}",
        days[local.tm_wday as usize],
        local.tm_mday,
        months[local.tm_mon as usize],
        local.tm_year + 1900,
        local.tm_hour,
        local.tm_min,
        local.tm_sec,
        zone
    ))
    .unwrap_or_else(|_| HeaderValue::from_static(""))
}

#[cfg(test)]
#[path = "mjpeg_tests.rs"]
mod tests;

#[cfg(all(test, feature = "native-fixture"))]
#[path = "mjpeg_native_tests.rs"]
mod native_tests;

#[cfg(all(test, feature = "native-fixture"))]
#[path = "mjpeg_socket_tests.rs"]
mod socket_tests;
