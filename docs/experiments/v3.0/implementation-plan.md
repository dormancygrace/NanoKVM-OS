# Full Rust/C replacement: execution plan

Status: ACTIVE, explicitly requested goal, created 2026-10-05. Goal owner: chat 01a108fb-2fbc-7ef0-86d4-e359836f3c07. No token budget. Baseline a53b25579ab87cc98f85323b4743deb0d4da907b; work only in v3.0-experimental.

## Definition of done
- All 204 inventory routes and their WS/media/system effects preserve API/UI, roles, settings and durable formats. No pending or unsupported replacement route remains unless the retained contract requires that response.
- Native C/C++ capture remains; final runtime/package contains neither the Go server nor a Go bridge. H.265 WebRTC is always disabled with the retained error; Direct H.265 works.
- Reproducible static generic riscv64 musl Rust runtime plus required native worker, APK/OpenRC, update and image paths. Maintained dependencies, provenance/licenses and repeatable build checks are recorded.
- Software parity, transport, failure, cancellation, concurrency, shutdown and resource bounds pass. Agreed device UI/media/HID/addon/stability/latency/performance checks pass before replacing runtime.
- Final handoff documents verified results and residual limitations. Goal is complete only when these gates actually pass.

## Work queue and exit criteria
| Order | Work package | Current state | Exit evidence |
|---|---|---|---|
| 1 | Wire screen state into Runtime and GET/POST screen API; shared monitor/portrait/pointer operations | DONE in isolation; 105 Go cases/nine connected tests qualified | Response, file and backend-effect differential fixtures; roles/default failure; concurrent write/rollback checks |
| 2 | Connect bounded native backend and whole capture/audio/EDID transactions | DONE software connection; actual worker fixtures/explicit loader qualified; production audio/activation/hardware pending | Actual synthetic worker lifecycle, cancellation/timeout/queue/transaction ordering, no default vendor launch |
| 3 | Shared subscriptions, MJPEG and Direct H.264/H.265 | DONE in isolation: source/Direct/MJPEG/frame-detect/status/FPS and real H1/TLS-H2/Chrome delivery | Real socket streams, IDR/reconfiguration/queue/drain/budget/failure tests |
| 4 | Audio/WebRTC and media consumers (RustDesk/MCP/PicoClaw) | ACTIVE NEXT: shared native USB audio/AudioControl/USB stop ownership, then selected webrtc-rs/consumers | Authenticated direct/relay, protocol/media and cleanup tests; unconditional H.265 WebRTC rejection |
| 5 | Remaining system/files/terminal/addon/update APIs | Pending; route matrix is source of completeness | Per-family parity and real effects inside isolated roots; no Go bridges |
| 6 | Complete package/APK/OpenRC/update/image and global resource/time limits | Pending | Reproducibility, package contents/install/rollback, complete host/target software gates |
| 7 | Agreed hardware window and final qualification | Not authorized for activation yet | UI compatibility, stability and measured performance on device; final handoff |

## Current frozen evidence
MJPEG/frame-detect/HTTP transport stage qualified:293 host AND generic static riscv64/QEMU tests (172unit+121integration; one explicit browser helper ignored), fmt/strict clippy/feature-free release and actual target AND host main HTTPS/307/Secure-cookie/SIGTERM/occupied-port/internal-loopback gates. Binary 8126816 bytes SHA25699a7e9c352b49845ff29d3a32dcf58d4dba6c9755184c05e176dae3087732af4;173 Cargo/SPDX including root, no DT_NEEDED/vendor/synthetic implementation symbols or MJPEG fixture strings. stage3-mjpeg-qualification.json freezes current sources, tests, C, fixtures, complete Go reference and browser proof.
Three new routes qualify in isolation:GET /api/stream/mjpeg,POST /api/stream/mjpeg/detect and /api/stream/mjpeg/detect/stop. Ledger71implemented-isolated/1partial(/api/ws)/132pending of204.32newtests=8frameDetect+10transport+14MJPEG;130Go detector rows/596JPEG pairs/17delivery/3multipart. Actual C shared/static/new viewer/status5 recovery/held-read replacement, H1five-second slow writer, authenticated role/factory/absolute expiry under four busy jobs/HID independence, genuine validated TLS/ALPNh2 native revoke/live peer/sealed-frame owners pass. Chrome Main first/new static viewer47.3ms/19ms decoded64x48 RGBA220,30,40,255; bounded fixture exited with0viewers/64slots/budget0/native joined. This is synthetic C/Chromium transport proof, not vendor/hardware/full production UI qualification.
One native MJPEG loop/shared immutable owners/cache refs/page-budgeted mutable copies/one-slot queues/5s per-viewer refresh; following multipart headers before actual flush. Owned capture/watchdog/coalesced viewer publication and last-read drain. FrameDetect preserves newest desired0/60 and overlapping generation/drop/expiry/reset restore. H1IOcontrol/H2per-stream reset bounded16KiB copies/runtime shutdown; strict TLS ALPN and disabledh2c. Default native/backend/main production gates retained. Ledger renderer now reads reviewed routes and preserves all evidence/statuses; actual invocation cannot reset qualified work.

## Rules against repeating completed work
- Continue the first incomplete work package. Do not reopen completed foundations without a changed source, failed check or concrete new integration requirement.
- Use existing immutable Go references. Regenerate only if adding missing cases or correcting fixture/provenance defects; document the reason.
- During development run focused checks for the changed behavior. Freeze and run full gates once a meaningful connected stage is ready; do not run the full suite merely to restate a prior pass.
- Every significant action has a before entry (scope, purpose, effects) and after entry (result, defect/fix, next step) in actions.md. Update CHECKPOINT.md at completed or interrupted boundaries.
- Prevalidate replacement anchors and new-file collisions before source/log mutations. Stop dependent sequences on failed commands. Commit only qualified bounded changes locally; no push/publication/stable merge.

## Immediate next step
 shared USB audio/Opus source and AudioControl/Runtime/USB rebind cleanup, then audio/video WebRTC and consumers. Source contract already read:existing C native/usb-audio/capture.c;48kHz stereo20ms192000bitrate complexity3; packets big-endian1..1275bytes, identity UAC1Gadget card0..255,eight subscribers/four freshest packets,retry100ms..2s, stop/join2s before rebind. Preserve native helper, no Go bridge. Add complete immutable Go audio framing/queue/card/close/retry/status reference and focused real helper lifecycle tests; integrate selected maintained webrtc-rs after source ownership, no new source exploration of qualified MJPEG without new failure/consumer requirement.
Audio/WebRTC/consumers,132pending routes/global header15s/idle2m/process limits/APK/OpenRC/image/native startup/agreed hardware remain required. H265 WebRTC always h265-webrtc-disabled including softwarecrypto; DirectH265 supported in isolation. Goal ACTIVE; stand192.168.4.128 read-only; no vendor/device/host settings/push/publication/stable merge. Commit this qualified bounded stage locally, refresh Windows qualified documentation snapshot, then continue audio.
