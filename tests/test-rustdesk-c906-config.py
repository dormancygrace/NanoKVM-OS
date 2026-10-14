#!/usr/bin/env python3
"""The native RustDesk recipe builds the qualified C906 binary.

The recipe pins a published source archive, which may predate the repository's
build settings, so it passes nanokvm-rustdesk-c906.toml with `cargo --config`.
That file must equal the release profile of addons/rustdesk/Cargo.toml and the
target flags of addons/rustdesk/.cargo/config.toml, and the recipe must use it
for the native target and list it with a checksum.
"""
import hashlib
import re
import tomllib
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PKG = ROOT / "firmware/alpine/packages/nanokvm-rustdesk"
CRATE = ROOT / "addons/rustdesk"
NATIVE = "riscv64-alpine-linux-musl"
CROSS = "riscv64gc-unknown-linux-musl"


class RustDeskC906Config(unittest.TestCase):
    def setUp(self):
        self.recipe_config = tomllib.loads((PKG / "nanokvm-rustdesk-c906.toml").read_text())
        self.crate_config = tomllib.loads((CRATE / ".cargo/config.toml").read_text())
        self.manifest = tomllib.loads((CRATE / "Cargo.toml").read_text())
        self.recipe = (PKG / "APKBUILD").read_text()

    def test_profile_matches_the_manifest(self):
        self.assertEqual(self.recipe_config["profile"]["release"], self.manifest["profile"]["release"])

    def test_flags_match_the_crate_config(self):
        flags = self.recipe_config["target"][NATIVE]["rustflags"]
        self.assertEqual(flags, self.crate_config["target"][NATIVE]["rustflags"])
        self.assertEqual(flags, self.crate_config["target"][CROSS]["rustflags"])
        self.assertIn("+xtheadbb", " ".join(flags))

    def test_recipe_uses_and_checksums_the_file(self):
        self.assertRegex(self.recipe, r'(?m)^source=".*\$pkgname-c906\.toml"')
        build = re.search(r"(?ms)^build\(\) \{(.*?)^\}", self.recipe).group(1)
        self.assertIn('--config "$srcdir/$pkgname-c906.toml"', build)
        self.assertIn(f"--target {NATIVE}", build)
        package = re.search(r"(?ms)^package\(\) \{(.*?)^\}", self.recipe).group(1)
        self.assertIn(f"target/{NATIVE}/release/nanokvm-rustdesk", package)
        digest = hashlib.sha512((PKG / "nanokvm-rustdesk-c906.toml").read_bytes()).hexdigest()
        self.assertIn(f"{digest}  nanokvm-rustdesk-c906.toml", self.recipe)

    def test_builder_checksums_the_file(self):
        builder = (ROOT / "scripts/build-rustdesk-addon.py").read_text()
        self.assertIn('"nanokvm-rustdesk-c906.toml"', builder)


if __name__ == "__main__":
    unittest.main()
