use base64::{engine::general_purpose::STANDARD, Engine};
use nanokvm_server::memory_status;
use serde_json::Value;
use std::{fs, path::Path};
fn oracle() -> Value {
    serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/memory-go-oracle.json"
    ))
    .unwrap()
}
fn files(root: &Path, case: &Value) {
    for path in case["dirs"].as_array().unwrap() {
        fs::create_dir_all(root.join(path.as_str().unwrap().trim_start_matches('/'))).unwrap();
    }
    for (path, value) in case["files"].as_object().unwrap() {
        let path = root.join(path.trim_start_matches('/'));
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, STANDARD.decode(value.as_str().unwrap()).unwrap()).unwrap();
    }
}
#[test]
fn actual_go_memory_video_availability_active_swap_and_nullable_statistics() {
    for case in oracle()["cases"].as_array().unwrap() {
        let root = tempfile::tempdir().unwrap();
        files(root.path(), case);
        let status = memory_status::read_status(root.path());
        assert_eq!(
            status.is_err(),
            case["error"].as_bool().unwrap(),
            "{}",
            case["name"]
        );
        if let Ok(status) = status {
            assert_eq!(
                serde_json::to_value(status).unwrap(),
                case["status"],
                "{}",
                case["name"]
            );
        }
        assert_eq!(
            serde_json::to_value(memory_status::video(root.path())).unwrap(),
            case["video"],
            "{}",
            case["name"]
        );
    }
}
#[test]
fn recompression_policy_and_swap_devices_remain_independent() {
    for (text, expected) in [
        ("", false),
        ("ZRAM_RECOMPRESS=1\n", true),
        ("ZRAM_RECOMPRESS=1\nZRAM_RECOMPRESS=0\n", false),
        ("ZRAM_RECOMPRESS=1\nZRAM_RECOMPRESS=invalid\n", true),
        ("ZRAM_RECOMPRESS=10", false),
        ("SD_ENABLED=1", false),
        ("ZRAM_RECOMPRESS=1\r\n", false),
    ] {
        assert_eq!(memory_status::recompress_enabled(text), expected);
    }
    assert!(memory_status::zstd_ready("#1: lz4 [zstd]\n"));
    assert!(!memory_status::zstd_ready("#2: lz4 [zstd]\n"));
    let swaps=memory_status::active_swaps("Filename Type Size Used Priority\n/dev/zram0 partition 65532 8192 100\n/swapfile file 262140 4096 10\n");
    assert_eq!(swaps["/dev/zram0"].size_mib, 64);
    assert_eq!(swaps["/swapfile"].size_mib, 256);
    assert_eq!(swaps["/dev/zram0"].used_bytes, 8 * 1024 * 1024);
    assert_eq!(swaps["/swapfile"].priority, 10);
    assert!(
        !memory_status::active_swaps("/dev/zram0 partition 65532 0 100\n")
            .contains_key("/swapfile")
    );
}
#[tokio::test]
async fn memory_admin_contract_and_actual_gin_title_read_errors_and_utf8() {
    use axum::{
        body::{to_bytes, Body},
        extract::ConnectInfo,
        http::{Request, StatusCode},
    };
    use nanokvm_server::{app, crypto, Runtime};
    use tower::ServiceExt;
    let root = tempfile::tempdir().unwrap();
    fs::create_dir_all(root.path().join("etc/kvm")).unwrap();
    fs::write(
        root.path().join("etc/kvm/server.yaml"),
        "proto: http\njwt:\n  secretKey: memory-test-secret\n",
    )
    .unwrap();
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
    for (name, status, code) in [("viewer", 403, 0), ("administrator", 200, -1)] {
        let mut req = Request::builder()
            .uri("/api/vm/memory/status")
            .header("authorization", format!("Bearer {}", token(name)))
            .body(Body::empty())
            .unwrap();
        req.extensions_mut().insert(ConnectInfo(
            "127.0.0.1:10000".parse::<std::net::SocketAddr>().unwrap(),
        ));
        let response = router.clone().oneshot(req).await.unwrap();
        assert_eq!(response.status(), StatusCode::from_u16(status).unwrap());
        let data = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        let value: Value = serde_json::from_slice(&data).unwrap();
        if status == 200 {
            assert_eq!(value["code"], code);
            assert_eq!(value["msg"], "Failed to read memory statistics");
        }
    }
    let oracle = oracle();
    let case = oracle["cases"]
        .as_array()
        .unwrap()
        .iter()
        .find(|case| case["name"] == "active-both")
        .unwrap();
    files(root.path(), case);
    let mut req = Request::builder()
        .uri("/api/vm/memory/status")
        .header(
            "authorization",
            format!("Bearer {}", token("administrator")),
        )
        .body(Body::empty())
        .unwrap();
    req.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:10000".parse::<std::net::SocketAddr>().unwrap(),
    ));
    let response = router.clone().oneshot(req).await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let bytes = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    let value: Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(value["data"], case["status"]);
    for case in oracle["titles"].as_array().unwrap() {
        let path = root.path().join("etc/kvm/web-title");
        let _ = fs::remove_file(&path);
        if let Some(bytes) = case["bytes"].as_str() {
            fs::write(path, STANDARD.decode(bytes).unwrap()).unwrap();
        }
        let mut req = Request::builder()
            .uri("/api/vm/web-title")
            .header("authorization", format!("Bearer {}", token("viewer")))
            .body(Body::empty())
            .unwrap();
        req.extensions_mut().insert(ConnectInfo(
            "127.0.0.1:10000".parse::<std::net::SocketAddr>().unwrap(),
        ));
        let response = router.clone().oneshot(req).await.unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let bytes = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        let value: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(value, case["response"], "{}", case["name"]);
    }
    runtime.shutdown();
}
