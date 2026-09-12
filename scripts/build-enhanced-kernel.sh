#!/bin/bash
# Compile milestone only: no packaging, installation or flashing.
set -euo pipefail
export PATH=${NANOKVM_HOST_PATH:-/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin}
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
: "${NANOKVM_KERNEL_SOURCE:?Set the patched Linux 7.2.5 source directory}"
: "${NANOKVM_OSDRV_SOURCE:?Set the patched pinned Sophgo osdrv source directory}"
: "${NANOKVM_BUILDROOT_OUTPUT:?Set the Enhanced GCC 16.2 Buildroot output}"
: "${NANOKVM_KERNEL_OUTPUT:?Set a dedicated kernel build directory}"
JOBS=${JOBS:-12}
# Keep release uname/build metadata independent of the workstation and rebuild count.
export KBUILD_BUILD_USER=${KBUILD_BUILD_USER:-nanokvm}
export KBUILD_BUILD_HOST=${KBUILD_BUILD_HOST:-builder}
export KBUILD_BUILD_VERSION=${KBUILD_BUILD_VERSION:-1}
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$ROOT" log -1 --format=%ct)}
KBUILD_BUILD_TIMESTAMP=${KBUILD_BUILD_TIMESTAMP:-$(LC_ALL=C date -u -d "@$SOURCE_DATE_EPOCH" '+%a %b %d %T UTC %Y')}
export SOURCE_DATE_EPOCH KBUILD_BUILD_TIMESTAMP
KERNEL=$(realpath "$NANOKVM_KERNEL_SOURCE")
OSDRV=$(realpath "$NANOKVM_OSDRV_SOURCE")
CROSS=$(realpath "$NANOKVM_BUILDROOT_OUTPUT")/host/bin/riscv64-buildroot-linux-musl-
mkdir -p "$NANOKVM_KERNEL_OUTPUT"
OUT=$(realpath "$NANOKVM_KERNEL_OUTPUT")
[[ "$KERNEL" != "$OUT" ]] || { echo 'Use an out-of-tree kernel output directory' >&2; exit 2; }
[[ $("${CROSS}gcc" -dumpfullversion) == 16.2.0 ]] || { echo 'Expected Enhanced GCC 16.2.0' >&2; exit 2; }
make -s -C "$KERNEL" kernelversion | grep -qx 7.2.5
test -f "$KERNEL/drivers/misc/nanokvm-efuse.c"
# The older port patches are a source prerequisite. Require the scheduler,
# reusable ION allocator and qualified memory/watchdog board description.
for policy in "$ROOT/firmware/kernel/patches/0016-nanokvm-fair-hrtick-default.patch" \
              "$ROOT/firmware/memory/cma/kernel.patch" \
              "$ROOT/firmware/kernel/patches/0017-nanokvm-cma64-watchdog.patch" \
              "$ROOT/firmware/kernel/patches/0018-riscv-uaccess-thead-address-constraints.patch" \
              "$ROOT/firmware/kernel/patches/0019-sg2002-temperature.patch" \
              "$ROOT/firmware/kernel/patches/0020-pikvm-cd-dvd-emulation.patch" \
              "$ROOT/firmware/kernel/patches/0021-sophgo-clock-parent-lock.patch" \
              "$ROOT/firmware/kernel/patches/0022-sg2002-cpufreq.patch" \
              "$ROOT/firmware/kernel/patches/0023-sg2002-thermal-cooling.patch" \
              "$ROOT/firmware/kernel/patches/0024-uac1-composite-iad.patch"; do
    if patch -d "$KERNEL" -p1 --forward --dry-run < "$policy" >/dev/null 2>&1; then
        patch -d "$KERNEL" -p1 --forward < "$policy"
    elif ! patch -d "$KERNEL" -p1 --reverse --dry-run < "$policy" >/dev/null 2>&1; then
        echo "Required NanoKVM patch does not match kernel source: $policy" >&2
        exit 1
    fi
done
cp "$ROOT/firmware/kernel/nanokvm_enhanced_defconfig" "$OUT/.config"
kcflags=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["kernel"]["kcflags"])' "$ROOT/firmware/toolchain/thead/profile.json")
args=(-C "$KERNEL" O="$OUT" ARCH=riscv CROSS_COMPILE="$CROSS" LOCALVERSION= KCFLAGS="$kcflags")
make "${args[@]}" olddefconfig
python3 "$ROOT/scripts/validate-enhanced-kernel-config.py" "$OUT/.config"
make "${args[@]}" -j"$JOBS" Image modules sophgo/sg2002-nanokvm-enhanced.dtb
grep -qx '7.2.5-nanokvm-os' "$OUT/include/config/kernel.release"
symbols=
for module in sys base cif vi vpss vcodec jpeg cvi_vc_drv ive dwa rgn snsr_i2c; do
    (cd "$OSDRV/interdrv/$module" &&
      make "${args[@]}" M="$PWD" CVIARCH=CV181X CVIARCH_L=cv181x KBUILD_EXTRA_SYMBOLS="$symbols" -j"$JOBS" modules)
    symbols="${symbols:+$symbols }$OSDRV/interdrv/$module/Module.symvers"
done
printf '%s\n' 'Compile milestone passed; hardware boot and complete media qualification remain pending.'
