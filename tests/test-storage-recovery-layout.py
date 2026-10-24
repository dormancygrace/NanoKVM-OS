#!/usr/bin/env python3
"""Both destructive-recovery entry points agree on old/new partition pairs."""
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
STAGER = (ROOT / 'firmware/alpine/compat/nanokvm-stage-update').read_text()
RECOVERY = (ROOT / 'firmware/alpine/recovery/init').read_text()


class LayoutTests(unittest.TestCase):
    def validate(self, start, data, size=1572864):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for part, field, value in [('mmcblk0p2', 'start', start), ('mmcblk0p2', 'size', size), ('mmcblk0p3', 'start', data)]:
                path = root / part / field
                path.parent.mkdir(exist_ok=True)
                path.write_text(str(value))
            functions = [
                STAGER[STAGER.index('sector_field() {'):STAGER.index('\n# The image recovery goes back')]
                + '\nmounted_as() { return 0; }\ncheck_recovery_layout\n',
                RECOVERY[RECOVERY.index('read_sector_field() {'):RECOVERY.index('verify_sha256() {')]
                + '\nvalidate_layout\n',
            ]
            result = []
            for function in functions:
                function = function.replace('/sys/class/block', str(root))
                script = 'SYS_BLOCK=' + str(root) + '\nBOOT_ROOT=/boot\nDATA_ROOT=/data\nfail() { exit 1; }\ndie() { exit 1; }\n' + function
                result.append(subprocess.run(['sh', '-c', script], capture_output=True).returncode == 0)
            return result

    def test_known_layouts(self):
        for root, data in [(131073, 1705984), (131073, 3180544), (139264, 1712128)]:
            with self.subTest(root=root, data=data):
                self.assertEqual(self.validate(root, data), [True, True])

    def test_mixed_unknown_or_overlapping_layouts_refused(self):
        for root, data in [(131073, 1712128), (139264, 1705984), (139264, 3180544),
                           (139265, 1712128), (139264, 1500000), ('', 1712128)]:
            with self.subTest(root=root, data=data):
                self.assertEqual(self.validate(root, data), [False, False])
        self.assertEqual(self.validate(139264, 1712128, 1572863), [False, False])


if __name__ == '__main__':
    unittest.main()
