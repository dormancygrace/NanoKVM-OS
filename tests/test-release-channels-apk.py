#!/usr/bin/env python3
"""Signed APK channel transitions in a disposable root; no device or services.
The packages use fixture payloads and the production core package inventory.
"""
import argparse
import importlib.util
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--apk', type=Path, required=True)
args = parser.parse_args()
apk = str(args.apk.resolve())
spec = importlib.util.spec_from_file_location('versions', ROOT / 'scripts/package-versions.py')
versions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versions)
core = list(versions.PACKAGES)
# The rollback must stay complete when packaging adds/removes a core component.
source = (ROOT / 'server/osupdate/channel.go').read_text()
inventory = source.split('var corePackages = []string{', 1)[1].split('}', 1)[0]
assert set(re.findall(r'"(nanokvm[^" ]*)"', inventory)) == set(core)

def run(*cmd):
    p = subprocess.run([apk, *map(str, cmd)], capture_output=True, text=True)
    assert p.returncode == 0, (cmd, p.stdout, p.stderr)
    return p.stdout

with tempfile.TemporaryDirectory(prefix='nkos-channels-') as directory:
    work = Path(directory)
    key, pub = work / 'test.key', work / 'test.pub'
    subprocess.run(['openssl', 'genpkey', '-algorithm', 'EC', '-pkeyopt',
                    'ec_paramgen_curve:P-256', '-out', str(key)], check=True, capture_output=True)
    subprocess.run(['openssl', 'pkey', '-in', str(key), '-pubout', '-out', str(pub)], check=True, capture_output=True)
    feeds = {}
    for channel, version in [('stable', '3.3.4-r0'), ('beta', '3.4.0_beta1-r0'), ('final', '3.4.0-r0')]:
        repo = work / channel
        repo.mkdir()
        feeds[channel] = repo / 'Packages.adb'
        for name in [*core, 'nanokvm-rustdesk', 'alpine-fixture']:
            ver = version if name in core else ('2.0-r0' if channel == 'beta' else '1.0-r0')
            payload = repo / name
            path = payload / f'usr/share/{name}/version'
            path.parent.mkdir(parents=True)
            path.write_text(ver)
            if name == 'nanokvm':
                cfg = payload / 'etc/kvm/channel-fixture.conf'
                cfg.parent.mkdir(parents=True)
                cfg.write_text('default-' + version)
            info = [f'name:{name}', f'version:{ver}', 'arch:riscv64', 'license:MIT']
            if name == 'nanokvm-release':
                info.append('depends:' + ' '.join(n + '>=' + version for n in core if n != name))
            if name == 'nanokvm-kernel-sg2002':
                info.append('depends:nanokvm-kmod-sg2002=' + version)
            cmd = ['--sign-key', key, 'mkpkg', '--files', payload, '--output', repo / f'{name}-{ver}.apk']
            for value in info:
                cmd += ['--info', value]
            run(*cmd)
        run('--allow-untrusted', '--sign-key', key, 'mkndx', '--output', feeds[channel], *repo.glob('*.apk'))
    root = work / 'root'
    (root / 'etc/apk/keys').mkdir(parents=True)
    (root / 'etc/apk/keys/test.pub').write_bytes(pub.read_bytes())
    repositories = root / 'etc/apk/repositories'
    common = ['--root', root, '--arch', 'riscv64', '--no-cache']
    def select(channel):
        repositories.write_text(str(feeds[channel]) + '\n')
        run(*common, 'update')  # trusted index, no --allow-untrusted
    def installed():
        return set(run(*common, 'info', '-v').splitlines())
    repositories.write_text(str(feeds['stable']) + '\n')
    run(*common, 'add', '--initdb', *([] if os.geteuid() == 0 else ['--usermode']), '--no-scripts',
        'nanokvm-release', 'nanokvm-rustdesk', 'alpine-fixture')
    cfg = root / 'etc/kvm/channel-fixture.conf'
    cfg.write_text('user setting')
    (root / 'data').mkdir()
    (root / 'data/user-file').write_text('keep')
    world = (root / 'etc/apk/world').read_bytes()
    select('beta')
    run(*common, 'upgrade', '--no-scripts')
    assert all(n + '-3.4.0_beta1-r0' in installed() for n in core)
    # An unrelated explicit hold survives rollback unchanged.
    world_path = root / 'etc/apk/world'
    world_path.write_text(world_path.read_text().replace('alpine-fixture\n', 'alpine-fixture=2.0-r0\n'))
    world = world_path.read_bytes()
    select('stable')
    available = run(*common, 'search', '--exact', *core).splitlines()
    assert set(available) == {n + '-3.3.4-r0' for n in core}
    before = installed()
    targets = [n + '=3.3.4-r0' for n in core]
    run(*common, 'add', '--virtual', '.nanokvm-stable-rollback', '--no-scripts', *targets, '--simulate')
    assert installed() == before
    run(*common, 'add', '--virtual', '.nanokvm-stable-rollback', '--no-scripts', *targets)
    run(*common, 'del', '--no-scripts', '.nanokvm-stable-rollback')
    assert all(n + '-3.3.4-r0' in installed() for n in core)
    assert {'nanokvm-rustdesk-2.0-r0', 'alpine-fixture-2.0-r0'} <= installed(), installed()
    assert (root / 'etc/apk/world').read_bytes() == world
    assert cfg.read_text() == 'user setting'
    assert (root / 'data/user-file').read_text() == 'keep'
    select('beta')
    run(*common, 'upgrade', '--no-scripts')
    select('final')
    run(*common, 'upgrade', '--no-scripts')
    assert all(n + '-3.4.0-r0' in installed() for n in core)
    assert cfg.read_text() == 'user setting'
    print('PASS: signed stable -> beta -> stable rollback -> beta -> final; 13 core packages, exact kernel/modules, config/data/world and independent packages preserved')
