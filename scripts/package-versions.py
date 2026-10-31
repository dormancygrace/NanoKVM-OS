#!/usr/bin/env python3
"""Choose the version of each NanoKVM OS package for a release.

A package keeps the version it was last published with while its content stays
the same: its payload tree and its recipe directory. Otherwise it gets the
release's APK version. nanokvm-release always changes, since it carries the
release version. nanokvm-kernel and nanokvm-kmod change together,
because the kernel requires its modules at exactly its own version.

  select    write versions.json and versions.txt (NAME VERSION CHANGED) from the
            payloads, the recipes and the packages.json of the previous release
  reused    print "FILE SHA256" for each package that keeps its published file
  manifest  write packages.json of a built repository
"""
import argparse
import hashlib
import json
import os
import re
import stat
import sys
from pathlib import Path

# Package name: its payload directory (None: no payload, a metapackage).
PACKAGES = {
    'nanokvm-keys': 'keys',
    'nanokvm-base': 'base',
    'nanokvm-kernel': 'kernel',
    'nanokvm-kmod': 'kmod',
    'nanokvm-firmware': 'firmware',
    'nanokvm-bootloader': 'bootloader',
    'nanokvm': 'app',
    'nanokvm-web': 'web',
    'nanokvm-app': None,
    'nanokvm-zlib': 'zlib',
    'nanokvm-openssl': 'openssl',
    'nanokvm-exfatprogs': 'exfatprogs',
    'nanokvm-i2c-tools': 'i2c-tools',
    'nanokvm-tpu': 'tpu',
    'nanokvm-release': 'release',
    # The names of nanokvm-kernel and the others before 3.7.0.
    'nanokvm-kernel-sg2002': None,
    'nanokvm-kmod-sg2002': None,
    'nanokvm-firmware-sg2002': None,
    'nanokvm-bootloader-sg2002': None,
}
GROUPS = [{'nanokvm-kernel', 'nanokvm-kmod'}]
ALWAYS = {'nanokvm-release'}
PKGREL = 'r0'


def tree_digest(root):
    """Paths, types, modes, link targets and file contents below root."""
    digest = hashlib.sha256()
    if root is None:
        return digest.hexdigest()
    root = Path(root)
    for path in sorted(root.rglob('*'), key=lambda p: p.relative_to(root).as_posix()):
        name = path.relative_to(root).as_posix()
        info = path.lstat()
        mode = stat.S_IMODE(info.st_mode)
        if stat.S_ISLNK(info.st_mode):
            line = f'L {name} {os.readlink(path)}'
        elif stat.S_ISDIR(info.st_mode):
            line = f'D {name} {mode:o}'
        elif stat.S_ISREG(info.st_mode):
            line = f'F {name} {mode:o} {hashlib.sha256(path.read_bytes()).hexdigest()}'
        else:
            raise ValueError(f'{path}: not a file, directory or link')
        digest.update(line.encode() + b'\n')
    return digest.hexdigest()


def content(payloads, recipes, name):
    payload = PACKAGES[name]
    if payload is not None and not (Path(payloads) / payload).is_dir():
        raise ValueError(f'missing payload {payload} of {name}')
    recipe = Path(recipes) / name
    if not (recipe / 'APKBUILD').is_file():
        raise ValueError(f'missing recipe {recipe}/APKBUILD')
    return hashlib.sha256(('signing ecdsa-sha256-v1 payload ' + tree_digest(payload and Path(payloads) / payload)
                           + ' recipe ' + tree_digest(recipe)).encode()).hexdigest()


def select(payloads, recipes, version, previous):
    """{name: {version, content, changed}}; previous is a packages.json or None."""
    published = (previous or {}).get('packages', {})
    chosen = {}
    for name in PACKAGES:
        digest = content(payloads, recipes, name)
        old = published.get(name)
        # A stable release must replace prerelease packages even if content
        # is identical (or reverted to the last stable payload). The beta feed
        # supplies the last tested manifest; stable packages can still be reused.
        promote = old is not None and not re.search(r'_(alpha|beta|pre|rc)', version) and bool(
            re.search(r'_(alpha|beta|pre|rc)', old['version']))
        changed = promote or name in ALWAYS or old is None or old.get('content_sha256') != digest
        chosen[name] = {'version': version if changed else old['version'], 'content_sha256': digest, 'changed': changed}
    for group in GROUPS:
        if any(chosen[name]['changed'] for name in group):
            for name in group:
                chosen[name].update(version=version, changed=True)
    return chosen


def apk_name(name, version):
    return f'{name}-{version}-{PKGREL}.apk'


def write_select(args):
    previous = json.loads(Path(args.previous).read_text()) if args.previous else None
    chosen = select(args.payloads, args.recipes, args.version, previous)
    out = Path(args.output)
    out.mkdir(parents=True, exist_ok=True)
    (out / 'versions.json').write_text(json.dumps({'release': args.version, 'packages': chosen}, indent=2) + '\n')
    (out / 'versions.txt').write_text(''.join(f"{n} {p['version']} {int(p['changed'])}\n" for n, p in chosen.items()))
    for name, item in chosen.items():
        print(f"{name} {item['version']} {'new' if item['changed'] else 'unchanged'}")


def write_reused(args):
    versions = json.loads(Path(args.versions).read_text())['packages']
    published = json.loads(Path(args.previous).read_text())['packages']
    for name, item in versions.items():
        if not item['changed']:
            old = published[name]
            if old['version'] != item['version'] or old['apk'] != apk_name(name, item['version']):
                raise ValueError(f'{name}: the previous manifest does not record {item["version"]}')
            print(old['apk'], old['apk_sha256'])


def write_manifest(args):
    versions = json.loads(Path(args.versions).read_text())
    repo = Path(args.repo)
    packages = {}
    for name, item in versions['packages'].items():
        file = repo / apk_name(name, item['version'])
        if not file.is_file():
            raise ValueError(f'{file} is missing')
        packages[name] = {**item, 'apk': file.name, 'apk_sha256': hashlib.sha256(file.read_bytes()).hexdigest()}
    (repo / 'packages.json').write_text(json.dumps({'release': versions['release'], 'packages': packages}, indent=2) + '\n')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    s = sub.add_parser('select')
    s.add_argument('--payloads', required=True)
    s.add_argument('--recipes', required=True)
    s.add_argument('--version', required=True)
    s.add_argument('--previous', help='packages.json of the previous release')
    s.add_argument('--output', required=True, help='directory for versions.json and versions.txt')
    r = sub.add_parser('reused')
    r.add_argument('--versions', required=True)
    r.add_argument('--previous', required=True)
    m = sub.add_parser('manifest')
    m.add_argument('--versions', required=True)
    m.add_argument('--repo', required=True)
    args = parser.parse_args(argv)
    try:
        {'select': write_select, 'reused': write_reused, 'manifest': write_manifest}[args.command](args)
    except (ValueError, KeyError) as error:
        sys.exit(f'package-versions: {error}')


if __name__ == '__main__':
    main()
