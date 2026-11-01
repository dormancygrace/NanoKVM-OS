#!/usr/bin/env python3
"""Ethernet and Wi-Fi on one subnet: the Wi-Fi address answers, also when
Ethernet loses its link, and Ethernet takes over again with the link.

Without arguments the shipped settings and their packaging are checked.
--netns (root) runs the device in network namespaces: an "eth" and a "wlan"
veth on one subnet behind a bridge with the gateway. The Ethernet link goes
away by its peer going down, which drops the carrier as a cable would.
Alpine's own settings are the negative control.
"""
import argparse
from pathlib import Path
import re
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[1]
CONF = ROOT / 'firmware/alpine/etc/sysctl.d/40-nanokvm-multihome.conf'
# Alpine's values and the kernel's defaults, set explicitly: a new namespace
# inherits its all/default settings from the host's.
ALPINE = {
    'net.ipv4.conf.all.rp_filter': '1', 'net.ipv4.conf.default.rp_filter': '1',
    'net.ipv4.conf.all.ignore_routes_with_linkdown': '0', 'net.ipv4.conf.default.ignore_routes_with_linkdown': '0',
    'net.ipv6.conf.all.ignore_routes_with_linkdown': '0', 'net.ipv6.conf.default.ignore_routes_with_linkdown': '0',
    'net.ipv4.conf.all.arp_ignore': '0', 'net.ipv4.conf.all.arp_announce': '0',
}


def shipped():
    return dict(re.findall(r'^([\w.]+) = (\S+)$', CONF.read_text(), re.M))


class SettingsTests(unittest.TestCase):
    def test_settings(self):
        self.assertEqual(shipped(), {
            'net.ipv4.conf.all.ignore_routes_with_linkdown': '1',
            'net.ipv4.conf.default.ignore_routes_with_linkdown': '1',
            'net.ipv6.conf.all.ignore_routes_with_linkdown': '1',
            'net.ipv6.conf.default.ignore_routes_with_linkdown': '1',
            'net.ipv4.conf.all.rp_filter': '2',
            'net.ipv4.conf.default.rp_filter': '2',
            'net.ipv4.conf.all.arp_ignore': '1',
            'net.ipv4.conf.all.arp_announce': '2',
        })

    def test_packaged_after_alpine(self):
        # OpenRC applies /usr/lib/sysctl.d (Alpine: rp_filter = 1) before /etc/sysctl.d.
        packages = (ROOT / 'platform/packages.list').read_text()
        self.assertRegex(packages, r'(?m)^base +file +/etc/sysctl\.d/40-nanokvm-multihome\.conf +0644 +'
                                   r'firmware/alpine/etc/sysctl\.d/40-nanokvm-multihome\.conf$')


def sh(*args, check=True):
    return subprocess.run(args, check=check, capture_output=True, text=True)


def answers(lan, address):
    return sh('ip', 'netns', 'exec', lan, 'ping', '-c', '2', '-W', '1', '-i', '0.3', address, check=False).returncode == 0


def run_netns(settings):
    kvm, lan = 'mh-kvm', 'mh-lan'
    for ns in (kvm, lan):
        sh('ip', 'netns', 'del', ns, check=False)
        sh('ip', 'netns', 'add', ns)
    try:
        def k(*args):
            return sh('ip', 'netns', 'exec', kvm, *args)

        def n(*args):
            return sh('ip', 'netns', 'exec', lan, *args)
        # The device's settings come first, as at boot, then its interfaces.
        for key, value in settings.items():
            # IPv6 may be off on the host; the test is about IPv4.
            sh('ip', 'netns', 'exec', kvm, 'sysctl', '-q', '-w', f'{key}={value}', check=not key.startswith('net.ipv6.'))
        n('ip', 'link', 'add', 'br0', 'type', 'bridge')
        n('ip', 'addr', 'add', '10.9.0.1/24', 'dev', 'br0')
        n('ip', 'link', 'set', 'br0', 'up')
        n('ip', 'link', 'set', 'lo', 'up')
        for dev, addr, metric in (('eth0', '10.9.0.11', '10'), ('wlan0', '10.9.0.12', '200')):
            k('ip', 'link', 'add', dev, 'type', 'veth', 'peer', 'name', f'{dev}p', 'netns', lan)
            n('ip', 'link', 'set', f'{dev}p', 'master', 'br0', 'up')
            k('ip', 'addr', 'add', f'{addr}/24', 'dev', dev)
            k('ip', 'link', 'set', dev, 'up')
            k('ip', 'route', 'add', 'default', 'via', '10.9.0.1', 'dev', dev, 'metric', metric)
        k('ip', 'link', 'set', 'lo', 'up')
        # The gateway reaches each address at its own interface, as a router
        # that learned them does (Linux would also answer ARP for the Wi-Fi
        # address on Ethernet, and the frames would come in there).
        for dev, addr in (('eth0', '10.9.0.11'), ('wlan0', '10.9.0.12')):
            mac = k('cat', f'/sys/class/net/{dev}/address').stdout.strip()
            n('ip', 'neigh', 'replace', addr, 'lladdr', mac, 'dev', 'br0', 'nud', 'permanent')
        results = {'both up: wlan address': answers(lan, '10.9.0.12'),
                   'both up: eth address': answers(lan, '10.9.0.11')}
        n('ip', 'link', 'set', 'eth0p', 'down')
        carrier = k('cat', '/sys/class/net/eth0/carrier').stdout.strip()
        assert carrier == '0', f'eth0 carrier {carrier} with its peer down'
        results['eth link down: wlan address'] = answers(lan, '10.9.0.12')
        n('ip', 'link', 'set', 'eth0p', 'up')
        results['eth link back: eth address'] = answers(lan, '10.9.0.11')
        route = k('ip', 'route', 'get', '10.9.0.1').stdout
        results['eth link back: route via eth0'] = ' dev eth0 ' in route
        return results
    finally:
        for ns in (kvm, lan):
            sh('ip', 'netns', 'del', ns, check=False)


def netns_main():
    nanokvm = run_netns({**ALPINE, **shipped()})
    alpine = run_netns(ALPINE)
    failed = [name for name, ok in nanokvm.items() if not ok]
    for name, ok in nanokvm.items():
        print(f'{"PASS" if ok else "FAIL"} {name}')
    # Negative control: without the settings the Wi-Fi address does not
    # answer, neither beside Ethernet nor without its link.
    for name in ('both up: wlan address', 'eth link down: wlan address'):
        print(f'control (Alpine) {name}: {"answers" if alpine[name] else "silent"}')
        if alpine[name]:
            failed.append(f'control: {name}')
    return 1 if failed else 0


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--netns', action='store_true', help='run the namespace test (root)')
    args, rest = parser.parse_known_args()
    if args.netns:
        sys.exit(netns_main())
    unittest.main(argv=[sys.argv[0], *rest])
