# Alpine C906 scalar package overlay

`scripts/build-alpine-tuned-packages.sh` builds a small signed repository of
Alpine v3.24 packages rebuilt for the scalar T-Head C906 extensions. It uses
the aports commit
`92921ccb60bf1bea0d4666b2c0533ce22c5fae9e` and increments each recipe's
`pkgrel` by one. The checked-out aports tree is never modified; the script
creates a private copy of each selected `APKBUILD`.

The experimental build set is `busybox coreutils zlib lz4 zstd xz openssl`.
`musl`, `apk-tools`, the kernel, modules, vendor libraries and NanoKVM
application stay unchanged. This keeps the stock-versus-tuned comparison
focused on the CPU-sensitive userspace pieces.

## Build environment

The build must execute in an Alpine v3.24 `riscv64` rootfs. On the x86_64
builder VM, install `qemu-user-static` and `binfmt-support`, enable the
`qemu-riscv64` binfmt entry, copy `qemu-riscv64-static` into the rootfs, and
enter it with `chroot`. Inside the rootfs install `alpine-sdk`, `git`, `tar`,
`openssl` and the normal Alpine build dependencies. The script checks
`/etc/apk/arch` rather than `uname`, because qemu-user preserves the host's
`uname -m`. Run it as the unprivileged user in the `abuild` group; `abuild`
must not be run as root.

An unprivileged WSL2 probe with `proot 5.1.0` and `qemu-riscv64-static`
8.2.2 is not a supported substitute for that builder. The command shape was:

```sh
proot -R /path/to/riscv64-rootfs \
  -q /path/to/qemu-riscv64-static \
  /bin/sh
```

The target `git` could read the aports files but could not discover `.git`
because of proot path translation. After a temporary test-only archive shim,
BusyBox reached compilation, where Alpine GCC failed with
`'-fuse-linker-plugin', but liblto_plugin.so not found`; disabling that plugin
then failed to locate `Scrt1.o`, `crti.o` and `crtbeginS.o` even though the
files were present in the rootfs. The production script has no proot-specific
workarounds.

WSL2 works when the distribution is entered as root and the riscv64 rootfs is
mounted with a real `chroot`. Register `qemu-riscv64-static` through
`binfmt_misc` and export `QEMU_CPU=thead-c906` for the chroot build. The CPU
selection matters: configure scripts execute newly built target probes, and
generic qemu-riscv64 raises `SIGILL` on their XThead scalar instructions. A VM
with the same binfmt/chroot setup or a native riscv64/C906 builder is also
supported.

## Completed repository build

The full overlay was built successfully in the WSL2 real-chroot environment
with QEMU_CPU set to thead-c906. The repository contains 46 APKs from the seven
approved source packages:

| Source package | Tuned revision |
|---|---|
| busybox | 1.37.0-r32 |
| coreutils | 9.11-r1 |
| zlib | 1.3.2-r1 |
| lz4 | 1.10.0-r2 |
| zstd | 1.5.7-r3 |
| xz | 5.8.4-r1 |
| openssl | 3.5.8-r1 |

The signed legacy APKINDEX.tar.gz has 46 entries and is kept alongside the
apk-tools v3 Packages.adb. Every APK is listed once in manifest.tsv, all
package and index signatures verify with the repository public key, and the
two index hashes are recorded under
firmware/alpine/evidence/2026-09-19-full-c906. The build log is retained
outside Git under work/alpine/tuned-full-build.log because it is large.

Use a dedicated signing key supplied outside the checkout:

```sh
export SIGN_KEY=/build/keys/nanokvm-alpine-c906.rsa
export http_proxy=…
export https_proxy=…
scripts/build-alpine-tuned-packages.sh
```

`SIGN_KEY` signs both APKs (through `abuild`) and `Packages.adb`. The private
key is not copied into the output repository. The output contains the signed
index, APK SHA256 manifest, pinned aports commit, exact C906 flags and a
`repositories` file with the custom repository first, followed by Alpine
`main` and `community` fallback repositories.

The generated flags append to Alpine's existing hardening and optimization
flags:

```text
-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync
-mtune=thead-c906 -mno-fence-tso -mabi=lp64d
```

There is deliberately no `V`, `GCV`, `v0p7` or `xtheadvector`. APK tests are
disabled in the generated copies because qemu-user is not a C906 emulator.
Functional checks, output hashes and performance measurements belong on the
physical NanoKVM. The script does not claim QEMU timings.

After the build, copy the public key to the NanoKVM image's `/etc/apk/keys`
and use the generated `repositories` file. The repository's higher `pkgrel`
selects the tuned build when its package version matches the Alpine fallback;
Alpine remains the fallback for packages outside this set and for a tuned name
when that package is absent from the custom repository.

## Qualified package set

The physical same-card comparison qualifies `busybox`, `coreutils`, `lz4`,
`zstd` and `openssl` for the default C906 overlay. `xz` is excluded because its
64 MiB encode median regressed by 1.57%. `zlib` is excluded because the test
matrix did not isolate libz; BusyBox supplies the measured gzip command.

The qualified repository is derived only from the already signed APKs in the
full experimental repository. Its indexes, manifest and checksums are
regenerated after filtering so APK cannot select an omitted higher revision.
Official Alpine `main` and `community` remain after it in the repository list,
which supplies stock `xz`, `zlib` and every package outside the overlay. Device
results and the selection rationale are recorded in
`firmware/alpine/evidence/2026-09-19-full-c906`.
