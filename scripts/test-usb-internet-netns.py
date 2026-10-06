#!/usr/bin/env python3
"""Qualify the real USB runtime using only disposable local Linux namespaces.

Run as root after building work/nkos-usb-internet. Requires iproute2, nft,
dnsmasq, ping, ethtool, busybox (udhcpc). Never accesses a NanoKVM or Windows networking.
"""
import json, os, pathlib, shutil, subprocess, tempfile, time
ROOT = pathlib.Path(__file__).resolve().parents[1]
HELPER = ROOT / 'work/nkos-usb-internet'
SCRIPT = ROOT / 'firmware/alpine/compat/S30usbnet'
assert os.geteuid() == 0, 'run as root'
assert HELPER.is_file(), 'build work/nkos-usb-internet first'

def cmd(*args, **kw):
    try:
        return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, **kw)
    except subprocess.CalledProcessError as error:
        print(error.output)
        raise

with tempfile.TemporaryDirectory(prefix='usb-internet-netns-', dir=ROOT / 'work') as tmp:
    path = pathlib.Path(tmp); path.chmod(0o755)
    router, client, upstream = [f'nkos-usb-{os.getpid()}-{s}' for s in ('r', 'c', 'u')]
    namespaces = []
    processes = []
    for folder in ('boot', 'run', 'gadget/configs/c.1'):
        (path / folder).mkdir(parents=True)
    (path / 'boot/rndis.ipv4_prefix').write_text('10.22.33\n')
    (path / 'gadget/UDC').write_text('test-udc\n')
    link = path / 'gadget/configs/c.1/ncm.usb0'; link.symlink_to('/dev/null')
    uid = path / 'uid';uid.write_text('namespace-test-device\n')
    resolv = path / 'resolv.conf'; resolv.write_text('nameserver 1.1.1.1\n'); resolv.chmod(0o644)
    env = dict(os.environ, NANOKVM_USB_NET_BOOT=str(path / 'boot'),
               NANOKVM_USB_NET_RUN=str(path / 'run'), NANOKVM_USB_NET_GADGET=str(path / 'gadget'),
               NANOKVM_USB_INTERNET_HELPER=str(HELPER), NANOKVM_USB_NET_RESOLV='/etc/resolv.conf',
               NANOKVM_USB_NET_PID=str(path / 'udhcpd.pid'), NANOKVM_USB_NET_UID=str(uid))
    def ns(name, *args, **kw): return cmd('ip', 'netns', 'exec', name, *args, **kw)
    def apply():
        ns(router, 'sh', str(SCRIPT), 'start', env=env)
        return json.loads((path / 'run/status.json').read_text())
    def status(): return json.loads((path / 'run/status.json').read_text())
    def nft(): return json.loads(ns(router, 'nft', '-j', 'list', 'ruleset'))
    def internet(expect):
        result = subprocess.run(['ip', 'netns', 'exec', client, 'ping', '-c', '1', '-W', '1', '1.1.1.1'], capture_output=True)
        assert (result.returncode == 0) == expect, result.stdout.decode()
    def dhcp(sharing):
        # The real BusyBox client runs in a disposable namespace and only logs
        # lease options; it does not run a host networking configuration script.
        hook = path / 'dhcp-hook'
        hook.write_text('#!/bin/sh\nif [ "$1" = bound ] || [ "$1" = renew ]; then\n'
                        'printf "%s|%s|%s|%s\\n" "$ip" "$router" "$dns" "$lease" > "' + str(path / 'dhcp-options') + '"\nfi\n')
        hook.chmod(0o755)
        ns(client, 'busybox', 'udhcpc', '-B', '-i', 'host0', '-n', '-q', '-t', '6', '-T', '1', '-s', str(hook))
        ip, gateway, dns, lease = (path / 'dhcp-options').read_text().strip().split('|')
        assert ip.startswith('10.22.33.') and lease == '120', (ip, gateway, dns, lease)
        assert bool(gateway) == sharing and bool(dns) == sharing, (gateway, dns)
    try:
        for name in (router, client, upstream):
            cmd('ip', 'netns', 'add', name); namespaces.append(name)
            ns(name, 'ip', 'link', 'set', 'lo', 'up')
        resolvdir = pathlib.Path('/etc/netns') / router
        resolvdir.mkdir(parents=True, exist_ok=False)
        (resolvdir / 'resolv.conf').write_text('nameserver 1.1.1.1\n')
        for left, right, leftns, rightns in [('usb0', 'host0', router, client), ('eth0', 'lan0', router, upstream), ('wlan0', 'lan1', router, upstream)]:
            # Names exist only inside namespaces; no host uplink is touched.
            cmd('ip', '-n', leftns, 'link', 'add', left, 'type', 'veth', 'peer', 'name', right, 'netns', rightns)
            ns(leftns, 'ip', 'link', 'set', left, 'up'); ns(rightns, 'ip', 'link', 'set', right, 'up')
        ns(router, 'ip', 'addr', 'add', '192.0.2.2/24', 'dev', 'eth0')
        ns(upstream, 'ip', 'addr', 'add', '192.0.2.1/24', 'dev', 'lan0')
        ns(router, 'ip', 'addr', 'add', '198.51.100.2/24', 'dev', 'wlan0')
        ns(upstream, 'ip', 'addr', 'add', '198.51.100.1/24', 'dev', 'lan1')
        ns(upstream, 'ip', 'addr', 'add', '1.1.1.1/32', 'dev', 'lo')
        server = subprocess.Popen(['ip', 'netns', 'exec', upstream, 'dnsmasq', '--no-daemon', '--conf-file=', '--no-resolv', '--bind-interfaces', '--listen-address=1.1.1.1', '--address=/usb-internet.test/1.1.1.1'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        processes.append(server);time.sleep(.15)
        ns(router, 'ip', 'addr', 'add', '169.254.44.1/24', 'dev', 'usb0')
        ns(router, 'ethtool', '-K', 'usb0', 'tx', 'off')
        ns(client, 'ethtool', '-K', 'host0', 'tx', 'off')
        state = apply(); assert state['state'] == 'disabled', state
        dhcp(False)
        ns(client, 'ip', 'addr', 'replace', '10.22.33.100/24', 'dev', 'host0')
        ns(client, 'ip', 'route', 'replace', 'default', 'via', '10.22.33.1')
        ns(client, 'ping', '-c', '1', '-W', '1', '10.22.33.1')
        flag = path / 'boot/usb.internet';flag.touch()
        state = apply();assert state['state'] == 'waiting-uplink', state
        internet(False);dhcp(False)
        ns(router, 'ip', 'route', 'add', 'default', 'via', '192.0.2.1', 'dev', 'eth0')
        state = apply();assert state['state'] == 'active' and state['uplink'] == 'eth0', state
        internet(True);dhcp(True)
        # Actual DNS query through the USB gateway and uplink resolver.
        query = "import socket,struct; q=b'\\x12\\x34\\x01\\x00\\x00\\x01\\x00\\x00\\x00\\x00\\x00\\x00'+b'\\x0cusb-internet\\x04test\\x00\\x00\\x01\\x00\\x01'; s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.settimeout(2); s.sendto(q,('10.22.33.1',53)); r=s.recv(4096); assert r[-4:]==socket.inet_aton('1.1.1.1'),r"
        ns(client, 'python3', '-c', query)
        print('Active state:', state)
        first = (path / 'run/dnsmasq.pid').read_text();rules = nft()
        apply();apply()
        assert (path / 'run/dnsmasq.pid').read_text() == first, 'reapply restarted DHCP'
        def without_counters(value):
            if isinstance(value, dict): return {k: without_counters(v) for k, v in value.items() if k not in ('packets', 'bytes')}
            if isinstance(value, list): return [without_counters(v) for v in value]
            return value
        assert without_counters(rules) == without_counters(nft()), 'reapply changed rules'
        # Restart helper lifecycle, retaining boot preference.
        ns(router, 'sh', str(SCRIPT), 'stop', env=env)
        assert not any(x.get('table', {}).get('name') == 'nkos_usb_internet' for x in nft()['nftables'])
        assert '169.254.44.1' in ns(router, 'ip', '-j', 'addr', 'show', 'dev', 'usb0'), 'manual USB address lost'
        state = apply();assert state['enabled'] and state['state'] == 'active', state
        internet(True)
        ns(router, 'sh', str(ROOT / 'kvmapp/system/init.d/S34mssclamp'), 'start')
        state = apply();assert state.get('offloadReason') != 'network-policy', state
        # A foreign VPN policy must survive and suppress software offload.
        ns(router, 'nft', 'add', 'table', 'inet', 'foreign_vpn')
        ns(router, 'nft', 'add', 'chain', 'inet', 'foreign_vpn', 'forward', '{ type filter hook forward priority 0; policy accept; }')
        state = apply();assert not state['flowOffload'] and state['offloadReason'] == 'network-policy', state
        foreign = ns(router, 'nft', '-j', 'list', 'table', 'inet', 'foreign_vpn')
        internet(True)
        flag.unlink();state = apply();assert state['state'] == 'disabled', state
        internet(False);dhcp(False)
        assert foreign == ns(router, 'nft', '-j', 'list', 'table', 'inet', 'foreign_vpn')
        assert 'masquerade' not in ns(router, 'nft', 'list', 'table', 'inet', 'nkos_usb_internet')
        assert 'flowtable' not in ns(router, 'nft', 'list', 'table', 'inet', 'nkos_usb_internet')
        ns(client, 'ping', '-c', '1', '-W', '1', '10.22.33.1')
        flag.touch();apply()
        ns(router, 'ip', 'route', 'replace', 'default', 'via', '198.51.100.1', 'dev', 'wlan0')
        state = apply();assert state['uplink'] == 'wlan0', state
        internet(True)
        ns(router, 'ip', 'link', 'set', 'wlan0', 'down')
        state = apply();assert state['state'] == 'waiting-uplink', state
        internet(False)
        # Restore Ethernet before exercising VPN route selection.
        ns(router, 'ip', 'route', 'replace', 'default', 'via', '192.0.2.1', 'dev', 'eth0')
        ns(router, 'ip', 'link', 'add', 'vpn0', 'type', 'dummy');ns(router, 'ip', 'link', 'set', 'vpn0', 'up')
        ns(router, 'ip', 'route', 'add', '1.1.1.1/32', 'dev', 'vpn0')
        state = apply();assert state['state'] == 'waiting-uplink', state
        internet(False)
        ns(router, 'ip', 'route', 'del', '1.1.1.1/32')
        # Explicit subnet collision pauses sharing, keeps local access.
        ns(router, 'ip', 'route', 'add', '10.22.33.0/24', 'dev', 'eth0', 'table', '100')
        state = apply();assert state['state'] == 'subnet-conflict', state
        internet(False)
        ns(client, 'ping', '-c', '1', '-W', '1', '10.22.33.1')
        (path / 'boot/rndis.ipv4_prefix').unlink()
        state = apply();assert state['address'] != '10.22.33.1/24' and state['state'] == 'active', state
        # Removing NCM stops its daemon/NAT, while preserving the preference.
        link.unlink();state = apply();assert state['state'] == 'waiting-ncm' and state['enabled'], state
        assert not (path / 'run/dnsmasq.pid').exists()
        assert foreign == ns(router, 'nft', '-j', 'list', 'table', 'inet', 'foreign_vpn')
        # Exercise the real OpenRC command's event-driven watcher, without
        # manual apply after a route change. It also checks no feedback loop.
        link.symlink_to('/dev/null')
        watchenv = dict(env, NANOKVM_USB_NET_SCRIPT=str(SCRIPT))
        watcher = subprocess.Popen(['ip', 'netns', 'exec', router, str(HELPER), 'watch'], env=watchenv, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        processes.append(watcher)
        def wait_state(expected, timeout=3):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                if status()['state'] == expected: return
                time.sleep(.05)
            raise AssertionError(status())
        wait_state('active')
        ns(router, 'ip', 'route', 'del', 'default')
        wait_state('waiting-uplink')
        ns(router, 'ip', 'route', 'add', 'default', 'via', '192.0.2.1', 'dev', 'eth0')
        wait_state('active')
        watcher.terminate();watcher.wait(timeout=3);processes.remove(watcher)
        print('PASS: DHCP local/active, DNS, NAT, no uplink, idempotence, restart/persistence, disable/local access, foreign rules, uplink change/loss, VPN route, subnet conflict/selection, gadget removal, event-driven watcher')
    finally:
        for process in processes:
            process.terminate(); process.wait(timeout=3)
        for name in reversed(namespaces):
            # Terminate only processes in these uniquely named test namespaces.
            for pid in cmd('ip', 'netns', 'pids', name).split():
                subprocess.run(['kill', '-TERM', pid], check=False)
            cmd('ip', 'netns', 'del', name)
        shutil.rmtree(pathlib.Path('/etc/netns') / router, ignore_errors=True)
