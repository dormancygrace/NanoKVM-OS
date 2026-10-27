#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Verify direction locking, CPU migration and teardown in the actual zcomp.c."""
from pathlib import Path
import argparse
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
    include = output / 'include/linux'
    include.mkdir(parents=True, exist_ok=True)
    for name in ['kernel.h', 'string.h', 'err.h', 'slab.h', 'wait.h', 'sched.h',
                 'cpuhotplug.h', 'vmalloc.h', 'sysfs.h', 'mutex.h']:
        (include / name).write_text('/* Supplied by compat.h. */\n')
    backend = kernel / 'drivers/block/zram'
    executable = output / 'contract'
    subprocess.run(['cc', *support.compiler_flags(), '-pthread',
                    '-I' + str(include.parent), '-I' + str(backend),
                    '-include', str(ROOT / 'tests/zram-streams/compat.h'),
                    str(backend / 'zcomp.c'), str(ROOT / 'tests/zram-streams/contract.c'),
                    '-o', str(executable)], check=True)
    subprocess.run([str(executable)], check=True, timeout=60)


if args.output:
    check(args.output.resolve())
else:
    with tempfile.TemporaryDirectory(prefix='zram-streams-') as temporary:
        check(Path(temporary))
