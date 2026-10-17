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
    def test_patch_and_minor_from_main(self):
        self.assertEqual(n.next_release(RELEASES, 'patch', 'main', False),
                         {'version': 'v3.2.1', 'apk_version': '3.2.1', 'image_version': 'v1.4', 'previous': 'v3.2.0'})
        self.assertEqual(n.next_release(RELEASES, 'minor', 'main', False)['version'], 'v3.3.0')

    def test_a_new_image_gets_the_next_image_version(self):
        self.assertEqual(n.next_release(RELEASES, 'minor', 'main', True)['image_version'], 'v1.5')

    def test_a_release_without_an_image_keeps_the_last_image_version(self):
        releases = [release('v3.2.1')] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main', False),
                         {'version': 'v3.2.2', 'apk_version': '3.2.2', 'image_version': 'v1.4', 'previous': 'v3.2.1'})

    def test_a_release_branch_continues_its_own_line(self):
        releases = [release('v3.2.1')] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'release/3.2', False)['version'], 'v3.2.2')

    def test_no_patch_of_an_older_line_once_a_newer_one_is_released(self):
        releases = [release('v3.3.0'), release('v3.2.1')] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main', False)['version'], 'v3.3.1')
        with self.assertRaisesRegex(ValueError, 'v3.3.0 is released already'):
            n.next_release(releases, 'patch', 'release/3.2', False)
        with self.assertRaisesRegex(ValueError, 'v3.3.0 is released already'):
            n.next_release([release('v3.3.0', draft=True)] + RELEASES, 'patch', 'release/3.2', False)
        with self.assertRaisesRegex(ValueError, 'patch releases only'):
            n.next_release(releases, 'minor', 'release/3.2', False)
        with self.assertRaisesRegex(ValueError, 'no vX.Y.Z release to follow'):
            n.next_release(releases, 'patch', 'release/3.9', False)

    def test_a_draft_takes_its_version(self):
        releases = [release('v3.2.1', draft=True)] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main', False)['version'], 'v3.2.2')

    def test_a_draft_image_does_not_count(self):
        releases = [release('v3.3.0', 'v1.5', draft=True)] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main', False)['image_version'], 'v1.4')

    def test_notes_skip_unpublished_drafts(self):
        for tag in ('v3.2.1', 'v3.3.0'):
            with self.subTest(tag=tag):
                releases = [release(tag, draft=True)] + RELEASES
                result = n.next_release(releases, 'patch', 'main', False)
                self.assertEqual(result['previous'], 'v3.2.0')

    def test_notes_prefer_a_published_release_of_the_same_line(self):
        releases = [release('v3.3.2', draft=True), release('v3.3.1'), release('v3.2.9')] + RELEASES
        result = n.next_release(releases, 'patch', 'main', False)
        self.assertEqual(result['version'], 'v3.3.3')
        self.assertEqual(result['previous'], 'v3.3.1')

    def test_new_images_reserve_draft_numbers(self):
        releases = [release('v3.3.0', 'v1.5', draft=True)] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main', True)['image_version'], 'v1.6')

    def test_add_on_releases_do_not_count(self):
        releases = [release('nanokvm-rustdesk-0.5.3-r6')] + RELEASES
        self.assertEqual(n.next_release(releases, 'patch', 'main', False)['version'], 'v3.2.1')

    def test_other_refs_are_refused(self):
        with self.assertRaisesRegex(ValueError, 'from main or a release/X.Y branch'):
            n.next_release(RELEASES, 'patch', 'feature/x', False)


if __name__ == '__main__':
    unittest.main()
