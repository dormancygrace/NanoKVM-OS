#!/usr/bin/env python3
"""Compile the patched dispatcher itself with mocked hardware and syscall edges."""
import argparse
from pathlib import Path
import subprocess
import tempfile
ROOT = Path(__file__).resolve().parents[4]

def added_file(patch, name):
    section = patch.split('+++ b/' + name + '\n', 1)[1].split('\n--- ', 1)[0]
    return ''.join(line[1:] for line in section.splitlines(keepends=True)
                   if line.startswith('+'))

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--negative-control', action='store_true')
    a = parser.parse_args()
    patch = (ROOT / 'firmware/crypto/openssl-c906/0001-chacha20-c906.patch').read_text()
    files = {'chacha_c906.c': added_file(patch, 'crypto/chacha/chacha_c906.c')}
    with tempfile.TemporaryDirectory(prefix='chacha-dispatch-') as td:
        p = Path(td)
        for name in ('crypto', 'internal'): (p / name).mkdir()
        (p / 'internal/cryptlib.h').write_text('char *ossl_safe_getenv(const char *);\n')
        (p / 'crypto/riscv_arch.h').write_text('extern int ossl_riscv_has_xtheadvector;\n')
        (p / 'crypto/chacha.h').write_text('void ChaCha20_ctr32_c(unsigned char *, const unsigned char *, size_t, const unsigned int [8], const unsigned int [4]);\n')
        (p / 'chacha_c906.h').write_text(added_file(patch, 'crypto/chacha/chacha_c906.h'))
        def write(changed=None):
            for name, text in files.items():
                if changed and changed[0] == name:
                    assert changed[1] in text, changed
                    text = text.replace(changed[1], changed[2])
                (p / name).write_text(text)
        write()
        command = ['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-fsanitize=address,undefined',
                   '-fno-omit-frame-pointer', '-pthread', '-I' + td, str(Path(__file__).with_suffix('.c')),
                   '-lcrypto', '-o', str(p / 'test')]
        subprocess.run(command, check=True)
        subprocess.run([str(p / 'test')], check=True)
        if a.negative_control:
            for name, change in (
                ('capability gate', ('chacha_c906.c', '!ossl_riscv_has_xtheadvector', '0')),
                ('threshold', ('chacha_c906.c', 'len < OSSL_CHACHA_C906_MIN_BYTES ||', 'len < 1 ||')),
                ('syscall-free path', ('chacha_c906.c', '    state[0] = 0x61707865;',
                                       '    (void)prctl(70, 0L, 0L, 0L, 0L);\n    state[0] = 0x61707865;')),
            ):
                write(change)
                subprocess.run(command, check=True)
                run = subprocess.run([str(p / 'test')], capture_output=True, text=True)
                if run.returncode == 0 or 'Assertion' not in run.stderr:
                    raise SystemExit('negative control failed: ' + name + '\n' + run.stderr)
                print('PASS negative control: removing ' + name + ' is detected')
if __name__ == '__main__': main()
