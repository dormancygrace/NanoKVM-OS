# RustDesk 1.5 migration: NanoKVM add-on 0.3.0-r2

The endpoint uses RustDesk 1.5.0 fada664df7a294d1d1a9ca3e7cd3637069122f17
and hbb_common 229b904508364c8997aad0fb5af57effac859f60 as protocol reference.
Its own version is 0.3.0-r2; it remains a partial HDMI/HID endpoint.

Implemented:
- KX v1 independent transmit/receive BLAKE2b keys, exact transcript and legacy v0.
- Signed hbbs key-exchange parameters, marker and signature-domain checks.
- Pion 4.2.22 ICE/DTLS/SCTP data-channel bridge using the application's existing stack.
- Authenticated encrypted SDP/ICE signaling; signed IdPk binds the DTLS certificate.
- Ordered reliable channel, upstream 60000-byte fragment payloads and protobuf framing.
- Bounded setup, message, input burst and ICE queues; duplicate offer/candidate handling.
- Temporary-password rotation on new admission and bounded 30-second reconnect cache.
- Dead-peer heartbeat timeout and HID release; view-only permissions never claim HID.
- Lightweight extension inventory; Extensions renders immediately before runtime status.
- Obsolete refresh/operation results are discarded across UI unmount and config/package writes.

Shared hardware encoding remains H.264/H.265 Annex B. Neither transport adds an
encoder or changes device bitrate/GOP/FPS. The browser's RTP/SRTP pipeline is separate.

Limits:
Audio, clipboard, files, terminal, proxy/WebSocket signaling and a TURN configuration
are outside this HDMI/HID endpoint. A relay-only ICE request uses legacy encrypted
relay. Legacy direct TCP defaults to loopback. New daemon dependencies require the
updated app to provide nanokvm-rustdesk-webrtc=1 in addition to bridge=1.

Host evidence, 2026-10-03:
- Rust: 39 passed, 2 device-only ignored. Independent crypto vectors, encrypted
  v0/v1 loopback, signed signaling roundtrips, DTLS identity/framing and view-only HID test.
- Go: service/rustdesk and router pass with race detection/hardware stubs.
  Real Pion peer tests bidirectional fragmented payloads, bursts and setup teardown.
- Protocol field audit: 162 selected field tags match the canonical 1.5.0 schema.
- UI: TypeScript, scoped Prettier and ESLint pass for lifecycle correction.

Device/client qualification, 2026-10-03:
- Upgraded 0.2.1-r1 to 0.3.0-r1 with application 2.0_beta8-r11. Registration,
  temporary credentials, config, identity and installed native-library hashes survive.
- Official Windows RustDesk 1.5.0 displays encrypted TCP relay with H.264 at
  50–59 FPS, and direct encrypted WebRTC with H.264 at 13–28 FPS.
- A new admitted connection rotates the current temporary password. Existing
  connections continue. View-only testing leaves the USB gadget disabled.
- During a 28.126-second WebRTC CPU window the application consumed 70.55% and
  the daemon 3.65% of total CPU ticks. Application RSS was about 47 MiB with
  46–47 descriptors; after disconnect the session count was zero and descriptors 35.
- Daemon stop/start removes and recreates private temporary credentials and restores
  registration. Client windows are closed and original client preferences restored.
- Chrome Main verifies logo, matching card backgrounds, separate package/protocol
  versions, installed-only extension children, and no upgrade action without a candidate.
- No source archive, test APK or agent backup is retained on the device.

The lower WebRTC FPS remains a qualification limit. CPU cost in the Go
DTLS/SCTP bridge is a hypothesis until a bounded profile isolates it; browser
RTP/SRTP results do not establish RustDesk data-channel performance. The exact
RustDesk 1.5.0 WebRTC fork does not implement ChaCha20 DTLS cipher suites, so
Chrome's preferred cipher is not a compatible performance fix for this client.
Reconnect caching and KX v1 have host protocol tests; this device run did not
capture negotiated KX v1 separately or establish a real reconnect test.

The Windows official 1.5.0 x86-64 executable digest is
8555777215510d83d2d61c9dc984e4fcc838bd7e79f9d18a42585431f5e8bb47;
its Authenticode signature was valid (PURSLANE).

The package contains source.json with an immutable public URL/digest, not source
archives. Published source includes Rust vendor dependencies and the standalone
Go data-channel component with its locked/vendor dependencies and integration hook.

## IPC memory and stall limits

512 KiB is the aggregate inbound application queue per peer, not a global budget.
A peer may hold one outbound protobuf frame up to 8 MiB, approximately 2 MiB plus
one 60001-byte fragment in Pion's buffered amount, an inbound reassembly up to
256 KiB, a 512 KiB input queue and a 256 KiB SCTP receive window. Four configured
transport slots therefore bound these explicit payload buffers to approximately
45 MiB, plus Pion/ICE/DTLS/SCTP objects, retransmission metadata, Go allocation/GC
headroom and the Rust/video buffers. This is a limit analysis, not measured RSS.
Normal HDMI frames are much smaller; device CPU/RSS still require qualification.
The 8 MiB outgoing limit preserves valid large video frames. Incoming control
messages have a separate 256 KiB cap.

An idle IPC sender may wait without a read timeout. After the first framing byte,
the remaining header and payload must arrive within three seconds. Session
cancellation closes the IPC connection to interrupt idle and partial reads.
Tests cover partial header/body timeout, cancellation, duplicate attach, session
and connection caps, oversized frames and actual Unix peer credentials.

Revision r1 additionally honors client view-only at login and live option changes.
Tests verify no HID/lease calls before enable, held-button release after disable,
and continued heartbeat/video with server-side permissions still enforced.

## r2 policy and packaging corrections

WebRTC is opt-in; absent configuration means disabled. The default path adds
classic encrypted TCP/LAN rendezvous with a temporary listener, repeated bounded
outbound attempts and relay fallback. No permanent LAN port is enabled. The
1.5 controller suppresses TCP while it sends a WebRTC offer, so turning it off
on the client is necessary to request TCP by ID. Host tests verify listener reuse,
retention of a successful crossing, canonical LocalAddr/permissions tags and
compatibility of missing configuration. Device qualification above describes r1;
r2 direct throughput was qualified in a separate explicitly coordinated slot.

A headless view-only controller reached the device by ID through the official
hbbs, verified the signed identity and negotiated encrypted KX v1. It received
1198 H.264 encoded frames (14,025,983 bytes) in 20 seconds: 59.9 received frames/s.
This measures frame reception; it does not decode or display video and cannot
be compared directly with the official-client display FPS above. After closing,
sessions returned to zero, registration remained active and USB was unbound.
Config, identity and native library hashes remained unchanged across installation.
Only the add-on lifecycle restarted its daemon; no app, kernel, route or encoder
changes were made in this qualification. The uploaded test APK was removed.

Every WebRTC setup connect/write and signaling step now has a deadline and a
shutdown interrupt. Active serving receives a stop signal and is awaited for
cleanup rather than simply dropping its input/media future. Every source/APK
build uses a fresh temporary host tree and refuses to overwrite an existing
artifact. The source archive contains APKBUILD.in as a recipe template because
an archive cannot embed its own digest; the external production APKBUILD includes
SHA-512 checksums for the immutable archive and init script.

## Common application deployment

The runtime owner integrated migration and hardening into common commit 2cbc2ac,
added performance changes through c33d6c3, and installed nanokvm-app
2.0_beta8-r15. Its APK SHA-256 is
ac7711dc4899dac032be49f7aa36114d4e7c3943ab9ac4739a43c697bab18c4a;
the server SHA-256 is
7e3e4f2e183c9f11228e80b8e7e39951608a4a46ac0f542ad2498a62a5e867c1.
The owner verified Chrome Main shows add-on 0.3.0-r2, protocol 1.5.0,
Running/Registered, and the capability-gated Allow WebRTC connections switch
visible and off. No settings were saved; the verification tab was closed.
This UI qualification was performed by the runtime owner, separately from
this worktree's headless direct TCP measurement.
