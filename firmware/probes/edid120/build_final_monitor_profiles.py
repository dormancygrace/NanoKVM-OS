#!/usr/bin/env python3
"""Create final-mode EDIDs with 720p120, FHD75, or QHD50 preferred."""

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
    # Auto is deliberately the same byte sequence as the explicit QHD50
    # profile.  The runtime already resolves monitor value 0 to
    # NanoKVM-final-video-profiles.bin; the package install step maps this
    # generated Auto file to that stable runtime name.
    auto_data, auto_timing = profiles[1440]
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
            "identical_to": "NanoKVM-monitor-1440.bin",
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
