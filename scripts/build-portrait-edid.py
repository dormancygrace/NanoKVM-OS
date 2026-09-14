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
AVC_MODE = (1296, 2304, 171000, 160, 48, 32, 45, 3, 10, 0x1a)
MAX_MODE = (1440, 2560, 166720, 160, 48, 32, 45, 3, 10, 0x1a)

def profile(source, mode=MODE):
    data = bytearray(builder.profile(source, mode))
    height_mm = 530
    data[21:23] = bytes((30, height_mm // 10))
    data[66:69] = bytes((300 & 255, height_mm & 255, (300 >> 8) << 4 | (height_mm >> 8)))
    data[127] = (-sum(data[:127])) & 255
    return bytes(data)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--profile", choices=("hd", "fhd", "h264", "max"), default="fhd")
    args = parser.parse_args()
    data = profile(args.input.read_bytes(), {"hd": HD_MODE, "fhd": MODE, "h264": AVC_MODE, "max": MAX_MODE}[args.profile])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(data)
    print(f"{hashlib.sha256(data).hexdigest()}  {args.output}")

if __name__ == "__main__":
    main()
