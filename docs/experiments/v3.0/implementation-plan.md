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
| 3 | Shared subscriptions, MJPEG and Direct H.264/H.265 | NEXT | Real socket streams, IDR/reconfiguration/queue/drain/budget/failure tests |
| 4 | Audio/WebRTC and media consumers (RustDesk/MCP/PicoClaw) | Transport candidates qualified; integration pending | Authenticated direct/relay, protocol/media and cleanup tests; unconditional H.265 WebRTC rejection |
| 5 | Remaining system/files/terminal/addon/update APIs | Pending; route matrix is source of completeness | Per-family parity and real effects inside isolated roots; no Go bridges |
| 6 | Complete package/APK/OpenRC/update/image and global resource/time limits | Pending | Reproducibility, package contents/install/rollback, complete host/target software gates |
| 7 | Agreed hardware window and final qualification | Not authorized for activation yet | UI compatibility, stability and measured performance on device; final handoff |

## Current frozen evidence
Connected native backend stage:226 tests on host AND generic static riscv64/QEMU (105unit+121integration), fmt/clippy/feature-free release and actual host/target TLS/307/Secure-cookie/SIGTERM/occupied-port/internal-loopback gates. Release 6,991,696 bytes SHA2564ffaeafa5ac354bd3b15c9552ed5ec6a743e2656b40d974ad5e8dc3f12844dca,173 SPDX dependencies without missing licenses. Frozen proof stage3-native-backend-qualification.json includes13 actual synthetic C-process/API/transaction fixtures and256 actual Go status cases. Explicit root-only loader is implemented; default isolated backend/production activation gate remain. Real audio capture/vendor/device/package qualification are pending. Ledger64isolated/1partial/139pending; no new routes from backend wiring alone.

## Rules against repeating completed work
- Continue the first incomplete work package. Do not reopen completed foundations without a changed source, failed check or concrete new integration requirement.
- Use existing immutable Go references. Regenerate only if adding missing cases or correcting fixture/provenance defects; document the reason.
- During development run focused checks for the changed behavior. Freeze and run full gates once a meaningful connected stage is ready; do not run the full suite merely to restate a prior pass.
- Every significant action has a before entry (scope, purpose, effects) and after entry (result, defect/fix, next step) in actions.md. Update CHECKPOINT.md at completed or interrupted boundaries.
- Prevalidate replacement anchors and new-file collisions before source/log mutations. Stop dependent sequences on failed commands. Commit only qualified bounded changes locally; no push/publication/stable merge.

## Immediate next step
Work packages1 and native software connection2 are qualified. Implement shared video source/subscriptions and authenticated MJPEG/Direct endpoints, with actual ownership/queue/IDR/codec/demand/cadence failure tests. Real audio capture/owner, final main native startup/activation and hardware gates remain explicit later requirements. Do not repeat unchanged screen/status/ABI qualification without a new integration reason.
