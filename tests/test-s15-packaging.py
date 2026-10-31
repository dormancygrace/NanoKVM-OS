#!/usr/bin/env python3
"""Init-script layout of the base and app packages in platform/packages.list."""
from pathlib import Path


root = Path(__file__).resolve().parents[1]
removed_init_scripts = (
    'S00kmod S01fs S02config S10uuid S12temperature S13cpufreq '
    'S13nanokvm-watchdog S25wifimod S29qdisc S30eth S30usbnet S30wifi '
    'S34mssclamp S35flowoffload S38memory S49persistent-cron S50ssdpd '
    'S50sshd S80dnsmasq S94hwcrypto S98tailscaled S50avahi-daemon'
).split()
kept_init_scripts = {
    'S03usbdev', 'S03usbhid', 'S15kvmhwd',
    'S95nanokvm', 'S96picoclaw', 'nanokvm-server-supervisor',
}
legacy = '/usr/libexec/nanokvm/legacy/'
app_init = '/kvmapp/system/init.d/'

entries = {}
for line in (root / 'platform/packages.list').read_text().splitlines():
    fields = line.split()
    if not fields or fields[0].startswith('#'):
        continue
    package, kind, path = fields[:3]
    assert (package, path) not in entries, 'duplicate entry: ' + path
    entries[package, path] = (kind, *fields[3:])


def file_entry(package, path, source, mode='0755'):
    assert entries.get((package, path)) == ('file', mode, source), (package, path, entries.get((package, path)))


def link_entry(package, path, target):
    assert entries.get((package, path)) == ('link', '-', target), (package, path, entries.get((package, path)))


# The app ships only the init copies something still runs.
shipped = {Path(path).name for package, path in entries if package == 'app' and path.startswith(app_init)}
assert shipped == kept_init_scripts, 'app init copies: ' + ', '.join(sorted(shipped ^ kept_init_scripts))
assert not shipped & set(removed_init_scripts)

# S15kvmhwd: the compat version in both places, never the stock Sipeed copy.
file_entry('base', legacy + 'S15kvmhwd', 'firmware/alpine/compat/S15kvmhwd')
file_entry('app', app_init + 'S15kvmhwd', 'firmware/alpine/compat/S15kvmhwd')
link_entry('base', '/etc/init.d/S15kvmhwd', legacy + 'S15kvmhwd')

# A later app-only update must change the startup policy seen through the base
# entry point, so the base links to the app's S95 instead of carrying a copy.
link_entry('base', legacy + 'S95nanokvm', app_init + 'S95nanokvm')
link_entry('base', '/etc/init.d/S95nanokvm', legacy + 'S95nanokvm')
file_entry('app', app_init + 'S95nanokvm', 'kvmapp/system/init.d/S95nanokvm')
file_entry('app', app_init + 'nanokvm-server-supervisor', 'kvmapp/system/init.d/nanokvm-server-supervisor')

# Sophgo libsys.so runs `devmem`, which Alpine's BusyBox does not provide.
file_entry('base', '/usr/sbin/devmem', '@tools/devmem')
file_entry('base', '/usr/share/licenses/nanokvm-base/busybox-LICENSE', '@tools/busybox-LICENSE', '0644')
assert 'scripts/build-busybox-devmem.sh' in (root / 'platform/build.sh').read_text()

file_entry('base', '/usr/sbin/nkos-usb-internet', '@server/nkos-usb-internet')
file_entry('base', '/etc/init.d/nanokvm-usb-internet', 'firmware/alpine/openrc/nanokvm-usb-internet')
assert '"$s/out/nkos-usb-internet"' in (root / 'platform/build.sh').read_text()
file_entry('base', legacy + 'S30usbnet', 'firmware/alpine/compat/S30usbnet')
file_entry('base', legacy + 'S50avahi-daemon', 'firmware/alpine/compat/S50avahi-daemon')
link_entry('base', '/etc/init.d/S50avahi-daemon', legacy + 'S50avahi-daemon')

# Every /etc/init.d link of the base resolves to a legacy script it ships.
for (package, path), (kind, *rest) in entries.items():
    if package == 'base' and kind == 'link' and path.startswith('/etc/init.d/'):
        target = rest[1]
        assert target.startswith(legacy), (path, target)
        assert ('base', target) in entries, 'dangling init link: ' + path
for (package, path), (kind, *rest) in entries.items():
    if kind in ('file', 'tree') and not rest[1].startswith(('@', '%')):
        assert (root / rest[1]).exists(), 'missing source: ' + rest[1]

print('S15 packaging test passed')
