//! Private packet transport. Descriptors become owned before any validation.
use crate::Error;
use std::{
    mem,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    time::{Duration, Instant},
};
pub const SIZE: usize = 24;
// Linux SCM_MAX_FD is 253. Receive all kernel-admissible rights so malformed
// messages can close them explicitly before rejecting the private protocol.
// A small ancillary buffer also exposes a QEMU linux-user truncation leak.
const MAX_RIGHTS: usize = 253;
const CONTROL_BYTES: usize =
    unsafe { libc::CMSG_SPACE((MAX_RIGHTS * mem::size_of::<i32>()) as u32) } as usize;
const CONTROL_WORDS: usize = CONTROL_BYTES.div_ceil(mem::size_of::<usize>());

fn abi_from<T: TryFrom<usize>>(value: usize) -> Result<T, Error> {
    T::try_from(value).map_err(|_| "native control length exceeds ABI".into())
}
fn abi_into<T: TryInto<usize>>(value: T) -> Result<usize, Error> {
    value
        .try_into()
        .map_err(|_| "native control length exceeds ABI".into())
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u8)]
pub enum Op {
    Init = 1,
    Hdmi = 2,
    Signal = 3,
    Gop = 4,
    GopMode = 5,
    Chroma = 6,
    ChromaStatus = 7,
    Keyframe = 8,
    Close = 9,
    Edid = 10,
    Mjpeg = 11,
    Video = 12,
    FrameDetect = 13,
}
#[derive(Clone, Copy, Debug)]
pub struct Packet(pub [u8; SIZE]);
impl Packet {
    pub fn request(op: Op, sequence: u32) -> Result<Self, Error> {
        if sequence == 0 {
            return Err("invalid capture sequence".into());
        }
        let mut p = [0; SIZE];
        p[..4].copy_from_slice(b"NKC1");
        p[4] = op as u8;
        p[5] = 1;
        p[8..12].copy_from_slice(&sequence.to_le_bytes());
        Ok(Self(p))
    }
    pub fn u16(&self, at: usize) -> u16 {
        u16::from_le_bytes(self.0[at..at + 2].try_into().unwrap())
    }
    pub fn u32(&self, at: usize) -> u32 {
        u32::from_le_bytes(self.0[at..at + 4].try_into().unwrap())
    }
    pub fn put16(&mut self, at: usize, value: u16) {
        self.0[at..at + 2].copy_from_slice(&value.to_le_bytes());
    }
    pub fn put32(&mut self, at: usize, value: u32) {
        self.0[at..at + 4].copy_from_slice(&value.to_le_bytes());
    }
    pub fn reply_to(&self, request: &Self) -> bool {
        self.0[..4] == *b"NKC1"
            && self.0[4] == request.0[4]
            && self.0[5] == 1
            && self.0[7] == 0
            && self.u32(8) == request.u32(8)
            && self.u32(20) == 0
    }
}
pub struct Socket {
    fd: OwnedFd,
}
impl Socket {
    #[cfg(all(test, feature = "native-fixture"))]
    pub(crate) fn test_owned(fd: OwnedFd) -> Self {
        Self { fd }
    }
    pub fn pair() -> Result<(Self, OwnedFd), Error> {
        let mut fds = [0; 2];
        if unsafe {
            libc::socketpair(
                libc::AF_UNIX,
                libc::SOCK_SEQPACKET | libc::SOCK_CLOEXEC,
                0,
                fds.as_mut_ptr(),
            )
        } != 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok((
            Self {
                fd: unsafe { OwnedFd::from_raw_fd(fds[0]) },
            },
            unsafe { OwnedFd::from_raw_fd(fds[1]) },
        ))
    }
    pub fn raw_fd(&self) -> i32 {
        self.fd.as_raw_fd()
    }
    pub fn shutdown(&self) {
        unsafe {
            libc::shutdown(self.raw_fd(), libc::SHUT_RDWR);
        }
    }
    fn wait(
        &self,
        events: i16,
        deadline: Instant,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        loop {
            if cancelled() {
                return Err("native capture cancelled".into());
            }
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err("native capture timed out".into());
            }
            let millis = remaining.as_millis().clamp(1, 50) as i32;
            let mut fd = libc::pollfd {
                fd: self.raw_fd(),
                events,
                revents: 0,
            };
            let n = unsafe { libc::poll(&mut fd, 1, millis) };
            if n > 0 {
                if fd.revents & libc::POLLNVAL != 0 {
                    return Err("native capture descriptor closed".into());
                }
                return Ok(());
            }
            if n < 0 && std::io::Error::last_os_error().kind() != std::io::ErrorKind::Interrupted {
                return Err(std::io::Error::last_os_error().into());
            }
        }
    }
    pub fn send(
        &self,
        packet: &Packet,
        descriptor: Option<&OwnedFd>,
        deadline: Instant,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(), Error> {
        let mut control = [0usize; 8];
        let mut iov = libc::iovec {
            iov_base: packet.0.as_ptr().cast_mut().cast(),
            iov_len: SIZE,
        };
        let mut msg: libc::msghdr = unsafe { mem::zeroed() };
        msg.msg_iov = &mut iov;
        msg.msg_iovlen = 1;
        if let Some(fd) = descriptor {
            msg.msg_control = control.as_mut_ptr().cast();
            msg.msg_controllen =
                abi_from(unsafe { libc::CMSG_SPACE(mem::size_of::<i32>() as u32) } as usize)?;
            unsafe {
                let header = libc::CMSG_FIRSTHDR(&msg);
                (*header).cmsg_level = libc::SOL_SOCKET;
                (*header).cmsg_type = libc::SCM_RIGHTS;
                (*header).cmsg_len =
                    abi_from(libc::CMSG_LEN(mem::size_of::<i32>() as u32) as usize)?;
                libc::CMSG_DATA(header).cast::<i32>().write(fd.as_raw_fd());
            }
        }
        loop {
            self.wait(libc::POLLOUT, deadline, cancelled)?;
            let n = unsafe {
                libc::sendmsg(self.raw_fd(), &msg, libc::MSG_NOSIGNAL | libc::MSG_DONTWAIT)
            };
            if n == SIZE as isize {
                return Ok(());
            }
            if n >= 0 {
                return Err("short native capture packet".into());
            }
            let error = std::io::Error::last_os_error();
            if !matches!(
                error.kind(),
                std::io::ErrorKind::WouldBlock | std::io::ErrorKind::Interrupted
            ) {
                return Err(error.into());
            }
        }
    }
    pub fn receive(
        &self,
        deadline: Instant,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<(Packet, Vec<OwnedFd>), Error> {
        loop {
            self.wait(libc::POLLIN, deadline, cancelled)?;
            let mut packet = Packet([0; SIZE]);
            let mut control = [0usize; CONTROL_WORDS];
            let mut iov = libc::iovec {
                iov_base: packet.0.as_mut_ptr().cast(),
                iov_len: SIZE,
            };
            let mut msg: libc::msghdr = unsafe { mem::zeroed() };
            msg.msg_iov = &mut iov;
            msg.msg_iovlen = 1;
            msg.msg_control = control.as_mut_ptr().cast();
            msg.msg_controllen = abi_from(mem::size_of_val(&control))?;
            let n = unsafe {
                libc::recvmsg(
                    self.raw_fd(),
                    &mut msg,
                    libc::MSG_CMSG_CLOEXEC | libc::MSG_DONTWAIT,
                )
            };
            if n < 0 {
                let error = std::io::Error::last_os_error();
                if matches!(
                    error.kind(),
                    std::io::ErrorKind::WouldBlock | std::io::ErrorKind::Interrupted
                ) {
                    continue;
                }
                return Err(error.into());
            }
            let mut descriptors = Vec::new();
            unsafe {
                let mut header = libc::CMSG_FIRSTHDR(&msg);
                while !header.is_null() {
                    let c = &*header;
                    let length = abi_into(c.cmsg_len)?;
                    if c.cmsg_level == libc::SOL_SOCKET
                        && c.cmsg_type == libc::SCM_RIGHTS
                        && length >= libc::CMSG_LEN(0) as usize
                    {
                        let count = (length - libc::CMSG_LEN(0) as usize) / mem::size_of::<i32>();
                        let data = libc::CMSG_DATA(header).cast::<i32>();
                        for i in 0..count {
                            descriptors.push(OwnedFd::from_raw_fd(data.add(i).read()));
                        }
                    }
                    header = libc::CMSG_NXTHDR(&msg, header);
                }
            }
            if n == 0 {
                return Err("native capture peer closed".into());
            }
            if n != SIZE as isize
                || descriptors.len() > 1
                || msg.msg_flags & (libc::MSG_TRUNC | libc::MSG_CTRUNC) != 0
            {
                return Err("invalid native capture packet size".into());
            }
            return Ok((packet, descriptors));
        }
    }
}
pub fn deadline(timeout: Duration) -> Result<Instant, Error> {
    Instant::now()
        .checked_add(timeout)
        .ok_or_else(|| "invalid native capture deadline".into())
}
