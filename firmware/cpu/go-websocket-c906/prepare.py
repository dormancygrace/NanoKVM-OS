#!/usr/bin/env python3
"""Stage the pinned Gorilla masking patch and a relocatable server module."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

HERE = Path(__file__).resolve().parent
MODULE = "github.com/gorilla/websocket"
VERSION = "v1.5.3"
PINS = {
    "mask.go": "964df1f35553a0893ae496fc41da0020583af35c3a3d21c6d94f6bc3f17bcc5c",
    "go.mod": "09dc8a9227a54c1069ea065d610a5f2ca195f1f5fba6e7dbb2d7eb0fd6e01df6",
}
TEST_PINS = {"client_server_test.go": "b58a89d3398023b2c025f86f9a27bc4d3b149bd846fe12f96be4ad028d2cb2e8"}
PATCH_FILES = ("mask_other.go", "mask_linux_riscv64.go", "mask_linux_riscv64.s")


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def stage_module(source, output, tests=False):
    source, output = Path(source).resolve(), Path(output).resolve()
    if output.exists() or output.is_relative_to(source) or source.is_relative_to(output):
        raise ValueError("Module output must be new and outside the module cache")
    for name, digest in (PINS | (TEST_PINS if tests else {})).items():
        if sha256(source / name) != digest:
            raise ValueError(f"{name} differs from Gorilla {VERSION}; review the patch before updating the pin")
    for name in PATCH_FILES:
        if (source / name).exists():
            raise ValueError(f"Upstream file conflicts with the patch: {name}")
    original = (source / "mask.go").read_text()
    declaration = "func maskBytes(key [4]byte, pos int, b []byte) int {"
    if original.count(declaration) != 1:
        raise ValueError("Expected one original maskBytes declaration")
    output.mkdir(parents=True)
    # Only the package root is needed, not examples, repository metadata or a
    # complete module-cache copy. Keep upstream tests when explicitly requested.
    for path in sorted(source.iterdir()):
        if path.is_file() and (path.name in ("go.mod", "go.sum", "LICENSE") or path.suffix in (".go", ".s")):
            if tests or not path.name.endswith("_test.go"):
                shutil.copyfile(path, output / path.name)
    (output / "mask.go").write_text(original.replace(declaration, declaration.replace("maskBytes", "maskBytesGeneric")))
    for name in PATCH_FILES:
        shutil.copyfile(HERE / name, output / name)
    if tests:
        # Go 1.27 closes CONNECT connections unless the handler hijacks them.
        # Preserve upstream assertions while replacing its old 200-only fixture.
        client_test = output / "client_server_test.go"
        fixture = client_test.read_text()
        old = '\t\t\t\tw.WriteHeader(http.StatusOK)\n\t\t\t\treturn'
        if fixture.count(old) != 2:
            raise ValueError("Expected both legacy CONNECT fixtures")
        client_test.write_text(fixture.replace(old,
            '\t\t\t\tc906ProxyTunnel(t, w, s.Server.Listener.Addr().String())\n\t\t\t\treturn'))
        for path in sorted((HERE / "tests").glob("*.go")):
            if (output / path.name).exists():
                raise ValueError(f"Test file conflicts with upstream: {path.name}")
            shutil.copyfile(path, output / path.name)
    return {path.name: sha256(path) for path in sorted(output.iterdir()) if path.is_file()}


def prepare(go, project, output, tests=False):
    project, output = Path(project).resolve(), Path(output).resolve()
    if output.exists() or project.is_relative_to(output) or output.is_relative_to(project):
        raise ValueError("Use a new build output directory outside the source project")
    output.mkdir(parents=True)
    # Build from a private module root so replacements can stay relative.
    # Absolute replacement paths are embedded in Go's build information even
    # with -trimpath and make identical builds differ by output directory.
    staged_project = output / "server"
    shutil.copytree(project, staged_project, symlinks=True,
                    ignore=shutil.ignore_patterns(".git", "__pycache__", "*.test"))
    # common's cgo directives use this sibling header tree in both normal
    # builds and teststub builds. Keep its relative layout in the private root.
    headers = Path("support/additional/kvm/include")
    shutil.copytree(project.parent / headers, output / headers, symlinks=True)
    modfile = staged_project / "go.mod"
    env = dict(os.environ, GOTOOLCHAIN="local", GOWORK="off")

    def run(*args):
        return subprocess.check_output([str(go), *args], cwd=staged_project, env=env, text=True)

    module = json.loads(run("mod", "edit", "-json", "-modfile=" + str(modfile)))
    if not any(x["Path"] == MODULE and x["Version"] == VERSION for x in module.get("Require", [])):
        raise ValueError(f"The server must require {MODULE} {VERSION}; review upgrades explicitly")
    # Existing local forks must remain inside the staged module. Reject new
    # external/absolute replacements rather than leaking host paths into builds.
    for replacement in module.get("Replace", []):
        old, new = replacement["Old"], replacement["New"]
        if old["Path"] == MODULE:
            raise ValueError("Unexpected pre-existing Gorilla replacement")
        if not new.get("Version"):
            path = Path(new["Path"])
            if path.is_absolute() or not (project / path).resolve().is_relative_to(project):
                raise ValueError("Local replacements must be relative and inside the server module")
    info = json.loads(run("mod", "download", "-modfile=" + str(modfile), "-json", MODULE))
    if info.get("Error") or info.get("Path") != MODULE or info.get("Version") != VERSION or not info.get("Sum"):
        raise ValueError(f"Could not obtain the checksum-verified pinned module: {info}")
    module_dir = output / "websocket"
    hashes = stage_module(info["Dir"], module_dir, tests)
    run("mod", "edit", "-modfile=" + str(modfile), "-replace=" + MODULE + "=../websocket")
    manifest = {
        "module": MODULE, "version": VERSION, "upstream_sum": info["Sum"],
        "upstream_pins": PINS, "files": hashes, "tests": tests,
        "server_mod_sha256": sha256(project / "go.mod"),
        "server_sum_sha256": sha256(project / "go.sum"),
    }
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return modfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", type=Path, required=True)
    parser.add_argument("--project", type=Path, default=HERE.parents[2] / "server")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--tests", action="store_true")
    args = parser.parse_args()
    try:
        print(prepare(args.go.absolute(), args.project, args.output, args.tests))
    except (ValueError, OSError) as error:
        parser.exit(1, f"{error}\n")


if __name__ == "__main__":
    main()
