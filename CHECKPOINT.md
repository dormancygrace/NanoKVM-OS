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
- 125 host/target tests pass: 42 unit, 24 API, nine CPU, eight time, three identity, nineteen existing/paste real-socket, nine USB/monitor and eleven GPIO/system fixture tests; 13 actual Go response cases and two-way JWT interoperability. Binding fixes committed as d820817. LED slice committed as c80594e. Ledger: 46/204 implemented in isolation, one partial (/api/ws), 157 pending.
- Live authenticated/origin-checked/revocable WS, bounded HID queues/descriptors, HTTP leases, release/transfer/shutdown and LED REST/WS readiness/replacement/backoff are wired and tested. Manual/addon mode leases, cancellation, held/cooldown and PicoClaw locking are wired. See stage3-coordinator.md and its qualification JSON. Jiggler API/worker/priority/cleanup and actual 15-second timer are tested; see stage3-jiggler.md and qualification JSON. Admin USB reset/profile lifecycle, confined script install and cancellable actions pass; see stage3-usb.md. Internal recover token gate and real companion HTTP exception pass; body waits no longer occupy execution jobs. See stage3-internal-usb.md. Composition/budget/rollback/EDID core and injected lifecycle pass; default native media backend remains unavailable. See stage3-composition.md. HTTP paste and 833-key actual cadence/cleanup pass; see stage3-paste.md. URL form/query/method/media/escape parity repaired; maintained mediatype 0.23.0 qualifies on target. See stage3-form.md. Current release SHA256 7f0b7bfceb51a0f5d8df7b9db4f56e0947616affa82e1570cc1840f2cbefd36c, 6,701,392 bytes. No hardware input/media/performance qualification.
- Existing UI login passes in Chrome Main on an isolated loopback Rust server. Real host AND riscv64 musl/QEMU HTTPS certificate validation, 307 redirect, Secure cookie, SIGTERM and occupied-port rejection pass.
- YAML uses maintained serde-saphyr 1.3 with Viper key/null/default fixtures; dependency repository metadata and resolved licenses recorded.
- Select webrtc-rs 0.21.0 (runtime-tokio + crypto-ring) for feature parity: host/target direct and authenticated relay-only ICE/DTLS/SCTP exchanges pass. str0m 0.24.1 host/target direct exchange also passes but needs a separate TURN client; retained as a measured performance alternative. Neither qualifies hardware/browser media yet.
- Go app depends on patched Pion ICE/DTLS/SRTP and RustDesk currently calls Go media/HID/WebRTC bridges.

## Next action
Identity/info/adminmdnsstatus passes125host/target/TLS tests with28Go file/IP/PIDcases and actualownedlibcgetifaddrs genericmusl/QEMU ABI. Readonlyexecutor network, notstand. See stage3-info.md; Identity committed locally (initialdf0aec2, log amendment). Time16e525f with125Go cases (122testsoriginal), CPU10424d2, GPIO987dafa, hostname36058e6, OLED94ef207 retained. Jiff0.2.37 std-only explicitTZif linked,173SPDXpackages. Shared preferences preserve final-link snapshots, modes, absence, post-rename fault recovery. Next readonlymemory status and dashboard. Fullyreadall dashboard source/tests plus memory-status.go/test,video-memory.go/test,memory-maintenance.go,memory.go; maintenance tests/Go-memory-limit utils remainunread. ReadMemoryStatus: meminfo uint64*k1024,total>0,minavailable/cacheShmem/swapFree,swaps activezram0/swapfile/ceilMiB/default64/256, persisted inactive selections/recompressionlastwins, helper+sys/module/built-in/modulesglobavailability, secondaryzstd/mmstat/selectedalgorithm/IONbytes, devicetreevideo mode active/selected/bothboardbootimages/rebootneeded. Plan pureGo filesystem oracle and scopedreadonlyport before telemetrydashboard. Dashboard needs independent1secCPU sampler/stale3s, temperatureboardhwmon→thermal fallback,maxvalid [-40,150]C,actualcpuinfofreq,proc uptime/load/kernel/hostname, networkkindnetlink/MAC/MTU/addresses andstatfs root/boot/data. mDNSenable/disable/SSH,swap/video mutation/maintenance andGo-memory-limit equivalent remainpending. No Go server/transportbridge. Continueother204APIs/media/addons/package/image/hardware;46isolated/1partial/157pending. Standread-only,noactivation/publish/merge; H.265WebRTCdisabled. Readcheckpointaftercompaction.

## User steering (2026-10-05)
Choose the best maintained upstream projects; do not pick abandoned libraries or constrain the design to the old Rust 1.86 installation. Dependency maintenance/qualification recorded and committed with stage 2; webrtc-rs selected for integrated TURN parity, str0m retained as a measured alternative. No abandoned-project fallback or Go runtime bridge.
Fix implementation defects immediately when found (explicit user steering). This slice corrected boot-marker names before qualification, fail/stale-queue handling and shutdown release; ordinary/dependent origin tests account for Go's default trusted loopback proxies.
