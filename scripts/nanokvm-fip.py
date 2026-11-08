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
# The one frame layout the build produces (lz4 -12 -B5 --no-frame-crc) and the FIP accepts:
# FLG: version 01, independent blocks, no block/content checksum, no content size, no
# dictionary; BD: 256 KiB blocks (small enough for the reference LZ4F decoder's buffers, which
# the FSBL used before platform/fsbl patch 0006).
LZ4_FLG = 0x60
LZ4_BD = 0x50
LZ4_BLOCK_MAX = 256 << 10
# LZ4 block format: the last 5 bytes are literals and the last match starts at least 12 bytes
# before the end of the block; LZ4_decompress_safe() keeps 8 input bytes after a non-final
# literal run (token, offset, last literals).
LZ4_LASTLITERALS = 5
LZ4_MFLIMIT = 12


def xxh32(data, seed=0):
    p1, p2, p3, p4, p5 = 2654435761, 2246822519, 3266489917, 668265263, 374761393
    m = 0xffffffff

    def rotl(x, r):
        return ((x << r) | (x >> (32 - r))) & m

    i, n = 0, len(data)
    if n >= 16:
        v = [(seed + p1 + p2) & m, (seed + p2) & m, seed, (seed - p1) & m]
        while i + 16 <= n:
            for k in range(4):
                lane = int.from_bytes(data[i + 4 * k:i + 4 * k + 4], "little")
                v[k] = rotl((v[k] + lane * p2) & m, 13) * p1 & m
            i += 16
        h = (rotl(v[0], 1) + rotl(v[1], 7) + rotl(v[2], 12) + rotl(v[3], 18)) & m
    else:
        h = (seed + p5) & m
    h = (h + n) & m
    while i + 4 <= n:
        h = rotl((h + int.from_bytes(data[i:i + 4], "little") * p3) & m, 17) * p4 & m
        i += 4
    while i < n:
        h = rotl((h + data[i] * p5) & m, 11) * p1 & m
        i += 1
    h ^= h >> 15
    h = h * p2 & m
    h ^= h >> 13
    h = h * p3 & m
    return h ^ (h >> 16)


def lz4_length(block, i, length):
    if length == 15:
        while True:
            require(i < len(block), "truncated LZ4 length")
            length += block[i]
            i += 1
            if block[i - 1] != 255:
                break
    return i, length


def lz4_block(block):
    """Decode one independent LZ4 block under the rules of LZ4_decompress_safe() and the format."""
    out = bytearray()
    i = 0
    last_match = None
    while True:
        require(i < len(block), "LZ4 block does not end with literals")
        token = block[i]
        i, length = lz4_length(block, i + 1, token >> 4)
        require(i + length <= len(block), "truncated LZ4 literals")
        if i + length == len(block):  # the last sequence: literals only
            out += block[i:]
            break
        require(i + length <= len(block) - (2 + 1 + LZ4_LASTLITERALS), "LZ4 literals too close to the block end")
        out += block[i:i + length]
        i += length
        offset = block[i] | block[i + 1] << 8
        i, length = lz4_length(block, i + 2, token & 15)
        length += 4
        require(0 < offset <= len(out), "LZ4 match outside its block")
        start = len(out) - offset
        if offset >= length:
            out += out[start:start + length]
        else:
            for k in range(length):
                out.append(out[start + k])
        require(len(out) <= LZ4_BLOCK_MAX, "LZ4 block larger than the frame's block size")
        last_match = (start + offset, len(out))
    if last_match:
        require(last_match[0] <= len(out) - LZ4_MFLIMIT and last_match[1] <= len(out) - LZ4_LASTLITERALS,
                "LZ4 match too close to the block end")
    require(len(out) <= LZ4_BLOCK_MAX, "LZ4 block larger than the frame's block size")
    return out


def lz4_frame(data, limit):
    """Decode one LZ4 frame of the build's layout; return the content and the frame length."""
    require(data[:4] == LZ4_FRAME_MAGIC, "not an LZ4 frame")
    require(len(data) >= 7 and data[4] == LZ4_FLG and data[5] == LZ4_BD, "unexpected LZ4 frame flags or block size")
    require(data[6] == (xxh32(data[4:6]) >> 8) & 0xff, "LZ4 frame header checksum mismatch")
    pos = 7
    out = bytearray()
    while True:
        require(pos + 4 <= len(data), "truncated LZ4 frame")
        size = int.from_bytes(data[pos:pos + 4], "little")
        pos += 4
        if not size:
            break
        raw, size = size & 0x80000000, size & 0x7fffffff
        require(size <= LZ4_BLOCK_MAX, "LZ4 block larger than the frame's block size")
        require(pos + size <= len(data), "truncated LZ4 block")
        out += data[pos:pos + size] if raw else lz4_block(data[pos:pos + size])
        require(len(out) <= limit, "LZ4 content larger than its destination")
        pos += size
    return bytes(out), pos


def lz4_payload(data, expected, name, limit):
    content, end = lz4_frame(data, limit)
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
    # finish_monitor() decodes up to the U-Boot header
    lz4_payload(new["MONITOR"], monitor, "MONITOR", UBOOT_ADDR - 32 - MONITOR_ADDR)
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
    # load_loader_2nd() decodes into DECOMP_DST_SIZE (16 MiB)
    lz4_payload(loader[32:], uboot, "U-Boot", 16 << 20)
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
            # No frame checksum (the FIP CRCs cover the compressed bytes);
            # 256 KiB blocks (LZ4_BD).
            lz4 = [str(a.lz4), "-12", "-B5", "--no-frame-crc", "-c", "-q"]
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
