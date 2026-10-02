#!/usr/bin/env python3
"""Offline contracts for upstream monitoring; no real remote writes."""
import importlib.util
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
