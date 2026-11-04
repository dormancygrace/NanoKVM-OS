#!/usr/bin/env python3
"""Plan (default) or apply legacy package cleanup AFTER a verified Pages migration.
OS releases, their Git tags, images, migration archives and sources are preserved.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
import tempfile

import addon_packages
import pages_store as store
from release_assets import TECHNICAL, STORAGE_PREFIX


def plan(releases):
    result = []
    for release in releases:
        tag = release['tag_name']
        if release['draft']:
            continue
        if tag.startswith('nanokvm-rustdesk-') and addon_packages.TAG.fullmatch(tag):
            # Already installed APKs have immutable GitHub source URLs.
            # Preserve these legacy source releases; remove binary assets only.
            assets = [a for a in release['assets'] if a['name'] in TECHNICAL or a['name'].endswith('.apk')]
            if assets:
                result.append({'tag': tag, 'assets': assets})
        elif (addon_packages.TAG.fullmatch(tag) or
                (tag.startswith(STORAGE_PREFIX) and store.a.VERSION.fullmatch(tag[len(STORAGE_PREFIX):]))):
            result.append({'release': release['id'], 'tag': tag})
        elif re.fullmatch(r'v[0-9][A-Za-z0-9.-]*', tag):
            assets = [a for a in release['assets'] if a['name'] in TECHNICAL or a['name'].endswith('.apk')]
            if assets:
                result.append({'tag': tag, 'assets': assets})
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--apk', default='apk', help='host apk-tools 3, required for --apply')
    args = parser.parse_args()
    releases = store.inventory()
    changes = plan(releases)
    print(json.dumps(changes, indent=2))
    if not args.apply:
        return
    # Fail before the first deletion if any retained object is missing or the
    # deployed stable/beta/add-on indexes do not verify with our public keys.
    with tempfile.TemporaryDirectory() as temporary:
        s = store.Store(temporary)
        s.load()
        public = {r['tag_name'] for r in releases if not r['draft'] and r.get('published_at')}
        if not s.snapshots or store.retained(s.snapshots, public) != s.snapshots:
            raise ValueError('Pages catalog is empty or has not applied retention')
        # Require all legacy snapshots that migration promises to preserve.
        required = {}
        for r in releases:
            if (not r['draft'] and store.a.VERSION.fullmatch(r['tag_name']) and
                    any(x['name'] == f"NanoKVM-OS-{r['tag_name']}-source.tar.xz" for x in r['assets'])):
                key = store.identity('os', r['tag_name'])
                required[key] = dict(kind='os', tag=r['tag_name'], files={})
        for r in releases:
            if not r['draft'] and addon_packages.TAG.fullmatch(r['tag_name']):
                key = store.identity('addon', r['tag_name'])
                required[key] = dict(kind='addon', tag=r['tag_name'], files={})
        # Include newer Pages-only versions before deciding what is obsolete.
        required.update(s.snapshots)
        if not store.retained(required, public).keys() <= s.snapshots.keys():
            raise ValueError('Pages migration is incomplete')
        for item in s.snapshots.values():
            for f in item['files'].values():
                s.get(f)
        for f in s.sources.values():
            s.get(f)
        for release in releases:
            match = addon_packages.TAG.fullmatch(release['tag_name'])
            if match and match[1] == 'nanokvm-rustdesk' and not release['draft']:
                for name in ('APKBUILD', f'nanokvm-rustdesk-{match[2]}-source.tar.gz'):
                    if f'package-store/sources/{release["tag_name"]}/{name}' not in s.sources:
                        raise ValueError('Permanent RustDesk source migration is incomplete')
        branch = store.a.alpine_branch()
        for prefix in ('alpine', 'beta/alpine', 'addons'):
            data = store.a.fetch(f'{store.BASE}/{prefix}/v{branch}/riscv64/Packages.adb')
            index = Path(temporary) / 'Packages.adb'
            index.write_bytes(data)
            subprocess.run([args.apk, '--keys-dir', str(store.a.ROOT / 'firmware/alpine/keys'),
                            'verify', str(index)], check=True)
        by_tag = {r['tag_name']: r for r in releases}
        for item in changes:
            if 'release' in item:
                subprocess.run(['gh', 'api', '--method', 'DELETE',
                                f'repos/{store.a.REPOSITORY}/releases/{item["release"]}'], check=True)
                continue
            release = by_tag[item['tag']]
            removed = {a['name'] for a in item['assets']}
            sums_asset = next((a for a in release['assets'] if a['name'] == 'SHA256SUMS'), None)
            if sums_asset:
                # Retain the original hashes of all remaining user downloads.
                sums = store.a.checksums(store.a.fetch(sums_asset['browser_download_url']).decode())
                path = Path(temporary) / 'SHA256SUMS'
                path.write_text(''.join(f'{sha}  {name}\n' for name, sha in sums.items() if name not in removed))
                subprocess.run(['gh', 'release', 'upload', item['tag'], '--repo', store.a.REPOSITORY,
                                '--clobber', str(path)], check=True)
            for asset in item['assets']:
                subprocess.run(['gh', 'api', '--method', 'DELETE',
                                f'repos/{store.a.REPOSITORY}/releases/assets/{asset["id"]}'], check=True)


if __name__ == '__main__':
    main()
