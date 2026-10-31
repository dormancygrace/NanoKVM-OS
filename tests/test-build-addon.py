#!/usr/bin/env python3
"""The add-on builder's recipes, payload and mkpkg call; no Go or apk."""
import importlib.util
import json
import os
import sys
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('build_addon', ROOT / 'scripts/build-addon.py')
b = importlib.util.module_from_spec(spec)
spec.loader.exec_module(b)


class RecipeTests(unittest.TestCase):
    def test_every_built_add_on_has_a_valid_recipe(self):
        recipes = ['nanokvm-' + d.name for d in b.ADDONS.iterdir() if (d / 'addon.json').is_file()]
        self.assertIn('nanokvm-ipmi', recipes)
        for name in recipes:
            r = b.recipe(name)
            self.assertEqual(r['name'], name)
            self.assertIn(name, b.addon_packages.ADDONS)

    def test_the_ipmi_add_on_has_its_own_version(self):
        r = b.recipe('nanokvm-ipmi')
        self.assertNotIn('PKGVER', json.dumps({k: v for k, v in r.items() if k != 'directory'}))
        self.assertEqual(r['go']['path'], '/usr/sbin/nkos-ipmi')
        self.assertEqual(set(r['scripts']), {'pre-upgrade', 'post-upgrade', 'pre-deinstall'})

    def test_the_ipmi_add_on_needs_nanokvm_os_3_5_0(self):
        # /api/internal/atx and the BMC settings arrived in 3.5.0.
        self.assertEqual(b.recipe('nanokvm-ipmi')['os'], '3.5.0')
        self.assertEqual(b.depends(b.recipe('nanokvm-ipmi')), 'openrc nanokvm-os>=3.5.0')
        self.assertEqual(b.depends({'depends': 'openrc', 'os': '3.6.0_beta2'}), 'openrc nanokvm-os>=3.6.0_beta2')

    def test_bad_recipes_are_refused(self):
        with self.assertRaises(ValueError):
            b.recipe('nanokvm-web')
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            good = json.loads((b.ADDONS / 'ipmi/addon.json').read_text())
            (d / 'ipmi').mkdir()
            for name in ['nanokvm-ipmi.initd', 'nanokvm-ipmi.pre-upgrade', 'nanokvm-ipmi.post-upgrade',
                         'nanokvm-ipmi.pre-deinstall']:
                (d / 'ipmi' / name).write_text('#!/bin/sh\n')
            for bad in [{'version': '3.4'}, {'name': 'nanokvm-web'},
                        {'files': {'/etc/../x': {'source': 'nanokvm-ipmi.initd', 'mode': '0755'}}},
                        {'scripts': {'trigger': 'nanokvm-ipmi.initd'}}, {'licenses': ['NO-SUCH-LICENSE']},
                        {'os': ''}, {'os': '3.5'}, {'os': 'v3.5.0'}, {'os': '3.5.0-beta.1'},
                        {'depends': 'openrc nanokvm-os>=3.4.0'}]:
                with self.subTest(bad=bad):
                    (d / 'ipmi/addon.json').write_text(json.dumps({**good, **bad}))
                    with self.assertRaises(ValueError):
                        b.recipe('nanokvm-ipmi', d)


class ModelRecipeTests(unittest.TestCase):
    MODELS = ['nanokvm-model-ocr', 'nanokvm-model-ocr-latin', 'nanokvm-model-ocr-cyrillic',
              'nanokvm-model-ocr-chinese', 'nanokvm-model-screen']

    def test_model_add_ons_need_the_tpu_abi(self):
        for name in self.MODELS:
            r = b.recipe(name)
            self.assertNotIn('go', r)
            self.assertIn('nanokvm-tpu-abi=1', b.depends(r).split(), name)
        # A language needs the detector.
        for lang in ('latin', 'cyrillic', 'chinese'):
            self.assertIn('nanokvm-model-ocr', b.recipe('nanokvm-model-ocr-' + lang)['depends'].split())

    def test_convert_parts(self):
        self.assertEqual(b.convert_parts(b.recipe('nanokvm-model-ocr')), ['det'])
        self.assertEqual(b.convert_parts(b.recipe('nanokvm-model-ocr-latin')), ['latin'])
        self.assertEqual(b.convert_parts(b.recipe('nanokvm-model-screen')), ['screen'])
        self.assertEqual(b.convert_parts(b.recipe('nanokvm-ipmi')), [])

    def test_every_model_is_in_one_add_on(self):
        # The files the server and the helpers read (server/service/vm).
        wanted = {'ocr-det.cvimodel', 'screen.cvimodel'} | {
            f'{lang}/{f}' for lang in ('latin', 'cyrillic', 'chinese') for f in ('ocr-rec.cvimodel', 'ocr-dict.txt')} | {
            'latin/ocr-sure.cvimodel', 'chinese/ocr-sure.cvimodel'}
        shipped = [m for name in self.MODELS for m in b.recipe(name)['models']]
        self.assertEqual(sorted(shipped), sorted(wanted))

    def test_bad_model_recipes_are_refused(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            good = json.loads((b.ADDONS / 'model-screen/addon.json').read_text())
            (d / 'model-screen').mkdir()
            for bad in [{'models': ['../x.cvimodel']}, {'models': ['/abs.cvimodel']}, {'models': []},
                        {'go': {'package': '.', 'path': '/usr/bin/x'}}]:
                (d / 'model-screen/addon.json').write_text(json.dumps({**good, **bad}))
                with self.assertRaises(ValueError, msg=bad):
                    b.recipe('nanokvm-model-screen', d)
            without = dict(good)
            del without['models']
            (d / 'model-screen/addon.json').write_text(json.dumps(without))
            with self.assertRaises(ValueError):
                b.recipe('nanokvm-model-screen', d)

    def test_model_payload(self):
        r = b.recipe('nanokvm-model-ocr-latin')
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            models = d / 'models'
            for path in r['models']:
                (models / path).parent.mkdir(parents=True, exist_ok=True)
                (models / path).write_bytes(path.encode())
            files = b.model_files(r, models)
            self.assertEqual(sorted(files), sorted(r['models']))
            root = d / 'payload'
            b.payload(r, None, root, {'models': files}, models)
            for path in r['models']:
                model = root / 'usr/lib/nanokvm-tpu/models' / path
                self.assertEqual(model.read_bytes(), path.encode())
                self.assertEqual(os.stat(model).st_mode & 0o777, 0o644)
            self.assertTrue((root / 'usr/share/licenses/nanokvm-model-ocr-latin/LICENSE.PaddleOCR').is_file())
            (models / r['models'][0]).unlink()
            with self.assertRaises(SystemExit):
                b.model_files(r, models)

    def test_converter_pins_are_recorded(self):
        self.assertEqual(set(b.converter()), {'image', 'tpu_mlir'})
        self.assertTrue(b.converter()['image'].startswith('sophgo/tpuc_dev:'))


class BuildTests(unittest.TestCase):
    def test_go_build_uses_the_addon_module(self):
        r = b.recipe('nanokvm-ipmi')
        with tempfile.TemporaryDirectory() as d:
            output = Path(d)
            with patch.object(b.subprocess, 'run') as run, patch.object(
                b.subprocess, 'check_output', return_value='go version test\n'
            ):
                binary, version = b.build_go(r, Path('/toolchain/bin/go'), output)
            args, kwargs = run.call_args
            self.assertEqual(kwargs['cwd'], ROOT / 'addons/ipmi')
            self.assertEqual(args[0][-1], './cmd/nkos-ipmi')
            self.assertEqual(kwargs['env']['GOARCH'], 'riscv64')
            self.assertEqual(kwargs['env']['CGO_ENABLED'], '0')
            self.assertTrue(kwargs['check'])
            self.assertEqual(binary, output / 'nkos-ipmi')
            self.assertEqual(version, 'go version test')


class PackageTests(unittest.TestCase):
    def test_payload_and_mkpkg(self):
        r = b.recipe('nanokvm-ipmi')
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            program = d / 'nkos-ipmi'
            program.write_bytes(b'\x7fELF')
            root = d / 'payload'
            b.payload(r, program, root, {'commit': 'abc'})
            self.assertEqual(os.stat(root / 'usr/sbin/nkos-ipmi').st_mode & 0o777, 0o755)
            self.assertEqual(os.stat(root / 'etc/init.d/nanokvm-ipmi').st_mode & 0o777, 0o755)
            self.assertTrue((root / 'usr/share/licenses/nanokvm-ipmi/LICENSE').is_file())
            self.assertEqual(json.loads((root / 'usr/share/nanokvm-ipmi/source.json').read_text()), {'commit': 'abc'})
            command = b.mkpkg(r, Path('/apk'), root, d / 'out.apk', '0.1.0-r2')
            self.assertEqual(command[:6], ['/apk', 'mkpkg', '--files', str(root), '--output', str(d / 'out.apk')])
            self.assertIn('version:0.1.0-r2', command)
            self.assertIn('name:nanokvm-ipmi', command)
            self.assertIn('depends:openrc nanokvm-os>=3.5.0', command)
            scripts = [command[i + 1] for i, v in enumerate(command) if v == '--script']
            self.assertEqual([s.split(':', 1)[0] for s in scripts], ['post-upgrade', 'pre-deinstall', 'pre-upgrade'])


if __name__ == '__main__':
    unittest.main()
