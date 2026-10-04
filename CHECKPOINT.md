# v3.0-experimental checkpoint

Status: active implementation; not a complete Go replacement.
Baseline: a53b25579ab87cc98f85323b4743deb0d4da907b (codex/v2.1-b1-base / PR #45).
Branch/worktree: v3.0-experimental, /home/dgrace/nanokvm-astra/v3.0-experimental.

## Constraints
- Keep UI/API/settings/credentials/APK/OpenRC compatibility and every existing feature.
- Final replacement runtime may not run the Go server or a Go transport bridge.
- Preserve native C/C++ capture and H.265 WebRTC CryptoDMA block until separately qualified.
- Shared device 192.168.4.128: read-only inventory only; runtime changes require an agreed window.
- Browser tests use Chrome Main. No on-device agent backups. Do not publish releases or merge stable.
- Commit finished stages only in this branch. Read this file first after context compaction.

## Plan and acceptance gates
1. Source/runtime inventory and route matrix; Go boundary and native ABI; dependency/target proof.
2. Rust bootstrap, isolated root, TLS/auth/config/static UI; contract tests and real UI login.
3. Sessions/roles/HID/USB and system/config APIs; differential tests of responses and side effects.
4. Native media subscriptions/MJPEG/Direct, audio and WebRTC; protocol/transport tests.
5. Integration/addons (including RustDesk without Go bridge), terminal/files/MCP, updates/APK/OpenRC.
6. Reproducible riscv64 musl package/image; independent host and sandbox checks.
7. Agreed device window: full UI compatibility and stability/latency/performance comparisons.

## Current evidence
- Worktree clean at baseline; exact branch verified.
- Parent AGENTS.md read; no nested AGENTS.md present.
- Rust 1.86.0/cargo 1.86.0 installed in WSL with riscv64gc-unknown-linux-musl target.
- Go app depends on patched Pion ICE/DTLS/SRTP and RustDesk currently calls Go media/HID/WebRTC bridges.

## Next action
Stage 1 source ledger and architecture written: 204 routes, 44,250 lines. Implement stage 2 auth/config/TLS/static UI in server-rust; prove generic riscv64 musl with the read-only matched toolchain at /home/dgrace/nanokvm-astra/work/v2.1-b1-20261004/platform/buildroot-output/host/bin/riscv64-buildroot-linux-musl-gcc. Native/UI/Go/QEMU outputs are under the same platform directory. Authn source semantics read. No device changes.
