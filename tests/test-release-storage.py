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
    state['releases'][tag] = dict(tag_name=tag, draft=True, assets=[],
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
        if tag.startswith('v'):
            assert not state['releases']['os-packages-' + tag]['draft']
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

    def invoke(self, **overrides):
        result = subprocess.run(['bash', str(ROOT / 'scripts/publish-release.sh')],
                                env={**self.env, **overrides}, capture_output=True, text=True)
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

    def test_publish_orders_storage_before_os_and_dispatches_last(self):
        assets.split(self.directory)
        result, state = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        public, storage = state['releases']['v3.3.1'], state['releases']['os-packages-v3.3.1']
        self.assertFalse(public['draft'])
        self.assertFalse(storage['draft'])
        self.assertTrue(storage['prerelease'])
        self.assertFalse(storage['latest'])
        self.assertIn(assets.marker('v3.3.1'), public['body'])
        self.assertEqual({a['name'] for a in public['assets']}, {
            'SHA256SUMS', 'NanoKVM-OS-v3.3.1-source.tar.xz', 'NanoKVM-OS-v3.3.1.img.zip'})
        self.assertEqual(state['calls'][-1], ['workflow', 'run', 'apk-repository.yml', '--ref', 'main'])
        create = next(c for c in state['calls'] if c[:3] == ['release', 'create', 'v3.3.1'])
        self.assertIn(self.env['SHA'], create)
        self.assertIn(self.env['PREVIOUS'], create)

    def manually_publish_os_after_failed_storage(self):
        assets.split(self.directory)
        result, state = self.invoke(DRAFT='true', FAIL_PUBLISH='os-packages-v3.3.1')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(state['releases']['os-packages-v3.3.1']['draft'])
        state['releases']['v3.3.1']['draft'] = False
        self.state.write_text(json.dumps(state))
        return state

    def test_manual_publication_refuses_missing_incomplete_or_corrupt_storage(self):
        state = self.manually_publish_os_after_failed_storage()
        snapshot = json.dumps(state)
        for problem in ('missing', 'missing-index', 'published-missing-index', 'bad-hash', 'missing-apk-with-valid-checksums'):
            with self.subTest(problem=problem):
                state = json.loads(snapshot)
                storage = state['releases']['os-packages-v3.3.1']
                if problem == 'missing':
                    del state['releases']['os-packages-v3.3.1']
                elif problem in ('missing-index', 'published-missing-index'):
                    storage['draft'] = problem != 'published-missing-index'
                    storage['assets'] = [a for a in storage['assets'] if a['name'] != 'Packages.adb']
                elif problem == 'bad-hash':
                    next(a for a in storage['assets'] if a['name'].endswith('.apk'))['data'] = 'corrupt'
                else:
                    storage['assets'] = [a for a in storage['assets'] if not a['name'].endswith('.apk')]
                    sums = next(a for a in storage['assets'] if a['name'] == 'SHA256SUMS')
                    sums['data'] = ''.join(line + '\n' for line in sums['data'].splitlines()
                                           if not line.endswith('.apk'))
                count = len(state['calls'])
                self.state.write_text(json.dumps(state))
                result, after = self.invoke()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('os-packages-v3.3.1', result.stderr)
                self.assertFalse(any(c[0] == 'workflow' or c[:2] == ['release', 'edit']
                                     for c in after['calls'][count:]))

    def test_resume_rejects_corrupt_original_storage_or_public_downloads(self):
        assets.split(self.directory)
        result, state = self.invoke(DRAFT='true')
        self.assertEqual(result.returncode, 0, result.stderr)
        snapshot = json.dumps(state)
        for tag, name in [('os-packages-v3.3.1', 'nanokvm-3.3.1-r0.apk'),
                          ('v3.3.1', 'NanoKVM-OS-v3.3.1.img.zip')]:
            for corruption in ('bytes', 'missing'):
                with self.subTest(tag=tag, corruption=corruption):
                    state = json.loads(snapshot)
                    if corruption == 'bytes':
                        next(a for a in state['releases'][tag]['assets'] if a['name'] == name)['data'] = 'corrupt'
                    else:
                        state['releases'][tag]['assets'] = [a for a in state['releases'][tag]['assets'] if a['name'] != name]
                    self.state.write_text(json.dumps(state))
                    count = len(state['calls'])
                    result, state = self.invoke()
                    self.assertNotEqual(result.returncode, 0)
                    self.assertTrue(state['releases']['v3.3.1']['draft'])
                    self.assertFalse(any(c[0] == 'workflow' or c[:2] in (['release', 'edit'], ['release', 'upload'])
                                         for c in state['calls'][count:]))

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


if __name__ == '__main__':
    unittest.main()
