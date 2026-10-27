#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Exercise the actual zram zstd backend, including allocation failures."""
from pathlib import Path
import argparse
import shutil
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
    include = support.headers(output, kernel)
    for name in ['mutex.h', 'slab.h', 'vmalloc.h']:
        (include / 'linux' / name).write_text('/* Supplied by compat.h. */\n')
    library = support.build_codec(output, 'codec', kernel / 'lib/zstd', kernel, include)
    reference = output / 'reference-source'
    shutil.copytree(kernel / 'drivers/block/zram', reference / 'drivers/block/zram',
                    dirs_exist_ok=True)
    patch = ROOT / 'platform/kernel/0057-zram-zstd-empty-dictionaries.patch'
    subprocess.run(['patch', '--batch', '--reverse', '--fuzz=0', '-p1',
                    '-d', str(reference)], input=patch.read_text(), text=True, check=True)
    for name, source, expected in [('reference', reference, 3), ('candidate', kernel, 1)]:
        backend = source / 'drivers/block/zram'
        executable = output / name
        subprocess.run(['cc', *support.compiler_flags(), '-pthread',
                        '-DEXPECT_EMPTY_DICT_ALLOCATIONS=' + str(expected),
                        '-I' + str(include), '-I' + str(backend),
                        '-include', str(ROOT / 'tests/zram-zstd/compat.h'),
                        str(backend / 'backend_zstd.c'),
                        str(ROOT / 'tests/zram-zstd/contract.c'), str(library),
                        '-o', str(executable)], check=True)
        subprocess.run([str(executable)], check=True)


if args.output:
    check(args.output.resolve())
else:
    with tempfile.TemporaryDirectory(prefix='zram-zstd-') as temporary:
        check(Path(temporary))
