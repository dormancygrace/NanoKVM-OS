#!/usr/bin/env python3
"""Exercise the real storage remount policy without touching host mounts."""
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SOURCE = (ROOT / 'firmware/alpine/openrc/nanokvm-storage').read_text()
FUNCTION = SOURCE[SOURCE.index('apply_mount_policy() {'):SOURCE.index('\nstart() {')]


class MountPolicyTests(unittest.TestCase):
    def run_policy(self, mounts, fail=False):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'mounts'
            path.write_text(mounts)
            script = FUNCTION.replace('/proc/mounts', str(path))
            script += '\nmount() { printf "mount %s\\n" "$*"; return ' + str(int(fail)) + '; }\n'
            script += 'ewarn() { printf "warning %s\\n" "$*"; }\napply_mount_policy\n'
            result = subprocess.run(['sh', '-c', script], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            return result.stdout.splitlines()

    def test_existing_card_gets_noatime_without_changing_discard_or_barriers(self):
        self.assertEqual(self.run_policy(
            '/dev/mmcblk0p2 / f2fs rw,relatime,discard,barrier,background_gc=off 0 0\n'
            '/dev/mmcblk0p1 /boot vfat rw,relatime 0 0\n'
            '/dev/mmcblk0p3 /data exfat rw,relatime 0 0\n'), [
                'mount -o remount,noatime,background_gc=off /',
                'mount -o remount,noatime /boot',
                'mount -o remount,noatime /data'])

    def test_already_applied_is_noop(self):
        self.assertEqual(self.run_policy(
            '/dev/mmcblk0p2 / f2fs rw,noatime,background_gc=off 0 0\n'
            '/dev/mmcblk0p1 /boot vfat rw,noatime 0 0\n'
            '/dev/mmcblk0p3 /data exfat rw,noatime 0 0\n'), [])

    def test_gc_still_disabled_when_noatime_was_already_set(self):
        self.assertEqual(self.run_policy('/dev/mmcblk0p2 / f2fs rw,noatime,background_gc=on 0 0\n'),
                         ['mount -o remount,noatime,background_gc=off /'])

    def test_foreign_mounts_and_unavailable_data_are_untouched(self):
        self.assertEqual(self.run_policy(
            'rootfs / ext4 rw,relatime 0 0\n'
            'nanokvm-data-unavailable /data tmpfs ro 0 0\n'
            'external /media/usb vfat rw,relatime 0 0\n'), [])

    def test_remount_failure_warns_and_continues(self):
        lines = self.run_policy('/dev/mmcblk0p2 / f2fs rw,relatime 0 0\n/dev/mmcblk0p1 /boot vfat rw,relatime 0 0\n', fail=True)
        self.assertEqual(sum(line.startswith('mount ') for line in lines), 2)
        self.assertEqual(sum(line.startswith('warning ') for line in lines), 2)


if __name__ == '__main__':
    unittest.main()
