#!/usr/bin/env python3
"""usb-flags-migrate moves the USB flags from /boot to /etc/kvm/usb, also
when a power cut interrupted an earlier run or a rollback wrote /boot again."""
from pathlib import Path
import os
import subprocess
import tempfile

script = Path(__file__).resolve().parents[1] / 'firmware/alpine/compat/nanokvm-usb-flags-migrate'


def run(boot, flags):
    env = dict(os.environ, NANOKVM_USB_FLAGS_BOOT=str(boot), NANOKVM_USB_FLAGS_DIR=str(flags))
    result = subprocess.run(['sh', str(script)], env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    return result.stdout


def tree(path):
    if not path.exists():
        return None
    return {p.name: p.read_text() for p in sorted(path.iterdir())}


def fixture():
    t = Path(tempfile.mkdtemp(prefix='usb-flags-'))
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
(new / '.complete').write_text('')
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
(new / '.complete').write_text('')
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
