# Building beta-9

This release is sequence 22. Its application, build recipes and native capture sources correspond to the integration commit identified in `firmware/release/beta9/build-manifest.json`. The kernel and 126 modules are reused from the accepted beta-8 kernel build: Linux 7.2.5-nanokvm-os-r2. The matching complete kernel patch and configuration are in `firmware/release/beta8/`; apply the complete patch once to pristine Linux 7.2.5 rather than combining it with individual kernel patches.

Use `scripts/build-enhanced.sh`, the checked-in Buildroot external tree and the enhanced native/application build scripts. Target C/C++ uses GCC 16.2.0 with -O2. OpenSSL is 4.0.2. `scripts/build-enhanced-capture.py` rebuilds libkvm; `scripts/build-enhanced-server.py` stages the matching native dependencies. The release stage uses `--version 1.0.0-beta.9 --update-sequence 22`. The required input directories are documented by each script and supplied by environment variables; a release build also requires the pinned board and native inputs, not just a standalone Go build.

Assemble the SD image with `scripts/assemble-enhanced-test-image.py` and compress it as the release ZIP. No full-system update package is built for beta-9. The full-package tools remain available for future compatible updates. The production signing private key is not distributed. Historical snapshots under `firmware/release/source-components` retain their original versions; use the beta-9 metadata and current build recipes for this release.

See [the release notes](RELEASE-beta-9.md) for the packaged changes and supported update path. Build checks passed. Sequence 22 was flashed and boot-qualified on the development device; see [acceptance](BETA9-ACCEPTANCE.md). Capture testing was not performed.

The rootfs is 1488 MiB and boot is 64 MiB, as specified in [SD layout v2](SD-LAYOUT-v2.md). This release distributes only the complete SD image; the full-package tooling documents the contract for future updates. The reused kernel patch/configuration remain under `firmware/release/beta8/`.
