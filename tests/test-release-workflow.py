#!/usr/bin/env python3
"""Static checks of the release workflow: where the keys are used, and that
its key names match the published release keys. No network."""
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
BUILD = (ROOT / '.github/workflows/release-build.yml').read_text()
KEYS = ROOT / 'firmware/alpine/keys'


def job(text, name):
    """The text of one job of a workflow, up to the next job."""
    match = re.search(rf'^  {name}:\n(.*?)(?=^  [a-z-]+:\n|\Z)', text, re.M | re.S)
    assert match, name
    return match[1]


class ReleaseWorkflowTests(unittest.TestCase):
    def test_key_names_are_the_published_keys(self):
        rsa = re.search(r'^  RSA_KEY_NAME: (\S+)$', RELEASE, re.M)[1]
        ec = re.search(r'^  EC_KEY_NAME: (\S+)$', RELEASE, re.M)[1]
        self.assertTrue((KEYS / f'{rsa}.rsa.pub').is_file(), rsa)
        self.assertTrue((KEYS / f'{ec}.pub').is_file(), ec)

    def test_only_the_approved_job_on_a_tag_sees_the_keys(self):
        self.assertEqual(RELEASE.count('${{ secrets.'), 2)
        sign = job(RELEASE, 'sign')
        self.assertIn('secrets.APK_RSA_KEY', sign)
        self.assertIn('secrets.APK_EC_KEY', sign)
        self.assertIn("if: needs.build.outputs.publish == 'true'", sign)
        self.assertIn('environment: release', sign)
        self.assertIn('if: always()', sign)
        self.assertIn('bash scripts/publish-release.sh', sign)
        for name in ('build', 'dry-run'):
            self.assertNotIn('environment:', job(RELEASE, name))
            self.assertNotIn('${{ secrets.', job(RELEASE, name))
        self.assertIn('keys: -d', job(RELEASE, 'dry-run'))
        self.assertIn("if: needs.build.outputs.publish != 'true' && needs.build.outputs.skip != 'true'", job(RELEASE, 'dry-run'))
        # Only a hand-started run or a pushed tag publishes; a pull request never does.
        build = job(RELEASE, 'build')
        self.assertRegex(build, r"\*\)\n\s+printf .*\\npublish=false\\n")
        self.assertIn('[ "$DRY_RUN" = true ] && echo publish=false', build)

    def test_unchanged_packages_come_from_the_published_repository(self):
        previous = re.search(r'^  NANOKVM_PREVIOUS: (\S+)$', RELEASE, re.M)[1]
        self.assertRegex(previous, r'^https://dormancygrace\.github\.io/NanoKVM-OS/alpine/v[0-9.]+/riscv64$')
        action = (ROOT / '.github/actions/assemble-release/action.yml').read_text()
        self.assertIn('p["changed"] and print(p["apk"])', action)
        self.assertIn('"$repo/packages.json" "$a/"', action)

    def test_only_version_tags_and_the_button_release(self):
        self.assertRegex(RELEASE, r"push:\n    tags: \['v\*'\]\n")
        self.assertNotRegex(RELEASE, r'push:\n    branches')
        for name in ('kind', 'image', 'dry_run'):
            self.assertRegex(RELEASE, rf'\n      {name}:\n')
        # A patch release is published at once, and so are its packages.
        sign = job(RELEASE, 'sign')
        self.assertIn('gh workflow run apk-repository.yml', (ROOT / 'scripts/publish-release.sh').read_text())
        self.assertIn('actions: write', sign)

    def test_the_tag_must_match_release_env(self):
        build = job(RELEASE, 'build')
        self.assertIn('. firmware/alpine/release.env', build)
        self.assertIn('"$GITHUB_REF_NAME" != "$NANOKVM_VERSION"', build)
        self.assertIn('python3 scripts/next-release.py', build)

    def test_the_tag_of_a_published_draft_releases_nothing_again(self):
        build = job(RELEASE, 'build')
        self.assertIn('gh release view "$GITHUB_REF_NAME"', build)
        self.assertEqual(build.count("if: steps.version.outputs.skip != 'true'"), 3)

    def test_both_workflows_prepare_the_build_alike(self):
        self.assertIn('uses: ./.github/actions/prepare-build', BUILD)
        self.assertEqual(RELEASE.count('uses: ./.github/actions/prepare-build'), 3)
        # Only builds of main and pull requests save caches; a tag's would serve no one.
        self.assertIn("save-caches: 'true'", BUILD)
        self.assertNotIn('save-caches', RELEASE)


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
            (root / 'gh').write_text('#!/bin/sh\ncat "$RELEASES"\n')
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
                    if event == 'workflow_dispatch':
                        self.assertEqual(values['version'], 'v3.3.1')
                        self.assertEqual(values['apk_version'], '3.3.1')
                        self.assertEqual(values['image_version'], 'v1.6')
                        self.assertEqual(values['previous'], 'v3.2.0')
                        self.assertEqual(values['draft'], 'false')

    def test_publication_and_retries(self):
        # Execute the production publisher with a stateful GitHub CLI fixture.
        # No real release, upload or workflow dispatch is performed.
        stub = """#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
p = Path(os.environ['STATE'])
state = json.loads(p.read_text())
args = sys.argv[1:]
state['calls'].append(args)
p.write_text(json.dumps(state))
cmd = args[:2]
if cmd == ['release', 'view']:
    if state['release'] == 'missing':
        sys.exit(1)
    print(state['release'])
elif cmd == ['release', 'create']:
    assert state['release'] == 'missing'
    assert '--draft' in args
    state['release'] = 'true'
    p.write_text(json.dumps(state))
    if os.environ.get('FAIL_UPLOAD') == '1':
        sys.exit(1)
elif cmd == ['release', 'upload']:
    assert state['release'] == 'true'
    if os.environ.get('FAIL_UPLOAD') == '1':
        sys.exit(1)
elif cmd == ['release', 'edit']:
    assert state['release'] == 'true'
    assert '--draft=false' in args
    state['release'] = 'false'
    p.write_text(json.dumps(state))
elif args[:3] == ['workflow', 'run', 'apk-repository.yml']:
    assert state['release'] == 'false'
    if os.environ.get('FAIL_DISPATCH') == '1':
        sys.exit(1)
else:
    raise AssertionError(args)
"""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'gh').write_text(stub)
            (root / 'gh').chmod(0o755)
            (root / 'assets').mkdir()
            (root / 'assets/SHA256SUMS').write_text('fixture')
            path = root / 'state.json'
            env = {**os.environ, 'PATH': str(root) + ':' + os.environ['PATH'],
                   'STATE': str(path), 'OUT': str(root), 'NANOKVM_VERSION': 'v3.3.1',
                   'NANOKVM_IMAGE_VERSION': 'v1.5', 'IMAGE': 'false', 'DRAFT': 'false',
                   'PREVIOUS': 'v3.2.0', 'SHA': 'a' * 40, 'DEFAULT_BRANCH': 'main'}
            def reset(release='missing'):
                path.write_text(json.dumps({'release': release, 'calls': []}))
            def invoke(**overrides):
                result = subprocess.run(['bash', str(ROOT / 'scripts/publish-release.sh')],
                                        env={**env, **overrides}, capture_output=True, text=True)
                return result, json.loads(path.read_text())
            reset()
            result, state = invoke()
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual([c[:2] for c in state['calls']],
                             [['release', 'view'], ['release', 'create'], ['release', 'edit'], ['workflow', 'run']])
            create = state['calls'][1]
            self.assertEqual(create[create.index('--target') + 1], env['SHA'])
            self.assertEqual(create[create.index('--notes-start-tag') + 1], env['PREVIOUS'])
            reset()
            result, state = invoke(DRAFT='true', IMAGE='true')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(state['release'], 'true')
            self.assertEqual(len(state['calls']), 2)
            self.assertIn('NanoKVM OS v3.3.1 · Image v1.5', state['calls'][1])
            # An incomplete upload remains private, and rerunning completes it.
            reset()
            result, state = invoke(FAIL_UPLOAD='1')
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(state['release'], 'true')
            self.assertFalse(any(c[:2] == ['release', 'edit'] for c in state['calls']))
            result, state = invoke()
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(state['release'], 'false')
            self.assertEqual(state['calls'][-3][:2], ['release', 'upload'])
            # A failed Pages dispatch after publication can be retried without
            # recreating the release or overwriting public assets.
            reset()
            result, state = invoke(FAIL_DISPATCH='1')
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(state['release'], 'false')
            count = len(state['calls'])
            result, state = invoke()
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual([c[:2] for c in state['calls'][count:]],
                             [['release', 'view'], ['workflow', 'run']])



class AddonWorkflowTests(unittest.TestCase):
    ADDON = (ROOT / '.github/workflows/rustdesk.yml').read_text()

    def test_only_the_publish_job_may_write(self):
        self.assertNotIn('write', job(self.ADDON, 'build'))
        publish = job(self.ADDON, 'publish')
        self.assertIn('contents: write', publish)
        self.assertIn("if: github.event_name == 'workflow_dispatch' && inputs.dry_run != true", publish)

    def test_addon_revision_requires_both_tag_inventories(self):
        match = re.search(r'^      - name: Version\n(.*?)(?=^      - |\Z)', self.ADDON, re.M | re.S)
        script = textwrap.dedent(match[1].split('        run: |\n', 1)[1])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'gh').write_text("""#!/bin/sh
case "$*" in
  *repos/example/current/tags*) [ "$FAIL" != current ] || exit 17
    echo nanokvm-rustdesk-0.5.3-r8 ;;
  *repos/example/old/tags*) [ "$FAIL" != old ] || exit 18
    echo nanokvm-rustdesk-0.5.3-r5 ;;
  *) exit 99 ;;
esac
""")
            (root / 'gh').chmod(0o755)
            env = {**os.environ, 'PATH': str(root) + ':' + os.environ['PATH'],
                   'GITHUB_REPOSITORY': 'example/current', 'OLD_REPOSITORY': 'example/old',
                   'GITHUB_OUTPUT': str(root / 'output'), 'RUNNER_TEMP': str(root)}
            for failure in ('none', 'current', 'old'):
                with self.subTest(failure=failure):
                    (root / 'output').write_text('')
                    result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], cwd=ROOT,
                                            env={**env, 'FAIL': failure}, capture_output=True, text=True)
                    if failure == 'none':
                        self.assertEqual(result.returncode, 0, result.stderr)
                        self.assertIn('revision=9', (root / 'output').read_text())
                    else:
                        self.assertNotEqual(result.returncode, 0, result.stdout)
                        self.assertEqual((root / 'output').read_text(), '')

    def test_the_add_on_release_is_never_the_latest(self):
        self.assertIn('--latest=false', job(self.ADDON, 'publish'))
        self.assertNotIn('${{ secrets.', self.ADDON)


if __name__ == '__main__':
    unittest.main()
