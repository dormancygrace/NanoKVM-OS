#!/usr/bin/env python3
"""The next release version for the Release workflow's button; no network."""
import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('next_release', ROOT / 'scripts/next-release.py')
n = importlib.util.module_from_spec(spec)
spec.loader.exec_module(n)


def release(tag, image=None, draft=False):
    assets = [{'name': 'Packages.adb'}]
    if image:
        assets.append({'name': f'NanoKVM-OS-Image-{image}-apps-{tag}.img.zip'})
    return {'tag_name': tag, 'draft': draft, 'assets': assets}


RELEASES = [release('v3.2.0', 'v1.4'), release('v3.1.0', 'v1.3'), release('v3.0-a1', 'v1.2-a1'), release('v2.0-b7')]


class NextReleaseTests(unittest.TestCase):
    def test_beta_series_and_stable_are_independent(self):
        first = n.next_release(RELEASES, 'minor', 'main', 'beta')
        self.assertEqual(first, dict(version='v3.3.0-beta.1', apk_version='3.3.0_beta1',
                                    previous='v3.2.0'))
        history = RELEASES + [release('v3.3.0-beta.1', 'v1.5-beta.1'),
                              release('v3.3.0-beta.2', 'v1.5-beta.2', draft=True)]
        result = n.next_release(history, 'minor', 'main', 'beta')
        self.assertEqual(result['version'], 'v3.3.0-beta.3')
        self.assertEqual(result['previous'], 'v3.3.0-beta.1')
        stable = n.next_release(history, 'minor', 'main')
        self.assertEqual(stable['version'], 'v3.3.0')
        self.assertEqual(n.next_release(history, 'patch', 'main')['version'], 'v3.2.1')
        history.append(release('v3.3.0', 'v1.5'))
        self.assertEqual(n.next_release(history, 'minor', 'main', 'beta')['version'], 'v3.4.0-beta.1')

    def test_beta_cannot_publish_below_active_beta_line(self):
        history = RELEASES + [release('v3.3.0-beta.1')]
        with self.assertRaisesRegex(ValueError, 'newer beta target'):
            n.next_release(history, 'patch', 'main', 'beta')

    def test_technical_storage_does_not_reserve_an_os_version(self):
        history = RELEASES + [release('os-packages-v99.0.0'), release('os-packages-v4.0.0-beta.1')]
        for channel in ('stable', 'beta'):
            self.assertEqual(n.next_release(history, 'minor', 'main', channel),
                             n.next_release(RELEASES, 'minor', 'main', channel))

    def test_patch_and_minor_from_main(self):
        self.assertEqual(n.next_release(RELEASES, 'patch', 'main'),
                         {'version': 'v3.2.1', 'apk_version': '3.2.1', 'previous': 'v3.2.0'})
        self.assertEqual(n.next_release(RELEASES, 'minor', 'main')['version'], 'v3.3.0')

    def test_versions_do_not_require_any_historical_images(self):
        history = [{'tag_name': 'v3.6.0', 'draft': False}]
        self.assertEqual(n.next_release(history, 'patch', 'main'),
                         dict(version='v3.6.1', apk_version='3.6.1', previous='v3.6.0'))
        self.assertEqual(n.next_release(history, 'minor', 'main', 'beta'),
                         dict(version='v3.7.0-beta.1', apk_version='3.7.0_beta1', previous='v3.6.0'))

    def test_a_release_without_an_image_advances_the_os_version(self):
        releases = [release('v3.2.1')] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main'),
                         {'version': 'v3.2.2', 'apk_version': '3.2.2', 'previous': 'v3.2.1'})

    def test_a_release_branch_continues_its_own_line(self):
        releases = [release('v3.2.1')] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'release/3.2')['version'], 'v3.2.2')

    def test_no_patch_of_an_older_line_once_a_newer_one_is_released(self):
        releases = [release('v3.3.0'), release('v3.2.1')] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main')['version'], 'v3.3.1')
        with self.assertRaisesRegex(ValueError, 'v3.3.0 is released already'):
            n.next_release(releases, 'patch', 'release/3.2')
        with self.assertRaisesRegex(ValueError, 'v3.3.0 is released already'):
            n.next_release([release('v3.3.0', draft=True)] + RELEASES, 'patch', 'release/3.2')
        with self.assertRaisesRegex(ValueError, 'patch releases only'):
            n.next_release(releases, 'minor', 'release/3.2')
        with self.assertRaisesRegex(ValueError, 'no vX.Y.Z release to follow'):
            n.next_release(releases, 'patch', 'release/3.9')

    def test_a_draft_takes_its_version(self):
        releases = [release('v3.2.1', draft=True)] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main')['version'], 'v3.2.2')

    def test_historical_image_numbers_do_not_affect_os_versions(self):
        history = [release('v3.2.0', 'v99.8'), release('v3.1.0', 'v99.7')]
        self.assertEqual(n.next_release(history, 'minor', 'main'),
                         n.next_release(RELEASES, 'minor', 'main'))

    def test_notes_skip_unpublished_drafts(self):
        for tag in ('v3.2.1', 'v3.3.0'):
            with self.subTest(tag=tag):
                releases = [release(tag, draft=True)] + RELEASES
                result = n.next_release(releases, 'patch', 'main')
                self.assertEqual(result['previous'], 'v3.2.0')

    def test_notes_prefer_a_published_release_of_the_same_line(self):
        releases = [release('v3.3.2', draft=True), release('v3.3.1'), release('v3.2.9')] + RELEASES
        result = n.next_release(releases, 'patch', 'main')
        self.assertEqual(result['version'], 'v3.3.3')
        self.assertEqual(result['previous'], 'v3.3.1')

    def test_new_image_names_need_no_second_version_counter(self):
        history = [release('v3.3.0', draft=True)] + RELEASES
        history[0]['assets'] = [{'name': 'NanoKVM-OS-v3.3.0.img.zip'}]
        self.assertEqual(n.next_release(history, 'patch', 'main'),
                         dict(version='v3.3.1', apk_version='3.3.1', previous='v3.2.0'))

    def test_add_on_releases_do_not_count(self):
        releases = [release('nanokvm-rustdesk-0.5.3-r6')] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main')['version'], 'v3.2.1')

    def test_other_refs_are_refused(self):
        with self.assertRaisesRegex(ValueError, 'from main or a release/X.Y branch'):
            n.next_release(RELEASES, 'patch', 'feature/x')


if __name__ == '__main__':
    unittest.main()
