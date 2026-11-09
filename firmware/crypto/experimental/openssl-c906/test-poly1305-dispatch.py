#!/usr/bin/env python3
"""Compile the patched Poly1305 dispatcher (0012) with C models of Cryptogams and the vector kernel."""
import argparse
from pathlib import Path
import subprocess
import tempfile
ROOT = Path(__file__).resolve().parents[4]
SERIES = ROOT / 'firmware/crypto/openssl-c906'


def added_file(patch, name):
    section = patch.split('+++ b/' + name + '\n', 1)[1].split('\ndiff --git ', 1)[0].split('\n--- ', 1)[0]
    return ''.join(line[1:] for line in section.splitlines(keepends=True) if line.startswith('+'))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--negative-control', action='store_true')
    a = parser.parse_args()
    patch = (SERIES / '0012-poly1305-c906.patch').read_text()
    files = {'poly1305_c906.c': added_file(patch, 'crypto/poly1305/poly1305_c906.c'),
             'poly1305_c906.h': added_file(patch, 'crypto/poly1305/poly1305_c906.h')}
    with tempfile.TemporaryDirectory(prefix='poly1305-dispatch-') as td:
        p = Path(td)
        (p / 'crypto').mkdir()

        def write(change=None):
            for name, text in files.items():
                if change and change[0] == name:
                    assert change[1] in text, change
                    text = text.replace(change[1], change[2])
                (p / name).write_text(text)
        write()
        command = ['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-fsanitize=address,undefined',
                   '-fno-omit-frame-pointer', '-pthread', '-I' + td, str(Path(__file__).with_suffix('.c')),
                   '-lcrypto', '-o', str(p / 'test')]
        subprocess.run(command, check=True)
        subprocess.run([str(p / 'test')], check=True)
        if a.negative_control:
            for name, change in (
                ('syscall-free path', ('poly1305_c906.c', '        ossl_poly1305_blocks_c906v(r, inp, groups, l);',
                                       '        (void)prctl(70, 0L, 0L, 0L, 0L);\n        ossl_poly1305_blocks_c906v(r, inp, groups, l);')),
                ('threshold', ('poly1305_c906.c', 'groups >= OSSL_POLY1305_C906_MIN_GROUPS', 'groups >= 1')),
                ('h2 fold', ('poly1305_c906.c', 'c = (st[2] >> 2) * 5;', 'c = 0;')),
                ('h2 in the top limb', ('poly1305_c906.c', '(st[1] >> 40) | (st[2] << 24)', '(st[1] >> 40)')),
                ('carry before radix 2^64', ('poly1305_c906.c', 'l[2] += l[1] >> 26;', ';')),
            ):
                write(change)
                subprocess.run(command, check=True)
                run = subprocess.run([str(p / 'test')], capture_output=True, text=True)
                if run.returncode == 0 or 'Assertion' not in run.stderr:
                    raise SystemExit('negative control failed: ' + name + '\n' + run.stdout + run.stderr)
                print('PASS negative control: breaking the ' + name + ' is detected')


if __name__ == '__main__':
    main()
