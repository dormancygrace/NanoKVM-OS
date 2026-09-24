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

## Sophgo `devmem` dependency

Sophgo `libsys.so` invokes `devmem ADDRESS 32 VALUE` during `CVI_SYS_Init`.
Alpine's BusyBox and busybox-extras do not supply this applet, so build the
single-applet BusyBox executable before preparing release payloads:

```sh
NANOKVM_BUILDROOT_OUTPUT=/path/to/buildroot-output \
  ./scripts/build-busybox-devmem.sh /path/to/devmem-output
python3 scripts/prepare-alpine-release-payloads.py \
  --port-payloads /path/to/port-payloads \
  --accepted-root /path/to/accepted-root \
  --server /path/to/server-final --web /path/to/web \
  --devmem /path/to/devmem-output/devmem \
  --output /path/to/release-payloads
```

The build pins upstream BusyBox 1.36.1 and its source checksum, enables only
the `devmem` applet, and compiles it with `-O2` for RV64GC/musl. The base APK
owns `/usr/sbin/devmem` and carries the BusyBox GPL-2.0 license. It does not
replace Alpine's `/bin/busybox`. The missing command is a confirmed dependency;
restoring it alone does not establish the cause of a particular VI failure.

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

The exact BusyBox source archive for the shipped devmem executable is available at `firmware/sources/busybox-1.36.1.tar.bz2`; use `scripts/build-busybox-devmem.sh` to reproduce it.
