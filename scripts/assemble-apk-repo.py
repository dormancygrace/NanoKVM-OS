#!/usr/bin/env python3
"""Assemble the NanoKVM OS APK repository that GitHub Pages serves, from the
packages and indexes attached to the GitHub releases.

The newest published release that carries Packages.adb supplies both indexes.
Newest means the highest vX.Y.Z (releases with other tags, from before that
scheme, rank below, by publication time): a minor release published from its
draft after a patch release must not replace the patch's indexes.
Every package that the indexes of the newest KEEP such releases name is kept,
so a device whose cached index is a few releases old still finds its files.
Each package comes from the newest release that has it and must match that
release's SHA256SUMS and the size its index records.

The result, below OUTPUT:
  alpine/vX.Y/riscv64/   *.apk, APKINDEX.tar.gz (v2 index), Packages.adb (v3),
                         packages.json (the version and content of each package)
  keys/                  the public release keys (firmware/alpine/keys)
  index.html             the repository lines
  latest-image/          automatic README badge and redirect to its image release
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
import sys
import tarfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from urllib.parse import quote

import addon_packages

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = 'dormancygrace/NanoKVM-OS'
VERSION = re.compile(r'v(\d+)\.(\d+)\.(\d+)')
INDEXES = ('APKINDEX.tar.gz', 'Packages.adb')
MARKER = '.nanokvm-apk-repository'
MARKER_CONTENT = 'NanoKVM APK repository assembler v1\n'
PAGES_URL = 'https://dormancygrace.github.io/NanoKVM-OS'


RETRY_DELAYS = (5, 10, 20, 30, 60)
RETRY_STATUS = {404, 429, 500, 502, 503, 504}


def fetch(url, accept='application/octet-stream', sleep=time.sleep):
    headers = {'Accept': accept, 'User-Agent': 'nanokvm-apk-repo'}
    token = os.environ.get('GH_TOKEN')
    if token and url.startswith('https://api.github.com/'):
        headers['Authorization'] = 'Bearer ' + token
    # The files of a release that was published a moment ago, which is what
    # starts this script, can answer 404 for a while; so can a busy server.
    for delay in (*RETRY_DELAYS, None):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=120) as response:
                return response.read()
        except urllib.error.HTTPError as error:
            if delay is None or error.code not in RETRY_STATUS:
                raise
            print(f'{url}: HTTP {error.code}, retrying in {delay} s', file=sys.stderr)
        sleep(delay)


def releases(repository, fetch=fetch):
    """Published releases with a v3 index, newest first."""
    found, page = [], 1
    while True:
        batch = json.loads(fetch(f'https://api.github.com/repos/{repository}/releases?per_page=100&page={page}',
                                 'application/vnd.github+json'))
        found += [r for r in batch if not r['draft'] and r.get('published_at')
                  and {a['name'] for a in r['assets']} >= {*INDEXES, 'SHA256SUMS'}]
        if len(batch) < 100:
            return sorted(found, key=order, reverse=True)
        page += 1


def order(release):
    match = VERSION.fullmatch(release['tag_name'])
    return (1, tuple(map(int, match.groups())), '') if match else (0, (), release['published_at'])


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


def install_link(published):
    """The newest image, for new cards: not every release carries one."""
    for release in published:
        image = next((a for a in release['assets'] if a['name'].endswith('.img.zip')), None)
        if image:
            sums = next(a for a in release['assets'] if a['name'] == 'SHA256SUMS')
            return (f'\n<p>To install on a new SD card: <a href="{escape(image["browser_download_url"])}">'
                    f'{escape(image["name"])}</a> (<a href="{escape(sums["browser_download_url"])}">SHA256SUMS</a>), '
                    f'from release {escape(release["tag_name"])}. It updates itself to the current release.</p>')
    return ''


def image_badge(output, published):
    """Keep the README image version and destination together across hotfixes."""
    message, color = 'not available', 'lightgrey'
    target = f'https://github.com/{REPOSITORY}/releases'
    for release in published:
        image = next((a for a in release['assets'] if re.fullmatch(
            r'NanoKVM-OS-Image-(v.+)-apps-v.+\.img\.zip', a['name'])), None)
        if image:
            message = re.fullmatch(r'NanoKVM-OS-Image-(v.+)-apps-v.+\.img\.zip', image['name'])[1]
            color = 'blue'
            target += '/tag/' + quote(release['tag_name'], safe='')
            break
    directory = Path(output) / 'latest-image'
    directory.mkdir()
    (directory / 'badge.json').write_text(json.dumps({
        'schemaVersion': 1, 'label': 'SD image', 'message': message, 'color': color,
    }) + '\n')
    (directory / 'index.html').write_text(f'''<!doctype html>
<meta charset="utf-8">
<title>NanoKVM OS — latest SD image</title>
<meta http-equiv="refresh" content="0; url={escape(target)}">
<p><a href="{escape(target)}">Latest NanoKVM OS SD image: {escape(message)}</a></p>
''')


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
    addons = {}
    for release in reversed(published):
        if 'addons.json' in {a['name'] for a in release['assets']}:
            data = checked(release, 'addons.json', sums[release['tag_name']], fetch)
            addons.update(addon_packages.manifest(data))
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
        # Native APK v3 add-ons occur only in Packages.adb, not the v2 index.
        if 'addons.json' in {a['name'] for a in release['assets']}:
            data = checked(release, 'addons.json', sums[release['tag_name']], fetch)
            wanted.update({name: item['size'] for name, item in addon_packages.manifest(data).items()})
    for name, size in sorted(wanted.items()):
        source = next((r for r in published if name in {a['name'] for a in r['assets']}), None)
        if source is not None:
            data = checked(source, name, sums[source['tag_name']], fetch, size)
        elif name in addons:
            data = addon_packages.checked(name, addons[name], fetch, size)
        else:
            raise ValueError(f'No release carries {name}')
        (repo / name).write_bytes(data)
    # Preserve the verified pins of retained add-ons for the package check.
    (repo / 'addons.json').write_text(json.dumps({'packages': {
        name: addons[name] for name in sorted(wanted) if name in addons
    }}, indent=2) + '\n')
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
<ul>{"".join(key_links)}</ul>{install_link(published)}
''')
    image_badge(output, published)
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
