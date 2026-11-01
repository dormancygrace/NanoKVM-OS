#!/usr/bin/env python3
"""A FAT or exFAT volume left dirty is checked before it is mounted.

Runs firmware/alpine/compat/nanokvm-check-volume without root against small
boot-sector files and stub fsck tools: a clean volume runs nothing, a dirty
one runs the right tool, and no outcome stops the boot. When the host has
dosfstools or exfatprogs, a real image is also made dirty and checked, and
the flag must be clear afterwards.
"""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
source = (repo / 'firmware/alpine/compat/nanokvm-check-volume').read_text()
PATH_LINE = 'export PATH=/usr/sbin:/usr/bin:/sbin:/bin'
assert PATH_LINE in source


def boot_sector(kind, dirty):
    b = bytearray(512)
    if kind == 'exfat':
        b[3:11] = b'EXFAT   '
        b[106] = 2 if dirty else 0
    elif kind == 'fat32':
        b[82:90] = b'FAT32   '
        b[65] = 1 if dirty else 0
    elif kind == 'fat16':
        b[54:62] = b'FAT16   '
        b[37] = 1 if dirty else 0
    return bytes(b)


def run(image=None, tools=('fsck.fat', 'fsck.exfat'), fsck_rc=0, device=None):
    with tempfile.TemporaryDirectory(prefix='nkos-check-volume-') as temp:
        t = Path(temp)
        bin_dir = t / 'bin'
        bin_dir.mkdir()
        calls = t / 'calls'
        calls.touch()
        for tool in tools:
            (bin_dir / tool).write_text(f'#!/bin/sh\necho "{tool} $*" >> {calls}\nexit "${{FSCK_RC:-0}}"\n')
            (bin_dir / tool).chmod(0o755)
        (bin_dir / 'logger').write_text('#!/bin/sh\nexit 0\n')
        (bin_dir / 'logger').chmod(0o755)
        # The stubs come first; dd, od and tr are the host's.
        script = t / 'check.sh'
        script.write_text(source.replace(PATH_LINE, f'export PATH={bin_dir}:/usr/bin:/bin'))
        if device is None:
            device = t / 'dev'
            device.write_bytes(image)
        env = dict(os.environ, FSCK_RC=str(fsck_rc))
        r = subprocess.run(['sh', str(script), str(device)], text=True, capture_output=True, env=env)
        return r.returncode, calls.read_text().strip(), r.stderr


def check(name, condition, detail=''):
    assert condition, f'{name}: {detail}'
    print('ok', name)


for kind in ('exfat', 'fat32', 'fat16'):
    rc, calls, _ = run(boot_sector(kind, False))
    check(f'{kind} clean: nothing runs', rc == 0 and calls == '', calls)

rc, calls, _ = run(boot_sector('exfat', True))
check('exfat dirty: fsck.exfat -p', rc == 0 and calls.startswith('fsck.exfat -p ') and calls.endswith('/dev'), calls)
for kind in ('fat32', 'fat16'):
    rc, calls, _ = run(boot_sector(kind, True))
    check(f'{kind} dirty: fsck.fat -a -w', rc == 0 and calls.startswith('fsck.fat -a -w '), calls)

rc, calls, _ = run(boot_sector('fat16', True), fsck_rc=1)
check('errors corrected count as checked', rc == 0 and calls, calls)
rc, calls, err = run(boot_sector('exfat', True), fsck_rc=4)
check('errors left: exit 1, still no boot stop', rc == 1 and 'exited with 4' in err, (rc, err))
rc, calls, err = run(boot_sector('fat32', True), tools=('fsck.exfat',))
check('missing fsck.fat: exit 1', rc == 1 and 'missing' in err and calls == '', (rc, err))
rc, calls, _ = run(bytes(512))
check('unknown filesystem: nothing runs', rc == 0 and calls == '', calls)


def flag(image=None, device=None):
    """--dirty: exit 0 when the volume is dirty, 1 when not; never runs fsck."""
    with tempfile.TemporaryDirectory(prefix='nkos-check-volume-flag-') as temp:
        if device is None:
            device = Path(temp) / 'dev'
            device.write_bytes(image)
        return subprocess.run(['sh', '-c', source.split('\n', 1)[1], 'check', '--dirty', str(device)],
                              capture_output=True).returncode


for kind in ('exfat', 'fat32', 'fat16'):
    check(f'--dirty {kind}', flag(boot_sector(kind, True)) == 0 and flag(boot_sector(kind, False)) == 1)
check('--dirty unknown or missing device: not dirty',
      flag(bytes(512)) == 1 and flag(device='/nonexistent/nanokvm-device') == 1)
rc, calls, _ = run(device=Path('/nonexistent/nanokvm-device'))
check('missing device: nothing runs', rc == 0 and calls == '', calls)
rc, calls, _ = run(device='UUID=1234-ABCD')
check('UUID spec: left to mount', rc == 0 and calls == '', calls)


def real(kind, mkfs, fsck, state_offset, dirty_value):
    if not (shutil.which(mkfs) and shutil.which(fsck)):
        print('skip', kind, 'real image:', mkfs, 'or', fsck, 'not installed')
        return
    with tempfile.TemporaryDirectory(prefix='nkos-check-volume-real-') as temp:
        image = Path(temp) / 'image'
        image.write_bytes(b'\0' * (64 << 20))
        args = [mkfs, str(image)] if kind == 'exfat' else [mkfs, '-F', '16', str(image)]
        subprocess.run(args, check=True, capture_output=True)
        with image.open('r+b') as f:
            f.seek(state_offset)
            f.write(bytes([dirty_value]))
        script = Path(temp) / 'check.sh'
        script.write_text(source)
        r = subprocess.run(['sh', str(script), str(image)], text=True, capture_output=True)
        state = image.read_bytes()[state_offset]
        check(f'{kind} real image: checked and clean', r.returncode == 0 and state & dirty_value == 0,
              (r.returncode, r.stderr, state))


real('fat16', 'mkfs.fat', 'fsck.fat', 37, 1)
real('exfat', 'mkfs.exfat', 'fsck.exfat', 106, 2)
