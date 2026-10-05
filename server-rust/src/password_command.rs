//! A password is sent only to the owned passwd process's stdin.
use crate::Error;
use std::{
    io::Write,
    os::unix::process::CommandExt,
    process::{Command, Stdio},
    time::{Duration, Instant},
};
pub(crate) fn set(
    command: &mut Command,
    password: &str,
    timeout: Duration,
    cancelled: impl Fn() -> bool,
) -> Result<(), Error> {
    if password.contains(['\n', '\r', '\0']) {
        return Err("invalid root password".into());
    }
    let deadline = Instant::now()
        .checked_add(timeout)
        .ok_or("invalid command timeout")?;
    if cancelled() || timeout.is_zero() {
        return Err("system password update cancelled".into());
    }
    let mut child = command
        .process_group(0)
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()?;
    let result = (|| -> Result<(), Error> {
        let mut input = child.stdin.take().ok_or("password input unavailable")?;
        writeln!(input, "{password}")?;
        let confirmation = Instant::now() + Duration::from_millis(100);
        while Instant::now() < confirmation {
            if cancelled() {
                return Err("system password update cancelled".into());
            }
            if Instant::now() >= deadline {
                return Err("system password update timed out".into());
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        if cancelled() {
            return Err("system password update cancelled".into());
        }
        if Instant::now() >= deadline {
            return Err("system password update timed out".into());
        }
        writeln!(input, "{password}")?;
        drop(input);
        loop {
            if cancelled() {
                return Err("system password update cancelled".into());
            }
            if Instant::now() >= deadline {
                return Err("system password update timed out".into());
            }
            if let Some(exit) = child.try_wait()? {
                return if exit.success() {
                    Ok(())
                } else {
                    Err("system password update failed".into())
                };
            }
            std::thread::sleep(Duration::from_millis(10));
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
    fn password_confirmation_uses_only_stdin_and_owned_command_is_cancelled() {
        let root = tempfile::tempdir().unwrap();
        let result = root.path().join("result");
        let start = Instant::now();
        set(Command::new("sh").args(["-c","IFS= read -r first; IFS= read -r second; printf '%s\\n%s\\n' \"$first\" \"$second\" > \"$FIXTURE_RESULT\""]).env("FIXTURE_RESULT",&result),"fixture-pass",Duration::from_secs(2),||false).unwrap();
        assert!(start.elapsed() >= Duration::from_millis(95));
        assert_eq!(
            std::fs::read(result).unwrap(),
            b"fixture-pass\nfixture-pass\n"
        );
        for invalid in ["fixture\npass", "fixture\rpass", "fixture\0pass"] {
            assert!(set(
                &mut Command::new("true"),
                invalid,
                Duration::from_secs(1),
                || false
            )
            .unwrap_err()
            .to_string()
            .contains("invalid root password"));
        }
        let start = Instant::now();
        assert!(set(
            Command::new("sh").args(["-c", "sleep 30 & wait"]),
            "fixture-pass",
            Duration::from_millis(60),
            || false
        )
        .unwrap_err()
        .to_string()
        .contains("timed out"));
        assert!(start.elapsed() < Duration::from_secs(1));
        let start = Instant::now();
        assert!(set(
            Command::new("sh").args(["-c", "sleep 30 & wait"]),
            "fixture-pass",
            Duration::from_secs(2),
            || start.elapsed() > Duration::from_millis(60)
        )
        .unwrap_err()
        .to_string()
        .contains("cancelled"));
        assert!(start.elapsed() < Duration::from_secs(1));
    }
}
