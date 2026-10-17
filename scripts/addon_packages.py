#!/usr/bin/env python3
"""Fetch checksum-pinned optional APKs, without installing or bundling them.

The release carries addons.json; the APKs remain in their own source releases.
Both release signing and Pages assembly use the same manifest and checks.
Core APK signatures are verified separately. Checksum-pinned unsigned add-ons
are authenticated on devices by the signed APK v3 index.
"""
import argparse
import hashlib
import json
import re
import subprocess
import urllib.request
from pathlib import Path


# Add-on releases: this repository's nanokvm-rustdesk-* releases (the RustDesk
# add-on workflow), and the separate packages repository that held them before.
SOURCES = ('https://github.com/dormancygrace/NanoKVM-OS/releases/download/nanokvm-rustdesk-',
           'https://github.com/dormancygrace/NanoKVM-OS-packages/releases/download/')


def manifest(data):
    packages = json.loads(data)['packages']
    for name, item in packages.items():
        if not re.fullmatch(r'[a-z0-9][a-z0-9+_.-]*[.]apk', name):
            raise ValueError(f'Invalid add-on filename: {name}')
        if not item['url'].startswith(SOURCES):
            raise ValueError(f'Invalid add-on source: {name}')
        if not re.fullmatch(r'[a-f0-9]{64}', item['sha256']):
            raise ValueError(f'Invalid add-on SHA256: {name}')
        if not isinstance(item['size'], int) or item['size'] <= 0:
            raise ValueError(f'Invalid add-on size: {name}')
    return packages


def checked(name, item, fetch, size=None):
    if size is not None and size != item['size']:
        raise ValueError(f'{name}: add-on size differs from its index')
    data = fetch(item['url'])
    if len(data) != item['size'] or hashlib.sha256(data).hexdigest() != item['sha256']:
        raise ValueError(f'{name}: add-on differs from its pinned size or SHA256')
    return data


def fetch(url):
    with urllib.request.urlopen(url, timeout=120) as response:
        return response.read()


def verify_repository(output, apk='apk', keys=None):
    """Verify signatures except for explicitly pinned, byte-verified add-ons."""
    entries = manifest((output / 'addons.json').read_bytes())
    for name, item in entries.items():
        checked(name, item, lambda url: (output / name).read_bytes())
    for package in sorted(output.glob('*.apk')):
        command = [apk]
        if keys is not None:
            command += ['--keys-dir', str(keys)]
        if package.name in entries:
            command += ['--allow-untrusted']
        subprocess.run(command + ['verify', str(package)], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', type=Path)
    parser.add_argument('--verify', action='store_true', help='verify an assembled repository')
    parser.add_argument('--apk', default='apk')
    parser.add_argument('--keys', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.verify:
        verify_repository(args.output, args.apk, args.keys)
        return
    if args.manifest is None:
        parser.error('--manifest is required for downloading')
    packages = manifest(args.manifest.read_bytes())
    args.output.mkdir(parents=True, exist_ok=True)
    for name, item in packages.items():
        (args.output / name).write_bytes(checked(name, item, fetch))
        print(f'Verified optional add-on: {name}')


if __name__ == '__main__':
    main()
