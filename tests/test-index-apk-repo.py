#!/usr/bin/env python3
"""Index inventory and signature-boundary regression tests."""
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
spec = importlib.util.spec_from_file_location('indexer', ROOT / 'scripts/index-apk-repo.py')
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
# `apk adbdump` of the published nanokvm-rustdesk-0.5.5-r0.apk, most file
# entries left out.
FIXTURE = (ROOT / 'tests/fixtures/nanokvm-rustdesk-0.5.5-r0.adbdump').read_text()

class IndexTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name)
        (self.repo / 'APKINDEX.tar.gz').write_bytes(b'legacy')
        self.addon = 'nanokvm-rustdesk-0.6.0-r0.apk'
        self.calls = []
    def run_index(self, wrong=False, inventory=False, unsigned_core=False, addons_only=True, pins=None):
        def call(command, **kwargs):
            self.calls.append(command)
            if unsigned_core and 'verify' in command:
                raise subprocess.CalledProcessError(1, command)
            if 'adbdump' in command:
                if command[-1].endswith(self.addon):
                    dump = FIXTURE.replace('version: 0.5.5-r0', 'version: 0.6.0-r0')
                    return dump.replace('name: nanokvm-rustdesk\n', 'name: nanokvm\n', 1) if wrong else dump
                core = '' if addons_only else '  - name: nanokvm\n    version: 3.3.1-r0\n'
                return core + ('' if inventory else '  - name: nanokvm-rustdesk\n    version: 0.6.0-r0\n')
            return ''
        pins = {self.addon: {}} if pins is None else pins
        with mock.patch.object(m.addon_packages, 'verify_repository'), mock.patch.object(m.addon_packages, 'manifest', return_value=pins), mock.patch.object(m.assembler, 'index_packages', return_value={'nanokvm-3.3.1-r0.apk': 42}) as core, mock.patch.object(m.subprocess, 'check_output', side_effect=call):
            (self.repo / 'addons.json').write_text('{}')
            result = m.build_index(self.repo, self.repo / 'keys', addons_only=addons_only)
            self.core_read = core.called
            return result
    def test_the_addon_index_has_the_pinned_addons_only(self):
        self.assertEqual(self.run_index(), [self.addon])
        self.assertFalse(self.core_read)
        self.assertFalse(any('verify' in c for c in self.calls))
        mk = next(c for c in self.calls if 'mkndx' in c)
        self.assertEqual(mk[-1:], [str(self.repo / self.addon)])
        self.assertIn('NanoKVM OS add-ons', mk)
    def test_the_os_index_has_the_current_core_packages(self):
        def call_inventory():
            return self.run_index(addons_only=False, pins={}, inventory=True)
        self.assertEqual(call_inventory(), ['nanokvm-3.3.1-r0.apk'])
        self.assertTrue(self.core_read)
        mk = next(c for c in self.calls if 'mkndx' in c)
        self.assertEqual(mk[-1:], [str(self.repo / 'nanokvm-3.3.1-r0.apk')])
        self.assertIn('NanoKVM OS packages', mk)
    def test_addon_cannot_impersonate_core_package(self):
        with self.assertRaisesRegex(ValueError, 'metadata mismatch'): self.run_index(wrong=True)
        self.assertFalse(any('mkndx' in c for c in self.calls))
    def test_unsigned_os_index_rejected_before_regeneration(self):
        with self.assertRaises(subprocess.CalledProcessError): self.run_index(unsigned_core=True, addons_only=False)
        self.assertFalse(any('mkndx' in c for c in self.calls))
    def test_selecting_addon_is_rejected_before_regeneration(self):
        global FIXTURE
        saved = FIXTURE
        FIXTURE = FIXTURE.replace('paths:', '  install-if: # 1 items\n    - nanokvm\npaths:', 1)
        try:
            with self.assertRaisesRegex(ValueError, 'install-if'): self.run_index()
        finally:
            FIXTURE = saved
        self.assertFalse(any('mkndx' in c for c in self.calls))
    def test_index_missing_new_addon_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'inventory'): self.run_index(inventory=True)

class MetadataTests(unittest.TestCase):
    name = 'nanokvm-rustdesk-0.5.5-r0.apk'
    def test_published_addon_is_accepted(self):
        info = m.addon_packages.metadata(FIXTURE, self.name)
        self.assertEqual((info['name'], info['version'], info['arch']), ('nanokvm-rustdesk', '0.5.5-r0', 'riscv64'))
        self.assertEqual(info['depends'].split()[-1], 'openrc')
    def refused(self, dump, what):
        with self.assertRaisesRegex(ValueError, what): m.addon_packages.metadata(dump, self.name)
    def test_selection_and_replacement_fields_are_refused(self):
        # As apk mkpkg --info writes them: install-if would select the add-on
        # on every device, provides/replaces let it stand in for a core package.
        for field in ('install-if', 'provides', 'replaces', 'recommends', 'provider-priority', 'layer'):
            self.refused(FIXTURE.replace('paths:', f'  {field}: # 1 items\n    - nanokvm\npaths:', 1), field)
    def test_conflicts_triggers_and_unknown_scripts_are_refused(self):
        self.refused(FIXTURE.replace("    - openrc\n", "    - openrc\n    - '!nanokvm'\n", 1), 'conflict with nanokvm')
        self.refused(FIXTURE + 'triggers: # 1 items\n  - /usr/lib/*\n', 'triggers')
        self.refused(FIXTURE.replace('  pre-upgrade: |', '  pre-fetch: |', 1), 'script pre-fetch')
        self.refused(FIXTURE.replace('  installed-size:', '  installed-size: 1\n    - x\n  installed-size:', 1), 'unexpected info line')


if __name__ == '__main__': unittest.main()
