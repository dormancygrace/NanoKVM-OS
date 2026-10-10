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

## Packages and image

- Seven core APKs total 38,411,060 bytes (38.4 MB). Their signatures and the legacy APKINDEX signature pass verification.
- The publication index retains optional RustDesk 0.5.3-r2. All eight packages verify; the combined Packages.adb verifies independently with the RSA and ECDSA public keys.
- Using APK 3.0.8 from the v2.5-a1 image and a copy of its installed database, `apk update` followed by simulated `apk upgrade` resolves all seven NanoKVM upgrades plus Alpine zlib 1.3.2-r1, without dependency conflicts. This is a solver check, not an on-device installation or execution of package triggers.
- Full-image ZIP: 70,140,001 bytes (70.1 MB). Uncompressed image: 872,415,744 bytes (832 MiB). MBR confirms a 64 MiB boot partition and a 768 MiB F2FS root partition.
- ZIP CRC passes and its extracted image hash equals the raw image. Image identity is v1.1-a1 / bundled applications v2.6-a1; rootfs application version is 2.6-a1. USB Internet helper/service and NanoKVM server are present.
- Public key directories contain no private keys. Temporary signing keys were removed from the package builder.
- The corresponding source archive is assembled from the final preparation commit, with a per-file SHA256 manifest. Final source/archive verification and publication checksums are recorded alongside the artifacts in the host release directory.

## Device status

No access, install or reboot. Existing user prohibition remains in effect. New FIP cold boot and physical HDMI transitions are not qualified by these host checks.
