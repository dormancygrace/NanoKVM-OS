#!/usr/bin/env python3
"""Isolated DHCP startup/options and RFC3442 hook regression checks."""
import os
from pathlib import Path
import subprocess
import tempfile

root=Path(__file__).resolve().parents[1]
for name in ('S30eth','S30wifi'):
    lines=[line for line in (root/'kvmapp/system/init.d'/name).read_text().splitlines()
           if ('udhcpc ' in line or '"$NANOKVM_UDHCPC" ' in line) and not line.lstrip().startswith('#')]
    assert lines,name
    for line in lines:
        assert '-B' in line and '-O 121' in line, line

with tempfile.TemporaryDirectory(prefix='nkos-dhcp-test-') as temp:
    temp=Path(temp); binary=temp/'bin'; binary.mkdir()
    log=temp/'calls'; resolver=temp/'resolv'; resolver.touch()
    for command in ('ifconfig','route','ip'):
        p=binary/command
        p.write_text('#!/bin/sh\nprintf "%s %s\\n" "'+command+'" "$*" >> "$TEST_LOG"\n'
                     + ('[ "$1" = -n ] && printf "Destination Gateway Genmask Flags Metric Ref Use Iface\\n"\n[ "$1" = del ] && exit 1\n' if command=='route' else '')+'exit 0\n')
        p.chmod(0o755)
    script=(root/'firmware/buildroot/board/overlay/usr/share/udhcpc/default.script').read_text()
    script=script.replace('RESOLV_CONF="/etc/resolv.conf"', 'RESOLV_CONF="'+str(resolver)+'"')
    script=script.replace('[ -e /boot/resolv.conf ] || command -v resolvconf >/dev/null 2>&1','false')
    script=script.replace('/sbin/ifconfig',str(binary/'ifconfig'))
    script=script.replace('/usr/sbin/avahi-autoipd',str(temp/'absent-avahi'))
    hook=temp/'hook';hook.write_text(script)
    env=dict(os.environ,PATH=str(binary)+':/usr/bin:/bin',TEST_LOG=str(log),interface='test0',ip='192.0.2.20',subnet='255.255.255.0',router='192.0.2.1',dns='',domain='',search='',ipv6='')
    for routes,expected in [('198.51.100.0/24 192.0.2.2 0.0.0.0/0 192.0.2.3', ['ip -4 route add 198.51.100.0/24 via 192.0.2.2 dev test0 proto dhcp metric 100','ip -4 route add 0.0.0.0/0 via 192.0.2.3 dev test0 proto dhcp metric 100']),('', ['ip -4 route add default via 192.0.2.1 dev test0 proto dhcp metric 100'])]:
        log.write_text('')
        subprocess.run(['sh',str(hook),'bound'],env=dict(env,staticroutes=routes),check=True,capture_output=True)
        calls=log.read_text()
        for text in expected: assert text in calls,calls
        if routes: assert 'route add default via 192.0.2.1' not in calls,calls
print('DHCP: startup options and classless-route precedence passed')
