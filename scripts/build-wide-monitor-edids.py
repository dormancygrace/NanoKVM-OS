#!/usr/bin/env python3
"""Build the 16:10 and 21:9 HDMI monitor profiles, without hardware access.

One file per size and refresh rate, NanoKVM-monitor-<w>x<h>-<hz>.bin
(server/common/monitor_profiles.go): a single preferred detailed timing in
CVT reduced blanking (v1), the 800x600 legacy fallback, the source's HDMI
identity and audio blocks. platform/build.sh adds the RGB range declaration
to every profile afterwards (scripts/edid-rgb-range.py).
"""
import argparse
import hashlib
import importlib.util
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("monitor_edids", HERE / "build-monitor-edids.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
# The legacy fallback only, no other landscape timing.
builder.MODES = builder.MODES[:1]

# (width, height): (vertical sync width of CVT for its aspect ratio, refresh
# rates fastest first). The rates match widerMonitorRates in
# server/common/monitor_profiles.go: 2560x1600 above 50 Hz and 3440x1440 above
# 40 Hz exceed what the capture and the encoder sustain.
PROFILES = {
    (1920, 1200): (6, (60, 50, 30)),
    (2560, 1600): (6, (50, 30)),
    (2560, 1080): (10, (60, 50, 30)),
    (3440, 1440): (10, (40, 30)),
}
# The highest pixel clock any of them may use (MHz), the LT6911UXC input limit
# the 16:9 profiles also respect.
MAX_CLOCK_MHZ = 270
WIDTH_MM = 600


def cvt_rb(width, height, rate, vsync):
    """CVT reduced blanking v1: horizontal blank 160 (front 48, sync 32), vertical
    front porch 3, back porch at least 6, at least 460 us of vertical blank,
    the pixel clock in 250 kHz steps."""
    htotal = width + 160
    vtotal = height + 3 + vsync + 6
    while (vtotal - height) * 1e6 / (rate * vtotal) < 460:
        vtotal += 1
    clock_khz = htotal * vtotal * rate // 250 * 250 // 1000
    return (width, height, clock_khz, 160, 48, 32, vtotal - height, 3, vsync, 0x1a)


def serial(width, height, rate):
    """The monitor serial: 16:9 profiles use height*100+rate (at most 216120),
    these 991000 + 1000 * their place in PROFILES + rate."""
    return 991000 + 1000 * list(PROFILES).index((width, height)) + rate


def profile(source, width, height, rate, vsync):
    mode = cvt_rb(width, height, rate, vsync)
    if mode[2] > MAX_CLOCK_MHZ * 1000:
        raise ValueError(f"{width}x{height}@{rate}: {mode[2] / 1000} MHz is above {MAX_CLOCK_MHZ} MHz")
    data = bytearray(builder.profile(source, mode))
    # The screen size follows the aspect ratio, 600 mm wide.
    height_mm = WIDTH_MM * height // width
    data[21:23] = bytes((WIDTH_MM // 10, height_mm // 10))
    data[66:69] = bytes((WIDTH_MM & 255, height_mm & 255, (WIDTH_MM >> 8) << 4 | (height_mm >> 8)))
    # Windows reapplies the mode last used with a monitor identity, so each
    # size and rate is its own monitor.
    data[12:16] = serial(width, height, rate).to_bytes(4, "little")
    for pos in range(54, 126, 18):
        if data[pos:pos + 4] == b"\0\0\0\xff":
            data[pos + 5:pos + 18] = f"NKVM{width}x{height}"[:12].encode("ascii").ljust(13, b" ")
    data[127] = (-sum(data[:127])) & 255
    return bytes(data)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--input", type=Path, required=True, help="the stock receiver EDID (E21_NanoKVM.bin)")
    parser.add_argument("--output", type=Path, required=True, help="directory for the profiles")
    args = parser.parse_args()
    source = args.input.read_bytes()
    args.output.mkdir(parents=True, exist_ok=True)
    for (width, height), (vsync, rates) in PROFILES.items():
        for rate in rates:
            data = profile(source, width, height, rate, vsync)
            target = args.output / f"NanoKVM-monitor-{width}x{height}-{rate}.bin"
            target.write_bytes(data)
            print(f"{hashlib.sha256(data).hexdigest()}  {target}")


if __name__ == "__main__":
    main()
