# v2.6-a1 preparation and validation

Base: main de0e1dc0, PRs #65–#70 integrated. Image v1.1-a1 is the user-confirmed separate image version.

## Host checks completed

- Merge resolutions retain capture pause, JPEG geometry leases, concurrent-MJPEG pacing and both sets of artifact pins.
- Combined Go teststub suite, native quality/sink/capture/policy/geometry/pacing contracts, EDID tests and platform-build script tests passed.
- Final main CI revealed a stale per-frame ACK assertion and a GPIO clock-sampling timing dependency. Release fixes keep exact clock-mapping assertions and explicitly cover four-frame/40 ms ACK batching, backpressure release and timer cancellation.
- GPIO suite passed 50 runs with the race detector; web production build, lint and all 182 tests passed.

- All 42 init/tooling script checks passed. APK queue-timeout test now isolates lock waiting from process startup and asserts both exact output and the minimum lock wait; its suite passed five runs with the race detector under build load.

- Go build, vet and the complete teststub suite passed after those test fixes.

## Fresh platform build completed

- All components were built into a fresh output tree; only downloaded inputs and dependency caches were reused.
- 596 output hashes audited. Linux Image, all 96 module files, U-Boot, initramfs, board service and firmware reproduce the preceding release byte-for-byte. Changed outputs follow #65–#70 and the expanded boot profile matrix.
- All 25 board/memory profiles have the intended pool sizes and CMA/fixed properties. Each boot image composed from package inputs matches its independently built FIT byte-for-byte.
- FIP structure, CRCs, preserved first-stage/DDR components and load addresses pass the build verifier. FIP and OpenSBI binaries match the merged-main pins; OpenSBI's provenance manifest changes to record the merged build script.
- Shipped server contains the custom runtime hooks; 202 ChaCha20 and 1,439 SHA1 XTheadVector instructions match their source. The built web tree exactly matches the separately tested production build.
- PR #71 CI and CodeQL pass at preparation commit 6b5f7c9d.

## Packaging pending

Signed APKs, rootfs/image, upgrade dependency resolution and the corresponding source archive are the remaining host checks.

## Device status

No access, install or reboot. Existing user prohibition remains in effect. New FIP cold boot and physical HDMI transitions are not qualified by these host checks.
