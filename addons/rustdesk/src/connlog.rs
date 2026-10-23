// SPDX-License-Identifier: AGPL-3.0-only
// Messages about connections, which anyone who knows the ID can cause, are
// limited to a few lines a minute; the rest are counted and reported as one
// line when the next minute starts.
use std::{
    sync::Mutex,
    time::{Duration, Instant},
};

const WINDOW: Duration = Duration::from_secs(60);
const LINES_PER_WINDOW: u32 = 20;

struct Limit {
    start: Instant,
    lines: u32,
    suppressed: u32,
}

impl Limit {
    // Whether a line may be written now, and how many were suppressed in
    // the window that just ended.
    // Starts a new window once the current one is over, returning how many
    // lines the old one suppressed.
    fn ended(&mut self, now: Instant) -> u32 {
        if now.saturating_duration_since(self.start) < WINDOW {
            return 0;
        }
        let suppressed = self.suppressed;
        self.start = now;
        self.lines = 0;
        self.suppressed = 0;
        suppressed
    }
    fn allow_at(&mut self, now: Instant) -> (bool, u32) {
        let ended = self.ended(now);
        if self.lines < LINES_PER_WINDOW {
            self.lines += 1;
            (true, ended)
        } else {
            self.suppressed += 1;
            (false, ended)
        }
    }
}

static LIMIT: Mutex<Option<Limit>> = Mutex::new(None);

pub(crate) fn allow() -> bool {
    let now = Instant::now();
    let (allowed, suppressed) = LIMIT
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
        .get_or_insert(Limit {
            start: now,
            lines: 0,
            suppressed: 0,
        })
        .allow_at(now);
    report(suppressed);
    allowed
}

// Reports a window's suppressed lines once it has ended, also when no
// further message comes.
pub(crate) fn flush() {
    let suppressed = LIMIT
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
        .as_mut()
        .map_or(0, |limit| limit.ended(Instant::now()));
    report(suppressed);
}

fn report(suppressed: u32) {
    if suppressed != 0 {
        eprintln!("RustDesk: {suppressed} connection messages suppressed in the last minute");
    }
}

macro_rules! connection_log {
    ($($argument:tt)*) => {
        if $crate::connlog::allow() {
            eprintln!($($argument)*);
        }
    };
}
pub(crate) use connection_log;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_flood_is_cut_to_a_few_lines_and_a_count() {
        let start = Instant::now();
        let mut limit = Limit {
            start,
            lines: 0,
            suppressed: 0,
        };
        let allowed = (0..1000)
            .filter(|_| limit.allow_at(start + Duration::from_secs(1)).0)
            .count();
        assert_eq!(allowed, LINES_PER_WINDOW as usize);
        assert_eq!(limit.allow_at(start + WINDOW), (true, 1000 - LINES_PER_WINDOW));
        assert_eq!(limit.allow_at(start + WINDOW), (true, 0));
        // A count is reported when the window ends without another message.
        for _ in 0..LINES_PER_WINDOW + 3 {
            limit.allow_at(start + WINDOW);
        }
        assert_eq!(limit.ended(start + WINDOW + Duration::from_secs(1)), 0);
        assert_eq!(limit.ended(start + WINDOW * 2), 5);
        assert_eq!(limit.ended(start + WINDOW * 3), 0);
    }
}
