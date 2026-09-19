#!/usr/bin/env python3
"""Build a manual-test FIT that boots a nested Alpine root on partition 2.

The installed /boot/boot.sd is never replaced. Load the generated FIT manually
from U-Boot over UART.
"""

from __future__ import annotations

import argparse
import gzip
from pathlib import Path
import shutil
import subprocess


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--release-root",
        type=Path,
        default=Path("/home/dgrace/.local/share/nkos-build/releases/beta14-seq30"),
    )
    parser.add_argument(
        "--f2fs-build",
        type=Path,
        default=Path("build/f2fs"),
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=Path("work/alpine/fit"),
    )
    parser.add_argument("--nested-root", default="mnt/alpine-test")
    args = parser.parse_args()

    args.output.mkdir(parents=True, exist_ok=True)
    base = args.f2fs_build.resolve()
    source_init = (base / "normal-initramfs/init").read_text()
    needle = "mount -t proc proc /realroot/proc"
    if source_init.count(needle) != 1:
        raise SystemExit(f"expected one init insertion point, found {source_init.count(needle)}")

    nested = args.nested_root.strip("/")
    alpine_boot = f"""if [ -f /realroot/{nested}/etc/alpine-release ]; then
    echo 'ALPINE_TEST: mounting Alpine root'
    mkdir -p /alpine-root
    mount -o bind /realroot/{nested} /alpine-root || exec busybox sh
    mount -t proc proc /alpine-root/proc || exec busybox sh
    mount -t sysfs sysfs /alpine-root/sys || exec busybox sh
    mount -t devtmpfs devtmpfs /alpine-root/dev || exec busybox sh
    echo 'ALPINE_TEST: starting Alpine PID 1'
    exec switch_root /alpine-root /sbin/init
fi

"""
    init_path = args.output / "init"
    init_path.write_text(source_init.replace(needle, alpine_boot + needle))
    init_path.chmod(0o755)

    listing = (base / "normal-initramfs.cpio.list").read_text()
    old_init = str(base / "normal-initramfs/init")
    if listing.count(old_init) != 1:
        raise SystemExit("unexpected initramfs listing")
    (args.output / "initramfs.list").write_text(
        listing.replace(old_init, str(init_path.resolve()))
    )

    gen_cpio = base / "kernel-output/usr/gen_init_cpio"
    cpio = args.output / "initramfs.cpio"
    with cpio.open("wb") as stream:
        subprocess.run(
            [str(gen_cpio), "-t", "0", str(args.output / "initramfs.list")],
            stdout=stream,
            check=True,
        )
    (args.output / "initramfs.cpio.gz").write_bytes(
        gzip.compress(cpio.read_bytes(), mtime=0)
    )

    # Release artifacts keep board FIT inputs under boot/<board>.  The old
    # boot-pcie-f2fs-root name belongs to the repository build tree and is not
    # present below a release root.
    source_fit = base / "boot" / "pcie"
    if not source_fit.is_dir():
        raise SystemExit(f"missing release FIT directory: {source_fit}")
    for name in ("Image.zst", "board.dtb", "boot.its"):
        shutil.copyfile(source_fit / name, args.output / name)
    mkimage = Path(
        "/home/dgrace/.local/share/nkos-build/buildroot-output/host/bin/mkimage"
    )
    subprocess.run(
        [str(mkimage), "-f", "boot.its", "boot-alpine-test.sd"],
        cwd=args.output,
        check=True,
    )
    print(args.output / "boot-alpine-test.sd")


if __name__ == "__main__":
    main()
