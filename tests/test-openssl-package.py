#!/usr/bin/env python3
"""Real abuild/apk transition from Alpine OpenSSL to the NanoKVM runtime.

Run in Alpine with alpine-sdk, openssl and Python installed. --payload uses the
cross-built runtime; without it, fixture files test the package graph in CI.
No target program is executed; --root confines changes to the test directory.
"""
import argparse
import os
import re
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def run(*args, **kwargs):
    p = subprocess.run([str(x) for x in args], capture_output=True, text=True, **kwargs)
    if p.returncode:
        raise AssertionError((args, p.stdout, p.stderr))
    return p.stdout


def installed(root):
    return {l[2:] for l in (root / 'lib/apk/db/installed').read_text().splitlines()
            if l.startswith('P:')}


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--payload', type=Path)
    ap.add_argument('--output', type=Path)
    a = ap.parse_args()
    pin = next(line.split()[1] for line in (ROOT / 'platform/sources.lock').read_text().splitlines()
               if line.startswith('openssl '))
    upstream = re.search(r'/openssl-([0-9]+\.[0-9]+\.[0-9]+)\.tar\.gz$', pin).group(1)
    recipe_text = (ROOT / 'firmware/alpine/packages/nanokvm-openssl/APKBUILD').read_text()
    aliases = re.search(r'^provides="([^"]+)"', recipe_text, re.M).group(1).split()
    for name in ('openssl', 'openssl3', 'libcrypto3', 'libssl3', 'cmd:openssl'):
        assert name + '=' + upstream + '-r100' in aliases, (name, upstream, aliases)
    with tempfile.TemporaryDirectory(prefix='nkos-openssl-') as tmp:
        w = Path(tmp)
        recipe = w / 'recipes/nanokvm-openssl'
        recipe.mkdir(parents=True)
        shutil.copy2(ROOT / 'firmware/alpine/packages/nanokvm-openssl/APKBUILD', recipe)
        source = w / 'nanokvm-openssl-3.6.0'
        if a.payload:
            shutil.copytree(a.payload, source)
        else:
            for name in ('usr/bin/openssl', 'usr/lib/libcrypto.so.3', 'usr/lib/libssl.so.3',
                         'etc/ssl/openssl.cnf', 'etc/ssl/ct_log_list.cnf'):
                p = source / name
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text('NanoKVM package fixture\n')
        archive = recipe / 'nanokvm-openssl-3.6.0.tar.gz'
        with tarfile.open(archive, 'w:gz') as tar:
            tar.add(source, arcname=source.name)
        key, public = w / 'test.key', w / 'test.pub'
        run('openssl', 'genpkey', '-algorithm', 'EC', '-pkeyopt', 'ec_paramgen_curve:P-256', '-out', key)
        run('openssl', 'pkey', '-in', key, '-pubout', '-out', public)
        # The same RSA256 marker/ECDSA signing policy as the production builder.
        binpath = w / 'bin'
        binpath.mkdir()
        wrapper = binpath / 'abuild-sign'
        wrapper.write_text('#!/bin/sh\nexec /usr/bin/abuild-sign -t RSA256 -k "$PACKAGER_PRIVKEY" -p "$PACKAGER_PUBKEY" "$@"\n')
        wrapper.chmod(0o755)
        apk_wrapper = binpath / 'apk-builder'
        apk_wrapper.write_text('#!/bin/sh\nexec /sbin/apk --keys-dir ' + str(w) + ' \"$@\"\n')
        apk_wrapper.chmod(0o755)
        env = dict(os.environ, APK=str(apk_wrapper), CARCH='riscv64', PKGVER='3.6.0', REPODEST=str(w / 'repo'),
                   PACKAGER_PRIVKEY=str(key), PACKAGER_PUBKEY=str(public),
                   SOURCE_DATE_EPOCH='1790640000', PATH=str(binpath) + ':' + os.environ['PATH'])
        run('abuild', '-F', 'checksum', cwd=recipe, env=env)
        run('abuild', '-F', '-d', cwd=recipe, env=env)
        package = w / 'repo/recipes/riscv64/nanokvm-openssl-3.6.0-r0.apk'
        assert package.is_file()
        feeds = {}
        for version, dependency, channel in [('3.5.0-r0', 'openssl', 'stock'),
                                              ('3.6.0-r0', 'nanokvm-openssl=3.6.0-r0', 'custom')]:
            repo = w / channel
            repo.mkdir()
            run('apk', '--sign-key', key, 'mkpkg', '--info', 'name:nanokvm-release',
                '--info', 'version:' + version, '--info', 'arch:riscv64',
                '--info', 'depends:' + dependency, '--output', repo / ('release-' + version + '.apk'))
            if channel == 'custom':
                shutil.copy2(package, repo)
            index = repo / 'Packages.adb'
            run('apk', '--keys-dir', w, '--sign-key', key, 'mkndx', '--output', index, *repo.glob('*.apk'))
            feeds[channel] = index
        target = w / 'root'
        # The Docker host may be x86_64; riscv64 uses another official Alpine key.
        shutil.copytree('/usr/share/apk/keys', target / 'etc/apk/keys')
        shutil.copy2(public, target / 'etc/apk/keys/test.pub')
        def select(channel):
            (target / 'etc/apk/repositories').write_text(
                'https://dl-cdn.alpinelinux.org/alpine/v3.24/main\n' + str(feeds[channel]) + '\n')
        select('stock')
        base = ['apk', '--root', target, '--arch', 'riscv64']
        run(*base, 'add', '--initdb', '--no-scripts', 'busybox', 'openssl', 'curl',
            'openssh-client-default', 'apk-tools', 'nanokvm-release')
        stock = {'openssl', 'libcrypto3', 'libssl3'}
        assert stock <= installed(target)
        config = target / 'etc/ssl/openssl.cnf'
        custom = config.read_bytes() + b'\n# keep user configuration\n'
        config.write_bytes(custom)
        if a.output:
            a.output.mkdir(parents=True, exist_ok=True)
            for name in ('libcrypto.so.3', 'libssl.so.3'):
                shutil.copy2(target / 'usr/lib' / name, a.output / ('alpine-' + name))
            shutil.copy2(package, a.output)
            shutil.copy2(public, a.output / 'test.pub')
        select('custom')
        transaction = run(*base, 'upgrade', '--no-scripts')
        assert 'nanokvm-openssl' in installed(target), transaction
        assert not stock & installed(target), (transaction, installed(target))
        assert {'curl', 'openssh-client-default', 'apk-tools'} <= installed(target)
        assert config.read_bytes() == custom
        for name in ('usr/bin/openssl', 'usr/lib/libcrypto.so.3', 'usr/lib/libssl.so.3'):
            assert (target / name).read_bytes() == (source / name).read_bytes()
        # An ordinary full upgrade must not silently restore Alpine's copies.
        run(*base, 'upgrade', '--no-scripts')
        assert not stock & installed(target)
        # A missing component in a broken newer index must not silently remove
        # a runtime which that release still requires. APK must reject atomically.
        before = {name: (target / name).read_bytes() for name in
                  ('lib/apk/db/installed', 'etc/apk/world', 'etc/ssl/openssl.cnf')}
        rejected = subprocess.run([str(x) for x in [*base, 'add', '--no-scripts',
            '--virtual', '.nanokvm-stable-rollback', 'nanokvm-release=3.6.0-r0',
            '!nanokvm-openssl', 'openssl']], capture_output=True, text=True)
        assert rejected.returncode > 0, (rejected.stdout, rejected.stderr)
        for name, content in before.items():
            assert (target / name).read_bytes() == content, name
        # The production rollback uses this solver constraint when the old
        # release index predates nanokvm-openssl. One atomic APK transaction.
        select('stock')
        run(*base, 'add', '--no-scripts', '--virtual', '.nanokvm-stable-rollback',
            'nanokvm-release=3.5.0-r0', '!nanokvm-openssl', 'openssl')
        run(*base, 'del', '--no-scripts', '.nanokvm-stable-rollback')
        assert stock <= installed(target)
        assert 'nanokvm-openssl' not in installed(target)
        assert config.read_bytes() == custom
        print('PASS: signed real APK, Alpine -> NanoKVM -> Alpine; curl/ssh/apk dependencies and user configuration retained')


if __name__ == '__main__':
    main()
