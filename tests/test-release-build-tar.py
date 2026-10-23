#!/usr/bin/env python3
"""The signing job unpacks only the images and the source archive."""
import importlib.util
import io
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('release_build_tar', ROOT / 'scripts/release_build_tar.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
SOURCE = 'NanoKVM-OS-v3.5.0-source.tar.xz'


class UnpackTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.out = self.root / 'out'
        self.out.mkdir()

    def build_tar(self):
        """What the build job packs: tar -C OUT -cf TAR images SOURCE."""
        build = Path(tempfile.mkdtemp(dir=self.root))
        (build / 'images/opensbi').mkdir(parents=True)
        (build / 'images/Image').write_bytes(b'kernel')
        (build / 'images/opensbi/fw_dynamic.bin').write_bytes(b'sbi')
        (build / 'images/Image').chmod(0o755)
        (build / SOURCE).write_bytes(b'source')
        archive = build.with_suffix('.tar')
        subprocess.run(['tar', '-C', str(build), '-cf', str(archive), 'images', SOURCE], check=True)
        return archive

    def with_member(self, archive, info, data=b''):
        with tarfile.open(archive, 'a:') as tar:
            tar.addfile(info, io.BytesIO(data) if info.isreg() else None)
        return archive

    def test_build_outputs_are_unpacked_with_their_modes(self):
        names = m.unpack(self.build_tar(), self.out)
        self.assertIn(SOURCE, names)
        self.assertEqual((self.out / 'images/opensbi/fw_dynamic.bin').read_bytes(), b'sbi')
        self.assertEqual((self.out / 'images/Image').stat().st_mode & 0o777, 0o755)

    def refused(self, info, what, data=b''):
        archive = self.with_member(self.build_tar(), info, data)
        with self.assertRaisesRegex(ValueError, what):
            m.unpack(archive, self.out)
        # Nothing is written before every member is checked.
        self.assertEqual(list(self.out.iterdir()), [])

    def test_a_file_over_the_restored_toolchain_is_refused(self):
        info = tarfile.TarInfo('buildroot-output/host/bin/python3')
        info.size, info.mode = 4, 0o755
        self.refused(info, 'Unexpected member', b'evil')

    def test_links_are_refused(self):
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE):
            info = tarfile.TarInfo('images/boot')
            info.type, info.linkname = kind, '../buildroot-output/host/bin'
            self.refused(info, 'Not a file or directory')

    def test_absolute_and_parent_names_are_refused(self):
        for name in ('/etc/passwd', 'images/../buildroot-output/host/bin/zstd'):
            info = tarfile.TarInfo(name)
            info.size = 1
            self.refused(info, 'Unsafe member name', b'x')

    def test_the_sign_job_uses_it(self):
        action = (ROOT / '.github/actions/assemble-release/action.yml').read_text()
        self.assertIn('python3 scripts/release_build_tar.py "$RUNNER_TEMP/release-build.tar" "$OUT"', action)
        self.assertNotIn('-xf "$RUNNER_TEMP/release-build.tar"', action)


if __name__ == '__main__':
    unittest.main()
