#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Check the actual kernel status helper against standard and T-Head VS states."""
import argparse
from pathlib import Path
import re
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    parser.add_argument('--cc', default='cc')
    args = parser.parse_args()
    text = (args.source / 'arch/riscv/include/asm/vector.h').read_text()
    match = re.search(r'static __always_inline bool riscv_v_is_on\(void\)\s*\{.*?\n\}', text, re.S)
    if not match:
        parser.error('status helper not found')
    definitions = (args.source / 'arch/riscv/include/asm/csr.h').read_text()
    masks = {}
    for name in ('SR_VS', 'SR_VS_THEAD'):
        match_mask = re.search(r'^#define\s+' + name + r'\s+_AC\((0x[0-9a-fA-F]+),\s*UL\)', definitions, re.M)
        if not match_mask:
            parser.error('unsupported mask definition: ' + name)
        masks[name] = match_mask.group(1)
    code = '''#include <stdbool.h>
#include <stdio.h>
static unsigned long test_status;
static bool test_thead;
#undef __always_inline
#define __always_inline inline
#define CSR_SSTATUS 0
#define csr_read(csr) test_status
#define has_xtheadvector() test_thead
'''
    code += '\n'.join('#define ' + k + ' ' + v + 'UL' for k, v in masks.items())
    code += '\n' + match.group(0) + '''
int main(void) {
    static const struct { bool thead; unsigned long status; bool expected; } rows[] = {
        {false, 0, false}, {false, 0x200, true}, {false, 0x400, true}, {false, 0x600, true},
        {false, 0x800000, false}, {false, 0x1000000, false}, {false, 0x1800000, false},
        {true, 0, false}, {true, 0x800000, true}, {true, 0x1000000, true}, {true, 0x1800000, true},
        {true, 0x200, false}, {true, 0x400, false}, {true, 0x600, false},
        {false, 0x1800600, true}, {true, 0x1800600, true},
        {true, 0x8000000201800022UL, true}, {true, 0x200000022UL, false},
        {false, 0x6000, false}, {true, 0x6000, false}
    };
    _Static_assert(sizeof(unsigned long) == 8, "64-bit host required");
    for (unsigned i = 0; i < sizeof(rows)/sizeof(rows[0]); i++) {
        test_thead=rows[i].thead; test_status=rows[i].status;
        if (riscv_v_is_on() != rows[i].expected) {
            fprintf(stderr, "FAIL row=%u thead=%u sstatus=0x%lx expected=%u\\n",
                i, test_thead, test_status, rows[i].expected);
            return 1;
        }
    }
    puts("PASS actual helper: 20 standard/T-Head OFF/INITIAL/CLEAN/DIRTY and cross-mask cases");
    return 0;
}
'''
    with tempfile.TemporaryDirectory(prefix='vector-status-') as directory:
        source, binary = Path(directory)/'check.c', Path(directory)/'check'
        source.write_text(code)
        subprocess.run([args.cc, '-O3', '-Wall', '-Wextra', '-Werror', str(source), '-o', str(binary)], check=True)
        result = subprocess.run([str(binary)])
        raise SystemExit(result.returncode)


if __name__ == '__main__':
    main()
