# H.264 and H.265 video

The existing on-screen video menu contains a single **Codec** selector for the
Direct and WebRTC modes. H.265/HEVC is the default; H.264/AVC remains available
as a compatibility fallback. MJPEG does not use this selector.

NanoKVM intentionally exposes only CBR. The existing **Quality** item controls
the device-wide bitrate (1000, 2000, 3000 or 10000 Kbit/s), and the existing
**GOP** item controls the keyframe interval. This keeps codec selection in the
same place as the other frequently used video controls without duplicating
bitrate and GOP in Settings.

## Capability handling

H.265 is selectable only when the browser reports support for the active
transport:

- Direct checks `VideoDecoder.isConfigSupported()` for the hardware stream's
  actual H.265 Main/Level 5 declaration, `hev1.1.6.L150.B0`, and requires
  WebCodecs in a secure context. H.264 is declared as the encoder's actual
  Main/constraint-set-1/Level 4.2 profile, `avc1.4D402A`;
- WebRTC checks `RTCRtpReceiver.getCapabilities("video")` for `video/H265`.

If a browser cannot decode H.265, the effective codec falls back to H.264. The
selector disables H.265 and records the fallback so the displayed selection
matches the stream.

## Stream API

New clients use one codec query parameter:

```text
/api/stream/video/direct?codec=h265
/api/stream/video?codec=h264
```

The codec is validated before the WebSocket upgrade. An explicit `rc=cbr` is
accepted for compatibility with development clients; other rate-control modes
are rejected. Existing `/api/stream/h264` and `/api/stream/h264/direct` clients
remain H.264/CBR compatibility endpoints.

The SG2002 has one hardware video encoder session. Direct and WebRTC viewers
using the same codec share it. Bitrate and GOP remain device-wide Screen state,
so changes made through the established menu are applied to the shared encoder
without creating per-browser profiles.

For H.265, the Video menu also exposes NormalP and SmartP. SmartP is the product
default when no saved preference exists. H.264 always uses NormalP. A changed
H.265 GOP preference is persisted but is not hot-switched on an active encoder;
the UI compares the saved value with the native active value and requires a
device restart only while those values differ.

The native API returns one complete Annex-B access unit per call. It joins all
CVITEK VENC packs, recognizes H.264 IDR and H.265 IRAP NAL units, and marks the
result as a key or delta frame. Direct passes those access units to WebCodecs.
WebRTC uses Pion's H.264 or H.265 RTP payloader with a 1216-byte RTP MTU,
including the base RTP header. Adding the largest negotiated SRTP tag (16),
UDP (8) and IPv6 (40) gives 1280 bytes. DTLS negotiates the keys separately;
media packets do not carry a DTLS record header. No RTP extensions are enabled.
TURN framing and unknown path overhead need separate qualification before
increasing this budget. Route-specific packetization is not implemented yet.

The VENC pack array is allocated from the count returned by
`CVI_VENC_QueryStatus()` before `CVI_VENC_GetStream()` writes it. The public
access-unit interface still accepts at most eight packs and rejects larger
vendor results after releasing them. This prevents the previous fixed-size
vendor buffer from being overrun before its returned count could be checked.

## Frame pacing

A native capture thread, `nkos-capture` (`server/common/capture_worker.c`),
paces H.264/H.265 sessions. On one P, every syscall or cgo call that the kernel
deschedules lets sysmon hand the P to another thread, so the Go side avoids
waiting in either:

- The thread keeps the absolute cadence of the Go loop it replaces: the same
  bounded catch-up after a late read, and a restart of the cadence when the
  rate changes. It runs the existing `kvmv_read_video_sink` read and copies the
  packs of the access unit into one of two C buffers before the vendor stream
  is released.
- It signals an eventfd only when the consumer has announced that it waits.
  The session goroutine waits there through the netpoller, copies the buffer
  into Go-owned storage with headroom, frees it with an atomic store and fans
  the frame out. It uses no timer, yield or cgo call per frame. If both buffers
  are full, freeing one wakes the thread with a raw write that keeps the P.
- Bitrate, GOP, size and rate changes are published under the thread's mutex
  and apply from its next read. Unchanged parameters cost nothing. The session
  rereads the capture settings when the Screen snapshot changes, and every
  0.5 s for source-dependent limits.
- Other native operations (MJPEG reads, HDMI, GOP, frame detection, chroma and
  monitor profiles) pause the thread and wait for its read in flight, as
  `KvmVision.mutex` excluded them from Go-paced reads. Stopping a session
  interrupts the wait at once. Closing it waits for the read in flight and drops
  unconsumed frames before another profile can start.

Result codes, key/delta classification and headroom are unchanged, and no
native library change is required. MJPEG keeps its blocking read.

`NANOKVM_NATIVE_CAPTURE_WORKER=0` selects the previous Go-paced blocking loop
for this release. The server also falls back to it, and logs the reason, when
the thread cannot be started or its eventfd fails.
`NANOKVM_CAPTURE_DEBUG=1` logs the handoffs every 10 s: frames, native reads,
eventfd signals, Go waits, producer wakeups and waits, parameter updates and
pauses.

## Building and testing

`libkvm.so` and `libkvm_mmf.so` must be rebuilt and deployed together with the
Go server when the H.265 GOP selector is included. The `mmf_venc_cfg_t` layout
remains unchanged; the MMF and capture libraries use additive mode-selection
symbols. The existing-libraries server builder rejects a native set that lacks
any required symbol and records the matched bundle contract and file hashes in
its manifest. Installation must stage the server and both native libraries as
one set, verify the hashes, and perform a tested device reboot only after all
three files are present. A service-only hot restart is not qualified here.

The transport/session tests do not require SG2002 hardware:

```sh
cd server
CGO_ENABLED=1 go test -tags teststub -race ./service/stream/... ./common
```

The production RISC-V build must additionally pass `make release-build`; the
frontend is checked with `pnpm lint` and `pnpm build` in `web/`.
