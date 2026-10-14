#!/usr/bin/env python3
"""Reject old and mixed native ABIs before the server builder invokes Go."""
from contextlib import redirect_stderr
import io
from pathlib import Path
import runpy
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / 'scripts'))
import nanokvm_cpu_profile  # noqa: E402


class NativeBundleABITests(unittest.TestCase):
    def check_bundle(self, old_capture=False, old_mmf=False, old_dependency=False,
                     expected=None):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            libraries = root / 'libraries'
            libraries.mkdir()
            cross = root / 'buildroot/host/bin'
            cross.mkdir(parents=True)
            # Actual ELF symbol tables, read by the production builder.
            (cross / 'riscv64-buildroot-linux-musl-nm').symlink_to('/usr/bin/nm')
            video = 'kvmv_read_video_sink' if old_capture else 'kvmv_read_video_sink_v2'
            encoder = 'mmf_add_venc_channel' if old_mmf else 'mmf_add_venc_channel_v2'
            dependency = 'mmf_add_venc_channel' if old_dependency else 'mmf_add_venc_channel_v2'
            sources = {
                'kvm': f'void {video}(void) {{}}\n'
                    'void kvmv_read_mjpeg_sink(void) {}\n'
                    'void set_mjpeg_chroma(void) {}\n'
                    'void get_mjpeg_chroma_status(void) {}\n'
                    'extern void mmf_add_vi_channel_configured(void);\n'
                    'extern void mmf_vi_drop_pending(void);\n'
                    f'extern void {dependency}(void);\n'
                    'void imports(void) { mmf_add_vi_channel_configured(); '
                    f'mmf_vi_drop_pending(); {dependency}(); }}\n',
                'kvm_mmf': f'void {encoder}(void) {{}}\n'
                    'void mmf_add_vi_channel_configured(void) {}\n'
                    'void mmf_vi_drop_pending(void) {}\n',
                **{name: 'void unused(void) {}\n' for name in ('ae', 'awb', 'af', 'isp_algo')},
            }
            for name, source in sources.items():
                c = root / f'{name}.c'
                c.write_text(source)
                subprocess.run(['gcc', '-shared', '-fPIC', '-nostdlib', str(c),
                                '-o', str(libraries / f'lib{name}.so')], check=True)
            go = root / 'go/bin/go'
            go.parent.mkdir(parents=True)
            go.write_text('#!/bin/sh\necho reached > "$(dirname "$0")/reached"\nexit 42\n')
            go.chmod(0o755)
            args = ['build-server-existing-libs.py', '--libraries', str(libraries),
                    '--buildroot-output', str(root / 'buildroot'), '--go', str(go),
                    '--output', str(root / 'output')]
            errors = io.StringIO()
            # CPU flag probing is unrelated to the native ABI; all ELF checks
            # and the builder's ordering run unchanged.
            with patch.object(sys, 'argv', args), redirect_stderr(errors), \
                    patch.object(nanokvm_cpu_profile, 'record'), \
                    patch.object(nanokvm_cpu_profile, 'flags', return_value=[]):
                if expected:
                    with self.assertRaises(SystemExit) as failure:
                        runpy.run_path(str(REPO / 'scripts/build-server-existing-libs.py'), run_name='__main__')
                    self.assertEqual(failure.exception.code, 2)
                    self.assertIn(expected, errors.getvalue())
                    self.assertFalse((root / 'output').exists())
                    self.assertFalse((go.parent / 'reached').exists())
                else:
                    with self.assertRaises(subprocess.CalledProcessError) as sentinel:
                        runpy.run_path(str(REPO / 'scripts/build-server-existing-libs.py'), run_name='__main__')
                    self.assertEqual(sentinel.exception.returncode, 42)
                    self.assertTrue((go.parent / 'reached').exists())

    def test_old_complete_bundle_is_rejected(self):
        self.check_bundle(old_capture=True, old_mmf=True, old_dependency=True,
                          expected='libkvm.so is missing kvmv_read_video_sink_v2')

    def test_old_capture_in_new_bundle_is_rejected(self):
        self.check_bundle(old_capture=True,
                          expected='libkvm.so is missing kvmv_read_video_sink_v2')

    def test_old_mmf_in_new_bundle_is_rejected(self):
        self.check_bundle(old_mmf=True,
                          expected='libkvm_mmf.so is missing mmf_add_venc_channel_v2')

    def test_capture_still_importing_old_mmf_is_rejected(self):
        self.check_bundle(old_dependency=True,
                          expected='libkvm.so does not require mmf_add_venc_channel_v2')

    def test_matching_bundle_reaches_go(self):
        self.check_bundle()


if __name__ == '__main__':
    unittest.main()
