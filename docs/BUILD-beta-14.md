# Building beta-14 sequence 30

Use the external prerequisites described in BUILD-beta-11.md. This repository
does not bundle the toolchain, kernel source, vendor SDK or prior staged rootfs.

Build Linux 7.2.5-nanokvm-os-r4 with scripts/build-enhanced-kernel.sh and the
checked-in configuration. Build matched modules using
scripts/build-enhanced-release-modules.py with explicit kernel source/output,
patched SDIO source, pinned RTL SDK and --include-sg2002-aes-probe to retain the
existing CryptoDMA feature. Build the five profiles with build-universal-boot.py
and its explicit --kernel-output argument. Do not reuse the beta-13 kernel.

Rebuild the board service, MMF, capture library, server, EDID utility/profiles and
web from this source using the corresponding scripts/build-enhanced-*.py,
build-server-existing-libs.py and nanokvm-board-tools recipe. Use the patched
Go runtime and the existing matched MPI libraries. The OpenSSL 4 base remains.

The release scripts under firmware/release/beta14 consume the staged inputs
under NANOKVM_BUILD_BASE/releases/beta14-seq30: kernel-output, board-stage,
boot, system, capture, server, web, edid and tools. The pristine beta-10 rootfs
provides the package/toolchain foundation; current components replace it.
Run stage-rootfs.sh as root, then build-artifacts.sh with NANOKVM_BUILD_BASE,
NANOKVM_RELEASE_OUTPUT and SOURCE_DATE_EPOCH. No update package is generated.

See BETA14-ACCEPTANCE.md for actual checks and pending hardware acceptance.
