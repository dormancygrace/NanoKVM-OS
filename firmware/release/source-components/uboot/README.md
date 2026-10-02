# U-Boot 2026.07-00003-ga5149dd2536c source export

This is the corresponding source of the U-Boot in `fip.bin` of NanoKVM OS v2.0-a2 through v2.0-b7 and beta-11 through beta-14 (FIP SHA-256 `8254d38124e66877ab6c4a894f656d8ee6dc299d813dd06f31037ddaa2e51b9d`, LOADER_2ND `u-boot.bin` SHA-256 `4a3f900306704624c69202d5d539f159ba1a3cdb2b2ec6c9d54ae62452a0548c`).

- Upstream: `u-boot-2026.07.tar.bz2` from <https://ftp.denx.de/pub/u-boot/>, SHA-256 `78e8bfc382fe388f9b55aa1daf8c563522a037779b5d4c349d1415e381f1243e` (tag v2026.07).
- Patches: `firmware/uboot/patches/0001` to `0003`, applied in order. The third patch is commit `a5149dd2536c`, the release's Git description.
- Configuration: `firmware/uboot/nanokvm_enhanced_defconfig`.

```sh
tar -xjf u-boot-2026.07.tar.bz2
for p in /path/to/NanoKVM-OS/firmware/uboot/patches/*.patch; do
  patch -d u-boot-2026.07 -p1 < "$p"
done
/path/to/NanoKVM-OS/firmware/release/source-components/uboot/build-uboot.sh \
  "$PWD/u-boot-2026.07" "$PWD/uboot-output" \
  /path/to/buildroot-output/host/bin/riscv64-buildroot-linux-musl- 8
```

The release was built from a Git checkout without `SOURCE_DATE_EPOCH`, so its version string contains the Git description and the build time `Sep 07 2026 - 02:24:07 +0300`. The recipe reproduces both with `LOCALVERSION` and a pinned clock.

## Verification

On 2026-10-02 the tarball, the three patches and the defconfig were built with Buildroot 2026.08 GCC 16.2.0 and GNU Binutils 2.45.1, the binutils version recorded in the release binary. The resulting `u-boot.bin` was bit-identical to the U-Boot decompressed from the v2.0-b7 FIP. With the current Buildroot binutils 2.47.20260726 the same source builds a different binary (not device-tested), so only the 2.45.1 build matches `EXPECTED-SHA256SUMS`.
