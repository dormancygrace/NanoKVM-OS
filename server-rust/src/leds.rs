//! Keyboard host output reports: one readiness-driven reader, replaceable
//! descriptors, and a latest-value watch notification instead of an event queue.
use crate::Error;
use serde::Serialize;
use std::{
    fs::{File, OpenOptions},
    io::Read,
    os::fd::AsRawFd,
    os::unix::fs::{FileTypeExt, OpenOptionsExt},
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex, Weak,
    },
    time::{SystemTime, UNIX_EPOCH},
};
use tokio::{io::unix::AsyncFd, sync::watch};
const ZERO_TIME: &str = "0001-01-01T00:00:00Z";
#[derive(Clone)]
struct Raw {
    bits: u8,
    known: bool,
    time: String,
}
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub keyboard_enabled: bool,
    pub num_lock: bool,
    pub caps_lock: bool,
    pub scroll_lock: bool,
    pub known: bool,
    pub updated_at: String,
}
pub struct Leds {
    root: PathBuf,
    status: watch::Sender<Raw>,
    source: watch::Sender<Option<Arc<File>>>,
    lifecycle: Mutex<()>,
    started: AtomicBool,
    stopping: AtomicBool,
    retry: AtomicBool,
    enabled: AtomicBool,
}
struct Descriptor(Arc<File>);
impl AsRawFd for Descriptor {
    fn as_raw_fd(&self) -> std::os::fd::RawFd {
        self.0.as_raw_fd()
    }
}
fn timestamp(time: SystemTime) -> Result<String, Error> {
    let duration = time.duration_since(UNIX_EPOCH)?;
    let seconds = duration.as_secs().try_into()?;
    let mut output = std::mem::MaybeUninit::<libc::tm>::uninit();
    // gmtime_r fills the caller-owned struct on success; time_t is checked.
    if unsafe { libc::gmtime_r(&seconds, output.as_mut_ptr()) }.is_null() {
        return Err("LED timestamp unavailable".into());
    }
    let tm = unsafe { output.assume_init() };
    let year = i64::from(tm.tm_year) + 1900;
    if !(0..=9999).contains(&year) {
        return Err("LED timestamp out of range".into());
    }
    let fraction = if duration.subsec_nanos() == 0 {
        String::new()
    } else {
        format!(".{:09}", duration.subsec_nanos())
            .trim_end_matches('0')
            .into()
    };
    Ok(format!(
        "{year:04}-{:02}-{:02}T{:02}:{:02}:{:02}{fraction}Z",
        tm.tm_mon + 1,
        tm.tm_mday,
        tm.tm_hour,
        tm.tm_min,
        tm.tm_sec
    ))
}
impl Leds {
    pub fn new(root: PathBuf) -> Arc<Self> {
        let enabled = root
            .join("sys/kernel/config/usb_gadget/g0/configs/c.1/hid.GS0")
            .exists();
        Arc::new(Self {
            root,
            status: watch::channel(Raw {
                bits: 0,
                known: false,
                time: ZERO_TIME.into(),
            })
            .0,
            source: watch::channel(None).0,
            lifecycle: Mutex::new(()),
            started: AtomicBool::new(false),
            stopping: AtomicBool::new(false),
            retry: AtomicBool::new(false),
            enabled: AtomicBool::new(enabled),
        })
    }
    pub fn snapshot(&self, rest: bool) -> Status {
        let raw = self.status.borrow();
        let enabled = self.enabled.load(Ordering::Acquire);
        Status {
            keyboard_enabled: enabled,
            num_lock: raw.bits & 1 != 0,
            caps_lock: raw.bits & 2 != 0,
            scroll_lock: raw.bits & 4 != 0,
            known: enabled && raw.known,
            updated_at: if rest && raw.time == ZERO_TIME {
                String::new()
            } else {
                raw.time.clone()
            },
        }
    }
    pub fn missing_reader(&self) -> bool {
        self.source.borrow().is_none()
    }
    /// Call off the async event loop after a gadget lifecycle transition.
    pub fn refresh(&self) {
        let enabled = self
            .root
            .join("sys/kernel/config/usb_gadget/g0/configs/c.1/hid.GS0")
            .exists();
        if self.enabled.swap(enabled, Ordering::AcqRel) != enabled {
            self.status.send_modify(|_| {});
        }
    }
    pub fn replace_reader(&self, reader: Option<File>) -> Result<(), Error> {
        let _lock = self
            .lifecycle
            .lock()
            .map_err(|_| "LED lifecycle unavailable")?;
        if self.stopping.load(Ordering::Acquire) {
            return Err("LED reader stopped".into());
        }
        self.source.send_replace(reader.map(Arc::new));
        self.retry.store(false, Ordering::Release);
        Ok(())
    }
    fn detach(&self, reader: &Arc<File>) {
        let Ok(_lock) = self.lifecycle.lock() else {
            return;
        };
        self.source.send_if_modified(|current| {
            if current
                .as_ref()
                .is_some_and(|file| Arc::ptr_eq(file, reader))
            {
                self.retry.store(true, Ordering::Release);
                *current = None;
                true
            } else {
                false
            }
        });
    }
    fn update(&self, reader: &Arc<File>, bits: u8, time: SystemTime) -> Result<(), Error> {
        let stamp = timestamp(time)?;
        let _lock = self
            .lifecycle
            .lock()
            .map_err(|_| "LED lifecycle unavailable")?;
        if !self
            .source
            .borrow()
            .as_ref()
            .is_some_and(|current| Arc::ptr_eq(current, reader))
        {
            return Ok(());
        }
        self.status.send_if_modified(|raw| {
            let bits = bits & 7;
            let changed = !raw.known || raw.bits != bits;
            *raw = Raw {
                bits,
                known: true,
                time: stamp,
            };
            changed
        });
        Ok(())
    }
    pub(crate) fn subscribe(&self) -> watch::Receiver<impl Clone> {
        self.status.subscribe()
    }
    pub fn start(self: &Arc<Self>) {
        let Ok(handle) = tokio::runtime::Handle::try_current() else {
            return;
        };
        if self.started.swap(true, Ordering::AcqRel) {
            return;
        }
        handle.spawn(reader(Arc::downgrade(self), self.source.subscribe()));
    }
    pub fn stop(&self) {
        let _lock = self.lifecycle.lock().ok();
        self.stopping.store(true, Ordering::Release);
        self.source.send_replace(None);
    }
    fn retry_reader(&self) -> Result<(), Error> {
        let path = self.root.join("dev/hidg0").canonicalize()?;
        if !path.starts_with(&self.root)
            || !path.metadata()?.file_type().is_char_device()
            || ["disable_hid", "usb.disable_keyboard"]
                .iter()
                .any(|marker| self.root.join("boot").join(marker).exists())
        {
            return Err("keyboard LED descriptor unavailable".into());
        }
        let reader = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NONBLOCK | libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(path)?;
        self.refresh();
        let _lock = self
            .lifecycle
            .lock()
            .map_err(|_| "LED lifecycle unavailable")?;
        if !self.stopping.load(Ordering::Acquire)
            && self.source.borrow().is_none()
            && self.retry.load(Ordering::Acquire)
        {
            self.source.send_replace(Some(Arc::new(reader)));
            self.retry.store(false, Ordering::Release);
        }
        Ok(())
    }
}
async fn reader(store: Weak<Leds>, mut source: watch::Receiver<Option<Arc<File>>>) {
    loop {
        if store
            .upgrade()
            .is_none_or(|store| store.stopping.load(Ordering::Acquire))
        {
            return;
        }
        let current = source.borrow_and_update().clone();
        let Some(current) = current else {
            let retry = store
                .upgrade()
                .is_some_and(|store| store.retry.load(Ordering::Acquire));
            if retry {
                tokio::select! {
                    biased;
                    changed = source.changed() => { if changed.is_err() { return; } }
                    _ = tokio::time::sleep(std::time::Duration::from_secs(2)) => {
                        let weak = store.clone();
                        let _ = tokio::task::spawn_blocking(move || { if let Some(store) = weak.upgrade() { let _ = store.retry_reader(); } }).await;
                    }
                }
            } else if source.changed().await.is_err() {
                return;
            }
            continue;
        };
        let fd = match AsyncFd::new(Descriptor(current.clone())) {
            Ok(fd) => fd,
            Err(_) => {
                if let Some(store) = store.upgrade() {
                    store.detach(&current);
                }
                continue;
            }
        };
        loop {
            tokio::select! {
                biased;
                changed = source.changed() => { if changed.is_err() { return; } break; }
                ready = fd.readable() => {
                    let Ok(mut ready) = ready else { if let Some(store) = store.upgrade() { store.detach(&current); } break; };
                    let mut bytes = [0u8;64];
                    match ready.try_io(|file| (&*file.get_ref().0).read(&mut bytes)) {
                        Ok(Ok(n)) if n > 0 => { if let Some(store) = store.upgrade() { let _ = store.update(&current, bytes[0], SystemTime::now()); } }
                        Err(_) => {}, // WouldBlock clears readiness; await the next event.
                        _ => { if let Some(store) = store.upgrade() { store.detach(&current); } break; }
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        io::Write,
        os::{fd::OwnedFd, unix::net::UnixStream},
        time::Duration,
    };
    fn pipe() -> (File, UnixStream) {
        let (input, output) = UnixStream::pair().unwrap();
        input.set_nonblocking(true).unwrap();
        (File::from(OwnedFd::from(input)), output)
    }
    #[tokio::test]
    async fn reader_readiness_replacement_stale_eof_and_stop_preserve_led_states() {
        let temp = tempfile::tempdir().unwrap();
        std::fs::create_dir_all(
            temp.path()
                .join("sys/kernel/config/usb_gadget/g0/configs/c.1/hid.GS0"),
        )
        .unwrap();
        let store = Leds::new(temp.path().into());
        assert!(!store.snapshot(true).known && store.snapshot(true).updated_at.is_empty());
        assert_eq!(store.snapshot(false).updated_at, ZERO_TIME);
        let mut status = store.subscribe();
        let (input, mut output) = pipe();
        store.replace_reader(Some(input)).unwrap();
        let old = store.source.borrow().clone().unwrap();
        store.start();
        output.write_all(&[7]).unwrap();
        tokio::time::timeout(Duration::from_secs(3), status.changed())
            .await
            .unwrap()
            .unwrap();
        let state = store.snapshot(false);
        assert!(state.known && state.num_lock && state.caps_lock && state.scroll_lock);
        let (replacement, mut next) = pipe();
        store.replace_reader(Some(replacement)).unwrap();
        store.detach(&old);
        store.update(&old, 0, SystemTime::now()).unwrap();
        assert!(!store.missing_reader() && store.snapshot(false).caps_lock);
        next.write_all(&[2]).unwrap();
        tokio::time::timeout(Duration::from_secs(3), status.changed())
            .await
            .unwrap()
            .unwrap();
        assert!(!store.snapshot(false).num_lock && store.snapshot(false).caps_lock);
        let current = store.source.borrow().clone().unwrap();
        store
            .update(
                &current,
                2,
                UNIX_EPOCH + Duration::new(1640995200, 123450000),
            )
            .unwrap();
        assert!(!status.has_changed().unwrap());
        assert_eq!(
            store.snapshot(true).updated_at,
            "2022-01-01T00:00:00.12345Z"
        );
        drop(next);
        tokio::time::timeout(Duration::from_secs(3), async {
            while !store.missing_reader() {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
        store.stop();
        assert!(store.replace_reader(None).is_err());
    }
}
