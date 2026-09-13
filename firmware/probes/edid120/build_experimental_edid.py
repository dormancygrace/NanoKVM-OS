#!/usr/bin/env python3
"""Build and validate the uninstalled NanoKVM 720p120 EDID experiment.

This script only transforms a supplied 256-byte EDID file.  It never opens a
device, invokes the EDID programming helper, or changes a packaged profile.
The input is pinned to the currently packaged QHD30 EDID by SHA-256 so that a
regeneration cannot silently use a different monitor identity or mode set.
"""

from __future__ import annotations

import argparse
from dataclasses import asdict, dataclass
import hashlib
import json
from pathlib import Path


PROFILE_NAME = "experimental-720p120"
EXPECTED_SOURCE_SHA256 = (
    "ba66e441dc6fb8902f02b8b9a2163689a7983854b9e9487685ec9b467e685d09"
)
EXPECTED_VIDEO_SVDS = (4, 31, 20, 19, 1, 16)
CTA_VIC_720P120 = 47
CTA_720P120_DTD = bytes.fromhex(
    "023a007251d01e206e285500dc0c1100001e"
)
EXPECTED_VENDOR_BLOCK = bytes.fromhex("65030c001000")


@dataclass(frozen=True)
class Timing:
    width: int
    height: int
    pixel_clock_hz: int
    hblank: int
    vblank: int
    hfront: int
    hsync: int
    vfront: int
    vsync: int
    flags: int

    @property
    def htotal(self) -> int:
        return self.width + self.hblank

    @property
    def vtotal(self) -> int:
        return self.height + self.vblank

    @property
    def refresh_hz(self) -> float:
        return self.pixel_clock_hz / (self.htotal * self.vtotal)

    def summary(self) -> dict[str, object]:
        result = asdict(self)
        result.update(
            htotal=self.htotal,
            vtotal=self.vtotal,
            refresh_hz=round(self.refresh_hz, 6),
        )
        return result


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def check_basic_edid(data: bytes, *, label: str) -> None:
    if len(data) != 256:
        raise ValueError(f"{label}: expected exactly 256 bytes, got {len(data)}")
    if data[:8] != bytes.fromhex("00ffffffffffff00"):
        raise ValueError(f"{label}: invalid EDID header")
    if data[126] != 1:
        raise ValueError(f"{label}: expected exactly one extension block")
    for offset in (0, 128):
        if sum(data[offset : offset + 128]) % 256:
            raise ValueError(f"{label}: block at 0x{offset:02x} has bad checksum")


def decode_dtd(raw: bytes, *, label: str) -> Timing:
    if len(raw) != 18:
        raise ValueError(f"{label}: DTD must be 18 bytes")
    pixel_clock_hz = int.from_bytes(raw[:2], "little") * 10_000
    if not pixel_clock_hz:
        raise ValueError(f"{label}: DTD has no pixel clock")
    width = raw[2] | ((raw[4] & 0xF0) << 4)
    hblank = raw[3] | ((raw[4] & 0x0F) << 8)
    height = raw[5] | ((raw[7] & 0xF0) << 4)
    vblank = raw[6] | ((raw[7] & 0x0F) << 8)
    hfront = raw[8] | ((raw[11] & 0xC0) << 2)
    hsync = raw[9] | ((raw[11] & 0x30) << 4)
    vfront = (raw[10] >> 4) | ((raw[11] & 0x0C) << 2)
    vsync = (raw[10] & 0x0F) | ((raw[11] & 0x03) << 4)
    return Timing(
        width=width,
        height=height,
        pixel_clock_hz=pixel_clock_hz,
        hblank=hblank,
        vblank=vblank,
        hfront=hfront,
        hsync=hsync,
        vfront=vfront,
        vsync=vsync,
        flags=raw[17],
    )


def parse_cta_blocks(data: bytes) -> tuple[list[tuple[int, bytes]], int]:
    if data[128:130] != bytes((2, 3)):
        raise ValueError("expected CTA extension revision 3")
    dtd_offset = data[130]
    if not 4 <= dtd_offset <= 127:
        raise ValueError(f"invalid CTA DTD offset {dtd_offset}")
    pos = 132
    end = 128 + dtd_offset
    blocks: list[tuple[int, bytes]] = []
    while pos < end:
        header = data[pos]
        length = header & 0x1F
        stop = pos + 1 + length
        if stop > end:
            raise ValueError("CTA data block crosses the DTD offset")
        blocks.append((header >> 5, bytes(data[pos + 1 : stop])))
        pos = stop
    if pos != end:
        raise ValueError("CTA data block parsing did not end at DTD offset")
    return blocks, 128 + dtd_offset


def parse_cta_dtds(data: bytes, start: int) -> list[tuple[int, bytes, Timing]]:
    result: list[tuple[int, bytes, Timing]] = []
    pos = start
    while pos < 255:
        if pos + 18 > 255:
            if any(data[pos:255]):
                raise ValueError("non-zero partial CTA DTD before checksum")
            break
        raw = bytes(data[pos : pos + 18])
        if not any(raw):
            if any(data[pos:255]):
                raise ValueError("non-zero CTA data follows an empty DTD slot")
            break
        result.append((pos, raw, decode_dtd(raw, label=f"CTA DTD 0x{pos:02x}")))
        pos += 18
    return result


def find_range_descriptor(data: bytes) -> int:
    matches = [
        pos
        for pos in range(54, 126, 18)
        if data[pos : pos + 5] == bytes.fromhex("000000fd00")
    ]
    if len(matches) != 1:
        raise ValueError(f"expected one monitor-range descriptor, found {len(matches)}")
    return matches[0]


def validate_source(source: bytes) -> None:
    check_basic_edid(source, label="source")
    digest = sha256(source)
    if digest != EXPECTED_SOURCE_SHA256:
        raise ValueError(
            "source SHA-256 does not match packaged NanoKVM-QHD30.bin: "
            f"got {digest}, expected {EXPECTED_SOURCE_SHA256}"
        )
    if source[130] != 17:
        raise ValueError("source QHD30 CTA DTD offset changed from 17")
    base = decode_dtd(source[54:72], label="source preferred DTD")
    if (base.width, base.height) != (2560, 1440):
        raise ValueError("source preferred DTD is not QHD")
    if not 29.7 <= base.refresh_hz <= 30.2:
        raise ValueError(f"source preferred DTD is not approximately 30 Hz: {base.refresh_hz:.6f}")
    blocks, dtd_start = parse_cta_blocks(source)
    video_blocks = [payload for tag, payload in blocks if tag == 2]
    if video_blocks != [bytes(EXPECTED_VIDEO_SVDS)]:
        raise ValueError(f"unexpected source CTA video block(s): {video_blocks!r}")
    vendor_blocks = [bytes(((tag << 5) | len(payload),)) + payload
                     for tag, payload in blocks if tag == 3]
    if vendor_blocks != [EXPECTED_VENDOR_BLOCK]:
        raise ValueError("source HDMI vendor block differs from packaged profile")
    dtds = parse_cta_dtds(source, dtd_start)
    if len(dtds) != 3:
        raise ValueError(f"expected three source CTA DTDs, found {len(dtds)}")
    expected_sizes = [(1920, 1080), (1280, 720), (1920, 1080)]
    if [(timing.width, timing.height) for _, _, timing in dtds] != expected_sizes:
        raise ValueError("source QHD30 CTA DTD order changed")
    range_pos = find_range_descriptor(source)
    if (source[range_pos + 5], source[range_pos + 6], source[range_pos + 7],
            source[range_pos + 8], source[range_pos + 9]) != (29, 76, 30, 83, 17):
        raise ValueError("source QHD30 range limits changed")


def build_candidate(source: bytes) -> bytes:
    validate_source(source)
    blocks, source_dtd_start = parse_cta_blocks(source)
    candidate_blocks: list[tuple[int, bytes]] = []
    video_seen = False
    for tag, payload in blocks:
        if tag == 2:
            if video_seen:
                raise ValueError("source has multiple CTA video blocks")
            video_seen = True
            if CTA_VIC_720P120 in payload:
                raise ValueError("source already advertises CTA VIC 47")
            candidate_blocks.append((tag, payload + bytes((CTA_VIC_720P120,))))
        else:
            candidate_blocks.append((tag, payload))
    if not video_seen:
        raise ValueError("source has no CTA video block")

    candidate_dtd_offset = source[130] + 1
    if candidate_dtd_offset > 127:
        raise ValueError("adding VIC 47 would overflow CTA data-block space")
    source_dtds = parse_cta_dtds(source, source_dtd_start)
    candidate_dtd_start = 128 + candidate_dtd_offset
    new_dtd_pos = candidate_dtd_start + 18 * len(source_dtds)
    if new_dtd_pos + 18 > 255:
        raise ValueError("no CTA DTD slot remains for 720p120")

    candidate = bytearray(source)
    # Preserve the base block, including its preferred QHD DTD and all identity
    # descriptors; only range limits need to describe the new 90 kHz/120 Hz mode.
    range_pos = find_range_descriptor(candidate)
    candidate[range_pos + 6] = 120  # maximum vertical rate, Hz
    candidate[range_pos + 8] = 90   # maximum horizontal rate, kHz

    # Rebuild only CTA data-block/DTD storage.  The original DTDs move one byte
    # because the video block gained one SVD; their bytes remain unchanged.
    candidate[128:256] = bytes(128)
    candidate[128:132] = source[128:132]
    candidate[130] = candidate_dtd_offset
    pos = 132
    for tag, payload in candidate_blocks:
        if len(payload) > 31:
            raise ValueError("CTA data block payload is too long")
        candidate[pos] = (tag << 5) | len(payload)
        candidate[pos + 1 : pos + 1 + len(payload)] = payload
        pos += 1 + len(payload)
    if pos != candidate_dtd_start:
        raise ValueError("candidate CTA DTD offset does not match data blocks")
    for _, raw, _ in source_dtds:
        candidate[pos : pos + 18] = raw
        pos += 18
    candidate[pos : pos + 18] = CTA_720P120_DTD

    for offset in (0, 128):
        candidate[offset + 127] = (-sum(candidate[offset : offset + 127])) & 0xFF
    validate_candidate(bytes(candidate), source=source)
    return bytes(candidate)


def validate_candidate(candidate: bytes, *, source: bytes | None = None) -> None:
    check_basic_edid(candidate, label="candidate")
    if candidate[24] & 0x02 == 0:
        raise ValueError("candidate lost the EDID preferred-timing flag")
    base = decode_dtd(candidate[54:72], label="candidate preferred DTD")
    if (base.width, base.height) != (2560, 1440) or not 29.7 <= base.refresh_hz <= 30.2:
        raise ValueError("candidate no longer prefers QHD at approximately 30 Hz")

    blocks, dtd_start = parse_cta_blocks(candidate)
    video_blocks = [payload for tag, payload in blocks if tag == 2]
    expected_video = bytes(EXPECTED_VIDEO_SVDS + (CTA_VIC_720P120,))
    if video_blocks != [expected_video]:
        raise ValueError(f"candidate CTA video block is {video_blocks!r}, expected {expected_video!r}")
    vendor_blocks = [bytes(((tag << 5) | len(payload),)) + payload
                     for tag, payload in blocks if tag == 3]
    if vendor_blocks != [EXPECTED_VENDOR_BLOCK]:
        raise ValueError("candidate HDMI vendor block changed")
    dtds = parse_cta_dtds(candidate, dtd_start)
    if len(dtds) != 4:
        raise ValueError(f"expected four candidate CTA DTDs, found {len(dtds)}")
    timings = [timing for _, _, timing in dtds]
    expected_sizes = [(1920, 1080), (1280, 720), (1920, 1080), (1280, 720)]
    if [(t.width, t.height) for t in timings] != expected_sizes:
        raise ValueError("candidate CTA DTD order is not FHD, 720p, FHD, 720p")
    if not all(abs(t.refresh_hz - 60.0) < 0.2 for t in timings[:3]):
        raise ValueError("candidate lost a 60 Hz compatibility DTD")
    high = timings[3]
    if bytes(dtds[3][1]) != CTA_720P120_DTD:
        raise ValueError("candidate 720p120 DTD bytes differ from CTA VIC 47 timing")
    if (high.width, high.height, high.pixel_clock_hz, high.htotal, high.vtotal) != (
        1280, 720, 148_500_000, 1650, 750
    ):
        raise ValueError("candidate 720p120 DTD geometry/clock is wrong")
    if abs(high.refresh_hz - 120.0) > 0.2:
        raise ValueError(f"candidate 720p120 DTD reports {high.refresh_hz:.6f} Hz")
    range_pos = find_range_descriptor(candidate)
    if (candidate[range_pos + 5], candidate[range_pos + 6], candidate[range_pos + 7],
            candidate[range_pos + 8], candidate[range_pos + 9]) != (29, 120, 30, 90, 17):
        raise ValueError("candidate range limits do not cover QHD30 through 720p120")

    if source is not None:
        validate_source(source)
        if candidate[54:72] != source[54:72] or candidate[24] != source[24]:
            raise ValueError("candidate changed the source preferred timing semantics")
        for index in range(127):
            if index not in (114, 116) and candidate[index] != source[index]:
                raise ValueError(f"candidate changed unrelated base-block byte {index}")
        source_blocks, source_dtd_start = parse_cta_blocks(source)
        source_nonvideo = [(tag, payload) for tag, payload in source_blocks if tag != 2]
        candidate_nonvideo = [(tag, payload) for tag, payload in blocks if tag != 2]
        if candidate_nonvideo != source_nonvideo:
            raise ValueError("candidate changed a non-video CTA data block")
        source_dtds = parse_cta_dtds(source, source_dtd_start)
        if [raw for _, raw, _ in dtds[: len(source_dtds)]] != [raw for _, raw, _ in source_dtds]:
            raise ValueError("candidate changed an existing CTA DTD")


def mode_list(candidate: bytes) -> list[dict[str, object]]:
    _, dtd_start = parse_cta_blocks(candidate)
    modes = [decode_dtd(candidate[54:72], label="preferred DTD")]
    modes.extend(timing for _, _, timing in parse_cta_dtds(candidate, dtd_start))
    return [timing.summary() for timing in modes]


def manifest(source: bytes, candidate: bytes, *, output_name: str) -> dict[str, object]:
    blocks, dtd_start = parse_cta_blocks(candidate)
    svds = next(payload for tag, payload in blocks if tag == 2)
    range_pos = find_range_descriptor(candidate)
    changed = [i for i, (before, after) in enumerate(zip(source, candidate)) if before != after]
    return {
        "profile": PROFILE_NAME,
        "experimental": True,
        "hardware_access": False,
        "source": {
            "file": "NanoKVM-QHD30.bin",
            "sha256": sha256(source),
            "preferred_dtd_preserved": source[54:72] == candidate[54:72],
        },
        "candidate": {
            "file": output_name,
            "sha256": sha256(candidate),
            "bytes": len(candidate),
            "checksums_valid": all(sum(candidate[i : i + 128]) % 256 == 0 for i in (0, 128)),
        },
        "cta": {
            "revision": candidate[129],
            "dtd_offset": candidate[130],
            "native_dtd_count": candidate[131] & 0x0F,
            "video_identification_codes": list(svds),
            "added_vic": CTA_VIC_720P120,
            "vic_47_standard_timing": {
                "width": 1280,
                "height": 720,
                "progressive": True,
                "pixel_clock_hz": 148_500_000,
                "h_total": 1650,
                "v_total": 750,
                "refresh_hz": 120.0,
                "reference": "CTA-861 VIC 47; cross-checked against Linux DRM CEA timing table",
            },
        },
        "range_limits": {
            "min_vertical_hz": candidate[range_pos + 5],
            "max_vertical_hz": candidate[range_pos + 6],
            "min_horizontal_khz": candidate[range_pos + 7],
            "max_horizontal_khz": candidate[range_pos + 8],
            "max_pixel_clock_mhz": candidate[range_pos + 9] * 10,
        },
        "modes": mode_list(candidate),
        "changed_bytes": changed,
        "notes": [
            "Generated for offline review; no EDID was programmed.",
            "The base preferred QHD30 DTD and CTA native-count semantics remain unchanged.",
            "Actual LT6911UXC acceptance, source mode discovery, and 120 Hz capture remain unqualified.",
        ],
    }


def print_summary(candidate: bytes, *, source_digest: str | None = None) -> None:
    modes = mode_list(candidate)
    print(f"profile: {PROFILE_NAME}")
    if source_digest:
        print(f"source_sha256: {source_digest}")
    print(f"candidate_sha256: {sha256(candidate)}")
    print("modes:")
    for mode in modes:
        print(
            "  {width}x{height}: {refresh_hz:.6f} Hz, "
            "clock {pixel_clock_hz} Hz, totals {htotal}x{vtotal}".format(**mode)
        )
    blocks, dtd_start = parse_cta_blocks(candidate)
    svds = next(payload for tag, payload in blocks if tag == 2)
    print(f"cta_vics: {','.join(str(vic) for vic in svds)}")
    print(f"cta_dtd_offset: {dtd_start - 128}")
    print(f"checksums: {[sum(candidate[i:i + 128]) % 256 for i in (0, 128)]}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, help="packaged NanoKVM-QHD30.bin used for regeneration")
    parser.add_argument("--output", type=Path, help="candidate .bin output path")
    parser.add_argument("--force", action="store_true", help="replace an existing generated output")
    parser.add_argument("--check", type=Path, help="check an existing candidate without writing")
    parser.add_argument("--source", type=Path, help="source QHD30 EDID for preservation checks with --check")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.check:
        candidate = args.check.read_bytes()
        source = args.source.read_bytes() if args.source else None
        validate_candidate(candidate, source=source)
        print_summary(candidate, source_digest=sha256(source) if source else None)
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
