# Building NanoKVM OS v2.0 b1

This source release uses Alpine 3.24, Linux 7.2.6-nanokvm-os-r1 and the existing
NanoKVM native runtime. The external toolchain, kernel source, vendor SDK,
matched media libraries, FIP and accepted initramfs are required inputs; they
are not bundled in this repository. See firmware/alpine/README.md,
firmware/alpine/packages/README.md and scripts/README.md.

Build the kernel and matching modules with build-enhanced-kernel.sh and
build-enhanced-release-modules.py. Build all board FITs with build-universal-boot.py
using the matching kernel output and F2FS-capable initramfs. Retain the accepted
SG2002/AIC patches and the matched native media libraries.

Build the server with build-server-existing-libs.py and the prepared NanoKVM
Go runtime, and build the web directory with its pinned dependencies. Assemble
payloads using prepare-alpine-release-payloads.py and produce signed APKs with
build-alpine-packages.sh. Supply your own signing key and install its public key
in the image; private signing keys are not part of this source release.

The accepted component revisions are app 2.0_beta1-r2, base 2.0_beta1-r0,
kernel/modules 2.0_alpha2-r0 and release 2.0_beta1-r0. Build a clean Alpine
root bundle through build-alpine-personal-image.sh / the configured builder,
then use build-alpine-sd-image.py with explicit --rootfs-archive, --fip,
--f2fs-tools and --output arguments. The output is the full image ZIP plus
SHA256SUMS. See the release notes for installation and hardware scope.
