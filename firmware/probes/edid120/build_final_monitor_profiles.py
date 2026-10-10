#!/usr/bin/env python3
"""Create final-mode EDIDs: one per monitor resolution and refresh rate.

NanoKVM-monitor-<height>-<hz>.bin prefers that mode; NanoKVM-monitor-<height>.bin
is the fastest rate of a resolution, as before. The server picks the slowest
rate that is not below the stream frame rate, so the source renders and sends
no more frames than are streamed.

A profile advertises no mode at or above its own resolution other than its
preferred timing. A GPU driver does not honour the preferred timing: given
another mode of the same or a larger resolution (a 1080p50 CTA code, a QHD
descriptor) it outputs that one. Lower-resolution fallbacks (VGA, 800x600,
1024x768, 720p ...) stay.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import sys


HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location(
    "final_monitor_edid", HERE / "build_experimental_edid.py"
)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cannot load EDID validation helpers")
PRIMARY = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PRIMARY
SPEC.loader.exec_module(PRIMARY)

EXPECTED_SOURCE_SHA256 = (
    "b0361c1ead6728f44a41d9f0f8f38b80f4815a7b5a8ee303f0c3fd0cb8d06a30"
)
PREFERRED = {
    720: (1280, 720, 120.0),
    1080: (1920, 1080, 75.0),
    1440: (2560, 1440, 50.0),
}

# CTA-861 Video Identification Codes as VIC:<width>x<height><p|i><Hz>. Pixel
# repeated modes list the active width (720(1440)x480i is 720x480i) and rates
# are nominal (59.94 Hz is 60). Codes 1-64 may carry the "native" bit 7.
CTA_VIC_TABLE = """
 1:640x480p60 2:720x480p60 3:720x480p60 4:1280x720p60 5:1920x1080i60
 6:720x480i60 7:720x480i60 8:720x240p60 9:720x240p60 10:2880x480i60
 11:2880x480i60 12:2880x240p60 13:2880x240p60 14:1440x480p60 15:1440x480p60
 16:1920x1080p60 17:720x576p50 18:720x576p50 19:1280x720p50 20:1920x1080i50
 21:720x576i50 22:720x576i50 23:720x288p50 24:720x288p50 25:2880x576i50
 26:2880x576i50 27:2880x288p50 28:2880x288p50 29:1440x576p50 30:1440x576p50
 31:1920x1080p50 32:1920x1080p24 33:1920x1080p25 34:1920x1080p30
 35:2880x480p60 36:2880x480p60 37:2880x576p50 38:2880x576p50
 39:1920x1080i50 40:1920x1080i100 41:1280x720p100 42:720x576p100 43:720x576p100
 44:720x576i100 45:720x576i100 46:1920x1080i120 47:1280x720p120
 48:720x480p120 49:720x480p120 50:720x480i120 51:720x480i120
 52:720x576p200 53:720x576p200 54:720x576i200 55:720x576i200
 56:720x480p240 57:720x480p240 58:720x480i240 59:720x480i240
 60:1280x720p24 61:1280x720p25 62:1280x720p30 63:1920x1080p120 64:1920x1080p100
 65:1280x720p24 66:1280x720p25 67:1280x720p30 68:1280x720p50 69:1280x720p60
 70:1280x720p100 71:1280x720p120 72:1920x1080p24 73:1920x1080p25
 74:1920x1080p30 75:1920x1080p50 76:1920x1080p60 77:1920x1080p100
 78:1920x1080p120 79:1680x720p24 80:1680x720p25 81:1680x720p30
 82:1680x720p50 83:1680x720p60 84:1680x720p100 85:1680x720p120
 86:2560x1080p24 87:2560x1080p25 88:2560x1080p30 89:2560x1080p50
 90:2560x1080p60 91:2560x1080p100 92:2560x1080p120 93:3840x2160p24
 94:3840x2160p25 95:3840x2160p30 96:3840x2160p50 97:3840x2160p60
 98:4096x2160p24 99:4096x2160p25 100:4096x2160p30 101:4096x2160p50
 102:4096x2160p60 103:3840x2160p24 104:3840x2160p25 105:3840x2160p30
 106:3840x2160p50 107:3840x2160p60 108:1280x720p48 109:1280x720p48
 110:1680x720p48 111:1920x1080p48 112:1920x1080p48 113:2560x1080p48
 114:3840x2160p48 115:4096x2160p48 116:3840x2160p48 117:3840x2160p100
 118:3840x2160p120 119:3840x2160p100 120:3840x2160p120 121:5120x2160p24
 122:5120x2160p25 123:5120x2160p30 124:5120x2160p48 125:5120x2160p50
 126:5120x2160p60 127:5120x2160p100 193:5120x2160p120 194:7680x4320p24
 195:7680x4320p25 196:7680x4320p30 197:7680x4320p48 198:7680x4320p50
 199:7680x4320p60 200:7680x4320p100 201:7680x4320p120 202:7680x4320p24
 203:7680x4320p25 204:7680x4320p30 205:7680x4320p48 206:7680x4320p50
 207:7680x4320p60 208:7680x4320p100 209:7680x4320p120 210:10240x4320p24
 211:10240x4320p25 212:10240x4320p30 213:10240x4320p48 214:10240x4320p50
 215:10240x4320p60 216:10240x4320p100 217:10240x4320p120 218:4096x2160p100
 219:4096x2160p120
"""
CTA_VICS = {
    int(vic): (int(w), int(h), scan == "i", int(hz))
    for vic, w, h, scan, hz in re.findall(r"(\d+):(\d+)x(\d+)([pi])(\d+)", CTA_VIC_TABLE)
}
# EDID 1.3 established timings in bytes 35 and 36: (byte, bit, width, height).
ESTABLISHED = [
    (35, 0x80, 720, 400), (35, 0x40, 720, 400), (35, 0x20, 640, 480),
    (35, 0x10, 640, 480), (35, 0x08, 640, 480), (35, 0x04, 640, 480),
    (35, 0x02, 800, 600), (35, 0x01, 800, 600), (36, 0x80, 800, 600),
    (36, 0x40, 800, 600), (36, 0x20, 832, 624), (36, 0x10, 1024, 768),
    (36, 0x08, 1024, 768), (36, 0x04, 1024, 768), (36, 0x02, 1024, 768),
    (36, 0x01, 1280, 1024),
]
STD_ASPECT = {0: (16, 10), 1: (4, 3), 2: (5, 4), 3: (16, 9)}
# A CTA code is the preferred timing when it has its size and rate (59.94 Hz
# is listed as 60); the profile builders use CTA timings for the preferred DTDs.
VIC_RATE_TOLERANCE_HZ = 0.5
# HDMI vendor block: HDMI_Video_present is bit 5 of payload byte 7 (extra HDMI VICs).
HDMI_VIDEO_PRESENT = (7, 0x20)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def vic_entry(code: int) -> dict:
    """The mode of a CTA video data block byte."""
    vic = code & 0x7F if 1 <= (code & 0x7F) <= 64 else code
    if vic not in CTA_VICS:
        raise ValueError(f"review CTA video code {code}")
    width, height, interlaced, hz = CTA_VICS[vic]
    return dict(kind="vic", code=code, width=width, height=height,
                interlaced=interlaced, hz=hz)


def list_modes(data: bytes) -> list[dict]:
    """Every mode an EDID advertises: established and standard timings, base
    and CTA detailed timings and CTA video codes."""
    modes = []
    for byte, bit, w, h in ESTABLISHED:
        if data[byte] & bit:
            modes.append(dict(kind="established", width=w, height=h))
    for pos in range(38, 54, 2):
        a, b = data[pos], data[pos + 1]
        if (a, b) == (1, 1):
            continue
        w = (a + 31) * 8
        num, den = STD_ASPECT[b >> 6]
        modes.append(dict(kind="standard", width=w, height=w * den // num,
                          hz=(b & 63) + 60))
    for pos in (54, 72, 90, 108):
        raw = bytes(data[pos:pos + 18])
        if raw[0] | raw[1]:
            t = PRIMARY.decode_dtd(raw, label=f"base DTD 0x{pos:02x}")
            modes.append(dict(kind="base-dtd", width=t.width, height=t.height,
                              hz=t.refresh_hz, raw=raw))
        elif raw[3] in (0xF7, 0xF8, 0xFA):
            raise ValueError("review base descriptors that list further timings")
    blocks, start = PRIMARY.parse_cta_blocks(data)
    for tag, payload in blocks:
        if tag == 2:
            modes.extend(vic_entry(code) for code in payload)
        elif tag == 7 and payload and payload[0] in (3, 13, 14, 15):
            raise ValueError("review extended CTA blocks that list video modes")
        elif tag == 3 and payload[:3] == bytes.fromhex("030c00") \
                and len(payload) > HDMI_VIDEO_PRESENT[0] \
                and payload[HDMI_VIDEO_PRESENT[0]] & HDMI_VIDEO_PRESENT[1]:
            raise ValueError("review the HDMI video modes of the vendor block")
    for _, raw, t in PRIMARY.parse_cta_dtds(data, start):
        modes.append(dict(kind="cta-dtd", width=t.width, height=t.height,
                          hz=t.refresh_hz, raw=raw))
    return modes


def is_own_mode(mode: dict, preferred_raw: bytes, preferred) -> bool:
    """The preferred timing itself: its descriptors and a progressive CTA code
    of the same size and rate."""
    if mode["kind"] in ("base-dtd", "cta-dtd"):
        return mode["raw"] == preferred_raw
    return (mode["kind"] == "vic" and not mode["interlaced"]
            and (mode["width"], mode["height"]) == (preferred.width, preferred.height)
            and abs(mode["hz"] - preferred.refresh_hz) <= VIC_RATE_TOLERANCE_HZ)


def at_or_above(mode: dict, preferred) -> bool:
    """A mode is at or above the preferred resolution when it is as wide or as
    tall; a mode lower in both directions is a fallback."""
    return mode["width"] >= preferred.width or mode["height"] >= preferred.height


def modes_above_preferred(data: bytes) -> list[dict]:
    """Modes at or above the preferred resolution other than the preferred
    timing; empty when the EDID follows the single-rate rule."""
    raw = bytes(data[54:72])
    preferred = PRIMARY.decode_dtd(raw, label="preferred")
    return [m for m in list_modes(data)
            if at_or_above(m, preferred) and not is_own_mode(m, raw, preferred)]


def prune_to_preferred(data: bytes) -> bytes:
    """Remove every mode at or above the preferred resolution except the
    preferred timing. Lower-resolution fallbacks, the name and range
    descriptors and every other CTA block (HDMI vendor, audio, speakers) stay."""
    raw = bytes(data[54:72])
    preferred = PRIMARY.decode_dtd(raw, label="preferred")

    def above(w: int, h: int) -> bool:
        return w >= preferred.width or h >= preferred.height

    out = bytearray(data)
    for byte, bit, w, h in ESTABLISHED:
        if above(w, h):
            out[byte] &= ~bit & 0xFF
    for pos in range(38, 54, 2):
        a, b = out[pos], out[pos + 1]
        if (a, b) == (1, 1):
            continue
        w = (a + 31) * 8
        num, den = STD_ASPECT[b >> 6]
        if above(w, w * den // num):
            out[pos:pos + 2] = b"\x01\x01"
    for pos in (72, 90, 108):
        descriptor = bytes(out[pos:pos + 18])
        if descriptor[0] | descriptor[1]:
            t = PRIMARY.decode_dtd(descriptor, label=f"base DTD 0x{pos:02x}")
            if above(t.width, t.height) and descriptor != raw:
                out[pos:pos + 18] = bytes.fromhex("0000001000") + bytes(13)
    out[127] = (-sum(out[:127])) & 0xFF

    def keep(mode: dict) -> bool:
        return not at_or_above(mode, preferred) or is_own_mode(mode, raw, preferred)

    blocks, start = PRIMARY.parse_cta_blocks(data)
    cta = bytearray()
    for tag, payload in blocks:
        if tag == 2:
            payload = bytes(code for code in payload if keep(vic_entry(code)))
            if not payload:
                continue
        cta += bytes([(tag << 5) | len(payload)]) + payload
    dtds: list[bytes] = []
    for _, descriptor, t in PRIMARY.parse_cta_dtds(data, start):
        mode = dict(kind="cta-dtd", width=t.width, height=t.height, raw=descriptor)
        if keep(mode) and descriptor not in dtds:
            dtds.append(descriptor)
    if 4 + len(cta) + 18 * len(dtds) > 127:
        raise ValueError("CTA block does not fit")
    ext = bytearray(128)
    ext[:4] = bytes([2, 3, 4 + len(cta), data[131]])
    ext[4:4 + len(cta)] = cta
    offset = 4 + len(cta)
    for descriptor in dtds:
        ext[offset:offset + 18] = descriptor
        offset += 18
    ext[127] = (-sum(ext[:127])) & 0xFF
    result = bytes(out[:128] + ext)
    PRIMARY.check_basic_edid(result, label="pruned profile")
    if modes_above_preferred(result):
        raise ValueError("a mode at or above the preferred resolution remains")
    return result


def accepted_full_edid(source: bytes) -> tuple[bytes, list]:
    """The accepted final EDID with the duplicate FHD60 descriptor replaced by
    QHD50: every unique fallback timing, before the single-rate pruning."""
    if sha256(source) != EXPECTED_SOURCE_SHA256:
        raise ValueError("input is not the accepted final EDID")
    PRIMARY.check_basic_edid(source, label="final source")
    _, dtd_start = PRIMARY.parse_cta_blocks(source)
    dtds = PRIMARY.parse_cta_dtds(source, dtd_start)
    # The tested QHD50 timing uses the QHD40 blanking at a 201.42 MHz clock.
    qhd40 = [raw for _, raw, timing in dtds
             if (timing.width, timing.height) == (2560, 1440)
             and abs(timing.refresh_hz - 40) < 0.1]
    fhd60 = [(offset, raw) for offset, raw, timing in dtds
             if (timing.width, timing.height) == (1920, 1080)
             and abs(timing.refresh_hz - 60) < 0.1]
    if len(qhd40) != 1 or len(fhd60) != 2 or fhd60[0][1] != fhd60[1][1]:
        raise ValueError("expected QHD40 and identical duplicate FHD60 timings")
    qhd50 = bytearray(qhd40[0])
    qhd50[:2] = (20142).to_bytes(2, "little")
    candidate = bytearray(source)
    offset = fhd60[1][0]
    candidate[offset:offset + 18] = qhd50
    candidate[255] = (-sum(candidate[128:255])) & 0xFF
    original_modes = {raw for _, raw, _ in dtds}
    dtds = PRIMARY.parse_cta_dtds(candidate, dtd_start)
    if not original_modes.issubset({bytes(raw) for _, raw, _ in dtds}):
        raise ValueError("a unique fallback timing was removed")
    return bytes(candidate), dtds


def with_preferred(data: bytes, raw: bytes) -> bytes:
    """data with raw as the base-block preferred DTD and the preferred bit set."""
    out = bytearray(data)
    out[24] = (out[24] | 2) & ~1
    out[54:72] = raw
    out[127] = (-sum(out[:127])) & 0xFF
    return bytes(out)


def full_profile(source: bytes, height: int) -> tuple[bytes, object]:
    """The profile of a height with every fallback timing still listed."""
    candidate, dtds = accepted_full_edid(source)
    width, expected_height, rate = PREFERRED[height]
    matches = [
        (raw, timing)
        for _, raw, timing in dtds
        if timing.width == width
        and timing.height == expected_height
        and abs(timing.refresh_hz - rate) < 0.1
    ]
    if len(matches) != 1:
        raise ValueError(f"expected one {width}x{height}@{rate:g} DTD")
    raw, timing = matches[0]
    candidate = with_preferred(candidate, raw)
    PRIMARY.check_basic_edid(candidate, label=f"monitor-{height}")
    preferred = PRIMARY.decode_dtd(candidate[54:72], label="preferred")
    if preferred != timing:
        raise ValueError("preferred DTD differs from accepted CTA timing")
    return candidate, preferred


def build_profile(source: bytes, height: int) -> tuple[bytes, object]:
    candidate, _ = full_profile(source, height)
    data = prune_to_preferred(candidate)
    return data, PRIMARY.decode_dtd(data[54:72], label="preferred")


# 3840x2160 at 29.98 Hz, CVT reduced blanking: the lowest pixel clock and
# therefore MIPI rate for UHD (CTA VIC 95 needs 297 MHz).
UHD30 = dict(width=3840, height=2160, pixel_clock_hz=262_750_000, hblank=160,
             vblank=31, hfront=48, hsync=32, vfront=3, vsync=5, flags=0x1A)
UHD_MAX_PIXEL_CLOCK_MHZ = 270
UHD_MAX_TMDS_MHZ = 300


def encode_dtd(t: dict, size: bytes) -> bytes:
    d = bytearray(18)
    d[:2] = (t["pixel_clock_hz"] // 10_000).to_bytes(2, "little")
    d[2:5] = bytes([t["width"] & 255, t["hblank"] & 255,
                    ((t["width"] >> 8) << 4) | (t["hblank"] >> 8)])
    d[5:8] = bytes([t["height"] & 255, t["vblank"] & 255,
                    ((t["height"] >> 8) << 4) | (t["vblank"] >> 8)])
    d[8:12] = bytes([t["hfront"] & 255, t["hsync"] & 255,
                     ((t["vfront"] & 15) << 4) | (t["vsync"] & 15),
                     ((t["hfront"] >> 8) << 6) | ((t["hsync"] >> 8) << 4)
                     | ((t["vfront"] >> 4) << 2) | (t["vsync"] >> 4)])
    d[12:17] = size
    d[17] = t["flags"]
    return bytes(d)


def build_uhd_profile(source: bytes) -> tuple[bytes, object]:
    """UHD30 preferred; every lower-resolution final mode stays as a fallback.

    UHD30 is the base-block preferred DTD; the QHD50 it displaces stays in
    the CTA list, as do the 1080p and 720p modes (all below 2160, so the
    single-rate rule removes nothing). The CTA block is full, so the 720p60
    DTD (also CTA VIC 4) makes room for a Max_TMDS_Clock byte in the HDMI
    vendor block. The range limit is raised from 250 to 270 MHz to admit
    262.75 MHz.
    """
    qhd, _ = full_profile(source, 1440)
    blocks, dtd_start = PRIMARY.parse_cta_blocks(qhd)
    dtds = PRIMARY.parse_cta_dtds(qhd, dtd_start)
    hd60 = [raw for _, raw, timing in dtds
            if (timing.width, timing.height) == (1280, 720)
            and abs(timing.refresh_hz - 60) < 0.1]
    video = [payload for tag, payload in blocks if tag == 2]
    vsdb = [payload for tag, payload in blocks if tag == 3]
    if len(hd60) != 1 or len(video) != 1 or 4 not in video[0]:
        raise ValueError("expected one 720p60 DTD that duplicates CTA VIC 4")
    if len(vsdb) != 1 or vsdb[0] != bytes.fromhex("030c001000"):
        raise ValueError("review the HDMI vendor block before extending it")
    uhd = encode_dtd(UHD30, qhd[66:71])
    out = bytearray(qhd[:128])
    out[54:72] = uhd
    pos = PRIMARY.find_range_descriptor(out)
    if out[pos + 9] != 25:
        raise ValueError("expected a 250 MHz range limit")
    out[pos + 9] = UHD_MAX_PIXEL_CLOCK_MHZ // 10
    out[127] = (-sum(out[:127])) & 0xFF
    # Max_TMDS_Clock follows one byte of HDMI feature flags (all clear).
    cta = bytearray()
    for tag, payload in blocks:
        if tag == 3:
            payload = payload + bytes([0, UHD_MAX_TMDS_MHZ // 5])
        cta += bytes([(tag << 5) | len(payload)]) + payload
    ext = bytearray(128)
    ext[:4] = bytes([2, 3, 4 + len(cta), qhd[131]])
    ext[4:4 + len(cta)] = cta
    offset = 4 + len(cta)
    for raw in [raw for _, raw, _ in dtds if raw != hd60[0]]:
        if offset + 18 > 127:
            raise ValueError("CTA fallback timings do not fit")
        ext[offset:offset + 18] = raw
        offset += 18
    ext[127] = (-sum(ext[:127])) & 0xFF
    candidate = bytes(out + ext)
    PRIMARY.check_basic_edid(candidate, label="monitor-2160")
    preferred = PRIMARY.decode_dtd(candidate[54:72], label="preferred")
    if (preferred.width, preferred.height) != (3840, 2160) or abs(preferred.refresh_hz - 30) > 0.05:
        raise ValueError("unexpected UHD30 timing")
    kept = {raw for _, raw, _ in PRIMARY.parse_cta_dtds(candidate, 128 + candidate[130])}
    if not {raw for _, raw, _ in dtds if raw != hd60[0]} <= kept:
        raise ValueError("a fallback timing was removed")
    if modes_above_preferred(candidate):
        raise ValueError("a mode at or above 3840x2160 other than UHD30 is listed")
    return candidate, preferred


# Refresh rates per monitor height; the first is the default profile.
RATES = {720: (120, 60, 30), 1080: (100, 75, 60, 30), 1440: (60, 50, 40, 30), 2160: (30,)}

# Timings that the final CTA block does not already list.
EXTRA_TIMINGS = {
    # CVT reduced blanking; the encoder sustains about 109 fps at 1080p.
    (1920, 1080, 100): dict(width=1920, height=1080, pixel_clock_hz=235_500_000, hblank=160,
                            vblank=53, hfront=48, hsync=32, vfront=3, vsync=5, flags=0x1A),
    # CVT reduced blanking.
    (2560, 1440, 60): dict(width=2560, height=1440, pixel_clock_hz=241_500_000, hblank=160,
                           vblank=41, hfront=48, hsync=32, vfront=3, vsync=5, flags=0x1A),
    # CTA VIC 34.
    (1920, 1080, 30): dict(width=1920, height=1080, pixel_clock_hz=74_250_000, hblank=280,
                           vblank=45, hfront=88, hsync=44, vfront=4, vsync=5, flags=0x1E),
    # CVT reduced blanking; CTA VIC 62 has a front porch beyond the DTD field.
    (1280, 720, 30): dict(width=1280, height=720, pixel_clock_hz=31_750_000, hblank=160,
                          vblank=14, hfront=48, hsync=32, vfront=3, vsync=5, flags=0x1A),
    # CVT reduced blanking, as scripts/build-monitor-edids.py.
    (2560, 1440, 30): dict(width=2560, height=1440, pixel_clock_hz=120_750_000, hblank=160,
                           vblank=41, hfront=48, hsync=32, vfront=3, vsync=5, flags=0x1A),
}
WIDTHS = {720: 1280, 1080: 1920, 1440: 2560, 2160: 3840}


def build_rate_profile(source: bytes, height: int, rate: int) -> tuple[bytes, object]:
    """The profile of a height whose only mode at or above its resolution is
    the preferred timing at this refresh rate."""
    if height == 2160:
        if rate != 30:
            raise ValueError("3840x2160 is offered at 30 Hz only")
        return build_uhd_profile(source)
    width = WIDTHS[height]
    full, dtds = accepted_full_edid(source)
    listed = [raw for _, raw, t in dtds
              if (t.width, t.height) == (width, height) and abs(t.refresh_hz - rate) < 0.1]
    if len(listed) > 1:
        raise ValueError(f"duplicate {width}x{height}@{rate} DTDs")
    raw = listed[0] if listed else encode_dtd(EXTRA_TIMINGS[(width, height, rate)], full[66:71])
    candidate = prune_to_preferred(with_preferred(full, raw))
    PRIMARY.check_basic_edid(candidate, label=f"monitor-{height}-{rate}")
    timing = PRIMARY.decode_dtd(candidate[54:72], label="preferred")
    if (timing.width, timing.height) != (width, height) or abs(timing.refresh_hz - rate) > 0.1:
        raise ValueError(f"unexpected {width}x{height}@{rate} timing")
    pos = PRIMARY.find_range_descriptor(candidate)
    if timing.pixel_clock_hz > candidate[pos + 9] * 10_000_000:
        raise ValueError("preferred pixel clock exceeds the range limit")
    return candidate, timing


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    source = args.input.read_bytes()
    args.output.mkdir(parents=True, exist_ok=True)
    manifest = []
    for height in sorted(PREFERRED):
        data, timing = build_profile(source, height)
        target = args.output / f"NanoKVM-monitor-{height}.bin"
        target.write_bytes(data)
        manifest.append(
            {
                "file": target.name,
                "width": timing.width,
                "height": timing.height,
                "refresh_hz": round(timing.refresh_hz, 6),
                "source_sha256": sha256(source),
                "sha256": sha256(data),
                "only_mode_at_or_above_resolution": True,
            }
        )
    for height, rates in sorted(RATES.items()):
        for rate in rates:
            data, timing = build_rate_profile(source, height, rate)
            target = args.output / f"NanoKVM-monitor-{height}-{rate}.bin"
            target.write_bytes(data)
            manifest.append(
                {
                    "file": target.name,
                    "width": timing.width,
                    "height": timing.height,
                    "refresh_hz": round(timing.refresh_hz, 6),
                    "source_sha256": sha256(source),
                    "sha256": sha256(data),
                    "only_mode_at_or_above_resolution": True,
                }
            )
    uhd_data, uhd_timing = build_uhd_profile(source)
    uhd_target = args.output / "NanoKVM-monitor-2160.bin"
    uhd_target.write_bytes(uhd_data)
    manifest.append(
        {
            "file": uhd_target.name,
            "width": uhd_timing.width,
            "height": uhd_timing.height,
            "refresh_hz": round(uhd_timing.refresh_hz, 6),
            "source_sha256": sha256(source),
            "sha256": sha256(uhd_data),
            "removed_cta_mode": "1280x720@60 DTD (CTA VIC 4 remains)",
            "added_mode": "3840x2160@30 preferred",
            "only_mode_at_or_above_resolution": True,
        }
    )
    # Auto is 1920x1080 at 100 Hz; QHD and UHD are explicit choices.
    # The runtime already resolves monitor value 0 to
    # NanoKVM-final-video-profiles.bin; the package install step maps this
    # generated Auto file to that stable runtime name.
    auto_data, auto_timing = build_rate_profile(source, 1080, 100)
    auto_target = args.output / "NanoKVM-monitor-auto.bin"
    auto_target.write_bytes(auto_data)
    manifest.append(
        {
            "file": auto_target.name,
            "profile": "automatic",
            "width": auto_timing.width,
            "height": auto_timing.height,
            "refresh_hz": round(auto_timing.refresh_hz, 6),
            "source_sha256": sha256(source),
            "sha256": sha256(auto_data),
            "identical_to": "NanoKVM-monitor-1080-100.bin",
            "only_mode_at_or_above_resolution": True,
        }
    )
    (args.output / "final-monitor-profiles.json").write_text(
        json.dumps(manifest, indent=2) + "\n"
    )
    print(json.dumps(manifest, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
