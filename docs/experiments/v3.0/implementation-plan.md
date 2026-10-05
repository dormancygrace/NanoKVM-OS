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
| 3 | Shared subscriptions, MJPEG and Direct H.264/H.265 | IN PROGRESS: source foundation DONE software; status/FPS/APIs/transports NEXT | Real socket streams, IDR/reconfiguration/queue/drain/budget/failure tests |
| 4 | Audio/WebRTC and media consumers (RustDesk/MCP/PicoClaw) | Transport candidates qualified; integration pending | Authenticated direct/relay, protocol/media and cleanup tests; unconditional H.265 WebRTC rejection |
| 5 | Remaining system/files/terminal/addon/update APIs | Pending; route matrix is source of completeness | Per-family parity and real effects inside isolated roots; no Go bridges |
| 6 | Complete package/APK/OpenRC/update/image and global resource/time limits | Pending | Reproducibility, package contents/install/rollback, complete host/target software gates |
| 7 | Agreed hardware window and final qualification | Not authorized for activation yet | UI compatibility, stability and measured performance on device; final handoff |

## Current frozen evidence
Shared video source foundation:240 tests on host AND generic static riscv64/QEMU (119unit+121integration), fmt/clippy/feature-free release and actual host/target TLS/307/Secure-cookie/SIGTERM/occupied-port/internal-loopback gates. Release 7,011,384 bytes SHA25628ee3476bc2991cdab8c16fbc338b14d13739ea0930fb3fad3eff13c90c84819, unchanged173 SPDX dependencies. stage3-video-source-qualification.json records frozen sources,14 real synthetic C/source fixtures,18 original Go tests and220 differential values. Native backend/EDID foundation remains qualified; source/Runtime/Actor/HDMI ownership and shutdown are connected. Closed queue lifetime and failed-IDR admission intent are fixed. Media routes, capture-status/FPS, real audio/vendor/native startup/package/hardware remain pending. Ledger64isolated/1partial/139pending; no route completion from source foundation alone.

## Rules against repeating completed work
- Continue the first incomplete work package. Do not reopen completed foundations without a changed source, failed check or concrete new integration requirement.
- Use existing immutable Go references. Regenerate only if adding missing cases or correcting fixture/provenance defects; document the reason.
- During development run focused checks for the changed behavior. Freeze and run full gates once a meaningful connected stage is ready; do not run the full suite merely to restate a prior pass.
- Every significant action has a before entry (scope, purpose, effects) and after entry (result, defect/fix, next step) in actions.md. Update CHECKPOINT.md at completed or interrupted boundaries.
- Prevalidate replacement anchors and new-file collisions before source/log mutations. Stop dependent sequences on failed commands. Commit only qualified bounded changes locally; no push/publication/stable merge.

## Immediate next step
Implement the remaining package3 connection: capture-status/WS and FPS counter, encoder-state/config/frame-detect contracts, authenticated MJPEG and Direct H264/H265 transports. Reuse the qualified source/Actor/HDMI and existing immutable Go references. Prove real socket auth/origin/revocation/expiry, signed flow ACKs/queue/IDR/drain, multipart/dedup/cache, viewer demand and cancellation/shutdown. Source tests are not complete media route or hardware qualification. Subsequent audio/WebRTC/consumers, remaining APIs, global limits/package/image/native startup and agreed hardware gates remain required.
