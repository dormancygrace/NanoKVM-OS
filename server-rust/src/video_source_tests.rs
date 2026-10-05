use super::*;
use crate::{
    capture_tests::worker_command,
    monitor::Backend,
    native_backend::{AudioControl, EdidProgrammer, Native},
    native_capture::Worker,
    native_frame::{Budget, Pending, DIRECT_HEADROOM},
    systemops::{Action, Executor},
    Runtime,
};
use serde_json::{json, Value};
use std::os::unix::fs::symlink;

pub(crate) struct NoEffects;
impl AudioControl for NoEffects {
    fn stop(&self) -> Result<(), Error> {
        Ok(())
    }
}
impl EdidProgrammer for NoEffects {
    fn apply(&self, _: &Path, _: bool, _: Duration, _: &dyn Fn() -> bool) -> Result<(), Error> {
        panic!("video tests must not program EDID")
    }
}
impl Executor for NoEffects {
    fn run(&self, _: Action, _: Duration) -> Result<(), Error> {
        panic!("video tests must not run host commands")
    }
}
pub(crate) struct Fixture {
    pub(crate) root: tempfile::TempDir,
    pub(crate) runtime: Arc<Runtime>,
    pub(crate) backend: Arc<Native>,
    pub(crate) budget: Arc<Budget>,
}
impl Fixture {
    pub(crate) fn new(selection: Option<&str>, status: Option<i32>, hold: bool) -> Self {
        let root = tempfile::tempdir().unwrap();
        for dir in ["etc/kvm", "kvmapp/kvm", "run/nanokvm", "web"] {
            fs::create_dir_all(root.path().join(dir)).unwrap();
        }
        fs::write(
            root.path().join("etc/kvm/server.yaml"),
            "authentication: disable\nproto: http\n",
        )
        .unwrap();
        fs::write(root.path().join("etc/kvm/hw"), "pcie").unwrap();
        fs::write(root.path().join("web/index.html"), "fixture").unwrap();
        if let Some(selection) = selection {
            fs::write(root.path().join("etc/kvm/encoder_codec"), selection).unwrap();
        }
        let budget = Budget::new(128 * 1024);
        let mut command = worker_command();
        command.env("NK_FIXTURE_TRACE_FILE", root.path().join("trace"));
        if let Some(status) = status {
            command.env("NK_FIXTURE_VIDEO_STATUS", status.to_string());
        } else {
            command.env("NK_FIXTURE_KEYFRAMES", "1");
        }
        if hold {
            fs::write(root.path().join("hold"), "hold").unwrap();
            command.env("NK_FIXTURE_VIDEO_HOLD", root.path().join("hold"));
        }
        let actor = Actor::new(Worker::spawn(command, budget.clone()).unwrap()).unwrap();
        let backend =
            Native::from_actor(root.path(), actor, Arc::new(NoEffects), Arc::new(NoEffects))
                .unwrap();
        let runtime =
            Runtime::load_with_backends(root.path(), Arc::new(NoEffects), backend.clone()).unwrap();
        Self {
            root,
            runtime,
            backend,
            budget,
        }
    }
    pub(crate) fn source(&self) -> &Arc<Source> {
        &self.runtime.video
    }
    pub(crate) async fn initialize(&self) {
        let hdmi = self.runtime.hdmi.clone();
        tokio::task::spawn_blocking(move || hdmi.initialize())
            .await
            .unwrap()
            .unwrap();
    }
    pub(crate) fn trace(&self) -> String {
        fs::read_to_string(self.root.path().join("trace")).unwrap_or_default()
    }
    pub(crate) async fn until_trace(&self, value: &str) {
        let deadline = Instant::now() + Duration::from_secs(4);
        while !self.trace().contains(value) {
            assert!(
                Instant::now() < deadline,
                "missing {value}: {}",
                self.trace()
            );
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    }
    pub(crate) async fn cleanup(&self) {
        self.runtime.shutdown_async().await.unwrap();
        assert!(self.backend.actor().finished());
        assert!(self.source().lock().session.is_none());
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.runtime.shutdown();
        let _ = self.backend.join();
    }
}
fn oracle() -> Value {
    serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/video-source-go-oracle.json"
    ))
    .unwrap()
}
fn frame(result: i32, timestamp: i64) -> VideoFrame {
    VideoFrame {
        result,
        timestamp,
        duration: Duration::ZERO,
        storage: None,
    }
}
#[test]
fn queues_portrait_cadence_and_conflicts_match_complete_go_source() {
    let reference = oracle();
    for case in reference["queues"].as_array().unwrap() {
        let requested = Arc::new(AtomicBool::new(false));
        let entry = Entry::new(1, requested.clone());
        for (index, step) in case["steps"].as_array().unwrap().iter().enumerate() {
            let accepted =
                entry.offer(frame(step["result"].as_i64().unwrap() as i32, index as i64));
            let queue = entry.lock();
            assert_eq!(
                json!({"accepted":accepted,"waiting":queue.waiting,"requested":requested.load(Ordering::Acquire),"queued":queue.frames.len(),"result":step["result"]}),
                *step
            );
        }
        let actual: Vec<_> = entry
            .lock()
            .frames
            .iter()
            .map(|frame| json!({"result":frame.result,"timestamp":frame.timestamp}))
            .collect();
        assert_eq!(json!(actual), case["frames"]);
    }
    for case in reference["portraits"].as_array().unwrap() {
        assert_eq!(
            portrait_blocked(
                Codec::parse(case["codec"].as_str().unwrap()).unwrap(),
                case["streamHeight"].as_u64().unwrap() as u16,
                case["width"].as_i64().unwrap(),
                case["height"].as_i64().unwrap()
            ),
            case["blocked"].as_bool().unwrap()
        );
    }
    let start = Instant::now();
    for case in reference["cadence"].as_array().unwrap() {
        let period = Duration::from_nanos(case["period"].as_u64().unwrap());
        let now = start + Duration::from_nanos(case["offset"].as_u64().unwrap());
        assert_eq!(
            advance_deadline(start, now, period)
                .duration_since(start)
                .as_nanos() as u64,
            case["next"].as_u64().unwrap()
        );
    }
    let conflict = Conflict {
        active: EncoderConfig::legacy(),
        requested: EncoderConfig::default(),
    };
    assert_eq!(
        conflict.to_string(),
        reference["conflict"].as_str().unwrap()
    );
    assert_eq!(normalized_fps(-1), 30);
    assert_eq!(normalized_fps(0), 30);
    let raw = Screen {
        bit_rate: 101,
        fps: 0,
        gop: 0,
        ..Screen::default()
    };
    let native = native_config(EncoderConfig::default(), raw);
    assert_eq!((native.bitrate, native.gop, native.fps), (500, 1, 30));
}
#[tokio::test]
async fn closed_queue_has_priority_releases_shared_reservations_and_wakes_waiters() {
    let budget = Budget::new(8192);
    let storage = Pending::new_data(&budget, 14, DIRECT_HEADROOM)
        .unwrap()
        .finish_video(true, 7)
        .unwrap();
    let packet = storage.packet_bytes();
    let entry = Entry::new(1, Arc::new(AtomicBool::new(false)));
    assert!(entry.offer(VideoFrame {
        storage: Some(storage.clone()),
        result: 3,
        duration: Duration::from_millis(20),
        timestamp: 7
    }));
    drop(storage);
    entry.close();
    assert!(entry.next().await.is_none());
    assert!(!entry.offer(frame(3, 8)));
    assert!(budget.used() > 0); // An already admitted network write remains its owner.
    drop(packet);
    assert_eq!(budget.used(), 0);
    let waiting = Entry::new(2, Arc::new(AtomicBool::new(false)));
    let reader = waiting.clone();
    let task = tokio::spawn(async move { reader.next().await });
    tokio::task::yield_now().await;
    waiting.close();
    assert!(tokio::time::timeout(Duration::from_secs(1), task)
        .await
        .unwrap()
        .unwrap()
        .is_none());
}
#[tokio::test]
async fn actual_c_shared_h265_session_prefix_idr_snapshot_and_last_close() {
    let fixture = Fixture::new(None, None, false);
    fixture.initialize().await;
    let first = fixture.source().subscribe(None).await.unwrap();
    let old = fixture.source().snapshot(&first.session);
    let second = fixture.source().subscribe(None).await.unwrap();
    assert!(Arc::ptr_eq(&first.session, &second.session));
    assert_eq!(old.len(), 1);
    let joined = fixture.source().snapshot(&first.session);
    assert_eq!(joined.len(), 2);
    let read = async { tokio::join!(first.next(), second.next()) };
    let (one, two) = tokio::time::timeout(Duration::from_secs(4), read)
        .await
        .unwrap();
    let one = one.unwrap();
    let two = two.unwrap();
    assert!(one.is_keyframe() && two.is_keyframe());
    assert!(Arc::ptr_eq(
        one.storage.as_ref().unwrap(),
        two.storage.as_ref().unwrap()
    ));
    assert_eq!(one.data(), b"synthetic-frame");
    assert!(one.timestamp > 0);
    assert_eq!(one.duration, Duration::from_millis(20));
    let storage = one.storage.as_ref().unwrap();
    assert_eq!(storage.headroom(), 9);
    assert_eq!(storage.as_ref().as_ref()[0], 1);
    assert_eq!(
        i64::from_le_bytes(storage.as_ref().as_ref()[1..9].try_into().unwrap()),
        one.timestamp
    );
    assert_eq!(
        fixture.source().active_config(),
        Some(EncoderConfig::default())
    );
    let conflict = fixture
        .source()
        .subscribe(Some(EncoderConfig::legacy()))
        .await
        .err()
        .unwrap();
    assert!(conflict.downcast_ref::<Conflict>().is_some());
    fixture.source().select(EncoderConfig::default());
    assert!(!first.closed());
    first.close();
    assert_eq!(joined.len(), 2);
    assert_eq!(fixture.source().snapshot(&second.session).len(), 1);
    assert!(!old[0].offer(frame(3, 999)));
    second.close();
    assert_eq!(fixture.source().active_config(), None);
    fixture.source().join().await;
    assert!(!fixture.backend.actor().stopped());
    drop(one);
    drop(two);
    assert_eq!(fixture.budget.used(), 0);
    fixture.cleanup().await;
}
#[tokio::test]
async fn actual_c_drain_waits_without_poisoning_and_passive_join_resolves_latest_selection() {
    let fixture = Fixture::new(None, None, true);
    fixture.initialize().await;
    let old = fixture.source().subscribe(None).await.unwrap();
    fixture.until_trace("codec:2").await;
    old.close();
    assert_eq!(fixture.source().active_config(), None);
    let source = fixture.source().clone();
    let mut next = tokio::spawn(async move { source.subscribe(None).await });
    assert!(tokio::time::timeout(Duration::from_millis(40), &mut next)
        .await
        .is_err());
    fixture.source().select(EncoderConfig::legacy());
    assert!(fixture
        .source()
        .subscribe(Some(EncoderConfig::default()))
        .await
        .err()
        .unwrap()
        .downcast_ref::<Conflict>()
        .is_some());
    assert!(!fixture.backend.actor().stopped());
    fs::remove_file(fixture.root.path().join("hold")).unwrap();
    let next = tokio::time::timeout(Duration::from_secs(3), next)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert_eq!(next.config(), EncoderConfig::legacy());
    assert!(!Arc::ptr_eq(&old.session, &next.session));
    let frame = tokio::time::timeout(Duration::from_secs(3), next.next())
        .await
        .unwrap()
        .unwrap();
    assert!(frame.is_keyframe());
    assert!(fixture.trace().contains("codec:1"));
    next.close();
    drop(frame);
    fixture.source().join().await;
    assert!(!fixture.backend.actor().stopped());
    assert_eq!(fixture.budget.used(), 0);
    fixture.cleanup().await;
}
#[tokio::test]
async fn cancellation_of_waiting_replacement_never_cancels_old_native_read() {
    let fixture = Fixture::new(None, None, true);
    fixture.initialize().await;
    let old = fixture
        .source()
        .subscribe(Some(EncoderConfig::legacy()))
        .await
        .unwrap();
    fixture.until_trace("codec:1").await;
    old.close();
    let source = fixture.source().clone();
    let waiting = tokio::spawn(async move { source.subscribe(None).await });
    tokio::time::sleep(Duration::from_millis(20)).await;
    waiting.abort();
    assert!(waiting.await.err().unwrap().is_cancelled());
    fs::remove_file(fixture.root.path().join("hold")).unwrap();
    fixture.source().join().await;
    assert!(!fixture.backend.actor().stopped());
    let next = fixture.source().subscribe(None).await.unwrap();
    assert_eq!(next.config(), EncoderConfig::default());
    next.close();
    fixture.source().join().await;
    fixture.cleanup().await;
}
#[tokio::test]
async fn selection_closes_old_decoder_queues_and_does_not_leak_frames_into_replacement() {
    let fixture = Fixture::new(None, None, false);
    fixture.initialize().await;
    let first = fixture
        .source()
        .subscribe(Some(EncoderConfig::legacy()))
        .await
        .unwrap();
    let second = fixture.source().subscribe(None).await.unwrap();
    let old_snapshot = fixture.source().snapshot(&first.session);
    assert!(tokio::time::timeout(Duration::from_secs(4), first.next())
        .await
        .unwrap()
        .unwrap()
        .is_keyframe());
    fixture.source().select(EncoderConfig::default());
    assert!(first.closed() && second.closed());
    assert!(first.next().await.is_none() && second.next().await.is_none());
    assert_eq!(
        fixture.source().selected_config(),
        Some(EncoderConfig::default())
    );
    let replacement = fixture.source().subscribe(None).await.unwrap();
    assert_eq!(fixture.source().snapshot(&first.session).len(), 0);
    assert_eq!(fixture.source().snapshot(&replacement.session).len(), 1);
    assert!(!old_snapshot[0].offer(frame(3, 777)));
    let current = tokio::time::timeout(Duration::from_secs(3), replacement.next())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(replacement.config(), EncoderConfig::default());
    assert!(current.is_keyframe());
    replacement.close();
    drop(current);
    fixture.source().join().await;
    assert_eq!(fixture.budget.used(), 0);
    fixture.cleanup().await;
}
#[tokio::test]
async fn source_native_error_preserves_initial_idr_wait_and_absolute_cadence() {
    let fixture = Fixture::new(None, Some(-5), false);
    fixture.initialize().await;
    let subscription = fixture.source().subscribe(None).await.unwrap();
    for _ in 0..3 {
        let next = tokio::time::timeout(Duration::from_secs(3), subscription.next())
            .await
            .unwrap()
            .unwrap();
        assert_eq!(next.result, -5);
        assert!(next.data().is_empty());
        assert_eq!((next.duration, next.timestamp), (Duration::ZERO, 0));
        assert!(subscription.entry.lock().waiting);
    }
    subscription.close();
    fixture.source().join().await;
    assert!(!fixture.backend.actor().stopped());
    assert_eq!(fixture.budget.used(), 0);
    fixture.cleanup().await;
}
#[tokio::test]
async fn actual_c_live_quality_clamp_fps_and_global_keyframe_coalescing() {
    let fixture = Fixture::new(None, None, false);
    fixture.initialize().await;
    let source = fixture.source();
    let mut last = None;
    let origin = Instant::now();
    for step in oracle()["keyframes"].as_array().unwrap() {
        if step["request"].as_bool().unwrap() {
            source.request_keyframe();
        }
        let taken = source.take_keyframe(
            origin + Duration::from_nanos(step["offset"].as_u64().unwrap()),
            &mut last,
        );
        assert_eq!(taken, step["taken"].as_bool().unwrap());
        assert_eq!(
            source.keyframe.load(Ordering::Acquire),
            step["pending"].as_bool().unwrap()
        );
    }
    fixture.runtime.screen.set("fps", 30).unwrap();
    let first = source.subscribe(None).await.unwrap();
    fixture.until_trace("codec:2").await;
    fixture.runtime.screen.set("quality", 101).unwrap();
    fixture.until_trace("bitrate:500").await;
    fixture.until_trace("fps:30").await;
    let before = fixture.trace().matches("keyframe:1").count();
    for _ in 0..100 {
        source.request_keyframe();
    }
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert!(fixture.trace().matches("keyframe:1").count() <= before + 1);
    first.close();
    source.join().await;
    assert!(!fixture.backend.actor().stopped());
    fixture.cleanup().await;
}
#[tokio::test]
async fn native_read_timeout_closes_subscribers_and_shutdown_joins_without_leaked_budget() {
    let fixture = Fixture::new(None, None, true);
    fixture.initialize().await;
    let first = fixture.source().subscribe(None).await.unwrap();
    fixture.until_trace("codec:2").await;
    tokio::time::timeout(Duration::from_secs(4), async {
        while let Some(frame) = first.next().await {
            // Queue timeout may become observable just before the owner exits.
            assert!(frame.result < 0 && frame.data().is_empty());
        }
    })
    .await
    .unwrap();
    assert!(fixture.backend.actor().stopped());
    assert_eq!(fixture.source().active_config(), None);
    assert!(fixture.source().subscribe(None).await.is_err());
    fixture.cleanup().await;
    assert_eq!(fixture.budget.used(), 0);
}
#[tokio::test]
async fn maximum_portrait_rejects_h264_before_native_capture_and_h265_remains_available() {
    let fixture = Fixture::new(None, None, false);
    fs::write(fixture.root.path().join("run/nanokvm/width"), "1440").unwrap();
    fs::write(fixture.root.path().join("run/nanokvm/height"), "2560").unwrap();
    let error = fixture
        .source()
        .subscribe(Some(EncoderConfig::legacy()))
        .await
        .err()
        .unwrap();
    assert_eq!(
        error.to_string(),
        "maximum portrait output requires H.265 Direct or a smaller stream resolution"
    );
    assert!(fixture.source().lock().session.is_none());
    assert!(!fixture.trace().contains("codec:"));
    fixture.initialize().await;
    let subscription = fixture.source().subscribe(None).await.unwrap();
    subscription.close();
    fixture.source().join().await;
    fixture.runtime.screen.set("resolution", 1080).unwrap();
    let smaller = fixture
        .source()
        .subscribe(Some(EncoderConfig::legacy()))
        .await
        .unwrap();
    smaller.close();
    fixture.source().join().await;
    fixture.cleanup().await;
}
#[tokio::test]
async fn bounded_subscriber_admission_and_saved_selection_follow_confined_legacy_links() {
    let fixture = Fixture::new(Some(" h264\n"), None, false);
    assert_eq!(
        fixture.source().selected_config(),
        Some(EncoderConfig::legacy())
    );
    let mut subscriptions = Vec::new();
    for _ in 0..MAX_SUBSCRIBERS {
        subscriptions.push(fixture.source().subscribe(None).await.unwrap());
    }
    assert_eq!(fixture.source().lock().entries.len(), MAX_SUBSCRIBERS);
    assert_eq!(
        fixture
            .source()
            .subscribe(None)
            .await
            .err()
            .unwrap()
            .to_string(),
        "video subscriber limit reached"
    );
    drop(subscriptions);
    fixture.source().join().await;
    assert_eq!(fixture.budget.used(), 0);
    fs::remove_file(fixture.root.path().join("etc/kvm/encoder_codec")).unwrap();
    fs::write(fixture.root.path().join("etc/kvm/selection"), "h265\n").unwrap();
    symlink(
        "/etc/kvm/selection",
        fixture.root.path().join("etc/kvm/encoder_codec"),
    )
    .unwrap();
    let linked = Source::new(
        fixture.root.path().to_owned(),
        fixture.runtime.screen.clone(),
        fixture.runtime.hdmi.clone(),
        None,
    );
    assert_eq!(linked.selected_config(), Some(EncoderConfig::default()));
    assert_eq!(
        linked.subscribe(None).await.err().unwrap().to_string(),
        "native video backend is not linked"
    );
    fs::write(fixture.root.path().join("etc/kvm/selection"), "H265").unwrap();
    let invalid = Source::new(
        fixture.root.path().to_owned(),
        fixture.runtime.screen.clone(),
        fixture.runtime.hdmi.clone(),
        None,
    );
    assert_eq!(invalid.selected_config(), None);
    fixture.cleanup().await;
}
#[tokio::test]
async fn shutdown_cancels_warmup_and_held_native_read_and_wakes_all_viewers() {
    for hold in [false, true] {
        let fixture = Fixture::new(None, None, hold);
        fixture.initialize().await;
        let first = fixture.source().subscribe(None).await.unwrap();
        if hold {
            fixture.until_trace("codec:2").await;
        }
        fixture.cleanup().await;
        assert!(first.next().await.is_none());
        assert_eq!(fixture.budget.used(), 0);
        assert!(fixture.source().subscribe(None).await.is_err());
    }
}

#[tokio::test]
async fn source_warmup_and_native_read_leave_single_thread_event_loop_responsive() {
    let fixture = Fixture::new(None, None, true);
    fixture.initialize().await;
    let first = fixture.source().subscribe(None).await.unwrap();
    let began = Instant::now();
    tokio::time::sleep(Duration::from_millis(30)).await;
    assert!(began.elapsed() < Duration::from_millis(500));
    fixture.until_trace("codec:2").await;
    let began = Instant::now();
    tokio::time::sleep(Duration::from_millis(30)).await;
    assert!(began.elapsed() < Duration::from_millis(500));
    fs::remove_file(fixture.root.path().join("hold")).unwrap();
    first.close();
    fixture.source().join().await;
    assert!(!fixture.backend.actor().stopped());
    fixture.cleanup().await;
}

#[tokio::test]
async fn full_actor_queue_preserves_keyframe_intent_until_native_admission_recovers() {
    let fixture = Fixture::new(None, None, false);
    fixture.initialize().await;
    let hdmi = fixture.runtime.hdmi.clone();
    let actor = fixture.backend.actor().clone();
    // Consume the fresh-read lease with a real synthetic JPEG so it cannot
    // mask a lost IDR intent by requesting another refresh after discard.
    tokio::task::spawn_blocking(move || {
        let lease = hdmi.acquire_read(&|| false).unwrap();
        let captured = actor
            .call_blocking(
                Request::Mjpeg {
                    width: 0,
                    height: 0,
                    quality: 90,
                },
                Instant::now(),
                Duration::from_secs(2),
                &|| false,
            )
            .unwrap();
        assert!(captured.frame.is_some() && lease.claim_fresh().unwrap());
    })
    .await
    .unwrap();
    let hold = Arc::new(AtomicBool::new(true));
    let entered = Arc::new(AtomicBool::new(false));
    let actor = fixture.backend.actor().clone();
    let held = hold.clone();
    let began = entered.clone();
    let busy = tokio::task::spawn_blocking(move || {
        actor.transaction_blocking(
            Instant::now(),
            Duration::from_secs(5),
            Duration::ZERO,
            &|| false,
            move |_, context| {
                began.store(true, Ordering::Release);
                while held.load(Ordering::Acquire) {
                    context.remaining()?;
                    std::thread::sleep(Duration::from_millis(5));
                }
                Ok(Outcome {
                    status: 0,
                    frame: None,
                })
            },
        )
    });
    while !entered.load(Ordering::Acquire) {
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    let mut queued = Vec::new();
    for _ in 0..8 {
        let actor = fixture.backend.actor().clone();
        queued.push(tokio::spawn(async move {
            actor
                .call(Request::Signal, Instant::now(), Duration::from_secs(5))
                .await
        }));
        tokio::task::yield_now().await;
    }
    let subscription = fixture.source().subscribe(None).await.unwrap();
    let frame = tokio::time::timeout(Duration::from_secs(2), subscription.next())
        .await
        .unwrap()
        .unwrap();
    assert!(frame.result < 0 && frame.data().is_empty());
    assert!(
        fixture.source().keyframe.load(Ordering::Acquire),
        "rejected refresh lost its intent"
    );
    assert!(!fixture.trace().contains("keyframe:1"));
    hold.store(false, Ordering::Release);
    busy.await.unwrap().unwrap();
    for task in queued {
        assert_eq!(task.await.unwrap().unwrap().status, 1);
    }
    tokio::time::timeout(Duration::from_secs(3), async {
        loop {
            let frame = subscription.next().await.unwrap();
            if frame.result >= 0 {
                assert!(frame.is_keyframe());
                break;
            }
        }
    })
    .await
    .unwrap();
    subscription.close();
    fixture.source().join().await;
    assert!(!fixture.backend.actor().stopped());
    assert_eq!(fixture.budget.used(), 0);
    fixture.cleanup().await;
}
