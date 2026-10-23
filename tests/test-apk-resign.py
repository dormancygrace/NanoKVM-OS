#!/usr/bin/env python3
"""Replacing an APK v2 signature keeps the control stream, the identity."""
import gzip
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('apk_resign', ROOT / 'scripts/apk_resign.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


def stream(files, cut=True):
    """A gzip stream of a tar of files; abuild cuts the end-of-archive blocks."""
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode='w', format=tarfile.USTAR_FORMAT) as tar:
        for name, content in files.items():
            info = tarfile.TarInfo(name)
            info.size = len(content)
            tar.addfile(info, io.BytesIO(content))
    raw = data.getvalue()
    if cut:
        raw = raw.rstrip(b'\0')
        raw += b'\0' * (-len(raw) % 512)
    return gzip.compress(raw, mtime=0)


CONTROL = stream({'.PKGINFO': b'pkgname = nanokvm-app\n'})
DATA = stream({'usr/share/x': b'files'}, cut=False)


class ResignTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.apk = self.dir / 'nanokvm-app-3.5.0-r0.apk'
        self.apk.write_bytes(stream({'.SIGN.RSA256.nanokvm-build-ec-1.pub': b'build'}) + CONTROL + DATA)
        self.work = self.dir / 'work'
        self.work.mkdir()

    def sign(self, entry):
        # What abuild-sign does: prepend a signature stream to the file.
        control = self.work / 'control.tar.gz'
        control.write_bytes(stream({entry: b'release'}) + control.read_bytes())

    def test_only_the_signature_changes(self):
        m.split(self.apk, self.work)
        self.sign('.SIGN.RSA256.nkos-release-ec-b8e89b66.pub')
        m.join(self.apk, self.work, 'nkos-release-ec-b8e89b66.pub')
        streams = m.members(self.apk.read_bytes())
        self.assertEqual(streams[1:], [CONTROL, DATA])
        self.assertEqual(m.names(streams[0]), ['.SIGN.RSA256.nkos-release-ec-b8e89b66.pub'])

    def test_a_sha1_or_foreign_signature_is_refused(self):
        for entry in ('.SIGN.RSA.nkos-release-ec-b8e89b66.pub', '.SIGN.RSA256.other.pub'):
            m.split(self.apk, self.work)
            self.sign(entry)
            with self.assertRaisesRegex(ValueError, 'unexpected signature'):
                m.join(self.apk, self.work, 'nkos-release-ec-b8e89b66.pub')

    def test_changed_control_data_is_refused(self):
        m.split(self.apk, self.work)
        (self.work / 'control.tar.gz').write_bytes(stream({'.PKGINFO': b'pkgname = evil\n'}))
        self.sign('.SIGN.RSA256.nkos-release-ec-b8e89b66.pub')
        with self.assertRaisesRegex(ValueError, 'changed the control data'):
            m.join(self.apk, self.work, 'nkos-release-ec-b8e89b66.pub')

    def test_other_files_are_not_split(self):
        for content, what in ((CONTROL + DATA, '2 gzip streams'),
                              (CONTROL + CONTROL + DATA, 'not a signature'),
                              (stream({'.SIGN.RSA256.k.pub': b's'}) + DATA + DATA, 'no .PKGINFO')):
            self.apk.write_bytes(content)
            with self.assertRaisesRegex(ValueError, what):
                m.split(self.apk, self.work)


if __name__ == '__main__':
    unittest.main()
