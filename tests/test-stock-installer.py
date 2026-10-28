#!/usr/bin/env python3
"""Exercise MBR transactions, authenticated-download parsing and stock staging.

All block devices are sparse files. Tests execute the real layout helper and
stock shell entry point (filesystem paths isolated, uname/df/reboot simulated).
No physical device is opened and no installed filesystem is changed.
"""
import hashlib
import gzip
import importlib.util
import io
import os
import re
from pathlib import Path
import shutil
import struct
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

REPO = Path(__file__).resolve().parents[1]
SOURCE = REPO / 'firmware/stock-install'


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


bootstrap = load('stock_bootstrap', REPO / 'scripts/install-stock.py')
builder = load('stock_builder', REPO / 'scripts/build-stock-installer.py')


def mbr(parts=((1, 32768, 0x0c), (32769, 3145728, 0x83), (3180544, 1048576, 7))):
    value = bytearray(512)
    value[:16] = bytes(range(16))
    value[440:444] = b'NKOS'
    value[510:] = b'\x55\xaa'
    for index, (start, size, kind) in enumerate(parts):
        offset = 446 + 16 * index
        value[offset + 4] = kind
        struct.pack_into('<II', value, offset + 8, start, size)
    return value


def disk(path, value=None):
    value = mbr() if value is None else value
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('wb') as stream:
        stream.write(value)
        stream.truncate(4 * 1024**3)
    return bytes(value)


def write(root, path, data='', mode=0o644):
    target = root / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(data)
    target.chmod(mode)
    return target


class LayoutFixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix='stock-layout-')
        cls.bin = Path(cls.temp.name) / 'layout'
        subprocess.run(['cc', '-std=c11', '-O2', '-Wall', '-Wextra', '-Werror',
                        '-DSTOCK_LAYOUT_TEST', str(SOURCE / 'layout.c'), '-o', str(cls.bin)], check=True)

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def setUp(self):
        self.work = tempfile.TemporaryDirectory(prefix='stock-card-')
        self.addCleanup(self.work.cleanup)
        self.root = Path(self.work.name)
        self.card = self.root / 'card'
        self.original = disk(self.card)
        self.saved = self.root / 'mbr.old'
        self.saved.write_bytes(self.original)

    def command(self, *args):
        return subprocess.run([str(self.bin), *map(str, args)], capture_output=True)


class Layout(LayoutFixture):
    def test_plan_preserves_boot_and_data(self):
        result = self.command('plan', self.card)
        self.assertEqual(result.returncode, 0, result.stderr)
        fields = list(map(int, result.stdout.split()))
        self.assertEqual(fields[:6], [1, 32768, 32769, 3145728, 3180544, 1048576])
        new_boot, new_root, new_size = fields[6:]
        self.assertEqual(new_boot % 8192, 0)
        self.assertEqual(new_root - new_boot, 131072)
        self.assertGreaterEqual(new_boot, fields[2])
        self.assertEqual(new_root + new_size, fields[2] + fields[3])
        self.assertGreaterEqual(new_size, 1572864)

    def test_commit_changes_only_two_entries_and_is_idempotent(self):
        saved = self.command('save', self.card)
        self.assertEqual(saved.stdout, self.original)
        self.assertEqual(self.command('state', self.card, self.saved).stdout, b'stock\n')
        self.assertEqual(self.command('commit', self.card, self.saved).returncode, 0)
        with self.card.open('rb') as stream:
            current = stream.read(512)
        self.assertEqual(current[:446], self.original[:446])
        self.assertEqual(current[478:], self.original[478:])
        self.assertNotEqual(current[446:478], self.original[446:478])
        self.assertEqual(self.command('state', self.card, self.saved).stdout, b'installed\n')
        self.assertEqual(self.command('commit', self.card, self.saved).returncode, 0)
        # Mapping again must never format a filesystem that already runs NKOS.
        result = self.command('map-root', self.card, self.saved)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b'refusing to remap', result.stderr)

    def test_refuses_geometry_and_stale_transactions(self):
        for label, change in [
            ('GPT', lambda m: m.__setitem__(450, 0xee)),
            ('wrong root', lambda m: m.__setitem__(466, 0x0b)),
            ('wrong data', lambda m: m.__setitem__(482, 0x05)),
            ('fourth partition', lambda m: m.__setitem__(498, 0x83)),
            ('missing signature', lambda m: m.__setitem__(510, 0)),
            ('overlap', lambda m: struct.pack_into('<I', m, 470, 100)),
            ('too small', lambda m: struct.pack_into('<I', m, 474, 1572864)),
            ('overflow', lambda m: struct.pack_into('<I', m, 490, 0xffffffff)),
        ]:
            with self.subTest(label=label):
                value = mbr()
                change(value)
                disk(self.card, value)
                result = self.command('plan', self.card)
                self.assertNotEqual(result.returncode, 0)
                with self.card.open('rb') as stream:
                    self.assertEqual(stream.read(512), value)
        disk(self.card)
        with self.card.open('r+b') as stream:
            stream.seek(440)
            stream.write(b'FAIL')
        self.assertIn(b'changed since staging', self.command('commit', self.card, self.saved).stderr)

    def test_production_refuses_regular_files(self):
        production = self.root / 'production'
        subprocess.run(['cc', '-Wall', '-Wextra', '-Werror', str(SOURCE / 'layout.c'), '-o', str(production)], check=True)
        result = subprocess.run([str(production), 'commit', str(self.card), str(self.saved)], capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b'only /dev/mmcblk0', result.stderr)


class Download(unittest.TestCase):
    def test_unique_exact_checksum(self):
        value = 'a' * 64
        name = 'nanokvm_3.6.0.tar.gz'
        self.assertEqual(bootstrap.expected_hash(value + '  ' + name + '\n', name), value)
        for data in ['', 'bad  ' + name, value + '  ../' + name,
                     (value + '  ' + name + '\n') * 2]:
            with self.subTest(data=data):
                with self.assertRaises(ValueError):
                    bootstrap.expected_hash(data, name)

    def test_redirect_rejects_plain_http(self):
        with self.assertRaises(ValueError):
            bootstrap.HTTPSOnly().redirect_request(None, None, 302, '', {}, 'http://example.com/payload')
        with self.assertRaises(ValueError):
            bootstrap.fetch('http://example.com/payload', 100)

    def test_truncated_and_oversized_downloads(self):
        class Response(io.BytesIO):
            headers = {'Content-Length': '12'}
        class Opener:
            def open(self, *args, **kwargs):
                return Response(b'short')
        with patch.object(bootstrap.urllib.request, 'build_opener', return_value=Opener()):
            with self.assertRaisesRegex(ValueError, 'truncated'):
                bootstrap.fetch('https://example.com/payload', 100)
            with self.assertRaisesRegex(ValueError, 'size limit'):
                bootstrap.fetch('https://example.com/payload', 3)

    def test_safe_extract_and_malicious_archives(self):
        with tempfile.TemporaryDirectory(prefix='stock-archive-') as temporary:
            root = Path(temporary)
            prefix = 'nanokvm_3.6.0'
            tree = root / prefix
            for path in ['version', 'migration/install.sh', 'migration/SHA256SUMS',
                         'server/NanoKVM-Server', 'kvm_system/kvm_system', 'system/init.d/S95nanokvm']:
                write(tree, path, '3.6.0\n' if path == 'version' else 'test\n', 0o755)
            archive = root / 'package.tar.gz'
            builder.package_tree(tree, archive)
            target = root / 'out'
            target.mkdir()
            payload = bootstrap.unpack(archive, target, '3.6.0')
            self.assertTrue((payload / 'install.sh').is_file())
            expected = hashlib.sha256(archive.read_bytes()).hexdigest()
            bootstrap.verify(archive, expected)
            with self.assertRaises(ValueError):
                bootstrap.verify(archive, '0' * 64)
            for name, kind in [(prefix + '/../escape', tarfile.REGTYPE),
                               ('/absolute', tarfile.REGTYPE), ('other/file', tarfile.REGTYPE),
                               (prefix + '/link', tarfile.SYMTYPE), (prefix + '/hard', tarfile.LNKTYPE),
                               (prefix + '/device', tarfile.CHRTYPE)]:
                with self.subTest(name=name):
                    with tarfile.open(archive, 'w:gz') as tar:
                        info = tarfile.TarInfo(name)
                        info.type = kind
                        info.linkname = '/etc/shadow'
                        tar.addfile(info)
                    empty = root / 'empty'
                    empty.mkdir(exist_ok=True)
                    with self.assertRaises(ValueError):
                        bootstrap.unpack(archive, empty, '3.6.0')
                    self.assertEqual(list(empty.iterdir()), [])


class Staging(LayoutFixture):
    def prepare(self):
        root = self.root
        self.card = root / 'dev/mmcblk0'
        self.original = disk(self.card)
        self.bundle = root / 'bundle'
        self.bundle.mkdir()
        shutil.copyfile(self.bin, self.bundle / 'stock-layout')
        (self.bundle / 'stock-layout').chmod(0o755)
        for path, content in {'etc/kvm/hw': 'pcie\n', 'kvmapp/version': '2.5.0\n',
                              'etc/shadow': 'root:TESTHASH:1::::::\ndaemon:*:1::::::\n',
                              'etc/kvm/wifi.ssid': 'my network', 'etc/kvm/wifi.pass': 'password',
                              'root/.ssh/authorized_keys': 'ssh-ed25519 fixture\n',
                              'boot/boot.sd': 'stock boot', 'boot/eth.nodhcp': '192.0.2.2/24 192.0.2.1\n',
                              'data/user.iso': 'do not change',
                              'proc/device-tree/compatible': 'sophgo,sg2002\x00',
                              'proc/cmdline': 'root=' + str(root / 'dev/mmcblk0p2') + ' rw\n'}.items():
            write(root, path, content)
        (root / 'run').mkdir()
        (root / 'tmp').mkdir()
        for i, (start, size) in enumerate([(1,32768),(32769,3145728),(3180544,1048576)], 1):
            write(root, f'sys/class/block/mmcblk0p{i}/start', str(start))
            write(root, f'sys/class/block/mmcblk0p{i}/size', str(size))
        write(root, 'proc/mounts', '\n'.join([
            f'{root}/dev/mmcblk0p1 {root}/boot vfat rw 0 0',
            f'{root}/dev/mmcblk0p3 {root}/data exfat rw 0 0',
            f'{root}/dev/mmcblk0p2 / ext4 rw 0 0']) + '\n')
        for name in ['recovery.sd', 'boot.img.gz', 'alpine-rootfs.tar.gz', 'install.sh', 'offline-run.sh']:
            write(self.bundle, name, name + ' fixture')
        write(self.bundle, 'rootfs-kib', '1024\n')
        write(self.bundle, 'SHA256SUMS', ''.join(hashlib.sha256(f.read_bytes()).hexdigest() + '  ' + f.name + '\n'
              for f in sorted(self.bundle.iterdir())))
        shims = root / 'shims'
        shims.mkdir()
        write(shims, 'id', '#!/bin/sh\necho 0\n', 0o755)
        write(shims, 'uname', '#!/bin/sh\necho riscv64\n', 0o755)
        write(shims, 'df', '#!/bin/sh\necho "Filesystem 1024-blocks Used Available Capacity Mounted"\necho "disk 9999999 1 ${TEST_FREE_KIB:-9999998} 1% mount"\n', 0o755)
        write(shims, 'sync', '#!/bin/sh\nexit 0\n', 0o755)
        write(shims, 'reboot', '#!/bin/sh\necho reboot >"$TEST_ROOT/rebooted"\n', 0o755)
        source = (SOURCE / 'install.sh').read_text()
        source = re.sub(r'(?<![A-Za-z0-9_/.])/(?:proc|sys|etc|boot|data|run|tmp|root|kvmapp|dev)(?=/|\b)',
                        lambda match: str(root) + match.group(), source)
        source = source.replace('"/$path"', '"' + str(root) + '/$path"')
        source = source.replace('PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'PATH=' + str(shims) + ':/usr/sbin:/usr/bin:/sbin:/bin')
        # BusyBox ash may prefer internal applets to PATH; functions override
        # those too. In particular a test must never run the host reboot applet.
        overrides = """id() { echo 0; }
uname() { echo riscv64; }
df() { echo 'Filesystem 1024-blocks Used Available Capacity Mounted'; echo "disk 9999999 1 ${TEST_FREE_KIB:-9999998} 1% mount"; }
sync() { :; }
reboot() { echo reboot >"$TEST_ROOT/rebooted"; }
"""
        source = source.replace('export PATH\n', 'export PATH\n' + overrides)
        self.script = write(root, 'install-isolated.sh', source)
        self.env = {**os.environ, 'TEST_ROOT': str(root)}

    def invoke(self, mode='--check'):
        return subprocess.run(['busybox', 'sh', str(self.script), str(self.bundle), mode],
                              env=self.env, text=True, capture_output=True, timeout=30)

    def test_check_makes_no_staging_or_boot_changes(self):
        self.prepare()
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / 'data/.nanokvm-stock-install').exists())
        self.assertEqual((self.root / 'boot/boot.sd').read_text(), 'stock boot')
        self.assertFalse((self.root / 'rebooted').exists())

    def test_success_stages_and_activates_without_partition_writes(self):
        self.prepare()
        result = self.invoke('--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        stage = self.root / 'data/.nanokvm-stock-install'
        self.assertEqual((stage / 'mbr.old').read_bytes(), self.original)
        self.assertEqual((stage / 'stock-boot.sd').read_text(), 'stock boot')
        self.assertEqual((stage / 'root.shadow').read_text(), 'root:TESTHASH:1::::::\n')
        self.assertEqual((self.root / 'boot/boot.sd').read_bytes(), (self.bundle / 'recovery.sd').read_bytes())
        self.assertEqual((self.root / 'data/user.iso').read_text(), 'do not change')
        self.assertTrue((self.root / 'rebooted').exists())
        with self.card.open('rb') as stream:
            self.assertEqual(stream.read(512), self.original)
        with tarfile.open(stage / 'settings.tar') as tar:
            self.assertEqual(tar.extractfile('./etc/kvm/wifi.ssid').read(), b'my network')
        # A retry never overwrites the transaction with a new snapshot.
        self.assertNotEqual(self.invoke('--yes').returncode, 0)

    def test_preflight_refuses_bad_inputs_before_writes(self):
        self.prepare()
        baseline = self.invoke()
        self.assertEqual(baseline.returncode, 0, baseline.stderr)
        reasons = {'checksum': 'damaged installation bundle', 'space': 'lacks space',
                   'readonly': 'must be writable', 'gadget': 'detach every USB',
                   'geometry': 'geometry differ'}
        for kind in reasons:
            with self.subTest(kind=kind):
                old = None
                file = None
                if kind == 'checksum':
                    file = self.bundle / 'recovery.sd'; old = file.read_bytes(); file.write_bytes(b'broken')
                elif kind == 'space':
                    self.env['TEST_FREE_KIB'] = '1'
                elif kind == 'readonly':
                    file = self.root / 'proc/mounts'; old = file.read_bytes(); file.write_text(file.read_text().replace('exfat rw', 'exfat ro'))
                elif kind == 'gadget':
                    file = write(self.root, 'sys/kernel/config/usb_gadget/g0/functions/mass_storage.usb0/lun.0/file', '/data/user.iso')
                else:
                    file = self.root / 'sys/class/block/mmcblk0p2/start'; old = file.read_bytes(); file.write_text('99')
                result = self.invoke('--yes')
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn(reasons[kind], result.stderr)
                self.assertFalse((self.root / 'data/.nanokvm-stock-install').exists())
                self.assertEqual((self.root / 'boot/boot.sd').read_text(), 'stock boot')
                if file is not None:
                    if old is None: file.unlink()
                    else: file.write_bytes(old)
                self.env.pop('TEST_FREE_KIB', None)



class Recovery(LayoutFixture):
    def prepare(self):
        Staging.prepare(self)
        rootfs = self.root / 'new-root'
        for path, text, mode in [
            ('etc/alpine-release', '3.24.3\n', 0o644), ('bin/busybox', 'executable', 0o755),
            ('etc/init.d/nanokvm-app', 'executable', 0o755),
            ('etc/shadow', 'root:IMAGE:1::::::\ndaemon:*:1::::::\n', 0o600),
            ('etc/kvm/ssh_stop', '', 0o600), ('etc/nanokvm-firstboot-storage', '', 0o644)]:
            write(rootfs, path, text, mode)
        with tarfile.open(self.bundle / 'alpine-rootfs.tar.gz', 'w:gz') as tar:
            tar.add(rootfs, arcname='.')
        (rootfs / 'etc').chmod(0o755)
        boot_raw = b'boot filesystem fixture\n' * 1024
        (self.bundle / 'boot.img.gz').write_bytes(gzip.compress(boot_raw))
        (self.bundle / 'SHA256SUMS').write_text(''.join(
            hashlib.sha256(f.read_bytes()).hexdigest() + '  ' + f.name + '\n'
            for f in sorted(self.bundle.iterdir()) if f.name != 'SHA256SUMS'))
        result = Staging.invoke(self, '--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.stage = self.root / 'data/.nanokvm-stock-install'
        self.raw_hash = hashlib.sha256(boot_raw).hexdigest()
        wrapper = write(self.root, 'layout-wrapper', r"""#!/bin/sh
case "$1" in
 map-boot) echo map-boot >>"$TEST_ROOT/actions"; echo "$TEST_ROOT/boot-loop" ;;
 map-root) echo map-root >>"$TEST_ROOT/actions"; echo "$TEST_ROOT/root-loop" ;;
 commit)
    echo commit >>"$TEST_ROOT/actions"
    [ "${FAIL_STEP:-}" != commit ] || exit 31
    exec "$TEST_LAYOUT" "$@" ;;
 *) exec "$TEST_LAYOUT" "$@" ;;
esac
""", 0o755)
        self.env['TEST_LAYOUT'] = str(self.bin)
        source = (SOURCE / 'init').read_text()
        for token, value in {'ROOT_HASH': hashlib.sha256((self.stage / 'alpine-rootfs.tar.gz').read_bytes()).hexdigest(),
                             'BOOT_HASH': hashlib.sha256((self.stage / 'boot.img.gz').read_bytes()).hexdigest(),
                             'BOOT_RAW_HASH': self.raw_hash}.items():
            source = source.replace('@' + token + '@', value)
        source = source.replace('/busybox mkdir', 'mkdir').replace('/sbin/stock-layout', str(wrapper))
        source = source.replace('/sbin/mkfs.f2fs', 'format_root')
        source = source.replace('exec /bin/sh </dev/console >/dev/console 2>&1', 'exit 81')
        source = re.sub(r'(?<![A-Za-z0-9_/.])/(?:dev|proc|sys|data|realroot|oldboot|settings.paths)(?=/|\b)',
                        lambda match: str(self.root) + match.group(), source)
        overrides = r"""
mount() { echo mount >>"$TEST_ROOT/actions"; }
umount() { echo umount >>"$TEST_ROOT/actions"; }
losetup() { echo detach >>"$TEST_ROOT/actions"; }
sync() { :; }
sleep() { :; }
reboot() { echo reboot >>"$TEST_ROOT/actions"; trap - EXIT; exit 80; }
format_root() {
    echo format >>"$TEST_ROOT/actions"
    [ "${FAIL_STEP:-}" != format ] || return 32
    rm -rf "$TEST_ROOT/realroot"
    mkdir -p "$TEST_ROOT/realroot"
}
"""
        source = source.replace('export PATH=/bin:/sbin', 'export PATH=/usr/bin:/bin:/usr/sbin:/sbin\n' + overrides)
        self.recovery = write(self.root, 'init-isolated.sh', source)

    def recover(self):
        return subprocess.run(['busybox', 'sh', str(self.recovery)], env=self.env,
                              text=True, capture_output=True, timeout=30)

    def test_complete_preserves_access_and_commits_last(self):
        self.prepare()
        result = self.recover()
        self.assertEqual(result.returncode, 80, result.stderr)
        self.assertEqual((self.root / 'realroot/etc/shadow').read_text(), 'root:TESTHASH:1::::::\ndaemon:*:1::::::\n')
        self.assertEqual((self.root / 'realroot/etc/kvm/wifi.ssid').read_text(), 'my network')
        self.assertEqual((self.root / 'realroot/boot/eth.nodhcp').read_text(), '192.0.2.2/24 192.0.2.1\n')
        self.assertEqual((self.root / 'realroot/etc').stat().st_mode & 0o777, 0o755)
        self.assertEqual((self.root / 'realroot/etc/kvm/wifi.pass').stat().st_mode & 0o777, 0o600)
        self.assertFalse((self.root / 'realroot/etc/kvm/ssh_stop').exists())
        self.assertFalse((self.root / 'realroot/etc/nanokvm-firstboot-storage').exists())
        self.assertTrue((self.root / 'realroot/etc/nanokvm-stock-migration').exists())
        self.assertEqual(self.command('state', self.card, self.stage / 'mbr.old').stdout, b'installed\n')
        actions = (self.root / 'actions').read_text().splitlines()
        self.assertGreater(actions.index('commit'), actions.index('format'))
        self.assertEqual(actions[actions.index('commit')-2:actions.index('commit')], ['detach', 'detach'])
        self.assertEqual((self.root / 'data/user.iso').read_text(), 'do not change')
        # An already committed card must never be formatted again.
        previous = actions.count('format')
        self.assertEqual(self.recover().returncode, 80)
        self.assertEqual((self.root / 'actions').read_text().splitlines().count('format'), previous)

    def test_restart_after_interrupted_format_or_commit(self):
        for step in ['format', 'commit']:
            with self.subTest(step=step):
                # Each case needs a distinct stock card and settings transaction.
                self.setUp()
                self.prepare()
                self.env['FAIL_STEP'] = step
                result = self.recover()
                self.assertEqual(result.returncode, 81, result.stderr)
                self.assertTrue((self.stage / 'formatting').is_file())
                self.assertEqual(self.command('state', self.card, self.stage / 'mbr.old').stdout, b'stock\n')
                shadow = (self.stage / 'root.shadow').read_bytes()
                self.env.pop('FAIL_STEP')
                result = self.recover()
                self.assertEqual(result.returncode, 80, result.stderr)
                self.assertEqual((self.stage / 'root.shadow').read_bytes(), shadow)
                self.assertEqual(self.command('state', self.card, self.stage / 'mbr.old').stdout, b'installed\n')

    def test_corrupt_payload_returns_to_stock_before_formatting(self):
        self.prepare()
        (self.stage / 'boot.img.gz').write_bytes(b'broken')
        result = self.recover()
        self.assertEqual(result.returncode, 80, result.stderr)
        self.assertIn('checksum mismatch', (self.stage / 'status').read_text())
        self.assertEqual((self.root / 'oldboot/boot.sd').read_text(), 'stock boot')
        self.assertNotIn('format', (self.root / 'actions').read_text().splitlines())
        self.assertFalse((self.stage / 'formatting').exists())
        self.assertEqual(self.command('state', self.card, self.stage / 'mbr.old').stdout, b'stock\n')

    def test_settings_link_is_rejected_before_formatting(self):
        self.prepare()
        with tarfile.open(self.stage / 'settings.tar', 'w:') as tar:
            info = tarfile.TarInfo('./etc/hostname')
            info.type = tarfile.SYMTYPE
            info.linkname = '/etc/shadow'
            tar.addfile(info)
        (self.stage / 'transaction.sha256').write_text(''.join(
            hashlib.sha256((self.stage / name).read_bytes()).hexdigest() + '  ' + name + '\n'
            for name in ['mbr.old', 'stock-boot.sd', 'root.shadow', 'ssh-state', 'settings.tar']))
        result = self.recover()
        self.assertEqual(result.returncode, 80, result.stderr)
        self.assertIn('link or special file', result.stderr)
        self.assertNotIn('format', (self.root / 'actions').read_text().splitlines())

    def test_corrupt_settings_never_format_or_restore_untrusted_boot(self):
        self.prepare()
        (self.stage / 'settings.tar').write_bytes(b'broken')
        result = self.recover()
        self.assertEqual(result.returncode, 81, result.stderr)
        self.assertIn('transaction is incomplete', result.stderr)
        self.assertNotIn('format', (self.root / 'actions').read_text().splitlines())
        self.assertFalse((self.root / 'oldboot/boot.sd').exists())



class OfflinePackage(unittest.TestCase):
    def test_restores_stock_app_before_dispatch_and_refuses_missing_backup(self):
        with tempfile.TemporaryDirectory(prefix='stock-offline-') as temporary:
            root = Path(temporary)
            write(root, 'root/old/server/NanoKVM-Server', 'stock binary')
            write(root, 'root/old/system/init.d/S95nanokvm', '#!/bin/sh\necho stock-restart >>"$TEST_ROOT/order"\n', 0o755)
            write(root, 'kvmapp/migration/install.sh', '#!/bin/sh\necho install >>"$TEST_ROOT/order"\n', 0o755)
            (root / 'var/log').mkdir(parents=True)
            def isolate(text):
                return re.sub(r'(?<![A-Za-z0-9_/.])/(?:root|kvmapp|var)(?=/|\b)',
                              lambda match: str(root) + match.group(), text)
            write(root, 'kvmapp/migration/offline-run.sh', isolate((SOURCE / 'offline-run.sh').read_text()), 0o755)
            script = write(root, 'hook', isolate((SOURCE / 'S95nanokvm').read_text()) + '\nwait\n')
            env = {**os.environ, 'TEST_ROOT': str(root)}
            result = subprocess.run(['busybox', 'sh', str(script), 'restart'], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / 'order').read_text(), 'stock-restart\ninstall\n')
            self.assertEqual((root / 'kvmapp/server/NanoKVM-Server').read_text(), 'stock binary')
            self.assertTrue((root / 'root/.nanokvm-stock-package/migration/install.sh').is_file())
            result = subprocess.run(['busybox', 'sh', str(script), 'restart'], env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('already exists', result.stderr)
            shutil.rmtree(root / 'root/.nanokvm-stock-package')
            result = subprocess.run(['busybox', 'sh', str(script), 'restart'], env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('backup', result.stderr)
            self.assertEqual((root / 'kvmapp/server/NanoKVM-Server').read_text(), 'stock binary')


if __name__ == '__main__':
    unittest.main()
