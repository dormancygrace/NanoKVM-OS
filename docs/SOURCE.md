# Corresponding source for NanoKVM OS v2

This page lists where the source code of the copyleft components in the NanoKVM OS v2 SD image and APK packages can be found. It covers v2.0-a2 through v2.0-b7. All of them ship the same kernel and the same `fip.bin`.

## Kernel and boot loader

| Binary | License | Source | Verification |
|---|---|---|---|
| Linux `7.2.6-nanokvm-os-r1`: the `Image` in every `boot.sd` and the 62 in-tree modules | GPL-2.0 | `linux-7.2.6.tar.xz` from kernel.org and [source-components/kernel](../firmware/release/source-components/kernel/README.md) (cumulative patch, configuration, build recipe) | Rebuilt bit for bit: `Image`, `Module.symvers` and every in-tree module match the release build |
| Board device trees in `boot.sd` | GPL-2.0 OR MIT | `firmware/boards/` with the kernel tree above, built by `scripts/build-universal-boot.py` | Source only |
| U-Boot `2026.07-00003-ga5149dd2536c` (LOADER_2ND in `fip.bin`) | GPL-2.0+ | `u-boot-2026.07.tar.bz2` from ftp.denx.de, `firmware/uboot/` (patches, defconfig) and [source-components/uboot](../firmware/release/source-components/uboot/README.md) | Rebuilt bit for bit with GCC 16.2.0 and Binutils 2.45.1 |
| FSBL/BL2, BLCP, DDR parameters and OpenSBI 0.9 in `fip.bin` | BSD-3-Clause (FSBL), BSD-2-Clause (OpenSBI) | Unchanged from the stock Sipeed NanoKVM FIP (SHA-256 `4a40ec18…`), built from [sipeed/LicheeRV-Nano-Build](https://github.com/sipeed/LicheeRV-Nano-Build) at `1daa602f`, directories `fsbl/` and `opensbi/` | The 2026-09-05 boot audit found the stock FIP identical to a local build of that SDK |

`firmware/sources.json` records the upstream archives and commits with their SHA-256 digests.

## Source archive for each release

`scripts/build-gpl-source-bundle.sh <output-dir>` writes `NanoKVM-OS-<version>-gpl-source.tar`. The archive contains:

- the kernel.org and U-Boot upstream archives;
- the project patches, configurations and build recipes listed above;
- the board device trees and FIP packaging scripts;
- the source commit and a `SHA256SUMS` file.

Attach it to the GitHub release next to the image, so the source is offered from the same place as the binaries.

## Other components

- **Out-of-tree kernel modules**: 17 modules in `/lib/modules/7.2.6-nanokvm-os-r1/extra`. They cover the SOPHGO media drivers, AIC8800 and RTL8733BS Wi-Fi, cryptodev and the SG2002 CryptoDMA driver. The license is GPL-2.0. Upstream pins are in `firmware/sources.json`, and project changes are in `firmware/osdrv/`, `firmware/wifi/` and `firmware/crypto/`. A verified export like the kernel one above has not been added yet.
- **The initramfs in `boot.sd`**: BusyBox 1.38.0, e2fsprogs 1.47.4, f2fs-tools 1.16.0, util-linux libraries and musl. These are Buildroot 2026.08 builds. `firmware/release/licenses/manifest.csv` lists these Buildroot packages and their upstream archives, except f2fs-tools.
- **Alpine Linux 3.24 packages in the root filesystem**: Alpine publishes their build recipes ([aports](https://gitlab.alpinelinux.org/alpine/aports)) and source archives. The installed versions are recorded in `/lib/apk/db/installed` of each image.
- **The NanoKVM application, web interface and packages**: GPL-3.0, this repository.

## Written offer

For three years after we last distribute a NanoKVM OS v2 release, we will give anyone, on request, a complete machine-readable copy of the corresponding source of the GPL-licensed components in that release. We charge no more than the cost of physically performing the distribution. To request it, open an issue in this repository and name the release.
