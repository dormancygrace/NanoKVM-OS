use nanokvm_server::{
    cpufreq::{Backend, Manager, Native},
    Error,
};
use std::{
    collections::BTreeMap,
    fs,
    path::Path,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};
const POLICY: &str = "/sys/devices/system/cpu/cpufreq/policy0";
struct Fake {
    native: Native,
    files: Mutex<BTreeMap<String, Vec<u8>>>,
    writes: Mutex<Vec<(String, String)>>,
    mirror: AtomicBool,
    clamp: AtomicBool,
    fail: AtomicBool,
    delay: AtomicBool,
    preference_fail: AtomicBool,
    pending: Mutex<Option<(Instant, Vec<u8>)>>,
}
impl Fake {
    fn new(root: &Path) -> Self {
        let files = [
            ("scaling_driver", b"sg2002-cpufreq".as_slice()),
            ("cpuinfo_cur_freq", b"850000"),
            ("scaling_min_freq", b"850000"),
            ("scaling_max_freq", b"850000"),
        ]
        .into_iter()
        .map(|(name, value)| (format!("{POLICY}/{name}"), value.to_vec()))
        .collect();
        Self {
            native: Native::new(root.into()),
            files: Mutex::new(files),
            writes: Mutex::new(Vec::new()),
            mirror: AtomicBool::new(true),
            clamp: AtomicBool::new(false),
            fail: AtomicBool::new(false),
            delay: AtomicBool::new(false),
            preference_fail: AtomicBool::new(false),
            pending: Mutex::new(None),
        }
    }
    fn file(&self, path: &str, value: &str) {
        self.files
            .lock()
            .unwrap()
            .insert(path.into(), value.as_bytes().into());
    }
    fn thermal(&self, zone: &str, cooling: &str, state: &str) {
        self.file("/sys/class/thermal/thermal_zone0/type", zone);
        self.file("/sys/class/thermal/thermal_zone0/cdev0/type", cooling);
        self.file("/sys/class/thermal/thermal_zone0/cdev0/cur_state", state);
    }
}
impl Backend for Fake {
    fn atomic_preference(&self, path: &Path, data: &[u8], mode: u32) -> Result<(), Error> {
        if path
            .file_name()
            .is_some_and(|name| name == "nanokvm-cpufreq")
            && self.preference_fail.swap(false, Ordering::AcqRel)
        {
            return Err("injected runtime preference failure".into());
        }
        nanokvm_server::store::atomic_write(path, data, mode)
    }
    fn read(&self, path: &str) -> Result<Vec<u8>, Error> {
        let mut pending = self.pending.lock().unwrap();
        if pending
            .as_ref()
            .is_some_and(|(at, _)| Instant::now() >= *at)
        {
            let (_, value) = pending.take().unwrap();
            self.files
                .lock()
                .unwrap()
                .insert(format!("{POLICY}/scaling_max_freq"), value);
        }
        drop(pending);
        match self.files.lock().unwrap().get(path).cloned() {
            Some(value) => Ok(value),
            None => self.native.read(path),
        }
    }
    fn write(&self, path: &str, data: &[u8]) -> Result<(), Error> {
        self.writes
            .lock()
            .unwrap()
            .push((path.into(), String::from_utf8_lossy(data).into()));
        if self.fail.load(Ordering::Acquire) {
            return Err("sysfs denied".into());
        }
        if path.ends_with("/scaling_max_freq") {
            if self.clamp.load(Ordering::Acquire) {
                self.file(path, "850000");
                return Ok(());
            }
            if data == b"1000000" && self.delay.load(Ordering::Acquire) {
                *self.pending.lock().unwrap() =
                    Some((Instant::now() + Duration::from_millis(40), data.into()));
                return Ok(());
            }
        }
        if path.ends_with("/scaling_setspeed") {
            if self.pending.lock().unwrap().is_some() {
                return Err("setspeed before effective limit".into());
            }
            if self.mirror.load(Ordering::Acquire) && !self.clamp.load(Ordering::Acquire) {
                self.files
                    .lock()
                    .unwrap()
                    .insert(format!("{POLICY}/cpuinfo_cur_freq"), data.into());
            }
        }
        self.files.lock().unwrap().insert(path.into(), data.into());
        Ok(())
    }
    fn entries(&self, path: &str) -> Vec<String> {
        let prefix = format!("{path}/");
        self.files
            .lock()
            .unwrap()
            .keys()
            .filter_map(|key| key.strip_prefix(&prefix))
            .filter_map(|suffix| suffix.split('/').next())
            .map(str::to_owned)
            .collect::<std::collections::BTreeSet<_>>()
            .into_iter()
            .collect()
    }
}
fn fixture() -> (tempfile::TempDir, Arc<Fake>, Manager) {
    let root = tempfile::tempdir().unwrap();
    fs::create_dir_all(root.path().join("etc/kvm")).unwrap();
    fs::create_dir(root.path().join("run")).unwrap();
    let fake = Arc::new(Fake::new(root.path()));
    let manager = Manager::new(root.path().into(), fake.clone());
    (root, fake, manager)
}
#[test]
fn clock_readback_driver_capability_errors_and_boot_scoped_overclock() {
    let (root, fake, manager) = fixture();
    manager.apply(1000, true).unwrap();
    assert_eq!(manager.status().unwrap().running, 1000);
    assert_eq!(
        fs::read(root.path().join("etc/kvm/cpufreq")).unwrap(),
        b"1000\n"
    );
    fake.mirror.store(false, Ordering::Release);
    assert!(manager
        .apply(850, true)
        .unwrap_err()
        .to_string()
        .contains("CPU clock readback"));
    assert_eq!(manager.status().unwrap().target, 1000);
    fake.mirror.store(true, Ordering::Release);
    assert!(manager.apply(1100, true).is_err());
    fake.file(
        &format!("{POLICY}/scaling_available_frequencies"),
        "600000 850000 1000000 1050000 1075000 1100000 1125000 1150000",
    );
    for target in [1050, 1075, 1100, 1125, 1150] {
        manager.apply(target, true).unwrap();
        assert_eq!(manager.status().unwrap().running, target);
        assert_eq!(manager.status().unwrap().target, target);
        assert_eq!(
            fs::read(root.path().join("etc/kvm/cpufreq")).unwrap(),
            b"1000\n"
        );
    }
    fs::remove_file(root.path().join("run/nanokvm-cpufreq")).unwrap();
    manager.apply_saved().unwrap();
    assert_eq!(manager.status().unwrap().running, 1000);
    fake.file(&format!("{POLICY}/scaling_driver"), "unqualified");
    assert_eq!(
        manager.apply(850, true).unwrap_err().to_string(),
        "qualified CPU frequency driver is unavailable"
    );
}
#[test]
fn known_thermal_cooling_preserves_intended_target_and_effective_limit_waits() {
    let (_root, fake, manager) = fixture();
    fake.delay.store(true, Ordering::Release);
    let start = Instant::now();
    manager.apply(850, false).unwrap();
    assert!(start.elapsed() >= Duration::from_millis(40));
    fake.delay.store(false, Ordering::Release);
    fake.thermal("sg2002-cpu", "cpufreq-cpu0", "6");
    fake.clamp.store(true, Ordering::Release);
    fake.file(
        &format!("{POLICY}/scaling_available_frequencies"),
        "600000 850000 1000000 1125000",
    );
    fake.file(&format!("{POLICY}/cpuinfo_min_freq"), "600000");
    for target in [1000, 850, 1125] {
        manager.apply(target, true).unwrap();
        let status = manager.status().unwrap();
        assert!(status.throttled);
        assert_eq!(status.running, 850);
        assert_eq!(status.target, target);
    }
    assert!(fake
        .writes
        .lock()
        .unwrap()
        .iter()
        .any(|(name, value)| name.ends_with("scaling_min_freq") && value == "600000"));
    fake.thermal("sg2002-cpu", "cpufreq-cpu0", "0");
    fake.clamp.store(false, Ordering::Release);
    manager.apply(1000, false).unwrap();
    assert!(!manager.status().unwrap().throttled);
    assert_eq!(manager.status().unwrap().running, 1000);
    fake.thermal("other-zone", "cpufreq-cpu0", "1");
    assert!(!manager.status().unwrap().throttled);
    fake.thermal("sg2002-cpu", "other-cooling", "1");
    assert!(!manager.status().unwrap().throttled);
}
#[test]
fn preference_failure_does_not_change_boot_cap_or_clock_and_sysfs_errors_aggregate() {
    let (root, fake, manager) = fixture();
    manager.apply(1000, true).unwrap();
    let pref = root.path().join("etc/kvm/cpufreq");
    let runtime = root.path().join("run/nanokvm-cpufreq");
    fs::remove_file(&runtime).unwrap();
    fs::create_dir(&runtime).unwrap();
    assert!(manager.apply(850, true).is_err());
    assert_eq!(fs::read(&pref).unwrap(), b"1000\n");
    assert_eq!(manager.status().unwrap().running, 1000);
    fs::remove_dir(&runtime).unwrap();
    fake.fail.store(true, Ordering::Release);
    let error = manager.apply(850, true).unwrap_err().to_string();
    assert!(error.contains("sysfs denied"));
    assert!(error.contains("restore CPU clock"));
    assert_eq!(fs::read(&pref).unwrap(), b"1000\n");
}
#[test]
fn native_sysfs_missing_nodes_fail_without_creating_fake_hardware() {
    let root = tempfile::tempdir().unwrap();
    let path = root.path().join(POLICY.trim_start_matches('/'));
    fs::create_dir_all(&path).unwrap();
    let native = Native::new(root.path().into());
    assert!(native
        .write(&format!("{POLICY}/scaling_setspeed"), b"1000000")
        .is_err());
    assert!(!path.join("scaling_setspeed").exists());
}
#[test]
fn status_defaults_nil_options_and_raw_preferences_match_actual_go() {
    let cases: serde_json::Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/cpufreq-go-oracle.json"
    ))
    .unwrap();
    for case in cases.as_array().unwrap() {
        let root = tempfile::tempdir().unwrap();
        for (path, value) in case["files"].as_object().unwrap() {
            let path = root.path().join(path.trim_start_matches('/'));
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(path, value.as_str().unwrap()).unwrap();
        }
        let manager = Manager::new(
            root.path().into(),
            Arc::new(Native::new(root.path().into())),
        );
        assert_eq!(
            serde_json::to_value(manager.status().unwrap()).unwrap(),
            case["status"],
            "{}",
            case["name"]
        );
    }
}
#[tokio::test]
async fn cpu_admin_api_numeric_validation_readback_and_form_names() {
    use axum::{
        body::{to_bytes, Body},
        extract::ConnectInfo,
        http::{Request, StatusCode},
    };
    use nanokvm_server::{app, crypto, Runtime};
    use tower::ServiceExt;
    let root = tempfile::tempdir().unwrap();
    fs::create_dir_all(root.path().join("etc/kvm")).unwrap();
    fs::create_dir(root.path().join("run")).unwrap();
    fs::write(
        root.path().join("etc/kvm/server.yaml"),
        "proto: http\njwt:\n  secretKey: cpu-test-secret\n",
    )
    .unwrap();
    let fake = Arc::new(Fake::new(root.path()));
    let runtime = Runtime::load_with_hardware_backends(
        root.path(),
        Arc::new(nanokvm_server::systemops::Native::new(root.path().into())),
        Arc::new(nanokvm_server::monitor::Unavailable),
        Arc::new(nanokvm_server::gpio::Native::new(root.path().into())),
        fake.clone(),
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
    for (name, method, ctype, body, expected) in [
        ("viewer", "GET", "application/json", "", 403),
        (
            "viewer",
            "POST",
            "application/json",
            r#"{"target":1000}"#,
            403,
        ),
        (
            "administrator",
            "POST",
            "application/json",
            r#"{"target":850.0}"#,
            200,
        ),
        (
            "administrator",
            "POST",
            "application/json",
            r#"{"target":1200}"#,
            200,
        ),
        (
            "administrator",
            "POST",
            "application/x-www-form-urlencoded",
            "Target=%2B1000&Target=850",
            200,
        ),
        ("administrator", "GET", "application/json", "", 200),
    ] {
        let mut req = Request::builder()
            .method(method)
            .uri("/api/vm/cpu-frequency")
            .header("content-type", ctype)
            .header("authorization", format!("Bearer {}", token(name)))
            .body(Body::from(body))
            .unwrap();
        req.extensions_mut().insert(ConnectInfo(
            "127.0.0.1:10000".parse::<std::net::SocketAddr>().unwrap(),
        ));
        let rsp = router.clone().oneshot(req).await.unwrap();
        assert_eq!(rsp.status(), StatusCode::from_u16(expected).unwrap());
        let bytes = to_bytes(rsp.into_body(), 1 << 20).await.unwrap();
        let value: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        if name == "administrator" {
            if body.contains("850.0") || body.contains("1200") {
                assert_eq!(value["code"], -1);
            } else {
                assert_eq!(value["code"], 0);
            }
        }
    }
    assert_eq!(runtime.cpu.status().unwrap().running, 1000);
    runtime.shutdown();
}

#[test]
fn partial_preference_transaction_restores_bytes_modes_missing_files_and_clock() {
    use std::os::unix::fs::PermissionsExt;
    let (root, fake, manager) = fixture();
    manager.apply(1000, true).unwrap();
    let boot = root.path().join("etc/kvm/cpufreq");
    let run = root.path().join("run/nanokvm-cpufreq");
    fs::set_permissions(&boot, fs::Permissions::from_mode(0o640)).unwrap();
    fs::set_permissions(&run, fs::Permissions::from_mode(0o644)).unwrap();
    fake.preference_fail.store(true, Ordering::Release);
    assert!(manager
        .apply(850, true)
        .unwrap_err()
        .to_string()
        .contains("injected runtime preference"));
    for (path, mode) in [(&boot, 0o640), (&run, 0o644)] {
        assert_eq!(fs::read(path).unwrap(), b"1000\n");
        assert_eq!(
            fs::metadata(path).unwrap().permissions().mode() & 0o777,
            mode
        );
    }
    assert_eq!(manager.status().unwrap().running, 1000);
    fs::remove_file(&boot).unwrap();
    fs::remove_file(&run).unwrap();
    fake.preference_fail.store(true, Ordering::Release);
    assert!(manager.apply(850, true).is_err());
    assert!(!boot.exists());
    assert!(!run.exists());
    assert_eq!(manager.status().unwrap().running, 1000);
}
#[test]
fn unproven_qos_clamp_times_out_and_reports_failed_restore() {
    let (root, fake, manager) = fixture();
    fake.clamp.store(true, Ordering::Release);
    let start = Instant::now();
    let error = manager.apply(1000, true).unwrap_err().to_string();
    assert!(start.elapsed() >= Duration::from_secs(4));
    assert!(start.elapsed() < Duration::from_secs(7));
    assert!(error.contains("scaling_max_freq did not apply: 850000"));
    assert!(error.contains("restore CPU clock"));
    assert!(!root.path().join("etc/kvm/cpufreq").exists());
}
