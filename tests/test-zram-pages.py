#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Check the actual zram fill helper at protected page boundaries."""
from pathlib import Path
import argparse
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'tests/kernel-zstd'))
import support

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source', required=True, type=Path)
parser.add_argument('--output', type=Path)
args = parser.parse_args()
kernel = args.source.resolve()


def check(output):
    output.mkdir(parents=True, exist_ok=True)
    driver = (kernel / 'drivers/block/zram/zram_drv.c').read_text()
    helpers = []
    for name in ['zram_fill_page']:
        match = re.search(r'^static[^\n]*\b' + name + r'\(.*?^\}', driver,
                          flags=re.MULTILINE | re.DOTALL)
        if not match:
            raise RuntimeError('Cannot locate actual zram helper: ' + name)
        helpers.append(match.group(0))
    unit = output / 'helpers.c'
    unit.write_text('#include "' + str(ROOT / 'tests/kernel-zstd/compat.h') + '"\n'
                    '#define PAGE_SIZE 4096\n'
                    '#define IS_ALIGNED(n, a) (((n) & ((a) - 1)) == 0)\n'
                    '#define WARN_ON_ONCE(c) assert(!(c))\n'
                    'static void memset_l(void *p, unsigned long value, size_t count) {\n'
                    '    unsigned long *words = p; while (count--) *words++ = value;\n}\n'
                    + '\n'.join(helpers) + '\n#include "'
                    + str(ROOT / 'tests/zram-pages/contract.c') + '"\n')
    executable = output / 'contract'
    subprocess.run(['cc', *support.compiler_flags(), '-D__riscv_xlen=64',
                    '-D__riscv_xtheadmempair=1000000', str(unit),
                    '-o', str(executable)], check=True)
    subprocess.run([str(executable)], check=True)


if args.output:
    check(args.output.resolve())
else:
    with tempfile.TemporaryDirectory(prefix='zram-pages-') as temporary:
        check(Path(temporary))
