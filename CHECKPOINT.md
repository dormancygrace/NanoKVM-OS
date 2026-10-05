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
- 82 host/target tests pass: 33 unit, 21 API, nineteen existing/paste real-socket and nine USB/monitor fixture tests; 13 actual Go response cases and two-way JWT interoperability. Binding fixes committed as d820817. LED slice committed as c80594e. Ledger: 32/204 implemented in isolation, one partial (/api/ws), 171 pending.
- Live authenticated/origin-checked/revocable WS, bounded HID queues/descriptors, HTTP leases, release/transfer/shutdown and LED REST/WS readiness/replacement/backoff are wired and tested. Manual/addon mode leases, cancellation, held/cooldown and PicoClaw locking are wired. See stage3-coordinator.md and its qualification JSON. Jiggler API/worker/priority/cleanup and actual 15-second timer are tested; see stage3-jiggler.md and qualification JSON. Admin USB reset/profile lifecycle, confined script install and cancellable actions pass; see stage3-usb.md. Internal recover token gate and real companion HTTP exception pass; body waits no longer occupy execution jobs. See stage3-internal-usb.md. Composition/budget/rollback/EDID core and injected lifecycle pass; default native media backend remains unavailable. See stage3-composition.md. HTTP paste and 833-key actual cadence/cleanup pass; see stage3-paste.md. URL form/query/method/media/escape parity repaired; maintained mediatype 0.23.0 qualifies on target. See stage3-form.md. Current release SHA256 56fb182193ffb093f0c0033f2d60c7cdded4cd47e1b24f8a9201d0e7474646ae, 6,361,440 bytes. No hardware input/media/performance qualification.
- Existing UI login passes in Chrome Main on an isolated loopback Rust server. Real host AND riscv64 musl/QEMU HTTPS certificate validation, 307 redirect, Secure cookie, SIGTERM and occupied-port rejection pass.
- YAML uses maintained serde-saphyr 1.3 with Viper key/null/default fixtures; dependency repository metadata and resolved licenses recorded.
- Select webrtc-rs 0.21.0 (runtime-tokio + crypto-ring) for feature parity: host/target direct and authenticated relay-only ICE/DTLS/SCTP exchanges pass. str0m 0.24.1 host/target direct exchange also passes but needs a separate TURN client; retained as a measured performance alternative. Neither qualifies hardware/browser media yet.
- Go app depends on patched Pion ICE/DTLS/SRTP and RustDesk currently calls Go media/HID/WebRTC bridges.

## Next action
Internal recovery 5676c9f; composition/monitor association fc2cece; HTTP paste ba06d5c. Generic URL form correction passes 82 host/target/TLS tests and is locally committed; finish license-snapshot amend. Full hardware.go and GPIO monitor tests are now read. Then GPIO/ATX and hardware detection: full Go service/vm/gpio.go, gpio_monitor.go, internal/gpioio/gpio_linux.go and controller tests, router/vm.go, system.go read. Current baseline uses GPIO-v2 (not v1!) and legacy sysfs image ABI. v2 requests atomically start output inactive (flags input 1<<2/output 1<<3, output-values attr ID); request labels nanokvm-atx, chip label lookup, offset bounds, one pulse try-lock, cancellation/failure ALWAYS deassert+close and aggregate cleanup errors. Defaults 800ms, uint duration <=60000ms; invalid-message formatting depends on Go binder/validator. Monitor lazy-one-worker samples20ms, power active-low, HDD active-low plus350ms hold, 1s reopen retry, optionalHDD, close errors and stop. Read native GPIO-v2 UAPI before implementing; hardware mapping and monitor tests are fully read. Enhanced marker is /etc/nanokvm-buildroot containing flavour=enhanced, optional /etc/kvm/board-profile overrides /etc/kvm/hw. Lite returns Beta with no pins; unknown enhanced board retains compatibility version with all pins empty. Legacy version removes newlines only, missing/invalid defaults Alpha. Alpha reset27/HDD25 on3020000.gpio, Beta reset25/noHDD, PCIE reset25 plus HDD3 on5021000.gpio; power23/LED24 on3020000.gpio. Native output is active-high, LEDs are active-low. Enhanced unknown board must expose no GPIO; read detection marker first. Rust config currently doesn't expose Hardware. Add typed uint schema/form parsing and actual Go validation/ABI fixtures; callbacks/isolated files must never request host GPIO. Reboot uses existing guarded executor, return success before delayed action as profile fix. Other Gin encodings and raw invalid-byte persistence remain separate integration. Control-mode PUT needs real PicoClaw runtime hooks; native audio/monitor backend remains unavailable until C ABI stage. Then system/config APIs, native media/addons/package/image/hardware. Stand read-only, no production activation or push/publication/stable merge. Log all significant actions/results and use WSL Python edits from Windows work/.

## User steering (2026-10-05)
Choose the best maintained upstream projects; do not pick abandoned libraries or constrain the design to the old Rust 1.86 installation. Dependency maintenance/qualification recorded and committed with stage 2; webrtc-rs selected for integrated TURN parity, str0m retained as a measured alternative. No abandoned-project fallback or Go runtime bridge.
Fix implementation defects immediately when found (explicit user steering). This slice corrected boot-marker names before qualification, fail/stale-queue handling and shutdown release; ordinary/dependent origin tests account for Go's default trusted loopback proxies.
