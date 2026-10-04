use axum::{
    body::{to_bytes, Body},
    extract::ConnectInfo,
    http::{Request, StatusCode},
    Router,
};
use nanokvm_server::{
    app,
    config::Config,
    crypto,
    store::{Patch, Store},
    Runtime,
};
use serde_json::{json, Value};
use std::{fs, net::SocketAddr, sync::Arc};
use tower::ServiceExt;

const ENCRYPTED: &str = "U2FsdGVkX18zLUxaLNGy7jL96oMO4tq6wDYwVzUMO3XfTY2Zy/ipO4LDEqtBT+fx";
fn fixture() -> (tempfile::TempDir, Arc<Runtime>, Router) {
    let temp = tempfile::tempdir().unwrap();
    let etc = temp.path().join("etc/kvm");
    fs::create_dir_all(&etc).unwrap();
    fs::write(etc.join("server.yaml"),"proto: http\nhost: 127.0.0.1\nport:\n  http: 38080\n  https: 38443\njwt:\n  secretKey: contract-secret\n  revokeTokensOnLogout: true\n").unwrap();
    fs::write(
        etc.join("pwd"),
        json!({"username":"owner","password":ENCRYPTED}).to_string(),
    )
    .unwrap();
    let web = temp.path().join("web");
    fs::create_dir_all(&web).unwrap();
    fs::write(web.join("index.html"), "contract-ui").unwrap();
    let state = Runtime::load(temp.path()).unwrap();
    let router = app(state.clone(), web);
    (temp, state, router)
}
async fn request(
    app: &Router,
    method: &str,
    path: &str,
    body: Value,
    headers: &[(&str, &str)],
) -> (StatusCode, axum::http::HeaderMap, Value) {
    let mut r = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json");
    for (k, v) in headers {
        r = r.header(*k, *v);
    }
    let mut r = r.body(Body::from(body.to_string())).unwrap();
    r.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:34000".parse::<SocketAddr>().unwrap(),
    ));
    let response = app.clone().oneshot(r).await.unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    let bytes = to_bytes(response.into_body(), 2 << 20).await.unwrap();
    let value = serde_json::from_slice(&bytes)
        .unwrap_or_else(|_| String::from_utf8_lossy(&bytes).into_owned().into());
    (status, headers, value)
}
async fn login(app: &Router, name: &str) -> String {
    let (status, headers, body) = request(
        app,
        "POST",
        "/api/auth/login",
        json!({"username":name,"password":ENCRYPTED}),
        &[("x-nanokvm-return-token", "true")],
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["code"], 0);
    assert_eq!(headers["cache-control"], "no-store");
    body["data"]["token"].as_str().unwrap().into()
}
#[tokio::test]
async fn ui_cookie_legacy_migration_and_revocation() {
    let (_temp, state, app) = fixture();
    let (_, headers, v) = request(
        &app,
        "POST",
        "/api/auth/login",
        json!({"username":"owner","password":ENCRYPTED}),
        &[],
    )
    .await;
    assert_eq!(v, json!({"code":0,"msg":"success","data":null}));
    let cookie = headers["set-cookie"].to_str().unwrap();
    assert!(cookie.contains("HttpOnly; SameSite=Strict"));
    assert!(!cookie.contains("Secure"));
    let cookie = cookie.split(';').next().unwrap();
    assert!(state.store.get("owner").unwrap().hash.starts_with("$2b$"));
    let (_, _, v) = request(
        &app,
        "GET",
        "/api/auth/account",
        Value::Null,
        &[("cookie", cookie)],
    )
    .await;
    assert_eq!(
        v["data"],
        json!({"username":"owner","role":"admin","mustChangePassword":false})
    );
    let (status, _, v) = request(
        &app,
        "GET",
        "/api/auth/account",
        Value::Null,
        &[("cookie", cookie), ("authorization", "Bearer ")],
    )
    .await;
    assert_eq!(status, StatusCode::UNAUTHORIZED);
    assert_eq!(v, "unauthorized");
    request(
        &app,
        "POST",
        "/api/auth/logout",
        Value::Null,
        &[("cookie", cookie)],
    )
    .await;
    assert_eq!(
        request(
            &app,
            "GET",
            "/api/auth/account",
            Value::Null,
            &[("cookie", cookie)]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
}
#[tokio::test]
async fn user_roles_mutations_and_owner_policy() {
    let (_temp, state, app) = fixture();
    let admin = login(&app, "owner").await;
    let auth = format!("Bearer {admin}");
    let (_, _, v) = request(
        &app,
        "POST",
        "/api/auth/users",
        json!({"username":"alice","password":ENCRYPTED,"role":"user"}),
        &[("authorization", &auth)],
    )
    .await;
    assert_eq!(v["code"], 0);
    let alice = login(&app, "alice").await;
    let alice_auth = format!("Bearer {alice}");
    assert_eq!(
        request(
            &app,
            "GET",
            "/api/auth/users",
            Value::Null,
            &[("authorization", &alice_auth)]
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let (_, _, v) = request(
        &app,
        "GET",
        "/api/auth/users",
        Value::Null,
        &[("authorization", &auth)],
    )
    .await;
    assert!(v["data"]["users"]
        .as_array()
        .unwrap()
        .iter()
        .all(|u| u.get("password").is_none() && u.get("tokenVersion").is_none()));
    let (_, _, v) = request(
        &app,
        "PUT",
        "/api/auth/users/owner",
        json!({"role":"user"}),
        &[("authorization", &auth)],
    )
    .await;
    assert_eq!(v["code"], -2);
    let (_, _, v) = request(
        &app,
        "POST",
        "/api/auth/users/owner/password",
        json!({"password":ENCRYPTED}),
        &[("authorization", &auth)],
    )
    .await;
    assert_eq!(v["code"], -3);
    // Idempotent updates retain existing sessions; effective updates revoke.
    request(
        &app,
        "PUT",
        "/api/auth/users/alice",
        json!({"enabled":true}),
        &[("authorization", &auth)],
    )
    .await;
    assert_eq!(
        request(
            &app,
            "GET",
            "/api/auth/account",
            Value::Null,
            &[("authorization", &alice_auth)]
        )
        .await
        .0,
        StatusCode::OK
    );
    request(
        &app,
        "PUT",
        "/api/auth/users/alice",
        json!({"enabled":false}),
        &[("authorization", &auth)],
    )
    .await;
    assert_eq!(
        request(
            &app,
            "GET",
            "/api/auth/account",
            Value::Null,
            &[("authorization", &alice_auth)]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    assert!(!state.store.get("alice").unwrap().enabled);
}
#[tokio::test]
async fn factory_password_gate_and_corrupt_store_fail_closed() {
    let (temp, state, app) = fixture();
    let token = login(&app, "owner").await;
    let auth = format!("Bearer {token}");
    let file = temp.path().join("etc/kvm/pwd");
    let mut db: Value = serde_json::from_slice(&fs::read(&file).unwrap()).unwrap();
    db["users"][0]["mustChangePassword"] = true.into();
    fs::write(&file, db.to_string()).unwrap();
    let (status, _, v) = request(
        &app,
        "GET",
        "/api/vm/screen",
        Value::Null,
        &[("authorization", &auth)],
    )
    .await;
    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(v["code"], -10);
    assert_eq!(
        request(
            &app,
            "GET",
            "/api/auth/account",
            Value::Null,
            &[("authorization", &auth)]
        )
        .await
        .0,
        StatusCode::OK
    );
    fs::write(&file, b"broken").unwrap();
    assert_eq!(
        request(
            &app,
            "GET",
            "/api/auth/account",
            Value::Null,
            &[("authorization", &auth)]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    fs::remove_file(&file).unwrap();
    assert_ne!(
        state.store.get("admin").unwrap().token_version,
        state.store.get("admin").unwrap().token_version
    );
    assert_eq!(
        request(
            &app,
            "GET",
            "/api/auth/account",
            Value::Null,
            &[("authorization", &auth)]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
}
#[tokio::test]
async fn every_baseline_nonpublic_route_is_protected() {
    let (_temp, _, app) = fixture();
    let routes: Vec<Value> = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/routes-baseline.json"
    ))
    .unwrap();
    for route in routes
        .into_iter()
        .filter(|r| r["authorization"] != "public")
    {
        let method = route["method"].as_str().unwrap();
        let method = if method == "ANY" { "POST" } else { method };
        let path = route["path"]
            .as_str()
            .unwrap()
            .split('/')
            .map(|p| if p.starts_with(':') { "contract-id" } else { p })
            .collect::<Vec<_>>()
            .join("/");
        assert_eq!(
            request(&app, method, &path, Value::Null, &[]).await.0,
            StatusCode::UNAUTHORIZED,
            "{method} {path}"
        );
    }
}
#[tokio::test]
async fn static_file_cache_and_symlink_confinement() {
    let (temp, _, app) = fixture();
    let (status, h, body) = request(&app, "GET", "/", Value::Null, &[]).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body, "contract-ui");
    assert!(h["cache-control"].to_str().unwrap().contains("no-store"));
    fs::write(temp.path().join("secret"), "confidential").unwrap();
    std::os::unix::fs::symlink(temp.path().join("secret"), temp.path().join("web/external"))
        .unwrap();
    assert_eq!(
        request(&app, "GET", "/external", Value::Null, &[]).await.0,
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        request(&app, "GET", "/%2e%2e/secret", Value::Null, &[])
            .await
            .0,
        StatusCode::NOT_FOUND
    );
}
#[test]
fn system_password_failure_rolls_back_account_and_session() {
    let (_temp, state, _) = fixture();
    state
        .store
        .authenticate("owner", "operator-password")
        .unwrap();
    let previous = fs::read(&state.store.path).unwrap();
    assert!(state
        .store
        .password("owner", "another-password", || Err(
            "simulated system update failure".into()
        ))
        .is_err());
    assert_eq!(fs::read(&state.store.path).unwrap(), previous);
}
#[test]
fn last_admin_owner_and_legacy_mirror_survive_rename() {
    let (_temp, state, _) = fixture();
    state
        .store
        .authenticate("owner", "operator-password")
        .unwrap();
    state
        .store
        .update(
            "owner",
            "owner",
            Patch {
                username: Some("renamed".into()),
                ..Default::default()
            },
        )
        .unwrap();
    let v: Value = serde_json::from_slice(&fs::read(&state.store.path).unwrap()).unwrap();
    assert_eq!(v["username"], "renamed");
    assert_eq!(v["password"], v["users"][0]["password"]);
    assert!(state.store.delete("other", "renamed").is_err());
    let no_owner = tempfile::tempdir().unwrap();
    let store = Store::new(no_owner.path().join("pwd"));
    fs::write(&store.path,json!({"version":1,"users":[{"username":"admin","password":"hash","role":"admin","enabled":true,"tokenVersion":1}]}).to_string()).unwrap();
    assert!(store
        .update(
            "other",
            "admin",
            Patch {
                role: Some("user".into()),
                ..Default::default()
            }
        )
        .is_err());
}
#[test]
fn config_is_preserved_and_secret_is_persistent_private() {
    use std::os::unix::fs::PermissionsExt;
    let temp = tempfile::tempdir().unwrap();
    let first = Config::load(temp.path()).unwrap();
    let second = Config::load(temp.path()).unwrap();
    assert_eq!(first.jwt.secret_key, second.jwt.secret_key);
    let key = temp.path().join("etc/kvm/.jwt_secret");
    assert_eq!(
        fs::metadata(key).unwrap().permissions().mode() & 0o777,
        0o600
    );
    let config = temp.path().join("etc/kvm/server.yaml");
    let data="proto: https\nport:\n  http: 38080\n  https: 38443\ncustom-setting: keep-this\nsecurity:\n  trustedProxies: []\n";
    fs::write(&config, data).unwrap();
    let c = Config::load(temp.path()).unwrap();
    assert!(c.security.trusted_proxies.is_empty());
    assert_eq!(fs::read_to_string(&config).unwrap(), data);
    fs::write(&config, "proto: invalid\n").unwrap();
    assert!(Config::load(temp.path()).is_err());
    assert_eq!(fs::read_to_string(config).unwrap(), "proto: invalid\n");
}

#[test]
fn viper_compatible_yaml_keys_nulls_and_unset_logout_flag() {
    let temp = tempfile::tempdir().unwrap();
    let etc = temp.path().join("etc/kvm");
    fs::create_dir_all(&etc).unwrap();
    let yaml = "PROTO: http\nPORT:\n  HTTP: 38080\n  HTTPS: 38443\nJWT:\n  secretkey: fixed-test-key\n  refreshtokenduration: 3600\nSECURITY:\n  LOGINMAXFAILURES: -5\n  TRUSTEDPROXIES: null\nTURN:\n  TURNADDR: turn:example.invalid:3478\n  TURNUSER: test-only\n  TURNCRED: dummy\nALPINE:\n  BUILDERURL: https://example.invalid/\ncustom-setting: retained\n";
    fs::write(etc.join("server.yaml"), yaml).unwrap();
    let c = Config::load(temp.path()).unwrap();
    assert_eq!(c.proto, "http");
    assert_eq!(c.port.http, 38080);
    assert_eq!(c.jwt.secret_key, "fixed-test-key");
    assert_eq!(c.jwt.refresh_token_duration, 3600);
    assert!(!c.jwt.revoke_tokens_on_logout); // Viper's zero value with an explicit key.
    assert_eq!(c.security.login_max_failures, 5);
    assert_eq!(c.security.trusted_proxies, ["127.0.0.1/32", "::1/128"]);
    assert_eq!(c.turn.turn_addr, "turn:example.invalid:3478");
    assert_eq!(c.alpine.builder_url, "https://example.invalid/");
    assert_eq!(fs::read_to_string(etc.join("server.yaml")).unwrap(), yaml);
    fs::write(etc.join("server.yaml"), "proto: http\nPROTO: https\n").unwrap();
    assert!(Config::load(temp.path()).is_err());
}
#[test]
fn go_jwt_claims_and_bcrypt_formats() {
    let claims = crypto::Claims {
        username: "legacy".into(),
        sub: "legacy".into(),
        token_version: 42,
        exp: u64::MAX,
        iat: None,
        nbf: None,
    };
    assert!(crypto::verify(&crypto::sign(&claims, "test").unwrap(), "test", 1).is_ok());
    assert!(serde_json::from_str::<crypto::Claims>(
        r#"{"username":"legacy","sub":"legacy","tokenVersion":42}"#
    )
    .is_err());
    let hash = bcrypt::hash("operator-password", 10).unwrap();
    assert!(bcrypt::verify("operator-password", &hash.replacen("$2b$", "$2a$", 1)).unwrap());
}

#[tokio::test]
async fn exact_go_oracle_responses_with_go_tokens_and_hashes() {
    let temp = tempfile::tempdir().unwrap();
    let etc = temp.path().join("etc/kvm");
    fs::create_dir_all(&etc).unwrap();
    let fixture: Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/auth-go-oracle.json"
    ))
    .unwrap();
    fs::write(etc.join("server.yaml"),"proto: http\nport:\n  http: 38080\n  https: 38443\njwt:\n  secretKey: v3-test-only-secret\n").unwrap();
    fs::write(etc.join("pwd"), fixture["database"].to_string()).unwrap();
    let state = Runtime::load(temp.path()).unwrap();
    let web = temp.path().join("web");
    fs::create_dir_all(&web).unwrap();
    let router = app(state, web);
    for case in fixture["cases"].as_array().unwrap() {
        let auth = case["token"].as_str().map(|t| format!("Bearer {t}"));
        let headers = auth
            .as_ref()
            .map(|h| vec![("authorization", h.as_str())])
            .unwrap_or_default();
        let (status, _, response) = request(
            &router,
            case["method"].as_str().unwrap(),
            case["path"].as_str().unwrap(),
            serde_json::from_str(case["body"].as_str().unwrap()).unwrap(),
            &headers,
        )
        .await;
        assert_eq!(
            status.as_u16() as u64,
            case["status"].as_u64().unwrap(),
            "{}",
            case["name"]
        );
        assert_eq!(response, case["response"], "{}", case["name"]);
    }
}

#[tokio::test]
async fn form_login_and_self_password_change_revoke_the_old_session() {
    let (_temp, state, app) = fixture();
    state
        .store
        .create("viewer", "operator-password", "user")
        .unwrap();
    let form =
        serde_urlencoded::to_string([("username", "viewer"), ("password", ENCRYPTED)]).unwrap();
    let mut req = Request::builder()
        .method("POST")
        .uri("/api/auth/login")
        .header("content-type", "application/x-www-form-urlencoded")
        .header("x-nanokvm-return-token", "true")
        .header("x-forwarded-proto", "https")
        .body(Body::from(form))
        .unwrap();
    req.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:34000".parse::<SocketAddr>().unwrap(),
    ));
    let response = app.clone().oneshot(req).await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert!(response.headers()["set-cookie"]
        .to_str()
        .unwrap()
        .contains("; Secure"));
    let body: Value =
        serde_json::from_slice(&to_bytes(response.into_body(), 1 << 20).await.unwrap()).unwrap();
    let auth = format!("Bearer {}", body["data"]["token"].as_str().unwrap());
    let (_, _, v) = request(
        &app,
        "POST",
        "/api/auth/password",
        json!({"password":ENCRYPTED, "currentPassword":ENCRYPTED}),
        &[("authorization", &auth)],
    )
    .await;
    assert_eq!(v["code"], 0);
    assert_eq!(
        request(
            &app,
            "GET",
            "/api/auth/account",
            Value::Null,
            &[("authorization", &auth)]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    let (_, headers, v) = request(
        &app,
        "POST",
        "/api/auth/login",
        json!({"username":"viewer","password":ENCRYPTED}),
        &[("x-forwarded-proto", "https")],
    )
    .await;
    assert_eq!(v["code"], 0);
    assert!(headers["set-cookie"].to_str().unwrap().contains("; Secure"));
}

#[tokio::test]
async fn configured_authentication_disable_and_untrusted_forwarding_are_preserved() {
    let (temp, _, _) = fixture();
    let config = temp.path().join("etc/kvm/server.yaml");
    fs::write(
        &config,
        "proto: http\nport:\n  http: 38080\n  https: 38443\nauthentication: disable\n",
    )
    .unwrap();
    let app = app(Runtime::load(temp.path()).unwrap(), temp.path().join("web"));
    let (_, headers, v) = request(&app, "POST", "/api/auth/login", Value::Null, &[]).await;
    assert_eq!(v["code"], 0);
    assert!(!headers.contains_key("set-cookie"));
    let (_, _, v) = request(&app, "GET", "/api/auth/account", Value::Null, &[]).await;
    assert_eq!(v["data"]["role"], "admin");
    assert_eq!(
        request(&app, "GET", "/api/vm/screen", Value::Null, &[])
            .await
            .0,
        StatusCode::NOT_IMPLEMENTED
    );
    fs::write(
        &config,
        "proto: http\nport:\n  http: 38080\n  https: 38443\nsecurity:\n  trustedProxies: []\n",
    )
    .unwrap();
    let app = nanokvm_server::app(Runtime::load(temp.path()).unwrap(), temp.path().join("web"));
    let (_, headers, v) = request(
        &app,
        "POST",
        "/api/auth/login",
        json!({"username":"owner","password":ENCRYPTED}),
        &[("x-forwarded-proto", "https")],
    )
    .await;
    assert_eq!(v["code"], 0);
    assert!(!headers["set-cookie"].to_str().unwrap().contains("; Secure"));
}
