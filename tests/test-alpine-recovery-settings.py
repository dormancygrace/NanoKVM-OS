#!/usr/bin/env python3
"""Run real recovery functions and BusyBox tar, without mounts or devices."""
from pathlib import Path
import os
import shlex
import shutil
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
source = (repo / 'firmware/alpine/recovery/init').read_text()
functions = source[source.index('verify_sha256() {'):source.index('mount_payloads() {')]
functions += source[source.index('validate_settings() {'):source.index('validate_root() {')]
assert source.index('\nvalidate_settings\n') < source.index('\nmkfs.f2fs ')
busybox = shutil.which('busybox')
assert busybox, 'BusyBox is required'


def check(name, files, directories=(), damage=None, no_archive=False):
    with tempfile.TemporaryDirectory(prefix='recovery-settings-') as tmp:
        t = Path(tmp)
        old = t / 'old'
        root = t / 'realroot'
        old.mkdir()
        for directory in directories:
            (old / directory).mkdir(parents=True, exist_ok=True)
            (old / directory).chmod(0o700)
        for path, data in files.items():
            file = old / path
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text(data)
            file.chmod(0o600)
        (root / 'root/.ssh').mkdir(parents=True)
        (root / 'root/.ssh/authorized_keys').write_text('IMAGE KEY MUST NOT SURVIVE')
        (root / 'etc/ssh').mkdir(parents=True)
        (root / 'etc/ssh/ssh_host_ed25519_key').write_text('IMAGE HOST KEY')
        (root / 'etc/ssh/sshd_config').write_text('image config')
        (root / 'etc/kvm').mkdir(parents=True)
        (root / 'etc/kvm/mdns_disabled').write_text('1\n')
        archive = t / 'settings.tar'
        if not no_archive:
            paths = [p for p in ('root/.ssh', 'etc/ssh', 'etc/hosts', 'etc/kvm') if (old / p).exists()]
            subprocess.run([busybox, 'tar', '--numeric-owner', '-cf', str(archive), '-C', str(old), *paths], check=True)
        recovery_busybox = busybox
        if damage == 'hash-failure':
            # find -exec with a semicolon can hide failed child commands.
            # Fail only per-file hashing; the archive hash still succeeds.
            wrapper = t / 'fault-busybox'
            wrapper.write_text('#!/bin/sh\n'
                               'if [ "$1" = sha256sum ]; then\n'
                               '    case "$2" in ./*) exit 23 ;; esac\n'
                               'fi\nexec ' + shlex.quote(busybox) + ' "$@"\n')
            wrapper.chmod(0o755)
            recovery_busybox = str(wrapper)
        func = functions.replace('/busybox', shlex.quote(recovery_busybox))
        for path in ('/realroot', '/root-shadow.saved', '/settings-check', '/settings.paths', '/settings.sha256'):
            func = func.replace(path, str(t) + path)
        # For missing-restored-file coverage, inject loss after successful tar
        # extraction but before the production integrity checks.
        if damage == 'missing-file':
            marker = '\twhile IFS= read -r path; do'
            at = func.index(marker, func.index('restore_settings() {'))
            func = func[:at] + f'\trm -f {root}/root/.ssh/authorized_keys\n' + func[at:]
        between = ''
        if damage == 'archive-changed':
            between = f"printf corrupt >> '{archive}'\n"
        if damage == 'corrupt-before':
            archive.write_bytes(b'not a tar archive')
        script = 'set -eu\nfail() { echo "$*" >&2; exit 17; }\n' + func
        script += f"\nSETTINGS_BACKUP='{archive}'\nvalidate_settings\n{between}restore_settings\n"
        result = subprocess.run(['sh', '-c', script], text=True, capture_output=True, timeout=20)
        if damage:
            assert result.returncode == 17, (name, result)
            if damage == 'hash-failure':
                assert 'cannot hash preserved settings' in result.stderr, result
                assert (root / 'root/.ssh/authorized_keys').read_text() == 'IMAGE KEY MUST NOT SURVIVE'
            print('PASS:', name, '(failure detected)')
            return
        assert result.returncode == 0, (name, result)
        for path, data in files.items():
            assert (root / path).read_text() == data, (name, path)
            assert (root / path).stat().st_mode & 0o777 == 0o600
            assert (root / path).stat().st_uid == (old / path).stat().st_uid
            assert (root / path).stat().st_gid == (old / path).stat().st_gid
        for directory in directories:
            assert (root / directory).is_dir()
            assert (root / directory).stat().st_mode & 0o777 == 0o700
        for path in ('root/.ssh/authorized_keys', 'etc/ssh/ssh_host_ed25519_key'):
            if path not in files:
                assert not (root / path).exists(), (name, 'inherited image credentials', path)
        if (old / 'etc/kvm').is_dir():
            # Restoring enabled mDNS must remove the fresh image's marker.
            assert (root / 'etc/kvm/mdns_disabled').exists() == ('etc/kvm/mdns_disabled' in files), name
        print('PASS:', name)

check('empty client directory', {}, ('root/.ssh',))
check('known hosts only', {'root/.ssh/known_hosts': 'known host\n'})
check('outbound private key only', {'root/.ssh/id_ed25519': 'client key\n'})
check('SSH configuration only', {'etc/ssh/sshd_config': 'Port 22\n'})
check('alternate host identity', {'etc/ssh/ssh_host_rsa_key': 'RSA key\n'})
complete = {'root/.ssh/authorized_keys': 'operator key\n', 'etc/ssh/ssh_host_ed25519_key': 'host key\n',
            'etc/ssh/ssh_host_ed25519_key.pub': 'host public key\n', 'etc/hosts': '127.0.0.1 localhost\n'}
check('complete identity', complete)
check('no optional SSH paths', {'etc/hosts': '127.0.0.1 localhost\n'})
check('no archive', {}, no_archive=True)
check('mDNS enabled survives new image default', {}, ('etc/kvm',))
check('mDNS disabled stays disabled', {'etc/kvm/mdns_disabled': '1\n'})
check('missing expected restored file', complete, damage='missing-file')
check('archive changed after validation', complete, damage='archive-changed')
check('invalid archive before formatting', complete, damage='corrupt-before')
check('per-file hash failure before formatting', complete, damage='hash-failure')

# Exercise the real service with a rooted ssh-keygen invocation. -A creates
# missing keys once and leaves existing private/public keys byte-for-byte intact.
with tempfile.TemporaryDirectory(prefix='recovery-keygen-') as tmp:
    t = Path(tmp)
    (t / 'etc/ssh').mkdir(parents=True)
    (t / 'bin').mkdir()
    keygen = shutil.which('ssh-keygen')
    assert keygen
    (t / 'bin/ssh-keygen').write_text(f'#!/bin/sh\nexec {keygen} "$@" -f "{t}"\n')
    (t / 'bin/ssh-keygen').chmod(0o755)
    script = (repo / 'firmware/alpine/openrc/nanokvm-ssh').read_text()
    script = script.replace('/etc/kvm/ssh_stop', str(t / 'ssh_stop')).replace('/boot/start_ssh_once', str(t / 'start_ssh_once'))
    script = script.replace('rc-service', 'rc_service')
    script += '\nrc_service() { test -s "$KEY_DIR/ssh_host_ed25519_key"; }\neinfo() { :; }\nstart\n'
    env = dict(os.environ, PATH=str(t / 'bin') + ':' + os.environ['PATH'], KEY_DIR=str(t / 'etc/ssh'))
    subprocess.run(['sh', '-c', script], env=env, check=True, timeout=30)
    keys = {p.name: p.read_bytes() for p in (t / 'etc/ssh').iterdir()}
    assert keys
    subprocess.run(['sh', '-c', script], env=env, check=True, timeout=30)
    assert keys == {p.name: p.read_bytes() for p in (t / 'etc/ssh').iterdir()}
    (t / 'ssh_stop').touch()
    # Disabled SSH must not invoke either key generation or sshd.
    (t / 'bin/ssh-keygen').write_text('#!/bin/sh\nexit 99\n')
    subprocess.run(['sh', '-c', script.replace('\nstart\n', '\nrc_service() { exit 99; }; start\n')], env=env, check=True)
print('PASS: unique first-start host keys, preserved existing identity and disabled SSH')
