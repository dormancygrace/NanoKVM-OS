#!/usr/bin/env python3
"""Exercise real APK solving/file ownership with production recipe metadata.
Payloads and external Alpine dependencies are fixtures; no service is executed.
"""
import argparse
from pathlib import Path
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--apk", type=Path, required=True)
args = parser.parse_args()
apk = str(args.apk.resolve())

def run(*cmd, ok=True):
    p = subprocess.run([apk, *map(str, cmd)], text=True, capture_output=True)
    assert (p.returncode == 0) == ok, (cmd, p.stdout, p.stderr)
    return p.stdout

def metadata(name):
    recipe = REPO / "firmware/alpine/packages" / name / "APKBUILD"
    p = subprocess.run(["sh", "-c",
        '. "$1"; printf "%s\n" "$pkgver-r$pkgrel" "$depends" "$provides" "$replaces"',
        "sh", str(recipe)], env={"PATH": "/usr/bin:/bin", "PKGVER": "3.0_alpha1"},
        text=True, capture_output=True, check=True)
    return p.stdout.splitlines()

with tempfile.TemporaryDirectory(prefix="nkos-split-app-") as directory:
    work = Path(directory)
    root = work / "root"
    repository = work / "repo"
    repository.mkdir()
    def package(name, version, files, depends="", provides="", replaces=""):
        payload = work / (name + "-" + version)
        payload.mkdir()
        for path, text in files.items():
            p = payload / path
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(text)
        cmd = ["mkpkg", "--files", payload, "--output", repository / (name + "-" + version + ".apk"),
               "--info", "name:" + name, "--info", "version:" + version, "--info", "arch:riscv64"]
        for field, value in [("depends", depends), ("provides", provides), ("replaces", replaces)]:
            if value:
                cmd += ["--info", field + ":" + value]
        run(*cmd)
    binary = "kvmapp/server/NanoKVM-Server"
    asset = "kvmapp/server/web/index.html"
    library = "kvmapp/server/dl_lib/libkvm.so"
    config = "kvmapp/kvm/fps"
    package("nanokvm-app", "3.0_alpha1-r1",
            {binary: "old server", asset: "old web", library: "old lib", config: "60"},
            provides="nanokvm-rustdesk-bridge=1")
    package("runtime-fixture", "1-r0", {}, provides="libgcc=99 libstdc++=99 nanokvm-base=3.0_alpha1-r1")
    package("rustdesk-fixture", "1-r0", {}, depends="nanokvm-rustdesk-bridge=1")
    def index():
        run("mkndx", "--allow-untrusted", "--output", repository / "Packages.adb",
            *sorted(repository.glob("*.apk")))
    index()
    (root / "etc/apk/protected_paths.d").mkdir(parents=True)
    (root / "etc/apk/protected_paths.d/nanokvm.list").write_text("+kvmapp/kvm\n")
    (root / "etc/apk/repositories").write_text(str(repository / "Packages.adb") + "\n")
    base = ["--root", root, "--arch", "riscv64", "--allow-untrusted"]
    run(*base, "add", "--initdb", "--usermode", "--no-scripts", "nanokvm-app", "runtime-fixture", "rustdesk-fixture")
    (root / config).write_text("75")
    for name, files in [
        ("nanokvm", {binary: "new server", library: "new lib", config: "60"}),
        ("nanokvm-web", {asset: "new web"}),
        ("nanokvm-app", {})
    ]:
        version, depends, provides, replaces = metadata(name)
        package(name, version, files, depends, provides, replaces)
    index()
    run(*base, "upgrade", "--no-scripts")
    assert run(*base, "info", "-e", "-v", "nanokvm-app").strip() == "nanokvm-app-3.0_alpha1-r2"
    assert run(*base, "info", "-e", "rustdesk-fixture")
    assert (root / binary).read_text() == "new server"
    assert (root / asset).read_text() == "new web"
    assert (root / library).read_text() == "new lib"
    assert (root / config).read_text() == "75", "user configuration must survive ownership transfer"
    for path, owner in [(binary, "nanokvm-3.0_alpha1-r0"), (asset, "nanokvm-web-3.0_alpha1-r0")]:
        assert owner in run(*base, "info", "--who-owns", "/" + path)
    # A compatible web revision must not upgrade or overwrite the backend.
    version, depends, provides, replaces = metadata("nanokvm-web")
    package("nanokvm-web", "3.0_alpha1-r1", {asset: "web-only fix"}, depends, provides, replaces)
    index()
    run(*base, "upgrade", "--no-scripts", "nanokvm-web")
    assert (root / asset).read_text() == "web-only fix"
    assert (root / binary).read_text() == "new server"
    assert run(*base, "info", "-e", "-v", "nanokvm").strip() == "nanokvm-3.0_alpha1-r0"
    # A fresh installation follows the same metapackage dependency graph.
    fresh = work / "fresh"
    (fresh / "etc/apk").mkdir(parents=True)
    (fresh / "etc/apk/repositories").write_text(str(repository / "Packages.adb") + "\n")
    freshbase = ["--root", fresh, "--arch", "riscv64", "--allow-untrusted"]
    run(*freshbase, "add", "--initdb", "--usermode", "--no-scripts", "nanokvm-app", "runtime-fixture", "rustdesk-fixture")
    assert (fresh / binary).read_text() == "new server"
    assert (fresh / asset).read_text() == "web-only fix"
    # An incompatible browser API must be rejected rather than silently installed.
    package("nanokvm-web", "3.0_alpha1-r2", {asset: "incompatible"}, "nanokvm-web-api=2", replaces=replaces)
    index()
    run(*base, "add", "--no-scripts", "nanokvm-web=3.0_alpha1-r2", ok=False)
    assert (root / asset).read_text() == "web-only fix"
print("PASS: migration, file ownership, protected settings, independent web upgrade and API rejection")
