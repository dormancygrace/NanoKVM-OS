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
        ec = re.search(r'^  EC_KEY_NAME: (\S+)$', RELEASE, re.M)[1]
        self.assertTrue((KEYS / f'{ec}.pub').is_file(), ec)

    def test_only_the_approved_job_on_a_tag_sees_the_keys(self):
        self.assertEqual(RELEASE.count('${{ secrets.'), 1)
        sign = job(RELEASE, 'sign')
        self.assertNotIn('APK_RSA_KEY', RELEASE)
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
        pr_case = build.split('            *)', 1)[1].split(';;', 1)[0]
        self.assertIn('publish=false', pr_case)
        self.assertNotIn('publish=true', pr_case)
        self.assertIn('[ "$DRY_RUN" = true ] && echo publish=false', build)

    def test_pages_uses_only_ec_signing_but_keeps_historical_trust(self):
        pages = (ROOT / '.github/workflows/apk-repository.yml').read_text()
        self.assertNotIn('APK_RSA_KEY', pages)
        self.assertEqual(pages.count('${{ secrets.'), 1)
        self.assertIn('scripts/check-ec-key.sh', pages)
        self.assertIn('--sign-key /signing/release.key', pages)
        self.assertIn('adbsign --reset-signatures', pages)
        self.assertIn('--keys-dir /signing verify', pages)
        self.assertIn('--keys-dir /keys verify', pages)
        self.assertTrue((KEYS / 'dgrace-6aaddbb6.rsa.pub').is_file())

    def test_unchanged_packages_come_from_the_published_repository(self):
        previous = re.search(r'^  NANOKVM_PREVIOUS: (\S+)$', RELEASE, re.M)[1]
        self.assertRegex(previous, r'^https://dormancygrace\.github\.io/NanoKVM-OS/alpine/v[0-9.]+/riscv64$')
        action = (ROOT / '.github/actions/assemble-release/action.yml').read_text()
        self.assertIn('p["changed"] and print(p["apk"])', action)
        self.assertIn('"$repo/packages.json" "$a/"', action)

    def test_os_build_is_independent_of_add_on_releases(self):
        build = (ROOT / 'platform/build.sh').read_text()
        packages = build[build.index('packages() ('):build.index('rootfs() {')]
        self.assertNotIn('addon_packages.py', packages)
        self.assertNotIn('addons.json', packages)
        self.assertLess(packages.index('/sbin/apk verify ./*.apk'), packages.index('/sbin/apk --sign-key'))
        self.assertIn('--allow-untrusted mkndx', packages)
        self.assertNotIn('addons.json', (ROOT / '.github/actions/assemble-release/action.yml').read_text())
        for workflow in ('addon.yml', 'rustdesk.yml'):
            publish = job((ROOT / '.github/workflows' / workflow).read_text(), 'publish')
            self.assertIn('gh release create', publish)
            self.assertIn('gh workflow run apk-repository.yml --ref main', publish)
            self.assertNotIn('gh pr create', publish)
            self.assertNotIn('git push', publish)
            self.assertNotIn('pull-requests: write', publish)

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

    def test_only_version_tags_and_the_button_release(self):
        self.assertRegex(RELEASE, r"push:\n    tags: \['v\*'\]\n")
        self.assertNotRegex(RELEASE, r'push:\n    branches')
        for name in ('kind', 'image', 'dry_run'):
            self.assertRegex(RELEASE, rf'\n      {name}:\n')
        # A patch release is published at once, and so are its packages.
        sign = job(RELEASE, 'sign')
        self.assertIn("gh('workflow', 'run', 'apk-repository.yml'", (ROOT / 'scripts/publish-release.py').read_text())
        self.assertIn('actions: write', sign)

    def test_release_events_dispatch_pages_on_main_without_signing_on_the_tag(self):
        pages = (ROOT / '.github/workflows/apk-repository.yml').read_text()
        dispatch = job(pages, 'dispatch')
        self.assertIn("if: github.event_name == 'release'", dispatch)
        self.assertIn('actions: write', dispatch)
        self.assertIn('--repo "$GITHUB_REPOSITORY" --ref "$DEFAULT_BRANCH"', dispatch)
        self.assertIn("if: github.event_name != 'release'", job(pages, 'assemble'))
        self.assertIn('needs: assemble', job(pages, 'sign'))
        self.assertIn("github.event_name == 'release' && github.run_id", pages)

    def test_the_tag_must_match_release_env(self):
        build = job(RELEASE, 'build')
        self.assertIn('. firmware/alpine/release.env', build)
        self.assertIn('"$GITHUB_REF_NAME" != "$NANOKVM_VERSION"', build)
        self.assertIn('python3 scripts/next-release.py', build)

    def test_the_tag_of_a_published_draft_releases_nothing_again(self):
        build = job(RELEASE, 'build')
        self.assertIn('gh release view "$GITHUB_REF_NAME"', build)
        self.assertEqual(build.count("if: steps.version.outputs.skip != 'true'"), 5)

    def test_compiler_cache_stays_out_of_signing_and_prs_do_not_save(self):
        self.assertNotIn('compiler-cache', job(RELEASE, 'sign'))
        finish = (ROOT / '.github/actions/save-compiler-cache/action.yml').read_text()
        self.assertIn("github.ref == 'refs/heads/main'", finish)
        self.assertIn('actions/cache/save@', finish)
        prepare = (ROOT / '.github/actions/prepare-build/action.yml').read_text()
        self.assertIn('tests/test-compiler-cache.py --toolchain', prepare)
        self.assertIn('CCACHE_NAMESPACE=nkos-$id', prepare)
        for workflow in (RELEASE, BUILD, (ROOT / '.github/workflows/rustdesk.yml').read_text()):
            self.assertIn('uses: ./.github/actions/save-compiler-cache', job(workflow, 'build'))
            self.assertIn("if: always() && steps.prepare.outputs.ccache-key != ''", job(workflow, 'build'))

    def test_both_workflows_prepare_the_build_alike(self):
        self.assertIn('uses: ./.github/actions/prepare-build', BUILD)
        # Only the build job: the jobs that sign restore nothing from a cache.
        self.assertEqual(RELEASE.count('uses: ./.github/actions/prepare-build'), 1)
        self.assertIn('uses: ./.github/actions/prepare-build', job(RELEASE, 'build'))
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
                        self.assertEqual(values['image_version'], 'v1.6')
                        self.assertEqual(values['previous'], 'v3.2.0')
                        self.assertEqual(values['draft'], 'false' if event == 'workflow_dispatch' else 'true')

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
            self.assertEqual(values['image_version'], 'v1.6-beta.1')

    def test_both_channels_compare_against_latest_tested_packages(self):
        # The packages are built, and compared with the published ones, in the
        # build job; the jobs that sign only sign.
        packages = run_step('Packages, root file system and image')
        step = RELEASE.split('      - name: Packages, root file system and image', 1)[1].split('      - uses:', 1)[0]
        self.assertIn("NANOKVM_CHANNEL: ${{ steps.version.outputs.channel }}", step)
        self.assertIn("NANOKVM_PREVIOUS: https://dormancygrace.github.io/NanoKVM-OS/beta/alpine/", step)
        self.assertIn('platform/build.sh -o "$OUT" -u payloads packages rootfs image', packages)
        for name in ('sign', 'dry-run'):
            self.assertNotIn('NANOKVM_PREVIOUS', job(RELEASE, name))
        pages = (ROOT / '.github/workflows/apk-repository.yml').read_text()
        # Both channels and the add-on repository are indexed and signed.
        self.assertEqual(pages.count('for repo in /site/alpine/$branch/riscv64 /site/beta/alpine/$branch/riscv64; do'), 1)
        self.assertIn('index-apk-repo.py --repo "$addonrepo" --keys /keys --addons', pages)
        self.assertEqual(pages.count('for repo in site/alpine/$branch/riscv64 site/beta/alpine/$branch/riscv64 '
                                     'site/addons/$branch/riscv64; do'), 1)




class AddonWorkflowTests(unittest.TestCase):
    ADDON = (ROOT / '.github/workflows/rustdesk.yml').read_text()

    def test_only_the_publish_job_may_write(self):
        self.assertNotIn('write', job(self.ADDON, 'build'))
        publish = job(self.ADDON, 'publish')
        self.assertIn('contents: write', publish)
        self.assertIn("if: github.event_name == 'workflow_dispatch' && inputs.dry_run != true", publish)

    def test_addon_revision_requires_the_tag_inventory(self):
        # The revision counts this repository's tags only; the archived
        # packages repository is never asked.
        match = re.search(r'^      - name: Version\n(.*?)(?=^      - |\Z)', self.ADDON, re.M | re.S)
        script = textwrap.dedent(match[1].split('        run: |\n', 1)[1])
        version = re.search(r'^version = "(.*)"$', (ROOT / 'addons/rustdesk/Cargo.toml').read_text(), re.M)[1]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'gh').write_text("""#!/bin/sh
case "$*" in
  *repos/example/current/tags*) [ "$FAIL" != current ] || exit 17
    echo nanokvm-rustdesk-$VERSION-r8 ;;
  *) exit 99 ;;
esac
""")
            (root / 'gh').chmod(0o755)
            env = {**os.environ, 'PATH': str(root) + ':' + os.environ['PATH'],
                   'GITHUB_REPOSITORY': 'example/current', 'VERSION': version,
                   'GITHUB_OUTPUT': str(root / 'output'), 'RUNNER_TEMP': str(root)}
            for failure in ('none', 'current'):
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
