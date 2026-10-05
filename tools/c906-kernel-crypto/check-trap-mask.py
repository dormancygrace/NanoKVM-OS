#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Check actual trap-entry mask expressions against architectural state contracts."""
import argparse
from pathlib import Path
import re
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    args = parser.parse_args()
    csr = (args.source / 'arch/riscv/include/asm/csr.h').read_text()
    entry = (args.source / 'arch/riscv/kernel/entry.S').read_text()
    definitions = []
    for name in ('SR_SUM', 'SR_FS', 'SR_VS', 'SR_VS_THEAD', 'SR_FS_VS'):
        match = re.search(r'^#define\s+' + name + r'\s+(.+)$', csr, re.M)
        if not match:
            raise ValueError('missing architectural constant ' + name)
        definitions.append('#define ' + name + ' ' + match.group(1))
    alternative = re.search(
        r'ALTERNATIVE\(\s*__stringify\(li t0,\s*([^\n)]+)\),\s*'
        r'__stringify\(li t0,\s*([^\n)]+)\),\s*THEAD_VENDOR_ID,\s*'
        r'RISCV_VENDOR_EXT_ALTERNATIVES_BASE\s*\+\s*RISCV_ISA_VENDOR_EXT_XTHEADVECTOR,\s*'
        r'CONFIG_RISCV_ISA_XTHEADVECTOR\)', entry)
    if alternative:
        standard, thead = alternative.groups()
    else:
        plain = re.search(r'^\s*li t0,\s*(SR_SUM[^\n]+)$', entry, re.M)
        if not plain:
            raise ValueError('unrecognized entry mask; inspect it before changing the checker')
        standard = thead = plain.group(1)
    if any(not re.fullmatch(r'[A-Z_ |]+', expression) for expression in (standard, thead)):
        raise ValueError('unexpected entry expression')
    # These literal contracts are independent of the extracted implementation.
    # Standard cores preserve the legacy bit positions; T-Head clears them.
    cases = []
    for legacy in (0, 1):
        for vector in (0, 0x200, 0x400, 0x600):
            for old_vector in (0, 0x800000, 0x1000000, 0x1800000):
                for floating in (0, 0x2000, 0x4000, 0x6000):
                    before = 0x200040122 | vector | old_vector | floating
                    expected = 0x200000122 | (0 if legacy else old_vector)
                    cases.append((legacy, before, expected))
    rows = ',\n'.join('{%d,0x%xULL,0x%xULL}' % case for case in cases)
    source = '''#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#define _AC(value, suffix) value##suffix
''' + '\n'.join(definitions) + f'''
static uint64_t entry_mask(bool thead)
{{ return thead ? ({thead}) : ({standard}); }}
struct test {{ bool thead; uint64_t before, expected; }};
static const struct test tests[] = {{
{rows}
}};
int main(void)
{{
    for (unsigned i = 0; i < sizeof(tests)/sizeof(tests[0]); i++) {{
        uint64_t after = tests[i].before & ~entry_mask(tests[i].thead);
        if (after != tests[i].expected) {{
            fprintf(stderr, "FAIL trap state %u legacy=%u after=0x%llx expected=0x%llx\\n",
                i, tests[i].thead, (unsigned long long)after,
                (unsigned long long)tests[i].expected);
            return 1;
        }}
    }}
    puts("PASS actual entry expressions: 128 standard/T-Head vector and FPU state combinations");
    return 0;
}}
'''
    with tempfile.TemporaryDirectory(prefix='c906-trap-mask-') as temporary:
        folder = Path(temporary)
        (folder / 'check.c').write_text(source)
        subprocess.run(['cc', '-O3', '-Wall', '-Wextra', '-Werror',
                        str(folder / 'check.c'), '-o', str(folder / 'check')], check=True)
        subprocess.run([str(folder / 'check')], check=True)


if __name__ == '__main__':
    main()
