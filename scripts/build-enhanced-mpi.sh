#!/bin/bash
# Compile core MPI libraries only; ISP and application integration remain pending.
set -euo pipefail
export PATH=${NANOKVM_HOST_PATH:-/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin}
: "${NANOKVM_MPI_SOURCE:?Set the patched pinned cvi_mpi source directory}"
: "${NANOKVM_OSDRV_SOURCE:?Set the patched pinned osdrv source directory}"
: "${NANOKVM_KERNEL_SOURCE:?Set the patched Linux source directory}"
: "${NANOKVM_BUILDROOT_OUTPUT:?Set the Enhanced Buildroot output}"
MPI=$(realpath "$NANOKVM_MPI_SOURCE")
CROSS=$(realpath "$NANOKVM_BUILDROOT_OUTPUT")/host/bin/riscv64-buildroot-linux-musl-
OSDRV=$(realpath "$NANOKVM_OSDRV_SOURCE")
KERNEL=$(realpath "$NANOKVM_KERNEL_SOURCE")
JOBS=${JOBS:-8}
# Macro/debug source locations must not expose the build workstation.
path_flags="-ffile-prefix-map=$MPI=./cvi_mpi -ffile-prefix-map=$OSDRV=./osdrv -ffile-prefix-map=$KERNEL=./linux -ffile-prefix-map=$(realpath "$NANOKVM_BUILDROOT_OUTPUT")=./toolchain"
profile_tool="$(dirname "$0")/nanokvm_cpu_profile.py"
opt_flags="$(python3 "$profile_tool" userspace) -mcmodel=medany $path_flags"
python3 "$profile_tool" userspace --record "$MPI/cpu-profile-core.json" --compiler "${CROSS}gcc"
[[ $("${CROSS}gcc" -dumpfullversion) == 16.2.0 ]]
macros=$("${CROSS}gcc" -dM -E -D__CV181X__ -I"$MPI/include" -include linux/cvi_defines.h -x c /dev/null)
grep -q '^#define __CV181X__' <<< "$macros"
if grep -q '^#define __CV180X__' <<< "$macros"; then
    echo 'Conflicting CV180X/CV181X header selection' >&2
    exit 1
fi
mkdir -p "$MPI/lib/3rd"
# Do not invoke vendor prepare: it rewrites the chip header by line number.
for module in sys vi vpss vo rgn gdc venc vdec misc ive; do
    make -C "$MPI/modules/$module" CROSS_COMPILE="$CROSS" CHIP_ARCH=CV181X \
        OSDRV_PATH="$OSDRV" KERNEL_PATH="$KERNEL" ISP_SRC_RELEASE=0 OPT_LEVEL="$opt_flags" -B -j"$JOBS"
done
printf '%s\n' 'Core MPI compile passed; ISP closure and hardware qualification remain pending.'
