# RustDesk 1.5 migration: NanoKVM add-on 0.3.0-r1

The endpoint uses RustDesk 1.5.0 fada664df7a294d1d1a9ca3e7cd3637069122f17
and hbb_common 229b904508364c8997aad0fb5af57effac859f60 as protocol reference.
Its own version is 0.3.0-r1; it remains a partial HDMI/HID endpoint.

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

Device/client qualification is pending a coordinated slot after the runtime
owner's FPS experiments. No claim of a working production WebRTC session is made
from the host tests alone. The Windows official 1.5.0 x86-64 executable digest is
8555777215510d83d2d61c9dc984e4fcc838bd7e79f9d18a42585431f5e8bb47.

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
