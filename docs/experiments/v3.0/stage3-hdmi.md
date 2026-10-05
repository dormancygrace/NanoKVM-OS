# HDMI control and demand

The Rust handlers cover `GET /api/vm/hdmi` and `POST /api/vm/hdmi/{reset,enable,disable,timeout}`. They use the existing route inventory for authorization: state/reset require a session; enable/disable/timeout require an administrator. They do not acquire HID ownership. The shared media backend now has explicit HDMI control and signal methods, with unavailable defaults. No native process is launched by this stage.

`hdmi_disable` and `hdmi_idle_timeout` keep their existing paths and formats. Missing/invalid timeout defaults to zero; accepted values are 0..10080 minutes. JSON integers, case folding, duplicates, primitive nulls and first-value decoding follow Go binding. Form names use `Minutes`, the first body value takes precedence over the query, and Gin 1.12 Unicode trimming is retained. Existing file permissions survive timeout updates. New files use 0644. Settings mutations synchronize data and the parent directory before issuing the native command.

The Manager serializes native HDMI effects with control state. Per-streamer monotonically increasing revisions prevent stale viewer reports from cancelling newer demand. Negative counts become zero. RAII leases suppress idle shutdown until the last owner releases; reader leases wait for the one-second warmup outside the state mutex and share one read slot. Only the first successful reader consumes the fresh-frame claim. Native failures never mark a frame ready. A 250ms idle worker uses weak ownership and performs blocking backend work outside the async event loop. Actual vendor latency remains unqualified.

A reset disables capture, waits one second and restores capture only if its control revision still owns the operation and durable administrator intent permits it. Concurrent disable, enable, a newer reset or shutdown wins over the older reset. Cancelling its HTTP handler restores only the state owned by that reset and releases the blocking execution permit. State changes while a reader is active invalidate its fresh-frame claim.

## Evidence

- `scripts/refresh-v3-hdmi-oracle.py` extracts immutable baseline a53b25579ab87cc98f85323b4743deb0d4da907b. Complete HDMI handler/demand logic is unchanged; only two persistent path constants point into a temporary root and native vision calls are stubbed. The pinned module files and proto request/response are checked and hashed. Go is a development oracle only.
- `hdmi-go-oracle.json` records 41 actual responses, native-call sequences and persisted-file results, plus 19 demand transitions.
- `scripts/refresh-v3-form-scalar-oracle.py` and `form-scalar-go-oracle.json` record 63 actual pinned Gin integer/bool form cases, including whitespace-only/Unicode values, overflow, defaults, first body/query values and duplicates. The rejected raw-strconv hypothesis is corrected in the action log; non-string whitespace trimming is required.
- Twelve lifecycle unit tests cover the oracle, source/counter bounds, saved intent, stale idle reports, leases, warmup, read serialization, fresh claims, reset cancellation/admin races, persistent/native failures and event-loop progress during a delayed backend call.
- Seven API tests cover the actual oracle and effects, roles/HID independence, durable intent before native calls, persistence/native errors, unavailable defaults, cancelled router futures and actual loopback HTTP disconnect.
- `stage3-hdmi-qualification.json` records the completed frozen-source host/target/static/TLS gates after qualification. No hardware media/performance claim follows from fixture results.

## Deliberate error-path corrections

Baseline Go logs persistent-setting failures and still reports success. Rust returns code -2, `HDMI operation failed`, and does not send the native command after a persistence error. An uncertain native result also returns failure while retaining the saved intent; it does not manufacture a signal or ready frame. Reset of disabled capture retains the existing -2, `HDMI capture is disabled`, response. Invalid timeout arguments retain -1, `invalid arguments`.

An invalid/dangling disable marker remains a disable intent. It cannot silently enable capture. Mutations reject nonregular marker/timeout files and all paths remain confined to the runtime root. Internal viewer bookkeeping has at most 64 source names of at most 128 bytes and checked counters; overflow/admission failures preserve earlier demand.

## Remaining integration

The default media backend still reports unavailable. Real startup must initialize the native worker/backend and then call `Manager::initialize`; fixtures call it explicitly. Connect MJPEG/Direct/WebRTC/RustDesk authoritative source counts and MCP read leases to this Manager. Native monitor/audio/profile operations still need whole-transaction serialization with capture. Native actor requests already have bounded IPC deadlines, but production HDMI backend wiring and hardware behavior remain pending. H.265 WebRTC stays disabled; Direct H.265 remains required. There is no package install, device activation, push or stable merge in this stage.
