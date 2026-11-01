#!/usr/bin/env python3
"""Exercise the bootloader activation script on isolated host fixtures, never a device."""
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'firmware/alpine/packages/nanokvm-bootloader/activate-bootloader'
NEW, OLD = b'new fip ' * 64, b'old fip ' * 64


class BootloaderActivationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.payload = self.root / 'payload'
        self.boot = self.root / 'fat'
        for path in (self.payload, self.boot, self.root / 'bin', self.root / 'run', self.root / 'etc/kvm'):
            path.mkdir(parents=True, exist_ok=True)
        (self.payload / 'fip.bin').write_bytes(NEW)
        (self.boot / 'fip.bin').write_bytes(OLD)
        (self.root / 'mounts').write_text('/dev/mmcblk0p1 ' + str(self.boot) + ' vfat rw 0 0\n')
        self.stub('mountpoint', 'exit 0')
        # Reads "from the card"; CORRUPT names a file to read back wrong.
        self.stub('readcard', '[ "$(basename "$1")" = "${CORRUPT:-}" ] && { echo garbage; exit 0; }; cat "$1"')
        source = SOURCE.read_text()
        source = source.replace('dd if="$1" bs=64k iflag=direct 2>/dev/null', 'readcard "$1"')
        source = source.replace('/usr/lib/nanokvm/bootloader', str(self.payload))
        source = re.sub(r'/boot(?=/|\s|;|"|$)', str(self.boot), source)
        source = source.replace('/etc/kvm', str(self.root / 'etc/kvm'))
        source = source.replace('/proc/mounts', str(self.root / 'mounts')).replace('/run', str(self.root / 'run'))
        source = source.replace('export PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'export PATH=' + str(self.root / 'bin') + ':/usr/bin:/bin')
        self.script = self.root / 'activate'
        self.script.write_text(source)

    def stub(self, name, body):
        f = self.root / 'bin' / name
        f.write_text('#!/bin/sh\n' + body + '\n')
        f.chmod(0o755)

    def run_activation(self, **env):
        import os
        return subprocess.run(['sh', str(self.script)], capture_output=True, text=True, env={**os.environ, **env})

    def files(self):
        return {p.name: p.read_bytes() for p in self.boot.iterdir()}

    def test_installs_keeps_previous_and_requests_reboot(self):
        result = self.run_activation()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.files(), {'fip.bin': NEW, 'fip.prev': OLD})
        self.assertIn('nanokvm-bootloader', (self.root / 'run/reboot-required.pkgs').read_text())

    def test_current_bootloader_is_left_alone(self):
        (self.boot / 'fip.bin').write_bytes(NEW)
        (self.boot / 'fip.new').write_bytes(b'leftover')
        result = self.run_activation()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.files(), {'fip.bin': NEW})
        self.assertFalse((self.root / 'run/reboot-required').exists())

    def test_first_install_without_a_bootloader(self):
        (self.boot / 'fip.bin').unlink()
        result = self.run_activation()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.files(), {'fip.bin': NEW})

    def test_replaces_an_older_previous_copy(self):
        (self.boot / 'fip.prev').write_bytes(b'older')
        self.assertEqual(self.run_activation().returncode, 0)
        self.assertEqual(self.files(), {'fip.bin': NEW, 'fip.prev': OLD})

    def test_bad_write_leaves_the_bootloader(self):
        result = self.run_activation(CORRUPT='fip.new')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('fip.bin is unchanged', result.stderr)
        self.assertEqual(self.files(), {'fip.bin': OLD})
        self.assertFalse((self.root / 'run/reboot-required').exists())

    def test_bad_final_readback_restores_previous(self):
        result = self.run_activation(CORRUPT='fip.bin')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.files(), {'fip.bin': OLD})
        self.assertIn('fip.bin is the previous one', result.stderr)
        self.assertFalse((self.root / 'run/reboot-required').exists())

    def test_bad_previous_copy_changes_nothing(self):
        result = self.run_activation(CORRUPT='fip.prev')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('fip.bin is unchanged', result.stderr)
        self.assertEqual(self.files(), {'fip.bin': OLD})

    def test_recovery_refuses(self):
        (self.boot / 'rec').write_text('')
        result = self.run_activation()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.files(), {'fip.bin': OLD, 'rec': b''})

    def test_opt_out(self):
        (self.root / 'etc/kvm/bootloader-update.disabled').write_text('')
        result = self.run_activation()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('skipped', result.stdout)
        self.assertEqual(self.files(), {'fip.bin': OLD})

    def test_missing_payload_fails(self):
        (self.payload / 'fip.bin').unlink()
        result = self.run_activation()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.files(), {'fip.bin': OLD})


if __name__ == '__main__':
    unittest.main()
