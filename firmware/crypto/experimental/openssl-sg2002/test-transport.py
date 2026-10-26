#!/usr/bin/env python3
"""Exercise direct-DMA transport failure semantics with ASan/UBSan on the host."""
import argparse
import re
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[4]

def added_files(patch):
    files = {}
    path = None
    for line in patch.splitlines(keepends=True):
        if line.startswith('--- '):
            path = None
            new = line.strip() == '--- /dev/null'
        elif line.startswith('+++ b/') and new:
            path = line[6:].strip()
            files[path] = ''
        elif path is not None and line.startswith('+'):
            files[path] += line[1:]
    return files

def apply_transport_updates(directory):
    # Exercise the transport after every later per-algorithm patch as shipped.
    for patch in sorted((ROOT / 'firmware/crypto/openssl-c906').glob('*.patch')):
        for section in re.split(r'(?m)(?=^--- )', patch.read_text()):
            if section.startswith('--- a/crypto/sg2002.c\n'):
                subprocess.run(['patch', '--batch', '--fuzz=0', '-p1'],
                               input=section, text=True, cwd=directory,
                               stdout=subprocess.DEVNULL, check=True)

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--no-sanitizers', action='store_true')
    p.add_argument('--negative-control', action='store_true')
    a = p.parse_args()
    patch = ROOT / 'firmware/crypto/openssl-c906/0002-sg2002-transport.patch'
    files = added_files(patch.read_text())
    # Never drift from the real kernel ioctl ABI, including its syscall license.
    assert files['include/crypto/sg2002_crypto.h'] == (ROOT / 'platform/modules/sg2002-aes/sg2002_crypto.h').read_text()
    with tempfile.TemporaryDirectory(prefix='nkos-sg2002-test-') as tmp:
        d = Path(tmp)
        for name, text in files.items():
            dest = d / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_text(text)
        apply_transport_updates(d)
        flags = [] if a.no_sanitizers else ['-fsanitize=address,undefined']
        binary = d / 'test'
        subprocess.run(['cc', '-O1', '-g', '-Wall', '-Wextra', '-Werror', *flags,
                        '-I' + str(d / 'crypto'), '-I' + str(d / 'include'),
                        str(Path(__file__).with_suffix('.c')), '-lcrypto', '-o', str(binary)], check=True)
        subprocess.run([str(binary)], check=True)
        if a.negative_control:
            source = d / 'crypto/sg2002.c'
            original = source.read_text()
            broken = original.replace('r.output = hash ? 0 : (uintptr_t)scratch;',
                                      'r.output = request->output;')
            assert broken != original
            source.write_text(broken)
            subprocess.run(['cc', '-O1', '-g', '-Wall', '-Wextra', '-Werror', *flags,
                            '-I' + str(d / 'crypto'), '-I' + str(d / 'include'),
                            str(Path(__file__).with_suffix('.c')), '-lcrypto', '-o', str(binary)], check=True)
            result = subprocess.run([str(binary)], capture_output=True)
            assert result.returncode != 0 and b'buf[k]' in result.stderr, (
                'Unsafe output commit was not detected by the expected assertion', result.stderr)
            print('PASS negative control: direct writes to caller output rejected')

if __name__ == '__main__':
    main()
