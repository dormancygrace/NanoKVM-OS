use base64::{engine::general_purpose::STANDARD, Engine};
use nanokvm_server::{
    dashboard::{Backend, Filesystem, Manager, Native, Sampler, CPU},
    sysinfo::{self, TelemetryInterface},
    Error,
};
use serde_json::Value;
use std::{
    collections::BTreeMap,
    fs,
    path::Path,
    sync::Arc,
    time::{Duration, Instant},
};
fn oracle() -> Value {
    serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/dashboard-go-oracle.json"
    ))
    .unwrap()
}
#[derive(Clone)]
struct Fixture {
    interfaces: Vec<TelemetryInterface>,
    stats: BTreeMap<String, Filesystem>,
}
impl Backend for Fixture {
    fn architecture(&self) -> String {
        "riscv64".into()
    }
    fn cores(&self) -> usize {
        2
    }
    fn storage(&self, path: &str) -> Result<Filesystem, Error> {
        self.stats
            .get(path)
            .copied()
            .ok_or_else(|| "missing fixture statfs".into())
    }
    fn interfaces(&self) -> Result<Vec<TelemetryInterface>, Error> {
        Ok(self.interfaces.clone())
    }
}
fn contract(actual: &Value, expected: &Value, name: &str) {
    match (actual, expected) {
        (Value::Number(a), Value::Number(b)) => {
            if let (Some(a), Some(b)) = (a.as_u64(), b.as_u64()) {
                assert_eq!(a, b, "{name}")
            } else {
                assert_eq!(a.as_f64(), b.as_f64(), "{name}")
            }
        }
        (Value::Array(a), Value::Array(b)) => {
            assert_eq!(a.len(), b.len(), "{name}");
            for (i, (a, b)) in a.iter().zip(b).enumerate() {
                contract(a, b, &format!("{name}[{i}]"))
            }
        }
        (Value::Object(a), Value::Object(b)) => {
            assert_eq!(a.len(), b.len(), "{name}");
            for (key, b) in b {
                contract(
                    a.get(key).unwrap_or_else(|| panic!("{name}.{key} absent")),
                    b,
                    &format!("{name}.{key}"),
                )
            }
        }
        _ => assert_eq!(actual, expected, "{name}"),
    }
}
#[test]
fn actual_go_dashboard_files_interfaces_mounts_and_sensor_contracts() {
    for case in oracle()["cases"].as_array().unwrap() {
        let root = tempfile::tempdir().unwrap();
        for (path, bytes) in case["files"].as_object().unwrap() {
            let path = root.path().join(path.trim_start_matches('/'));
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(path, STANDARD.decode(bytes.as_str().unwrap()).unwrap()).unwrap();
        }
        let interfaces = case["interfaces"]
            .as_array()
            .unwrap()
            .iter()
            .map(|item| TelemetryInterface {
                interface: sysinfo::Interface {
                    index: item["Index"].as_u64().unwrap() as u32,
                    name: item["Name"].as_str().unwrap().into(),
                    up: item["Up"].as_bool().unwrap(),
                    running: item["Running"].as_bool().unwrap(),
                    addresses: Vec::new(),
                },
                loopback: item["Loopback"].as_bool().unwrap(),
                kind: item["Kind"].as_str().unwrap().into(),
                mac: item["MAC"].as_str().unwrap().into(),
                mtu: item["MTU"].as_i64().unwrap(),
                addresses: item["Addresses"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .map(|v| v.as_str().unwrap().into())
                    .collect(),
            })
            .collect();
        let stats = case["stats"]
            .as_object()
            .unwrap()
            .iter()
            .map(|(name, item)| {
                (
                    name.clone(),
                    Filesystem {
                        blocks: item["Blocks"].as_u64().unwrap(),
                        available: item["Available"].as_u64().unwrap(),
                        free: item["Free"].as_u64().unwrap(),
                        block_size: item["BlockSize"].as_i64().unwrap(),
                        flags: item["Flags"].as_u64().unwrap(),
                    },
                )
            })
            .collect();
        let manager = Manager::new(root.path().into(), Arc::new(Fixture { interfaces, stats }));
        let actual = serde_json::to_value(manager.read_at(123456000, Instant::now())).unwrap();
        contract(&actual, &case["status"], case["name"].as_str().unwrap());
    }
}
#[test]
fn actual_go_cpu_intervals_stale_boundary_and_mapped_non_cidr_addresses() {
    let now = Instant::now();
    let mut sampler = Sampler::default();
    for (i, case) in oracle()["sampler"].as_array().unwrap().iter().enumerate() {
        let at = now + Duration::from_secs(i as u64);
        let cpu = case["cpu"].is_object().then(|| CPU {
            total: case["cpu"]["total"].as_u64().unwrap(),
            idle: case["cpu"]["idle"].as_u64().unwrap(),
        });
        sampler.update(cpu, at);
        for (field, seconds) in [("usage", 0), ("at3", 3), ("at4", 4)] {
            assert_eq!(
                sampler.value(at + Duration::from_secs(seconds)),
                case[field].as_f64(),
                "interval{i} {field}"
            )
        }
    }
    for case in oracle()["cidrs"].as_array().unwrap() {
        assert_eq!(
            sysinfo::cidr(
                case["address"].as_str().unwrap().parse().unwrap(),
                case["mask"].as_str().unwrap().parse().unwrap()
            )
            .unwrap(),
            case["text"].as_str().unwrap()
        )
    }
}
#[test]
fn native_affinity_filesystem_and_network_metadata_are_owned_and_isolated() {
    let root = tempfile::tempdir().unwrap();
    let backend = Native::new(
        root.path().into(),
        Arc::new(sysinfo::Native::new(root.path().into())),
    );
    assert_eq!(backend.cores(), 0);
    assert!(backend.storage("/").is_err());
    assert!(backend.interfaces().is_err());
    let backend = Native::new(
        Path::new("/").into(),
        Arc::new(sysinfo::Native::new(Path::new("/").into())),
    );
    assert!(backend.cores() > 0);
    assert!(backend.storage("/tmp").is_err());
    let filesystem = backend.storage("/").unwrap();
    let storage = nanokvm_server::dashboard::storage("/", true, Some(filesystem));
    // The Go native snapshot is evidence from the oracle run, not a fixture
    // tied to one host disk capacity. These checks remain portable.
    assert!(storage.total > 0);
    assert!(storage.free <= storage.total);
    assert!(storage.used <= storage.total);
    assert!(storage.available);
    let interfaces = backend.interfaces().unwrap();
    assert!(!interfaces.is_empty());
    assert!(interfaces.iter().any(|iface| iface.loopback));
    assert!(interfaces
        .iter()
        .all(|iface| iface.interface.index > 0 && iface.mtu > 0));
    assert!(interfaces
        .iter()
        .all(|iface| iface.addresses.iter().all(|address| address.contains('/'))));
    // Allocation remains owned after subsequent libc/netlink calls free their buffers.
    let again = backend.interfaces().unwrap();
    assert_eq!(
        interfaces
            .iter()
            .map(|iface| (&iface.interface.name, &iface.mac, iface.mtu))
            .collect::<Vec<_>>(),
        again
            .iter()
            .map(|iface| (&iface.interface.name, &iface.mac, iface.mtu))
            .collect::<Vec<_>>()
    );
}
#[tokio::test]
async fn sampler_measures_without_http_and_stops_when_aborted() {
    let root = tempfile::tempdir().unwrap();
    fs::create_dir(root.path().join("proc")).unwrap();
    let path = root.path().join("proc/stat");
    fs::write(&path, "cpu 10 0 0 10").unwrap();
    let manager = Manager::new(
        root.path().into(),
        Arc::new(Fixture {
            interfaces: Vec::new(),
            stats: BTreeMap::new(),
        }),
    );
    let task = manager.start().unwrap();
    fs::write(&path, "cpu 20 0 0 20").unwrap();
    tokio::time::timeout(Duration::from_secs(4), async {
        loop {
            if manager.read().cpu_usage == Some(50.0) {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await
        }
    })
    .await
    .unwrap();
    task.abort();
    let _ = task.await;
    fs::write(&path, "cpu 40 0 0 20").unwrap();
    tokio::time::sleep(Duration::from_millis(1100)).await;
    assert_eq!(manager.read().cpu_usage, Some(50.0));
    tokio::time::sleep(Duration::from_millis(2100)).await;
    assert_eq!(manager.read().cpu_usage, None);
    let weak = Arc::downgrade(&manager);
    drop(manager);
    assert!(weak.upgrade().is_none());
}
#[tokio::test]
async fn dashboard_session_api_embeds_memory_null_and_hardware_identity() {
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
        "proto: http\njwt:\n  secretKey: dashboard-tests\n",
    )
    .unwrap();
    fs::create_dir(root.path().join("web")).unwrap();
    fs::write(root.path().join("web/index.html"), "test").unwrap();
    let runtime = Runtime::load(root.path()).unwrap();
    runtime
        .store
        .create("alice", "operator-password", "user")
        .unwrap();
    let user = runtime.store.get("alice").unwrap();
    let token = crypto::sign(
        &crypto::Claims {
            username: "alice".into(),
            sub: "alice".into(),
            token_version: user.token_version,
            exp: u64::MAX,
            iat: None,
            nbf: None,
        },
        &runtime.config.jwt.secret_key,
    )
    .unwrap();
    let router = app(runtime.clone(), root.path().join("web"));
    async fn get(router: &axum::Router, token: Option<&str>) -> (StatusCode, Value) {
        let mut request = Request::builder().uri("/api/vm/dashboard");
        if let Some(token) = token {
            request = request.header("authorization", format!("Bearer {token}"))
        }
        let mut request = request.body(Body::empty()).unwrap();
        request.extensions_mut().insert(ConnectInfo(
            "127.0.0.1:1234".parse::<std::net::SocketAddr>().unwrap(),
        ));
        let response = router.clone().oneshot(request).await.unwrap();
        let status = response.status();
        let data = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        (status, serde_json::from_slice(&data).unwrap())
    }
    assert_eq!(get(&router, None).await.0, StatusCode::UNAUTHORIZED);
    let (status, value) = get(&router, Some(&token)).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(value["code"], 0);
    assert_eq!(value["data"]["hardware"], "Alpha");
    assert_eq!(value["data"]["application"], "1.0.0");
    assert_eq!(value["data"]["image"], "");
    assert_eq!(value["data"]["memory"], Value::Null);
    assert_eq!(
        value["data"]["system"]["storage"].as_array().unwrap().len(),
        3
    );
    assert_eq!(value["data"]["system"]["interfaces"], serde_json::json!([]));
    fs::create_dir(root.path().join("proc")).unwrap();
    fs::write(
        root.path().join("proc/meminfo"),
        "MemTotal: 1024 kB\nMemAvailable: 768 kB\n",
    )
    .unwrap();
    fs::write(
        root.path().join("proc/swaps"),
        "Filename Type Size Used Priority\n",
    )
    .unwrap();
    assert_eq!(
        get(&router, Some(&token)).await.1["data"]["memory"]["totalBytes"],
        1024 * 1024
    );
    runtime.shutdown();
}
