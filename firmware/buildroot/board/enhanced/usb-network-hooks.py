#!/usr/bin/env python3
"""Attach the Enhanced USB network service to both selectable USB templates."""
import sys
from pathlib import Path

root = Path(sys.argv[1])
for name in ('S03usbdev', 'S03usbhid'):
    path = root / name
    text = path.read_text()
    if '# Enhanced USB network lifecycle' in text:
        continue
    replacements = {
        '\nstart_usb_dev(){\n': '\nstart_usb_dev(){\n    # Enhanced USB network lifecycle\n    /etc/init.d/S30rndis stop || return 1\n',
        '\nstop_usb_dev(){\n': '\nstop_usb_dev(){\n    /etc/init.d/S30rndis stop || return 1\n',
        '    ls /sys/class/udc/ | cat > UDC\n': '    ls /sys/class/udc/ | cat > UDC || return 1\n    /etc/init.d/S30rndis start || return 1\n',
        '\nrestart_usb_dev(){\n': '\nrestart_usb_dev(){\n    /etc/init.d/S30rndis stop || return 1\n',
        '    ls /sys/class/udc/ | cat > /sys/kernel/config/usb_gadget/g0/UDC\n': '    ls /sys/class/udc/ | cat > /sys/kernel/config/usb_gadget/g0/UDC || return 1\n    /etc/init.d/S30rndis start || return 1\n',
        '   restart_phy)\n': '   restart_phy)\n    /etc/init.d/S30rndis stop || exit 1\n',
    }
    for old, new in replacements.items():
        if text.count(old) != 1:
            raise SystemExit(f'{path}: expected one anchor {old!r}')
        text = text.replace(old, new)
    path.write_text(text)
