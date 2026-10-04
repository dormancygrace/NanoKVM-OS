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
- Exact branch verified. Stage 1 inventory committed as 4556d2e; stage 2 committed as 240d818e53eee49863881c097ff821f7598c906f. Working tree clean immediately after that commit.
- Parent AGENTS.md read; no nested AGENTS.md present.
- Rust 1.99.0 pinned for the project; global default remains 1.86.0. Generic riscv64 musl binary executes under QEMU.
- 24 host/target tests pass after the bounded stage 3 settings/input-core slice; eight real Go oracle response cases, two-way JWT interoperability, persisted HID settings, ownership/stale-ticket and HID descriptor/deadline contracts included. 22/204 route implementations, 182 pending.
- Seven HID settings/read APIs implemented; input ownership and framing core tested but NOT connected to WS/device IO yet. See stage3-input.md and stage3-qualification.json. Current release SHA256 27c932b2aca15c3364c44a1866808941f46862e82e5cacfa813d1187b363fa40, 5,538,272 bytes.
- Existing UI login passes in Chrome Main on an isolated loopback Rust server. Real host AND riscv64 musl/QEMU HTTPS certificate validation, 307 redirect, Secure cookie, SIGTERM and occupied-port rejection pass.
- YAML uses maintained serde-saphyr 1.3 with Viper key/null/default fixtures; dependency repository metadata and resolved licenses recorded.
- Select webrtc-rs 0.21.0 (runtime-tokio + crypto-ring) for feature parity: host/target direct and authenticated relay-only ICE/DTLS/SCTP exchanges pass. str0m 0.24.1 host/target direct exchange also passes but needs a separate TURN client; retained as a measured performance alternative. Neither qualifies hardware/browser media yet.
- Go app depends on patched Pion ICE/DTLS/SRTP and RustDesk currently calls Go media/HID/WebRTC bridges.

## Next action
Commit bounded stage 3 settings/input-core slice, then wire authenticated/origin-checked/revocable /api/ws to nonblocking HID descriptors and bounded workers. Preserve 4 KiB messages, 90-second heartbeat, 10-second writes, 4401 revocation closes, release-before-transfer and stale queue cancellation; LED lifecycle, manual/MCP/PicoClaw preemption, jiggler, USB rebind/reopen remain required. Relevant Go ws/hid/session sources and tests read. scripts/check-v3.py, check-v3-transport.py and refresh-v3-oracle.py reproduce qualification. NK_V3_PLATFORM points to matched read-only artifacts under /home/dgrace/nanokvm-astra/work/v2.1-b1-20261004/platform. Runtime still refuses production activation and nonloopback hosts. No media/device parity or performance claim and no stand changes.

## User steering (2026-10-05)
Choose the best maintained upstream projects; do not pick abandoned libraries or constrain the design to the old Rust 1.86 installation. Dependency maintenance/qualification recorded and committed with stage 2; webrtc-rs selected for integrated TURN parity, str0m retained as a measured alternative. No abandoned-project fallback or Go runtime bridge.
