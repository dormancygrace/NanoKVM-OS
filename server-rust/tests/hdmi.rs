use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::{Request, StatusCode},
    Router,
};
use nanokvm_server::{
    app,
    crypto::{self, Claims},
    monitor::Backend,
    systemops::{Action, Executor},
    Error, Runtime,
};
use serde_json::{json, Value};
use std::{
    fs,
    net::SocketAddr,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};
use tower::ServiceExt;

struct Fake {
    root: PathBuf,
    calls: Mutex<Vec<bool>>,
    markers: Mutex<Vec<bool>>,
    fail: AtomicBool,
}
impl Executor for Fake {
    fn run(&self, _: Action, _: Duration) -> Result<(), Error> {
        panic!("HDMI must not execute system commands")
    }
}
impl Backend for Fake {
    fn stop_audio(&self) -> Result<(), Error> {
        panic!("HDMI must not change audio")
    }
    fn apply_monitor_profile(&self, _: &Path) -> Result<(), Error> {
        panic!("HDMI must not program EDID")
    }
    fn set_hdmi(&self, enabled: bool) -> Result<(), Error> {
        self.markers
            .lock()
            .unwrap()
            .push(self.root.join("etc/kvm/hdmi_disable").exists());
        self.calls.lock().unwrap().push(enabled);
        if self.fail.load(Ordering::Acquire) {
            Err("fixture native failure".into())
        } else {
            Ok(())
        }
    }
    fn has_hdmi_signal(&self) -> Result<bool, Error> {
        Ok(true)
    }
}
struct Fixture {
    root: tempfile::TempDir,
    fake: Arc<Fake>,
    runtime: Arc<Runtime>,
    router: Router,
}
impl Fixture {
    fn new(auth: bool) -> Self {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir_all(root.path().join("etc/kvm")).unwrap();
        fs::create_dir_all(root.path().join("web")).unwrap();
        fs::write(root.path().join("web/index.html"), "HDMI fixture").unwrap();
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
            markers: Mutex::new(vec![]),
            fail: AtomicBool::new(false),
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
        self.runtime.shutdown();
    }
}
async fn request(
    router: &Router,
    path: &str,
    body: &str,
    content_type: &str,
    token: &str,
) -> (StatusCode, Value) {
    let method = if path.split('?').next() == Some("/api/vm/hdmi") {
        "GET"
    } else {
        "POST"
    };
    let mut request = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", content_type)
        .body(Body::from(body.to_owned()))
        .unwrap();
    if !token.is_empty() {
        request
            .headers_mut()
            .insert("authorization", format!("Bearer {token}").parse().unwrap());
    }
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:41000".parse::<SocketAddr>().unwrap(),
    ));
    let response = router.clone().oneshot(request).await.unwrap();
    let status = response.status();
    let data = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    (status, serde_json::from_slice(&data).unwrap())
}
async fn until(test: impl Fn() -> bool) {
    tokio::time::timeout(Duration::from_secs(3), async {
        while !test() {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
}
#[tokio::test]
async fn exact_baseline_hdmi_handlers_match_responses_native_calls_and_saved_files() {
    let oracle: Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/hdmi-go-oracle.json"
    ))
    .unwrap();
    for row in oracle["cases"].as_array().unwrap() {
        let f = Fixture::new(false);
        let c = &row["case"];
        if c["Disabled"] == true {
            fs::write(f.root.path().join("etc/kvm/hdmi_disable"), []).unwrap();
        }
        fs::write(
            f.root.path().join("etc/kvm/hdmi_idle_timeout"),
            c["Timeout"].as_str().unwrap(),
        )
        .unwrap();
        f.runtime
            .hdmi
            .set_viewers("oracle", c["Viewers"].as_i64().unwrap())
            .unwrap();
        f.fake.calls.lock().unwrap().clear();
        let path = if c["Path"] == "get" {
            "/api/vm/hdmi".to_owned()
        } else {
            format!("/api/vm/hdmi/{}", c["Path"].as_str().unwrap())
        };
        let path = format!("{path}?{}", c["Query"].as_str().unwrap());
        let (status, response) = request(
            &f.router,
            &path,
            c["Body"].as_str().unwrap(),
            c["ContentType"].as_str().unwrap(),
            "",
        )
        .await;
        assert_eq!(status, StatusCode::OK, "{}", c["Name"]);
        assert_eq!(response, row["response"], "{}", c["Name"]);
        assert_eq!(
            json!(*f.fake.calls.lock().unwrap()),
            row["calls"],
            "{}",
            c["Name"]
        );
        assert_eq!(
            f.root.path().join("etc/kvm/hdmi_disable").exists(),
            row["disabled"].as_bool().unwrap(),
            "{}",
            c["Name"]
        );
        assert_eq!(
            fs::read_to_string(f.root.path().join("etc/kvm/hdmi_idle_timeout")).unwrap(),
            row["savedTimeout"].as_str().unwrap(),
            "{}",
            c["Name"]
        );
    }
}
#[tokio::test]
async fn session_can_reset_but_only_admin_can_change_durable_capture_intent() {
    let f = Fixture::new(true);
    let user = f.token("viewer", "user");
    let admin = f.token("administrator", "admin");
    // HDMI controls do not depend on another session's HID ownership.
    let (_owner, _status) = f.runtime.input.join().unwrap();
    assert_eq!(
        request(&f.router, "/api/vm/hdmi", "", "application/json", "")
            .await
            .0,
        StatusCode::UNAUTHORIZED
    );
    for path in ["enable", "disable", "timeout"] {
        assert_eq!(
            request(
                &f.router,
                &format!("/api/vm/hdmi/{path}"),
                "{}",
                "application/json",
                &user
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
    }
    assert!(f.fake.calls.lock().unwrap().is_empty());
    assert_eq!(
        request(
            &f.router,
            "/api/vm/hdmi/reset",
            "",
            "application/json",
            &user
        )
        .await
        .1["code"],
        0
    );
    assert_eq!(*f.fake.calls.lock().unwrap(), [false, true]);
    assert_eq!(
        request(
            &f.router,
            "/api/vm/hdmi/disable",
            "invalid ignored body",
            "application/json",
            &admin
        )
        .await
        .1["code"],
        0
    );
    assert_eq!(*f.fake.markers.lock().unwrap(), [false, false, true]);
    assert_eq!(
        request(
            &f.router,
            "/api/vm/hdmi/reset",
            "",
            "application/json",
            &user
        )
        .await
        .1,
        json!({"code":-2,"msg":"HDMI capture is disabled","data":null})
    );
    assert_eq!(*f.fake.calls.lock().unwrap(), [false, true, false]);
    assert_eq!(
        request(
            &f.router,
            "/api/vm/hdmi/enable",
            "",
            "application/json",
            &admin
        )
        .await
        .1["code"],
        0
    );
    assert!(!f.fake.markers.lock().unwrap().last().unwrap());
}
#[tokio::test]
async fn dropping_http_reset_restores_capture_and_releases_the_blocking_job() {
    let f = Fixture::new(false);
    let router = f.router.clone();
    let task = tokio::spawn(async move {
        request(&router, "/api/vm/hdmi/reset", "", "application/json", "").await
    });
    until(|| !f.fake.calls.lock().unwrap().is_empty()).await;
    task.abort();
    let _ = task.await;
    until(|| f.runtime.jobs.available_permits() == 4 && f.fake.calls.lock().unwrap().len() == 2)
        .await;
    assert_eq!(*f.fake.calls.lock().unwrap(), [false, true]);
}
#[tokio::test]
async fn concurrent_admin_disable_cannot_be_overwritten_by_reset_completion() {
    let f = Fixture::new(false);
    let router = f.router.clone();
    let task = tokio::spawn(async move {
        request(&router, "/api/vm/hdmi/reset", "", "application/json", "").await
    });
    until(|| !f.fake.calls.lock().unwrap().is_empty()).await;
    assert_eq!(
        request(
            &f.router,
            "/api/vm/hdmi/disable",
            "",
            "application/json",
            ""
        )
        .await
        .1["code"],
        0
    );
    assert_eq!(task.await.unwrap().1["code"], 0);
    assert_eq!(*f.fake.calls.lock().unwrap(), [false, false]);
    assert_eq!(
        request(&f.router, "/api/vm/hdmi", "", "application/json", "")
            .await
            .1["data"]["enabled"],
        false
    );
}
#[tokio::test]
async fn persistence_and_native_failures_are_reported_instead_of_success() {
    let f = Fixture::new(false);
    let marker = f.root.path().join("etc/kvm/hdmi_disable");
    fs::create_dir(&marker).unwrap();
    assert_eq!(
        request(&f.router, "/api/vm/hdmi/enable", "", "application/json", "")
            .await
            .1["code"],
        -2
    );
    assert_eq!(
        request(
            &f.router,
            "/api/vm/hdmi/disable",
            "",
            "application/json",
            ""
        )
        .await
        .1["code"],
        -2
    );
    let timeout = f.root.path().join("etc/kvm/hdmi_idle_timeout");
    fs::create_dir(&timeout).unwrap();
    assert_eq!(
        request(
            &f.router,
            "/api/vm/hdmi/timeout",
            "{\"Minutes\":10}",
            "application/json",
            ""
        )
        .await
        .1["code"],
        -2
    );
    assert!(f.fake.calls.lock().unwrap().is_empty());
    fs::remove_dir(marker).unwrap();
    f.fake.fail.store(true, Ordering::Release);
    assert_eq!(
        request(
            &f.router,
            "/api/vm/hdmi/disable",
            "",
            "application/json",
            ""
        )
        .await
        .1["code"],
        -2
    );
    assert!(f.root.path().join("etc/kvm/hdmi_disable").exists());
}
#[tokio::test]
async fn default_runtime_reports_unavailable_native_capture_without_faking_signal() {
    let f = Fixture::new(false);
    let runtime = Runtime::load(f.root.path()).unwrap();
    let router = app(runtime.clone(), f.root.path().join("web"));
    assert_eq!(
        request(&router, "/api/vm/hdmi", "", "application/json", "")
            .await
            .1["code"],
        -2
    );
    assert_eq!(
        request(&router, "/api/vm/hdmi/reset", "", "application/json", "")
            .await
            .1["code"],
        -2
    );
    assert!(!f.root.path().join("etc/kvm/hdmi_disable").exists());
    assert!(f.fake.calls.lock().unwrap().is_empty());
    runtime.shutdown();
}

#[tokio::test]
async fn real_http_disconnect_cancels_reset_and_restores_only_its_capture_state() {
    use tokio::io::AsyncWriteExt;
    let f = Fixture::new(false);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let router = f.router.clone();
    let server = tokio::spawn(async move {
        axum::serve(
            listener,
            router.into_make_service_with_connect_info::<SocketAddr>(),
        )
        .await
        .unwrap();
    });
    let mut connection = tokio::net::TcpStream::connect(address).await.unwrap();
    connection
        .write_all(
            format!(
                "POST /api/vm/hdmi/reset HTTP/1.1\r\nHost: {address}\r\nContent-Length: 0\r\n\r\n"
            )
            .as_bytes(),
        )
        .await
        .unwrap();
    until(|| !f.fake.calls.lock().unwrap().is_empty()).await;
    drop(connection);
    until(|| f.runtime.jobs.available_permits() == 4 && f.fake.calls.lock().unwrap().len() == 2)
        .await;
    assert_eq!(*f.fake.calls.lock().unwrap(), [false, true]);
    assert!(!f.root.path().join("etc/kvm/hdmi_disable").exists());
    server.abort();
    let _ = server.await;
}
