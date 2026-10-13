#!/usr/bin/env python3
"""Check native add-on upgrades from pinned world entries in a temporary APK root."""
import argparse
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--apk", type=Path, required=True, help="native apk-tools 3 executable")
args = parser.parse_args()
apk = str(args.apk.resolve())

def run(command):
    result = subprocess.run([apk, *command], text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return result.stdout

with tempfile.TemporaryDirectory(prefix="nkos-addon-upgrade-") as directory:
    work = Path(directory)
    root, empty = work / "root", work / "empty"
    root.mkdir()
    empty.mkdir()
    for name, version in [
        ("nanokvm-rustdesk", "0.5.3-r2"), ("nanokvm-rustdesk", "0.5.3-r3"),
        ("keep-pinned", "1-r0"), ("keep-pinned", "2-r0"),
    ]:
        run(["mkpkg", "--files", str(empty),
             "--output", str(work / f"{name}-{version}.apk"),
             "--info", f"name:{name}", "--info", f"version:{version}",
             "--info", "arch:riscv64"])
    run(["mkndx", "--allow-untrusted", "--output", str(work / "Packages.adb"),
         *map(str, work.glob("*.apk"))])
    (root / "etc/apk").mkdir(parents=True)
    (root / "etc/apk/repositories").write_text(str(work / "Packages.adb") + "\n")
    base = ["--root", str(root), "--arch", "riscv64", "--allow-untrusted"]
    run([*base, "add", "--initdb", "--usermode", "--no-scripts",
         "nanokvm-rustdesk=0.5.3-r2", "keep-pinned=1-r0"])
    world = root / "etc/apk/world"
    before = world.read_text()
    run([*base, "upgrade", "--no-scripts", "nanokvm-rustdesk"])
    assert world.read_text() == before
    assert run([*base, "info", "-e", "-v", "nanokvm-rustdesk"]).strip() == "nanokvm-rustdesk-0.5.3-r2"
    run([*base, "add", "--no-scripts", "--upgrade", "nanokvm-rustdesk"])
    assert run([*base, "info", "-e", "-v", "nanokvm-rustdesk"]).strip() == "nanokvm-rustdesk-0.5.3-r3"
    assert run([*base, "info", "-e", "-v", "keep-pinned"]).strip() == "keep-pinned-1-r0"
    assert world.read_text().splitlines() == ["keep-pinned=1-r0", "nanokvm-rustdesk"]
    assert run(["version", "--test", "0.5.3-r2", "0.5.3-r3"]).strip() == "<"
print("PASS: requested add-on upgraded; unrelated package and its pin preserved")
