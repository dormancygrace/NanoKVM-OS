#!/usr/bin/env python3
"""Build the pinned OpenSSL source with reviewed NanoKVM patches for NanoKVM's C906/musl runtime."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

from nanokvm_cpu_profile import flags, record

ROOT = Path(__file__).resolve().parents[1]
CONFIG = ['linux64-riscv64', '--prefix=/usr', '--libdir=lib', '--openssldir=/etc/ssl',
          'enable-fips', 'enable-ktls', 'shared', 'no-zlib', 'no-async', 'no-comp',
          'no-idea', 'no-mdc2', 'no-rc5', 'no-ec2m', 'no-ssl3', 'no-seed',
          'no-weak-ssl-ciphers']


def source_pin():
    rows = [l.split() for l in (ROOT / 'platform/sources.lock').read_text().splitlines()
            if l.startswith('openssl ')]
    if len(rows) != 1:
        raise ValueError('Expected exactly one OpenSSL source pin')
    _, url, digest = rows[0]
    return url, digest


def build(archive, output, cross, jobs):
    url, digest = source_pin()
    if hashlib.sha256(archive.read_bytes()).hexdigest() != digest:
        raise ValueError('OpenSSL source checksum mismatch')
    if output.exists():
        raise ValueError('OpenSSL output must be a new directory: ' + str(output))
    output.mkdir(parents=True)
    # A pinned release tarball only, with Python's traversal/symlink protection.
    with tarfile.open(archive) as tar:
        tar.extractall(output, filter='data')
    source = output / url.rsplit('/', 1)[-1].removesuffix('.tar.gz')
    patches = sorted((ROOT / 'firmware/crypto/openssl-c906').glob('*.patch'))
    subprocess.run(['python3', str(ROOT / 'firmware/crypto/openssl-c906/generate-chacha.py'),
                    '--binutils', str(cross), '--check'], check=True)
    subprocess.run(['python3', str(ROOT / 'firmware/crypto/openssl-c906/generate-sha256.py'),
                    '--check'], check=True)
    subprocess.run(['python3', str(ROOT / 'firmware/crypto/openssl-c906/generate-ghash.py'),
                    '--binutils', str(cross), '--check'], check=True)
    patch_records = []
    for patch in patches:
        subprocess.run(['patch', '--batch', '--forward', '--fuzz=0', '-p1',
                        '-i', str(patch)], cwd=source, check=True)
        patch_records.append({'file': patch.name,
                              'sha256': hashlib.sha256(patch.read_bytes()).hexdigest()})
    compiler = Path(str(cross) + 'gcc')
    cflags = flags('openssl') + ['-g0', '-fstack-protector-strong',
                               '-fstack-clash-protection', '-Wformat',
                               '-Werror=format-security', '-D_FORTIFY_SOURCE=2']
    record(output / 'cpu-profile.json', compiler, 'openssl', cflags)
    env = dict(os.environ)
    for key in ('CC', 'CXX', 'CPP', 'AR', 'RANLIB', 'CROSS_COMPILE', 'CFLAGS',
                'CXXFLAGS', 'CPPFLAGS', 'LDFLAGS', 'MAKEFLAGS'):
        env.pop(key, None)
    env.update(PATH=str(compiler.parent) + os.pathsep + env.get('PATH', ''),
               CC='gcc', AR='ar', RANLIB='ranlib', CFLAGS=' '.join(cflags),
               LDFLAGS='-Wl,--as-needed,-O1,--sort-common,-z,relro,-z,now',
               # Upstream release date; unrelated commits must not change libcrypto.
               SOURCE_DATE_EPOCH='1790640000')
    prefix = Path(cross).name
    command = ['perl', './Configure', *CONFIG, '--cross-compile-prefix=' + prefix,
               '-Wa,--noexecstack']
    log = output / 'build.log'
    with log.open('w') as stream:
        for cmd in (command, ['make', '-j' + str(jobs), 'build_sw'],
                    ['make', 'DESTDIR=' + str(output / 'install'), 'install_sw', 'install_ssldirs']):
            subprocess.run(cmd, cwd=source, env=env, stdout=stream,
                           stderr=subprocess.STDOUT, check=True)
    install = output / 'install'
    runtime = output / 'runtime'
    # Development/static/FIPS artifacts are not part of this runtime package.
    for rel in ('usr/bin/openssl', 'usr/lib/libcrypto.so.3', 'usr/lib/libssl.so.3',
                'etc/ssl/openssl.cnf', 'etc/ssl/ct_log_list.cnf'):
        dest = runtime / rel
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(install / rel, dest)
    for directory in ('usr/lib/engines-3', 'usr/lib/ossl-modules'):
        for src in sorted((install / directory).glob('*.so')):
            if src.name == 'fips.so':
                continue
            dest = runtime / directory / src.name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dest)
    for path in runtime.rglob('*'):
        if path.is_file() and path.read_bytes()[:4] == b'\x7fELF':
            subprocess.run([str(cross) + 'strip', '--strip-unneeded',
                            '--remove-section=.comment', '--remove-section=.note', str(path)], check=True)
    license_dir = runtime / 'usr/share/licenses/nanokvm-openssl'
    license_dir.mkdir(parents=True)
    shutil.copy2(source / 'LICENSE.txt', license_dir / 'LICENSE.txt')
    shutil.copy2(ROOT / 'firmware/cpu/go-crypto-c906/LICENSE',
                 license_dir / 'C906-BSD-3-Clause.txt')
    info_dir = runtime / 'usr/share/nanokvm/openssl'
    info_dir.mkdir(parents=True)
    shutil.copy2(output / 'cpu-profile.json', info_dir / 'cpu-profile.json')
    metadata = {'source': url, 'sha256': digest, 'configure': command[2:],
                'custom_crypto_code': bool(patch_records), 'patches': patch_records,
                'cryptodma': any(p['file'].endswith('-dma.patch') for p in patch_records)}
    (info_dir / 'source.json').write_text(json.dumps(metadata, indent=2) + '\n')
    return runtime


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--archive', required=True, type=Path)
    p.add_argument('--output', required=True, type=Path)
    p.add_argument('--cross-prefix', required=True)
    p.add_argument('--jobs', type=int, default=4)
    a = p.parse_args()
    print(build(a.archive.resolve(), a.output.resolve(), a.cross_prefix, a.jobs))


if __name__ == '__main__':
    main()
