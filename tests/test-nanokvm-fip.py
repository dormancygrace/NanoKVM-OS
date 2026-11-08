#!/usr/bin/env python3
"""Exercise real FIP validation against a built candidate and corrupt inputs."""
import argparse
import copy
import hashlib
import importlib.util
import json
import struct
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("fip", REPO / "scripts/nanokvm-fip.py")
fip = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fip)

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("--images", type=Path, required=True)
a = p.parse_args()
candidate = (a.images / "fip.bin").read_bytes()
bl2 = (a.images / "fsbl/bl2.bin").read_bytes()
chip_conf = (a.images / "fsbl/chip_conf.bin").read_bytes()
ddr_param = (a.images / "fsbl/ddr_param.bin").read_bytes()
monitor = (a.images / "opensbi/fw_dynamic.bin").read_bytes()
uboot = (a.images / "u-boot.bin").read_bytes()
manifest = json.loads((a.images / "opensbi/build-manifest.json").read_text())
report = fip.validate(candidate, bl2, chip_conf, ddr_param, monitor, uboot, manifest)
assert json.loads((a.images / "fip-manifest.json").read_text())["fip_sha256"] == hashlib.sha256(candidate).hexdigest()
print("built candidate: pass")


def rejected(name, action, message):
    try:
        action()
    except ValueError as e:
        assert message in str(e), (name, str(e))
        print(name + ": pass")
        return
    raise AssertionError(name + " was accepted")


def changed(data, offset):
    out = bytearray(data)
    out[offset] ^= 1
    return bytes(out)


def field_offset(layout, key):
    return sum(size for name, size in layout[:next(i for i, row in enumerate(layout) if row[0] == key)])


def patch_param2(changes):
    out = bytearray(candidate)
    p1, p2, _, _ = fip.inspect(candidate)
    start = p1["PARAM2_LOADADDR"]
    for key, value in changes.items():
        struct.pack_into("<I", out, start + field_offset(fip.P2_FIELDS, key), value)
    struct.pack_into("<I", out, start + 8, fip.crc(out[start + 12:start + 4096]))
    return bytes(out)


p1, p2, parts, spans = fip.inspect(candidate)
rejected("truncated FIP", lambda: fip.inspect(candidate[:-1]), "size/alignment")
rejected("PARAM2 CRC corruption", lambda: fip.inspect(changed(candidate, p1["PARAM2_LOADADDR"] + 128)), "PARAM2 CRC")
rejected("PARAM1 CRC corruption", lambda: fip.inspect(changed(candidate, 32)), "PARAM1 CRC")
rejected("monitor CRC corruption", lambda: fip.inspect(changed(candidate, p2["MONITOR_LOADADDR"] + 128)), "MONITOR CRC")
rejected("DDR CRC corruption", lambda: fip.inspect(changed(candidate, p2["DDR_PARAM_LOADADDR"] + 128)), "DDR_PARAM CRC")
rejected("loader CRC corruption", lambda: fip.inspect(changed(candidate, p2["LOADER_2ND_LOADADDR"] + 40)), "LOADER_2ND CRC")
rejected("wrong monitor run address",
         lambda: fip.inspect(patch_param2({"MONITOR_RUNADDR": 0x80001000})), "MONITOR run address")
rejected("overlapping components",
         lambda: fip.inspect(patch_param2({"MONITOR_LOADADDR": p2["DDR_PARAM_LOADADDR"],
                                           "MONITOR_SIZE": p2["DDR_PARAM_SIZE"],
                                           "MONITOR_CKSUM": p2["DDR_PARAM_CKSUM"]})), "overlaps")
rejected("unexpected suffix", lambda: fip.inspect(candidate + b"X" + bytes(511)), "FIP suffix")
bad = copy.deepcopy(manifest)
bad["pmp_regions"][1]["bytes"] = 0x8000
rejected("undersized RW reservation", lambda: fip.validate(candidate, bl2, chip_conf, ddr_param, monitor, uboot, bad), "PMP contract")
bad = copy.deepcopy(manifest)
bad["fdt_destination"] = 0x80011000
rejected("old FDT destination", lambda: fip.validate(candidate, bl2, chip_conf, ddr_param, monitor, uboot, bad), "FDT relocation")
rejected("wrong U-Boot payload", lambda: fip.validate(candidate, bl2, chip_conf, ddr_param, monitor, uboot + b"X", manifest), "U-Boot differs")
out = bytearray(candidate)
bl2start = 4096 + p1["BLCP_IMG_SIZE"]
out[bl2start + 100] ^= 1
struct.pack_into("<I", out, field_offset(fip.P1_FIELDS, "BL2_IMG_CKSUM"),
                 fip.crc(out[bl2start:bl2start + p1["BL2_IMG_SIZE"]]))
struct.pack_into("<I", out, 12, fip.crc(out[16:2048]))
rejected("valid-CRC BL2 modification",
         lambda: fip.validate(bytes(out), bl2, chip_conf, ddr_param, monitor, uboot, manifest), "BL2 differs")
out = bytearray(candidate)
out[fip.CHIP_CONF_OFFSET + 8] ^= 1  # second chip configuration write
struct.pack_into("<I", out, 12, fip.crc(out[16:2048]))
rejected("valid-CRC chip configuration change",
         lambda: fip.validate(bytes(out), bl2, chip_conf, ddr_param, monitor, uboot, manifest), "chip configuration differs")


def with_monitor(change):
    """The candidate with its MONITOR bytes changed and every outer CRC recomputed."""
    out = bytearray(candidate)
    start, size = p2["MONITOR_LOADADDR"], p2["MONITOR_SIZE"]
    body = bytearray(out[start:start + size])
    change(body)
    out[start:start + size] = body
    p2start = p1["PARAM2_LOADADDR"]
    struct.pack_into("<I", out, p2start + field_offset(fip.P2_FIELDS, "MONITOR_CKSUM"), fip.crc(bytes(body)))
    struct.pack_into("<I", out, p2start + 8, fip.crc(out[p2start + 12:p2start + 4096]))
    fip.inspect(bytes(out))  # the outer CRCs are valid
    return bytes(out)


def flip(offset):
    def change(body):
        body[offset] ^= 1
    return change


def frame(block):
    """An LZ4 frame of the build's layout around one compressed block."""
    head = bytes([fip.LZ4_FLG, fip.LZ4_BD])
    return (fip.LZ4_FRAME_MAGIC + head + bytes([(fip.xxh32(head) >> 8) & 0xff]) +
            struct.pack("<I", len(block)) + block + bytes(4))


def validate(data):
    return fip.validate(data, bl2, chip_conf, ddr_param, monitor, uboot, manifest)


# The FSBL's LZ4F decoder rejects these frames; the FIP must not certify them.
rejected("monitor LZ4 header checksum", lambda: validate(with_monitor(flip(6))), "header checksum")
rejected("monitor LZ4 dictionary flag", lambda: validate(with_monitor(flip(4))), "frame flags")
rejected("monitor LZ4 4 MiB blocks", lambda: validate(with_monitor(lambda b: b.__setitem__(5, 0x70))), "frame flags")
rejected("monitor LZ4 block cut short", lambda: validate(with_monitor(flip(8))), "LZ4")
content, end = fip.lz4_frame(parts["MONITOR"], 1 << 30)
assert content == monitor
rejected("monitor larger than its destination", lambda: fip.lz4_frame(parts["MONITOR"], len(monitor) - 1), "destination")
# 12 literals, then a match of 4 at offset 8 that ends the block
rejected("block ending in a match", lambda: fip.lz4_frame(frame(bytes([0xc0]) + b"abcdefghijkl" + b"\x08\x00"), 1 << 20),
         "block end")
# a match inside the last 12 bytes, then 5 literals
rejected("match too close to the block end",
         lambda: fip.lz4_frame(frame(bytes([0x80]) + b"abcdefgh" + b"\x08\x00" + bytes([0x50]) + b"vwxyz"), 1 << 20),
         "too close")
assert fip.lz4_frame(frame(bytes([0x50]) + b"vwxyz"), 1 << 20)[0] == b"vwxyz"
print("literal-only block: pass")
real_inspect = fip.inspect


def with_small_core(data):
    p1, p2, parts, spans = real_inspect(data)
    return p1, p2, {**parts, "BLCP_2ND": bytes(512)}, spans


fip.inspect = with_small_core
try:
    rejected("small-core image without DT reservation",
             lambda: fip.validate(candidate, bl2, chip_conf, ddr_param, monitor, uboot, manifest), "small-core reservation")
finally:
    fip.inspect = real_inspect
