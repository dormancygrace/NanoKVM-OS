#!/usr/bin/env python3
"""Build standalone candidate probes against an existing matching prepared kernel."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess


def add_argument(code, name, argument, rename=None):
    matches = list(re.finditer(r"\b" + re.escape(name) + r"\s*\(", code))
    for match in reversed(matches):
        pos = match.end()
        depth = 1
        while depth:
            if code[pos] == "(":
                depth += 1
            elif code[pos] == ")":
                depth -= 1
            pos += 1
        code = code[:pos-1] + ", " + argument + code[pos-1:]
        if rename:
            code = code[:match.start()] + rename + code[match.start()+len(name):]
    return code

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("build", type=Path)
    parser.add_argument("cross")
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    source, build, output = args.source.resolve(), args.build.resolve(), args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    tools = Path(__file__).resolve().parent
    base_header = (source / "lib/lz4/lz4defs.h").read_text()
    base_source = (source / "lib/lz4/lz4_compress.c").read_text()
    exports = sorted(set(re.findall(r"EXPORT_SYMBOL\((\w+)\)", base_source) + re.findall(r"^(?:int|void)\s+(LZ4_\w+)\s*\(", base_source, re.M)))
    if "LZ4_compress_fast" not in exports or base_header.count("return __ffs(val) >> 3;") != 1:
        raise ValueError("unexpected LZ4 source layout")
    th_header = base_header.replace("return __ffs(val) >> 3;", """
    unsigned long index;
    asm(".option push\\n.option arch,+xtheadbb\\n"
        "th.rev %0,%1\\nth.ff1 %0,%0\\n.option pop"
        : "=&r"(index) : "r"(val));
    return index >> 3;""")
    for variant, header in [("th", th_header), ("vec", th_header), ("lazy", th_header)]:
        folder = output / variant
        folder.mkdir()
        if variant == "vec":
            header = header.replace("unsigned int LZ4_count(", "unsigned int LZ4_count_scalar(")
            marker = "typedef enum { noLimit = 0"
            if header.count(marker) != 1:
                raise ValueError("unexpected LZ4 count insertion point")
            wrapper = """
extern size_t screen_vector_prefix(const void *, const void *, size_t);
static FORCE_INLINE unsigned int LZ4_count(
    const BYTE *pIn, const BYTE *pMatch, const BYTE *pInLimit)
{
    size_t n = pInLimit - pIn;
    unsigned int prefix;
    if (n < 128)
        return LZ4_count_scalar(pIn, pMatch, pInLimit);
    /* Short matches remain scalar; take vector state once per compressor. */
    prefix = LZ4_count_scalar(pIn, pMatch, pIn + 64);
    if (prefix < 64)
        return prefix;
    return 64 + screen_vector_prefix(pIn + 64, pMatch + 64, n - 64);
}

"""
            header = header.replace(marker, wrapper + marker)

        if variant == "lazy":
            header = header.replace("unsigned int LZ4_count(", "unsigned int LZ4_count_scalar(")
            marker = "typedef enum { noLimit = 0"
            wrapper = """
#include <asm/simd.h>
#include <asm/vector.h>
struct screen_lazy_context { bool active; u64 *entries; };
extern size_t screen_vector_prefix(const void *, const void *, size_t);
static FORCE_INLINE unsigned int LZ4_count(const BYTE *a, const BYTE *b, const BYTE *end)
{
    return LZ4_count_scalar(a, b, end);
}
static FORCE_INLINE unsigned int LZ4_count_lazy(
    const BYTE *a, const BYTE *b, const BYTE *end, struct screen_lazy_context *lazy)
{
    size_t n = end - a;
    unsigned int prefix;
    if (!lazy || n < 128)
        return LZ4_count_scalar(a, b, end);
    prefix = LZ4_count_scalar(a, b, a + 64);
    if (prefix < 64)
        return prefix;
    if (!lazy->active) {
        if (!has_xtheadvector() || !may_use_simd())
            return 64 + LZ4_count_scalar(a + 64, b + 64, end);
        kernel_vector_begin();
        lazy->active = true;
        (*lazy->entries)++;
    }
    return 64 + screen_vector_prefix(a + 64, b + 64, n - 64);
}

"""
            header = header.replace(marker, wrapper + marker)
        (folder / "lz4defs.h").write_text(header)
        code = re.sub(r"^EXPORT_SYMBOL\(\w+\);\s*$", "", base_source, flags=re.M)

        if variant == "lazy":
            begin = code.index("static FORCE_INLINE int LZ4_compress_generic(")
            ext = code.index("static int LZ4_compress_fast_extState(")
            public = code.index("\nint LZ4_compress_fast(", ext)
            generic = code[begin:ext].replace("const U32 acceleration)",
                "const U32 acceleration, struct screen_lazy_context *lazy)")
            generic = add_argument(generic, "LZ4_count", "lazy", rename="LZ4_count_lazy")
            state = code[ext:public].replace("int acceleration)",
                "int acceleration, struct screen_lazy_context *lazy)")
            state = add_argument(state, "LZ4_compress_generic", "lazy")
            rest = code[public:]
            rest = add_argument(rest, "LZ4_compress_fast_extState", "NULL")
            rest = add_argument(rest, "LZ4_compress_generic", "NULL")
            code = code[:begin] + generic + state + rest
            code += """
int screen_lazy_compress(const char *, char *, int, int, int, void *, u64 *);
int screen_lazy_compress(const char *source, char *dest, int inputSize,
                        int maxOutputSize, int acceleration, void *work, u64 *entries)
{
    struct screen_lazy_context lazy = { .entries = entries };
    int ret = LZ4_compress_fast_extState(work, source, dest, inputSize,
                                       maxOutputSize, acceleration, &lazy);
    if (lazy.active)
        kernel_vector_end();
    return ret;
}
"""
        macros = "\n".join(f"#define {name} screen_{variant}_{name}" for name in exports)
        code = macros + "\n" + code.replace('#include "lz4defs.h"', f'#include "{variant}/lz4defs.h"')
        (output / f"lz4_{variant}.c").write_text(code)
    (output / "scalar").mkdir()
    (output / "scalar/lz4defs.h").write_text(base_header)
    (output / "prefix_helpers.c").write_text("""
#include <linux/types.h>
#include "scalar/lz4defs.h"
unsigned int screen_scalar_prefix(const unsigned char *a, const unsigned char *b, size_t n);
unsigned int screen_th_prefix(const unsigned char *a, const unsigned char *b, size_t n);
unsigned int screen_scalar_prefix(const unsigned char *a, const unsigned char *b, size_t n)
{
    return LZ4_count(a, b, a + n);
}
unsigned int screen_th_prefix(const unsigned char *a, const unsigned char *b, size_t n)
{
    const unsigned char *start = a, *end = a + n;
    while (end - a >= 8) {
        unsigned long diff = LZ4_read_ARCH(a) ^ LZ4_read_ARCH(b), index;
        if (diff) {
            asm(".option push\\n.option arch,+xtheadbb\\n"
                "th.rev %0,%1\\nth.ff1 %0,%0\\n.option pop"
                : "=&r"(index) : "r"(diff));
            return a - start + (index >> 3);
        }
        a += 8; b += 8;
    }
    while (a < end && *a == *b) { a++; b++; }
    return a - start;
}
""")
    for name in ["screen_probe.c", "screen_protocol.h", "screen_xtheadvector.S", "screen_client.c"]:
        shutil.copy2(tools / name, output / name)
    (output / "Makefile").write_text(
        "obj-m += c906_screen_probe.o\n"
        "c906_screen_probe-y := screen_probe.o screen_xtheadvector.o prefix_helpers.o lz4_th.o lz4_vec.o lz4_lazy.o\n"
        "ccflags-y += -O3 -fno-tree-vectorize -fno-tree-slp-vectorize\n")
    command = ["make", "-C", str(source), f"O={build}", f"M={output}", "ARCH=riscv",
               f"CROSS_COMPILE={args.cross}", "-j4", "modules"]
    with (output / "module-build.log").open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    command = [args.cross + "gcc", "-O2", "-march=rv64gc_xtheadvector_xtheadbb", "-mabi=lp64d",
               "-fno-tree-vectorize", "-fno-tree-slp-vectorize", "-static", "-Wall", "-Wextra", "-Werror",
               str(output / "screen_client.c"), "-o", str(output / "screen-client")]
    with (output / "client-build.log").open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    manifest = {
        "source": str(source), "build": str(build), "cross": args.cross,
        "kernel_lz4_sha256": hashlib.sha256((source / "lib/lz4/lz4_compress.c").read_bytes()).hexdigest(),
        "kernel_lz4defs_sha256": hashlib.sha256((source / "lib/lz4/lz4defs.h").read_bytes()).hexdigest(),
        "kcflags": os.environ.get("KCFLAGS", ""),
        "artifacts": {name: hashlib.sha256((output / name).read_bytes()).hexdigest()
                      for name in ["c906_screen_probe.ko", "screen-client"]},
    }
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps(manifest, indent=2))

if __name__ == "__main__":
    main()
