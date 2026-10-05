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
- 102 host/target tests pass: 39 unit, 24 API, nineteen existing/paste real-socket, nine USB/monitor and eleven GPIO/system fixture tests; 13 actual Go response cases and two-way JWT interoperability. Binding fixes committed as d820817. LED slice committed as c80594e. Ledger: 40/204 implemented in isolation, one partial (/api/ws), 163 pending.
- Live authenticated/origin-checked/revocable WS, bounded HID queues/descriptors, HTTP leases, release/transfer/shutdown and LED REST/WS readiness/replacement/backoff are wired and tested. Manual/addon mode leases, cancellation, held/cooldown and PicoClaw locking are wired. See stage3-coordinator.md and its qualification JSON. Jiggler API/worker/priority/cleanup and actual 15-second timer are tested; see stage3-jiggler.md and qualification JSON. Admin USB reset/profile lifecycle, confined script install and cancellable actions pass; see stage3-usb.md. Internal recover token gate and real companion HTTP exception pass; body waits no longer occupy execution jobs. See stage3-internal-usb.md. Composition/budget/rollback/EDID core and injected lifecycle pass; default native media backend remains unavailable. See stage3-composition.md. HTTP paste and 833-key actual cadence/cleanup pass; see stage3-paste.md. URL form/query/method/media/escape parity repaired; maintained mediatype 0.23.0 qualifies on target. See stage3-form.md. Current release SHA256 3ce1e2c4ee758ed26aed61504c64ef3c5428457674c36382ee339d88c5979ba7, 6,461,016 bytes. No hardware input/media/performance qualification.
- Existing UI login passes in Chrome Main on an isolated loopback Rust server. Real host AND riscv64 musl/QEMU HTTPS certificate validation, 307 redirect, Secure cookie, SIGTERM and occupied-port rejection pass.
- YAML uses maintained serde-saphyr 1.3 with Viper key/null/default fixtures; dependency repository metadata and resolved licenses recorded.
- Select webrtc-rs 0.21.0 (runtime-tokio + crypto-ring) for feature parity: host/target direct and authenticated relay-only ICE/DTLS/SCTP exchanges pass. str0m 0.24.1 host/target direct exchange also passes but needs a separate TURN client; retained as a measured performance alternative. Neither qualifies hardware/browser media yet.
- Go app depends on patched Pion ICE/DTLS/SRTP and RustDesk currently calls Go media/HID/WebRTC bridges.

## Next action
GPIO/hardware/reboot committed 987dafa (96 tests) and hostname slice now passes 99 host/target/static/TLS tests. Hostname committed 36058e6 with 99 tests; OLED/string slice now 102 host/target/TLS tests. OLED/string slice committed locally. Next CPU/time settings. Full cpufreq.go and cpufreq_test.go read: sg2002-cpufreq driver required, verify actual hardware running clock, known thermal zone/cooling state only, wait effective min/max QoS 2s per limit, rollback on failure, legal 850/1000/1050/1075/1100/1125/1150 MHz, capability options file, persistent boot capped 1000 and /run overclock preference. OLED now implemented/tested; signed sleep/target schema and per-byte UTF8/lone UTF16 normalizer with actual Go oracles qualify. Raw-number parsing handles -0 and signed 64 bounds; generic/shortcut JSON depth 10000 check is iterative. Target CPU field prepared; add route schema and handler. Timeconfig config.go/chrony.go/ntp.go now fully read, tests still unread: system tzdata zone.tab sorted/compact+UTC; direct TZif validation; JSON format 12/24 and 1..6distinct host/IP servers; fallback timezone+ntpd/chrony server lines; atomic 0644 localtime/timezone/date-time JSON/changed-server daemon config transaction; reverse rollback+daemon recovery on restart failure. Display-only edits do not restart or step clock. Chrony CSV 14fields/stratum 1..15/nonlocalref/leap; ntpd READVAR 750ms sequence/header/source 6/leap!=3. Jiff 0.2.37 explicit TZif std-only probe now 58 Go cases match host AND generic-rv64 static musl/QEMU, six system zones 1900..9999 plus corrupt data. Source/lock probes/jiff, replay scripts/check-v3-jiff.py and jiff-probe-qualification.json. Near-final 9999 day Jiff Timestamp has a smaller global range than Go; probe uses Dec 29 and planned use is parser only. No server dependency added. Official maintenance Sep 12. Metadata saved jiff-maintenance-candidate.json; use qualified explicit TZif when implementing timeconfig. Hostname fields/hosts byte preservation/form/admin/Alpine symlink tests pass, no native hostname command. Continue other system/config APIs then native media/addons/package/image/hardware. Full replacement remains incomplete, 40 isolated / 1 partial / 163 pending routes. Control-mode PUT needs PicoClaw runtime hooks; native audio/monitor unavailable until C ABI stage. Stand read-only, no production activation, push/publication/stable merge. Read checkpoint after compaction.

## User steering (2026-10-05)
Choose the best maintained upstream projects; do not pick abandoned libraries or constrain the design to the old Rust 1.86 installation. Dependency maintenance/qualification recorded and committed with stage 2; webrtc-rs selected for integrated TURN parity, str0m retained as a measured alternative. No abandoned-project fallback or Go runtime bridge.
Fix implementation defects immediately when found (explicit user steering). This slice corrected boot-marker names before qualification, fail/stale-queue handling and shutdown release; ordinary/dependent origin tests account for Go's default trusted loopback proxies.
