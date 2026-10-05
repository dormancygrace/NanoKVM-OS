use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::{Request, StatusCode},
    Router,
};
use futures_util::{SinkExt, StreamExt};
use nanokvm_server::{
    app,
    crypto::{self, Claims},
    Runtime,
};
use serde_json::{json, Value};
use std::{
    fs,
    net::SocketAddr,
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};
use tokio::{
    net::TcpStream,
    task::JoinHandle,
    time::{sleep, timeout, Instant},
};
use tokio_tungstenite::{
    connect_async,
    tungstenite::{self, client::IntoClientRequest, Message},
    WebSocketStream,
};
use tower::ServiceExt;

type Socket = WebSocketStream<tokio_tungstenite::MaybeTlsStream<TcpStream>>;
const ENCRYPTED: &str = "U2FsdGVkX18zLUxaLNGy7jL96oMO4tq6wDYwVzUMO3XfTY2Zy/ipO4LDEqtBT+fx";
struct Server {
    root: tempfile::TempDir,
    runtime: Arc<Runtime>,
    router: Router,
    address: SocketAddr,
    task: JoinHandle<()>,
}
impl Drop for Server {
    fn drop(&mut self) {
        self.task.abort();
    }
}
impl Server {
    async fn new(extra: &str) -> Self {
        Self::with_executor(extra, None).await
    }
    async fn with_executor(
        extra: &str,
        executor: Option<Arc<dyn nanokvm_server::systemops::Executor>>,
    ) -> Self {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir_all(root.path().join("etc/kvm")).unwrap();
        fs::write(root.path().join("etc/kvm/server.yaml"), format!("proto: http\njwt:\n  secretKey: websocket-test-secret\n  revokeTokensOnLogout: true\nsecurity:\n  trustedProxies: []\n{extra}")).unwrap();
        fs::write(
            root.path().join("etc/kvm/pwd"),
            json!({"username":"owner","password":ENCRYPTED}).to_string(),
        )
        .unwrap();
        fs::create_dir(root.path().join("dev")).unwrap();
        for i in 0..3 {
            fs::write(root.path().join(format!("dev/hidg{i}")), []).unwrap();
        }
        fs::create_dir(root.path().join("web")).unwrap();
        fs::write(root.path().join("web/index.html"), "test").unwrap();
        let runtime = match executor {
            Some(executor) => Runtime::load_with_executor(root.path(), executor).unwrap(),
            None => Runtime::load(root.path()).unwrap(),
        };
        runtime
            .store
            .authenticate("owner", "operator-password")
            .unwrap()
            .unwrap();
        let router = app(runtime.clone(), root.path().join("web"));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let service = router.clone();
        let task = tokio::spawn(async move {
            axum::serve(
                listener,
                service.into_make_service_with_connect_info::<SocketAddr>(),
            )
            .await
            .unwrap();
        });
        Self {
            root,
            runtime,
            router,
            address,
            task,
        }
    }
    fn token(&self, name: &str, ttl: u64) -> String {
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
                exp: now + ttl,
                iat: Some(now),
                nbf: None,
            },
            &self.runtime.config.jwt.secret_key,
        )
        .unwrap()
    }
    async fn socket(
        &self,
        token: &str,
        headers: &[(&str, &str)],
    ) -> Result<Socket, tungstenite::Error> {
        let mut request = format!("ws://{}/api/ws", self.address)
            .into_client_request()
            .unwrap();
        request
            .headers_mut()
            .insert("cookie", format!("nano-kvm-token={token}").parse().unwrap());
        for (key, value) in headers {
            request.headers_mut().insert(
                tungstenite::http::HeaderName::from_bytes(key.as_bytes()).unwrap(),
                value.parse().unwrap(),
            );
        }
        connect_async(request).await.map(|(socket, _)| socket)
    }
    async fn api(
        &self,
        method: &str,
        path: &str,
        token: &str,
        data: Value,
        lease: Option<&str>,
    ) -> (StatusCode, Value) {
        let mut request = Request::builder()
            .method(method)
            .uri(path)
            .header("content-type", "application/json")
            .header("authorization", format!("Bearer {token}"));
        if let Some(lease) = lease {
            request = request.header("x-nanokvm-input-lease", lease);
        }
        let mut request = request.body(Body::from(data.to_string())).unwrap();
        request.extensions_mut().insert(ConnectInfo(self.address));
        let response = self.router.clone().oneshot(request).await.unwrap();
        let status = response.status();
        let data = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        (status, serde_json::from_slice(&data).unwrap())
    }
    async fn hid(&self, i: usize, length: usize) -> Vec<u8> {
        timeout(Duration::from_secs(5), async {
            loop {
                let bytes = fs::read(self.root.path().join(format!("dev/hidg{i}"))).unwrap();
                if bytes.len() >= length {
                    return bytes;
                }
                sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .expect("HID report deadline")
    }
}
async fn event(socket: &mut Socket, kind: &str) -> Value {
    timeout(Duration::from_secs(5), async {
        loop {
            if let Message::Text(text) = socket.next().await.unwrap().unwrap() {
                let value: Value = serde_json::from_str(&text).unwrap();
                if value["type"] == kind {
                    return value;
                }
            }
        }
    })
    .await
    .expect("socket event deadline")
}
async fn control(socket: &mut Socket, enabled: bool) -> Value {
    timeout(Duration::from_secs(5), async {
        loop {
            let event = event(socket, "control").await;
            let data: Value = serde_json::from_str(event["data"].as_str().unwrap()).unwrap();
            if data["enabled"] == enabled {
                return data;
            }
        }
    })
    .await
    .unwrap()
}
async fn close_code(socket: &mut Socket) -> u16 {
    timeout(Duration::from_secs(5), async {
        loop {
            if let Message::Close(frame) = socket.next().await.expect("close frame").unwrap() {
                return frame.unwrap().code.into();
            }
        }
    })
    .await
    .expect("revocation close deadline")
}

#[tokio::test]
async fn real_sockets_transfer_release_filter_viewers_and_enforce_http_leases() {
    let server = Server::new("").await;
    let token = server.token("owner", 120);
    let origin = format!("http://{}", server.address);
    let mut a = server.socket(&token, &[("origin", &origin)]).await.unwrap();
    let owned_a = control(&mut a, true).await;
    let lease_a = owned_a["lease"].as_str().unwrap();
    let mut b = server.socket(&token, &[("origin", &origin)]).await.unwrap();
    assert!(control(&mut b, false).await.get("lease").is_none());
    a.send(Message::Binary(vec![0].into())).await.unwrap();
    assert_eq!(
        event(&mut a, "heartbeat").await,
        json!({"type":"heartbeat","data":""})
    );
    // Invalid keyboard frames and valid viewer frames never reach the descriptor.
    a.send(Message::Binary(vec![1, 1].into())).await.unwrap();
    b.send(Message::Binary(vec![1, 0, 0, 5, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    a.send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    assert_eq!(server.hid(0, 8).await, [0, 0, 4, 0, 0, 0, 0, 0]);
    let (_, response) = server
        .api(
            "POST",
            "/api/hid/paste",
            &token,
            json!({"content":"a"}),
            None,
        )
        .await;
    assert_eq!(response["code"], -4);
    let (status, _) = server
        .api("POST", "/api/hid/paste", &token, json!({}), Some(lease_a))
        .await;
    assert_eq!(status, StatusCode::NOT_IMPLEMENTED); // Paste itself is still pending.
    b.send(Message::Binary(vec![3, 1].into())).await.unwrap();
    let owned_b = control(&mut b, true).await;
    assert!(control(&mut a, false).await.get("lease").is_none());
    let lease_b = owned_b["lease"].as_str().unwrap();
    assert_ne!(lease_a, lease_b);
    assert_eq!(
        server.hid(0, 16).await,
        [vec![0, 0, 4, 0, 0, 0, 0, 0], vec![0; 8]].concat()
    );
    let (_, response) = server
        .api("POST", "/api/hid/paste", &token, json!({}), Some(lease_a))
        .await;
    assert_eq!(response["code"], -4);
    assert!(server.runtime.input.allows_http(lease_b));
    b.send(Message::Binary(vec![2, 1, 1, 2, 3, 4, 0, 0].into()))
        .await
        .unwrap();
    assert_eq!(server.hid(2, 7).await, [1, 1, 2, 3, 4, 0, 0]);
    b.close(None).await.unwrap();
    control(&mut a, true).await; // Sole remaining eligible viewer is promoted after cleanup.
    assert_eq!(
        server.hid(2, 14).await,
        [vec![1, 1, 2, 3, 4, 0, 0], vec![0, 1, 2, 3, 4, 0, 0]].concat()
    );
    a.close(None).await.unwrap();
    timeout(Duration::from_secs(5), async {
        while !server.runtime.input.allows_http("") {
            sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn account_mutation_revokes_live_socket_and_cleanup_but_idempotent_update_keeps_it() {
    let server = Server::new("").await;
    server
        .runtime
        .store
        .create("viewer", "operator-password", "user")
        .unwrap();
    let admin = server.token("owner", 120);
    let viewer = server.token("viewer", 120);
    let mut socket = server.socket(&viewer, &[]).await.unwrap();
    control(&mut socket, true).await;
    socket
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 8).await;
    assert_eq!(
        server
            .api(
                "PUT",
                "/api/auth/users/viewer",
                &admin,
                json!({"role":"user","enabled":true}),
                None
            )
            .await
            .1["code"],
        0
    );
    socket.send(Message::Binary(vec![0].into())).await.unwrap();
    event(&mut socket, "heartbeat").await;
    assert_eq!(
        server
            .api(
                "PUT",
                "/api/auth/users/viewer",
                &admin,
                json!({"enabled":false}),
                None
            )
            .await
            .1["code"],
        0
    );
    assert_eq!(close_code(&mut socket).await, 4401);
    assert_eq!(
        server.hid(0, 16).await,
        [vec![0, 0, 4, 0, 0, 0, 0, 0], vec![0; 8]].concat()
    );
    assert!(server.runtime.input.allows_http(""));
}

#[tokio::test]
async fn origin_factory_auth_logout_and_expiry_gate_real_upgrades() {
    let server = Server::new("").await;
    let token = server.token("owner", 120);
    for (auth, headers, expected) in [
        ("", vec![], 401),
        (
            token.as_str(),
            vec![("origin", "https://foreign.example")],
            403,
        ),
        (
            token.as_str(),
            vec![
                ("origin", "https://foreign.example"),
                ("x-forwarded-host", "foreign.example"),
                ("x-forwarded-proto", "https"),
            ],
            403,
        ),
        (
            token.as_str(),
            vec![("authorization", "Bearer malformed")],
            401,
        ),
    ] {
        match server.socket(auth, &headers).await {
            Err(tungstenite::Error::Http(response)) => {
                assert_eq!(response.status().as_u16(), expected)
            }
            _ => panic!("upgrade accepted invalid authentication/origin"),
        }
    }
    let mut socket = server.socket(&token, &[]).await.unwrap();
    control(&mut socket, true).await;
    assert_eq!(
        server
            .api("POST", "/api/auth/logout", &token, Value::Null, None)
            .await
            .1["code"],
        0
    );
    assert_eq!(close_code(&mut socket).await, 4401);
    let short = server.token("owner", 2);
    let mut expiring = server.socket(&short, &[]).await.unwrap();
    control(&mut expiring, true).await;
    let start = Instant::now();
    assert_eq!(close_code(&mut expiring).await, 4401);
    assert!(start.elapsed() < Duration::from_secs(4));
    // Factory gate must run even before the WebSocket extractor rejects missing headers.
    let factory = Server::new("authentication: enable\n").await;
    fs::remove_file(factory.root.path().join("etc/kvm/pwd")).unwrap();
    factory
        .runtime
        .store
        .authenticate("admin", "admin")
        .unwrap()
        .unwrap();
    let token = factory.token("admin", 120);
    assert_eq!(
        factory
            .api("GET", "/api/ws", &token, Value::Null, None)
            .await
            .0,
        StatusCode::FORBIDDEN
    );
}

#[tokio::test]
async fn runtime_shutdown_releases_held_reports_and_closes_all_sockets() {
    let server = Server::new("").await;
    let token = server.token("owner", 120);
    let mut socket = server.socket(&token, &[]).await.unwrap();
    control(&mut socket, true).await;
    socket
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 8).await;
    let state = server.runtime.clone();
    tokio::task::spawn_blocking(move || state.shutdown())
        .await
        .unwrap();
    assert_eq!(close_code(&mut socket).await, 4401);
    assert_eq!(
        server.hid(0, 16).await,
        [vec![0, 0, 4, 0, 0, 0, 0, 0], vec![0; 8]].concat()
    );
    match server.socket(&token, &[]).await {
        Err(tungstenite::Error::Http(response)) => {
            assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE)
        }
        _ => panic!("shutdown accepted a new socket"),
    }
}

#[tokio::test]
async fn oversized_socket_message_closes_with_1009_and_releases_input() {
    let server = Server::new("").await;
    let token = server.token("owner", 120);
    let mut socket = server.socket(&token, &[]).await.unwrap();
    control(&mut socket, true).await;
    socket
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 8).await;
    socket
        .send(Message::Binary(vec![0; 4097].into()))
        .await
        .unwrap();
    assert_eq!(close_code(&mut socket).await, 1009);
    assert_eq!(
        server.hid(0, 16).await,
        [vec![0, 0, 4, 0, 0, 0, 0, 0], vec![0; 8]].concat()
    );
}

#[tokio::test]
async fn blocked_nonblocking_hid_does_not_stall_control_and_discards_old_queue() {
    use std::{
        ffi::CString,
        io::{Read, Write},
        os::unix::fs::OpenOptionsExt,
    };
    let server = Server::new("").await;
    let path = server.root.path().join("dev/hidg0");
    fs::remove_file(&path).unwrap();
    let name = CString::new(path.as_os_str().as_encoded_bytes()).unwrap();
    // This fixture is a FIFO inside the isolated root, never a device node.
    assert_eq!(unsafe { libc::mkfifo(name.as_ptr(), 0o600) }, 0);
    let mut reader = fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NONBLOCK)
        .open(&path)
        .unwrap();
    let mut filler = fs::OpenOptions::new()
        .write(true)
        .custom_flags(libc::O_NONBLOCK)
        .open(&path)
        .unwrap();
    let block = vec![0xaa; 4096];
    loop {
        match filler.write(&block) {
            Ok(_) => {}
            Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => break,
            Err(error) => panic!("{error}"),
        }
    }
    let token = server.token("owner", 120);
    let mut a = server.socket(&token, &[]).await.unwrap();
    control(&mut a, true).await;
    let mut b = server.socket(&token, &[]).await.unwrap();
    control(&mut b, false).await;
    for _ in 0..32 {
        a.send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
            .await
            .unwrap();
    }
    // Allow a blocked write and its failed neutral cleanup to complete first.
    sleep(Duration::from_millis(180)).await;
    let start = Instant::now();
    b.send(Message::Binary(vec![3, 1].into())).await.unwrap();
    timeout(Duration::from_secs(2), control(&mut b, true))
        .await
        .unwrap();
    assert!(start.elapsed() < Duration::from_secs(2));
    let mut buffer = [0; 8192];
    loop {
        match reader.read(&mut buffer) {
            Ok(0) => break,
            Ok(n) => assert!(buffer[..n].iter().all(|b| *b == 0xaa)),
            Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => break,
            Err(error) => panic!("{error}"),
        }
    }
    b.send(Message::Binary(vec![1, 0, 0, 6, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    let report = timeout(Duration::from_secs(5), async {
        let mut reports = Vec::new();
        loop {
            match reader.read(&mut buffer) {
                Ok(n) if n > 0 => {
                    reports.extend_from_slice(&buffer[..n]);
                    if reports.len() >= 16 {
                        return reports;
                    }
                }
                Ok(_) => {}
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {}
                Err(error) => panic!("{error}"),
            }
            sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(report, [vec![0; 8], vec![0, 0, 6, 0, 0, 0, 0, 0]].concat());
    b.close(None).await.unwrap();
    a.close(None).await.unwrap();
}

#[tokio::test]
async fn led_rest_and_socket_snapshots_follow_host_output_and_bound_keyboard_state() {
    use std::{
        io::Write,
        os::{fd::OwnedFd, unix::net::UnixStream},
    };
    let server = Server::new("").await;
    let bound = server
        .root
        .path()
        .join("sys/kernel/config/usb_gadget/g0/configs/c.1/hid.GS0");
    fs::create_dir_all(&bound).unwrap();
    let token = server.token("owner", 120);
    let initial = server
        .api("GET", "/api/hid/leds", &token, Value::Null, None)
        .await
        .1["data"]
        .clone();
    assert_eq!(
        initial,
        json!({"keyboardEnabled":true,"numLock":false,"capsLock":false,"scrollLock":false,"known":false,"updatedAt":""})
    );
    let mut socket = server.socket(&token, &[]).await.unwrap();
    let initial = event(&mut socket, "hid-led-status").await;
    let initial: Value = serde_json::from_str(initial["data"].as_str().unwrap()).unwrap();
    assert_eq!(initial["updatedAt"], "0001-01-01T00:00:00Z");
    control(&mut socket, true).await;
    assert!(server.runtime.hid.leds().missing_reader()); // Fake regular input files are never read as output reports.
    let (reader, mut host) = UnixStream::pair().unwrap();
    reader.set_nonblocking(true).unwrap();
    server
        .runtime
        .hid
        .leds()
        .replace_reader(Some(fs::File::from(OwnedFd::from(reader))))
        .unwrap();
    host.write_all(&[3]).unwrap();
    let update = event(&mut socket, "hid-led-status").await;
    let update: Value = serde_json::from_str(update["data"].as_str().unwrap()).unwrap();
    assert_eq!(update["known"], true);
    assert_eq!(update["numLock"], true);
    assert_eq!(update["capsLock"], true);
    assert_eq!(update["scrollLock"], false);
    assert!(update["updatedAt"].as_str().unwrap().ends_with('Z'));
    assert_eq!(
        server
            .api("GET", "/api/hid/leds", &token, Value::Null, None)
            .await
            .1["data"],
        update
    );
    fs::remove_dir(bound).unwrap();
    let disabled = server
        .api("GET", "/api/hid/leds", &token, Value::Null, None)
        .await
        .1["data"]
        .clone();
    assert_eq!(disabled["keyboardEnabled"], false);
    assert_eq!(disabled["known"], false);
    let update = event(&mut socket, "hid-led-status").await;
    assert_eq!(
        serde_json::from_str::<Value>(update["data"].as_str().unwrap()).unwrap(),
        disabled
    );
    socket.close(None).await.unwrap();
}

#[tokio::test]
async fn manual_socket_preempts_addon_waits_cleanup_and_holds_lane_through_cooldown() {
    use nanokvm_server::inputcontrol::{Cause, OperationKind};
    let server = Server::new("").await;
    let token = server.token("owner", 120);
    let mut socket = server.socket(&token, &[]).await.unwrap();
    control(&mut socket, true).await;
    let mut operation = server
        .runtime
        .coordinator
        .begin(OperationKind::Hid)
        .unwrap();
    socket
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    assert_eq!(
        timeout(Duration::from_secs(2), operation.cancelled())
            .await
            .unwrap(),
        Cause::ManualPreempted
    );
    assert!(fs::read(server.root.path().join("dev/hidg0"))
        .unwrap()
        .is_empty());
    // Other HTTP work and socket heartbeats continue while this socket waits.
    let (status, _) = timeout(
        Duration::from_secs(1),
        server.api("GET", "/api/hid/mode", &token, Value::Null, None),
    )
    .await
    .unwrap();
    assert_eq!(status, StatusCode::OK);
    drop(operation); // An actual addon drops this after its bounded HID cleanup.
    server.hid(0, 8).await;
    assert!(server
        .runtime
        .coordinator
        .begin(OperationKind::Hid)
        .is_err());
    let read = server
        .runtime
        .coordinator
        .begin(OperationKind::ReadOnly)
        .unwrap();
    socket
        .send(Message::Binary(vec![1, 0, 0, 0, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    assert_eq!(server.hid(0, 16).await[8..], [0; 8]);
    assert_eq!(read.cause(), None);
    drop(read);
    assert!(server
        .runtime
        .coordinator
        .begin(OperationKind::Hid)
        .is_err());
    sleep(Duration::from_millis(2050)).await;
    server
        .runtime
        .coordinator
        .begin(OperationKind::Hid)
        .unwrap();
    socket.close(None).await.unwrap();
}

#[tokio::test]
async fn picoclaw_lock_filters_new_input_but_allows_release_and_viewer_close_is_safe() {
    let server = Server::new("").await;
    let token = server.token("owner", 120);
    let mut owner = server.socket(&token, &[]).await.unwrap();
    control(&mut owner, true).await;
    assert!(server.runtime.pico_lock.acquire("agent-one").unwrap());
    owner
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    owner.send(Message::Binary(vec![0].into())).await.unwrap();
    event(&mut owner, "heartbeat").await;
    assert!(fs::read(server.root.path().join("dev/hidg0"))
        .unwrap()
        .is_empty());
    assert!(server.runtime.pico_lock.release_owned("agent-one"));
    owner
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 8).await;
    let mut viewer = server.socket(&token, &[]).await.unwrap();
    control(&mut viewer, false).await;
    viewer.close(None).await.unwrap();
    sleep(Duration::from_millis(30)).await;
    assert_eq!(
        fs::read(server.root.path().join("dev/hidg0"))
            .unwrap()
            .len(),
        8
    );
    server.runtime.pico_lock.acquire("agent-two").unwrap();
    owner
        .send(Message::Binary(vec![1, 0, 0, 5, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    owner
        .send(Message::Binary(vec![1, 0, 0, 0, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    assert_eq!(server.hid(0, 16).await[8..], [0; 8]);
    owner.close(None).await.unwrap();
}

#[tokio::test]
async fn socket_release_and_disconnect_drain_ai_mode_transitions() {
    use nanokvm_server::controlmode::Mode;
    let server = Server::new("").await;
    let token = server.token("owner", 120);
    let mut owner = server.socket(&token, &[]).await.unwrap();
    control(&mut owner, true).await;
    owner
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 8).await;
    let (preempt, mut saw_preempt) = tokio::sync::mpsc::channel(1);
    let mode = server.runtime.control.clone();
    let switch = tokio::task::spawn_blocking(move || {
        mode.switch(
            None,
            Mode::Mcp,
            || {
                preempt.blocking_send(()).unwrap();
                Ok(())
            },
            || Ok(()),
        )
    });
    timeout(Duration::from_secs(2), saw_preempt.recv())
        .await
        .unwrap()
        .unwrap();
    owner
        .send(Message::Binary(vec![1, 0, 0, 5, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    owner
        .send(Message::Binary(vec![1, 0, 0, 0, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    assert_eq!(server.hid(0, 16).await[8..], [0; 8]);
    timeout(Duration::from_secs(2), switch)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert_eq!(server.runtime.control.current(), Mode::Mcp);
    owner
        .send(Message::Binary(vec![1, 0, 0, 6, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 24).await;
    let mode = server.runtime.control.clone();
    let switch =
        tokio::task::spawn_blocking(move || mode.switch(None, Mode::Off, || Ok(()), || Ok(())));
    owner.close(None).await.unwrap();
    timeout(Duration::from_secs(2), switch)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert_eq!(server.hid(0, 32).await[24..], [0; 8]);
    assert_eq!(server.runtime.control.current(), Mode::Off);
}

#[tokio::test]
async fn jiggler_background_reports_skip_owned_control_and_compensate_before_manual_input() {
    use nanokvm_server::jiggler::move_once;
    let server = Server::new("").await;
    let token = server.token("owner", 120);
    let mut owner = server.socket(&token, &[]).await.unwrap();
    control(&mut owner, true).await;
    assert!(!move_once(server.runtime.clone()).await.unwrap());
    server.runtime.jiggler.configure(true, "absolute").unwrap();
    server.runtime.pico_lock.acquire("active-agent").unwrap();
    assert!(!move_once(server.runtime.clone()).await.unwrap());
    assert!(server.runtime.pico_lock.release_owned("active-agent"));
    assert!(move_once(server.runtime.clone()).await.unwrap());
    assert_eq!(
        server.hid(2, 14).await,
        [
            vec![0, 0, 0x3f, 0, 0x3f, 0, 0],
            vec![0, 0xff, 0x3f, 0xff, 0x3f, 0, 0]
        ]
        .concat()
    );
    server.runtime.jiggler.configure(true, "relative").unwrap();
    let movement = tokio::spawn(move_once(server.runtime.clone()));
    server.hid(1, 5).await;
    owner
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 8).await;
    assert!(movement.await.unwrap().unwrap());
    // Compensation completed before the manual reservation acquired the lane.
    assert_eq!(
        fs::read(server.root.path().join("dev/hidg1")).unwrap(),
        [vec![0, 10, 10, 0, 0], vec![0, 246, 246, 0, 0]].concat()
    );
    assert!(!move_once(server.runtime.clone()).await.unwrap());
    owner
        .send(Message::Binary(vec![1, 0, 0, 0, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 16).await;
    assert!(!move_once(server.runtime.clone()).await.unwrap());
    server.runtime.shutdown();
    let length = fs::metadata(server.root.path().join("dev/hidg1"))
        .unwrap()
        .len();
    assert!(!move_once(server.runtime.clone()).await.unwrap());
    sleep(Duration::from_millis(30)).await;
    assert_eq!(
        fs::metadata(server.root.path().join("dev/hidg1"))
            .unwrap()
            .len(),
        length
    );
}

#[tokio::test]
async fn persisted_jiggler_worker_moves_after_inactivity_and_stops_on_disable() {
    let server = Server::new("").await;
    server.runtime.jiggler.configure(true, "relative").unwrap();
    assert!(fs::read(server.root.path().join("dev/hidg1"))
        .unwrap()
        .is_empty());
    timeout(Duration::from_secs(20), async {
        loop {
            if fs::metadata(server.root.path().join("dev/hidg1"))
                .unwrap()
                .len()
                >= 10
            {
                break;
            }
            sleep(Duration::from_millis(25)).await;
        }
    })
    .await
    .expect("15-second inactivity worker");
    assert_eq!(
        fs::read(server.root.path().join("dev/hidg1")).unwrap(),
        [vec![0, 10, 10, 0, 0], vec![0, 246, 246, 0, 0]].concat()
    );
    server.runtime.jiggler.configure(false, "").unwrap();
    assert_eq!(server.runtime.jiggler.status().unwrap()["enabled"], false);
    assert!(!nanokvm_server::jiggler::move_once(server.runtime.clone())
        .await
        .unwrap());
    server.runtime.shutdown();
}

struct UsbFixture {
    root: std::sync::Mutex<Option<std::path::PathBuf>>,
    calls: std::sync::Mutex<Vec<nanokvm_server::systemops::Action>>,
    fail: std::sync::atomic::AtomicBool,
}
impl UsbFixture {
    fn new() -> Arc<Self> {
        Arc::new(Self {
            root: std::sync::Mutex::new(None),
            calls: std::sync::Mutex::new(Vec::new()),
            fail: std::sync::atomic::AtomicBool::new(false),
        })
    }
}
impl nanokvm_server::systemops::Executor for UsbFixture {
    fn run(
        &self,
        action: nanokvm_server::systemops::Action,
        bound: Duration,
    ) -> Result<(), nanokvm_server::Error> {
        use nanokvm_server::systemops::Action;
        assert_eq!(bound, Duration::from_secs(10));
        self.calls.lock().unwrap().push(action);
        if self.fail.load(std::sync::atomic::Ordering::Acquire) {
            return Err("fixture command failed".into());
        }
        if action == Action::UsbPhyRestart {
            let root = self.root.lock().unwrap().clone().unwrap();
            assert_eq!(fs::read(root.join("dev/hidg0")).unwrap()[8..16], [0; 8]);
            for i in 0..3 {
                fs::rename(
                    root.join(format!("dev/hidg{i}")),
                    root.join(format!("dev/old-hidg{i}")),
                )?;
            }
            // Simulate delayed configfs device-node reappearance, never kernel IO.
            std::thread::spawn(move || {
                std::thread::sleep(Duration::from_millis(150));
                for i in 0..3 {
                    fs::write(root.join(format!("dev/hidg{i}")), []).unwrap();
                }
            });
        }
        Ok(())
    }
}

#[tokio::test]
async fn usb_reset_releases_old_input_reopens_new_nodes_and_preserves_socket_control() {
    use nanokvm_server::systemops::Action;
    let fixture = UsbFixture::new();
    let server = Server::with_executor("", Some(fixture.clone())).await;
    *fixture.root.lock().unwrap() = Some(server.root.path().to_path_buf());
    let token = server.token("owner", 120);
    let mut owner = server.socket(&token, &[]).await.unwrap();
    let old_control = control(&mut owner, true).await;
    owner
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    server.hid(0, 8).await;
    let start = Instant::now();
    let response = server
        .api("POST", "/api/hid/reset", &token, Value::Null, None)
        .await;
    assert_eq!(response.1["code"], 0);
    assert!(start.elapsed() >= Duration::from_millis(150));
    assert_eq!(*fixture.calls.lock().unwrap(), [Action::UsbPhyRestart]);
    assert_eq!(
        control(&mut owner, true).await["lease"],
        old_control["lease"]
    );
    assert!(fs::read(server.root.path().join("dev/hidg0"))
        .unwrap()
        .is_empty());
    owner
        .send(Message::Binary(vec![1, 0, 0, 5, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    assert_eq!(server.hid(0, 8).await, [0, 0, 5, 0, 0, 0, 0, 0]);
    fixture
        .fail
        .store(true, std::sync::atomic::Ordering::Release);
    assert_eq!(
        server
            .api("POST", "/api/hid/reset", &token, Value::Null, None)
            .await
            .1["msg"],
        "failed to reset hid"
    );
    // A failed command still releases old ownership generations and allows recovery.
    owner
        .send(Message::Binary(vec![1, 0, 0, 6, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    timeout(Duration::from_secs(2), async {
        loop {
            if fs::read(server.root.path().join("dev/hidg0"))
                .unwrap()
                .starts_with(&[0, 0, 6, 0, 0, 0, 0, 0])
            {
                break;
            }
            sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    owner.close(None).await.unwrap();
}

#[tokio::test]
async fn hid_profile_install_validation_noop_symlink_copy_reboot_and_admin_gate() {
    use nanokvm_server::systemops::Action;
    let fixture = UsbFixture::new();
    let server = Server::with_executor("", Some(fixture.clone())).await;
    *fixture.root.lock().unwrap() = Some(server.root.path().to_path_buf());
    let token = server.token("owner", 120);
    for path in [
        "sys/kernel/config/usb_gadget/g0",
        "etc/init.d",
        "usr/libexec/nanokvm/legacy",
        "kvmapp/system/init.d",
    ] {
        fs::create_dir_all(server.root.path().join(path)).unwrap();
    }
    fs::write(
        server
            .root
            .path()
            .join("sys/kernel/config/usb_gadget/g0/bcdDevice"),
        "0x0710\n",
    )
    .unwrap();
    let target = server
        .root
        .path()
        .join("usr/libexec/nanokvm/legacy/S03usbdev");
    fs::write(&target, "old").unwrap();
    std::os::unix::fs::symlink(
        "/usr/libexec/nanokvm/legacy/S03usbdev",
        server.root.path().join("etc/init.d/S03usbdev"),
    )
    .unwrap();
    fs::write(
        server.root.path().join("kvmapp/system/init.d/S03usbhid"),
        "new profile",
    )
    .unwrap();
    assert_eq!(
        server
            .api(
                "POST",
                "/api/hid/mode",
                &token,
                json!({"mode":"normal"}),
                None
            )
            .await
            .1["code"],
        0
    );
    assert!(fixture.calls.lock().unwrap().is_empty());
    assert_eq!(
        server
            .api("POST", "/api/hid/mode", &token, json!({"mode":""}), None)
            .await
            .1["code"],
        -1
    );
    assert_eq!(
        server
            .api(
                "POST",
                "/api/hid/mode",
                &token,
                json!({"mode":"invalid"}),
                None
            )
            .await
            .1["code"],
        -2
    );
    assert_eq!(
        server
            .api(
                "POST",
                "/api/hid/mode",
                &token,
                json!({"MODE":"hid-only"}),
                None
            )
            .await
            .1["code"],
        0
    );
    assert_eq!(fs::read(&target).unwrap(), b"new profile");
    // The response precedes the delayed reboot dispatch.
    assert!(fixture.calls.lock().unwrap().is_empty());
    timeout(Duration::from_secs(2), async {
        loop {
            if !fixture.calls.lock().unwrap().is_empty() {
                break;
            }
            sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(*fixture.calls.lock().unwrap(), [Action::Reboot]);
    fs::remove_file(server.root.path().join("kvmapp/system/init.d/S03usbhid")).unwrap();
    assert_eq!(
        server
            .api(
                "POST",
                "/api/hid/mode",
                &token,
                json!({"mode":"hid-only"}),
                None
            )
            .await
            .1["code"],
        -3
    );
    assert_eq!(fs::read(&target).unwrap(), b"new profile");
    assert!(
        fs::symlink_metadata(server.root.path().join("etc/init.d/S03usbdev"))
            .unwrap()
            .file_type()
            .is_symlink()
    );
    server
        .runtime
        .store
        .create("viewer", "operator-password", "user")
        .unwrap();
    let viewer = server.token("viewer", 120);
    assert_eq!(
        server
            .api("POST", "/api/hid/reset", &viewer, Value::Null, None)
            .await
            .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        server
            .api(
                "POST",
                "/api/hid/mode",
                &viewer,
                json!({"mode":"normal"}),
                None
            )
            .await
            .0,
        StatusCode::FORBIDDEN
    );
    server.runtime.shutdown();
}
