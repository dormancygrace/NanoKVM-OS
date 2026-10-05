use crate::{
    native_frame::{Budget, Frame, Pending},
    native_protocol::{self, Op, Packet, Socket},
    Error,
};
use std::{
    os::{fd::AsRawFd, unix::process::CommandExt},
    process::{Child, Command, Stdio},
    sync::Arc,
    time::Duration,
};
unsafe extern "C" {
    fn nk_capture_run(fd: i32) -> i32;
}
#[test]
fn c_fixture_child() {
    if std::env::var_os("NK_V3_CAPTURE_FIXTURE_CHILD").is_none() {
        return;
    }
    let result = unsafe { nk_capture_run(3) };
    std::process::exit(if result == 0 { 0 } else { 1 });
}
struct Fixture {
    socket: Socket,
    child: Child,
    sequence: u32,
    budget: Arc<Budget>,
}
impl Fixture {
    fn new(limit: usize) -> Self {
        let (socket, inherited) = Socket::pair().unwrap();
        let exe = std::env::current_exe().unwrap();
        let mut command = if cfg!(target_arch = "riscv64") {
            let runner =
                std::env::var_os("CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER").unwrap();
            let mut command = Command::new(runner);
            command.arg(exe);
            command
        } else {
            Command::new(exe)
        };
        command
            .args(["--exact", "capture_tests::c_fixture_child", "--nocapture"])
            .env("NK_V3_CAPTURE_FIXTURE_CHILD", "1")
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let fd = inherited.as_raw_fd();
        unsafe {
            command.pre_exec(move || {
                if fd == 3 {
                    if libc::fcntl(fd, libc::F_SETFD, 0) < 0 {
                        return Err(std::io::Error::last_os_error());
                    }
                } else if libc::dup2(fd, 3) < 0 {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let child = command.spawn().unwrap();
        drop(inherited);
        Self {
            socket,
            child,
            sequence: 0,
            budget: Budget::new(limit),
        }
    }
    fn request(&mut self, op: Op) -> Packet {
        self.sequence += 1;
        Packet::request(op, self.sequence).unwrap()
    }
    fn call(&self, request: &Packet) -> Result<(i32, Option<Arc<Frame>>), Error> {
        let deadline = native_protocol::deadline(Duration::from_secs(3))?;
        self.socket.send(request, None, deadline, &|| false)?;
        let mut storage: Option<Pending> = None;
        let mut offered = 0;
        loop {
            let (packet, descriptors) = self.socket.receive(deadline, &|| false)?;
            if !descriptors.is_empty() || !packet.reply_to(request) {
                return Err("invalid fixture reply".into());
            }
            match packet.0[6] {
                2 => {
                    assert!(storage.is_none());
                    offered = packet.u32(16);
                    let allocation =
                        Pending::new_data(&self.budget, offered as usize, request.u16(22) as usize);
                    let mut accept = packet;
                    accept.0[6] = 3;
                    if let Ok(pending) = allocation {
                        accept.put32(12, 0);
                        let fd = pending.descriptor();
                        self.socket.send(&accept, Some(fd), deadline, &|| false)?;
                        storage = Some(pending);
                    } else {
                        accept.put32(12, (-libc::ENOMEM) as u32);
                        accept.put32(16, 0);
                        self.socket.send(&accept, None, deadline, &|| false)?;
                    }
                }
                1 => {
                    let status = packet.u32(12) as i32;
                    let size = packet.u32(16);
                    if size == 0 {
                        return Ok((status, None));
                    }
                    assert_eq!(size, offered);
                    return Ok((
                        status,
                        Some({
                            let pending = storage.take().ok_or("missing frame descriptor")?;
                            if request.u16(22) == 9 {
                                pending.finish_video(status == 3, 0x0102030405060708)?
                            } else {
                                pending.finish()?
                            }
                        }),
                    ));
                }
                _ => return Err("invalid fixture reply kind".into()),
            }
        }
    }
    fn init(&mut self) {
        let mut request = self.request(Op::Init);
        request.put16(16, 422);
        request.0[21] = 1;
        assert_eq!(self.call(&request).unwrap().0, 0);
    }
    fn close(&mut self) {
        let request = self.request(Op::Close);
        assert_eq!(self.call(&request).unwrap().0, 0);
        let deadline = std::time::Instant::now() + Duration::from_secs(2);
        while self.child.try_wait().unwrap().is_none() {
            assert!(std::time::Instant::now() < deadline);
            std::thread::sleep(Duration::from_millis(5));
        }
        assert!(self.child.wait().unwrap().success());
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.socket.shutdown();
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
#[test]
fn actual_c_worker_init_controls_split_packs_budget_and_immutable_ownership() {
    let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize;
    let mut fixture = Fixture::new(page);
    let before = fixture.request(Op::Signal);
    assert_eq!(fixture.call(&before).unwrap().0, -libc::EPIPE);
    fixture.init();
    let signal = fixture.request(Op::Signal);
    assert_eq!(fixture.call(&signal).unwrap().0, 1);
    let mode = fixture.request(Op::GopMode);
    assert_eq!(fixture.call(&mode).unwrap().0, 1);
    let mut request = fixture.request(Op::Mjpeg);
    request.put16(16, 90);
    let (status, frame) = fixture.call(&request).unwrap();
    assert_eq!(status, 0);
    let frame = frame.unwrap();
    assert_eq!(frame.as_ref().as_ref(), b"synthetic-frame");
    assert_eq!(fixture.budget.used(), page);
    let cloned = frame.clone();
    drop(frame);
    let request_again = fixture.request(Op::Mjpeg);
    let mut request_again = request_again;
    request_again.put16(16, 90);
    assert!(fixture.call(&request_again).unwrap().0 < 0);
    assert_eq!(fixture.budget.used(), page);
    drop(cloned);
    assert_eq!(fixture.budget.used(), 0);
    for codec in [1, 2] {
        let mut request = fixture.request(Op::Video);
        request.put16(16, 3000);
        request.0[18] = codec;
        request.0[19] = 30;
        request.0[20] = 50;
        let (status, frame) = fixture.call(&request).unwrap();
        assert_eq!(status, if codec == 1 { 3 } else { 4 });
        assert_eq!(frame.unwrap().as_ref().as_ref(), b"synthetic-frame");
        assert_eq!(fixture.budget.used(), 0);
    }
    fixture.close();
}
#[test]
fn malformed_c_callback_packs_never_expose_partial_frames_or_leak_reservations() {
    let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize;
    let mut fixture = Fixture::new(page * 2);
    fixture.init();
    for quality in [80, 81, 82, 83, 84, 85, 86] {
        let mut request = fixture.request(Op::Mjpeg);
        request.put16(16, quality);
        let (status, frame) = fixture.call(&request).unwrap();
        assert!(status < 0, "quality{quality}");
        assert!(frame.is_none());
        assert_eq!(fixture.budget.used(), 0);
    }
    fixture.close();
}
#[test]
fn private_transport_deadlines_cancellation_and_invalid_packet_fd_ownership() {
    let (socket, other) = Socket::pair().unwrap();
    let start = std::time::Instant::now();
    assert!(socket
        .receive(start + Duration::from_millis(60), &|| false)
        .is_err());
    assert!(start.elapsed() < Duration::from_secs(1));
    assert!(socket
        .receive(start + Duration::from_secs(1), &|| true)
        .is_err());
    drop(other);
    assert!(socket
        .receive(std::time::Instant::now() + Duration::from_secs(1), &|| {
            false
        })
        .unwrap_err()
        .to_string()
        .contains("peer closed"));
    let mut fixture = Fixture::new(4096);
    let mut invalid = fixture.request(Op::Init);
    invalid.0[0] = b'X';
    let deadline = native_protocol::deadline(Duration::from_secs(1)).unwrap();
    fixture
        .socket
        .send(&invalid, None, deadline, &|| false)
        .unwrap();
    assert!(fixture.socket.receive(deadline, &|| false).is_err());
}

fn pipe() -> (std::os::fd::OwnedFd, std::os::fd::OwnedFd) {
    use std::os::fd::FromRawFd;
    let mut fds = [0; 2];
    assert_eq!(
        unsafe { libc::pipe2(fds.as_mut_ptr(), libc::O_CLOEXEC | libc::O_NONBLOCK) },
        0
    );
    unsafe {
        (
            std::os::fd::OwnedFd::from_raw_fd(fds[0]),
            std::os::fd::OwnedFd::from_raw_fd(fds[1]),
        )
    }
}
fn assert_pipe_closed(reader: &std::os::fd::OwnedFd) {
    // Other concurrently spawning fixture children may briefly inherit this
    // CLOEXEC fd between fork and exec. Require EOF within a bounded second.
    let deadline = std::time::Instant::now() + Duration::from_secs(1);
    loop {
        let mut byte = 0u8;
        let n = unsafe { libc::read(reader.as_raw_fd(), (&mut byte as *mut u8).cast(), 1) };
        if n == 0 {
            return;
        }
        assert!(
            n < 0 && std::io::Error::last_os_error().kind() == std::io::ErrorKind::WouldBlock,
            "unexpected pipe data"
        );
        assert!(
            std::time::Instant::now() < deadline,
            "transferred writer descriptor leaked"
        );
        std::thread::sleep(Duration::from_millis(5));
    }
}

fn raw_send(fd: i32, bytes: &[u8], descriptors: &[i32]) {
    use std::mem;
    let mut control = [0usize; 136];
    let mut iov = libc::iovec {
        iov_base: bytes.as_ptr().cast_mut().cast(),
        iov_len: bytes.len(),
    };
    let mut msg: libc::msghdr = unsafe { mem::zeroed() };
    msg.msg_iov = &mut iov;
    msg.msg_iovlen = 1;
    if !descriptors.is_empty() {
        msg.msg_control = control.as_mut_ptr().cast();
        msg.msg_controllen = unsafe { libc::CMSG_SPACE(mem::size_of_val(descriptors) as u32) } as _;
        unsafe {
            let header = libc::CMSG_FIRSTHDR(&msg);
            (*header).cmsg_level = libc::SOL_SOCKET;
            (*header).cmsg_type = libc::SCM_RIGHTS;
            (*header).cmsg_len = libc::CMSG_LEN(mem::size_of_val(descriptors) as u32) as _;
            std::ptr::copy_nonoverlapping(
                descriptors.as_ptr(),
                libc::CMSG_DATA(header).cast(),
                descriptors.len(),
            );
        }
    }
    assert_eq!(
        unsafe { libc::sendmsg(fd, &msg, libc::MSG_NOSIGNAL) },
        bytes.len() as isize
    );
}
#[test]
fn zero_short_oversized_and_excessive_rights_close_every_received_rust_descriptor() {
    for (size, count) in [(0, 1), (1, 1), (25, 1), (24, 2), (24, 32), (24, 253)] {
        let (socket, peer) = Socket::pair().unwrap();
        let (reader, writer) = pipe();
        let packet = vec![0u8; size];
        raw_send(peer.as_raw_fd(), &packet, &vec![writer.as_raw_fd(); count]);
        drop(writer);
        assert!(socket
            .receive(
                native_protocol::deadline(Duration::from_secs(1)).unwrap(),
                &|| false
            )
            .is_err());
        assert_pipe_closed(&reader);
    }
    let (socket, peer) = Socket::pair().unwrap();
    let (reader, writer) = pipe();
    let packet = Packet::request(Op::Signal, 1).unwrap();
    raw_send(peer.as_raw_fd(), &packet.0, &[writer.as_raw_fd()]);
    drop(writer);
    let (_, owned) = socket
        .receive(
            native_protocol::deadline(Duration::from_secs(1)).unwrap(),
            &|| false,
        )
        .unwrap();
    assert_eq!(owned.len(), 1);
    assert_ne!(
        unsafe { libc::fcntl(owned[0].as_raw_fd(), libc::F_GETFD) } & libc::FD_CLOEXEC,
        0
    );
    drop(owned);
    assert_pipe_closed(&reader);
}
#[test]
fn malformed_requests_close_every_descriptor_in_the_c_worker() {
    for (size, count) in [
        (0, 1),
        (1, 1),
        (25, 1),
        (24, 1),
        (24, 2),
        (24, 32),
        (24, 253),
    ] {
        let mut fixture = Fixture::new(4096);
        let request = fixture.request(Op::Init);
        let mut bytes = request.0.to_vec();
        bytes.resize(size, 0);
        let (reader, writer) = pipe();
        raw_send(
            fixture.socket.raw_fd(),
            &bytes,
            &vec![writer.as_raw_fd(); count],
        );
        drop(writer);
        assert!(fixture
            .socket
            .receive(
                native_protocol::deadline(Duration::from_secs(1)).unwrap(),
                &|| false
            )
            .is_err());
        assert_pipe_closed(&reader);
        assert!(!fixture.child.wait().unwrap().success());
    }
}
#[test]
fn c_worker_rejects_wrong_sized_unsealed_nonregular_and_extra_descriptors() {
    use std::os::fd::{FromRawFd, OwnedFd};
    let mut fixture = Fixture::new(8192);
    fixture.init();
    for variant in 0..7 {
        let mut request = fixture.request(Op::Mjpeg);
        request.put16(16, 90);
        let deadline = native_protocol::deadline(Duration::from_secs(1)).unwrap();
        fixture
            .socket
            .send(&request, None, deadline, &|| false)
            .unwrap();
        let (mut accept, descriptors) = fixture.socket.receive(deadline, &|| false).unwrap();
        assert!(descriptors.is_empty());
        assert_eq!(accept.0[6], 2);
        let total = accept.u32(16) as usize;
        accept.0[6] = 3;
        let (reader, writer) = pipe();
        let pending = Pending::new(&fixture.budget, total + usize::from(variant == 1)).unwrap();
        let unsealed = unsafe {
            OwnedFd::from_raw_fd(libc::memfd_create(
                c"unsealed".as_ptr(),
                libc::MFD_CLOEXEC | libc::MFD_ALLOW_SEALING,
            ))
        };
        assert_eq!(
            unsafe { libc::ftruncate(unsealed.as_raw_fd(), total as _) },
            0
        );
        match variant {
            0 => raw_send(fixture.socket.raw_fd(), &accept.0, &[]),
            1 => raw_send(fixture.socket.raw_fd(), &accept.0, &[pending.raw_fd()]),
            2 => raw_send(fixture.socket.raw_fd(), &accept.0, &[unsealed.as_raw_fd()]),
            3 => raw_send(fixture.socket.raw_fd(), &accept.0, &[writer.as_raw_fd()]),
            4 => raw_send(
                fixture.socket.raw_fd(),
                &accept.0,
                &[pending.raw_fd(), writer.as_raw_fd()],
            ),
            5 => {
                accept.put32(8, request.u32(8) + 1);
                raw_send(fixture.socket.raw_fd(), &accept.0, &[writer.as_raw_fd()]);
            }
            6 => {
                assert_eq!(
                    unsafe { libc::fcntl(pending.raw_fd(), libc::F_ADD_SEALS, libc::F_SEAL_WRITE) },
                    0
                );
                raw_send(fixture.socket.raw_fd(), &accept.0, &[pending.raw_fd()]);
            }
            _ => unreachable!(),
        }
        drop(writer);
        let (result, descriptors) = fixture.socket.receive(deadline, &|| false).unwrap();
        assert!(descriptors.is_empty());
        assert!(result.reply_to(&request));
        assert!((result.u32(12) as i32) < 0);
        assert_eq!(result.u32(16), 0);
        assert_pipe_closed(&reader);
        drop(pending);
        drop(unsealed);
        assert_eq!(fixture.budget.used(), 0);
    }
    fixture.close();
}
#[test]
fn direct_prefix_and_video_data_share_one_sealed_allocation() {
    let mut fixture = Fixture::new(8192);
    fixture.init();
    for codec in [1, 2] {
        let mut request = fixture.request(Op::Video);
        request.put16(16, 3000);
        request.0[18] = codec;
        request.0[19] = 30;
        request.0[20] = 50;
        request.put16(22, 9);
        let (status, frame) = fixture.call(&request).unwrap();
        let frame = frame.unwrap();
        let packet = frame.as_ref().as_ref();
        assert_eq!(status, if codec == 1 { 3 } else { 4 });
        assert_eq!(packet[0], u8::from(codec == 1));
        assert_eq!(&packet[1..9], &0x0102030405060708u64.to_le_bytes());
        assert_eq!(frame.data(), b"synthetic-frame");
        assert_eq!(frame.data().as_ptr(), unsafe { packet.as_ptr().add(9) });
        assert_eq!(frame.len(), 9 + frame.data().len());
        assert_eq!(frame.headroom(), 9);
        drop(frame);
        assert_eq!(fixture.budget.used(), 0);
    }
    fixture.close();
    for op in [Op::Video, Op::Mjpeg] {
        let mut fixture = Fixture::new(8192);
        fixture.init();
        let mut request = fixture.request(op);
        request.put16(22, if op == Op::Video { 8 } else { 9 });
        let deadline = native_protocol::deadline(Duration::from_secs(1)).unwrap();
        fixture
            .socket
            .send(&request, None, deadline, &|| false)
            .unwrap();
        assert!(fixture.socket.receive(deadline, &|| false).is_err());
        assert!(!fixture.child.wait().unwrap().success());
    }
}
#[test]
fn abandoned_offer_times_out_and_worker_closes_the_capture_cleanly() {
    let mut fixture = Fixture::new(8192);
    fixture.init();
    let mut request = fixture.request(Op::Mjpeg);
    request.put16(16, 90);
    let deadline = native_protocol::deadline(Duration::from_secs(4)).unwrap();
    fixture
        .socket
        .send(&request, None, deadline, &|| false)
        .unwrap();
    let (offer, descriptors) = fixture.socket.receive(deadline, &|| false).unwrap();
    assert!(descriptors.is_empty());
    assert_eq!(offer.0[6], 2);
    let (result, descriptors) = fixture.socket.receive(deadline, &|| false).unwrap();
    assert!(descriptors.is_empty());
    assert!((result.u32(12) as i32) < 0);
    assert_eq!(result.u32(16), 0);
    fixture.close();
}

fn worker_command() -> Command {
    let exe = std::env::current_exe().unwrap();
    let mut command = if cfg!(target_arch = "riscv64") {
        let runner = std::env::var_os("CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER").unwrap();
        let mut command = Command::new(runner);
        command.arg(exe);
        command
    } else {
        Command::new(exe)
    };
    command
        .args(["--exact", "capture_tests::c_fixture_child", "--nocapture"])
        .env("NK_V3_CAPTURE_FIXTURE_CHILD", "1")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    command
}
fn real_worker(budget: Arc<Budget>) -> crate::native_capture::Worker {
    crate::native_capture::Worker::spawn(worker_command(), budget).unwrap()
}
fn init_worker(worker: &mut crate::native_capture::Worker) {
    let outcome = worker
        .call(
            crate::native_capture::Request::Init {
                smart_gop: true,
                chroma: 422,
            },
            std::time::Instant::now(),
            Duration::from_secs(1),
            &|| false,
        )
        .unwrap();
    assert_eq!(outcome.status, 0);
    assert!(outcome.frame.is_none());
}
#[test]
fn rust_owner_drains_budget_refusal_and_shares_frames_after_worker_shutdown() {
    use crate::native_capture::{Codec, Request, VideoConfig};
    let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize;
    let budget = Budget::new(page);
    let mut worker = real_worker(budget.clone());
    init_worker(&mut worker);
    let origin = std::time::Instant::now();
    assert_eq!(
        worker
            .call(
                Request::FrameDetect(255),
                origin,
                Duration::from_secs(1),
                &|| false
            )
            .unwrap()
            .status,
        0
    );
    let config = VideoConfig {
        width: 0,
        height: 0,
        codec: Codec::H265,
        bitrate: 3000,
        gop: 30,
        fps: 50,
    };
    let frame = worker
        .call(
            Request::Video(config),
            origin,
            Duration::from_secs(1),
            &|| false,
        )
        .unwrap()
        .frame
        .unwrap();
    let payload = frame.packet_bytes();
    let data = frame.data_bytes();
    assert_eq!(data.as_ref(), b"synthetic-frame");
    assert_eq!(payload.as_ptr(), frame.as_ref().as_ref().as_ptr());
    assert_eq!(data.as_ptr(), frame.data().as_ptr());
    let rejected = worker
        .call(
            Request::Video(config),
            origin,
            Duration::from_secs(1),
            &|| false,
        )
        .unwrap();
    assert_eq!(rejected.status, -3);
    assert!(rejected.frame.is_none());
    assert!(!worker.closed());
    worker
        .call(Request::Close, origin, Duration::from_secs(1), &|| false)
        .unwrap();
    assert!(worker.closed());
    assert!(worker.pid().is_none());
    drop(worker);
    drop(frame);
    assert_eq!(budget.used(), page);
    assert_eq!(data.as_ref(), b"synthetic-frame");
    drop(payload);
    assert_eq!(budget.used(), page);
    drop(data);
    assert_eq!(budget.used(), 0);
}
#[test]
fn rust_owner_rejects_local_settings_before_effects_and_kills_cancelled_or_stalled_capture() {
    use crate::native_capture::Request;
    use std::sync::atomic::{AtomicBool, Ordering};
    let budget = Budget::new(8192);
    let mut worker = real_worker(budget.clone());
    init_worker(&mut worker);
    let origin = std::time::Instant::now();
    assert!(worker
        .call(Request::Gop(0), origin, Duration::from_secs(1), &|| false)
        .is_err());
    assert!(!worker.closed());
    assert!(worker
        .call(Request::Signal, origin, Duration::from_secs(1), &|| true)
        .is_err());
    assert!(!worker.closed());
    assert_eq!(
        worker
            .call(Request::Signal, origin, Duration::from_secs(1), &|| false)
            .unwrap()
            .status,
        1
    );
    drop(worker);
    for quality in [87, 88] {
        for cancel in [false, true] {
            let mut worker = real_worker(budget.clone());
            init_worker(&mut worker);
            let pid = worker.pid().unwrap();
            let stop = Arc::new(AtomicBool::new(false));
            let stopped = stop.clone();
            let thread = std::thread::spawn(move || {
                std::thread::sleep(Duration::from_millis(100));
                stopped.store(cancel, Ordering::Release);
            });
            let start = std::time::Instant::now();
            let result = worker.call(
                Request::Mjpeg {
                    width: 0,
                    height: 0,
                    quality,
                },
                origin,
                Duration::from_millis(300),
                &|| stop.load(Ordering::Acquire),
            );
            assert!(result.is_err());
            assert!(start.elapsed() < Duration::from_secs(2));
            assert!(worker.closed());
            assert!(worker.pid().is_none());
            assert_eq!(
                unsafe { libc::kill(pid as i32, 0) },
                -1,
                "native fixture process survived cancellation"
            );
            assert_eq!(
                std::io::Error::last_os_error().raw_os_error(),
                Some(libc::ESRCH)
            );
            assert_eq!(budget.used(), 0);
            thread.join().unwrap();
        }
    }
    assert!(crate::native_capture::Worker::launch(
        std::path::Path::new("/isolated-fixture"),
        budget
    )
    .is_err());
}
#[test]
fn malformed_worker_replies_poison_transport_without_exposing_frames_or_retaining_budget() {
    use crate::native_capture::{Request, Worker};
    for variant in 0..14 {
        let budget = Budget::new(8192);
        let (socket, peer) = Socket::pair().unwrap();
        let mut worker = Worker::test_socket(socket, budget.clone());
        let thread = std::thread::spawn(move || {
            let peer = Socket::test_owned(peer);
            let deadline = native_protocol::deadline(Duration::from_secs(1)).unwrap();
            let (request, descriptors) = peer.receive(deadline, &|| false).unwrap();
            assert!(descriptors.is_empty());
            let mut reply = request;
            reply.0[6] = 1;
            reply.0[12..].fill(0);
            match variant {
                0 => reply.0[0] = b'X',
                1 => reply.put32(8, request.u32(8) + 1),
                2 => reply.0[4] = Op::Signal as u8,
                3 => reply.put32(20, 1),
                4 => reply.0[6] = 99,
                5 => reply.put32(16, 1),
                6 => {} // Success without a frame.
                7 => {
                    reply.0[6] = 2;
                    reply.put32(16, 0);
                }
                8 => {
                    reply.0[6] = 2;
                    reply.put32(16, crate::native_frame::MAX_FRAME as u32 + 1);
                }
                9 => {
                    reply.0[6] = 2;
                    reply.put32(16, 14);
                    reply.put32(12, (-1i32) as u32);
                }
                10..=13 => {
                    reply.0[6] = 2;
                    reply.put32(16, 14);
                    peer.send(&reply, None, deadline, &|| false).unwrap();
                    let (accept, descriptors) = peer.receive(deadline, &|| false).unwrap();
                    assert_eq!(accept.0[6], 3);
                    assert_eq!(descriptors.len(), 1);
                    drop(descriptors);
                    match variant {
                        10 => {} // Duplicate offer.
                        11 => {
                            reply.0[6] = 1;
                            reply.put32(16, 15);
                        }
                        12 => {
                            reply.0[6] = 1;
                            reply.put32(12, (-1i32) as u32);
                        }
                        13 => {
                            reply.0[6] = 1;
                            reply.put32(16, 0);
                        }
                        _ => unreachable!(),
                    }
                }
                _ => unreachable!(),
            }
            peer.send(&reply, None, deadline, &|| false).unwrap();
        });
        assert!(
            worker
                .call(
                    Request::Mjpeg {
                        width: 0,
                        height: 0,
                        quality: 90
                    },
                    std::time::Instant::now(),
                    Duration::from_secs(1),
                    &|| false
                )
                .is_err(),
            "variant {variant}"
        );
        assert!(worker.closed());
        assert_eq!(budget.used(), 0);
        thread.join().unwrap();
    }
}

#[test]
fn ordinary_owner_drop_deinitializes_native_capture_before_reaping() {
    let directory = tempfile::tempdir().unwrap();
    let marker = directory.path().join("closed");
    let mut command = worker_command();
    command.env("NK_FIXTURE_CLOSE_FILE", &marker);
    let mut worker = crate::native_capture::Worker::spawn(command, Budget::new(8192)).unwrap();
    init_worker(&mut worker);
    let pid = worker.pid().unwrap();
    drop(worker);
    assert_eq!(std::fs::read_to_string(marker).unwrap(), "deinitialized\n");
    assert_eq!(unsafe { libc::kill(pid as i32, 0) }, -1);
    assert_eq!(
        std::io::Error::last_os_error().raw_os_error(),
        Some(libc::ESRCH)
    );
}

#[tokio::test(flavor = "current_thread")]
async fn async_actor_serializes_controls_with_one_native_owner_and_releases_frames() {
    use crate::{native_capture::Request, native_capture_actor::Actor};
    let budget = Budget::new(8192);
    let actor = Actor::new(real_worker(budget.clone())).unwrap();
    let origin = std::time::Instant::now();
    assert_eq!(
        actor
            .call(
                Request::Init {
                    smart_gop: true,
                    chroma: 422
                },
                origin,
                Duration::from_secs(1)
            )
            .await
            .unwrap()
            .status,
        0
    );
    let (signal, gop) = tokio::join!(
        actor.call(Request::Signal, origin, Duration::from_secs(1)),
        actor.call(Request::Gop(31), origin, Duration::from_secs(1))
    );
    assert_eq!(signal.unwrap().status, 1);
    assert_eq!(gop.unwrap().status, 0);
    let frame = actor
        .call(
            Request::Mjpeg {
                width: 0,
                height: 0,
                quality: 90,
            },
            origin,
            Duration::from_secs(1),
        )
        .await
        .unwrap()
        .frame
        .unwrap();
    actor.shutdown().await.unwrap();
    assert!(actor.finished());
    assert_eq!(frame.as_ref().as_ref(), b"synthetic-frame");
    drop(frame);
    assert_eq!(budget.used(), 0);
}
#[tokio::test(flavor = "current_thread")]
async fn async_actor_bounded_queue_deadlines_and_request_drop_do_not_starve_runtime() {
    use crate::{native_capture::Request, native_capture_actor::Actor};
    let budget = Budget::new(8192);
    let actor = Actor::new(real_worker(budget.clone())).unwrap();
    let origin = std::time::Instant::now();
    actor
        .call(
            Request::Init {
                smart_gop: true,
                chroma: 422,
            },
            origin,
            Duration::from_secs(1),
        )
        .await
        .unwrap();
    let reader = actor.clone();
    let active = tokio::spawn(async move {
        reader
            .call(
                Request::Mjpeg {
                    width: 0,
                    height: 0,
                    quality: 88,
                },
                origin,
                Duration::from_secs(5),
            )
            .await
    });
    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    while budget.used() == 0 {
        assert!(std::time::Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    let mut queued = Vec::new();
    for _ in 0..8 {
        let handle = actor.clone();
        queued.push(tokio::spawn(async move {
            handle
                .call(Request::Signal, origin, Duration::from_millis(100))
                .await
        }));
    }
    // Yield until the eight tasks submit their fixed queue slots.
    tokio::time::sleep(Duration::from_millis(10)).await;
    let error = actor
        .call(Request::Signal, origin, Duration::from_secs(1))
        .await
        .err()
        .unwrap();
    assert!(error.to_string().contains("queue is full"));
    for task in queued {
        assert!(task.await.unwrap().is_err());
    }
    assert!(!actor.finished());
    assert!(budget.used() > 0); // No queued request interleaved with native write.
    active.abort();
    let _ = active.await;
    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    while !actor.finished() {
        assert!(std::time::Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    assert_eq!(budget.used(), 0);
    assert!(actor
        .call(Request::Signal, origin, Duration::from_secs(1))
        .await
        .is_err());
    actor.shutdown().await.unwrap();
}
#[tokio::test(flavor = "current_thread")]
async fn async_actor_shutdown_cancels_native_writer_and_all_queued_callers() {
    use crate::{native_capture::Request, native_capture_actor::Actor};
    let budget = Budget::new(8192);
    let actor = Actor::new(real_worker(budget.clone())).unwrap();
    let origin = std::time::Instant::now();
    actor
        .call(
            Request::Init {
                smart_gop: true,
                chroma: 422,
            },
            origin,
            Duration::from_secs(1),
        )
        .await
        .unwrap();
    let reader = actor.clone();
    let active = tokio::spawn(async move {
        reader
            .call(
                Request::Mjpeg {
                    width: 0,
                    height: 0,
                    quality: 88,
                },
                origin,
                Duration::from_secs(5),
            )
            .await
    });
    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    while budget.used() == 0 {
        assert!(std::time::Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    let waiting = actor.clone();
    let queued = tokio::spawn(async move {
        waiting
            .call(Request::Signal, origin, Duration::from_secs(5))
            .await
    });
    tokio::task::yield_now().await;
    let (first, second) = tokio::join!(actor.shutdown(), actor.shutdown());
    first.unwrap();
    second.unwrap();
    assert!(actor.finished());
    assert!(active.await.unwrap().is_err());
    assert!(queued.await.unwrap().is_err());
    assert!(actor.finished());
    assert_eq!(budget.used(), 0);
}
