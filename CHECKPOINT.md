# v3.0-experimental checkpoint

Status: active implementation; not a complete Go replacement.
Baseline: a53b25579ab87cc98f85323b4743deb0d4da907b (codex/v2.1-b1-base / PR #45).
Branch/worktree: v3.0-experimental, /home/dgrace/nanokvm-astra/v3.0-experimental.

## Constraints
- Keep UI/API/settings/credentials/APK/OpenRC compatibility and every existing feature.
- Final replacement runtime may not run the Go server or a Go transport bridge.
- Preserve native C/C++ capture. H.265 WebRTC must always return h265-webrtc-disabled, including software crypto; direct H.265 remains in scope.
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
- Bounded stage 3 settings/input-core slice committed as 54e73bf. Live WS/HID slice committed as aef607e with 33 host/target tests.
- Parent AGENTS.md read; no nested AGENTS.md present.
- Rust 1.99.0 pinned for the project; global default remains 1.86.0. Generic riscv64 musl binary executes under QEMU.
- 80 host/target tests pass: 32 unit, 20 API, nineteen existing/paste real-socket and nine USB/monitor fixture tests; 13 actual Go response cases and two-way JWT interoperability. Binding fixes committed as d820817. LED slice committed as c80594e. Ledger: 32/204 implemented in isolation, one partial (/api/ws), 171 pending.
- Live authenticated/origin-checked/revocable WS, bounded HID queues/descriptors, HTTP leases, release/transfer/shutdown and LED REST/WS readiness/replacement/backoff are wired and tested. Manual/addon mode leases, cancellation, held/cooldown and PicoClaw locking are wired. See stage3-coordinator.md and its qualification JSON. Jiggler API/worker/priority/cleanup and actual 15-second timer are tested; see stage3-jiggler.md and qualification JSON. Admin USB reset/profile lifecycle, confined script install and cancellable actions pass; see stage3-usb.md. Internal recover token gate and real companion HTTP exception pass; body waits no longer occupy execution jobs. See stage3-internal-usb.md. Composition/budget/rollback/EDID core and injected lifecycle pass; default native media backend remains unavailable. See stage3-composition.md. HTTP paste and 833-key actual cadence/cleanup pass; see stage3-paste.md. Current release SHA256 d03c5d6438f65487c1dcbabb5540c91490302179b03fa695f930ab2ca418faf6, 6,345,440 bytes. No hardware input/media/performance qualification.
- Existing UI login passes in Chrome Main on an isolated loopback Rust server. Real host AND riscv64 musl/QEMU HTTPS certificate validation, 307 redirect, Secure cookie, SIGTERM and occupied-port rejection pass.
- YAML uses maintained serde-saphyr 1.3 with Viper key/null/default fixtures; dependency repository metadata and resolved licenses recorded.
- Select webrtc-rs 0.21.0 (runtime-tokio + crypto-ring) for feature parity: host/target direct and authenticated relay-only ICE/DTLS/SCTP exchanges pass. str0m 0.24.1 host/target direct exchange also passes but needs a separate TURN client; retained as a measured performance alternative. Neither qualifies hardware/browser media yet.
- Go app depends on patched Pion ICE/DTLS/SRTP and RustDesk currently calls Go media/HID/WebRTC bridges.

## Next action
Internal recovery 5676c9f; composition/monitor association fc2cece. HTTP paste slice passes 80 host/target/TLS tests; commit the bounded slice. Then immediately fix the confirmed generic form-binding defect: current params ignores URL query, incorrectly reads bodies for unknown content-type/DELETE and silently accepts malformed percent encoding/semicolon separators where Go net/http ParseForm + url.ParseQuery rejects. Exact Gin v1.12 binding/form.go, binding.Default and matched Go request.go read; Form uses body first plus query, only POST/PUT/PATCH urlencoded bodies, absent type means octet-stream, JSON ignores query. Other Gin XML/YAML/TOML/MsgPack/ProtoBuf/multipart encodings remain separate integration work; don't claim complete generic binding. Add actual Go binder fixtures and Rust regression cases before qualification. Preserve HTTP cancellation/generation and all existing forms. Native audio/monitor default backend remains unavailable until C ABI wiring. Control-mode PUT depends on unported PicoClaw runtime/preemption/event hooks; don't fabricate runtime state. Then remaining system/config APIs, native media, addons, package/image and agreed hardware window. H.265 WebRTC stays disabled unconditionally; direct H.265 in scope. All significant actions/results permanently logged. Use WSL Python edits via Windows work/; no WSL restart, push/publication/stable merge or stand changes.

## User steering (2026-10-05)
Choose the best maintained upstream projects; do not pick abandoned libraries or constrain the design to the old Rust 1.86 installation. Dependency maintenance/qualification recorded and committed with stage 2; webrtc-rs selected for integrated TURN parity, str0m retained as a measured alternative. No abandoned-project fallback or Go runtime bridge.
Fix implementation defects immediately when found (explicit user steering). This slice corrected boot-marker names before qualification, fail/stale-queue handling and shutdown release; ordinary/dependent origin tests account for Go's default trusted loopback proxies.
