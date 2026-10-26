#!/usr/bin/env python3
"""Check the actual small-table kernel compressor with guarded buffers."""
from pathlib import Path
import argparse
import os
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--output', type=Path)
parser.add_argument('--oracle', default='liblz4.so.1')
args = parser.parse_args()


def check(directory):
    directory.mkdir(parents=True, exist_ok=True)
    # Reconstruct the private core from PR1, then apply the actual PR2 patch.
    first = ROOT / 'platform/kernel/0052-lz4-update-compressor-1.10.patch'
    second = ROOT / 'platform/kernel/0053-lz4-zram-small-table.patch'
    for part in first.read_text().split('diff --git ')[1:]:
        name = part.splitlines()[0].split(' b/')[1]
        if name != 'lib/lz4/lz4_110.h':
            continue
        target = directory / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text('\n'.join(line[1:] for line in part.splitlines()
                                    if line.startswith('+') and not line.startswith('+++')) + '\n')
    (directory / 'lib/lz4/lz4_compress_4k.c').unlink(missing_ok=True)
    selected = ''.join('diff --git ' + part for part in second.read_text().split('diff --git ')[1:]
                       if part.splitlines()[0].split(' b/')[1] in {
                           'lib/lz4/lz4_110.h', 'lib/lz4/lz4_compress_4k.c'})
    subprocess.run(['patch', '--batch', '--fuzz=0', '-p1', '-d', str(directory)],
                   input=selected, text=True, check=True)
    include = directory / 'include/linux'
    include.mkdir(parents=True, exist_ok=True)
    for name in ['kernel.h', 'limits.h', 'lz4.h', 'module.h', 'string.h']:
        (include / name).write_text('/* Supplied by the native compatibility header. */\n')
    flags = ['-std=gnu11', '-O2', '-g', '-fno-strict-aliasing', '-Wall', '-Wextra', '-Werror']
    if os.environ.get('NANOKVM_SANITIZE') == '1':
        flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-fno-sanitize-recover=all']
    library = directory / 'small-lz4.so'
    subprocess.run(['cc', *flags, '-fPIC', '-shared', '-Wl,-Bsymbolic-functions',
                    '-include', str(ROOT / 'tests/lz4-compressor/compat.h'),
                    '-DLZ4_MEM_COMPRESS_4K=4128', '-I' + str(include.parent),
                    str(directory / 'lib/lz4/lz4_compress_4k.c'), '-o', str(library)], check=True)
    symbols = subprocess.check_output(['nm', '-D', '--defined-only', str(library)], text=True)
    assert {line.split()[-1] for line in symbols.splitlines()} == {'LZ4_compress_fast_4k'}
    test = directory / 'small-table'
    subprocess.run(['cc', *flags, str(ROOT / 'tests/lz4-compressor/small-table.c'),
                    '-ldl', '-o', str(test)], check=True)
    subprocess.run([str(test), str(library.resolve()), args.oracle], check=True)


if args.output:
    check(args.output.resolve())
else:
    with tempfile.TemporaryDirectory(prefix='lz4-small-table-') as tmp:
        check(Path(tmp))
