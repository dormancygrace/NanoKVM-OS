# Building beta-13 sequence 29

This release adds automatic first-boot board selection and board-specific FITs.
The accepted kernel payload and all 92 modules remain 7.2.5-nanokvm-os-r3.
OpenSSL 4.0.2, -O2 userspace and accepted Web/capture assets are retained.
The server is rebuilt with the Lite/base ATX guard; the probe is a static -O2
RISC-V binary. Input identities are in firmware/release/beta13/build-manifest.json.

Use scripts/build-universal-boot.py to assemble the detection and four normal
profiles. Stage rootfs as root with firmware/release/beta13/stage-rootfs.sh,
then run build-artifacts.sh as the build user. Set NANOKVM_BUILD_BASE,
NANOKVM_RELEASE_OUTPUT and SOURCE_DATE_EPOCH as required by the scripts.
The accepted input directories are universal-boot-v2, universal-server-v1,
releases/beta10-seq23 and releases/beta11-seq27/web. The local release output
is releases/beta13-seq29-r2. These are staged input names, not downloads.

See [prerequisites](BUILD-beta-11.md), [board profiles](../firmware/boards/README.md),
[acceptance](BETA13-ACCEPTANCE.md) and [distribution status](DISTRIBUTION.md).
The repository does not include all external SDK/runtime/build prerequisites.
Outputs are the full image ZIP, SHA256SUMS and build manifest. No update
package is produced for this release.
