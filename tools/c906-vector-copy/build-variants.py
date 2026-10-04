#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Build copy-only LMUL variants outside the source tree."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("kernel_source", type=Path)
parser.add_argument("kernel_build", type=Path)
parser.add_argument("cross_prefix")
parser.add_argument("output", type=Path)
args = parser.parse_args()
source = Path(__file__).resolve().parent
output = args.output.resolve()
if output.is_relative_to(source):
    parser.error("variant outputs must be outside this source directory")
output.mkdir(parents=True, exist_ok=False)
for lmul in (1, 2, 4, 8):
    destination = output / f"m{lmul}"
    destination.mkdir()
    for name in ("Makefile", "probe.c", "protocol.h",
                 "usercopy_xtheadvector.S", "memset_xtheadvector.S"):
        shutil.copyfile(source / name, destination / name)
    assembly = destination / "usercopy_xtheadvector.S"
    text = assembly.read_text()
    needle = "a3, a2, e8, m8"
    if text.count(needle) != 1:
        raise RuntimeError("unexpected copy assembly shape")
    assembly.write_text(text.replace(needle, f"a3, a2, e8, m{lmul}"))
    command = ["make", "-C", str(args.kernel_source.resolve()),
               "O=" + str(args.kernel_build.resolve()), "M=" + str(destination),
               "ARCH=riscv", "CROSS_COMPILE=" + args.cross_prefix,
               "CC=" + args.cross_prefix + "gcc",
               "KCFLAGS=" + os.environ.get("KCFLAGS", ""), "modules"]
    with (destination / "build.log").open("w") as stream:
        subprocess.run(command, stdout=stream, stderr=subprocess.STDOUT, check=True)
    module = destination / "c906_vector_probe.ko"
    metadata = {"copy_lmul": lmul, "fill_lmul": 8, "argv": command,
                "sha256": hashlib.sha256(module.read_bytes()).hexdigest()}
    (destination / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    print(f"m{lmul}: {metadata['sha256']}")
