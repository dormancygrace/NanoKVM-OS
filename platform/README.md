# Building NanoKVM OS

`platform/build.sh` builds the complete NanoKVM OS SD card image from this repository with one command:
- the toolchain, the Linux kernel and all kernel modules, U-Boot, OpenSBI, `fip.bin`, the initramfs and the `boot.sd` images;
- the SOPHGO media libraries, the board service `kvm_system`, the server, the update helpers and the web UI;
- the board tools, the EDID profiles and the firmware files;
- the seven `nanokvm-*` APK packages, the Alpine 3.24 root file system and the SD card image.

Every upstream input is pinned in `sources.lock` and checked when it is downloaded. The outputs of the build steps are checked against `expected.sha256`. The OpenSBI/FIP candidate passed host checks and ROM UART RAM boot on SG2002 with Linux 7.2.9, timer/PLIC, fixed PMU counters and live video. Persistent installation and candidate SD cold boot remain untested.

| Output | Shipped in |
|---|---|
| `images/Image` (Linux `7.2.9-nanokvm-os-r1`) | every `boot.sd` |
| `images/lib/modules/7.2.9-nanokvm-os-r1/` (65 in-tree and 17 out-of-tree modules, `modules.*`) | `nanokvm-kmod-sg2002` |
| `images/boot/*.sd`, `*.sha256`, `kernel.release` (5 boards × 5 video memory modes: FHD as `NAME.dtb`, then `NAME-fhd-fixed`, `NAME-qhd`, `NAME-qhd-fixed`, `NAME-uhd`) | `nanokvm-kernel-sg2002`, `/usr/lib/nanokvm/boot` |
| `images/dtb/*.dtb`, `images/initramfs.cpio.zst` | inside the `boot.sd` images |
| `images/u-boot.bin`, `images/fip.bin` | boot partition of the SD image |
| `images/opensbi/*`, `images/fip-manifest.json` | host build provenance and loader checks |
| `images/native/*.so` (19 libraries) | `nanokvm-app`, `/kvmapp/server/dl_lib` |
| `images/system/kvm_system` | `nanokvm-app`, `/kvmapp/kvm_system` |
| `images/server/NanoKVM-Server`, `nkos-update`, `nkos-apply-updates`, `nkos-usb-internet` | `nanokvm-app`, `nanokvm-base` |
| `images/web/` | `nanokvm-app`, `/kvmapp/server/web` |
| `images/tools/` (devmem, `nanokvm_update_edid`, `nkos-board-probe`, `usb-audio-capture`, EDID profiles) | `nanokvm-base`, `nanokvm-app`, `nanokvm-firmware-sg2002` |
| `images/firmware/` (AIC8800 Wi-Fi, regulatory database, video codec) | `nanokvm-firmware-sg2002` |
| `release/apk/` | the signed APK repository of the seven packages: `recipes/riscv64/` with the packages, `APKINDEX.tar.gz` and `Packages.adb`, and the public keys |
| `release/alpine-rootfs.tar.gz`, `release/installed-packages.txt` | the root file system and its package versions |
| `release/image/NanoKVM-OS-Image-<image-version>-apps-<application-version>.img.zip`, `SHA256SUMS` | the SD card image |

## Image and application versions

Starting with applications **v2.5-a1**, full SD images have their own version.
The current **Image v1.1-a1** includes applications **v2.6-a1**; both are alpha releases.
`firmware/alpine/release.env` is the source of truth: `NANOKVM_IMAGE_VERSION`
identifies the image, `NANOKVM_VERSION` the applications, and
`NANOKVM_APK_VERSION` their APK-comparable version (`2.6_alpha1`).

The image filename and `build-manifest.json` record both versions. The image
builder also checks `/kvmapp/version` against the requested bundle version.
`/boot/image.json` records the original image and its bundled applications;
this file is not owned by an APK and is not rewritten by component updates.
About shows the installed image, its original applications, and the current
application version separately. Existing installations retain their original
`/boot/ver` identity; an APK update does not label them as a newly flashed image.

## Build

On a Linux x86-64 host (tested on Ubuntu 24.04) with:
- the [Buildroot requirements](https://buildroot.org/downloads/manual/manual.html#requirement), plus `git` 2.27 or newer, `curl`, `openssl`, `libssl-dev`, `flex` and `bison`;
- for the packages, rootfs and image steps: Linux 6.7 or newer, `newuidmap` and `newgidmap` (package `uidmap`), and subordinate IDs for the build user in `/etc/subuid` and `/etc/subgid` (Ubuntu adds them for every user).

```sh
platform/build.sh -d
```

This creates a local test image signed with generated test keys. For official
release signing, supply both keys as described [below](#signing-key).

The build needs no root. The packages, rootfs and image steps run as root of a user namespace: it maps the subordinate IDs, so that the root file system gets its real owners, and starts riscv64 programs through qemu in the namespace's own `binfmt_misc`. Files in `OUTPUT/apk-builder` and `OUTPUT/rootfs` then belong to subordinate IDs; `build.sh clean` deletes them.

The first run downloads about 2 GB and needs about 25 GB of disk space. Building the toolchain takes most of the first run (20 minutes on 16 threads); the other steps take about 10 minutes. Later runs reuse the toolchain while its inputs are unchanged: the Buildroot pin, `firmware/buildroot` and the toolchain recipe in `build.sh`. If one of them changes, `build.sh` refuses the old toolchain and names the directories to delete. Steps can be run on their own, for example `platform/build.sh kernel modules verify`. The step order is in `build.sh --help`. Use `-o DIR` for another output directory and `-j N` for the number of jobs.

Go modules, npm packages and Alpine packages are downloaded during the build and checked against `server/go.sum`, `web/pnpm-lock.yaml` and the signed Alpine indexes. Alpine updates its 3.24 packages with security fixes, so a later build installs their newer versions; `release/installed-packages.txt` lists the versions in the image.

### Signing key

The APK packages and indexes are signed, and the packages and rootfs steps need keys. Release builds pass both maintainer keys: `-k path/to/dgrace-6aaddbb6.rsa` (RSA, with its `.rsa.pub` next to it) and `-e path/to/nkos-release-ec-b8e89b66.key` (ECDSA P-256, with `nkos-release-ec-b8e89b66.pub` next to it). Both public keys must be in `firmware/alpine/keys`, or `build.sh` stops before the first step. Local test builds pass `-d` instead: the first run creates the test keys `OUTPUT/keys/nanokvm-test-*.rsa` and `nanokvm-test-ec-*.key`, and the image trusts them, so never publish those packages or images. apk does not tie a key to a repository: a device trusts every key in `/etc/apk/keys` for every repository it uses.

The packages step signs the packages and the v2 index `APKINDEX.tar.gz` with the RSA key (RSA256: RSA with SHA-256), and writes the apk-tools v3 index `Packages.adb` next to it, signed with the ECDSA key and the RSA key; it checks that each key alone verifies `Packages.adb`. Devices up to 2.0 read the v2 index; images from 2.5 on read `https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb`.

The package `nanokvm-keys` installs the public keys of `firmware/alpine/keys` (with `-d`, also the test keys) in `/etc/apk/keys` and owns them, so a later release can add or remove a key; on an older device it also moves the NanoKVM repository line to the v3 index. The rootfs step copies the keys into the root before it installs `nanokvm-release`, and apk takes over the identical files; the step fails if a key in `/etc/apk/keys` has no owner. Publishing a release, moving to the ECDSA key and replacing a key are described in [firmware/alpine/README.md](../firmware/alpine/README.md#signing-keys).

The root file system uses official Alpine packages (`BUILD_PROFILE="stock"`). Releases up to v2.0-b7 replaced busybox, coreutils, openssl, lz4 and zstd with C906-tuned builds (`c906-scalar`); that overlay is not built here.

## What is here

| Path | Contents |
|---|---|
| `opensbi/` | upstream generic SG2002 configuration, M-mode DT, FDT handoff and C906 draft VS compatibility patches |
| `sources.lock` | Every upstream input: archives by SHA-256, Git trees by commit and tree id |
| `build.sh` | All build steps |
| `expected.sha256` | Hash of every output in `images/`; `build.sh verify` fails if an output is missing, differs or is not listed |
| `packages.list` | The contents of the seven APK packages: each file, its mode and where it comes from |
| `kernel/` | `config` and patches for kernel.org Linux 7.2.9 |
| `modules/<name>/` | Patches for the SOPHGO media drivers (`osdrv`), AIC8800 and RTL8733BS Wi-Fi and cryptodev; `sg2002-aes/` is the CryptoDMA driver source |
| `uboot/` | `defconfig` and patches for U-Boot 2026.07 |
| `fip/base-fip.bin` | First-stage boot firmware; the build replaces OpenSBI and U-Boot while retaining the vendor first-stage loader |
| `boot/` | FIT template, initramfs file list and `init`; `stock-init` is the stock Sipeed initramfs init, and `stock-init.diff` turns it into `init` |
| `native/cvi_mpi/` | Patches for the SOPHGO media libraries (`cvi_mpi`) |
| `native/maixcdk/` | Patches for the MaixCDK sources of `kvm_system` |

The build also uses these parts of the repository:
- `firmware/buildroot/`: platform-only source patches and `configs/nanokvm_platform_defconfig` for the toolchain, host tools and initramfs userland;
- `firmware/boards/`: the board device trees and the board probe;
- `firmware/sensor/patches/`: NanoKVM I2C/MIPI wiring and LT6911UXC/LT6911D identification; applied before compiling MMF;
- `firmware/mpi/`: the MMF capture-size helper, compatibility headers and the hashes of the SOPHGO ISP objects (`vendor-isp-objects.json`);
- `firmware/alpine/`: the APK recipes, OpenRC services and compatibility scripts, `release.env` with the version;
- `server/`, `web/`, `support/sg2002/`, `native/usb-audio/`, `tools/nanokvm_update_edid/`, `kvmapp/`: the application;
- `scripts/`: the component builders that `build.sh` calls.

## Changing a component

Each component is an upstream tree from `sources.lock` with the patches of its directory applied in name order. To change one:

1. Run its step once, for example `build.sh kernel`.
2. Edit the prepared tree, for example `build/platform/kernel/src`. The build records the unchanged tree in `build/platform/kernel/src.git`.
3. Save the change as the next numbered patch in the component directory and run the step again:

   ```sh
   export GIT_DIR=build/platform/kernel/src.git GIT_WORK_TREE=build/platform/kernel/src
   git add -A && git diff --cached > platform/kernel/0002-my-change.patch
   unset GIT_DIR GIT_WORK_TREE
   ```
4. Run `build.sh verify`. It names every output that changed. Update `expected.sha256` only for the outputs you meant to change, move them to the section of new binaries, and say so in the commit.

A changed kernel also needs a new release name: update `CONFIG_LOCALVERSION` in `kernel/config` and `release` in `build.sh`. `build.sh` sets the fixed build user, host and timestamps, so that two builds of the same tree are identical.

## Verification

`build.sh verify` compares every output in `images/` with `expected.sha256` and rejects missing, changed or unlisted files. The release uses GCC 16.2 / Binutils 2.47 and the component profiles in `cpu-profile.json`: kernel and modules use `-O3` without LTO, while source-owned userspace uses scalar `-O2`.

Kernel, device trees and modules change with the reviewed C906 vector-context, video-clock and memory-profile updates. The AIC8800 driver includes the cfg80211 compatibility patch. U-Boot, the initramfs, FIT images, native media libraries, system service and native tools are built from the pinned inputs. Closed SOPHGO ISP/3A objects remain pinned binary inputs: relinking does not recompile those objects. Go uses its own compiler; the profile applies to its C/C++ interoperability code.

Audit the effective compiler commands as well as the supplied flags:

```sh
python3 scripts/audit-kbuild-profile.py build/platform/kernel/build
python3 scripts/audit-kbuild-profile.py build/platform/modules
python3 scripts/audit-kbuild-profile.py build/platform/uboot/build --kind bootloader
```

The audit checks the last effective optimization, ISA and ABI options. Kernel vDSO objects retain Kbuild's explicit portable/CFI ISA choices and are reported separately; they retain the kernel optimization level and C906 tuning. Kernel and bootloader builds do not enable floating-point or automatic vector code generation.

APK signatures depend on the signing key; the root filesystem also depends on the current official Alpine 3.24 package versions. They are listed in `release/installed-packages.txt`. Existing installations with the optional C906 overlay require the documented stock-package migration; installing a NanoKVM application update alone does not replace every Alpine package.

A matching hash verifies the expected build outputs, not hardware operation. Boot-test new U-Boot/FIT images, media capture and encoding, and Wi-Fi on a device before publishing. A passing build does not establish compatibility with hardware variants that were not available for testing.

## Corresponding source

The image contains GPL-licensed code:
- Linux and its modules, U-Boot;
- BusyBox, e2fsprogs, util-linux and f2fs-tools in the initramfs, and the BusyBox `devmem` applet;
- the NanoKVM application (GPL-3.0);
- Alpine packages.

`platform/build.sh source` writes `build/platform/NanoKVM-OS-<version>-source.tar.xz` after a build. Run it on a clean checkout. The archive contains:
- the GPL-licensed inputs from `sources.lock`: Buildroot, Linux, U-Boot, `osdrv`, the AIC8800 package, cryptodev, the Sipeed SDK paths and BusyBox, plus the BSD-licensed upstream OpenSBI source;
- the Buildroot downloads of the initramfs packages;
- this repository at the release commit, the source commit and `SHA256SUMS`.

The other inputs of `sources.lock` are not redistributed; see [docs/DISTRIBUTION.md](../docs/DISTRIBUTION.md). Attach the archive to the GitHub release next to the image. The v2.0-b7 archive (`NanoKVM-OS-v2.0-b7-platform-source.tar.xz`) covers the kernel, modules and boot images only; it also contains util-linux 2.41.5 and `B7-INITRAMFS.txt`, because that initramfs was assembled before this build existed.

The reference `fip/base-fip.bin` contains stock FSBL/BL2 (BSD-3-Clause), OpenSBI 0.9 (BSD-2-Clause), DDR parameters and small-core fields. The output `images/fip.bin` preserves FSBL/BL2, BLCP, DDR parameters and BLCP_2ND bytes and replaces MONITOR with pinned upstream OpenSBI 1.9 and LOADER_2ND with U-Boot. Vendor reference source is in [sipeed/LicheeRV-Nano-Build](https://github.com/sipeed/LicheeRV-Nano-Build) at the `sipeed-sdk` commit in `sources.lock` (`fsbl/`, `opensbi/`). OpenSBI source is separately pinned and included by the source step. See [the OpenSBI port and recovery procedure](opensbi/README.md).

Alpine publishes the build recipes ([aports](https://gitlab.alpinelinux.org/alpine/aports)) and source archives of its packages. The installed versions are listed in `/lib/apk/db/installed` of each image and in `release/installed-packages.txt`.

### Written offer

*Draft: the maintainer has not approved this wording yet.*

For three years after we last distribute a NanoKVM OS v2 release, we will give anyone, on request, a complete machine-readable copy of the corresponding source of the GPL-licensed components in that release, for no more than the cost of physically performing the distribution. To request it, open an issue in this repository and name the release.

## Buildroot scope

The platform profile builds no installed OS rootfs and includes no private addon
manager. Alpine owns system packages and optional software. BusyBox has a broad
conditional dependency list: selecting unrelated packages here can rebuild them
even when only the `busybox` target is requested. Keep this profile limited to
the actual platform dependency graph.

Recipe cleanup changes the toolchain input fingerprint. Existing output must not
be reused under a new fingerprint; the next full platform build must still pass
`verify` against `expected.sha256`. A dependency/configuration check alone does
not qualify newly built boot images.

## Component optimization profiles

Kernel and modules use GCC `-O3` without LTO; source-owned userspace
uses scalar `-O2`. USB audio explicitly selects float Opus with the
`audio` profile. Handwritten vendor RVV/assembly remains available.
See [component profiles](../docs/component-build-profiles.md) for the optional
RustDesk performance build, measurement limits and release checksum policy.
