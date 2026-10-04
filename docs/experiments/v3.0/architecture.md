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

TLS uses rustls 0.23 with ring explicitly selected and passes real riscv64 musl TLS execution under QEMU. YAML uses active serde-saphyr 1.3: repository maintenance evidence led us to replace the initial serde_yaml_ng choice. Case-insensitive keys, explicit null defaults, negative login limits, STUN/TURN/Alpine fields and Viper's explicit-key logout flag semantics have fixtures. Existing YAML is never rewritten; unknown settings remain in the original file. Invalid/ambiguous configurations fail closed and are preserved, rather than silently selecting defaults. Every dependency is pinned by Cargo.lock; resolved package licenses are recorded in dependency-licenses.json. Project toolchain pins Rust 1.99.0 without changing the global default. ISA/ABI is generic rv64gc/lp64d. Do not import RustDesk's optional T-Head/fat-LTO flags without measurement. Vendor C retains scalar -O2; dependency CFLAGS leave per-file optimization to each crypto build script (AWS-LC jitterentropy requires -O0).

C/FFI uses the existing `server/include/kvm_vision.h` ABI. A single capture owner serializes native reads/settings/maintenance, copies or transfers ownership exactly once, and frees frames through free_kvmv_data. Existing capture_worker.c is reference for one-inflight native reads; destruction joins reads and must not cancel vendor calls unsafely. Retain dual-VPSS MJPEG chroma, GOP selection before native init, HDMI lifetime/idle handling, shared codec arbitration and bounded subscriber queues. Native ABI version/symbol checks belong to packaging.

Select webrtc-rs 0.21.0 with explicit runtime-tokio/crypto-ring for the parity implementation. Both it and str0m 0.24.1 are actively maintained and pass actual ICE/DTLS/SCTP two-peer loopback exchanges on host and riscv64 musl/QEMU. webrtc-rs additionally passes an authenticated relay-only TURN exchange on both targets; str0m deliberately leaves the TURN client/NIC gathering to the application. str0m's rust-crypto feature still pulls AWS-LC through certificate generation; its cross build passes after fixing our global CFLAGS override. Keep str0m as a measured performance alternative, not an abandoned-project rejection. Neither probe qualifies browser media, hardware stability, PMTU, codec handling or C906 CPU/memory/latency. H.265 WebRTC stays unconditionally blocked as `h265-webrtc-disabled`, including software-AES settings; H.265 Direct remains in scope. Preserve Pion adaptations (parameter-set retention at every IRAP, PMTU, ICE behavior, negotiated cipher order) in the chosen transport.

## Stage gates
The isolated Rust authentication/UI slice is deliberately not installed on the stand. Pending routes respond with a clear HTTP 501 after the correct access gate; no success stubs and no proxy to Go. Build scripts must refuse production image activation while parity is incomplete. TLS/config persistence, real Chrome Main login, JSON/form ciphertext, Go-issued/Rust-issued token interoperability, owner password rollback, user policy and lockout behavior are initial gates. Media/system parity and all performance claims remain unverified until later gates and a coordinated hardware window.

## Primary dependency references (checked 2026-10-05)
- https://docs.rs/crate/axum/0.8.9 — MIT, MSRV 1.80; Tokio/Hyper routing.
- https://doc.rust-lang.org/rustc/platform-support/riscv64gc-unknown-linux-musl.html — distributed Tier 2 target; cross-compile/QEMU testing supported.
- https://github.com/bourumir-wyngs/serde-saphyr — chosen maintained YAML parser; Viper compatibility fixtures pass.
- https://github.com/webrtc-rs/webrtc — selected for integrated ICE/TURN/SCTP; host and target qualification recorded.
- https://github.com/algesten/str0m — maintained SansIO alternative; deliberate TURN-client boundary documented upstream.
- See dependency-selection.md, dependency-maintenance.json, dependency-licenses.json and validation.md for dated evidence and limitations.
