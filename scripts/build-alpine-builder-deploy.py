#!/usr/bin/env python3
"""Package the minimal NanoKVM Alpine personal-image builder for a Linux VM."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import tarfile
import tempfile


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def resolve(repo: Path, path: Path) -> Path:
    return path.resolve() if path.is_absolute() else (repo / path).resolve()


def require_file(path: Path, label: str) -> Path:
    if not path.is_file():
        raise SystemExit(f"missing {label}: {path}")
    return path


def require_directory(path: Path, label: str) -> Path:
    if not path.is_dir():
        raise SystemExit(f"missing {label}: {path}")
    return path


def recovery_runtime(repo: Path, build: Path) -> list[Path]:
    listing = require_file(build / "installer-initramfs.cpio.list", "initramfs list")
    paths = {listing}
    for line in listing.read_text(encoding="utf-8").splitlines():
        fields = line.split()
        if fields and fields[0] == "file":
            source = require_file(Path(fields[2]), f"initramfs source for {fields[1]}")
            try:
                source.relative_to(repo)
            except ValueError as error:
                raise SystemExit(f"initramfs source is outside the checkout: {source}") from error
            paths.add(source)
    for relative in (
        "kernel-output/usr/gen_init_cpio",
        "boot-pcie-installer/Image.zst",
        "boot-pcie-installer/board.dtb",
        "boot-pcie-installer/boot.its",
        "buildroot-output/host/bin/mkimage",
    ):
        paths.add(require_file(build / relative, "recovery build input"))
    return sorted(paths)


def copy_file(source: Path, destination: Path) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, destination)


def manifest(root: Path) -> str:
    rows = []
    for path in sorted(item for item in root.rglob("*") if item.is_file()):
        rows.append(f"{sha256(path)}  {path.relative_to(root).as_posix()}")
    return "\n".join(rows) + "\n"


def normalized(info: tarfile.TarInfo) -> tarfile.TarInfo:
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    info.mtime = 0
    return info


def write_archive(source: Path, output: Path) -> None:
    with output.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w|") as archive:
                archive.add(source, arcname="nanokvm-builder", filter=normalized)


def main() -> None:
    repo = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-rootfs", type=Path, required=True)
    parser.add_argument("--boot-fit", type=Path, required=True)
    parser.add_argument("--nanokvm-repo", type=Path, required=True)
    parser.add_argument("--tuned-repo", type=Path, required=True)
    parser.add_argument("--repo-key", type=Path, required=True)
    parser.add_argument("--qemu-static", type=Path, required=True)
    parser.add_argument("--f2fs-build", type=Path, default=Path("build/f2fs"))
    parser.add_argument("--sample-package", default="nano")
    parser.add_argument(
        "--output",
        type=Path,
        default=Path("work/alpine/nanokvm-builder-vm-deploy.tar.gz"),
    )
    parser.add_argument("--force", action="store_true")
    args = parser.parse_args()

    inputs = {
        "base_rootfs": require_file(resolve(repo, args.base_rootfs), "base rootfs"),
        "boot_fit": require_file(resolve(repo, args.boot_fit), "normal FIT"),
        "repo_key": require_file(resolve(repo, args.repo_key), "repository public key"),
        "qemu": require_file(resolve(repo, args.qemu_static), "qemu-riscv64-static"),
    }
    nanokvm_repo = require_directory(resolve(repo, args.nanokvm_repo), "NanoKVM repository")
    tuned_repo = require_directory(resolve(repo, args.tuned_repo), "qualified C906 repository")
    f2fs_build = require_directory(resolve(repo, args.f2fs_build), "F2FS recovery build")
    output = resolve(repo, args.output)
    if output.exists() and not args.force:
        parser.error(f"output exists; pass --force to replace it: {output}")
    output.parent.mkdir(parents=True, exist_ok=True)

    key_text = inputs["repo_key"].read_text(encoding="ascii", errors="strict")
    if "PRIVATE KEY" in key_text or "PUBLIC KEY" not in key_text:
        parser.error("--repo-key must be a PEM public key; private keys are forbidden")

    with tempfile.TemporaryDirectory(prefix="nanokvm-builder-deploy-") as temp_name:
        bundle = Path(temp_name) / "nanokvm-builder"
        for name in (
            "build-alpine-personal-image.sh",
            "build-alpine-update-bundle.sh",
            "build-alpine-recovery-fit.py",
            "serve-alpine-personal-builder.py",
        ):
            copy_file(repo / "scripts" / name, bundle / "scripts" / name)
        copy_file(
            repo / "firmware/alpine/recovery/init",
            bundle / "firmware/alpine/recovery/init",
        )
        for source in recovery_runtime(repo, f2fs_build):
            copy_file(source, bundle / source.relative_to(repo))

        copy_file(inputs["base_rootfs"], bundle / "input" / inputs["base_rootfs"].name)
        copy_file(inputs["boot_fit"], bundle / "input/boot-alpine.sd")
        copy_file(inputs["repo_key"], bundle / "keys" / inputs["repo_key"].name)
        copy_file(inputs["qemu"], bundle / "qemu/qemu-riscv64-static")
        shutil.copytree(nanokvm_repo, bundle / "repos/nanokvm")
        shutil.copytree(tuned_repo, bundle / "repos/c906-qualified")
        (bundle / "requests").mkdir()
        (bundle / "requests/sample.packages").write_text(
            args.sample_package + "\n", encoding="utf-8"
        )

        base_hash = sha256(inputs["base_rootfs"])
        boot_hash = sha256(inputs["boot_fit"])
        key_name = inputs["repo_key"].name
        config = {
            "project_root": ".",
            "base_rootfs": f"input/{inputs['base_rootfs'].name}",
            "base_sha256": base_hash,
            "boot_fit": "input/boot-alpine.sd",
            "boot_sha256": boot_hash,
            "nanokvm_repo": "repos/nanokvm",
            "tuned_repo": "repos/c906-qualified",
            "repo_keys": [f"keys/{key_name}"],
            "qemu_static": "qemu/qemu-riscv64-static",
            "output_root": "output",
            "alpine_version": "3.24",
            "alpine_main": "https://dl-cdn.alpinelinux.org/alpine/v3.24/main",
            "alpine_community": "https://dl-cdn.alpinelinux.org/alpine/v3.24/community",
            "public_origin": "https://nkos.pesin.pro",
        }
        (bundle / "builder-config.json").write_text(
            json.dumps(config, indent=2, sort_keys=True) + "\n", encoding="utf-8"
        )
        (bundle / "DEPLOY.md").write_text(
            "# NanoKVM Alpine personal-image builder\n\n"
            "Prerequisites: root access, Python 3, tar, mount, device-tree-compiler, "
            "qemu-user-static and binfmt-support. On Ubuntu: `sudo apt-get install "
            "python3 qemu-user-static binfmt-support device-tree-compiler`. The "
            "archive supplies the exact qemu-riscv64-static binary used by the "
            "builder; binfmt must still be enabled for APK maintainer scripts.\n\n"
            "Start a temporary repository server from this directory:\n\n"
            "```sh\npython3 -m http.server 18080 --directory repos\n```\n\n"
            "In another shell, run the sample build:\n\n"
            "```sh\n"
            "sudo ./scripts/build-alpine-personal-image.sh \\\n"
            "  --profile c906-scalar \\\n"
            f"  --base-rootfs input/{inputs['base_rootfs'].name} \\\n"
            f"  --base-sha256 {base_hash} \\\n"
            "  --boot-fit input/boot-alpine.sd \\\n"
            f"  --boot-sha256 {boot_hash} \\\n"
            "  --tuned-repo http://127.0.0.1:18080/c906-qualified \\\n"
            "  --nanokvm-repo http://127.0.0.1:18080/nanokvm \\\n"
            f"  --repo-key keys/{key_name} \\\n"
            "  --packages-file requests/sample.packages \\\n"
            "  --qemu-static qemu/qemu-riscv64-static \\\n"
            "  --output output/sample\n"
            "```\n\n"
            "Or start the synchronous local API, which also serves both signed "
            "repositories and generated artifacts:\n\n"
            "```sh\n"
            "sudo ./scripts/serve-alpine-personal-builder.py \\\n"
            "  --config builder-config.json --listen 127.0.0.1 --port 8080\n"
            "curl -sS -X POST http://127.0.0.1:8080/v1/builds \\\n"
            "  -H 'Content-Type: application/json' \\\n"
            "  -d '{\"profile\":\"c906-scalar\",\"packages\":[\"nano\"]}'\n"
            "```\n\nPrivate signing keys are intentionally absent.\n",
            encoding="utf-8",
        )
        (bundle / "SHA256SUMS").write_text(manifest(bundle), encoding="utf-8")
        private_markers = (
            "-----BEGIN PRIVATE KEY-----",
            "-----BEGIN RSA PRIVATE KEY-----",
            "-----BEGIN OPENSSH PRIVATE KEY-----",
        )
        if any(
            marker in path.read_text(encoding="ascii", errors="ignore")
            for path in bundle.rglob("*")
            if path.is_file() and path.stat().st_size < 1024 * 1024
            for marker in private_markers
        ):
            raise SystemExit("private key material found in deployment bundle")
        write_archive(bundle, output)

    print(f"{sha256(output)}  {output}")


if __name__ == "__main__":
    main()
