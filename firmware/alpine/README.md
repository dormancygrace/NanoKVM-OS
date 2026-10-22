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

1. the signed NanoKVM repository on GitHub Pages, read through its v3 index
   (`https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb`; see [signing keys](#signing-keys));
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
| `dgrace-6aaddbb6.rsa.pub` | RSA 4096 | Verification of historical packages and indexes only |
| `nkos-release-ec-b8e89b66.pub` | ECDSA P-256 | All new core packages, `APKINDEX.tar.gz` and `Packages.adb` from v3.4.0 |

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

- `APKINDEX.tar.gz`, the v2 index, contains the core abuild packages.
- `Packages.adb`, the apk-tools v3 index, also includes independent add-ons.

From v3.4.0 both indexes and every newly built core APK are signed only with
ECDSA. APK v2 retains the historical `.SIGN.RSA256.<public-key-name>` marker:
in apk-tools this selects SHA-256; OpenSSL selects ECDSA from the EC public key.
It is **not an RSA signature**. The build rejects non-EC or mismatched signing
keys and verifies new files using the EC public key alone. The integration test
also verifies the signatures directly with OpenSSL, rejects tampered files and
checks upgrades through both index formats. Keeping abuild preserves its
package metadata, dependencies, install scripts and triggers.

Releases up to 3.2 were published on `nkos.pesin.pro`; images from 2.5 on read
`https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb`. From 3.3 on the
repository is on GitHub Pages, at `https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb`. After `nanokvm-keys` is installed
or upgraded, its script moves either earlier official line there, once the keys
are in place; Alpine's lines and any other line stay as they are. 3.3 is also
published on `nkos.pesin.pro`, so that devices still reading it find the
release that moves them.

apk also checks a downloaded package against the hash in its trusted index.
Independent add-ons may be unsigned; their checked bytes are authenticated by
the ECDSA-signed v3 repository index. They do not require an OS release.

### Publishing a release

Release reviewed changes from main, with device checks appropriate to the
changes before publication:

- **Patch release** (v3.4.1): fixes, whenever needed. In GitHub, Actions →
  Release → Run workflow, kind `patch` and **Use workflow from: main**. Its notes list
  the merged pull requests, and it is published as soon as the sign job is approved.
- **Minor release** (v3.5.0): new features, about once a month. Kind `minor`;
  it stays a draft until its notes are written and it is published.
- **A patch of the current line from a branch**, when main already holds
  changes that should not ship yet: create `release/X.Y` from the newest
  `vX.Y.Z` tag (`git push origin vX.Y.Z:refs/heads/release/X.Y`), merge the
  fix into main and cherry-pick it there, and run a patch release with
  `release/X.Y` selected in **Use workflow from**. Devices follow one
  repository, the newest release, so this
  works only while X.Y is the newest line; the workflow refuses it otherwise.
  It also needs a tag that contains this release workflow (from the first
  release made with it on), since older trees ignore the computed version.

#### Stable and beta channels

The workflow's **channel** selector is independent of its source branch:
`stable` (default) publishes normal releases, while `beta` publishes opt-in
previews. Keep developing on `main`; `release/X.Y` remains an optional source
branch for fixes. Separate Git branches named stable and beta are not needed.

For example, after v3.3.4, select `kind: minor`, `channel: beta` to build
**v3.4.0-beta.1** (APK **3.4.0_beta1-r0**); repeat for beta.2. Select the same
kind with `channel: stable` to build **v3.4.0**. Drafts reserve beta numbers;
betas do not advance the next stable version. Beta releases are GitHub
prereleases and never replace Latest. Patch/minor draft behavior and signing
approval are the same in both channels. A beta for an older target than the
existing beta series is refused.

Pages publishes two independently indexed and signed feeds:

- Stable: `https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb`
- Beta: `https://dormancygrace.github.io/NanoKVM-OS/beta/alpine/v3.24/riscv64/Packages.adb`

Stable excludes every prerelease (and beta tags even if mislabelled on GitHub).
Beta selects the highest stable or beta version; the final release outranks its
betas. Before the first beta it mirrors Stable. Each feed retains packages
referenced by its six newest eligible releases. Both include independently
published stable add-ons. The root latest-image badge stays stable; the
`beta/latest-image/` page can also point to beta images.

Release assembly compares package contents against the newest tested manifest
from the beta feed, which includes Stable. Unchanged stable packages retain
their versions; a stable build always rebuilds a package previously versioned
as a prerelease. This also replaces an experimental change that was reverted
before release. If the beta manifest is not published yet (HTTP 404), assembly
falls back to the stable manifest and fetches reused APKs from that same feed.
Network failures and other HTTP errors stop the build; they do not silently
trigger a full rebuild.

The UI changes only the official NanoKVM line in `/etc/apk/repositories`, keeps
Alpine and unrelated repositories, and refuses ambiguous/custom NanoKVM feeds.
It clears old offers and refreshes the new channel. Active updates, APK
transactions and notice checks prevent a concurrent switch. Switching alone does not install packages. After selecting Stable with a
prerelease installed, **Install stable version** performs a confirmed rollback
through the detached update helper. APK refreshes and verifies the official
stable index, requires the complete core set, simulates dependency resolution,
then installs a temporary `.nanokvm-stable-rollback` virtual package requiring
exact versions of the thirteen core packages. The helper removes this constraint
after the transaction. Before installing, it durably registers a small OpenRC
boot service under `/etc/init.d/nanokvm-rollback-recovery`. After an interrupted
rollback this service removes the constraint offline, even if the restored
stable updater predates channels; it removes itself only after success. Normal
completion removes the service immediately. The new updater also attempts
cleanup before a periodic notice check (without interrupting active updates). Independent add-ons and Alpine packages are not rollback targets;
APK preserves their versions unless dependencies require a change. Existing
world holds remain intact; conflicting holds make the solver refuse the rollback. Normal package scripts
retain protected settings/user data and mark kernel/bootloader changes for
reboot. This is a package rollback, not a disk snapshot: future incompatible
configuration/data migrations must provide a backward migration before a beta
is published. If the web UI is unusable, select the stable repository in
`/etc/apk/repositories` over SSH, then run `nkos-update apk rollback`.

Deploy `apk-repository.yml` from the new main **before publishing the first OS
release with this selector** so the beta URL already exists. This deployment
needs the usual signing approval; it does not release a new OS. Then run the
Release workflow with `channel: beta`, `dry_run: true` to check the candidate
before a real beta publication. A new beta image receives e.g. `v1.6-beta.1`
and defaults to the beta feed; stable images and existing installations default
to stable. For local builds, set `NANOKVM_CHANNEL=beta` together with matching
`NANOKVM_VERSION`/`NANOKVM_APK_VERSION` and image versions.

Use **Use workflow from** to select the source branch; there is no separate
checkout ref. Every job uses the same commit selected when the run starts.

The workflow (`.github/workflows/release.yml`) takes the next version after the
newest eligible release (`scripts/next-release.py`; drafts count), builds everything on
a GitHub runner without the signing keys, and then waits in the `release`
environment until a maintainer approves the sign job. That job signs the
packages that changed and both indexes with the ECDSA release key (environment
secret `APK_EC_KEY`: the unencrypted PEM private key matching
`nkos-release-ec-b8e89b66.pub`). Public OS releases contain the corresponding-source
archive, `SHA256SUMS`, and the SD image when requested. Packages and metadata go
to a separate technical release in this same repository (see below). An unchanged
package keeps its version and stays in the release that first carried it
([package versions](packages/README.md#package-versions)).

The SD image is always built, as a check that the packages install from
scratch, but attached only with `image` checked: when first boot, the card
layout or installation changes, since a new card runs the image's code until
its first update. The bootloader needs no image; its package updates it.
New image numbers also reserve those held by drafts. Release notes start
from a published tag, never an unpublished draft. Publication stages both asset
sets as drafts, publishes package storage first, and only then publishes the OS
release (or leaves its draft ready for review). Retrying an already published
OS release only restarts the repository update and does not replace public assets.
The page of the APK repository links the newest image for new cards.

A pushed `vX.Y.Z` tag releases with the versions of
`firmware/alpine/release.env`, as a draft with the image. While devices still
read `https://nkos.pesin.pro/repos/nanokvm/riscv64/`, upload each release's
`*.apk` files, `APKINDEX.tar.gz` and `Packages.adb` there too: the packages
first and the two indexes last, keeping older package files for clients with a
cached index.

A pull request that changes the release process, or a run with `dry_run`,
builds the same release with local test keys and keeps it as an artifact. A local build signs the same way with
`platform/build.sh -o OUTPUT -e KEY.key`, with `KEY.pub` next to it.

The APK repository workflow deploys the same repository to GitHub Pages, at
`https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb`,
when an OS or supported independent add-on release is published. The release
publisher also dispatches this workflow from main because events created with
`GITHUB_TOKEN` do not start another workflow. Manual release events also
dispatch assembly from main, so deployment satisfies the `github-pages`
environment branch policy instead of attempting to deploy from a tag. The workflow
(`.github/workflows/apk-repository.yml`, `scripts/assemble-apk-repo.py`) takes
the indexes of the newest eligible release in each channel (the highest version) and the packages that the indexes of
the newest six releases name, checks each file against its release's
`SHA256SUMS` and the index, and verifies the result with Alpine's apk before it
deploys. The page `https://dormancygrace.github.io/NanoKVM-OS/` lists the
stable repository line and the public keys; `/beta/` lists the beta feed.

### Public downloads and technical package storage

New OS releases separate their files into two releases in **NanoKVM-OS**:

| Release | Attached files |
| --- | --- |
| `vX.Y.Z` (or `vX.Y.Z-beta.N`) | Corresponding-source archive, optional SD image ZIP, and `SHA256SUMS` covering only those downloads |
| `os-packages-vX.Y.Z` (or `os-packages-vX.Y.Z-beta.N`) | Changed APKs, both indexes, `packages.json`, `addons.json`, installed package/license lists, image build manifest when applicable, and its own `SHA256SUMS` |

GitHub also provides its automatic source ZIP/tarball on each tag. Those do not
replace the complete corresponding-source archive. Technical releases are
labelled **Package storage**, marked as prereleases and explicitly excluded
from Latest. They still appear in GitHub's releases list; this is storage in
the same repository, not a hidden asset service.

The assembly artifact has `assets/public/` and `assets/packages/` directories,
including in dry runs. The publisher preserves edited OS release notes and
appends a hidden `nanokvm-package-storage` comment. Pages pairs releases by the
exact `os-packages-` tag convention; the comment also makes missing storage an
error instead of silently serving an older version. Keep the comment when
editing notes. A published storage release without its published OS release
is ignored. A minor OS draft can therefore be published manually after review:
its technical files are already complete. The OS tag and prerelease flag, not
the storage release's flag, decide Stable/Beta membership.

Old all-in-one releases remain readable and are not moved. Device repository
URLs, keys, package names and update/rollback commands do not change. Pages
retains each channel's six newest package sets and can retrieve an unchanged
APK from **any older eligible release**, including old technical storage.
**Keep old technical releases and their tags even outside the six-release
window.** Independent add-on releases remain independent and continue to
augment both channels.

Uploads only replace draft assets while storage is unpublished. A rerun of the
`sign` job reassembles and re-signs packages and indexes; randomized ECDSA
signatures can change their bytes. The image is also rebuilt from those packages.
Once storage is published, the publisher resumes from the **original remote
pair**: it verifies the storage inventory, hashes and package manifest, then the
public draft's image/source inventory and hashes, and finishes publication
without uploading freshly assembled files. A retry needs neither a saved local
assembly nor an artifact of the signed output. Corrupt or incomplete original
assets stop publication; a new build is never silently mixed with old storage.
If a maintainer publishes the OS draft before the storage publication completes,
rerunning the publisher verifies every uploaded storage file against its
SHA256SUMS and checks the changed APK set against `packages.json`, then publishes
the complete storage draft before dispatching Pages. It uses the original remote
assets; local build output is not needed on this recovery path. Missing or
corrupt storage stops with its tag in the error; restore its original files
before retrying. The public OS release is not modified.

To discard and rebuild an **OS version that has never been published**, discard
both its OS draft and its `os-packages-<version>` release, plus both associated
tags if present. Check that no published OS version used this storage first.
Removing only the OS draft is insufficient: the allocator proposes the same
version again, while the surviving published storage rejects a rebuilt set of
bytes. This is a deliberate manual cleanup, never an automatic deletion by the
publisher. Do not apply this procedure to a version that has ever been published;
issue a new patch/beta version instead, retaining its existing files for clients.

If only the Pages dispatch failed after OS publication, rerunning the publisher
or dispatching `apk-repository.yml` from main is sufficient. Existing published
releases are never overwritten by this publisher.

Merge the assembler and publisher together before the first split release;
release branches must carry both changes. Make the first split publication a
**beta** and verify the real GitHub asset pairing before a stable release:
confirm that the technical release is published, both feed manifests and APKs
verify, the beta feed selects the new version, Stable stays on its previous
version, and the image/checksum links point to the public beta release. CI's
current live-inventory check covers only legacy releases until the first split
release exists; fixture tests do not replace this rollout check. Both channels
are deployed together, so a pairing failure blocks the entire new Pages
deployment while preserving the previously deployed Stable and Beta feeds.
A failure to resolve or verify a
published release's storage stops Pages assembly before deployment, leaving
the currently deployed repository in place.

### ECDSA-only signing from v3.4.0

The release and repository workflows no longer read `APK_RSA_KEY`. The signing
policy is part of each core package's content fingerprint, so v3.4.0 rebuilds
all thirteen core packages once; a previous RSA-signed package cannot be reused
as the current release's package. Later releases resume ordinary content reuse.
Historical RSA-signed files and their public key are retained for verification
and rollback; no new RSA signature is created.

Devices on 3.3.x already have the EC key and the Pages repository URL. They
update normally. Devices that still trust **only RSA** must first install a
historical transition release (2.5 through 3.3.x), or install the published EC
public key and current repository URL manually. New indexes cannot be verified
with the RSA key alone; keeping a v2 index does not change this requirement.

### Replacing a key

1. Create the new key on the signing machine, add its public key to `keys/`,
   and publish a release signed with the old key. `nanokvm-keys` ships the new
   key; devices that install the release trust both keys. `platform/build.sh`
   refuses `-e` keys whose public key is not in `keys/`, so a key
   cannot be used before it is shipped.
2. Sign the following releases with the new key. During a transition, sign each
   index with both keys, so that a device that skipped step 1 can still update:
   `Packages.adb` with two `--sign-key` options, and `APKINDEX.tar.gz` with a
   second signature in front, `abuild-sign -t RSA256 -k old.key -p old.pub APKINDEX.tar.gz`
   with an ECDSA key.
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
