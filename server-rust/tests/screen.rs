use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::{Request, StatusCode},
    Router,
};
use base64::{engine::general_purpose::STANDARD, Engine};
use nanokvm_server::{
    app,
    crypto::{self, Claims},
    monitor::{Backend, VideoStatus},
    systemops::{Action, Executor},
    Error, Runtime,
};
use serde_json::{json, Value};
use std::{
    fs,
    net::SocketAddr,
    os::unix::fs::{symlink, PermissionsExt},
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Condvar, Mutex,
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};
use tower::ServiceExt;

fn oracle() -> Value {
    serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/screen-api-go-oracle.json"
    ))
    .unwrap()
}
struct Fake {
    root: PathBuf,
    calls: Mutex<Vec<Value>>,
    status: Mutex<VideoStatus>,
    fail_chroma: bool,
    fail_profile: bool,
    fail_gop: AtomicBool,
    block_next: AtomicBool,
    entered: AtomicBool,
    release: (Mutex<bool>, Condvar),
}
impl Fake {
    fn maybe_block(&self) {
        if self.block_next.swap(false, Ordering::AcqRel) {
            self.entered.store(true, Ordering::Release);
            let (lock, wake) = &self.release;
            let guard = lock.lock().unwrap();
            let (_released, timeout) = wake
                .wait_timeout_while(guard, Duration::from_secs(5), |released| !*released)
                .unwrap();
            assert!(!timeout.timed_out(), "fixture backend was not released");
        }
    }
    fn unblock(&self) {
        *self.release.0.lock().unwrap() = true;
        self.release.1.notify_all();
    }
}
impl Executor for Fake {
    fn run(&self, _: Action, _: Duration) -> Result<(), Error> {
        panic!("screen API must not execute host commands")
    }
}
impl Backend for Fake {
    fn stop_audio(&self) -> Result<(), Error> {
        panic!("native profile transaction, not the handler, owns audio")
    }
    fn apply_monitor_profile(&self, path: &Path) -> Result<(), Error> {
        let label = if path
            .file_name()
            .unwrap()
            .to_string_lossy()
            .starts_with("nanokvm-pointer-")
        {
            "decorated-pointer".to_owned()
        } else {
            format!("/{}", path.strip_prefix(&self.root).unwrap().display())
        };
        self.calls
            .lock()
            .unwrap()
            .push(json!({"kind":"profile","path":label,"data":STANDARD.encode(fs::read(path)?)}));
        self.maybe_block();
        if self.fail_profile {
            Err("fixture monitor failure".into())
        } else {
            Ok(())
        }
    }
    fn set_gop(&self, value: u8) -> Result<(), Error> {
        self.calls
            .lock()
            .unwrap()
            .push(json!({"kind":"gop","value":value}));
        if self.fail_gop.load(Ordering::Acquire) {
            Err("fixture GOP failure".into())
        } else {
            Ok(())
        }
    }
    fn set_mjpeg_chroma(&self, value: u16) -> Result<(), Error> {
        self.calls
            .lock()
            .unwrap()
            .push(json!({"kind":"chroma","value":value}));
        self.maybe_block();
        if self.fail_chroma {
            return Err("fixture chroma failure".into());
        }
        let mut status = self.status.lock().unwrap();
        status.mjpeg_chroma = if status.chroma_fallback.is_empty() {
            value
        } else {
            422
        };
        Ok(())
    }
    fn video_status(&self) -> Result<VideoStatus, Error> {
        Ok(self.status.lock().unwrap().clone())
    }
}
struct Fixture {
    root: tempfile::TempDir,
    fake: Arc<Fake>,
    runtime: Arc<Runtime>,
    router: Router,
}
impl Fixture {
    fn new(case: &Value, auth: bool) -> Self {
        let root = tempfile::tempdir().unwrap();
        for dir in [
            "etc/kvm",
            "kvmapp/kvm",
            "proc/device-tree/reserved-memory/ion",
            "sys/module/cv181x_vi/parameters",
            "sys/kernel/config/usb_gadget/g0/os_desc",
            "usr/share/nanokvm/edid",
            "run/nanokvm",
            "boot",
            "web",
        ] {
            fs::create_dir_all(root.path().join(dir)).unwrap();
        }
        let edids: Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/usb-go-oracle.json"
        ))
        .unwrap();
        let data = STANDARD
            .decode(edids["edids"][0]["input"].as_str().unwrap())
            .unwrap();
        for name in [
            "NanoKVM-stock.bin",
            "NanoKVM-final-video-profiles.bin",
            "NanoKVM-monitor-600.bin",
            "NanoKVM-monitor-720.bin",
            "NanoKVM-monitor-1080.bin",
            "NanoKVM-monitor-1440.bin",
            "NanoKVM-cube-monitor-720.bin",
            "NanoKVM-cube-monitor-1080.bin",
            "NanoKVM-portrait-720x1280.bin",
            "NanoKVM-portrait-1080x1920.bin",
            "NanoKVM-portrait-1296x2304.bin",
            "NanoKVM-portrait-1440x2560.bin",
        ] {
            if case["MissingProfile"] != name {
                fs::write(root.path().join("usr/share/nanokvm/edid").join(name), &data).unwrap();
            }
        }
        for (path, key) in [
            ("etc/kvm/hw", "Board"),
            ("etc/kvm/hdmi_version", "Chip"),
            ("etc/kvm/monitor_resolution", "Monitor"),
            ("etc/kvm/monitor_portrait_resolution", "PortraitResolution"),
            (
                "sys/module/cv181x_vi/parameters/yuv_bypass_aligned_stride",
                "Stride",
            ),
        ] {
            fs::write(root.path().join(path), case[key].as_str().unwrap()).unwrap();
        }
        for (path, key, data) in [
            ("etc/kvm/monitor_portrait", "Portrait", "1\n"),
            ("etc/kvm/monitor_power_cycle_pending", "Pending", ""),
            ("boot/usb.pointer_windows", "Pointer", ""),
        ] {
            if case[key] == true {
                fs::write(root.path().join(path), data).unwrap();
            }
        }
        fs::write(
            root.path().join("etc/kvm/usb_container_id"),
            "2ca7b40c-7bd1-4f25-b573-a13a975ddc07\n",
        )
        .unwrap();
        fs::write(
            root.path()
                .join("sys/kernel/config/usb_gadget/g0/os_desc/container_id"),
            "",
        )
        .unwrap();
        fs::write(
            root.path()
                .join("proc/device-tree/reserved-memory/ion/size"),
            ((case["IonMiB"].as_u64().unwrap() as u32) * 1024 * 1024).to_be_bytes(),
        )
        .unwrap();
        let blocked = case["BlockedSetting"].as_str().unwrap();
        if !blocked.is_empty() {
            fs::create_dir(root.path().join("kvmapp/kvm").join(blocked)).unwrap();
        }
        fs::write(root.path().join("web/index.html"), "screen fixture").unwrap();
        fs::write(
            root.path().join("etc/kvm/server.yaml"),
            format!(
                "authentication: {}\nproto: http\n",
                if auth { "enable" } else { "disable" }
            ),
        )
        .unwrap();
        let fake = Arc::new(Fake {
            root: root.path().to_owned(),
            calls: Mutex::new(vec![]),
            status: Mutex::new(VideoStatus {
                gop_mode: case["ActiveMode"].as_u64().unwrap() as u8,
                mjpeg_chroma: 422,
                chroma_fallback: case["Fallback"].as_str().unwrap().into(),
            }),
            fail_chroma: case["FailChroma"] == true,
            fail_profile: case["FailProfile"] == true,
            fail_gop: AtomicBool::new(false),
            block_next: AtomicBool::new(false),
            entered: AtomicBool::new(false),
            release: (Mutex::new(false), Condvar::new()),
        });
        let runtime = Runtime::load_with_backends(root.path(), fake.clone(), fake.clone()).unwrap();
        let router = app(runtime.clone(), root.path().join("web"));
        Self {
            root,
            fake,
            runtime,
            router,
        }
    }
    fn plain(auth: bool) -> Self {
        Self::new(&oracle()["cases"][0]["case"], auth)
    }
    fn token(&self, name: &str, role: &str) -> String {
        self.runtime
            .store
            .create(name, "fixture-password", role)
            .unwrap();
        let user = self.runtime.store.get(name).unwrap();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        crypto::sign(
            &Claims {
                username: name.into(),
                sub: name.into(),
                token_version: user.token_version,
                exp: now + 300,
                iat: Some(now),
                nbf: None,
            },
            &self.runtime.config.jwt.secret_key,
        )
        .unwrap()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.fake.unblock();
        self.runtime.shutdown();
    }
}
async fn request(
    router: &Router,
    method: &str,
    body: &str,
    ct: &str,
    query: &str,
    token: &str,
) -> (StatusCode, Value) {
    let mut builder = Request::builder()
        .method(method)
        .uri(format!("/api/vm/screen?{query}"))
        .header("content-type", ct);
    if !token.is_empty() {
        builder = builder.header("authorization", format!("Bearer {token}"));
    }
    let mut request = builder.body(Body::from(body.to_owned())).unwrap();
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:43000".parse::<SocketAddr>().unwrap(),
    ));
    let response = router.clone().oneshot(request).await.unwrap();
    let status = response.status();
    let bytes = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    (
        status,
        serde_json::from_slice(&bytes).unwrap_or(Value::Null),
    )
}
async fn post(router: &Router, body: &str) -> Value {
    request(router, "POST", body, "application/json", "", "")
        .await
        .1
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
fn snapshot(runtime: &Runtime) -> Value {
    let s = runtime.screen.snapshot().unwrap();
    json!({"Width":s.width,"Height":s.height,"FPS":s.fps,"Quality":s.quality,"BitRate":s.bit_rate,"GOP":s.gop,"GOPMode":s.gop_mode,"MjpegChroma":s.mjpeg_chroma})
}
#[tokio::test]
async fn complete_immutable_go_screen_api_profile_file_and_native_effect_cases() {
    for row in oracle()["cases"].as_array().unwrap() {
        let c = &row["case"];
        let f = Fixture::new(c, false);
        let response = request(
            &f.router,
            c["Method"].as_str().unwrap(),
            c["Body"].as_str().unwrap(),
            c["ContentType"].as_str().unwrap(),
            c["Query"].as_str().unwrap(),
            "",
        )
        .await;
        assert_eq!(
            response.0.as_u16(),
            row["status"].as_u64().unwrap() as u16,
            "{}",
            c["Name"]
        );
        assert_eq!(response.1, row["response"], "{}", c["Name"]);
        assert_eq!(
            json!(*f.fake.calls.lock().unwrap()),
            row["calls"],
            "{}",
            c["Name"]
        );
        assert_eq!(snapshot(&f.runtime), row["screen"], "{}", c["Name"]);
        let mut saved = serde_json::Map::new();
        for path in row["saved"].as_object().unwrap().keys() {
            saved.insert(
                path.clone(),
                fs::read_to_string(f.root.path().join(path.trim_start_matches('/')))
                    .map_or(Value::Null, Value::String),
            );
        }
        assert_eq!(Value::Object(saved), row["saved"], "{}", c["Name"]);
        assert!(fs::read_dir(f.root.path().join("run")).unwrap().all(|p| !p
            .unwrap()
            .file_name()
            .to_string_lossy()
            .starts_with("nanokvm-pointer-")));
    }
}
#[tokio::test]
async fn screen_roles_and_input_owner_are_independent() {
    let f = Fixture::plain(true);
    let user = f.token("operator", "user");
    let viewer = f.token("spectator", "user");
    let admin = f.token("administrator", "admin");
    f.runtime
        .input
        .acquire_external("other-input-owner".into(), || {})
        .unwrap();
    assert!(!f.runtime.input.allows_http(""));
    assert_eq!(
        request(&f.router, "GET", "", "", "", "").await.0,
        StatusCode::UNAUTHORIZED
    );
    for token in [&user, &viewer] {
        assert_eq!(
            request(&f.router, "GET", "", "", "", token).await.1["code"],
            0
        );
        assert_eq!(
            request(
                &f.router,
                "POST",
                "{\"Type\":\"fps\",\"Value\":60}",
                "application/json",
                "",
                token
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
    }
    assert_eq!(
        request(
            &f.router,
            "POST",
            "{\"Type\":\"fps\",\"Value\":60}",
            "application/json",
            "",
            &admin
        )
        .await
        .1["code"],
        0
    );
    assert!(f.fake.calls.lock().unwrap().is_empty());
}
#[tokio::test]
async fn missing_native_backend_does_not_fabricate_status_or_acknowledge_gop() {
    let f = Fixture::plain(false);
    let runtime = Runtime::load(f.root.path()).unwrap();
    let router = app(runtime.clone(), f.root.path().join("web"));
    assert_eq!(request(&router, "GET", "", "", "", "").await.1["code"], -2);
    assert_eq!(
        post(&router, "{\"Type\":\"gop\",\"Value\":90}").await["code"],
        -2
    );
    assert_eq!(runtime.screen.snapshot().unwrap().gop, 30);
    assert_eq!(
        post(&router, "{\"Type\":\"mjpeg_chroma\",\"Value\":420}").await["code"],
        -4
    );
    assert!(!f.root.path().join("kvmapp/kvm/mjpeg_chroma").exists());
    assert_eq!(
        post(&router, "{\"Type\":\"monitor\",\"Value\":720}").await["code"],
        -4
    );
    assert_eq!(
        fs::read_to_string(f.root.path().join("etc/kvm/monitor_resolution")).unwrap(),
        "0"
    );
    runtime.shutdown();
}
#[tokio::test]
async fn chroma_rollback_serializes_publication_and_cancels_waiting_requests() {
    let mut c = oracle()["cases"][0]["case"].clone();
    c["BlockedSetting"] = "mjpeg_chroma".into();
    let f = Fixture::new(&c, false);
    f.fake.block_next.store(true, Ordering::Release);
    let r = f.router.clone();
    let first =
        tokio::spawn(async move { post(&r, "{\"Type\":\"mjpeg_chroma\",\"Value\":420}").await });
    until(|| f.fake.entered.load(Ordering::Acquire)).await;
    assert_eq!(f.runtime.screen.snapshot().unwrap().mjpeg_chroma, 422);
    let r = f.router.clone();
    let waiting = tokio::spawn(async move { post(&r, "{\"Type\":\"fps\",\"Value\":60}").await });
    until(|| f.runtime.jobs.available_permits() == 2).await;
    waiting.abort();
    let _ = waiting.await;
    until(|| f.runtime.jobs.available_permits() == 3).await;
    assert!(!f.root.path().join("kvmapp/kvm/fps").exists());
    f.fake.unblock();
    assert_eq!(first.await.unwrap()["code"], -2);
    assert_eq!(
        *f.fake.calls.lock().unwrap(),
        [
            json!({"kind":"chroma","value":420}),
            json!({"kind":"chroma","value":422})
        ]
    );
    assert_eq!(f.fake.status.lock().unwrap().mjpeg_chroma, 422);
    assert_eq!(f.runtime.screen.snapshot().unwrap().mjpeg_chroma, 422);
}
#[tokio::test]
async fn usb_pointer_and_monitor_share_the_whole_profile_and_persist_lock() {
    let f = Fixture::plain(false);
    f.fake.block_next.store(true, Ordering::Release);
    let r = f.router.clone();
    let first = tokio::spawn(async move { post(&r, "{\"Type\":\"monitor\",\"Value\":720}").await });
    until(|| f.fake.entered.load(Ordering::Acquire)).await;
    let runtime = f.runtime.clone();
    let second =
        tokio::task::spawn_blocking(move || runtime.monitor.apply_pointer(&runtime.root, false));
    tokio::time::sleep(Duration::from_millis(30)).await;
    assert_eq!(f.fake.calls.lock().unwrap().len(), 1);
    assert_eq!(
        fs::read_to_string(f.root.path().join("etc/kvm/monitor_resolution")).unwrap(),
        "0"
    );
    f.fake.unblock();
    assert_eq!(first.await.unwrap()["code"], 0);
    second.await.unwrap().unwrap();
    let calls = f.fake.calls.lock().unwrap();
    assert_eq!(calls.len(), 2);
    assert_eq!(
        calls[0]["path"],
        "/usr/share/nanokvm/edid/NanoKVM-monitor-720.bin"
    );
    assert_eq!(calls[1]["path"], calls[0]["path"]);
}
#[tokio::test]
async fn durable_modes_confined_legacy_links_and_marker_unlink_are_retained() {
    let f = Fixture::plain(false);
    let root = f.root.path();
    fs::write(root.join("etc/kvm/fps"), "50").unwrap();
    fs::set_permissions(root.join("etc/kvm/fps"), fs::Permissions::from_mode(0o640)).unwrap();
    symlink("/etc/kvm/fps", root.join("kvmapp/kvm/fps")).unwrap();
    assert_eq!(
        post(&f.router, "{\"Type\":\"fps\",\"Value\":75}").await["code"],
        0
    );
    assert_eq!(fs::read_to_string(root.join("etc/kvm/fps")).unwrap(), "75");
    assert_eq!(
        fs::metadata(root.join("etc/kvm/fps"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o640
    );
    assert!(fs::symlink_metadata(root.join("kvmapp/kvm/fps"))
        .unwrap()
        .file_type()
        .is_symlink());
    symlink("/etc/kvm/fps", root.join("etc/kvm/monitor_portrait")).unwrap();
    assert_eq!(
        post(&f.router, "{\"Type\":\"portrait\",\"Value\":1}").await["code"],
        0
    );
    assert!(!fs::symlink_metadata(root.join("etc/kvm/monitor_portrait"))
        .unwrap()
        .file_type()
        .is_symlink());
    assert_eq!(
        fs::metadata(root.join("etc/kvm/monitor_portrait"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    assert_eq!(fs::read_to_string(root.join("etc/kvm/fps")).unwrap(), "75");
    symlink(
        "/etc/kvm/fps",
        root.join("etc/kvm/monitor_power_cycle_pending"),
    )
    .unwrap();
    assert_eq!(
        post(
            &f.router,
            "{\"Type\":\"monitor_power_cycle_ack\",\"confirmPowerCycle\":true}"
        )
        .await["code"],
        0
    );
    assert_eq!(fs::read_to_string(root.join("etc/kvm/fps")).unwrap(), "75");
    assert!(!root.join("etc/kvm/monitor_power_cycle_pending").exists());
    fs::remove_file(root.join("kvmapp/kvm/fps")).unwrap();
    symlink("../../../outside", root.join("kvmapp/kvm/fps")).unwrap();
    assert_eq!(
        post(&f.router, "{\"Type\":\"fps\",\"Value\":60}").await["code"],
        -2
    );
    assert_eq!(f.runtime.screen.snapshot().unwrap().fps, 75);
}
#[tokio::test]
async fn primitive_confirmation_nulls_status_failures_and_input_rate_are_explicit() {
    let f = Fixture::plain(false);
    assert_eq!(post(&f.router,"{\"Type\":\"monitor_power_cycle_ack\",\"confirmPowerCycle\":true,\"CONFIRMPOWERCYCLE\":null}").await["code"],0);
    f.fake.fail_gop.store(true, Ordering::Release);
    assert_eq!(
        post(&f.router, "{\"Type\":\"gop\",\"Value\":100}").await["code"],
        -2
    );
    assert_eq!(f.runtime.screen.snapshot().unwrap().gop, 30);
    fs::write(f.root.path().join("run/nanokvm/width"), "2560").unwrap();
    fs::write(f.root.path().join("run/nanokvm/height"), "1440").unwrap();
    assert_eq!(
        post(&f.router, "{\"Type\":\"fps\",\"Value\":120}").await["code"],
        0
    );
    let get = request(&f.router, "GET", "", "", "", "").await.1;
    assert_eq!(get["data"]["fps"], 120);
    assert_eq!(get["data"]["effectiveFps"], 50);
    fs::remove_dir_all(f.root.path().join("kvmapp/kvm")).unwrap();
    assert_eq!(
        post(&f.router, "{\"Type\":\"fps\",\"Value\":60}").await["code"],
        -2
    );
    assert!(!f.root.path().join("kvmapp/kvm").exists());
}

#[tokio::test]
async fn cancelled_screen_profile_waiting_for_usb_never_applies_or_saves_it() {
    let f = Fixture::plain(false);
    f.fake.block_next.store(true, Ordering::Release);
    let runtime = f.runtime.clone();
    let usb =
        tokio::task::spawn_blocking(move || runtime.monitor.apply_pointer(&runtime.root, false));
    until(|| f.fake.entered.load(Ordering::Acquire)).await;
    let r = f.router.clone();
    let waiting =
        tokio::spawn(async move { post(&r, "{\"Type\":\"monitor\",\"Value\":720}").await });
    until(|| f.runtime.jobs.available_permits() == 3).await;
    waiting.abort();
    let _ = waiting.await;
    until(|| f.runtime.jobs.available_permits() == 4).await;
    assert_eq!(f.fake.calls.lock().unwrap().len(), 1);
    assert_eq!(
        fs::read_to_string(f.root.path().join("etc/kvm/monitor_resolution")).unwrap(),
        "0"
    );
    f.fake.unblock();
    usb.await.unwrap().unwrap();
    assert_eq!(f.fake.calls.lock().unwrap().len(), 1);
}
#[tokio::test]
async fn profile_save_failures_report_changed_hardware_without_faking_saved_state() {
    let f = Fixture::plain(false);
    let saved = f.root.path().join("etc/kvm/monitor_resolution");
    fs::remove_file(&saved).unwrap();
    fs::create_dir(&saved).unwrap();
    let response = post(&f.router, "{\"Type\":\"monitor\",\"Value\":720}").await;
    assert_eq!(response["code"], -4);
    assert!(response["msg"]
        .as_str()
        .unwrap()
        .starts_with("monitor changed, but saving its setting failed:"));
    assert!(saved.is_dir());
    assert_eq!(f.fake.calls.lock().unwrap().len(), 1);
    let saved = f.root.path().join("etc/kvm/monitor_portrait");
    fs::create_dir(&saved).unwrap();
    let response = post(&f.router, "{\"Type\":\"portrait\",\"Value\":1}").await;
    assert_eq!(response["code"], -4);
    assert!(response["msg"]
        .as_str()
        .unwrap()
        .starts_with("monitor changed, but saving portrait setting failed:"));
    assert!(saved.is_dir());
    assert_eq!(f.fake.calls.lock().unwrap().len(), 2);
    assert!(!f.runtime.monitor.status(&f.runtime.root).unwrap().portrait);
}
