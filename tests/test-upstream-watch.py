#!/usr/bin/env python3
"""Offline contracts for upstream monitoring; no real remote writes."""
import importlib.util
import sys
import json
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

    def test_release_pins_reject_drafts_and_prereleases(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        for name in ('inih', 'miniz'):
            source = next(s for s in sources if s['name'] == name)
            for flag in ('draft', 'prerelease'):
                with self.subTest(name=name, flag=flag), patch.object(u, 'github', return_value={flag: True}) as api:
                    result = u.check(source)
                self.assertEqual(result['status'], 'error')
                self.assertIn('stable release', result['detail'])
                api.assert_called_once_with('repos/' + source['repo'] + '/releases/latest')

    def test_nested_go_pseudo_version_is_not_truncated(self):
        source = next(s for s in json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources'] if s['name'] == 'Go CORS nested module')
        current = u.current_pin(ROOT, source)
        self.assertRegex(current, r'^v0\.0\.0-[0-9]+-[a-f0-9]+$')
        with patch.object(u, 'request', return_value=json.dumps({'Version': current})):
            self.assertEqual(u.check(source)['status'], 'current')


if __name__ == '__main__':
    unittest.main()
