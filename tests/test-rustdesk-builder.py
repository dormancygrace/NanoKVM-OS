#!/usr/bin/env python3
"""Exercise real package assembly with stub compilers/apk, without network."""
import hashlib
import json
import re
import runpy
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
PKG = Path("firmware/alpine/packages/nanokvm-rustdesk")
TARGET = "riscv64gc-unknown-linux-musl"


class RustDeskBuilderTests(unittest.TestCase):
    def build(self, version, revision=None):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "scripts").mkdir()
            builder = root / "scripts/build-rustdesk-addon.py"
            shutil.copyfile(ROOT / "scripts/build-rustdesk-addon.py", builder)
            for name in ("build-usb-audio.py", "nanokvm_cpu_profile.py"):
                (root / "scripts" / name).symlink_to(ROOT / "scripts" / name)
            for name in ("server", "firmware"):
                (root / name).symlink_to(ROOT / name, target_is_directory=True)
            crate = root / "addons/rustdesk"
            crate.mkdir(parents=True)
            for item in (ROOT / "addons/rustdesk").iterdir():
                if item.name == "Cargo.toml":
                    crate.joinpath(item.name).write_text(re.sub(
                        r'(?m)^version = ".*"$', 'version = "' + version + '"', item.read_text(), count=1))
                else:
                    crate.joinpath(item.name).symlink_to(item, target_is_directory=item.is_dir())
            source_recipe = (ROOT / PKG / "APKBUILD").read_bytes()
            output = root / "output"
            url = "https://example.invalid/nanokvm-rustdesk-" + version + "-source.tar.gz"
            captured = {}

            def run(command, **kwargs):
                if command[:2] == ["cargo", "build"]:
                    binary = Path(kwargs["env"]["CARGO_TARGET_DIR"]) / TARGET / "release/nanokvm-rustdesk"
                    binary.parent.mkdir(parents=True)
                    binary.write_bytes(b"stub compiler output")
                elif command[0] == "go":
                    self.assertIn(command[1:], (["mod", "tidy"], ["mod", "vendor"]))
                else:
                    self.assertEqual(command[1], "mkpkg")
                    payload = Path(command[command.index("--files") + 1])
                    captured["source"] = json.loads((payload / "usr/share/nanokvm-rustdesk/source.json").read_text())
                    captured["command"] = command
                    Path(command[command.index("--output") + 1]).write_bytes(b"stub apk output")
                return subprocess.CompletedProcess(command, 0)

            def check_output(command, **kwargs):
                if command[:2] == ["cargo", "vendor"]:
                    Path(command[-1]).mkdir()
                    return ""
                self.assertEqual(command, ["rustc", "-vV"])
                return "stub compiler version"

            args = [str(builder), "--linker", str(root / "cc"), "--apk", str(root / "apk"),
                    "--output", str(output), "--source-url", url]
            if revision is not None:
                args += ["--package-revision", str(revision)]
            with patch.object(sys, "argv", args), patch("subprocess.run", side_effect=run), \
                    patch("subprocess.check_output", side_effect=check_output), patch("os.chown"):
                runpy.run_path(str(builder), run_name="__main__")
            recipe = (output / "APKBUILD").read_text()
            archive = output / ("nanokvm-rustdesk-" + version + "-source.tar.gz")
            with tarfile.open(archive) as tar:
                embedded = tar.extractfile("nanokvm-rustdesk-" + version + "/packaging/APKBUILD.in").read().decode()
            self.assertIn("pkgver=" + version + "\n", recipe)
            self.assertIn("pkgver=" + version + "\n", embedded)
            self.assertIn("sourceurl=" + url + "\n", recipe)
            self.assertIn(hashlib.sha512(archive.read_bytes()).hexdigest() + "  " + archive.name, recipe)
            self.assertEqual(captured["source"]["sha256"], hashlib.sha256(archive.read_bytes()).hexdigest())
            self.assertEqual(captured["source"]["url"], url)
            self.assertEqual((ROOT / PKG / "APKBUILD").read_bytes(), source_recipe)
            return recipe, embedded, captured

    def test_new_source_version_starts_at_revision_zero(self):
        recipe, embedded, captured = self.build("9.9.9")
        self.assertIn("pkgrel=0\n", recipe)
        self.assertIn("pkgrel=0\n", embedded)
        self.assertEqual(captured["source"]["package_version"], "9.9.9-r0")
        self.assertIn("version:9.9.9-r0", captured["command"])

    def test_explicit_revision_for_new_version(self):
        recipe, embedded, captured = self.build("9.9.9", 2)
        self.assertIn("pkgrel=2\n", recipe)
        self.assertIn("pkgrel=2\n", embedded)
        self.assertEqual(captured["source"]["package_version"], "9.9.9-r2")

    def test_negative_revision_is_rejected(self):
        with self.assertRaises(SystemExit) as caught:
            self.build("9.9.9", -1)
        self.assertEqual(caught.exception.code, 2)

    def test_same_version_retains_recipe_revision(self):
        original = (ROOT / PKG / "APKBUILD").read_text()
        version = re.search(r"(?m)^pkgver=(.*)$", original)[1]
        revision = re.search(r"(?m)^pkgrel=(.*)$", original)[1]
        recipe, embedded, captured = self.build(version)
        self.assertIn("pkgrel=" + revision + "\n", recipe)
        self.assertEqual(captured["source"]["package_version"], version + "-r" + revision)


if __name__ == "__main__":
    unittest.main()
