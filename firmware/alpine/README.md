# NanoKVM Alpine base

This directory contains the first direct Alpine port for NanoKVM. The port uses
the official Alpine 3.24 riscv64 userspace and NanoKVM's SG2002 kernel, DTB and
kernel modules.

## Scope

The first milestone deliberately keeps the current application paths and board
scripts:

- Alpine packages own the normal Linux userspace and install into `/`;
- the matched NanoKVM kernel and modules provide SG2002 board support;
- `/kvmapp` carries the current server, web UI and native vendor libraries;
- `/data` remains exFAT;
- partition 2 remains F2FS;
- OpenRC wrappers call the already qualified board scripts in a visible order.

There is no A/B partition scheme and no automatic rollback timer. Development
FITs can be loaded manually from U-Boot. The installed migration uses a one-shot
recovery FIT, then replaces itself with the normal Alpine FIT.

## Manual recovery FIT

Build the one-shot recovery FIT with the SHA256 values of the two files placed
on the exFAT update partition:

```sh
./scripts/build-alpine-recovery-fit.py \\
  --rootfs-sha256 <alpine-rootfs.tar.gz-sha256> \\
  --boot-sha256 <boot-alpine.sd-sha256>
```

The resulting `work/alpine/recovery/boot-alpine-recovery.sd` validates the
p2 start and size plus the p3 start, formats only p2 as F2FS, installs the
verified Alpine root and atomically replaces p1's `boot.sd`. p3 is read-only
while payloads are verified. Before formatting, recovery briefly mounts p3
read-write and stores a fresh archive of the old `/etc/kvm`; after extraction it
restores those settings with their ownership and modes onto F2FS. p3 is then
mounted read-write at `/data` with `nosuid`, `nodev` and `noexec`. It has no
rollback or A/B state.
The complete path, including the following normal reboot, passed on the physical
device; exact hashes and logs are under
`firmware/alpine/evidence/2026-09-19-recovery`.


Build all update payloads from a prepared Alpine root in one command:

```sh
ROOTFS=/path/to/alpine-root \
BOOT_FIT=/path/to/boot-alpine.sd \
OUTPUT=work/alpine/update \
  ./scripts/build-alpine-update-bundle.sh
```

For a root archive captured with numeric owners from a running device, use
`ROOTFS_ARCHIVE=/path/to/alpine-rootfs.tar.gz` instead of `ROOTFS`.

The builder excludes the contents of `/boot`, `/data` and volatile virtual
filesystems, validates the Alpine and NanoKVM entry points, builds the recovery
FIT with the resulting hashes and verifies the final `SHA256SUMS`. It rejects a
decompressed tar stream above 650 MiB to keep headroom on the 768 MiB F2FS
partition. `nanokvm-stage-update` verifies a complete bundle and free space
before publishing it below `/data/nanokvm-update`; `--activate` installs the
matching recovery FIT but leaves reboot explicit.

`nanokvm-kernel-sg2002` owns the board FITs and metadata below
`/usr/lib/nanokvm/boot` and depends on the matching module package. Its APK
trigger activates the selected board image in `/boot/boot.sd` and requests a
reboot. This native package path preserves rootfs and settings. The recovery
bundle described above is a separate full-system reinstallation path.

## Repository policy

The default profile is `stock`. Ordinary userspace packages, including BusyBox,
coreutils, LZ4, zstd and OpenSSL, come from official Alpine repositories.
NanoKVM still supplies its SG2002 kernel, drivers, board firmware and application.

The target repository list is:

1. the signed NanoKVM repository (`https://nkos.pesin.pro/repos/nanokvm`);
2. Alpine v3.24 `main`;
3. Alpine v3.24 `community`;
4. Alpine `edge/community`, tagged as `@edgecommunity`.

The experimental `c906-scalar` profile and its recipes, qualification tools and
benchmark evidence are retained under `tuning/`. Its repository is only used
when explicitly requested; it is not required to build or run a stock image.

The edge repository is never used as an untagged upgrade source. The VPN UI
requests `tailscale@edgecommunity`, `tailscale-openrc@edgecommunity`,
`netbird@edgecommunity` and `netbird-openrc@edgecommunity` explicitly. This
keeps the rest of the operating system on Alpine 3.24. OpenVPN comes from the
stable Alpine repository. OpenVPN, Tailscale and NetBird are optional and are
absent from the default `nanokvm-release` dependency set.

The NanoKVM repository is a rolling package channel within the selected
stable Alpine branch. New signed revisions become available through ordinary
`apk update` and `apk upgrade`; the same indexes are inputs to attended image
builds. First-party entries in `/etc/apk/world` normally use unversioned package
names so a newer signed revision can be selected. Exact version constraints are
reserved for an explicit operator hold or a reproducibility test.

Kernel, matching modules and board FITs use the same APK update path. The kernel
trigger activates the board-specific FIT and records that a reboot is required.
Full recovery images remain necessary for partition layout, filesystem, FIP or
bootloader changes. Moving to another Alpine stable branch is an explicit system
upgrade rather than an incidental package update.

## Existing C906 installations

Removing the tuned repository alone does not replace its higher `pkgrel`
packages. Run the one-time maintenance script from the matching source checkout
on the device, first with `--simulate`, then with `--apply`:

```sh
sh scripts/migrate-alpine-stock.sh --simulate
sh scripts/migrate-alpine-stock.sh --apply
```

It uses native `apk upgrade --available` for installed packages from the seven
historical overlay origins only (including their library and utility
subpackages), after excluding the known tuned repository. APK may also update dependencies while solving this transaction; review the
simulation first. It preserves APK world constraints, settings and installed
optional packages, and does not request a NanoKVM kernel/app upgrade.
Custom C906 repository URLs must be removed explicitly before running it.
The profile is changed to `stock` only after APK succeeds. No partition changes,
rootfs replacement, backup copies or permanent update wrapper are involved.
Subsequent updates use ordinary `apk update` / `apk upgrade`. Do not run an
unrestricted `apk upgrade --available` against a partial set of repositories.

## Request-driven images

`scripts/build-alpine-personal-image.sh` is the backend primitive for an
Attended Sysupgrade style service. A request selects `stock` or
`c906-scalar`, adds explicit APK names, pins the base rootfs and normal FIT by
SHA256, then installs from the ordered signed repositories and emits the same
hash-bound recovery bundle. See `attended/README.md` for the request and VM
build interface.

## Stock and tuned builds

The stock image uses official Alpine package binaries plus the same NanoKVM
kernel, DTB, modules, firmware and application payload as the tuned image.

The retained experimental tuning target is scalar C906 code with the existing `lp64d`
ABI:

```
-O2
-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov
       _xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx
       _xtheadmempair_xtheadsync
-mtune=thead-c906
-mno-fence-tso
-mabi=lp64d
```

`xtheadvector` is tested separately in selected leaf functions. It is not
enabled globally: C906 XTheadVector follows the legacy 0.7.1 model and is
incompatible with standard RVV 1.0 code.

## Development staging commands

For the pre-install development path, stage Alpine below the running NanoKVM
root at `/mnt/alpine-test`:

```sh
PORT_DIR=/tmp/nanokvm-alpine-port \
  ./scripts/stage-alpine-rootfs.sh
```

Build the manual test FIT:

```sh
./scripts/build-alpine-probe-fit.py
```

Load it from U-Boot over UART:

```
fatload mmc 0:1 0x81800000 boot-alpine-test.sd
bootm 0x81800000#config-sg2002_licheervnano_sd_minimal
```

Export the tested runtime into package payloads and build the signed
repository on an Alpine builder with a configured `abuild` key:

```sh
ROOTFS=/path/to/tested-root \
  PROFILE=stock \
  OUTPUT=work/alpine/payloads/stock \
  KERNEL_RELEASE=7.2.5-nanokvm-os-r4 \
  ./scripts/export-alpine-payloads.sh

PAYLOAD_ROOT=work/alpine/payloads \
  REPODEST=work/alpine/repo \
  ./scripts/build-alpine-packages.sh stock
```

The repository consumed by APK is
`work/alpine/repo/stock/recipes/riscv64`; its parent
`work/alpine/repo/stock/recipes` is the repository URL because APK appends
the target architecture.
