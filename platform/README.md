# Building NanoKVM OS

`platform/build.sh` builds the complete NanoKVM OS SD card image from this repository with one command:
- the toolchain, the Linux kernel and all kernel modules, U-Boot, OpenSBI, `fip.bin`, the initramfs and the `boot.sd` images;
- the SOPHGO media libraries, the board service `kvm_system`, the server, the update helpers and the web UI;
- the board tools, the EDID profiles and the firmware files;
- the fifteen `nanokvm-*` APK packages, the Alpine 3.24 root file system and the SD card image.

Every upstream input is pinned in `sources.lock` and checked when it is downloaded. Two builds of the same tree give identical outputs, and CI lists the outputs that each change alters (see [Verification](#verification)). The full image and the `nanokvm-bootloader-sg2002` package install the built `fip.bin` (see [opensbi/README.md](opensbi/README.md)).

| Output | Shipped in |
|---|---|
| `images/Image` (Linux `7.3.1-nanokvm-os-r6`) | every `boot.sd` |
| `images/lib/modules/7.3.1-nanokvm-os-r6/` (84 in-tree and 19 out-of-tree modules, `modules.*`) | `nanokvm-kmod-sg2002` |
| `images/boot/*.sd`, `*.sha256`, `kernel.release` (one per board, `NAME.dtb`, each with the 144 MiB CMA video region) | `nanokvm-kernel-sg2002`, `/usr/lib/nanokvm/boot` |
| `images/dtb/*.dtb`, `images/initramfs.cpio.lz4` | inside the `boot.sd` images |
| `images/licenses/initramfs/` (Buildroot `legal-info`: `manifest.csv` and the license texts of the initramfs userland and C library) | `nanokvm-kernel-sg2002`, `/usr/share/licenses/nanokvm-kernel-sg2002/initramfs` |
| `images/u-boot.bin`, `images/fip.bin` | boot partition of the SD image |
| `images/opensbi/*`, `images/fip-manifest.json` | host build provenance and loader checks |
| `images/native/*.so` (6 HDMI-light libraries) | `nanokvm`, `/kvmapp/server/dl_lib` |
| `images/system/kvm_system` | `nanokvm`, `/kvmapp/kvm_system` |
| `images/server/NanoKVM-Server`, `nkos-update`, `nkos-apply-updates`, `nkos-usb-internet` | `nanokvm`, `nanokvm-base` |
| `images/web/` | `nanokvm-web`, `/kvmapp/server/web` |
| `images/tools/` (devmem, `nanokvm_update_edid`, `nkos-board-probe`, `usb-audio-capture`, EDID profiles) | `nanokvm-base`, `nanokvm`, `nanokvm-firmware-sg2002` |
| `images/tools/zlib-ng/` (zlib-ng as `libz.so.1`, its license) | `nanokvm-zlib`, in place of Alpine's `zlib` |
| `images/tools/exfatprogs/`, `images/tools/i2c-tools/` (exfatprogs 1.4.3 and i2c-tools 4.4 with their licenses) | `nanokvm-exfatprogs`, `nanokvm-i2c-tools` |
| `images/tools/xz/COPYING.0BSD` (liblzma license; zlib-ng and liblzma are also linked statically into NanoKVM-Server for its gzip and xz readers) | `nanokvm`, `/usr/share/licenses/nanokvm` |
| `fip.bin`, `fip-licenses/` | `nanokvm-bootloader-sg2002`; its trigger puts `fip.bin` on the boot partition |
| `images/firmware/` (AIC8800 Wi-Fi, regulatory database, video codec) | `nanokvm-firmware-sg2002` |
| `release/apk/` | the signed APK repository of the fifteen packages: `recipes/riscv64/` with the packages, `APKINDEX.tar.gz` and `Packages.adb`, and the public keys |
| `release/alpine-rootfs.tar.gz`, `release/installed-packages.txt`, `release/installed-licenses.txt` | the root file system, its package versions and their declared licenses |
| `release/image/NanoKVM-OS-Image-<image-version>-apps-<application-version>.img.zip`, `SHA256SUMS` | the SD card image |

## Image and application versions

Starting with applications **v2.5-a1**, full SD images have their own version.
**Image v1.0-a1** includes applications **v2.5-a1**; both are alpha releases.
`firmware/alpine/release.env` is the source of truth: `NANOKVM_IMAGE_VERSION`
identifies the image, `NANOKVM_VERSION` the applications, and
`NANOKVM_APK_VERSION` their APK-comparable version (`2.5_alpha1`).

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
- `libatomic` for the official Node.js 26 binaries (on Ubuntu: `libatomic1`);
- OpenSSL 3.5 or newer on the host: the kernel configuration records whether the host OpenSSL supports ML-DSA (`CONFIG_OPENSSL_SUPPORTS_ML_DSA`), so an older one fails the `kernel` step's configuration check. The pins come from Ubuntu 26.04;
- for the packages, rootfs and image steps: Linux 6.7 or newer, `newuidmap` and `newgidmap` (package `uidmap`), and subordinate IDs for the build user in `/etc/subuid` and `/etc/subgid` (Ubuntu adds them for every user).

```sh
platform/build.sh -d
```

This creates a local test image signed with generated test keys. For official
release signing, supply the ECDSA key as described [below](#signing-key). The
Release workflow builds with `-u` instead: a throwaway key that the image does
not trust signs the packages and indexes, and `scripts/sign-release.sh` then
signs the repository with the release key, outside the build.

The build needs no root. The packages, rootfs and image steps run as root of a user namespace: it maps the subordinate IDs, so that the root file system gets its real owners, and starts riscv64 programs through qemu in the namespace's own `binfmt_misc`. Files in `OUTPUT/apk-builder` and `OUTPUT/rootfs` then belong to subordinate IDs; `build.sh clean` deletes them.

The first run downloads about 2 GB and needs about 25 GB of disk space. Building the toolchain takes most of the first run (20 minutes on 16 threads); the other steps take about 10 minutes. Later runs reuse the toolchain while its inputs are unchanged: the Buildroot pin, `firmware/buildroot` and the toolchain recipe in `build.sh`. If one of them changes, `build.sh` refuses the old toolchain and names the directories to delete. Steps can be run on their own, for example `platform/build.sh kernel modules hashes`. The step order is in `build.sh --help`. Use `-o DIR` for another output directory and `-j N` for the number of jobs.

Go modules, npm packages and Alpine packages are downloaded during the build and checked against `server/go.sum`, `web/pnpm-lock.yaml` and the signed Alpine indexes. Alpine updates its 3.24 packages with security fixes, so a later build installs their newer versions; `release/installed-packages.txt` lists the versions in the image.

### Debug kernel

Use a separate output directory for each kernel profile. The builder records
its profile in `OUTPUT/.kernel-profile` and rejects a different profile before
changing kernel or package artifacts, even when the environment variable was
omitted on a later invocation. Existing kernel/boot release metadata is checked
as well. Do not remove the stamp to reuse mixed artifacts.

```sh
NANOKVM_KERNEL_PROFILE=debug platform/build.sh -o /path/to/debug-output -d
```

The kernel build fills `CONFIG_CC_VERSION_TEXT` from the selected compiler before
`olddefconfig`. This descriptive banner follows Buildroot updates automatically;
the complete resulting configuration is still compared with the expected file.
Numeric compiler versions, capability probes and kernel options remain pinned.
A mismatch prints a unified diff and stops the build.

The debug kernel has a `-debug` release suffix and adds `kernel/config-debug.fragment`:
perf and the CPU counters, frame pointers, function, syscall and block I/O
tracing, kprobes, SysRq, hung-task and lockup detectors, dynamic debug,
`/proc/config.gz` and per-task accounting, plus DWARF for `vmlinux` on the
build host (its installed modules are stripped). It costs time on every call
and interrupt, so it is not for measuring release behaviour.

Its packages and SD image need the test keys (`-d`): nothing built with the
debug kernel can reach a published repository. The image, named
`NanoKVM-OS-Image-VERSION-debug-…`, adds the tools of `debug-packages` from
Alpine main and community, and keeps community in its repositories. A kernel
from the published repository replaces the debug kernel on `apk upgrade`.

## Signing key

From v3.4.0, the packages and both indexes are signed only with ECDSA.
Release builds pass `-e path/to/nkos-release-ec-b8e89b66.key`, with the matching
`nkos-release-ec-b8e89b66.pub` next to it. The public key must already be in
`firmware/alpine/keys`. The build rejects RSA keys, encrypted keys and mismatched
pairs before building. `-k` is no longer accepted. Local builds use `-d` to
create/reuse `OUTPUT/keys/nanokvm-test-ec-*.key`; never publish these packages or
images. apk trusts each key in `/etc/apk/keys` for every repository it uses.

The v2 packages and `APKINDEX.tar.gz` use ECDSA/SHA-256 with apk-tools' historical
`RSA256` digest marker, while `Packages.adb` uses native v3 ECDSA signing. All
new core packages are checked with the EC key alone; historical RSA public keys
remain installed for old package verification and rollback. See the
[signing policy and migration](../firmware/alpine/README.md#ecdsa-only-signing-from-v340).

The package `nanokvm-keys` installs the public keys of `firmware/alpine/keys` (with `-d`, also the test keys) in `/etc/apk/keys` and owns them, so a later release can add or remove a key; on an older device it also moves the NanoKVM repository line to the v3 index. The rootfs step copies the keys into the root before it installs `nanokvm-release`, and apk takes over the identical files; the step fails if a key in `/etc/apk/keys` has no owner. Publishing a release, moving to the ECDSA key and replacing a key are described in [firmware/alpine/README.md](../firmware/alpine/README.md#signing-keys).

The root file system uses official Alpine packages (`BUILD_PROFILE="stock"`), with NanoKVM-built zlib-ng and OpenSSL runtimes. `nanokvm-openssl` supplies OpenSSL 3.5.9 LTS with the C906 `-O3` profile and ABI3 libraries. Releases up to v2.0-b7 used a broader `c906-scalar` overlay; that overlay is not built here.

## What is here

| Path | Contents |
|---|---|
| `opensbi/` | upstream generic SG2002 configuration, M-mode DT, FDT handoff and C906 draft VS compatibility patches |
| `sources.lock` | Every upstream input: archives by SHA-256, Git trees by commit and tree id |
| `build.sh` | All build steps |
| `packages.list` | The contents of the fifteen APK packages: each file, its mode and where it comes from |
| `kernel/` | `config` and patches for kernel.org Linux 7.3.1 |
| `modules/<name>/` | Patches for the SOPHGO media drivers (`osdrv`), AIC8800 and RTL8733BS Wi-Fi and cryptodev; `sg2002-aes/` is the CryptoDMA driver source |
| `uboot/` | `defconfig` and patches for U-Boot 2026.10 |
| `fip/base-fip.bin` | First-stage boot firmware; the build replaces OpenSBI and U-Boot while retaining the vendor first-stage loader |
| `boot/` | FIT template, initramfs file list and `init`; `stock-init` is the stock Sipeed initramfs init, and `stock-init.diff` turns it into `init` |
| `native/cvi_mpi/` | Patches for the SOPHGO media libraries (`cvi_mpi`) |

The build also uses these parts of the repository:
- `firmware/buildroot/`: platform-only source patches and `configs/nanokvm_platform_defconfig` for the toolchain, host tools and initramfs userland;
- `firmware/boards/`: the board device trees and the board probe;
- `firmware/sensor/patches/`: NanoKVM I2C/MIPI wiring and LT6911UXC/LT6911D identification; applied before compiling MMF;
- `firmware/mpi/`: the MMF capture-size helper, compatibility headers and the hashes of the SOPHGO ISP objects (`vendor-isp-objects.json`);
- `firmware/alpine/`: the APK recipes, OpenRC services and compatibility scripts, `release.env` with the version;
- `server/`, `web/`, `support/sg2002/` (including `nanokvm_update_edid/`), `kvmapp/`: the application;
- `scripts/`: the component builders that `build.sh` calls.

## U-Boot console and uEnv.txt

U-Boot boots at once; `uEnv.txt` on the boot partition can reopen the serial
console window and add kernel options. See [docs/BOOT.md](../docs/BOOT.md).

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
4. Keep a copy of `build/platform/images/SHA256SUMS` from before the change, run `build.sh hashes` after it, and compare: `scripts/compare-outputs.py OLD-SHA256SUMS build/platform/images/SHA256SUMS` names every output that changed. Only the outputs you meant to change should be listed; say which in the commit. The pull request gets the same comparison against main from CI.

A changed kernel also needs a new release name: update `CONFIG_LOCALVERSION` in `kernel/config` and `release` in `build.sh`. `build.sh` sets the fixed build user, host and timestamps, so that two builds of the same tree are identical.

## Verification

`build.sh hashes` writes `images/SHA256SUMS`, the hash of every output. Two builds of the same tree give the same hashes.

The Release build workflow (`.github/workflows/release-build.yml`) builds every push to main and every pull request that can change an output. It publishes `SHA256SUMS` as the `outputs-sha256` artifact and comments on the pull request with the outputs that differ from the main build it starts from (`scripts/compare-outputs.py`). An output that changes although none of its inputs did means the build is not reproducible: treat it as a bug. `opensbi/build-manifest.json` records the hash of `build.sh`, so it changes with every edit of `build.sh`.

To check that a local build reproduces a CI build, download that run's hashes and compare:

```sh
gh run download RUN-ID -n outputs-sha256 -D ci
scripts/compare-outputs.py --check ci/SHA256SUMS build/platform/images/SHA256SUMS
```

The release uses GCC 16.2 / Binutils 2.47 and the component profiles in `cpu-profile.json`: kernel and modules use `-O3` without LTO and with `-mno-strict-align` (the C906 handles misaligned access in hardware), while source-owned userspace uses scalar `-O2`.

Kernel, device trees and modules change with the reviewed C906 vector-context, video-clock and memory-profile updates. The AIC8800 driver includes the cfg80211 compatibility patch. U-Boot, the initramfs, FIT images, native media libraries, system service and native tools are built from the pinned inputs. Closed SOPHGO ISP/3A objects remain pinned binary inputs: relinking does not recompile those objects. Go uses its own compiler; the profile applies to its C/C++ interoperability code.

Audit the effective compiler commands as well as the supplied flags:

```sh
python3 scripts/audit-kbuild-profile.py build/platform/kernel/build
python3 scripts/audit-kbuild-profile.py build/platform/modules
python3 scripts/audit-kbuild-profile.py build/platform/uboot/build --kind bootloader
```

The audit checks the last effective optimization, ISA and ABI options. Kernel vDSO objects retain Kbuild's explicit portable/CFI ISA choices and are reported separately; they retain the kernel optimization level and C906 tuning. Kernel and bootloader builds do not enable floating-point or automatic vector code generation.

APK signatures depend on the signing key; the root filesystem also depends on the current official Alpine 3.24 package versions. They are listed in `release/installed-packages.txt`. Existing installations with the optional C906 overlay require the documented stock-package migration; installing a NanoKVM application update alone does not replace every Alpine package.

Identical hashes show that a build reproduces, not that it works on hardware. Boot-test new U-Boot/FIT images, media capture and encoding, and Wi-Fi on a device before publishing. A passing build does not establish compatibility with hardware variants that were not available for testing.

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

The reference `fip/base-fip.bin` contains stock FSBL/BL2 (BSD-3-Clause), OpenSBI 0.9 (BSD-2-Clause), DDR parameters and small-core fields. The output `images/fip.bin` preserves BLCP, DDR parameters and BLCP_2ND bytes, patches one BL2 instruction so the FSBL sets the C906 PLL to 1000 MHz instead of 850 MHz (`scripts/nanokvm-fip.py`), and replaces MONITOR with pinned upstream OpenSBI 1.9 and LOADER_2ND with U-Boot. Vendor reference source is in [sipeed/LicheeRV-Nano-Build](https://github.com/sipeed/LicheeRV-Nano-Build) at the `sipeed-sdk` commit in `sources.lock` (`fsbl/`, `opensbi/`). OpenSBI source is separately pinned and included by the source step. See [the OpenSBI port and recovery procedure](opensbi/README.md).

Alpine publishes the build recipes ([aports](https://gitlab.alpinelinux.org/alpine/aports)) and source archives of its packages. The installed versions are listed in `/lib/apk/db/installed` of each image and in `release/installed-packages.txt`.

### Written offer

*Draft: the maintainer has not approved this wording yet.*

For three years after we last distribute a NanoKVM OS v2 release, we will give anyone, on request, a complete machine-readable copy of the corresponding source of the GPL-licensed components in that release, for no more than the cost of physically performing the distribution. To request it, open an issue in this repository and name the release.

## Compiler cache

Buildroot and the external platform steps use Buildroot's ccache-enabled SDK
wrapper. This covers C/C++ compilation for the kernel, modules, U-Boot,
OpenSBI and native code. Go, Rust compilation, linking and image packaging
are not cached by ccache. The existing complete-toolchain and download caches
remain in use.

The compiler cache defaults to `OUTPUT/ccache` with a **5 GB** size limit and
least-recently-used cleanup. It survives removal of component build trees.
For local builds, use a shared directory outside disposable task outputs:

```sh
export NANOKVM_CCACHE_DIR=/home/dgrace/build/nanokvm-os/cache/ccache
export NANOKVM_CCACHE_MAXSIZE=5G
platform/build.sh -o /home/dgrace/build/nanokvm-os/current kernel modules
platform/build.sh -o /home/dgrace/build/nanokvm-os/current cache-stats
```

`NANOKVM_CCACHE=0` bypasses both Buildroot and SDK compiler caching for a
comparison build. Enabling support changes the toolchain fingerprint, so the
first build needs a new toolchain; do not relabel an older toolchain cache.
No relaxed timestamp/header checks or path rewriting are enabled. Different
build paths may therefore miss the cache, retaining original debug paths.

CI restores compiler entries separately from the toolchain archive. Snapshot
keys include the runner image, toolchain identity and run. A toolchain change
starts a new cache family; local shared caches likewise use a namespace for
each toolchain fingerprint. ccache also checks source, headers and flags.
Only jobs on `main` save shared snapshots; PRs consume them. The signing job
never restores or uses a compiler cache. Each build reports cache size, hits,
misses and cleanup statistics in its job summary. The 5 GB limit applies to
each compiler cache; retained GitHub snapshots also consume repository cache
storage and are subject to GitHub's cache eviction policy.

`tests/test-compiler-cache.py` checks cold/warm/bypassed object equality and
header/flag invalidation. CI additionally runs it against the real RISC-V SDK
wrapper after preparing the toolchain. These small checks establish cache
correctness; they do not measure the speedup of a full firmware build.

## Buildroot scope

The platform profile builds no installed OS rootfs and includes no private addon
manager. Alpine owns system packages and optional software. BusyBox has a broad
conditional dependency list: selecting unrelated packages here can rebuild them
even when only the `busybox` target is requested. Keep this profile limited to
the actual platform dependency graph.

Recipe cleanup changes the toolchain input fingerprint. Existing output must not
be reused under a new fingerprint; the next full platform build must still
reproduce every output the change does not touch. A dependency/configuration check alone does
not qualify newly built boot images.

## Component optimization profiles

Kernel and modules use GCC `-O3` without LTO; source-owned userspace
uses scalar `-O2`. USB audio explicitly selects float Opus with the
`audio` profile. Handwritten vendor RVV/assembly remains available.
See [component profiles](../docs/component-build-profiles.md) for the optional
RustDesk performance build, measurement limits and release checksum policy.

## HDMI-light media runtime

The standard build and `nanokvm` ship the complete six-library HDMI-light bundle. Open MPI components are linked into `libkvm_mmf.so`; the four vendor algorithm DSOs remain separate. `platform/build.sh native` always selects this bundle. `NANOKVM_MMF_PROFILE=full` is retained only for explicit comparison builds through the lower-level MMF script.

APK owns the old MPI library paths and removes files no longer shipped when upgrading `nanokvm`; the existing transaction hook restarts the application after the update. Do not overlay individual libraries in a running process or retain old MPI DSOs alongside the new set. Native license notices are included in the application package.

Measured on a NanoKVM PCIe: about 0.63 MiB lower server PSS, no demonstrated CPU improvement.


### Application package split

The build produces fifteen core APKs. `nanokvm` owns the server, board service,
native libraries and application scripts; `nanokvm-web` owns
`/kvmapp/server/web`. `nanokvm-app` is a file-free compatibility metapackage
(revision r2 for v3.0-a1) depending on both. Keep it while older installations
and `nanokvm-release` depend on the old package name. The new packages declare
`replaces=nanokvm-app` so APK can transfer ownership during the upgrade.
The OpenRC service is still named `nanokvm-app`.

### Tools in place of Alpine community packages

`nanokvm-exfatprogs` (exfatprogs 1.4.3: `mkfs.exfat` formats `/data` at first
boot) and `nanokvm-i2c-tools` (i2c-tools 4.4: `i2cdetect` identifies the board)
are the only programs of the image that Alpine ships in its community
repository, so the image needs only Alpine main. The `tools` step builds them
from the pinned upstream releases with the image's toolchain: i2c-tools
statically, exfatprogs against the device's `libblkid`. Each package
`provides` and `replaces` the Alpine package of the same tool, so an upgrade
removes the Alpine package and a dependency on the tool's name is satisfied.

The browser package requires `nanokvm-web-api=2`, provided by the server
(2: the browser sends passwords as typed, marked `passwordEncoding: "plain"`,
not as CryptoJS ciphertext).
Increment this contract version when the browser/server API becomes incompatible,
and update both recipes together. Compatible fixes may increment either package's
`pkgrel` independently. Do not pin their revisions to each other.
The backend requires `nanokvm-base>=3.0_alpha1-r1`, which contains the updated
transaction helper: web-only updates require a browser reload, not a service
restart. Backend and native-library updates still restart a running server.
The initial migration can restart it because the previous hook captures the old
combined runtime tree before the transaction.

`/kvmapp/build-manifest.json` covers only backend files;
`/kvmapp/web-build-manifest.json` covers only browser files. They list file
hashes only. The release version (`/kvmapp/version`, `VERSION` in
`/etc/nanokvm-release`) and the source commit (`SOURCE_COMMIT`) belong to
`nanokvm-release`, so `nanokvm` and `nanokvm-web` change only when their files
do. Package revisions are visible through Software, diagnostic reports,
or `apk info -v nanokvm nanokvm-web nanokvm-app`.

### Independent add-on updates

APK add-ons have their own releases and a common signed Pages repository,
separate from both stable and beta OS repositories. The APK repository workflow
discovers stable releases of the add-ons in `ADDONS` of `scripts/addon_packages.py`
(RustDesk and IPMI), tagged NAME-VERSION-rN by the add-on workflows. It verifies
published checksums and package metadata, then signs the add-on v3 index in the
protected `release` environment. It retains six releases per add-on.

OS builds do not download or pin add-ons, and add-on publication does not change
`packages.json`, the OS version, OS indexes or the latest SD-image link. Add-ons
declare capability dependencies; a version requiring a newer runtime cannot be
installed on an incompatible OS. PicoClaw and acme.sh continue to use their
existing upstream and Alpine update mechanisms.
