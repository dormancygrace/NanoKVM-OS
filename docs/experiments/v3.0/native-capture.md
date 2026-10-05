# Native capture boundary

Status: Rust/C source and synthetic execution implemented; hardware and media routes remain unqualified. The default Rust server does not launch the helper. This is one component of the complete Go replacement, not a completed migration.

## Ownership and linkage

The Rust server remains a generic static riscv64 musl executable. The retained capture stack is distributed as shared libraries and cannot be loaded by that static runtime. `nanokvm-capture-worker` is a small native C hardware owner linked to the retained musl/libkvm stack. All browser transports, subscriptions, authorization and settings remain in Rust; there is no Go transport bridge.

`native_capture::Worker` owns a private inherited `SOCK_SEQPACKET` socketpair and a child process group. Mutable ownership permits one native operation at a time. `native_capture_actor::Actor` adds one blocking owner thread and eight bounded queued commands. It checks cancellation and absolute deadlines before admission. Dropping an asynchronous request cancels its work; queued cancellation does not disturb the active read. Shutdown cancels active work, rejects queued callers and joins the owner thread outside the Tokio event loop. Concurrent shutdown calls wait for the same join.

Normal teardown sends CLOSE so C executes `kvmv_deinit` and joins native work. A timeout, in-flight cancellation or malformed private reply poisons the connection and terminates/reaps its owned process group. The actor does not automatically relaunch after an uncertain native failure. Actual DMA/driver cleanup after that fallback requires the agreed hardware qualification; the synthetic fixture proves process and shared-memory ownership only.

`build-v3-capture.py` checks fourteen function-pointer types against immutable baseline `a53b25579ab87cc98f85323b4743deb0d4da907b`. The actual C declarations are included alongside that header, so conflicting declarations fail compilation. The script verifies all fourteen exported symbols in the matched libkvm and links the dynamic target helper. It only inspects the resulting ELF; it never executes or installs it. `native-capture-build.json` records hashes, interpreter and dependencies.

## Private wire format

Every packet is exactly 24 bytes in little endian. It uses explicit byte offsets, not C/Rust struct layout.

| Bytes | Common field |
| --- | --- |
| 0–3 | `NKC1` magic |
| 4 | Opcode |
| 5 | Version 1 |
| 6 | Request 0, result 1, frame offer 2, frame accept 3 |
| 7 | Zero |
| 8–11 | Nonzero u32 sequence |

Requests use width at 12–13, height at 14–15, MJPEG quality/video bitrate or initial chroma at 16–17, codec at 18, GOP at 19, FPS at 20, scalar parameter at 21 and headroom at 22–23. Headroom is zero except VIDEO may use nine. Opcodes are INIT, HDMI, SIGNAL, GOP, GOP_MODE, CHROMA, CHROMA_STATUS, KEYFRAME, CLOSE, EDID, MJPEG, VIDEO and FRAME_DETECT. C applies GOP mode before init and chroma afterwards; reads and controls require initialized capture. Failed initial chroma selection deinitializes before returning failure.

Replies use signed i32 status at 12–15, data length at 16–19 and zero at 20–23. Offers must match the request/opcode/sequence, carry status zero and advertise 1 through 64 MiB. Rust sends an ACCEPT containing one frame descriptor, or rejects allocation without a descriptor and drains the failed native read. Duplicate offers, foreign descriptors, mismatched identity/length, or successful results without a complete frame close the Rust transport.

Both receivers immediately own every received `SCM_RIGHTS` descriptor, including malformed or zero-length packets. They receive the complete Linux maximum of 253 rights and reject more than one, while retaining packet/ancillary truncation checks. Receiving all rights makes closure explicit and avoids the QEMU linux-user truncation defect found in the initial tests. The observed QEMU 8.2.2 failure only affected excess rights beyond the small guest control buffer; zero/short/oversized packets and ordinary transfer passed. The same missing-close conversion is visible in upstream 11.1.2. See [Linux SCM_MAX_FD](https://github.com/torvalds/linux/blob/master/include/net/scm.h) and [QEMU conversion source](https://github.com/qemu/qemu/blob/v11.1.2/linux-user/syscall.c). No ownership test is skipped.

## One callback copy, immutable frame

The first borrowed native pack advertises its total length. Rust admits an exact-size `memfd`, charges whole memory pages and installs grow/shrink seals before sending its descriptor. The C callback accepts only a regular descriptor with exactly the expected length and those seals. It maps writable shared memory, closes its descriptor and copies contiguous native packs exactly once. Packs must be nonempty, keep the same total and cover the complete frame. C unmaps before returning a successful result. An abandoned offer has a two-second handshake bound.

Video storage includes nine bytes of Direct headroom. C writes encoded data after that prefix. Rust fills the keyframe byte and little-endian microsecond timestamp after C unmaps, then installs WRITE and SEAL seals before exposing a readonly mapping. A still-live writable mapping prevents sealing and therefore frame exposure. Native key result 3 and delta result 4 retain their original meaning. H.265 is supported by this native/Direct boundary; H.265 WebRTC must still always return `h265-webrtc-disabled`.

`Arc<Frame>` owns the mapping, descriptor and page reservation. Existing `Bytes::from_owner` support shares either the whole Direct packet or the encoded data slice without copying the frame. The reservation survives every Arc/Bytes subscriber and is reclaimed only after the last owner. Shrinking the budget leaves live frames intact and rejects new admissions above the limit. This is a frame admission budget, not the finished replacement of Go's global `GOMEMLIMIT` behavior.

## Reproduction and remaining gates

Run `python3 scripts/build-v3-capture.py` for declaration/type/export/ELF checks. Run `python3 scripts/check-v3.py --host --target --tls` for the server gates; its test/clippy commands enable the explicit `native-fixture` feature. That feature links the synthetic C ABI implementations via maintained [rust-lang/cc-rs](https://github.com/rust-lang/cc-rs) 1.6.0 already present in Cargo.lock. Default release builds do not enable it and never link the fixture or vendor libraries into Rust.

Twenty added tests cover real C split callbacks, incomplete/invalid packs, descriptor types/seals/lengths/counts through 253, bounded abandoned offers, readonly sealing, headroom/data pointers, shared frame lifetime, concurrent page admission, forged replies, ordinary native deinit, timeout/cancellation while C is mapped, bounded async queue admission and concurrent shutdown. One test is the child fixture entry; nineteen are substantive parent checks. The actual vendor library is not loaded by these tests.

Remaining work includes saved screen settings, HDMI demand/idle/warmup/leases, monitor maintenance and audio serialization through the actor, shared video subscriptions, MJPEG/Direct/WebRTC/RustDesk transports, full memory policy, packaging/OpenRC/image gates and the agreed hardware window. No stand activation, performance or native driver-cleanup claim is made by this stage.
