#!/usr/bin/env python3
"""Durable Pages package snapshots; Actions artifacts are transport, not storage.

The catalog and its hash-checked objects are carried into every deployment.
Public OS releases activate staged OS snapshots. Add-ons activate independently.
"""
import argparse
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import shutil
import stat
import tempfile
import urllib.error
import zipfile

import addon_packages
from release_assets import REQUIRED, TECHNICAL

_spec = importlib.util.spec_from_file_location('assembler', Path(__file__).with_name('assemble-apk-repo.py'))
a = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(a)
BASE = a.PAGES_URL
CATALOG = 'package-store/catalog.json'
LIMIT = 950_000_000  # Leave headroom below the published Pages 1 GB limit.
WORKFLOWS = {'release.yml': 'os', 'rustdesk.yml': 'addon', 'addon.yml': 'addon'}
SAFE = re.compile(r'[A-Za-z0-9][A-Za-z0-9+_.-]*')


def filename(name):
    if not isinstance(name, str) or not SAFE.fullmatch(name):
        raise ValueError(f'Unsafe filename: {name!r}')
    return name


def identity(kind, tag):
    if kind not in ('os', 'addon') or not (a.VERSION if kind == 'os' else addon_packages.TAG).fullmatch(tag):
        raise ValueError(f'Invalid snapshot: {kind}/{tag}')
    return kind + '/' + tag


def version(item):
    if item['kind'] == 'os':
        return a.order({'tag_name': item['tag'], 'published_at': ''})
    m = addon_packages.TAG.fullmatch(item['tag'])
    return tuple(map(int, m[2].split('.'))), int(m[3])


def retained(snapshots, published):
    """Three stable, ONE beta, three per add-on; latest pending OS per channel."""
    groups = {}
    for item in snapshots.values():
        if item['kind'] == 'addon':
            group, keep = addon_packages.TAG.fullmatch(item['tag'])[1], 3
        else:
            beta = bool(a.VERSION.fullmatch(item['tag'])[4])
            active = item['tag'] in published
            group = ('beta' if beta else 'stable') + ('' if active else '-pending')
            keep = 1 if beta or not active else 3
        groups.setdefault((group, keep), []).append(item)
    return {identity(i['kind'], i['tag']): i for (_, keep), items in groups.items()
            for i in sorted(items, key=version, reverse=True)[:keep]}


def inventory():
    result, page = [], 1
    while True:
        batch = json.loads(a.fetch(f'https://api.github.com/repos/{a.REPOSITORY}/releases?per_page=100&page={page}', 'application/vnd.github+json'))
        result.extend(batch)
        if len(batch) < 100:
            return result
        page += 1


class Store:
    def __init__(self, cache):
        self.cache = Path(cache)
        self.cache.mkdir(parents=True, exist_ok=True)
        self.snapshots, self.seen = {}, set()

    def put(self, name, data, path=None):
        filename(name)
        sha = hashlib.sha256(data).hexdigest()
        path = path or f'package-store/objects/{sha}/{name}'
        target = self.cache / path
        target.parent.mkdir(parents=True, exist_ok=True)
        if target.exists() and target.read_bytes() != data:
            raise ValueError(f'Immutable object changed: {path}')
        target.write_bytes(data)
        return {'path': path, 'sha256': sha, 'size': len(data)}

    def get(self, item):
        path, sha, size = item['path'], item['sha256'], item['size']
        if (not re.fullmatch(r'[a-f0-9]{64}', sha) or type(size) is not int or not 0 < size <= LIMIT
                or not re.fullmatch(r'package-store/(?:objects/[a-f0-9]{64}|sources/nanokvm-rustdesk-[0-9.]+-r[0-9]+)/[A-Za-z0-9][A-Za-z0-9+_.-]*', path)):
            raise ValueError('Invalid catalog object')
        target = self.cache / path
        data = target.read_bytes() if target.exists() else a.fetch(BASE + '/' + path)
        if len(data) != size or hashlib.sha256(data).hexdigest() != sha:
            raise ValueError(f'Corrupt Pages object: {path}')
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        return data

    def load(self):
        data = json.loads(a.fetch(BASE + '/' + CATALOG))
        if data['schema'] != 1:
            raise ValueError('Unsupported Pages catalog schema')
        for key, item in data['snapshots'].items():
            if identity(item['kind'], item['tag']) != key:
                raise ValueError('Invalid snapshot key')
            for name in item['files']:
                filename(name)
        self.snapshots = data['snapshots']
        self.seen = set(data['artifacts'])

    def add(self, kind, tag, files):
        key = identity(kind, tag)
        item = {'kind': kind, 'tag': tag, 'files': files}
        if key in self.snapshots and self.snapshots[key] != item:
            raise ValueError(f'Refusing to replace immutable snapshot {key}')
        self.snapshots[key] = item

    def candidate(self, data, expected_kind):
        # Never extract an untrusted ZIP onto the host filesystem.
        with zipfile.ZipFile(io.BytesIO(data)) as z:
            names = z.namelist()
            if len(names) != len(set(names)) or sum(i.file_size for i in z.infolist()) > LIMIT:
                raise ValueError('Duplicate entries or oversized candidate')
            for entry in z.infolist():
                filename(entry.filename)
                mode = entry.external_attr >> 16
                if entry.is_dir() or (stat.S_IFMT(mode) and not stat.S_ISREG(mode)):
                    raise ValueError('Candidate contains a non-regular file')
            meta = json.loads(z.read('snapshot.json'))
            kind, tag = meta['kind'], meta['tag']
            identity(kind, tag)
            if kind != expected_kind:
                raise ValueError('Candidate kind differs from its producer workflow')
            sums = a.checksums(z.read('SHA256SUMS').decode())
            if set(sums) != set(names) - {'snapshot.json', 'SHA256SUMS'}:
                raise ValueError('Incomplete candidate checksums')
            files = {}
            for name in sorted(sums):
                payload = z.read(name)
                if hashlib.sha256(payload).hexdigest() != sums[name]:
                    raise ValueError(f'Candidate checksum mismatch: {name}')
                if kind == 'os':
                    if name not in TECHNICAL and not name.endswith('.apk'):
                        raise ValueError('Unexpected OS file')
                elif name not in {tag + '.apk', 'APKBUILD',
                                  f'nanokvm-rustdesk-{addon_packages.TAG.fullmatch(tag)[2]}-source.tar.gz'}:
                    raise ValueError('Unexpected add-on file')
                source = f'package-store/sources/{tag}/{name}' if kind == 'addon' and not name.endswith('.apk') else None
                files[name] = self.put(name, payload, source)
            if kind == 'os':
                if not REQUIRED <= files.keys():
                    raise ValueError('Incomplete OS metadata')
                manifest = json.loads(self.get(files['packages.json']))
                if manifest['release'] != tag[1:].replace('-beta.', '_beta'):
                    raise ValueError('Wrong OS manifest version')
                # Unchanged packages may originate many releases earlier. Keep
                # the complete dependency closure, not just changed APKs.
                available = {n: f for s in self.snapshots.values() if s['kind'] == 'os' for n, f in s['files'].items()}
                for name, size in a.index_packages(self.get(files['APKINDEX.tar.gz'])).items():
                    filename(name)
                    if name not in files:
                        files[name] = available[name]
                    record = next(p for p in manifest['packages'].values() if p['apk'] == name)
                    if files[name]['size'] != size or files[name]['sha256'] != record['apk_sha256']:
                        raise ValueError(f'Incorrect OS package closure: {name}')
            elif tag + '.apk' not in files:
                raise ValueError('Missing add-on APK')
            if kind == 'addon' and tag.startswith('nanokvm-rustdesk-') and len(files) != 3:
                raise ValueError('RustDesk requires corresponding source and APKBUILD')
            self.add(kind, tag, files)

    def ingest(self):
        # Scan ALL pending production artifacts. GitHub concurrency replaces
        # pending runs, so consuming only the triggering run loses publications.
        pending, page = [], 1
        while True:
            url = f'https://api.github.com/repos/{a.REPOSITORY}/actions/artifacts?per_page=100&page={page}'
            batch = json.loads(a.fetch(url, 'application/vnd.github+json'))['artifacts']
            pending.extend(x for x in batch if x['name'].startswith('pages-candidate-') and not x['expired'] and x['id'] not in self.seen)
            if len(batch) < 100:
                break
            page += 1
        runs = {}
        for artifact in sorted(pending, key=lambda x: x['id']):
            run_id = artifact['workflow_run']['id']
            if run_id not in runs:
                runs[run_id] = json.loads(a.fetch(f'https://api.github.com/repos/{a.REPOSITORY}/actions/runs/{run_id}', 'application/vnd.github+json'))
            run = runs[run_id]
            workflow = run['path'].removeprefix('.github/workflows/')
            # The production-only upload step is the authorization;
            # PR/dry-run build artifacts have different names and are not read.
            if not trusted_run(run):
                print(f'Ignoring non-production artifact {artifact["id"]} from run {run_id}')
                self.seen.add(artifact['id'])
                continue
            if run['status'] != 'completed':
                continue  # Its workflow_run event schedules another pass.
            self.candidate(a.fetch(artifact['archive_download_url']), WORKFLOWS[workflow])
            self.seen.add(artifact['id'])

    def bootstrap(self):
        """Read-only import of legacy releases, including unchanged APK closure."""
        releases = a.releases(a.REPOSITORY, channel='beta')
        selected = {}
        for r in releases:
            if a.VERSION.fullmatch(r['tag_name']):
                selected[identity('os', r['tag_name'])] = {'kind': 'os', 'tag': r['tag_name'], 'files': {}}
        selected = retained(selected, {r['tag_name'] for r in releases})
        sums = {}
        def old_file(release, name):
            tag = release['tag_name']
            if tag not in sums:
                sums[tag] = a.checksums(a.asset(release, 'SHA256SUMS').decode())
            return a.checked(release, name, sums[tag])
        for r in releases:
            if 'os/' + r['tag_name'] not in selected:
                continue
            files = {n: self.put(n, old_file(r, n)) for n in TECHNICAL if n in {x['name'] for x in r['assets']}}
            for name, size in a.index_packages(self.get(files['APKINDEX.tar.gz'])).items():
                source = next(s for s in releases if name in {x['name'] for x in s['assets']})
                files[name] = self.put(name, old_file(source, name))
                if files[name]['size'] != size:
                    raise ValueError('Legacy package size mismatch')
            self.add('os', r['tag_name'], files)
        pins = a.independent_addons(a.REPOSITORY, keep=3)
        by_tag = {r['tag_name']: r for r in inventory()}
        for name, pin in pins.items():
            tag = name[:-4]
            files = {name: self.put(name, addon_packages.checked(name, pin, a.fetch))}
            if tag.startswith('nanokvm-rustdesk-'):
                for n in ('APKBUILD', f'nanokvm-rustdesk-{addon_packages.TAG.fullmatch(tag)[2]}-source.tar.gz'):
                    files[n] = self.put(n, old_file(by_tag[tag], n), f'package-store/sources/{tag}/{n}')
            self.add('addon', tag, files)

    def render(self, output):
        public = {r['tag_name']: r for r in inventory() if not r['draft'] and r.get('published_at') and a.VERSION.fullmatch(r['tag_name'])}
        for beta in (False, True):
            candidates = [r for r in public.values() if bool(a.VERSION.fullmatch(r['tag_name'])[4]) == beta
                          and any(x['name'] == f"NanoKVM-OS-{r['tag_name']}-source.tar.xz" for x in r['assets'])]
            if candidates:
                head = max(candidates, key=a.order)['tag_name']
                if 'os/' + head not in self.snapshots:
                    raise ValueError(f'Published OS release {head} has no staged packages')
        self.snapshots = retained(self.snapshots, public)
        releases, pins, payloads = [], {}, {}
        for item in self.snapshots.values():
            files = item['files']
            for name, f in files.items():
                payloads[BASE + '/' + f['path']] = self.get(f)
            if item['kind'] == 'os' and item['tag'] in public:
                r = public[item['tag']]
                assets = [{'name': n, 'browser_download_url': BASE + '/' + f['path']} for n, f in files.items()]
                sum_url = 'memory:' + item['tag']
                payloads[sum_url] = ''.join(f"{f['sha256']}  {n}\n" for n, f in files.items()).encode()
                assets.append({'name': 'SHA256SUMS', 'browser_download_url': sum_url})
                assets.extend(x for x in r['assets'] if x['name'] != 'SHA256SUMS')
                releases.append({**r, 'assets': assets, '_public_assets': r['assets']})
            elif item['kind'] == 'addon':
                name = item['tag'] + '.apk'
                f = files[name]
                pins[name] = {'url': BASE + '/' + f['path'], 'sha256': f['sha256'], 'size': f['size']}
        releases.sort(key=a.order, reverse=True)
        branch = a.alpine_branch()
        # The beta channel remains the newer of latest stable and latest beta.
        # The stable feed keeps its three releases; beta keeps just its head.
        for channel, directory, keep in [('stable', output, 3), ('beta', output / 'beta', 1)]:
            a.assemble(directory, releases, keep, branch, a.ROOT / 'firmware/alpine/keys',
                       fetch=lambda url: payloads[url], channel=channel)
        for directory, include_beta in [(output, False), (output / 'beta', True)]:
            shutil.rmtree(directory / 'latest-image')
            a.image_badge(directory, sorted(public.values(), key=a.order, reverse=True), include_beta)
        a.assemble_addons(output, branch, pins, fetch=lambda url: payloads[url])
        for item in self.snapshots.values():
            for f in item['files'].values():
                target = output / f['path']
                target.parent.mkdir(parents=True, exist_ok=True)
                if not target.exists():
                    shutil.copyfile(self.cache / f['path'], target)
        catalog = output / CATALOG
        catalog.parent.mkdir(parents=True, exist_ok=True)
        catalog.write_text(json.dumps({'schema': 1, 'snapshots': self.snapshots, 'artifacts': sorted(self.seen)}, sort_keys=True) + '\n')
        total = sum(p.stat().st_size for p in output.rglob('*') if p.is_file())
        if total > LIMIT:
            raise ValueError(f'Pages site {total} bytes exceeds {LIMIT}; deployment refused')
        print(f'Pages: {len(self.snapshots)} snapshots, {total:,} bytes')


def trusted_run(run, kind=None):
    workflow = run['path'].removeprefix('.github/workflows/')
    return (workflow in WORKFLOWS and (kind is None or WORKFLOWS[workflow] == kind)
            and run['event'] in ('workflow_dispatch', 'push')
            and run['head_repository']['full_name'] == a.REPOSITORY
            and (run['head_branch'] == 'main' or (workflow == 'release.yml' and
                 re.fullmatch(r'(release/\d+\.\d+|v\d+\.\d+\.\d+(?:-beta\.\d+)?)', run['head_branch']))))


def already_staged(tag):
    # Only production uses this; dry runs never call --stage.
    try:
        catalog = json.loads(a.fetch(BASE + '/' + CATALOG))
        if 'os/' + tag in catalog['snapshots']:
            return True
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
    page = 1
    while True:
        batch = json.loads(a.fetch(f'https://api.github.com/repos/{a.REPOSITORY}/actions/artifacts?per_page=100&page={page}', 'application/vnd.github+json'))['artifacts']
        for artifact in batch:
            if artifact['name'] == 'pages-candidate-os-' + tag and not artifact['expired']:
                run_id = artifact['workflow_run']['id']
                run = json.loads(a.fetch(f'https://api.github.com/repos/{a.REPOSITORY}/actions/runs/{run_id}', 'application/vnd.github+json'))
                if trusted_run(run, 'os'):
                    return True
        if len(batch) < 100:
            return False
        page += 1


def stage(directory, kind, tag):
    """Add transport metadata only after the production build has passed checks."""
    identity(kind, tag)
    if kind == 'os' and os.environ.get('GITHUB_ACTIONS') == 'true' and already_staged(tag):
        print(f'{tag}: reusing the original staged packages')
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write('upload=false\n')
        return
    directory = Path(directory)
    if (directory / 'snapshot.json').exists():
        raise ValueError('Candidate already staged')
    for p in directory.iterdir():
        filename(p.name)
        if not p.is_file() or p.is_symlink():
            raise ValueError('Candidate must contain regular files only')
    sums = a.checksums((directory / 'SHA256SUMS').read_text())
    if set(sums) != {p.name for p in directory.iterdir()} - {'SHA256SUMS'}:
        raise ValueError('Incomplete candidate checksums')
    for name, sha in sums.items():
        if hashlib.sha256((directory / name).read_bytes()).hexdigest() != sha:
            raise ValueError(f'Candidate checksum mismatch: {name}')
    (directory / 'snapshot.json').write_text(json.dumps({'kind': kind, 'tag': tag}) + '\n')
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write('upload=true\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--bootstrap', action='store_true', help='explicit first import from legacy Releases')
    parser.add_argument('--no-ingest', action='store_true', help='read-only legacy/site validation')
    parser.add_argument('--stage', type=Path)
    parser.add_argument('--kind', choices=['os', 'addon'])
    parser.add_argument('--tag')
    args = parser.parse_args()
    if args.stage:
        stage(args.stage, args.kind, args.tag)
        return
    if not args.output:
        parser.error('--output required')
    with tempfile.TemporaryDirectory() as tmp:
        store = Store(tmp)
        try:
            store.load()
        except urllib.error.HTTPError as error:
            if error.code != 404 or not args.bootstrap:
                raise
            store.bootstrap()
        if not args.no_ingest:
            store.ingest()
        store.render(args.output)


if __name__ == '__main__':
    main()
