//! Validated constant firmware commands and bounded combined output.
use crate::Error;
use std::{
    collections::VecDeque,
    io::Read,
    os::{
        fd::{AsRawFd, FromRawFd, OwnedFd},
        unix::process::CommandExt,
    },
    process::{Command, Stdio},
    time::{Duration, Instant},
};
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SwapKind {
    Zram,
    Sd,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Board {
    Alpha,
    Beta,
    Pcie,
    Lite,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum VideoMode {
    Cma,
    Fixed,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Action {
    Configure {
        kind: SwapKind,
        enabled: bool,
        size: i64,
        recompress: Option<bool>,
    },
    Video {
        board: Board,
        mode: VideoMode,
    },
    Recompress,
}
#[derive(Clone, Debug)]
pub struct Output {
    pub success: bool,
    pub bytes: Vec<u8>,
    trimmed_tail: Vec<u8>,
    trimmed_prefix: Vec<u8>,
}
impl Output {
    pub fn new(success: bool, bytes: &[u8]) -> Self {
        let mut collector = Collector::default();
        collector.push(bytes);
        collector.finish(success)
    }
    pub fn swap_error(&self) -> String {
        if self.trimmed_tail.is_empty() {
            "Failed to change swap".into()
        } else {
            crate::json_text::text(&self.trimmed_tail).into_owned()
        }
    }
    pub fn video_error(&self) -> String {
        let bytes = &self.bytes[self.bytes.len().saturating_sub(800)..];
        format!(
            "Failed to select video memory mode: {}",
            crate::json_text::text(bytes).trim()
        )
    }
    pub fn maintenance_error(&self) -> String {
        crate::json_text::text(&self.trimmed_prefix)
            .chars()
            .take(512)
            .collect()
    }
}
impl Board {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "alpha" => Some(Self::Alpha),
            "beta" => Some(Self::Beta),
            "pcie" => Some(Self::Pcie),
            "lite" => Some(Self::Lite),
            _ => None,
        }
    }
    pub fn name(self) -> &'static str {
        match self {
            Self::Alpha => "alpha",
            Self::Beta => "beta",
            Self::Pcie => "pcie",
            Self::Lite => "lite",
        }
    }
}
impl VideoMode {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "cma" => Some(Self::Cma),
            "fixed" => Some(Self::Fixed),
            _ => None,
        }
    }
    pub fn name(self) -> &'static str {
        match self {
            Self::Cma => "cma",
            Self::Fixed => "fixed",
        }
    }
}
impl Action {
    pub fn valid(self) -> bool {
        match self {
            Self::Configure {
                kind: SwapKind::Zram,
                size,
                ..
            } => [32, 64, 128, 162].contains(&size),
            Self::Configure {
                kind: SwapKind::Sd,
                size,
                recompress,
                ..
            } => recompress.is_none() && [128, 256, 512].contains(&size),
            _ => true,
        }
    }
    pub(crate) fn command(self) -> Result<Command, Error> {
        if !self.valid() {
            return Err("invalid swap type or size".into());
        }
        let mut command = match self {
            Self::Configure {
                kind,
                enabled,
                size,
                recompress,
            } => {
                let mut c = Command::new("sh");
                c.args([
                    "/etc/init.d/S38memory",
                    "configure",
                    if kind == SwapKind::Zram { "zram" } else { "sd" },
                    if enabled { "1" } else { "0" },
                ]);
                c.arg(size.to_string());
                if let Some(v) = recompress {
                    c.arg(if v { "1" } else { "0" });
                }
                c
            }
            Self::Video { board, mode } => {
                let mut c = Command::new("/usr/libexec/nanokvm/activate-kernel");
                c.args([board.name(), mode.name()]);
                c
            }
            Self::Recompress => {
                let mut c = Command::new("nice");
                c.args(["-n", "19", "sh", "/etc/init.d/S38memory", "recompress"]);
                c
            }
        };
        command.env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin");
        Ok(command)
    }
}
fn tail(queue: &mut VecDeque<u8>, bytes: &[u8], limit: usize) {
    queue.extend(bytes);
    while queue.len() > limit {
        queue.pop_front();
    }
}
// Retain both trim-before-tail and tail-before-trim semantics without storing
// arbitrary output or arbitrary trailing whitespace. Partial UTF-8 spans reads.
#[derive(Default)]
struct Collector {
    raw: VecDeque<u8>,
    trimmed: VecDeque<u8>,
    prefix: Vec<u8>,
    whitespace: VecDeque<u8>,
    white_prefix: Vec<u8>,
    pending: Vec<u8>,
    started: bool,
}
impl Collector {
    fn push(&mut self, bytes: &[u8]) {
        tail(&mut self.raw, bytes, 16384);
        self.pending.extend_from_slice(bytes);
        self.decode(false);
    }
    fn decode(&mut self, final_read: bool) {
        let mut at = 0;
        while at < self.pending.len() {
            let slice = &self.pending[at..];
            let width = match slice[0] {
                0..=127 => 1,
                194..=223 => 2,
                224..=239 => 3,
                240..=244 => 4,
                _ => 1,
            };
            if slice.len() < width && !final_read {
                break;
            }
            let (count, space) = if slice.len() >= width {
                match std::str::from_utf8(&slice[..width]) {
                    Ok(s) => (width, s.chars().next().unwrap().is_whitespace()),
                    Err(_) => (1, false),
                }
            } else {
                (1, false)
            };
            let bytes = &slice[..count];
            if space {
                if self.started {
                    tail(&mut self.whitespace, bytes, 800);
                    let room = 2048 - self.white_prefix.len();
                    self.white_prefix
                        .extend_from_slice(&bytes[..bytes.len().min(room)]);
                }
            } else {
                if self.started {
                    let white: Vec<_> = self.whitespace.drain(..).collect();
                    tail(&mut self.trimmed, &white, 800);
                    let room = 2048 - self.prefix.len();
                    self.prefix
                        .extend_from_slice(&self.white_prefix[..self.white_prefix.len().min(room)]);
                    self.white_prefix.clear();
                }
                self.started = true;
                tail(&mut self.trimmed, bytes, 800);
                let room = 2048 - self.prefix.len();
                self.prefix.extend_from_slice(&bytes[..count.min(room)]);
            }
            at += count;
        }
        self.pending.drain(..at);
    }
    fn finish(mut self, success: bool) -> Output {
        self.decode(true);
        Output {
            success,
            bytes: self.raw.into(),
            trimmed_tail: self.trimmed.into(),
            trimmed_prefix: self.prefix,
        }
    }
}
pub(crate) fn combined(
    command: &mut Command,
    timeout: Duration,
    cancelled: impl Fn() -> bool,
) -> Result<Output, Error> {
    let deadline = Instant::now()
        .checked_add(timeout)
        .ok_or("invalid command timeout")?;
    if timeout.is_zero() {
        return Err("command timed out".into());
    }
    if cancelled() {
        return Err("command cancelled".into());
    }
    let mut fds = [0; 2];
    if unsafe { libc::pipe2(fds.as_mut_ptr(), libc::O_CLOEXEC) } != 0 {
        return Err(std::io::Error::last_os_error().into());
    }
    let reader = unsafe { OwnedFd::from_raw_fd(fds[0]) };
    let writer = unsafe { OwnedFd::from_raw_fd(fds[1]) };
    let flags = unsafe { libc::fcntl(reader.as_raw_fd(), libc::F_GETFL) };
    if flags < 0
        || unsafe { libc::fcntl(reader.as_raw_fd(), libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0
    {
        return Err(std::io::Error::last_os_error().into());
    }
    let mut child = command
        .process_group(0)
        .stdin(Stdio::null())
        .stdout(Stdio::from(writer.try_clone()?))
        .stderr(Stdio::from(writer))
        .spawn()?;
    // Command retains its Stdio descriptors after spawn; close those parent copies.
    command.stdout(Stdio::null()).stderr(Stdio::null());
    let mut reader = std::fs::File::from(reader);
    let result = (|| -> Result<Output, Error> {
        let mut collector = Collector::default();
        let mut eof = false;
        let mut exit = None;
        loop {
            if cancelled() {
                return Err("command cancelled".into());
            }
            if Instant::now() >= deadline {
                return Err("command timed out".into());
            }
            if !eof {
                for _ in 0..64 {
                    let mut buffer = [0u8; 4096];
                    match reader.read(&mut buffer) {
                        Ok(0) => {
                            eof = true;
                            break;
                        }
                        Ok(count) => collector.push(&buffer[..count]),
                        Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => break,
                        Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                        Err(error) => return Err(error.into()),
                    }
                }
            }
            if exit.is_none() {
                exit = child.try_wait()?;
            }
            if let Some(exit) = exit {
                if eof {
                    return Ok(collector.finish(exit.success()));
                }
            }
            std::thread::sleep(
                deadline
                    .saturating_duration_since(Instant::now())
                    .min(Duration::from_millis(10)),
            );
        }
    })();
    if let Ok(pid) = i32::try_from(child.id()) {
        unsafe {
            libc::kill(-pid, libc::SIGKILL);
        }
    }
    let _ = child.kill();
    let _ = child.wait();
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn actual_go_errors_preserve_trim_order_utf8_and_long_whitespace_in_every_chunk_size() {
        use base64::{engine::general_purpose::STANDARD, Engine};
        let oracle: serde_json::Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/memory-command-go-oracle.json"
        ))
        .unwrap();
        for case in oracle["cases"].as_array().unwrap() {
            let fixture = &case["case"];
            if fixture["Fail"] != true {
                continue;
            }
            let bytes = STANDARD
                .decode(fixture["Output"].as_str().unwrap())
                .unwrap();
            for chunk in [1, 2, 3, 7, 4096] {
                let mut collector = Collector::default();
                for part in bytes.chunks(chunk) {
                    collector.push(part);
                }
                let output = collector.finish(false);
                let message = if fixture["Path"] == "/api/vm/memory/swap" {
                    output.swap_error()
                } else {
                    output.video_error()
                };
                assert_eq!(
                    message, case["response"]["msg"],
                    "{} chunk{chunk}",
                    fixture["Name"]
                );
                assert!(output.bytes.len() <= 16384);
                assert!(output.trimmed_tail.len() <= 800);
                assert!(output.trimmed_prefix.len() <= 2048);
            }
        }
    }
    #[test]
    fn firmware_commands_have_only_validated_constant_arguments() {
        for (kind, sizes) in [
            (SwapKind::Zram, vec![32, 64, 128, 162]),
            (SwapKind::Sd, vec![128, 256, 512]),
        ] {
            for size in sizes {
                let action = Action::Configure {
                    kind,
                    size,
                    enabled: true,
                    recompress: None,
                };
                let command = action.command().unwrap();
                assert_eq!(command.get_program(), "sh");
                let args: Vec<_> = command.get_args().map(|s| s.to_str().unwrap()).collect();
                assert_eq!(
                    args,
                    vec![
                        "/etc/init.d/S38memory",
                        "configure",
                        if kind == SwapKind::Zram { "zram" } else { "sd" },
                        "1",
                        &size.to_string()
                    ]
                );
            }
        }
        assert!(Action::Configure {
            kind: SwapKind::Sd,
            enabled: false,
            size: 256,
            recompress: Some(false)
        }
        .command()
        .is_err());
        assert!(Action::Configure {
            kind: SwapKind::Zram,
            enabled: false,
            size: 63,
            recompress: None
        }
        .command()
        .is_err());
        let video = Action::Video {
            board: Board::Pcie,
            mode: VideoMode::Fixed,
        }
        .command()
        .unwrap();
        assert_eq!(video.get_program(), "/usr/libexec/nanokvm/activate-kernel");
        assert_eq!(video.get_args().collect::<Vec<_>>(), vec!["pcie", "fixed"]);
        let recompress = Action::Recompress.command().unwrap();
        assert_eq!(recompress.get_program(), "nice");
        assert_eq!(
            recompress.get_args().collect::<Vec<_>>(),
            vec!["-n", "19", "sh", "/etc/init.d/S38memory", "recompress"]
        );
    }
    #[test]
    fn real_combined_pipe_order_tail_timeout_cancel_and_flood_are_bounded() {
        let output = combined(
            Command::new("sh").args(["-c", "printf one; printf two >&2; printf three; exit 1"]),
            Duration::from_secs(2),
            || false,
        )
        .unwrap();
        assert!(!output.success);
        assert_eq!(output.bytes, b"onetwothree");
        let output = combined(
            Command::new("sh").args([
                "-c",
                "printf ' error '; head -c 30000 /dev/zero | tr '\\000' ' '; exit 1",
            ]),
            Duration::from_secs(2),
            || false,
        )
        .unwrap();
        assert_eq!(output.swap_error(), "error");
        assert_eq!(output.bytes.len(), 16384);
        assert_eq!(output.video_error(), "Failed to select video memory mode: ");
        assert!(combined(&mut Command::new("true"), Duration::from_secs(1), || true).is_err());
        for script in [
            "sleep 30 & exit 0",
            "while :; do printf 'abcdef0123456789'; done",
        ] {
            let start = Instant::now();
            assert!(combined(
                Command::new("sh").args(["-c", script]),
                Duration::from_millis(60),
                || false
            )
            .unwrap_err()
            .to_string()
            .contains("timed out"));
            assert!(start.elapsed() < Duration::from_secs(2));
        }
        let start = Instant::now();
        assert!(combined(
            Command::new("sh").args(["-c", "sleep 30 & wait"]),
            Duration::from_secs(2),
            || start.elapsed() > Duration::from_millis(60)
        )
        .unwrap_err()
        .to_string()
        .contains("cancelled"));
        assert!(start.elapsed() < Duration::from_secs(1));
    }
}
