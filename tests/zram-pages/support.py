# SPDX-License-Identifier: GPL-2.0-only
import os


def compiler_flags():
    flags = ['-std=gnu11', '-O3', '-g', '-fno-strict-aliasing',
             '-fno-tree-vectorize', '-fno-tree-slp-vectorize']
    if os.environ.get('NANOKVM_SANITIZE') == '1':
        flags += ['-O1', '-fsanitize=address,undefined',
                  '-fno-sanitize-recover=all', '-fno-omit-frame-pointer']
    return flags
