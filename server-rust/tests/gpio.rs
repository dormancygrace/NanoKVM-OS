use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::{Request, StatusCode},
    Router,
};
use nanokvm_server::{
    app,
    gpio::{Backend, Controller, Line, Native},
    gpio_monitor::{Monitor, Status},
    hardware::Hardware,
    systemops::{Action, Executor},
    Error, Runtime,
};
use serde_json::{json, Value};
use std::{
    fs,
    net::SocketAddr,
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};
use tower::ServiceExt;
#[derive(Default)]
struct Fake {
    events: Mutex<Vec<String>>,
    power_high: AtomicBool,
    hdd_high: AtomicBool,
    fail_open: AtomicBool,
    fail_hdd_open: AtomicBool,
    fail_read: AtomicBool,
    fail_assert: AtomicBool,
    fail_release: AtomicBool,
    fail_close: AtomicBool,
    opens: AtomicUsize,
}
impl Fake {
    fn events(&self) -> Vec<String> {
        self.events.lock().unwrap().clone()
    }
    fn event(&self, event: String) {
        self.events.lock().unwrap().push(event);
    }
}
struct Pin {
    fake: Arc<Fake>,
    name: String,
    closed: bool,
}
impl Line for Pin {
    fn set(&mut self, active: bool) -> Result<(), Error> {
        self.fake.event(format!("{}:{active}", self.name));
        if (active && self.fake.fail_assert.load(Ordering::Acquire))
            || (!active && self.fake.fail_release.load(Ordering::Acquire))
        {
            Err(if active {
                "assert failed"
            } else {
                "release failed"
            }
            .into())
        } else {
            Ok(())
        }
    }
    fn get(&mut self) -> Result<bool, Error> {
        if self.fake.fail_read.load(Ordering::Acquire) {
            return Err("read failed".into());
        }
        Ok(if self.name == "hdd" {
            self.fake.hdd_high.load(Ordering::Acquire)
        } else {
            self.fake.power_high.load(Ordering::Acquire)
        })
    }
    fn close(&mut self) -> Result<(), Error> {
        assert!(!self.closed);
        self.closed = true;
        self.fake.event(format!("{}:close", self.name));
        if self.fake.fail_close.load(Ordering::Acquire) {
            Err("close failed".into())
        } else {
            Ok(())
        }
    }
}
struct FakeBackend(Arc<Fake>);
impl Backend for FakeBackend {
    fn open(&self, device: &str, output: bool) -> Result<Box<dyn Line>, Error> {
        self.0.opens.fetch_add(1, Ordering::AcqRel);
        self.0.event(format!("open:{device}:{output}"));
        if self.0.fail_open.load(Ordering::Acquire)
            || (device == "hdd" && self.0.fail_hdd_open.load(Ordering::Acquire))
        {
            return Err("open failed".into());
        }
        Ok(Box::new(Pin {
            fake: self.0.clone(),
            name: device.into(),
            closed: false,
        }))
    }
}
fn backend(fake: &Arc<Fake>) -> Arc<dyn Backend> {
    Arc::new(FakeBackend(fake.clone()))
}
fn hardware() -> Hardware {
    Hardware {
        version: "PCIE",
        power: "power".into(),
        reset: "reset".into(),
        power_led: "power".into(),
        hdd_led: "hdd".into(),
    }
}
fn until(condition: impl Fn() -> bool) {
    let end = Instant::now() + Duration::from_secs(3);
    while !condition() {
        assert!(Instant::now() < end, "fixture deadline");
        std::thread::sleep(Duration::from_millis(5));
    }
}
#[test]
fn atx_overlap_cancel_validation_and_cleanup_errors_are_bounded() {
    let fake = Arc::new(Fake::default());
    let controller = Arc::new(Controller::new(backend(&fake)));
    let cancelled = Arc::new(AtomicBool::new(false));
    let c = controller.clone();
    let cancel = cancelled.clone();
    let start = Instant::now();
    let task = std::thread::spawn(move || {
        c.pulse("power", Duration::from_secs(60), || {
            if cancel.load(Ordering::Acquire) {
                Err("context canceled".into())
            } else {
                Ok(())
            }
        })
    });
    until(|| fake.events().contains(&"power:true".into()));
    assert_eq!(
        controller
            .pulse("reset", Duration::from_millis(1), || Ok(()))
            .unwrap_err()
            .to_string(),
        "another ATX pulse is in progress"
    );
    cancelled.store(true, Ordering::Release);
    assert_eq!(
        task.join().unwrap().unwrap_err().to_string(),
        "context canceled"
    );
    assert!(start.elapsed() < Duration::from_secs(2));
    assert_eq!(
        fake.events(),
        [
            "open:power:true",
            "power:true",
            "power:false",
            "power:close"
        ]
    );
    let before = fake.opens.load(Ordering::Acquire);
    for duration in [Duration::ZERO, Duration::from_secs(61)] {
        assert!(controller.pulse("power", duration, || Ok(())).is_err());
    }
    assert!(controller
        .pulse("power", Duration::from_secs(1), || Err(
            "context canceled".into()
        ))
        .is_err());
    assert_eq!(fake.opens.load(Ordering::Acquire), before);
    fake.fail_assert.store(true, Ordering::Release);
    fake.fail_release.store(true, Ordering::Release);
    fake.fail_close.store(true, Ordering::Release);
    assert_eq!(
        controller
            .pulse("reset", Duration::from_millis(1), || Ok(()))
            .unwrap_err()
            .to_string(),
        "assert failed\nrelease failed\nclose failed"
    );
    fake.fail_assert.store(false, Ordering::Release);
    fake.fail_release.store(false, Ordering::Release);
    fake.fail_close.store(false, Ordering::Release);
    let start = Instant::now();
    controller
        .pulse("reset", Duration::from_millis(80), || Ok(()))
        .unwrap();
    assert!(start.elapsed() >= Duration::from_millis(80));
}
#[test]
fn panic_still_deasserts_and_closes_the_output() {
    let fake = Arc::new(Fake::default());
    let c = Controller::new(backend(&fake));
    let count = AtomicUsize::new(0);
    let panic = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        c.pulse("power", Duration::from_secs(1), || {
            if count.fetch_add(1, Ordering::AcqRel) == 2 {
                panic!("fixture");
            }
            Ok(())
        })
    }));
    assert!(panic.is_err());
    assert_eq!(
        fake.events(),
        [
            "open:power:true",
            "power:true",
            "power:false",
            "power:close"
        ]
    );
}
#[test]
fn native_sysfs_pins_existing_nodes_and_isolated_v2_never_requests_host_gpio() {
    let temp = tempfile::tempdir().unwrap();
    let value = temp.path().join("sys/class/gpio/gpio503/value");
    fs::create_dir_all(value.parent().unwrap()).unwrap();
    fs::write(&value, "1\n").unwrap();
    let native = Native::new(temp.path().into());
    let mut input = native.open("/sys/class/gpio/gpio503/value", false).unwrap();
    assert!(input.get().unwrap());
    fs::write(&value, "0\n").unwrap();
    assert!(!input.get().unwrap());
    fs::write(&value, "bad").unwrap();
    assert!(input.get().is_err());
    input.close().unwrap();
    let mut output = native.open("/sys/class/gpio/gpio503/value", true).unwrap();
    fs::remove_file(&value).unwrap();
    output.set(true).unwrap();
    output.set(false).unwrap();
    output.close().unwrap();
    assert!(!value.exists());
    assert!(native.open("/sys/class/gpio/gpio503/value", true).is_err());
    assert!(!value.exists());
    assert!(native.open("gpio-v2:3020000.gpio:23", true).is_err());
    assert!(native.open("/etc/passwd", true).is_err());
    assert!(native.open("gpio-v2::1", false).is_err());
}
#[test]
fn board_detection_profiles_legacy_newline_and_unknown_enhanced_pins() {
    let temp = tempfile::tempdir().unwrap();
    fs::create_dir_all(temp.path().join("etc/kvm")).unwrap();
    let hw = temp.path().join("etc/kvm/hw");
    assert_eq!(Hardware::detect(temp.path()).version, "Alpha");
    for (raw, version, reset, hdd) in [
        ("alpha\n", "Alpha", 507, true),
        ("beta\n", "Beta", 505, false),
        ("pcie\n", "PCIE", 505, false),
        (" beta \n", "Alpha", 507, true),
        ("b\ne\nt\na\n", "Beta", 505, false),
    ] {
        fs::write(&hw, raw).unwrap();
        let h = Hardware::detect(temp.path());
        assert_eq!(h.version, version);
        assert_eq!(h.reset, format!("/sys/class/gpio/gpio{reset}/value"));
        assert_eq!(!h.hdd_led.is_empty(), hdd);
    }
    fs::write(
        temp.path().join("etc/nanokvm-buildroot"),
        "flavour=enhanced\n",
    )
    .unwrap();
    for (raw, version, reset, hdd) in [
        ("alpha\n", "Alpha", 27, "gpio-v2:3020000.gpio:25"),
        ("beta\n", "Beta", 25, ""),
        ("pcie\n", "PCIE", 25, "gpio-v2:5021000.gpio:3"),
    ] {
        fs::write(&hw, raw).unwrap();
        let h = Hardware::detect(temp.path());
        assert_eq!(h.version, version);
        assert_eq!(h.reset, format!("gpio-v2:3020000.gpio:{reset}"));
        assert_eq!(h.power, "gpio-v2:3020000.gpio:23");
        assert_eq!(h.power_led, "gpio-v2:3020000.gpio:24");
        assert_eq!(h.hdd_led, hdd);
    }
    fs::write(&hw, "pcie").unwrap();
    let profile = temp.path().join("etc/kvm/board-profile");
    for raw in ["lite\n", "unknown", ""] {
        fs::write(&profile, raw).unwrap();
        let h = Hardware::detect(temp.path());
        assert_eq!(
            h.version,
            if raw.starts_with("lite") {
                "Beta"
            } else {
                "PCIE"
            }
        );
        assert_eq!(
            (&h.power, &h.reset, &h.power_led, &h.hdd_led),
            (
                &String::new(),
                &String::new(),
                &String::new(),
                &String::new()
            )
        );
    }
}
#[test]
fn monitor_active_low_hdd_hold_optional_input_and_stop_before_start() {
    let fake = Arc::new(Fake::default());
    fake.hdd_high.store(true, Ordering::Release);
    let monitor = Monitor::new(&hardware(), backend(&fake));
    assert_eq!(
        monitor.current(|| false).unwrap(),
        Status {
            power: true,
            hdd: false
        }
    );
    fake.hdd_high.store(false, Ordering::Release);
    until(|| monitor.current(|| false).unwrap().hdd);
    fake.hdd_high.store(true, Ordering::Release);
    std::thread::sleep(Duration::from_millis(100));
    assert!(monitor.current(|| false).unwrap().hdd);
    until(|| !monitor.current(|| false).unwrap().hdd);
    fake.power_high.store(true, Ordering::Release);
    until(|| !monitor.current(|| false).unwrap().power);
    monitor.stop();
    assert!(monitor.current(|| false).is_err());
    assert!(fake.events().contains(&"power:close".into()));
    assert!(fake.events().contains(&"hdd:close".into()));
    let mut hw = hardware();
    hw.hdd_led.clear();
    let fake = Arc::new(Fake::default());
    let monitor = Monitor::new(&hw, backend(&fake));
    assert!(!monitor.current(|| false).unwrap().hdd);
    assert_eq!(fake.opens.load(Ordering::Acquire), 1);
    monitor.stop();
    let fake = Arc::new(Fake::default());
    let unused = Monitor::new(&hw, backend(&fake));
    unused.stop();
    unused.stop();
    assert_eq!(fake.opens.load(Ordering::Acquire), 0);
}
#[test]
fn monitor_read_errors_close_reopen_and_clear_stale_status() {
    let fake = Arc::new(Fake::default());
    fake.fail_open.store(true, Ordering::Release);
    let monitor = Monitor::new(&hardware(), backend(&fake));
    assert_eq!(
        monitor.current(|| false).unwrap_err().to_string(),
        "open failed"
    );
    fake.fail_open.store(false, Ordering::Release);
    until(|| monitor.current(|| false).is_ok());
    fake.fail_read.store(true, Ordering::Release);
    fake.fail_close.store(true, Ordering::Release);
    until(|| monitor.current(|| false).is_err());
    let error = monitor.current(|| false).unwrap_err().to_string();
    assert!(error.contains("read failed"));
    assert!(error.contains("close failed"));
    fake.fail_read.store(false, Ordering::Release);
    fake.fail_close.store(false, Ordering::Release);
    until(|| monitor.current(|| false).is_ok());
    assert!(fake.opens.load(Ordering::Acquire) >= 5);
    monitor.stop();
}
#[derive(Default)]
struct Commands(Mutex<Vec<Action>>);
impl Executor for Commands {
    fn run(&self, action: Action, _: Duration) -> Result<(), Error> {
        self.0.lock().unwrap().push(action);
        Ok(())
    }
    fn stop(&self) {}
}
struct Fixture {
    root: tempfile::TempDir,
    runtime: Arc<Runtime>,
    router: Router,
    gpio: Arc<Fake>,
    commands: Arc<Commands>,
    token: String,
}
impl Fixture {
    fn new() -> Self {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir_all(root.path().join("etc/kvm")).unwrap();
        fs::write(
            root.path().join("etc/kvm/server.yaml"),
            "proto: http\njwt:\n  secretKey: gpio-test-secret\nsecurity:\n  trustedProxies: []\n",
        )
        .unwrap();
        let gpio = Arc::new(Fake::default());
        let commands = Arc::new(Commands::default());
        let runtime = Runtime::load_with_peripherals(
            root.path(),
            commands.clone(),
            Arc::new(nanokvm_server::monitor::Unavailable),
            backend(&gpio),
        )
        .unwrap();
        runtime
            .store
            .create("operator", "test-password", "user")
            .unwrap();
        let user = runtime.store.get("operator").unwrap();
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let token = nanokvm_server::crypto::sign(
            &nanokvm_server::crypto::Claims {
                username: "operator".into(),
                sub: "operator".into(),
                token_version: user.token_version,
                exp: now + 300,
                iat: Some(now),
                nbf: None,
            },
            &runtime.config.jwt.secret_key,
        )
        .unwrap();
        let router = app(runtime.clone(), root.path().join("web"));
        Self {
            root,
            runtime,
            router,
            gpio,
            commands,
            token,
        }
    }
    fn lease(&self) -> (u64, String) {
        let (id, status) = self.runtime.input.join().unwrap();
        let lease = status.borrow().lease.clone().unwrap();
        (id, lease)
    }
    async fn request(
        &self,
        method: &str,
        path: &str,
        body: &str,
        lease: &str,
    ) -> (StatusCode, Value) {
        request(&self.router, method, path, body, &self.token, lease).await
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.runtime.shutdown();
    }
}
async fn request(
    router: &Router,
    method: &str,
    path: &str,
    body: &str,
    token: &str,
    lease: &str,
) -> (StatusCode, Value) {
    let mut req = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json")
        .header("authorization", format!("Bearer {token}"))
        .header("x-nanokvm-input-lease", lease)
        .body(Body::from(body.to_owned()))
        .unwrap();
    req.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:10000".parse::<SocketAddr>().unwrap(),
    ));
    let rsp = router.clone().oneshot(req).await.unwrap();
    let status = rsp.status();
    let bytes = to_bytes(rsp.into_body(), 1 << 20).await.unwrap();
    (status, serde_json::from_slice(&bytes).unwrap())
}
#[tokio::test]
async fn atx_api_duration_default_hardware_role_ownership_and_reboot_gate() {
    let f = Fixture::new();
    let (_, lease) = f.lease();
    let (_, v) = f.request("GET", "/api/vm/hardware", "", " ").await;
    assert_eq!(
        v,
        json!({"code":0,"msg":"success","data":{"version":"Alpha"}})
    );
    let (_, v) = f
        .request("POST", "/api/vm/gpio", r#"{"Type":"power"}"#, "missing")
        .await;
    assert_eq!(v["code"], -4);
    assert_eq!(f.gpio.opens.load(Ordering::Acquire), 0);
    for (body,code,msg) in [(r#"{"Type":"power","Duration":60001}"#,-1,"ATX duration must not exceed 60000 ms"),(r#"{"Type":"other"}"#,-2,"invalid power event: other"),(r#"{"Type":"power","Duration":-1}"#,-1,"invalid arguments: json: cannot unmarshal number -1 into Go struct field SetGpioReq.Duration of type uint")]{let (_,v)=f.request("POST","/api/vm/gpio",body,&lease).await;assert_eq!(v["code"],code);assert_eq!(v["msg"],msg);}
    let start = Instant::now();
    let (_, v) = f
        .request("POST", "/api/vm/gpio", r#"{"Type":"power"}"#, &lease)
        .await;
    assert_eq!(v["code"], 0);
    assert!(start.elapsed() >= Duration::from_millis(800));
    assert!(f
        .gpio
        .events()
        .contains(&"/sys/class/gpio/gpio503/value:false".into()));
    let (_, v) = f.request("GET", "/api/vm/gpio", "", "").await;
    assert_eq!(v["data"], json!({"pwr":true,"hdd":true}));
    let (status, _) = f.request("POST", "/api/vm/system/reboot", "", "").await;
    assert_eq!(status, StatusCode::FORBIDDEN);
    assert!(f.commands.0.lock().unwrap().is_empty());
    assert!(f.root.path().is_dir());
}
#[tokio::test]
async fn long_atx_stops_on_lease_transfer_session_revoke_and_http_disconnect() {
    use tokio::io::AsyncWriteExt;
    let f = Fixture::new();
    let (owner, lease) = f.lease();
    let original_lease = lease.clone();
    let router = f.router.clone();
    let token = f.token.clone();
    let body = r#"{"Type":"power","Duration":60000}"#;
    let task = tokio::spawn(async move {
        request(&router, "POST", "/api/vm/gpio", body, &token, &lease).await
    });
    async_until(|| {
        f.gpio
            .events()
            .contains(&"/sys/class/gpio/gpio503/value:true".into())
    })
    .await;
    let (other, _) = f.runtime.input.join().unwrap();
    f.runtime.input.set_control(other, true).unwrap();
    let (_, v) = tokio::time::timeout(Duration::from_secs(2), task)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(v["code"], -3);
    f.runtime.input.set_control(owner, true).unwrap();
    let lease = original_lease;
    let router = f.router.clone();
    let token = f.token.clone();
    let lease2 = lease.clone();
    let before = f.gpio.opens.load(Ordering::Acquire);
    let task = tokio::spawn(async move {
        request(&router, "POST", "/api/vm/gpio", body, &token, &lease2).await
    });
    async_until(|| f.gpio.opens.load(Ordering::Acquire) > before).await;
    f.runtime.store.revoke("operator").unwrap();
    let (_, v) = tokio::time::timeout(Duration::from_secs(2), task)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(v["code"], -3);
    // Use a fresh valid token for a real TCP disconnect.
    let user = f.runtime.store.get("operator").unwrap();
    let mut claims =
        nanokvm_server::crypto::verify(&f.token, &f.runtime.config.jwt.secret_key, 0).unwrap();
    claims.token_version = user.token_version;
    let token = nanokvm_server::crypto::sign(&claims, &f.runtime.config.jwt.secret_key).unwrap();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let service = f.router.clone();
    let server = tokio::spawn(async move {
        axum::serve(
            listener,
            service.into_make_service_with_connect_info::<SocketAddr>(),
        )
        .await
        .unwrap()
    });
    let before = f
        .gpio
        .events()
        .iter()
        .filter(|e| e.ends_with(":close"))
        .count();
    let mut connection = tokio::net::TcpStream::connect(addr).await.unwrap();
    connection.write_all(format!("POST /api/vm/gpio HTTP/1.1\r\nHost: {addr}\r\nContent-Type: application/json\r\nAuthorization: Bearer {token}\r\nX-NanoKVM-Input-Lease: {lease}\r\nContent-Length: {}\r\n\r\n{body}",body.len()).as_bytes()).await.unwrap();
    async_until(|| {
        f.gpio
            .events()
            .iter()
            .filter(|e| e.ends_with(":true") && !e.starts_with("open:"))
            .count()
            >= 3
    })
    .await;
    drop(connection);
    async_until(|| {
        f.gpio
            .events()
            .iter()
            .filter(|e| e.ends_with(":close"))
            .count()
            > before
    })
    .await;
    assert_eq!(
        f.gpio.events().last().unwrap(),
        "/sys/class/gpio/gpio503/value:close"
    );
    server.abort();
}
async fn async_until(condition: impl Fn() -> bool) {
    let end = tokio::time::Instant::now() + Duration::from_secs(3);
    while !condition() {
        assert!(tokio::time::Instant::now() < end, "async fixture deadline");
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
}

#[test]
fn optional_hdd_open_failure_releases_power_and_retries() {
    let fake = Arc::new(Fake::default());
    fake.fail_hdd_open.store(true, Ordering::Release);
    let monitor = Monitor::new(&hardware(), backend(&fake));
    assert!(monitor.current(|| false).is_err());
    assert!(fake.events().contains(&"power:close".into()));
    fake.fail_hdd_open.store(false, Ordering::Release);
    until(|| monitor.current(|| false).is_ok());
    monitor.stop();
}
#[tokio::test]
async fn admin_reboot_response_precedes_one_deduplicated_action() {
    let f = Fixture::new();
    f.runtime
        .store
        .create("administrator", "test-password", "admin")
        .unwrap();
    let user = f.runtime.store.get("administrator").unwrap();
    let mut claims =
        nanokvm_server::crypto::verify(&f.token, &f.runtime.config.jwt.secret_key, 0).unwrap();
    claims.username = "administrator".into();
    claims.sub = claims.username.clone();
    claims.token_version = user.token_version;
    let token = nanokvm_server::crypto::sign(&claims, &f.runtime.config.jwt.secret_key).unwrap();
    for _ in 0..2 {
        let (status, value) =
            request(&f.router, "POST", "/api/vm/system/reboot", "", &token, "").await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(value["code"], 0);
    }
    assert!(f.commands.0.lock().unwrap().is_empty());
    async_until(|| !f.commands.0.lock().unwrap().is_empty()).await;
    assert_eq!(*f.commands.0.lock().unwrap(), [Action::Reboot]);
}

#[tokio::test]
async fn unavailable_native_reboot_fails_before_success_without_any_host_action() {
    let f = Fixture::new();
    f.runtime
        .store
        .create("administrator", "test-password", "admin")
        .unwrap();
    let user = f.runtime.store.get("administrator").unwrap();
    let mut claims =
        nanokvm_server::crypto::verify(&f.token, &f.runtime.config.jwt.secret_key, 0).unwrap();
    claims.username = "administrator".into();
    claims.sub = claims.username.clone();
    claims.token_version = user.token_version;
    let token = nanokvm_server::crypto::sign(&claims, &f.runtime.config.jwt.secret_key).unwrap();
    let runtime = Runtime::load(f.root.path()).unwrap();
    let router = app(runtime.clone(), f.root.path().join("web"));
    let (_, value) = request(&router, "POST", "/api/vm/system/reboot", "", &token, "").await;
    assert_eq!(value["code"], -1);
    assert_eq!(value["msg"], "operation failed");
    runtime.shutdown();
}
