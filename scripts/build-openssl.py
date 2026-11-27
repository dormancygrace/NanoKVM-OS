#!/usr/bin/env python3
"""Build the pinned OpenSSL source with reviewed NanoKVM patches for NanoKVM's C906/musl runtime."""
import argparse
import collections
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

from nanokvm_cpu_profile import flags, record

ROOT = Path(__file__).resolve().parents[1]
# Not built: the FIPS provider (the runtime package never carried fips.so), kTLS (the kernel has no CONFIG_TLS) and
# the AF_ALG, CryptoAPI and VIA PadLock engines (the kernel has no AF_ALG; the other two are empty on RISC-V). The
# ENGINE API, loader_attic and the legacy provider stay: Alpine programs link the API, and the legacy provider has MD4,
# DES and Blowfish for WPA-Enterprise (wpa_supplicant loads it) and old OpenVPN profiles.
CONFIG = ['linux64-riscv64', '--prefix=/usr', '--libdir=lib', '--openssldir=/etc/ssl',
          'no-afalgeng', 'no-capieng', 'no-padlockeng', 'enable-ec_nistp_64_gcc_128', 'shared', 'no-zlib',
          'no-async', 'no-comp', 'no-idea', 'no-mdc2', 'no-rc5', 'no-ec2m', 'no-ssl3', 'no-seed',
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
    for bits in ('256', '512'):
        subprocess.run(['python3', str(ROOT / 'firmware/cpu/go-crypto-c906/gen_sha2_c906.py'), '--bits', bits,
                        '--name', 'ossl_sha%s_c906_blocks' % bits, '--check',
                        str(ROOT / 'firmware/crypto/openssl-c906/0003-sha256-dma.patch'),
                        'crypto/sha/sha%s-c906.S' % bits], check=True)
    subprocess.run(['python3', str(ROOT / 'firmware/cpu/go-crypto-c906/gen_ghash_table.py'),
                    '--flavor', 'openssl', '--tables', '2', '--name', 'ossl_ghash_blocks_c906', '--check',
                    str(ROOT / 'firmware/crypto/openssl-c906/0010-ghash-c906.patch')], check=True)
    subprocess.run(['python3', str(ROOT / 'firmware/cpu/go-crypto-c906/gen_poly1305_vector.py'),
                    '--binutils', str(cross), '--openssl', '--check',
                    str(ROOT / 'firmware/crypto/openssl-c906/0012-poly1305-c906.patch')], check=True)
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
    # One section per function, so that the linker can place the functions of
    # a TLS record next to each other (hot-sections.ld). The C906 has a 2-way
    # 32 KiB instruction cache and no L2: hot code spread over the library
    # evicted itself (1.2M to 2.0M misses per run of the record benchmark,
    # depending on unrelated layout changes; 0.5M once grouped).
    cflags.append('-ffunction-sections')
    order = ROOT / 'firmware/crypto/openssl-c906/hot-sections.ld'
    record(output / 'cpu-profile.json', compiler, 'openssl', cflags)
    env = dict(os.environ)
    for key in ('CC', 'CXX', 'CPP', 'AR', 'RANLIB', 'CROSS_COMPILE', 'CFLAGS',
                'CXXFLAGS', 'CPPFLAGS', 'LDFLAGS', 'MAKEFLAGS'):
        env.pop(key, None)
    env.update(PATH=str(compiler.parent) + os.pathsep + env.get('PATH', ''),
               CC='gcc', AR='ar', RANLIB='ranlib', CFLAGS=' '.join(cflags),
               LDFLAGS='-Wl,--as-needed,-O1,--sort-common,-z,relro,-z,now,--section-ordering-file=' + str(order),
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
    check_hot_layout(order, [source / 'libcrypto.so.3', source / 'libssl.so.3'], str(cross) + 'nm')
    install = output / 'install'
    runtime = output / 'runtime'
    # Development and static artifacts are not part of this runtime package.
    for rel in ('usr/bin/openssl', 'usr/lib/libcrypto.so.3', 'usr/lib/libssl.so.3',
                'etc/ssl/openssl.cnf', 'etc/ssl/ct_log_list.cnf'):
        dest = runtime / rel
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(install / rel, dest)
    for directory in ('usr/lib/engines-3', 'usr/lib/ossl-modules'):
        for src in sorted((install / directory).glob('*.so')):
            dest = runtime / directory / src.name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dest)
    for path in runtime.rglob('*'):
        if path.is_file() and path.read_bytes()[:4] == b'\x7fELF':
            subprocess.run([str(cross) + 'strip', '--strip-unneeded',
                            '--remove-section=.comment', '--remove-section=.note', str(path)], check=True)
    license_dir = runtime / 'usr/share/licenses/nanokvm-openssl3'
    license_dir.mkdir(parents=True)
    shutil.copy2(source / 'LICENSE.txt', license_dir / 'LICENSE.txt')
    shutil.copy2(ROOT / 'firmware/cpu/go-crypto-c906/LICENSE',
                 license_dir / 'C906-BSD-3-Clause.txt')
    shutil.copy2(ROOT / 'firmware/cpu/go-crypto-c906/CRYPTOGAMS-LICENSE',
                 license_dir / 'CRYPTOGAMS-BSD-3-Clause.txt')
    info_dir = runtime / 'usr/share/nanokvm/openssl'
    info_dir.mkdir(parents=True)
    shutil.copy2(output / 'cpu-profile.json', info_dir / 'cpu-profile.json')
    metadata = {'source': url, 'sha256': digest, 'configure': command[2:],
                'custom_crypto_code': bool(patch_records), 'patches': patch_records,
                'cryptodma': any(p['file'].endswith('-dma.patch') for p in patch_records)}
    (info_dir / 'source.json').write_text(json.dumps(metadata, indent=2) + '\n')
    return runtime


def check_hot_layout(order, libraries, nm):
    """hot-sections.ld names functions of libcrypto and libssl; a new OpenSSL
    may rename or inline them, and the linker ignores a pattern that matches
    nothing. Nearly all names must still be functions of one of the two
    libraries, and those of each library next to each other."""
    names = {line.split('(.text.', 1)[1].rstrip(')') for line in
             (line.strip() for line in order.read_text().splitlines()) if '(.text.' in line}
    found, spans = set(), []
    for library in libraries:
        out = subprocess.run([nm, '-n', str(library)], check=True, capture_output=True, text=True).stdout
        rows = [row for row in (line.split() for line in out.splitlines())
                if len(row) == 3 and row[1] in 'tT' and row[2] in names]
        found.update(row[2] for row in rows)
        # A static name of several files (the NIST curves) is listed for one of them only.
        count = collections.Counter(row[2] for row in rows)
        hot = [int(row[0], 16) for row in rows if count[row[2]] == 1]
        spans.append(max(hot) - min(hot) if hot else 0)
    if not names:
        raise SystemExit('%s lists no function; regenerate it (firmware/crypto/openssl-c906/README.md)' % order)
    if len(found) < len(names) * 0.9 or max(spans) > 320 * 1024:
        raise SystemExit('hot-sections.ld no longer matches libcrypto and libssl: %d of %d functions, span %d KiB; '
                         'regenerate it (firmware/crypto/openssl-c906/README.md)'
                         % (len(found), len(names), max(spans) // 1024))


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
