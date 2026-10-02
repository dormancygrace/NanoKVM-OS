# Building NanoKVM OS

`platform/build.sh` builds the complete NanoKVM OS SD card image from this repository with one command:
- the toolchain, the Linux kernel and all kernel modules, U-Boot, `fip.bin`, the initramfs and the `boot.sd` images;
- the SOPHGO media libraries, the board service `kvm_system`, the server, the update helpers and the web UI;
- the board tools, the EDID profiles and the firmware files;
- the six `nanokvm-*` APK packages, the Alpine 3.24 root file system and the SD card image.

Every upstream input is pinned in `sources.lock` and checked when it is downloaded. The outputs of the build steps are checked against `expected.sha256`.

| Output | Shipped in |
|---|---|
| `images/Image` (Linux `7.2.6-nanokvm-os-r1`) | every `boot.sd` |
| `images/lib/modules/7.2.6-nanokvm-os-r1/` (62 in-tree and 17 out-of-tree modules, `modules.*`) | `nanokvm-kmod-sg2002` |
| `images/boot/*.sd`, `*.sha256`, `kernel.release` (5 boards × CMA/fixed video memory) | `nanokvm-kernel-sg2002`, `/usr/lib/nanokvm/boot` |
| `images/dtb/*.dtb`, `images/initramfs.cpio.zst` | inside the `boot.sd` images |
| `images/u-boot.bin`, `images/fip.bin` | boot partition of the SD image |
| `images/native/*.so` (19 libraries) | `nanokvm-app`, `/kvmapp/server/dl_lib` |
| `images/system/kvm_system` | `nanokvm-app`, `/kvmapp/kvm_system` |
| `images/server/NanoKVM-Server`, `nkos-update`, `nkos-apply-updates` | `nanokvm-app`, `nanokvm-base` |
| `images/web/` | `nanokvm-app`, `/kvmapp/server/web` |
| `images/tools/` (devmem, `nanokvm_update_edid`, `nkos-board-probe`, `usb-audio-capture`, EDID profiles) | `nanokvm-base`, `nanokvm-app`, `nanokvm-firmware-sg2002` |
| `images/firmware/` (AIC8800 Wi-Fi, regulatory database, video codec) | `nanokvm-firmware-sg2002` |
| `release/apk/` | the signed APK repository of the six packages |
| `release/alpine-rootfs.tar.gz`, `release/installed-packages.txt` | the root file system and its package versions |
| `release/image/NanoKVM-OS-<version>.img.zip`, `SHA256SUMS` | the SD card image |

## Build

On a Linux x86-64 host (tested on Ubuntu 24.04) with:
- the [Buildroot requirements](https://buildroot.org/downloads/manual/manual.html#requirement), plus `git` 2.27 or newer, `curl`, `openssl`, `libssl-dev`, `flex` and `bison`;
- for the packages, rootfs and image steps: Linux 6.7 or newer, `newuidmap` and `newgidmap` (package `uidmap`), and subordinate IDs for the build user in `/etc/subuid` and `/etc/subgid` (Ubuntu adds them for every user).

```sh
platform/build.sh
```

The build needs no root. The packages, rootfs and image steps run as root of a user namespace: it maps the subordinate IDs, so that the root file system gets its real owners, and starts riscv64 programs through qemu in the namespace's own `binfmt_misc`. Files in `OUTPUT/apk-builder` and `OUTPUT/rootfs` then belong to subordinate IDs; `build.sh clean` deletes them.

The first run downloads about 2 GB and needs about 25 GB of disk space. Building the toolchain takes most of the first run (20 minutes on 16 threads); the other steps take about 10 minutes. Later runs reuse the toolchain while its inputs are unchanged: the Buildroot pin, `firmware/buildroot` and the toolchain recipe in `build.sh`. If one of them changes, `build.sh` refuses the old toolchain and names the directories to delete. Steps can be run on their own, for example `platform/build.sh kernel modules verify`. The step order is in `build.sh --help`. Use `-o DIR` for another output directory and `-j N` for the number of jobs.

Go modules, npm packages and Alpine packages are downloaded during the build and checked against `server/go.sum`, `web/pnpm-lock.yaml` and the signed Alpine indexes. Alpine updates its 3.24 packages with security fixes, so a later build installs their newer versions; `release/installed-packages.txt` lists the versions in the image.

### Signing key

The APK packages are signed. Without `-k`, the first run creates a key in `OUTPUT/keys` and the image trusts it. Release builds pass the maintainer key: `-k path/to/dgrace-6aaddbb6.rsa`, with its `.rsa.pub` next to it. Every image also trusts the public key of NanoKVM OS releases in `firmware/alpine/keys` and uses the release repository `https://nkos.pesin.pro/repos/nanokvm`, so an image built from this repository updates to later releases.

The root file system uses official Alpine packages (`BUILD_PROFILE="stock"`). Releases up to v2.0-b7 replaced busybox, coreutils, openssl, lz4 and zstd with C906-tuned builds (`c906-scalar`); that overlay is not built here.

## What is here

| Path | Contents |
|---|---|
| `sources.lock` | Every upstream input: archives by SHA-256, Git trees by commit and tree id |
| `build.sh` | All build steps |
| `expected.sha256` | Hash of every output in `images/`; `build.sh verify` fails if an output is missing, differs or is not listed |
| `packages.list` | The contents of the six APK packages: each file, its mode and where it comes from |
| `kernel/` | `config` and patches for kernel.org Linux 7.2.6 |
| `modules/<name>/` | Patches for the SOPHGO media drivers (`osdrv`), AIC8800 and RTL8733BS Wi-Fi and cryptodev; `sg2002-aes/` is the CryptoDMA driver source |
| `uboot/` | `defconfig` and patches for U-Boot 2026.07 |
| `fip/base-fip.bin` | First-stage boot firmware; the build replaces only U-Boot in it |
| `boot/` | FIT template, initramfs file list and `init`; `stock-init` is the stock Sipeed initramfs init, and `stock-init.diff` turns it into `init` |
| `native/cvi_mpi/` | Patches for the SOPHGO media libraries (`cvi_mpi`) |
| `native/maixcdk/` | Patches for the MaixCDK sources of `kvm_system` |

The build also uses these parts of the repository:
- `firmware/buildroot/`: platform-only source patches and `configs/nanokvm_platform_defconfig` for the toolchain, host tools and initramfs userland;
- `firmware/boards/`: the board device trees and the board probe;
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

`build.sh verify` compares every output in `images/` with `expected.sha256`. The file has these sections:

1. **Platform, identical to NanoKVM OS v2.0-a2 through v2.0-b7** (99 outputs):
   - the kernel `Image` and `boot/kernel.release`;
   - 78 of the 79 modules, and the `modules.*` files that do not depend on module order;
   - the 10 device trees of the released `boot.sd` images.
2. **Platform, changed after v2.0-b7:** `aic8800_fdrv.ko`. `modules/aic8800/0002` adds the Linux 7.3 cfg80211 fix after the release.
3. **Platform, new binaries not yet in a release:**
   - `u-boot.bin` and `fip.bin`: b7's U-Boot was built from exactly these patches and defconfig with GNU Binutils 2.45.1; a rebuild reproduced it bit for bit. The single Buildroot toolchain (Binutils 2.47) produces a different binary.
   - the initramfs, the `boot.sd` images and their `.sha256` files: the initramfs is built from the Buildroot packages and compressed with the pinned zstd.
   - `modules.alias`, `modules.dep`, their `.bin` forms, `modules.symbols.bin` and `modules.weakdep`: the same entries as b7, with the modules in sorted order. `nanokvm-activate-kernel` runs `depmod` again on the device.
4. **Application and firmware, identical to v2.0-b7:** five of the media libraries (the relinked SOPHGO ISP and 3A objects and `libkvm.so`), `kvm_system`, `nkos-update`, `nkos-apply-updates`, `devmem`, the EDID profiles, the USB audio notices, the web UI files that later commits did not change, the codec firmware, the regulatory database and 49 AIC8800 firmware files.
5. **Rebuilt from the v2.0-b7 sources with this toolchain:** 14 media libraries, `nanokvm_update_edid`, `nkos-board-probe` and `usb-audio-capture`. The v2.0-b7 binaries came from earlier builds whose toolchain and flags were not recorded.
6. **Changed after v2.0-b7:** the server and web UI (later commits), and the AIC8800 firmware, which now comes from the Radxa package of the driver (23 files in a newer version, the AIC8800D80N directory new).

Built from the v2.0-b7 commit (`02e8fd1`), the server, both update helpers, all 176 files of the web UI and `kvm_system` are identical to v2.0-b7.

The release steps are not in `expected.sha256`: the APK signatures depend on the key, and the root file system on the current Alpine packages. Compared with the v2.0-b7 image, a build from this repository installs the same 163 packages, with official Alpine builds instead of the C906-tuned busybox, coreutils, openssl, lz4 and zstd, and with the Alpine security updates published since. The six `nanokvm-*` packages contain exactly the files of `images/` named in `packages.list`.

A matching hash shows that the build is repeatable, not that the binaries work. Boot-test new binaries on a device before they are published: U-Boot and the boot images, the rebuilt media libraries (video capture and encoding) and the new AIC8800 firmware (Wi-Fi).

## Corresponding source

The image contains GPL-licensed code:
- Linux and its modules, U-Boot;
- BusyBox, e2fsprogs, util-linux and f2fs-tools in the initramfs, and the BusyBox `devmem` applet;
- the NanoKVM application (GPL-3.0);
- Alpine packages.

`platform/build.sh source` writes `build/platform/NanoKVM-OS-<version>-source.tar.xz` after a build. Run it on a clean checkout. The archive contains:
- the GPL-licensed inputs from `sources.lock`: Buildroot, Linux, U-Boot, `osdrv`, the AIC8800 package, cryptodev, the Sipeed SDK paths and BusyBox;
- the Buildroot downloads of the initramfs packages;
- this repository at the release commit, the source commit and `SHA256SUMS`.

The other inputs of `sources.lock` are not redistributed; see [docs/DISTRIBUTION.md](../docs/DISTRIBUTION.md). Attach the archive to the GitHub release next to the image. The v2.0-b7 archive (`NanoKVM-OS-v2.0-b7-platform-source.tar.xz`) covers the kernel, modules and boot images only; it also contains util-linux 2.41.5 and `B7-INITRAMFS.txt`, because that initramfs was assembled before this build existed.

The first-stage firmware in `fip/base-fip.bin` is unchanged from the stock Sipeed NanoKVM firmware: FSBL/BL2 (BSD-3-Clause), OpenSBI 0.9 (BSD-2-Clause), and the SOPHGO DDR parameters and small-core loader. Its source is in [sipeed/LicheeRV-Nano-Build](https://github.com/sipeed/LicheeRV-Nano-Build) at the `sipeed-sdk` commit in `sources.lock` (`fsbl/`, `opensbi/`).

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
