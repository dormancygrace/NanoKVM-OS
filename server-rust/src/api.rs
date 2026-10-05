use crate::{config, crypto, store, Error, Runtime};
use axum::{
    body::to_bytes,
    extract::{ConnectInfo, Request, State},
    http::{HeaderMap, Method, StatusCode},
    response::{IntoResponse, Response},
    Json,
};
use serde::Deserialize;
use serde_json::{json, Value};
use std::{
    net::{IpAddr, SocketAddr},
    path::PathBuf,
    sync::Arc,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

#[derive(Clone, Deserialize)]
struct Route {
    method: String,
    path: String,
    authorization: String,
    input_owner: bool,
}
fn matched(method: &str, path: &str) -> Option<Route> {
    static ROUTES: std::sync::OnceLock<Vec<Route>> = std::sync::OnceLock::new();
    let routes = ROUTES.get_or_init(|| {
        serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/routes-baseline.json"
        ))
        .expect("checked source inventory")
    });
    routes
        .iter()
        .find(|r| {
            if r.method != method && r.method != "ANY" {
                return false;
            }
            let pattern: Vec<_> = r.path.split('/').collect();
            let actual: Vec<_> = path.split('/').collect();
            pattern.len() == actual.len()
                && pattern.iter().zip(actual).all(|(p, a)| {
                    if p.starts_with(':') {
                        !a.is_empty()
                    } else {
                        *p == a
                    }
                })
        })
        .cloned()
}
pub(crate) fn ok(data: Value) -> Response {
    Json(json!({"code":0,"msg":"success","data":data})).into_response()
}
pub(crate) fn error(code: i32, msg: &str) -> Response {
    Json(json!({"code":code,"msg":msg,"data":null})).into_response()
}
pub(crate) fn pending() -> Response {
    (
        StatusCode::NOT_IMPLEMENTED,
        Json(json!({"code":-1,"msg":"v3 migration pending","data":null})),
    )
        .into_response()
}
pub(crate) fn unauthorized() -> Response {
    (StatusCode::UNAUTHORIZED, Json("unauthorized")).into_response()
}
pub(crate) fn now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("system time before Unix epoch")
        .as_secs()
}
fn authenticate(s: &Runtime, headers: &HeaderMap) -> Result<store::User, Error> {
    Ok(principal(s, headers)?.user)
}
pub(crate) fn principal(
    s: &Runtime,
    headers: &HeaderMap,
) -> Result<crate::sessions::Principal, Error> {
    if s.config.authentication == "disable" {
        return Ok(crate::sessions::Principal {
            user: store::User {
                username: "admin".into(),
                hash: String::new(),
                role: "admin".into(),
                enabled: true,
                token_version: 0,
                must_change_password: false,
                system_account: false,
            },
            expires: None,
        });
    }
    let token = if let Some(header) = headers.get("authorization") {
        let h = header.to_str()?;
        if h.len() <= 7
            || !h
                .get(..7)
                .is_some_and(|v| v.eq_ignore_ascii_case("Bearer "))
        {
            return Err("malformed bearer token".into());
        }
        let t = h[7..].trim();
        if t.is_empty() || t.chars().any(char::is_whitespace) {
            return Err("malformed bearer token".into());
        }
        t.to_owned()
    } else {
        headers
            .get_all("cookie")
            .iter()
            .filter_map(|v| v.to_str().ok())
            .flat_map(|v| v.split(';'))
            .find_map(|p| p.trim().strip_prefix("nano-kvm-token="))
            .ok_or("missing session cookie")?
            .to_owned()
    };
    let c = crypto::verify(&token, &s.config.jwt.secret_key, now())?;
    let u = s.store.get(&c.username)?;
    if !u.enabled || u.token_version != c.token_version {
        return Err("session revoked".into());
    }
    Ok(crate::sessions::Principal {
        user: u,
        expires: Some(c.exp),
    })
}
fn secure_cookie(s: &Runtime, h: &HeaderMap, peer: IpAddr) -> bool {
    let trusted = s.config.security.trusted_proxies.iter().any(|p| {
        p.parse::<ipnet::IpNet>().is_ok_and(|n| n.contains(&peer))
            || p.parse::<IpAddr>().is_ok_and(|p| p == peer)
    });
    if trusted {
        if let Some(proto) = h
            .get("x-forwarded-proto")
            .and_then(|v| v.to_str().ok())
            .map(str::trim)
        {
            if proto.eq_ignore_ascii_case("http") {
                return false;
            }
            if proto.eq_ignore_ascii_case("https") {
                return true;
            }
        }
    }
    s.config.proto == "https"
}
fn cookie(response: &mut Response, value: &str, age: i64, secure: bool) {
    let age = age.max(0);
    response.headers_mut().insert(
        "set-cookie",
        format!(
            "nano-kvm-token={value}; Path=/; Max-Age={age}; HttpOnly; SameSite=Strict{}",
            if secure { "; Secure" } else { "" }
        )
        .parse()
        .expect("encoded JWT cookie"),
    );
}
fn fields(method: &Method, path: &str) -> &'static [&'static str] {
    match (method.as_str(), path) {
        ("POST", "/api/auth/login") => &["username", "password"],
        ("POST", "/api/auth/password") => &["password", "currentPassword"],
        ("POST", "/api/auth/users") => &["username", "password", "role"],
        ("POST", "/api/vm/web-title") => &["title"],
        ("POST", "/api/vm/hostname") => &["hostname"],
        ("POST", "/api/vm/oled") => &["sleep"],
        ("POST", "/api/vm/cpu-frequency") => &["target"],
        ("POST", "/api/hid/shortcut") => &["keys"],
        ("POST", "/api/hid/paste") => &["content", "langue"],
        ("POST", "/api/hid/mode") => &["mode"],
        ("POST", "/api/vm/device/virtual") => &["device"],
        ("PUT", "/api/vm/device/virtual") => &[
            "pointerProfile",
            "keyboard",
            "relative",
            "absolute",
            "network",
            "disk",
            "serial",
            "audio",
            "mode",
            "revision",
        ],
        ("DELETE", "/api/hid/shortcut") => &["id"],
        ("POST", "/api/hid/shortcut/leader-key") => &["key"],
        ("POST", "/api/vm/mouse-jiggler/") => &["enabled", "mode"],
        ("PUT", _) if path.starts_with("/api/auth/users/") => &["username", "role", "enabled"],
        ("POST", _) if path.starts_with("/api/auth/users/") => &["password"],
        _ => &[],
    }
}
fn params(
    headers: &HeaderMap,
    body: &[u8],
    method: &Method,
    path: &str,
    query: Option<&str>,
) -> Result<Value, Error> {
    if method != Method::GET
        && headers
            .get("content-type")
            .and_then(|v| v.to_str().ok())
            .is_some_and(|v| v.split(';').next() == Some("application/json"))
    {
        Ok(crate::binding::json(
            body,
            fields(method, path),
            method == Method::PUT && path.starts_with("/api/auth/users/"),
        )?)
    } else {
        let form = crate::form_binding::parameters(headers, body, method, query)?;
        let mut v = serde_json::Map::new();
        for (key, value) in form {
            let Some(&canonical) = fields(method, path).iter().find(|canonical| {
                key == match **canonical {
                    "keys" => "Keys",
                    "id" => "ID",
                    "key" => "Key",
                    "title" => "Title",
                    "hostname" => "Hostname",
                    "sleep" => "Sleep",
                    "target" => "Target",
                    "mode" => "Mode",
                    "device" => "Device",
                    "pointerProfile" => "PointerProfile",
                    "revision" => "Revision",
                    "keyboard" => "Keyboard",
                    "relative" => "Relative",
                    "absolute" => "Absolute",
                    "network" => "Network",
                    "disk" => "Disk",
                    "serial" => "Serial",
                    "audio" => "Audio",
                    "enabled" if path == "/api/vm/mouse-jiggler/" => "Enabled",
                    name => name,
                }
            }) else {
                continue;
            };
            if canonical != "keys" && v.contains_key(canonical) {
                continue;
            }
            if canonical == "keys" {
                let item = crate::binding::json_key(value.trim().as_bytes())?;
                v.entry("keys")
                    .or_insert_with(|| json!([]))
                    .as_array_mut()
                    .unwrap()
                    .push(item);
                continue;
            }
            let value = if matches!(canonical, "sleep" | "target") {
                let value = value.trim();
                let value = if value.is_empty() { "0" } else { value };
                Value::from(value.parse::<i64>()?)
            } else if matches!(
                canonical,
                "enabled"
                    | "keyboard"
                    | "relative"
                    | "absolute"
                    | "network"
                    | "disk"
                    | "serial"
                    | "audio"
            ) {
                Value::Bool(match value.trim() {
                    "1" | "t" | "T" | "true" | "TRUE" | "True" => true,
                    "" | "0" | "f" | "F" | "false" | "FALSE" | "False" => false,
                    _ => return Err("invalid boolean".into()),
                })
            } else {
                Value::String(value)
            };
            v.insert(canonical.into(), value);
        }
        Ok(Value::Object(v))
    }
}
fn string<'a>(v: &'a Value, key: &str) -> Result<&'a str, Error> {
    v[key].as_str().ok_or_else(|| "invalid parameters".into())
}
fn required<'a>(v: &'a Value, key: &str) -> Result<&'a str, Error> {
    string(v, key).and_then(|s| {
        if s.is_empty() {
            Err("invalid parameters".into())
        } else {
            Ok(s)
        }
    })
}
fn change_password(s: &Runtime, name: &str, encrypted: &str) -> Result<(), Error> {
    let password = crypto::decrypt(encrypted).map_err(|_| "invalid password")?;
    if password.is_empty() {
        return Err("invalid password".into());
    }
    let user = s.store.get(name)?;
    s.store.password(name, &password, || {
        if !user.system_account {
            return Ok(());
        }
        // Sandbox roots never invoke a host account command.
        if s.root != std::path::Path::new("/") {
            return Err("system password update unavailable in isolated root".into());
        }
        use std::{
            io::Write,
            process::{Command, Stdio},
            thread,
        };
        let mut child = Command::new("passwd")
            .arg("root")
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()?;
        let result = (|| -> Result<(), Error> {
            let input = child.stdin.as_mut().ok_or("password input unavailable")?;
            writeln!(input, "{password}")?;
            thread::sleep(Duration::from_millis(100));
            writeln!(input, "{password}")?;
            drop(child.stdin.take());
            let deadline = Instant::now() + Duration::from_secs(10);
            loop {
                if let Some(exit) = child.try_wait()? {
                    return if exit.success() {
                        Ok(())
                    } else {
                        Err("system password update failed".into())
                    };
                }
                if Instant::now() >= deadline {
                    return Err("system password update timed out".into());
                }
                thread::sleep(Duration::from_millis(20));
            }
        })();
        if result.is_err() {
            let _ = child.kill();
            let _ = child.wait();
        }
        result
    })
}

pub async fn dispatch(State((s, _)): State<(Arc<Runtime>, PathBuf)>, request: Request) -> Response {
    crate::jiggler::Jiggler::start(&s);
    if s.stopping.load(std::sync::atomic::Ordering::Acquire) {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    }
    let method = request.method().clone();
    let path = request.uri().path().to_owned();
    let query = request.uri().query().map(str::to_owned);
    let Some(route) = matched(method.as_str(), &path) else {
        return StatusCode::NOT_FOUND.into_response();
    };
    let peer = request
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .map(|c| c.0.ip());
    let Some(peer) = peer else {
        return StatusCode::INTERNAL_SERVER_ERROR.into_response();
    };
    let headers = request.headers().clone();
    let body = match to_bytes(request.into_body(), 1 << 20).await {
        Ok(b) => b,
        Err(_) => return error(-1, "invalid parameters"),
    };
    // Body buffering is async IO, not a blocking job. A slow/incomplete body
    // must not occupy all execution slots used by other API and socket work.
    let Ok(permit) = s.jobs.clone().try_acquire_owned() else {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    };
    let cancelled = Arc::new(crate::request_cancel::Cancellation::default());
    let _guard = crate::request_cancel::Guard(cancelled.clone());
    let result = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        let mut response = handle(
            &s,
            &route,
            &method,
            &path,
            &headers,
            peer,
            (&body, &cancelled, query.as_deref()),
        );
        if method == Method::POST && path == "/api/auth/login" {
            response
                .0
                .headers_mut()
                .insert("cache-control", "no-store".parse().unwrap());
            response
                .0
                .headers_mut()
                .insert("vary", "X-NanoKVM-Return-Token".parse().unwrap());
        }
        response
    })
    .await;
    match result {
        Ok((r, delay)) => {
            if !delay.is_zero() {
                tokio::time::sleep(delay).await;
            }
            r
        }
        Err(_) => (StatusCode::INTERNAL_SERVER_ERROR, Json("internal error")).into_response(),
    }
}
fn handle(
    s: &Runtime,
    route: &Route,
    method: &Method,
    path: &str,
    headers: &HeaderMap,
    peer: IpAddr,
    request_body: (&[u8], &crate::request_cancel::Cancellation, Option<&str>),
) -> (Response, Duration) {
    let (body, cancelled, query) = request_body;
    let result = || -> Response {
        if s.stopping.load(std::sync::atomic::Ordering::Acquire) {
            return StatusCode::SERVICE_UNAVAILABLE.into_response();
        }
        let user = if route.authorization == "public" {
            None
        } else if route.authorization == "loopback-internal-token" {
            if !s.internal.allowed(peer, headers) {
                return unauthorized();
            }
            None
        } else if route.authorization == "mcp-api-key" {
            return unauthorized();
        } else {
            let Ok(u) = authenticate(s, headers) else {
                return unauthorized();
            };
            let allowed = matches!(
                (method.as_str(), path),
                ("GET", "/api/auth/account")
                    | ("GET", "/api/auth/password")
                    | ("POST", "/api/auth/password")
                    | ("POST", "/api/auth/logout")
                    | ("GET", "/api/vm/web-title")
            );
            if u.must_change_password && !allowed {
                return (
                    StatusCode::FORBIDDEN,
                    Json(json!({"code":-10,"msg":"password change required"})),
                )
                    .into_response();
            }
            if route.authorization == "admin" && u.role != "admin" {
                return (StatusCode::FORBIDDEN, Json("forbidden")).into_response();
            }
            Some(u)
        };
        let parsed = params(headers, body, method, path, query);
        if route.input_owner
            && !s.input.allows_http(
                headers
                    .get("x-nanokvm-input-lease")
                    .and_then(|h| h.to_str().ok())
                    .unwrap_or(""),
            )
        {
            return error(-4, "another session holds input control");
        }
        let secure = secure_cookie(s, headers, peer);
        match (method.as_str(), path) {
            ("GET", "/api/vm/cpu-frequency") => crate::cpufreq::get(s),
            ("POST", "/api/vm/cpu-frequency") => crate::cpufreq::set(s, parsed),
            ("GET", "/api/vm/oled") => crate::oled::get(s),
            ("POST", "/api/vm/oled") => crate::oled::set(s, parsed),
            ("GET", "/api/vm/hostname") => crate::hostname::get(s),
            ("POST", "/api/vm/hostname") => crate::hostname::set(s, parsed),
            ("GET", "/api/vm/hardware") => ok(json!({"version":s.hardware.version})),
            ("GET", "/api/vm/gpio") => crate::gpio_api::get(s, cancelled),
            ("POST", "/api/vm/gpio") => {
                let Ok(principal) = principal(s, headers) else {
                    return unauthorized();
                };
                crate::gpio_api::set(s, headers, body, query, principal, cancelled)
            }
            ("POST", "/api/vm/system/reboot") => match s.schedule_reboot() {
                Ok(()) => ok(Value::Null),
                Err(_) => error(-1, "operation failed"),
            },
            ("GET", "/api/vm/mouse-jiggler") | ("POST", "/api/vm/mouse-jiggler/") => {
                crate::jiggler::handle(s, method, parsed)
            }
            ("POST", "/api/hid/paste") => {
                let Ok(principal) = principal(s, headers) else {
                    return unauthorized();
                };
                crate::paste::handle(
                    s,
                    parsed,
                    principal,
                    headers
                        .get("x-nanokvm-input-lease")
                        .and_then(|value| value.to_str().ok())
                        .unwrap_or(""),
                    cancelled,
                )
            }
            ("POST", "/api/internal/usb/recover") => crate::usb::recover(s),
            ("GET" | "POST" | "PUT", "/api/vm/device/virtual") => {
                crate::usb::composition_response(s, method, parsed)
            }
            ("GET", "/api/branding") => crate::branding::status(&s.root),
            ("GET", "/api/branding/logo") => crate::branding::image(&s.root, "logo.png", headers),
            ("GET", "/api/branding/favicon") => {
                crate::branding::image(&s.root, "favicon.png", headers)
            }
            ("GET", "/api/auth/account") => {
                let u = user.unwrap();
                ok(
                    json!({"username":u.username,"role":u.role,"mustChangePassword":u.must_change_password}),
                )
            }
            ("GET", "/api/auth/password") => {
                ok(json!({"isUpdated":!user.unwrap().must_change_password}))
            }
            ("POST", "/api/auth/logout") => {
                let u = user.unwrap();
                if s.config.authentication != "disable"
                    && s.config.jwt.revoke_tokens_on_logout
                    && s.store.revoke(&u.username).is_err()
                {
                    return error(-1, "failed to revoke session");
                }
                if s.config.authentication != "disable" && s.config.jwt.revoke_tokens_on_logout {
                    s.revoke_sessions(&u.username);
                }
                let mut r = ok(Value::Null);
                cookie(&mut r, "", -1, secure);
                r
            }
            ("POST", "/api/auth/password") => {
                let Ok(v) = parsed else {
                    return error(-1, "invalid parameters");
                };
                let Ok(encrypted) = required(&v, "password") else {
                    return error(-1, "invalid parameters");
                };
                let current = crypto::decrypt(v["currentPassword"].as_str().unwrap_or(""));
                let Ok(current) = current else {
                    return error(-3, "current password is required");
                };
                if current.is_empty() {
                    return error(-3, "current password is required");
                }
                let u = user.unwrap();
                match s.store.authenticate(&u.username, &current) {
                    Err(_) => return error(-4, "authentication unavailable"),
                    Ok(None) => return error(-3, "current password is incorrect"),
                    Ok(Some(_)) => {}
                }
                if let Err(e) = change_password(s, &u.username, encrypted) {
                    return error(-5, &e.to_string());
                }
                s.revoke_sessions(&u.username);
                let mut r = ok(Value::Null);
                cookie(&mut r, "", -1, secure);
                r
            }
            ("GET", "/api/auth/users") => match s.store.list() {
                Ok(users) => {
                    ok(json!({"users":users.iter().map(store::User::info).collect::<Vec<_>>()}))
                }
                Err(_) => error(-1, "failed to load users"),
            },
            ("POST", "/api/auth/users") => {
                let Ok(v) = parsed else {
                    return error(-1, "invalid parameters");
                };
                let (Ok(name), Ok(encrypted), Ok(role)) = (
                    required(&v, "username"),
                    required(&v, "password"),
                    required(&v, "role"),
                ) else {
                    return error(-1, "invalid parameters");
                };
                let password = crypto::decrypt(encrypted)
                    .map_err(|_| "invalid password".to_owned())
                    .and_then(|p| {
                        store::valid_password(&p)
                            .map(|_| p)
                            .map_err(|e| e.to_string())
                    });
                let Ok(password) = password else {
                    return error(-2, &password.unwrap_err());
                };
                match s.store.create(name, &password, role) {
                    Ok(()) => ok(Value::Null),
                    Err(e) => error(-3, &e.to_string()),
                }
            }
            ("GET", "/api/vm/web-title") => match std::fs::read_to_string(
                config::rooted(&s.root, "/etc/kvm/web-title").unwrap(),
            ) {
                Ok(title) => ok(json!({"title":title.replace('\n',"")})),
                Err(_) => error(-1, "read web title failed"),
            },
            ("POST", "/api/vm/web-title") => {
                let Ok(v) = parsed else {
                    return error(-1, "invalid arguments");
                };
                let title = match v.get("title") {
                    None | Some(Value::Null) => "",
                    Some(Value::String(v)) => v.as_str(),
                    _ => return error(-1, "invalid arguments"),
                };
                let file = config::rooted(&s.root, "/etc/kvm/web-title").unwrap();
                if title.is_empty() || title == "NanoKVM OS" {
                    if std::fs::remove_file(file).is_err() {
                        return error(-2, "reset failed");
                    }
                } else if store::atomic_write(&file, title.as_bytes(), 0o644).is_err() {
                    return error(-3, "write failed");
                }
                ok(Value::Null)
            }
            _ if path.starts_with("/api/auth/users/") => {
                let pieces: Vec<_> = path
                    .trim_start_matches("/api/auth/users/")
                    .split('/')
                    .collect();
                let name = match percent_encoding::percent_decode_str(pieces[0]).decode_utf8() {
                    Ok(n) => n,
                    Err(_) => return error(-1, "invalid parameters"),
                };
                let actor = user.unwrap().username;
                match method.as_str() {
                    "PUT" => {
                        let patch = parsed.and_then(|v| {
                            serde_json::from_value::<store::Patch>(v).map_err(Into::into)
                        });
                        let Ok(patch) = patch else {
                            return error(-1, "invalid parameters");
                        };
                        if patch.username.is_none()
                            && patch.role.is_none()
                            && patch.enabled.is_none()
                        {
                            return error(-1, "invalid parameters");
                        }
                        match s.store.update(&actor, &name, patch) {
                            Ok(changed) => {
                                if changed {
                                    s.revoke_sessions(&name);
                                }
                                ok(Value::Null)
                            }
                            Err(e) => error(-2, &e.to_string()),
                        }
                    }
                    "DELETE" => match s.store.delete(&actor, &name) {
                        Ok(()) => {
                            s.revoke_sessions(&name);
                            ok(Value::Null)
                        }
                        Err(e) => error(-1, &e.to_string()),
                    },
                    "POST" => {
                        let Ok(v) = parsed else {
                            return error(-1, "invalid parameters");
                        };
                        let Ok(encrypted) = required(&v, "password") else {
                            return error(-1, "invalid parameters");
                        };
                        match s.store.get(&name) {
                            Err(e) => return error(-2, &e.to_string()),
                            Ok(u) if u.system_account => {
                                return error(-3, "the device owner must change its own password")
                            }
                            _ => {}
                        }
                        match change_password(s, &name, encrypted) {
                            Ok(()) => {
                                s.revoke_sessions(&name);
                                ok(Value::Null)
                            }
                            Err(e) => error(-4, &e.to_string()),
                        }
                    }
                    _ => StatusCode::NOT_FOUND.into_response(),
                }
            }
            _ if path.starts_with("/api/hid/") => {
                crate::hid_settings::handle(s, method, path, parsed)
            }
            _ => pending(),
        }
    };
    if method == Method::POST && path == "/api/auth/login" {
        if s.config.authentication == "disable" {
            return (ok(Value::Null), Duration::ZERO);
        }
        let security = &s.config.security;
        let mut attempts = match s.lockout.lock() {
            Ok(l) => l,
            Err(_) => {
                return (
                    error(-3, "authentication unavailable"),
                    Duration::from_secs(2),
                )
            }
        };
        if let Some(msg) = attempts.check(peer, security.login_lockout_duration, Instant::now()) {
            return (error(-5, msg), Duration::from_secs(3));
        }
        drop(attempts);
        let parsed = params(headers, body, method, path, query);
        let Ok(v) = parsed else {
            return (error(-1, "invalid parameters"), Duration::from_secs(3));
        };
        let (Ok(name), Ok(encrypted)) = (required(&v, "username"), required(&v, "password")) else {
            return (error(-1, "invalid parameters"), Duration::from_secs(3));
        };
        let password = crypto::decrypt(encrypted);
        let authenticated = match password {
            Ok(p) if !p.is_empty() => s.store.authenticate(name, &p),
            _ => Ok(None),
        };
        let u = match authenticated {
            Err(_) => {
                return (
                    error(-3, "authentication unavailable"),
                    Duration::from_secs(2),
                )
            }
            Ok(None) => {
                let msg = s.lockout.lock().ok().and_then(|mut l| {
                    l.failure(
                        peer,
                        security.login_lockout_duration,
                        security.login_max_failures,
                        Instant::now(),
                    )
                });
                return (
                    error(
                        if msg.is_some() { -5 } else { -2 },
                        msg.unwrap_or("invalid username or password"),
                    ),
                    Duration::from_secs(2),
                );
            }
            Ok(Some(u)) => u,
        };
        if let Ok(mut l) = s.lockout.lock() {
            l.clear(peer);
        }
        let n = now();
        let Some(exp) = n.checked_add(s.config.jwt.refresh_token_duration) else {
            return (error(-3, "generate token failed"), Duration::from_secs(1));
        };
        let token = crypto::sign(
            &crypto::Claims {
                username: u.username.clone(),
                sub: u.username,
                token_version: u.token_version,
                exp,
                iat: Some(n),
                nbf: None,
            },
            &s.config.jwt.secret_key,
        );
        let Ok(token) = token else {
            return (error(-3, "generate token failed"), Duration::from_secs(1));
        };
        let data = if headers
            .get("x-nanokvm-return-token")
            .and_then(|v| v.to_str().ok())
            .is_some_and(|v| v.trim().eq_ignore_ascii_case("true"))
        {
            json!({"token":token})
        } else {
            Value::Null
        };
        let mut r = ok(data);
        cookie(
            &mut r,
            &token,
            s.config.jwt.refresh_token_duration.min(i64::MAX as u64) as i64,
            secure_cookie(s, headers, peer),
        );
        return (r, Duration::ZERO);
    }
    (result(), Duration::ZERO)
}
