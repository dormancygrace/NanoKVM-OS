#!/usr/bin/env python3
"""Exercise the production service with real IPv4/IPv6 packets in isolated netns.

Run as root on a Linux host with iproute2 and nft. Never changes host interfaces,
routes, forwarding or nft rules. Temporary namespaces are removed on exit.
"""
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import sys
import uuid


def checksum(data):
    data += b'\0' * (len(data) % 2)
    total = sum(struct.unpack('!%dH' % (len(data) // 2), data))
    while total >> 16:
        total = (total & 65535) + (total >> 16)
    return (~total) & 65535


def pseudo(version, source, dest, proto, size):
    family = socket.AF_INET if version == 4 else socket.AF_INET6
    pair = socket.inet_pton(family, source) + socket.inet_pton(family, dest)
    return pair + (struct.pack('!BBH', 0, proto, size) if version == 4
                   else struct.pack('!I3xB', size, proto))


def send(version, source, dest, flags, mss, proto):
    if proto == 6:
        segment = struct.pack('!HHIIBBHHHBBH', 45678, 45679, 100, 0,
                              6 << 4, flags, 4096, 0, 0, 2, 4, mss)
        offset = 16
    else:
        segment = struct.pack('!HHHH', 45678, 45679, 8, 0)
        offset = 6
    value = checksum(pseudo(version, source, dest, proto, len(segment)) + segment)
    segment = segment[:offset] + struct.pack('!H', value or 65535) + segment[offset+2:]
    family = socket.AF_INET if version == 4 else socket.AF_INET6
    s = socket.socket(family, socket.SOCK_RAW, socket.IPPROTO_RAW)
    if version == 4:
        header = struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20+len(segment), 101,
                             0, 64, proto, 0, socket.inet_aton(source), socket.inet_aton(dest))
    else:
        header = struct.pack('!IHBB16s16s', 6 << 28, len(segment), proto, 64,
                             socket.inet_pton(family, source), socket.inet_pton(family, dest))
    s.sendto(header + segment, (dest, 0))
    s.close()


def capture(interface):
    s = socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.htons(3))
    s.bind((interface, 0))
    s.settimeout(5)
    print('READY', flush=True)
    while True:
        data = s.recv(65535)
        ether = struct.unpack('!H', data[12:14])[0]
        if ether == 0x0800:
            version, proto, offset = 4, data[23], 14 + (data[14] & 15) * 4
            size = struct.unpack('!H', data[16:18])[0] - (offset-14)
            source, dest = socket.inet_ntoa(data[26:30]), socket.inet_ntoa(data[30:34])
        elif ether == 0x86dd:
            version, proto, offset = 6, data[20], 54
            size = struct.unpack('!H', data[18:20])[0]
            source = socket.inet_ntop(socket.AF_INET6, data[22:38])
            dest = socket.inet_ntop(socket.AF_INET6, data[38:54])
        else:
            continue
        segment = data[offset:offset+size]
        if proto not in (6, 17) or len(segment) < 8 or segment[:4] != struct.pack('!HH', 45678, 45679):
            continue
        result = {'version': version, 'proto': proto,
                  'checksum': checksum(pseudo(version, source, dest, proto, size)+segment)}
        if proto == 6:
            result.update(mss=struct.unpack('!H', segment[22:24])[0], flags=segment[13])
        print(json.dumps(result), flush=True)
        return


def run():
    if os.geteuid() != 0:
        raise SystemExit('Root required for isolated network namespaces')
    root = Path(__file__).resolve().parents[1]
    service = root / 'kvmapp/system/init.d/S34mssclamp'
    prefix = 'mss-' + uuid.uuid4().hex[:8]
    src, gw, dst = [prefix+x for x in ('s', 'g', 'd')]
    created = []

    def command(*args, **kwargs):
        return subprocess.check_output(args, text=True, **kwargs)

    def ns(name, *args, **kwargs):
        return command('ip', 'netns', 'exec', name, *map(str, args), **kwargs)

    def packet(version, flags, mss, expected, local=False, reverse=False, proto=6):
        sender = gw if local else dst if reverse else src
        receiver = src if reverse else dst
        source = ('198.18.1.1' if local else '198.18.1.2' if reverse else '198.18.0.2') if version == 4 else (
            '2001:db8:1::1' if local else '2001:db8:1::2' if reverse else '2001:db8::2')
        dest = ('198.18.0.2' if reverse else '198.18.1.2') if version == 4 else (
            '2001:db8::2' if reverse else '2001:db8:1::2')
        proc = subprocess.Popen(['ip', 'netns', 'exec', receiver, sys.executable,
                                 __file__, 'capture', 'e'], stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, text=True)
        try:
            assert proc.stdout.readline().strip() == 'READY'
            ns(sender, sys.executable, __file__, 'send', version, source, dest, flags, mss, proto)
            stdout, stderr = proc.communicate(timeout=7)
            assert proc.returncode == 0, stderr
            observed = json.loads(stdout)
            assert observed['checksum'] == 0, observed
            if proto == 6:
                assert observed['mss'] == expected and observed['flags'] == flags, observed
            print('PASS', dict(version=version, flags=flags, original=mss,
                               expected=expected, local=local, reverse=reverse, proto=proto))
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.wait()

    try:
        for name in (src, gw, dst):
            command('ip', 'netns', 'add', name)
            created.append(name)
            ns(name, 'ip', 'link', 'set', 'lo', 'up')
        for name, side, block in ((src, 'l', 0), (dst, 'r', 1)):
            ns(gw, 'ip', 'link', 'add', 'name', side, 'type', 'veth', 'peer', 'name', 'e', 'netns', name)
            for owner, iface, host in ((gw, side, 1), (name, 'e', 2)):
                ns(owner, 'ip', 'addr', 'add', f'198.18.{block}.{host}/24', 'dev', iface)
                ns(owner, 'ip', '-6', 'addr', 'add', f'2001:db8:{block}::{host}/64', 'dev', iface, 'nodad')
                ns(owner, 'ip', 'link', 'set', 'dev', iface, 'up')
            ns(name, 'ip', 'route', 'add', 'default', 'via', f'198.18.{block}.1')
            ns(name, 'ip', '-6', 'route', 'add', 'default', 'via', f'2001:db8:{block}::1')
        ns(gw, 'sysctl', '-qw', 'net.ipv4.ip_forward=1', 'net.ipv6.conf.all.forwarding=1')
        ns(gw, 'nft', '-f', '-', input='add table inet untouched\nadd chain inet untouched sentinel\n')
        for action in ('start', 'start', 'reload'):
            ns(gw, 'sh', service, action)
        assert ns(gw, 'nft', 'list', 'table', 'inet', 'nkos_mss').count('hook postrouting') == 1
        for version, baseline in ((4, 1460), (6, 1440)):
            packet(version, 2, baseline, baseline)  # ordinary 1500-byte path
        ns(gw, 'ip', 'link', 'set', 'dev', 'r', 'mtu', '1420')
        ns(dst, 'ip', 'link', 'set', 'dev', 'e', 'mtu', '1420')
        for version, baseline, capped in ((4, 1460, 1380), (6, 1440, 1360)):
            for flags in (2, 18):  # SYN and SYN-ACK, each direction
                packet(version, flags, baseline, capped)
                packet(version, flags, baseline, capped, reverse=True)
            packet(version, 2, 1200, 1200)  # never increase a smaller MSS
            packet(version, 16, baseline, baseline)  # non-SYN is untouched
            packet(version, 2, baseline, capped, local=True)
            packet(version, 0, 0, None, proto=17)  # UDP/checksum untouched
        ns(gw, 'ip', 'route', 'replace', '198.18.1.0/24', 'dev', 'r', 'mtu', '1300')
        packet(4, 2, 1460, 1260)  # route PMTU, not an interface-name/MTU constant
        for action in ('stop', 'stop'):
            ns(gw, 'sh', service, action)
        ns(gw, 'nft', 'list', 'table', 'inet', 'untouched')
        packet(4, 2, 1460, 1460)  # prove the policy caused the observed reduction
        print('PASS: lifecycle, unrelated table preservation, IPv4/IPv6 dataplane')
    finally:
        for name in reversed(created):
            subprocess.run(['ip', 'netns', 'delete', name], check=True)


if __name__ == '__main__':
    if len(sys.argv) == 1:
        run()
    elif sys.argv[1] == 'capture':
        capture(sys.argv[2])
    else:
        send(int(sys.argv[2]), sys.argv[3], sys.argv[4], *map(int, sys.argv[5:]))
