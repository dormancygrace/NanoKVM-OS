#!/usr/bin/env python3
"""Assemble the NanoKVM OS APK repository that GitHub Pages serves, from the
packages and indexes attached to the GitHub releases.

The newest published release that carries Packages.adb supplies both indexes.
Every package that the indexes of the newest KEEP such releases name is kept,
so a device whose cached index is a few releases old still finds its files.
Each package comes from the newest release that has it and must match that
release's SHA256SUMS and the size its index records.

The result, below OUTPUT:
  alpine/vX.Y/riscv64/   *.apk, APKINDEX.tar.gz (v2 index), Packages.adb (v3),
                         packages.json (the version and content of each package)
  keys/                  the public release keys (firmware/alpine/keys)
  index.html             the repository lines
"""
import argparse
import gzip
import hashlib
from html import escape
import io
import json
import os
import re
import shutil
import tarfile
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = 'dormancygrace/NanoKVM-OS'
INDEXES = ('APKINDEX.tar.gz', 'Packages.adb')
MARKER = '.nanokvm-apk-repository'
MARKER_CONTENT = 'NanoKVM APK repository assembler v1\n'
PAGES_URL = 'https://dormancygrace.github.io/NanoKVM-OS'


def fetch(url, accept='application/octet-stream'):
    headers = {'Accept': accept, 'User-Agent': 'nanokvm-apk-repo'}
    token = os.environ.get('GH_TOKEN')
    if token and url.startswith('https://api.github.com/'):
        headers['Authorization'] = 'Bearer ' + token
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=120) as response:
        return response.read()


def releases(repository, fetch=fetch):
    """Published releases with a v3 index, newest first."""
    found, page = [], 1
    while True:
        batch = json.loads(fetch(f'https://api.github.com/repos/{repository}/releases?per_page=100&page={page}',
                                 'application/vnd.github+json'))
        found += [r for r in batch if not r['draft'] and r.get('published_at')
                  and {a['name'] for a in r['assets']} >= {*INDEXES, 'SHA256SUMS'}]
        if len(batch) < 100:
            return sorted(found, key=lambda r: r['published_at'], reverse=True)
        page += 1


def asset(release, name, fetch=fetch):
    url = next(a['browser_download_url'] for a in release['assets'] if a['name'] == name)
    return fetch(url)


def checksums(text):
    sums = {}
    for line in text.splitlines():
        if line.strip():
            digest, name = line.split(None, 1)
            sums[name.lstrip('*')] = digest
    return sums


def index_packages(apkindex):
    """{file name: size} from a v2 index. Its first gzip member, the signature,
    is a tar without an end marker, so the members read as one tar stream."""
    with tarfile.open(fileobj=io.BytesIO(gzip.decompress(apkindex)), mode='r|') as tar:
        for member in tar:
            if member.name == 'APKINDEX':
                text = tar.extractfile(member).read().decode()
                break
        else:
            raise ValueError('APKINDEX.tar.gz has no APKINDEX')
    packages = {}
    for record in text.split('\n\n'):
        fields = dict(line.split(':', 1) for line in record.splitlines() if ':' in line)
        if 'P' in fields:
            packages[f"{fields['P']}-{fields['V']}.apk"] = int(fields['S'])
    if not packages:
        raise ValueError('APKINDEX names no packages')
    return packages


def checked(release, name, sums, fetch=fetch, size=None):
    data = asset(release, name, fetch)
    if hashlib.sha256(data).hexdigest() != sums.get(name):
        raise ValueError(f"{name} of {release['tag_name']} does not match its SHA256SUMS")
    if size is not None and len(data) != size:
        raise ValueError(f"{name} of {release['tag_name']} is {len(data)} bytes, its index says {size}")
    return data


def alpine_branch(root=ROOT):
    text = (root / 'scripts/build-alpine-packages.sh').read_text()
    return re.search(r'^ALPINE_VERSION="([0-9]+\.[0-9]+)"', text, re.M)[1]


def assemble(output, published, keep, branch, keys, fetch=fetch):
    if not published:
        raise ValueError('No published release carries Packages.adb')
    output = Path(output)
    if output.exists():
        # Replace only an earlier result of this script, never another directory.
        marker = output / MARKER
        if any(output.iterdir()) and not (marker.is_file() and marker.read_text() == MARKER_CONTENT):
            raise ValueError(f'{output} is not empty and not an assembled repository')
        shutil.rmtree(output)
    repo = output / 'alpine' / ('v' + branch) / 'riscv64'
    repo.mkdir(parents=True)
    sums = {r['tag_name']: checksums(asset(r, 'SHA256SUMS', fetch).decode()) for r in published}
    newest = published[0]
    for name in INDEXES:
        (repo / name).write_bytes(checked(newest, name, sums[newest['tag_name']], fetch))
    # The next release reads it to keep unchanged packages; older releases have none.
    if 'packages.json' in {a['name'] for a in newest['assets']}:
        (repo / 'packages.json').write_bytes(checked(newest, 'packages.json', sums[newest['tag_name']], fetch))
    # The size each package has in the newest index that names it.
    wanted = {}
    for release in reversed(published[:keep]):
        index = checked(release, 'APKINDEX.tar.gz', sums[release['tag_name']], fetch)
        wanted.update(index_packages(index))
    for name, size in sorted(wanted.items()):
        source = next((r for r in published if name in {a['name'] for a in r['assets']}), None)
        if source is None:
            raise ValueError(f'No release carries {name}')
        (repo / name).write_bytes(checked(source, name, sums[source['tag_name']], fetch, size))
    (output / 'keys').mkdir()
    key_links = []
    for key in sorted(Path(keys).glob('*.pub')):
        shutil.copy(key, output / 'keys' / key.name)
        key_links.append(f'<li><a href="keys/{escape(key.name)}">{escape(key.name)}</a></li>')
    line = f'{PAGES_URL}/alpine/v{branch}/riscv64/Packages.adb'
    (output / 'index.html').write_text(f'''<!doctype html>
<meta charset="utf-8">
<title>NanoKVM OS packages</title>
<h1>NanoKVM OS packages</h1>
<p>APK repository of <a href="https://github.com/{REPOSITORY}">NanoKVM OS</a>, release {escape(newest['tag_name'])}.
Devices use the line <code><a href="{line}">{line}</a></code> in
<code>/etc/apk/repositories</code>; the <code>nanokvm-keys</code> package installs the signing keys.</p>
<ul>{"".join(key_links)}</ul>
''')
    (output / '.nojekyll').write_text('')
    (output / MARKER).write_text(MARKER_CONTENT)
    return newest['tag_name'], sorted(wanted)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--keep', type=int, default=6, help='releases whose packages stay available (default 6)')
    parser.add_argument('--repository', default=REPOSITORY)
    args = parser.parse_args()
    if args.keep < 1:
        parser.error('--keep must be at least 1')
    tag, names = assemble(args.output, releases(args.repository), args.keep, alpine_branch(),
                          ROOT / 'firmware/alpine/keys')
    print(f'{args.output}: indexes of {tag}, {len(names)} packages')
    for name in names:
        print('  ' + name)


if __name__ == '__main__':
    main()
