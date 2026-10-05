//! One owner of the private native capture process. Browser transports stay in Rust.
use crate::{
    native_frame::{Budget, Frame, Pending, DIRECT_HEADROOM, MAX_FRAME},
    native_protocol::{self, Op, Packet, Socket},
    Error,
};
use std::{
    os::{fd::AsRawFd, unix::process::CommandExt},
    path::Path,
    process::{Child, Command, Stdio},
    sync::Arc,
    time::{Duration, Instant},
};

#[derive(Clone, Copy, Debug)]
pub enum Codec {
    H264,
    H265,
}
#[derive(Clone, Copy, Debug)]
pub struct VideoConfig {
    pub width: u16,
    pub height: u16,
    pub codec: Codec,
    pub bitrate: u16,
    pub gop: u8,
    pub fps: u8,
}
#[derive(Clone, Copy, Debug)]
pub enum Request {
    Init {
        smart_gop: bool,
        chroma: u16,
    },
    Hdmi(bool),
    Signal,
    Gop(u8),
    GopMode,
    Chroma(bool),
    ChromaStatus,
    Keyframe,
    Edid(bool),
    FrameDetect(u8),
    Mjpeg {
        width: u16,
        height: u16,
        quality: u16,
    },
    Video(VideoConfig),
    Close,
}
impl Request {
    fn packet(self, sequence: u32) -> Result<Packet, Error> {
        let op = match self {
            Self::Init { .. } => Op::Init,
            Self::Hdmi(_) => Op::Hdmi,
            Self::Signal => Op::Signal,
            Self::Gop(_) => Op::Gop,
            Self::GopMode => Op::GopMode,
            Self::Chroma(_) => Op::Chroma,
            Self::ChromaStatus => Op::ChromaStatus,
            Self::Keyframe => Op::Keyframe,
            Self::Edid(_) => Op::Edid,
            Self::FrameDetect(_) => Op::FrameDetect,
            Self::Mjpeg { .. } => Op::Mjpeg,
            Self::Video(_) => Op::Video,
            Self::Close => Op::Close,
        };
        let mut packet = Packet::request(op, sequence)?;
        match self {
            Self::Init { smart_gop, chroma } => {
                if !matches!(chroma, 420 | 422) {
                    return Err("invalid native MJPEG chroma".into());
                }
                packet.put16(16, chroma);
                packet.0[21] = u8::from(smart_gop);
            }
            Self::Hdmi(value) | Self::Chroma(value) | Self::Edid(value) => {
                packet.0[21] = u8::from(value)
            }
            Self::Gop(value) => {
                if !(1..=100).contains(&value) {
                    return Err("invalid native GOP".into());
                }
                packet.0[21] = value;
            }
            Self::FrameDetect(value) => packet.0[21] = value,
            Self::Mjpeg {
                width,
                height,
                quality,
            } => {
                if !(1..=100).contains(&quality) {
                    return Err("invalid native MJPEG quality".into());
                }
                packet.put16(12, width);
                packet.put16(14, height);
                packet.put16(16, quality);
            }
            Self::Video(config) => {
                if !(500..=20000).contains(&config.bitrate)
                    || !(1..=100).contains(&config.gop)
                    || !(10..=120).contains(&config.fps)
                {
                    return Err("invalid native video settings".into());
                }
                packet.put16(12, config.width);
                packet.put16(14, config.height);
                packet.put16(16, config.bitrate);
                packet.0[18] = match config.codec {
                    Codec::H264 => 1,
                    Codec::H265 => 2,
                };
                packet.0[19] = config.gop;
                packet.0[20] = config.fps;
                packet.put16(22, DIRECT_HEADROOM as u16);
            }
            _ => {}
        }
        Ok(packet)
    }
    fn frame_success(self, status: i32) -> bool {
        match self {
            Self::Mjpeg { .. } => status == 0,
            Self::Video(_) => matches!(status, 3 | 4),
            _ => false,
        }
    }
    fn is_capture(self) -> bool {
        matches!(self, Self::Mjpeg { .. } | Self::Video(_))
    }
}
pub struct Outcome {
    pub status: i32,
    pub frame: Option<Arc<Frame>>,
}
/// Exclusive mutable ownership prevents concurrent IPC/native calls. A future
/// capture actor owns this value and applies bounded queue admission above it.
pub struct Worker {
    socket: Socket,
    child: Option<Child>,
    budget: Arc<Budget>,
    sequence: u32,
    closed: bool,
}
impl Worker {
    #[cfg(all(test, feature = "native-fixture"))]
    pub(crate) fn test_socket(socket: Socket, budget: Arc<Budget>) -> Self {
        Self {
            socket,
            child: None,
            budget,
            sequence: 0,
            closed: false,
        }
    }
    /// Explicit native launch only; loading a Runtime does not call this method.
    pub fn launch(root: &Path, budget: Arc<Budget>) -> Result<Self, Error> {
        if root != Path::new("/") {
            return Err("native capture is unavailable in an isolated root".into());
        }
        let mut command = Command::new("/usr/libexec/nanokvm/nanokvm-capture-worker");
        command
            .args(["--fd", "3"])
            .env_clear()
            .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
            .env(
                "LD_LIBRARY_PATH",
                "/usr/lib/nanokvm:/kvmapp/server/dl_lib:/usr/lib",
            )
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::inherit());
        Self::spawn(command, budget)
    }
    pub(crate) fn spawn(mut command: Command, budget: Arc<Budget>) -> Result<Self, Error> {
        let (socket, inherited) = Socket::pair()?;
        let fd = inherited.as_raw_fd();
        // Only async-signal-safe syscalls between fork and exec. The socket's
        // original CLOEXEC fd closes at exec after dup2 to the fixed worker fd.
        unsafe {
            command.pre_exec(move || {
                if libc::setpgid(0, 0) != 0 {
                    return Err(std::io::Error::last_os_error());
                }
                if fd == 3 {
                    if libc::fcntl(fd, libc::F_SETFD, 0) < 0 {
                        return Err(std::io::Error::last_os_error());
                    }
                } else if libc::dup2(fd, 3) < 0 {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let child = command.spawn()?;
        drop(inherited);
        Ok(Self {
            socket,
            child: Some(child),
            budget,
            sequence: 0,
            closed: false,
        })
    }
    pub fn pid(&self) -> Option<u32> {
        self.child.as_ref().map(Child::id)
    }
    pub fn closed(&self) -> bool {
        self.closed
    }
    pub fn call(
        &mut self,
        request: Request,
        origin: Instant,
        timeout: Duration,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<Outcome, Error> {
        if self.closed {
            return Err("native capture is closed".into());
        }
        // Local validation and pre-admission cancellation cannot desynchronize
        // the worker because no packet has been sent yet.
        let sequence = self.sequence.wrapping_add(1).max(1);
        let packet = request.packet(sequence)?;
        let deadline = native_protocol::deadline(timeout)?;
        if cancelled() {
            return Err("native capture cancelled".into());
        }
        self.sequence = sequence;
        let result = self.exchange(request, &packet, origin, deadline, cancelled);
        if result.is_err() {
            self.terminate();
        }
        if matches!(request, Request::Close) && result.is_ok() {
            self.closed = true;
            if let Err(error) = self.reap_until(deadline, cancelled) {
                self.terminate();
                return Err(error);
            }
            self.socket.shutdown();
        }
        result
    }
    fn exchange(
        &self,
        request: Request,
        packet: &Packet,
        origin: Instant,
        deadline: Instant,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<Outcome, Error> {
        self.socket.send(packet, None, deadline, cancelled)?;
        let mut pending = None;
        let mut offered = None;
        let mut rejected = false;
        loop {
            let (reply, descriptors) = self.socket.receive(deadline, cancelled)?;
            if !descriptors.is_empty() || !reply.reply_to(packet) {
                return Err("invalid native capture reply identity".into());
            }
            let status = reply.u32(12) as i32;
            let size = reply.u32(16) as usize;
            match reply.0[6] {
                2 => {
                    if !request.is_capture()
                        || offered.is_some()
                        || status != 0
                        || size == 0
                        || size > MAX_FRAME
                    {
                        return Err("invalid native frame offer".into());
                    }
                    offered = Some(size);
                    let allocation = Pending::new_data(&self.budget, size, packet.u16(22) as usize);
                    let mut accept = reply;
                    accept.0[6] = 3;
                    match allocation {
                        Ok(storage) => {
                            self.socket.send(
                                &accept,
                                Some(storage.descriptor()),
                                deadline,
                                cancelled,
                            )?;
                            pending = Some(storage);
                        }
                        Err(_) => {
                            rejected = true;
                            accept.put32(12, (-libc::ENOMEM) as u32);
                            accept.put32(16, 0);
                            self.socket.send(&accept, None, deadline, cancelled)?;
                        }
                    }
                }
                1 => {
                    if size != 0 {
                        if rejected || !request.frame_success(status) || offered != Some(size) {
                            return Err("invalid native frame result".into());
                        }
                        let storage = pending.take().ok_or("missing native frame allocation")?;
                        let frame = if packet.u16(22) == DIRECT_HEADROOM as u16 {
                            let timestamp = u64::try_from(origin.elapsed().as_micros())
                                .map_err(|_| "native timestamp overflow")?;
                            storage.finish_video(status == 3, timestamp)?
                        } else {
                            storage.finish()?
                        };
                        return Ok(Outcome {
                            status,
                            frame: Some(frame),
                        });
                    }
                    if request.frame_success(status) || (rejected && status >= 0) {
                        return Err("native success without complete frame".into());
                    }
                    // Preserve the Go/native IMG_BUFFER_FULL status when local
                    // frame admission fails; the worker has drained the read.
                    return Ok(Outcome {
                        status: if rejected { -3 } else { status },
                        frame: None,
                    });
                }
                _ => return Err("invalid native capture reply kind".into()),
            }
        }
    }
    fn reap_until(&mut self, deadline: Instant, cancelled: &dyn Fn() -> bool) -> Result<(), Error> {
        let Some(child) = self.child.as_mut() else {
            return Ok(());
        };
        loop {
            if let Some(status) = child.try_wait()? {
                self.child.take();
                return if status.success() {
                    Ok(())
                } else {
                    Err("native worker exited unsuccessfully".into())
                };
            }
            if cancelled() || Instant::now() >= deadline {
                return Err("native worker shutdown timed out".into());
            }
            std::thread::sleep(Duration::from_millis(5));
        }
    }
    pub fn terminate(&mut self) {
        self.closed = true;
        self.socket.shutdown();
        if let Some(mut child) = self.child.take() {
            if let Ok(pid) = i32::try_from(child.id()) {
                unsafe {
                    libc::kill(-pid, libc::SIGKILL);
                }
            }
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}
impl Drop for Worker {
    fn drop(&mut self) {
        if !self.closed {
            // On normal owner teardown ask C to deinitialize/join first.
            // An unresponsive child falls back to owned process-group cleanup.
            let _ = self.call(
                Request::Close,
                Instant::now(),
                Duration::from_secs(2),
                &|| false,
            );
        }
        self.terminate();
    }
}
