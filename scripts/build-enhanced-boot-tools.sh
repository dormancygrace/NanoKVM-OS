#!/bin/bash
# Build packaging tools only; no bootloader firmware is generated or installed.
set -euo pipefail
: "${NANOKVM_UBOOT_SOURCE:?Set the pinned U-Boot 2026.10 source}"
: "${NANOKVM_UBOOT_TOOLS_OUTPUT:?Set a separate host-tools output directory}"
src=$(realpath "$NANOKVM_UBOOT_SOURCE")
mkdir -p "$NANOKVM_UBOOT_TOOLS_OUTPUT"
out=$(realpath "$NANOKVM_UBOOT_TOOLS_OUTPUT")
[[ "$src" != "$out" ]] || exit 2
[[ $(git -C "$src" rev-parse HEAD) == 5508406582f6f7120dce0c4b3059819a41788db2 ]] || exit 2
make -C "$src" O="$out" tools-only_defconfig
python3 - "$out/.config" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1])
s=p.read_text()
s=s.replace('CONFIG_TOOLS_MKEFICAPSULE=y', '# CONFIG_TOOLS_MKEFICAPSULE is not set')
p.write_text(s)
PY
make -C "$src" O="$out" olddefconfig
make -C "$src" O="$out" -j"${JOBS:-12}" tools-only NO_SDL=1
"$out/tools/mkimage" -V
