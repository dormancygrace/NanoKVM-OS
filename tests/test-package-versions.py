#!/usr/bin/env python3
"""Package version selection on fake payload and recipe trees."""
import hashlib
import importlib.util
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('package_versions', ROOT / 'scripts/package-versions.py')
pv = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pv)


class PreviousRepositoryTests(unittest.TestCase):
    def fixture(self, codes, filename='packages.json', fetch_apk=False):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        root = Path(tmp.name)
        curl = root / 'curl'
        curl.write_text("#!/usr/bin/env python3\nimport os,sys,pathlib\na=sys.argv[1:]\nu=a[-1]\n"
                        "with open(os.environ['CALLS'],'a') as f: f.write(u+'\\n')\n"
                        "key=('beta' if '/beta/' in u else 'stable')+('_apk' if u.endswith('.apk') else '')\n"
                        "code=os.environ.get(key,'404')\npathlib.Path(a[a.index('-o')+1]).write_text(code)\nprint(code,end='')\n")
        curl.chmod(0o755)
        build = (ROOT / 'platform/build.sh').read_text()
        function = build[build.index('previous_file() {'):build.index('# The version of each package')]
        script = function + '\nprevious_file "$FILE" "$OUTPUT" || exit $?\n'
        if fetch_apk:
            script += 'previous_file sample.apk "$OUTPUT" || exit $?\n'
        script += 'printf "%s" "$NANOKVM_PREVIOUS"\n'
        env = {**os.environ, **codes, 'PATH': str(root) + ':' + os.environ['PATH'],
               'NANOKVM_PREVIOUS': 'https://dormancygrace.github.io/NanoKVM-OS/beta/alpine/v3.24/riscv64',
               'FILE': filename, 'OUTPUT': str(root / 'output'), 'CALLS': str(root / 'calls')}
        result = subprocess.run(['bash', '-e', '-c', script], env=env, capture_output=True, text=True)
        return result, (root / 'calls').read_text().splitlines()

    def test_initial_beta_404_falls_back_and_reuses_same_feed(self):
        result, calls = self.fixture({'beta': '404', 'stable': '200', 'stable_apk': '200'}, fetch_apk=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 3)
        self.assertIn('/beta/', calls[0])
        self.assertNotIn('/beta/', calls[1])
        self.assertNotIn('/beta/', calls[2])
        self.assertNotIn('/beta/', result.stdout)

    def test_existing_beta_does_not_fall_back(self):
        result, calls = self.fixture({'beta': '200'})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 1)
        self.assertIn('/beta/', result.stdout)

    def test_server_failure_does_not_silently_rebuild_everything(self):
        result, calls = self.fixture({'beta': '503', 'stable': '200'})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('HTTP 503', result.stderr)
        self.assertEqual(len(calls), 1)

    def test_missing_apk_does_not_mix_feeds(self):
        result, calls = self.fixture({'beta_apk': '404', 'stable_apk': '200'}, filename='sample.apk')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 1)

    def test_no_prior_releases_is_still_a_first_build(self):
        result, calls = self.fixture({'beta': '404', 'stable': '404'})
        self.assertEqual(result.returncode, 1)
        self.assertEqual(len(calls), 2)


class PackageVersionTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.payloads, self.recipes = self.root / 'payloads', self.root / 'recipes'
        for name, payload in pv.PACKAGES.items():
            (self.recipes / name).mkdir(parents=True)
            (self.recipes / name / 'APKBUILD').write_text(f'pkgname={name}\n')
            if payload:
                (self.payloads / payload / 'usr').mkdir(parents=True)
                (self.payloads / payload / 'usr' / 'file').write_text(payload)

    def publish(self, version):
        """packages.json as a release built from the current trees would publish it."""
        chosen = pv.select(self.payloads, self.recipes, version, getattr(self, 'previous', None))
        packages = {n: {**p, 'apk': pv.apk_name(n, p['version']), 'apk_sha256': 'f' * 64} for n, p in chosen.items()}
        self.previous = {'release': version, 'packages': packages}
        return chosen

    def versions(self, chosen):
        return {n: p['version'] for n, p in chosen.items() if p['changed']}

    def test_release_requires_every_selected_component_version_directly(self):
        # The unchanged compatibility metapackage retains its old constraints;
        # the release must require the tested versions even with it installed.
        versions = {name: '3.3.0' for name in pv.PACKAGES}
        for name in ('nanokvm', 'nanokvm-web', 'nanokvm-exfatprogs', 'nanokvm-i2c-tools'):
            versions[name] = '3.4.0'
        env = {**os.environ, 'PKGVER': '3.4.0',
               **{'V_' + n.replace('-', '_'): v for n, v in versions.items()}}
        result = subprocess.run(
            ['sh', '-c', '. "$1"; printf "%s" "$depends"', 'sh',
             str(ROOT / 'firmware/alpine/packages/nanokvm-release/APKBUILD')],
            env=env, capture_output=True, text=True, check=True)
        deps = result.stdout.split()
        for name, version in versions.items():
            if name != 'nanokvm-release':
                with self.subTest(package=name):
                    self.assertIn(f'{name}>={version}-r0', deps)

    def test_stable_promotes_beta_packages_including_a_reverted_change(self):
        self.publish('3.3.4')
        web = self.payloads / 'web/usr/file'
        original = web.read_text()
        web.write_text('experimental change')
        self.publish('3.4.0_beta1')
        web.write_text(original)
        self.publish('3.4.0_beta2')
        chosen = self.publish('3.4.0')
        self.assertEqual(self.versions(chosen), {'nanokvm-web': '3.4.0', 'nanokvm-release': '3.4.0'})
        self.assertEqual(chosen['nanokvm']['version'], '3.3.4')
        self.assertTrue(all('_beta' not in p['version'] for p in chosen.values()))

    def test_first_release_changes_everything(self):
        chosen = self.publish('3.3.0')
        self.assertTrue(all(p['changed'] and p['version'] == '3.3.0' for p in chosen.values()))

    def test_unchanged_packages_keep_their_version_release_always_changes(self):
        self.publish('3.3.0')
        chosen = self.publish('3.4.0')
        self.assertEqual(self.versions(chosen), {'nanokvm-release': '3.4.0'})
        self.assertEqual(chosen['nanokvm-web']['version'], '3.3.0')

    def test_a_changed_payload_file_mode_link_or_recipe_changes_its_package(self):
        self.publish('3.3.0')
        (self.payloads / 'web/usr/file').write_text('fix')
        self.assertEqual(self.versions(self.publish('3.4.0')), {'nanokvm-web': '3.4.0', 'nanokvm-release': '3.4.0'})
        (self.payloads / 'base/usr/file').chmod(0o755)
        self.assertIn('nanokvm-base', self.versions(self.publish('3.5.0')))
        os.symlink('/somewhere', self.payloads / 'zlib/usr/link')
        self.assertIn('nanokvm-zlib', self.versions(self.publish('3.6.0')))
        (self.recipes / 'nanokvm-app/APKBUILD').write_text('pkgname=nanokvm-app\ndepends=x\n')
        self.assertEqual(self.versions(self.publish('3.7.0')), {'nanokvm-app': '3.7.0', 'nanokvm-release': '3.7.0'})

    def test_kernel_and_modules_change_together(self):
        self.publish('3.3.0')
        (self.payloads / 'kmod-sg2002/usr/file').write_text('module')
        self.assertEqual(self.versions(self.publish('3.4.0')),
                         {'nanokvm-kernel-sg2002': '3.4.0', 'nanokvm-kmod-sg2002': '3.4.0', 'nanokvm-release': '3.4.0'})

    def test_a_package_new_to_the_manifest_is_new(self):
        self.publish('3.3.0')
        del self.previous['packages']['nanokvm-bootloader-sg2002']
        self.assertIn('nanokvm-bootloader-sg2002', self.versions(self.publish('3.4.0')))

    def test_a_missing_payload_or_recipe_fails(self):
        (self.payloads / 'web/usr/file').unlink()
        (self.payloads / 'web/usr').rmdir()
        (self.payloads / 'web').rmdir()
        with self.assertRaisesRegex(ValueError, 'missing payload web'):
            self.publish('3.3.0')

    def test_reused_lists_the_published_files(self):
        self.publish('3.3.0')
        previous = self.root / 'previous.json'
        previous.write_text(json.dumps(self.previous))
        out = self.root / 'v'
        pv.main(['select', '--payloads', str(self.payloads), '--recipes', str(self.recipes), '--version', '3.4.0',
                 '--previous', str(previous), '--output', str(out)])
        text = (out / 'versions.txt').read_text()
        self.assertIn('nanokvm-web 3.3.0 0\n', text)
        self.assertIn('nanokvm-release 3.4.0 1\n', text)
        from contextlib import redirect_stdout
        import io
        printed = io.StringIO()
        with redirect_stdout(printed):
            pv.main(['reused', '--versions', str(out / 'versions.json'), '--previous', str(previous)])
        lines = printed.getvalue().splitlines()
        self.assertEqual(len(lines), 12)
        self.assertIn('nanokvm-web-3.3.0-r0.apk ' + 'f' * 64, lines)

    def test_manifest_records_each_file_and_its_hash(self):
        out = self.root / 'v'
        pv.main(['select', '--payloads', str(self.payloads), '--recipes', str(self.recipes), '--version', '3.3.0',
                 '--output', str(out)])
        repo = self.root / 'repo'
        repo.mkdir()
        for name in pv.PACKAGES:
            (repo / pv.apk_name(name, '3.3.0')).write_bytes(name.encode())
        pv.main(['manifest', '--versions', str(out / 'versions.json'), '--repo', str(repo)])
        manifest = json.loads((repo / 'packages.json').read_text())
        self.assertEqual(manifest['release'], '3.3.0')
        web = manifest['packages']['nanokvm-web']
        self.assertEqual(web['apk'], 'nanokvm-web-3.3.0-r0.apk')
        self.assertEqual(web['apk_sha256'], hashlib.sha256(b'nanokvm-web').hexdigest())
        (repo / 'nanokvm-web-3.3.0-r0.apk').unlink()
        with self.assertRaises(SystemExit):
            pv.main(['manifest', '--versions', str(out / 'versions.json'), '--repo', str(repo)])


class SigningTransitionTests(unittest.TestCase):
    def test_legacy_rsa_packages_are_rebuilt_once(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            payloads, recipes = root / 'payloads', root / 'recipes'
            previous = {'packages': {}}
            for name, payload in pv.PACKAGES.items():
                if payload:
                    (payloads / payload).mkdir(parents=True, exist_ok=True)
                recipe = recipes / name
                recipe.mkdir(parents=True)
                (recipe / 'APKBUILD').write_text('fixture')
                legacy = hashlib.sha256(('payload ' + pv.tree_digest(payload and payloads / payload)
                                         + ' recipe ' + pv.tree_digest(recipe)).encode()).hexdigest()
                previous['packages'][name] = {'version': '3.3.4', 'content_sha256': legacy}
            selected = pv.select(payloads, recipes, '3.4.0', previous)
            self.assertTrue(all(p['changed'] and p['version'] == '3.4.0' for p in selected.values()))
            again = pv.select(payloads, recipes, '3.4.1', {'packages': selected})
            self.assertEqual({n for n, p in again.items() if p['changed']}, pv.ALWAYS)


if __name__ == '__main__':
    unittest.main()
