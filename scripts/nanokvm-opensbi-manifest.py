#!/usr/bin/env python3
"""Record built OpenSBI provenance and enforce the SG2002 split RO/RW memory contract."""
import argparse
import hashlib
import json
import struct
import subprocess
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
TEXT = 0x80000000
RO_SIZE = 0x40000
RW_SIZE = 0x10000
FDT_DEST = 0x80100000
STACK_SIZE = 8192
HEAP_SIZE = 0xa000


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for name in ("elf", "binary", "dtb", "config", "output"):
        p.add_argument("--" + name, type=Path, required=True)
    p.add_argument("--nm", required=True)
    p.add_argument("--compiler", required=True)
    p.add_argument("--fdtget", required=True)
    a = p.parse_args()
    config = dict(line.split("=", 1) for line in a.config.read_text().splitlines()
                  if line.startswith("CONFIG_") and "=" in line)
    require(config["CONFIG_DEFAULT_HART_STACK_SIZE"] == str(STACK_SIZE),
            "unexpected effective hart stack size")
    require(config.get("CONFIG_PLATFORM_GENERIC_SINGLE_FW_REGION") != "y",
            "single firmware PMP region is incompatible with this layout")
    def dtget(node, prop):
        return subprocess.check_output([a.fdtget, "-t", "s", str(a.dtb), node, prop],
                                       text=True).strip()
    heap = subprocess.check_output([a.fdtget, "-t", "x", str(a.dtb),
                                    "/chosen/opensbi-config", "heap-size"], text=True).strip()
    require(int(heap, 16) == HEAP_SIZE, "unexpected DT-selected heap size")
    require(dtget("/chosen/opensbi-config", "compatible") == "opensbi,config",
            "heap configuration would not be selected")
    cpus = subprocess.check_output([a.fdtget, "-l", str(a.dtb), "/cpus"], text=True).split()
    require(cpus == ["cpu@0"] and dtget("/cpus/cpu@0", "status") == "okay",
            "expected exactly one application hart")
    require("v" not in dtget("/cpus/cpu@0", "riscv,isa-extensions").split(),
            "C906 draft vector must not be advertised as standard V")
    symbols = {}
    for line in subprocess.check_output([a.nm, str(a.elf)], text=True).splitlines():
        fields = line.split()
        if len(fields) == 3:
            symbols[fields[2]] = int(fields[0], 16)
    raw = a.elf.read_bytes()
    require(len(raw) >= 64 and raw[:6] == b"\x7fELF\x02\x01", "ELF must be little-endian ELF64")
    require(struct.unpack_from("<H", raw, 18)[0] == 243, "ELF is not RISC-V")
    require(struct.unpack_from("<Q", raw, 24)[0] == TEXT, "incorrect entry point")
    require(struct.unpack_from("<I", raw, 48)[0] & 6 == 0, "firmware must use soft-float ABI")
    require(symbols["_fw_start"] == TEXT, "incorrect firmware start")
    # Match fw_base.S: one HART stack followed by the DT-selected aligned heap.
    runtime_end = symbols["_fw_end"] + STACK_SIZE + HEAP_SIZE
    extent = runtime_end - TEXT
    rw_start = symbols["_fw_rw_start"]
    ro_size = rw_start - TEXT
    require(ro_size == RO_SIZE and TEXT % ro_size == 0, "unexpected RO firmware PMP extent")
    rw_extent = runtime_end - rw_start
    require(rw_extent > 0, "empty RW firmware extent")
    rw_size = 1 << (rw_extent - 1).bit_length()
    require(rw_size == RW_SIZE and rw_start % rw_size == 0, "unexpected RW firmware PMP extent")
    regions = [{"base": TEXT, "bytes": ro_size, "m_permissions": "RX"},
               {"base": rw_start, "bytes": rw_size, "m_permissions": "RW"}]
    pmp_size = ro_size + rw_size
    require(a.binary.stat().st_size <= symbols["_fw_end"] - TEXT, "binary exceeds ELF firmware extent")
    dtb = a.dtb.read_bytes()
    require(struct.unpack_from(">I", dtb)[0] == 0xd00dfeed, "invalid DTB magic")
    require(struct.unpack_from(">I", dtb, 4)[0] == len(dtb), "invalid DTB size")
    source_offset = symbols["fw_fdt_bin"] - TEXT
    require(a.binary.read_bytes()[source_offset:source_offset + len(dtb)] == dtb,
            "embedded DTB differs from the compiled M-mode DT")
    # FDT fixups need spare space at the relocation destination, not in rodata.
    require(len(dtb) + 8192 <= 0x10000, "DTB plus fixup space exceeds relocation capacity")
    require(TEXT + pmp_size <= FDT_DEST, "FDT destination overlaps firmware PMP")
    require(FDT_DEST + 0x10000 < 0x80200000 - 32, "FDT destination overlaps U-Boot")
    pin = next(line.split() for line in (REPO / "platform/sources.lock").read_text().splitlines()
               if line.startswith("opensbi "))
    manifest = {
        "schema": 2, "version": "1.9", "upstream_url": pin[1],
        "upstream_commit": pin[2], "upstream_tree": pin[3].split("=", 1)[1],
        "source_date_epoch": 1782907200, "platform": "generic", "firmware": "FW_DYNAMIC",
        "isa": "rv64imac_zicsr_zifencei", "abi": "lp64", "optimization": "-O2", "lto": False,
        "compiler": subprocess.check_output([a.compiler, "--version"], text=True).splitlines()[0],
        "patches": {f.name: sha(f) for f in sorted((REPO / "platform/opensbi").glob("*.patch"))},
        "builder_sha256": sha(REPO / "platform/build.sh"),
        "manifest_builder_sha256": sha(REPO / "scripts/nanokvm-opensbi-manifest.py"),
        "effective_config_sha256": sha(a.config),
        "defconfig_sha256": sha(REPO / "platform/opensbi/defconfig"),
        "dts_sha256": sha(REPO / "platform/opensbi/sg2002.dts"),
        "binary_sha256": sha(a.binary), "binary_bytes": a.binary.stat().st_size,
        "dtb_sha256": sha(a.dtb), "dtb_bytes": len(dtb),
        "text_start": TEXT, "fw_end": symbols["_fw_end"], "rw_start": rw_start, "runtime_end": runtime_end,
        "pmp_regions": regions, "pmp_bytes": pmp_size, "hart_count": 1,
        "stack_bytes": STACK_SIZE, "heap_bytes": HEAP_SIZE,
        "fdt_source": symbols["fw_fdt_bin"], "fdt_destination": FDT_DEST,
        "fdt_max_bytes": 0x10000, "next_address": 0x80200000,
        "hardware_validation": "pending"
    }
    a.output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    print(f"OpenSBI 1.9: {a.binary.stat().st_size} file bytes, {extent} runtime bytes, RO/RW PMP {ro_size:#x}+{rw_size:#x}")


if __name__ == "__main__":
    main()
