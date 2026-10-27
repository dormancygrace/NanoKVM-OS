# SPDX-License-Identifier: GPL-2.0-only
from pathlib import Path
import os
import shutil
import subprocess

HERE = Path(__file__).resolve().parent
LINUX_HEADERS = ['bitops.h', 'compiler.h', 'errno.h', 'init.h', 'kernel.h',
                 'limits.h', 'math64.h', 'module.h', 'printk.h', 'stddef.h',
                 'string.h', 'swab.h', 'types.h', 'unaligned.h']


def compiler_flags():
    flags = ['-std=gnu11', '-O3', '-g', '-fno-strict-aliasing',
             '-fno-tree-vectorize', '-fno-tree-slp-vectorize']
    if os.environ.get('NANOKVM_SANITIZE') == '1':
        flags += ['-O1', '-fsanitize=address,undefined',
                  '-fno-sanitize-recover=all', '-fno-omit-frame-pointer']
    return flags


def headers(output, kernel):
    include = output / 'include/linux'
    include.mkdir(parents=True, exist_ok=True)
    for name in LINUX_HEADERS:
        (include / name).write_text('/* Supplied by compat.h. */\n')
    for name in ['zstd.h', 'zstd_lib.h', 'zstd_errors.h', 'xxhash.h']:
        shutil.copyfile(kernel / 'include/linux' / name, include / name)
    return include.parent


def build_codec(output, name, codec, kernel, include, wrapper=None, extra_flags=()):
    units = sorted(codec.glob('common/*.c')) + sorted(codec.glob('compress/*.c'))
    units += sorted(codec.glob('decompress/*.c')) + sorted(codec.glob('zstd_*_module.c'))
    units += [kernel / 'lib/xxhash.c']
    if wrapper is not None:
        units.append(wrapper)
    library = output / (name + '.so')
    subprocess.run(['cc', *compiler_flags(), '-fPIC', '-shared',
                    '-Wl,-Bsymbolic-functions', '-Wl,-z,defs',
                    '-DZSTD_DISABLE_ASM=1', '-DZSTD_NO_INTRINSICS=1',
                    '-D__LITTLE_ENDIAN=1', *extra_flags, '-I' + str(include),
                    '-I' + str(codec), '-include', str(HERE / 'compat.h'),
                    *map(str, units), '-o', str(library)], check=True)
    return library
