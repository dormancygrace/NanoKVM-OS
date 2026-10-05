use crate::{
    capture_tests::worker_command,
    monitor::Backend,
    native_backend::{self, AudioControl, EdidProgrammer, Native},
    native_capture::{Request, Worker},
    native_capture_actor::Actor,
    native_frame::Budget,
    systemops::{Action, Executor},
    Error, Runtime,
};
use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::Request as HttpRequest,
    Router,
};
use serde_json::{json, Value};
use std::{
    fs,
    io::Write,
    net::SocketAddr,
    os::unix::fs::PermissionsExt,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};
use tower::ServiceExt;

#[derive(Default)]
struct Audio {
    stops: AtomicUsize,
}
impl AudioControl for Audio {
    fn stop(&self) -> Result<(), Error> {
        self.stops.fetch_add(1, Ordering::AcqRel);
        Ok(())
    }
}
impl Executor for Audio {
    fn run(&self, _: Action, _: Duration) -> Result<(), Error> {
        panic!("native backend fixtures must not run host commands")
    }
}
struct Programmer {
    root: PathBuf,
    hold: AtomicBool,
    entered: AtomicBool,
    fail: AtomicBool,
    calls: Mutex<Vec<(bool, bool)>>,
}
impl EdidProgrammer for Programmer {
    fn apply(
        &self,
        path: &Path,
        cube: bool,
        timeout: Duration,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        assert!(path.starts_with(&self.root));
        assert!(timeout <= Duration::from_secs(90));
        self.calls.lock().unwrap().push((
            cube,
            self.root
                .join("etc/kvm/monitor_power_cycle_pending")
                .is_file(),
        ));
        let mut log = fs::OpenOptions::new()
            .append(true)
            .open(self.root.join("trace"))
            .unwrap();
        writeln!(log, "helper:{}", u8::from(cube)).unwrap();
        self.entered.store(true, Ordering::Release);
        let deadline = Instant::now() + timeout;
        while self.hold.load(Ordering::Acquire) {
            if cancelled() || Instant::now() >= deadline {
                return Err("fixture helper cancelled or timed out".into());
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        if self.fail.load(Ordering::Acquire) {
            Err("fixture programming failed".into())
        } else {
            Ok(())
        }
    }
}
struct Fixture {
    root: tempfile::TempDir,
    backend: Arc<Native>,
    audio: Arc<Audio>,
    programmer: Arc<Programmer>,
    budget: Arc<Budget>,
}
impl Fixture {
    fn new(board: &str, faults: &[(&str, &str)], settings: &[(&str, &str)]) -> Self {
        let root = tempfile::tempdir().unwrap();
        for dir in [
            "etc/kvm",
            "kvmapp/kvm",
            "usr/share/nanokvm/edid",
            "run",
            "web",
        ] {
            fs::create_dir_all(root.path().join(dir)).unwrap();
        }
        fs::write(root.path().join("etc/kvm/hw"), board).unwrap();
        fs::write(
            root.path().join("etc/kvm/hdmi_version"),
            if board == "pcie" { "ux" } else { "c" },
        )
        .unwrap();
        fs::write(root.path().join("etc/kvm/monitor_resolution"), "0").unwrap();
        fs::write(
            root.path().join("etc/kvm/server.yaml"),
            "authentication: disable\nproto: http\n",
        )
        .unwrap();
        for (name, data) in settings {
            fs::write(root.path().join("kvmapp/kvm").join(name), data).unwrap();
        }
        for name in ["NanoKVM-monitor-720.bin", "NanoKVM-cube-monitor-720.bin"] {
            fs::write(
                root.path().join("usr/share/nanokvm/edid").join(name),
                "synthetic-edid",
            )
            .unwrap();
        }
        fs::write(root.path().join("web/index.html"), "native fixture").unwrap();
        let audio = Arc::new(Audio::default());
        let budget = Budget::new(8192);
        let programmer = Arc::new(Programmer {
            root: root.path().to_owned(),
            hold: AtomicBool::new(false),
            entered: AtomicBool::new(false),
            fail: AtomicBool::new(false),
            calls: Mutex::new(vec![]),
        });
        let mut command = worker_command();
        command.env("NK_FIXTURE_TRACE_FILE", root.path().join("trace"));
        for (name, value) in faults {
            command.env(name, value);
        }
        let actor = Actor::new(Worker::spawn(command, budget.clone()).unwrap()).unwrap();
        let backend =
            Native::from_actor(root.path(), actor, audio.clone(), programmer.clone()).unwrap();
        fs::write(root.path().join("trace"), "").unwrap();
        Self {
            root,
            backend,
            audio,
            programmer,
            budget,
        }
    }
    fn profile(&self) -> PathBuf {
        self.root.path().join("usr/share/nanokvm/edid").join(
            if fs::read_to_string(self.root.path().join("etc/kvm/hw")).unwrap() == "pcie" {
                "NanoKVM-monitor-720.bin"
            } else {
                "NanoKVM-cube-monitor-720.bin"
            },
        )
    }
    fn trace(&self) -> Vec<String> {
        fs::read_to_string(self.root.path().join("trace"))
            .unwrap()
            .lines()
            .map(str::to_owned)
            .collect()
    }
    fn runtime(&self) -> Arc<Runtime> {
        Runtime::load_with_backends(self.root.path(), self.audio.clone(), self.backend.clone())
            .unwrap()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.programmer.hold.store(false, Ordering::Release);
        self.backend.stop();
        self.backend.join().unwrap();
    }
}
async fn until(check: impl Fn() -> bool) {
    tokio::time::timeout(Duration::from_secs(3), async {
        while !check() {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
}
async fn request(router: &Router, method: &str, path: &str, body: Value) -> Value {
    let mut request = HttpRequest::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json")
        .body(Body::from(body.to_string()))
        .unwrap();
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:43400".parse::<SocketAddr>().unwrap(),
    ));
    let response = router.clone().oneshot(request).await.unwrap();
    serde_json::from_slice(&to_bytes(response.into_body(), 1 << 20).await.unwrap()).unwrap()
}
#[test]
fn all_native_chroma_bytes_match_complete_immutable_go_function() {
    let oracle: Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/native-status-go-oracle.json"
    ))
    .unwrap();
    for c in oracle["cases"].as_array().unwrap() {
        let status = native_backend::decode_status(7, c["flags"].as_u64().unwrap() as u8);
        assert_eq!(
            status.mjpeg_chroma,
            c["active"].as_u64().unwrap() as u16,
            "{c}"
        );
        assert_eq!(status.chroma_fallback, c["reason"].as_str().unwrap(), "{c}");
        assert_eq!(status.gop_mode, 7);
    }
}
#[test]
fn native_backend_loads_saved_mode_chroma_and_exposes_actual_c_status_and_audio_owner() {
    let f = Fixture::new("pcie", &[], &[("gop_mode", "0"), ("mjpeg_chroma", "420")]);
    let status = f.backend.video_status().unwrap();
    assert_eq!(status.gop_mode, 0);
    assert_eq!(status.mjpeg_chroma, 420);
    f.backend.set_mjpeg_chroma(422).unwrap();
    assert_eq!(f.backend.video_status().unwrap().mjpeg_chroma, 422);
    f.backend.set_gop(90).unwrap();
    assert!(f.backend.has_hdmi_signal().unwrap());
    assert!(f.backend.set_gop(0).is_err());
    assert!(f.backend.set_mjpeg_chroma(421).is_err());
    f.backend.stop_audio().unwrap();
    assert_eq!(f.audio.stops.load(Ordering::Acquire), 1);
    assert!(f.trace().contains(&"gop:90".into()));
    assert!(!f.trace().contains(&"gop:0".into()));
}
#[test]
fn real_native_edid_order_cube_marker_modes_and_helper_failures_are_preserved() {
    for board in ["pcie", "alpha"] {
        for failed in [false, true] {
            let f = Fixture::new(board, &[], &[]);
            f.programmer.fail.store(failed, Ordering::Release);
            let result = f.backend.apply_monitor_profile(&f.profile());
            assert_eq!(result.is_err(), failed);
            if let Err(error) = result {
                assert_eq!(error.to_string(), "monitor EDID programming failed");
            }
            let cube = board == "alpha";
            assert_eq!(
                f.trace(),
                if cube {
                    vec!["edid:1", "helper:1", "edid:0"]
                } else {
                    vec!["hdmi:0", "helper:0", "hdmi:1"]
                }
            );
            assert_eq!(*f.programmer.calls.lock().unwrap(), [(cube, cube)]);
            let marker = f.root.path().join("etc/kvm/monitor_power_cycle_pending");
            assert_eq!(marker.exists(), cube);
            if cube {
                assert_eq!(fs::read_to_string(&marker).unwrap(), "1\n");
                assert_eq!(
                    fs::metadata(marker).unwrap().permissions().mode() & 0o777,
                    0o600
                );
            }
            assert!(f.backend.has_hdmi_signal().unwrap());
        }
    }
}
#[test]
fn cube_marker_failure_restores_maintenance_without_starting_programmer() {
    let f = Fixture::new("alpha", &[], &[]);
    fs::create_dir(f.root.path().join("etc/kvm/monitor_power_cycle_pending")).unwrap();
    assert!(f.backend.apply_monitor_profile(&f.profile()).is_err());
    assert!(f.programmer.calls.lock().unwrap().is_empty());
    assert_eq!(f.trace(), ["edid:1", "edid:0"]);
    assert!(f.backend.has_hdmi_signal().unwrap());
}
#[test]
fn pause_and_restore_faults_have_different_cleanup_and_fail_closed_on_restore() {
    for (board, name, value, expected, closed) in [
        ("alpha", "NK_FIXTURE_EDID_FAIL", "1", vec!["edid:1"], false),
        (
            "pcie",
            "NK_FIXTURE_HDMI_FAIL",
            "0",
            vec!["hdmi:0", "hdmi:1"],
            false,
        ),
        (
            "alpha",
            "NK_FIXTURE_EDID_FAIL",
            "0",
            vec!["edid:1", "helper:1", "edid:0", "close:0"],
            true,
        ),
        (
            "pcie",
            "NK_FIXTURE_HDMI_FAIL",
            "1",
            vec!["hdmi:0", "helper:0", "hdmi:1", "close:0"],
            true,
        ),
    ] {
        let f = Fixture::new(board, &[(name, value)], &[]);
        let result = f.backend.apply_monitor_profile(&f.profile());
        assert!(result.is_err());
        assert_eq!(f.trace(), expected, "{board}/{name}/{value}");
        assert_eq!(f.backend.has_hdmi_signal().is_err(), closed);
        if closed {
            assert!(result
                .err()
                .unwrap()
                .to_string()
                .contains("cannot restore capture"));
            assert_eq!(f.programmer.calls.lock().unwrap().len(), 1);
        } else {
            assert!(f.programmer.calls.lock().unwrap().is_empty());
        }
    }
}
#[test]
fn helper_timeout_restores_capture_keeps_cube_marker_and_allows_later_controls() {
    let mut f = Fixture::new("alpha", &[], &[]);
    Arc::get_mut(&mut f.backend)
        .unwrap()
        .test_profile_timeout(Duration::from_millis(200));
    f.programmer.hold.store(true, Ordering::Release);
    let start = Instant::now();
    assert!(f.backend.apply_monitor_profile(&f.profile()).is_err());
    assert!(start.elapsed() < Duration::from_secs(3));
    assert_eq!(f.trace(), ["edid:1", "helper:1", "edid:0"]);
    assert!(f
        .root
        .path()
        .join("etc/kvm/monitor_power_cycle_pending")
        .exists());
    assert!(f.backend.has_hdmi_signal().unwrap());
}
#[tokio::test(flavor = "current_thread")]
async fn compound_profile_shares_eight_async_blocking_slots_and_captures_cannot_interleave() {
    let f = Fixture::new("pcie", &[], &[]);
    f.programmer.hold.store(true, Ordering::Release);
    let backend = f.backend.clone();
    let path = f.profile();
    let profile = tokio::task::spawn_blocking(move || backend.apply_monitor_profile(&path));
    until(|| f.programmer.entered.load(Ordering::Acquire)).await;
    let mut queued = vec![];
    for index in 0..8 {
        let actor = f.backend.actor().clone();
        queued.push(tokio::spawn(async move {
            actor
                .call(
                    if index == 0 {
                        Request::Mjpeg {
                            width: 0,
                            height: 0,
                            quality: 90,
                        }
                    } else {
                        Request::Signal
                    },
                    Instant::now(),
                    Duration::from_secs(3),
                )
                .await
        }));
    }
    tokio::time::sleep(Duration::from_millis(20)).await;
    let actor = f.backend.actor().clone();
    let busy = tokio::task::spawn_blocking(move || {
        actor.call_blocking(
            Request::Signal,
            Instant::now(),
            Duration::from_secs(1),
            &|| false,
        )
    });
    let error = tokio::time::timeout(Duration::from_millis(250), busy)
        .await
        .unwrap()
        .unwrap()
        .err()
        .unwrap();
    assert!(error.to_string().contains("queue is full"));
    assert_eq!(f.budget.used(), 0);
    assert_eq!(f.trace(), ["hdmi:0", "helper:0"]);
    f.programmer.hold.store(false, Ordering::Release);
    profile.await.unwrap().unwrap();
    for (index, task) in queued.into_iter().enumerate() {
        let outcome = task.await.unwrap().unwrap();
        if index == 0 {
            assert_eq!(outcome.frame.unwrap().as_ref().as_ref(), b"synthetic-frame");
        } else {
            assert_eq!(outcome.status, 1);
        }
    }
    assert_eq!(f.budget.used(), 0);
    let trace = f.trace();
    assert!(
        trace.iter().position(|s| s == "frame:90").unwrap()
            > trace.iter().position(|s| s == "hdmi:1").unwrap()
    );
}
#[tokio::test(flavor = "current_thread")]
async fn actual_api_worker_preserves_settings_and_latest_admin_hdmi_disable_after_profile() {
    let f = Fixture::new("pcie", &[], &[("gop_mode", "0"), ("mjpeg_chroma", "420")]);
    let runtime = f.runtime();
    let router = crate::app(runtime.clone(), f.root.path().join("web"));
    let get = request(&router, "GET", "/api/vm/screen", Value::Null).await;
    assert_eq!(get["code"], 0);
    assert_eq!(get["data"]["gopModeActive"], 0);
    assert_eq!(get["data"]["mjpegChromaActive"], 420);
    assert_eq!(
        request(
            &router,
            "POST",
            "/api/vm/screen",
            json!({"Type":"mjpeg_chroma","Value":422})
        )
        .await["data"]["mjpegChromaActive"],
        422
    );
    assert_eq!(
        request(
            &router,
            "POST",
            "/api/vm/screen",
            json!({"Type":"gop","Value":95})
        )
        .await["code"],
        0
    );
    assert_eq!(runtime.screen.snapshot().unwrap().gop, 95);
    f.programmer.hold.store(true, Ordering::Release);
    fs::write(f.root.path().join("trace"), "").unwrap();
    let r = router.clone();
    let profile = tokio::spawn(async move {
        request(
            &r,
            "POST",
            "/api/vm/screen",
            json!({"Type":"monitor","Value":720}),
        )
        .await
    });
    until(|| f.programmer.entered.load(Ordering::Acquire)).await;
    let r = router.clone();
    let disabled =
        tokio::spawn(async move { request(&r, "POST", "/api/vm/hdmi/disable", Value::Null).await });
    until(|| f.root.path().join("etc/kvm/hdmi_disable").exists()).await;
    f.programmer.hold.store(false, Ordering::Release);
    assert_eq!(profile.await.unwrap()["code"], 0);
    assert_eq!(disabled.await.unwrap()["code"], 0);
    assert_eq!(f.trace(), ["hdmi:0", "helper:0", "hdmi:0", "hdmi:0"]);
    assert_eq!(
        request(&router, "GET", "/api/vm/hdmi", Value::Null).await["data"]["enabled"],
        false
    );
    assert_eq!(
        fs::read_to_string(f.root.path().join("etc/kvm/monitor_resolution")).unwrap(),
        "720"
    );
    runtime.shutdown_async().await.unwrap();
    assert!(f.backend.actor().finished());
}
#[tokio::test(flavor = "current_thread")]
async fn runtime_shutdown_cancels_helper_restores_deinitializes_and_joins_off_event_loop() {
    let f = Fixture::new("alpha", &[], &[]);
    let runtime = f.runtime();
    let router = crate::app(runtime.clone(), f.root.path().join("web"));
    f.programmer.hold.store(true, Ordering::Release);
    let task = tokio::spawn(async move {
        request(
            &router,
            "POST",
            "/api/vm/screen",
            json!({"Type":"monitor","Value":720,"confirmPowerCycle":true}),
        )
        .await
    });
    until(|| f.programmer.entered.load(Ordering::Acquire)).await;
    tokio::time::timeout(Duration::from_secs(3), runtime.shutdown_async())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(task.await.unwrap()["code"], -4);
    assert!(f.backend.actor().finished());
    assert_eq!(f.trace(), ["edid:1", "helper:1", "edid:0", "close:0"]);
    assert!(f.audio.stops.load(Ordering::Acquire) >= 1);
    assert!(f
        .root
        .path()
        .join("etc/kvm/monitor_power_cycle_pending")
        .exists());
    assert_eq!(f.budget.used(), 0);
}
#[tokio::test(flavor = "current_thread")]
async fn cancelled_blocking_queue_entry_never_reaches_c_worker() {
    let f = Fixture::new("pcie", &[], &[]);
    f.programmer.hold.store(true, Ordering::Release);
    let b = f.backend.clone();
    let path = f.profile();
    let active = tokio::task::spawn_blocking(move || b.apply_monitor_profile(&path));
    until(|| f.programmer.entered.load(Ordering::Acquire)).await;
    let cancelled = Arc::new(AtomicBool::new(false));
    let flag = cancelled.clone();
    let actor = f.backend.actor().clone();
    let waiting = tokio::task::spawn_blocking(move || {
        actor.call_blocking(
            Request::Gop(70),
            Instant::now(),
            Duration::from_secs(3),
            &|| flag.load(Ordering::Acquire),
        )
    });
    tokio::time::sleep(Duration::from_millis(20)).await;
    cancelled.store(true, Ordering::Release);
    assert!(waiting.await.unwrap().is_err());
    f.programmer.hold.store(false, Ordering::Release);
    active.await.unwrap().unwrap();
    assert!(f.backend.has_hdmi_signal().unwrap());
    assert!(!f.trace().contains(&"gop:70".into()));
}
#[test]
fn root_only_native_loader_and_programmer_never_run_in_isolated_fixtures() {
    let root = tempfile::tempdir().unwrap();
    let audio = Arc::new(Audio::default());
    assert!(Runtime::load_with_native(root.path(), Budget::new(8192), audio.clone()).is_err());
    assert_eq!(audio.stops.load(Ordering::Acquire), 0);
    let programmer = native_backend::FirmwareProgrammer::new(root.path().to_owned());
    assert!(programmer
        .apply(root.path(), false, Duration::from_secs(1), &|| false)
        .is_err());
    assert_eq!(fs::read_dir(root.path()).unwrap().count(), 0);
}

#[test]
fn native_pause_has_its_own_short_deadline_and_never_waits_the_helper_budget() {
    let f = Fixture::new("pcie", &[("NK_FIXTURE_HDMI_STALL", "0")], &[]);
    let start = Instant::now();
    assert!(f.backend.apply_monitor_profile(&f.profile()).is_err());
    assert!(start.elapsed() < Duration::from_secs(4));
    assert!(f.programmer.calls.lock().unwrap().is_empty());
    assert_eq!(f.trace(), ["hdmi:0"]);
    assert!(f.backend.has_hdmi_signal().is_err());
}

#[test]
fn firmware_programmer_keeps_profile_metacharacters_in_one_literal_argument() {
    let root = tempfile::tempdir().unwrap();
    let path = root.path().join("profile with spaces;$(false).bin");
    fs::write(&path, "fixture").unwrap();
    let programmer = native_backend::FirmwareProgrammer::new(PathBuf::from("/"));
    for cube in [false, true] {
        let command = programmer.command(&path, cube).unwrap();
        assert_eq!(command.get_program(), "/usr/sbin/nanokvm_update_edid");
        let args: Vec<_> = command.get_args().collect();
        if cube {
            assert_eq!(
                args,
                [
                    std::ffi::OsStr::new("--accept-power-cycle"),
                    path.as_os_str()
                ]
            );
        } else {
            assert_eq!(args, [path.as_os_str()]);
        }
    }
    assert!(!root.path().join("false").exists());
}
