#!/usr/bin/env python3
"""Append the final FHD75 or diagnostic FHD70 DTD to the pinned QHD40 EDID."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import sys


HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location(
    "final_video_edid", HERE / "build_experimental_edid.py"
)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cannot load EDID validation helpers")
PRIMARY = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PRIMARY
SPEC.loader.exec_module(PRIMARY)

EXPECTED_SOURCE_SHA256 = (
    "3c3e11bd90dfd9a47f593f1cb5911301ad8590fa06635eac840d641611198c43"
)
EXPECTED_SOURCE_DTD_COUNT = 5
FHD_CLOCKS = {70: 162_930_000, 75: 174_500_000}


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def fhd_dtd(rate: int) -> bytes:
    clock_10khz = FHD_CLOCKS[rate] // 10_000
    # 1920x1080 active, 2080x1119 total, 48/32 and 3/5 porches.
    return (
        clock_10khz.to_bytes(2, "little")
        + bytes.fromhex("80a07038274030203500dc0c1100001a")
    )


def build(source: bytes, rate: int) -> bytes:
    if sha256(source) != EXPECTED_SOURCE_SHA256:
        raise ValueError("input is not the pinned QHD40 profile")
    PRIMARY.check_basic_edid(source, label="QHD40 source")
    _, dtd_start = PRIMARY.parse_cta_blocks(source)
    source_dtds = PRIMARY.parse_cta_dtds(source, dtd_start)
    if len(source_dtds) != EXPECTED_SOURCE_DTD_COUNT:
        raise ValueError("QHD40 source DTD layout changed")
    append_at = dtd_start + 18 * len(source_dtds)
    if append_at != 236 or any(source[append_at:255]):
        raise ValueError("terminal CTA DTD slot is unavailable")

    candidate = bytearray(source)
    range_pos = PRIMARY.find_range_descriptor(candidate)
    candidate[range_pos + 9] = 25  # 250 MHz covers FHD75 and QHD40.
    candidate[append_at : append_at + 18] = fhd_dtd(rate)
    for offset in (0, 128):
        candidate[offset + 127] = (-sum(candidate[offset : offset + 127])) & 0xFF

    PRIMARY.check_basic_edid(candidate, label=f"FHD{rate} candidate")
    _, candidate_dtd_start = PRIMARY.parse_cta_blocks(candidate)
    dtds = PRIMARY.parse_cta_dtds(candidate, candidate_dtd_start)
    if [raw for _, raw, _ in dtds[:-1]] != [raw for _, raw, _ in source_dtds]:
        raise ValueError("an existing timing changed")
    timing = dtds[-1][2]
    if (timing.width, timing.height, timing.htotal, timing.vtotal) != (
        1920, 1080, 2080, 1119
    ):
        raise ValueError("FHD geometry mismatch")
    if abs(timing.refresh_hz - rate) > 0.1:
        raise ValueError(f"refresh mismatch: {timing.refresh_hz}")
    return bytes(candidate)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--rate", type=int, choices=sorted(FHD_CLOCKS), required=True)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--force", action="store_true")
    args = parser.parse_args()
    if args.output.exists() and not args.force:
        raise SystemExit(f"output exists; pass --force to regenerate: {args.output}")
    source = args.input.read_bytes()
    candidate = build(source, args.rate)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(candidate)
    _, dtd_start = PRIMARY.parse_cta_blocks(candidate)
    timing = PRIMARY.parse_cta_dtds(candidate, dtd_start)[-1][2]
    manifest = {
        "profile": f"final-qhd40-720p120-fhd{args.rate}",
        "source": {"file": args.input.name, "sha256": sha256(source)},
        "candidate": {
            "file": args.output.name,
            "sha256": sha256(candidate),
            "bytes": len(candidate),
        },
        "timing": {
            "width": timing.width,
            "height": timing.height,
            "pixel_clock_hz": timing.pixel_clock_hz,
            "h_total": timing.htotal,
            "v_total": timing.vtotal,
            "refresh_hz": round(timing.refresh_hz, 6),
        },
        "preserved": ["QHD30", "QHD40", "FHD60", "720p60", "720p120"],
    }
    args.output.with_suffix(".json").write_text(
        json.dumps(manifest, indent=2) + "\n"
    )
    print(json.dumps(manifest, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
