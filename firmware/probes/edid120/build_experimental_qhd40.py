#!/usr/bin/env python3
"""Build and validate the optional 2560x1440 approximately-40-Hz EDID.

The input is the verified VIC47 candidate.  This script only writes the
explicitly named generated output and manifest; it never touches hardware or
changes the existing candidate.
"""

from __future__ import annotations

import argparse
from dataclasses import asdict, dataclass
import hashlib
import importlib.util
import json
from pathlib import Path
import sys


HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location(
    "edid120_qhd40_primary_builder", HERE / "build_experimental_edid.py"
)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cannot load the primary EDID builder")
PRIMARY = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PRIMARY
SPEC.loader.exec_module(PRIMARY)


PROFILE_NAME = "experimental-qhd40"
EXPECTED_SOURCE_SHA256 = (
    "cb9d3e25bc8906d03d462ab80b932352ec72b21add9e89e80dd13e4688a6ec0f"
)
EXPECTED_SOURCE_DTD_COUNT = 4
QHD40_DTD = bytes.fromhex(
    "f13e00a0a0a0295030203500dc0c1100001a"
)


@dataclass(frozen=True)
class Qhd40Timing:
    width: int
    height: int
    pixel_clock_hz: int
    h_total: int
    v_total: int
    hfront: int
    hsync: int
    vfront: int
    vsync: int
    flags: int

    @property
    def refresh_hz(self) -> float:
        return self.pixel_clock_hz / (self.h_total * self.v_total)

    def summary(self) -> dict[str, object]:
        result = asdict(self)
        result.update(
            hblank=self.h_total - self.width,
            vblank=self.v_total - self.height,
            refresh_hz=round(self.refresh_hz, 6),
        )
        return result


EXPECTED_QHD40 = Qhd40Timing(
    width=2560,
    height=1440,
    pixel_clock_hz=161_130_000,
    h_total=2720,
    v_total=1481,
    hfront=48,
    hsync=32,
    vfront=3,
    vsync=5,
    flags=0x1A,
)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def validate_source(source: bytes) -> None:
    if sha256(source) != EXPECTED_SOURCE_SHA256:
        raise ValueError(
            "source must be the verified VIC47 candidate: "
            f"got {sha256(source)}, expected {EXPECTED_SOURCE_SHA256}"
        )
    PRIMARY.validate_candidate(source)
    _, dtd_start = PRIMARY.parse_cta_blocks(source)
    dtds = PRIMARY.parse_cta_dtds(source, dtd_start)
    if len(dtds) != EXPECTED_SOURCE_DTD_COUNT:
        raise ValueError(f"expected four source CTA DTDs, found {len(dtds)}")


def build_candidate(source: bytes) -> bytes:
    validate_source(source)
    blocks, source_dtd_start = PRIMARY.parse_cta_blocks(source)
    source_dtds = PRIMARY.parse_cta_dtds(source, source_dtd_start)
    dtd_start = 128 + source[130]
    new_dtd_pos = dtd_start + 18 * len(source_dtds)
    if new_dtd_pos + 18 > 255:
        raise ValueError("no CTA DTD slot remains for QHD40")

    candidate = bytearray(source)
    candidate[new_dtd_pos : new_dtd_pos + 18] = QHD40_DTD
    for offset in (0, 128):
        candidate[offset + 127] = (-sum(candidate[offset : offset + 127])) & 0xFF
    validate_candidate(bytes(candidate), source=source)
    return bytes(candidate)


def validate_candidate(candidate: bytes, *, source: bytes | None = None) -> None:
    PRIMARY.check_basic_edid(candidate, label="candidate")
    if candidate[130] != 18:
        raise ValueError(f"candidate CTA DTD offset changed to {candidate[130]}")
    base = PRIMARY.decode_dtd(candidate[54:72], label="candidate preferred DTD")
    if (base.width, base.height) != (2560, 1440) or abs(base.refresh_hz - 30.0) > 0.2:
        raise ValueError("candidate lost the preferred QHD30 timing")
    blocks, dtd_start = PRIMARY.parse_cta_blocks(candidate)
    video_blocks = [payload for tag, payload in blocks if tag == 2]
    expected_video = bytes(PRIMARY.EXPECTED_VIDEO_SVDS + (PRIMARY.CTA_VIC_720P120,))
    if video_blocks != [expected_video]:
        raise ValueError("candidate changed the existing VIC list")
    vendor_blocks = [bytes(((tag << 5) | len(payload),)) + payload
                     for tag, payload in blocks if tag == 3]
    if vendor_blocks != [PRIMARY.EXPECTED_VENDOR_BLOCK]:
        raise ValueError("candidate changed the HDMI vendor block")

    dtds = PRIMARY.parse_cta_dtds(candidate, dtd_start)
    expected_count = EXPECTED_SOURCE_DTD_COUNT + 1 if source is not None else 5
    if len(dtds) != expected_count:
        raise ValueError(f"expected {expected_count} CTA DTDs, found {len(dtds)}")
    if source is not None:
        validate_source(source)
        source_blocks, source_dtd_start = PRIMARY.parse_cta_blocks(source)
        if [(tag, payload) for tag, payload in blocks] != [
            (tag, payload) for tag, payload in source_blocks
        ]:
            raise ValueError("candidate changed an existing CTA data block")
        source_dtds = PRIMARY.parse_cta_dtds(source, source_dtd_start)
        if [raw for _, raw, _ in dtds[: len(source_dtds)]] != [
            raw for _, raw, _ in source_dtds
        ]:
            raise ValueError("candidate changed an existing CTA DTD")
        if candidate[:255] != source[:255]:
            # The source and candidate share every byte except the appended
            # DTD and CTA checksum.  This explicit diff guard catches accidental
            # changes while allowing the new DTD slot.
            allowed = set(range(dtd_start + 18 * len(source_dtds),
                                dtd_start + 18 * (len(source_dtds) + 1))) | {255}
            changed = {i for i, (before, after) in enumerate(zip(source, candidate))
                       if before != after}
            if changed - allowed:
                raise ValueError(f"candidate changed unrelated bytes: {sorted(changed - allowed)}")

    qhd40 = dtds[-1]
    if qhd40[1] != QHD40_DTD:
        raise ValueError("candidate QHD40 DTD bytes differ from the fixture")
    timing = qhd40[2]
    if (timing.width, timing.height, timing.pixel_clock_hz, timing.htotal, timing.vtotal,
            timing.hfront, timing.hsync, timing.vfront, timing.vsync, timing.flags) != (
                EXPECTED_QHD40.width, EXPECTED_QHD40.height,
                EXPECTED_QHD40.pixel_clock_hz, EXPECTED_QHD40.h_total,
                EXPECTED_QHD40.v_total, EXPECTED_QHD40.hfront, EXPECTED_QHD40.hsync,
                EXPECTED_QHD40.vfront, EXPECTED_QHD40.vsync, EXPECTED_QHD40.flags):
        raise ValueError("candidate QHD40 DTD geometry/clock is wrong")
    if abs(timing.refresh_hz - 40.0) > 0.02:
        raise ValueError(f"candidate QHD40 DTD reports {timing.refresh_hz:.6f} Hz")
    range_pos = PRIMARY.find_range_descriptor(candidate)
    if candidate[range_pos + 9] * 10 < 162:
        raise ValueError("candidate range pixel-clock limit does not cover QHD40")


def mode_list(candidate: bytes) -> list[dict[str, object]]:
    _, dtd_start = PRIMARY.parse_cta_blocks(candidate)
    modes = [PRIMARY.decode_dtd(candidate[54:72], label="preferred DTD")]
    modes.extend(timing for _, _, timing in PRIMARY.parse_cta_dtds(candidate, dtd_start))
    return [timing.summary() for timing in modes]


def manifest(source: bytes, candidate: bytes, *, output_name: str) -> dict[str, object]:
    blocks, dtd_start = PRIMARY.parse_cta_blocks(candidate)
    svds = next(payload for tag, payload in blocks if tag == 2)
    source_dtd_start = 128 + source[130]
    source_dtds = PRIMARY.parse_cta_dtds(source, source_dtd_start)
    dtd_pos = dtd_start + 18 * len(source_dtds)
    changed = [i for i, (before, after) in enumerate(zip(source, candidate)) if before != after]
    return {
        "profile": PROFILE_NAME,
        "experimental": True,
        "hardware_access": False,
        "source": {
            "file": "NanoKVM-experimental-720p120.bin",
            "sha256": sha256(source),
            "unchanged": True,
        },
        "candidate": {
            "file": output_name,
            "sha256": sha256(candidate),
            "bytes": len(candidate),
            "checksums_valid": all(sum(candidate[i : i + 128]) % 256 == 0 for i in (0, 128)),
        },
        "preferred": {
            "width": 2560,
            "height": 1440,
            "refresh_hz": round(PRIMARY.decode_dtd(candidate[54:72], label="preferred").refresh_hz, 6),
            "preserved": candidate[54:72] == source[54:72],
        },
        "cta": {
            "revision": candidate[129],
            "dtd_offset": candidate[130],
            "native_dtd_count": candidate[131] & 0x0F,
            "video_identification_codes": list(svds),
            "appended_dtd": {
                "offset": dtd_pos,
                "timing": EXPECTED_QHD40.summary(),
                "nominal_refresh_hz": 40.0,
            },
        },
        "modes": mode_list(candidate),
        "changed_bytes": changed,
        "notes": [
            "Generated for offline review; no EDID was programmed.",
            "The QHD30 preferred DTD, VIC47 720p120 DTD, FHD60 and 720p60 DTDs remain present.",
            "The 161.13 MHz reduced-blanking clock reports 39.9993 Hz with 2720x1481 totals.",
            "Actual receiver/source acceptance and QHD40 capture performance remain unqualified.",
            "SG2002 5M30 documentation does not guarantee QHD40; H.265 is the preferred initial trial codec.",
        ],
    }


def print_summary(candidate: bytes, *, source_digest: str | None = None) -> None:
    if source_digest:
        print(f"source_sha256: {source_digest}")
    print(f"candidate_sha256: {sha256(candidate)}")
    for mode in mode_list(candidate):
        print(
            "  {width}x{height}: {refresh_hz:.6f} Hz, clock {pixel_clock_hz} Hz, "
            "totals {htotal}x{vtotal}".format(**mode)
        )
    blocks, dtd_start = PRIMARY.parse_cta_blocks(candidate)
    svds = next(payload for tag, payload in blocks if tag == 2)
    print(f"cta_vics: {','.join(str(vic) for vic in svds)}")
    print(f"cta_dtd_offset: {dtd_start - 128}")
    print(f"checksums: {[sum(candidate[i:i + 128]) % 256 for i in (0, 128)]}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, help="verified VIC47 candidate")
    parser.add_argument("--output", type=Path, help="new candidate .bin path")
    parser.add_argument("--force", action="store_true")
    parser.add_argument("--check", type=Path, help="check an existing candidate")
    parser.add_argument("--source", type=Path, help="VIC47 candidate for preservation checks")
    args = parser.parse_args()
    if args.check:
        candidate = args.check.read_bytes()
        source = args.source.read_bytes() if args.source else None
        validate_candidate(candidate, source=source)
        print_summary(candidate, source_digest=sha256(source) if source else None)
        print("result: PASS (offline EDID validation; no hardware access)")
        return 0
    if not args.input or not args.output:
        raise SystemExit("--input and --output are required unless --check is used")
    source = args.input.read_bytes()
    candidate = build_candidate(source)
    if args.output.exists() and not args.force:
        raise SystemExit(f"output exists; pass --force to regenerate: {args.output}")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(candidate)
    manifest_path = args.output.with_suffix(".json")
    manifest_path.write_text(
        json.dumps(manifest(source, candidate, output_name=args.output.name), indent=2) + "\n"
    )
    print_summary(candidate, source_digest=sha256(source))
    print(f"manifest: {manifest_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
