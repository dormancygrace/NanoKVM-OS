use base64::{engine::general_purpose::STANDARD, Engine};
use nanokvm_server::{
    sysinfo::{Interface, Interfaces, Manager, Native},
    Error,
};
use serde_json::Value;
use std::{fs, path::Path, sync::Arc};
#[derive(Clone)]
struct Fixture(Vec<Interface>);
impl Interfaces for Fixture {
    fn list(&self) -> Result<Vec<Interface>, Error> {
        Ok(self.0.clone())
    }
}
fn oracle() -> Value {
    serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/info-go-oracle.json"
    ))
    .unwrap()
}
#[test]
fn actual_baseline_go_identity_interface_selection_and_pid_bytes() {
    for case in oracle().as_array().unwrap() {
        let root = tempfile::tempdir().unwrap();
        for (path, value) in case["files"].as_object().unwrap() {
            let path = root.path().join(path.trim_start_matches('/'));
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(path, STANDARD.decode(value.as_str().unwrap()).unwrap()).unwrap();
        }
        let interfaces = case["interfaces"]
            .as_array()
            .unwrap()
            .iter()
            .enumerate()
            .map(|(index, iface)| Interface {
                index: index as u32 + 1,
                name: iface["name"].as_str().unwrap().into(),
                up: iface["up"].as_bool().unwrap(),
                running: iface["running"].as_bool().unwrap(),
                addresses: iface["addresses"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .map(|ip| ip.as_str().unwrap().parse().unwrap())
                    .collect(),
            })
            .collect();
        let manager = Manager::new(root.path().into(), Arc::new(Fixture(interfaces)));
        assert_eq!(
            serde_json::to_value(manager.read()).unwrap(),
            case["info"],
            "{}",
            case["name"]
        );
        assert_eq!(
            manager.mdns_enabled(),
            case["mdnsEnabled"].as_bool().unwrap(),
            "{}",
            case["name"]
        );
    }
}
#[test]
fn native_getifaddrs_is_owned_and_isolated_root_refuses_host_interface_discovery() {
    let root = tempfile::tempdir().unwrap();
    assert!(Native::new(root.path().into())
        .list()
        .unwrap_err()
        .to_string()
        .contains("isolated root"));
    for _ in 0..3 {
        let interfaces = Native::new(Path::new("/").into()).list().unwrap();
        assert!(!interfaces.is_empty());
        assert!(interfaces
            .iter()
            .all(|iface| iface.index > 0 && !iface.name.is_empty()));
        assert!(interfaces
            .windows(2)
            .all(|items| items[0].index < items[1].index));
        assert!(interfaces
            .iter()
            .any(|iface| iface.addresses.iter().any(|ip| ip.is_loopback())));
    }
}
#[tokio::test]
async fn info_read_allows_sessions_and_mdns_status_requires_admin() {
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
        "proto: http\njwt:\n  secretKey: info-test-secret\n",
    )
    .unwrap();
    let commands = Arc::new(nanokvm_server::systemops::Native::new(root.path().into()));
    let network = Arc::new(Fixture(vec![Interface {
        index: 2,
        name: "eth0".into(),
        up: true,
        running: true,
        addresses: vec!["192.0.2.1".parse().unwrap()],
    }]));
    let runtime = Runtime::load_with_information_backends(
        root.path(),
        commands.clone(),
        Arc::new(nanokvm_server::monitor::Unavailable),
        Arc::new(nanokvm_server::gpio::Native::new(root.path().into())),
        Arc::new(nanokvm_server::cpufreq::Native::new(root.path().into())),
        Arc::new(nanokvm_server::timeconfig::Native::new(
            root.path().into(),
            commands,
        )),
        network,
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
    fs::create_dir_all(root.path().join("run/avahi-daemon")).unwrap();
    fs::write(root.path().join("run/avahi-daemon/pid"), "123\n").unwrap();
    fs::write(root.path().join("etc/hostname"), "nano\n").unwrap();
    for (name, path, status) in [
        ("viewer", "/api/vm/info", 200),
        ("viewer", "/api/vm/mdns", 403),
        ("administrator", "/api/vm/mdns", 200),
    ] {
        let mut req = Request::builder()
            .uri(path)
            .header("authorization", format!("Bearer {}", token(name)))
            .body(Body::empty())
            .unwrap();
        req.extensions_mut().insert(ConnectInfo(
            "127.0.0.1:10000".parse::<std::net::SocketAddr>().unwrap(),
        ));
        let response = router.clone().oneshot(req).await.unwrap();
        assert_eq!(response.status(), StatusCode::from_u16(status).unwrap());
        let bytes = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        let value: Value = serde_json::from_slice(&bytes).unwrap();
        if status == 200 {
            assert_eq!(value["code"], 0);
            if path.ends_with("info") {
                assert_eq!(value["data"]["ips"][0]["addr"], "192.0.2.1");
                assert_eq!(value["data"]["mdns"], "nano.local");
            } else {
                assert_eq!(value["data"]["enabled"], true);
            }
        }
    }
    runtime.shutdown();
}
