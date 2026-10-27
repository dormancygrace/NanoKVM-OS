#!/usr/bin/env python3
"""Real abuild/apk/OpenSSL EC signing and RSA-to-EC upgrade test (Alpine 3.24).
Run in the CI container; no production key, network repository or device writes.
"""
import gzip
import hashlib
import io
import os
import shutil
import subprocess
import tarfile
import tempfile
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def run(*args, ok=True, **kwargs):
    result = subprocess.run([str(a) for a in args], capture_output=True, text=True, **kwargs)
    if ok and (result.returncode or 'ERROR:' in result.stderr):
        raise AssertionError(f"{args}: {result.stdout[-4000:]} {result.stderr[-4000:]}")
    if not ok:
        assert result.returncode != 0, args
    return result

def tar_member(name, data):
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode='w') as archive:
        info = tarfile.TarInfo(name)
        info.size, info.mode = len(data), 0o644
        if not name.startswith('.'):
            info.pax_headers = {'APK-TOOLS.checksum.SHA1': hashlib.sha1(data).hexdigest()}
        archive.addfile(info, io.BytesIO(data))
        end = archive.offset
    return stream.getvalue()[:end]

def member(blob):
    decoder = zlib.decompressobj(31)
    decoded = decoder.decompress(blob)
    assert decoder.eof
    return decoded, blob[:len(blob) - len(decoder.unused_data)], decoder.unused_data

with tempfile.TemporaryDirectory() as directory:
    work = Path(directory)
    ec, rsa, wrong = (work / name for name in ('ec', 'rsa', 'wrong'))
    for path in (ec, rsa, wrong):
        path.mkdir()
    key, public = ec / 'test.key', ec / 'test.pub'
    run('openssl', 'genpkey', '-algorithm', 'EC', '-pkeyopt', 'ec_paramgen_curve:P-256', '-out', key)
    run('openssl', 'pkey', '-in', key, '-pubout', '-out', public)
    run('openssl', 'genrsa', '-out', rsa / 'test.rsa', '2048')
    run('openssl', 'pkey', '-in', rsa / 'test.rsa', '-pubout', '-out', rsa / 'test.rsa.pub')
    run('openssl', 'genpkey', '-algorithm', 'EC', '-pkeyopt', 'ec_paramgen_curve:P-256', '-out', wrong / 'test.key')
    run('openssl', 'pkey', '-in', wrong / 'test.key', '-pubout', '-out', wrong / 'test.pub')
    validate = ROOT / 'scripts/check-ec-key.sh'
    run(validate, key, public)
    run(validate, rsa / 'test.rsa', rsa / 'test.rsa.pub', ok=False)
    run(validate, key, wrong / 'test.pub', ok=False)
    run('openssl', 'pkey', '-in', key, '-aes256', '-passout', 'pass:test', '-out', work / 'encrypted.key')
    run(validate, work / 'encrypted.key', public, ok=False)
    run('openssl', 'pkey', '-in', key, '-aes256', '-passout', 'pass:', '-out', work / 'empty-pass.key')
    run(validate, work / 'empty-pass.key', public, ok=False)
    run('openssl', 'genpkey', '-algorithm', 'EC', '-pkeyopt', 'ec_paramgen_curve:P-384', '-out', work / 'p384.key')
    run('openssl', 'pkey', '-in', work / 'p384.key', '-pubout', '-out', work / 'p384.pub')
    run(validate, work / 'p384.key', work / 'p384.pub')
    payloads = work / 'payloads'
    for name in ('base', 'kernel-sg2002', 'kmod-sg2002', 'firmware-sg2002',
                 'bootloader-sg2002', 'app', 'web', 'zlib', 'openssl', 'exfatprogs', 'i2c-tools'):
        path = payloads / name / 'usr/share/ec-test'
        path.mkdir(parents=True)
        (path / name).write_text(name)
    keys_payload = payloads / 'keys/etc/apk/keys'
    keys_payload.mkdir(parents=True)
    for source in (public, rsa / 'test.rsa.pub'):
        (keys_payload / source.name).write_bytes(source.read_bytes())
    apk_wrapper = work / 'apk-builder'
    apk_wrapper.write_text(f'#!/bin/sh\nexec /sbin/apk --keys-dir {ec} "$@"\n')
    apk_wrapper.chmod(0o755)
    env = {**os.environ, 'APK': str(apk_wrapper), 'PACKAGER_PRIVKEY': str(key), 'PACKAGER_PUBKEY': str(public),
           'PAYLOAD_ROOT': str(payloads), 'REPODEST': str(work / 'repo'),
           'SRCDEST': str(work / 'sources'), 'ABUILD_FLAGS': '-F -d',
           'PKGVER': '3.4.0', 'SOURCE_DATE_EPOCH': '1792627200'}
    # Synthetic payloads have no ELF objects. Disable dependency scanning only
    # in these temporary recipe copies; real release builds retain it unchanged.
    fixture = work / 'src'
    (fixture / 'scripts').mkdir(parents=True)
    shutil.copytree(ROOT / 'firmware/alpine/packages', fixture / 'firmware/alpine/packages')
    shutil.copy2(ROOT / 'firmware/alpine/release.env', fixture / 'firmware/alpine/release.env')
    for filename in ('build-alpine-packages.sh', 'check-ec-key.sh'):
        shutil.copy2(ROOT / 'scripts' / filename, fixture / 'scripts' / filename)
    for name in ('nanokvm-base', 'nanokvm-kernel-sg2002', 'nanokvm-kmod-sg2002', 'nanokvm-firmware-sg2002'):
        recipe = fixture / 'firmware/alpine/packages' / name / 'APKBUILD'
        with recipe.open('a') as stream:
            stream.write('\noptions="$options !tracedeps"\n')
    run('sh', fixture / 'scripts/build-alpine-packages.sh', 'stock', env=env)
    repo = work / 'repo/stock/recipes/riscv64'
    packages = sorted(repo.glob('*.apk'))
    assert len(packages) == 14
    for path in packages + [repo / 'APKINDEX.tar.gz']:
        run('apk', '--keys-dir', ec, 'verify', path)
        run('apk', '--keys-dir', rsa, 'verify', path, ok=False)
        run('apk', '--keys-dir', wrong, 'verify', path, ok=False)
        # Check the actual cryptography, not the legacy RSA256 filename marker.
        signatures, _, rest = member(path.read_bytes())
        with tarfile.open(fileobj=io.BytesIO(signatures)) as archive:
            names = archive.getnames()
            assert names == ['.SIGN.RSA256.test.pub'], names
            (work / 'signature').write_bytes(archive.extractfile(names[0]).read())
        _, control, _ = member(rest)
        (work / 'control').write_bytes(control)
        run('openssl', 'dgst', '-sha256', '-verify', public, '-signature', work / 'signature', work / 'control')
        tampered = work / path.name
        data = bytearray(path.read_bytes())
        data[len(data) // 2] ^= 1
        tampered.write_bytes(data)
        run('apk', '--keys-dir', ec, 'verify', tampered, ok=False)
    run('apk', '--keys-dir', ec, '--sign-key', key, 'mkndx', '--output', repo / 'Packages.adb', *packages)
    run('apk', '--keys-dir', ec, 'verify', repo / 'Packages.adb')
    run('apk', '--keys-dir', rsa, 'verify', repo / 'Packages.adb', ok=False)
    # Model an installed 3.3.x nanokvm-keys: its RSA-signed package delivers
    # the EC public key before the EC-only update, just like published 3.3.x.
    directories = b''
    for name in ('etc/', 'etc/apk/', 'etc/apk/keys/'):
        entry = tarfile.TarInfo(name)
        entry.type, entry.mode = tarfile.DIRTYPE, 0o755
        directories += entry.tobuf()
    data = gzip.compress(directories + b''.join(tar_member('etc/apk/keys/' + p.name, p.read_bytes())
                                 for p in (public, rsa / 'test.rsa.pub')) + bytes(1024))
    control = gzip.compress(tar_member('.PKGINFO', (
        'pkgname = nanokvm-keys\npkgver = 3.3.4-r0\narch = riscv64\n'
        'pkgdesc = previous trust bundle\nsize = 1024\ndatahash = ' + hashlib.sha256(data).hexdigest() + '\n').encode()))
    (work / 'old-control').write_bytes(control)
    run('openssl', 'dgst', '-sha256', '-sign', rsa / 'test.rsa', '-out', work / 'old-signature', work / 'old-control')
    old = work / 'nanokvm-keys-3.3.4-r0.apk'
    old.write_bytes(gzip.compress(tar_member('.SIGN.RSA256.test.rsa.pub', (work / 'old-signature').read_bytes())) + control + data)
    old_repo = work / 'old/riscv64'
    old_repo.mkdir(parents=True)
    shutil.copy2(old, old_repo / old.name)
    run('apk', '--keys-dir', rsa, 'index', '--output', old_repo / 'APKINDEX.tar.gz', old_repo / old.name)
    run('abuild-sign', '-t', 'RSA256', '-k', rsa / 'test.rsa', '-p', rsa / 'test.rsa.pub', old_repo / 'APKINDEX.tar.gz')
    # Only external shell capability is a fixture. Production package scripts
    # remain in the real APKs but are not executed on this test root.
    runtime = work / 'test-shell-1-r0.apk'
    run('apk', '--sign-key', key, 'mkpkg', '--info', 'name:test-shell',
        '--info', 'version:1-r0', '--info', 'arch:riscv64', '--info', 'provides:/bin/sh', '--output', runtime)
    for index in ('APKINDEX.tar.gz', 'Packages.adb'):
        target = work / ('root-' + index)
        (target / 'etc/apk/keys').mkdir(parents=True)
        (target / 'etc/apk/keys/test.rsa.pub').write_bytes((rsa / 'test.rsa.pub').read_bytes())
        (target / 'etc/apk/repositories').write_text(str(old_repo.parent) + '\n')
        base = ('apk', '--root', target, '--arch', 'riscv64')
        run(*base, 'add', '--no-scripts', '--initdb', 'nanokvm-keys')
        assert (target / 'etc/apk/keys/test.pub').read_bytes() == public.read_bytes()
        run(*base, 'add', '--no-scripts', runtime)
        url = str(repo.parent) if index == 'APKINDEX.tar.gz' else str(repo / index)
        (target / 'etc/apk/repositories').write_text(url + '\n')
        run(*base, 'update')
        upgrade = run(*base, 'upgrade', '--no-scripts', 'nanokvm-keys')
        installed = run(*base, 'list', '--installed', 'nanokvm-keys').stdout
        assert '3.4.0-r0' in installed, (index, upgrade.stdout, installed)
    # Pages re-signing resets earlier signatures, leaving exactly EC trust.
    run('apk', '--keys-dir', ec, '--sign-key', rsa / 'test.rsa', '--sign-key', key, 'mkndx', '--output', repo / 'Packages.adb', *packages)
    run('apk', '--keys-dir', rsa, 'verify', repo / 'Packages.adb')
    run('apk', '--allow-untrusted', '--sign-key', key, 'adbsign', '--reset-signatures', repo / 'Packages.adb')
    run('apk', '--keys-dir', ec, 'verify', repo / 'Packages.adb')
    run('apk', '--keys-dir', rsa, 'verify', repo / 'Packages.adb', ok=False)
    print('PASS: 14 real abuild packages; ECDSA/SHA-256 verified by apk and OpenSSL; tamper/wrong-key rejected; v2/v3 upgrade and index rotation')
