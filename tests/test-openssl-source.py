#!/usr/bin/env python3
"""Apply every OpenSSL patch to the pinned source and execute its C906 tests."""
import argparse
import hashlib
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', type=Path, help='reuse a downloaded pinned archive')
    parser.add_argument('--cross-prefix', default='riscv64-linux-gnu-')
    parser.add_argument('--negative-control', action='store_true')
    args = parser.parse_args()
    pins = [line.split() for line in (ROOT / 'platform/sources.lock').read_text().splitlines()
            if line.startswith('openssl ')]
    if len(pins) != 1:
        raise SystemExit('expected exactly one OpenSSL source pin')
    _, url, digest = pins[0]
    with tempfile.TemporaryDirectory(prefix='openssl-source-test-') as temporary:
        work = Path(temporary)
        archive = args.archive
        if archive is None:
            archive = work / 'openssl.tar.gz'
            with urllib.request.urlopen(url, timeout=60) as response, archive.open('wb') as dest:
                shutil.copyfileobj(response, dest)
        with archive.open('rb') as stream:
            actual = hashlib.file_digest(stream, 'sha256').hexdigest()
        if actual != digest:
            raise SystemExit('OpenSSL source checksum mismatch')
        with tarfile.open(archive) as tar:
            tar.extractall(work, filter='data')
        source = work / url.rsplit('/', 1)[-1].removesuffix('.tar.gz')
        patches = sorted((ROOT / 'firmware/crypto/openssl-c906').glob('*.patch'))
        if not patches:
            raise SystemExit('no OpenSSL patches found')
        for patch in patches:
            subprocess.run(['patch', '--batch', '--forward', '--fuzz=0', '-p1',
                            '-i', str(patch)], cwd=source, check=True)
        # Generate the real pinned headers, without building another libcrypto.
        subprocess.run(['perl', 'Configure', 'linux64-riscv64',
                        '--cross-compile-prefix=' + args.cross_prefix], cwd=source, check=True)
        subprocess.run(['make', '-s', '-j2', 'build_generated'], cwd=source, check=True)
        command = ['python3', str(ROOT / 'firmware/crypto/experimental/openssl-sha256/test-scalar.py'),
                   '--source', str(source), '--cross-prefix', args.cross_prefix]
        if args.negative_control:
            command.append('--negative-control')
        subprocess.run(command, check=True)
        command[1] = str(ROOT / 'firmware/crypto/experimental/openssl-ghash/test-arithmetic.py')
        subprocess.run(command, check=True)
        print(f'PASS: {len(patches)} complete patches applied with zero fuzz to SHA-256-verified source')


if __name__ == '__main__':
    main()
