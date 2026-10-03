use std::{
    io,
    sync::atomic::{AtomicU64, Ordering},
    time::{SystemTime, UNIX_EPOCH},
};
static REGISTERED: AtomicU64 = AtomicU64::new(0);
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
        "last_registration_ack": timestamp, "updated_at": now()
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
