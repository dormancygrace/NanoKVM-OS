#!/usr/bin/env python3
"""Exercise production USB class selection and live rebinds without a device."""
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[1]
FLAGS = ('usb.disk0', 'usb.ncm', 'usb.rndis0', 'usb.acm', 'usb.audio')
PER_INTERFACE = (0, 0, 0)
IAD = (0xef, 2, 1)
SHELLS = [['sh']]
if shutil.which('busybox'):
    SHELLS.append(['busybox', 'sh'])


def fixture(name, directory):
    source = (ROOT / 'kvmapp/system/init.d' / name).read_text()
    source = source.split('case "$1" in\n', 1)[0]
    paths = {
        '/etc/kvm/usb': directory / 'flags',
        'conf=/boot': 'conf=' + str(directory / 'boot'),
        '/sys/kernel/config/usb_gadget': directory / 'configfs',
        '/sys/class/cvi-base/base_uid': directory / 'uid',
        '/sys/class/udc': directory / 'udc',
        '/proc/cviusb/otg_role': directory / 'absent-role',
        '/etc/init.d/S29qdisc': directory / 'absent-qdisc',
        '/kvmapp/system/share/serial-console/nanokvm-serial-console.img': directory / 'absent-helper',
    }
    for old, new in paths.items():
        source = source.replace(old, str(new))
    source = source.replace('. /etc/profile', ':')
    gadget = shlex.quote(str(directory / 'configfs/g0'))
    # configfs creates attributes and default LUNs with each object, and
    # removes a function's attributes when rmdir releases it. Model only that
    # filesystem behaviour; descriptor writes and link selection stay real.
    return source + f'''
mkdir() {{
    command mkdir "$@" || return
    if [ -d {gadget} ]; then
        command mkdir -p {gadget}/os_desc
        [ -e {gadget}/UDC ] || : > {gadget}/UDC
    fi
    if [ -d {gadget}/functions/ncm.usb0 ]; then
        command mkdir -p {gadget}/functions/ncm.usb0/os_desc/interface.ncm
    fi
    if [ -d {gadget}/functions/mass_storage.disk0 ]; then
        command mkdir -p {gadget}/functions/mass_storage.disk0/lun.0
        [ -e {gadget}/functions/mass_storage.disk0/lun.0/cdrom ] ||
            echo 0 > {gadget}/functions/mass_storage.disk0/lun.0/cdrom
        [ -e {gadget}/functions/mass_storage.disk0/lun.0/file ] ||
            : > {gadget}/functions/mass_storage.disk0/lun.0/file
    fi
    return 0
}}
rmdir() {{
    case "$1" in
        {gadget}/functions/ncm.usb0|{gadget}/functions/rndis.usb0)
            command rm -r -- "$1" ;;
        *) command rmdir "$@" ;;
    esac
}}
start_usb_dev
'''


with tempfile.TemporaryDirectory(prefix='usb-device-class-') as temporary:
    for shell in SHELLS:
        for layout in ('active', 'legacy'):
            with tempfile.TemporaryDirectory(dir=temporary) as name:
                directory = Path(name)
                for folder in ('flags', 'boot', 'configfs', 'udc'):
                    (directory / folder).mkdir()
                flags = directory / ('flags' if layout == 'active' else 'boot')
                if layout == 'active':
                    (flags / '.layout-v1').touch()
                # One boot keyboard, leaving room for optional functions.
                for flag in ('usb.disable_relative', 'usb.disable_absolute'):
                    (flags / flag).touch()
                (directory / 'uid').write_text('UID: 1234567890abcdef\n')
                (directory / 'udc/controller').touch()
                gadget = directory / 'configfs/g0'

                def start(profile, enabled, expected):
                    for flag in FLAGS:
                        (flags / flag).unlink(missing_ok=True)
                        if flag in enabled:
                            (flags / flag).touch()
                    result = subprocess.run(shell, input=fixture(profile, directory),
                                            text=True, capture_output=True, cwd=directory)
                    assert result.returncode == 0 and not result.stderr, result.stderr
                    actual = tuple(int((gadget / field).read_text().strip(), 0) for field in
                                   ('bDeviceClass', 'bDeviceSubClass', 'bDeviceProtocol'))
                    assert actual == expected, (profile, shell, layout, enabled, actual, expected)
                    assert (gadget / 'UDC').read_text().strip() == 'controller'
                    linked = {path.name for path in (gadget / 'configs/c.1').iterdir() if path.is_symlink()}
                    wanted = {'hid.GS0'}
                    if 'usb.acm' in enabled:
                        wanted.add('acm.GS0')
                    network = profile == 'S03usbdev' and bool({'usb.ncm', 'usb.rndis0'} & set(enabled))
                    if profile == 'S03usbdev':
                        if 'usb.disk0' in enabled:
                            wanted.add('mass_storage.disk0')
                        if 'usb.audio' in enabled:
                            wanted.add('uac1.audio0')
                        if network:
                            wanted.add('ncm.usb0')
                    assert linked == wanted, (profile, enabled, linked, wanted)
                    assert int((gadget / 'os_desc/use').read_text()) == int(network)
                    assert (gadget / 'os_desc/c.1').is_symlink() == network

                # A cold start and repeated on -> off -> on changes reuse g0,
                # just like live composition changes without a device reboot.
                start('S03usbdev', (), PER_INTERFACE)
                start('S03usbdev', ('usb.disk0',), PER_INTERFACE)
                for optional in ('usb.ncm', 'usb.rndis0', 'usb.acm', 'usb.audio'):
                    for _ in range(2):
                        start('S03usbdev', ('usb.disk0', optional), IAD)
                        start('S03usbdev', ('usb.disk0',), PER_INTERFACE)
                # Removing one IAD function must not clear the class while
                # another remains. HID-only ignores full-profile flags.
                start('S03usbdev', ('usb.ncm', 'usb.acm', 'usb.audio'), IAD)
                start('S03usbdev', ('usb.acm', 'usb.audio'), IAD)
                start('S03usbdev', ('usb.audio',), IAD)
                start('S03usbhid', ('usb.audio', 'usb.ncm', 'usb.disk0'), PER_INTERFACE)
                start('S03usbhid', ('usb.acm',), IAD)
                start('S03usbhid', (), PER_INTERFACE)
                start('S03usbdev', ('usb.disk0',), PER_INTERFACE)
                print('PASS: USB class, optional functions and live profile transitions:', shell, layout)
