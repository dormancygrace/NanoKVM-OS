#!/usr/bin/env python3
"""The signing job receives only finished release outputs, with their modes."""
import importlib.util
import io
import os
import re
from pathlib import Path
import subprocess
import tarfile
import tempfile
import textwrap
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

    def pack(self, build, *, check=True, stock=False):
        # Execute the actual workflow, skipping only the expensive build.
        workflow = (ROOT / '.github/workflows/release.yml').read_text()
        step = re.search(r'^      - name: Packages, root file system and image\n'
                         r'(.*?)(?=^      - |\Z)', workflow, re.M | re.S)[1]
        run = textwrap.dedent(step.split('        run: |\n', 1)[1])
        build_command, packing = run.split('\n', 1)
        self.assertTrue(build_command.startswith('platform/build.sh '))
        # The optional builder is also expensive; exercise its handoff with a
        # prepared output just as the ordinary image builder above.
        command = '  platform/build.sh -o "$OUT" stock-installer'
        self.assertIn(command, packing)
        packing = packing.replace(command, '  :')
        result = subprocess.run(['bash', '-e', '-o', 'pipefail', '-c', packing], cwd=ROOT,
                              env={**os.environ, 'OUT': str(build), 'RUNNER_TEMP': str(self.root),
                                   'NANOKVM_VERSION': 'v3.5.0', 'LC_ALL': 'C',
                                   'BUILD_STOCK_INSTALLER': 'true' if stock else 'false'},
                              capture_output=True, text=True)
        if check:
            self.assertEqual(result.returncode, 0, result.stderr)
        return result

    def build_tar(self):
        """What the build job packs (release.yml)."""
        build = Path(tempfile.mkdtemp(prefix="build outputs ", dir=self.root))
        (build / 'release/apk/recipes/riscv64').mkdir(parents=True)
        (build / 'release/image').mkdir()
        (build / 'release/apk/recipes/riscv64/nanokvm-3.5.0-r0.apk').write_bytes(b'apk')
        (build / 'release/image/NanoKVM-OS.img.zip').write_bytes(b'image')
        (build / 'release/image/NanoKVM-OS.img.zip').chmod(0o755)
        for name in FILES:
            (build / name).write_bytes(b'list')
        (build / 'release/image/build-manifest.json').write_bytes(b'{}')
        # The real image builder leaves an extracted root with private runtime
        # paths, symlinks and raw filesystem images. None belongs in the handoff.
        stage = build / 'release/image/root'
        stage.mkdir()
        (stage / 'private').write_bytes(b'private')
        (stage / 'link').symlink_to('private')
        stage.chmod(0)
        self.addCleanup(stage.chmod, 0o700)
        for name in ('rootfs.f2fs', 'boot-partition.img', 'NanoKVM-OS.img', 'SHA256SUMS'):
            (build / 'release/image' / name).write_bytes(b'intermediate')
        # Built, but not handed over.
        (build / 'release/alpine-rootfs.tar.gz').write_bytes(b'rootfs')
        (build / SOURCE).write_bytes(b'source')
        self.pack(build)
        return self.root / 'release-build.tar'

    def with_member(self, archive, info, data=b''):
        with tarfile.open(archive, 'a:') as tar:
            tar.addfile(info, io.BytesIO(data) if info.isreg() else None)
        return archive

    def test_build_outputs_are_unpacked_with_their_modes(self):
        names = m.unpack(self.build_tar(), self.out)
        self.assertEqual(set(n.rstrip('/') for n in names), {
            'release/apk', 'release/apk/recipes', 'release/apk/recipes/riscv64',
            'release/apk/recipes/riscv64/nanokvm-3.5.0-r0.apk',
            'release/image/NanoKVM-OS.img.zip', 'release/image/build-manifest.json',
            *FILES, SOURCE,
        })
        self.assertEqual((self.out / 'release/apk/recipes/riscv64/nanokvm-3.5.0-r0.apk').read_bytes(), b'apk')
        self.assertEqual((self.out / 'release/image/NanoKVM-OS.img.zip').stat().st_mode & 0o777, 0o755)
        self.assertTrue((self.out / FILES[0]).is_file())

    def test_opt_in_stock_installer_is_transferred(self):
        self.build_tar()
        build = next(p for p in self.root.iterdir()
                     if p.is_dir() and p.name.startswith('build outputs '))
        package = build / 'release/stock-installer/nanokvm_3.5.0.tar.gz'
        package.parent.mkdir()
        package.write_bytes(b'installer')
        (package.parent / 'SHA256SUMS').write_text('checksums')
        self.pack(build, stock=True)
        m.unpack(self.root / 'release-build.tar', self.out)
        self.assertEqual((self.out / package.relative_to(build)).read_bytes(), b'installer')

    def test_missing_finished_image_or_manifest_fails_the_build(self):
        self.build_tar()
        build = next(p for p in self.root.iterdir()
                     if p.is_dir() and p.name.startswith('build outputs '))
        for name in ('NanoKVM-OS.img.zip', 'build-manifest.json'):
            path = build / 'release/image' / name
            data = path.read_bytes()
            path.unlink()
            try:
                result = self.pack(build, check=False)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn('Cannot stat', result.stderr)
            finally:
                path.write_bytes(data)

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
