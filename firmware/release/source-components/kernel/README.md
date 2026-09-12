# Linux 7.2.4 source export

`linux-nanokvm-os.patch` is a cumulative patch against the unmodified Linux **7.2.4** archive pinned in `firmware/sources.json`. It includes the selected board, ION/CMA, watchdog, USB and T-Head uaccess changes. Do not also apply `firmware/kernel/patches/` to this export.

```sh
tar -xf linux-7.2.4.tar.xz
cd linux-7.2.4
patch -p1 < /path/to/NanoKVM-OS/firmware/release/source-components/kernel/linux-nanokvm-os.patch
cd ..
/path/to/NanoKVM-OS/firmware/release/source-components/kernel/build-kernel.sh \
  "$PWD/linux-7.2.4" "$PWD/kernel-output" \
  /path/to/buildroot-output/host/bin/riscv64-buildroot-linux-musl- 8
```

The output directory must not already exist. The recipe uses the selected config, GCC 16.2.0 and scalar kernel ISA flags in `build-settings.json`, with a fixed build timestamp. Zacas/Zabha remain assembler support for runtime-gated kernel alternatives, not an assertion that C906 implements those extensions. `BUILD-SHA256SUMS` records the produced Image/DTB hashes; identical version labels alone do not guarantee bit-identical toolchains.

The cumulative patch was applied to the SHA-256-verified upstream archive and every changed file byte-compared with the selected prepared source (45 files). This validates the source export; it is not a new kernel build or device test. Out-of-tree media, Wi-Fi and CryptoDMA modules, firmware blobs and FIT/SD packaging are separate build steps described in [BUILD.md](../../../../docs/BUILD.md).

## Beta-4

The cumulative patch now reconstructs the selected Linux 7.2.5-nanokvm-os tree from the same upstream Linux 7.2.4 archive. It includes the official 7.2.4-to-7.2.5 changes, project drivers and DVD/UAC1/temperature/CPU-frequency changes. Patch application was checked and all 523 modified files compared byte-for-byte with the actual build source. Do not apply the incremental project patches a second time. `kernel.config` is the actual beta-4 build configuration.
