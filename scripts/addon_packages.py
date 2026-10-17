#!/usr/bin/env python3
"""Fetch checksum-pinned optional APKs, without installing or bundling them.

The release carries addons.json; the APKs remain in their own source releases.
Both release signing and Pages assembly use the same manifest and checks.
APK signature verification is additionally performed by apk when indexing.
"""
import argparse
import hashlib
import json
import re
import urllib.request
from pathlib import Path


def manifest(data):
    packages = json.loads(data)['packages']
    for name, item in packages.items():
        if not re.fullmatch(r'[a-z0-9][a-z0-9+_.-]*[.]apk', name):
            raise ValueError(f'Invalid add-on filename: {name}')
        if not item['url'].startswith('https://github.com/dormancygrace/NanoKVM-OS-packages/releases/download/'):
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    packages = manifest(args.manifest.read_bytes())
    args.output.mkdir(parents=True, exist_ok=True)
    for name, item in packages.items():
        (args.output / name).write_bytes(checked(name, item, fetch))
        print(f'Verified optional add-on: {name}')


if __name__ == '__main__':
    main()
