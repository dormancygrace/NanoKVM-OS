# Linux 7.2.6-nanokvm-os-r1 source export

This is the corresponding source of the kernel shipped in NanoKVM OS v2.0-a2 through v2.0-b7 (`7.2.6-nanokvm-os-r1`, the `Image` in every `boot.sd` of the `nanokvm-kernel-sg2002` package) and of its 62 in-tree modules.

`linux-nanokvm-os.patch` is a cumulative patch against the unmodified **Linux 7.2.6** archive from kernel.org (`linux-7.2.6.tar.xz`, SHA-256 `039aef84f2b0994aeda3f4fcfc3d02ec9d7a9bbb9020ea264c43f446c860f606`). It contains every NanoKVM change: the board device tree, ION/CMA, eFuse, HDMI reset, SG2002 Ethernet PHY, temperature, CPU frequency, clock, pinctrl, RTC, SD/SDIO, USB gadget (HID, mass storage/DVD, UAC1), scheduler and T-Head uaccess changes. Do not also apply `firmware/kernel/patches/`; they are already included. `kernel.config` is the release build configuration.

```sh
tar -xf linux-7.2.6.tar.xz
(cd linux-7.2.6 && git apply /path/to/NanoKVM-OS/firmware/release/source-components/kernel/linux-nanokvm-os.patch)
/path/to/NanoKVM-OS/firmware/release/source-components/kernel/build-kernel.sh \
  "$PWD/linux-7.2.6" "$PWD/kernel-output" \
  /path/to/buildroot-output/host/bin/riscv64-buildroot-linux-musl- 8
```

The output directory must not exist yet. The recipe uses the release configuration, the scalar ISA flags in `build-settings.json`, the fixed build user, host and timestamp, and a minimal `PATH` (host tools such as `rustc` would otherwise change `.config`). It then checks the result against `EXPECTED-SHA256SUMS`.

## Verification

On 2026-10-02 the patch was applied to the SHA-256-verified kernel.org archive. The resulting tree matched the release build tree byte for byte (`diff -r`, symlinks not followed). The only exceptions were four leftover patch backups (`*.orig`, `*.rej`) that the build does not use and that are not part of this export. A rebuild with the Buildroot 2026.08 toolchain (GCC 16.2.0, GNU Binutils 2.47.20260726) reproduced the release `Image`, `Module.symvers` and all 62 in-tree modules bit for bit. The `Image` is the one compressed into every v2.0-b7 `boot.sd`.

The board device trees in `boot.sd` are built from `firmware/boards/` with this kernel tree by `scripts/build-universal-boot.py`. Out-of-tree modules (media, Wi-Fi, CryptoDMA) are built separately; see [SOURCE.md](../../../../docs/SOURCE.md).
