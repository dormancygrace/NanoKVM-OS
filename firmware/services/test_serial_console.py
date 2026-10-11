"""nanokvm-serial-console.sh against fake roots: what it detects, what it
refuses, that plan and status write nothing and run nothing, and that apply
and undo are reversible. Runs under dash, busybox sh and bash when present."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('nanokvm-serial-console.sh')
SERIAL = 'd19506b31359c372'
KERNEL = '6.1.0-test'

SHELLS = [s for s in (['dash'], ['busybox', 'sh'], ['bash']) if shutil.which(s[0])]
if not SHELLS:
    raise SystemExit('no POSIX shell found')


def tree_digest(root: Path):
    """Every path under root with the content of regular files."""
    out = {}
    for p in sorted(root.rglob('*')):
        rel = str(p.relative_to(root))
        if p.is_symlink():
            out[rel] = 'link:' + os.readlink(p)
        elif p.is_file():
            out[rel] = hashlib.sha256(p.read_bytes()).hexdigest()
        else:
            out[rel] = 'dir'
    return out


class FakeHost:
    def __init__(self, tmp, *, os_release='ID=debian\nPRETTY_NAME="Debian GNU/Linux 12 (bookworm)"\n',
                 init='systemd', systemd_unit=True, agetty=True, getty=False, inittab=False,
                 cdc='module', device=True, tty='ttyACM0', other_acm=(), container=None,
                 ostree=False, nixos=False, root_ro=False, selinux=None, modemmanager=False,
                 runit=False, busybox=False):
        self.root = Path(tmp) / 'root'
        self.bin = Path(tmp) / 'bin'
        self.log = Path(tmp) / 'calls.log'
        r = self.root
        for d in ('etc', 'proc/1', 'sys/bus/usb/devices', 'sys/class/tty', 'run', 'var'):
            (r / d).mkdir(parents=True, exist_ok=True)
        if os_release is not None:
            (r / 'etc/os-release').write_text(os_release)
        (r / 'proc/1/comm').write_text(init + '\n')
        (r / 'proc/1/environ').write_bytes(b'PATH=/usr/bin\0' + (b'container=lxc\0' if container == 'env' else b''))
        (r / 'proc/1/cgroup').write_text('0::/lxc/test\n' if container == 'cgroup' else '0::/init.scope\n')
        if container == 'file':
            (r / '.dockerenv').write_text('')
        (r / 'proc/mounts').write_text('/dev/root / ext4 %s,relatime 0 0\n' % ('ro' if root_ro else 'rw'))
        if init == 'systemd':
            (r / 'run/systemd/system').mkdir(parents=True)
            if systemd_unit:
                (r / 'lib/systemd/system').mkdir(parents=True)
                (r / 'lib/systemd/system/serial-getty@.service').write_text('[Unit]\n')
        if inittab:
            (r / 'etc/inittab').write_text('::sysinit:/sbin/openrc sysinit\ntty1::respawn:/sbin/getty 38400 tty1\n')
        if runit:
            (r / 'etc/runit').mkdir(parents=True)
            (r / 'etc/runit/1').write_text('#!/bin/sh\n')
        if busybox:
            # Alpine: /sbin/init and PID 1 are busybox
            (r / 'bin').mkdir(exist_ok=True)
            (r / 'bin/busybox').write_text('')
            (r / 'sbin').mkdir(exist_ok=True)
            (r / 'sbin/init').symlink_to('/bin/busybox')
            (r / 'proc/1/exe').symlink_to('/bin/busybox')
        for name, present in (('agetty', agetty), ('getty', getty)):
            if present:
                (r / 'sbin').mkdir(exist_ok=True)
                p = r / 'sbin' / name
                p.write_text('#!/bin/sh\n')
                p.chmod(0o755)
        if cdc == 'loaded':
            (r / 'sys/bus/usb/drivers/cdc_acm').mkdir(parents=True)
        elif cdc == 'builtin':
            (r / 'lib/modules' / KERNEL).mkdir(parents=True)
            (r / 'lib/modules' / KERNEL / 'modules.builtin').write_text('kernel/drivers/usb/class/cdc-acm.ko\n')
        elif cdc == 'module':
            d = r / 'lib/modules' / KERNEL / 'kernel/drivers/usb/class'
            d.mkdir(parents=True)
            (d / 'cdc-acm.ko.xz').write_bytes(b'')
        if device:
            d = r / 'sys/bus/usb/devices/1-1'
            d.mkdir()
            (d / 'idVendor').write_text('3346\n')
            (d / 'idProduct').write_text('1009\n')
            (d / 'serial').write_text(SERIAL + '\n')
            if tty:
                (d / '1-1:1.0/tty' / tty).mkdir(parents=True)
                (r / 'sys/class/tty' / tty).mkdir()
        for name in other_acm:
            (r / 'sys/class/tty' / name).mkdir()
        if ostree:
            (r / 'run/ostree-booted').write_text('')
        if nixos:
            (r / 'etc/NIXOS').write_text('')
        if selinux is not None:
            (r / 'sys/fs/selinux').mkdir(parents=True)
            (r / 'sys/fs/selinux/enforce').write_text(str(selinux))
        if modemmanager:
            (r / 'usr/sbin').mkdir(parents=True)
            (r / 'usr/sbin/ModemManager').write_text('')
        # command stubs: everything the script may run is logged
        self.bin.mkdir()
        for name in ('udevadm', 'systemctl', 'restorecon', 'modprobe'):
            stub = self.bin / name
            stub.write_text('#!/bin/sh\necho "%s $*" >> "$NANOKVM_TEST_LOG"\nexit 0\n' % name)
            stub.chmod(0o755)
        for name, body in (('id', 'echo 0'), ('sleep', ':')):
            stub = self.bin / name
            stub.write_text('#!/bin/sh\n%s\n' % body)
            stub.chmod(0o755)
        self.log.write_text('')

    def run(self, shell, *args, stdin=''):
        # busybox sh runs its own applets (uname, id, sleep) without PATH, so
        # the kernel release comes from the environment, not a stub.
        env = dict(os.environ, NANOKVM_ROOT=str(self.root), NANOKVM_TEST_LOG=str(self.log),
                   NANOKVM_KERNEL=KERNEL, PATH=str(self.bin) + ':/usr/bin:/bin')
        return subprocess.run(shell + [str(SCRIPT), *args], input=stdin, capture_output=True,
                              text=True, env=env, timeout=30)

    def calls(self):
        return self.log.read_text().splitlines()


class ScriptTest(unittest.TestCase):
    def host(self, **kw):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, True)
        return FakeHost(tmp, **kw)

    def assertReadOnly(self, host, shell, *args, code):
        before = tree_digest(host.root)
        host.log.write_text('')
        r = host.run(shell, *args)
        self.assertEqual(r.returncode, code, r.stdout + r.stderr)
        self.assertEqual(tree_digest(host.root), before, 'files changed by ' + ' '.join(args))
        self.assertEqual(host.calls(), [], 'commands run by ' + ' '.join(args))
        return r

    def test_systemd_plan_apply_undo(self):
        for shell in SHELLS:
            with self.subTest(shell=shell[0]):
                h = self.host()
                r = self.assertReadOnly(h, shell, code=0)
                self.assertIn('ok:      NanoKVM 3346:1009 is connected as /dev/ttyACM0 (serial %s)' % SERIAL, r.stdout)
                self.assertIn('plan:    write /etc/udev/rules.d/90-nanokvm-serial-console.rules', r.stdout)
                self.assertIn('serial ' + SERIAL, r.stdout)
                self.assertReadOnly(h, shell, 'status', code=0)
                self.assertNotIn('plan:', h.run(shell, 'status').stdout)

                r = h.run(shell, 'apply', '--yes')
                self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
                rule = (h.root / 'etc/udev/rules.d/90-nanokvm-serial-console.rules').read_text()
                lines = [l for l in rule.splitlines() if l and not l.startswith('#')]
                self.assertEqual(len(lines), 2)
                self.assertEqual(lines[0], 'SUBSYSTEM=="usb", ATTR{idVendor}=="3346", ATTR{idProduct}=="1009", '
                                 'ATTR{serial}=="%s", ENV{ID_MM_DEVICE_IGNORE}="1"' % SERIAL)
                self.assertIn('ACTION=="add", SUBSYSTEM=="tty", KERNEL=="ttyACM[0-9]*", ATTRS{idVendor}=="3346", '
                              'ATTRS{idProduct}=="1009", ATTRS{serial}=="%s", ' % SERIAL, lines[1])
                self.assertIn('ENV{ID_MM_DEVICE_IGNORE}="1", ENV{ID_MM_PORT_IGNORE}="1", TAG+="systemd", '
                              'ENV{SYSTEMD_WANTS}+="serial-getty@%k.service"', lines[1])
                self.assertEqual(h.calls(), ['udevadm control --reload',
                                             'udevadm trigger --subsystem-match=tty --sysname-match=ttyACM0 --action=add',
                                             'systemctl is-active --quiet serial-getty@ttyACM0.service'])
                self.assertIn('serial-getty@ttyACM0.service is running', r.stdout)
                manifests = list((h.root / 'var/backups/nanokvm-serial-console').glob('*/manifest'))
                self.assertEqual(len(manifests), 1)
                self.assertEqual(manifests[0].read_text().splitlines()[0],
                                 'created /etc/udev/rules.d/90-nanokvm-serial-console.rules')

                # set up: plan says so and apply changes nothing
                r = self.assertReadOnly(h, shell, code=0)
                self.assertIn('nothing to do: already set up', r.stdout)
                before = tree_digest(h.root)
                h.log.write_text('')
                r = h.run(shell, 'apply', '--yes')
                self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
                self.assertIn('already has this content', r.stdout)
                self.assertIn('nothing changed', r.stdout)
                # a no-op apply leaves no record, so undo reverts the real change
                self.assertEqual(tree_digest(h.root), before)

                h.log.write_text('')
                r = h.run(shell, 'undo')
                self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
                self.assertIn('removed /etc/udev/rules.d/90-nanokvm-serial-console.rules', r.stdout)
                self.assertFalse((h.root / 'etc/udev/rules.d/90-nanokvm-serial-console.rules').exists())
                self.assertEqual(h.calls(), ['udevadm control --reload',
                                             'systemctl stop serial-getty@ttyACM0.service'])
                r = h.run(shell, 'undo')
                self.assertEqual(r.returncode, 0)
                self.assertIn('nothing to undo', r.stdout)

    def test_apply_needs_a_terminal_or_yes(self):
        h = self.host()
        r = self.assertReadOnly(h, SHELLS[0], 'apply', code=0)
        self.assertIn('pass --yes', r.stdout)

    def test_device_absent_needs_serial(self):
        h = self.host(device=False)
        r = self.assertReadOnly(h, SHELLS[0], code=0)
        self.assertIn('warn:    NanoKVM is not connected now and no --serial given', r.stdout)
        r = h.run(SHELLS[0], 'apply', '--yes')
        self.assertEqual(r.returncode, 1)
        self.assertIn('needs its serial number', r.stderr)
        self.assertFalse((h.root / 'etc/udev').exists())

        r = h.run(SHELLS[0], 'apply', '--yes', '--serial', 'abc123')
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn('ATTRS{serial}=="abc123"', (h.root / 'etc/udev/rules.d/90-nanokvm-serial-console.rules').read_text())
        self.assertEqual(h.calls(), ['udevadm control --reload'])
        self.assertIn('the login starts when the NanoKVM', r.stdout)

    def test_connected_device_wins_over_serial_option(self):
        h = self.host()
        r = h.run(SHELLS[0], 'plan', '--serial=other')
        self.assertIn('warn:    --serial other differs from the connected NanoKVM', r.stdout)
        self.assertIn('serial ' + SERIAL, r.stdout)

    def test_device_without_tty(self):
        h = self.host(tty=None)
        r = self.assertReadOnly(h, SHELLS[0], code=0)
        self.assertIn('has no ttyACM: USB serial is not enabled on it', r.stdout)

    def test_other_acm_ports_are_only_mentioned(self):
        h = self.host(other_acm=('ttyACM1', 'ttyACM2'))
        r = h.run(SHELLS[0])
        self.assertIn('other CDC ACM ports: ttyACM1 ttyACM2', r.stdout)
        self.assertEqual(r.returncode, 0)

    def test_refusals(self):
        cases = {
            'container env': (dict(container='env'), 2, 'this is a container'),
            'container cgroup': (dict(container='cgroup'), 2, 'this is a container'),
            'container file': (dict(container='file'), 2, 'this is a container'),
            'ostree': (dict(ostree=True), 2, 'OSTree'),
            'nixos': (dict(nixos=True), 2, 'NixOS'),
            'read-only root': (dict(root_ro=True), 2, 'mounted read-only'),
            'no cdc_acm': (dict(cdc=None), 2, 'no cdc_acm driver for kernel ' + KERNEL),
            'no serial-getty unit': (dict(systemd_unit=False), 2, 'without serial-getty@.service'),
            'no agetty': (dict(agetty=False), 2, 'no agetty'),
            'runit': (dict(init='runit', runit=True), 3, 'runit as PID 1 is not handled'),
            'unknown init': (dict(init='init'), 3, 'unknown init system'),
            'no os-release': (dict(os_release=None, init='init'), 3, 'unknown (no /etc/os-release)'),
        }
        for name, (kw, code, text) in cases.items():
            with self.subTest(name):
                h = self.host(**kw)
                for args in ((), ('apply', '--yes')):
                    r = self.assertReadOnly(h, SHELLS[0], *args, code=code)
                    self.assertIn(text, r.stdout)
                    self.assertIn('Manual setup', r.stdout)
                r = self.assertReadOnly(h, SHELLS[0], 'status', code=2)

    def test_cdc_variants_and_notes(self):
        for cdc, text in (('loaded', 'driver is loaded'), ('builtin', 'built into the kernel'),
                          ('module', 'module is available')):
            with self.subTest(cdc):
                r = self.host(cdc=cdc).run(SHELLS[0])
                self.assertEqual(r.returncode, 0)
                self.assertIn(text, r.stdout)
        r = self.host(modemmanager=True).run(SHELLS[0])
        self.assertIn('ModemManager is installed', r.stdout)

    def test_selinux_relabels_the_rule(self):
        h = self.host(selinux=1, os_release='ID=rocky\nID_LIKE="rhel centos fedora"\nPRETTY_NAME="Rocky Linux 9.4"\n')
        r = h.run(SHELLS[0], 'apply', '--yes')
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn('like rhel centos fedora', r.stdout)
        self.assertEqual(h.calls()[0], 'restorecon %s/etc/udev/rules.d/90-nanokvm-serial-console.rules' % h.root)

    def test_rule_for_another_serial_is_reported(self):
        h = self.host()
        (h.root / 'etc/udev/rules.d').mkdir(parents=True)
        (h.root / 'etc/udev/rules.d/90-nanokvm-serial-console.rules').write_text('ATTRS{serial}=="old"\n')
        r = self.assertReadOnly(h, SHELLS[0], code=0)
        self.assertIn('is present for serial old, not ' + SERIAL, r.stdout)
        self.assertIn('plan:    write', r.stdout)
        r = h.run(SHELLS[0], 'apply', '--yes')
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        manifest = next((h.root / 'var/backups/nanokvm-serial-console').glob('*/manifest'))
        self.assertEqual(manifest.read_text().splitlines()[0], 'replaced /etc/udev/rules.d/90-nanokvm-serial-console.rules')
        self.assertEqual((manifest.parent / '90-nanokvm-serial-console.rules.before').read_text(), 'ATTRS{serial}=="old"\n')
        h.run(SHELLS[0], 'undo')
        self.assertEqual((h.root / 'etc/udev/rules.d/90-nanokvm-serial-console.rules').read_text(), 'ATTRS{serial}=="old"\n')

    def test_inittab_init(self):
        alpine = 'ID=alpine\nPRETTY_NAME="Alpine Linux v3.21"\n'
        for shell in SHELLS:
            with self.subTest(shell=shell[0]):
                h = self.host(os_release=alpine, init='init', inittab=True, agetty=False, getty=True, busybox=True)
                r = self.assertReadOnly(h, shell, code=0)
                self.assertIn('init:    inittab', r.stdout)
                self.assertIn('busybox init with /etc/inittab, getty /sbin/getty', r.stdout)
                self.assertIn('plan:    append one respawn line for /dev/ttyACM0 to /etc/inittab', r.stdout)
                before = (h.root / 'etc/inittab').read_text()
                r = h.run(shell, 'apply', '--yes')
                self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
                after = (h.root / 'etc/inittab').read_text()
                self.assertTrue(after.startswith(before))
                self.assertEqual(after[len(before):],
                                 'ttyACM0::respawn:/sbin/getty -L 115200 ttyACM0 vt220 # nanokvm-serial-console\n')
                self.assertEqual(h.calls(), [])  # init is not signalled under a fake root
                r = h.run(shell, 'apply', '--yes')
                self.assertIn('already has the console line', r.stdout)
                self.assertEqual((h.root / 'etc/inittab').read_text(), after)
                r = h.run(shell, 'undo')
                self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
                self.assertEqual((h.root / 'etc/inittab').read_text(), before)

    def test_sysvinit_inittab_line(self):
        # Devuan: sysvinit with its id:runlevels:action:process form
        devuan = 'ID=devuan\nID_LIKE=debian\nPRETTY_NAME="Devuan GNU/Linux 5 (daedalus)"\n'
        h = self.host(os_release=devuan, init='init', inittab=True)
        r = self.assertReadOnly(h, SHELLS[0], code=0)
        self.assertIn('ok:      init with /etc/inittab, getty /sbin/agetty', r.stdout)
        r = h.run(SHELLS[0], 'apply', '--yes')
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertTrue((h.root / 'etc/inittab').read_text().endswith(
            'nkac:2345:respawn:/sbin/agetty -L 115200 ttyACM0 vt220 # nanokvm-serial-console\n'))

    def test_inittab_without_device_warns(self):
        h = self.host(init='init', inittab=True, device=False)
        r = h.run(SHELLS[0], 'apply', '--yes', '--serial', SERIAL)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn('respawning too fast', r.stdout)
        self.assertIn('nkac:2345:respawn:/sbin/agetty -L 115200 ttyACM0', (h.root / 'etc/inittab').read_text())

    def test_old_debian_layout(self):
        h = self.host(os_release='ID=debian\nPRETTY_NAME="Debian GNU/Linux 8 (jessie)"\n')
        r = h.run(SHELLS[0])
        self.assertEqual(r.returncode, 0)
        self.assertIn('Debian GNU/Linux 8', r.stdout)

    def test_help_and_bad_argument(self):
        r = self.host().run(SHELLS[0], '--help')
        self.assertEqual(r.returncode, 0)
        self.assertIn('plan', r.stdout)
        r = self.host().run(SHELLS[0], '--bogus')
        self.assertEqual(r.returncode, 1)


if __name__ == '__main__':
    unittest.main()
