#!/usr/bin/env python3
"""The helper disk: a readable FAT12 image (checked with mtools when present),
reproducible, with the script and the readme as the only files."""
import importlib.util
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

repo = Path(__file__).resolve().parents[1]
builder = repo / 'scripts/build-serial-console-disk.py'
script = repo / 'firmware/services/nanokvm-serial-console.sh'
spec = importlib.util.spec_from_file_location('disk', builder)
disk = importlib.util.module_from_spec(spec)
spec.loader.exec_module(disk)


def mtool(name):
    return shutil.which(name) or (f'/usr/bin/{name}' if Path(f'/usr/bin/{name}').exists() else None)


class DiskTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.image = self.tmp / 'helper.img'
        subprocess.run(['python3', str(builder), '--script', str(script), '--output', str(self.image)], check=True)

    def test_layout(self):
        data = self.image.read_bytes()
        self.assertEqual(len(data), 1474560)
        self.assertEqual(data[510:512], b'\x55\xAA')
        self.assertEqual(data[54:62], b'FAT12   ')
        self.assertEqual(data[43:54], b'NANOKVM    ')
        # both FATs equal, media descriptor first
        fat1 = data[512:512 + 9 * 512]
        self.assertEqual(fat1, data[512 + 9 * 512:512 + 18 * 512])
        self.assertEqual(fat1[:3], b'\xF0\xFF\xFF')
        # the script is the first file, stored from cluster 2 (sector 33)
        content = script.read_bytes()
        start = 33 * 512
        self.assertEqual(data[start:start + len(content)], content)

    def test_reproducible(self):
        again = self.tmp / 'again.img'
        subprocess.run(['python3', str(builder), '--script', str(script), '--output', str(again)], check=True)
        self.assertEqual(self.image.read_bytes(), again.read_bytes())

    def test_root_directory_entries(self):
        data = self.image.read_bytes()
        root = data[19 * 512:19 * 512 + 224 * 32]
        entries = [root[i:i + 32] for i in range(0, len(root), 32) if root[i] != 0]
        self.assertEqual([e[:11] for e in entries], [b'NANOKVM    ', b'SETUP   SH ', b'README  TXT'])
        self.assertEqual(entries[0][11], 0x08)
        for e in entries[1:]:
            self.assertEqual(e[11], 0x20)
            self.assertEqual(e[12], 0x18, 'shown in lower case')

    @unittest.skipUnless(mtool('mdir') and mtool('mtype'), 'mtools missing')
    def test_mtools_reads_it(self):
        listing = subprocess.run([mtool('mdir'), '-i', str(self.image), '::'], capture_output=True, text=True, check=True).stdout
        # mdir prints 8.3 names in columns: "setup    sh"
        self.assertRegex(listing.lower(), r'setup\s+sh\s')
        self.assertRegex(listing.lower(), r'readme\s+txt\s')
        self.assertIn('nanokvm', listing.lower())
        got = subprocess.run([mtool('mtype'), '-i', str(self.image), '::setup.sh'], capture_output=True, check=True).stdout
        self.assertEqual(got, script.read_bytes())
        got = subprocess.run([mtool('mtype'), '-i', str(self.image), '::readme.txt'], capture_output=True, check=True).stdout
        self.assertEqual(got.decode(), disk.README)

    def test_too_large_is_refused(self):
        with self.assertRaises(AssertionError):
            disk.build([('BIG.BIN', b'x' * (2848 * 512))])


if __name__ == '__main__':
    unittest.main()
