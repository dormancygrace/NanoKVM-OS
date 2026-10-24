#!/usr/bin/env python3
"""Real cold/warm/bypassed compilation, including invalidation; no network.

With --toolchain HOST, exercise the Buildroot SDK wrapper and its own ccache.
Without it use the host compiler through system ccache for fast CI coverage.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--toolchain', type=Path)
args, remaining = parser.parse_known_args()


class CompilerCacheTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='nkos-ccache-')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.config = self.root / 'ccache.conf'
        self.config.write_text('')
        self.base = {k: v for k, v in os.environ.items()
                     if not k.startswith(('CCACHE_', 'BR_CACHE_', 'NANOKVM_CCACHE', 'BR2_USE_CCACHE'))}
        self.base['CCACHE_CONFIGPATH'] = str(self.config)
        self.env = self.environment()
        if args.toolchain:
            self.cache = str(args.toolchain.resolve() / 'bin/ccache')
            self.compiler = [str(args.toolchain.resolve() / 'bin/riscv64-buildroot-linux-musl-gcc')]
        else:
            self.cache = shutil.which('ccache')
            self.compiler = [self.cache, shutil.which('gcc')]
        self.assertTrue(self.cache and Path(self.cache).is_file(), 'ccache is required')
        self.assertTrue(self.compiler[-1] and Path(self.compiler[-1]).is_file(), 'compiler is required')
        self.source = self.root / 'probe.c'
        self.header = self.root / 'value.h'
        self.output = self.root / 'probe.o'
        self.source.write_text('#include "value.h"\nint probe(int n) { return n * VALUE + 7; }\n')
        self.header.write_text('#define VALUE 3\n')

    def environment(self, **values):
        result = subprocess.run(['bash', '-euc',
            'source "$1"; configure_compiler_cache "$2"; '
            'python3 -c "import os,json; print(json.dumps(dict(os.environ)))"',
            'sh', str(ROOT / 'platform/compiler-cache.sh'), str(self.root)],
            env={**self.base, **values}, capture_output=True, text=True, check=True)
        return json.loads(result.stdout)

    def stats(self):
        output = subprocess.check_output([self.cache, '--print-stats'], env=self.env, text=True)
        return {k: int(v) for k, v in (line.split() for line in output.splitlines())}

    def compile(self, env=None, optimization='-O2'):
        self.output.unlink(missing_ok=True)
        subprocess.run([*self.compiler, optimization, '-g0', '-c', str(self.source), '-o', str(self.output)],
                       env=env or self.env, check=True, capture_output=True)
        return hashlib.sha256(self.output.read_bytes()).hexdigest()

    def test_repeat_hits_and_matches_bypassed_compilation(self):
        cold = self.compile()
        before = self.stats()
        self.assertGreater(before['cache_miss'], 0, 'SDK did not use ccache')
        self.assertEqual(self.compile(), cold)
        warm = self.stats()
        self.assertGreater(warm['direct_cache_hit'] + warm['preprocessed_cache_hit'],
                           before['direct_cache_hit'] + before['preprocessed_cache_hit'])
        self.assertEqual(self.compile(self.environment(NANOKVM_CCACHE='0')), cold)
        self.assertEqual(self.stats()['cache_miss'], warm['cache_miss'])
        self.assertEqual(self.stats()['direct_cache_hit'], warm['direct_cache_hit'])

    def test_header_and_flags_invalidate_cached_objects(self):
        original = self.compile()
        misses = self.stats()['cache_miss']
        self.header.write_text('#define VALUE 19\n')
        changed = self.compile()
        self.assertNotEqual(original, changed)
        self.assertGreater(self.stats()['cache_miss'], misses)
        self.assertEqual(changed, self.compile(self.environment(NANOKVM_CCACHE='0')))
        misses = self.stats()['cache_miss']
        unoptimized = self.compile(optimization='-O0')
        self.assertGreater(self.stats()['cache_miss'], misses)
        self.assertEqual(unoptimized, self.compile(self.environment(NANOKVM_CCACHE='0'), '-O0'))

    def test_namespace_change_cannot_reuse_an_old_toolchain_entry(self):
        self.env = self.environment(CCACHE_NAMESPACE='sdk-a')
        first = self.compile()
        misses = self.stats()['cache_miss']
        self.env = self.environment(CCACHE_NAMESPACE='sdk-b')
        self.assertEqual(self.compile(), first)
        self.assertGreater(self.stats()['cache_miss'], misses)

    def test_cache_directory_limit_and_disable(self):
        self.assertEqual(self.env['BR2_USE_CCACHE'], '1')
        self.assertEqual(self.env['BR_CACHE_DIR'], str(self.root / 'ccache'))
        self.assertEqual(self.env['CCACHE_DIR'], self.env['BR_CACHE_DIR'])
        self.assertEqual(self.env['CCACHE_MAXSIZE'], '5G')
        custom = self.environment(NANOKVM_CCACHE_DIR=str(self.root / 'shared'), NANOKVM_CCACHE_MAXSIZE='8G')
        self.assertEqual(custom['BR_CACHE_DIR'], str(self.root / 'shared'))
        self.assertEqual(custom['CCACHE_MAXSIZE'], '8G')
        disabled = self.environment(NANOKVM_CCACHE='0')
        self.assertEqual(disabled['BR2_USE_CCACHE'], '0')
        self.assertEqual(disabled['CCACHE_DISABLE'], '1')
        with self.assertRaises(subprocess.CalledProcessError):
            self.environment(NANOKVM_CCACHE='yes')


if __name__ == '__main__':
    unittest.main(argv=[__file__, *remaining])
