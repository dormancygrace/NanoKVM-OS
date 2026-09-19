#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CHROOT_ROOT=${CHROOT_ROOT:-$ROOT/work/alpine/builder-root}
QEMU_STATIC=${QEMU_STATIC:-$CHROOT_ROOT/usr/bin/qemu-riscv64-static}
OUT=${OUT:-$ROOT/work/alpine/xtheadvector-probe}
EVIDENCE=${EVIDENCE:-$ROOT/firmware/alpine/evidence/2026-09-19-xtheadvector-probe}
SOURCE=$ROOT/firmware/alpine/bench/xtheadvector-probe.c
GCC=$CHROOT_ROOT/usr/bin/gcc
OBJDUMP=$CHROOT_ROOT/usr/bin/objdump
READELF=$CHROOT_ROOT/usr/bin/readelf

for file in "$SOURCE" "$QEMU_STATIC" "$GCC" "$OBJDUMP" "$READELF"; do
    [ -f "$file" ] || { echo "missing probe input: $file" >&2; exit 1; }
done

mkdir -p "$OUT/scalar" "$OUT/xtheadvector" "$EVIDENCE"

run_target() {
    QEMU_LD_PREFIX="$CHROOT_ROOT" "$QEMU_STATIC" -L "$CHROOT_ROOT" "$@"
}

SCALAR_FLAGS="-O3 -pipe -static -fno-tree-vectorize -fno-tree-slp-vectorize -march=rv64gc -mtune=thead-c906 -mno-fence-tso -mabi=lp64d"
VECTOR_FLAGS="-O3 -pipe -static -march=rv64gc_xtheadvector -mtune=thead-c906 -mno-fence-tso -mabi=lp64d -mrvv-vector-bits=zvl -mrvv-max-lmul=m1"

{
    echo "compiler=$(run_target "$GCC" --version | sed -n '1p')"
    echo "objdump=$(run_target "$OBJDUMP" --version | sed -n '1p')"
    echo "source=$SOURCE"
    echo "scalar_flags=$SCALAR_FLAGS"
    echo "vector_flags=$VECTOR_FLAGS"
    echo "device_run=forbidden"
} > "$EVIDENCE/toolchain.txt"

echo "gcc $SCALAR_FLAGS -o $OUT/scalar/xtheadvector-probe $SOURCE" > "$EVIDENCE/scalar-build.log"
run_target "$GCC" $SCALAR_FLAGS -fopt-info-vec-all="$OUT/scalar/vectorization.txt" \
    -o "$OUT/scalar/xtheadvector-probe" "$SOURCE" \
    >> "$EVIDENCE/scalar-build.log" 2>&1
echo "gcc $VECTOR_FLAGS -o $OUT/xtheadvector/xtheadvector-probe $SOURCE" > "$EVIDENCE/xtheadvector-build.log"
run_target "$GCC" $VECTOR_FLAGS -fopt-info-vec-all="$OUT/xtheadvector/vectorization.txt" \
    -o "$OUT/xtheadvector/xtheadvector-probe" "$SOURCE" \
    >> "$EVIDENCE/xtheadvector-build.log" 2>&1

cp "$OUT/scalar/vectorization.txt" "$EVIDENCE/scalar-vectorization.txt"
cp "$OUT/xtheadvector/vectorization.txt" "$EVIDENCE/xtheadvector-vectorization.txt"

run_target "$OBJDUMP" -d "$OUT/scalar/xtheadvector-probe" > "$EVIDENCE/scalar-objdump.txt"
run_target "$OBJDUMP" -d "$OUT/xtheadvector/xtheadvector-probe" > "$EVIDENCE/xtheadvector-objdump.txt"
run_target "$READELF" -A "$OUT/scalar/xtheadvector-probe" > "$EVIDENCE/scalar-readelf-attributes.txt"
run_target "$READELF" -A "$OUT/xtheadvector/xtheadvector-probe" > "$EVIDENCE/xtheadvector-readelf-attributes.txt"

VECTOR_RE='(^|[[:space:]])(v(set|le|se|l|s|add|sub|mul|xor|and|or|sll|srl|mv|merge|slide|red|wmacc|wmul|wadd|wsub)[^[:space:]]*|th\.v[^[:space:]]*)'
if ! grep -E "$VECTOR_RE" "$EVIDENCE/xtheadvector-objdump.txt" > "$EVIDENCE/vector-instructions.txt"; then
    echo "no vector instructions found in XTheadVector binary" >&2
    exit 1
fi
if grep -E "$VECTOR_RE" "$EVIDENCE/scalar-objdump.txt" > "$EVIDENCE/scalar-vector-instructions.txt"; then
    echo "scalar binary unexpectedly contains vector instructions" >&2
    exit 1
fi

(cd "$OUT" && sha256sum scalar/xtheadvector-probe xtheadvector/xtheadvector-probe) \
    > "$EVIDENCE/SHA256SUMS"
{
    echo "scalar_vector_instruction_count=$(wc -l < "$EVIDENCE/scalar-vector-instructions.txt" | tr -d ' ')"
    echo "xtheadvector_instruction_count=$(wc -l < "$EVIDENCE/vector-instructions.txt" | tr -d ' ')"
    echo "status=pass"
} > "$EVIDENCE/result.txt"

printf '%s\n' "XTheadVector probe passed: $EVIDENCE"
