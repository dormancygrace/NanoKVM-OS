# v2.6-a1 preparation and validation

Base: main 9cbbdcaa, PRs #65–#70 and #72 integrated. Image v1.1-a1 is the user-confirmed separate image version.

## Host checks completed

- Merge resolutions retain capture pause, JPEG geometry leases, concurrent-MJPEG pacing and both sets of artifact pins.
- Combined Go teststub suite, native quality/sink/capture/policy/geometry/pacing contracts, EDID tests and platform-build script tests passed.
- Final main CI revealed a stale per-frame ACK assertion and a GPIO clock-sampling timing dependency. Release fixes keep exact clock-mapping assertions and explicitly cover four-frame/40 ms ACK batching, backpressure release and timer cancellation.
- GPIO suite passed 50 runs with the race detector; web production build, lint and all 182 tests passed.

- All 42 init/tooling script checks passed. APK queue-timeout test now isolates lock waiting from process startup and asserts both exact output and the minimum lock wait; its suite passed five runs with the race detector under build load.

- Go build, vet and the complete teststub suite passed after those test fixes.

- The CPU-frequency polling test now publishes complete fixture values atomically, checks the effective limit itself before setspeed, reports worker errors and joins asynchronous updates before fixture cleanup. Its focused race-detector run passed 50 repetitions; no production CPU-frequency behavior changed. The full VM race suite, complete Go teststub suite and vet passed after the fix.

## Fresh platform build completed

- All components were built into a fresh output tree; only downloaded inputs and dependency caches were reused.
- 596 output hashes audited. Linux Image, all 96 module files, U-Boot, initramfs, board service and firmware reproduce the preceding release byte-for-byte. Changed outputs follow #65–#70 and the expanded boot profile matrix.
- All 25 board/memory profiles have the intended pool sizes and CMA/fixed properties. Each boot image composed from package inputs matches its independently built FIT byte-for-byte.
- FIP structure, CRCs, preserved first-stage/DDR components and load addresses pass the build verifier. FIP and OpenSBI binaries match the merged-main pins; OpenSBI's provenance manifest changes to record the merged build script.
- Shipped server contains the custom runtime hooks; 202 ChaCha20 and 1,439 SHA1 XTheadVector instructions match their source. The built web tree exactly matches the separately tested production build.
- The preceding preparation passed PR #71 CI and CodeQL. Checks for the final PR #72 integration are recorded in the host checkpoint and PR #71.

## Packages and image

- Seven core APKs total 38,418,408 bytes (38.4 MB). Their signatures and the legacy APKINDEX signature pass verification.
- The publication index retains optional RustDesk 0.5.3-r2. All eight packages verify; the combined Packages.adb verifies independently with the RSA and ECDSA public keys.
- Using APK 3.0.8 from the v2.5-a1 image and a copy of its installed database, `apk update` followed by simulated `apk upgrade` resolves all seven NanoKVM upgrades plus Alpine zlib 1.3.2-r1, without dependency conflicts. This is a solver check, not an on-device installation or execution of package triggers.
- Full-image ZIP: 70,147,388 bytes (70.1 MB). Uncompressed image: 872,415,744 bytes (832 MiB). MBR confirms a 64 MiB boot partition and a 768 MiB F2FS root partition.
- ZIP CRC passes and its extracted image hash equals the raw image. Image identity is v1.1-a1 / bundled applications v2.6-a1; rootfs application version is 2.6-a1. USB Internet helper/service and NanoKVM server are present.
- Public key directories contain no private keys. Temporary signing keys were removed from the package builder.
- The corresponding source archive is assembled from the final preparation commit, with a per-file SHA256 manifest. Final source/archive verification and publication checksums are recorded alongside the artifacts in the host release directory.

## Device status

The user authorized a complete Image v1.1-a1 installation with settings preserved and /data discarded. The pre-#72 image at a58ee643 was installed on the PCIe stand: the complete SD image passed read-back SHA256 verification before settings were restored. UART confirmed OpenSBI 1.9 and its split RX/RW PMP regions, automatic PCIe selection and QHD CMA boot. FIP, selected FIT and server hashes matched the artifacts. Thirteen selected configuration/credential checks passed. Wi-Fi, WireGuard, SSH and the existing Chrome Main web session returned. Signed RustDesk 0.5.3-r2 was restored with its identity/configuration. Current and two previous boot-log archives remain available.

Chrome Main displayed applications 2.6-a1 / Image v1.1-a1 and Direct H.265 2560x1440 at 57–60/60 FPS. The browser console showed no errors; OpenRC default services started. /data was recreated at sector 1705984. Root free space after the RustDesk install was approximately 560 MiB. USB gadget was subsequently disabled at the user's request.

PR #72 was reviewed at adc02fd18. Local `go test -race -tags teststub ./service/stream/...`, `go vet -tags teststub ./service/stream/...` and `git diff --check` passed. Its failed Web UI CI job was the existing contradictory ACK test already corrected in this release branch. The author separately qualified QHD SmartP and NormalP in Chrome on the stand; these results are not an independent retest of the combined release.

The final #72 rebuild changes the application, packages and image contents. The prior installation acceptance is not a claim that those final bytes were installed. Physical cold power cycling, exhaustive HDMI/browser transitions and Internet sharing with the managed PC were not tested.
