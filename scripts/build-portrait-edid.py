#!/usr/bin/env python3
"""Build qualified HDMI portrait profiles, without hardware access."""
import argparse
import hashlib
import importlib.util
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("monitor_edids", HERE / "build-monitor-edids.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
# Keep the established legacy fallback, but no landscape QHD/high-rate DTDs.
builder.MODES = builder.MODES[:1]
MODE = (1080, 1920, 183680, 160, 48, 32, 55, 3, 10, 0x1a)
HD_MODE = (720, 1280, 139080, 160, 48, 32, 37, 3, 10, 0x1a)
MAX_MODE = (1440, 2560, 208400, 160, 48, 32, 45, 3, 10, 0x1a)
LEGACY = {(720, 1280): (120, HD_MODE), (1080, 1920): (75, MODE),
          (1440, 2560): (50, MAX_MODE)}
# Refresh rates of each portrait profile, fastest first, as landscape: the
# monitor follows the stream frame rate (server/common/monitor_rate.go).
RATES = {1280: (120, 60, 30), 1920: (90, 75, 60, 30), 2560: (60, 50, 30)}
WIDTHS = {1280: 720, 1920: 1080, 2560: 1440}


def rate_mode(width, height, rate):
    """The established timing at its own rate, else CVT reduced blanking with
    the portrait vertical sync width of the established profiles."""
    legacy_rate, legacy = LEGACY[(width, height)]
    if rate == legacy_rate:
        return legacy
    htotal = width + 160
    vtotal = height + 14
    while (vtotal - height) * 1e6 / (rate * vtotal) < 460:
        vtotal += 1
    clock_khz = htotal * vtotal * rate // 250 * 250 // 1000
    return (width, height, clock_khz, 160, 48, 32, vtotal - height, 3, 10, 0x1a)

def profile(source, mode=MODE, rate=None):
    """The portrait profile of mode; rate gives it the monitor identity of its
    rate profile (see with_identity in build_final_monitor_profiles.py:
    Windows reapplies the mode last used with an identity), by default the
    rate of the established profile of that size."""
    data = bytearray(builder.profile(source, mode))
    height_mm = 530
    data[21:23] = bytes((30, height_mm // 10))
    data[66:69] = bytes((300 & 255, height_mm & 255, (300 >> 8) << 4 | (height_mm >> 8)))
    width, height = mode[0], mode[1]
    if rate is None:
        rate = LEGACY[(width, height)][0]
    data[12:16] = (height * 100 + rate).to_bytes(4, "little")
    for pos in range(54, 126, 18):
        if data[pos:pos + 4] == b"\0\0\0\xff":
            data[pos + 5:pos + 18] = f"NKVM{height}P{rate}\n".encode("ascii").ljust(13, b" ")
    data[127] = (-sum(data[:127])) & 255
    return bytes(data)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--profile", choices=("hd", "fhd", "max"), default="fhd")
    parser.add_argument("--rates", action="store_true",
                        help="write NanoKVM-portrait-<w>x<h>-<hz>.bin for every profile into --output")
    args = parser.parse_args()
    if args.rates:
        args.output.mkdir(parents=True, exist_ok=True)
        source = args.input.read_bytes()
        for height, rates in RATES.items():
            for rate in rates:
                width = WIDTHS[height]
                data = profile(source, rate_mode(width, height, rate), rate)
                target = args.output / f"NanoKVM-portrait-{width}x{height}-{rate}.bin"
                target.write_bytes(data)
                print(f"{hashlib.sha256(data).hexdigest()}  {target}")
        return
    data = profile(args.input.read_bytes(), {"hd": HD_MODE, "fhd": MODE, "max": MAX_MODE}[args.profile])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(data)
    print(f"{hashlib.sha256(data).hexdigest()}  {args.output}")

if __name__ == "__main__":
    main()
