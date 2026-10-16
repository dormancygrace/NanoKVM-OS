#!/usr/bin/env python3
"""Stock packages refuse C906 metadata; migration respects APK failures/holds."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class StockProfileTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_package_builder_rejects_mislabeled_release(self):
        payloads = self.root / "payloads"
        for name in ("base", "kernel-sg2002", "kmod-sg2002", "firmware-sg2002", "app", "web", "release/etc"):
            (payloads / name).mkdir(parents=True)
        (payloads / "release/etc/nanokvm-build-profile").write_text("c906-scalar\n")
        (payloads / "release/etc/nanokvm-release").write_text('BUILD_PROFILE="c906-scalar"\n')
        env = dict(os.environ, PAYLOAD_ROOT=str(payloads), REPODEST=str(self.root / "repo"),
                   SRCDEST=str(self.root / "sources"), ABUILD="true")
        result = subprocess.run(["sh", str(ROOT / "scripts/build-alpine-packages.sh")],
                                env=env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release metadata does not match stock", result.stderr)

    def migration(self, mode, fail=False, custom=False, fix_fail=False, checksum_hold=False, v3=False):
        index = "/riscv64/Packages.adb" if v3 else ""
        for name, content in {
            "etc/alpine-release": "3.24.2\n",
            "etc/nanokvm-build-profile": "c906-scalar\n",
            "etc/nanokvm-release": 'VERSION="2.0-b12"\nBUILD_PROFILE="c906-scalar"\n',
            "etc/apk/repositories": f"https://nkos.pesin.pro/repos/c906-qualified{index}\nhttps://nkos.pesin.pro/repos/nanokvm{index}\nhttps://dl-cdn.alpinelinux.org/alpine/v3.24/main\n@edgecommunity https://dl-cdn.alpinelinux.org/alpine/edge/community\n" + ("https://custom.test/c906-scalar\n" if custom else ""),
            "etc/apk/world": "busybox\nnanokvm-kernel-sg2002" + ("><Q1test=\n" if checksum_hold else "=held-version\n") + "htop\n",
            "lib/apk/db/installed": "P:busybox\nV:1-r1\no:busybox\n\nP:libcrypto3\nV:3-r1\no:openssl\n\nP:htop\nV:3-r0\no:htop\n\nP:nanokvm-kernel-sg2002\nV:2-r0\no:nanokvm-kernel-sg2002\n\n",
        }.items():
            p = self.root / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(content)
        bindir = self.root / "bin"
        bindir.mkdir(exist_ok=True)
        for name, content in {
            "id": "#!/bin/sh\necho 0\n",
            "apk": '#!/bin/sh\nprintf "%s\\n" "$*" >> "$TEST_LOG"\ncase "$*" in *upgrade*) exit "$TEST_FAIL" ;; *fix*) exit "$TEST_FIX_FAIL" ;; esac\n',
        }.items():
            p = bindir / name
            p.write_text(content)
            p.chmod(0o755)
        # Redirect filesystem paths only in the test copy. No host or device writes.
        source = (ROOT / "scripts/migrate-alpine-stock.sh").read_text()
        source = source.replace("/etc/", str(self.root) + "/etc/").replace("/lib/apk/", str(self.root) + "/lib/apk/")
        env = dict(os.environ, PATH=str(bindir) + ":" + os.environ["PATH"],
                   TEST_LOG=str(self.root / "apk.log"), TEST_FAIL="1" if fail else "0", TEST_FIX_FAIL="1" if fix_fail else "0")
        return subprocess.run(["sh", "-s", "--", mode], input=source, text=True, capture_output=True, env=env)

    def test_migration_targets_subpackages_and_preserves_world(self):
        result = self.migration("--apply")
        self.assertEqual(result.returncode, 0, result.stderr)
        log = (self.root / "apk.log").read_text()
        self.assertIn("upgrade --available busybox libcrypto3", log)
        self.assertIn("fix --reinstall busybox libcrypto3", log)
        self.assertNotIn("htop", log)
        self.assertNotIn("kernel", log)
        self.assertEqual((self.root / "etc/nanokvm-build-profile").read_text(), "stock\n")
        self.assertEqual((self.root / "etc/nanokvm-release").read_text(),
                         'VERSION="2.0-b12"\nBUILD_PROFILE="c906-scalar"\n')
        self.assertIn("kernel-sg2002=held-version", (self.root / "etc/apk/world").read_text())
        repos = (self.root / "etc/apk/repositories").read_text()
        self.assertNotIn("c906", repos)
        self.assertIn("/repos/nanokvm", repos)
        self.assertIn("@edgecommunity", repos)

    def test_migration_removes_v3_c906_line(self):
        result = self.migration("--apply", v3=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        repos = (self.root / "etc/apk/repositories").read_text()
        self.assertNotIn("c906", repos)
        self.assertIn("https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb\n", repos)

    def test_simulate_leaves_profile_and_repositories_unchanged(self):
        result = self.migration("--simulate")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--simulate busybox libcrypto3", (self.root / "apk.log").read_text())
        self.assertEqual((self.root / "etc/nanokvm-build-profile").read_text(), "c906-scalar\n")
        self.assertIn("c906-qualified", (self.root / "etc/apk/repositories").read_text())

    def test_apk_failure_does_not_claim_stock(self):
        result = self.migration("--apply", fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / "etc/nanokvm-build-profile").read_text(), "c906-scalar\n")
        self.assertIn("c906-qualified", (self.root / "etc/apk/repositories").read_text())

    def test_reinstall_failure_does_not_claim_stock(self):
        result = self.migration("--apply", fix_fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / "etc/nanokvm-build-profile").read_text(), "c906-scalar\n")
        self.assertIn("c906-qualified", (self.root / "etc/apk/repositories").read_text())

    def test_checksum_hold_refuses_before_apk_can_remove_it(self):
        result = self.migration("--apply", checksum_hold=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum holds", result.stderr)
        self.assertFalse((self.root / "apk.log").exists())
        self.assertIn("><Q1test=", (self.root / "etc/apk/world").read_text())

    def test_custom_tuned_repository_requires_explicit_removal(self):
        result = self.migration("--apply", custom=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "apk.log").exists())


if __name__ == "__main__":
    unittest.main()
