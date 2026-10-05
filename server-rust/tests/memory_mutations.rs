use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::Request,
};
use base64::{engine::general_purpose::STANDARD, Engine};
use nanokvm_server::{
    app, crypto,
    memory_command::{Action, Board, Output, SwapKind, VideoMode},
    memory_ops::Manager,
    memory_status,
    systemops::{self, Executor},
    update_lock, Error, Runtime,
};
use serde_json::Value;
use std::{
    fs,
    path::Path,
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};
use tower::ServiceExt;
#[derive(Default)]
struct Commands {
    calls: Mutex<Vec<Action>>,
    output: Mutex<Option<Output>>,
    block: AtomicBool,
    active: AtomicUsize,
    peak: AtomicUsize,
    cancelled: AtomicBool,
}
impl Executor for Commands {
    fn run(&self, _: systemops::Action, _: Duration) -> Result<(), Error> {
        Err("unrelated command".into())
    }
    fn memory(
        &self,
        action: Action,
        _timeout: Duration,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<Output, Error> {
        self.calls.lock().unwrap().push(action);
        let active = self.active.fetch_add(1, Ordering::AcqRel) + 1;
        self.peak.fetch_max(active, Ordering::AcqRel);
        while self.block.load(Ordering::Acquire) && !cancelled() {
            std::thread::sleep(Duration::from_millis(5));
        }
        self.active.fetch_sub(1, Ordering::AcqRel);
        if cancelled() {
            self.cancelled.store(true, Ordering::Release);
            return Err("cancelled".into());
        }
        Ok(self
            .output
            .lock()
            .unwrap()
            .clone()
            .unwrap_or_else(|| Output::new(true, b"")))
    }
}
fn write(root: &Path, path: &str, bytes: impl AsRef<[u8]>) {
    let file = root.join(path.trim_start_matches('/'));
    fs::create_dir_all(file.parent().unwrap()).unwrap();
    fs::write(file, bytes).unwrap();
}
fn config(root: &Path) {
    write(
        root,
        "/etc/kvm/server.yaml",
        b"proto: http\njwt:\n  secretKey: memory-mutation-secret\n",
    );
}
fn board(root: &Path) {
    write(
        root,
        "/sys/firmware/devicetree/base/sipeed,board-revision",
        b"pcie\0",
    );
    write(root, "/usr/lib/nanokvm/boot/pcie.sd", b"cma");
    write(root, "/usr/lib/nanokvm/boot/pcie-fixed.sd", b"fixed");
}
fn parse_call(call: &Value) -> Action {
    let a = call.as_array().unwrap();
    let text = |i: usize| a[i].as_str().unwrap();
    if text(0) == "sh" {
        Action::Configure {
            kind: if text(3) == "zram" {
                SwapKind::Zram
            } else {
                SwapKind::Sd
            },
            enabled: text(4) == "1",
            size: text(5).parse().unwrap(),
            recompress: a.get(6).map(|v| v == "1"),
        }
    } else {
        assert_eq!(text(0), "/usr/libexec/nanokvm/activate-kernel");
        Action::Video {
            board: Board::parse(text(1)).unwrap(),
            mode: VideoMode::parse(text(2)).unwrap(),
        }
    }
}
async fn request(
    router: &axum::Router,
    token: &str,
    method: &str,
    path: &str,
    content_type: &str,
    body: &str,
) -> (u16, Value) {
    let mut request = Request::builder()
        .method(method)
        .uri(path)
        .header("authorization", format!("Bearer {token}"))
        .header("content-type", content_type)
        .body(Body::from(body.to_owned()))
        .unwrap();
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:15000".parse::<std::net::SocketAddr>().unwrap(),
    ));
    let response = router.clone().oneshot(request).await.unwrap();
    let status = response.status().as_u16();
    let bytes = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    (status, serde_json::from_slice(&bytes).unwrap())
}
fn token(runtime: &Runtime, name: &str) -> String {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs();
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
}
#[tokio::test]
async fn actual_go_memory_mutation_binding_effects_and_error_output() {
    let root = tempfile::tempdir().unwrap();
    config(root.path());
    let commands = Arc::new(Commands::default());
    let runtime = Runtime::load_with_executor(root.path(), commands.clone()).unwrap();
    runtime
        .store
        .create("administrator", "test-password", "admin")
        .unwrap();
    let token = token(&runtime, "administrator");
    let router = app(runtime.clone(), root.path().join("web"));
    let oracle: Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/memory-command-go-oracle.json"
    ))
    .unwrap();
    for case in oracle["cases"].as_array().unwrap() {
        let fixture = &case["case"];
        let name = &fixture["Name"];
        for dir in ["sys", "usr", "proc", "kvmapp"] {
            let path = root.path().join(dir);
            if path.exists() {
                fs::remove_dir_all(&path).unwrap();
            }
        }
        for (path, value) in fixture["Files"].as_object().unwrap() {
            write(
                root.path(),
                path,
                STANDARD.decode(value.as_str().unwrap()).unwrap(),
            );
        }
        if fixture["ReadError"] != true {
            write(
                root.path(),
                "/proc/meminfo",
                "MemTotal: 1024 kB\nMemAvailable: 512 kB\n",
            );
        }
        let sd = if fixture["SDEnabled"] == true {
            format!(
                "/swapfile file {} 0 10\n",
                fixture["SDSize"].as_i64().unwrap() * 1024 - 4
            )
        } else {
            String::new()
        };
        write(
            root.path(),
            "/proc/swaps",
            format!("Filename Type Size Used Priority\n{sd}"),
        );
        *commands.output.lock().unwrap() = Some(Output::new(
            fixture["Fail"] != true,
            &STANDARD
                .decode(fixture["Output"].as_str().unwrap())
                .unwrap(),
        ));
        commands.calls.lock().unwrap().clear();
        let held = if fixture["LockError"].as_str().is_some_and(|s| !s.is_empty()) {
            Some(update_lock::Lock::acquire(root.path()).unwrap())
        } else {
            None
        };
        let (status, response) = request(
            &router,
            &token,
            fixture["Method"].as_str().unwrap(),
            fixture["Path"].as_str().unwrap(),
            fixture["ContentType"].as_str().unwrap(),
            fixture["Body"].as_str().unwrap(),
        )
        .await;
        assert_eq!(status, 200, "{name}");
        assert_eq!(
            response["code"], case["response"]["code"],
            "{name}: {response}"
        );
        assert_eq!(response["msg"], case["response"]["msg"], "{name}");
        if fixture["Path"] == "/api/vm/swap" {
            assert_eq!(response, case["response"], "{name}");
        } else if response["code"] == 0 {
            assert_eq!(
                response["data"],
                serde_json::to_value(memory_status::read_status(root.path()).unwrap()).unwrap(),
                "{name}"
            );
        }
        let expected: Vec<_> = case["calls"]
            .as_array()
            .unwrap()
            .iter()
            .map(parse_call)
            .collect();
        assert_eq!(*commands.calls.lock().unwrap(), expected, "{name}");
        drop(held);
    }
    runtime.shutdown();
}
#[tokio::test]
async fn memory_mutations_require_admin_and_native_isolation_refuses_helpers() {
    let root = tempfile::tempdir().unwrap();
    config(root.path());
    board(root.path());
    let runtime = Runtime::load(root.path()).unwrap();
    runtime
        .store
        .create("viewer", "test-password", "user")
        .unwrap();
    runtime
        .store
        .create("administrator", "test-password", "admin")
        .unwrap();
    let router = app(runtime.clone(), root.path().join("web"));
    let viewer = token(&runtime, "viewer");
    let admin = token(&runtime, "administrator");
    for (method, path, body) in [
        (
            "POST",
            "/api/vm/memory/swap",
            "{\"kind\":\"sd\",\"sizeMiB\":256}",
        ),
        ("POST", "/api/vm/memory/video", "{\"mode\":\"fixed\"}"),
        ("POST", "/api/vm/swap", "{\"size\":256}"),
        ("GET", "/api/vm/swap", ""),
    ] {
        assert_eq!(
            request(&router, &viewer, method, path, "application/json", body)
                .await
                .0,
            403
        );
        if method == "POST" {
            assert_eq!(
                request(&router, &admin, method, path, "application/json", body)
                    .await
                    .1["code"],
                -2
            );
        }
    }
    assert_eq!(
        fs::read(root.path().join("usr/lib/nanokvm/boot/pcie.sd")).unwrap(),
        b"cma"
    );
    assert!(!root.path().join("etc/kvm/video-memory-mode").exists());
    runtime.shutdown();
}
#[test]
fn update_lock_is_owned_nonblocking_regular_and_refuses_links() {
    use std::os::unix::fs::{symlink, PermissionsExt};
    let root = tempfile::tempdir().unwrap();
    let lock = update_lock::Lock::acquire(root.path()).unwrap();
    assert_eq!(
        fs::metadata(root.path().join("kvmapp/.os-update/lock"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    assert!(update_lock::Lock::acquire(root.path())
        .unwrap_err()
        .to_string()
        .contains("another update"));
    drop(lock);
    drop(update_lock::Lock::acquire(root.path()).unwrap());
    let path = root.path().join("kvmapp/.os-update/lock");
    fs::remove_file(&path).unwrap();
    symlink("/etc/passwd", &path).unwrap();
    assert!(update_lock::Lock::acquire(root.path()).is_err());
    fs::remove_file(&path).unwrap();
    let name = std::ffi::CString::new(path.as_os_str().as_encoded_bytes()).unwrap();
    assert_eq!(unsafe { libc::mkfifo(name.as_ptr(), 0o600) }, 0);
    let start = Instant::now();
    assert!(update_lock::Lock::acquire(root.path()).is_err());
    assert!(start.elapsed() < Duration::from_secs(1));
    fs::remove_file(path).unwrap();
    fs::remove_dir(root.path().join("kvmapp/.os-update")).unwrap();
    symlink("/tmp", root.path().join("kvmapp/.os-update")).unwrap();
    assert!(update_lock::Lock::acquire(root.path())
        .unwrap_err()
        .to_string()
        .contains("unsafe update state"));
}
fn wait_for(condition: impl Fn() -> bool) {
    let deadline = Instant::now() + Duration::from_secs(3);
    while !condition() {
        assert!(Instant::now() < deadline);
        std::thread::sleep(Duration::from_millis(5));
    }
}
#[test]
fn swap_video_mutex_cancels_waiters_and_update_lock_releases_after_failure() {
    let root = tempfile::tempdir().unwrap();
    board(root.path());
    let commands = Arc::new(Commands::default());
    commands.block.store(true, Ordering::Release);
    let manager = Manager::new(root.path().to_path_buf(), commands.clone());
    let action = Action::Configure {
        kind: SwapKind::Zram,
        enabled: true,
        size: 64,
        recompress: Some(true),
    };
    let first = manager.clone();
    let run = std::thread::spawn(move || first.configure(action, &|| false));
    wait_for(|| commands.active.load(Ordering::Acquire) == 1);
    let flag = Arc::new(AtomicBool::new(false));
    let cancelled = flag.clone();
    let video = manager.clone();
    let run_video = std::thread::spawn(move || {
        video.select_video(VideoMode::Fixed, &|| cancelled.load(Ordering::Acquire))
    });
    wait_for(|| root.path().join("kvmapp/.os-update/lock").exists());
    assert_eq!(commands.calls.lock().unwrap().len(), 1);
    flag.store(true, Ordering::Release);
    assert!(run_video.join().unwrap().is_err());
    drop(update_lock::Lock::acquire(root.path()).unwrap());
    commands.block.store(false, Ordering::Release);
    run.join().unwrap().unwrap();
    manager.select_video(VideoMode::Fixed, &|| false).unwrap();
    assert_eq!(commands.peak.load(Ordering::Acquire), 1);
    *commands.output.lock().unwrap() = Some(Output::new(false, b"bad image"));
    assert!(manager
        .select_video(VideoMode::Cma, &|| false)
        .unwrap_err()
        .to_string()
        .ends_with("bad image"));
    drop(update_lock::Lock::acquire(root.path()).unwrap());
}
#[tokio::test]
async fn actual_fifteen_second_maintenance_timer_policy_and_shutdown_cancellation() {
    let root = tempfile::tempdir().unwrap();
    config(root.path());
    let commands = Arc::new(Commands::default());
    commands.block.store(true, Ordering::Release);
    let runtime = Runtime::load_with_executor(root.path(), commands.clone()).unwrap();
    let manager = runtime.memory.clone();
    for (setting, swaps) in [
        (
            "ZRAM_RECOMPRESS=0\n",
            "Filename Type Size Used Priority\n/dev/zram0 partition 65532 0 100\n",
        ),
        (
            "ZRAM_RECOMPRESS=1\r\n",
            "Filename Type Size Used Priority\n/dev/zram0 partition 65532 0 100\n",
        ),
        (
            "ZRAM_RECOMPRESS=1\n",
            "Filename Type Size Used Priority\n/swapfile file 262140 0 10\n",
        ),
    ] {
        write(root.path(), "/etc/kvm/memory.conf", setting);
        write(root.path(), "/proc/swaps", swaps);
        manager.maintenance(&|| false).unwrap();
        assert!(commands.calls.lock().unwrap().is_empty());
    }
    write(root.path(), "/etc/kvm/memory.conf", "ZRAM_RECOMPRESS=1\n");
    write(
        root.path(),
        "/proc/swaps",
        "Filename Type Size Used Priority\n/dev/zram0 partition 65532 0 100\n",
    );
    let start = Instant::now();
    let deadline = start + Duration::from_secs(18);
    while commands.calls.lock().unwrap().is_empty() {
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    assert!(start.elapsed() >= Duration::from_secs(14));
    assert_eq!(*commands.calls.lock().unwrap(), vec![Action::Recompress]);
    runtime.shutdown();
    let deadline = Instant::now() + Duration::from_secs(2);
    while !commands.cancelled.load(Ordering::Acquire) {
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(commands.active.load(Ordering::Acquire), 0);
    assert_eq!(commands.peak.load(Ordering::Acquire), 1);
    manager.maintenance(&|| false).unwrap();
    assert_eq!(commands.calls.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn dropped_http_memory_operation_cancels_command_and_frees_execution_permit() {
    let root = tempfile::tempdir().unwrap();
    config(root.path());
    let commands = Arc::new(Commands::default());
    commands.block.store(true, Ordering::Release);
    let runtime = Runtime::load_with_executor(root.path(), commands.clone()).unwrap();
    runtime
        .store
        .create("administrator", "test-password", "admin")
        .unwrap();
    let token = token(&runtime, "administrator");
    let router = app(runtime.clone(), root.path().join("web"));
    let run = tokio::spawn(async move {
        request(
            &router,
            &token,
            "POST",
            "/api/vm/memory/swap",
            "application/json",
            "{\"kind\":\"sd\",\"sizeMiB\":256}",
        )
        .await
    });
    let deadline = Instant::now() + Duration::from_secs(2);
    while commands.active.load(Ordering::Acquire) != 1 {
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(runtime.jobs.available_permits(), 3);
    run.abort();
    assert!(run.await.unwrap_err().is_cancelled());
    let deadline = Instant::now() + Duration::from_secs(2);
    while !commands.cancelled.load(Ordering::Acquire) || runtime.jobs.available_permits() != 4 {
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    commands.block.store(false, Ordering::Release);
    runtime
        .memory
        .configure(
            Action::Configure {
                kind: SwapKind::Sd,
                enabled: false,
                size: 256,
                recompress: None,
            },
            &|| false,
        )
        .unwrap();
    assert_eq!(commands.peak.load(Ordering::Acquire), 1);
    runtime.shutdown();
}
