mod api;
mod binding;
mod branding;
pub mod config;
pub mod crypto;
pub mod hid_device;
pub mod hid_reports;
mod hid_settings;
pub mod input;
pub mod lockout;
pub mod redirect;
mod sessions;
pub mod store;
mod ws;
mod ws_origin;
pub type Error = Box<dyn std::error::Error + Send + Sync>;
use axum::{
    extract::{Request, State},
    response::{IntoResponse, Response},
    routing::{any, get},
    Router,
};
use std::{
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};
use tokio::sync::Semaphore;
use tower::ServiceExt;
use tower_http::services::ServeDir;

pub struct Runtime {
    pub root: PathBuf,
    pub config: config::Config,
    pub store: store::Store,
    pub lockout: Mutex<lockout::Lockout>,
    pub jobs: Arc<Semaphore>,
    pub hid_settings: Mutex<()>,
    pub hid: Arc<hid_device::Devices>,
    pub input: Arc<input::Hub>,
    pub(crate) sessions: sessions::Registry,
    pub(crate) socket_slots: Arc<Semaphore>,
    pub(crate) hid_jobs: Arc<Semaphore>,
    pub(crate) stopping: std::sync::atomic::AtomicBool,
}
impl Runtime {
    pub fn load(root: &Path) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        let hid = Arc::new(hid_device::Devices::new(root.clone()));
        let release = hid.clone();
        Ok(Arc::new(Self {
            config: config::Config::load(&root)?,
            store: store::Store::new(config::rooted(&root, "/etc/kvm/pwd")?),
            root,
            lockout: Mutex::new(lockout::Lockout::default()),
            jobs: Arc::new(Semaphore::new(4)),
            hid_settings: Mutex::new(()),
            hid,
            input: Arc::new(input::Hub::new(move || {
                if let Err(error) = release.release_all() {
                    eprintln!("HID release failed: {error}");
                }
            })),
            sessions: sessions::Registry::default(),
            socket_slots: Arc::new(Semaphore::new(64)),
            hid_jobs: Arc::new(Semaphore::new(2)),
            stopping: std::sync::atomic::AtomicBool::new(false),
        }))
    }
    pub(crate) fn revoke_sessions(&self, username: &str) {
        for id in self.sessions.revoke(username) {
            if let Err(error) = self.input.leave(id) {
                eprintln!("session input cleanup failed: {error}");
            }
        }
    }
    pub fn shutdown(&self) {
        self.stopping
            .store(true, std::sync::atomic::Ordering::Release);
        self.socket_slots.close();
        self.hid_jobs.close();
        for id in self.sessions.revoke_all() {
            let _ = self.input.leave(id);
        }
        if let Err(error) = self.hid.close() {
            eprintln!("shutdown HID release failed: {error}");
        }
    }
}
pub fn app(state: Arc<Runtime>, web: PathBuf) -> Router {
    Router::new()
        .route("/api/ws", get(ws::connect))
        .route("/api", any(api::dispatch))
        .route("/api/{*path}", any(api::dispatch))
        .fallback(static_file)
        .with_state((state, web))
}
async fn static_file(State((_, web)): State<(Arc<Runtime>, PathBuf)>, req: Request) -> Response {
    let decoded = match percent_encoding::percent_decode_str(req.uri().path()).decode_utf8() {
        Ok(v) => v,
        Err(_) => return axum::http::StatusCode::NOT_FOUND.into_response(),
    };
    let relative = Path::new(decoded.trim_start_matches('/'));
    if relative.components().any(|c| {
        !matches!(
            c,
            std::path::Component::Normal(_) | std::path::Component::CurDir
        )
    }) {
        return axum::http::StatusCode::NOT_FOUND.into_response();
    }
    let target = web.join(relative);
    let target = if target.is_dir() {
        target.join("index.html")
    } else {
        target
    };
    if !target
        .canonicalize()
        .is_ok_and(|path| path.starts_with(&web))
    {
        return axum::http::StatusCode::NOT_FOUND.into_response();
    }
    let index = req.uri().path() == "/" || req.uri().path() == "/index.html";
    let mut response = ServeDir::new(web)
        .oneshot(req)
        .await
        .unwrap()
        .into_response();
    if index {
        response.headers_mut().insert(
            "cache-control",
            "no-store, no-cache, must-revalidate, max-age=0"
                .parse()
                .unwrap(),
        );
        response
            .headers_mut()
            .insert("pragma", "no-cache".parse().unwrap());
        response
            .headers_mut()
            .insert("expires", "0".parse().unwrap());
    }
    response
}
