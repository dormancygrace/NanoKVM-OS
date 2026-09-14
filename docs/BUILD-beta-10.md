# Building beta-10

Beta-10 is version `1.0.0-beta.10`, sequence 23. Exact source and artifact hashes
are recorded in `firmware/release/beta10/build-manifest.json`. The public commit
contains the curated application and firmware sources from that integration.

The release retains the refreshed beta-9 userspace, OpenSSL 4.0.2, bootloader and
normal initramfs. Target C/C++ uses GCC 16.2.0 and `-O2`. Use the project's patched
Go runtime and pinned native/toolchain inputs; a standalone Go build is insufficient.

Build Linux 7.2.5 with the complete patch under
`firmware/release/beta8/linux-7.2.5-nanokvm-os-r2.patch` and the new configuration
`firmware/release/beta10/kernel.config`, which selects `7.2.5-nanokvm-os-r3`.
Apply the complete kernel patch once, without also applying individual kernel
patches. Rebuild the external drivers, including media patches 0021–0024, the
pinned AIC and Realtek sources and CryptoDMA. The final board stage has 92 modules.
Use the enhanced kernel/module build scripts and `stage-enhanced-board.py --beta`.

Build native capture with `scripts/build-enhanced-capture.py` and the application
with `scripts/build-enhanced-server.py`. Retain the exact beta-9 `nkos-update`
helper at capability 2, sequence 0; only NanoKVM-Server needs the new discovery
logic. Rebuild the web application with its frozen lockfile before running the
production-bundle tests. Include the four portrait EDIDs and matched native libraries.

The release assembly starts from the published beta-9 rootfs. Mount the extracted
root partition under OUTPUT/rootfs-mount; supply board-stage and web under OUTPUT.
Apply `firmware/release/beta10/prepare-rootfs.py --repo REPO --output OUTPUT
--accepted ACCEPTED_PORTRAIT_BUNDLE --server FINAL_SERVER_BINARY`, then unmount it.
The script retains fresh-image defaults and installs the r3 modules, D80 firmware,
FQ-CoDel policy, application and system metadata. Recovered normal initramfs file
lists can be generated with `initramfs-list-recovery.py` in the same directory.

Generate the normal FIT with `scripts/build-enhanced-fit.py`, the complete image
with `scripts/assemble-enhanced-test-image.py`, and the full update payload with
`scripts/build-full-update-images.py`. Sign using `server/cmd/nkos-full-package`;
the production private key is not distributed. Select names using
`scripts/release_names.py 1.0.0-beta.10`, ZIP the image with the same inner basename,
and generate `SHA256SUMS` for the ZIP and signed package.

The layout is 64 MiB boot plus 1488 MiB system; see [layout](SD-LAYOUT-v2.md).
Host checks and device acceptance are recorded in [acceptance](BETA10-ACCEPTANCE.md).
