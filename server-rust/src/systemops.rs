//! Fixed firmware actions and a bounded process runner. Isolated roots refuse
//! host device commands; fixtures inject a trusted Executor instead.
use crate::Error;
use std::{
    os::unix::process::CommandExt,
    path::PathBuf,
    process::{Command, Stdio},
    thread,
    time::{Duration, Instant},
};
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Action {
    UsbPhyRestart,
    UsbStop,
    UsbStart,
    ReloadInit,
    Reboot,
    ApplyHostname,
    NtpRestart,
    ChronyRestart,
    NtpStatus,
    ChronyTracking,
}
pub trait Executor: Send + Sync {
    /// Refuse an unavailable/stopped backend before acknowledging delayed work.
    fn check(&self, _action: Action) -> Result<(), Error> {
        Ok(())
    }
    fn run(&self, action: Action, timeout: Duration) -> Result<(), Error>;
    fn output(&self, _action: Action, _timeout: Duration) -> Result<Vec<u8>, Error> {
        Err("command output unavailable".into())
    }
    fn stop(&self) {}
}
pub struct Native {
    root: PathBuf,
    stopped: std::sync::atomic::AtomicBool,
}
impl Native {
    pub fn new(root: PathBuf) -> Self {
        Self {
            root,
            stopped: std::sync::atomic::AtomicBool::new(false),
        }
    }
}
impl Executor for Native {
    fn check(&self, _action: Action) -> Result<(), Error> {
        if self.root != std::path::Path::new("/") {
            return Err("device command unavailable in isolated root".into());
        }
        if self.stopped.load(std::sync::atomic::Ordering::Acquire) {
            return Err("runtime is stopping".into());
        }
        Ok(())
    }
    fn run(&self, action: Action, timeout: Duration) -> Result<(), Error> {
        self.check(action)?;
        let mut command = match action {
            Action::UsbPhyRestart => {
                let mut command = Command::new("sh");
                command.args(["/etc/init.d/S03usbdev", "restart_phy"]);
                command
            }
            Action::UsbStop | Action::UsbStart => {
                let mut command = Command::new("sh");
                command.args([
                    "/etc/init.d/S03usbdev",
                    if action == Action::UsbStop {
                        "stop"
                    } else {
                        "start"
                    },
                ]);
                command
            }
            Action::ReloadInit => {
                let mut command = Command::new("kill");
                command.args(["-HUP", "1"]);
                command
            }
            Action::Reboot => Command::new("reboot"),
            Action::NtpStatus | Action::ChronyTracking => {
                return Err("action requires status/output interface".into())
            }
            Action::NtpRestart | Action::ChronyRestart => {
                let mut command = Command::new(if action == Action::ChronyRestart {
                    "/etc/init.d/S49chronyd"
                } else {
                    "/etc/init.d/S49ntp"
                });
                command.arg("restart");
                command
            }
            Action::ApplyHostname => {
                let mut command = Command::new("hostname");
                command.args(["-F", "/etc/hostname"]);
                command
            }
        };
        bounded_with_cancel(&mut command, timeout, || {
            self.stopped.load(std::sync::atomic::Ordering::Acquire)
        })
    }
    fn output(&self, action: Action, timeout: Duration) -> Result<Vec<u8>, Error> {
        self.check(action)?;
        if action != Action::ChronyTracking {
            return Err("action has no output interface".into());
        }
        let mut command = Command::new("/usr/bin/chronyc");
        command
            .args([
                "-n",
                "-c",
                "-u",
                "root",
                "-h",
                "/run/chrony/chronyd.sock",
                "tracking",
            ])
            .env("LC_ALL", "C");
        captured_with_cancel(&mut command, timeout, || {
            self.stopped.load(std::sync::atomic::Ordering::Acquire)
        })
    }
    fn stop(&self) {
        self.stopped
            .store(true, std::sync::atomic::Ordering::Release);
    }
}
#[cfg(test)]
fn bounded(command: &mut Command, timeout: Duration) -> Result<(), Error> {
    bounded_with_cancel(command, timeout, || false)
}
fn bounded_with_cancel(
    command: &mut Command,
    timeout: Duration,
    cancelled: impl Fn() -> bool,
) -> Result<(), Error> {
    let deadline = Instant::now()
        .checked_add(timeout)
        .ok_or("invalid command timeout")?;
    if timeout.is_zero() {
        return Err("command timed out".into());
    }
    if cancelled() {
        return Err("runtime is stopping".into());
    }
    let mut child = command
        .process_group(0)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()?;
    let result = (|| -> Result<(), Error> {
        loop {
            if cancelled() {
                return Err("runtime is stopping".into());
            }
            if let Some(exit) = child.try_wait()? {
                return if exit.success() {
                    Ok(())
                } else {
                    Err(format!("device command exited {exit}").into())
                };
            }
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err("command timed out".into());
            }
            thread::sleep(remaining.min(Duration::from_millis(20)));
        }
    })();
    if result.is_err() {
        // Each command owns a new process group. Kill descendants as well as the
        // shell on timeout so they cannot resume firmware mutations later.
        if let Ok(pid) = i32::try_from(child.id()) {
            unsafe {
                libc::kill(-pid, libc::SIGKILL);
            }
        }
        let _ = child.kill();
        let _ = child.wait();
    }
    result
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn shutdown_cancellation_terminates_owned_shell_and_descendant() {
        use std::sync::{
            atomic::{AtomicBool, Ordering},
            Arc,
        };
        let temp = tempfile::tempdir().unwrap();
        let pids = temp.path().join("owned-pids");
        let cancelled = Arc::new(AtomicBool::new(false));
        let flag = cancelled.clone();
        let file = pids.clone();
        let cancel = thread::spawn(move || {
            let deadline = Instant::now() + Duration::from_secs(2);
            loop {
                if fs_pids(&file).len() == 2 {
                    break;
                }
                assert!(Instant::now() < deadline);
                thread::sleep(Duration::from_millis(5));
            }
            flag.store(true, Ordering::Release);
        });
        let result = bounded_with_cancel(
            Command::new("sh")
                .args([
                    "-c",
                    "echo $$ > \"$PID_FILE\"; sleep 30 & echo $! >> \"$PID_FILE\"; wait",
                ])
                .env("PID_FILE", &pids),
            Duration::from_secs(5),
            || cancelled.load(Ordering::Acquire),
        );
        cancel.join().unwrap();
        assert!(result
            .unwrap_err()
            .to_string()
            .contains("runtime is stopping"));
        let deadline = Instant::now() + Duration::from_secs(1);
        for pid in fs_pids(&pids) {
            loop {
                let stat = std::fs::read_to_string(format!("/proc/{pid}/stat"));
                // A terminated orphan can briefly await PID 1's zombie reap.
                if stat.is_err()
                    || stat
                        .unwrap()
                        .rsplit_once(") ")
                        .is_some_and(|(_, rest)| rest.starts_with("Z "))
                {
                    break;
                }
                assert!(Instant::now() < deadline);
                thread::sleep(Duration::from_millis(5));
            }
        }
    }
    fn fs_pids(path: &std::path::Path) -> Vec<u32> {
        std::fs::read_to_string(path)
            .unwrap_or_default()
            .lines()
            .filter_map(|line| line.parse::<u32>().ok())
            .collect()
    }
    #[test]
    fn isolated_commands_are_refused_and_owned_process_timeout_is_bounded() {
        let temp = tempfile::tempdir().unwrap();
        let runner = Native::new(temp.path().to_path_buf());
        assert!(runner
            .run(Action::UsbPhyRestart, Duration::from_secs(10))
            .is_err());
        assert!(runner.run(Action::Reboot, Duration::from_secs(10)).is_err());
        bounded(Command::new("true").arg("ignored"), Duration::from_secs(2)).unwrap();
        assert!(bounded(&mut Command::new("false"), Duration::from_secs(2)).is_err());
        let start = Instant::now();
        assert!(bounded(
            Command::new("sh").args(["-c", "sleep 30 & wait"]),
            Duration::from_millis(50)
        )
        .unwrap_err()
        .to_string()
        .contains("timed out"));
        assert!(start.elapsed() < Duration::from_secs(3));
    }
}

fn captured_with_cancel(
    command: &mut Command,
    timeout: Duration,
    cancelled: impl Fn() -> bool,
) -> Result<Vec<u8>, Error> {
    use std::{io::Read, os::fd::AsRawFd};
    let deadline = Instant::now()
        .checked_add(timeout)
        .ok_or("invalid command timeout")?;
    if timeout.is_zero() {
        return Err("command timed out".into());
    }
    if cancelled() {
        return Err("runtime is stopping".into());
    }
    let mut child = command
        .process_group(0)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()?;
    let result = (|| -> Result<Vec<u8>, Error> {
        let mut stdout = child.stdout.take().ok_or("missing command output pipe")?;
        let fd = stdout.as_raw_fd();
        let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
        if flags < 0 || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        let mut bytes = Vec::new();
        let mut exited = None;
        let mut eof = false;
        loop {
            if cancelled() {
                return Err("runtime is stopping".into());
            }
            if Instant::now() >= deadline {
                return Err("command timed out".into());
            }
            if !eof {
                loop {
                    let mut buffer = [0; 1024];
                    match stdout.read(&mut buffer) {
                        Ok(0) => {
                            eof = true;
                            break;
                        }
                        Ok(count) => {
                            if bytes.len() + count > 16384 {
                                return Err("device command output exceeds limit".into());
                            }
                            bytes.extend_from_slice(&buffer[..count]);
                        }
                        Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => break,
                        Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                        Err(error) => return Err(error.into()),
                    }
                }
            }
            if exited.is_none() {
                exited = child.try_wait()?;
            }
            if let Some(exit) = exited {
                if !exit.success() {
                    return Err(format!("device command exited {exit}").into());
                }
                if eof {
                    return Ok(bytes);
                }
            }
            thread::sleep(
                deadline
                    .saturating_duration_since(Instant::now())
                    .min(Duration::from_millis(10)),
            );
        }
    })();
    // An inherited pipe cannot leave a descendant alive after the owned command.
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
mod output_tests {
    use super::*;
    #[test]
    fn captured_output_is_exact_bounded_and_descendant_pipe_obeys_deadline() {
        assert_eq!(
            captured_with_cancel(
                Command::new("printf").arg("tracking\n"),
                Duration::from_secs(2),
                || false
            )
            .unwrap(),
            b"tracking\n"
        );
        assert!(captured_with_cancel(
            Command::new("sh").args(["-c", "head -c 20000 /dev/zero"]),
            Duration::from_secs(2),
            || false
        )
        .unwrap_err()
        .to_string()
        .contains("output exceeds"));
        let start = Instant::now();
        assert!(captured_with_cancel(
            Command::new("sh").args(["-c", "sleep 30 & exit 0"]),
            Duration::from_millis(60),
            || false
        )
        .unwrap_err()
        .to_string()
        .contains("timed out"));
        assert!(start.elapsed() < Duration::from_secs(2));
        assert!(
            captured_with_cancel(&mut Command::new("false"), Duration::from_secs(2), || false)
                .is_err()
        );
        assert!(
            captured_with_cancel(&mut Command::new("true"), Duration::from_secs(2), || true)
                .is_err()
        );
    }
}
