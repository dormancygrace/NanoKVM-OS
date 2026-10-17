#!/usr/bin/env python3
"""The next RustDesk add-on package revision; no network."""
import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('revision', ROOT / 'scripts/next-rustdesk-revision.py')
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)
TAGS = ['v3.2.0', 'nanokvm-rustdesk-0.5.3-r5', 'nanokvm-rustdesk-0.5.3-r0', 'nanokvm-rustdesk-0.5.1-r0',
        'python-input-3.14.7-r1', 'nanokvm-rustdesk-0.5.3-r12x']


class RevisionTests(unittest.TestCase):
    def test_one_more_than_the_highest_of_the_version(self):
        self.assertEqual(r.next_revision('0.5.3', TAGS), 6)
        self.assertEqual(r.next_revision('0.5.3', TAGS + ['nanokvm-rustdesk-0.5.3-r10']), 11)

    def test_a_new_version_starts_at_zero(self):
        self.assertEqual(r.next_revision('0.6.0', TAGS), 0)

    def test_other_versions_and_tags_do_not_count(self):
        self.assertEqual(r.next_revision('0.5.1', TAGS), 1)
        self.assertEqual(r.next_revision('0.5.3', ['nanokvm-rustdesk-0.5.30-r9']), 0)


if __name__ == '__main__':
    unittest.main()
