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
    # Both supported template lineages unbind the gadget in this function.
    # The older mainline template calls it stop_usb_dev; the beta-5 application
    # template calls it start_usb_host and switches roles after unbinding (the
    # post-build hook removes the obsolete vendor role write first).  Accept one
    # known shape only and prove it performs the UDC teardown before injecting
    # the network-service stop.
    stop_anchors = ('\nstop_usb_dev(){\n', '\nstart_usb_host(){\n')
    selected = [anchor for anchor in stop_anchors if text.count(anchor) == 1]
    if len(selected) != 1 or sum(text.count(anchor) for anchor in stop_anchors) != 1:
        raise SystemExit(f'{path}: expected exactly one supported gadget-stop function')
    stop_anchor = selected[0]
    body_start = text.index(stop_anchor) + len(stop_anchor)
    body_end = text.find('\n}\n', body_start)
    if body_end < 0 or "echo '' > /sys/kernel/config/usb_gadget/g0/UDC || return 1" not in text[body_start:body_end]:
        raise SystemExit(f'{path}: gadget-stop function does not safely unbind UDC')
    replacements = {
        '\nstart_usb_dev(){\n': '\nstart_usb_dev(){\n    # Enhanced USB network lifecycle\n    /etc/init.d/S30usbnet stop || return 1\n',
        stop_anchor: stop_anchor + '    /etc/init.d/S30usbnet stop || return 1\n',
        '    ls /sys/class/udc/ | cat > UDC\n': '    ls /sys/class/udc/ | cat > UDC || return 1\n    /etc/init.d/S30usbnet start || return 1\n',
        '\nrestart_usb_dev(){\n': '\nrestart_usb_dev(){\n    /etc/init.d/S30usbnet stop || return 1\n',
        '    ls /sys/class/udc/ | cat > /sys/kernel/config/usb_gadget/g0/UDC\n': '    ls /sys/class/udc/ | cat > /sys/kernel/config/usb_gadget/g0/UDC || return 1\n    /etc/init.d/S30usbnet start || return 1\n',
        '   restart_phy)\n': '   restart_phy)\n    /etc/init.d/S30usbnet stop || exit 1\n',
    }
    for old, new in replacements.items():
        if text.count(old) != 1:
            raise SystemExit(f'{path}: expected one anchor {old!r}')
        text = text.replace(old, new)
    path.write_text(text)
