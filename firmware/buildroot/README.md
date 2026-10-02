# Buildroot's role in NanoKVM OS

The current OS is Alpine. This external tree supplies only the platform cross
toolchain, host utilities and the BusyBox/ext4/F2FS tools used by boot initramfs.
Use `platform/build.sh`; its sole profile is `configs/nanokvm_platform_defconfig`.

The retired Buildroot rootfs profiles, OpenVPN 3/Asio, private apk-tools,
nkos-addons and Superfile recipes are available in Git history, not supported
build entry points. Old beta build notes and release scripts describe their
original revisions and must not be run against the current tree.

Retained board scripts, firmware pins/blobs and EDID tool recipes still serve
native payload preparation and tests. Do not delete these just because the
root filesystem moved to Alpine. Runtime code lives under `firmware/alpine`,
`kvmapp` and `server`.

The platform profile preserves GCC 16.2, binutils 2.47, musl, the C906 ISA and
`-O2` choices. Its kernel headers remain pinned at 7.2.5 independently of the
7.2.6 kernel; this cleanup does not upgrade the ABI or packages.

Validate this boundary from a clean pinned Buildroot archive with
`python3 scripts/test-platform-config.py` (or pass `--archive /path/to/archive`).
This applies our patches and resolves Kconfig/the dependency graph without
building or flashing firmware. CI runs it alongside the platform script tests.
