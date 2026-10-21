#!/usr/bin/env python3
"""Run production startup gates against doubles: never start on migration error."""
from pathlib import Path
import os
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
s95 = (root / 'kvmapp/system/init.d/S95nanokvm').read_text()
usb = (root / 'firmware/alpine/openrc/nanokvm-usb').read_text()
shells = [['sh']]
if subprocess.run(['sh', '-c', 'command -v busybox'], capture_output=True).returncode == 0:
    shells.append(['busybox', 'sh'])
with tempfile.TemporaryDirectory(prefix='usb-layout-start-') as directory:
    tmp = Path(directory)
    for shell in shells:
        for status in (0, 1):
            log = tmp / 'log'
            definitions = s95.split('case "$1" in\n', 1)[0]
            definitions = definitions.replace('/usr/libexec/nanokvm/usb-flags-migrate', 'migrate')
            definitions = definitions.replace('/run/nanokvm', str(tmp / 'run'))
            definitions += f"""
migrate() {{ echo migrate >> {log}; return {status}; }}
pidof() {{ return 1; }}
prepare_hdmi_receiver() {{ return 0; }}
remove_legacy_links() {{ :; }}
start_background() {{ echo start >> {log}; }}
start_services
"""
            log.unlink(missing_ok=True)
            proc = subprocess.run(shell, input=definitions, text=True, capture_output=True)
            assert proc.returncode == status, proc.stderr
            assert log.read_text().splitlines() == (['migrate'] if status else ['migrate', 'start', 'start'])

            fixture = usb.replace('/usr/libexec/nanokvm/usb-flags-migrate', 'migrate')
            fixture = fixture.replace('/usr/libexec/nanokvm/legacy/S03usbdev', 'gadget')
            fixture = fixture.replace('/usr/libexec/nanokvm/legacy/S30usbnet', 'usbnet')
            fixture += f"""
ebegin() {{ :; }}
eend() {{ return "$1"; }}
migrate() {{ echo migrate >> {log}; return {status}; }}
gadget() {{ echo gadget >> {log}; }}
usbnet() {{ echo usbnet >> {log}; }}
start
"""
            log.unlink(missing_ok=True)
            proc = subprocess.run(shell, input=fixture, text=True, capture_output=True)
            assert proc.returncode == status, proc.stderr
            assert log.read_text().splitlines() == (['migrate'] if status else ['migrate', 'gadget', 'usbnet'])
        # Restart must propagate a failed start, not print OK after sync.
        definitions, dispatch = s95.split('case "$1" in\n', 1)
        fixture = definitions + "\nstop_services() { :; }\nstart_services() { return 1; }\nset -- restart\ncase \"$1\" in\n" + dispatch
        proc = subprocess.run(shell, input=fixture, text=True, capture_output=True)
        assert proc.returncode != 0 and 'OK' not in proc.stdout
        print('PASS: app, gadget and restart migration gates:', shell)

    # Execute the actual shell layout selectors for all transaction states.
    app, flags = tmp / 'app-layout', tmp / 'flags'
    flags.mkdir()
    active = flags / '.layout-v1'
    for has_app in (False, True):
        for is_active in (False, True):
            for p, present in ((app, has_app), (active, is_active)):
                p.unlink(missing_ok=True)
                if present: p.touch()
            for name in ('S03usbdev', 'S03usbhid'):
                src = (root / 'kvmapp/system/init.d' / name).read_text()
                # Only the path-selection preamble, before gadget functions.
                end = src.index('[ -e "$conf/.layout-v1" ] || conf=/boot')
                preamble = src[:end] + src[end:].split('\n', 1)[0]
                preamble = preamble.replace('/etc/kvm/usb', str(flags))
                got = subprocess.check_output(['sh'], input=preamble + '\nprintf %s "$conf"\n', text=True)
                assert got == (str(flags) if is_active else '/boot')
            src = (root / 'firmware/alpine/compat/S30usbnet').read_text()
            begin = src.index('if [ -f /usr/share/nanokvm/usb-flags-layout ]')
            end = src.index('\nfi', begin) + 3
            selector = src[begin:end].replace('/usr/share/nanokvm/usb-flags-layout', str(app)).replace('/etc/kvm/usb', str(flags))
            env = dict(os.environ)
            env.pop('NANOKVM_USB_NET_BOOT', None)
            got = subprocess.check_output(['sh'], input=selector + '\nprintf %s "$NANOKVM_USB_NET_BOOT"\n', text=True, env=env)
            assert got == (str(flags) if has_app and is_active else '/boot')
    print('PASS: shell consumers retain legacy paths until activation')
