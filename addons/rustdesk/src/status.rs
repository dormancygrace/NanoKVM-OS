use std::{
    io,
    sync::atomic::{AtomicU64, Ordering},
    time::{SystemTime, UNIX_EPOCH},
};
static REGISTERED: AtomicU64 = AtomicU64::new(0);
// Recent wrong passwords (reset by a correct one) and when the last came.
static PASSWORD_FAILURES: AtomicU64 = AtomicU64::new(0);
static LAST_PASSWORD_FAILURE: AtomicU64 = AtomicU64::new(0);
pub fn password_failures(recent: u32) {
    PASSWORD_FAILURES.store(u64::from(recent), Ordering::Relaxed);
    if recent != 0 {
        LAST_PASSWORD_FAILURE.store(now(), Ordering::Relaxed);
    }
}
pub fn registered() {
    REGISTERED.store(now(), Ordering::Relaxed);
}
pub fn disconnected() {
    REGISTERED.store(0, Ordering::Relaxed);
}
fn now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}
pub fn write(id: &str, sessions: usize) -> io::Result<()> {
    let timestamp = REGISTERED.load(Ordering::Relaxed);
    let data = serde_json::to_vec(&serde_json::json!({
        "pid": std::process::id(), "id": id, "sessions": sessions,
        "registered": timestamp != 0 && now().saturating_sub(timestamp) < 60,
        "last_registration_ack": timestamp, "updated_at": now(),
        "password_failures": PASSWORD_FAILURES.load(Ordering::Relaxed),
        "last_password_failure": LAST_PASSWORD_FAILURE.load(Ordering::Relaxed)
    }))?;
    std::fs::write("/run/nanokvm-rustdesk/.status.tmp", data)?;
    std::fs::rename(
        "/run/nanokvm-rustdesk/.status.tmp",
        "/run/nanokvm-rustdesk/status.json",
    )
}
pub struct Guard;
impl Drop for Guard {
    fn drop(&mut self) {
        let _ = std::fs::remove_file("/run/nanokvm-rustdesk/status.json");
    }
}
