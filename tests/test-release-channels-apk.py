#!/usr/bin/env python3
"""Signed APK channel transitions in a disposable root; no device or services.
The packages use fixture payloads and the production core package inventory.
"""
import argparse
import importlib.util
import functools
import http.server
import json
import shlex
import threading
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--apk', type=Path, required=True)
parser.add_argument('--commands', type=Path, required=True, help='production commands exported by Go integration test')
args = parser.parse_args()
commands = json.loads(args.commands.read_text())
apk = str(args.apk.resolve())
spec = importlib.util.spec_from_file_location('versions', ROOT / 'scripts/package-versions.py')
versions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versions)
core = list(versions.PACKAGES)
# The rollback must stay complete when packaging adds/removes a core component.
assert set(commands['core']) == set(core)

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
    class QuietHandler(http.server.SimpleHTTPRequestHandler):
        def log_message(self, *_):
            pass
    httpd = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(QuietHandler, directory=str(work)))
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    feeds = {channel: f'http://127.0.0.1:{httpd.server_port}/{channel}/Packages.adb' for channel in feeds}
    root = work / 'root'
    (root / 'etc/apk/keys').mkdir(parents=True)
    (root / 'etc/apk/keys/test.pub').write_bytes(pub.read_bytes())
    repositories = root / 'etc/apk/repositories'
    (root / 'var/cache/apk').mkdir(parents=True)
    common = ['--root', root, '--arch', 'riscv64']
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
    query = [arg.replace('@REPOSITORY@', feeds['stable']) for arg in commands['query']]
    # Model the cache miss reported on a device; successful index refresh does
    # not guarantee that a query with different repository options can reuse it.
    for cached in (root / 'var/cache/apk').iterdir():
        if cached.is_file():
            cached.unlink()
    bad = subprocess.run([apk, *map(str, common), '--no-network', *query], capture_output=True)
    available = run(*common, *query).splitlines()
    assert bad.returncode != 0 or set(bad.stdout.decode().splitlines()) != set(available)
    assert set(available) == {n + '-3.3.4-r0' for n in core}
    before = installed()
    install = commands['install']
    run(*common, *install, '--no-scripts', '--simulate')
    assert installed() == before
    # Model reboot after the virtual-package transaction but before helper
    # cleanup. The restored v3.3.4 updater knows only ordinary apk upgrade.
    run(*common, *install, '--no-scripts')
    assert '.nanokvm-stable-rollback' in world_path.read_text()
    select('final')
    run(*common, 'upgrade', '--no-scripts')
    assert all(n + '-3.3.4-r0' in installed() for n in core), 'negative control did not retain pins'
    select('stable')
    service = root / 'etc/init.d/nanokvm-rollback-recovery'
    link = root / 'etc/runlevels/boot/nanokvm-rollback-recovery'
    service.parent.mkdir(parents=True)
    link.parent.mkdir(parents=True)
    wrapper = work / 'recovery-apk'
    wrapper.write_text('#!/bin/sh\nexec ' + shlex.join([apk, *map(str, common), '--no-scripts']) + ' "$@"\n')
    wrapper.chmod(0o700)
    recovery = commands['recovery'].replace('/sbin/apk', str(wrapper)).replace('/lib/apk/', str(root / 'lib/apk') + '/').replace('/etc/apk/', str(root / 'etc/apk') + '/').replace('/etc/init.d/', str(root / 'etc/init.d') + '/').replace('/etc/runlevels/', str(root / 'etc/runlevels') + '/')
    service.write_text(recovery)
    link.symlink_to(service)
    # The recovery must not need the downgraded updater or a reachable feed.
    repositories.write_text('http://127.0.0.1:1/unreachable/Packages.adb\n')
    subprocess.run(['sh', '-c', '. "$1"; start', 'sh', str(service)], check=True)
    assert not service.exists() and not link.exists()
    assert '.nanokvm-stable-rollback' not in world_path.read_text()
    # A boot before the package transaction is also harmless and self-cleans.
    service.write_text(recovery)
    link.symlink_to(service)
    subprocess.run(['sh', '-c', '. "$1"; start', 'sh', str(service)], check=True)
    assert not service.exists() and not link.exists()
    # Recovery failure retains both files for the next boot.
    select('beta')
    run(*common, 'upgrade', '--no-scripts')
    select('stable')
    run(*common, *install, '--no-scripts')
    service.write_text(recovery.replace(str(wrapper), '/bin/false'))
    link.symlink_to(service)
    failed = subprocess.run(['sh', '-c', '. "$1"; start', 'sh', str(service)])
    assert failed.returncode != 0 and service.exists() and link.exists()
    service.write_text(recovery)
    subprocess.run(['sh', '-c', '. "$1"; start', 'sh', str(service)], check=True)
    assert all(n + '-3.3.4-r0' in installed() for n in core)
    assert {'nanokvm-rustdesk-2.0-r0', 'alpine-fixture-2.0-r0'} <= installed(), installed()
    assert (root / 'etc/apk/world').read_bytes() == world
    assert cfg.read_text() == 'user setting'
    assert (root / 'data/user-file').read_text() == 'keep'
    # Resume with the old stable updater's ordinary command, no new-helper cleanup.
    select('final')
    run(*common, 'upgrade', '--no-scripts')
    assert all(n + '-3.4.0-r0' in installed() for n in core)
    select('final')
    run(*common, 'upgrade', '--no-scripts')
    assert all(n + '-3.4.0-r0' in installed() for n in core)
    assert cfg.read_text() == 'user setting'
    httpd.shutdown()
    print('PASS: signed stable -> beta -> stable rollback -> old-updater final, interrupted reboot recovery; 13 core packages, exact kernel/modules, config/data/world and independent packages preserved')
