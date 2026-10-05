use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::{Request, StatusCode},
    Router,
};
use base64::{engine::general_purpose::STANDARD, Engine};
use nanokvm_server::{
    app,
    composition::{self, Composition, Mode, Snapshot, Transaction, FLAGS},
    monitor::{self, Backend, Monitor},
    systemops::{Action, Executor},
    Error, Runtime,
};
use serde_json::{json, Value};
use std::{
    fs,
    os::unix::fs::{symlink, PermissionsExt},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::Duration,
};
use tower::ServiceExt;
const ORACLE: &str = include_str!("../../docs/experiments/v3.0/usb-go-oracle.json");
fn base() -> Composition {
    Composition {
        mode: Mode::Normal,
        keyboard: true,
        relative: true,
        absolute: true,
        network: false,
        disk: false,
        serial: false,
        audio: false,
        windows_pointer: false,
    }
}
fn roots(root: &Path) {
    for path in [
        "boot",
        "etc/init.d",
        "etc/kvm",
        "usr/libexec/nanokvm/legacy",
        "kvmapp/system/init.d",
        "sys/kernel/config/usb_gadget/g0/configs/c.1",
        "sys/kernel/config/usb_gadget/g0/functions/hid.GS2",
        "sys/kernel/config/usb_gadget/g0/os_desc",
        "usr/share/nanokvm/edid",
        "run",
        "dev",
        "web",
    ] {
        fs::create_dir_all(root.join(path)).unwrap();
    }
    fs::write(root.join("usr/libexec/nanokvm/legacy/S03usbdev"), "normal").unwrap();
    fs::set_permissions(
        root.join("usr/libexec/nanokvm/legacy/S03usbdev"),
        fs::Permissions::from_mode(0o751),
    )
    .unwrap();
    symlink(
        "/usr/libexec/nanokvm/legacy/S03usbdev",
        root.join("etc/init.d/S03usbdev"),
    )
    .unwrap();
    fs::write(root.join("kvmapp/system/init.d/S03usbdev"), "normal").unwrap();
    fs::write(root.join("kvmapp/system/init.d/S03usbhid"), "hid-only").unwrap();
    fs::write(
        root.join("etc/kvm/server.yaml"),
        "authentication: disable\nproto: http\nsecurity:\n  trustedProxies: []\n",
    )
    .unwrap();
    fs::write(root.join("etc/inittab"), "# keep\n acm::respawn:/sbin/getty -L ttyGS0 115200 vt100\nconsole::respawn:/sbin/getty ttyS0\n").unwrap();
    fs::set_permissions(root.join("etc/inittab"), fs::Permissions::from_mode(0o640)).unwrap();
    fs::write(root.join("etc/kvm/hw"), "pcie\n").unwrap();
    fs::write(root.join("etc/kvm/hdmi_version"), "ux\n").unwrap();
    fs::write(
        root.join("sys/kernel/config/usb_gadget/g0/os_desc/container_id"),
        "",
    )
    .unwrap();
    let data: Value = serde_json::from_str(ORACLE).unwrap();
    fs::write(
        root.join("usr/share/nanokvm/edid/NanoKVM-stock.bin"),
        STANDARD
            .decode(data["edids"][0]["input"].as_str().unwrap())
            .unwrap(),
    )
    .unwrap();
    fs::write(root.join("web/index.html"), "usb-fixture").unwrap();
    for i in 0..3 {
        fs::write(root.join(format!("dev/hidg{i}")), []).unwrap();
    }
}
#[test]
fn exhaustive_compositions_and_edids_match_actual_baseline_go() {
    let oracle: Value = serde_json::from_str(ORACLE).unwrap();
    for case in oracle["compositions"].as_array().unwrap() {
        let mask = case["mask"].as_u64().unwrap();
        let flag = |i: u32| -> bool { mask & (1u64 << i) != 0u64 };
        let state = Composition {
            mode: Mode::parse(case["mode"].as_str().unwrap()).unwrap(),
            keyboard: flag(0),
            relative: flag(1),
            absolute: flag(2),
            network: flag(3),
            disk: flag(4),
            serial: flag(5),
            audio: flag(6),
            windows_pointer: flag(7),
        };
        assert_eq!(state.revision(), case["revision"].as_str().unwrap());
        let (input, output) = state.usage();
        assert_eq!(json!([input, output]), json!([case["in"], case["out"]]));
        assert_eq!(
            state
                .validate()
                .err()
                .map(|e| e.to_string())
                .unwrap_or_default(),
            case["error"].as_str().unwrap()
        );
    }
    let id = monitor::parse_container_id("2ca7b40c-7bd1-4f25-b573-a13a975ddc07\n").unwrap();
    for case in oracle["edids"].as_array().unwrap() {
        let input = STANDARD.decode(case["input"].as_str().unwrap()).unwrap();
        let result = monitor::decorate(&input, &id);
        if case["error"] == "" {
            assert_eq!(
                result.unwrap(),
                STANDARD.decode(case["output"].as_str().unwrap()).unwrap(),
                "{}",
                case["name"]
            );
        } else {
            assert_eq!(
                result.unwrap_err().to_string(),
                case["error"].as_str().unwrap(),
                "{}",
                case["name"]
            );
        }
    }
    for bad in [
        "",
        "00000000-0000-0000-0000-000000000000",
        "2ca7b40c7bd14f25b573a13a975ddc07",
        "2ca7b40c-7bd1-4f25-b573-a13a975ddc0z",
    ] {
        assert!(monitor::parse_container_id(bad).is_err());
    }
}
#[test]
fn transaction_preserves_iso_script_link_and_exact_rollback_at_every_phase() {
    for phase in ["", "install", "stop", "start", "verify", "rollback"] {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        roots(root);
        composition::restore(
            &root.join("boot/usb.disk0"),
            &Snapshot {
                exists: true,
                mode: 0o640,
                data: b"/data/images/rescue.iso\n".to_vec(),
            },
        )
        .unwrap();
        composition::restore(
            &root.join("boot/usb.ncm"),
            &Snapshot {
                exists: true,
                mode: 0o600,
                data: b"preserved network\n".to_vec(),
            },
        )
        .unwrap();
        let paths: Vec<_> = FLAGS
            .iter()
            .map(|name| root.join("boot").join(name))
            .chain(std::iter::once(
                root.join("usr/libexec/nanokvm/legacy/S03usbdev"),
            ))
            .collect();
        let saved: Vec<_> = paths
            .iter()
            .map(|path| composition::snapshot(path).unwrap())
            .collect();
        let current = Composition {
            network: true,
            disk: true,
            ..base()
        };
        let candidate = Composition {
            keyboard: false,
            relative: false,
            absolute: false,
            serial: true,
            audio: true,
            ..current
        };
        let calls = Mutex::new(Vec::new());
        let failed = std::cell::Cell::new(false);
        let fail = |name| -> Result<(), Error> {
            if !failed.get() && (phase == name || phase == "rollback" && name == "verify") {
                failed.set(true);
                Err(format!("injected {name}").into())
            } else {
                Ok(())
            }
        };
        let install = |_: Mode| {
            calls.lock().unwrap().push("install");
            fs::write(root.join("usr/libexec/nanokvm/legacy/S03usbdev"), "new")?;
            fail("install")
        };
        let run = |action| {
            let name = if action == Action::UsbStop {
                "stop"
            } else {
                "start"
            };
            calls.lock().unwrap().push(name);
            fail(name)
        };
        let verify = |state| {
            calls.lock().unwrap().push("verify");
            if phase == "rollback" && state == current {
                Err("rollback gadget unbound".into())
            } else {
                fail("verify")
            }
        };
        let tx = Transaction {
            root,
            install: &install,
            run: &run,
            verify: &verify,
        };
        let result = tx.apply(current, candidate);
        if phase.is_empty() {
            result.unwrap();
            assert_eq!(
                *calls.lock().unwrap(),
                ["install", "stop", "start", "verify"]
            );
            for i in [1, 2] {
                assert_eq!(composition::snapshot(&paths[i]).unwrap(), saved[i]);
            }
            assert!(root.join("boot/usb.acm").exists());
            assert!(root.join("boot/disable_hid").exists());
        } else {
            let error = result.unwrap_err().to_string();
            assert!(error.contains("injected"));
            if phase == "rollback" {
                assert!(error.contains("restoring USB configuration also failed"));
            }
            for (path, want) in paths.iter().zip(&saved) {
                assert_eq!(
                    &composition::snapshot(path).unwrap(),
                    want,
                    "{}: {}",
                    phase,
                    path.display()
                );
            }
        }
        assert_eq!(
            fs::read_link(root.join("etc/init.d/S03usbdev")).unwrap(),
            Path::new("/usr/libexec/nanokvm/legacy/S03usbdev")
        );
        let count = calls.lock().unwrap().len();
        tx.apply(current, current).unwrap();
        assert_eq!(calls.lock().unwrap().len(), count);
        assert!(tx
            .apply(
                current,
                Composition {
                    serial: true,
                    ..current
                }
            )
            .is_err());
        assert_eq!(calls.lock().unwrap().len(), count);
    }
}
#[test]
fn corrupt_marker_empty_gadget_and_remote_defaults_are_checked() {
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path();
    roots(root);
    symlink("/outside-image", root.join("boot/usb.disk0")).unwrap();
    let never = |_: Action| -> Result<(), Error> { panic!("must reject before commands") };
    let tx = Transaction {
        root,
        install: &|_| panic!("must reject before install"),
        run: &never,
        verify: &|_| Ok(()),
    };
    assert!(tx
        .apply(
            base(),
            Composition {
                network: true,
                ..base()
            }
        )
        .unwrap_err()
        .to_string()
        .contains("not a regular file"));
    let gadget = root.join("sys/kernel/config/usb_gadget/g0");
    let empty = Composition {
        keyboard: false,
        relative: false,
        absolute: false,
        ..base()
    };
    fs::write(gadget.join("UDC"), "\n").unwrap();
    composition::verify(root, empty).unwrap();
    fs::write(gadget.join("UDC"), "4340000.usb").unwrap();
    assert!(composition::verify(root, empty).is_err());
    let full = Composition {
        network: true,
        disk: true,
        serial: true,
        ..empty
    };
    assert!(full.remote_access_defaults(true).validate().is_err());
    let audio = Composition {
        windows_pointer: true,
        disk: true,
        ..empty
    }
    .remote_access_defaults(true);
    assert!(audio.audio && audio.windows_pointer && audio.disk && audio.keyboard);
    assert_eq!(audio.remote_access_defaults(false), audio);
}
struct Fixture {
    root: PathBuf,
    calls: Mutex<Vec<Action>>,
    fail: Mutex<Option<Action>>,
    bad_verify: Mutex<bool>,
    profiles: Mutex<Vec<Vec<u8>>>,
    audio_calls: Mutex<usize>,
}
impl Executor for Fixture {
    fn run(&self, action: Action, timeout: Duration) -> Result<(), Error> {
        assert_eq!(
            timeout,
            Duration::from_secs(if action == Action::ReloadInit { 10 } else { 20 })
        );
        self.calls.lock().unwrap().push(action);
        if self.fail.lock().unwrap().as_ref() == Some(&action) {
            self.fail.lock().unwrap().take();
            return Err("injected command failure".into());
        }
        let gadget = self.root.join("sys/kernel/config/usb_gadget/g0");
        if action == Action::UsbStop {
            fs::write(gadget.join("UDC"), "")?;
            for i in 0..3 {
                let _ = fs::rename(
                    self.root.join(format!("dev/hidg{i}")),
                    self.root.join(format!("dev/old-hidg{i}")),
                );
            }
        }
        if action == Action::UsbStart {
            let mode = fs::read_to_string(self.root.join("usr/libexec/nanokvm/legacy/S03usbdev"))?;
            fs::write(
                gadget.join("bcdDevice"),
                if mode == "hid-only" {
                    "0x0720"
                } else {
                    "0x0710"
                },
            )?;
            for entry in fs::read_dir(gadget.join("configs/c.1"))? {
                fs::remove_file(entry?.path())?;
            }
            let s = Composition::read(&self.root);
            let flags = [
                ("hid.GS0", s.keyboard),
                ("hid.GS1", s.relative),
                ("hid.GS2", s.absolute),
                ("ncm.usb0", s.network),
                ("mass_storage.disk0", s.disk),
                ("acm.GS0", s.serial),
                ("uac1.audio0", s.audio),
            ];
            for (name, enabled) in flags {
                let path = gadget.join("configs/c.1").join(name);
                let _ = fs::remove_file(&path);
                if enabled {
                    symlink(format!("../../functions/{name}"), path)?;
                }
            }
            for (i, enabled) in [s.keyboard, s.relative, s.absolute].into_iter().enumerate() {
                if enabled {
                    fs::write(self.root.join(format!("dev/hidg{i}")), [])?;
                }
            }
            let bad = std::mem::take(&mut *self.bad_verify.lock().unwrap());
            fs::write(
                gadget.join("UDC"),
                if s.empty() || bad { "" } else { "4340000.usb" },
            )?;
            fs::write(
                gadget.join("functions/hid.GS2/report_desc"),
                [0x05, if s.windows_pointer { 0x0d } else { 1 }],
            )?;
        }
        Ok(())
    }
}
impl Backend for Fixture {
    fn stop_audio(&self) -> Result<(), Error> {
        *self.audio_calls.lock().unwrap() += 1;
        Ok(())
    }
    fn apply_monitor_profile(&self, path: &Path) -> Result<(), Error> {
        assert!(path.starts_with(&self.root));
        self.profiles.lock().unwrap().push(fs::read(path)?);
        Ok(())
    }
}
fn fixture() -> (tempfile::TempDir, Arc<Fixture>, Arc<Runtime>, Router) {
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path();
    roots(root);
    let f = Arc::new(Fixture {
        root: root.to_path_buf(),
        calls: Mutex::new(vec![]),
        fail: Mutex::new(None),
        bad_verify: Mutex::new(false),
        profiles: Mutex::new(vec![]),
        audio_calls: Mutex::new(0),
    });
    f.run(Action::UsbStart, Duration::from_secs(20)).unwrap();
    f.calls.lock().unwrap().clear();
    let rt = Runtime::load_with_backends(root, f.clone(), f.clone()).unwrap();
    let router = app(rt.clone(), root.join("web"));
    (temp, f, rt, router)
}
async fn request(router: &Router, method: &str, data: &str, form: bool) -> (StatusCode, Value) {
    let mut request = Request::builder()
        .method(method)
        .uri("/api/vm/device/virtual")
        .header(
            "content-type",
            if form {
                "application/x-www-form-urlencoded"
            } else {
                "application/json"
            },
        )
        .body(Body::from(data.to_owned()))
        .unwrap();
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:38000".parse::<std::net::SocketAddr>().unwrap(),
    ));
    let response = router.clone().oneshot(request).await.unwrap();
    let status = response.status();
    let body = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    (status, serde_json::from_slice(&body).unwrap())
}
fn draft(state: Composition) -> Value {
    let mut value = state.response(false);
    value.as_object_mut().unwrap().remove("pointerProfile");
    value
}
#[tokio::test]
async fn usb_api_required_pointer_bools_nulls_forms_revisions_and_noop() {
    let (_temp, f, rt, router) = fixture();
    let (_, response) = request(&router, "GET", "", false).await;
    assert_eq!(response["data"], base().response(true));
    let valid = draft(base());
    assert_eq!(
        request(&router, "PUT", &valid.to_string(), false).await.1["code"],
        0
    );
    assert!(f.calls.lock().unwrap().is_empty());
    let s = valid.to_string();
    let mixed = s.replacen(
        "\"mode\":\"normal\"",
        "\"MODE\":\"normal\",\"mode\":null",
        1,
    );
    assert_eq!(request(&router, "PUT", &mixed, false).await.1["code"], 0);
    for name in [
        "keyboard", "relative", "absolute", "network", "disk", "serial", "audio",
    ] {
        let mut v = valid.clone();
        v.as_object_mut().unwrap().remove(name);
        assert_eq!(
            request(&router, "PUT", &v.to_string(), false).await.1["code"],
            -1
        );
        v[name] = Value::Null;
        assert_eq!(
            request(&router, "PUT", &v.to_string(), false).await.1["code"],
            -1
        );
    }
    let null = s.replacen(
        "\"keyboard\":true",
        "\"keyboard\":true,\"KEYBOARD\":null",
        1,
    );
    assert_eq!(request(&router, "PUT", &null, false).await.1["code"], -1);
    let mut v = valid.clone();
    v["revision"] = json!("stale");
    assert_eq!(
        request(&router, "PUT", &v.to_string(), false).await.1["code"],
        -5
    );
    v = valid.clone();
    v["serial"] = json!(true);
    v["network"] = json!(true);
    assert_eq!(
        request(&router, "PUT", &v.to_string(), false).await.1["code"],
        -4
    );
    let form=format!("Mode=normal&Revision={}&Keyboard=true&Relative=true&Absolute=true&Network=false&Disk=false&Serial=false&Audio=false",base().revision());
    assert_eq!(request(&router, "PUT", &form, true).await.1["code"], 0);
    assert_eq!(
        request(&router, "POST", "device=disk", true).await.1["code"],
        -1
    );
    assert_eq!(
        request(&router, "POST", r#"{"Device":"unknown"}"#, false)
            .await
            .1["code"],
        -2
    );
    assert!(f.calls.lock().unwrap().is_empty());
    rt.shutdown();
}
#[tokio::test]
async fn usb_api_rebind_and_rollback_preserve_iso_edid_and_permanent_serial_migration() {
    let (temp, f, rt, router) = fixture();
    let root = temp.path();
    composition::restore(
        &root.join("boot/usb.disk0"),
        &Snapshot {
            exists: true,
            mode: 0o640,
            data: b"/data/rescue.iso\n".to_vec(),
        },
    )
    .unwrap();
    f.run(Action::UsbStart, Duration::from_secs(20)).unwrap();
    f.calls.lock().unwrap().clear();
    let current = Composition::read(root);
    let mut v = draft(current);
    v["relative"] = json!(false);
    v["absolute"] = json!(false);
    v["serial"] = json!(true);
    v["pointerProfile"] = json!("windows");
    *f.bad_verify.lock().unwrap() = true;
    assert_eq!(
        request(&router, "PUT", &v.to_string(), false).await.1["code"],
        -3
    );
    assert_eq!(Composition::read(root), current);
    assert_eq!(
        fs::read(root.join("boot/usb.disk0")).unwrap(),
        b"/data/rescue.iso\n"
    );
    assert_eq!(
        *f.calls.lock().unwrap(),
        [
            Action::ReloadInit,
            Action::UsbStop,
            Action::UsbStart,
            Action::UsbStop,
            Action::UsbStart
        ]
    );
    assert_eq!(
        fs::read(root.join("etc/inittab")).unwrap(),
        b"# keep\nconsole::respawn:/sbin/getty ttyS0\n"
    );
    assert_eq!(
        fs::metadata(root.join("etc/inittab"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o640
    );
    {
        let profiles = f.profiles.lock().unwrap();
        assert_eq!(profiles.len(), 2);
        assert_eq!(
            profiles[1],
            fs::read(root.join("usr/share/nanokvm/edid/NanoKVM-stock.bin")).unwrap()
        );
    }
    let id = fs::read(root.join("etc/kvm/usb_container_id")).unwrap();
    assert_eq!(
        fs::metadata(root.join("etc/kvm/usb_container_id"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    f.calls.lock().unwrap().clear();
    let result = request(&router, "PUT", &v.to_string(), false).await.1;
    assert_eq!(result["code"], 0);
    assert_eq!(result["data"]["pointerProfile"], "windows");
    assert_eq!(
        *f.calls.lock().unwrap(),
        [Action::UsbStop, Action::UsbStart]
    );
    assert_eq!(fs::read(root.join("etc/kvm/usb_container_id")).unwrap(), id);
    assert_eq!(fs::read_dir(root.join("run")).unwrap().count(), 0);
    composition::verify(root, Composition::read(root)).unwrap();
    rt.shutdown();
}
#[tokio::test]
async fn legacy_toggle_and_hid_only_audio_share_transaction_and_budget() {
    let (_temp, f, rt, router) = fixture();
    let initial = base();
    let mut v = draft(initial);
    v["mode"] = json!("hid-only");
    v["keyboard"] = json!(false);
    v["relative"] = json!(false);
    v["absolute"] = json!(false);
    let result = request(&router, "PUT", &v.to_string(), false).await.1;
    assert_eq!(result["code"], 0);
    assert_eq!(result["data"]["mode"], "hid-only");
    assert_eq!(
        request(&router, "POST", "Device=network", true).await.1["code"],
        -4
    );
    let result = request(&router, "POST", "Device=audio", true).await.1;
    assert_eq!(result["code"], 0);
    assert_eq!(result["data"], json!({"on":true}));
    let current = Composition::read(&f.root);
    assert_eq!(current.mode, Mode::Normal);
    assert!(current.audio);
    composition::verify(&f.root, current).unwrap();
    *f.fail.lock().unwrap() = Some(Action::UsbStart);
    assert_eq!(
        request(&router, "POST", r#"{"DEVICE":"disk"}"#, false)
            .await
            .1["code"],
        -3
    );
    assert_eq!(Composition::read(&f.root), current);
    composition::verify(&f.root, current).unwrap();
    rt.shutdown();
}
#[test]
fn monitor_profiles_uuid_and_hardware_gates_are_real_and_fail_closed() {
    let (temp, f, rt, _router) = fixture();
    let root = temp.path();
    let monitor = Monitor::new(f.clone());
    assert!(monitor::pointer_supported(root));
    monitor.apply_pointer(root, true).unwrap();
    let id = fs::read(root.join("etc/kvm/usb_container_id")).unwrap();
    monitor.apply_pointer(root, true).unwrap();
    assert_eq!(fs::read(root.join("etc/kvm/usb_container_id")).unwrap(), id);
    let profiles = f.profiles.lock().unwrap();
    assert_eq!(profiles[0], profiles[1]);
    drop(profiles);
    for (board, chip) in [
        ("alpha", "c"),
        ("beta", "ux"),
        ("pcie", "c"),
        ("unknown", "ux"),
    ] {
        fs::write(root.join("etc/kvm/hw"), board).unwrap();
        fs::write(root.join("etc/kvm/hdmi_version"), chip).unwrap();
        assert!(!monitor::pointer_supported(root));
        assert!(monitor.apply_pointer(root, true).is_err());
    }
    rt.shutdown();
}

#[tokio::test]
async fn actual_socket_retains_lease_and_releases_old_node_during_composition() {
    use futures_util::{SinkExt, StreamExt};
    use tokio_tungstenite::{
        connect_async,
        tungstenite::{client::IntoClientRequest, Message},
    };
    let (temp, _f, rt, router) = fixture();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let app = router.clone();
    let task = tokio::spawn(async move {
        axum::serve(
            listener,
            app.into_make_service_with_connect_info::<std::net::SocketAddr>(),
        )
        .await
        .unwrap();
    });
    let mut upgrade = format!("ws://{address}/api/ws")
        .into_client_request()
        .unwrap();
    upgrade
        .headers_mut()
        .insert("origin", format!("http://{address}").parse().unwrap());
    let (mut socket, _) = connect_async(upgrade).await.unwrap();
    async fn control(
        socket: &mut tokio_tungstenite::WebSocketStream<
            tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>,
        >,
    ) -> Value {
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                if let Message::Text(text) = socket.next().await.unwrap().unwrap() {
                    let event: Value = serde_json::from_str(&text).unwrap();
                    if event["type"] == "control" {
                        let data: Value =
                            serde_json::from_str(event["data"].as_str().unwrap()).unwrap();
                        if data["enabled"] == true {
                            return data;
                        }
                    }
                }
            }
        })
        .await
        .unwrap()
    }
    let owned = control(&mut socket).await;
    socket
        .send(Message::Binary(vec![1, 0, 0, 4, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    let path = temp.path().join("dev/hidg0");
    tokio::time::timeout(Duration::from_secs(3), async {
        while fs::read(&path).unwrap().len() < 8 {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(
        request(&router, "POST", r#"{"device":"audio"}"#, false)
            .await
            .1["code"],
        0
    );
    assert_eq!(control(&mut socket).await["lease"], owned["lease"]);
    let old = fs::read(temp.path().join("dev/old-hidg0")).unwrap();
    assert_eq!(&old[..8], &[0, 0, 4, 0, 0, 0, 0, 0]);
    assert_eq!(&old[old.len() - 8..], &[0; 8]);
    socket
        .send(Message::Binary(vec![1, 0, 0, 5, 0, 0, 0, 0, 0].into()))
        .await
        .unwrap();
    tokio::time::timeout(Duration::from_secs(3), async {
        while fs::read(&path).unwrap().len() < 8 {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(fs::read(&path).unwrap(), [0, 0, 5, 0, 0, 0, 0, 0]);
    socket.close(None).await.unwrap();
    rt.shutdown();
    task.abort();
}
#[test]
fn descriptor_reopen_failure_retains_the_original_operation_error() {
    let (temp, _f, rt, _) = fixture();
    for i in 0..3 {
        fs::remove_file(temp.path().join(format!("dev/hidg{i}"))).unwrap();
    }
    let error = rt
        .hid
        .reconfigure(false, || Err("original USB apply failure".into()))
        .unwrap_err()
        .to_string();
    assert!(error.contains("original USB apply failure"));
    assert!(error.contains("reopen HID devices"));
    rt.shutdown();
}
