#!/usr/bin/env python3
"""Exercise the production Go APK worker against disposable native repositories."""
import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--apk", type=Path, required=True)
parser.add_argument("--worker-test", type=Path, required=True)
args = parser.parse_args()
apk = str(args.apk.resolve())
worker = str(args.worker_test.resolve())


def run(*args):
    result = subprocess.run([apk, *map(str, args)], text=True, capture_output=True)
    assert result.returncode == 0, (args, result.stdout, result.stderr)
    return result.stdout


with tempfile.TemporaryDirectory(prefix="nkos-update-selections-") as directory:
    work = Path(directory)
    old, new = work / "old", work / "new"
    old.mkdir()
    new.mkdir()
    payload = work / "payload"
    payload.mkdir()
    for name, versions in {
        "fixture": ["1.0-r0", "1.0-r1", "1.1-r0", "2.0-r0"],
        "nanokvm-rustdesk": ["1.0-r0", "1.1-r0"],
        "nanokvm-private": ["1.0-r0"],
        "nanokvmish": ["1.0-r0", "1.1-r0"],
        "tailscale": ["1.0-r0", "1.1-r0"],
    }.items():
        for version in versions:
            repo = old if version == "1.0-r0" else new
            run("mkpkg", "--files", payload, "--output", repo / f"{name}-{version}.apk",
                "--info", f"name:{name}", "--info", f"version:{version}",
                "--info", "arch:noarch", "--info", "license:MIT")
    for repo in [old, new]:
        run("--allow-untrusted", "mkndx", "--output", repo / "Packages.adb", *repo.glob("*.apk"))
    cases = [
        ("unconstrained", "fixture", "2.0-r0"),
        ("upper-includes-latest", "fixture<3", "2.0-r0"),
        ("upper-intermediate", "fixture<2", "1.1-r0"),
        ("inclusive-upper", "fixture<=1.1-r0", "1.1-r0"),
        ("fuzzy-patch", "fixture~1.0", "1.0-r1"),
        ("exact", "fixture=1.0-r0", "1.0-r0"),
        ("lower", "fixture>=1.0-r0", "2.0-r0"),
        ("file-pins", None, None),
    ]
    for label, selection, target in cases:
        root = work / label
        (root / "etc/apk").mkdir(parents=True)
        repositories = root / "etc/apk/repositories"
        repositories.write_text(str(old / "Packages.adb") + "\n")
        common = ["--root", root, "--allow-untrusted"]
        if selection:
            run(*common, "add", "--initdb", "--usermode", "--no-scripts", selection)
            expected = {
                "upgrades": [] if target == "1.0-r0" else [f"fixture-1.0-r0 < {target}"],
                "installed": {"fixture": target},
                "world": [selection],
            }
        else:
            names = ["nanokvm-rustdesk", "nanokvm-private", "nanokvmish", "tailscale"]
            run(*common, "add", "--initdb", "--usermode", "--no-scripts", "--force-non-repository",
                *[old / f"{name}-1.0-r0.apk" for name in names])
            kept = [line for line in (root / "etc/apk/world").read_text().splitlines()
                    if line.startswith(("nanokvmish>", "tailscale>"))]
            expected = {
                "upgrades": ["nanokvm-rustdesk-1.0-r0 < 1.1-r0"],
                "installed": {name: "1.1-r0" if name == "nanokvm-rustdesk" else "1.0-r0" for name in names},
                "world": ["nanokvm-rustdesk", "nanokvm-private", *kept],
            }
        repositories.write_text(str(new / "Packages.adb") + "\n")
        wrapper = root / "apk-wrapper"
        prefix = shlex.join([apk, *map(str, common)])
        wrapper.write_text("#!/bin/sh\ncase \"$1\" in add|upgrade) exec " + prefix +
                           ' "$@" --no-scripts;; *) exec ' + prefix + ' "$@";; esac\n')
        wrapper.chmod(0o700)
        (root / "expected.json").write_text(json.dumps(expected))
        result = subprocess.run([worker, "-test.run=^TestAPKSelectionsIntegration$", "-test.v"],
                                env={**os.environ, "NKOS_APK_TEST_ROOT": str(root)},
                                text=True, capture_output=True)
        assert result.returncode == 0, (label, result.stdout, result.stderr)
        print(f"PASS {label}", flush=True)
