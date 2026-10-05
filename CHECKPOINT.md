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
- 213 host/target tests pass: 92 unit, nine screen, seven HDMI API/socket, 27 API, five dashboard, nine CPU, eight time, three identity, three memory read, six memory mutation, three service, twenty-one existing/paste real-socket, nine USB/monitor and eleven GPIO/system fixture tests; 13 actual Go response cases and two-way JWT interoperability. Binding fixes committed as d820817. LED slice committed as c80594e. Ledger: 64/204 implemented in isolation, one partial (/api/ws), 139 pending.
- Live authenticated/origin-checked/revocable WS, bounded HID queues/descriptors, HTTP leases, release/transfer/shutdown and LED REST/WS readiness/replacement/backoff are wired and tested. Manual/addon mode leases, cancellation, held/cooldown and PicoClaw locking are wired. See stage3-coordinator.md and its qualification JSON. Jiggler API/worker/priority/cleanup and actual 15-second timer are tested; see stage3-jiggler.md and qualification JSON. Admin USB reset/profile lifecycle, confined script install and cancellable actions pass; see stage3-usb.md. Internal recover token gate and real companion HTTP exception pass; body waits no longer occupy execution jobs. See stage3-internal-usb.md. Composition/budget/rollback/EDID core and injected lifecycle pass; default native media backend remains unavailable. See stage3-composition.md. HTTP paste and 833-key actual cadence/cleanup pass; see stage3-paste.md. URL form/query/method/media/escape parity repaired; maintained mediatype 0.23.0 qualifies on target. See stage3-form.md. Current release SHA256 876a9a96f9b36abf03423c3fe66ac892c65e84cc2d17b2776106e7f911c16293, 6,989,616 bytes. Screen state model/cache/rate policy qualifies in stage3-screen-state.md and qualification JSON; its Runtime/API connection now qualifies in stage3-screen-api.md; media transports remain pending. HDMI control/demand/idle/warmup/RAII read leases/reset cancellation/admin races and durable failures qualify in stage3-hdmi.md and qualification JSON; default native backend remains unavailable. No hardware input/media/performance qualification.
- Existing UI login passes in Chrome Main on an isolated loopback Rust server. Real host AND riscv64 musl/QEMU HTTPS certificate validation, 307 redirect, Secure cookie, SIGTERM and occupied-port rejection pass.
- YAML uses maintained serde-saphyr 1.3 with Viper key/null/default fixtures; dependency repository metadata and resolved licenses recorded.
- Select webrtc-rs 0.21.0 (runtime-tokio + crypto-ring) for feature parity: host/target direct and authenticated relay-only ICE/DTLS/SCTP exchanges pass. str0m 0.24.1 host/target direct exchange also passes but needs a separate TURN client; retained as a measured performance alternative. Neither qualifies hardware/browser media yet.
- Go app depends on patched Pion ICE/DTLS/SRTP and RustDesk currently calls Go media/HID/WebRTC bridges.

## Active goal
An active Codex goal was explicitly requested and created on 2026-10-05 for this chat (01a108fb-2fbc-7ef0-86d4-e359836f3c07), without a token budget. Complete the full Go-to-Rust/C replacement in this branch, retaining all 204 API/WS/media/system functions, UI, authorization, configuration and persistent formats. Retain native C/C++; no Go server or Go bridge in the final runtime. Qualify a reproducible static riscv64 musl runtime, APK/OpenRC package and image, then agreed hardware stability/performance checks. H.265 WebRTC always returns h265-webrtc-disabled; H.265 Direct is supported. Prefer maintained upstream projects and fix discovered defects immediately.
The goal remains active: 64 isolated routes, one partial and 139 pending do not constitute completion. Device 192.168.4.128 remains read-only until an agreed window; no publication, push or stable merge. Concrete work packages and evidence/retest rules are in docs/experiments/v3.0/implementation-plan.md. Update this checkpoint and actions.md before/after significant changes and checks.

## Next action
Connected screen API work package completed and qualified: full213 tests on host AND static generic riscv64/QEMU (92unit+121integration), fmt/clippy, default static release and actual host/target HTTPS/307/Secure-cookie/SIGTERM/occupied-port/internal-loopback gates. Evidence stage3-screen-api-qualification.json; release 6989616 bytes, SHA256 876a9a96f9b36abf03423c3fe66ac892c65e84cc2d17b2776106e7f911c16293; no DT_NEEDED/synthetic/vendor native symbols; 173 SPDX dependencies without missing licenses. Route ledger now64isolated/1partial/139pending of204.
Runtime now owns the screen Manager; GET/POST /api/vm/screen are connected. Reused105 actual immutable Go handler/profile cases match response/status/saved files/whole screen/ordered native calls, including all monitor/portrait/ION/stride/power-cycle/pointer paths. Nine integration fixtures prove roles and HID independence, cancellable settings/publication/chroma rollback, cancellation behind independent USB monitor lock, shared profile/persist serialization, preserved modes/legacy symlinks/atomic portrait final-link replacement/marker unlink, native GOP failure, missing parents and honest profile-after-effect persistence failure. Corrected invented viewer fixture role to actual user and updated stale earlier501 expectation. Default native status/GOP/chroma/profile backend remains unavailable; no vendor/device effects.
NEXT package2 in implementation-plan.md: add bounded synchronous native Actor admission for the blocking Backend; execute compound capture/EDID maintenance/program/restore transactions without interleaving; map native status and effects; connect explicit runtime startup/shutdown and audio ownership. Native14 ABI/helper/worker/frame/async actor already qualified (native-capture docs); do not reopen those foundations without a concrete integration requirement. Whole native helper transaction: Cube maintenance pause, durable pending marker BEFORE bounded nanokvm_update_edid --accept-power-cycle, resume; other boards HDMI pause/helper/restore. Runtime worker is not launched yet. Retain isolation refusal and hardware controls only in approved window.
Then package3 shared subscriptions/MJPEG/Direct with immutable codec config, bounded queues/IDR/drain/frame budget/cadence; package4 audio/WebRTC/RustDesk/MCP/PicoClaw; remaining APIs/terminal/files/addons/updater/APK/OpenRC/image and agreed hardware gates. Frame budget is not full GOMEMLIMIT; HTTP header15s/idle2m still pending. Goal ACTIVE, full migration far incomplete. Stand192.168.4.128 read-only; no push/publication/stable merge. H265 WebRTC always disabled; DirectH265 in scope. Use WSL --cd repo; read checkpoint first after compaction.

## User steering (2026-10-05)
Choose the best maintained upstream projects; do not pick abandoned libraries or constrain the design to the old Rust 1.86 installation. Dependency maintenance/qualification recorded and committed with stage 2; webrtc-rs selected for integrated TURN parity, str0m retained as a measured alternative. No abandoned-project fallback or Go runtime bridge.
Fix implementation defects immediately when found (explicit user steering). This slice corrected boot-marker names before qualification, fail/stale-queue handling and shutdown release; ordinary/dependent origin tests account for Go's default trusted loopback proxies.
