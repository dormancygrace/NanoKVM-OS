#!/usr/bin/env python3
"""nanokvm-keys moves only the official NanoKVM line, to the GitHub Pages repository."""
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
RECIPE = ROOT / "firmware/alpine/packages/nanokvm-keys"
SCRIPT = RECIPE / "nanokvm-keys.post-install"
V2 = "https://nkos.pesin.pro/repos/nanokvm"
V3 = "https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb"
NEW = "https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb"
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

    def test_v2_line_moves(self):
        text, files = self.run_script(V2 + "\n" + ALPINE)
        self.assertEqual(text, NEW + "\n" + ALPINE)
        self.assertEqual(files, ["repositories"])

    def test_v3_line_moves(self):
        text, _ = self.run_script(ALPINE + V3 + "\n")
        self.assertEqual(text, ALPINE + NEW + "\n")

    def test_trailing_slash_and_spaces(self):
        text, _ = self.run_script("  " + V2 + "/ \n" + ALPINE)
        self.assertEqual(text, NEW + "\n" + ALPINE)

    def test_one_new_line_for_several_official_ones(self):
        text, _ = self.run_script(V3 + "\n" + ALPINE + V2 + "\n" + NEW + "\n")
        self.assertEqual(text, NEW + "\n" + ALPINE)

    def test_other_lines_unchanged(self):
        original = ("https://nkos.pesin.pro/repos/c906-qualified\n"
                    "@nk https://nkos.pesin.pro/repos/nanokvm\n"
                    "# https://nkos.pesin.pro/repos/nanokvm\n"
                    "https://nkos.pesin.pro/repos/nanokvm-test\n" + ALPINE)
        text, _ = self.run_script(original)
        self.assertEqual(text, original)

    def test_already_moved_unchanged(self):
        text, _ = self.run_script(NEW + "\n" + ALPINE)
        self.assertEqual(text, NEW + "\n" + ALPINE)

    def test_missing_file(self):
        text, files = self.run_script(None)
        self.assertIsNone(text)
        self.assertEqual(files, [])

    def test_runs_after_install_and_upgrade(self):
        recipe = (RECIPE / "APKBUILD").read_text()
        self.assertIn('install="$pkgname.post-install $pkgname.post-upgrade"', recipe)
        self.assertEqual((RECIPE / "nanokvm-keys.post-upgrade").read_bytes(), SCRIPT.read_bytes())

    def test_one_repository_url_everywhere(self):
        self.assertIn(f"new={NEW}\n", SCRIPT.read_text())
        build = (ROOT / "platform/build.sh").read_text()
        self.assertIn(f"printf '%s\\n' {NEW} https://dl-cdn.alpinelinux.org/alpine/v3.24/main", build)
        pages = re.search(r"^PAGES_URL = '([^']+)'", (ROOT / "scripts/assemble-apk-repo.py").read_text(), re.M)[1]
        self.assertTrue(NEW.startswith(pages + "/alpine/v3.24/riscv64/"))
        release = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertIn(f"NANOKVM_PREVIOUS: {NEW.rsplit('/', 1)[0]}\n", release)


if __name__ == "__main__":
    unittest.main()
