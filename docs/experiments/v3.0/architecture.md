# Rust/C replacement architecture

Baseline a53b25579ab87cc98f85323b4743deb0d4da907b. Status: implementation in progress.

## Measured source boundary
The reproducible inventory script found 204 HTTP registrations, 277 project Go source files (44,250 lines excluding tests and vendored Pion), 82 process invocation sites and 227 absolute path literals. `inventory.json` retains all five go.mod files and actual external import paths; `routes-baseline.json` resolves constant-based and grouped paths with source locations. Public Wi-Fi AP bootstrap, public branding, session, administrator, loopback-internal-token and MCP API-key routes are distinct. Input-owner checks and WebSocket message permissions are additional to route authentication.

The central app owns authn/config, browser streams, HID and input arbitration, networking/storage/system settings, diagnostics/log archives, extensions/addons, PicoClaw/MCP and updater orchestration. main.go also initializes native capture, persisted CPU/IPv6/HDMI intent, jiggler, memory maintenance and log archiving. Those side effects require independent parity tests before production boot.

## Runtime process boundary
- Replace `NanoKVM-Server` entirely with one Rust executable at the existing runtime path; Go source stays as a comparison reference only.
- RustDesk has four Go-owned Unix sockets (media.sock, audio.sock, control.sock, webrtc.sock) with root SO_PEERCRED checks. Rewrite their handlers/transport in Rust. Retaining a Pion Go bridge is not an acceptable final runtime.
- Port nkos-update/nkos-apply-updates responsibilities or establish a separately authorized boundary; final replacement must not silently keep required Go helpers.
- Retain native C/C++ `kvm_system`, libkvm/libkvm_mmf and vendor MPI dependencies, shell/OpenRC helpers, usb-audio-capture and Alpine tools. Do not rebuild official Alpine packages without a concrete need.

## Design decisions
Rust/Tokio owns HTTP/TLS/auth, concurrent session cancellation, protocol framing, native subscription lifetime and validated system actions. Axum is a thin HTTP routing layer; use a single Tokio event-loop thread on the one-core C906 and a bounded blocking pool for bcrypt and blocking filesystem/system calls. Account mutations reload disk under one store lock and persist via mode-0600 temporary + fsync + rename + directory fsync. JWT is HS256 in pure Rust HMAC/SHA256 and retains username/sub/tokenVersion/iat/exp; bcrypt and legacy salted AES credential decoding preserve existing data. All random secrets use OS entropy, and errors fail closed.

TLS uses rustls with ring explicitly selected (not an implicit AWS-LC default), subject to a real riscv64 musl link/execution proof. YAML uses serde_yaml_ng rather than the archived serde_yaml crate, with unknown settings retained in the source file; no settings rewrite until its API is ported. Every dependency is pinned by Cargo.lock and its package license is recorded after resolution. Initial builds pin installed Rust 1.86.0 and generic rv64gc/lp64d. Do not import RustDesk's optional T-Head/fat-LTO flags into the server without measurement; native C stays with the repository scalar -O2 profile.

C/FFI uses the existing `server/include/kvm_vision.h` ABI. A single capture owner serializes native reads/settings/maintenance, copies or transfers ownership exactly once, and frees frames through free_kvmv_data. Existing capture_worker.c is reference for one-inflight native reads; destruction joins reads and must not cancel vendor calls unsafely. Retain dual-VPSS MJPEG chroma, GOP selection before native init, HDMI lifetime/idle handling, shared codec arbitration and bounded subscriber queues. Native ABI version/symbol checks belong to packaging.

WebRTC candidate selection remains open: inspect str0m and webrtc-rs for codec/SCTP/ICE-TURN semantics, GPL compatibility, riscv64 musl compilation and localhost peer exchanges. An advertised feature list is insufficient. H.265 WebRTC stays unconditionally blocked as `h265-webrtc-disabled`, including software-AES settings; H.265 Direct remains in scope. Preserve Pion adaptations (parameter-set retention at every IRAP, PMTU, ICE behavior, negotiated cipher order) in the chosen transport.

## Stage gates
The isolated Rust authentication/UI slice is deliberately not installed on the stand. Pending routes respond with a clear HTTP 501 after the correct access gate; no success stubs and no proxy to Go. Build scripts must refuse production image activation while parity is incomplete. TLS/config persistence, real Chrome Main login, JSON/form ciphertext, Go-issued/Rust-issued token interoperability, owner password rollback, user policy and lockout behavior are initial gates. Media/system parity and all performance claims remain unverified until later gates and a coordinated hardware window.

## Primary dependency references (checked 2026-10-05)
- https://docs.rs/crate/axum/0.8.9 — MIT, MSRV 1.80; Tokio/Hyper routing.
- https://doc.rust-lang.org/rustc/platform-support/riscv64gc-unknown-linux-musl.html — distributed Tier 2 target; cross-compile/QEMU testing supported.
- https://docs.rs/crate/serde_yaml_ng/0.10.0 — MIT, serde-yaml fork; YAML 1.1 compatibility needs tests against Go fixtures.
- https://docs.rs/crate/str0m/latest — candidate only; crypto/SCTP dependency and actual target support still need qualification.
