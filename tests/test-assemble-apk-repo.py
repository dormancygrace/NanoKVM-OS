#!/usr/bin/env python3
"""Assemble the Pages APK repository from fake releases; no network."""
import gzip
import hashlib
import importlib.util
import io
import json
import tarfile
import tempfile
import unittest
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
spec = importlib.util.spec_from_file_location('assemble', ROOT / 'scripts/assemble-apk-repo.py')
a = importlib.util.module_from_spec(spec)
spec.loader.exec_module(a)


def tar_member(name, data, end=False):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode='w', format=tarfile.USTAR_FORMAT) as tar:
        info = tarfile.TarInfo(name)
        info.size = len(data)
        tar.addfile(info, io.BytesIO(data))
    raw = out.getvalue()
    # abuild strips the end marker of the signature segment.
    return raw if end else raw[:512 + (len(data) + 511) // 512 * 512]


def apkindex(packages):
    text = ''.join(f'C:Q1x=\nP:{p}\nV:{v}\nS:{s}\n\n' for p, v, s in packages)
    return (gzip.compress(tar_member('.SIGN.RSA256.key.rsa.pub', b'signature'))
            + gzip.compress(tar_member('DESCRIPTION', b'x') + tar_member('APKINDEX', text.encode(), end=True)))


class Fake:
    """Releases with assets served from memory; fetch() as in the script."""

    def __init__(self):
        self.files, self.list = {}, []

    def release(self, tag, published, packages, extra=(), draft=False, sums=True):
        assets = {}
        for p, v in packages:
            assets[f'{p}-{v}.apk'] = f'{p} {v}'.encode()
        assets['APKINDEX.tar.gz'] = apkindex([(p, v, len(f'{p} {v}')) for p, v in packages])
        assets['Packages.adb'] = b'adb ' + tag.encode()
        assets.update(extra)
        if sums:
            assets['SHA256SUMS'] = ''.join(f'{hashlib.sha256(d).hexdigest()}  {n}\n'
                                           for n, d in sorted(assets.items())).encode()
        for name, data in assets.items():
            self.files[f'https://dl.test/{tag}/{name}'] = data
        self.list.append({'tag_name': tag, 'draft': draft, 'published_at': published,
                          'assets': [{'name': n, 'browser_download_url': f'https://dl.test/{tag}/{n}'} for n in assets]})

    def fetch(self, url, accept=None):
        if url.startswith('https://api.github.com/'):
            return json.dumps(self.list).encode()
        return self.files[url]


class AssembleTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.out = Path(self.tmp.name) / 'site'
        self.keys = Path(self.tmp.name) / 'keys'
        self.keys.mkdir()
        (self.keys / 'release.rsa.pub').write_text('key')
        self.fake = Fake()
        self.fake.release('v1', '2026-01-01T00:00:00Z', [('nanokvm', '1-r0'), ('nanokvm-web', '1-r0')])
        self.fake.release('v2', '2026-02-01T00:00:00Z', [('nanokvm', '2-r0'), ('nanokvm-web', '1-r0')])
        self.fake.release('v3', '2026-03-01T00:00:00Z', [('nanokvm', '3-r0'), ('nanokvm-web', '3-r0')])

    def run_assemble(self, keep=3):
        published = a.releases('o/r', self.fake.fetch)
        return a.assemble(self.out, published, keep, '3.24', self.keys, self.fake.fetch)

    def repo(self):
        return self.out / 'alpine/v3.24/riscv64'

    def test_newest_indexes_and_packages_of_the_kept_releases(self):
        tag, names = self.run_assemble(keep=2)
        self.assertEqual(tag, 'v3')
        self.assertEqual((self.repo() / 'Packages.adb').read_bytes(), b'adb v3')
        self.assertEqual(sorted(p.name for p in self.repo().glob('*.apk')),
                         ['nanokvm-2-r0.apk', 'nanokvm-3-r0.apk', 'nanokvm-web-1-r0.apk', 'nanokvm-web-3-r0.apk'])
        self.assertTrue((self.out / 'keys/release.rsa.pub').exists())
        self.assertIn('alpine/v3.24/riscv64/Packages.adb', (self.out / 'index.html').read_text())

    def test_packages_json_of_the_newest_release_is_published(self):
        self.run_assemble()
        self.assertFalse((self.repo() / 'packages.json').exists())
        self.fake.release('v4', '2026-04-01T00:00:00Z', [('nanokvm', '4-r0')], extra={'packages.json': b'{}'})
        self.run_assemble()
        self.assertEqual((self.repo() / 'packages.json').read_bytes(), b'{}')

    def test_an_unchanged_package_comes_from_an_older_release(self):
        # v4 publishes only nanokvm; its index still names nanokvm-web 3.
        self.fake.release('v4', '2026-04-01T00:00:00Z', [('nanokvm', '4-r0')])
        self.fake.files['https://dl.test/v4/APKINDEX.tar.gz'] = apkindex(
            [('nanokvm', '4-r0', len('nanokvm 4-r0')), ('nanokvm-web', '3-r0', len('nanokvm-web 3-r0'))])
        sums = self.fake.files['https://dl.test/v4/SHA256SUMS'].decode().splitlines()
        sums = [l for l in sums if not l.endswith('APKINDEX.tar.gz')]
        sums.append(hashlib.sha256(self.fake.files['https://dl.test/v4/APKINDEX.tar.gz']).hexdigest() + '  APKINDEX.tar.gz')
        self.fake.files['https://dl.test/v4/SHA256SUMS'] = '\n'.join(sums).encode()
        self.run_assemble(keep=1)
        self.assertEqual(sorted(p.name for p in self.repo().glob('*.apk')), ['nanokvm-4-r0.apk', 'nanokvm-web-3-r0.apk'])
        self.assertEqual((self.repo() / 'nanokvm-web-3-r0.apk').read_bytes(), b'nanokvm-web 3-r0')

    def external_addon(self):
        name = 'nanokvm-rustdesk-0.5.3-r5.apk'
        data = b'signed optional APK'
        url = 'https://github.com/dormancygrace/NanoKVM-OS-packages/releases/download/r5/' + name
        item = {'url': url, 'sha256': hashlib.sha256(data).hexdigest(), 'size': len(data)}
        self.fake.files[url] = data
        self.fake.release('v4', '2026-04-01T00:00:00Z', [('nanokvm', '4-r0')], extra={
            'addons.json': json.dumps({'packages': {name: item}}).encode(),
            'APKINDEX.tar.gz': apkindex([('nanokvm', '4-r0', len('nanokvm 4-r0'))])})
        return name, item, data

    def test_optional_addon_from_separate_release_is_available(self):
        name, _, data = self.external_addon()
        self.run_assemble(keep=1)
        self.assertEqual((self.repo() / name).read_bytes(), data)
        self.assertEqual((self.repo() / 'Packages.adb').read_bytes(), b'adb v4')
        self.assertNotIn(name, [a['name'] for a in self.fake.list[-1]['assets']])
        self.assertNotIn(name, a.index_packages((self.repo() / 'APKINDEX.tar.gz').read_bytes()))

    def test_optional_addon_corruption_is_rejected(self):
        _, item, _ = self.external_addon()
        self.fake.files[item['url']] = b'tampered'
        with self.assertRaisesRegex(ValueError, 'pinned size or SHA256'):
            self.run_assemble(keep=1)

    def test_optional_addon_index_size_must_match(self):
        name, item, _ = self.external_addon()
        item['size'] += 1
        with self.assertRaisesRegex(ValueError, 'size differs from its index'):
            a.addon_packages.checked(name, item, self.fake.fetch, size=item['size'] - 1)

    def test_optional_addon_manifest_must_match_release_checksums(self):
        self.external_addon()
        self.fake.files['https://dl.test/v4/addons.json'] = b'{}'
        with self.assertRaisesRegex(ValueError, 'SHA256SUMS'):
            self.run_assemble(keep=1)

    def test_optional_addon_manifest_rejects_unsafe_names_and_sources(self):
        name, item, _ = self.external_addon()
        main = 'https://github.com/dormancygrace/NanoKVM-OS/releases/download/nanokvm-rustdesk-0.5.3-r6/' + name
        self.assertIn(name, a.addon_packages.manifest(json.dumps({'packages': {name: {**item, 'url': main}}})))
        other_release = 'https://github.com/dormancygrace/NanoKVM-OS/releases/download/v3.3.0/' + name
        for bad_name, bad_item in [('../evil.apk', item), (name, {**item, 'url': 'http://evil/evil.apk'}),
                                   (name, {**item, 'url': other_release}),
                                   (name, {**item, 'sha256': 'bad'}), (name, {**item, 'size': -1})]:
            with self.subTest(name=bad_name, item=bad_item):
                with self.assertRaises(ValueError):
                    a.addon_packages.manifest(json.dumps({'packages': {bad_name: bad_item}}))

    def test_the_highest_version_is_newest_not_the_latest_published(self):
        self.fake.release('v3.3.1', '2026-05-01T00:00:00Z', [('nanokvm', '31-r0')])
        self.fake.release('v3.3.0', '2026-06-01T00:00:00Z', [('nanokvm', '30-r0')])  # its draft published later
        self.fake.release('v3.2.9', '2026-07-01T00:00:00Z', [('nanokvm', '29-r0')])
        self.assertEqual(self.run_assemble(keep=1)[0], 'v3.3.1')
        self.assertEqual(sorted(p.name for p in self.repo().glob('*.apk')), ['nanokvm-31-r0.apk'])

    def test_the_page_links_the_newest_image(self):
        self.run_assemble()
        self.assertNotIn('new SD card', (self.out / 'index.html').read_text())
        self.fake.release('v2.0.0', '2026-01-05T00:00:00Z', [('nanokvm', '20-r0')],
                          extra={'NanoKVM-OS-Image-v1.4-apps-v2.0.0.img.zip': b'image'})
        self.fake.release('v2.0.1', '2026-01-06T00:00:00Z', [('nanokvm', '21-r0')])
        self.run_assemble()
        page = (self.out / 'index.html').read_text()
        self.assertIn('NanoKVM-OS-Image-v1.4-apps-v2.0.0.img.zip', page)
        self.assertIn('from release v2.0.0', page)

    def test_drafts_and_releases_without_indexes_or_sums_are_skipped(self):
        self.fake.release('v9', '2026-09-01T00:00:00Z', [('nanokvm', '9-r0')], draft=True)
        self.fake.release('v8', '2026-08-01T00:00:00Z', [('nanokvm', '8-r0')], sums=False)
        self.fake.list.append({'tag_name': 'old', 'draft': False, 'published_at': '2026-10-01T00:00:00Z', 'assets': []})
        self.assertEqual(self.run_assemble()[0], 'v3')

    def test_a_package_that_differs_from_its_checksum_fails(self):
        self.fake.files['https://dl.test/v3/nanokvm-3-r0.apk'] = b'tampered'
        with self.assertRaisesRegex(ValueError, 'SHA256SUMS'):
            self.run_assemble()

    def test_a_package_whose_size_differs_from_the_index_fails(self):
        data = b'nanokvm 3-r0 longer'
        self.fake.files['https://dl.test/v3/nanokvm-3-r0.apk'] = data
        sums = self.fake.files['https://dl.test/v3/SHA256SUMS'].decode().replace(
            hashlib.sha256(b'nanokvm 3-r0').hexdigest(), hashlib.sha256(data).hexdigest())
        self.fake.files['https://dl.test/v3/SHA256SUMS'] = sums.encode()
        with self.assertRaisesRegex(ValueError, 'its index says'):
            self.run_assemble()

    def test_a_package_no_release_carries_fails(self):
        self.fake.files['https://dl.test/v3/APKINDEX.tar.gz'] = apkindex([('nanokvm', '7-r0', 3)])
        sums = self.fake.files['https://dl.test/v3/SHA256SUMS'].decode().splitlines()
        sums = [l for l in sums if not l.endswith('APKINDEX.tar.gz')]
        sums.append(hashlib.sha256(self.fake.files['https://dl.test/v3/APKINDEX.tar.gz']).hexdigest() + '  APKINDEX.tar.gz')
        self.fake.files['https://dl.test/v3/SHA256SUMS'] = '\n'.join(sums).encode()
        with self.assertRaisesRegex(ValueError, 'No release carries nanokvm-7-r0.apk'):
            self.run_assemble(keep=1)

    def test_refuses_to_replace_another_directory(self):
        self.out.mkdir()
        (self.out / 'notes.txt').write_text('keep')
        with self.assertRaisesRegex(ValueError, 'not an assembled repository'):
            self.run_assemble()
        self.assertTrue((self.out / 'notes.txt').exists())
        (self.out / 'notes.txt').unlink()
        self.run_assemble()  # an empty directory
        self.run_assemble()  # an earlier result

    def test_nojekyll_does_not_authorize_deleting_an_unrelated_site(self):
        self.out.mkdir()
        (self.out / '.nojekyll').write_text('')
        (self.out / 'notes.txt').write_text('keep')
        with self.assertRaisesRegex(ValueError, 'not an assembled repository'):
            self.run_assemble()
        self.assertEqual((self.out / 'notes.txt').read_text(), 'keep')

    def test_landing_page_has_a_usable_repository_url_and_key_links(self):
        self.run_assemble()
        page = (self.out / 'index.html').read_text()
        self.assertIn('https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb', page)
        self.assertIn('href="keys/release.rsa.pub"', page)
        self.assertNotIn('href="keys/"', page)

    def test_no_release_fails(self):
        self.fake.list = []
        with self.assertRaisesRegex(ValueError, 'No published release'):
            self.run_assemble()

    def test_branch_comes_from_the_package_builder(self):
        self.assertRegex(a.alpine_branch(), r'^\d+\.\d+$')


if __name__ == '__main__':
    unittest.main()
