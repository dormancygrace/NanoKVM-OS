//! Existing browser HID framing and descriptor compatibility, without device IO.
use std::{
    io::{self, Write},
    time::{Duration, Instant},
};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    Keyboard,
    Relative,
    Absolute,
}
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Report {
    kind: Kind,
    data: Vec<u8>,
}
#[derive(Debug, PartialEq, Eq)]
pub enum Frame {
    Heartbeat,
    Control(bool),
    Report(Report),
}
pub fn parse(frame: &[u8]) -> Option<Frame> {
    let (&event, bytes) = frame.split_first()?;
    match event {
        0 => Some(Frame::Heartbeat),
        1 if bytes.len() == 8 => Some(Frame::Report(Report {
            kind: Kind::Keyboard,
            data: bytes.to_vec(),
        })),
        2 => {
            let mut data = bytes.to_vec();
            if data.len() == 4 || data.len() == 6 {
                data.push(0);
            }
            let kind = match data.len() {
                5 => Kind::Relative,
                7 => Kind::Absolute,
                _ => return None,
            };
            Some(Frame::Report(Report { kind, data }))
        }
        3 if bytes == [0] => Some(Frame::Control(false)),
        3 if bytes == [1] => Some(Frame::Control(true)),
        _ => None,
    }
}
impl Report {
    pub fn kind(&self) -> Kind {
        self.kind
    }
    pub fn bytes(&self) -> &[u8] {
        &self.data
    }
    pub fn held(&self) -> bool {
        match self.kind {
            Kind::Keyboard => self.data[0] != 0 || self.data[2..].iter().any(|&b| b != 0),
            Kind::Relative | Kind::Absolute => self.data[0] != 0,
        }
    }
    pub fn starts_cooldown(&self) -> bool {
        self.kind == Kind::Keyboard
            || self.data[0] != 0
            || self.data[self.data.len() - 2..].iter().any(|&b| b != 0)
    }
    pub fn released(&self) -> Self {
        let mut data = vec![0; self.data.len()];
        if self.kind == Kind::Absolute {
            data[1..5].copy_from_slice(&self.data[1..5]);
        }
        Self {
            kind: self.kind,
            data,
        }
    }
    pub fn packets(&self, windows_pointer: bool) -> Vec<Vec<u8>> {
        if self.kind != Kind::Absolute || !windows_pointer {
            return vec![self.data.clone()];
        }
        let data = &self.data;
        let pressed = data[0] & 3 != 0;
        let flags = 4 | u8::from(pressed) | if data[0] & 2 != 0 { 2 } else { 0 };
        vec![
            vec![
                1,
                flags,
                data[1],
                data[2],
                data[3],
                data[4],
                0,
                if pressed { 4 } else { 0 },
            ],
            vec![2, data[0] & 0x1c, 0, 0, data[5], data[6]],
        ]
    }
}

/// The supplied writer must perform one nonblocking syscall per attempt.
pub fn write_bounded(writer: &mut impl Write, data: &[u8], timeout: Duration) -> io::Result<()> {
    let deadline = Instant::now()
        .checked_add(timeout)
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "invalid HID timeout"))?;
    loop {
        match writer.write(data) {
            Ok(n) if n == data.len() => return Ok(()),
            Ok(_) => return Err(io::Error::new(io::ErrorKind::WriteZero, "short HID write")),
            Err(e) if e.kind() == io::ErrorKind::WouldBlock => {
                let remaining = deadline.saturating_duration_since(Instant::now());
                if remaining.is_zero() {
                    return Err(io::ErrorKind::TimedOut.into());
                }
                std::thread::sleep(remaining.min(Duration::from_millis(1)));
            }
            Err(e) => return Err(e),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn report(bytes: &[u8]) -> Report {
        match parse(bytes).unwrap() {
            Frame::Report(report) => report,
            _ => panic!("expected report"),
        }
    }
    #[test]
    fn legacy_mouse_packets_release_and_windows_descriptor_are_preserved() {
        assert_eq!(parse(&[3, 2]), None);
        assert_eq!(parse(&[1, 0, 0]), None);
        assert_eq!(parse(&[0]), Some(Frame::Heartbeat));
        let relative = report(&[2, 0, 1, 2, 0]);
        assert_eq!(relative.data, [0, 1, 2, 0, 0]);
        assert_eq!(relative.kind, Kind::Relative);
        assert!(!relative.held() && !relative.starts_cooldown());
        let absolute = report(&[2, 3, 1, 2, 3, 4, 5, 6]);
        assert!(absolute.held() && absolute.starts_cooldown());
        assert_eq!(
            absolute.packets(true),
            [vec![1, 7, 1, 2, 3, 4, 0, 4], vec![2, 0, 0, 0, 5, 6]]
        );
        assert_eq!(absolute.released().data, [0, 1, 2, 3, 4, 0, 0]);
        assert_eq!(
            absolute.released().packets(true)[0],
            [1, 4, 1, 2, 3, 4, 0, 0]
        );
        let old_absolute = report(&[2, 0, 1, 2, 3, 4, 0]);
        assert_eq!(old_absolute.data, [0, 1, 2, 3, 4, 0, 0]);
        assert!(!report(&[1, 0, 7, 0, 0, 0, 0, 0, 0]).held()); // Reserved byte is not a held key.
    }
    struct Writer {
        blocked: bool,
        short: bool,
        calls: usize,
    }
    impl Write for Writer {
        fn write(&mut self, b: &[u8]) -> io::Result<usize> {
            self.calls += 1;
            if self.blocked {
                Err(io::ErrorKind::WouldBlock.into())
            } else {
                Ok(if self.short { b.len() - 1 } else { b.len() })
            }
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
    #[test]
    fn nonblocking_hid_write_bounds_retry_and_rejects_short_reports() {
        let mut writer = Writer {
            blocked: true,
            short: false,
            calls: 0,
        };
        assert_eq!(
            write_bounded(&mut writer, &[0; 8], Duration::ZERO)
                .unwrap_err()
                .kind(),
            io::ErrorKind::TimedOut
        );
        assert_eq!(writer.calls, 1);
        writer.blocked = false;
        writer.short = true;
        assert_eq!(
            write_bounded(&mut writer, &[0; 8], Duration::from_millis(50))
                .unwrap_err()
                .kind(),
            io::ErrorKind::WriteZero
        );
        writer.short = false;
        assert!(write_bounded(&mut writer, &[0; 8], Duration::from_millis(50)).is_ok());
    }
}
