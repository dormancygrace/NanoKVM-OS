# Connected native backend and compound monitor transactions

`native_backend::Native` connects the retained `monitor::Backend` to the already qualified C capture worker. Screen/HDMI/profile APIs now reach actual worker IPC when this backend is injected. `Runtime::load_with_native` explicitly launches/initializes the fixed worker for the canonical firmware root `/`, selects saved GOP/chroma before capture initialization, constructs the ordinary Runtime and initializes durable HDMI intent. It refuses isolated roots. The default isolated executable still uses the unavailable backend, and main's production activation gate remains until full functional/package/hardware qualification.

No Go runtime/transport bridge or new dependency is involved. Native C/C++ ABI, the worker protocol and production worker C source remain unchanged. The C fixture gained only child-local traces/fault injection, enabled in tests.

## One owner and bounded admission

The existing actor's eight-slot queue now accepts asynchronous capture/control calls, synchronous calls from API blocking workers, and private trusted compound jobs. All use absolute queue/request deadlines and drop cancellation. Blocking admission does not invoke a Tokio runtime or block_on; HTTP dispatch already runs it off the event-loop thread. Cancelled queued requests are skipped before native effects. Saturated queues fail immediately.

Compound jobs hold the same owner throughout pause, external programming and restoration. Frames, HDMI controls, status reads and GOP/chroma commands cannot interleave with an EDID transaction. A video-status pair is read within one job and decoded using the retained fallback priority. Unknown status bits preserve Go's behavior; negative/out-of-byte results are failures.

## EDID lifecycle and defects corrected

For Cube, native maintenance pauses capture, then the mode-0600 pending marker is atomically persisted and synced **before** launching `nanokvm_update_edid --accept-power-cycle <profile>`. The marker survives helper failure, timeout and shutdown because receiver flash can already have changed. Other boards pause HDMI and invoke the same fixed utility with one profile argument. The original helper-failure response is retained.

Native pause/control waits have their own two-second cap; the helper is capped at 90 seconds within an overall 94-second queue/job budget. The fixed utility runs with a cleared environment and fixed PATH, as an owned process group through the already qualified bounded combined-output runner. Timeout/cancellation kills descendants and reaps the process. A literal profile argument handles spaces/metacharacters without a shell. The firmware programmer independently refuses isolation and nonregular profiles.

Restoration runs under the same owner with independent short deadlines even after the main operation expires or shutdown is requested. The blocking transaction permits at most nine seconds of cleanup grace, covering maintenance/HDMI restore and deinit. It honors the latest durable `hdmi_disable` marker rather than unconditionally turning HDMI on: an administrator can persist a disable while its native command waits behind maintenance. Cube maintenance resumes, then explicitly keeps HDMI off when that durable intent requires it.

A restoration failure cannot leave the owner admitting frames as if capture recovered. It requests bounded native deinit, then ensures owned termination. The primary programming error is preserved when both programming and restore fail; cleanup failure is logged. No worker automatically restarts after uncertain failure.

`Runtime::shutdown_async` runs stop and native join in the blocking pool; main's signal/normal completion paths use it. Actual helper cancellation, restoration, C deinit and worker thread joining are tested on a current-thread Tokio runtime. The synchronous stop path remains available for existing callers.

## Audio boundary and limitations

The backend owns an explicit `AudioControl` interface and delegates USB lifecycle audio stop and shutdown to it. Fixtures verify the owner is called. Real audio capture/subscriptions are not implemented in this stage: callers must supply that owner, and `AudioUnavailable` reports a failure. The future audio package must replace that boundary with the shared capture/Opus owner. No silent audio-ready claim is made.

The root-only loader is a code path, not device activation. Real vendor initialization/capture, I2C/EDID helper execution and hardware performance remain unqualified and require the agreed window. No host/device service, setting, package or runtime was changed.

## Evidence

`refresh-v3-native-status-oracle.py` executes the complete immutable Go GetMjpegChromaStatus function for all 256 possible native bytes. Only its C byte read and six constants are replaced with fixture values taken from the unchanged header. The source/module hashes and results are recorded in native-status-go-oracle.json.

Thirteen integration tests use the real synthetic C worker process and private IPC: saved initialization/actual status/GOP/chroma/audio ownership; all status bytes against actual Go; normal/Cube helper success/failure order and durable marker mode; marker-write failure; pause/restore faults and fail-closed owner; helper timeout with recovery; stalled native pause with owned termination; shared async/blocking eight-slot saturation/no frame interleave; real screen/HDMI APIs with concurrent durable disable; shutdown/cancel/restore/deinit/join; cancelled queued GOP; isolated-root refusal; and inspection of literal fixed utility arguments. Existing capture-process/actor regressions pass.

`stage3-native-backend-qualification.json` records frozen source hashes and full 226 host AND static generic riscv64/QEMU tests (105 unit, 121 integration), fmt/clippy, default release, 173 SPDX dependency licenses without missing declarations, actual host/target certificate-verified HTTPS/307/Secure-cookie/SIGTERM/occupied-port/internal-loopback gates, and feature-free ELF exclusion of vendor/synthetic native implementations/DT_NEEDED. These prove software/fixture integration, not device readiness.

Commands: `cargo test --locked --features native-fixture --lib native_backend_tests`; `cargo test --locked --features native-fixture --lib capture_tests`; full `python3 scripts/check-v3.py --host --target --tls`; supplemental host `python3 scripts/check-v3.py --tls`. Actions, observed defects, fixes and results remain in actions.md.

## Next work package

Implement the shared video source and subscriptions: HDMI demand/read leases, immutable per-subscription codec configuration, bounded 24-frame queues, IDR/overflow/reconfiguration/draining, coalesced keyframe requests, capture cadence and shared native-frame lifetime/budget. Connect authenticated MJPEG and Direct H.264/H.265 routes using that source. Then audio/WebRTC/media consumers, remaining APIs/addons/files/terminal/update, package/image/global limits and agreed device gates. H.265 WebRTC always returns h265-webrtc-disabled; H.265 Direct remains required. The route ledger stays 64 isolated, one partial and 139 pending; the full goal remains active.
