use super::*;
use crate::{
    app,
    crypto::{self, Claims},
    media_status::Mode,
    video_source::{tests::Fixture, Codec},
};
use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::Request,
    Router,
};
use serde_json::{json, Value};
use std::{
    fs,
    os::unix::fs::{symlink, PermissionsExt},
    time::{SystemTime, UNIX_EPOCH},
};
use tokio::{net::TcpStream, task::JoinHandle};
use tokio_tungstenite::{
    connect_async,
    tungstenite::{self, client::IntoClientRequest, Message as ClientMessage},
    MaybeTlsStream, WebSocketStream,
};
use tower::ServiceExt;
type Socket = WebSocketStream<MaybeTlsStream<TcpStream>>;
struct Server {
    fixture: Fixture,
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
    async fn new(status: Option<i32>, hold: bool, auth: bool) -> Self {
        Self::with_backend(status, hold, auth, true).await
    }
    async fn with_backend(status: Option<i32>, hold: bool, auth: bool, available: bool) -> Self {
        let mut fixture = Fixture::new(None, status, hold);
        if !available {
            fixture.cleanup().await;
            fixture.runtime = Runtime::load_with_backends(
                fixture.root.path(),
                Arc::new(crate::video_source::tests::NoEffects),
                Arc::new(crate::monitor::Unavailable),
            )
            .unwrap();
        }
        if auth {
            let runtime =
                Arc::get_mut(&mut fixture.runtime).expect("fixture Runtime has no external owners");
            runtime.config.authentication = "enable".into();
            runtime.config.security.trusted_proxies.clear();
            let copy = fixture.runtime.clone();
            tokio::task::spawn_blocking(move || {
                copy.store
                    .create("operator", "test-password-strong", "admin")
                    .unwrap();
                copy.store
                    .create("viewer", "test-password-strong", "user")
                    .unwrap();
            })
            .await
            .unwrap();
        }
        if available {
            fixture.initialize().await;
        }
        let router = app(fixture.runtime.clone(), fixture.root.path().join("web"));
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
            fixture,
            router,
            address,
            task,
        }
    }
    fn runtime(&self) -> &Arc<Runtime> {
        &self.fixture.runtime
    }
    fn token(&self, name: &str, ttl: u64) -> String {
        let user = self.runtime().store.get(name).unwrap();
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
            &self.runtime().config.jwt.secret_key,
        )
        .unwrap()
    }
    async fn socket(
        &self,
        path: &str,
        token: &str,
        origin: Option<&str>,
    ) -> Result<Socket, tungstenite::Error> {
        let mut request = format!("ws://{}{path}", self.address)
            .into_client_request()
            .unwrap();
        if !token.is_empty() {
            request
                .headers_mut()
                .insert("cookie", format!("nano-kvm-token={token}").parse().unwrap());
        }
        if let Some(origin) = origin {
            request
                .headers_mut()
                .insert("origin", origin.parse().unwrap());
        }
        connect_async(request).await.map(|(socket, _)| socket)
    }
    async fn api(
        &self,
        method: &str,
        path: &str,
        token: &str,
        body: &str,
        content_type: &str,
    ) -> (u16, Value, String) {
        let mut request = Request::builder().method(method).uri(path);
        if !token.is_empty() {
            request = request.header("authorization", format!("Bearer {token}"));
        }
        if !content_type.is_empty() {
            request = request.header("content-type", content_type);
        }
        let mut request = request.body(Body::from(body.to_owned())).unwrap();
        request.extensions_mut().insert(ConnectInfo(self.address));
        let response = self.router.clone().oneshot(request).await.unwrap();
        let status = response.status().as_u16();
        let cache = response
            .headers()
            .get("cache-control")
            .map_or("", |value| value.to_str().unwrap())
            .to_owned();
        let body = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        (
            status,
            if body.is_empty() {
                Value::Null
            } else {
                serde_json::from_slice(&body).unwrap()
            },
            cache,
        )
    }
    async fn viewers(&self, count: usize) {
        timeout(Duration::from_secs(3), async {
            loop {
                let hdmi = self.runtime().hdmi.clone();
                let state = tokio::task::spawn_blocking(move || hdmi.snapshot())
                    .await
                    .unwrap()
                    .unwrap();
                if state["viewerCount"].as_u64().unwrap() == count as u64 {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
    }
    async fn cleanup(&self) {
        self.fixture.cleanup().await;
        timeout(Duration::from_secs(3), async {
            while self.runtime().socket_slots.available_permits() != 64 {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
        assert_eq!(self.fixture.budget.used(), 0);
        assert_eq!(self.runtime().direct.active.load(Ordering::Acquire), 0);
        assert!(!self.runtime().direct.publication.lock().unwrap().running);
    }
}
async fn binary(socket: &mut Socket) -> Bytes {
    timeout(Duration::from_secs(4), async {
        loop {
            match socket.next().await.unwrap().unwrap() {
                ClientMessage::Binary(data) => return data,
                ClientMessage::Ping(data) => {
                    socket.send(ClientMessage::Pong(data)).await.unwrap();
                }
                value => panic!("expected native Direct frame, got {value:?}"),
            }
        }
    })
    .await
    .unwrap()
}
async fn close_frame(socket: &mut Socket) -> (u16, String) {
    timeout(Duration::from_secs(5), async {
        loop {
            match socket.next().await.unwrap().unwrap() {
                ClientMessage::Close(Some(frame)) => {
                    return (u16::from(frame.code), frame.reason.to_string())
                }
                ClientMessage::Ping(data) => {
                    let _ = socket.send(ClientMessage::Pong(data)).await;
                }
                _ => {}
            }
        }
    })
    .await
    .unwrap()
}
async fn capture_event(socket: &mut Socket) -> Value {
    timeout(Duration::from_secs(4), async {
        loop {
            match socket.next().await.unwrap().unwrap() {
                ClientMessage::Text(text) => {
                    let event: Value = serde_json::from_str(&text).unwrap();
                    if event["type"] == "capture-status" {
                        assert!(
                            event["data"].is_string(),
                            "legacy capture data is a JSON string"
                        );
                        return serde_json::from_str(event["data"].as_str().unwrap()).unwrap();
                    }
                }
                ClientMessage::Ping(data) => socket.send(ClientMessage::Pong(data)).await.unwrap(),
                _ => {}
            }
        }
    })
    .await
    .unwrap()
}
#[tokio::test]
async fn encoder_state_all_actual_go_cases_through_router_and_native_source() {
    let oracle: Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/encoder-state-go-oracle.json"
    ))
    .unwrap();
    for (index, expected) in oracle["cases"].as_array().unwrap().iter().enumerate() {
        let case = &expected["case"];
        let user = case["Role"] == "user" && case["Method"] == "POST";
        let server = Server::new(None, false, user).await;
        let active = Codec::parse(case["Active"].as_str().unwrap());
        let subscription = if let Some(codec) = active {
            Some(
                server
                    .runtime()
                    .video
                    .subscribe(Some(EncoderConfig { codec }))
                    .await
                    .unwrap(),
            )
        } else {
            None
        };
        if let Some(codec) = Codec::parse(case["Selected"].as_str().unwrap()) {
            server.runtime().video.select(EncoderConfig { codec });
        }
        if case["Blocked"].as_bool().unwrap() {
            fs::create_dir(server.fixture.root.path().join("etc/kvm/encoder_codec")).unwrap();
        }
        let token = if user {
            server.token("viewer", 120)
        } else {
            String::new()
        };
        let (status, response, cache) = server
            .api(
                case["Method"].as_str().unwrap(),
                "/api/stream/state",
                &token,
                case["Body"].as_str().unwrap(),
                case["ContentType"].as_str().unwrap(),
            )
            .await;
        assert_eq!(status, expected["status"], "case{index}: {case}");
        assert_eq!(response, expected["response"], "case{index}: {case}");
        assert_eq!(cache, expected["cacheControl"], "case{index}");
        let path = server.fixture.root.path().join("etc/kvm/encoder_codec");
        let saved = fs::read_to_string(&path).ok();
        assert_eq!(json!(saved), expected["saved"], "case{index}");
        if saved.is_some() {
            assert_eq!(
                fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                expected["mode"].as_u64().unwrap() as u32
            );
        }
        let selection = server.runtime().video.selected_config();
        assert_eq!(
            selection.is_some(),
            expected["selected"].as_bool().unwrap(),
            "case{index}"
        );
        assert_eq!(
            selection.map_or(String::new(), |config| config.codec.to_string()),
            expected["codec"],
            "case{index}"
        );
        drop(subscription);
        server.cleanup().await;
    }
}
#[tokio::test]
async fn actual_c_two_h265_viewers_share_source_fps_and_never_join_hid() {
    let server = Server::new(None, false, false).await;
    let (input_id, input_status) = server.runtime().input.join().unwrap();
    assert_eq!(input_id, 1);
    let mut first = server
        .socket("/api/stream/video/direct", "", None)
        .await
        .unwrap();
    let one = binary(&mut first).await;
    assert_eq!(one.len(), 24);
    assert_eq!(one[0], 1);
    assert_eq!(&one[9..], b"synthetic-frame");
    assert!(i64::from_le_bytes(one[1..9].try_into().unwrap()) > 0);
    let mut second = server
        .socket("/api/stream/video/direct", "", None)
        .await
        .unwrap();
    let two = binary(&mut second).await;
    assert_eq!(two[0], 1);
    server.viewers(2).await;
    {
        let state = server.runtime().direct.lock();
        assert_eq!(state.entries.len(), 2);
        assert!(state.session.is_some());
    }
    assert_eq!(
        server.runtime().video.encoder_state(),
        (true, false, "h265".into())
    );
    let (next_id, _) = server.runtime().input.join().unwrap();
    assert_eq!(next_id, 2);
    assert!(input_status.borrow().enabled);
    assert!(server.fixture.trace().contains("codec:2"));
    assert!(!server.fixture.trace().contains("codec:1"));
    timeout(Duration::from_secs(6), async {
        while server.runtime().frame_rate.fps() == 0 {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    let fps = server.runtime().frame_rate.fps();
    assert!(
        (10..=60).contains(&fps),
        "source count must not multiply by viewers: {fps}"
    );
    assert_eq!(
        fs::read_to_string(server.fixture.root.path().join("run/nanokvm/now_fps")).unwrap(),
        fps.to_string()
    );
    first.close(None).await.unwrap();
    drop(first);
    server.viewers(1).await;
    second.close(None).await.unwrap();
    drop(second);
    server.viewers(0).await;
    server.runtime().video.join().await;
    server.runtime().direct.join().await;
    assert!(!server.fixture.backend.actor().stopped());
    server.runtime().input.leave(input_id).unwrap();
    server.runtime().input.leave(next_id).unwrap();
    server.cleanup().await;
}
#[tokio::test]
async fn actual_c_legacy_ignores_query_conflicts_and_selection_reconfigures_without_poisoning() {
    let server = Server::new(None, false, true).await;
    let token = server.token("operator", 120);
    let mut legacy = server
        .socket(
            "/api/stream/h264/direct?codec=h265&rc=invalid",
            &token,
            None,
        )
        .await
        .unwrap();
    assert_eq!(binary(&mut legacy).await[0], 1);
    assert!(server.fixture.trace().contains("codec:1"));
    let mut conflict = server
        .socket("/api/stream/video/direct", &token, None)
        .await
        .unwrap();
    let rejected = close_frame(&mut conflict).await;
    assert_eq!(rejected.0, 1008);
    assert!(rejected.1.contains("{Codec:h264}"));
    drop(conflict);
    let before = server.api("GET", "/api/stream/state", &token, "", "").await;
    assert_eq!(
        before.1["data"],
        json!({"active":true,"selected":false,"codec":"h264"})
    );
    assert_eq!(before.2, "no-store");
    let selected = server
        .api(
            "POST",
            "/api/stream/state",
            &token,
            r#"{"codec":"h265"}"#,
            "text/plain",
        )
        .await;
    assert_eq!(selected.1["code"], 0);
    assert_eq!(
        close_frame(&mut legacy).await,
        (1008, "encoder-reconfigured".into())
    );
    drop(legacy);
    assert_eq!(
        server.runtime().video.encoder_state(),
        (false, true, "h265".into())
    );
    let mut replacement = server
        .socket("/api/stream/video/direct", &token, None)
        .await
        .unwrap();
    assert_eq!(binary(&mut replacement).await[0], 1);
    assert!(server.fixture.trace().contains("codec:2"));
    assert!(!server.fixture.backend.actor().stopped());
    replacement.close(None).await.unwrap();
    drop(replacement);
    server.viewers(0).await;
    server.cleanup().await;
}
#[tokio::test]
async fn actual_c_flow_signed_ack_and_resync_over_socket() {
    let server = Server::new(None, false, false).await;
    let mut socket = server
        .socket("/api/stream/video/direct?flow=1", "", None)
        .await
        .unwrap();
    let frame = binary(&mut socket).await;
    assert_eq!(frame[0], 1);
    let mut ack = vec![2];
    ack.extend_from_slice(&(-1i64).to_le_bytes());
    socket
        .send(ClientMessage::Binary(ack.into()))
        .await
        .unwrap();
    socket
        .send(ClientMessage::Text("ack".into()))
        .await
        .unwrap();
    assert!(
        timeout(Duration::from_millis(80), socket.next())
            .await
            .is_err(),
        "negative ACK must not release positive native timestamp"
    );
    let mut ack = vec![2];
    ack.extend_from_slice(&frame[1..9]);
    socket
        .send(ClientMessage::Binary(ack.into()))
        .await
        .unwrap();
    let next = binary(&mut socket).await;
    assert!(
        i64::from_le_bytes(next[1..9].try_into().unwrap())
            > i64::from_le_bytes(frame[1..9].try_into().unwrap())
    );
    socket
        .send(ClientMessage::Binary(vec![3].into()))
        .await
        .unwrap();
    let recovered = binary(&mut socket).await;
    assert_eq!(recovered[0], 1);
    socket.close(None).await.unwrap();
    drop(socket);
    server.viewers(0).await;
    server.cleanup().await;
}
#[tokio::test]
async fn direct_auth_factory_origin_and_literal_invalid_query_gates() {
    let server = Server::new(None, false, true).await;
    let token = server.token("operator", 120);
    for (path, token, origin, status) in [
        ("/api/stream/video/direct", "", None, 401),
        (
            "/api/stream/video/direct",
            token.as_str(),
            Some("http://untrusted.example"),
            403,
        ),
        (
            "/api/stream/video/direct?codec=%25s",
            token.as_str(),
            None,
            400,
        ),
        ("/api/stream/video/direct?rc=vbr", token.as_str(), None, 400),
    ] {
        let error = server.socket(path, token, origin).await.err().unwrap();
        let tungstenite::Error::Http(response) = error else {
            panic!("unexpected upgrade error: {error}")
        };
        assert_eq!(response.status().as_u16(), status);
        if path.contains("%25s") {
            assert_eq!(
                response.body().as_ref().unwrap(),
                b"invalid codec \"%s\": expected h264 or h265"
            );
        }
    }
    let factory = server.token("admin", 120);
    let tungstenite::Error::Http(response) = server
        .socket("/api/stream/h264/direct", &factory, None)
        .await
        .err()
        .unwrap()
    else {
        panic!()
    };
    assert_eq!(response.status().as_u16(), 403);
    assert!(!server.fixture.trace().contains("codec:"));
    server.cleanup().await;
}
#[tokio::test]
async fn media_revocation_and_absolute_expiry_ignore_busy_api_jobs_and_preserve_hid() {
    let server = Server::new(None, false, true).await;
    let (id, input_status) = server.runtime().input.join().unwrap();
    assert!(input_status.borrow().enabled);
    let token = server.token("viewer", 120);
    let mut socket = server
        .socket("/api/stream/video/direct", &token, None)
        .await
        .unwrap();
    binary(&mut socket).await;
    let mut permits = Vec::new();
    for _ in 0..4 {
        permits.push(server.runtime().jobs.clone().try_acquire_owned().unwrap());
    }
    server.runtime().revoke_sessions("viewer");
    assert_eq!(close_frame(&mut socket).await.0, 4401);
    drop(socket);
    assert!(input_status.borrow().enabled);
    drop(permits);
    server.viewers(0).await;
    let token = server.token("viewer", 4);
    let mut socket = server
        .socket("/api/stream/video/direct", &token, None)
        .await
        .unwrap();
    binary(&mut socket).await;
    let mut permits = Vec::new();
    for _ in 0..4 {
        permits.push(server.runtime().jobs.clone().try_acquire_owned().unwrap());
    }
    assert_eq!(close_frame(&mut socket).await.0, 4401);
    drop(socket);
    drop(permits);
    assert!(input_status.borrow().enabled);
    server.runtime().input.leave(id).unwrap();
    server.viewers(0).await;
    server.cleanup().await;
}
#[tokio::test]
async fn direct_native_error_is_reported_to_existing_and_new_input_sockets_as_json_string() {
    let server = Server::new(Some(-5), false, false).await;
    let mut input = server.socket("/api/ws", "", None).await.unwrap();
    let mut direct = server
        .socket("/api/stream/video/direct", "", None)
        .await
        .unwrap();
    let status = capture_event(&mut input).await;
    assert_eq!(status["mode"], "direct");
    assert_eq!(status["result"], -5);
    assert_eq!(status["message"], "Retrieving image");
    assert_eq!(status["severity"], "warning");
    assert!(timeout(Duration::from_millis(80), direct.next())
        .await
        .is_err());
    server.runtime().capture_status.update(Mode::Mjpeg, -7);
    server.runtime().capture_status.update(Mode::H264, -1);
    let mut initial = server.socket("/api/ws", "", None).await.unwrap();
    for mode in ["direct", "h264", "mjpeg"] {
        assert_eq!(capture_event(&mut initial).await["mode"], mode);
    }
    direct.close(None).await.unwrap();
    drop(direct);
    server.viewers(0).await;
    server.runtime().video.join().await;
    server.runtime().direct.join().await;
    server.runtime().capture_status.update(Mode::Direct, 3);
    let mut recovered = capture_event(&mut input).await;
    while recovered["mode"] != "direct" {
        recovered = capture_event(&mut input).await;
    }
    assert_eq!(recovered["ok"], true);
    input.close(None).await.unwrap();
    initial.close(None).await.unwrap();
    drop(input);
    drop(initial);
    server.viewers(0).await;
    server.cleanup().await;
}
#[tokio::test]
async fn encoder_selection_replaces_final_symlink_and_write_failure_preserves_active_source() {
    let server = Server::new(None, false, false).await;
    let dir = server.fixture.root.path().join("etc/kvm");
    fs::write(dir.join("original"), "h264\n").unwrap();
    symlink("original", dir.join("encoder_codec")).unwrap();
    let response = server
        .api(
            "POST",
            "/api/stream/state",
            "",
            r#"{"codec":"h265"}"#,
            "application/json",
        )
        .await;
    assert_eq!(response.1["code"], 0);
    assert!(!fs::symlink_metadata(dir.join("encoder_codec"))
        .unwrap()
        .file_type()
        .is_symlink());
    assert_eq!(fs::read_to_string(dir.join("original")).unwrap(), "h264\n");
    assert_eq!(
        fs::metadata(dir.join("encoder_codec"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    let mut socket = server
        .socket("/api/stream/video/direct", "", None)
        .await
        .unwrap();
    binary(&mut socket).await;
    fs::remove_file(dir.join("encoder_codec")).unwrap();
    fs::create_dir(dir.join("encoder_codec")).unwrap();
    let response = server
        .api(
            "POST",
            "/api/stream/state",
            "",
            r#"{"codec":"h264"}"#,
            "application/json",
        )
        .await;
    assert_eq!(response.1["code"], -2);
    assert_eq!(
        server.runtime().video.encoder_state(),
        (true, true, "h265".into())
    );
    assert_eq!(&binary(&mut socket).await[9..], b"synthetic-frame");
    socket.close(None).await.unwrap();
    drop(socket);
    server.viewers(0).await;
    server.cleanup().await;
}
#[tokio::test]
async fn incoming_direct_message_limit_and_shutdown_release_native_budget() {
    let server = Server::new(None, false, false).await;
    let mut socket = server
        .socket("/api/stream/video/direct", "", None)
        .await
        .unwrap();
    binary(&mut socket).await;
    socket
        .send(ClientMessage::Binary(vec![0; 65].into()))
        .await
        .unwrap();
    assert_eq!(close_frame(&mut socket).await.0, 1009);
    drop(socket);
    server.viewers(0).await;
    let mut socket = server
        .socket("/api/stream/video/direct", "", None)
        .await
        .unwrap();
    binary(&mut socket).await;
    server.runtime().shutdown_async().await.unwrap();
    assert_eq!(close_frame(&mut socket).await.0, 4401);
    drop(socket);
    server.cleanup().await;
}

#[tokio::test]
async fn last_socket_close_waits_held_native_read_and_replacement_without_poisoning_actor() {
    let server = Server::new(None, true, false).await;
    let mut old = server
        .socket("/api/stream/video/direct", "", None)
        .await
        .unwrap();
    server.fixture.until_trace("codec:2").await;
    old.close(None).await.unwrap();
    drop(old);
    timeout(Duration::from_millis(500), async {
        while !server.runtime().direct.lock().entries.is_empty() {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    assert!(!server.fixture.backend.actor().stopped());
    let selected = server
        .api(
            "POST",
            "/api/stream/state",
            "",
            r#"{"codec":"h264"}"#,
            "application/json",
        )
        .await;
    assert_eq!(selected.1["code"], 0);
    let mut next = server
        .socket("/api/stream/video/direct?codec=h264", "", None)
        .await
        .unwrap();
    assert!(timeout(Duration::from_millis(50), next.next())
        .await
        .is_err());
    assert!(!server.fixture.backend.actor().stopped());
    fs::remove_file(server.fixture.root.path().join("hold")).unwrap();
    assert_eq!(binary(&mut next).await[0], 1);
    assert!(server.fixture.trace().contains("codec:1"));
    assert!(!server.fixture.backend.actor().stopped());
    next.close(None).await.unwrap();
    drop(next);
    server.viewers(0).await;
    server.cleanup().await;
}
#[tokio::test]
async fn viewer_publication_is_one_coalesced_joined_worker_with_latest_revision() {
    let server = Server::new(None, false, false).await;
    for version in 1..=64 {
        server.runtime().direct.publish(version as usize, version);
    }
    server.runtime().direct.publish(7, 65);
    server.runtime().direct.publish(99, 1);
    assert_eq!(server.runtime().direct.active.load(Ordering::Acquire), 1);
    {
        let pending = server.runtime().direct.publication.lock().unwrap();
        assert!(pending.running);
        assert_eq!(pending.latest, Some((7, 65)));
    }
    server.runtime().direct.join().await;
    server.viewers(7).await;
    server.runtime().direct.publish(0, 66);
    server.runtime().direct.join().await;
    server.viewers(0).await;
    assert!(!server.fixture.trace().contains("codec:"));
    server.cleanup().await;
}
#[tokio::test]
async fn default_unavailable_backend_truthful_policy_close_and_saved_inactive_selection() {
    let server = Server::with_backend(None, false, false, false).await;
    let selected = server
        .api(
            "POST",
            "/api/stream/state",
            "",
            r#"{"codec":"h265"}"#,
            "application/json",
        )
        .await;
    assert_eq!(selected.1["code"], 0);
    let state = server.api("GET", "/api/stream/state", "", "", "").await;
    assert_eq!(
        state.1["data"],
        json!({"active":false,"selected":true,"codec":"h265"})
    );
    let mut socket = server
        .socket("/api/stream/video/direct", "", None)
        .await
        .unwrap();
    assert_eq!(
        close_frame(&mut socket).await,
        (1008, "native video backend is not linked".into())
    );
    drop(socket);
    assert!(!server.fixture.trace().contains("codec:"));
    server.cleanup().await;
}
