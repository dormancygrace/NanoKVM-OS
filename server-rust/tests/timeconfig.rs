use base64::{engine::general_purpose::STANDARD, Engine};
use nanokvm_server::{
    time_sync,
    timeconfig::{Backend, Config, Manager, Native},
    Error,
};
use serde_json::{json, Value};
use std::{
    fs,
    net::UdpSocket,
    os::unix::fs::{symlink, PermissionsExt},
    path::Path,
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};
fn oracle() -> Value {
    serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/time-go-oracle.json"
    ))
    .unwrap()
}
fn zones(root: &Path) {
    for (name, data) in oracle()["zones"].as_object().unwrap() {
        let path = root.join("usr/share/zoneinfo").join(name);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, STANDARD.decode(data.as_str().unwrap()).unwrap()).unwrap();
    }
    fs::create_dir_all(root.join("etc/kvm")).unwrap();
}
fn config(value: &Value) -> Config {
    Config {
        servers: value["servers"].as_array().map(|items| {
            items
                .iter()
                .map(|item| item.as_str().unwrap().into())
                .collect()
        }),
        timezone: value["timezone"].as_str().unwrap().into(),
        format: value["format"].as_str().unwrap().into(),
    }
}
struct Fake {
    restarts: AtomicUsize,
    fail_restart: AtomicUsize,
    fail_path: Mutex<Option<String>>,
    fail_after_write: AtomicBool,
    sync_error: AtomicBool,
    chrony: AtomicBool,
}
impl Default for Fake {
    fn default() -> Self {
        Self {
            restarts: AtomicUsize::new(0),
            fail_restart: AtomicUsize::new(0),
            fail_path: Mutex::new(None),
            fail_after_write: AtomicBool::new(false),
            sync_error: AtomicBool::new(false),
            chrony: AtomicBool::new(false),
        }
    }
}
impl Backend for Fake {
    fn restart(&self, chrony: bool) -> Result<(), Error> {
        self.restarts.fetch_add(1, Ordering::AcqRel);
        self.chrony.store(chrony, Ordering::Release);
        if self
            .fail_restart
            .try_update(Ordering::AcqRel, Ordering::Acquire, |n| n.checked_sub(1))
            .is_ok()
        {
            return Err("daemon restart denied".into());
        }
        Ok(())
    }
    fn synchronized(&self, chrony: bool) -> Result<bool, Error> {
        self.chrony.store(chrony, Ordering::Release);
        if self.sync_error.load(Ordering::Acquire) {
            Err("daemon status unavailable".into())
        } else {
            Ok(true)
        }
    }
    fn now_millis(&self) -> i64 {
        1789128483000
    }
    fn atomic_preference(&self, path: &Path, data: &[u8], mode: u32) -> Result<(), Error> {
        let mut fail = self.fail_path.lock().unwrap();
        if fail.as_ref().is_some_and(|name| path.ends_with(name)) {
            fail.take();
            if self.fail_after_write.swap(false, Ordering::AcqRel) {
                nanokvm_server::store::atomic_write(path, data, mode)?;
            }
            return Err("preference write denied".into());
        }
        drop(fail);
        nanokvm_server::store::atomic_write(path, data, mode)
    }
}
fn fixture() -> (tempfile::TempDir, Arc<Fake>, Manager) {
    let root = tempfile::tempdir().unwrap();
    zones(root.path());
    let fake = Arc::new(Fake::default());
    let manager = Manager::new(root.path().into(), fake.clone());
    (root, fake, manager)
}
#[test]
fn actual_go_validation_read_defaults_zone_names_and_daemon_bytes() {
    let (_root, _fake, manager) = fixture();
    let oracle = oracle();
    assert_eq!(
        serde_json::to_value(manager.zones()).unwrap(),
        oracle["zoneNames"]
    );
    for case in oracle["validation"].as_array().unwrap() {
        let message = manager
            .validate(&config(&case["config"]))
            .err()
            .map(|e| e.to_string())
            .unwrap_or_default();
        assert_eq!(message, case["error"], "{}", case["name"]);
    }
    for case in oracle["reads"].as_array().unwrap() {
        let root = tempfile::tempdir().unwrap();
        zones(root.path());
        for (path, data) in case["files"].as_object().unwrap() {
            let path = root.path().join("etc").join(path);
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(path, data.as_str().unwrap()).unwrap();
        }
        let manager = Manager::new(root.path().into(), Arc::new(Fake::default()));
        let read = manager.read();
        assert_eq!(
            read.is_err(),
            case["error"].as_bool().unwrap(),
            "{}",
            case["name"]
        );
        if let Ok(cfg) = read {
            assert_eq!(
                serde_json::to_value(cfg).unwrap(),
                case["config"],
                "{}",
                case["name"]
            );
        }
    }
    for case in oracle["configs"].as_array().unwrap() {
        let old = STANDARD.decode(case["input"].as_str().unwrap()).unwrap();
        let servers = case["servers"]
            .as_array()
            .unwrap()
            .iter()
            .map(|v| v.as_str().unwrap().into())
            .collect::<Vec<_>>();
        assert_eq!(
            STANDARD.encode(nanokvm_server::timeconfig::server_config(
                &old,
                &servers,
                case["chrony"].as_bool().unwrap()
            )),
            case["output"],
            "{}",
            case["name"]
        );
    }
}
#[test]
fn localtime_symlink_replacement_preserves_tzdata_and_display_changes_do_not_restart() {
    let (root, fake, manager) = fixture();
    let ntp =
        b"server old.example iburst\nrestrict default noquery\ndriftfile /var/lib/ntp/drift\n";
    fs::write(root.path().join("etc/ntp.conf"), ntp).unwrap();
    symlink("/usr/share/zoneinfo/UTC", root.path().join("etc/localtime")).unwrap();
    let utc = fs::read(root.path().join("usr/share/zoneinfo/UTC")).unwrap();
    let mut cfg = manager.read().unwrap();
    cfg.timezone = "Asia/Jerusalem".into();
    manager.save(&cfg).unwrap();
    assert_eq!(fake.restarts.load(Ordering::Acquire), 0);
    assert!(fs::symlink_metadata(root.path().join("etc/localtime"))
        .unwrap()
        .is_file());
    assert_eq!(
        fs::read(root.path().join("usr/share/zoneinfo/UTC")).unwrap(),
        utc
    );
    let data = fs::read(root.path().join("etc/localtime")).unwrap();
    assert_eq!(data, manager.zone_data("Asia/Jerusalem").unwrap());
    let zone = jiff::tz::TimeZone::tzif("Asia/Jerusalem", &data).unwrap();
    for (timestamp, seconds) in [(1768478400, 7200), (1784116800, 10800)] {
        assert_eq!(
            zone.to_offset(jiff::Timestamp::from_second(timestamp).unwrap())
                .seconds(),
            seconds
        );
    }
    cfg.format = "12".into();
    manager.save(&cfg).unwrap();
    assert_eq!(fake.restarts.load(Ordering::Acquire), 0);
    assert_eq!(fs::read(root.path().join("etc/ntp.conf")).unwrap(), ntp);
    cfg.servers = Some(vec!["new.example".into(), "192.0.2.123".into()]);
    manager.save(&cfg).unwrap();
    assert_eq!(fake.restarts.load(Ordering::Acquire), 1);
    assert_eq!(manager.read().unwrap(), cfg);
    let actual = fs::read_to_string(root.path().join("etc/ntp.conf")).unwrap();
    assert!(!actual.contains("old.example"));
    assert!(actual.contains("driftfile"));
    assert!(actual.contains("restrict default noquery"));
    for name in ["localtime", "timezone", "kvm/date-time.json", "ntp.conf"] {
        assert_eq!(
            fs::metadata(root.path().join("etc").join(name))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o644
        );
    }
}
#[test]
fn restart_and_partial_file_failures_restore_links_bytes_modes_and_absence() {
    for mode in [0, 1, 2] {
        let fail_write = mode < 2;
        let (root, fake, manager) = fixture();
        let ntp = b"server old.example iburst\nrestrict default noquery\n";
        fs::write(root.path().join("etc/ntp.conf"), ntp).unwrap();
        fs::write(root.path().join("etc/timezone"), b"UTC\n").unwrap();
        fs::set_permissions(
            root.path().join("etc/timezone"),
            fs::Permissions::from_mode(0o640),
        )
        .unwrap();
        symlink("/usr/share/zoneinfo/UTC", root.path().join("etc/localtime")).unwrap();
        let utc = fs::read(root.path().join("usr/share/zoneinfo/UTC")).unwrap();
        if fail_write {
            fake.fail_after_write.store(mode == 1, Ordering::Release);
            *fake.fail_path.lock().unwrap() = Some("kvm/date-time.json".into());
        } else {
            fake.fail_restart.store(2, Ordering::Release);
        }
        let result = manager.save(&Config {
            servers: Some(vec!["new.example".into()]),
            timezone: "Asia/Jerusalem".into(),
            format: "12".into(),
        });
        assert!(result.is_err());
        assert_eq!(
            fs::read_link(root.path().join("etc/localtime")).unwrap(),
            Path::new("/usr/share/zoneinfo/UTC")
        );
        assert_eq!(
            fs::read(root.path().join("usr/share/zoneinfo/UTC")).unwrap(),
            utc
        );
        assert_eq!(
            fs::read(root.path().join("etc/timezone")).unwrap(),
            b"UTC\n"
        );
        assert_eq!(
            fs::metadata(root.path().join("etc/timezone"))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o640
        );
        assert_eq!(fs::read(root.path().join("etc/ntp.conf")).unwrap(), ntp);
        assert!(!root.path().join("etc/kvm/date-time.json").exists());
        assert_eq!(
            fake.restarts.load(Ordering::Acquire),
            if fail_write { 0 } else { 2 }
        );
        assert_eq!(manager.read().unwrap().format, "24");
        if !fail_write {
            assert_eq!(
                result
                    .unwrap_err()
                    .to_string()
                    .matches("daemon restart denied")
                    .count(),
                2
            );
        }
    }
    let (root, fake, manager) = fixture();
    fake.fail_restart.store(1, Ordering::Release);
    assert!(manager
        .save(&Config {
            servers: Some(vec!["new.example".into()]),
            timezone: "UTC".into(),
            format: "12".into()
        })
        .is_err());
    for name in [
        "etc/localtime",
        "etc/timezone",
        "etc/kvm/date-time.json",
        "etc/ntp.conf",
    ] {
        assert!(!root.path().join(name).exists());
    }
    assert_eq!(fake.restarts.load(Ordering::Acquire), 2);
}
#[test]
fn chrony_policy_uses_one_daemon_and_unavailable_sync_reports_false() {
    let (root, fake, manager) = fixture();
    fs::write(
        root.path().join("etc/chrony.conf"),
        "server old.example iburst\nport 0\ncmdport 0\nmakestep 1.0 3\n",
    )
    .unwrap();
    let mut cfg = manager.read().unwrap();
    cfg.servers = Some(vec!["new.example".into()]);
    manager.save(&cfg).unwrap();
    assert_eq!(fake.restarts.load(Ordering::Acquire), 1);
    assert!(fake.chrony.load(Ordering::Acquire));
    assert!(!root.path().join("etc/ntp.conf").exists());
    let text = fs::read_to_string(root.path().join("etc/chrony.conf")).unwrap();
    assert!(text.contains("cmdport 0"));
    assert!(text.contains("makestep 1.0 3"));
    assert!(!text.contains("old.example"));
    fake.sync_error.store(true, Ordering::Release);
    let state = manager.status().unwrap();
    assert_eq!(state["synchronized"], false);
    assert_eq!(state["daemon"], "chrony");
    assert_eq!(state["now"], 1789128483000i64);
}
#[test]
fn actual_go_local_chrony_and_ntp_status_parsers() {
    let oracle = oracle();
    for case in oracle["chrony"].as_array().unwrap() {
        let result = time_sync::parse_chrony(case["text"].as_str().unwrap());
        assert_eq!(
            result.is_err(),
            case["error"].as_bool().unwrap(),
            "{}",
            case["name"]
        );
        if let Ok(value) = result {
            assert_eq!(
                value,
                case["synchronized"].as_bool().unwrap(),
                "{}",
                case["name"]
            );
        }
    }
    for case in oracle["ntp"].as_array().unwrap() {
        let packet = STANDARD.decode(case["packet"].as_str().unwrap()).unwrap();
        let result = time_sync::parse_ntp(&packet, case["sequence"].as_u64().unwrap() as u16);
        assert_eq!(
            result.is_err(),
            case["error"].as_bool().unwrap(),
            "{}",
            case["name"]
        );
        if let Ok(value) = result {
            assert_eq!(
                value,
                case["synchronized"].as_bool().unwrap(),
                "{}",
                case["name"]
            );
        }
    }
}
#[test]
fn actual_udp_readvar_response_sequence_validation_and_timeout() {
    let socket = UdpSocket::bind("127.0.0.1:0").unwrap();
    let address = socket.local_addr().unwrap();
    socket
        .set_read_timeout(Some(Duration::from_secs(2)))
        .unwrap();
    let worker = std::thread::spawn(move || {
        for wrong in [false, true] {
            let mut request = [0; 32];
            let (n, peer) = socket.recv_from(&mut request).unwrap();
            assert_eq!(n, 12);
            assert_eq!(&request[..2], &[4 << 3 | 6, 2]);
            assert_eq!(&request[4..12], &[0; 8]);
            let mut reply = request[..12].to_vec();
            reply[1] |= 0x80;
            reply[4..6].copy_from_slice(&0x0615u16.to_be_bytes());
            if wrong {
                reply[3] = reply[3].wrapping_add(1);
            }
            socket.send_to(&reply, peer).unwrap();
        }
    });
    assert!(time_sync::ntp_at(address, Duration::from_secs(1)).unwrap());
    assert!(time_sync::ntp_at(address, Duration::from_secs(1))
        .unwrap_err()
        .to_string()
        .contains("invalid local NTP"));
    worker.join().unwrap();
    let silent = UdpSocket::bind("127.0.0.1:0").unwrap();
    let start = Instant::now();
    assert!(time_sync::ntp_at(silent.local_addr().unwrap(), Duration::from_millis(60)).is_err());
    assert!(start.elapsed() < Duration::from_secs(2));
}
#[test]
fn isolated_native_time_status_and_restart_refuse_host_resources() {
    let root = tempfile::tempdir().unwrap();
    let commands = Arc::new(nanokvm_server::systemops::Native::new(root.path().into()));
    let native = Native::new(root.path().into(), commands);
    for chrony in [true, false] {
        assert!(native
            .synchronized(chrony)
            .unwrap_err()
            .to_string()
            .contains("isolated root"));
        assert!(native.restart(chrony).is_err());
    }
}
#[tokio::test]
async fn time_api_session_read_admin_write_json_force_and_failures() {
    use axum::{
        body::{to_bytes, Body},
        extract::ConnectInfo,
        http::{Request, StatusCode},
    };
    use nanokvm_server::{app, crypto, Runtime};
    use tower::ServiceExt;
    let root = tempfile::tempdir().unwrap();
    zones(root.path());
    fs::write(
        root.path().join("etc/kvm/server.yaml"),
        "proto: http\njwt:\n  secretKey: time-test-secret\n",
    )
    .unwrap();
    let backend = Arc::new(Fake::default());
    let commands = Arc::new(nanokvm_server::systemops::Native::new(root.path().into()));
    let runtime = Runtime::load_with_all_backends(
        root.path(),
        commands,
        Arc::new(nanokvm_server::monitor::Unavailable),
        Arc::new(nanokvm_server::gpio::Native::new(root.path().into())),
        Arc::new(nanokvm_server::cpufreq::Native::new(root.path().into())),
        backend.clone(),
    )
    .unwrap();
    runtime
        .store
        .create("administrator", "test-password", "admin")
        .unwrap();
    runtime
        .store
        .create("viewer", "test-password", "user")
        .unwrap();
    let router = app(runtime.clone(), root.path().join("web"));
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let token = |name: &str| {
        let user = runtime.store.get(name).unwrap();
        crypto::sign(
            &crypto::Claims {
                username: name.into(),
                sub: name.into(),
                token_version: user.token_version,
                exp: now + 300,
                iat: Some(now),
                nbf: None,
            },
            &runtime.config.jwt.secret_key,
        )
        .unwrap()
    };
    for (name, method, content, body, status, code) in [
        ("viewer", "GET", "application/json", "", 200, 0),
        ("viewer", "POST", "application/json", "{}", 403, 0),
        (
            "administrator",
            "POST",
            "application/json",
            "{\"servers\":[1]}",
            200,
            -1,
        ),
        ("administrator", "POST", "application/json", "null", 200, -1),
        (
            "administrator",
            "POST",
            "application/x-www-form-urlencoded",
            "servers=x&timezone=UTC&format=24",
            200,
            -1,
        ),
        (
            "administrator",
            "POST",
            "text/plain",
            r#"{"SERVERS":["new.example"],"TIMEZONE":"UTC","FORMAT":"24"} true"#,
            200,
            0,
        ),
    ] {
        let mut request = Request::builder()
            .method(method)
            .uri("/api/vm/date-time")
            .header("content-type", content)
            .header("authorization", format!("Bearer {}", token(name)))
            .body(Body::from(body))
            .unwrap();
        request.extensions_mut().insert(ConnectInfo(
            "127.0.0.1:10000".parse::<std::net::SocketAddr>().unwrap(),
        ));
        let response = router.clone().oneshot(request).await.unwrap();
        assert_eq!(response.status(), StatusCode::from_u16(status).unwrap());
        let data = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        let value: Value = serde_json::from_slice(&data).unwrap();
        if status == 200 {
            assert_eq!(value["code"], code, "{body}");
            if code == 0 {
                assert_eq!(value["data"]["now"], 1789128483000i64);
                assert_eq!(value["data"]["synchronized"], true);
            }
        }
    }
    backend.fail_restart.store(1, Ordering::Release);
    let body = json!({"servers":["failure.example"],"timezone":"UTC","format":"12"});
    let mut request = Request::builder()
        .method("POST")
        .uri("/api/vm/date-time")
        .header(
            "authorization",
            format!("Bearer {}", token("administrator")),
        )
        .body(Body::from(body.to_string()))
        .unwrap();
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:10000".parse::<std::net::SocketAddr>().unwrap(),
    ));
    let response = router.clone().oneshot(request).await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let data = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    let value: Value = serde_json::from_slice(&data).unwrap();
    assert_eq!(value["code"], -2);
    assert_eq!(
        runtime.time.read().unwrap().servers,
        Some(vec!["new.example".into()])
    );
    fs::write(root.path().join("etc/kvm/date-time.json"), "{broken").unwrap();
    let mut request = Request::builder()
        .uri("/api/vm/date-time")
        .header("authorization", format!("Bearer {}", token("viewer")))
        .body(Body::empty())
        .unwrap();
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:10000".parse::<std::net::SocketAddr>().unwrap(),
    ));
    let response = router.oneshot(request).await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let data = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    let value: Value = serde_json::from_slice(&data).unwrap();
    assert_eq!(value["code"], -1);
    assert_eq!(value["msg"], "Cannot read date and time settings");
    runtime.shutdown();
}
