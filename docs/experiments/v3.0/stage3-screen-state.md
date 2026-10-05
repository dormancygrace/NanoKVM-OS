# Screen snapshots and rate policy

`server-rust/src/screen.rs` implements the retained screen configuration and effective capture rate. This was qualified as a model foundation; its Runtime and both screen API handlers now connect and qualify in stage3-screen-api.md. Media transports remain pending. No native process, capture or profile programming starts from this module.

Boot defaults are Auto resolution, 50 FPS, MJPEG quality 80, bitrate 3000 kbit/s, GOP 30, SmartP and chroma 422. `NANOKVM_MJPEG_422=0` supplies a 420 default before saved files. Existing files retain their paths and meaning. Boot loading ignores invalid integer files, clamps FPS, normalizes retained quality/bitrate presets and limits saved QHD to Full HD if the booted Device Tree does not expose exactly four big-endian bytes with at least 62 MiB ION. The file is read through the confined runtime root.

Publication accepts the same ignore/clamp rules as Go and exposes complete value snapshots through an RwLock. `check` preserves the baseline's precise normalization fields; it does not add extra normalization of FPS/GOP. Each capture snapshot limits the selected FPS using both input and requested output dimensions. Unknown dimensions allow 120, 720p permits 120, normalized Full HD including aligned 1088 permits 75, and larger frames permit 50. The selected/saved FPS remains unchanged. Input file reads are cached for one second; changing output resolution takes effect immediately. The separate legacy FHD classifier retains its orientation-specific 1088x1920 exception.

`ReadVideoValue` discards Go's Atoi error. Therefore status-number overflow saturates to the signed limit, while syntax before uint64 overflow returns zero. The Rust status parser preserves even the early uint64-overflow case followed by malformed suffixes. Stored boot integers use strict parsing and ignore all errors, which is a different contract.

## Evidence

`scripts/refresh-v3-screen-state-oracle.py` executes complete immutable `common/screen.go` and `common/video_status.go` with only package renaming and fixed path redirection into a temporary root. The module files and source hashes are recorded in `screen-state-go-oracle.json`. There are 104 boot/publication/status/capture cases, 81 dimension/rate/FHD cases and four raw normalization cases.

Six Rust unit tests cover those actual Go results, deterministic cache expiry and input/output limits, concurrent whole-resolution publication, and confined absolute legacy links. `stage3-screen-state-qualification.json` records complete 204 host and static generic riscv64/QEMU tests, fmt/clippy, default release and actual host/target TLS gates. These are software/fixture checks, not hardware media qualification.

The next handler reference is already prepared: `scripts/refresh-v3-screen-api-oracle.py` executes the complete immutable screen handler, monitor/portrait/profile selection and Windows-pointer decoration against native vision/status and principal fixtures. Its 105 cases in `screen-api-go-oracle.json` include settings, JSON/forms, chroma rollback, monitor/portrait gates, power-cycle acknowledgement and pointer decoration. Native profile transactions and real authentication are outside this development oracle; the reference alone did not qualify a Rust handler. Connected Rust handler qualification is now recorded in stage3-screen-api.md.

## Remaining work

Runtime/GET/POST/shared profile controls now qualify in stage3-screen-api.md and its JSON; route ledger64isolated/1partial/139pending. Add bounded synchronous actor admission, compound capture/audio/EDID transactions, explicit native startup/shutdown and media subscriptions. H.265 WebRTC remains disabled; Direct H.265 is required. No dependency was added, and no vendor library, device, package installation, publication or stable merge was used for these checks.
