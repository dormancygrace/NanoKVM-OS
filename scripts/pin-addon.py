#!/usr/bin/env python3
"""Pin a released add-on APK in firmware/alpine/addons.json.

The pin of the same add-on is replaced; the pins of the other add-ons stay.
OS builds use the pins as their snapshot of the add-ons; devices get add-on
updates from the repository index without waiting for the pin.
"""
import argparse
import hashlib
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import addon_packages  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('apk', type=Path, help='the released APK, NAME-VERSION-rN.apk')
    parser.add_argument('--manifest', type=Path, default=ROOT / 'firmware/alpine/addons.json')
    args = parser.parse_args()
    data = args.apk.read_bytes()
    tag = args.apk.name.removesuffix('.apk')
    entry = {'url': f'{addon_packages.RELEASES}{tag}/{args.apk.name}',
             'sha256': hashlib.sha256(data).hexdigest(), 'size': len(data)}
    args.manifest.write_text(addon_packages.pin(args.manifest.read_bytes(), args.apk.name, entry))


if __name__ == '__main__':
    main()
