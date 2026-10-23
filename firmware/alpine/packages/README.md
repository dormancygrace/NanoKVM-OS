# NanoKVM APK packages

These recipes are intentionally small. They package board specific files
already produced by the NanoKVM build into normal Alpine APKs; Alpine's own
`main` repository continues to provide the ordinary userspace (`community` is
optional, see [the repository list](../README.md)).

The recipes consume one prepared source archive per package. The helper
`scripts/build-alpine-packages.sh` creates those archives from a payload tree
and invokes `abuild` in dependency order.

## Payload layout

Set `PAYLOAD_ROOT` to a directory containing these subdirectories:

```
keys/                    etc/apk/keys/*.pub (default: firmware/alpine/keys)
base/                    files installed below /
kernel-sg2002/           FIT, DTB and kernel payload below /usr/lib/nanokvm/boot
kmod-sg2002/             lib/modules/<kernel-release>/
firmware-sg2002/         lib/firmware/ and optional usr/share/ data
app/                      kvmapp/ and application files below /
zlib/                     usr/lib/libz.so.1 (zlib-ng) and its license
release/                  etc/nanokvm-release, kvmapp/version and build profile metadata
```

The helper also accepts `PAYLOAD_ROOT/stock/...`. Files containing device
identity, private keys, `/data`, or user settings must not be put in these
payloads; `nanokvm-keys` refuses anything but public keys in `/etc/apk/keys`.
`nanokvm-base` depends on `nanokvm-keys`, which owns the repository keys (see
[signing keys](../README.md#signing-keys)).

## Package versions

Each package has its own version: the release in which its content last
changed. The content is the payload tree (paths, modes, links and file
contents), the recipe directory and the signing-policy identifier.
`scripts/package-versions.py` compares their combined fingerprint with
`packages.json` from the previous release. The ECDSA transition in v3.4.0
changes that identifier once, rebuilding every core package:

- an unchanged package keeps its published version, and the release reuses its
  published file instead of rebuilding it;
- a changed package gets the release's APK version;
- `nanokvm-release` always changes, since it carries the release version;
- `nanokvm-kernel-sg2002` and `nanokvm-kmod-sg2002` change together, because
  the kernel requires its modules at exactly its own version.

`nanokvm-app` and `nanokvm-release` depend on the versions chosen for the other
packages (`V_nanokvm_web` and so on in the build environment). The release
requires every selected component version directly, including the server and
web UI, because a reused `nanokvm-app` retains its older dependencies. A device may
therefore show `nanokvm-web` 3.3.0 next to `nanokvm-release` 3.5.0. The packages
step reads the previous repository from `NANOKVM_PREVIOUS`, a URL or a
directory with `packages.json`; without it every package gets the release
version, as before. It writes `packages.json` next to the indexes.

## Bootloader

`nanokvm-bootloader-sg2002` carries `fip.bin` (Sophgo FSBL, OpenSBI, U-Boot)
and the licenses of its parts. Its trigger puts the file on the boot partition
without ever leaving it with a partly written `fip.bin`; see
[docs/BOOT.md](../../../docs/BOOT.md#updating-fipbin). A device opts out with
`/etc/kvm/bootloader-update.disabled`.

## zlib-ng in place of Alpine's zlib

`nanokvm-zlib` installs zlib-ng in zlib-compatible mode as `libz.so.1`, which
apk, curl, OpenSSH, kmod and eudev load. Built for the C906 (see
`docs/component-build-profiles.md`), it inflates about 36% faster than Alpine's
zlib 1.3.2 on the device (measured with zlib-ng 2.3.3), and with the pinned
develop commit `apk verify` of Alpine's python3 package takes 16% less time. The package provides `zlib` and `so:libz.so.1` and replaces `zlib`, so
installing it, or upgrading to a `nanokvm-release` that depends on it, removes
Alpine's `zlib` in the same transaction. `zlib-dev` asks for an exact `zlib`
version and cannot be installed next to it.

The build pins a zlib-ng develop commit (see `platform/sources.lock`): no
zlib-ng release yet exports the zlib 1.3.2 API (`*_z` functions,
`deflateUsed`) that Alpine's zlib 1.3.2 offers. `platform/build.sh` compares
the dynamic symbols of the built library with `nanokvm-zlib/zlib-1.3.2.symbols`
(taken from Alpine's `libz.so.1.3.2`) and fails if any is missing. Move the pin
to the first release that has this API, and refresh the symbol list when
Alpine moves to a newer zlib.

## Sophgo `devmem` dependency

Sophgo `libsys.so` invokes `devmem ADDRESS 32 VALUE` during `CVI_SYS_Init`.
Alpine's BusyBox and busybox-extras do not supply this applet, so the `tools`
step of `platform/build.sh` builds a single-applet BusyBox executable with
`scripts/build-busybox-devmem.sh`.

The build pins upstream BusyBox 1.38.0 (labelled unstable by upstream) and its
source checksum, enables only
the `devmem` applet, and compiles it with `-O2` for RV64GC/musl. The base APK
owns `/usr/sbin/devmem` and carries the BusyBox GPL-2.0 license. It does not
replace Alpine's `/bin/busybox`. The missing command is a confirmed dependency;
restoring it alone does not establish the cause of a particular VI failure.

## Local build

On an Alpine riscv64 or x86_64 builder with `abuild` configured:

```sh
PAYLOAD_ROOT=/path/to/payloads \
  REPODEST=/path/to/repo \
  PACKAGER_PRIVKEY=/path/to/release.key \
  PACKAGER_PUBKEY=/path/to/release.pub \
  ./scripts/build-alpine-packages.sh stock
```

`platform/build.sh payloads` produces `stock` payloads, the only profile the
package builder accepts. Mismatched release metadata is rejected.

The APK repository URL is `REPODEST/stock/recipes`; package files and the
signed index are below its `riscv64` architecture directory. The script does not install
packages, modify a device, or contact the NanoKVM.

The helper copies each recipe to a temporary build directory and runs
`abuild checksum` there before `abuild -r`. This keeps checksums out of the
working tree while retaining normal APKBUILD checksum verification. The final
repository check requires a valid `APKINDEX.tar.gz` and one APK per recipe;
package and index signing use the validated ECDSA key and SHA-256. The v2
archive marker remains `RSA256` because apk-tools uses it to select SHA-256;
the actual algorithm is ECDSA, determined by the EC key. New files are verified
with the EC public key alone. See the [signing policy](../README.md#signing-keys).

The kernel APK stages all board FITs and their hashes below
`/usr/lib/nanokvm/boot` and has an exact dependency on the matching module APK.
On a running OpenRC system its trigger validates the detected board profile,
payload hash and module `vermagic`, runs `depmod`, and writes the selected FIT to
`/boot/boot.sd`. It then creates `/run/reboot-required`; the running kernel is
unchanged until the operator reboots. The trigger skips image assembly, where no
OpenRC softlevel exists and the image builder selects the detection FIT directly.

This makes kernel and module revisions normal signed repository updates while
keeping them in one APK transaction. It does not update the partition table,
filesystem, FIP or bootloader. Those changes continue to use a complete image or
the recovery installer.

The exact BusyBox source archive for the new devmem executable is available at
the checksum-pinned URL in `platform/sources.lock`; use
`scripts/build-busybox-devmem.sh` to reproduce it. `platform/build.sh source`
includes that archive in the release corresponding-source bundle. Publish the
matching source bundle alongside the image and packages; the upstream URL alone
is not the release source bundle. The historical
`firmware/sources/busybox-1.36.1.tar.bz2` is retained for the devmem executable
shipped with v2.0-b4 and is not an input to new builds.

## Add-ons released on their own

An add-on has a version of its own and releases of its own, `NAME-VERSION-rN`,
apart from the OS releases. The APK repository workflow indexes every such
release of the add-ons in `ADDONS` (`scripts/addon_packages.py`), so devices
install and update an add-on without an OS release. An add-on is never built
with the OS packages: there it would get the release's version, above all of
its own later versions.

- RustDesk: `addons/rustdesk`, built and released by the RustDesk add-on
  workflow (`.github/workflows/rustdesk.yml`).
- A Go program of `server/` (IPMI): a recipe `NAME/addon.json` here (version,
  minimum OS release, metadata, the program, files, lifecycle scripts,
  licenses), built by
  `scripts/build-addon.py` and released by the add-on release workflow
  (`.github/workflows/addon.yml`; Actions → Add-on release → Run workflow).

An add-on states the first NanoKVM OS release it works with: `"os": "3.5.0"`
in its recipe becomes the dependency `nanokvm-os>=3.5.0`, which
`nanokvm-release` provides as `nanokvm-os=VERSION` from 3.5.0 on. Raise it when
the add-on starts to use something newer of the OS. A beta has the APK version
`3.5.0_beta1`, below 3.5.0: a minimum of `3.5.0` excludes the betas of 3.5.0;
name the first beta (`3.5.0_beta1`) when the add-on already works there, and
the stable release satisfies it too. RustDesk states its needs
as capabilities of `nanokvm` instead (`nanokvm-rustdesk-bridge=1`, ...), which
3.4.0 already provides.

Devices find add-ons in the index, without a list of their own: an add-on is
any `nanokvm-*` package of a NanoKVM repository (an `/etc/apk/repositories`
line whose URL contains "nanokvm") outside `nanokvm-release` and its
dependencies. The update check stores them from the NanoKVM index it has
just fetched, so a published add-on appears on Software > Add-ons with the
next daily check (or Check now there), without an OS release. Before an
install the server refuses an add-on whose minimum release is above the
installed OS, and any transaction that `apk add --simulate` shows would
change an OS package: apk would meet the minimum by upgrading the whole OS.

A new add-on goes into `ADDONS`, gets a recipe and a choice in `addon.yml`.
To release a change, raise the recipe's version and run the workflow from
main; each run of the same version gets the next revision. The workflow opens
a pin PR for `firmware/alpine/addons.json` (below), which replaces that
add-on's pin only.

## Optional add-ons in the release repository

`firmware/alpine/addons.json` pins already-signed optional APKs from their
separate releases by URL, SHA-256 and size. The package step downloads them
after generating the core v2 index and before generating the v3 index; APK
verifies their release signatures. Native APK v3 add-ons cannot be represented
in the legacy v2 index.
They are not dependencies of `nanokvm-release` and are not installed in images.

CI attaches `addons.json`, not the add-on APK, to the OS release. Pages checks
this manifest against the release's `SHA256SUMS`, fetches each indexed add-on
from its own release and checks its hash and index size. When releasing a new
add-on revision, update this pin before the next OS repository publication.
Keep old add-on release assets available for cached indexes.
