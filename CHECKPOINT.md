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
- 204 host/target tests pass: 92 unit, seven HDMI API/socket, 27 API, five dashboard, nine CPU, eight time, three identity, three memory read, six memory mutation, three service, twenty-one existing/paste real-socket, nine USB/monitor and eleven GPIO/system fixture tests; 13 actual Go response cases and two-way JWT interoperability. Binding fixes committed as d820817. LED slice committed as c80594e. Ledger: 62/204 implemented in isolation, one partial (/api/ws), 141 pending.
- Live authenticated/origin-checked/revocable WS, bounded HID queues/descriptors, HTTP leases, release/transfer/shutdown and LED REST/WS readiness/replacement/backoff are wired and tested. Manual/addon mode leases, cancellation, held/cooldown and PicoClaw locking are wired. See stage3-coordinator.md and its qualification JSON. Jiggler API/worker/priority/cleanup and actual 15-second timer are tested; see stage3-jiggler.md and qualification JSON. Admin USB reset/profile lifecycle, confined script install and cancellable actions pass; see stage3-usb.md. Internal recover token gate and real companion HTTP exception pass; body waits no longer occupy execution jobs. See stage3-internal-usb.md. Composition/budget/rollback/EDID core and injected lifecycle pass; default native media backend remains unavailable. See stage3-composition.md. HTTP paste and 833-key actual cadence/cleanup pass; see stage3-paste.md. URL form/query/method/media/escape parity repaired; maintained mediatype 0.23.0 qualifies on target. See stage3-form.md. Current release SHA256 c984b8033be33e963dc7449553a01d0549a41663aa5a5a6eaedc73724a204bf8, 6,929,432 bytes. Screen state model/cache/rate policy qualifies in stage3-screen-state.md and qualification JSON; its Runtime/API/media connection is pending. HDMI control/demand/idle/warmup/RAII read leases/reset cancellation/admin races and durable failures qualify in stage3-hdmi.md and qualification JSON; default native backend remains unavailable. No hardware input/media/performance qualification.
- Existing UI login passes in Chrome Main on an isolated loopback Rust server. Real host AND riscv64 musl/QEMU HTTPS certificate validation, 307 redirect, Secure cookie, SIGTERM and occupied-port rejection pass.
- YAML uses maintained serde-saphyr 1.3 with Viper key/null/default fixtures; dependency repository metadata and resolved licenses recorded.
- Select webrtc-rs 0.21.0 (runtime-tokio + crypto-ring) for feature parity: host/target direct and authenticated relay-only ICE/DTLS/SCTP exchanges pass. str0m 0.24.1 host/target direct exchange also passes but needs a separate TURN client; retained as a measured performance alternative. Neither qualifies hardware/browser media yet.
- Go app depends on patched Pion ICE/DTLS/SRTP and RustDesk currently calls Go media/HID/WebRTC bridges.

## Next action
Latest full software qualification204 tests host+static generic riscv64/QEMU (92unit+112integration), fmt/clippy/default release and actual host AND target certificate-verified HTTPS/307/Secure-cookie/SIGTERM/occupied-port/internal loopback gates. Screen-state-qualification JSON stores frozen hashes/counts/ELF absence; release6929432 bytes SHA256c984b8033be33e963dc7449553a01d0549a41663aa5a5a6eaedc73724a204bf8,173 SPDX no missing. No vendor/device execution. Ledger62isolated/1partial/141pending of204 unchanged by model stage.
Completed HDMI stage at25ca66e: five isolated routes, durable settings before effects/error reporting, shared Backend hooks, revisioned/bounded demand, idle worker outside event loop, one-second reset/warmup, RAII leases/single-reader/fresh claim, HTTP cancellation/real disconnect/admin race/stop. Default native backend unavailable. Full41 Go API/file/effect cases+19 demand transitions; pinned Gin1.12 trim restored and63 scalar cases qualified (raw-strconv hypothesis was wrong).
Screen model now qualified in server-rust/src/screen.rs: immutable copy snapshots/RwLock, boot/env/file defaults, quality/bitrate normalization, exact4-byte ION62MiB QHD gate, selected/effective FPS and one-second cached source dimensions, portrait-specific FHD class, actual Go status overflow/syntax saturation. Six tests include104 actual state cases+81 rates+4 raw checks, deterministic cache, concurrent snapshots and confined links. Model is NOT connected to Runtime/API/native streams yet.
Next full screen/profile/backend integration. Immutable full screen API oracle READY: scripts/refresh-v3-screen-api-oracle.py and screen-api-go-oracle.json,105 actual handler/profile cases (whole Go common/screen/video_status/monitor/windows_pointer, fixed root paths; only native vision/status/current principal stubbed). Includes JSON/forms, FPS/resolution/quality/type/GOP/mode/chroma, native/save failure+rollback, all monitor/portrait/ION/stride/power-cycle/pointer decoration paths. Native monitor transaction and real auth outside oracle; no Rust screen handler implementation claim. Complete source common/screen.go, video_status.go, monitor.go, windows_pointer.go, kvm_vision.go, stream/video_source.go+encoder_config.go, service/vm/screen.go, all HDMI fully read.
Implement bounded synchronous native Actor admission for shared blocking Backend, whole monitor/audio/capture transactions, Monitor profile controls and both screen handlers; wire model into Runtime. Native14 ABI/helper/worker/frame/actor ownership already qualified in native-capture docs; no Runtime worker launch currently. Then shared subscriptions/MJPEG/Direct/WebRTC/RustDesk/MCP leases, remaining APIs/addons/terminal/files/updater/APK/OpenRC/package/image and agreed hardware gates. Frame budget is not full GOMEMLIMIT; HTTP header15s/idle2m pending (Hyper header includes idle). Full migration far incomplete. Stand192.168.4.128 read-only until agreed window; no push/publication/stable merge. H265 WebRTC always disabled; DirectH265 in scope. WSL --cd repo; read checkpoint first after compaction.

## User steering (2026-10-05)
Choose the best maintained upstream projects; do not pick abandoned libraries or constrain the design to the old Rust 1.86 installation. Dependency maintenance/qualification recorded and committed with stage 2; webrtc-rs selected for integrated TURN parity, str0m retained as a measured alternative. No abandoned-project fallback or Go runtime bridge.
Fix implementation defects immediately when found (explicit user steering). This slice corrected boot-marker names before qualification, fail/stale-queue handling and shutdown release; ordinary/dependent origin tests account for Go's default trusted loopback proxies.
