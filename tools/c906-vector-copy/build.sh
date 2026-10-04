#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-only
set -euo pipefail
if [[ $# != 3 ]]; then
    echo "Usage: $0 KERNEL_SOURCE KERNEL_BUILD CROSS_COMPILE_PREFIX" >&2
    exit 2
fi
source_dir=$(realpath "$1")
build_dir=$(realpath "$2")
cross=$3
probe_dir=$(cd "$(dirname "$0")" && pwd)
[[ -f "$build_dir/.config" && -f "$build_dir/Module.symvers" ]] || {
    echo "A configured, fully built matching kernel is required" >&2
    exit 1
}
[[ -x "$cross"gcc ]] || command -v "$cross"gcc >/dev/null
# Pass the matching kernel flags through the KCFLAGS environment.
# Kbuild supplies its configured ABI; the probe C remains scalar O3.
make -C "$source_dir" O="$build_dir" M="$probe_dir" ARCH=riscv \
    CROSS_COMPILE="$cross" CC="$cross"gcc modules
"$cross"gcc -O2 -march=rv64gc_xtheadvector -mabi=lp64d \
    -fno-tree-vectorize -fno-tree-slp-vectorize -fno-fast-math -static \
    -Wall -Wextra -Werror "$probe_dir/client.c" -o "$probe_dir/client"
"$cross"readelf -h "$probe_dir/client"
sha256sum "$probe_dir/c906_vector_probe.ko" "$probe_dir/client"
