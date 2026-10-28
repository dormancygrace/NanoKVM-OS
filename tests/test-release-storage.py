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
    sys.exit(1 if os.environ.get('FAIL_DISPATCH') else 0)
assert a[0] == 'release', a
cmd, tag = a[1:3]
if os.environ.get('FAIL') == cmd + ':' + tag:
    sys.exit(1)
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
                                      'NanoKVM-OS-Image-v1.5-apps-v3.3.1.img.zip'}:
            (self.directory / name).write_text('content of ' + name)
        apk = self.directory / 'nanokvm-3.3.1-r0.apk'
        (self.directory / 'packages.json').write_text(json.dumps({
            'release': '3.3.1', 'packages': {'nanokvm': {
                'changed': True, 'apk': apk.name, 'apk_sha256': assets.digest(apk)}}}))
        self.state = self.root / 'state.json'
        self.state.write_text(json.dumps({'calls': [], 'releases': {}}))
        self.env = {**os.environ, 'PATH': str(self.root) + ':' + os.environ['PATH'],
                    'STATE': str(self.state), 'OUT': str(self.root), 'NANOKVM_VERSION': 'v3.3.1',
                    'NANOKVM_IMAGE_VERSION': 'v1.5', 'GITHUB_REPOSITORY': 'example/test',
                    'IMAGE': 'true', 'DRAFT': 'false', 'PREVIOUS': 'v3.3.0',
                    'SHA': 'a' * 40, 'DEFAULT_BRANCH': 'main'}

    def invoke(self, **overrides):
        result = subprocess.run(['bash', str(ROOT / 'scripts/publish-release.sh')],
                                env={**self.env, **overrides}, capture_output=True, text=True)
        return result, json.loads(self.state.read_text())

    def test_split_public_downloads_and_separate_hashes(self):
        assets.split(self.directory)
        self.assertEqual({p.name for p in (self.directory / 'public').iterdir()}, {
            'NanoKVM-OS-v3.3.1-source.tar.xz', 'NanoKVM-OS-Image-v1.5-apps-v3.3.1.img.zip', 'SHA256SUMS'})
        for part in ('public', 'packages'):
            result = subprocess.run(['sha256sum', '-c', 'SHA256SUMS'], cwd=self.directory / part,
                                    capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('.apk', (self.directory / 'public/SHA256SUMS').read_text())
        self.assertNotIn('.img.zip', (self.directory / 'packages/SHA256SUMS').read_text())

    def test_optional_stock_installer_is_public_and_survives_publication_retry(self):
        name = 'nanokvm_3.3.1.tar.gz'
        (self.directory / name).write_text('stock installer')
        assets.split(self.directory)
        self.assertTrue((self.directory / 'public' / name).is_file())
        self.assertIn(name, (self.directory / 'public/SHA256SUMS').read_text())
        result, state = self.invoke(FAIL_PUBLISH='v3.3.1')
        self.assertNotEqual(result.returncode, 0)
        original = state['releases']['v3.3.1']['assets']
        self.resign_local_assembly()
        result, state = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state['releases']['v3.3.1']['assets'], original)

    def test_wrong_stock_installer_version_fails_before_moving_assets(self):
        (self.directory / 'nanokvm_3.3.2.tar.gz').write_text('wrong version')
        with self.assertRaisesRegex(ValueError, 'version differs'):
            assets.split(self.directory)
        self.assertFalse((self.directory / 'public').exists())

    def test_patch_without_image_contains_only_source_and_checksums(self):
        (self.directory / 'NanoKVM-OS-Image-v1.5-apps-v3.3.1.img.zip').unlink()
        (self.directory / 'build-manifest.json').unlink()
        assets.split(self.directory)
        result, state = self.invoke(IMAGE='false')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual({a['name'] for a in state['releases']['v3.3.1']['assets']},
                         {'SHA256SUMS', 'NanoKVM-OS-v3.3.1-source.tar.xz'})

    def resign_local_assembly(self):
        # A rerun of sign assembles and signs again. Both APKs/indexes and the
        # image built from them can change, despite using the same source SHA.
        for name in ('nanokvm-3.3.1-r0.apk', 'Packages.adb', 'APKINDEX.tar.gz'):
            (self.directory / 'packages' / name).write_text('new signature: ' + name)
        manifest = self.directory / 'packages/packages.json'
        data = json.loads(manifest.read_text())
        data['packages']['nanokvm']['apk_sha256'] = assets.digest(self.directory / 'packages/nanokvm-3.3.1-r0.apk')
        manifest.write_text(json.dumps(data))
        (self.directory / 'public/NanoKVM-OS-Image-v1.5-apps-v3.3.1.img.zip').write_text('rebuilt image')
        for name in ('public', 'packages'):
            sums = self.directory / name / 'SHA256SUMS'
            sums.write_text(''.join(f'{assets.digest(f)}  {f.name}\n'
                                   for f in sorted(sums.parent.iterdir()) if f != sums))

    def test_failure_after_storage_is_published_can_resume(self):
        assets.split(self.directory)
        result, state = self.invoke(FAIL_PUBLISH='v3.3.1')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(state['releases']['os-packages-v3.3.1']['draft'])
        self.assertTrue(state['releases']['v3.3.1']['draft'])
        original = json.loads(json.dumps(state['releases']))
        count = len(state['calls'])
        self.resign_local_assembly()
        result, state = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(state['releases']['v3.3.1']['draft'])
        for tag in original:
            self.assertEqual(state['releases'][tag]['assets'], original[tag]['assets'])
        self.assertFalse(any(c[:2] == ['release', 'upload'] for c in state['calls'][count:]))

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
            'SHA256SUMS', 'NanoKVM-OS-v3.3.1-source.tar.xz', 'NanoKVM-OS-Image-v1.5-apps-v3.3.1.img.zip'})
        self.assertEqual(state['calls'][-1], ['workflow', 'run', 'apk-repository.yml', '--ref', 'main'])
        create = next(c for c in state['calls'] if c[:3] == ['release', 'create', 'v3.3.1'])
        self.assertIn(self.env['SHA'], create)
        self.assertIn(self.env['PREVIOUS'], create)

    def test_minor_draft_ready_for_manual_publication_and_notes_preserved(self):
        assets.split(self.directory)
        result, state = self.invoke(DRAFT='true')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(state['releases']['v3.3.1']['draft'])
        self.assertFalse(state['releases']['os-packages-v3.3.1']['draft'])
        self.assertFalse(any(c[0] == 'workflow' for c in state['calls']))
        state['releases']['v3.3.1']['body'] = 'My edited release notes!\n' + assets.marker('v3.3.1')
        self.state.write_text(json.dumps(state))
        result, state = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state['releases']['v3.3.1']['body'], 'My edited release notes!\n' + assets.marker('v3.3.1'))

    def test_failed_upload_and_publication_are_retriable(self):
        assets.split(self.directory)
        for failure in ('upload:os-packages-v3.3.1', 'upload:v3.3.1',
                        'edit:os-packages-v3.3.1', 'edit:v3.3.1'):
            with self.subTest(failure=failure):
                self.state.write_text(json.dumps({'calls': [], 'releases': {}}))
                result, state = self.invoke(FAIL=failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(state['releases'].get('v3.3.1', {'draft': True})['draft'])
                self.assertFalse(any(c[0] == 'workflow' for c in state['calls']))
                result, state = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertFalse(state['releases']['v3.3.1']['draft'])

    def manually_publish_os_after_failed_storage(self):
        assets.split(self.directory)
        result, state = self.invoke(DRAFT='true', FAIL_PUBLISH='os-packages-v3.3.1')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(state['releases']['os-packages-v3.3.1']['draft'])
        state['releases']['v3.3.1']['draft'] = False
        self.state.write_text(json.dumps(state))
        return state

    def test_manual_os_publication_recovers_verified_storage_without_local_build(self):
        state = self.manually_publish_os_after_failed_storage()
        count = len(state['calls'])
        public = state['releases']['v3.3.1'].copy()
        # Recovery must use original remote bytes, even with no local build.
        result, state = self.invoke(OUT=str(self.root / 'no-build'))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(state['releases']['os-packages-v3.3.1']['draft'])
        self.assertEqual(public, state['releases']['v3.3.1'])
        calls = state['calls'][count:]
        self.assertFalse(any(c[:2] in (['release', 'upload'], ['release', 'create']) for c in calls))
        self.assertEqual(calls[-2][:3], ['release', 'edit', 'os-packages-v3.3.1'])
        self.assertEqual(calls[-1][:2], ['workflow', 'run'])

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

    def test_published_legacy_release_still_dispatches_without_storage(self):
        self.state.write_text(json.dumps({'calls': [], 'releases': {
            'v3.3.1': {'tag_name': 'v3.3.1', 'draft': False, 'body': 'Legacy', 'assets': []}}}))
        result, state = self.invoke(OUT=str(self.root / 'no-build'))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state['calls'][-1][:2], ['workflow', 'run'])

    def test_remote_pair_resume_works_without_any_local_build(self):
        assets.split(self.directory)
        result, state = self.invoke(DRAFT='true')
        self.assertEqual(result.returncode, 0, result.stderr)
        result, state = self.invoke(OUT=str(self.root / 'no-build'))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(state['releases']['v3.3.1']['draft'])

    def test_recut_with_deleted_os_draft_fails_without_touching_published_storage(self):
        assets.split(self.directory)
        result, state = self.invoke(DRAFT='true')
        self.assertEqual(result.returncode, 0, result.stderr)
        del state['releases']['v3.3.1']
        self.state.write_text(json.dumps(state))
        count = len(state['calls'])
        self.resign_local_assembly()
        result, state = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('OS draft is missing', result.stderr)
        self.assertEqual([c[0] for c in state['calls'][count:]], ['api', 'api'])

    def test_resume_rejects_corrupt_original_storage_or_public_downloads(self):
        assets.split(self.directory)
        result, state = self.invoke(DRAFT='true')
        self.assertEqual(result.returncode, 0, result.stderr)
        snapshot = json.dumps(state)
        for tag, name in [('os-packages-v3.3.1', 'nanokvm-3.3.1-r0.apk'),
                          ('v3.3.1', 'NanoKVM-OS-Image-v1.5-apps-v3.3.1.img.zip')]:
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

    def test_old_format_draft_assets_are_removed_before_storage_publication(self):
        assets.split(self.directory)
        result, state = self.invoke(FAIL_PUBLISH='os-packages-v3.3.1')
        self.assertNotEqual(result.returncode, 0)
        state['releases']['v3.3.1']['assets'].append({'name': 'old.apk', 'data': 'stale'})
        self.state.write_text(json.dumps(state))
        result, state = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('old.apk', {a['name'] for a in state['releases']['v3.3.1']['assets']})

    def test_dispatch_retry_does_not_touch_published_assets(self):
        assets.split(self.directory)
        result, state = self.invoke(FAIL_DISPATCH='1')
        self.assertNotEqual(result.returncode, 0)
        count = len(state['calls'])
        result, state = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([c[0] for c in state['calls'][count:]], ['api', 'api', 'workflow'])

    def test_beta_never_becomes_latest(self):
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
