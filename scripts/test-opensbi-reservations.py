#!/usr/bin/env python3
"""Run pinned U-Boot reservation-copy/deduplication code on real Linux DTBs.

Native test adapters supply scalar address-cell decoding and logging; the copy,
deduplication and libfdt implementations are taken unchanged from U-Boot source.
The source firmware DT is a fixture of the two calculated OpenSBI PMP regions.
"""
import argparse
import subprocess
import tempfile
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("--uboot-source", type=Path, required=True)
p.add_argument("--firmware-dtb", type=Path, required=True)
p.add_argument("--linux-dtbs", type=Path, required=True)
a = p.parse_args()


def function(path, signature):
    text = path.read_text()
    start = text.index(signature)
    return text[start:text.index("\n}\n", start) + 3]


header = r"""
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <assert.h>
#include <libfdt.h>
typedef uint64_t fdt_addr_t;
typedef uint64_t fdt_size_t;
typedef uint32_t u32;
struct fdt_memory { uint64_t start, end; };
#define FDT_ADDR_T_NONE UINT64_MAX
#define FDTDEC_RESERVED_MEMORY_NO_MAP 1
#define upper_32_bits(x) ((uint32_t)((uint64_t)(x) >> 32))
#define lower_32_bits(x) ((uint32_t)(x))
#define debug(...) ((void)0)
#define log_debug(...) ((void)0)
#define log_err(...) fprintf(stderr, __VA_ARGS__)
static size_t strlcpy(char *d, const char *s, size_t n) {
    size_t len = strlen(s);
    if (n) { size_t count = len < n - 1 ? len : n - 1; memcpy(d, s, count); d[count] = 0; }
    return len;
}
static int fdtdec_set_phandle(void *fdt, int node, uint32_t phandle) {
    return fdt_setprop_u32(fdt, node, "phandle", phandle);
}
static uint64_t cells(const fdt32_t *p, int n) {
    uint64_t v = 0; while (n--) v = (v << 32) | fdt32_to_cpu(*p++); return v;
}
static fdt_addr_t fdtdec_get_addr_size_fixed(const void *fdt, int node,
        const char *prop, int index, int na, int ns, fdt_size_t *size, bool translate) {
    int len; const fdt32_t *reg = fdt_getprop(fdt, node, prop, &len);
    assert(!translate);
    if (!reg || na < 1 || na > 2 || ns < 1 || ns > 2 || len < (index + 1) * (na + ns) * 4)
        return FDT_ADDR_T_NONE;
    reg += index * (na + ns); *size = cells(reg + na, ns); return cells(reg, na);
}
static fdt_addr_t fdtdec_get_addr_size_auto_parent(const void *fdt, int parent,
        int node, const char *prop, int index, fdt_size_t *size, bool translate) {
    return fdtdec_get_addr_size_fixed(fdt, node, prop, index,
        fdt_address_cells(fdt, parent), fdt_size_cells(fdt, parent), size, translate);
}
"""
body = r"""
#define CAPACITY (1024 * 1024)
static void *load(const char *path) {
    FILE *f = fopen(path, "rb"); assert(f);
    void *buf = calloc(1, CAPACITY); assert(buf);
    size_t n = fread(buf, 1, CAPACITY, f); assert(n > 0 && n < CAPACITY); fclose(f);
    assert(!fdt_check_header(buf)); assert(!fdt_open_into(buf, buf, CAPACITY)); return buf;
}
static void add(void *fdt, const char *name, uint64_t base, uint64_t size) {
    struct fdt_memory memory = {base, base + size - 1}; uint32_t phandle;
    assert(!fdtdec_add_reserved_memory(fdt, name, &memory, NULL, 0, &phandle,
                                     FDTDEC_RESERVED_MEMORY_NO_MAP));
}
static int check(const void *fdt) {
    uint64_t starts[32], ends[32]; int count = 0, ro = 0, rw = 0;
    int parent = fdt_path_offset(fdt, "/reserved-memory"), node;
    assert(parent >= 0);
    fdt_for_each_subnode(node, fdt, parent) {
        uint64_t size, addr = fdtdec_get_addr_size_auto_parent(fdt, parent, node, "reg", 0, &size, false);
        if (addr == FDT_ADDR_T_NONE) continue;
        assert(size && count < 32);
        if (addr == 0x80000000 && size == 0x40000) {
            ro++; assert(fdt_getprop(fdt, node, "no-map", NULL));
        }
        if (addr == 0x80040000 && size == 0x10000) {
            rw++; assert(fdt_getprop(fdt, node, "no-map", NULL));
        }
        starts[count] = addr; ends[count++] = addr + size;
    }
    if (ro != 1 || rw != 1) return 0;
    for (int i = 0; i < count; i++)
        for (int j = i + 1; j < count; j++)
            if (starts[i] < ends[j] && starts[j] < ends[i]) return 0;
    return 1;
}
int main(int argc, char **argv) {
    assert(argc >= 3);
    void *source = load(argv[1]);
    /* Region labels follow the size-sorted upstream domain order. */
    add(source, "mmode_resv1", 0x80000000, 0x40000);
    add(source, "mmode_resv0", 0x80040000, 0x10000);
    for (int i = 2; i < argc; i++) {
        void *target = load(argv[i]);
        assert(!riscv_fdt_copy_resv_mem_node(source, target));
        assert(check(target));
        assert(!riscv_fdt_copy_resv_mem_node(source, target));
        assert(check(target));
        printf("%s: split PMP copied, exact range deduplicated, repeated copy stable\n", argv[i]);
        /* Reproduce the overlap that an oversized static reservation causes. */
        add(target, "oversized", 0x80000000, 0x80000);
        assert(!check(target));
        free(target);
    }
    free(source); return 0;
}
"""
uboot = a.uboot_source.resolve()
fdtdec = uboot / "lib/fdtdec.c"
code = header + function(fdtdec, "static int fdtdec_init_reserved_memory(")
code += function(fdtdec, "int fdtdec_add_reserved_memory(")
code += function(uboot / "arch/riscv/lib/fdt_fixup.c", "int riscv_fdt_copy_resv_mem_node(") + body
dtbs = sorted(a.linux_dtbs.resolve().glob("*.dtb"))
assert len(dtbs) == 10, "expected five board variants with CMA/fixed memory"
libfdt = uboot / "scripts/dtc/libfdt"
with tempfile.TemporaryDirectory(prefix="nkos-reservations-") as directory:
    root = Path(directory)
    src, exe = root / "test.c", root / "test"
    src.write_text(code)
    files = ["fdt.c", "fdt_ro.c", "fdt_rw.c", "fdt_wip.c", "fdt_sw.c",
             "fdt_strerror.c", "fdt_empty_tree.c", "fdt_addresses.c"]
    subprocess.run(["cc", "-std=gnu11", "-O2", "-I", str(libfdt), str(src),
                    *[str(libfdt / f) for f in files], "-o", str(exe)], check=True)
    subprocess.run([str(exe), str(a.firmware_dtb.resolve()), *map(str, dtbs)], check=True)
