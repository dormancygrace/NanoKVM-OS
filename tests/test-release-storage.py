#!/usr/bin/env python3
"""Execute the production asset splitter/publisher against an offline GitHub CLI."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
import release_assets as assets
import pages_store as store
import hashlib
import io
import zipfile
import importlib.util
from unittest import mock

STUB = r"""#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
p = Path(os.environ['STATE'])
state = json.loads(p.read_text())
a = sys.argv[1:]
state['calls'].append(a)
def save():
    p.write_text(json.dumps(state))
save()
if a[0] == 'api':
    assert '--paginate' in a and '--slurp' in a
    if os.environ.get('FAIL_API'):
        print('gh: Forbidden (HTTP 403)', file=sys.stderr)
        sys.exit(1)
    print(json.dumps([list(state['releases'].values())]))
    sys.exit()
if a[:2] == ['workflow', 'run']:
    assert any(not r['draft'] and r['tag_name'].startswith('v') for r in state['releases'].values())
    sys.exit(0)
assert a[0] == 'release', a
cmd, tag = a[1:3]
r = state['releases'].get(tag)
if cmd == 'create':
    assert r is None and '--draft' in a
    state['releases'][tag] = dict(tag_name=tag, draft=True, assets=[], target_commitish=a[a.index('--target')+1],
        body=a[a.index('--notes')+1] if '--notes' in a else 'Generated notes',
        prerelease='--prerelease' in a, latest='--latest=false' not in a)
elif cmd == 'upload':
    assert r['draft']
    for path in a[4:]:
        f = Path(path)
        r['assets'] = [x for x in r['assets'] if x['name'] != f.name]
        r['assets'].append(dict(name=f.name, data=f.read_text()))
elif cmd == 'delete-asset':
    assert r['draft']
    r['assets'] = [x for x in r['assets'] if x['name'] != a[3]]
elif cmd == 'download':
    for item in r['assets']:
        if a[a.index('--pattern')+1] in ('*', item['name']):
            (Path(a[a.index('--dir')+1]) / item['name']).write_text(item['data'])
elif cmd == 'edit':
    assert r['draft']
    if '--draft=false' in a and os.environ.get('FAIL_PUBLISH') == tag:
        sys.exit(1)
    if '--notes-file' in a:
        r['body'] = Path(a[a.index('--notes-file')+1]).read_text()
    if '--draft=false' in a:
        r['draft'] = False
else:
    raise AssertionError(a)
save()
"""


class ReleaseStorageTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'gh').write_text(STUB)
        (self.root / 'gh').chmod(0o755)
        self.directory = self.root / 'assets'
        self.directory.mkdir()
        for name in assets.REQUIRED | {'nanokvm-3.3.1-r0.apk', 'build-manifest.json',
                                      'NanoKVM-OS-v3.3.1-source.tar.xz',
                                      'NanoKVM-OS-v3.3.1.img.zip'}:
            (self.directory / name).write_text('content of ' + name)
        apk = self.directory / 'nanokvm-3.3.1-r0.apk'
        (self.directory / 'packages.json').write_text(json.dumps({
            'release': '3.3.1', 'packages': {'nanokvm': {
                'changed': True, 'apk': apk.name, 'apk_sha256': assets.digest(apk)}}}))
        self.state = self.root / 'state.json'
        self.state.write_text(json.dumps({'calls': [], 'releases': {}}))
        self.env = {**os.environ, 'PATH': str(self.root) + ':' + os.environ['PATH'],
                    'STATE': str(self.state), 'OUT': str(self.root), 'NANOKVM_VERSION': 'v3.3.1',
                    'GITHUB_REPOSITORY': 'example/test',
                    'IMAGE': 'true', 'DRAFT': 'false', 'PREVIOUS': 'v3.3.0',
                    'SHA': 'a' * 40, 'DEFAULT_BRANCH': 'main'}

    def invoke(self, prepare=True, **overrides):
        for args in (['--prepare'], []) if prepare else ([],):
            result = subprocess.run(['bash', str(ROOT / 'scripts/publish-release.sh'), *args],
                                    env={**self.env, **overrides}, capture_output=True, text=True)
            if result.returncode:
                break
        return result, json.loads(self.state.read_text())

    def test_split_public_downloads_and_separate_hashes(self):
        assets.split(self.directory)
        self.assertEqual({p.name for p in (self.directory / 'public').iterdir()}, {
            'NanoKVM-OS-v3.3.1-source.tar.xz', 'NanoKVM-OS-v3.3.1.img.zip', 'SHA256SUMS'})
        for part in ('public', 'packages'):
            result = subprocess.run(['sha256sum', '-c', 'SHA256SUMS'], cwd=self.directory / part,
                                    capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('.apk', (self.directory / 'public/SHA256SUMS').read_text())
        self.assertNotIn('.img.zip', (self.directory / 'packages/SHA256SUMS').read_text())

    def test_wrong_or_duplicate_image_version_is_not_published(self):
        for name, duplicate in [('NanoKVM-OS-v3.3.2.img.zip', False),
                                ('NanoKVM-OS-Image-v1.8-apps-v3.3.2.img.zip', False),
                                ('NanoKVM-OS-Image-v1.8-apps-v3.3.1.img.zip', True)]:
            with self.subTest(name=name):
                # Recreate a fresh fixture for each independent rejected release.
                self.setUp()
                current = self.directory / 'NanoKVM-OS-v3.3.1.img.zip'
                if duplicate:
                    (self.directory / name).write_bytes(current.read_bytes())
                else:
                    current.rename(self.directory / name)
                assets.split(self.directory)
                result, state = self.invoke()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('incomplete or unexpected', result.stderr)
                self.assertFalse(state['releases'])

    def test_unknown_assets_fail_before_moving_anything(self):
        (self.directory / 'surprise.txt').write_text('unknown')
        with self.assertRaisesRegex(ValueError, 'Unclassified'):
            assets.split(self.directory)
        self.assertFalse((self.directory / 'public').exists())

    def test_only_os_downloads_are_published(self):
        assets.split(self.directory)
        result, state = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(set(state['releases']), {'v3.3.1'})
        public = state['releases']['v3.3.1']
        self.assertFalse(public['draft'])
        self.assertEqual({a['name'] for a in public['assets']}, {
            'SHA256SUMS', 'NanoKVM-OS-v3.3.1-source.tar.xz', 'NanoKVM-OS-v3.3.1.img.zip'})
        self.assertFalse(any(c[0] == 'workflow' for c in state['calls']))

    def test_published_assets_are_verified_and_never_replaced(self):
        assets.split(self.directory)
        result, state = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        count = len(state['calls'])
        result, state = self.invoke(prepare=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(c[:2] in (['release', 'edit'], ['release', 'upload'])
                             for c in state['calls'][count:]))
        next(a for a in state['releases']['v3.3.1']['assets'] if a['name'].endswith('.img.zip'))['data'] = 'corrupt'
        self.state.write_text(json.dumps(state))
        result, _ = self.invoke(prepare=False)
        self.assertNotEqual(result.returncode, 0)

    def test_draft_recut_does_not_publish_another_commits_downloads(self):
        assets.split(self.directory)
        result, state = self.invoke(DRAFT='true')
        self.assertEqual(result.returncode, 0, result.stderr)
        count = len(state['calls'])
        result, state = self.invoke(SHA='b' * 40)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('different commit', result.stderr)
        self.assertTrue(state['releases']['v3.3.1']['draft'])
        self.assertFalse(any(c[:2] in (['release', 'edit'], ['release', 'upload']) for c in state['calls'][count:]))

    def test_beta_never_becomes_latest(self):
        for suffix in ('source.tar.xz', 'img.zip'):
            old = 'NanoKVM-OS-v3.3.1' + ('-' if suffix.startswith('source') else '.') + suffix
            (self.directory / old).rename(self.directory / old.replace('v3.3.1', 'v3.4.0-beta.1'))
        assets.split(self.directory)
        result, state = self.invoke(NANOKVM_VERSION='v3.4.0-beta.1')
        self.assertEqual(result.returncode, 0, result.stderr)
        for release in state['releases'].values():
            self.assertFalse(release['latest'])
            self.assertTrue(release['prerelease'])

    def test_api_error_is_not_a_missing_release(self):
        assets.split(self.directory)
        result, state = self.invoke(FAIL_API='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(state['releases'], {})
        self.assertEqual(len(state['calls']), 1)


class PagesStoreTests(unittest.TestCase):
    def test_retention_separates_channels_and_addons(self):
        items = {}
        published = set()
        for kind, tags in [('os', ['v4.0.0', 'v4.0.1', 'v4.0.2', 'v4.0.3',
                                  'v4.1.0-beta.1', 'v4.1.0-beta.2']),
                           ('addon', ['nanokvm-ipmi-0.1.0-r0', 'nanokvm-ipmi-0.1.0-r1',
                                      'nanokvm-ipmi-0.1.0-r2', 'nanokvm-ipmi-0.1.0-r3'])]:
            for tag in tags:
                items[store.identity(kind, tag)] = dict(kind=kind, tag=tag, files={})
                if kind == 'os':
                    published.add(tag)
        result = store.retained(items, published)
        self.assertEqual(set(result), {'os/v4.0.1', 'os/v4.0.2', 'os/v4.0.3', 'os/v4.1.0-beta.2',
                                      'addon/nanokvm-ipmi-0.1.0-r1', 'addon/nanokvm-ipmi-0.1.0-r2',
                                      'addon/nanokvm-ipmi-0.1.0-r3'})
        # A draft cannot evict the current stable release set.
        items['os/v5.0.0'] = dict(kind='os', tag='v5.0.0', files={})
        self.assertEqual(set(store.retained(items, published)), set(result) | {'os/v5.0.0'})

    def test_every_pending_os_snapshot_above_its_head_is_kept(self):
        for published, pending, kept in [({'v4.2.0'}, {'v4.1.0', 'v4.3.0', 'v4.4.0'}, {'v4.3.0', 'v4.4.0'}),
                                         ({'v4.1.0'}, {'v4.2.0-beta.1', 'v4.2.0-beta.2'}, {'v4.2.0-beta.1', 'v4.2.0-beta.2'})]:
            items = {store.identity('os', t): dict(kind='os', tag=t, files={}) for t in published | pending}
            self.assertEqual({k[3:] for k in store.retained(items, published)} - published, kept)

    def test_cleanup_preserves_os_releases_and_user_downloads(self):
        spec = importlib.util.spec_from_file_location('cleanup', ROOT / 'scripts/cleanup-release-packages.py')
        cleanup = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cleanup)
        releases = [dict(id=1, tag_name='v4.0.0', draft=False, assets=[
            dict(id=10, name='nanokvm-4.0.0-r0.apk'), dict(id=11, name='NanoKVM-OS-v4.0.0.img.zip'),
            dict(id=12, name='NanoKVM-OS-v4.0.0-source.tar.xz'), dict(id=13, name='nanokvm_4.0.0.tar.gz')]),
            dict(id=2, tag_name='os-packages-v4.0.0', draft=False, assets=[]),
            dict(id=3, tag_name='nanokvm-ipmi-0.1.0-r0', draft=False, assets=[]),
            dict(id=4, tag_name='nanokvm-rustdesk-0.5.3-r6', draft=False, assets=[
                dict(id=40, name='nanokvm-rustdesk-0.5.3-r6.apk'),
                dict(id=41, name='nanokvm-rustdesk-0.5.3-source.tar.gz'), dict(id=42, name='APKBUILD')])]
        changes = cleanup.plan(releases)
        self.assertEqual(changes, [dict(tag='v4.0.0', assets=[dict(id=10, name='nanokvm-4.0.0-r0.apk')]),
                                   dict(release=2, tag='os-packages-v4.0.0'),
                                   dict(release=3, tag='nanokvm-ipmi-0.1.0-r0'),
                                   dict(tag='nanokvm-rustdesk-0.5.3-r6', assets=[dict(id=40, name='nanokvm-rustdesk-0.5.3-r6.apk')])])

    def test_only_production_workflow_provenance_is_accepted(self):
        run = dict(path='.github/workflows/release.yml', event='workflow_dispatch', head_branch='main',
                   head_repository={'full_name': store.a.REPOSITORY})
        self.assertTrue(store.trusted_run(run, 'os'))
        for change in [dict(event='pull_request'), dict(head_branch='feature/foo'),
                       dict(head_repository={'full_name': 'someone/fork'}), dict(path='.github/workflows/ci.yml')]:
            with self.subTest(change=change):
                self.assertFalse(store.trusted_run({**run, **change}))
        self.assertFalse(store.trusted_run(run, 'addon'))

    def test_object_paths_and_hashes_are_verified(self):
        with tempfile.TemporaryDirectory() as tmp:
            s = store.Store(tmp)
            item = s.put('test.apk', b'payload')
            self.assertEqual(s.get(item), b'payload')
            for change in [{'path': '../outside'}, {'path': 'https://example.org/key'},
                           {'sha256': '0' * 64}, {'size': 900}]:
                with self.subTest(change=change), self.assertRaises(ValueError):
                    s.get({**item, **change})
            for feed, ok in [('beta/alpine/v3.24/riscv64/test.apk', True), ('addons/v3.24/riscv64/other.apk', False),
                             ('package-store/catalog.json', False), ('alpine/v3.24/riscv64/../test.apk', False)]:
                s.cache, s.served = Path(tmp) / feed.replace('/', '_'), {item['path']: feed}
                with self.subTest(feed=feed), mock.patch.object(store.a, 'fetch', return_value=b'payload') as fetch:
                    self.assertEqual(s.get(item), b'payload') if ok else self.assertRaises(ValueError, s.get, item)
                    self.assertEqual(fetch.call_args_list, [mock.call(f"{store.BASE}/{feed}?sha256={item['sha256']}")] if ok else [])
            s.add('addon', 'nanokvm-ipmi-0.1.0-r0', {'test.apk': item}, 'a' * 40)
            with self.assertRaisesRegex(ValueError, 'immutable'):
                s.add('addon', 'nanokvm-ipmi-0.1.0-r0', {}, 'a' * 40)

    def test_candidate_checksums_paths_and_producer_kind(self):
        tag = 'nanokvm-ipmi-0.1.0-r0'
        def archive(extra=None, checksum=None):
            data = io.BytesIO()
            with zipfile.ZipFile(data, 'w') as z:
                z.writestr('snapshot.json', json.dumps(dict(kind='addon', tag=tag, commit='a' * 40)))
                z.writestr(tag + '.apk', b'payload')
                z.writestr('SHA256SUMS', (checksum or hashlib.sha256(b'payload').hexdigest()) + '  ' + tag + '.apk\n')
                if extra:
                    z.writestr(extra, b'bad')
            return data.getvalue()
        with tempfile.TemporaryDirectory() as tmp:
            s = store.Store(tmp)
            s.candidate(archive(), 'addon')
            self.assertIn('addon/' + tag, s.snapshots)
            with self.assertRaisesRegex(ValueError, 'commit differs'):
                s.candidate(archive(), 'addon', 'b' * 40)
            for data, kind in [(archive('../escape'), 'addon'), (archive(checksum='0' * 64), 'addon'),
                               (archive(), 'os')]:
                with self.assertRaises(ValueError):
                    s.candidate(data, kind)
    def test_ingest_checks_provenance_and_consumes_all_completed_candidates(self):
        # Independent producer runs must survive a superseded Pages dispatch.
        artifacts = [dict(id=i, name='pages-candidate-test', expired=False,
                          workflow_run={'id': i}, archive_download_url=f'https://api.github.com/artifact/{i}')
                     for i in (1, 2, 3, 4)]
        def fetch(url, *args):
            if '/actions/artifacts?' in url:
                return json.dumps(dict(artifacts=artifacts)).encode()
            if '/actions/runs/' in url:
                i = int(url.rsplit('/', 1)[1])
                return json.dumps(dict(path='.github/workflows/addon.yml',
                    event='pull_request' if i == 3 else 'workflow_dispatch',
                    head_branch='main', head_sha='a' * 40, head_repository={'full_name': store.a.REPOSITORY},
                    status='in_progress' if i == 4 else 'completed')).encode()
            return url.encode()
        with tempfile.TemporaryDirectory() as tmp:
            s = store.Store(tmp)
            with mock.patch.object(store.a, 'fetch', side_effect=fetch), mock.patch.object(s, 'candidate') as candidate:
                s.ingest()
            self.assertEqual(candidate.call_args_list, [mock.call(b'https://api.github.com/artifact/1', 'addon', 'a' * 40),
                                                       mock.call(b'https://api.github.com/artifact/2', 'addon', 'a' * 40)])
            self.assertEqual(s.seen, {1, 2, 3})

    def test_staged_os_reuse_requires_the_same_commit(self):
        tag = 'v4.1.0'
        catalog = {'snapshots': {'os/' + tag: {'commit': 'a' * 40}}}
        with mock.patch.object(store, 'read_catalog', return_value=catalog):
            self.assertTrue(store.already_staged(tag, 'a' * 40))
            with self.assertRaisesRegex(ValueError, 'different commit'):
                store.already_staged(tag, 'b' * 40)
        data = io.BytesIO()
        with zipfile.ZipFile(data, 'w') as z:
            z.writestr('snapshot.json', json.dumps(dict(kind='os', tag=tag, commit='a' * 40)))
        def fetch(url, *args):
            if '/actions/artifacts?' in url:
                return json.dumps({'artifacts': [dict(name='pages-candidate-os-' + tag, expired=False,
                    workflow_run={'id': 1}, archive_download_url='https://api.github.com/file')]}).encode()
            if '/actions/runs/' in url:
                return json.dumps(dict(path='.github/workflows/release.yml', event='workflow_dispatch',
                    head_branch='main', head_sha='a' * 40, head_repository={'full_name': store.a.REPOSITORY})).encode()
            return data.getvalue()
        with mock.patch.object(store, 'read_catalog', return_value={'snapshots': {}}), mock.patch.object(store.a, 'fetch', side_effect=fetch):
            self.assertTrue(store.already_staged(tag, 'a' * 40))
            with self.assertRaisesRegex(ValueError, 'different commit'):
                store.already_staged(tag, 'b' * 40)

    def test_catalog_is_uncached(self):
        with mock.patch.object(store.a, 'fetch', return_value=b'{}') as fetch:
            store.read_catalog()
            store.read_catalog()
            urls = [call.args[0] for call in fetch.call_args_list]
            self.assertNotEqual(urls[0], urls[1])
            self.assertTrue(all(url.startswith(store.BASE + '/' + store.CATALOG + '?fresh=') for url in urls))



if __name__ == '__main__':
    unittest.main()
