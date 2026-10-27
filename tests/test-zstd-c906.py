#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
'''Verify the actual patched kernel zstd against its unmodified implementation.

The source tree must have the repository's kernel patches applied. Codec input
files are copied to temporary output; the supplied kernel source is never edited.
'''
from pathlib import Path
import argparse
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'tests/kernel-zstd'))
import support
sys.path.insert(0, str(ROOT / 'tests/zstd-c906'))
from corpus import corpus

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source', required=True, type=Path)
parser.add_argument('--output', type=Path)
args = parser.parse_args()
kernel = args.source.resolve()


def check(output):
    output.mkdir(parents=True, exist_ok=True)
    reference = output / 'reference-source'
    shutil.copytree(kernel / 'lib/zstd', reference / 'lib/zstd', dirs_exist_ok=True)
    patch = ROOT / 'platform/kernel/0059-zstd-c906-bit-operations.patch'
    subprocess.run(['patch', '--batch', '--reverse', '--fuzz=0', '-p1',
                    '-d', str(reference)], input=patch.read_text(), text=True, check=True)
    include = support.headers(output, kernel)
    support.build_codec(output, 'reference', reference / 'lib/zstd', kernel, include,
                        wrapper=ROOT / 'tests/zstd-c906/wrapper.c',
                        extra_flags=['-D__riscv_xlen=64', '-D__riscv_xtheadbb=1000000'])
    support.build_codec(output, 'candidate', kernel / 'lib/zstd', kernel, include,
                        wrapper=ROOT / 'tests/zstd-c906/wrapper.c',
                        extra_flags=['-D__riscv_xlen=64', '-D__riscv_xtheadbb=1000000'])
    data = output / 'pages.bin'
    pages = corpus(data)
    test = output / 'contract'
    subprocess.run(['cc', *support.compiler_flags(),
                    str(ROOT / 'tests/zstd-c906/contract.c'), '-ldl', '-o', str(test)], check=True)
    subprocess.run([str(test), str(output), str(data)], check=True)
    print(f'Actual kernel codec contracts: {pages} generated pages, levels 1/3/9, '
          'boundary sizes, bit-reader edges, cross-decoding, guard pages and 300000 random/mutated inputs')


if args.output:
    check(args.output.resolve())
else:
    with tempfile.TemporaryDirectory(prefix='zstd-c906-') as temporary:
        check(Path(temporary))
