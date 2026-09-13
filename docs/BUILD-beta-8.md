# Building beta-8

This release is sequence 21. Its application, build recipes and native capture sources correspond to the integration commit identified in `firmware/release/beta8/build-manifest.json`. The kernel and 126 modules are reused from the accepted beta-8 kernel build: Linux 7.2.5-nanokvm-os-r2. The matching complete kernel patch and configuration are in `firmware/release/beta8/`; apply the complete patch once to pristine Linux 7.2.5 rather than combining it with individual kernel patches.

Use `scripts/build-enhanced.sh`, the checked-in Buildroot external tree and the enhanced native/application build scripts. Target C/C++ uses GCC 16.2.0 with -O2. OpenSSL is 4.0.2. `scripts/build-enhanced-capture.py` rebuilds libkvm; `scripts/build-enhanced-server.py` stages the matching native dependencies. The release stage uses `--version 1.0.0-beta.8 --update-sequence 21`. The required input directories are documented by each script and supplied by environment variables; a release build also requires the pinned board and native inputs, not just a standalone Go build.

Generate the full update payload with `scripts/build-full-update-images.py`, assemble the SD image with `scripts/assemble-enhanced-test-image.py`, and package the full update with `server/cmd/nkos-full-package`. The production signing private key is not distributed. Historical snapshots under `firmware/release/source-components` retain their original versions; use the beta-8 metadata and current build recipes for this release.

See [the release notes](RELEASE-beta-8.md) for the packaged changes and supported update path. Build and package checks passed; sequence 21 itself was not flashed or capture-tested during its assembly.
