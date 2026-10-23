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
FILES = m.FILES


class UnpackTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.out = self.root / 'out'
        self.out.mkdir()

    def build_tar(self):
        """What the build job packs (release.yml)."""
        build = Path(tempfile.mkdtemp(dir=self.root))
        (build / 'release/apk/recipes/riscv64').mkdir(parents=True)
        (build / 'release/image').mkdir()
        (build / 'release/apk/recipes/riscv64/nanokvm-3.5.0-r0.apk').write_bytes(b'apk')
        (build / 'release/image/NanoKVM-OS.img.zip').write_bytes(b'image')
        (build / 'release/image/NanoKVM-OS.img.zip').chmod(0o755)
        for name in FILES:
            (build / name).write_bytes(b'list')
        # Built, but not handed over.
        (build / 'release/alpine-rootfs.tar.gz').write_bytes(b'rootfs')
        (build / SOURCE).write_bytes(b'source')
        archive = build.with_suffix('.tar')
        subprocess.run(['tar', '-C', str(build), '-cf', str(archive), 'release/apk', 'release/image', *FILES, SOURCE],
                       check=True)
        return archive

    def with_member(self, archive, info, data=b''):
        with tarfile.open(archive, 'a:') as tar:
            tar.addfile(info, io.BytesIO(data) if info.isreg() else None)
        return archive

    def test_build_outputs_are_unpacked_with_their_modes(self):
        names = m.unpack(self.build_tar(), self.out)
        self.assertIn(SOURCE, names)
        self.assertEqual((self.out / 'release/apk/recipes/riscv64/nanokvm-3.5.0-r0.apk').read_bytes(), b'apk')
        self.assertEqual((self.out / 'release/image/NanoKVM-OS.img.zip').stat().st_mode & 0o777, 0o755)
        self.assertTrue((self.out / FILES[0]).is_file())

    def refused(self, info, what, data=b''):
        archive = self.with_member(self.build_tar(), info, data)
        with self.assertRaisesRegex(ValueError, what):
            m.unpack(archive, self.out)
        # Nothing is written before every member is checked.
        self.assertEqual(list(self.out.iterdir()), [])

    def test_anything_but_the_release_outputs_is_refused(self):
        for name in ('buildroot-output/host/bin/python3', 'images/Image', 'release/alpine-rootfs.tar.gz',
                     'release/keys/nanokvm-build-ec.key', 'release/apkevil'):
            info = tarfile.TarInfo(name)
            info.size, info.mode = 4, 0o755
            self.refused(info, 'Unexpected member', b'evil')

    def test_links_are_refused(self):
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE):
            info = tarfile.TarInfo('release/apk/boot')
            info.type, info.linkname = kind, '../buildroot-output/host/bin'
            self.refused(info, 'Not a file or directory')

    def test_absolute_and_parent_names_are_refused(self):
        for name in ('/etc/passwd', 'release/apk/../../buildroot-output/host/bin/zstd'):
            info = tarfile.TarInfo(name)
            info.size = 1
            self.refused(info, 'Unsafe member name', b'x')

    def test_the_sign_job_uses_it(self):
        action = (ROOT / '.github/actions/assemble-release/action.yml').read_text()
        self.assertIn('python3 scripts/release_build_tar.py "$RUNNER_TEMP/release-build.tar" "$OUT"', action)
        self.assertNotIn('-xf "$RUNNER_TEMP/release-build.tar"', action)


if __name__ == '__main__':
    unittest.main()
