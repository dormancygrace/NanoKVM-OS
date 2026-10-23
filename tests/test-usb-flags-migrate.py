#!/usr/bin/env python3
"""usb-flags-migrate moves the USB flags from /boot to /etc/kvm/usb, also
when a power cut interrupted an earlier run or a rollback wrote /boot again."""
from pathlib import Path
import os
import fcntl
import shutil
import atexit
import subprocess
import tempfile

fixtures = []
atexit.register(lambda: [t.cleanup() for t in fixtures])

script = Path(__file__).resolve().parents[1] / 'firmware/alpine/compat/nanokvm-usb-flags-migrate'


def run(boot, flags, ok=True, **extra):
    env = dict(os.environ, NANOKVM_USB_FLAGS_BOOT=str(boot), NANOKVM_USB_FLAGS_DIR=str(flags),
               NANOKVM_USB_FLAGS_LAYOUT=str(boot.parent / "layout"),
               NANOKVM_USB_FLAGS_PIDOF="false")
    env.update(extra)
    result = subprocess.run(['sh', str(script)], env=env, capture_output=True, text=True)
    assert (result.returncode == 0) == ok, (result.returncode, result.stderr)
    return result.stdout


def tree(path):
    if not path.exists():
        return None
    return {p.name: p.read_text() for p in sorted(path.iterdir()) if p.name != ".layout-v1"}


def fixture():
    temp = tempfile.TemporaryDirectory(prefix='usb-flags-')
    fixtures.append(temp)
    t = Path(temp.name)
    (t / 'layout').write_text('1\n')
    boot, kvm = t / 'boot', t / 'etc/kvm'
    boot.mkdir()
    kvm.mkdir(parents=True)
    # not USB flags: they stay on /boot
    (boot / 'wifi.sta').write_text('')
    (boot / 'usb.idVendor').write_text('0x1234\n')
    return boot, kvm / 'usb'


# An installation from before the move.
boot, flags = fixture()
(boot / 'usb.disk0').write_text('/data/debian.iso\n')
(boot / 'usb.vid').write_text('0x046d\n')
(boot / 'disable_hid').write_text('')
(boot / 'rndis.ipv4_prefix').write_text('10.0.0\n')
out = run(boot, flags)
assert tree(flags) == {'disable_hid': '', 'rndis.ipv4_prefix': '10.0.0\n', 'usb.disk0': '/data/debian.iso\n',
                       'usb.vid': '0x046d\n'}, tree(flags)
assert all((flags / n).stat().st_mode & 0o777 == 0o644 for n in tree(flags))
assert tree(boot) == {'usb.idVendor': '0x1234\n', 'wifi.sta': ''}, tree(boot)
assert not flags.with_name('usb.new').exists() and not flags.with_name('usb.old').exists()
assert 'Moved the USB flags' in out
# A second run changes nothing.
assert run(boot, flags) == '' and tree(flags)['usb.vid'] == '0x046d\n'
print('PASS: migrates an older installation once')

# A fresh root: the flags come with it, /boot has none.
boot, flags = fixture()
flags.mkdir()
(flags / 'usb.dev').write_text('')
(flags / 'usb.disk0').write_text('')
run(boot, flags)
assert tree(flags) == {'usb.dev': '', 'usb.disk0': ''}
print('PASS: leaves a fresh root alone')

# A rollback wrote /boot again: that set replaces the older one entirely.
boot, flags = fixture()
flags.mkdir()
(flags / 'usb.ncm').write_text('')
(flags / 'usb.vid').write_text('0x046d\n')
(boot / 'usb.acm').write_text('')
run(boot, flags)
assert tree(flags) == {'usb.acm': ''}, tree(flags)
assert 'usb.acm' not in tree(boot)
print('PASS: a set written by an older release after a rollback wins')

# Power cut after /boot was cleaned, before usb.new became usb.
boot, flags = fixture()
flags.mkdir()
(flags / 'usb.ncm').write_text('')
new = flags.with_name('usb.new')
new.mkdir()
(new / 'usb.audio').write_text('')
(new / '.complete').write_text('usb.audio\n')
run(boot, flags)
assert tree(flags) == {'usb.audio': ''}, tree(flags)
assert not new.exists()
print('PASS: finishes a complete usb.new')

# Power cut while usb.new was being written: /boot still has the flags.
boot, flags = fixture()
(boot / 'usb.audio').write_text('')
(boot / 'usb.acm').write_text('')
new = flags.with_name('usb.new')
new.mkdir()
(new / 'usb.audio').write_text('')
run(boot, flags)
assert tree(flags) == {'usb.acm': '', 'usb.audio': ''}, tree(flags)
print('PASS: redoes an incomplete usb.new from /boot')

# A power cut between moving usb aside and putting usb.new in place.
boot, flags = fixture()
old = flags.with_name('usb.old')
old.mkdir()
(old / 'usb.ncm').write_text('')
new = flags.with_name('usb.new')
new.mkdir()
(new / 'usb.acm').write_text('')
(new / '.complete').write_text('usb.acm\n')
run(boot, flags)
assert tree(flags) == {'usb.acm': ''} and not old.exists()
print('PASS: recovers between the two renames')

# Power cut in the middle of the /boot cleanup: the remnant is not a set.
boot, flags = fixture()
flags.mkdir()
(flags / 'usb.ncm').write_text('')
new = flags.with_name('usb.new')
new.mkdir()
(new / 'usb.acm').write_text('')
(new / 'usb.audio').write_text('')
(new / '.complete').write_text('usb.acm\nusb.audio\n')
(boot / 'usb.audio').write_text('')
run(boot, flags)
assert tree(flags) == {'usb.acm': '', 'usb.audio': ''}, tree(flags)
assert tree(boot) == {'usb.idVendor': '0x1234\n', 'wifi.sta': ''}, tree(boot)
print('PASS: a partial /boot cleanup is finished, not taken for a set')

# Power cut after the last rename: the marker and usb.old go.
boot, flags = fixture()
flags.mkdir()
(flags / 'usb.acm').write_text('')
(flags / '.complete').write_text('usb.acm\n')
old = flags.with_name('usb.old')
old.mkdir()
(old / 'usb.ncm').write_text('')
run(boot, flags)
assert tree(flags) == {'usb.acm': ''} and not old.exists()
print('PASS: clears the leftovers of a finished run')

# A truncated committed manifest must never delete a subset and then replace
# the full destination with the remaining source subset.
boot, flags = fixture()
new = flags.with_name('usb.new')
new.mkdir()
for name in ('usb.acm', 'usb.audio'):
    (boot / name).write_text('')
    (new / name).write_text('')
(new / '.complete').write_text('usb.acm\n')
before = tree(boot)
run(boot, flags, ok=False)
assert tree(boot) == before and not flags.exists()
# A manifest interrupted before its atomic publication is simply rebuilt.
(new / '.complete').rename(new / '.complete.tmp')
run(boot, flags)
assert tree(flags) == {'usb.acm': '', 'usb.audio': ''}
print('PASS: truncated manifest fails closed; unpublished manifest is rebuilt')

# New base alone, or an old running server during the transaction, must not
# take the legacy flags away from their writers.
for scenario in ('old-app', 'running-server'):
    boot, flags = fixture()
    (boot / 'usb.acm').write_text('')
    if scenario == 'old-app':
        (boot.parent / 'layout').unlink()
    env = dict(os.environ, NANOKVM_USB_FLAGS_BOOT=str(boot),
               NANOKVM_USB_FLAGS_DIR=str(flags),
               NANOKVM_USB_FLAGS_LAYOUT=str(boot.parent / 'layout'),
               NANOKVM_USB_FLAGS_PIDOF='true' if scenario == 'running-server' else 'false')
    subprocess.run(['sh', str(script)], env=env, check=True)
    assert (boot / 'usb.acm').exists() and not flags.exists()
print('PASS: base-only upgrade and running legacy writer defer migration')

# Inject an abrupt process death before and after every filesystem utility
# call. This models restart states, not storage hardware write guarantees.
# sync is doubled to avoid flushing the host once per simulated boundary.
with tempfile.TemporaryDirectory(prefix='usb-flags-fault-') as tmp:
    binpath = Path(tmp)
    wrapper = binpath / 'wrapper'
    wrapper.write_text('#!/bin/sh\nname=${0##*/}\nn=$(cat "$COUNT" 2>/dev/null || echo 0)\nn=$((n + 1))\nprintf \'%s\' "$n" > "$COUNT"\nif [ "$n" = "$FAIL_AT" ] && [ "$FAIL_WHEN" = before ]; then\n    kill -KILL "$PPID"; exit 99\nfi\nif [ "$name" != sync ]; then\n    "/usr/bin/$name" "$@" || exit $?\nfi\nif [ "$n" = "$FAIL_AT" ] && [ "$FAIL_WHEN" = after ]; then\n    kill -KILL "$PPID"; exit 99\nfi\n')
    wrapper.chmod(0o755)
    for name in ('cp', 'chmod', 'mv', 'rm', 'mkdir', 'sync'):
        (binpath / name).symlink_to(wrapper)
    def injected(fail_at, when):
        boot, flags = fixture()
        flags.mkdir()
        (flags / 'usb.ncm').write_text('old')
        desired = {'usb.acm': '', 'usb.audio': '', 'usb.disk0': '/data/test.iso\n'}
        for name, value in desired.items():
            (boot / name).write_text(value)
        counter = boot.parent / 'count'
        extra = dict(PATH=str(binpath) + ':' + os.environ['PATH'], COUNT=str(counter),
                     FAIL_AT=str(fail_at), FAIL_WHEN=when)
        run(boot, flags, ok=fail_at == 0, **extra)
        count = int(counter.read_text())
        run(boot, flags, **dict(extra, FAIL_AT='0'))
        assert tree(flags) == desired, (fail_at, when, tree(flags))
        assert not any((boot / n).exists() for n in desired)
        assert (flags / '.layout-v1').exists()
        return count
    count = injected(0, 'before')
    for point in range(1, count + 1):
        for when in ('before', 'after'):
            injected(point, when)
    print(f'PASS: recovery at {count * 2} filesystem command boundaries')

# Concurrent USB/application starts serialize the migration and retain the set.
boot, flags = fixture()
(boot / 'usb.acm').write_text('')
env = dict(os.environ, NANOKVM_USB_FLAGS_BOOT=str(boot),
           NANOKVM_USB_FLAGS_DIR=str(flags),
           NANOKVM_USB_FLAGS_LAYOUT=str(boot.parent / 'layout'),
           NANOKVM_USB_FLAGS_PIDOF='false')
workers = [subprocess.Popen(['sh', str(script)], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE) for _ in range(2)]
for worker in workers:
    out, err = worker.communicate(timeout=15)
    assert worker.returncode == 0, err
assert tree(flags) == {'usb.acm': ''}
print('PASS: concurrent migration requests serialize')

# Missing mount: refuse activation; no default gadget/application may start.
boot, flags = fixture()
env = dict(env, NANOKVM_USB_FLAGS_BOOT='', NANOKVM_USB_FLAGS_DIR=str(flags),
           NANOKVM_USB_FLAGS_LAYOUT=str(boot.parent / 'layout'))
source = script.read_text().replace('mountpoint -q "$boot"', 'false')
result = subprocess.run(['sh'], input=source, env=env, text=True, capture_output=True)
assert result.returncode != 0 and not (flags / '.layout-v1').exists()
print('PASS: absent boot mount refuses activation')

# Already migrated: a boot partition that does not mount must not stop the
# gadget or the application.
boot, flags = fixture()
flags.mkdir()
(flags / '.layout-v1').write_text('')
(flags / 'usb.ncm').write_text('')
env = dict(env, NANOKVM_USB_FLAGS_DIR=str(flags), NANOKVM_USB_FLAGS_LAYOUT=str(boot.parent / 'layout'))
# An empty mount table, whatever the machine running the test mounts.
no_mounts = boot.parent / 'mounts'
no_mounts.write_text('')
source = source.replace('/proc/mounts', str(no_mounts))
result = subprocess.run(['sh'], input=source, env=env, text=True, capture_output=True)
assert result.returncode == 0 and 'not mounted' in result.stderr, result
assert tree(flags) == {'usb.ncm': ''}, tree(flags)
# A migration still in progress keeps waiting for the mount.
(flags.parent / (flags.name + '.new')).mkdir()
result = subprocess.run(['sh'], input=source, env=env, text=True, capture_output=True)
assert result.returncode != 0, result
print('PASS: a settled layout starts without the boot mount')

# A settled layout skips pidof/cleanup after locking; a rollback takes the full run.
boot, flags = fixture()
flags.mkdir()
(flags / '.layout-v1').write_text('')
(flags / 'usb.dev').write_text('')
probe = boot.parent / 'pidof'
probe.write_text(f'#!/bin/sh\n: > "{boot.parent}/pidof-called"\nexit 1\n')
probe.chmod(0o755)
assert run(boot, flags, NANOKVM_USB_FLAGS_PIDOF=str(probe)) == ''
assert not (boot.parent / 'pidof-called').exists()
(boot / 'usb.ncm').write_text('')
assert 'Moved the USB flags' in run(boot, flags, NANOKVM_USB_FLAGS_PIDOF=str(probe))
assert (boot.parent / 'pidof-called').exists() and tree(flags) == {'usb.ncm': ''}, tree(flags)
print('PASS: a settled layout skips pidof and full migration')


# A settled-looking reader must wait for an in-flight writer, then recheck the
# flags rather than launching a gadget/server against the previous selection.
for shell in [['sh']] + ([['busybox', 'sh']] if shutil.which('busybox') else []):
    boot, flags = fixture()
    flags.mkdir()
    (flags / '.layout-v1').touch()
    (flags / 'usb.ncm').touch()
    env = dict(os.environ, NANOKVM_USB_FLAGS_BOOT=str(boot),
               NANOKVM_USB_FLAGS_DIR=str(flags),
               NANOKVM_USB_FLAGS_LAYOUT=str(boot.parent / 'layout'),
               NANOKVM_USB_FLAGS_PIDOF='false')
    with flags.with_suffix('.lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        reader = subprocess.Popen(shell + [str(script)], env=env,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            try:
                reader.communicate(timeout=.3)
            except subprocess.TimeoutExpired:
                pass
            else:
                raise AssertionError('settled reader returned while migration was locked')
            # The writer has removed /boot but not yet published usb.new.
            new = flags.with_suffix('.new')
            new.mkdir()
            (new / 'disable_hid').touch()
            (new / '.complete').write_text('disable_hid\n')
        finally:
            fcntl.flock(lock, fcntl.LOCK_UN)
            out, err = reader.communicate(timeout=15)
        assert reader.returncode == 0, err
        assert tree(flags) == {'disable_hid': ''}, tree(flags)
    print('PASS: settled reader serializes and rechecks:', shell)
