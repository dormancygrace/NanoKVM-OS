#!/bin/sh
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
TOOLCHAIN=${TOOLCHAIN:-/home/dgrace/.local/share/nkos-build/buildroot-output/host}
CC=${CC:-$TOOLCHAIN/bin/riscv64-buildroot-linux-musl-gcc}
OUT=${OUT:-$ROOT/work/alpine/c906-bench}
SOURCE=$ROOT/firmware/alpine/bench/c906-scalar-bench.c

mkdir -p "$OUT/portable" "$OUT/c906-scalar"

common="-O2 -static -fno-tree-vectorize -fno-tree-slp-vectorize -mabi=lp64d"
# Intentional word splitting: common is a fixed compiler flag list.
# shellcheck disable=SC2086
"$CC" $common -march=rv64gc \
    -o "$OUT/portable/c906-scalar-bench" "$SOURCE"
# shellcheck disable=SC2086
"$CC" $common \
    -march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync \
    -mtune=thead-c906 -mno-fence-tso \
    -o "$OUT/c906-scalar/c906-scalar-bench" "$SOURCE"

"$TOOLCHAIN/bin/riscv64-buildroot-linux-musl-readelf" -A \
    "$OUT/portable/c906-scalar-bench" > "$OUT/portable/readelf-attributes.txt"
"$TOOLCHAIN/bin/riscv64-buildroot-linux-musl-readelf" -A \
    "$OUT/c906-scalar/c906-scalar-bench" > "$OUT/c906-scalar/readelf-attributes.txt"
sha256sum "$OUT"/*/c906-scalar-bench > "$OUT/SHA256SUMS"
cat "$OUT/SHA256SUMS"
