#!/usr/bin/env python3
"""Resolve the platform package graph from a clean, checksum-pinned Buildroot."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import urllib.request

REPO = Path(__file__).resolve().parents[1]


def run(args, **kwargs):
    result = subprocess.run(args, capture_output=True, text=True, **kwargs)
    if result.returncode:
        raise RuntimeError(f"{args}:\n{result.stdout}\n{result.stderr}")
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path, help="Existing pinned Buildroot archive; otherwise download it")
    args = parser.parse_args()
    pin = next(line.split() for line in (REPO / "platform/sources.lock").read_text().splitlines()
               if line.startswith("buildroot "))
    _, url, digest = pin
    with tempfile.TemporaryDirectory(prefix="nkos-platform-config-") as directory:
        work = Path(directory)
        archive = args.archive
        if archive is None:
            archive = work / "buildroot.tar.xz"
            with urllib.request.urlopen(url, timeout=60) as response, archive.open("wb") as dest:
                while data := response.read(1024 * 1024):
                    dest.write(data)
        with archive.open("rb") as stream:
            assert hashlib.file_digest(stream, "sha256").hexdigest() == digest, "Buildroot archive checksum mismatch"
        source = work / "source"
        source.mkdir()
        # The pinned Buildroot source includes intentional absolute symlinks
        # in OS skeleton templates; use its ordinary source extraction path.
        run(["tar", "-xf", str(archive.resolve()), "-C", str(source)])
        buildroot, = source.iterdir()
        for patch in sorted((REPO / "firmware/buildroot/source-patches").glob("*.patch")):
            run(["patch", "--batch", "--forward", "-p1", "-i", str(patch)], cwd=buildroot)
        output = work / "output"
        env = dict(os.environ, PATH="/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
        make = ["make", "-s", "-C", str(buildroot), f"O={output}",
                f"BR2_EXTERNAL={REPO / 'firmware/buildroot'}"]
        run(make + ["nanokvm_platform_defconfig"], env=env)
        graph = json.loads(run(make + ["show-info"], env=env))
        target = {name for name, item in graph.items() if item["type"] == "target"}
        allowed = {"busybox", "e2fsprogs", "f2fs-tools", "gcc-final", "linux-headers", "lz4",
                   "musl", "musl-compat-headers", "skeleton", "skeleton-init-common",
                   "skeleton-init-none", "toolchain", "toolchain-buildroot", "util-linux"}
        assert target <= allowed, f"Unexpected target packages: {target - allowed}"
        assert {"busybox", "e2fsprogs", "f2fs-tools", "lz4", "musl", "util-linux"} <= target
        assert graph["host-gcc-final"]["version"] == "16.2.0"
        assert graph["host-binutils"]["version"].startswith("2.47")
        assert "host-gnutls" in graph["host-uboot-tools"]["dependencies"]
        assert "host-openssl" not in graph
        assert graph["linux-headers"]["version"] == "7.2.5"
        config = (output / ".config").read_text()
        for line in ["BR2_OPTIMIZE_2=y", "BR2_INIT_NONE=y", "BR2_TOOLCHAIN_BUILDROOT_MUSL=y",
                     "BR2_TOOLCHAIN_BUILDROOT_CXX=y", "BR2_GCC_ENABLE_OPENMP=y",
                     "BR2_ROOTFS_OVERLAY=\"\"", "BR2_ROOTFS_POST_BUILD_SCRIPT=\"\"",
                     "# BR2_TARGET_ROOTFS_TAR is not set"]:
            assert line in config.splitlines(), f"Missing platform setting: {line}"
        flags = run(make + ["printvars", "VARS=TARGET_CFLAGS GCC_COMMON_TARGET_CFLAGS GCC_COMMON_TARGET_CXXFLAGS"], env=env)
        assert "-O2" in flags and "-mno-fence-tso" in flags and "-mtune=thead-c906" in flags
        print(f"Clean patches and Kconfig pass: {len(target)} target and {len(graph) - len(target)} host/virtual entries")
        print("Target packages: " + ", ".join(sorted(target)))
        print("GCC 16.2 / binutils 2.47 / musl / C906 -O2 flags retained; no retired OS packages")


if __name__ == "__main__":
    main()
