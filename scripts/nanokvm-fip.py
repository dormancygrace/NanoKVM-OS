#!/usr/bin/env python3
"""Build/validate SG2002 FIP; independently check vendor format, CRCs and preservation."""
import argparse
import binascii
import hashlib
import json
import lzma
import struct
import subprocess
import sys
import tempfile
from pathlib import Path

ALIGN = 512
MONITOR_ADDR = 0x80000000
UBOOT_ADDR = 0x80200000
SMALL_CORE_ADDR = 0x8d000000
SMALL_CORE_SIZE = 0x200000
# Layout from pinned Sipeed fsbl/plat/cv181x/fiptool.py (BSD-3-Clause FSBL).
P1_FIELDS = [
    ("MAGIC1", 8), ("MAGIC2", 4), ("PARAM_CKSUM", 4), ("NAND_INFO", 128),
    ("NOR_INFO", 36), ("FIP_FLAGS", 8), ("CHIP_CONF_SIZE", 4),
    ("BLCP_IMG_CKSUM", 4), ("BLCP_IMG_SIZE", 4), ("BLCP_IMG_RUNADDR", 4),
    ("BLCP_PARAM_LOADADDR", 4), ("BLCP_PARAM_SIZE", 4), ("BL2_IMG_CKSUM", 4),
    ("BL2_IMG_SIZE", 4), ("BLD_IMG_SIZE", 4), ("PARAM2_LOADADDR", 4),
]
P2_FIELDS = [
    ("MAGIC1", 8), ("PARAM2_CKSUM", 4), ("RESERVED1", 4),
    ("DDR_PARAM_CKSUM", 4), ("DDR_PARAM_LOADADDR", 4), ("DDR_PARAM_SIZE", 4),
    ("DDR_PARAM_RESERVED", 4), ("BLCP_2ND_CKSUM", 4), ("BLCP_2ND_LOADADDR", 4),
    ("BLCP_2ND_SIZE", 4), ("BLCP_2ND_RUNADDR", 4), ("MONITOR_CKSUM", 4),
    ("MONITOR_LOADADDR", 4), ("MONITOR_SIZE", 4), ("MONITOR_RUNADDR", 4),
    ("LOADER_2ND_RESERVED0", 4), ("LOADER_2ND_LOADADDR", 4),
]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def crc(data):
    return 0xcafe0000 | binascii.crc_hqx(data, 0)


def require(condition, message):
    if not condition:
        raise ValueError(message)


def fields(data, layout):
    result, offset = {}, 0
    for name, size in layout:
        require(offset + size <= len(data), "truncated parameter block")
        result[name] = int.from_bytes(data[offset:offset + size], "little")
        offset += size
    return result


def inspect(data):
    require(len(data) >= 4096 and len(data) % ALIGN == 0, "invalid FIP size/alignment")
    require(data[:8] == b"CVBL01\n\0", "invalid PARAM1 magic")
    p1 = fields(data, P1_FIELDS)
    require(not (p1["FIP_FLAGS"] & 0x3c), "signed/encrypted FIP requires a separate signing flow")
    require(p1["BLD_IMG_SIZE"] == 0, "unsupported BLD component")
    require(p1["PARAM_CKSUM"] == crc(data[16:2048]), "PARAM1 CRC mismatch")
    p2addr = p1["PARAM2_LOADADDR"]
    require(p2addr % ALIGN == 0 and p2addr + 4096 <= len(data), "invalid PARAM2 offset")
    p2data = data[p2addr:p2addr + 4096]
    require(p2data[:8] == b"CVLD02\n\0", "invalid PARAM2 magic")
    p2 = fields(p2data, P2_FIELDS)
    require(p2["PARAM2_CKSUM"] == crc(p2data[12:]), "PARAM2 CRC mismatch")
    parts, spans = {}, [("PARAM1", 0, 4096), ("PARAM2", p2addr, p2addr + 4096)]

    def part(name, offset, size, checksum):
        require(offset % ALIGN == 0 and size % ALIGN == 0, name + " is misaligned")
        require(offset + size <= len(data), name + " is truncated")
        body = data[offset:offset + size]
        if size:
            require(checksum == crc(body), name + " CRC mismatch")
            spans.append((name, offset, offset + size))
        parts[name] = body

    pos = 4096
    for name in ("BLCP", "BL2"):
        size = p1[name + "_IMG_SIZE"]
        part(name, pos, size, p1[name + "_IMG_CKSUM"])
        pos += size
    for name in ("DDR_PARAM", "BLCP_2ND", "MONITOR"):
        part(name, p2[name + "_LOADADDR"], p2[name + "_SIZE"], p2[name + "_CKSUM"])
    lo = p2["LOADER_2ND_LOADADDR"]
    require(lo % ALIGN == 0 and lo + 32 <= len(data), "invalid LOADER_2ND offset")
    header = struct.unpack_from("<I4sIIQII", data, lo)
    _, magic, checksum, size, runaddr, _, _ = header
    require(magic in (b"BL33", b"B3MA", b"B3Z4"), "invalid LOADER_2ND magic")
    require(size >= 32 and lo + size <= len(data), "truncated LOADER_2ND")
    require(checksum == crc(data[lo + 12:lo + size]), "LOADER_2ND CRC mismatch")
    padded = (size + ALIGN - 1) // ALIGN * ALIGN
    require(lo + padded <= len(data), "truncated LOADER_2ND padding")
    parts["LOADER_2ND"] = data[lo:lo + size]
    spans.append(("LOADER_2ND", lo, lo + padded))
    for first, second in zip(sorted(spans, key=lambda v: v[1]), sorted(spans, key=lambda v: v[1])[1:]):
        require(first[2] <= second[1], first[0] + " overlaps " + second[0])
    require(not any(data[lo + size:lo + padded]), "nonzero loader padding")
    require(not any(data[lo + padded:]), "unrecognized nonzero FIP suffix")
    require(p2["MONITOR_RUNADDR"] == MONITOR_ADDR, "incorrect MONITOR run address")
    require(MONITOR_ADDR + len(parts["MONITOR"]) < UBOOT_ADDR - 32, "MONITOR overlaps U-Boot")
    require(runaddr + 32 == UBOOT_ADDR, "incorrect U-Boot run address")
    if parts["BLCP_2ND"]:
        require(p2["BLCP_2ND_RUNADDR"] == SMALL_CORE_ADDR, "unexpected small-core address")
        require(len(parts["BLCP_2ND"]) <= SMALL_CORE_SIZE, "small-core image exceeds reservation")
    return p1, p2, parts, spans


def validate(base, candidate, monitor, uboot, manifest):
    old1, old2, old, _ = inspect(base)
    new1, new2, new, spans = inspect(candidate)
    require(old1 == new1 and base[:old1["PARAM2_LOADADDR"]] == candidate[:new1["PARAM2_LOADADDR"]],
            "first-stage firmware or parameters changed")
    for name in ("BLCP", "BL2", "DDR_PARAM", "BLCP_2ND"):
        require(old[name] == new[name], name + " changed")
    for key in ("DDR_PARAM_CKSUM", "DDR_PARAM_LOADADDR", "DDR_PARAM_SIZE", "DDR_PARAM_RESERVED",
                "BLCP_2ND_CKSUM", "BLCP_2ND_LOADADDR", "BLCP_2ND_SIZE", "BLCP_2ND_RUNADDR"):
        require(old2[key] == new2[key], key + " changed")
    # The M-mode DT reserves no memory at SMALL_CORE_ADDR (platform/opensbi/sg2002.dts),
    # so a small-core image there would be overwritten by Linux.
    require(not new["BLCP_2ND"], "BLCP_2ND image requires a small-core reservation in the OpenSBI DT")
    padded_monitor = monitor + bytes((-len(monitor)) % ALIGN)
    require(new["MONITOR"] == padded_monitor, "packaged MONITOR differs")
    require(sha(monitor) == manifest["binary_sha256"], "OpenSBI manifest hash mismatch")
    require(manifest["pmp_regions"] == [
        {"base": MONITOR_ADDR, "bytes": 0x40000, "m_permissions": "RX"},
        {"base": MONITOR_ADDR + 0x40000, "bytes": 0x10000, "m_permissions": "RW"}],
        "OpenSBI does not match the split RO/RW PMP contract")
    require(manifest["pmp_bytes"] == 0x50000, "incorrect total PMP reservation")
    require(MONITOR_ADDR + len(monitor) <= manifest["fw_end"] <= manifest["runtime_end"] <= MONITOR_ADDR + 0x50000,
            "OpenSBI runtime exceeds PMP reservation")
    require(manifest["fdt_destination"] == 0x80100000 and manifest["fdt_max_bytes"] == 0x10000,
            "unexpected FDT relocation contract")
    loader = new["LOADER_2ND"]
    require(loader[4:8] == b"B3MA", "expected LZMA U-Boot")
    dec = lzma.LZMADecompressor(format=lzma.FORMAT_ALONE)
    require(dec.decompress(loader[32:], max_length=len(uboot) + 1) == uboot and dec.eof and not any(dec.unused_data),
            "packaged U-Boot differs or has extra compressed data")
    return {
        "schema": 2, "base_fip_sha256": sha(base), "fip_sha256": sha(candidate),
        "fip_bytes": len(candidate), "opensbi_version": manifest["version"],
        "opensbi_commit": manifest["upstream_commit"], "monitor_sha256": sha(monitor),
        "uboot_sha256": sha(uboot), "monitor_run_address": MONITOR_ADDR,
        "uboot_entry_address": UBOOT_ADDR, "pmp_bytes": manifest["pmp_bytes"],
        "pmp_regions": manifest["pmp_regions"],
        "preserved_components": ["BLCP", "BL2", "DDR_PARAM", "BLCP_2ND"],
        "components": {name: {"sha256": sha(new[name]), "bytes": len(new[name])}
                       for name in sorted(new)},
        "file_spans": [{"name": n, "offset": start, "end": end} for n, start, end in spans],
        "checks": ["parameter CRCs", "all image CRCs", "512-byte alignment", "no file overlaps",
                   "first-stage bytes", "DDR/small-core bytes", "no small-core image", "monitor bytes", "U-Boot decompression",
                   "load/run addresses", "320 KiB split RO/RW PMP contract"],
        "hardware_validation": "pending",
        "apk_updates_fip": False
    }


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("action", choices=("build", "verify"))
    for name in ("base", "monitor", "uboot", "opensbi-manifest", "output", "report"):
        p.add_argument("--" + name, type=Path, required=True)
    p.add_argument("--tool", type=Path)
    a = p.parse_args()
    manifest = json.loads(a.opensbi_manifest.read_text())
    if a.action == "build":
        require(a.tool is not None and a.tool.is_file(), "missing pinned FIP tool")
        # Verify original before the vendor tool reads it.
        inspect(a.base.read_bytes())
        with tempfile.TemporaryDirectory(prefix="nkos-fip-", dir=a.output.parent) as directory:
            raw = Path(directory) / "u-boot-raw.bin"
            raw.write_bytes(struct.pack("<I4sIIQII", 0, b"BL33", 0, 32 + a.uboot.stat().st_size,
                                        UBOOT_ADDR - 32, 0, 0) + a.uboot.read_bytes())
            result = subprocess.run([sys.executable, str(a.tool), "genfip", "--OLD_FIP", str(a.base),
                            "--MONITOR", str(a.monitor), "--MONITOR_RUNADDR", hex(MONITOR_ADDR),
                            "--LOADER_2ND", str(raw), "--compress", "lzma", str(a.output)],
                           capture_output=True)
            (a.output.parent / "fiptool.log").write_bytes(result.stdout + result.stderr)
            require(result.returncode == 0, "vendor FIP tool failed; see fiptool.log")
    report = validate(a.base.read_bytes(), a.output.read_bytes(), a.monitor.read_bytes(),
                      a.uboot.read_bytes(), manifest)
    a.report.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    print(f"FIP verified: {len(a.output.read_bytes())} bytes; OpenSBI {manifest['version']}; FSBL/DDR/small core preserved")


if __name__ == "__main__":
    main()
