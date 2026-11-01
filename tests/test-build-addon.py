#!/usr/bin/env python3
"""The add-on builder's recipe validation and model payload; no Go or apk."""
import importlib.util
import json
import os
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('build_addon', ROOT / 'scripts/build-addon.py')
b = importlib.util.module_from_spec(spec)
spec.loader.exec_module(b)


class RecipeTests(unittest.TestCase):
    def test_depends_names_the_minimum_os(self):
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


if __name__ == '__main__':
    unittest.main()
