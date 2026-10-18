#!/usr/bin/env python3
"""Index inventory and signature-boundary regression tests."""
import importlib.util
import json
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

class IndexTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name)
        (self.repo / 'APKINDEX.tar.gz').write_bytes(b'legacy')
        self.addon = 'nanokvm-rustdesk-0.6.0-r0.apk'
        self.calls = []
    def run_index(self, wrong=False, inventory=False, unsigned_core=False):
        def call(command, **kwargs):
            self.calls.append(command)
            if unsigned_core and 'verify' in command:
                raise subprocess.CalledProcessError(1, command)
            if 'adbdump' in command:
                if command[-1].endswith(self.addon):
                    return '  name: '+('nanokvm' if wrong else 'nanokvm-rustdesk')+'\n  version: 0.6.0-r0\n  arch: riscv64\n'
                return '  - name: nanokvm\n    version: 3.3.1-r0\n' + ('' if inventory else '  - name: nanokvm-rustdesk\n    version: 0.6.0-r0\n')
            return ''
        with mock.patch.object(m.addon_packages, 'verify_repository'), mock.patch.object(m.addon_packages, 'manifest', return_value={self.addon: {}}), mock.patch.object(m.assembler, 'index_packages', return_value={'nanokvm-3.3.1-r0.apk': 42}), mock.patch.object(m.subprocess, 'check_output', side_effect=call):
            (self.repo / 'addons.json').write_text('{}')
            return m.build_index(self.repo, self.repo / 'keys')
    def test_current_core_and_new_independent_addon_enter_index(self):
        self.assertEqual(self.run_index(), ['nanokvm-3.3.1-r0.apk', self.addon])
        mk = next(c for c in self.calls if 'mkndx' in c)
        self.assertEqual(mk[-2:], [str(self.repo / 'nanokvm-3.3.1-r0.apk'), str(self.repo / self.addon)])
    def test_addon_cannot_impersonate_core_package(self):
        with self.assertRaisesRegex(ValueError, 'metadata mismatch'): self.run_index(wrong=True)
        self.assertFalse(any('mkndx' in c for c in self.calls))
    def test_unsigned_os_index_rejected_before_regeneration(self):
        with self.assertRaises(subprocess.CalledProcessError): self.run_index(unsigned_core=True)
        self.assertFalse(any('mkndx' in c for c in self.calls))
    def test_index_missing_new_addon_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'inventory'): self.run_index(inventory=True)

class WorkflowTests(unittest.TestCase):
    def test_production_signing_is_protected_and_prs_never_sign(self):
        text=(ROOT/'.github/workflows/apk-repository.yml').read_text()
        signing=text.split('  sign:',1)[1].split('  deploy:',1)[0]
        self.assertIn("if: github.event_name != 'pull_request'", signing)
        self.assertIn('environment: release', signing)
        self.assertIn('adbsign --reset-signatures', signing)
        self.assertIn('needs: sign', text.split('  deploy:',1)[1])
        self.assertNotIn('secrets.',text.split('  sign:',1)[0])
    def test_addon_publication_dispatches_pages_without_waiting_for_pin(self):
        text=(ROOT/'.github/workflows/rustdesk.yml').read_text()
        publish=text.split('  publish:',1)[1]
        self.assertIn('actions: write',publish)
        self.assertLess(publish.index('gh release create'),publish.index('gh workflow run apk-repository.yml'))
        self.assertLess(publish.index('gh workflow run apk-repository.yml'),publish.index('gh pr create'))

if __name__ == '__main__': unittest.main()
