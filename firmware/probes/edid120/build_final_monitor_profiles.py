#!/usr/bin/env python3
"""Create final-mode EDIDs: one per monitor resolution and refresh rate.

NanoKVM-monitor-<height>-<hz>.bin prefers that mode; NanoKVM-monitor-<height>.bin
is the fastest rate of a resolution, as before. The server picks the slowest
rate that is not below the stream frame rate, so the source renders and sends
no more frames than are streamed.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
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


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def build_profile(source: bytes, height: int) -> tuple[bytes, object]:
    if sha256(source) != EXPECTED_SOURCE_SHA256:
        raise ValueError("input is not the accepted final EDID")
    PRIMARY.check_basic_edid(source, label="final source")
    _, dtd_start = PRIMARY.parse_cta_blocks(source)
    dtds = PRIMARY.parse_cta_dtds(source, dtd_start)
    # Replace the duplicate FHD60 descriptor, retaining every unique fallback.
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
    candidate[24] = (candidate[24] | 2) & ~1
    candidate[54:72] = raw
    candidate[127] = (-sum(candidate[:127])) & 0xFF
    PRIMARY.check_basic_edid(candidate, label=f"monitor-{height}")
    preferred = PRIMARY.decode_dtd(candidate[54:72], label="preferred")
    if preferred != timing:
        raise ValueError("preferred DTD differs from accepted CTA timing")
    return bytes(candidate), preferred


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
    """UHD30 preferred; every other final mode stays as a CTA fallback.

    UHD30 is the base-block preferred DTD; the QHD50 it displaces stays in
    the CTA list. The CTA block is full, so the 720p60 DTD (also CTA VIC 4)
    makes room for a Max_TMDS_Clock byte in the HDMI vendor block. The range
    limit is raised from 250 to 270 MHz to admit 262.75 MHz.
    """
    if sha256(source) != EXPECTED_SOURCE_SHA256:
        raise ValueError("input is not the accepted final EDID")
    qhd, _ = build_profile(source, 1440)
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
# Video Identification Codes of the final CTA video block: (width, height, Hz).
VIC_MODES = {1: (640, 480, 60), 4: (1280, 720, 60), 16: (1920, 1080, 60), 19: (1280, 720, 50),
             20: (1920, 1080, 50), 31: (1920, 1080, 50), 47: (1280, 720, 120)}
STD_ASPECT = {0: (16, 10), 1: (4, 3), 2: (5, 4), 3: (16, 9)}


def drop_faster_modes(data: bytes, width: int, height: int, rate: int) -> bytes:
    """Remove every mode of width x height faster than rate: standard timings,
    CTA video codes and CTA DTDs. Other resolutions stay as fallbacks."""
    def faster(w, h, hz):
        return (w, h) == (width, height) and hz > rate + 0.1

    out = bytearray(data)
    for pos in range(38, 54, 2):
        a, b = out[pos], out[pos + 1]
        if (a, b) == (1, 1):
            continue
        w = (a + 31) * 8
        num, den = STD_ASPECT[b >> 6]
        if faster(w, w * den // num, (b & 63) + 60):
            out[pos:pos + 2] = b"\x01\x01"
    out[127] = (-sum(out[:127])) & 0xFF
    blocks, start = PRIMARY.parse_cta_blocks(data)
    cta = bytearray()
    for tag, payload in blocks:
        if tag == 2:
            unknown = [v & 0x7F for v in payload if (v & 0x7F) not in VIC_MODES]
            if unknown:
                raise ValueError(f"review CTA video codes {unknown}")
            payload = bytes(v for v in payload if not faster(*VIC_MODES[v & 0x7F]))
        cta += bytes([(tag << 5) | len(payload)]) + payload
    ext = bytearray(128)
    ext[:4] = bytes([2, 3, 4 + len(cta), data[131]])
    ext[4:4 + len(cta)] = cta
    offset = 4 + len(cta)
    for _, raw, t in PRIMARY.parse_cta_dtds(data, start):
        if faster(t.width, t.height, t.refresh_hz):
            continue
        ext[offset:offset + 18] = raw
        offset += 18
    ext[127] = (-sum(ext[:127])) & 0xFF
    return bytes(out[:128] + ext)


def build_rate_profile(source: bytes, height: int, rate: int) -> tuple[bytes, object]:
    """The default-rate profile of a height with another preferred timing."""
    if height == 2160:
        if rate != 30:
            raise ValueError("3840x2160 is offered at 30 Hz only")
        return build_uhd_profile(source)
    data, default = build_profile(source, height)
    if abs(default.refresh_hz - rate) < 0.1:
        return data, default
    width = WIDTHS[height]
    _, start = PRIMARY.parse_cta_blocks(data)
    listed = [raw for _, raw, t in PRIMARY.parse_cta_dtds(data, start)
              if (t.width, t.height) == (width, height) and abs(t.refresh_hz - rate) < 0.1]
    if len(listed) > 1:
        raise ValueError(f"duplicate {width}x{height}@{rate} DTDs")
    raw = listed[0] if listed else encode_dtd(EXTRA_TIMINGS[(width, height, rate)], data[66:71])
    out = bytearray(data)
    out[54:72] = raw
    candidate = drop_faster_modes(bytes(out), width, height, rate)
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
    profiles: dict[int, tuple[bytes, object]] = {}
    for height in sorted(PREFERRED):
        data, timing = build_profile(source, height)
        profiles[height] = (data, timing)
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
                "unique_cta_modes_preserved": True,
            "added_cta_mode": "2560x1440@50",
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
        }
    )
    # Auto is 1920x1080 at 100 Hz; QHD and UHD are explicit choices.
    # profile.  The runtime already resolves monitor value 0 to
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
            "unique_cta_modes_preserved": True,
            "added_cta_mode": "2560x1440@50",
        }
    )
    (args.output / "final-monitor-profiles.json").write_text(
        json.dumps(manifest, indent=2) + "\n"
    )
    print(json.dumps(manifest, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
