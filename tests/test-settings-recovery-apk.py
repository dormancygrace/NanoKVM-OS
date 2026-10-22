#!/usr/bin/env python3
"""Real APK 3 solver: optional removal leaves the OS dependency closure intact."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--apk", type=Path, required=True)
apk = str(parser.parse_args().apk.resolve())
usermode = [] if os.geteuid() == 0 else ["--usermode"]
def run(*args):
    r = subprocess.run([apk, *map(str, args)], capture_output=True, text=True)
    if r.returncode:
        raise RuntimeError(r.stdout + r.stderr)
    return r.stdout
with tempfile.TemporaryDirectory(prefix="nkos-recovery-apk-") as tmp:
    work = Path(tmp)
    root, empty = work / "root", work / "empty"
    root.mkdir(); empty.mkdir()
    packages = {"busybox": "", "nanokvm": "busybox", "curl": "", "htop": "busybox", "nanokvm-rustdesk": "nanokvm", "nanokvm-release": "nanokvm curl"}
    for name, deps in packages.items():
        args = ["mkpkg", "--files", empty, "--output", work / f"{name}-1-r0.apk", "--info", f"name:{name}", "--info", "version:1-r0", "--info", "arch:riscv64"]
        if deps: args += ["--info", f"depends:{deps}"]
        run(*args)
    run("mkndx", "--allow-untrusted", "--output", work / "Packages.adb", *work.glob("*.apk"))
    (root / "etc/apk").mkdir(parents=True)
    (root / "etc/apk/repositories").write_text(str(work / "Packages.adb") + "\n")
    base = ["--root", root, "--arch", "riscv64", "--allow-untrusted"]
    run(*base, "add", "--initdb", *usermode, "--no-scripts", "nanokvm-release=1-r0", "curl", "htop", "nanokvm-rustdesk")
    # This is the exact deletion policy: no --recursive or --force. curl is an
    # optional world root but required by the release, so its binary must stay.
    run(*base, "del", "--no-network", "--no-progress", "--no-scripts", "curl", "htop", "nanokvm-rustdesk")
    assert (root / "etc/apk/world").read_text().splitlines() == ["nanokvm-release=1-r0"]
    installed = set(run(*base, "info").splitlines())
    assert installed == {"nanokvm-release", "nanokvm", "busybox", "curl"}, installed
print("PASS: optional APK roots removed offline; core and shared dependencies retained")
