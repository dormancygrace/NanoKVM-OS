#!/usr/bin/env python3
"""Check platform/build.sh verify and the toolchain input check without building."""

import hashlib
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[1]


def build(tree, out, *steps):
    return subprocess.run(["bash", str(tree / "platform/build.sh"), "-o", str(out), *steps],
                          capture_output=True, text=True)


def expect(result, success, name, text=None):
    assert (result.returncode == 0) == success, (name, result.stdout, result.stderr)
    if text is not None:
        assert text in result.stdout + result.stderr, (name, text, result.stdout, result.stderr)
    print(f"{name}: pass")


with tempfile.TemporaryDirectory(prefix="nkos-platform-") as directory:
    root = Path(directory)
    # A minimal checkout: build.sh and its lists, the release name and a
    # stand-in for firmware/buildroot.
    tree = root / "repo"
    shutil.copytree(REPO / "platform", tree / "platform",
                    ignore=shutil.ignore_patterns("*.patch", "*.bin", "sg2002-aes"))
    (tree / "firmware/alpine").mkdir(parents=True)
    shutil.copy(REPO / "firmware/alpine/release.env", tree / "firmware/alpine/release.env")
    defconfig = tree / "firmware/buildroot/configs/nanokvm_enhanced_defconfig"
    defconfig.parent.mkdir(parents=True)
    defconfig.write_text("BR2_riscv=y\n")
    out = root / "out"
    images = out / "images"

    outputs = {"Image": b"kernel", "u-boot.bin": b"loader", "boot/alpha.sd": b"fit"}
    for name, data in outputs.items():
        (images / name).parent.mkdir(parents=True, exist_ok=True)
        (images / name).write_bytes(data)
    (tree / "platform/expected.sha256").write_text(
        "# Test outputs\n\n" + "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n"
                                       for name, data in outputs.items()))
    expect(build(tree, out, "verify"), True, "complete matching outputs", "All 3 outputs match")
    expect(build(tree, out, "verify"), True, "verify ignores its own SHA256SUMS")

    (images / "boot/alpha.sd").write_bytes(b"changed")
    expect(build(tree, out, "verify"), False, "modified boot image fails")
    (images / "boot/alpha.sd").write_bytes(outputs["boot/alpha.sd"])
    (images / "u-boot.bin").unlink()
    expect(build(tree, out, "verify"), False, "missing U-Boot fails")
    (images / "u-boot.bin").write_bytes(outputs["u-boot.bin"])
    (images / "fip.bin").write_bytes(b"unverified")
    expect(build(tree, out, "verify"), False, "unlisted output fails", "fip.bin")
    (images / "fip.bin").unlink()
    expect(build(tree, out, "verify"), True, "restored outputs pass")

    # The toolchain is reused only if it was built from the current inputs.
    stamp = out / "buildroot-output/.platform-toolchain"
    stamp.parent.mkdir(parents=True)
    stamp.write_text("previous inputs\n")
    result = build(tree, out, "toolchain")
    expect(result, False, "toolchain from other inputs is rejected", "Delete")
    current = re.search(r"current:\s+([0-9a-f]{64})", result.stderr).group(1)
    stamp.write_text(current + "\n")
    expect(build(tree, out, "toolchain"), True, "unchanged inputs reuse the toolchain", "already built")
    defconfig.write_text("BR2_riscv=y\nBR2_CCACHE=y\n")
    expect(build(tree, out, "toolchain"), False, "changed Buildroot configuration is rejected", current)
    defconfig.write_text("BR2_riscv=y\n")
    lock = tree / "platform/sources.lock"
    original = lock.read_text()
    lock.write_text(re.sub(r"(?m)^(buildroot\s+\S+\s+)\S+", r"\g<1>" + "0" * 64, original))
    expect(build(tree, out, "toolchain"), False, "changed Buildroot archive pin is rejected")
    lock.write_text(original)
    expect(build(tree, out, "toolchain"), True, "restored inputs reuse the toolchain again")

    # An interrupted build from other inputs is not continued either.
    stamp.unlink()
    (out / "buildroot").mkdir()
    (out / "buildroot/.platform-patched").write_text("previous inputs\n")
    expect(build(tree, out, "toolchain"), False, "interrupted build from other inputs is rejected", "Delete")
