# v2.6-a1 preparation and validation

Base: main de0e1dc0, PRs #65–#70 integrated. Image v1.1-a1 is the user-confirmed separate image version.

## Host checks completed

- Merge resolutions retain capture pause, JPEG geometry leases, concurrent-MJPEG pacing and both sets of artifact pins.
- Combined Go teststub suite, native quality/sink/capture/policy/geometry/pacing contracts, EDID tests and platform-build script tests passed.
- Final main CI revealed a stale per-frame ACK assertion and a GPIO clock-sampling timing dependency. Release fixes keep exact clock-mapping assertions and explicitly cover four-frame/40 ms ACK batching, backpressure release and timer cancellation.
- GPIO suite passed 50 runs with the race detector; web production build, lint and all 182 tests passed.

- All 42 init/tooling script checks passed. APK queue-timeout test now isolates lock waiting from process startup and asserts both exact output and the minimum lock wait; its suite passed five runs with the race detector under build load.

- Go build, vet and the complete teststub suite passed after those test fixes.

## Build in progress

Fresh platform output, with only downloaded source archives/caches reused. Output checksums require auditing against the preceding release before refreshing expected.sha256. Signed APKs, rootfs/image and source archive are still pending.

## Device status

No access, install or reboot. Existing user prohibition remains in effect. New FIP cold boot and physical HDMI transitions are not qualified by these host checks.
