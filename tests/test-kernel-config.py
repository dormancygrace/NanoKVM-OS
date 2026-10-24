#!/usr/bin/env python3
"""Exercise the production config gate with a prepared Linux tree and cross SDK.

Example: test-kernel-config.py --source OUT/kernel/src --cross OUT/buildroot-output/host/bin/riscv64-buildroot-linux-musl-
Only a temporary output directory is written; the supplied source/SDK are reused.
"""
import argparse
import os
import re
from pathlib import Path
import shlex
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--source", type=Path, required=True)
parser.add_argument("--cross", required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
build = (repo / "platform/build.sh").read_text()
start = build.index('    cp "$here/kernel/config"')
end = build.index('    grep -qx "CONFIG_LTO_NONE=y"', start)
command = "set -euo pipefail\nf() {\n" + build[start:end] + "\n}; f\n"
original = (repo / "platform/kernel/config").read_text()
real_cross = str(Path(args.cross).absolute())

with tempfile.TemporaryDirectory(prefix="nkos-kernel-config-") as directory:
    root = Path(directory)
    (root / "platform/kernel").mkdir(parents=True)
    (root / "kernel/build").mkdir(parents=True)
    proxy = root / "bin"
    proxy.mkdir()
    # Retain the actual assembler/linker and compiler behavior. Only --version
    # is varied to model a Buildroot patch release without rebuilding the SDK.
    prefix = Path(real_cross).name
    for tool in Path(real_cross).parent.glob(prefix + "*"):
        if tool.name != prefix + "gcc":
            (proxy / tool.name).symlink_to(tool)
    gcc = proxy / (prefix + "gcc")
    gcc.write_text("#!/bin/sh\nif [ \"$1\" = --version ]; then\n"
                   "  printf '%s\\n' \"$TEST_CC_BANNER\"\n"
                   "else\n  exec " + shlex.quote(real_cross + "gcc") + " \"$@\"\nfi\n")
    gcc.chmod(0o755)
    banner = subprocess.check_output([real_cross + "gcc", "--version"], text=True).splitlines()[0]
    env = {**os.environ, "here": str(root / "platform"), "out": str(root),
           "kbuild": str(root / "kernel/build"), "ksrc": str(args.source.resolve()),
           "cross": str(proxy / prefix), "kernel_profile": "release",
           "kernel_timestamp": "2026-10-24 00:00:00 UTC", "isa": "", "LC_ALL": "C"}
    cases = [
        ("current compiler", banner, original, True),
        ("changed descriptive banner", banner + " (new Buildroot patch release)", original, True),
        ("numeric compiler drift rejected", banner, re.sub(r"(?m)^CONFIG_GCC_VERSION=.*$", "CONFIG_GCC_VERSION=1", original), False),
        ("kernel option drift rejected", banner, original.replace("CONFIG_64BIT=y", "# CONFIG_64BIT is not set"), False),
        ("empty compiler identity rejected", "", original, False),
    ]
    for name, identity, config, success in cases:
        (root / "platform/kernel/config").write_text(config)
        result = subprocess.run(["bash", "-c", command], env={**env, "TEST_CC_BANNER": identity},
                                capture_output=True, text=True)
        assert (result.returncode == 0) == success, (name, result.stdout, result.stderr)
        if success:
            actual = (root / "kernel/build/.config").read_text()
            assert f'CONFIG_CC_VERSION_TEXT="{identity}"' in actual
        elif "drift" in name:
            assert "@@" in result.stdout, (name, result.stdout, result.stderr)
        print(name + ": pass", flush=True)
