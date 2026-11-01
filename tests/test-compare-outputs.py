#!/usr/bin/env python3
"""Check scripts/compare-outputs.py."""

import hashlib
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[1]
SCRIPT = REPO / 'scripts/compare-outputs.py'


def sums(files):
    return ''.join(f'{hashlib.sha256(data).hexdigest()}  {name}\n' for name, data in sorted(files.items()))


class CompareOutputsTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)

    def tearDown(self):
        self.dir.cleanup()

    def run_compare(self, old, new, *args):
        a, b = self.root / 'old', self.root / 'new'
        a.write_text(sums(old))
        b.write_text(sums(new))
        return subprocess.run([sys.executable, str(SCRIPT), str(a), str(b), *args],
                              capture_output=True, text=True)

    def test_identical(self):
        files = {'Image': b'k', 'server/NanoKVM-Server': b's'}
        r = self.run_compare(files, files, '--check')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('All 2 outputs are identical', r.stdout)

    def test_changed_added_removed(self):
        old = {'Image': b'k', 'server/NanoKVM-Server': b's', 'tools/old': b'o'}
        new = {'Image': b'k', 'server/NanoKVM-Server': b's2', 'tools/new': b'n'}
        r = self.run_compare(old, new)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('1 changed, 1 added, 1 removed of 3 outputs', r.stdout)
        self.assertIn('~ server/NanoKVM-Server', r.stdout)
        self.assertIn('+ tools/new', r.stdout)
        self.assertIn('- tools/old', r.stdout)
        self.assertEqual(self.run_compare(old, new, '--check').returncode, 1)

    def test_sha256sum_formats(self):
        digest = hashlib.sha256(b'x').hexdigest()
        a = self.root / 'a'
        a.write_text(f'# comment\n\n{digest}  ./boot/x.sd\n')
        b = self.root / 'b'
        b.write_text(f'{digest} *boot/x.sd\n')
        r = subprocess.run([sys.executable, str(SCRIPT), str(a), str(b), '--check'], capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_empty_and_stdin_manifests_are_rejected(self):
        digest = hashlib.sha256(b'').hexdigest()
        for contents in ('', '# no files\n', f'{digest}  -\n'):
            with self.subTest(contents=contents):
                a = self.root / 'empty'
                a.write_text(contents)
                result = subprocess.run([sys.executable, str(SCRIPT), str(a), str(a), '--check'],
                                        capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn('identical', result.stdout)

    def test_bad_line(self):
        a = self.root / 'a'
        a.write_text('not a hash\n')
        r = subprocess.run([sys.executable, str(SCRIPT), str(a), str(a)], capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn('not a sha256sum line', r.stderr)


if __name__ == '__main__':
    unittest.main()
