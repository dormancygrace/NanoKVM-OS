#!/usr/bin/env python3
"""Build/validate SG2002 FIP from built parts; independently check vendor format, CRCs and every part."""
import argparse
import binascii
import hashlib
import json
import os
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
# PARAM1 values of the SDK's fip.mk (fip-all) for an SD boot with no small
# core: no NAND, NOR info unset (all 0xff), an empty BLCP image at the SDK's
# run address. CHIP_CONF is a fixed 760-byte field followed by key and
# signature fields, all zero without secure boot.
NOR_INFO = bytes([0xff]) * 36
BLCP_IMG_RUNADDR = 0x05200200
CHIP_CONF_OFFSET = 232
CHIP_CONF_FIELD = 760
# Layout from pinned Sipeed fsbl/plat/cv181x/fiptool.py (GPL-2.0 FSBL, sophgo/fsbl).
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


LZ4_FRAME_MAGIC = b"\x04\x22\x4d\x18"


def lz4_block(block, out):
    i = 0
    while i < len(block):
        token = block[i]
        i += 1
        length = token >> 4
        if length == 15:
            while True:
                require(i < len(block), "truncated LZ4 literal length")
                length += block[i]
                i += 1
                if block[i - 1] != 255:
                    break
        require(i + length <= len(block), "truncated LZ4 literals")
        out += block[i:i + length]
        i += length
        if i == len(block):
            return
        require(i + 2 <= len(block), "truncated LZ4 offset")
        offset = block[i] | block[i + 1] << 8
        i += 2
        require(0 < offset <= len(out), "invalid LZ4 match offset")
        length = token & 15
        if length == 15:
            while True:
                require(i < len(block), "truncated LZ4 match length")
                length += block[i]
                i += 1
                if block[i - 1] != 255:
                    break
        length += 4
        start = len(out) - offset
        if offset >= length:
            out += out[start:start + length]
        else:
            for k in range(length):
                out.append(out[start + k])


def lz4_frame(data):
    """Decode one LZ4 frame (as the FSBL's LZ4F_decompress does); return the content and the frame length."""
    require(data[:4] == LZ4_FRAME_MAGIC, "not an LZ4 frame")
    flg = data[4]
    require(flg >> 6 == 1, "unsupported LZ4 frame version")
    pos = 7 + (8 if flg & 0x08 else 0) + (4 if flg & 0x01 else 0)
    out = bytearray()
    while True:
        require(pos + 4 <= len(data), "truncated LZ4 frame")
        size = int.from_bytes(data[pos:pos + 4], "little")
        pos += 4
        if not size:
            break
        raw, size = size & 0x80000000, size & 0x7fffffff
        require(pos + size <= len(data), "truncated LZ4 block")
        if raw:
            out += data[pos:pos + size]
        else:
            lz4_block(data[pos:pos + size], out)
        pos += size + (4 if flg & 0x10 else 0)
    pos += 4 if flg & 0x04 else 0
    require(pos <= len(data), "truncated LZ4 frame")
    return bytes(out), pos


def lz4_payload(data, expected, name):
    content, end = lz4_frame(data)
    require(content == expected and not any(data[end:]),
            "packaged " + name + " differs or has extra compressed data")


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


def validate(candidate, bl2, chip_conf, ddr_param, monitor, uboot, manifest):
    p1, p2, new, spans = inspect(candidate)
    for key, value in (("MAGIC2", 0), ("NAND_INFO", 0), ("NOR_INFO", int.from_bytes(NOR_INFO, "little")),
                       ("FIP_FLAGS", 0), ("CHIP_CONF_SIZE", CHIP_CONF_FIELD), ("BLCP_IMG_SIZE", 0),
                       ("BLCP_IMG_RUNADDR", BLCP_IMG_RUNADDR), ("BLCP_PARAM_LOADADDR", 0),
                       ("BLCP_PARAM_SIZE", 0)):
        require(p1[key] == value, "unexpected first-stage parameter " + key)
    require(len(chip_conf) <= CHIP_CONF_FIELD and
            candidate[CHIP_CONF_OFFSET:CHIP_CONF_OFFSET + CHIP_CONF_FIELD] ==
            chip_conf + bytes(CHIP_CONF_FIELD - len(chip_conf)), "packaged chip configuration differs")
    require(not any(candidate[CHIP_CONF_OFFSET - 4:CHIP_CONF_OFFSET]) and
            not any(candidate[CHIP_CONF_OFFSET + CHIP_CONF_FIELD:4096]),
            "unexpected first-stage keys or signatures")
    require(new["BL2"] == bl2 + bytes((-len(bl2)) % ALIGN), "packaged BL2 differs from the built FSBL")
    require(new["DDR_PARAM"] == ddr_param + bytes((-len(ddr_param)) % ALIGN), "packaged DDR parameters differ")
    require(p2["DDR_PARAM_SIZE"] == len(new["DDR_PARAM"]), "unexpected DDR parameter size")
    require(not new["BLCP"], "unexpected BLCP image")
    # The M-mode DT reserves no memory at SMALL_CORE_ADDR (platform/opensbi/nanokvm.dts),
    # so a small-core image there would be overwritten by Linux.
    require(not new["BLCP_2ND"], "BLCP_2ND image requires a small-core reservation in the OpenSBI DT")
    # OpenSBI is an LZ4 frame; the FSBL (platform/fsbl patch 0005) decompresses it to MONITOR_RUNADDR.
    lz4_payload(new["MONITOR"], monitor, "MONITOR")
    require(sha(monitor) == manifest["binary_sha256"], "OpenSBI manifest hash mismatch")
    require(manifest["pmp_regions"] == [
        {"base": MONITOR_ADDR, "bytes": 0x20000, "m_permissions": "RX"},
        {"base": MONITOR_ADDR + 0x20000, "bytes": 0x10000, "m_permissions": "RW"}],
        "OpenSBI does not match the split RO/RW PMP contract")
    require(manifest["pmp_bytes"] == 0x30000, "incorrect total PMP reservation")
    require(MONITOR_ADDR + len(monitor) <= manifest["fw_end"] <= manifest["runtime_end"] <= MONITOR_ADDR + 0x30000,
            "OpenSBI runtime exceeds PMP reservation")
    require(manifest["fdt_destination"] == 0x80100000 and manifest["fdt_max_bytes"] == 0x10000,
            "unexpected FDT relocation contract")
    loader = new["LOADER_2ND"]
    require(loader[4:8] == b"B3Z4", "expected LZ4 U-Boot")
    lz4_payload(loader[32:], uboot, "U-Boot")
    return {
        "schema": 5, "fip_sha256": sha(candidate),
        "fip_bytes": len(candidate), "opensbi_version": manifest["version"],
        "opensbi_commit": manifest["upstream_commit"], "monitor_sha256": sha(monitor),
        "uboot_sha256": sha(uboot), "monitor_run_address": MONITOR_ADDR,
        "uboot_entry_address": UBOOT_ADDR, "pmp_bytes": manifest["pmp_bytes"],
        "pmp_regions": manifest["pmp_regions"],
        "bl2_sha256": sha(bl2), "bl2_bytes": len(bl2),
        "chip_conf_sha256": sha(chip_conf), "ddr_param_sha256": sha(ddr_param),
        "components": {name: {"sha256": sha(new[name]), "bytes": len(new[name])}
                       for name in sorted(new)},
        "file_spans": [{"name": n, "offset": start, "end": end} for n, start, end in spans],
        "checks": ["parameter CRCs", "all image CRCs", "512-byte alignment", "no file overlaps",
                   "first-stage parameters", "chip configuration bytes", "BL2 bytes", "DDR parameter bytes", "no BLCP or small-core image", "monitor LZ4 decompression", "U-Boot LZ4 decompression",
                   "load/run addresses", "192 KiB split RO/RW PMP contract"],
        "hardware_validation": "pending",
        "apk_updates_fip": False
    }


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("action", choices=("build", "verify"))
    for name in ("chip-conf", "bl2", "ddr-param", "monitor", "uboot", "opensbi-manifest", "output", "report"):
        p.add_argument("--" + name, type=Path, required=True)
    p.add_argument("--tool", type=Path)
    p.add_argument("--lz4", type=Path, help="lz4 command (build only)")
    a = p.parse_args()
    manifest = json.loads(a.opensbi_manifest.read_text())
    if a.action == "build":
        require(a.tool is not None and a.tool.is_file(), "missing pinned FIP tool")
        require(a.lz4 is not None and a.lz4.is_file(), "missing lz4 command")
        with tempfile.TemporaryDirectory(prefix="nkos-fip-", dir=a.output.parent) as directory:
            # The FSBL decodes an LZ4 frame in one LZ4F_decompress call; no
            # frame checksum (the FIP CRCs cover the compressed bytes).
            lz4 = [str(a.lz4), "-12", "--no-frame-crc", "-c", "-q"]
            monitor = Path(directory) / "fw_dynamic.bin.lz4"
            monitor.write_bytes(subprocess.run(lz4 + [str(a.monitor)], check=True, capture_output=True).stdout)
            # The vendor tool compresses U-Boot with the Python lz4 module;
            # this one runs the same lz4 command instead.
            shim = Path(directory) / "lz4"
            shim.mkdir()
            (shim / "__init__.py").write_text("")
            (shim / "frame.py").write_text(
                "import subprocess\n\n\ndef compress(data):\n"
                f"    return subprocess.run({lz4!r}, input=data, check=True, capture_output=True).stdout\n")
            raw = Path(directory) / "u-boot-raw.bin"
            raw.write_bytes(struct.pack("<I4sIIQII", 0, b"BL33", 0, 32 + a.uboot.stat().st_size,
                                        UBOOT_ADDR - 32, 0, 0) + a.uboot.read_bytes())
            empty = Path(directory) / "empty.bin"
            empty.write_bytes(b"")
            # The arguments of the SDK's fip.mk (fip-all), without a small core.
            result = subprocess.run([sys.executable, str(a.tool), "genfip", str(a.output),
                            "--CHIP_CONF", str(a.chip_conf), "--NOR_INFO", NOR_INFO.hex(),
                            "--NAND_INFO", "00000000", "--BL2", str(a.bl2),
                            "--BLCP_IMG_RUNADDR", hex(BLCP_IMG_RUNADDR), "--BLCP_PARAM_LOADADDR", "0",
                            "--BLCP", str(empty), "--DDR_PARAM", str(a.ddr_param),
                            "--MONITOR", str(monitor), "--MONITOR_RUNADDR", hex(MONITOR_ADDR),
                            "--LOADER_2ND", str(raw), "--compress", "lz4"],
                           capture_output=True, env={**os.environ, "PYTHONPATH": directory})
            (a.output.parent / "fiptool.log").write_bytes(result.stdout + result.stderr)
            require(result.returncode == 0, "vendor FIP tool failed; see fiptool.log")
    report = validate(a.output.read_bytes(), a.bl2.read_bytes(), a.chip_conf.read_bytes(),
                      a.ddr_param.read_bytes(), a.monitor.read_bytes(), a.uboot.read_bytes(), manifest)
    a.report.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    print(f"FIP verified: {len(a.output.read_bytes())} bytes; OpenSBI {manifest['version']}; all parts built")


if __name__ == "__main__":
    main()
