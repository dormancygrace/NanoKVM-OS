#!/usr/bin/env python3
"""Offline contracts for upstream monitoring; no real remote writes."""
import importlib.util
import io
import json
import tarfile
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('upstream', ROOT / 'scripts/check-upstream.py')
u = importlib.util.module_from_spec(spec)
spec.loader.exec_module(u)


class WatchTests(unittest.TestCase):
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
        self.assertEqual(selected[0]['kind'], 'alpine-package')
        self.assertNotIn('buildroot', selected[0]['path'])

    def test_alpine_baseline_revision_not_local_increment(self):
        sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
        source = next(s for s in sources if s['kind'] == 'alpine-package')
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
        source = next(s for s in sources if s['kind'] == 'alpine-package')
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


if __name__ == '__main__':
    unittest.main()
