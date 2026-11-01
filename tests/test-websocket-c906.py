#!/usr/bin/env python3
"""Exercise source pinning and isolated staging."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "firmware/cpu/go-websocket-c906"
spec = importlib.util.spec_from_file_location("websocket_prepare", SOURCE / "prepare.py")
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)


class StagingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.cache = self.root / "cache"
        self.cache.mkdir()
        (self.cache / "mask.go").write_bytes((SOURCE / "tests/upstream-mask.go.txt").read_bytes())
        (self.cache / "go.mod").write_text("module github.com/gorilla/websocket\n\ngo 1.12\n")
        (self.cache / "LICENSE").write_bytes((SOURCE / "LICENSE").read_bytes())
        (self.cache / "upstream_test.go").write_text("package websocket\n")
        self.output = self.root / "staged"

    def test_modified_mask_is_rejected_before_writing(self):
        p = self.cache / "mask.go"
        p.write_text(p.read_text().replace("return pos & 3", "return pos & 7"))
        with self.assertRaisesRegex(ValueError, "mask.go differs"):
            prepare.stage_module(self.cache, self.output)
        self.assertFalse(self.output.exists())

    def test_modified_module_is_rejected(self):
        (self.cache / "go.mod").write_text("module example.invalid/changed\n")
        with self.assertRaisesRegex(ValueError, "go.mod differs"):
            prepare.stage_module(self.cache, self.output)
        self.assertFalse(self.output.exists())

    def test_cache_and_existing_outputs_cannot_be_overwritten(self):
        for target in (self.cache, self.cache / "child", self.root):
            with self.assertRaisesRegex(ValueError, "new and outside"):
                prepare.stage_module(self.cache, target)


if __name__ == "__main__":
    unittest.main()
