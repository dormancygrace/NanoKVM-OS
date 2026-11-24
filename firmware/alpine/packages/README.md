# NanoKVM APK packages

These recipes are intentionally small. They package board specific files
already produced by the NanoKVM build into normal Alpine APKs; Alpine's own
`main` repository continues to provide the ordinary userspace (`community` is
listed too, see [the repository list](../README.md)).

The recipes consume one prepared source archive per package. The helper
`scripts/build-alpine-packages.sh` creates those archives from a payload tree
and invokes `abuild` in dependency order.

## Payload layout

Set `PAYLOAD_ROOT` to a directory containing these subdirectories:

```
keys/                    etc/apk/keys/*.pub (default: firmware/alpine/keys)
base/                    files installed below /
kernel/           FIT, DTB and kernel payload below /usr/lib/nanokvm/boot
kmod/             lib/modules/<kernel-release>/
firmware/         lib/firmware/ and optional usr/share/ data
app/                      kvmapp/ and application files below /
zlib/                     usr/lib/libz.so.1 (zlib-ng) and its license
openssl/                  OpenSSL command, ABI3 libraries, engines, legacy provider and configuration
release/                  etc/nanokvm-release, kvmapp/version and build profile metadata
```

The helper also accepts `PAYLOAD_ROOT/stock/...`. Files containing device
identity, private keys, `/data`, or user settings must not be put in these
payloads; `nanokvm-keys` refuses anything but public keys in `/etc/apk/keys`.
`nanokvm-base` depends on `nanokvm-keys`, which owns the repository keys (see
[signing keys](../README.md#signing-keys)).

## Package versions

Most packages carry the OS release in which their content last changed. The content is the payload tree (paths, modes, links and file
contents), the recipe directory and the signing-policy identifier.
`scripts/package-versions.py` compares their combined fingerprint with
`packages.json` from the previous release. The ECDSA transition in v3.4.0
changes that identifier once, rebuilding every core package:

- an unchanged package keeps its published version, and the release reuses its
  published file instead of rebuilding it;
- most changed packages get the release's APK version;
- `nanokvm-release` always changes, since it carries the release version;
- `nanokvm-kernel` and `nanokvm-kmod` share the upstream Linux version and
  revision. A change to either rebuilds the pair;
- `nanokvm-openssl3` and `nanokvm-exfatprogs1` use the upstream OpenSSL and
  exfatprogs versions. Their revisions and the Linux package revision identify the OS build that last changed their content.
  The decimal revision encodes OS major, three-digit minor, three-digit patch
  and three-digit beta number (999 for stable). This keeps maintenance releases
  and beta builds ordered even when published on different branches. The first
  published beta of each OS version builds these four packages so a device on a later
  maintenance release can still upgrade to the published beta.

`nanokvm-release` depends directly on the selected versions of all components
(`V_nanokvm_web` and so on in the build environment), including the server and
web UI. It provides the old `nanokvm-app` name at its own version, so existing
world selections do not require a separate compatibility package. A device may
therefore show `nanokvm-web` 3.3.0 next to `nanokvm-release` 3.5.0. The packages
step reads the previous repository from `NANOKVM_PREVIOUS`, a URL or a
directory with `packages.json`; without it every package is built. It writes `packages.json` next to the indexes.

The old `nanokvm-exfatprogs` name forwards explicit APK world entries to
`nanokvm-exfatprogs1`. Fresh installs depend directly on the latter.

## Bootloader

`nanokvm-bootloader` carries `fip.bin` (Sophgo FSBL, OpenSBI, U-Boot)
and the licenses of its parts. Its trigger puts the file on the boot partition
without ever leaving it with a partly written `fip.bin`; see
[docs/BOOT.md](../../../docs/BOOT.md#updating-fipbin). A device opts out with
`/etc/kvm/bootloader-update.disabled`.

## zlib-ng in place of Alpine's zlib

`nanokvm-tpu` installs `nanokvm-ocr` and `nanokvm-screen`, the programs
that run models on the TPU, linked statically with Sophgo's runtime (the `tpu`
step, `tpu/README.md`). It provides `nanokvm-tpu-abi=1`, which the model
add-ons depend on; the release depends on it, so an update installs it.

`nanokvm-zlib` installs zlib-ng in zlib-compatible mode as `libz.so.1`, which
apk, curl, OpenSSH, kmod and eudev load. Built for the C906 (see
`docs/COMPONENT-BUILD-PROFILES.md`), it inflates about 36% faster than Alpine's
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

## OpenSSL runtime

`nanokvm-openssl3` replaces Alpine's `openssl`, `libssl3` and `libcrypto3`
with the unmodified OpenSSL 3.5.9 LTS sources pinned in `platform/sources.lock`.
It provides their package names and ABI3 SONAMEs, so curl, OpenSSH and apk
continue to use their existing linkage. As with `nanokvm-zlib`, the release depends on the named
NanoKVM package at its selected minimum revision.
Alpine development, static and FIPS packages require an exact upstream package
version and are not supplied by this runtime package.

`scripts/build-openssl.py` uses the shared C906 profile with `-O3`, disables
compiler vectorization, removes `xtheadvector` from the baseline ISA, and uses
scalar inline memory operations. Upstream
assembly and runtime CPU dispatch are retained; no NanoKVM crypto assembly,
CryptoDMA hook or engine activation is added. Alpine's feature switches and
configuration paths are retained. The package contains the legacy provider and
built engines, plus the build flags and pinned-source metadata under
`/usr/share/nanokvm/openssl/`.

The pin follows the OpenSSL 3.5 LTS branch, supported upstream through
8 April 2030. The provided package revision `r100` only outranks
Alpine revisions of the same upstream version. Provider priority does not pin
a newer versioned alias. The named `nanokvm-openssl3>=...` dependency in
`nanokvm-release` keeps the selected runtime installed even when Alpine offers
a higher upstream version; signed APK tests cover that case during fresh
installation and upgrade. Requests requiring a newer library version fail
until NanoKVM publishes a compatible runtime. Do not raise the provided version
without updating the source.

NanoKVM maintainers own OpenSSL security updates for this runtime. They must
review upstream 3.5 LTS security/patch releases, update the source checksum and
provided upstream versions together, rerun compatibility and correctness tests,
and publish a signed OS/package update. An Alpine security update alone does
not update `nanokvm-openssl3`. The upstream monitor
reports patch releases only within 3.5.x and requests review before changing
the qualified pin and package aliases.

Older installations may list `nanokvm-openssl` in APK world. This name now
installs an empty compatibility package depending on `nanokvm-openssl3`.
Fresh installations require the new package directly. Switching back to an
older stable repository restores the runtime shipped by that release, including
Alpine's OpenSSL on releases predating the NanoKVM runtime.

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
apart from the OS releases. The APK repository workflow puts every such
release of the add-ons in `ADDONS` (`scripts/addon_packages.py`) into the
add-on repository on Pages (firmware/alpine/README.md), so devices install and
update an add-on without an OS release. An add-on is never built
with the OS packages: there it would get the release's version, above all of
its own later versions, and an OS release would rebuild and republish it
although it has not changed.

- RustDesk: `addons/rustdesk`, built and released by the RustDesk add-on
  workflow (`.github/workflows/rustdesk.yml`).
- ZeroTier: `firmware/alpine/packages/nanokvm-zerotier`, ZeroTier One's
  agent built from ZeroTier's release archive by
  `scripts/build-zerotier-addon.py`, and released by the ZeroTier add-on
  workflow (`.github/workflows/zerotier.yml`).
- TPU models and the computer sensors' agents: a recipe `addons/NAME/addon.json`
  (version, minimum OS release, metadata, the models or the .NET program,
  files, lifecycle scripts, licenses), built by `scripts/build-addon.py` and
  released by the add-on release workflow (`.github/workflows/addon.yml`;
  Actions → Add-on release → Run workflow).

A package in `RETIRED` (`scripts/addon_packages.py`) is no longer built: an
OS package carries its files, replaces it and provides its name at its own
version, as `nanokvm-base` does for `nanokvm-ipmi`. A world entry with that
name stays satisfied, and the versioned name cannot be installed twice: an
update removes the separate package, and its files change owner, in the same
transaction. Pages keeps serving the package's last releases, for devices on a
release without the OS package's copy and for a rollback to such a release,
whose world still names it.

An add-on states the first NanoKVM OS release it works with: `"os": "3.5.0"`
in its recipe becomes the dependency `nanokvm-os>=3.5.0`, which
`nanokvm-release` provides as `nanokvm-os=VERSION` from 3.5.0 on. Raise it when
the add-on starts to use something newer of the OS. A beta has the APK version
`3.5.0_beta1`, below 3.5.0: a minimum of `3.5.0` excludes the betas of 3.5.0;
name the first beta (`3.5.0_beta1`) when the add-on already works there, and
the stable release satisfies it too. RustDesk states its needs
as capabilities of `nanokvm` instead (`nanokvm-rustdesk-bridge=2`, ...). Each
is the version of one contract between the add-on and the server (bridge 2:
input over the binary control channel, not HTTP requests): increment it in
both recipes together when the two sides stop understanding each other, so
that apk installs the add-on only next to a server it can talk to.

RustDesk, the computer sensors and the screen model (`BASE` in
`scripts/addon_packages.py`) are dependencies of `nanokvm-release`, by name
only: every device and image has them, each keeps its own version, and a new
one reaches devices with the next package update. Their pages switch them on;
nothing of them runs before. Their `nanokvm-os>=VERSION` and the dependency of
`nanokvm-release` on them form a cycle, which apk resolves in one transaction.
Raising the `"os"` of one of them ties rollback to it: a rollback to a release
below the new minimum installs an older version of the package, which Pages
keeps while it is among the newest three, and is refused without one.

The OCR models are installed on request from AI > Text recognition. Before an
install the server refuses any transaction that `apk add --simulate` shows
would upgrade, downgrade, replace or remove an installed package: apk would
meet a model's minimum OS release by upgrading the whole OS.

A new add-on goes into `ADDONS`, gets a recipe and a choice in `addon.yml`.
To release a change, raise the recipe's version and run the workflow from
main; each run of the same version gets the next revision. Publication starts
the Pages workflow to verify the package and sign the common add-on index.
It does not open a pin PR or change the OS source tree.

OS builds, signing and release storage contain only OS packages; the image
build installs the add-ons in `BASE` from the signed add-on repository.
Pages discovers add-ons from their own releases and verifies each APK against
that release's `SHA256SUMS`, then validates its metadata before signing the
index. Keep old add-on release assets available for cached indexes and rollback.
