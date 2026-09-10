# RTL8733BS for NanoKVM OS

Pinned source and a Linux7.2.4-only compatibility patch for the SDIO Realtek variant. The driver is GPL-2.0; existing notices are retained. Legacy wire layouts come from Linux5.10 GPL headers. It is not a generic out-of-tree driver supporting every kernel version.

Build without installing:

```sh
python3 scripts/build-rtl8733bs.py \
  --sdk /path/to/LicheeRV-Nano-Build \
  --kernel-source /path/to/patched-linux \
  --kernel-output /path/to/matched-kernel-build \
  --buildroot-output /path/to/enhanced-buildroot \
  --output /path/to/new-rtl-build
```

Use a fresh output path. The SDK repository must contain the pinned commit in source.json; the script archives that commit rather than trusting the checkout. Kernel output must match7.2.4-nanokvm-enhanced; compiler must be GCC16.2. Module flags come from the shared kernel T-Head profile, without automatic FP/vector. The vendor's default optimization level remains unchanged.

The full build-enhanced-release-modules.py additionally requires --rtl8733bs-sdk. stage-enhanced-board.py --beta additionally requires --rtl8733bs-module. These inputs prevent silently shipping an AIC-only beta module set.

[Build and limited target qualification](../../../docs/VALIDATION.md). Realtek association/reconnect/endurance and new-module coldboot qualification remain open. The AIC BSP wildcard has since been corrected; see the [follow-up](../../../docs/VALIDATION.md). Successful initialization on an AIC board is not proof of functioning Realtek Wi-Fi.

Build timestamps use the pinned SDK commit epoch, so `__DATE__`/`__TIME__` do not
change the module on each build. [Integrated bundle and reproducibility evidence](../../../docs/VALIDATION.md).
