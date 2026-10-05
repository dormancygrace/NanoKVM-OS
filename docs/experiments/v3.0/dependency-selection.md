# Dependency selection, 2026-10-05

The user requires high-quality maintained projects. Selection uses current upstream activity, ownership/security development, license compatibility, actual riscv64 musl execution and NanoKVM feature compatibility. A recent commit alone is not a quality guarantee. Current choices do not restrict the implementation to the old installed Rust 1.86 compiler; the project pins Rust 1.99.0.

| Responsibility | Selection | Evidence and tradeoff |
|---|---|---|
| HTTP/WebSocket routing | [Axum](https://github.com/tokio-rs/axum) 0.8.9 | Active Tokio project; existing HTTP/UI authentication contracts pass. WebSocket feature enabled; WS feature parity still pending. |
| Async runtime | [Tokio](https://github.com/tokio-rs/tokio) 1.53.2 | Active upstream; one event-loop thread plus four admitted blocking jobs suits the single-core target. Hardware measurements still required. |
| Socket/descriptor support | [libc](https://github.com/rust-lang/libc) 0.2.190, [futures-util](https://github.com/rust-lang/futures-rs) 0.3.34, [tokio-tungstenite](https://github.com/snapview/tokio-tungstenite) 0.29.0 | Existing transitive projects, now explicit syscall/stream dependencies and real-socket test client. Current unarchived upstream activity recorded; actual host/target takeover, FIFO backpressure and revocation contracts pass. HID jobs admit two blocking workers, socket queues admit 200 reports per kind with 64 sockets maximum. |
| TLS | [rustls](https://github.com/rustls/rustls) 0.23.45 + [ring](https://github.com/briansmith/ring) 0.17.14 | Explicit crypto provider; linked and executed real certificate-validated HTTPS under riscv64 musl/QEMU. ring's resolved license is Apache-2.0 AND ISC. |
| YAML | [serde-saphyr](https://github.com/bourumir-wyngs/serde-saphyr) 1.3.0 | Active development through 2026-10-04; selected instead of serde_yaml_ng whose last recorded push was 2025-09-14. Case/null/default compatibility fixtures pass and original settings bytes are retained. |
| Existing account formats | [RustCrypto](https://github.com/RustCrypto) AES/CBC/HMAC/SHA2 + [bcrypt](https://github.com/Keats/rust-bcrypt) 0.19.3 | Maintained implementations preserve legacy encrypted credentials, bcrypt and HS256 JWT. Actual Go-issued JWTs/hashes accepted; Go middleware accepts a Rust-issued JWT. |
| WebRTC | [webrtc-rs](https://github.com/webrtc-rs/webrtc) 0.21.0 | Integrated ICE/TURN/DTLS/SRTP/SCTP. Single-thread Tokio probe passes direct and authenticated relay-only ICE/DTLS/SCTP exchanges on host and target. Codec/browser/hardware integration remains pending. |
| WebRTC alternative | [str0m](https://github.com/algesten/str0m) 0.24.1 | Active SansIO engine, no internal tasks. Direct peer exchange and target build pass. Application must provide TURN client/network gathering. Keep for later measured comparison if CPU/memory warrants the added integration. |

Upstream API metadata is recorded in dependency-maintenance.json (archived/disabled flags, push timestamps and repository license metadata). All selected projects and both transport candidates were unarchived and enabled. Resolved Cargo package licenses are recorded separately because GitHub's repository license detection is not sufficient for transitive dependencies. Versions are fixed by Cargo.lock, including the separate qualification probes.

The str0m rust-crypto feature does not imply a dependency tree containing only RustCrypto: dimpl's rcgen feature currently enables AWS-LC for certificate generation. Both transports can execute on the target; do not reject str0m due to our initial CFLAGS -O2 override of AWS-LC's required jitterentropy -O0. The corrected harness leaves dependency optimization to its build scripts.

The TURN fixture uses baseline Pion TURN 5.1.2 only as an independent local test oracle, has a v3oracle build tag, listens/relays exclusively on loopback, and is never part of the Rust replacement runtime or package. No external TURN/STUN service or shared NanoKVM device was contacted by these probes.

No C906 CPU, RSS, real-time latency, browser media or hardware stability claim follows from QEMU or probe binary sizes. The final transport choice remains subject to those acceptance gates. H.265 WebRTC remains blocked; a maintained library advertising H.265 does not override the CryptoDMA policy.
