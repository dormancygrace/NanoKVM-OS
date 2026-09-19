#!/usr/bin/env python3
"""Build the manual one-shot Alpine recovery FIT.

The FIT contains only the kernel, DTB and generated recovery initramfs. The
Alpine root and replacement boot FIT remain external payloads on p3 and are
verified by SHA256 values embedded in the initramfs /init.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess


HASH_RE = re.compile(r"^[0-9a-fA-F]{64}$")


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def rooted(repo: Path, path: Path) -> Path:
    return path if path.is_absolute() else repo / path


def replace_init_and_sources(listing: str, init_path: Path, build_root: Path) -> str:
    lines = listing.splitlines()
    init_indexes = [
        index
        for index, line in enumerate(lines)
        if line.startswith("file /init ")
    ]
    if len(init_indexes) != 1:
        raise ValueError(f"expected one file /init entry, found {len(init_indexes)}")

    # The checked-in generated list contains absolute paths from the original
    # build. Repoint all file sources to the selected build tree so the builder
    # remains relocatable while retaining the existing list topology.
    old_init_source = lines[init_indexes[0]].split()[2]
    old_build_root = Path(old_init_source).parent.parent
    old_prefix = str(old_build_root).rstrip("/") + "/"
    new_prefix = str(build_root).rstrip("/") + "/"
    rewritten: list[str] = []
    for index, line in enumerate(lines):
        if index == init_indexes[0]:
            parts = line.split()
            parts[2] = str(init_path)
            rewritten.append(" ".join(parts))
            continue
        if line.startswith("file "):
            parts = line.split()
            if len(parts) >= 3 and parts[2].startswith(old_prefix):
                parts[2] = new_prefix + parts[2][len(old_prefix) :]
                line = " ".join(parts)
        rewritten.append(line)

    if not any(line.startswith("slink /mv busybox ") for line in rewritten):
        rewritten.append("slink /mv busybox 777 0 0")
    if not any(line.startswith("slink /sha256sum busybox ") for line in rewritten):
        rewritten.append("slink /sha256sum busybox 777 0 0")
    return "\n".join(rewritten) + "\n"


def parse_hash(value: str, option: str) -> str:
    if not HASH_RE.fullmatch(value):
        raise argparse.ArgumentTypeError(f"{option} must be exactly 64 hexadecimal characters")
    return value.lower()


def main() -> None:
    repo = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser()
    parser.add_argument("--rootfs-sha256", required=True, type=lambda value: parse_hash(value, "--rootfs-sha256"))
    parser.add_argument("--boot-sha256", required=True, type=lambda value: parse_hash(value, "--boot-sha256"))
    parser.add_argument("--f2fs-build", type=Path, default=Path("build/f2fs"))
    parser.add_argument("--boot-dir", type=Path)
    parser.add_argument("--init-template", type=Path, default=Path("firmware/alpine/recovery/init"))
    parser.add_argument("--output", type=Path, default=Path("work/alpine/recovery"))
    args = parser.parse_args()

    build_root = rooted(repo, args.f2fs_build).resolve()
    boot_dir = rooted(repo, args.boot_dir).resolve() if args.boot_dir else build_root / "boot-pcie-installer"
    template_path = rooted(repo, args.init_template).resolve()
    output = rooted(repo, args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)

    required = [
        build_root / "installer-initramfs.cpio.list",
        build_root / "kernel-output/usr/gen_init_cpio",
        build_root / "installer-initramfs/busybox",
        boot_dir / "Image.zst",
        boot_dir / "board.dtb",
        boot_dir / "boot.its",
        build_root / "buildroot-output/host/bin/mkimage",
    ]
    missing = [str(path) for path in required if not path.is_file()]
    if not template_path.is_file():
        missing.append(str(template_path))
    if missing:
        raise SystemExit("missing recovery build input(s):\n" + "\n".join(missing))

    init_text = template_path.read_text(encoding="utf-8")
    init_text = init_text.replace("@ROOTFS_SHA256@", args.rootfs_sha256)
    init_text = init_text.replace("@BOOT_SHA256@", args.boot_sha256)
    if "@ROOTFS_SHA256@" in init_text or "@BOOT_SHA256@" in init_text:
        raise SystemExit("recovery init template has an unreplaced digest placeholder")
    generated_init = output / "init"
    generated_init.write_text(init_text, encoding="utf-8", newline="\n")
    generated_init.chmod(0o755)

    listing = (build_root / "installer-initramfs.cpio.list").read_text(encoding="utf-8")
    generated_listing = replace_init_and_sources(listing, generated_init, build_root)
    listing_path = output / "initramfs.list"
    listing_path.write_text(generated_listing, encoding="utf-8", newline="\n")

    cpio_path = output / "initramfs.cpio"
    gen_init_cpio = build_root / "kernel-output/usr/gen_init_cpio"
    with cpio_path.open("wb") as stream:
        subprocess.run(
            [str(gen_init_cpio), "-t", "0", str(listing_path)],
            stdout=stream,
            check=True,
        )

    gzip_path = output / "initramfs.cpio.gz"
    with gzip_path.open("wb") as stream:
        # GzipFile with an empty filename avoids embedding a host path and
        # keeps both the header mtime and generated bytes deterministic.
        with gzip.GzipFile(fileobj=stream, mode="wb", filename="", mtime=0) as compressor:
            compressor.write(cpio_path.read_bytes())

    for name in ("Image.zst", "board.dtb", "boot.its"):
        shutil.copyfile(boot_dir / name, output / name)
    mkimage = build_root / "buildroot-output/host/bin/mkimage"
    fit_path = output / "boot-alpine-recovery.sd"
    mkimage_env = os.environ.copy()
    mkimage_env["SOURCE_DATE_EPOCH"] = "0"
    subprocess.run(
        [str(mkimage), "-f", "boot.its", fit_path.name],
        cwd=output,
        env=mkimage_env,
        check=True,
    )

    manifest = {
        "format": 1,
        "rootfs_sha256": args.rootfs_sha256,
        "boot_sha256": args.boot_sha256,
        "boot_payload_sha256": args.boot_sha256,
        "initramfs_sha256": sha256(gzip_path),
        "fit_sha256": sha256(fit_path),
        "output": fit_path.name,
        "output_sha256": sha256(fit_path),
    }
    manifest_path = output / "manifest.json"
    manifest_path.write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
        newline="\n",
    )
    print(f"{fit_path} {manifest['output_sha256']}")
    print(f"{manifest_path}")


if __name__ == "__main__":
    main()
