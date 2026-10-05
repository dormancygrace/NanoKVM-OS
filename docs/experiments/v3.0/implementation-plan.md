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
| 2 | Connect bounded native backend and whole capture/audio/EDID transactions | NEXT; worker/frame/async actor qualified, blocking backend/transactions/startup pending | Actual synthetic worker lifecycle, cancellation/timeout/queue/transaction ordering, no default vendor launch |
| 3 | Shared subscriptions, MJPEG and Direct H.264/H.265 | Pending | Real socket streams, IDR/reconfiguration/queue/drain/budget/failure tests |
| 4 | Audio/WebRTC and media consumers (RustDesk/MCP/PicoClaw) | Transport candidates qualified; integration pending | Authenticated direct/relay, protocol/media and cleanup tests; unconditional H.265 WebRTC rejection |
| 5 | Remaining system/files/terminal/addon/update APIs | Pending; route matrix is source of completeness | Per-family parity and real effects inside isolated roots; no Go bridges |
| 6 | Complete package/APK/OpenRC/update/image and global resource/time limits | Pending | Reproducibility, package contents/install/rollback, complete host/target software gates |
| 7 | Agreed hardware window and final qualification | Not authorized for activation yet | UI compatibility, stability and measured performance on device; final handoff |

## Current frozen evidence
Connected screen stage:213 tests on host AND generic static riscv64/QEMU (92unit+121integration), fmt/clippy, feature-free release and actual host/target certificate-verified HTTPS/307/Secure-cookie/SIGTERM/occupied-port/internal-loopback gates. Release 6,989,616 bytes, SHA256 876a9a96f9b36abf03423c3fe66ac892c65e84cc2d17b2776106e7f911c16293. 173 SPDX dependencies, none missing. Full frozen evidence is stage3-screen-api-qualification.json. Route ledger:64 implemented-isolated, one partial-isolated,139 pending. Test count is not implemented route count.

## Rules against repeating completed work
- Continue the first incomplete work package. Do not reopen completed foundations without a changed source, failed check or concrete new integration requirement.
- Use existing immutable Go references. Regenerate only if adding missing cases or correcting fixture/provenance defects; document the reason.
- During development run focused checks for the changed behavior. Freeze and run full gates once a meaningful connected stage is ready; do not run the full suite merely to restate a prior pass.
- Every significant action has a before entry (scope, purpose, effects) and after entry (result, defect/fix, next step) in actions.md. Update CHECKPOINT.md at completed or interrupted boundaries.
- Prevalidate replacement anchors and new-file collisions before source/log mutations. Stop dependent sequences on failed commands. Commit only qualified bounded changes locally; no push/publication/stable merge.

## Immediate next step
Work package1 is qualified. Implement bounded synchronous admission to the existing native Actor, compound maintenance/helper/restore transactions and explicit native Backend/startup/shutdown/audio ownership. Then proceed to shared streams. Do not regenerate the unchanged Go screen references or requalify screen-only code without a new integration reason.
