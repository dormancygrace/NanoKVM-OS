# Buildroot platform source patches

Apply the numbered patches in filename order to the Buildroot archive pinned
in `platform/sources.lock`. `platform/build.sh` does this before configuring
`nanokvm_platform_defconfig`.

- `0006-components-refresh.patch`: retained updates for the actual platform
  dependency graph (compiler/binutils support, host build tools and util-linux).
- `0008-meson-target-o2-only.patch`: preserve the selected target optimization.
- `0009-host-tools-refresh.patch`: CMake 4.4.4 and U-Boot tools
  2026.10; the latter uses the same checksummed Git tag archive as the bootloader.

The former rootfs package updates (OpenVPN, OpenSSL 4, Chrony, Nano, netfilter,
json-c and unrelated utilities) no longer apply here. The operating system uses
Alpine APK recipes; native MPI json-c is pinned separately in `firmware/sources.json`.
Host U-Boot tools in this profile use GnuTLS, not host OpenSSL; FIT signature
support is not selected. Do not infer system OpenSSL versions from Buildroot.

Changes invalidate the platform toolchain input fingerprint. Do not relabel
old output as rebuilt: a change here needs a complete platform build and output
qualification.

Buildroot 2026.08.1 already supplies Python 3.14.8 and the libtool 2.6
support patch. The local refresh retains its 2.6.2 adjustments by modifying
that patch and extending version matching, rather than adding it again.
