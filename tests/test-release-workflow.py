#!/usr/bin/env python3
"""Static checks of the release workflow: where the keys are used, and that
its key names match the published release keys. No network."""
import re
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
        self.assertIn('--draft', sign)
        for name in ('build', 'dry-run'):
            self.assertNotIn('environment:', job(RELEASE, name))
            self.assertNotIn('${{ secrets.', job(RELEASE, name))
        self.assertIn('keys: -d', job(RELEASE, 'dry-run'))
        self.assertIn("if: needs.build.outputs.publish != 'true'", job(RELEASE, 'dry-run'))
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
        for name in ('kind', 'ref', 'image', 'dry_run'):
            self.assertRegex(RELEASE, rf'\n      {name}:\n')
        # A patch release is published at once, and so are its packages.
        sign = job(RELEASE, 'sign')
        self.assertIn('gh workflow run apk-repository.yml', sign)
        self.assertIn('actions: write', sign)

    def test_the_tag_must_match_release_env(self):
        build = job(RELEASE, 'build')
        self.assertIn('. firmware/alpine/release.env', build)
        self.assertIn('"$GITHUB_REF_NAME" != "$NANOKVM_VERSION"', build)
        self.assertIn('python3 scripts/next-release.py', build)

    def test_both_workflows_prepare_the_build_alike(self):
        self.assertIn('uses: ./.github/actions/prepare-build', BUILD)
        self.assertEqual(RELEASE.count('uses: ./.github/actions/prepare-build'), 3)
        # Only builds of main and pull requests save caches; a tag's would serve no one.
        self.assertIn("save-caches: 'true'", BUILD)
        self.assertNotIn('save-caches', RELEASE)


if __name__ == '__main__':
    unittest.main()
