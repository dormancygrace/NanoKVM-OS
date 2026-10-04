# Stage 2 qualification, 2026-10-05

Baseline: a53b25579ab87cc98f85323b4743deb0d4da907b, branch v3.0-experimental. This milestone is an isolated authentication/config/TLS/UI foundation, not a complete Go replacement. The binary refuses root `/` and nonloopback listeners while parity is incomplete. 15/204 route registrations have implementations; 189 remain pending. Never install this milestone as the production server.

## Passed checks
- `cargo fmt --all -- --check`, `cargo clippy --locked --all-targets -- -D warnings`.
- 18 host and riscv64 musl/QEMU tests: legacy encrypted account migration, bcrypt 2a/2b, JWT signature/expiry/identity, cookie/bearer precedence, factory password gate, role/owner/last-admin policy, account/session revocation, owner password rollback, corrupt/deleted state fail-closed behavior, static symlink confinement, YAML preservation/defaults/case/null compatibility, IPv6 lockout grouping and bounded lockout saturation, form login, trusted/untrusted forwarded-proto, configured authentication disable, HTTPS redirect.
- Eight actual Go oracle cases match exact HTTP status and JSON in Rust using Go-issued tokens/hashes. The isolated Go middleware also accepts a Rust-issued JWT with the same identity/tokenVersion. Fixtures contain public test-only credentials and are never live account data.
- Existing built UI login succeeds in Chrome **Main**, using dummy owner credentials and a separate loopback fixture root. The authenticated screen opens; capture, HID/USB and system functions are pending, as the UI reports. This does not prove media or hardware parity.
- Real host and target HTTPS with a freshly generated SAN certificate verified by the client: static UI, HTTP 307 preserving query, login Secure/HttpOnly/SameSite cookie and no-store headers. SIGTERM exits 0 and closes both listeners. An occupied HTTPS port aborts startup without serving HTTP.
- Release binary is static ELF64 RISC-V, RVC/double-float ABI, generic rv64gc/lp64d; `--version`, contracts and TLS execute under QEMU. 5,484,224 bytes; SHA256 `7ccf84e50dc7dfa1c505c6f9caec18c0fbc4f8c765ac637e15827170dbe222d1`.
- Both maintained transport candidates pass ICE/DTLS/SCTP direct loopback exchanges on host and target. Selected webrtc-rs also passes authenticated **relay-only** TURN exchanges on both. The TURN oracle is a loopback-only, test-tagged Go helper, never a runtime bridge/package dependency.

`qualification.json` retains check exit codes, timings, log hashes and artifact hashes. Local full logs stay under work/v3/qualification and work/v3/transport-{webrtc,str0m}. Timings and probe binary sizes are build/test evidence, not C906 performance measurements. `dependency-maintenance.json` records upstream repository metadata; `dependency-licenses.json` records all 163 resolved foundation packages' declared licenses.

## Reproduce in WSL
The scripts read the matched platform toolchain/QEMU/Go build without changing it. Override NK_V3_PLATFORM if its location differs. Rust's project toolchain is 1.99.0; global default remains 1.86.0.

```sh
python3 scripts/inventory-v3.py
python3 scripts/refresh-v3-oracle.py
python3 scripts/check-v3.py --host --target --tls
python3 scripts/check-v3-transport.py webrtc --target --turn
python3 scripts/check-v3-transport.py str0m --target
python3 scripts/update-v3-parity.py
```

Source inventory reads the immutable baseline Git archive, so new test-only Go helpers do not contaminate its counts or overwrite migration statuses. Cargo.lock pins the foundation and each separate transport probe. Dependency CFLAGS do not override crypto sources' required optimization levels.

## Remaining acceptance work
Session/input-owner arbitration, WS origin/session permissions, HID/USB, native capture/MJPEG/Direct/audio/WebRTC, browser transport/codec negotiation, system/config APIs, addons/RustDesk IPC, MCP, files/PTY/scripts, update helpers, APK/OpenRC/image integration and full API side effects remain unqualified. HTTPS certificate creation/renewal and internal-loopback helper exemptions are also pending. Owner root password synchronization only has rollback qualification in the sandbox. Preserve the unconditional `h265-webrtc-disabled` policy when WebRTC is integrated. Hardware correctness, stability, CPU/RSS and latency need the agreed stand window; stand 192.168.4.128 was not modified.
