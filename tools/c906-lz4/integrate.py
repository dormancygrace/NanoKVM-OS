#!/usr/bin/env python3
"""Generate the qualified opt-in patch; --apply is for an owned kernel tree."""
import argparse
import difflib
from pathlib import Path
from variants import ALIGNED_WILD_COPY

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--kernel", type=Path, required=True)
parser.add_argument("--patch", type=Path, required=True)
parser.add_argument("--apply", action="store_true")
args = parser.parse_args()
changes = {}

path = "arch/riscv/Kconfig"
before = (args.kernel / path).read_text()
marker = "config RISCV_ISA_V_PREEMPTIVE"
assert before.count(marker) == 1 and "config RISCV_C906_LZ4_DECOMPRESS" not in before
option = """config RISCV_C906_LZ4_DECOMPRESS
\tbool "C906 aligned LZ4 decompression copies"
\tdepends on 64BIT && ARCH_THEAD && LZ4_DECOMPRESS
\tdefault n
\thelp
\t  Use native aligned 64-bit copies for complete wild-copy spans in
\t  the LZ4 decompressor. Dispatch once per span, retain the original
\t  unaligned path and forward order for overlapping matches. The
\t  compressor and compiler vectorization policy stay unchanged.
\t  This scalar path was qualified on the NanoKVM C906.

"""
changes[path] = before, before.replace(marker, option + marker)

path = "lib/lz4/lz4defs.h"
before = (args.kernel / path).read_text()
marker = "BYTE *const e = (BYTE *)dstEnd;"
assert before.count(marker) == 1
block = "\n#if defined(CONFIG_RISCV_C906_LZ4_DECOMPRESS) && defined(LZ4_C906_ALIGNED_WILDCOPY)" + ALIGNED_WILD_COPY + "#endif\n"
changes[path] = before, before.replace(marker, marker + block)

path = "lib/lz4/lz4_decompress.c"
before = (args.kernel / path).read_text()
marker = '#include "lz4defs.h"'
assert before.count(marker) == 1
changes[path] = before, before.replace(marker, '#define LZ4_C906_ALIGNED_WILDCOPY\n' + marker)

patch = "".join("".join(difflib.unified_diff(before.splitlines(True), after.splitlines(True), fromfile="a/"+path, tofile="b/"+path)) for path, (before, after) in changes.items())
args.patch.parent.mkdir(parents=True, exist_ok=True)
args.patch.write_text(patch)
if args.apply:
    for path, (_, after) in changes.items():
        (args.kernel / path).write_text(after)
print("Generated opt-in C906 LZ4 decompressor-only patch")
