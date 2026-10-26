#!/usr/bin/env python3
"""Verify the OpenSSL GHASH assembly against the shared C906 kernel generator."""
import argparse
import importlib.util
from pathlib import Path
import sys
ROOT = Path(__file__).resolve().parents[3]
def generate(prefix):
    spec = importlib.util.spec_from_file_location('ghash_linux', ROOT / 'firmware/cpu/go-crypto-c906/gen_ghash_linux.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    text, _, vectors, _ = module.render()
    module.encoder().verify(prefix, vectors)
    module.selftest(text, 200)
    text = text.replace('#include <linux/linkage.h>', '/* BSD-3-Clause option; caller checks the current thread vector control. */\n#define SYM_FUNC_START(n) .globl n; .hidden n; .type n,@function; .p2align 4; n:\n#define SYM_FUNC_END(n) .size n,.-n')
    text = text.replace('ghash_blocks_c906', 'ossl_ghash_blocks_c906')
    return text + '\n.section .note.GNU-stack,"",@progbits\n'
def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binutils', required=True)
    p.add_argument('--check', action='store_true')
    a = p.parse_args()
    generated = generate(a.binutils)
    if a.check:
        patch = Path(__file__).with_name('0010-ghash-c906.patch').read_text()
        section = patch.split('+++ b/crypto/modes/ghash-c906.S\n', 1)[1].split('\ndiff --git ', 1)[0]
        added = ''.join(line[1:] for line in section.splitlines(keepends=True) if line.startswith('+'))
        if added != generated:
            sys.exit('GHASH assembly differs from the verified kernel generator')
        print('GHASH assembly matches; 200 cases per primitive and vector encodings verified')
    else:
        print(generated, end='')
if __name__ == '__main__':
    main()
