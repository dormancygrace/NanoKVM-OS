//! Native worker controls and indivisible EDID programming. Native launch and
//! the fixed privileged utility are refused outside the real firmware root.
use crate::{
    fsroot, memory_command,
    monitor::{self, Backend, VideoStatus},
    native_capture::{Outcome, Request, Worker},
    native_capture_actor::Actor,
    native_frame::Budget,
    screen::Manager,
    screen_store, Error,
};
use std::{
    fs,
    path::{Path, PathBuf},
    process::Command,
    sync::Arc,
    time::{Duration, Instant},
};
const CONTROL_TIMEOUT: Duration = Duration::from_secs(2);
const PROFILE_TIMEOUT: Duration = Duration::from_secs(94);
const CLEANUP_GRACE: Duration = Duration::from_secs(9);
pub trait AudioControl: Send + Sync {
    fn stop(&self) -> Result<(), Error>;
}
pub struct AudioUnavailable;
impl AudioControl for AudioUnavailable {
    fn stop(&self) -> Result<(), Error> {
        Err("native audio owner is not connected".into())
    }
}
pub trait EdidProgrammer: Send + Sync {
    fn apply(
        &self,
        path: &Path,
        cube: bool,
        timeout: Duration,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error>;
}
pub struct FirmwareProgrammer {
    root: PathBuf,
}
impl FirmwareProgrammer {
    pub fn new(root: PathBuf) -> Self {
        Self { root }
    }
    pub(crate) fn command(&self, path: &Path, cube: bool) -> Result<Command, Error> {
        if self.root != Path::new("/") {
            return Err("EDID programming is unavailable in an isolated root".into());
        }
        let path = fsroot::resolve(&self.root, path, false)?;
        if !fs::metadata(&path)?.is_file() {
            return Err("monitor profile is not a regular file".into());
        }
        let mut command = Command::new("/usr/sbin/nanokvm_update_edid");
        command
            .env_clear()
            .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin");
        if cube {
            command.arg("--accept-power-cycle");
        }
        command.arg(path);
        Ok(command)
    }
}
impl EdidProgrammer for FirmwareProgrammer {
    fn apply(
        &self,
        path: &Path,
        cube: bool,
        timeout: Duration,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        let mut command = self.command(path, cube)?;
        let result = memory_command::combined(
            &mut command,
            timeout.min(Duration::from_secs(90)),
            cancelled,
        )?;
        if !result.success {
            eprintln!("monitor EDID programming failed: {}", result.video_error());
            return Err("monitor EDID programming failed".into());
        }
        Ok(())
    }
}
pub struct Native {
    root: PathBuf,
    actor: Actor,
    audio: Arc<dyn AudioControl>,
    programmer: Arc<dyn EdidProgrammer>,
    profile_timeout: Duration,
}
impl Native {
    pub fn launch(
        root: &Path,
        budget: Arc<Budget>,
        audio: Arc<dyn AudioControl>,
    ) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        if root != Path::new("/") {
            return Err("native capture is unavailable in an isolated root".into());
        }
        let actor = Actor::new(Worker::launch(&root, budget)?)?;
        Self::from_actor(
            &root,
            actor,
            audio,
            Arc::new(FirmwareProgrammer::new(root.clone())),
        )
    }
    /// Uses an explicitly supplied owner. Fixtures must supply an isolated
    /// programmer/audio owner; the firmware utility still refuses isolation.
    pub fn from_actor(
        root: &Path,
        actor: Actor,
        audio: Arc<dyn AudioControl>,
        programmer: Arc<dyn EdidProgrammer>,
    ) -> Result<Arc<Self>, Error> {
        let root = root.canonicalize()?;
        let screen = Manager::load(&root)?.snapshot()?;
        let init = actor.call_blocking(
            Request::Init {
                smart_gop: screen.gop_mode == 1,
                chroma: screen.mjpeg_chroma,
            },
            Instant::now(),
            CONTROL_TIMEOUT,
            &|| false,
        )?;
        zero(init)?;
        Ok(Arc::new(Self {
            root,
            actor,
            audio,
            programmer,
            profile_timeout: PROFILE_TIMEOUT,
        }))
    }
    #[cfg(all(test, feature = "native-fixture"))]
    pub(crate) fn test_profile_timeout(&mut self, timeout: Duration) {
        self.profile_timeout = timeout;
    }
    pub fn actor(&self) -> &Actor {
        &self.actor
    }
    fn control(&self, request: Request) -> Result<Outcome, Error> {
        self.actor
            .call_blocking(request, Instant::now(), CONTROL_TIMEOUT, &|| false)
    }
    fn profile(&self, path: &Path) -> Result<(), Error> {
        let path = fsroot::resolve(&self.root, path, false)?;
        if !fs::metadata(&path)?.is_file() {
            return Err("monitor profile is not a regular file".into());
        }
        let root = self.root.clone();
        let programmer = self.programmer.clone();
        let cube = monitor::requires_power_cycle(&root);
        let result = self.actor.transaction_blocking(
            Instant::now(),
            self.profile_timeout,
            CLEANUP_GRACE,
            &|| false,
            move |worker, context| {
                let pause = if cube {
                    Request::Edid(true)
                } else {
                    Request::Hdmi(false)
                };
                let paused = context.remaining().and_then(|remaining| {
                    worker.call(
                        pause,
                        Instant::now(),
                        remaining.min(CONTROL_TIMEOUT),
                        &|| context.cancelled(),
                    )
                });
                if cube && !matches!(&paused,Ok(outcome) if outcome.status>=0) {
                    return Err(
                        "Cube EDID maintenance requires updated native video library".into(),
                    );
                }
                let operation = (|| -> Result<(), Error> {
                    if !cube && !matches!(&paused,Ok(outcome) if outcome.status>=0) {
                        return Err("cannot pause HDMI for monitor change".into());
                    }
                    if cube {
                        screen_store::write(
                            &root,
                            "/etc/kvm/monitor_power_cycle_pending",
                            b"1\n",
                            0o600,
                            false,
                        )?;
                    }
                    let timeout = context.remaining()?.min(Duration::from_secs(90));
                    programmer
                        .apply(&path, cube, timeout, &|| context.cancelled())
                        .map_err(|error| {
                            eprintln!("monitor EDID programming failed: {error}");
                            Box::<dyn std::error::Error + Send + Sync>::from(
                                "monitor EDID programming failed",
                            )
                        })
                })();
                // Restore independently of the expired/cancelled operation. Its own
                // short timeout bounds shutdown while maintaining the same owner.
                let restore = (|| -> Result<(), Error> {
                    let enabled = capture_enabled(&root)?;
                    worker
                        .call(
                            if cube {
                                Request::Edid(false)
                            } else {
                                Request::Hdmi(enabled)
                            },
                            Instant::now(),
                            CONTROL_TIMEOUT,
                            &|| false,
                        )
                        .and_then(nonnegative)?;
                    if cube && !enabled {
                        worker
                            .call(
                                Request::Hdmi(false),
                                Instant::now(),
                                CONTROL_TIMEOUT,
                                &|| false,
                            )
                            .and_then(nonnegative)?;
                    }
                    Ok(())
                })();
                if let Err(error) = restore {
                    eprintln!("restore native monitor capture failed: {error}");
                    // Unknown restoration must not admit more frames as though
                    // capture recovered. Request deinit, then ensure owned exit.
                    let _ = worker.call(Request::Close, Instant::now(), CONTROL_TIMEOUT, &|| false);
                    worker.terminate();
                    if operation.is_ok() {
                        return Err("cannot restore capture after monitor change".into());
                    }
                }
                operation?;
                Ok(Outcome {
                    status: 0,
                    frame: None,
                })
            },
        )?;
        zero(result)
    }
}
impl Backend for Native {
    fn capture_actor(&self) -> Option<Actor> {
        Some(self.actor.clone())
    }
    fn set_hdmi(&self, enabled: bool) -> Result<(), Error> {
        self.control(Request::Hdmi(enabled)).and_then(nonnegative)
    }
    fn has_hdmi_signal(&self) -> Result<bool, Error> {
        let status = self.control(Request::Signal)?.status;
        if !(0..=255).contains(&status) {
            Err("invalid native HDMI signal status".into())
        } else {
            Ok(status != 0)
        }
    }
    fn set_gop(&self, value: u8) -> Result<(), Error> {
        self.control(Request::Gop(value)).and_then(zero)
    }
    fn set_mjpeg_chroma(&self, value: u16) -> Result<(), Error> {
        if !matches!(value, 420 | 422) {
            return Err("invalid native MJPEG chroma".into());
        }
        self.control(Request::Chroma(value == 422)).and_then(zero)
    }
    fn video_status(&self) -> Result<VideoStatus, Error> {
        let result = self.actor.transaction_blocking(
            Instant::now(),
            CONTROL_TIMEOUT,
            Duration::ZERO,
            &|| false,
            |worker, context| {
                let mode = context.call(worker, Request::GopMode)?.status;
                let chroma = context.call(worker, Request::ChromaStatus)?.status;
                if !(0..=255).contains(&mode) || !(0..=255).contains(&chroma) {
                    return Err("invalid native video status".into());
                }
                Ok(Outcome {
                    status: (mode << 8) | chroma,
                    frame: None,
                })
            },
        )?;
        Ok(decode_status(
            (result.status >> 8) as u8,
            result.status as u8,
        ))
    }
    fn stop_audio(&self) -> Result<(), Error> {
        self.audio.stop()
    }
    fn apply_monitor_profile(&self, path: &Path) -> Result<(), Error> {
        self.profile(path)
    }
    fn stop(&self) {
        self.actor.stop();
        if let Err(error) = self.audio.stop() {
            eprintln!("stop native audio failed: {error}");
        }
    }
    fn join(&self) -> Result<(), Error> {
        self.actor.join_blocking()
    }
}
fn zero(outcome: Outcome) -> Result<(), Error> {
    if outcome.status == 0 && outcome.frame.is_none() {
        Ok(())
    } else {
        Err(format!("native control failed: {}", outcome.status).into())
    }
}
fn nonnegative(outcome: Outcome) -> Result<(), Error> {
    if outcome.status >= 0 && outcome.frame.is_none() {
        Ok(())
    } else {
        Err(format!("native control failed: {}", outcome.status).into())
    }
}
pub(crate) fn decode_status(mode: u8, flags: u8) -> VideoStatus {
    let active = if flags & 1 != 0 { 422 } else { 420 };
    let reason = if flags & 16 != 0 {
        "hardware"
    } else if flags & 32 != 0 && active == 422 {
        "resolution"
    } else if flags & 2 != 0 {
        "video"
    } else if flags & 4 != 0 {
        "frameDetection"
    } else if flags & 8 != 0 {
        "diagnostic"
    } else {
        ""
    };
    VideoStatus {
        gop_mode: mode,
        mjpeg_chroma: active,
        chroma_fallback: reason.into(),
    }
}

fn capture_enabled(root: &Path) -> Result<bool, Error> {
    let directory = fsroot::resolve(root, Path::new("/etc/kvm"), false)?;
    match fs::symlink_metadata(directory.join("hdmi_disable")) {
        Ok(_) => Ok(false),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(true),
        Err(error) => Err(error.into()),
    }
}
