# NanoKVM OS platform

This directory builds the board platform of NanoKVM OS v2 from source with one command:
- the toolchain;
- the Linux kernel and all kernel modules;
- U-Boot and `fip.bin`;
- the initramfs and the `boot.sd` images.

Everything the build uses is pinned here, and the result is checked against known hashes. The application, web UI and Alpine root filesystem are built separately (see [docs/BUILD.md](../docs/BUILD.md)).

| Output in `build/platform/images` | Shipped in |
|---|---|
| `Image` (Linux `7.2.6-nanokvm-os-r1`) | every `boot.sd` |
| `lib/modules/7.2.6-nanokvm-os-r1/` (62 in-tree and 17 out-of-tree modules, `modules.*`) | `nanokvm-kmod-sg2002` |
| `boot/*.sd`, `boot/*.sha256`, `boot/kernel.release` (5 boards × CMA/fixed video memory) | `nanokvm-kernel-sg2002`, `/usr/lib/nanokvm/boot` |
| `dtb/*.dtb` | inside the matching `boot.sd` |
| `initramfs.cpio.zst` | inside every `boot.sd` |
| `u-boot.bin`, `fip.bin` | boot partition of the SD image |

## Build

On a Linux x86-64 host with the [Buildroot requirements](https://buildroot.org/downloads/manual/manual.html#requirement), plus `git` 2.27 or newer, `curl`, `libssl-dev`, `flex` and `bison` (tested on Ubuntu 24.04):

```sh
platform/build.sh
```

The first run downloads about 1 GB and needs about 20 GB of disk space. Building the toolchain takes most of the first run (30 minutes on 16 threads). Later runs reuse the toolchain while its inputs are unchanged: the Buildroot pin, `firmware/buildroot` and the toolchain recipe in `build.sh`. If one of them changes, `build.sh` refuses the old toolchain and names the directories to delete. Steps can be run on their own, for example `platform/build.sh kernel modules verify`. The step order is in `build.sh --help`. Use `-o DIR` for another output directory and `-j N` for the number of jobs.

## What is here

| Path | Contents |
|---|---|
| `sources.lock` | Every upstream input: archives by SHA-256, Git trees by commit and tree id |
| `build.sh` | All build steps |
| `expected.sha256` | Hash of every output; `build.sh verify` fails if an output is missing, differs or is not listed |
| `kernel/` | `config` and patches for kernel.org Linux 7.2.6 |
| `modules/<name>/` | Patches for the SOPHGO media drivers (`osdrv`), AIC8800 and RTL8733BS Wi-Fi and cryptodev; `sg2002-aes/` is the CryptoDMA driver source |
| `uboot/` | `defconfig` and patches for U-Boot 2026.07 |
| `fip/base-fip.bin` | First-stage boot firmware; the build replaces only U-Boot in it |
| `boot/` | FIT template, initramfs file list and `init`; `stock-init` is the stock Sipeed initramfs init, and `stock-init.diff` turns it into `init` |

Two directories outside `platform/` are used as they are:
- `firmware/buildroot/`: platform-only source patches and `configs/nanokvm_platform_defconfig` for the toolchain, host tools and initramfs userland;
- `firmware/boards/`: the board device trees.

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

`build.sh verify` compares every output with `expected.sha256`. The file has three sections:

1. **Identical to NanoKVM OS v2.0-a2 through v2.0-b7** (99 outputs):
   - the kernel `Image` and `boot/kernel.release`;
   - 78 of the 79 modules, and the `modules.*` files that do not depend on module order;
   - the 10 device trees of the released `boot.sd` images.
2. **Changed after v2.0-b7:** `aic8800_fdrv.ko`. `modules/aic8800/0002` adds the Linux 7.3 cfg80211 fix after the release. The commit that added `platform/` (before `0002`) reproduces all 79 modules, and the source archive of v2.0-b7 is built from that commit.
3. **New binaries, not yet in a release** (28 outputs):
   - `u-boot.bin` and `fip.bin`;
   - the initramfs;
   - the `boot.sd` images and their `.sha256` files;
   - `modules.alias`, `modules.dep`, their `.bin` forms, and `modules.symbols.bin`.

   Why they differ from b7:
   - **U-Boot and `fip.bin`.** b7's U-Boot was built from exactly these patches and defconfig with GNU Binutils 2.45.1; a rebuild reproduced it bit for bit. The platform build uses the single Buildroot toolchain (Binutils 2.47), which produces a different binary.
   - **initramfs and `boot.sd`.** The initramfs is built from the Buildroot packages and compressed with the pinned zstd instead of gzip.
   - **`depmod` indexes.** They contain the same entries as b7, but the modules are now listed in sorted order instead of directory order. `nanokvm-activate-kernel` runs `depmod` again on the device.

   A matching hash shows that the build is repeatable, not that these binaries boot. Boot-test them on a device before they are published.

On 2026-10-02, two clean builds ran at the same time. Each started from a fresh clone and an empty output directory, and the two directories had different paths. Each built the toolchain and every output: the toolchains took about 50 minutes with both builds running, the remaining steps 7 minutes. The two builds produced the same bytes for all 128 outputs, and `verify` matched every entry in both.

## Corresponding source

The binaries above contain GPL-2.0 code:
- Linux and its modules;
- U-Boot;
- BusyBox, e2fsprogs, util-linux and f2fs-tools in the initramfs.

`platform/build.sh source` writes `build/platform/NanoKVM-OS-<version>-platform-source.tar.xz` after a build. Run it on a clean checkout. The archive contains:
- every input from `sources.lock`;
- the Buildroot downloads of the initramfs packages;
- this directory, `firmware/buildroot` and `firmware/boards`;
- the source commit and `SHA256SUMS`.

Attach it to the GitHub release next to the image. The v2.0-b7 archive also contains util-linux 2.41.5 and `B7-INITRAMFS.txt`, because that initramfs was assembled before this build existed.

The first-stage firmware in `fip/base-fip.bin` is unchanged from the stock Sipeed NanoKVM firmware: FSBL/BL2 (BSD-3-Clause), OpenSBI 0.9 (BSD-2-Clause), and the SOPHGO DDR parameters and small-core loader. Its source is in [sipeed/LicheeRV-Nano-Build](https://github.com/sipeed/LicheeRV-Nano-Build) at the `sipeed-sdk` commit in `sources.lock` (`fsbl/`, `opensbi/`).

Other components of the image:
- **Alpine Linux 3.24 packages**: Alpine publishes their build recipes ([aports](https://gitlab.alpinelinux.org/alpine/aports)) and source archives. The installed versions are listed in `/lib/apk/db/installed` of each image.
- **The NanoKVM application, web UI and packages** (GPL-3.0): this repository.

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
