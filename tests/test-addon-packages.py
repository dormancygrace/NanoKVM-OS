#!/usr/bin/env python3
"""Independent add-on registry; no network."""
import hashlib
import json
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
import addon_packages  # noqa: E402

RELEASES = 'https://github.com/dormancygrace/NanoKVM-OS/releases/download/'


def entry(filename, data=b'apk'):
    tag = filename.removesuffix('.apk')
    return {'url': f'{RELEASES}{tag}/{filename}', 'sha256': hashlib.sha256(data).hexdigest(), 'size': len(data)}


class RegistryTests(unittest.TestCase):
    def test_tags_name_only_registered_add_ons(self):
        for tag in ['nanokvm-rustdesk-0.5.5-r0', 'nanokvm-ipmi-0.1.0-r12']:
            self.assertTrue(addon_packages.TAG.fullmatch(tag), tag)
        for tag in ['v3.4.0', 'v3.4.0-beta.1', 'os-packages-v3.4.0', 'nanokvm-3.4.0-r0', 'nanokvm-web-3.4.0-r0',
                    'nanokvm-ipmi-0.1-r0', 'nanokvm-ipmi-0.1.0', 'nanokvm-tpu-0.1.0-r0', 'xnanokvm-ipmi-0.1.0-r0']:
            self.assertFalse(addon_packages.TAG.fullmatch(tag), tag)

    def test_every_add_on_has_a_release_source(self):
        for name in addon_packages.ADDONS:
            filename = f'{name}-1.0.0-r0.apk'
            self.assertEqual(addon_packages.addon(filename), name)
            self.assertIn(filename, addon_packages.manifest(json.dumps({'packages': {filename: entry(filename)}})))
        self.assertIsNone(addon_packages.addon('nanokvm-ipmi-1.0.0-r0.tar.gz'))


if __name__ == '__main__':
    unittest.main()
