#!/bin/bash
# Rebuild the released in-tree kernel and modules from prepared source and
# compare them with the release build.
set -euo pipefail
# Host tools found on PATH (for example rustc) are recorded in .config.
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
[ "$#" -ge 3 ] && [ "$#" -le 4 ] || { echo "Usage: $0 prepared-source new-output cross-prefix [jobs]" >&2; exit 2; }
here=$(cd "$(dirname "$0")" && pwd)
source_dir=$(realpath "$1")
build_dir=$(realpath -m "$2")
cross_prefix=$3
jobs=${4:-8}
case "$jobs" in ''|*[!0-9]*) exit 2;; esac
[ "$jobs" -ge 1 ] && [ "$jobs" -le 64 ]
case "$build_dir" in "$source_dir"|"$source_dir"/*) echo 'Use an output outside the source tree' >&2; exit 2;; esac
[ ! -e "$build_dir" ] || { echo 'Use a new output directory' >&2; exit 2; }
[ "$("${cross_prefix}gcc" -dumpfullversion)" = 16.2.0 ]
[ "$(make -s -C "$source_dir" kernelversion)" = 7.2.6 ]
export KBUILD_BUILD_USER=nanokvm KBUILD_BUILD_HOST=builder KBUILD_BUILD_VERSION=1
export KBUILD_BUILD_TIMESTAMP='Sat Sep 19 13:51:57 UTC 2026'
mkdir -p "$build_dir"
cp "$here/kernel.config" "$build_dir/.config"
export KCFLAGS='-march=rv64imac_zicsr_zifencei_zacas_zabha_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync -mtune=thead-c906 -mno-fence-tso -fno-tree-vectorize -fno-tree-slp-vectorize'
args=(-C "$source_dir" O="$build_dir" ARCH=riscv CROSS_COMPILE="$cross_prefix" LOCALVERSION=)
make "${args[@]}" olddefconfig
cmp "$here/kernel.config" "$build_dir/.config"
make "${args[@]}" -j"$jobs" Image modules
[ "$(cat "$build_dir/include/config/kernel.release")" = 7.2.6-nanokvm-os-r1 ]
# Identical results need the Buildroot 2026.08 GCC 16.2.0 and binutils 2.47.20260726.
(cd "$build_dir" && sha256sum --quiet -c "$here/EXPECTED-SHA256SUMS")
echo 'Image, Module.symvers and all in-tree modules match the v2.0 release kernel.'
