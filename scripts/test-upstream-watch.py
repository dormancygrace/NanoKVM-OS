#!/usr/bin/env python3
"""Offline contracts for upstream monitoring; no real remote writes."""
import importlib.util
import sys
import io
import json
import tarfile
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
spec = importlib.util.spec_from_file_location('upstream', ROOT / 'scripts/check-upstream.py')
u = importlib.util.module_from_spec(spec)
spec.loader.exec_module(u)


class WatchTests(unittest.TestCase):
    def test_json_c_tracks_stable_release_tags(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        source = next(s for s in sources if s['name'] == 'json_c')
        tags = [{'name': n} for n in ('json-c-0.19-20260627', 'json-c-0.19',
                                     'json-c-0.19.99', 'json-c-0.20-rc1-20261001')]
        with patch.object(u, 'github', return_value=tags):
            result = u.check(source)
            self.assertEqual(result['current'], '0.19')
            self.assertEqual(result['latest'], '0.19')
            self.assertEqual(result['status'], 'current')
        with patch.object(u, 'github', return_value=tags + [{'name': 'json-c-0.20-20270101'}]):
            self.assertEqual(u.check(source)['status'], 'update available')

    def test_inventory_matches_sources(self):
        import json
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        self.assertEqual(len(sources), len({s['name'] for s in sources}))
        for source in sources:
            with self.subTest(source=source['name']):
                self.assertTrue(u.current_pin(ROOT, source))

    @staticmethod
    def alpine_index(value, missing=None):
        data = ''.join('P:' + name + '\nV:' + value + '\nA:riscv64\n\n'
                       for name in ('openssl', 'libssl3', 'libcrypto3') if name != missing).encode()
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode='w:gz') as archive:
            item = tarfile.TarInfo('APKINDEX')
            item.size = len(data)
            archive.addfile(item, io.BytesIO(data))
        return output.getvalue()

    def test_openssl_uses_alpine_not_historical_buildroot(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        selected = [s for s in sources if 'openssl' in s['name'].lower()]
        self.assertEqual(len(selected), 1)
        self.assertEqual(selected[0]['kind'], 'alpine-stock')
        self.assertNotIn('buildroot', selected[0]['path'])

    def test_stock_alpine_does_not_require_c906_rebuild(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        source = next(s for s in sources if s['kind'] == 'alpine-stock')
        with patch.object(u, 'request_bytes', return_value=self.alpine_index('3.5.9-r0')), patch.object(u, 'request') as recipe:
            result = u.check(source)
            self.assertEqual(result['status'], 'repository-managed')
            self.assertEqual(result['latest'], '3.5.9-r0')
            self.assertIn('not inspected', result['detail'])
            recipe.assert_not_called()
        with patch.object(u, 'request_bytes', return_value=self.alpine_index('3.5.9-r0', missing='libssl3')):
            self.assertEqual(u.check(source)['status'], 'error')

    def test_retired_buildroot_packages_are_not_current_candidates(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        retired = {'openvpn3', 'Asio', 'Superfile upstream', 'apk-tools', 'nanokvm-vendor-libc'}
        self.assertFalse(retired.intersection(s['name'] for s in sources))

    def test_alpine_baseline_revision_not_local_increment(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        source = dict(name='Optional C906 recipe', path='scripts/build-alpine-tuned-packages.sh',
                      pattern=r'^APORTS_COMMIT=\$\{APORTS_COMMIT:-([0-9a-f]{40})\}',
                      kind='alpine-package', aport='main/openssl', arch='riscv64',
                      packages=['openssl', 'libssl3', 'libcrypto3'],
                      branch_pin=dict(path='scripts/build-alpine-packages.sh',
                                      pattern=r'ALPINE_VERSION="([0-9.]+)"'))
        for upstream, expected in [('3.5.8-r0', 'current'), ('3.5.8-r1', 'C906 rebuild required'),
                                   ('3.5.8-r10', 'C906 rebuild required'), ('3.5.9-r0', 'C906 rebuild required'),
                                   ('3.5.7-r10', 'pinned recipe ahead')]:
            with self.subTest(upstream=upstream), patch.object(u, 'request', return_value='pkgver=3.5.8\npkgrel=0\n') as recipe, patch.object(u, 'request_bytes', return_value=self.alpine_index(upstream)) as index:
                result = u.check(source)
                self.assertEqual(result['status'], expected)
                self.assertIn('C906 build: 3.5.8-r1', result['current'])
                self.assertIn('/v3.24/main/riscv64/APKINDEX.tar.gz', index.call_args.args[0])
                self.assertIn('ref=' + u.current_pin(ROOT, source), recipe.call_args.args[0])
        self.assertGreater(u.alpine_version('3.5.8-r10'), u.alpine_version('3.5.8-r2'))

    def test_alpine_missing_subpackage_and_recipe_failure_are_visible(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        source = dict(name='Optional C906 recipe', path='scripts/build-alpine-tuned-packages.sh',
                      pattern=r'^APORTS_COMMIT=\$\{APORTS_COMMIT:-([0-9a-f]{40})\}',
                      kind='alpine-package', aport='main/openssl', arch='riscv64',
                      packages=['openssl', 'libssl3', 'libcrypto3'],
                      branch_pin=dict(path='scripts/build-alpine-packages.sh',
                                      pattern=r'ALPINE_VERSION="([0-9.]+)"'))
        with patch.object(u, 'request', return_value='pkgver=3.5.8\npkgrel=0\n'), patch.object(u, 'request_bytes', return_value=self.alpine_index('3.5.9-r0', missing='libssl3')):
            result = u.check(source)
            self.assertEqual(result['status'], 'error')
            self.assertIn('Missing Alpine packages: libssl3', result['detail'])
        with patch.object(u, 'request', return_value='pkgver=$(false)\npkgrel=0\n'):
            self.assertEqual(u.check(source)['status'], 'error')
        with patch.object(u, 'request', side_effect=OSError('unavailable')):
            self.assertEqual(u.check(source)['status'], 'error')
        with self.assertRaises(ValueError):
            u.alpine_version('3.6.0_rc1-r0')

    def test_versions_numeric_and_no_downgrade(self):
        self.assertGreater(u.version('7.2.10'), u.version('7.2.9'))
        self.assertEqual(u.version('2026.08'), u.version('2026.8.0'))
        with self.assertRaises(ValueError):
            u.version('7.3-rc1')
        source = dict(name='example', path='pin', pattern=r'([0-9.]+)', kind='index',
                      url='https://example.test/', upstream_pattern=r'v([0-9]+\.[0-9]+\.[0-9]+)\b')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'pin').write_text('7.2.10')
            with patch.object(u, 'request', return_value='v7.2.9 v7.1.99'):
                self.assertEqual(u.check(source, root)['status'], 'pinned ahead')
            with patch.object(u, 'request', return_value='v7.2.11'):
                self.assertEqual(u.check(source, root)['status'], 'update available')
            with patch.object(u, 'request', side_effect=OSError('unavailable')):
                self.assertEqual(u.check(source, root)['status'], 'error')

    def test_missing_pin_is_visible(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'pin').write_text('no version')
            result = u.check(dict(name='x', path='pin', pattern=r'(v[0-9]+)'), root)
            self.assertEqual(result['status'], 'error')
            self.assertIn('exactly one', result['detail'])

    def test_unchanged_dashboard_no_write(self):
        issue = dict(number=7, title=u.TITLE, body=u.MARKER + '\nreport', state='open', html_url='test')
        with patch.object(u, 'github', return_value=[issue]) as api:
            u.publish('owner/repo', issue['body'], True)
            self.assertEqual(api.call_count, 1)

    def test_changed_dashboard_updates_existing(self):
        issue = dict(number=7, title=u.TITLE, body=u.MARKER + '\nold', state='open', html_url='test')
        with patch.object(u, 'github', side_effect=[[issue], {}]) as api:
            u.publish('owner/repo', u.MARKER + '\nnew', True)
            self.assertEqual(api.call_args.args[0], 'repos/owner/repo/issues/7')
            self.assertEqual(api.call_args.args[2], 'PATCH')

    def test_foreign_issue_not_edited(self):
        issue = dict(number=1, title=u.TITLE, body='not our marker', state='open', html_url='test')
        with patch.object(u, 'github', side_effect=[[issue], {'html_url':'new'}]) as api:
            u.publish('owner/repo', u.MARKER + '\nnew', True)
            self.assertEqual(api.call_args.args[2], 'POST')

    def test_commit_uses_actual_head_not_truncated_commit_list(self):
        source = dict(name='driver',path='pin',pattern=r'([a-f0-9]{40})',kind='github-commit',repo='org/driver',branch='stable')
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp); (root/'pin').write_text('a'*40)
            with patch.object(u,'github',side_effect=[{'sha':'b'*40},{'status':'ahead','commits':[{'sha':'c'*40}],'html_url':'https://github.com/org/driver/compare/a...b'}]):
                result=u.check(source,root)
                self.assertEqual(result['latest'],'b'*40)
                self.assertEqual(result['status'],'upstream changes')


class CoverageTests(unittest.TestCase):
    def test_all_tracked_manifests_and_platform_pins_have_owners(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        u.validate_coverage(ROOT, sources)
        with self.assertRaisesRegex(ValueError, 'Unmonitored dependency inputs'):
            u.validate_coverage(ROOT, [s for s in sources if s.get('lock_entry') != 'opensbi'])

    def test_new_manifest_breaks_coverage(self):
        import dependency_coverage as c
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        files = c.tracked_files(ROOT)
        with patch.object(c, 'tracked_files', return_value=files + ['new-addon/Cargo.toml']):
            with self.assertRaisesRegex(ValueError, 'new-addon/Cargo.toml'):
                c.validate_coverage(ROOT, sources)

    def test_rust_toolchains_require_separate_dependabot_coverage(self):
        import dependency_coverage as c
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        files = c.tracked_files(ROOT)
        configured = c.dependabot_directories((ROOT / '.github/dependabot.yml').read_text())
        without_toolchain = {k: v for k, v in configured.items() if k != 'rust-toolchain'}
        with patch.object(c, 'dependabot_directories', return_value=without_toolchain):
            with self.assertRaisesRegex(ValueError, 'addons/rustdesk/rust-toolchain.toml'):
                c.validate_coverage(ROOT, sources)
        for filename in ('rust-toolchain', 'rust-toolchain.toml'):
            with self.subTest(filename=filename), patch.object(c, 'tracked_files', return_value=files + ['new-addon/' + filename]):
                with self.assertRaisesRegex(ValueError, 'new-addon/' + filename):
                    c.validate_coverage(ROOT, sources)

    def test_new_buildroot_selection_is_not_silent(self):
        import dependency_coverage as c
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / 'firmware/buildroot/configs/nanokvm_platform_defconfig'
            path.parent.mkdir(parents=True)
            path.write_text('BR2_PACKAGE_NEW_LIBRARY=y\n')
            with self.assertRaisesRegex(ValueError, 'NEW_LIBRARY'):
                c.buildroot_recipes(root)

    def test_npm_inventory_follows_aliases_and_keeps_node_policy_visible(self):
        sources = u.npm_sources(ROOT)
        alias = next(s for s in sources if s['name'] == 'npm: typescript')
        self.assertEqual(alias['package'], '@typescript/typescript6')
        self.assertEqual(u.current_pin(ROOT, alias), json.loads((ROOT / 'web/package.json').read_text())['devDependencies']['typescript'].rsplit('@', 1)[1])
        self.assertIn('policy', next(s for s in sources if s['name'] == 'npm: @types/node'))
        eslint = next(s for s in sources if s['name'] == 'npm: eslint')
        parts = list(u.version(u.current_pin(ROOT, eslint))[:3]); parts[2] += 1
        latest = '.'.join(map(str, parts))
        with patch.object(u, 'request', return_value=json.dumps({'version': latest})):
            self.assertEqual(u.check(eslint)['status'], 'update available')
        with patch.object(u, 'request', side_effect=OSError('registry down')):
            self.assertEqual(u.check(eslint)['status'], 'error')

    def test_npm_ranges_cannot_be_silently_skipped(self):
        import dependency_coverage as c
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); (root / 'package.json').write_text('{"dependencies":{"example":"^1.0.0"}}')
            with patch.object(c, 'tracked_files', return_value=['package.json']):
                with self.assertRaisesRegex(ValueError, 'Unsupported npm pin'):
                    c.npm_sources(root)

    def test_tag_scan_reads_later_pages(self):
        with patch.object(u, 'github', side_effect=[[{'name': 'v1.0.0'}] * 100, [{'name': 'v2.0.0'}]]) as api:
            self.assertEqual(u.github_tags('org/repo')[-1]['name'], 'v2.0.0')
            self.assertIn('page=2', api.call_args.args[0])
        with patch.object(u, 'github', return_value=[{'name': 'v1.0.0'}] * 100):
            with self.assertRaisesRegex(ValueError, 'incomplete'):
                u.github_tags('org/repo')

    def test_opensbi_release_does_not_suggest_downgrade(self):
        source = next(s for s in json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources'] if s['name'] == 'OpenSBI')
        with patch.object(u, 'github', side_effect=[{'tag_name': 'v1.8', 'html_url': 'https://example.test'}, {'sha': 'b' * 40}, {'status': 'behind'}]):
            self.assertEqual(u.check(source)['status'], 'pinned ahead')

    def test_uboot_checks_stable_tags_not_archive_lag(self):
        source = next(s for s in json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources'] if s['name'] == 'U-Boot')
        with patch.object(u, 'github', return_value=[{'name': 'v2026.10'}, {'name': 'v2027.01-rc1'}]):
            self.assertEqual(u.check(source)['latest'], '2026.10')

    def test_nested_go_pseudo_version_is_not_truncated(self):
        source = next(s for s in json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources'] if s['name'] == 'Go CORS nested module')
        current = u.current_pin(ROOT, source)
        self.assertRegex(current, r'^v0\.0\.0-[0-9]+-[a-f0-9]+$')
        with patch.object(u, 'request', return_value=json.dumps({'Version': current})):
            self.assertEqual(u.check(source)['status'], 'current')

    def test_node_keeps_selected_major(self):
        source = next(s for s in json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources'] if s['name'] == 'Node (selected LTS)')
        major, minor, _ = u.version(u.current_pin(ROOT, source))[:3]
        latest = f'{major}.{minor + 1}.0'
        with patch.object(u, 'request', return_value=json.dumps([{'version': f'v{major + 2}.0.0'}, {'version': 'v' + latest}])):
            result = u.check(source)
            self.assertEqual(result['latest'], latest)
            self.assertEqual(result['status'], 'update available')

    def test_picoclaw_missing_asset_is_error(self):
        source = next(s for s in json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources'] if s['name'] == 'PicoClaw download channel')
        with patch.object(u, 'github', return_value={'assets': []}):
            self.assertEqual(u.check(source)['status'], 'error')

    def test_alpine_fetch_failure_is_error(self):
        with patch.object(u, 'request_bytes', side_effect=OSError('mirror down')):
            results = u.check_alpine_inventory(ROOT)
            self.assertEqual(results[0]['status'], 'error')
            self.assertIn('mirror down', results[0]['detail'])

    def test_closed_actionable_dashboard_reopens_even_when_unchanged(self):
        issue = dict(number=7, title=u.TITLE, body=u.MARKER + '\nreport', state='closed', html_url='test')
        with patch.object(u, 'github', side_effect=[[issue], {}]) as api:
            u.publish('owner/repo', issue['body'], True)
            self.assertEqual(api.call_args.args[1], {'state': 'open'})


if __name__ == '__main__':
    unittest.main()
