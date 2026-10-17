#!/usr/bin/env python3
"""Exercise release-build reporting with mocked GitHub calls; no network writes."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest

REPO = Path(__file__).resolve().parents[1]
WORKFLOW = REPO / '.github/workflows/release-build.yml'
DIGEST = hashlib.sha256(b'output').hexdigest()
FAKE = r"""#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ['FIXTURE_ROOT'])
f = json.loads((root / 'fixture.json').read_text())
a = sys.argv[1:]
with (root / 'calls.jsonl').open('a') as log:
    log.write(json.dumps([Path(sys.argv[0]).name, *a]) + '\n')
if Path(sys.argv[0]).name == 'git':
    if a == ['rev-parse', 'HEAD^1']:
        print(f['base'])
    elif a[:2] == ['merge-base', '--is-ancestor']:
        sys.exit(0 if a[2] in f['ancestors'] and a[3] == f['base'] else 1)
    else:
        raise AssertionError(a)
elif a[:2] == ['run', 'list']:
    assert a[a.index('--branch') + 1] == 'main'
    assert a[a.index('--status') + 1] == 'success'
    for id_, sha in f['runs']:
        print(id_, sha)
elif a[:2] == ['run', 'download']:
    if f.get('download_fail'):
        sys.exit(1)
    d = Path(a[a.index('-D') + 1])
    d.mkdir()
    (d / 'SHA256SUMS').write_text(f['manifest'])
elif a[:2] == ['api', '--paginate']:
    if f.get('comment_id'):
        print(f['comment_id'])
elif a[0] == 'api':
    endpoint = next(x for x in a if x.startswith('repos/'))
    body = next(x[5:] for x in a if x.startswith('body='))
    (root / 'posted.json').write_text(json.dumps({'endpoint': endpoint, 'body': body, 'patch': '-X' in a}))
else:
    raise AssertionError(a)
"""


def step(name):
    section = WORKFLOW.read_text().split('      - name: ' + name + '\n', 1)[1]
    section = section.split('      - name:', 1)[0]
    return textwrap.dedent(section.split('        run: |\n', 1)[1])


class ReleaseBuildTests(unittest.TestCase):
    def exercise(self, *, runs=(), ancestors=(), base='a' * 40,
                 download_fail=False, comment_id=None, manifest=None):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'bin').mkdir()
            for name in ('gh', 'git'):
                p = root / 'bin' / name
                p.write_text(FAKE)
                p.chmod(0o755)
            manifest = manifest if manifest is not None else f'{DIGEST}  Image\n'
            (root / 'fixture.json').write_text(json.dumps(dict(
                runs=runs, ancestors=ancestors, base=base, download_fail=download_fail,
                comment_id=comment_id, manifest=manifest)))
            images = root / 'out/images'
            images.mkdir(parents=True)
            (images / 'SHA256SUMS').write_text(f'{DIGEST}  Image\n')
            env = dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH'],
                       FIXTURE_ROOT=temp, RUNNER_TEMP=temp, OUT=str(root / 'out'),
                       GITHUB_STEP_SUMMARY=str(root / 'summary'), BASE_REF='main',
                       GITHUB_REPOSITORY='example/repo', PR='244', GH_TOKEN='fake',
                       RUN_URL='https://example.test/run/123')
            result = subprocess.run(['bash', '--noprofile', '--norc', '-eo', 'pipefail'],
                                    input=step('Compare with main'), text=True, cwd=REPO,
                                    env=env, capture_output=True, timeout=10)
            if result.returncode:
                return result, None, None, None
            comment = subprocess.run(['bash', '--noprofile', '--norc', '-eo', 'pipefail'],
                                     input=step('Comment on the pull request'), text=True, cwd=REPO,
                                     env=env, capture_output=True, timeout=10)
            self.assertEqual(comment.returncode, 0, comment.stderr)
            report = (root / 'summary').read_text()
            posted = json.loads((root / 'posted.json').read_text())
            calls = [json.loads(x) for x in (root / 'calls.jsonl').read_text().splitlines()]
            return result, report, posted, calls

    def test_no_baseline_creates_comment(self):
        result, report, posted, _ = self.exercise()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('No build of main', report)
        self.assertEqual(posted['endpoint'], 'repos/example/repo/issues/244/comments')
        self.assertFalse(posted['patch'])
        self.assertEqual(posted['body'], '<!-- release-build-outputs -->\n' + report.rstrip('\n'))

    def test_selects_newest_ancestor_and_updates_comment(self):
        sha = 'a' * 40
        result, report, posted, calls = self.exercise(
            runs=[(30, 'b' * 40), (20, sha), (10, 'c' * 40)],
            ancestors=[sha, 'c' * 40], comment_id=987)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('All 1 outputs are identical', report)
        self.assertNotIn('outputs changed since then', report)
        self.assertTrue(posted['patch'])
        self.assertEqual(posted['endpoint'], 'repos/example/repo/issues/comments/987')
        self.assertEqual([x[3] for x in calls if x[:3] == ['gh', 'run', 'download']], ['20'])

    def test_older_baseline_is_explicit(self):
        sha = 'c' * 40
        result, report, _, _ = self.exercise(runs=[(10, sha)], ancestors=[sha],
                                              manifest=f'{"0" * 64}  Image\n')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('main at cccccccccc', report)
        self.assertIn('main is at aaaaaaaaaa', report)
        self.assertIn('1 changed', report)

    def test_missing_artifact_is_explicit(self):
        sha = 'a' * 40
        result, report, _, _ = self.exercise(runs=[(10, sha)], ancestors=[sha], download_fail=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('could not be downloaded', report)
        self.assertNotIn('identical', report)

    def test_invalid_artifact_does_not_publish_success(self):
        sha = 'a' * 40
        result, _, posted, _ = self.exercise(runs=[(10, sha)], ancestors=[sha], manifest='broken\n')
        self.assertNotEqual(result.returncode, 0)
        self.assertIsNone(posted)


if __name__ == '__main__':
    unittest.main()
