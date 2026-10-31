#!/usr/bin/env python3
"""The -sg2002 packages become nanokvm-kernel and the others: real APK solving
and file ownership with the production recipe metadata (depends, replaces).
Payloads are fixtures; no script or service is executed.
"""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--apk", type=Path, required=True)
args = parser.parse_args()
apk = str(args.apk.resolve())
usermode = [] if os.geteuid() == 0 else ["--usermode"]
PARTS = ("kernel", "kmod", "firmware", "bootloader")


def run(*cmd, ok=True):
    p = subprocess.run([apk, *map(str, cmd)], text=True, capture_output=True)
    assert (p.returncode == 0) == ok, (cmd, p.stdout, p.stderr)
    return p.stdout


def metadata(name, version):
    """depends and replaces of a recipe, with every package at version."""
    recipe = REPO / "firmware/alpine/packages" / name / "APKBUILD"
    env = {"PATH": "/usr/bin:/bin", "PKGVER": version}
    env.update({"V_nanokvm_" + p: version for p in PARTS})
    p = subprocess.run(["sh", "-c", '. "$1"; printf "%s\n" "$depends" "$replaces"', "sh", str(recipe)],
                       env=env, text=True, capture_output=True, check=True)
    depends, replaces = p.stdout.splitlines()
    # Alpine and other NanoKVM packages are not part of this fixture.
    depends = " ".join(d for d in depends.split() if d.startswith("nanokvm-") and
                       d.split(">")[0].split("=")[0].removesuffix("-sg2002").removeprefix("nanokvm-") in PARTS)
    return depends, replaces


with tempfile.TemporaryDirectory(prefix="nkos-sg2002-rename-") as directory:
    work = Path(directory)
    root = work / "root"
    old, new = work / "old", work / "new"

    def package(repo, name, version, files, depends="", replaces=""):
        payload = work / f"payload-{name}-{version}"
        payload.mkdir()
        for path, text in files.items():
            (payload / path).parent.mkdir(parents=True, exist_ok=True)
            (payload / path).write_text(text)
        cmd = ["mkpkg", "--files", payload, "--output", repo / f"{name}-{version}.apk",
               "--info", "name:" + name, "--info", "version:" + version, "--info", "arch:riscv64"]
        for field, value in (("depends", depends), ("replaces", replaces)):
            if value:
                cmd += ["--info", field + ":" + value]
        run(*cmd)

    files = {p: {f"usr/lib/nanokvm/{p}/payload": p} for p in PARTS}
    # 3.6.0: the -sg2002 packages carry the files; the release requires them.
    old.mkdir()
    for p in PARTS:
        depends = "nanokvm-kmod-sg2002=3.6.0-r0" if p == "kernel" else ""
        package(old, f"nanokvm-{p}-sg2002", "3.6.0-r0", {k: v + " old" for k, v in files[p].items()}, depends)
    package(old, "nanokvm-release", "3.6.0-r0", {}, " ".join(f"nanokvm-{p}-sg2002>=3.6.0-r0" for p in PARTS))
    # 3.7.0: the production recipes of the new packages and the metapackages.
    new.mkdir()
    for p in PARTS:
        depends, replaces = metadata(f"nanokvm-{p}", "3.7.0")
        assert replaces == f"nanokvm-{p}-sg2002", replaces
        package(new, f"nanokvm-{p}", "3.7.0-r0", {k: v + " new" for k, v in files[p].items()}, depends, replaces)
        depends, replaces = metadata(f"nanokvm-{p}-sg2002", "3.7.0")
        assert depends == f"nanokvm-{p}>=3.7.0-r0" and not replaces, (depends, replaces)
        package(new, f"nanokvm-{p}-sg2002", "3.7.0-r0", {}, depends)
    release, _ = metadata("nanokvm-release", "3.7.0")
    package(new, "nanokvm-release", "3.7.0-r0", {}, release)
    for repo in (old, new):
        run("--allow-untrusted", "mkndx", "--output", repo / "Packages.adb", *sorted(repo.glob("*.apk")))

    common = ["--root", root, "--allow-untrusted", "--arch", "riscv64", "--repositories-file=/dev/null", "--no-cache"]
    # Installations made by hand list the old names in their world.
    for world in ([], [f"nanokvm-{p}-sg2002" for p in PARTS]):
        subprocess.run(["rm", "-rf", str(root)], check=True)
        run(*common, "add", "--initdb", *usermode, "--no-scripts", "--repository", old / "Packages.adb",
            "nanokvm-release", *world)
        run(*common, "--repository", new / "Packages.adb", "upgrade", "--no-scripts")
        installed = set(run(*common, "info", "-v").split())
        for p in PARTS:
            assert f"nanokvm-{p}-3.7.0-r0" in installed and f"nanokvm-{p}-sg2002-3.7.0-r0" in installed, installed
            for path, text in files[p].items():
                assert (root / path).read_text() == text + " new", path
            owner = run(*common, "info", "--who-owns", "/" + next(iter(files[p])))
            assert f"nanokvm-{p}-3.7.0-r0" in owner, owner
print("PASS: -sg2002 packages hand their files to nanokvm-kernel, -kmod, -firmware and -bootloader "
      "(release only and old names in world)")
