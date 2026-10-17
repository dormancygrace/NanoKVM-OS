# v3.2.0 / Image v1.4 validation

This record covers the published release based on main through PR #252.
It does not claim a release-candidate installation or SD-image boot on hardware.

## Source and build checks

- Full platform build completed; after #252, U-Boot and FIP were rebuilt and all **585 outputs** matched the committed hashes.
- Kernel, modules and U-Boot compiler-profile audits passed.
- All **54 CI script commands** passed. The raw-socket test subcase was unavailable; its parsing/fallback cases passed.
- Server build, vet and tests with the teststub/nomsgpack tags passed, plus focused storage/download/vm/network race tests.
- Decompression, PicoClaw, extension, update and APK-runner tests passed with `GOEXPERIMENT=cgocheck2` and the race detector.
- Web production build, lint and **219 tests** passed.
- APK 3.0.8 split-package migration tests passed: ownership transfer, protected settings, independent web updates and incompatible API rejection.
- Platform output validation and SD-image boot-layout tests were repeated after #252 and passed.

## Artifact validation

The twelve core packages passed ownership/version and signature checks. Both
repository indexes passed signature verification, including independent RSA
and ECDSA verification of the v3 index. ZIP contents, partition hashes and
geometry, FAT/F2FS checks and the embedded FIP identity all passed. All 2,433
source-archive file hashes were verified; all 20 GitHub asset SHA-256 digests
matched the local files. The dependency-solving dry run from the 3.1.0 package
database to 3.2.0 succeeded without running package scripts.

The release's `build-manifest.json` records these checks and source commit
`fb75b43b9ac30857076196dcfbe04adbef8194b9`; `SHA256SUMS` identifies the assets.
The image contains no private settings, SSH keys or optional add-ons.

## Hardware coverage and limits

- #251 tested corrected Linux boot FITs on the PCIe bench: SD reads were
  23.3–23.8 MB/s versus 8.0 MB/s. Wi-Fi association/traffic passed; no Wi-Fi
  throughput improvement was demonstrated. Lite, D80 and other cards need testing.
- #252 tested the new FIP through the ROM UART loader into RAM. U-Boot read
  boot.sd at about 23 MiB/s versus 2.9 MiB/s and verified/booted Linux.
  It was not written to the SD card, so a cold boot from SD with this FIP is
  still unverified. This bootloader update ships only in the full image.
- #220 and #223 include on-device compression-library tests. These are
  component-development results, not a complete upgrade to this release.
- The final twelve-package candidate and full Image v1.4 have not been installed
  on the test device during release preparation. Existing settings and the
  disabled USB gadget have not been changed.
