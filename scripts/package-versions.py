#!/usr/bin/env python3
"""Choose the version of each NanoKVM OS package for a release.

A package keeps the version it was last published with while its content stays
the same: its payload tree and its recipe directory. Most changed packages get the
release's APK version. nanokvm-release always changes, since it carries the
release version. nanokvm-kernel and nanokvm-kmod change together,
with the upstream Linux version. OpenSSL and exfatprogs also follow their upstream versions.

  select    write versions.json and versions.txt (NAME VERSION CHANGED PKGREL) from the
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
    'nanokvm-zlib': 'zlib',
    'nanokvm-openssl3': 'openssl',
    'nanokvm-openssl': None,
    'nanokvm-exfatprogs1': 'exfatprogs',
    'nanokvm-exfatprogs': None,
    'nanokvm-i2c-tools': None,
    'nanokvm-tpu': 'tpu',
    'nanokvm-release': 'release',
    # The names of nanokvm-kernel and the others before 3.7.0.
}
KERNEL = ('nanokvm-kernel', 'nanokvm-kmod')
ALWAYS = {'nanokvm-release'}
SOURCE_LOCK = Path(__file__).resolve().parents[1] / 'platform/sources.lock'


def component_versions():
    sources = dict(row.split()[:2] for row in SOURCE_LOCK.read_text().splitlines()
                   if row.startswith(('linux ', 'openssl ', 'exfatprogs ')))

    def upstream(name):
        archive = sources[name].rsplit('/', 1)[-1]
        match = re.fullmatch(rf'{name}-([0-9]+(?:\.[0-9]+)+)\.tar\.(?:xz|gz)', archive)
        if not match:
            raise ValueError(f'Unknown {name} archive version: {archive}')
        return match[1]

    linux = upstream('linux')
    if linux.count('.') == 1:
        linux += '.0'
    return {**dict.fromkeys(KERNEL, linux), 'nanokvm-openssl3': upstream('openssl'),
            'nanokvm-exfatprogs1': upstream('exfatprogs')}


def release_revision(version):
    """An ordered APK revision unique to this OS release across release branches."""
    match = re.fullmatch(r'(\d+)\.(\d+)\.(\d+)(?:_beta([1-9]\d*))?', version)
    if not match:
        raise ValueError(f'Unsupported OS release version: {version}')
    major, minor, patch = map(int, match.groups()[:3])
    stage = int(match[4]) if match[4] else 999
    if minor > 999 or patch > 999 or (match[4] and stage >= 999):
        raise ValueError(f'OS release exceeds the APK revision fields: {version}')
    return ((major * 1000 + minor) * 1000 + patch) * 1000 + stage


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
    revision = release_revision(version)
    # A maintenance release can follow a newer beta. Never inherit future APKs.
    published = (previous or {}).get('packages', {})
    if previous and release_revision(previous['release']) > revision:
        published = {}
    release_line, beta, _ = version.partition('_beta')
    previous_line, previous_beta, _ = (previous or {}).get('release', '').partition('_beta')
    first_published_beta = bool(beta and (not previous_beta or previous_line != release_line))
    chosen = {}
    components = component_versions()
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
    # These upstream versions advance independently of the OS. At the same
    # upstream version, a changed build must sort after the previous APK,
    # including when it first reaches devices through the beta channel.
    for group in (KERNEL, ('nanokvm-openssl3',), ('nanokvm-exfatprogs1',)):
        upstream = components[group[0]]
        old = [published.get(n, {}) for n in group]
        changed = any(chosen[n]['changed'] or p.get('version') != upstream or 'pkgrel' not in p
                      for n, p in zip(group, old))
        # Establish a revision for the new beta line before a lower-numbered
        # stable maintenance release can publish different bytes. A device
        # can then switch to this beta via an ordinary APK upgrade.
        changed = changed or first_published_beta
        for name, prior in zip(group, old):
            chosen[name].update(version=upstream, changed=changed,
                                pkgrel=revision if changed else prior['pkgrel'])
    return chosen


def apk_name(name, item):
    return f"{name}-{item['version']}-r{item.get('pkgrel', 0)}.apk"


def version_lines(packages):
    return ''.join(f"{name} {p['version']} {int(p['changed'])} {p.get('pkgrel', 0)}\n"
                   for name, p in packages.items())


def initial(args):
    components = component_versions()
    packages = {name: {'version': components.get(name, args.version), 'changed': True,
                       'pkgrel': release_revision(args.version) if name in components else 0}
                for name in PACKAGES}
    sys.stdout.write(version_lines(packages))


def baseline(args):
    prior = json.loads(Path(args.previous).read_text())['release']
    print('stable' if release_revision(prior) > release_revision(args.version) else 'previous')


def write_select(args):
    previous = json.loads(Path(args.previous).read_text()) if args.previous else None
    chosen = select(args.payloads, args.recipes, args.version, previous)
    out = Path(args.output)
    out.mkdir(parents=True, exist_ok=True)
    (out / 'versions.json').write_text(json.dumps({'release': args.version, 'packages': chosen}, indent=2) + '\n')
    (out / 'versions.txt').write_text(version_lines(chosen))
    for name, item in chosen.items():
        print(f"{name} {item['version']} {'new' if item['changed'] else 'unchanged'}")


def write_reused(args):
    versions = json.loads(Path(args.versions).read_text())['packages']
    published = json.loads(Path(args.previous).read_text())['packages']
    for name, item in versions.items():
        if not item['changed']:
            old = published[name]
            if old['version'] != item['version'] or old['apk'] != apk_name(name, item):
                raise ValueError(f'{name}: the previous manifest does not record {item["version"]}')
            print(old['apk'], old['apk_sha256'])


def write_manifest(args):
    versions = json.loads(Path(args.versions).read_text())
    repo = Path(args.repo)
    packages = {}
    for name, item in versions['packages'].items():
        file = repo / apk_name(name, item)
        if not file.is_file():
            raise ValueError(f'{file} is missing')
        packages[name] = {**item, 'apk': file.name, 'apk_sha256': hashlib.sha256(file.read_bytes()).hexdigest()}
    (repo / 'packages.json').write_text(json.dumps({'release': versions['release'], 'packages': packages}, indent=2) + '\n')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    sub.add_parser('initial', help='standalone build without a previous manifest').add_argument('--version', required=True)
    b = sub.add_parser('baseline', help='use the stable feed if the beta head is newer than the target')
    b.add_argument('--version', required=True)
    b.add_argument('--previous', required=True)
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
        {'initial': initial, 'baseline': baseline, 'select': write_select, 'reused': write_reused, 'manifest': write_manifest}[args.command](args)
    except (ValueError, KeyError) as error:
        sys.exit(f'package-versions: {error}')


if __name__ == '__main__':
    main()
