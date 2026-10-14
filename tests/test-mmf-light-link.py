#!/usr/bin/env python3
"""Host checks for the compact profile's ELF boundary, without camera hardware."""
from pathlib import Path
import subprocess
import tempfile
import unittest
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from nanokvm_mmf_link import audit_bundle, mmf_symbols, symbols  # noqa: E402


class LinkBoundaryTests(unittest.TestCase):
    def test_c_and_cpp_mmf_api_survive(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / 'api.cpp'
            source.write_text('extern "C" int mmf_c(int x) { return x; }\n'
                              'int mmf_cpp(bool x) { return x; }\n'
                              'int sample_private() { return 1; }\n')
            obj = root / 'api.o'
            subprocess.run(['g++', '-fPIC', '-c', str(source), '-o', str(obj)], check=True)
            exports = mmf_symbols('', obj)
            self.assertIn('mmf_c', exports)
            self.assertEqual(len(exports), 2)
            self.assertNotIn('sample_private', exports)
            self.assertTrue(exports <= set(symbols('', [obj])))

    def test_empty_mmf_api_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / 'private.cpp'
            source.write_text('int only_private() { return 0; }\n')
            obj = source.with_suffix('.o')
            subprocess.run(['g++', '-c', str(source), '-o', str(obj)], check=True)
            with self.assertRaisesRegex(ValueError, 'No MMF'):
                mmf_symbols('', obj)

    def test_unresolved_strong_import_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / 'broken.c'
            source.write_text('extern int missing_callback(void);\n'
                              'int call(void) { return missing_callback(); }\n')
            subprocess.run(['gcc', '-shared', '-fPIC', '-nostdlib', str(source),
                            '-o', str(root / 'libbroken.so')], check=True)
            with self.assertRaisesRegex(ValueError, 'missing_callback'):
                audit_bundle('', root)


if __name__ == '__main__':
    unittest.main()
