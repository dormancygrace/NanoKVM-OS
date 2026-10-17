#!/usr/bin/env python3
"""Offline behavior contracts for the kernel PR publisher."""
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
spec = importlib.util.spec_from_file_location('kernel_pr', ROOT / 'scripts/propose-kernel-update.py')
k = importlib.util.module_from_spec(spec)
spec.loader.exec_module(k)
FILES = ('.github/upstream-watch.json', 'platform/sources.lock', 'firmware/sources.json',
         'platform/build.sh', 'platform/kernel/config', 'README.md', 'platform/README.md')


class KernelPRTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in FILES:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / name, path)
        self.current = json.loads((self.root / 'firmware/sources.json').read_text())['linux']['version']
        major, minor, point = map(int, self.current.split('.'))
        self.next = f'{major}.{minor}.{point + 1}'
        self.series = f'{major}.{minor + 1}'
        self.digest = 'a' * 64

    def feed(self, *versions):
        return json.dumps({'releases': [{'version': v, 'released': {'date': '2026-10-14'}}
                                        for v in versions]})

    def plan(self, latest=None, hashes=None):
        latest = latest or self.next
        sums = hashes if hashes is not None else self.digest + '  linux-' + latest + '.tar.xz\n'
        with patch.object(k.upstream, 'request', side_effect=[self.feed(latest, self.current), sums]):
            return k.prepare(self.root)

    def test_skips_initial_rc_equal_and_older_versions(self):
        for versions in [(self.current,), (self.series, self.series + '.0', self.series + '-rc1', self.current),
                         ('1.0.1',)]:
            with self.subTest(versions=versions), patch.object(k.upstream, 'request', return_value=self.feed(*versions)) as request:
                self.assertIsNone(k.prepare(self.root))
                self.assertEqual(request.call_count, 1)

    def test_updates_related_pins_without_touching_checkout_or_qualified_hashes(self):
        before = {name: (self.root / name).read_bytes() for name in FILES}
        plan = self.plan()
        self.assertEqual(plan['latest'], self.next)
        self.assertEqual(set(plan['files']), set(FILES) - {'.github/upstream-watch.json'})
        manifest = json.loads(plan['files']['firmware/sources.json'])
        self.assertEqual(manifest['linux'], dict(version=self.next, url=plan['url'], sha256=self.digest))
        old_manifest = json.loads(before['firmware/sources.json'])
        del manifest['linux']
        del old_manifest['linux']
        self.assertEqual(manifest, old_manifest)
        self.assertIn(plan['url'] + '  ' + self.digest, plan['files']['platform/sources.lock'])
        old_other_pins = [line for line in before['platform/sources.lock'].decode().splitlines()
                          if not line.startswith('linux ')]
        new_other_pins = [line for line in plan['files']['platform/sources.lock'].splitlines()
                          if not line.startswith('linux ')]
        self.assertEqual(old_other_pins, new_other_pins)
        build = plan['files']['platform/build.sh']
        self.assertIn('release=' + self.next + '-nanokvm-os-', build)
        self.assertIn('linux-' + self.next + '.tar.xz', build)
        self.assertIn("kernel_timestamp='Wed Oct 14 00:00:00 UTC 2026'", build)
        self.assertNotIn(self.current, build)
        subprocess.run(['bash', '-n'], input=build, text=True, check=True)
        self.assertEqual(plan['files']['platform/kernel/config'].splitlines()[3:],
                         before['platform/kernel/config'].decode().splitlines()[3:])
        for name, content in before.items():
            self.assertEqual((self.root / name).read_bytes(), content)

    def test_new_series_point_release(self):
        plan = self.plan(self.series + '.1')
        self.assertEqual(plan['latest'], self.series + '.1')

    def test_invalid_or_ambiguous_checksums_fail(self):
        valid = self.digest + '  linux-' + self.next + '.tar.xz\n'
        for hashes in ('', 'invalid  linux-' + self.next + '.tar.xz\n',
                       valid + valid, self.digest + '  linux-' + self.next + '.tar.xz.sig\n'):
            with self.subTest(hashes=hashes), self.assertRaisesRegex(ValueError, 'SHA-256'):
                self.plan(hashes=hashes)

    def test_mismatched_manifest_or_build_pin_fails(self):
        manifest = self.root / 'firmware/sources.json'
        original = manifest.read_text()
        manifest.write_text(original.replace(self.current, '1.0.1'))
        with self.assertRaisesRegex(ValueError, 'manifests disagree'):
            self.plan()
        manifest.write_text(original)
        build = self.root / 'platform/build.sh'
        build.write_text(build.read_text().replace('release=' + self.current, 'release=1.0.1'))
        with self.assertRaisesRegex(ValueError, 'build pin changed'):
            self.plan()

    def test_network_errors_are_not_reported_as_current(self):
        with patch.object(k.upstream, 'request', side_effect=OSError('offline')):
            with self.assertRaises(OSError):
                k.prepare(self.root)

    def pull(self, ref, owner='dormancygrace', state='closed'):
        return {'html_url': 'https://example.test/pr/1', 'state': state,
                'head': {'ref': ref, 'label': owner + ':' + ref}}

    def test_existing_open_or_closed_pr_is_not_recreated_or_overwritten(self):
        for state in ('open', 'closed'):
            for ref in ('deps/upstream-linux-' + self.next, 'old-prefix/upstream-linux-' + self.next):
                with self.subTest(state=state, ref=ref), patch.object(
                        k, 'api', return_value=[self.pull(ref, state=state)]) as api:
                    self.assertEqual(k.publish(dict(latest=self.next)), 'https://example.test/pr/1')
                    api.assert_called_once()
                    self.assertIn('state=all', api.call_args.args[0])

    def test_other_candidates_and_forks_do_not_count(self):
        pulls = [self.pull('deps/upstream-linux-1.0.0'), self.pull('deps/upstream-linux-' + self.next, owner='someone'),
                 self.pull('upstream-linux-' + self.next)]
        self.assertEqual(k.proposed('deps/upstream-linux-' + self.next, pulls), [])

    def test_pull_requests_reads_every_page(self):
        full = [self.pull('deps/upstream-linux-1.0.%d' % i) for i in range(100)]
        with patch.object(k, 'api', side_effect=[full, full[:3]]) as api:
            self.assertEqual(len(k.pull_requests()), 103)
        self.assertEqual([c.args[0][-6:] for c in api.call_args_list], ['page=1', 'page=2'])

    def test_publisher_preserves_modes_identity_and_uses_draft(self):
        plan = self.plan()
        branch = 'deps/upstream-linux-' + self.next
        responses = [[], {'object': {'sha': 'base'}}, [], {'tree': {'sha': 'base-tree'}},
                     {'sha': 'tree'}, {'sha': 'commit'}, {}, {'html_url': 'https://example.test/pr/1'}]
        modes = b''.join((('100755' if name == 'platform/build.sh' else '100644') +
                          ' aaaa 0\t' + name + '\0').encode() for name in plan['files'])
        with patch.object(k, 'api', side_effect=responses) as api, \
                patch.object(k.subprocess, 'check_output', side_effect=['base\n', modes]):
            k.publish(plan, self.root)
        writes = [(c.args[0], c.args[2]) for c in api.call_args_list if len(c.args) > 1]
        self.assertEqual([path for path, _ in writes], ['git/trees', 'git/commits', 'git/refs', 'pulls'])
        tree = writes[0][1]
        self.assertEqual(tree['base_tree'], 'base-tree')
        self.assertEqual(next(f['mode'] for f in tree['tree'] if f['path'] == 'platform/build.sh'), '100755')
        commit = writes[1][1]
        self.assertEqual(commit['author'], k.IDENTITY)
        self.assertEqual(commit['committer'], k.IDENTITY)
        self.assertEqual(writes[2][1]['ref'], 'refs/heads/' + branch)
        pr = writes[3][1]
        self.assertTrue(pr['draft'])
        self.assertEqual(pr['base'], 'main')
        self.assertIn('Release build', pr['body'])
        self.assertIn('olddefconfig', pr['body'])

    def test_retry_after_branch_creation_does_not_rewrite_branch(self):
        plan = self.plan()
        branch = 'deps/upstream-linux-' + self.next
        responses = [[], {'object': {'sha': 'base'}}, [{'ref': 'refs/heads/' + branch}],
                     {'html_url': 'https://example.test/pr/1'}]
        with patch.object(k, 'api', side_effect=responses) as api, \
                patch.object(k.subprocess, 'check_output', return_value='base\n'):
            k.publish(plan, self.root)
        self.assertEqual([c.args[0] for c in api.call_args_list if len(c.args) > 1], ['pulls'])

    def test_stale_checkout_does_not_publish(self):
        with patch.object(k, 'api', side_effect=[[], {'object': {'sha': 'new-main'}}]) as api, \
                patch.object(k.subprocess, 'check_output', return_value='old-main\n'):
            with self.assertRaisesRegex(ValueError, 'current main'):
                k.publish(dict(latest=self.next), self.root)
        self.assertFalse(any(len(c.args) > 1 for c in api.call_args_list))


if __name__ == '__main__':
    unittest.main()
