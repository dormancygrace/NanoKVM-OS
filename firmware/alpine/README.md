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
The complete path, including the following normal reboot, has been tested on
a device.


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

1. the signed NanoKVM repository, read through its v3 index
   (`https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb`; see
   [signing keys](#signing-keys));
2. Alpine v3.24 `main`.

Alpine v3.24 `community` is off by default: the image needs nothing from it,
and every apk run would parse its index too. The Software page switches it on
(placed right after `main`, from the same mirror), and installing Tailscale or
NetBird does so itself. The page refuses to switch it off while installed
packages come only from it, and names them.

Devices from releases up to 3.2 list `community`. The server decides once
(`server/osupdate/community.go`), after the edge migration below: it removes
the line when `apk list --orphaned` shows no installed package that only
`community` provides, and keeps it otherwise. It waits while `exfatprogs` or
`i2c-tools` from `community` are still installed, until `nanokvm-base` replaces
them, and while an index cannot be read (apk then warns and reports every
package as orphaned). `/etc/kvm/apk-community.chosen` records that the decision
is made, by the server or by the user, and it is not made again.

Tailscale and NetBird come from the stable Alpine `community` repository,
OpenVPN from `main`. They are optional and absent from the default
`nanokvm-release` dependency set.

Releases up to 3.1 also listed Alpine `edge/community`, tagged as
`@edgecommunity`, for Tailscale and NetBird. Every apk run then parsed its index
too: about 30% of the time of an `apk version` and 10 MB of memory on the
device. The server removes that line once (`server/osupdate/edge.go`): world
entries `NAME@edgecommunity` become `NAME>=INSTALLED`, so a package installed
from edge is not downgraded to the older stable build and follows the stable
branch once it catches up, and only then does the line go. A world entry with a
tag that no repository has would stop every apk transaction.

The NanoKVM repository is a rolling package channel within the selected
stable Alpine branch. New signed revisions become available through ordinary
`apk update` and `apk upgrade`. First-party entries in `/etc/apk/world` normally use unversioned package
names so a newer signed revision can be selected. Exact version constraints are
reserved for an explicit operator hold or a reproducibility test.

Kernel, matching modules and board FITs use the same APK update path. The kernel
trigger activates the board-specific FIT and records that a reboot is required.
Full recovery images remain necessary for partition layout, filesystem, FIP or
bootloader changes. Moving to another Alpine stable branch is an explicit system
upgrade rather than an incidental package update.

## Signing keys

apk trusts every public key in `/etc/apk/keys` for every repository; it does
not tie a key to a repository. The NanoKVM release keys are in `keys/`:

| Key | Type | Signs |
|---|---|---|
| `dgrace-6aaddbb6.rsa.pub` | RSA 4096 | the packages and `APKINDEX.tar.gz` (RSA256, RSA with SHA-256); `Packages.adb` during the transition |
| `nkos-release-ec-b8e89b66.pub` | ECDSA P-256 | `Packages.adb` |

The package `nanokvm-keys` installs them in `/etc/apk/keys` and owns them, as
`alpine-keys` does for Alpine's keys; `nanokvm-base` depends on it. Because a
package owns the files, a release can add a key, and a key removed from `keys/`
is removed from devices when they update. A key copied by hand stays.

The image build must trust the keys before it can install from the repository,
so the rootfs step copies `keys/` into the root first. When apk then installs
`nanokvm-keys`, it takes over these identical files: `/etc/apk` is not a
protected path, so there is neither a conflict nor an `.apk-new`. Devices
installed before `nanokvm-keys` have the RSA key as a file without an owner;
their update takes it over the same way. (Tested with apk-tools 3.0.8 in a
scratch root on the device.)

### Two indexes

The repository has two indexes of the same abuild packages in `riscv64/`:

- `APKINDEX.tar.gz`, the v2 index, signed with the RSA key. Devices up to 2.0
  use the repository line `https://nkos.pesin.pro/repos/nanokvm`, read this
  index and trust only the RSA key.
- `Packages.adb`, the apk-tools v3 index, signed with the ECDSA key and, during
  the transition, the RSA key; apk accepts it if it trusts either key. Images
  from 2.5 on use the line
  `https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb`. When an older
  device installs 2.5, the post-install script of `nanokvm-keys` changes its
  NanoKVM line to that URL, after the keys are in place. Alpine's lines and any
  other line stay as they are.

apk checks a package from an index against the package hash in that index, so
the packages need no ECDSA signature.

### Publishing a release

1. Set the application and APK versions in `firmware/alpine/release.env`, and
   keep the image version unless the release brings a new image. Merge the
   change.
2. Push the tag, which must equal `NANOKVM_VERSION`, for example
   `git tag v3.3.0 && git push origin v3.3.0`.
3. The Release workflow (`.github/workflows/release.yml`) builds everything
   from the tag on a GitHub runner, without the signing keys. Its sign job then
   waits in the `release` environment until a maintainer approves it. It signs
   the packages and both indexes with the release keys (the environment
   secrets `APK_RSA_KEY` and `APK_EC_KEY`: unencrypted PEM private keys of
   `dgrace-6aaddbb6.rsa.pub` and `nkos-release-ec-b8e89b66.pub`), builds the
   root file system and the SD image, and creates a draft GitHub release with
   the packages, both indexes, the image, the source archive and `SHA256SUMS`.
4. Write the release notes in the draft and publish it. Publishing it
   publishes the packages (see below).
5. While devices still read `https://nkos.pesin.pro/repos/nanokvm/riscv64/`,
   upload the release's `*.apk` files, `APKINDEX.tar.gz` and `Packages.adb`
   there too: the packages first and the two indexes last, so that no index
   names a package that is not there yet, and keep older package files for
   clients with a cached index. Check the indexes from a scratch root, for
   example `apk --keys-dir DIR verify Packages.adb` with only one of the keys
   in `DIR`.

A pull request that changes the release process, or a manual run of the
workflow, builds the same release with local test keys and keeps it as an
artifact. A local build signs the same way with
`platform/build.sh -o OUTPUT -k KEY.rsa -e KEY.key`, each private key next to
its public key.

The APK repository workflow deploys the same repository to GitHub Pages, at
`https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb`,
whenever a GitHub release that carries the packages, both indexes and
`SHA256SUMS` is published. The workflow
(`.github/workflows/apk-repository.yml`, `scripts/assemble-apk-repo.py`) takes
the indexes of the newest such release and the packages that the indexes of
the newest three releases name, checks each file against its release's
`SHA256SUMS` and the index, and verifies the result with Alpine's apk before it
deploys. The page `https://dormancygrace.github.io/NanoKVM-OS/` lists the
repository line and the public keys.

### Moving to the ECDSA key alone

- While devices on 2.0 or earlier may still update, keep publishing
  `APKINDEX.tar.gz` signed with the RSA key, and keep the RSA signature on
  `Packages.adb`. Such a device can update from the v2 index; that update
  installs `nanokvm-keys` and moves it to the v3 index.
- Once those devices have updated, a later release may sign `Packages.adb` with
  the ECDSA key alone (remove the RSA `--sign-key` from the packages step of
  `platform/build.sh`). A device that still reads only the v2 index then keeps
  working as long as `APKINDEX.tar.gz` is published; after that it needs the
  ECDSA key and the new repository line by hand.
- The packages themselves stay signed with the RSA key, which `apk verify` and
  `apk add ./file.apk` check. Keep `dgrace-6aaddbb6.rsa.pub` in `keys/` while
  packages are signed with it.

### Replacing a key

1. Create the new key on the signing machine, add its public key to `keys/`,
   and publish a release signed with the old key. `nanokvm-keys` ships the new
   key; devices that install the release trust both keys. `platform/build.sh`
   refuses `-k` or `-e` keys whose public key is not in `keys/`, so a key
   cannot be used before it is shipped.
2. Sign the following releases with the new key. During a transition, sign each
   index with both keys, so that a device that skipped step 1 can still update:
   `Packages.adb` with two `--sign-key` options, and `APKINDEX.tar.gz` with a
   second signature in front, `abuild-sign -t RSA256 -k old.rsa APKINDEX.tar.gz`.
   apk-tools 3.0.8 accepts an index with two signatures if it trusts either key.
3. In a later release, remove the old key from `keys/`; updating `nanokvm-keys`
   removes it from devices.

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
Checksum holds (`package><checksum`) also require explicit operator handling:
APK `--available` clears those holds globally, so the helper refuses to proceed
until they have been released. Reapply any required holds after the transition.
Version constraints remain under the native APK solver.
Packages whose versions are unchanged are reinstalled from the selected
repositories as well: matching `pkgrel` values alone do not prove identical
binaries. The profile is changed to `stock` only after APK succeeds. No partition changes,
rootfs replacement, backup copies or permanent update wrapper are involved.
Subsequent updates use ordinary `apk update` / `apk upgrade`. Do not run an
unrestricted `apk upgrade --available` against a partial set of repositories.

## Development staging commands

The package payloads come from the `payloads` step of `platform/build.sh`,
which writes `OUTPUT/payloads` from `platform/packages.list`. To build a
signed repository from them on an Alpine builder with a configured `abuild`
key:

```sh
PAYLOAD_ROOT=OUTPUT/payloads \
  REPODEST=work/alpine/repo \
  ./scripts/build-alpine-packages.sh stock
```

The repository consumed by APK is
`work/alpine/repo/stock/recipes/riscv64`; its parent
`work/alpine/repo/stock/recipes` is the repository URL because APK appends
the target architecture. This helper writes only the v2 index; the packages
step of `platform/build.sh` also writes the signed v3 index `Packages.adb`.
