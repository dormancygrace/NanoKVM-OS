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
from unittest import mock
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


    def split_release(self, tag):
        """Model publication using separate public and technical asset URLs."""
        release = next(r for r in self.list if r['tag_name'] == tag)
        storage = {**release, 'tag_name': 'os-packages-' + tag, 'prerelease': True}
        technical, public = [], []
        for item in release['assets']:
            name = item['name']
            if name.endswith('.img.zip') or name.endswith('-source.tar.xz'):
                public.append(item)
            elif name != 'SHA256SUMS':
                url = f'https://dl.test/os-packages-{tag}/{name}'
                self.files[url] = self.files[item['browser_download_url']]
                technical.append({'name': name, 'browser_download_url': url})
        for group, prefix in [(public, tag), (technical, 'os-packages-' + tag)]:
            url = f'https://dl.test/{prefix}/SHA256SUMS'
            self.files[url] = ''.join(f'{hashlib.sha256(self.files[x["browser_download_url"]]).hexdigest()}  {x["name"]}\n'
                                      for x in group).encode()
            group.append({'name': 'SHA256SUMS', 'browser_download_url': url})
        release['assets'] = public
        release['body'] = f'Human edited notes\n<!-- nanokvm-package-storage: os-packages-{tag} -->'
        storage['assets'] = technical
        self.list.append(storage)
        return release, storage


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

    def test_beta_cannot_replace_stable_indexes_or_image(self):
        self.fake.release('v3.3.4', '2026-10-01', [('nanokvm', '3.3.4-r0')],
                          extra={'NanoKVM-OS-Image-v1.5-apps-v3.3.4.img.zip': b'image'})
        self.fake.release('v3.4.0-beta.10', '2026-10-03', [('nanokvm', '3.4.0_beta10-r0')],
                          extra={'NanoKVM-OS-v3.4.0-beta.10.img.zip': b'beta'})
        self.fake.list[-1]['prerelease'] = True
        self.fake.release('v3.4.0-beta.2', '2026-10-04', [('nanokvm', '3.4.0_beta2-r0')])
        # Even a forgotten GitHub prerelease flag must not leak a beta tag.
        stable = a.releases('o/r', self.fake.fetch)
        self.assertEqual(stable[0]['tag_name'], 'v3.3.4')
        both = a.releases('o/r', self.fake.fetch, channel='beta')
        self.assertEqual(both[0]['tag_name'], 'v3.4.0-beta.10')
        a.assemble(self.out, both, 2, '3.24', self.keys, self.fake.fetch)
        a.assemble(self.out / 'beta', both, 2, '3.24', self.keys, self.fake.fetch, channel='beta')
        self.assertEqual((self.repo() / 'Packages.adb').read_bytes(), b'adb v3.3.4')
        self.assertEqual((self.out / 'beta/alpine/v3.24/riscv64/Packages.adb').read_bytes(), b'adb v3.4.0-beta.10')
        self.assertEqual(json.loads((self.out / 'latest-image/badge.json').read_text())['message'], 'v1.5')
        self.assertEqual(json.loads((self.out / 'beta/latest-image/badge.json').read_text())['message'], 'v3.4.0-beta.10')
        self.fake.release('v3.4.0', '2026-10-05', [('nanokvm', '3.4.0-r0')])
        self.assertEqual(a.releases('o/r', self.fake.fetch, channel='beta')[0]['tag_name'], 'v3.4.0')

    def split_fixture(self, tag='v4.0.0', version='4.0.0-r0'):
        self.fake.release(tag, '2026-10-22', [('nanokvm', version), ('nanokvm-web', '1-r0')], extra={
            'packages.json': json.dumps({'version': version}).encode(),
            'addons.json': b'{"packages": {}}',
            f'NanoKVM-OS-{tag}-source.tar.xz': b'sources',
            f'NanoKVM-OS-{tag}.img.zip': b'image',
        })
        return self.fake.split_release(tag)

    def test_storage_without_os_add_on_snapshot(self):
        _, storage = self.split_fixture()
        storage['assets'] = [x for x in storage['assets'] if x['name'] != 'addons.json']
        self.assertEqual(self.run_assemble(keep=1)[0], 'v4.0.0')
        self.assertTrue((self.repo() / 'nanokvm-4.0.0-r0.apk').is_file())
        self.assertEqual(json.loads((self.repo() / 'addons.json').read_text()), {'packages': {}})

    def test_split_storage_mixed_with_legacy_releases_and_reused_packages(self):
        public, storage = self.split_fixture()
        # Reuse an unchanged package from a legacy release outside the kept
        # index window; do not upload that APK in the new technical release.
        storage['assets'] = [x for x in storage['assets'] if x['name'] != 'nanokvm-web-1-r0.apk']
        tag, names = self.run_assemble(keep=1)
        self.assertEqual(tag, 'v4.0.0')
        self.assertIn('nanokvm-web-1-r0.apk', names)
        self.assertEqual((self.repo() / 'nanokvm-web-1-r0.apk').read_bytes(), b'nanokvm-web 1-r0')
        self.assertEqual(json.loads((self.repo() / 'packages.json').read_bytes())['version'], '4.0.0-r0')
        html = (self.out / 'index.html').read_text()
        self.assertIn('https://dl.test/v4.0.0/SHA256SUMS', html)
        self.assertNotIn('os-packages-', html)
        self.assertIn('/tag/v4.0.0', (self.out / 'latest-image/index.html').read_text())

    def test_reuse_from_older_technical_storage_outside_index_window(self):
        self.split_fixture('v4.0.0', '4.0.0-r0')
        _, storage = self.split_fixture('v4.0.1', '4.0.1-r0')
        storage['assets'] = [x for x in storage['assets'] if x['name'] != 'nanokvm-web-1-r0.apk']
        # Only the older technical release can supply this unchanged APK.
        for r in self.fake.list:
            if r['tag_name'] in ('v1', 'v2', 'v3'):
                r['assets'] = [x for x in r['assets'] if x['name'] != 'nanokvm-web-1-r0.apk']
        self.assertEqual(self.run_assemble(keep=1)[0], 'v4.0.1')
        self.assertTrue((self.repo() / 'nanokvm-web-1-r0.apk').is_file())

    def test_public_and_storage_on_different_api_pages(self):
        public, storage = self.split_fixture()
        padding = [dict(tag_name=f'ignored-{i}', draft=True, assets=[]) for i in range(99)]
        def fetch(url, accept=None):
            if url.startswith('https://api.github.com/'):
                page = int(url.rsplit('page=', 1)[1])
                return json.dumps([public, *padding] if page == 1 else [storage]).encode()
            return self.fake.fetch(url, accept)
        published = a.releases('o/r', fetch)
        self.assertEqual([r['tag_name'] for r in published], ['v4.0.0'])
        a.assemble(self.out, published, 1, '3.24', self.keys, fetch)
        self.assertTrue((self.repo() / 'nanokvm-4.0.0-r0.apk').is_file())

    def test_orphan_storage_and_public_drafts_never_advance_channels(self):
        public, storage = self.split_fixture()
        public['draft'] = True
        self.assertEqual(a.releases('o/r', self.fake.fetch, channel='beta')[0]['tag_name'], 'v3')
        self.fake.list.remove(public)
        self.assertEqual(a.releases('o/r', self.fake.fetch, channel='beta')[0]['tag_name'], 'v3')

    def test_missing_or_incomplete_storage_fails_instead_of_silently_publishing_old_feed(self):
        public, storage = self.split_fixture()
        for failure in ('draft', 'index', 'missing'):
            with self.subTest(failure=failure):
                original = list(storage['assets'])
                if failure == 'draft':
                    storage['draft'] = True
                elif failure == 'index':
                    storage['assets'] = [x for x in original if x['name'] != 'Packages.adb']
                else:
                    self.fake.list.remove(storage)
                with self.assertRaisesRegex(ValueError, 'Missing or incomplete package storage'):
                    a.releases('o/r', self.fake.fetch)
                storage['draft'] = False
                storage['assets'] = original

    def test_storage_association_cannot_point_to_another_release(self):
        public, _ = self.split_fixture()
        public['body'] = '<!-- nanokvm-package-storage: os-packages-v3.0.0 -->'
        with self.assertRaisesRegex(ValueError, 'Invalid package storage association'):
            a.releases('o/r', self.fake.fetch)

    def test_deterministic_pair_survives_manually_edited_notes(self):
        public, _ = self.split_fixture()
        public['body'] = 'Replaced notes without the hidden marker'
        self.assertEqual(self.run_assemble()[0], 'v4.0.0')

    def test_storage_checksum_failure_stops_assembly(self):
        self.split_fixture()
        self.fake.files['https://dl.test/os-packages-v4.0.0/nanokvm-4.0.0-r0.apk'] = b'corrupt'
        with self.assertRaisesRegex(ValueError, 'SHA256SUMS'):
            self.run_assemble()

    def test_split_stable_beta_and_downgrade_packages_remain_separate(self):
        self.split_fixture('v4.0.0', '4.0.0-r0')
        public, _ = self.split_fixture('v4.1.0-beta.1', '4.1.0_beta1-r0')
        public['prerelease'] = True
        published = a.releases('o/r', self.fake.fetch, channel='beta')
        self.assertEqual(len(published), 5)  # two OS releases + three legacy, no storage releases
        a.assemble(self.out, published, 1, '3.24', self.keys, self.fake.fetch)
        a.assemble(self.out / 'beta', published, 1, '3.24', self.keys, self.fake.fetch, channel='beta')
        self.assertEqual((self.repo() / 'Packages.adb').read_bytes(), b'adb v4.0.0')
        beta = self.out / 'beta/alpine/v3.24/riscv64'
        self.assertEqual((beta / 'Packages.adb').read_bytes(), b'adb v4.1.0-beta.1')
        # The downgrade target still resolves every stable APK, regardless of
        # the newest beta package and storage prerelease flags.
        self.assertTrue((self.repo() / 'nanokvm-4.0.0-r0.apk').is_file())
        self.assertFalse((self.repo() / 'nanokvm-4.1.0_beta1-r0.apk').exists())
        self.assertTrue((beta / 'nanokvm-4.1.0_beta1-r0.apk').is_file())

    def test_empty_beta_channel_uses_stable_release(self):
        stable = a.releases('o/r', self.fake.fetch)
        beta = a.releases('o/r', self.fake.fetch, channel='beta')
        self.assertEqual(stable, beta)

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
        url = 'https://github.com/dormancygrace/NanoKVM-OS/releases/download/nanokvm-rustdesk-0.5.3-r5/' + name
        item = {'url': url, 'sha256': hashlib.sha256(data).hexdigest(), 'size': len(data)}
        self.fake.files[url] = data
        self.fake.release('v4', '2026-04-01T00:00:00Z', [('nanokvm', '4-r0')], extra={
            'addons.json': json.dumps({'packages': {name: item}}).encode(),
            'APKINDEX.tar.gz': apkindex([('nanokvm', '4-r0', len('nanokvm 4-r0'))])})
        return name, item, data

    def independent_release(self, version='0.6.0', revision=0, name='nanokvm-rustdesk', **flags):
        tag = f'{name}-{version}-r{revision}'
        filename = tag + '.apk'
        data = tag.encode()
        url = f'https://github.com/dormancygrace/NanoKVM-OS/releases/download/{tag}/{filename}'
        sums_url = url.rsplit('/', 1)[0] + '/SHA256SUMS'
        self.fake.files[url] = data
        self.fake.files[sums_url] = f'{hashlib.sha256(data).hexdigest()}  {filename}\n'.encode()
        self.fake.list.append({'tag_name': tag, 'draft': False, 'prerelease': False,
                              'published_at': '2026-09-01T00:00:00Z', **flags,
                              'assets': [{'name': filename, 'browser_download_url': url, 'size': len(data)},
                                         {'name': 'SHA256SUMS', 'browser_download_url': sums_url}]})
        return filename, url

    def addon_repo(self):
        return self.out / 'addons/v3.24/riscv64'

    def test_addons_are_a_repository_of_their_own(self):
        old, _, _ = self.external_addon()
        new, _ = self.independent_release()
        entries = a.independent_addons('dormancygrace/NanoKVM-OS', fetch=self.fake.fetch)
        tag, files = a.assemble(self.out, a.releases('o/r', self.fake.fetch), 1, '3.24', self.keys, self.fake.fetch)
        self.assertEqual(tag, 'v4')
        # The OS repository: the OS packages and indexes, no add-on, no pin;
        # an OS release's own add-on pins no longer matter.
        self.assertNotIn(old, files)
        self.assertNotIn(new, files)
        self.assertFalse(list(self.repo().glob('nanokvm-rustdesk-*')))
        self.assertEqual(json.loads((self.repo() / 'addons.json').read_text()), {'packages': {}})
        self.assertEqual((self.repo() / 'APKINDEX.tar.gz').read_bytes(), self.fake.files['https://dl.test/v4/APKINDEX.tar.gz'])
        # The add-on repository: the independent releases, with their pins.
        self.assertEqual(a.assemble_addons(self.out, '3.24', entries, self.fake.fetch), [new])
        self.assertEqual((self.addon_repo() / new).read_bytes(), new.removesuffix('.apk').encode())
        self.assertEqual(list(json.loads((self.addon_repo() / 'addons.json').read_text())['packages']), [new])
        self.assertFalse((self.addon_repo() / old).exists())
        self.assertIn('https://dormancygrace.github.io/NanoKVM-OS/addons/v3.24/riscv64/Packages.adb',
                      (self.out / 'index.html').read_text())

    def test_addon_repository_after_the_os_repository_only(self):
        self.run_assemble()
        a.assemble_addons(self.out, '3.24', {}, self.fake.fetch)
        with self.assertRaisesRegex(ValueError, 'assemble the OS repository first'):
            a.assemble_addons(self.out, '3.24', {}, self.fake.fetch)
        # The next run replaces the whole site, add-ons included.
        self.run_assemble()
        self.assertFalse((self.out / 'addons').exists())

    def test_addon_versions_are_numeric_and_draft_prerelease_ignored(self):
        self.independent_release('0.9.0', revision=12)
        wanted, _ = self.independent_release('0.10.0', revision=2)
        self.independent_release('0.10.0', revision=3, draft=True)
        self.independent_release('1.0.0', prerelease=True)
        self.assertEqual(list(a.independent_addons('dormancygrace/NanoKVM-OS', 1, self.fake.fetch)), [wanted])

    def test_every_registered_addon_is_released_independently(self):
        rustdesk, _ = self.independent_release('0.6.0')
        ipmi, _ = self.independent_release('0.1.0', name='nanokvm-ipmi')
        self.independent_release('0.1.0', name='nanokvm-unregistered')
        entries = a.independent_addons('dormancygrace/NanoKVM-OS', fetch=self.fake.fetch)
        self.assertEqual(sorted(entries), [ipmi, rustdesk])

    def test_independent_addon_corruption_stops_assembly(self):
        name, url = self.independent_release()
        entries = a.independent_addons('dormancygrace/NanoKVM-OS', fetch=self.fake.fetch)
        self.fake.files[url] += b'tampered'
        self.run_assemble()
        with self.assertRaisesRegex(ValueError, 'pinned size or SHA256'):
            a.assemble_addons(self.out, '3.24', entries, self.fake.fetch)

    def test_independent_addon_url_cannot_escape_repository(self):
        self.independent_release()
        self.fake.list[-1]['assets'][0]['browser_download_url'] = 'https://evil.invalid/x.apk'
        with self.assertRaisesRegex(ValueError, 'asset URL'):
            a.independent_addons('dormancygrace/NanoKVM-OS', fetch=self.fake.fetch)

    def test_repository_verification_only_relaxes_exact_pinned_addons(self):
        name, _ = self.independent_release()
        entries = a.independent_addons('dormancygrace/NanoKVM-OS', fetch=self.fake.fetch)
        self.run_assemble()
        a.assemble_addons(self.out, '3.24', entries, self.fake.fetch)
        with mock.patch.object(a.addon_packages.subprocess, 'run') as run:
            a.addon_packages.verify_repository(self.addon_repo(), 'apk', self.keys)
        calls = [c.args[0] for c in run.call_args_list]
        self.assertEqual(len(calls), 1)
        self.assertIn('--allow-untrusted', calls[0])
        self.assertTrue(calls[0][-1].endswith(name))
        # A modified add-on cannot reach the relaxed apk invocation.
        (self.addon_repo() / name).write_bytes(b'tamper')
        with mock.patch.object(a.addon_packages.subprocess, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'pinned size or SHA256'):
                a.addon_packages.verify_repository(self.addon_repo())
            run.assert_not_called()

    def test_repository_verification_rejects_unsigned_unpinned_package(self):
        self.external_addon()
        self.run_assemble(keep=1)
        unpinned = self.repo() / 'unexpected-1-r0.apk'
        unpinned.write_bytes(b'unsigned')
        def verify(command, **kwargs):
            if command[-1] == str(unpinned):
                self.assertNotIn('--allow-untrusted', command)
                raise a.addon_packages.subprocess.CalledProcessError(1, command)
        with mock.patch.object(a.addon_packages.subprocess, 'run', side_effect=verify):
            with self.assertRaises(a.addon_packages.subprocess.CalledProcessError):
                a.addon_packages.verify_repository(self.repo())

    def test_optional_addon_index_size_must_match(self):
        name, item, _ = self.external_addon()
        item['size'] += 1
        with self.assertRaisesRegex(ValueError, 'size differs from its index'):
            a.addon_packages.checked(name, item, self.fake.fetch, size=item['size'] - 1)

    def test_optional_addon_manifest_rejects_unsafe_names_and_sources(self):
        name, item, _ = self.external_addon()
        main = 'https://github.com/dormancygrace/NanoKVM-OS/releases/download/nanokvm-rustdesk-0.5.3-r6/' + name
        self.assertIn(name, a.addon_packages.manifest(json.dumps({'packages': {name: {**item, 'url': main}}})))
        other_release = 'https://github.com/dormancygrace/NanoKVM-OS/releases/download/v3.3.0/' + name
        old_repository = 'https://github.com/dormancygrace/NanoKVM-OS-packages/releases/download/r5/' + name
        for bad_name, bad_item in [('../evil.apk', item), (name, {**item, 'url': 'http://evil/evil.apk'}),
                                   (name, {**item, 'url': other_release}),
                                   (name, {**item, 'url': old_repository}),
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

    def test_no_release_fails(self):
        self.fake.list = []
        with self.assertRaisesRegex(ValueError, 'No published release'):
            self.run_assemble()


class Fetch(unittest.TestCase):
    """The files of a just-published release may answer 404 for a while."""

    def responses(self, *answers):
        def urlopen(request, timeout):
            answer = next(answers_left)
            if isinstance(answer, int):
                raise a.urllib.error.HTTPError(request.full_url, answer, 'status', {}, None)
            return io.BytesIO(answer)
        answers_left = iter(answers)
        return mock.patch.object(a.urllib.request, 'urlopen', urlopen)

    def test_a_missing_file_is_retried_until_it_appears(self):
        sleeps = []
        with self.responses(404, 404, b'data'), mock.patch('sys.stderr', io.StringIO()):
            self.assertEqual(a.fetch('https://example.invalid/x', sleep=sleeps.append), b'data')
        self.assertEqual(sleeps, list(a.RETRY_DELAYS[:2]))

    def test_a_file_that_stays_missing_fails_after_the_last_retry(self):
        sleeps = []
        with self.responses(*[404] * (len(a.RETRY_DELAYS) + 1)), mock.patch('sys.stderr', io.StringIO()):
            with self.assertRaises(a.urllib.error.HTTPError):
                a.fetch('https://example.invalid/x', sleep=sleeps.append)
        self.assertEqual(sleeps, list(a.RETRY_DELAYS))

    def test_other_errors_fail_at_once(self):
        sleeps = []
        with self.responses(403):
            with self.assertRaises(a.urllib.error.HTTPError):
                a.fetch('https://example.invalid/x', sleep=sleeps.append)
        self.assertEqual(sleeps, [])


if __name__ == '__main__':
    unittest.main()
