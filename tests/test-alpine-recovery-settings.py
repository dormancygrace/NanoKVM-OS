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
# Preserved and recorded before the format, or resumed from the record.
main = source[source.index('\nmount_initial_filesystems\n'):]
assert main.index('\tvalidate_settings\n\tmark_formatting\n') < main.index('\nmkfs.f2fs ')
assert main.index('if [ -f "$FORMAT_MARKER" ]') < main.index('backup_settings') < main.index('\nmkfs.f2fs ')
# The record goes only after boot.sd is the new image.
assert main.index('boot.sd || fail') < main.index('rm -f "/realroot$FORMAT_MARKER"')
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
        elif no_archive:
            # Nothing preserved: the new root keeps the rootfs default, mDNS off.
            assert (root / 'etc/kvm/mdns_disabled').exists(), name
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


def check_resume(name, case):
    """A run after a power cut during the format takes the recorded settings."""
    with tempfile.TemporaryDirectory(prefix='recovery-resume-') as tmp:
        t = Path(tmp)
        old = t / 'old'
        (old / 'etc/kvm').mkdir(parents=True)
        (old / 'etc/kvm/server.yaml').write_text('user settings\n')
        root = t / 'realroot'
        (root / 'etc').mkdir(parents=True)
        (root / 'etc/shadow').write_text('root:IMAGE:1::::::\nnobody:!:1::::::\n')
        archive = t / 'settings.tar'
        marker = t / 'recovery-formatting'
        shadow = t / "data-root-shadow.saved"
        subprocess.run([busybox, 'tar', '--numeric-owner', '-cf', str(archive), '-C', str(old), 'etc/kvm'], check=True)
        func = functions.replace('/busybox', shlex.quote(busybox))
        for path in ('/realroot', '/root-shadow.saved', '/settings-check', '/settings.paths', '/settings.sha256'):
            func = func.replace(path, str(t) + path)
        setup = (f"SETTINGS_BACKUP='{archive}'\nFORMAT_MARKER='{marker}'\nSHADOW_SAVED='{shadow}'\n")
        # The interrupted run: validated and recorded (mark_formatting's content).
        first = subprocess.run(['sh', '-c', 'set -eu\nfail() { echo "$*" >&2; exit 17; }\n' + func + setup +
                                'validate_settings\nprintf "%s\\n" "${SETTINGS_SHA256:-none}"'],
                               text=True, capture_output=True, check=True)
        saved = 'root:SAVED:19000:0:99999:7:::\n'
        shadow.write_text(saved)
        digest = subprocess.run([busybox, 'sha256sum', str(shadow)], capture_output=True, text=True,
                                check=True).stdout.split()[0]
        marker.write_text(first.stdout + digest + '\n')
        if case == 'shadow-empty':
            shadow.write_text('')
        elif case == 'shadow-truncated':
            shadow.write_text('root:$6$trunc')
        elif case == 'shadow-changed':
            shadow.write_text('root:OTHER:19000:0:99999:7:::\n')
        elif case == 'shadow-malformed':
            # A well-formed digest of a malformed entry, as a careless writer could leave.
            shadow.write_text('root:x\nbin:x\n')
            digest = subprocess.run([busybox, 'sha256sum', str(shadow)], capture_output=True, text=True,
                                    check=True).stdout.split()[0]
            marker.write_text(first.stdout + digest + '\n')
        if case == 'changed':
            archive.write_bytes(archive.read_bytes() + b'x')
        elif case == 'missing-archive':
            archive.unlink()
        elif case == 'missing-shadow':
            shadow.unlink()
        elif case in ('none', 'none-but-archive'):
            marker.write_text('none\n' + digest + '\n')
            if case == 'none':
                archive.unlink()
        script = ('set -eu\nfail() { echo "$*" >&2; exit 17; }\n' + func + setup +
                  'resume_settings\nrestore_settings\n')
        result = subprocess.run(['sh', '-c', script], text=True, capture_output=True, timeout=20)
        if case in ('ok', 'none'):
            assert result.returncode == 0, (name, result)
            assert (root / 'etc/shadow').read_text().splitlines()[0] == saved.strip(), name
            if case == 'ok':
                assert (root / 'etc/kvm/server.yaml').read_text() == 'user settings\n', name
            print('PASS:', name)
        else:
            assert result.returncode == 17, (name, result)
            assert (root / 'etc/shadow').read_text().startswith('root:IMAGE'), name
            print('PASS:', name, '(refused)')


check_resume('resume restores the recorded settings and password', 'ok')
check_resume('resume without preserved settings', 'none')
check_resume('resume refuses a changed archive', 'changed')
check_resume('resume refuses a missing archive', 'missing-archive')
check_resume('resume refuses a missing password record', 'missing-shadow')
check_resume('resume refuses an archive that appeared later', 'none-but-archive')
check_resume('resume refuses an emptied password record', 'shadow-empty')
check_resume('resume refuses a truncated password record', 'shadow-truncated')
check_resume('resume refuses a changed password record', 'shadow-changed')
check_resume('resume refuses a malformed password record', 'shadow-malformed')
