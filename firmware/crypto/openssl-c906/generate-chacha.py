#!/usr/bin/env python3
"""Verify the userspace ChaCha assembly against our kernel generator."""
import argparse
import importlib.util
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[3]
SOURCE = ROOT / 'firmware/cpu/go-crypto-c906/gen_chacha_linux.py'

def generate(prefix):
    spec = importlib.util.spec_from_file_location('chacha_linux', SOURCE)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    code = module.build()
    module.verify(prefix, code.vectors)
    text = module.HEADER + code.render()
    text = text.replace('#include <linux/linkage.h>', '''/* BSD-3-Clause option; C ABI wrapper for OpenSSL. */
#define SYM_FUNC_START(n) .globl n; .hidden n; .type n,@function; .p2align 4; n:
#define SYM_FUNC_END(n) .size n,.-n''')
    text = text.replace('chacha_xtheadvector', 'ossl_chacha20_c906_blocks')
    text = text.replace('caller holds kernel_vector_begin().',
                        'caller checks the current thread vector control.')
    return text + '\n.section .note.GNU-stack,"",@progbits\n'

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binutils', required=True)
    p.add_argument('--check', action='store_true')
    a = p.parse_args()
    generated = generate(a.binutils)
    if a.check:
        patch = (Path(__file__).parent / '0001-chacha20-c906.patch').read_text()
        section = patch.split('+++ b/crypto/chacha/chacha-c906.S\n', 1)[1]
        added = ''.join(line[1:] for line in section.splitlines(keepends=True)
                        if line.startswith('+'))
        if added != generated:
            sys.exit('ChaCha assembly differs from the verified kernel generator')
        print('ChaCha userspace assembly matches the kernel generator; encodings verified')
    else:
        print(generated, end='')

if __name__ == '__main__':
    main()
