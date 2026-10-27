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
    source = added_file(patch, 'crypto/chacha/chacha_c906.c')
    with tempfile.TemporaryDirectory(prefix='chacha-dispatch-') as td:
        p = Path(td)
        for name in ('crypto', 'internal'): (p / name).mkdir()
        (p / 'internal/cryptlib.h').write_text('char *ossl_safe_getenv(const char *);\n')
        (p / 'crypto/riscv_arch.h').write_text('extern int ossl_riscv_has_xtheadvector;\n')
        (p / 'crypto/chacha.h').write_text('void ChaCha20_ctr32_c(unsigned char *, const unsigned char *, size_t, const unsigned int [8], const unsigned int [4]);\n')
        (p / 'chacha_c906.h').write_text(added_file(patch, 'crypto/chacha/chacha_c906.h'))
        c = p / 'chacha_c906.c'
        c.write_text(source)
        command = ['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-fsanitize=address,undefined',
                   '-fno-omit-frame-pointer', '-I' + td, str(Path(__file__).with_suffix('.c')),
                   '-lcrypto', '-o', str(p / 'test')]
        subprocess.run(command, check=True)
        subprocess.run([str(p / 'test')], check=True)
        if a.negative_control:
            for name, old, new in (
                ('thread gate', 'control < 0 || (control & 3) != 2', 'control < -100'),
                ('capability gate', '!ossl_riscv_has_xtheadvector', '0'),
            ):
                assert old in source
                c.write_text(source.replace(old, new))
                subprocess.run(command, check=True)
                run = subprocess.run([str(p / 'test')], capture_output=True, text=True)
                if run.returncode == 0 or 'Assertion' not in run.stderr:
                    raise SystemExit('negative control failed: ' + name + '\n' + run.stderr)
                print('PASS negative control: removing ' + name + ' is detected')
if __name__ == '__main__': main()
