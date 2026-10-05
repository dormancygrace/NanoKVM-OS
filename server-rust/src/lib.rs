mod api;
mod binding;
mod branding;
pub mod composition;
pub mod config;
pub mod controlmode;
pub mod cpufreq;
pub mod crypto;
mod form_binding;
pub mod fsroot;
pub mod gpio;
mod gpio_api;
mod gpio_binding;
pub mod gpio_monitor;
pub mod hardware;
pub mod hid_device;
pub mod hid_reports;
mod hid_settings;
mod hostname;
pub mod input;
pub mod inputcontrol;
pub mod internal;
pub mod jiggler;
mod json_syntax;
mod json_text;
pub mod leds;
pub mod lockout;
pub mod memory_status;
pub mod monitor;
mod oled;
mod paste;
mod paste_layout;
mod preferences;
pub mod redirect;
mod request_cancel;
mod sessions;
pub mod store;
pub mod sysinfo;
pub mod systemops;
pub mod time_sync;
pub mod timeconfig;
pub mod usb;
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
    pub paste: Mutex<()>,
    pub system_settings: Mutex<()>,
    pub hid: Arc<hid_device::Devices>,
    pub input: Arc<input::Hub>,
    pub control: Arc<controlmode::Manager>,
    pub coordinator: Arc<inputcontrol::Coordinator>,
    pub pico_lock: Arc<inputcontrol::PicoLock>,
    pub jiggler: Arc<jiggler::Jiggler>,
    pub commands: Arc<dyn systemops::Executor>,
    pub monitor: monitor::Monitor,
    pub hardware: hardware::Hardware,
    pub atx: gpio::Controller,
    pub atx_leds: gpio_monitor::Monitor,
    pub cpu: cpufreq::Manager,
    pub time: timeconfig::Manager,
    pub info: sysinfo::Manager,
    pub(crate) internal: internal::Token,
    reboot_pending: Arc<std::sync::atomic::AtomicBool>,
    pub(crate) sessions: sessions::Registry,
    pub(crate) socket_slots: Arc<Semaphore>,
    pub(crate) hid_jobs: Arc<Semaphore>,
    pub(crate) control_jobs: Arc<Semaphore>,
    pub(crate) stopping: std::sync::atomic::AtomicBool,
}
impl Runtime {
    pub fn load(root: &Path) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        let commands = Arc::new(systemops::Native::new(root.clone()));
        Self::load_with_executor(&root, commands)
    }
    pub fn load_with_executor(
        root: &Path,
        commands: Arc<dyn systemops::Executor>,
    ) -> Result<Arc<Self>, Error> {
        Self::load_with_backends(root, commands, Arc::new(monitor::Unavailable))
    }
    pub fn load_with_backends(
        root: &Path,
        commands: Arc<dyn systemops::Executor>,
        media: Arc<dyn monitor::Backend>,
    ) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        let gpio = Arc::new(gpio::Native::new(root.clone()));
        Self::load_with_peripherals(&root, commands, media, gpio)
    }
    pub fn load_with_peripherals(
        root: &Path,
        commands: Arc<dyn systemops::Executor>,
        media: Arc<dyn monitor::Backend>,
        gpio: Arc<dyn gpio::Backend>,
    ) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        let cpu = Arc::new(cpufreq::Native::new(root.clone()));
        Self::load_with_hardware_backends(&root, commands, media, gpio, cpu)
    }
    pub fn load_with_hardware_backends(
        root: &Path,
        commands: Arc<dyn systemops::Executor>,
        media: Arc<dyn monitor::Backend>,
        gpio: Arc<dyn gpio::Backend>,
        cpu: Arc<dyn cpufreq::Backend>,
    ) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        let time = Arc::new(timeconfig::Native::new(root.clone(), commands.clone()));
        Self::load_with_all_backends(&root, commands, media, gpio, cpu, time)
    }
    pub fn load_with_all_backends(
        root: &Path,
        commands: Arc<dyn systemops::Executor>,
        media: Arc<dyn monitor::Backend>,
        gpio: Arc<dyn gpio::Backend>,
        cpu: Arc<dyn cpufreq::Backend>,
        time: Arc<dyn timeconfig::Backend>,
    ) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        let network = Arc::new(sysinfo::Native::new(root.clone()));
        Self::load_with_information_backends(&root, commands, media, gpio, cpu, time, network)
    }
    pub fn load_with_information_backends(
        root: &Path,
        commands: Arc<dyn systemops::Executor>,
        media: Arc<dyn monitor::Backend>,
        gpio: Arc<dyn gpio::Backend>,
        cpu: Arc<dyn cpufreq::Backend>,
        time: Arc<dyn timeconfig::Backend>,
        network: Arc<dyn sysinfo::Interfaces>,
    ) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        let info = sysinfo::Manager::new(root.clone(), network);
        let time = timeconfig::Manager::new(root.clone(), time);
        let cpu = cpufreq::Manager::new(root.clone(), cpu);
        if root == Path::new("/") {
            if let Err(error) = cpu.apply_saved() {
                eprintln!("apply saved CPU frequency failed: {error}");
            }
        }
        let hardware = hardware::Hardware::detect(&root);
        let hid = Arc::new(hid_device::Devices::new(root.clone()));
        let release = hid.clone();
        Ok(Arc::new(Self {
            config: config::Config::load(&root)?,
            store: store::Store::new(config::rooted(&root, "/etc/kvm/pwd")?),
            control: controlmode::Manager::new(
                config::rooted(&root, "/etc/kvm/ai-control.mode")?,
                controlmode::Mode::Picoclaw,
            ),
            coordinator: inputcontrol::Coordinator::new(),
            pico_lock: Arc::new(inputcontrol::PicoLock::default()),
            jiggler: jiggler::Jiggler::load(config::rooted(&root, "/etc/kvm/mouse-jiggler")?),
            commands,
            monitor: monitor::Monitor::new(media),
            atx: gpio::Controller::new(gpio.clone()),
            atx_leds: gpio_monitor::Monitor::new(&hardware, gpio),
            hardware,
            cpu,
            time,
            info,
            internal: internal::Token::load(&root)?,
            root,
            reboot_pending: Arc::new(std::sync::atomic::AtomicBool::new(false)),
            lockout: Mutex::new(lockout::Lockout::default()),
            jobs: Arc::new(Semaphore::new(4)),
            hid_settings: Mutex::new(()),
            paste: Mutex::new(()),
            system_settings: Mutex::new(()),
            hid,
            input: Arc::new(input::Hub::new(move || {
                if let Err(error) = release.release_all() {
                    eprintln!("HID release failed: {error}");
                }
            })),
            sessions: sessions::Registry::default(),
            socket_slots: Arc::new(Semaphore::new(64)),
            hid_jobs: Arc::new(Semaphore::new(2)),
            control_jobs: Arc::new(Semaphore::new(2)),
            stopping: std::sync::atomic::AtomicBool::new(false),
        }))
    }
    pub(crate) fn schedule_reboot(&self) -> Result<(), Error> {
        use std::sync::atomic::Ordering;
        self.commands.check(systemops::Action::Reboot)?;
        let handle = tokio::runtime::Handle::try_current()?;
        if self.reboot_pending.swap(true, Ordering::AcqRel) {
            return Ok(());
        }
        let pending = self.reboot_pending.clone();
        let commands = self.commands.clone();
        handle.spawn(async move {
            // Let the success body reach the client before a real reboot can
            // terminate the process. Bound/deduplicate the one pending action.
            tokio::time::sleep(std::time::Duration::from_millis(500)).await;
            let result = tokio::task::spawn_blocking(move || {
                commands.run(
                    systemops::Action::Reboot,
                    std::time::Duration::from_secs(10),
                )
            })
            .await;
            if !matches!(result, Ok(Ok(()))) {
                eprintln!("reboot failed: {result:?}");
            }
            pending.store(false, Ordering::Release);
        });
        Ok(())
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
        self.commands.stop();
        self.socket_slots.close();
        self.hid_jobs.close();
        self.control_jobs.close();
        self.coordinator.cancel(inputcontrol::Cause::ModeChanged);
        self.pico_lock.release("");
        self.jiggler.stop();
        self.atx_leds.stop();
        self.hid.leds().stop();
        for id in self.sessions.revoke_all() {
            let _ = self.input.leave(id);
        }
        if let Err(error) = self.input.synchronized(|| self.hid.close()) {
            eprintln!("shutdown HID release failed: {error}");
        }
    }
}
pub fn app(state: Arc<Runtime>, web: PathBuf) -> Router {
    state.hid.leds().start();
    jiggler::Jiggler::start(&state);
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
