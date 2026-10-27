#!/usr/bin/env python3
"""Validate the actual patched kernel decoder, including dictionary/partial APIs."""
from pathlib import Path
import argparse
import os
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source', required=True, type=Path, help='Patched Linux source tree')
parser.add_argument('--output', type=Path)
args = parser.parse_args()


def check(directory):
    directory.mkdir(parents=True, exist_ok=True)
    include = directory / 'include/linux'
    include.mkdir(parents=True, exist_ok=True)
    for name in ['types.h', 'string.h', 'kernel.h', 'bitops.h', 'unaligned.h', 'module.h', 'init.h']:
        (include / name).write_text('/* Supplied by the native compatibility header. */\n')
    shutil.copy2(args.source / 'include/linux/lz4.h', include / 'lz4.h')
    flags = ['-std=gnu11', '-O2', '-g', '-fno-strict-aliasing', '-Wall', '-Wextra',
             '-Werror', '-Wno-unused-parameter', '-Wno-sign-compare']
    if os.environ.get('NANOKVM_SANITIZE') == '1':
        flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-fno-sanitize-recover=all']
    library = directory / 'decoder.so'
    subprocess.run(['cc', *flags, '-fPIC', '-shared', '-Wl,-Bsymbolic-functions',
                    '-DCONFIG_64BIT=1', '-D__LITTLE_ENDIAN=1',
                    '-include', str(ROOT / 'tests/lz4-compressor/decoder-compat.h'),
                    '-I' + str(include.parent), str(args.source / 'lib/lz4/lz4_decompress.c'),
                    '-o', str(library)], check=True)
    test = directory / 'decoder-contract'
    subprocess.run(['cc', *flags, str(ROOT / 'tests/lz4-compressor/decoder-contract.c'),
                    '-ldl', '-o', str(test)], check=True)
    subprocess.run([str(test), str(library.resolve())], check=True)


if args.output:
    check(args.output.resolve())
else:
    with tempfile.TemporaryDirectory(prefix='lz4-decoder-') as tmp:
        check(Path(tmp))
