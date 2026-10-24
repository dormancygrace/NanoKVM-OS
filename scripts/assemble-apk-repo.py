#!/usr/bin/env python3
"""Assemble the NanoKVM OS APK repository that GitHub Pages serves, from the
packages and indexes attached to the GitHub releases.

The newest eligible published OS release supplies each channel's core packages
and legacy index. Stable excludes prereleases; beta includes stable and beta.
Independent stable add-on releases form a repository of their own, one for
both channels, whose v3 index Pages signs; they do not change the OS indexes,
the OS version or the latest SD image.
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
  beta/                  the same layout for the opt-in beta channel
  addons/vX.Y/riscv64/   the add-on APKs and addons.json (their pins); devices
                         read it from /etc/apk/repositories.d/nanokvm-addons.list
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
from release_assets import STORAGE_PREFIX, STORAGE_MARKER, storage_tag

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = 'dormancygrace/NanoKVM-OS'
VERSION = re.compile(r'v(\d+)\.(\d+)\.(\d+)(?:-beta\.([1-9][0-9]*))?')
INDEXES = ('APKINDEX.tar.gz', 'Packages.adb')
MARKER = '.nanokvm-apk-repository'
MARKER_CONTENT = 'NanoKVM APK repository assembler v1\n'
PAGES_URL = 'https://dormancygrace.github.io/NanoKVM-OS'
ADDON_DIR = 'addons'


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


def releases(repository, fetch=fetch, channel='stable'):
    """Published releases with a v3 index, newest first."""
    if channel not in ('stable', 'beta'):
        raise ValueError('Unknown release channel')
    inventory, page = [], 1
    while True:
        batch = json.loads(fetch(f'https://api.github.com/repos/{repository}/releases?per_page=100&page={page}',
                                 'application/vnd.github+json'))
        inventory.extend(batch)
        if len(batch) < 100:
            break
        page += 1
    by_tag = {r['tag_name']: r for r in inventory}
    found = []
    for release in inventory:
        if (release['tag_name'].startswith(STORAGE_PREFIX) or release['draft']
                or not release.get('published_at') or (channel == 'stable' and is_beta(release))):
            continue
        matches = STORAGE_MARKER.findall(release.get('body') or '')
        if matches or STORAGE_PREFIX + release['tag_name'] in by_tag:
            expected = storage_tag(release['tag_name'])
            if matches and matches != [expected]:
                raise ValueError(f'Invalid package storage association: {release["tag_name"]}')
            storage = by_tag.get(expected)
            required = {*INDEXES, 'SHA256SUMS', 'packages.json'}
            if (not storage or storage['draft'] or not storage.get('published_at')
                    or not required <= {a['name'] for a in storage['assets']}):
                raise ValueError(f'Missing or incomplete package storage: {expected}')
            # OS metadata determines channel and version. Package checksums and
            # files come from storage; image links retain the public checksums.
            release = {**release, '_public_assets': release['assets'],
                       'assets': storage['assets'] + [a for a in release['assets']
                                                     if a['name'] != 'SHA256SUMS']}
        if {a['name'] for a in release['assets']} >= {*INDEXES, 'SHA256SUMS'}:
            found.append(release)
    return sorted(found, key=order, reverse=True)


# The add-on release tags, NAME-VERSION-rN for the add-ons of
# addon_packages.ADDONS; PicoClaw/acme.sh retain their own upstream/Alpine
# update mechanisms.
ADDON_TAG = addon_packages.TAG


def independent_addons(repository, keep=6, fetch=fetch):
    """Discover stable add-on assets independently of OS release snapshots."""
    found, page = [], 1
    while True:
        batch = json.loads(fetch(f'https://api.github.com/repos/{repository}/releases?per_page=100&page={page}',
                                 'application/vnd.github+json'))
        found += [r for r in batch if not r['draft'] and not r.get('prerelease')
                  and r.get('published_at') and ADDON_TAG.fullmatch(r['tag_name'])]
        if len(batch) < 100:
            break
        page += 1
    def version(r):
        m = ADDON_TAG.fullmatch(r['tag_name'])
        return m[1], tuple(map(int, m[2].split('.'))), int(m[3])
    entries, counts = {}, {}
    for release in sorted(found, key=version, reverse=True):
        name, _, _ = version(release)
        if counts.get(name, 0) >= keep:
            continue
        counts[name] = counts.get(name, 0) + 1
        filename = release['tag_name'] + '.apk'
        metadata = next(a for a in release['assets'] if a['name'] == filename)
        sums = checksums(asset(release, 'SHA256SUMS', fetch).decode())
        url = f'https://github.com/{repository}/releases/download/{release["tag_name"]}/{filename}'
        if metadata['browser_download_url'] != url:
            raise ValueError(f'Unexpected add-on asset URL: {filename}')
        entry = {'url': url, 'sha256': sums[filename], 'size': metadata['size']}
        entries.update(addon_packages.manifest(json.dumps({'packages': {filename: entry}})))
    return entries


def is_beta(release):
    match = VERSION.fullmatch(release['tag_name'])
    return bool(release.get('prerelease') or (match and match[4]))


def order(release):
    match = VERSION.fullmatch(release['tag_name'])
    if match:
        # Stable X.Y.Z supersedes every X.Y.Z-beta.N, as it does in APK.
        return (1, tuple(map(int, match.groups()[:3])), not bool(match[4]), int(match[4] or 0))
    return (0, (), False, release['published_at'])


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
        public_assets = release.get('_public_assets', release['assets'])
        image = next((a for a in public_assets if a['name'].endswith('.img.zip')), None)
        if image:
            sums = next(a for a in public_assets if a['name'] == 'SHA256SUMS')
            return (f'\n<p>To install on a new SD card: <a href="{escape(image["browser_download_url"])}">'
                    f'{escape(image["name"])}</a> (<a href="{escape(sums["browser_download_url"])}">SHA256SUMS</a>), '
                    f'from release {escape(release["tag_name"])}. It updates itself to the current release.</p>')
    return ''


def image_badge(output, published, include_beta=False):
    """Keep the README image version and destination together across hotfixes."""
    message, color = 'not available', 'lightgrey'
    target = f'https://github.com/{REPOSITORY}/releases'
    for release in published:
        if is_beta(release) and not include_beta:
            continue
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


def assemble(output, published, keep, branch, keys, fetch=fetch, channel='stable'):
    published = [r for r in published if channel == 'beta' or not is_beta(r)]
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
    # The OS repository pins no add-on: they are in the add-on repository.
    (repo / 'addons.json').write_text(json.dumps({'packages': {}}, indent=2) + '\n')
    (output / 'keys').mkdir()
    key_links = []
    for key in sorted(Path(keys).glob('*.pub')):
        shutil.copy(key, output / 'keys' / key.name)
        key_links.append(f'<li><a href="keys/{escape(key.name)}">{escape(key.name)}</a></li>')
    prefix = 'beta/' if channel == 'beta' else ''
    line = f'{PAGES_URL}/{prefix}alpine/v{branch}/riscv64/Packages.adb'
    addon_line = addon_repository(branch)
    (output / 'index.html').write_text(f'''<!doctype html>
<meta charset="utf-8">
<title>NanoKVM OS packages</title>
<h1>NanoKVM OS packages</h1>
<p>APK repository of <a href="https://github.com/{REPOSITORY}">NanoKVM OS</a>, release {escape(newest['tag_name'])}.
Devices use the line <code><a href="{line}">{line}</a></code> in
<code>/etc/apk/repositories</code>; the <code>nanokvm-keys</code> package installs the signing keys.</p>
<p>Add-ons have a repository of their own, for every channel: <code><a href="{addon_line}">{addon_line}</a></code>,
in <code>/etc/apk/repositories.d/nanokvm-addons.list</code> (the <code>nanokvm-base</code> package).</p>
<ul>{"".join(key_links)}</ul>{install_link(published)}
''')
    image_badge(output, published, include_beta=channel == 'beta')
    (output / '.nojekyll').write_text('')
    (output / MARKER).write_text(MARKER_CONTENT)
    return newest['tag_name'], sorted(wanted)


def addon_repository(branch):
    return f'{PAGES_URL}/{ADDON_DIR}/v{branch}/riscv64/Packages.adb'


def assemble_addons(output, branch, independent, fetch=fetch):
    """The add-on repository below OUTPUT, an assembled site: the APKs of the
    independent add-on releases, checked against their pins, which addons.json
    keeps for the package check. Its index is made and signed as the OS ones."""
    repo = Path(output) / ADDON_DIR / ('v' + branch) / 'riscv64'
    if repo.exists():
        raise ValueError(f'{repo} exists: assemble the OS repository first')
    repo.mkdir(parents=True)
    for name, item in sorted(independent.items()):
        (repo / name).write_bytes(addon_packages.checked(name, item, fetch, item['size']))
    (repo / 'addons.json').write_text(json.dumps({'packages': dict(sorted(independent.items()))}, indent=2) + '\n')
    return sorted(independent)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--keep', type=int, default=6, help='releases whose packages stay available (default 6)')
    parser.add_argument('--repository', default=REPOSITORY)
    args = parser.parse_args()
    if args.keep < 1:
        parser.error('--keep must be at least 1')
    published = releases(args.repository, channel='beta')
    addons = independent_addons(args.repository, args.keep)
    for channel, output in [('stable', args.output), ('beta', args.output / 'beta')]:
        tag, names = assemble(output, published, args.keep, alpine_branch(),
                              ROOT / 'firmware/alpine/keys', channel=channel)
        print(f'{channel}: indexes of {tag}, {len(names)} packages')
        for name in names:
            print(f'  {name}')
    names = assemble_addons(args.output, alpine_branch(), addons)
    print(f'add-ons: {len(names)} packages')
    for name in names:
        print(f'  {name}')


if __name__ == '__main__':
    main()
