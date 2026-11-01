#!/usr/bin/env python3
"""Release workflow steps and the signer's inventory gate, run as shell. No network."""
import re
import os
import json
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RELEASE = (ROOT / '.github/workflows/release.yml').read_text()


def job(text, name):
    """The text of one job of a workflow, up to the next job."""
    match = re.search(rf'^  {name}:\n(.*?)(?=^  [a-z-]+:\n|\Z)', text, re.M | re.S)
    assert match, name
    return match[1]


class ReleaseWorkflowTests(unittest.TestCase):
    def test_signer_rejects_unlisted_add_on_before_signing(self):
        # Execute the real signer with a test key. An extra APK must be rejected
        # by the inventory gate, before it downloads tools or signs anything.
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp) / 'release/apk/recipes/riscv64'
            directory.mkdir(parents=True)
            (directory / 'packages.json').write_text(json.dumps({'packages': {
                'nanokvm': {'apk': 'nanokvm-3.5.0-r0.apk', 'changed': True}}}))
            (directory / 'nanokvm-3.5.0-r0.apk').write_bytes(b'core')
            (directory / 'nanokvm-ipmi-0.1.0-r0.apk').write_bytes(b'extraneous')
            result = subprocess.run(['bash', str(ROOT / 'scripts/sign-release.sh'), '-o', tmp, '-d'],
                                    capture_output=True, text=True, timeout=15)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('has other packages than packages.json', result.stderr)
            self.assertFalse((Path(tmp) / 'signer').exists())


def run_step(name):
    step = re.search(r'^      - name: ' + re.escape(name) +
                     r'\n(.*?)(?=^      - |^  [a-z-]+:|\Z)', RELEASE, re.M | re.S)
    assert step, name
    return textwrap.dedent(step[1].split('        run: |\n', 1)[1])


class ReleaseExecutionTests(unittest.TestCase):
    def test_branch_gate_precedes_checkout_and_all_jobs_pin_event_sha(self):
        build = job(RELEASE, 'build')
        self.assertLess(build.index('name: Validate release branch'), build.index('uses: actions/checkout'))
        self.assertNotIn('inputs.ref', RELEASE)
        self.assertEqual(RELEASE.count('ref: ${{ github.sha }}'), 3)
        script = run_step('Validate release branch')
        for ref, ok in [('refs/heads/main', True), ('refs/heads/release/3.3', True),
                        ('refs/heads/feature/x', False), ('refs/tags/main', False),
                        ('refs/tags/release/3.3', False), ('refs/pull/4/head', False),
                        ('refs/heads/release/3.3/extra', False)]:
            with self.subTest(ref=ref):
                result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script],
                                        env={**os.environ, 'GITHUB_REF': ref}, capture_output=True)
                self.assertEqual(result.returncode == 0, ok, result.stderr)

    def test_dispatch_version_outputs_and_nonpublishing_pr_path(self):
        script = run_step('Version')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            # gh api --jq '.[]' produces one object per line; jq -s in the
            # actual Version step consumes these as the release list.
            releases = [
                {'tag_name': 'v3.3.0', 'draft': True, 'assets': [
                    {'name': 'NanoKVM-OS-Image-v1.5-apps-v3.3.0.img.zip'}]},
                {'tag_name': 'v3.2.0', 'draft': False, 'assets': [
                    {'name': 'NanoKVM-OS-Image-v1.4-apps-v3.2.0.img.zip'}]},
            ]
            (root / 'gh').write_text('#!/bin/sh\n[ "$FAIL_API" != 1 ] || exit 17\ncat "$RELEASES"\n'
                                     '[ "$FAIL_API" != partial ] || exit 17\n')
            (root / 'gh').chmod(0o755)
            (root / 'releases').write_text('\n'.join(json.dumps(r) for r in releases))
            env = {**os.environ, 'PATH': str(root) + ':' + os.environ['PATH'],
                   'RELEASES': str(root / 'releases'), 'RUNNER_TEMP': str(root),
                   'GITHUB_REPOSITORY': 'example/test', 'GITHUB_OUTPUT': str(root / 'output'),
                   'EVENT': 'workflow_dispatch', 'KIND': 'patch', 'REF': 'main',
                   'IMAGE': 'true', 'DRY_RUN': 'false'}
            for event, dry, publish in [('workflow_dispatch', 'false', 'true'),
                                        ('workflow_dispatch', 'true', 'false'),
                                        ('pull_request', 'false', 'false')]:
                with self.subTest(event=event, dry=dry):
                    (root / 'output').write_text('')
                    result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], cwd=ROOT,
                                            env={**env, 'EVENT': event, 'DRY_RUN': dry},
                                            capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    values = dict(line.split('=', 1) for line in (root / 'output').read_text().splitlines())
                    self.assertEqual(values['publish'], publish)
                    if event in ('workflow_dispatch', 'pull_request'):
                        self.assertEqual(values['version'], 'v3.3.1')
                        self.assertEqual(values['apk_version'], '3.3.1')
                        self.assertNotIn('image_version', values)
                        self.assertEqual(values['previous'], 'v3.2.0')
                        self.assertEqual(values['draft'], 'false' if event == 'workflow_dispatch' else 'true')

            # A package-only hotfix also works when the entire release history
            # has no image assets. IMAGE controls publication, not versioning.
            (root / 'releases').write_text(json.dumps({'tag_name': 'v3.3.0', 'draft': False, 'assets': []}))
            (root / 'output').write_text('')
            result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], cwd=ROOT,
                                    env={**env, 'IMAGE': 'false'}, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            values = dict(line.split('=', 1) for line in (root / 'output').read_text().splitlines())
            self.assertEqual(values['version'], 'v3.3.1')
            self.assertEqual(values['image'], 'false')
            self.assertNotIn('image_version', values)
            (root / 'releases').write_text('\n'.join(json.dumps(r) for r in releases))

            # A failed release lookup must not fall back to stale release.env.
            (root / 'output').write_text('')
            failed = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], cwd=ROOT,
                                    env={**env, 'EVENT': 'pull_request', 'FAIL_API': '1'},
                                    capture_output=True, text=True)
            self.assertNotEqual(failed.returncode, 0)
            self.assertNotIn('version=', (root / 'output').read_text())
            # A later page failing after the first was printed. GitHub runs
            # steps with bash -e, without pipefail.
            (root / 'output').write_text('')
            failed = subprocess.run(['bash', '-e', '-c', script], cwd=ROOT,
                                    env={**env, 'FAIL_API': 'partial'}, capture_output=True, text=True)
            self.assertNotEqual(failed.returncode, 0)
            self.assertNotIn('version=', (root / 'output').read_text())

            (root / 'output').write_text('')
            result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], cwd=ROOT,
                                    env={**env, 'CHANNEL': 'beta'}, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            values = dict(line.split('=', 1) for line in (root / 'output').read_text().splitlines())
            self.assertEqual(values['channel'], 'beta')
            self.assertEqual(values['version'], 'v3.3.1-beta.1')
            self.assertEqual(values['apk_version'], '3.3.1_beta1')
            self.assertNotIn('image_version', values)


if __name__ == '__main__':
    unittest.main()
