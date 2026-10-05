use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::Request,
};
use base64::{engine::general_purpose::STANDARD, Engine};
use nanokvm_server::{
    app, crypto,
    systemops::{Action, Executor},
    Error, Runtime,
};
use serde_json::{json, Value};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};
use tower::ServiceExt;
struct Commands {
    root: PathBuf,
    calls: Mutex<Vec<Action>>,
    passwords: AtomicUsize,
    fail: AtomicBool,
    password_fail: AtomicBool,
    block: AtomicBool,
    cancelled: AtomicBool,
}
impl Commands {
    fn new(root: &Path) -> Arc<Self> {
        Arc::new(Self {
            root: root.into(),
            calls: Mutex::new(vec![]),
            passwords: AtomicUsize::new(0),
            fail: AtomicBool::new(false),
            password_fail: AtomicBool::new(false),
            block: AtomicBool::new(false),
            cancelled: AtomicBool::new(false),
        })
    }
}
impl Executor for Commands {
    fn run(&self, action: Action, _: Duration) -> Result<(), Error> {
        self.calls.lock().unwrap().push(action);
        if self.fail.load(Ordering::Acquire) {
            return Err("fixture failure".into());
        }
        match action {
            Action::SshEnable => {
                let _ = fs::remove_file(self.root.join("etc/kvm/ssh_stop"));
            }
            Action::SshDisable => fs::write(self.root.join("etc/kvm/ssh_stop"), b"")?,
            _ => {}
        }
        Ok(())
    }
    fn password(
        &self,
        password: &str,
        _: Duration,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        assert!((8..=72).contains(&password.len()));
        assert!(!password.contains(['\r', '\n', '\0']));
        self.passwords.fetch_add(1, Ordering::AcqRel);
        while self.block.load(Ordering::Acquire) && !cancelled() {
            std::thread::sleep(Duration::from_millis(5));
        }
        if cancelled() {
            self.cancelled.store(true, Ordering::Release);
            return Err("cancelled".into());
        }
        if self.password_fail.load(Ordering::Acquire) {
            return Err("fixture password failure".into());
        }
        Ok(())
    }
}
fn write(root: &Path, path: &str, bytes: impl AsRef<[u8]>) {
    let path = root.join(path.trim_start_matches('/'));
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
fn config(root: &Path) {
    write(
        root,
        "/etc/kvm/server.yaml",
        "proto: http\njwt:\n  secretKey: services-fixture-secret\n",
    );
}
fn oracle() -> Value {
    serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/services-go-oracle.json"
    ))
    .unwrap()
}
fn body_named(name: &str) -> String {
    oracle()["cases"]
        .as_array()
        .unwrap()
        .iter()
        .find(|c| c["case"]["Name"] == name)
        .unwrap()["case"]["Body"]
        .as_str()
        .unwrap()
        .into()
}
fn cipher_named(name: &str) -> Value {
    serde_json::from_str::<Value>(&body_named(name)).unwrap()["password"].clone()
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
        "127.0.0.1:13000".parse::<std::net::SocketAddr>().unwrap(),
    ));
    let response = router.clone().oneshot(request).await.unwrap();
    let status = response.status().as_u16();
    let bytes = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    (status, serde_json::from_slice(&bytes).unwrap())
}
fn expected_action(call: &Value) -> Action {
    let args: Vec<_> = call
        .as_array()
        .unwrap()
        .iter()
        .map(|s| s.as_str().unwrap())
        .collect();
    if args[0] == "rc-service" {
        if args[2] == "start" {
            Action::AlpineMdnsStart
        } else {
            Action::AlpineMdnsStop
        }
    } else if args[2].starts_with("cp -f ") {
        Action::LegacyMdnsStart
    } else if args[2].starts_with("kill -9 ") {
        Action::LegacyMdnsStop
    } else if args[2].ends_with(" permanent_on") {
        Action::SshEnable
    } else {
        assert!(args[2].ends_with(" permanent_off"));
        Action::SshDisable
    }
}
#[tokio::test]
async fn actual_go_mdns_ssh_response_policy_effects_and_password_before_start() {
    let root = tempfile::tempdir().unwrap();
    config(root.path());
    let commands = Commands::new(root.path());
    let runtime = Runtime::load_with_executor(root.path(), commands.clone()).unwrap();
    runtime
        .store
        .create("administrator", "test-password", "admin")
        .unwrap();
    let token = token(&runtime, "administrator");
    let router = app(runtime.clone(), root.path().join("web"));
    for case in oracle()["cases"].as_array().unwrap() {
        let c = &case["case"];
        let name = &c["Name"];
        for path in [
            "etc/alpine-release",
            "etc/kvm/mdns_disabled",
            "etc/kvm/ssh_stop",
        ] {
            let path = root.path().join(path);
            if path.is_dir() {
                fs::remove_dir_all(path).unwrap();
            } else {
                let _ = fs::remove_file(path);
            }
        }
        for dir in ["etc/init.d", "run", "kvmapp"] {
            let path = root.path().join(dir);
            if path.exists() {
                fs::remove_dir_all(path).unwrap();
            }
        }
        for dir in c["Dirs"].as_array().unwrap() {
            fs::create_dir_all(root.path().join(dir.as_str().unwrap())).unwrap();
        }
        for (path, value) in c["Files"].as_object().unwrap() {
            write(
                root.path(),
                path,
                STANDARD.decode(value.as_str().unwrap()).unwrap(),
            );
        }
        commands.fail.store(c["Fail"] == true, Ordering::Release);
        commands
            .password_fail
            .store(c["PasswordFail"] == true, Ordering::Release);
        commands.calls.lock().unwrap().clear();
        commands.passwords.store(0, Ordering::Release);
        let (status, response) = request(
            &router,
            &token,
            c["Method"].as_str().unwrap(),
            c["Path"].as_str().unwrap(),
            c["ContentType"].as_str().unwrap(),
            c["Body"].as_str().unwrap(),
        )
        .await;
        assert_eq!(status, 200, "{name}");
        assert_eq!(response, case["response"], "{name}");
        let calls: Vec<_> = case["calls"]
            .as_array()
            .unwrap()
            .iter()
            .map(expected_action)
            .collect();
        assert_eq!(*commands.calls.lock().unwrap(), calls, "{name}");
        assert_eq!(
            commands.passwords.load(Ordering::Acquire) > 0,
            case["changed"] == true,
            "{name}"
        );
        if c["Path"].as_str().unwrap().contains("mdns") {
            for (path, value) in case["state"].as_object().unwrap() {
                if !path.contains("ssh_stop") {
                    let actual = fs::read(root.path().join(path))
                        .ok()
                        .map(|v| STANDARD.encode(v));
                    assert_eq!(actual, value.as_str().map(str::to_owned), "{name}: {path}");
                }
            }
        }
    }
    runtime.shutdown();
}
#[tokio::test]
async fn admin_gates_native_isolation_and_fixed_rooted_service_script() {
    let root = tempfile::tempdir().unwrap();
    config(root.path());
    write(root.path(), "/etc/alpine-release", "");
    let runtime = Runtime::load(root.path()).unwrap();
    runtime
        .store
        .create("administrator", "test-password", "admin")
        .unwrap();
    runtime
        .store
        .create("viewer", "test-password", "user")
        .unwrap();
    let router = app(runtime.clone(), root.path().join("web"));
    for (method, path) in [
        ("POST", "/api/vm/mdns/enable"),
        ("POST", "/api/vm/mdns/disable"),
        ("GET", "/api/vm/ssh"),
        ("POST", "/api/vm/ssh/enable"),
        ("POST", "/api/vm/ssh/disable"),
    ] {
        let body = body_named("ssh-enable-good");
        assert_eq!(
            request(
                &router,
                &token(&runtime, "viewer"),
                method,
                path,
                "application/json",
                &body
            )
            .await
            .0,
            403
        );
        if method == "POST" {
            assert_eq!(
                request(
                    &router,
                    &token(&runtime, "administrator"),
                    method,
                    path,
                    "application/json",
                    &body
                )
                .await
                .1["code"],
                -1
            );
        }
    }
    assert!(!root.path().join("etc/kvm/mdns_disabled").exists());
    assert!(!root.path().join("etc/kvm/ssh_stop").exists());
    runtime.shutdown();
}
#[tokio::test]
async fn owner_password_backend_failure_rolls_back_database_and_cancel_does_not_enable_ssh() {
    let root = tempfile::tempdir().unwrap();
    config(root.path());
    let commands = Commands::new(root.path());
    let runtime = Runtime::load_with_executor(root.path(), commands.clone()).unwrap();
    runtime
        .store
        .password("admin", "operator-password", || Ok(()))
        .unwrap();
    let router = app(runtime.clone(), root.path().join("web"));
    let before = fs::read(root.path().join("etc/kvm/pwd")).unwrap();
    commands.password_fail.store(true, Ordering::Release);
    let body=json!({"password":cipher_named("ssh-enable-max"),"currentPassword":cipher_named("ssh-enable-good")}).to_string();
    let response = request(
        &router,
        &token(&runtime, "admin"),
        "POST",
        "/api/auth/password",
        "application/json",
        &body,
    )
    .await;
    assert_eq!(response.1["code"], -5);
    assert_eq!(fs::read(root.path().join("etc/kvm/pwd")).unwrap(), before);
    assert_eq!(commands.passwords.load(Ordering::Acquire), 1);
    commands.password_fail.store(false, Ordering::Release);
    commands.passwords.store(0, Ordering::Release);
    commands.block.store(true, Ordering::Release);
    let auth = token(&runtime, "admin");
    let task = tokio::spawn(async move {
        request(
            &router,
            &auth,
            "POST",
            "/api/vm/ssh/enable",
            "application/json",
            &body_named("ssh-enable-good"),
        )
        .await
    });
    let deadline = Instant::now() + Duration::from_secs(2);
    while commands.passwords.load(Ordering::Acquire) == 0 {
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    task.abort();
    assert!(task.await.unwrap_err().is_cancelled());
    let deadline = Instant::now() + Duration::from_secs(2);
    while !commands.cancelled.load(Ordering::Acquire) || runtime.jobs.available_permits() != 4 {
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert!(commands.calls.lock().unwrap().is_empty());
    runtime.shutdown();
}
