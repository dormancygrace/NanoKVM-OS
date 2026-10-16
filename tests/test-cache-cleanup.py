#!/usr/bin/env python3
"""Exercise the cache cleanup workflow with a fake gh; never delete real caches."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest

REPO = Path(__file__).resolve().parents[1]
WORKFLOW = REPO / '.github/workflows/cache-cleanup.yml'
FAKE_GH = """#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
args = sys.argv[1:]
with open('calls.jsonl', 'a') as f:
    f.write(json.dumps(args) + '\\n')
if args[:2] == ['cache', 'list']:
    assert args[args.index('--ref') + 1] == 'refs/heads/main'
    assert args[args.index('--sort') + 1] == 'created_at'
    assert args[args.index('--order') + 1] == 'desc'
    if os.environ.get('LIST_FAIL'):
        sys.exit(1)
    print(Path('fixture.json').read_text())
elif args[:2] == ['cache', 'delete'] or args[0] == 'api':
    status = os.environ.get('DELETE_STATUS', '204')
    if args[0] == 'api':
        assert args[args.index('--method') + 1] == 'DELETE'
        assert '--include' in args
        assert any(a.startswith('repos/example/repo/actions/caches/') for a in args)
    if status != 'network':
        print('HTTP/2.0 ' + status + ' Status\\r\\n\\r\\n')
    if status != '204':
        print('simulated delete failure ' + status, file=sys.stderr)
        sys.exit(1)
else:
    raise AssertionError(args)
"""


def cache(id_, key):
    return {'id': id_, 'key': key, 'sizeInBytes': 1048576}


def entries():
    # Creation-descending order; a recently accessed old cache must still lose.
    return [
        cache(10, 'codeql-overlay-base-database-1-aaa-cpp-v-new'),
        cache(11, 'toolchain-ubuntu24-' + 'a' * 64),
        cache(12, 'codeql-overlay-base-database-1-bbb-python-v-new'),
        cache(13, 'toolchain-ubuntu26-' + 'b' * 64),
        cache(20, 'codeql-overlay-base-database-1-aaa-cpp-v-old'),
        cache(21, 'toolchain-ubuntu24-' + 'c' * 64),
        cache(22, 'setup-go-Linux-old'),
        cache(23, 'pnpm-cache-old'),
    ]


class CacheCleanupTests(unittest.TestCase):
    def run_cleanup(self, caches, status='204', list_fail=False):
        script = textwrap.dedent(WORKFLOW.read_text().split('        run: |\n', 1)[1])
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            gh = root / 'gh'
            gh.write_text(FAKE_GH)
            gh.chmod(0o755)
            (root / 'fixture.json').write_text(json.dumps(caches))
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                       GH_REPO='example/repo', GH_TOKEN='fake', DELETE_STATUS=status)
            if list_fail:
                env['LIST_FAIL'] = '1'
            result = subprocess.run(['bash', '--noprofile', '--norc', '-eo', 'pipefail'],
                                    input=script, text=True, cwd=root, env=env,
                                    capture_output=True, timeout=10)
            calls = [json.loads(line) for line in (root / 'calls.jsonl').read_text().splitlines()]
            deleted = [
                int(args[2]) if args[:2] == ['cache', 'delete'] else
                int(next(a for a in args if a.startswith('repos/')).rsplit('/', 1)[1])
                for args in calls if args[:2] != ['cache', 'list']
            ]
            return result, deleted

    def test_only_superseded_target_groups(self):
        result, deleted = self.run_cleanup(entries())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(deleted, [20, 21])
        self.assertIn('2 caches to delete, 2 MiB', result.stdout)

    def test_no_candidates(self):
        result, deleted = self.run_cleanup(entries()[:4] + entries()[6:])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(deleted, [])

    def test_empty_list(self):
        result, deleted = self.run_cleanup([])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(deleted, [])

    def test_already_evicted_continues(self):
        result, deleted = self.run_cleanup(entries(), status='404')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(deleted, [20, 21])
        self.assertEqual(result.stdout.count('already gone'), 2)

    def test_other_delete_errors_fail(self):
        for status in ('403', '429', '500', 'network'):
            with self.subTest(status=status):
                result, deleted = self.run_cleanup(entries(), status=status)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(deleted, [20])
                self.assertIn('simulated delete failure', result.stderr)
                self.assertNotIn('already gone', result.stdout)

    def test_listing_failure_does_not_delete(self):
        result, deleted = self.run_cleanup(entries(), list_fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(deleted, [])


if __name__ == '__main__':
    unittest.main()
