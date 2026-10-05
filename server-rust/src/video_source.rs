//! One device-wide native encoder, bounded decoder queues and draining sessions.
//! Transports share immutable sealed frames; closing a viewer clears its owners.
use crate::{
    fsroot, hdmi,
    native_capture::{self, Outcome, Request, VideoConfig},
    native_capture_actor::Actor,
    native_frame::Frame,
    screen::{self, Screen},
    Error,
};
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeMap, VecDeque},
    fmt, fs,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex, MutexGuard,
    },
    time::{Duration, Instant},
};
use tokio::sync::Notify;

const QUEUE_CAPACITY: usize = 24;
const MAX_SUBSCRIBERS: usize = 64;
const CAPTURE_TIMEOUT: Duration = Duration::from_secs(2);
const KEYFRAME_INTERVAL: Duration = Duration::from_millis(500);

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Codec {
    H264,
    H265,
}
impl fmt::Display for Codec {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::H264 => "h264",
            Self::H265 => "h265",
        })
    }
}
impl Codec {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "h264" => Some(Self::H264),
            "h265" => Some(Self::H265),
            _ => None,
        }
    }
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct EncoderConfig {
    pub codec: Codec,
}
impl Default for EncoderConfig {
    fn default() -> Self {
        Self { codec: Codec::H265 }
    }
}
impl EncoderConfig {
    pub const fn legacy() -> Self {
        Self { codec: Codec::H264 }
    }
}
#[derive(Debug)]
pub struct Conflict {
    pub active: EncoderConfig,
    pub requested: EncoderConfig,
}
impl fmt::Display for Conflict {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "video encoder is already in use with {{Codec:{}}}; requested {{Codec:{}}}",
            self.active.codec, self.requested.codec
        )
    }
}
impl std::error::Error for Conflict {}

#[derive(Clone)]
pub struct VideoFrame {
    pub storage: Option<Arc<Frame>>,
    pub result: i32,
    pub duration: Duration,
    pub timestamp: i64,
}
impl VideoFrame {
    pub fn is_keyframe(&self) -> bool {
        self.result == 3
    }
    pub fn data(&self) -> &[u8] {
        self.storage.as_ref().map_or(&[], |frame| frame.data())
    }
    fn error(result: i32) -> Self {
        Self {
            storage: None,
            result,
            duration: Duration::ZERO,
            timestamp: 0,
        }
    }
}
struct Queue {
    frames: VecDeque<VideoFrame>,
    waiting: bool,
    closed: bool,
}
struct Entry {
    id: u64,
    queue: Mutex<Queue>,
    ready: Notify,
    keyframe: Arc<AtomicBool>,
}
impl Entry {
    fn new(id: u64, keyframe: Arc<AtomicBool>) -> Arc<Self> {
        Arc::new(Self {
            id,
            queue: Mutex::new(Queue {
                frames: VecDeque::with_capacity(QUEUE_CAPACITY),
                waiting: true,
                closed: false,
            }),
            ready: Notify::new(),
            keyframe,
        })
    }
    fn lock(&self) -> MutexGuard<'_, Queue> {
        self.queue.lock().unwrap_or_else(|error| error.into_inner())
    }
    fn close(&self) {
        let mut queue = self.lock();
        queue.closed = true;
        queue.frames.clear();
        drop(queue);
        self.ready.notify_waiters();
    }
    fn offer(&self, frame: VideoFrame) -> bool {
        let mut queue = self.lock();
        if queue.closed {
            return false;
        }
        if queue.waiting && frame.result >= 0 {
            if !frame.is_keyframe() {
                return false;
            }
            queue.waiting = false;
        }
        if queue.frames.len() == QUEUE_CAPACITY {
            queue.frames.clear();
            queue.waiting = true;
            self.keyframe.store(true, Ordering::Release);
            if !frame.is_keyframe() {
                return false;
            }
            queue.waiting = false;
        }
        queue.frames.push_back(frame);
        drop(queue);
        self.ready.notify_one();
        true
    }
    async fn next(&self) -> Option<VideoFrame> {
        loop {
            let ready = self.ready.notified();
            tokio::pin!(ready);
            ready.as_mut().enable();
            {
                let mut queue = self.lock();
                if queue.closed {
                    return None;
                }
                if let Some(frame) = queue.frames.pop_front() {
                    return Some(frame);
                }
            }
            ready.await;
        }
    }
}
struct Session {
    config: EncoderConfig,
    period: Duration,
    stop: AtomicBool,
    stopped: Notify,
    done: AtomicBool,
    finished: Notify,
}
impl Session {
    fn stop(&self) {
        self.stop.store(true, Ordering::Release);
        self.stopped.notify_waiters();
    }
    async fn wait(&self) {
        loop {
            let finished = self.finished.notified();
            tokio::pin!(finished);
            finished.as_mut().enable();
            if self.done.load(Ordering::Acquire) {
                return;
            }
            finished.await;
        }
    }
    async fn delay(&self, deadline: Instant) -> bool {
        let stopped = self.stopped.notified();
        tokio::pin!(stopped);
        stopped.as_mut().enable();
        if self.stop.load(Ordering::Acquire) {
            return false;
        }
        tokio::select! {
            biased;
            _ = stopped => false,
            _ = tokio::time::sleep_until(deadline.into()) => !self.stop.load(Ordering::Acquire),
        }
    }
}
struct State {
    selected: Option<EncoderConfig>,
    session: Option<Arc<Session>>,
    entries: BTreeMap<u64, Arc<Entry>>,
    snapshot: Arc<[Arc<Entry>]>,
    sequence: u64,
}
impl State {
    fn refresh(&mut self) {
        self.snapshot = self.entries.values().cloned().collect();
    }
}
pub struct Source {
    root: PathBuf,
    screen: Arc<screen::Manager>,
    hdmi: Arc<hdmi::Manager>,
    actor: Option<Actor>,
    frame_rate: Arc<crate::media_status::FrameRate>,
    state: Mutex<State>,
    keyframe: Arc<AtomicBool>,
    stopping: AtomicBool,
}
pub struct Subscription {
    source: Arc<Source>,
    session: Arc<Session>,
    entry: Arc<Entry>,
}
impl Subscription {
    pub fn config(&self) -> EncoderConfig {
        self.session.config
    }
    pub async fn next(&self) -> Option<VideoFrame> {
        self.entry.next().await
    }
    pub fn close(&self) {
        self.entry.close();
        self.source.remove(&self.session, self.entry.id);
    }
    pub fn closed(&self) -> bool {
        self.entry.lock().closed
    }
}
impl Drop for Subscription {
    fn drop(&mut self) {
        self.close();
    }
}
enum Admission {
    Joined(Subscription),
    Draining(Arc<Session>),
}
impl Source {
    pub fn new(
        root: PathBuf,
        screen: Arc<screen::Manager>,
        hdmi: Arc<hdmi::Manager>,
        actor: Option<Actor>,
    ) -> Arc<Self> {
        let frame_rate = crate::media_status::FrameRate::new(root.clone());
        Self::with_counter(root, screen, hdmi, actor, frame_rate)
    }
    pub(crate) fn with_counter(
        root: PathBuf,
        screen: Arc<screen::Manager>,
        hdmi: Arc<hdmi::Manager>,
        actor: Option<Actor>,
        frame_rate: Arc<crate::media_status::FrameRate>,
    ) -> Arc<Self> {
        let selected = fsroot::resolve(&root, Path::new("/etc/kvm/encoder_codec"), false)
            .ok()
            .and_then(|path| fs::read_to_string(path).ok())
            .and_then(|value| Codec::parse(value.trim()))
            .map(|codec| EncoderConfig { codec });
        Arc::new(Self {
            root,
            screen,
            hdmi,
            actor,
            frame_rate,
            state: Mutex::new(State {
                selected,
                session: None,
                entries: BTreeMap::new(),
                snapshot: Arc::from([]),
                sequence: 0,
            }),
            keyframe: Arc::new(AtomicBool::new(false)),
            stopping: AtomicBool::new(false),
        })
    }
    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|error| error.into_inner())
    }
    pub(crate) fn available(&self) -> bool {
        !self.stopping.load(Ordering::Acquire)
            && self.actor.as_ref().is_some_and(|actor| !actor.stopped())
    }
    pub(crate) fn encoder_state(&self) -> (bool, bool, String) {
        let state = self.lock();
        let active = !state.entries.is_empty() && state.session.is_some();
        let config = state.selected.or_else(|| {
            state
                .session
                .as_ref()
                .filter(|_| active)
                .map(|session| session.config)
        });
        (
            active,
            state.selected.is_some(),
            config.map_or_else(String::new, |config| config.codec.to_string()),
        )
    }
    pub fn active_config(&self) -> Option<EncoderConfig> {
        let state = self.lock();
        state
            .session
            .as_ref()
            .filter(|_| !state.entries.is_empty())
            .map(|session| session.config)
    }
    pub fn selected_config(&self) -> Option<EncoderConfig> {
        self.lock().selected
    }
    /// Persistence belongs to the state API's serialized save-before-publish.
    pub fn select(&self, config: EncoderConfig) {
        let mut state = self.lock();
        state.selected = Some(config);
        if state
            .session
            .as_ref()
            .is_some_and(|session| session.config != config)
        {
            for entry in state.entries.values() {
                entry.close();
            }
            state.entries.clear();
            state.refresh();
            if let Some(session) = &state.session {
                session.stop();
            }
        }
    }
    pub fn request_keyframe(&self) {
        self.keyframe.store(true, Ordering::Release);
    }
    fn take_keyframe(&self, now: Instant, last: &mut Option<Instant>) -> bool {
        if last.is_some_and(|last| now.saturating_duration_since(last) < KEYFRAME_INTERVAL)
            || !self.keyframe.swap(false, Ordering::AcqRel)
        {
            return false;
        }
        *last = Some(now);
        true
    }
    pub async fn subscribe(
        self: &Arc<Self>,
        requested: Option<EncoderConfig>,
    ) -> Result<Subscription, Error> {
        let handle = tokio::runtime::Handle::current();
        loop {
            let source = self.clone();
            let handle = handle.clone();
            // Short root/status reads and admission never block the event loop.
            // A detached cancelled join returns an RAII subscription which drops
            // and retires itself; the new capture task is started at admission.
            let result =
                tokio::task::spawn_blocking(move || source.admit(requested, handle)).await??;
            match result {
                Admission::Joined(subscription) => return Ok(subscription),
                Admission::Draining(session) => session.wait().await,
            }
        }
    }
    fn admit(
        self: &Arc<Self>,
        requested: Option<EncoderConfig>,
        handle: tokio::runtime::Handle,
    ) -> Result<Admission, Error> {
        if self.stopping.load(Ordering::Acquire) {
            return Err("video source stopped".into());
        }
        let actor = self
            .actor
            .as_ref()
            .ok_or("native video backend is not linked")?;
        if actor.stopped() {
            return Err("native capture owner stopped".into());
        }
        let mut state = self.lock();
        if self.stopping.load(Ordering::Acquire) {
            return Err("video source stopped".into());
        }
        let config = requested.unwrap_or_else(|| {
            state
                .selected
                .or_else(|| {
                    state
                        .session
                        .as_ref()
                        .filter(|_| !state.entries.is_empty())
                        .map(|session| session.config)
                })
                .unwrap_or_default()
        });
        if let Some(selected) = state.selected.filter(|selected| *selected != config) {
            return Err(Box::new(Conflict {
                active: selected,
                requested: config,
            }));
        }
        if let Some(session) = &state.session {
            if state.entries.is_empty() {
                return Ok(Admission::Draining(session.clone()));
            }
            if session.config != config {
                return Err(Box::new(Conflict {
                    active: session.config,
                    requested: config,
                }));
            }
        }
        if state.entries.len() >= MAX_SUBSCRIBERS {
            return Err("video subscriber limit reached".into());
        }
        let screen = self.screen.snapshot()?;
        if portrait_blocked(
            config.codec,
            screen.height,
            screen::read_video_value(&self.root, "/run/nanokvm/width"),
            screen::read_video_value(&self.root, "/run/nanokvm/height"),
        ) {
            return Err(
                "maximum portrait output requires H.265 Direct or a smaller stream resolution"
                    .into(),
            );
        }
        let id = state
            .sequence
            .checked_add(1)
            .ok_or("video subscription sequence exhausted")?;
        let start = state.session.is_none();
        if start {
            self.screen.check()?;
            let period = Duration::from_secs(1) / normalized_fps(self.screen.capture_screen()?.fps);
            state.session = Some(Arc::new(Session {
                config,
                period,
                stop: AtomicBool::new(false),
                stopped: Notify::new(),
                done: AtomicBool::new(false),
                finished: Notify::new(),
            }));
        }
        let session = state.session.as_ref().expect("session admitted").clone();
        let entry = Entry::new(id, self.keyframe.clone());
        state.sequence = id;
        state.entries.insert(id, entry.clone());
        state.refresh();
        self.request_keyframe();
        drop(state);
        if start {
            let source = self.clone();
            let session = session.clone();
            handle.spawn(async move {
                source.run(session).await;
            });
        }
        Ok(Admission::Joined(Subscription {
            source: self.clone(),
            session,
            entry,
        }))
    }
    fn remove(&self, session: &Arc<Session>, id: u64) {
        let mut state = self.lock();
        if state
            .session
            .as_ref()
            .is_none_or(|current| !Arc::ptr_eq(current, session))
        {
            return;
        }
        if state.entries.remove(&id).is_some() {
            state.refresh();
            if state.entries.is_empty() {
                session.stop();
            }
        }
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
    fn finish(&self, session: &Arc<Session>) {
        let mut state = self.lock();
        if state
            .session
            .as_ref()
            .is_some_and(|current| Arc::ptr_eq(current, session))
        {
            for entry in state.entries.values() {
                entry.close();
            }
            state.entries.clear();
            state.refresh();
            state.session = None;
        }
        session.done.store(true, Ordering::Release);
        session.finished.notify_waiters();
    }
    async fn run(self: Arc<Self>, session: Arc<Session>) {
        let origin = Instant::now();
        let mut period = session.period;
        let mut next = origin + period;
        let last_keyframe = Arc::new(Mutex::new(None));
        while session.delay(next).await {
            next = advance_deadline(next, Instant::now(), period);
            let source = self.clone();
            let current = session.clone();
            let last_keyframe = last_keyframe.clone();
            let captured = tokio::task::spawn_blocking(move || {
                let screen = source.screen.capture_screen()?;
                let period = Duration::from_secs(1) / normalized_fps(screen.fps);
                let lease = source
                    .hdmi
                    .acquire_read(&|| current.stop.load(Ordering::Acquire))?;
                if current.stop.load(Ordering::Acquire) {
                    return Err("video subscription retired before capture".into());
                }
                let actor = source
                    .actor
                    .as_ref()
                    .ok_or("native video backend is not linked")?;
                let now = Instant::now();
                if source.take_keyframe(
                    now,
                    &mut *last_keyframe
                        .lock()
                        .map_err(|_| "keyframe timing unavailable")?,
                ) {
                    let requested = actor
                        .call_blocking(Request::Keyframe, origin, CAPTURE_TIMEOUT, &|| false)
                        .and_then(|outcome| {
                            if outcome.status == 0 {
                                Ok(())
                            } else {
                                Err("native keyframe request failed".into())
                            }
                        });
                    if let Err(error) = requested {
                        // Admission can fail under shared control/maintenance
                        // pressure. Preserve decoder refresh intent for retry.
                        source.request_keyframe();
                        return Err(error);
                    }
                }
                // A last-viewer close must not cancel an in-flight native read:
                // wait for its bounded return before a replacement can start.
                let outcome = actor.call_blocking(
                    Request::Video(native_config(current.config, screen)),
                    origin,
                    CAPTURE_TIMEOUT,
                    &|| false,
                )?;
                let discard = if outcome.frame.is_some() {
                    lease.claim_fresh()?
                } else {
                    false
                };
                Ok::<_, Error>((outcome, period, discard))
            })
            .await;
            if session.stop.load(Ordering::Acquire) {
                break;
            }
            let (outcome, current_period, discard) = match captured {
                Ok(Ok(captured)) => captured,
                Ok(Err(error)) => {
                    eprintln!("native video capture failed: {error}");
                    (
                        Outcome {
                            status: -1,
                            frame: None,
                        },
                        period,
                        false,
                    )
                }
                Err(error) => {
                    eprintln!("video capture task failed: {error}");
                    break;
                }
            };
            if self.actor.as_ref().is_none_or(Actor::stopped) {
                break;
            }
            if current_period != period {
                period = current_period;
                next = Instant::now() + period;
            }
            if outcome.frame.is_some() {
                self.frame_rate.update();
            }
            if discard {
                self.request_keyframe();
            } else if let Some(frame) = outcome.frame {
                let timestamp = i64::from_le_bytes(
                    frame.as_ref().as_ref()[1..9]
                        .try_into()
                        .expect("native Direct prefix"),
                );
                let frame = VideoFrame {
                    storage: Some(frame),
                    result: outcome.status,
                    duration: period,
                    timestamp,
                };
                for entry in self.snapshot(&session).iter() {
                    entry.offer(frame.clone());
                }
            } else if outcome.status < 0 {
                for entry in self.snapshot(&session).iter() {
                    entry.offer(VideoFrame::error(outcome.status));
                }
            }
            tokio::task::yield_now().await;
        }
        self.finish(&session);
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
    }
    pub async fn join(&self) {
        let session = self.lock().session.clone();
        if let Some(session) = session {
            session.wait().await;
        }
    }
}
fn normalized_fps(fps: i64) -> u32 {
    if fps < 1 {
        30
    } else {
        fps.min(120) as u32
    }
}
fn native_config(config: EncoderConfig, screen: Screen) -> VideoConfig {
    VideoConfig {
        width: screen.width,
        height: screen.height,
        codec: match config.codec {
            Codec::H264 => native_capture::Codec::H264,
            Codec::H265 => native_capture::Codec::H265,
        },
        // Retained C++ maxmin_data(20000, 500, bitrate), including live API values.
        bitrate: screen.bit_rate.clamp(500, 20000),
        gop: screen.gop.clamp(1, 100),
        fps: normalized_fps(screen.fps).clamp(10, 120) as u8,
    }
}
fn portrait_blocked(codec: Codec, stream_height: u16, width: i64, height: i64) -> bool {
    codec == Codec::H264
        && width == 1440
        && height == 2560
        && (stream_height == 0 || stream_height >= 1440)
}
fn advance_deadline(previous: Instant, now: Instant, period: Duration) -> Instant {
    let next = previous + period;
    if next < now.checked_sub(period).unwrap_or(now) {
        now
    } else {
        next
    }
}

#[cfg(all(test, feature = "native-fixture"))]
#[path = "video_source_tests.rs"]
pub(crate) mod tests;
