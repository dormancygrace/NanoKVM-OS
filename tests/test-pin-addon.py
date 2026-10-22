#!/usr/bin/env python3
"""Add-on pins and the add-on registry; no network."""
import hashlib
import importlib.util
import json
import subprocess
import sys
import tempfile
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


class PinTests(unittest.TestCase):
    def test_a_pin_replaces_only_its_own_add_on(self):
        rustdesk, ipmi = 'nanokvm-rustdesk-0.5.5-r0.apk', 'nanokvm-ipmi-0.1.0-r0.apk'
        data = json.dumps({'packages': {rustdesk: entry(rustdesk), ipmi: entry(ipmi)}})
        newer = 'nanokvm-ipmi-0.2.0-r0.apk'
        packages = json.loads(addon_packages.pin(data, newer, entry(newer, b'new')))['packages']
        self.assertEqual(sorted(packages), [newer, rustdesk])
        packages = json.loads(addon_packages.pin(data, 'nanokvm-rustdesk-0.5.6-r0.apk',
                                                 entry('nanokvm-rustdesk-0.5.6-r0.apk')))['packages']
        self.assertEqual(sorted(packages), [ipmi, 'nanokvm-rustdesk-0.5.6-r0.apk'])

    def test_only_add_ons_are_pinned(self):
        with self.assertRaises(ValueError):
            addon_packages.pin('{"packages": {}}', 'nanokvm-3.4.0-r0.apk', entry('nanokvm-3.4.0-r0.apk'))

    def test_the_script_pins_a_released_apk(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            rustdesk = 'nanokvm-rustdesk-0.5.5-r0.apk'
            manifest = d / 'addons.json'
            manifest.write_text(json.dumps({'packages': {rustdesk: entry(rustdesk)}}))
            apk = d / 'nanokvm-ipmi-0.1.0-r3.apk'
            apk.write_bytes(b'package bytes')
            subprocess.run([sys.executable, str(ROOT / 'scripts/pin-addon.py'), str(apk), '--manifest', str(manifest)],
                           check=True)
            packages = json.loads(manifest.read_text())['packages']
            self.assertEqual(packages[apk.name], entry(apk.name, b'package bytes'))
            self.assertIn(rustdesk, packages)


if __name__ == '__main__':
    unittest.main()
