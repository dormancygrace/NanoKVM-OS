#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Build private, namespaced probes using the prepared running-kernel tree."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("source", type=Path)
    p.add_argument("build", type=Path)
    p.add_argument("cross")
    p.add_argument("output", type=Path)
    a = p.parse_args()
    source, build = a.source.resolve(), a.build.resolve()
    if not (build / "Module.symvers").is_file():
        raise ValueError("fully built matching kernel required")
    output = a.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    here = Path(__file__).resolve().parent
    for name in ["protocol.h", "candidates.h", "candidates.S", "crc.c", "probe.c", "client.c"]:
        shutil.copy2(here / name, output / name)
    original = source / "lib/crypto/chacha20poly1305.c"
    code = original.read_text()
    exports = sorted(set(re.findall(r"EXPORT_SYMBOL\((\w+)\)", code)))
    if len(exports) != 6:
        raise ValueError(f"unexpected AEAD exports: {exports}")
    code = re.sub(r"^EXPORT_SYMBOL\(\w+\);\s*$", "", code, flags=re.M)
    code = re.sub(r"^MODULE_\w+\(.*\);\s*$", "", code, flags=re.M)
    defines = "\n".join(f"#define {n} kc_{n}" for n in exports)
    (output / "aead.c").write_text(defines + "\n#define chacha_crypt kc_candidate_chacha_crypt\n" + code)
    qualified = here.parent / "c906-vector-copy/screen_client.c"
    text = qualified.read_text()
    start = text.index("struct vector_state {")
    end = text.index("static void context_test(void)", start)
    (output / "vector_state_helpers.h").write_text(text[start:end])
    (output / "Makefile").write_text(
        "obj-m += c906_crypto_probe.o\n"
        "c906_crypto_probe-y := probe.o candidates.o crc.o aead.o\n"
        "ccflags-y += -O3 -fno-tree-vectorize -fno-tree-slp-vectorize\n")
    command = ["make", "-C", str(source), f"O={build}", f"M={output}", "ARCH=riscv",
               f"CROSS_COMPILE={a.cross}", f"CC={a.cross}gcc", "-j4", "modules"]
    with (output / "module-build.log").open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    command = [a.cross + "gcc", "-O2", "-march=rv64gc_xtheadvector", "-mtune=thead-c906",
               "-mabi=lp64d", "-fno-tree-vectorize", "-fno-tree-slp-vectorize", "-static",
               "-Wall", "-Wextra", "-Werror", str(output / "client.c"), "-o", str(output / "crypto-client")]
    with (output / "client-build.log").open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    manifest = {
        "source": str(source), "build": str(build), "cross": a.cross,
        "kcflags": os.environ.get("KCFLAGS", ""),
        "aead_sha256": hashlib.sha256(original.read_bytes()).hexdigest(),
        "context_helpers_sha256": hashlib.sha256(qualified.read_bytes()).hexdigest(),
        "artifacts": {n: hashlib.sha256((output / n).read_bytes()).hexdigest()
                      for n in ["c906_crypto_probe.ko", "crypto-client"]},
    }
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps(manifest, indent=2))

if __name__ == "__main__":
    main()
