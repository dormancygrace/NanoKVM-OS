//! Exact-size immutable shared frames. A reservation survives every Arc clone.
use crate::Error;
use std::{
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    ptr::NonNull,
    sync::{
        atomic::{AtomicUsize, Ordering},
        Arc,
    },
};
pub const MAX_FRAME: usize = 64 * 1024 * 1024;
pub const DIRECT_HEADROOM: usize = 9;
pub struct Budget {
    limit: AtomicUsize,
    used: AtomicUsize,
}
impl Budget {
    pub fn new(limit: usize) -> Arc<Self> {
        Arc::new(Self {
            limit: AtomicUsize::new(limit),
            used: AtomicUsize::new(0),
        })
    }
    pub fn used(&self) -> usize {
        self.used.load(Ordering::Acquire)
    }
    pub fn limit(&self) -> usize {
        self.limit.load(Ordering::Acquire)
    }
    pub fn set_limit(&self, limit: usize) {
        self.limit.store(limit, Ordering::Release);
    }
    fn reserve(self: &Arc<Self>, size: usize) -> Result<Reservation, Error> {
        if size == 0 || size > MAX_FRAME + DIRECT_HEADROOM {
            return Err("invalid native frame size".into());
        }
        let page = usize::try_from(unsafe { libc::sysconf(libc::_SC_PAGESIZE) })
            .ok()
            .filter(|v| *v > 0)
            .ok_or("native page size unavailable")?;
        let charged = size
            .checked_add(page - 1)
            .ok_or("invalid native frame size")?
            / page
            * page;
        let mut used = self.used();
        loop {
            let next = used.checked_add(charged).ok_or("frame budget exceeded")?;
            if next > self.limit() {
                return Err("frame budget exceeded".into());
            }
            match self
                .used
                .compare_exchange_weak(used, next, Ordering::AcqRel, Ordering::Acquire)
            {
                Ok(_) => {
                    return Ok(Reservation {
                        budget: self.clone(),
                        size,
                        charged,
                    })
                }
                Err(current) => used = current,
            }
        }
    }
}
struct Reservation {
    budget: Arc<Budget>,
    size: usize,
    charged: usize,
}
impl Drop for Reservation {
    fn drop(&mut self) {
        self.budget.used.fetch_sub(self.charged, Ordering::AcqRel);
    }
}
pub(crate) struct Pending {
    fd: OwnedFd,
    reservation: Reservation,
    headroom: usize,
}
impl Pending {
    #[cfg(test)]
    pub fn new(budget: &Arc<Budget>, size: usize) -> Result<Self, Error> {
        Self::new_data(budget, size, 0)
    }
    pub fn new_data(
        budget: &Arc<Budget>,
        data_size: usize,
        headroom: usize,
    ) -> Result<Self, Error> {
        if data_size == 0 || data_size > MAX_FRAME || !matches!(headroom, 0 | DIRECT_HEADROOM) {
            return Err("invalid native frame dimensions".into());
        }
        let size = data_size
            .checked_add(headroom)
            .ok_or("invalid native frame size")?;
        let reservation = budget.reserve(size)?;
        let name = c"nanokvm-frame";
        let fd = unsafe {
            libc::memfd_create(name.as_ptr(), libc::MFD_CLOEXEC | libc::MFD_ALLOW_SEALING)
        };
        if fd < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        let fd = unsafe { OwnedFd::from_raw_fd(fd) };
        if unsafe { libc::ftruncate(fd.as_raw_fd(), size as libc::off_t) } != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        if unsafe {
            libc::fcntl(
                fd.as_raw_fd(),
                libc::F_ADD_SEALS,
                libc::F_SEAL_GROW | libc::F_SEAL_SHRINK,
            )
        } < 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok(Self {
            fd,
            reservation,
            headroom,
        })
    }
    #[cfg(test)]
    pub fn raw_fd(&self) -> i32 {
        self.fd.as_raw_fd()
    }
    pub fn descriptor(&self) -> &OwnedFd {
        &self.fd
    }
    pub fn finish(self) -> Result<Arc<Frame>, Error> {
        if self.headroom != 0 {
            return Err("Direct frame requires metadata".into());
        }
        self.seal()
    }
    pub fn finish_video(self, key: bool, timestamp: u64) -> Result<Arc<Frame>, Error> {
        if self.headroom != DIRECT_HEADROOM {
            return Err("missing Direct frame headroom".into());
        }
        let mut prefix = [0u8; DIRECT_HEADROOM];
        prefix[0] = u8::from(key);
        prefix[1..].copy_from_slice(&timestamp.to_le_bytes());
        loop {
            let n = unsafe {
                libc::pwrite(self.fd.as_raw_fd(), prefix.as_ptr().cast(), prefix.len(), 0)
            };
            if n == prefix.len() as isize {
                break;
            }
            if n < 0 && std::io::Error::last_os_error().kind() == std::io::ErrorKind::Interrupted {
                continue;
            }
            return Err("failed to write Direct frame prefix".into());
        }
        self.seal()
    }
    fn seal(self) -> Result<Arc<Frame>, Error> {
        // The C callback has already unmapped/closed its writable descriptor.
        // Sealing fails if any writer still owns an active shared writable map.
        if unsafe {
            libc::fcntl(
                self.fd.as_raw_fd(),
                libc::F_ADD_SEALS,
                libc::F_SEAL_WRITE | libc::F_SEAL_SEAL,
            )
        } < 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        let size = self.reservation.size;
        let address = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                size,
                libc::PROT_READ,
                libc::MAP_SHARED,
                self.fd.as_raw_fd(),
                0,
            )
        };
        if address == libc::MAP_FAILED {
            return Err(std::io::Error::last_os_error().into());
        }
        let Some(address) = NonNull::new(address.cast::<u8>()) else {
            unsafe {
                libc::munmap(address, size);
            }
            return Err("null native frame mapping".into());
        };
        Ok(Arc::new(Frame {
            address,
            _fd: self.fd,
            reservation: self.reservation,
            headroom: self.headroom,
        }))
    }
}
pub struct Frame {
    address: NonNull<u8>,
    _fd: OwnedFd,
    reservation: Reservation,
    headroom: usize,
}
// Successful construction installs F_SEAL_WRITE before exposing a readonly
// mapping. No writer can mutate these bytes, and Arc keeps the mapping alive.
unsafe impl Send for Frame {}
unsafe impl Sync for Frame {}
impl AsRef<[u8]> for Frame {
    fn as_ref(&self) -> &[u8] {
        unsafe { std::slice::from_raw_parts(self.address.as_ptr(), self.reservation.size) }
    }
}
struct SharedBytes {
    frame: Arc<Frame>,
    data_only: bool,
}
impl AsRef<[u8]> for SharedBytes {
    fn as_ref(&self) -> &[u8] {
        if self.data_only {
            self.frame.data()
        } else {
            self.frame.as_ref().as_ref()
        }
    }
}
impl Frame {
    pub fn packet_bytes(self: &Arc<Self>) -> axum::body::Bytes {
        axum::body::Bytes::from_owner(SharedBytes {
            frame: self.clone(),
            data_only: false,
        })
    }
    pub fn data_bytes(self: &Arc<Self>) -> axum::body::Bytes {
        axum::body::Bytes::from_owner(SharedBytes {
            frame: self.clone(),
            data_only: true,
        })
    }

    pub fn data(&self) -> &[u8] {
        &self.as_ref()[self.headroom..]
    }
    pub fn headroom(&self) -> usize {
        self.headroom
    }
    pub fn len(&self) -> usize {
        self.reservation.size
    }
    pub fn is_empty(&self) -> bool {
        false
    }
}
impl Drop for Frame {
    fn drop(&mut self) {
        unsafe {
            libc::munmap(self.address.as_ptr().cast(), self.reservation.size);
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn shared_frame_seals_are_immutable_and_reservation_lives_through_subscribers() {
        let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize;
        let budget = Budget::new(page);
        let pending = Pending::new(&budget, 8).unwrap();
        assert_eq!(budget.used(), page);
        assert_eq!(
            unsafe { libc::pwrite(pending.raw_fd(), b"frame123".as_ptr().cast(), 8, 0) },
            8
        );
        assert_eq!(unsafe { libc::ftruncate(pending.raw_fd(), 9) }, -1);
        let frame = pending.finish().unwrap();
        assert_eq!(frame.as_ref().as_ref(), b"frame123");
        let subscriber = frame.clone();
        drop(frame);
        assert_eq!(budget.used(), page);
        assert_eq!(
            unsafe { libc::pwrite(subscriber._fd.as_raw_fd(), b"x".as_ptr().cast(), 1, 0) },
            -1
        );
        drop(subscriber);
        assert_eq!(budget.used(), 0);
        assert!(Pending::new(&budget, 0).is_err());
        assert!(Pending::new(&budget, MAX_FRAME + 1).is_err());
        assert_eq!(budget.used(), 0);
        let first = Pending::new(&budget, 10).unwrap();
        assert!(Pending::new(&budget, 10).is_err());
        budget.set_limit(page - 1);
        assert!(Pending::new(&budget, 1).is_err());
        drop(first);
        assert_eq!(budget.used(), 0);
        budget.set_limit(page);
        let last = Pending::new(&budget, 5).unwrap();
        drop(last);
        assert_eq!(budget.used(), 0);
    }
    #[test]
    fn outstanding_c_write_mapping_prevents_frame_exposure() {
        let budget = Budget::new(unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize);
        let pending = Pending::new(&budget, 8).unwrap();
        let pointer = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                8,
                libc::PROT_WRITE,
                libc::MAP_SHARED,
                pending.raw_fd(),
                0,
            )
        };
        assert_ne!(pointer, libc::MAP_FAILED);
        assert!(pending.finish().is_err());
        assert_eq!(budget.used(), 0);
        assert_eq!(unsafe { libc::munmap(pointer, 8) }, 0);
    }
}

#[cfg(test)]
mod headroom_tests {
    use super::*;
    #[test]
    fn headroom_is_bounded_page_charged_and_never_exposed_without_metadata() {
        let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize;
        let budget = Budget::new(page * 2);
        let pending = Pending::new_data(&budget, page, DIRECT_HEADROOM).unwrap();
        assert_eq!(budget.used(), page * 2);
        assert!(pending.finish().is_err());
        assert_eq!(budget.used(), 0);
        assert!(Pending::new_data(&budget, 0, 9).is_err());
        assert!(Pending::new_data(&budget, MAX_FRAME + 1, 9).is_err());
        assert!(Pending::new_data(&budget, 10, 8).is_err());
        assert!(Pending::new(&budget, MAX_FRAME + 1).is_err());
        assert!(Pending::new(&budget, 1)
            .unwrap()
            .finish_video(true, 1)
            .is_err());
        assert_eq!(budget.used(), 0);
        let budget = Budget::new(MAX_FRAME + page);
        let maximum = Pending::new_data(&budget, MAX_FRAME, DIRECT_HEADROOM).unwrap();
        assert_eq!(budget.used(), MAX_FRAME + page);
        drop(maximum);
        assert_eq!(budget.used(), 0);
    }
}

#[cfg(test)]
mod concurrency_tests {
    use super::*;
    #[test]
    fn concurrent_admissions_charge_and_release_each_page_exactly_once() {
        let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize;
        let budget = Budget::new(page * 4);
        let start = Arc::new(std::sync::Barrier::new(17));
        let held = Arc::new(std::sync::Barrier::new(17));
        let release = Arc::new(std::sync::Barrier::new(17));
        let admitted = Arc::new(AtomicUsize::new(0));
        let mut threads = Vec::new();
        for _ in 0..16 {
            let budget = budget.clone();
            let start = start.clone();
            let held = held.clone();
            let release = release.clone();
            let admitted = admitted.clone();
            threads.push(std::thread::spawn(move || {
                start.wait();
                let reservation = Pending::new(&budget, 10).ok();
                if reservation.is_some() {
                    admitted.fetch_add(1, Ordering::AcqRel);
                }
                held.wait();
                release.wait();
                drop(reservation);
            }));
        }
        start.wait();
        held.wait();
        assert_eq!(admitted.load(Ordering::Acquire), 4);
        assert_eq!(budget.used(), page * 4);
        budget.set_limit(page); // Existing owners survive a soft-limit shrink.
        assert!(Pending::new(&budget, 1).is_err());
        release.wait();
        for thread in threads {
            thread.join().unwrap();
        }
        assert_eq!(budget.used(), 0);
        drop(Pending::new(&budget, 1).unwrap());
        assert_eq!(budget.used(), 0);
    }
}
