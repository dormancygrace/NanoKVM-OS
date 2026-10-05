use super::*;
use crate::{app, video_source::tests::Fixture};
use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::Request as HttpRequest,
};
use serde_json::{json, Value};
use std::net::SocketAddr;
use tower::ServiceExt;
fn detect_calls(fixture: &Fixture) -> Vec<u8> {
    fixture
        .trace()
        .lines()
        .filter_map(|line| line.strip_prefix("detect:"))
        .map(|value| value.parse().unwrap())
        .collect()
}
async fn request(
    fixture: &Fixture,
    path: &str,
    body: &str,
    content_type: &str,
    query: &str,
) -> (u16, Value) {
    let router = app(fixture.runtime.clone(), fixture.root.path().join("web"));
    let mut request = HttpRequest::builder()
        .method("POST")
        .uri(format!("{path}?{query}"));
    if !content_type.is_empty() {
        request = request.header("content-type", content_type);
    }
    let mut request = request.body(Body::from(body.to_owned())).unwrap();
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:10000".parse::<SocketAddr>().unwrap(),
    ));
    let response = router.oneshot(request).await.unwrap();
    let status = response.status().as_u16();
    let body = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    (status, serde_json::from_slice(&body).unwrap())
}
async fn until(fixture: &Fixture, calls: &[u8]) {
    tokio::time::timeout(Duration::from_secs(3), async {
        while detect_calls(fixture) != calls {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
}
#[tokio::test]
async fn all130_actual_go_frame_detect_handler_binding_calls_and_signed_sleep_match_router_c() {
    let oracle: Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/mjpeg-go-oracle.json"
    ))
    .unwrap();
    for (index, expected) in oracle["bindings"].as_array().unwrap().iter().enumerate() {
        let fixture = Fixture::new(None, None, false);
        *fixture.runtime.frame_detect.fixture_delays.lock().unwrap() = Some(Vec::new());
        let case = &expected["case"];
        let path = if case["Kind"] == "update" {
            "/api/stream/mjpeg/detect"
        } else {
            "/api/stream/mjpeg/detect/stop"
        };
        let (status, response) = request(
            &fixture,
            path,
            case["Body"].as_str().unwrap(),
            case["ContentType"].as_str().unwrap(),
            case["Query"].as_str().unwrap(),
        )
        .await;
        assert_eq!(status, expected["status"], "case{index} {case}");
        assert_eq!(response, expected["response"], "case{index} {case}");
        assert_eq!(
            json!(detect_calls(&fixture)),
            expected["calls"],
            "case{index} {case}"
        );
        assert_eq!(
            json!(fixture
                .runtime
                .frame_detect
                .fixture_delays
                .lock()
                .unwrap()
                .as_ref()
                .unwrap()),
            expected["sleeps"],
            "case{index} {case}"
        );
        assert!(!fixture.backend.actor().stopped());
        fixture.cleanup().await;
    }
}
#[tokio::test]
async fn real_one_second_pause_releases_api_jobs_and_restores_owned_native_command() {
    let fixture = Fixture::new(None, None, false);
    let copy = fixture.runtime.clone();
    let paused = tokio::spawn(async move {
        temporary(
            copy,
            HeaderMap::from_iter([(
                "content-type".parse().unwrap(),
                "application/json".parse().unwrap(),
            )]),
            Bytes::from_static(br#"{"duration":1}"#),
            None,
        )
        .await
    });
    until(&fixture, &[0]).await;
    tokio::time::timeout(Duration::from_secs(1), async {
        while fixture.runtime.jobs.available_permits() != 4 {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    let mut permits = Vec::new();
    for _ in 0..4 {
        permits.push(fixture.runtime.jobs.clone().try_acquire_owned().unwrap());
    }
    let start = Instant::now();
    let response = tokio::time::timeout(Duration::from_secs(2), paused)
        .await
        .unwrap()
        .unwrap();
    assert!(start.elapsed() >= Duration::from_millis(800));
    let body: Value =
        serde_json::from_slice(&to_bytes(response.into_body(), 1024).await.unwrap()).unwrap();
    assert_eq!(body["code"], 0);
    assert_eq!(detect_calls(&fixture), vec![0, 60]);
    drop(permits);
    assert!(!fixture.backend.actor().stopped());
    fixture.cleanup().await;
}
#[tokio::test]
async fn newer_disabled_setting_and_overlapping_pause_prevent_stale_restore() {
    let fixture = Fixture::new(None, None, false);
    let copy = fixture.runtime.clone();
    let old = tokio::spawn(async move {
        temporary(
            copy,
            HeaderMap::new(),
            Bytes::new(),
            Some("Duration=1".into()),
        )
        .await
    });
    until(&fixture, &[0]).await;
    let (_, response) = request(
        &fixture,
        "/api/stream/mjpeg/detect",
        r#"{"enabled":false}"#,
        "application/json",
        "",
    )
    .await;
    assert_eq!(response["code"], 0);
    old.await.unwrap();
    assert_eq!(detect_calls(&fixture), vec![0, 0]);
    assert_eq!(fixture.runtime.frame_detect.lock().desired, 0);
    let (_, response) = request(
        &fixture,
        "/api/stream/mjpeg/detect",
        r#"{"enabled":true}"#,
        "application/json",
        "",
    )
    .await;
    assert_eq!(response["code"], 0);
    let copy = fixture.runtime.clone();
    let old = tokio::spawn(async move {
        temporary(
            copy,
            HeaderMap::new(),
            Bytes::new(),
            Some("Duration=1".into()),
        )
        .await
    });
    until(&fixture, &[0, 0, 60, 0]).await;
    let copy = fixture.runtime.clone();
    let newer = tokio::spawn(async move {
        temporary(
            copy,
            HeaderMap::new(),
            Bytes::new(),
            Some("Duration=2".into()),
        )
        .await
    });
    until(&fixture, &[0, 0, 60, 0, 0]).await;
    old.await.unwrap();
    assert_eq!(detect_calls(&fixture), vec![0, 0, 60, 0, 0]);
    newer.await.unwrap();
    assert_eq!(detect_calls(&fixture), vec![0, 0, 60, 0, 0, 60]);
    fixture.cleanup().await;
}
#[tokio::test]
async fn dropping_pause_restores_and_shutdown_never_restores_after_native_close() {
    let fixture = Fixture::new(None, None, false);
    let copy = fixture.runtime.clone();
    let paused = tokio::spawn(async move {
        temporary(
            copy,
            HeaderMap::new(),
            Bytes::new(),
            Some("Duration=60".into()),
        )
        .await
    });
    until(&fixture, &[0]).await;
    paused.abort();
    assert!(paused.await.unwrap_err().is_cancelled());
    until(&fixture, &[0, 60]).await;
    fixture.runtime.frame_detect.join().await;
    assert!(!fixture.backend.actor().stopped());
    let copy = fixture.runtime.clone();
    let paused = tokio::spawn(async move {
        temporary(
            copy,
            HeaderMap::new(),
            Bytes::new(),
            Some("Duration=60".into()),
        )
        .await
    });
    until(&fixture, &[0, 60, 0]).await;
    fixture.cleanup().await;
    let response = tokio::time::timeout(Duration::from_secs(2), paused)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(detect_calls(&fixture), vec![0, 60, 0]);
    assert!(!fixture.runtime.frame_detect.lock().running);
}
#[tokio::test]
async fn native_detector_unavailable_reports_failure_without_recording_fake_intent() {
    let manager = Manager::new(None);
    let cancelled = Cancellation::default();
    assert!(manager.set(false, &cancelled).is_err());
    assert_eq!(manager.lock().desired, 60);
    assert!(manager.begin(&cancelled).is_err());
    assert!(manager.lock().temporary.is_none());
    manager.stop();
    manager.join().await;
}
#[tokio::test]
async fn expired_wait_has_no_deadline_for_disabled_auth_and_honors_absolute_expiry() {
    let fixture = Fixture::new(None, None, false);
    let principal = crate::sessions::Principal {
        user: crate::api::principal(&fixture.runtime, &HeaderMap::new())
            .unwrap()
            .user,
        expires: None,
    };
    assert!(tokio::time::timeout(
        Duration::from_millis(20),
        media_session::expired(&principal)
    )
    .await
    .is_err());
    let expired = crate::sessions::Principal {
        expires: Some(0),
        ..principal
    };
    tokio::time::timeout(Duration::from_millis(100), media_session::expired(&expired))
        .await
        .unwrap();
    fixture.cleanup().await;
}

async fn socket_pause_cancel(h2: bool) {
    use crate::transport::{Acceptor, Protocol};
    use axum::http::Method;
    use futures_util::future::poll_fn;
    use tokio::io::AsyncWriteExt;
    let fixture = Fixture::new(None, None, false);
    let router = app(fixture.runtime.clone(), fixture.root.path().join("web"));
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    listener.set_nonblocking(true).unwrap();
    let handle = axum_server::Handle::new();
    let shutdown = fixture.runtime.transport_shutdown();
    let server = axum_server::from_tcp(listener)
        .unwrap()
        .map(|inner| {
            Acceptor::new(
                inner,
                shutdown.clone(),
                if h2 {
                    Protocol::PriorKnowledge
                } else {
                    Protocol::Http1
                },
            )
        })
        .http1_only()
        .handle(handle.clone());
    let server =
        tokio::spawn(server.serve(router.into_make_service_with_connect_info::<SocketAddr>()));
    assert_eq!(handle.listening().await, Some(address));
    let body = br#"{"duration":60}"#;
    let mut stream = tokio::net::TcpStream::connect(address).await.unwrap();
    if h2 {
        let (mut client, connection) = h2::client::handshake(stream).await.unwrap();
        let connection = tokio::spawn(connection);
        poll_fn(|cx| client.poll_ready(cx)).await.unwrap();
        let (response, mut send) = client
            .send_request(
                HttpRequest::builder()
                    .method(Method::POST)
                    .uri("https://localhost/api/stream/mjpeg/detect/stop")
                    .header("content-type", "application/json")
                    .header("content-length", body.len())
                    .body(())
                    .unwrap(),
                false,
            )
            .unwrap();
        send.send_data(Bytes::copy_from_slice(body), true).unwrap();
        until(&fixture, &[0]).await;
        send.send_reset(h2::Reason::CANCEL);
        assert!(response.await.is_err());
        until(&fixture, &[0, 60]).await;
        assert!(!fixture.backend.actor().stopped());
        drop(send);
        drop(client);
        let _ = tokio::time::timeout(Duration::from_secs(2), connection)
            .await
            .unwrap();
    } else {
        let request = format!("POST /api/stream/mjpeg/detect/stop HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n", body.len());
        stream.write_all(request.as_bytes()).await.unwrap();
        stream.write_all(body).await.unwrap();
        until(&fixture, &[0]).await;
        drop(stream);
        until(&fixture, &[0, 60]).await;
        assert!(!fixture.backend.actor().stopped());
    }
    assert_eq!(fixture.runtime.jobs.available_permits(), 4);
    assert_eq!(fixture.runtime.socket_slots.available_permits(), 64);
    fixture.runtime.frame_detect.join().await;
    fixture.cleanup().await;
    handle.graceful_shutdown(Some(Duration::from_secs(1)));
    tokio::time::timeout(Duration::from_secs(2), server)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
}
#[tokio::test]
async fn actual_http1_disconnect_restores_detector_and_releases_native_api_session() {
    socket_pause_cancel(false).await;
}
#[tokio::test]
async fn actual_http2_reset_restores_detector_and_releases_native_api_session() {
    socket_pause_cancel(true).await;
}
