# NanoKVM APK packages

These recipes are intentionally small. They package board specific files
already produced by the NanoKVM build into normal Alpine APKs; Alpine's own
`main` and `community` repositories continue to provide the ordinary userspace.

The recipes consume one prepared source archive per package. The helper
`scripts/build-alpine-packages.sh` creates those archives from a payload tree
and invokes `abuild` in dependency order.

## Payload layout

Set `PAYLOAD_ROOT` to a directory containing these subdirectories:

```
base/                    files installed below /
kernel-sg2002/           FIT, DTB and kernel payload below /usr/lib/nanokvm/boot
kmod-sg2002/             lib/modules/<kernel-release>/
firmware-sg2002/         lib/firmware/ and optional usr/share/ data
app/                      kvmapp/ and application files below /
release/                  etc/nanokvm-release and build profile metadata
```

The helper also accepts `PAYLOAD_ROOT/<profile>/...` (for example
`PAYLOAD_ROOT/stock/app` and `PAYLOAD_ROOT/c906-scalar/app`) so stock and tuned
repositories can be generated from the same checkout. Files containing device
identity, keys, `/data`, or user settings must not be put in these payloads.

## Local build

On an Alpine riscv64 or x86_64 builder with `abuild` configured:

```sh
PAYLOAD_ROOT=/path/to/payloads \
  REPODEST=/path/to/repo \
  ./scripts/build-alpine-packages.sh stock
```

The APK repository URL is `REPODEST/stock/recipes`; package files and the
signed index are below its `riscv64` architecture directory. A C906 repository
is built with `c906-scalar` as the profile. The script does not install
packages, modify a device, or contact the NanoKVM.

The helper copies each recipe to a temporary build directory and runs
`abuild checksum` there before `abuild -r`. This keeps checksums out of the
working tree while retaining normal APKBUILD checksum verification. The final
repository check requires a valid `APKINDEX.tar.gz` and one APK per recipe;
package and index signing are performed by the configured abuild key.

The kernel APK deliberately does not replace `/boot/boot.sd` from an ordinary
package transaction. It stages the matched FIT and metadata below
`/usr/lib/nanokvm/boot`; the coordinated firmware/update bundle installs the
normal FIT together with the matching root, modules and firmware. This keeps
application APK upgrades ordinary while making a kernel transition one
hash-bound firmware operation.
