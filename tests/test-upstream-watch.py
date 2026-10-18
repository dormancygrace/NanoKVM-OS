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

    def test_buildroot_reports_only_new_stable_archive_releases(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        source = next(s for s in sources if s['name'] == 'Buildroot')
        fixtures = [
            ('buildroot-2026.08.tar.xz buildroot-2026.11-rc1.tar.xz buildroot-master.tar.gz',
             '2026.08', 'current'),
            ('buildroot-2026.08.tar.xz buildroot-2026.08.1.tar.xz buildroot-2026.11-rc2.tar.gz',
             '2026.08.1', 'update available'),
            ('buildroot-2026.08.tar.xz buildroot-2026.11.tar.gz buildroot-2027.02-rc1.tar.xz',
             '2026.11', 'update available'),
            ('buildroot-2025.02.10.tar.xz', '2025.02.10', 'pinned ahead'),
        ]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'platform').mkdir()
            (root / source['path']).write_text('buildroot https://example.test/buildroot-2026.08.tar.xz hash\n')
            for archives, latest, status in fixtures:
                index = '\n'.join('<a href="' + name + '">' + name + '</a>' for name in archives.split())
                with self.subTest(archives=archives), patch.object(u, 'request', return_value=index):
                    result = u.check(source, root)
                    self.assertEqual(result['latest'], latest)
                    self.assertEqual(result['status'], status)
            with patch.object(u, 'request', return_value='<a href="buildroot-2026.11-rc1.tar.xz">RC</a>'):
                self.assertEqual(u.check(source, root)['status'], 'error')

    def test_linux_waits_for_first_point_release(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        source = next(s for s in sources if s['name'] == 'Linux')
        cases = [
            (['7.3', '7.2.10'], '7.2.10', 'current'),
            (['7.3.0', '7.2.10'], '7.2.10', 'current'),
            (['8.0', '8.0.0', '7.3-rc7', '7.3.1-rc1', '7.2.10'], '7.2.10', 'current'),
            (['7.3.0', '7.2.11', '7.2.10'], '7.2.11', 'update available'),
            (['7.3.1', '7.2.10'], '7.3.1', 'update available'),
            (['7.3.9', '7.3.10', '7.2.10'], '7.3.10', 'update available'),
            (['8.0.1', '7.3.10', '7.2.10'], '8.0.1', 'update available'),
            (['7.3', '7.2.9'], '7.2.9', 'pinned ahead'),
        ]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            pin = root / source['path']
            pin.parent.mkdir(parents=True)
            pin.write_text('linux https://example.test/linux-7.2.10.tar.xz\n')
            for versions, latest, status in cases:
                feed = json.dumps({'releases': [{'version': v} for v in versions],
                                   'latest_stable': {'version': versions[0]}})
                with self.subTest(versions=versions), patch.object(u, 'request', return_value=feed) as request:
                    result = u.check(source, root)
                    request.assert_called_once_with('https://www.kernel.org/releases.json')
                    self.assertEqual(result['latest'], latest)
                    self.assertEqual(result['status'], status)

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

    def test_unmonitored_manifests_are_listed_and_not_stale(self):
        import dependency_coverage as c
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        files = c.tracked_files(ROOT)
        # A listed manifest is exempt; the same kind of file elsewhere is not.
        with patch.object(c, 'tracked_files', return_value=files + ['server/third_party/pion-ice/e2e/Dockerfile']):
            with self.assertRaisesRegex(ValueError, 'pion-ice/e2e/Dockerfile'):
                c.validate_coverage(ROOT, sources)
        gone = next(iter(c.UNMONITORED))
        with patch.object(c, 'tracked_files', return_value=[f for f in files if f != gone]):
            with self.assertRaisesRegex(ValueError, 'Stale unmonitored entries'):
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

    def test_download_channel_missing_asset_is_error(self):
        source = dict(name='Example channel', path='unused', kind='github-channel',
                      repo='example/project', asset='required.tar.gz')
        with patch.object(u, 'current_pin', return_value='latest'), \
                patch.object(u, 'github', return_value={'assets': []}):
            self.assertEqual(u.check(source)['status'], 'error')

    def test_report_respects_cadence_before_network_requests(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        all_names = {s['name'] for s in sources + u.npm_sources(ROOT)}
        for cadence in ('daily', 'quarterly', 'all'):
            def result(source):
                return dict(name=source['name'], path=source['path'], current='x',
                            latest='x', status='current', link='')
            with self.subTest(cadence=cadence), \
                    patch.object(sys, 'argv', ['check-upstream.py', '--cadence', cadence]), \
                    patch.object(u, 'check', side_effect=result) as check, \
                    patch.object(u, 'check_alpine_inventory', return_value=[]) as alpine, \
                    patch.object(sys, 'stdout', io.StringIO()), \
                    patch.dict(u.os.environ, {'GITHUB_STEP_SUMMARY': ''}):
                u.main()
            names = {c.args[0]['name'] for c in check.call_args_list}
            expected = {'tinyalsa'} if cadence == 'quarterly' else all_names - ({'tinyalsa'} if cadence == 'daily' else set())
            self.assertEqual(names, expected)
            self.assertEqual(alpine.call_count, int(cadence != 'quarterly'))
            self.assertFalse(any('picoclaw' in name.lower() for name in names))

    def test_unknown_cadence_fails_visibly(self):
        with self.assertRaisesRegex(ValueError, 'Unknown check cadence'):
            u.select_sources([], 'monthly')
        for cadence in ('all', 'daily', 'quarterly'):
            with self.subTest(cadence=cadence), self.assertRaisesRegex(ValueError, 'Unknown source cadence'):
                u.select_sources([dict(name='bad', cadence='quartery')], cadence)

    def test_split_app_packages_are_not_alpine_dependencies(self):
        with patch.object(u, 'request_bytes', return_value=WatchTests.alpine_index('3.5.9-r0')):
            results = {item['name']: item for item in u.check_alpine_inventory(ROOT)}
        for name in ('nanokvm', 'nanokvm-web', 'nanokvm-app'):
            self.assertNotIn('Alpine: ' + name, results)
        self.assertEqual(results['Alpine: openssl']['status'], 'repository-managed')
        self.assertEqual(results['Alpine: curl']['status'], 'error',
                         'missing external packages must still be reported')

    def test_alpine_fetch_failure_is_error(self):
        with patch.object(u, 'request_bytes', side_effect=OSError('mirror down')):
            results = u.check_alpine_inventory(ROOT)
            self.assertEqual(results[0]['status'], 'error')
            self.assertIn('mirror down', results[0]['detail'])


if __name__ == '__main__':
    unittest.main()
