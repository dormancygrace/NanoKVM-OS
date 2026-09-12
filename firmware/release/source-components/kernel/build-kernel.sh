#!/bin/bash
# Build only the ordinary in-tree kernel/module set from prepared source.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
[ "$#" -ge 3 ] && [ "$#" -le 4 ] || { echo "Usage: $0 prepared-source new-output cross-prefix [jobs]" >&2; exit 2; }
here=$(cd "$(dirname "$0")" && pwd)
source_dir=$(realpath "$1")
build_dir=$(realpath -m "$2")
cross_prefix=$3
jobs=${4:-8}
case "$jobs" in ''|*[!0-9]*) exit 2;; esac
[ "$jobs" -ge 1 ] && [ "$jobs" -le 32 ]
case "$build_dir" in "$source_dir"|"$source_dir"/*) echo 'Use an output outside the source tree' >&2; exit 2;; esac
[ ! -e "$build_dir" ] || { echo 'Use a new output directory' >&2; exit 2; }
[ "$("${cross_prefix}gcc" -dumpfullversion)" = 16.2.0 ]
[ "$(make -s -C "$source_dir" kernelversion)" = 7.2.5 ]
export SOURCE_DATE_EPOCH=1789084800 KBUILD_BUILD_USER=nanokvm KBUILD_BUILD_HOST=builder KBUILD_BUILD_VERSION=1
export KBUILD_BUILD_TIMESTAMP='Sat Sep 12 03:31:59 IDT 2026'
mkdir -p "$build_dir"
cp "$here/kernel.config" "$build_dir/.config"
export KCFLAGS='-march=rv64imac_zicsr_zifencei_zacas_zabha_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync -mtune=thead-c906 -mno-fence-tso -fno-tree-vectorize -fno-tree-slp-vectorize'
args=(-C "$source_dir" O="$build_dir" ARCH=riscv CROSS_COMPILE="$cross_prefix" LOCALVERSION=)
make "${args[@]}" olddefconfig
cmp "$here/kernel.config" "$build_dir/.config"
make "${args[@]}" -j"$jobs" Image modules sophgo/sg2002-nanokvm-enhanced.dtb
(cd "$build_dir" && sha256sum arch/riscv/boot/Image arch/riscv/boot/dts/sophgo/sg2002-nanokvm-enhanced.dtb > BUILD-SHA256SUMS)
# Compare hashes only with a build using the identical toolchain and complete inputs.
