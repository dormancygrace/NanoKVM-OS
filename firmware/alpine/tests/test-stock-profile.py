#!/usr/bin/env python3
"""Stock builds work without C906 inputs; migration respects APK failures/holds."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import types
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("builder", ROOT / "scripts/serve-alpine-personal-builder.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class StockProfileTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_cli_defaults_stock_and_tuned_requires_opt_in(self):
        args = ["sh", str(ROOT / "scripts/build-alpine-personal-image.sh"),
                "--validate-only", "--base-rootfs", "base.tar.gz", "--base-sha256", "0" * 64,
                "--boot-fit", "boot.sd", "--boot-sha256", "1" * 64,
                "--nanokvm-repo", "https://example.test/nanokvm", "--repo-key", "test.pub",
                "--output", "output"]
        result = subprocess.run(args, capture_output=True, text=True, check=True)
        self.assertIn("profile=stock", result.stdout)
        self.assertNotIn("c906", result.stdout)
        result = subprocess.run(args + ["--profile", "c906-scalar"], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        result = subprocess.run(args + ["--profile", "c906-scalar", "--tuned-repo", "https://example.test/c906"], capture_output=True, text=True, check=True)
        self.assertIn("profile=c906-scalar", result.stdout)
        result = subprocess.run(args + ["--runtime-tuned-repo", "https://example.test/c906"], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)

    def test_package_builder_rejects_mislabeled_release(self):
        payloads = self.root / "payloads"
        for name in ("base", "kernel-sg2002", "kmod-sg2002", "firmware-sg2002", "app", "release/etc"):
            (payloads / name).mkdir(parents=True)
        (payloads / "release/etc/nanokvm-build-profile").write_text("c906-scalar\n")
        (payloads / "release/etc/nanokvm-release").write_text('BUILD_PROFILE="c906-scalar"\n')
        env = dict(os.environ, PAYLOAD_ROOT=str(payloads), REPODEST=str(self.root / "repo"),
                   SRCDEST=str(self.root / "sources"), ABUILD="true")
        result = subprocess.run(["sh", str(ROOT / "scripts/build-alpine-packages.sh")],
                                env=env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release metadata does not match stock", result.stderr)

    def config(self, tuned=False):
        for name in ("base", "boot", "qemu", "scripts/build-alpine-personal-image.sh",
                     "scripts/build-alpine-update-bundle.sh", "scripts/build-alpine-recovery-fit.py",
                     "firmware/alpine/recovery/init", "repo/riscv64/APKINDEX.tar.gz", "key.pub"):
            p = self.root / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text("PUBLIC KEY" if name == "key.pub" else "fixture")
        data = dict(project_root=str(self.root), base_rootfs="base", boot_fit="boot",
                    nanokvm_repo="repo", repo_keys=["key.pub"], qemu_static="qemu",
                    base_sha256=builder.sha256(self.root / "base"), boot_sha256=builder.sha256(self.root / "boot"))
        if tuned:
            data["tuned_repo"] = "repo"
        p = self.root / "config.json"
        p.write_text(json.dumps(data))
        return builder.BuilderConfig(p)

    def test_server_without_tuned_repo(self):
        config = self.config()
        self.assertIsNone(config.tuned_repo)
        with patch.object(builder, "repository_index_sha256", return_value="official-index"):
            self.assertEqual(len(config.build_id("stock", [])), 24)
            with self.assertRaisesRegex(ValueError, "not enabled"):
                config.build_id("c906-scalar", [])
        handler = object.__new__(builder.RequestHandler)
        handler.server = types.SimpleNamespace(config=config, local_url="http://127.0.0.1:8080")
        command = handler.build_command("stock", [], self.root / "output")
        self.assertNotIn("--tuned-repo", command)

    def test_tuned_still_available_explicitly(self):
        config = self.config(tuned=True)
        with patch.object(builder, "repository_index_sha256", return_value="official-index"):
            self.assertNotEqual(config.build_id("stock", []), config.build_id("c906-scalar", []))
        handler = object.__new__(builder.RequestHandler)
        handler.server = types.SimpleNamespace(config=config, local_url="http://127.0.0.1:8080")
        self.assertIn("--tuned-repo", handler.build_command("c906-scalar", [], self.root / "output"))

    def migration(self, mode, fail=False, custom=False, fix_fail=False, checksum_hold=False):
        for name, content in {
            "etc/alpine-release": "3.24.2\n",
            "etc/nanokvm-build-profile": "c906-scalar\n",
            "etc/nanokvm-release": 'VERSION="2.0-b12"\nBUILD_PROFILE="c906-scalar"\n',
            "etc/apk/repositories": "https://nkos.pesin.pro/repos/c906-qualified\nhttps://nkos.pesin.pro/repos/nanokvm\nhttps://dl-cdn.alpinelinux.org/alpine/v3.24/main\n@edgecommunity https://dl-cdn.alpinelinux.org/alpine/edge/community\n" + ("https://custom.test/c906-scalar\n" if custom else ""),
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
