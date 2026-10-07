#!/usr/bin/env python3
"""nanokvm-keys post-install moves only the NanoKVM line to the v3 index."""
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "firmware/alpine/packages/nanokvm-keys/nanokvm-keys.post-install"
V3 = "https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb"
ALPINE = ("https://dl-cdn.alpinelinux.org/alpine/v3.24/main\n"
          "https://dl-cdn.alpinelinux.org/alpine/v3.24/community\n"
          "@edgecommunity https://dl-cdn.alpinelinux.org/alpine/edge/community\n")


class RepositorySwitchTests(unittest.TestCase):
    def run_script(self, content):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "repositories"
            if content is not None:
                path.write_text(content)
            # Redirect the file only in the test copy; no host writes.
            source = SCRIPT.read_text().replace("/etc/apk/repositories", str(path))
            result = subprocess.run(["sh", "-s"], input=source, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            left = sorted(p.name for p in Path(directory).iterdir())
            return (path.read_text() if path.exists() else None), left

    def test_v2_line_moves_to_v3(self):
        text, files = self.run_script("https://nkos.pesin.pro/repos/nanokvm\n" + ALPINE)
        self.assertEqual(text, V3 + "\n" + ALPINE)
        self.assertEqual(files, ["repositories"])

    def test_trailing_slash_and_spaces(self):
        text, _ = self.run_script("  https://nkos.pesin.pro/repos/nanokvm/ \n" + ALPINE)
        self.assertEqual(text, V3 + "\n" + ALPINE)

    def test_other_lines_unchanged(self):
        original = ("https://nkos.pesin.pro/repos/c906-qualified\n"
                    "@nk https://nkos.pesin.pro/repos/nanokvm\n"
                    "# https://nkos.pesin.pro/repos/nanokvm\n"
                    "https://nkos.pesin.pro/repos/nanokvm-test\n" + ALPINE)
        text, _ = self.run_script(original)
        self.assertEqual(text, original)

    def test_already_v3_unchanged(self):
        text, _ = self.run_script(V3 + "\n" + ALPINE)
        self.assertEqual(text, V3 + "\n" + ALPINE)

    def test_missing_file(self):
        text, files = self.run_script(None)
        self.assertIsNone(text)
        self.assertEqual(files, [])


if __name__ == "__main__":
    unittest.main()
