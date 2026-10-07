#!/usr/bin/env python3
"""Build linux/riscv64 test binaries for the std-vendored x/crypto packages.

The upstream x/crypto tests (from an extracted golang.org/x/crypto module) and
the tests in tests/ are added with a go build overlay, so neither GOROOT is
modified. Cross-check tests that need the riscv64 implementation hooks are
only added when the GOROOT contains them.
"""
from pathlib import Path
import argparse, json, os, subprocess, tempfile

PACKAGES = ['chacha20', 'chacha20poly1305', 'internal/poly1305']

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--goroot', type=Path, required=True)
parser.add_argument('--xcrypto', type=Path, required=True, help='extracted golang.org/x/crypto module with its tests')
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--gocache', type=Path)
args = parser.parse_args()
here = Path(__file__).resolve().parent
goroot = args.goroot.absolute()
args.output.mkdir(parents=True, exist_ok=True)
hooks = (goroot / 'src/vendor/golang.org/x/crypto/chacha20/chacha_riscv64.go').exists()
overlay = {}
for pkg in PACKAGES:
    target = goroot / 'src/vendor/golang.org/x/crypto' / pkg
    for f in sorted((args.xcrypto / pkg).glob('*_test.go')):
        overlay[str(target / f.name)] = str(f)
    for f in sorted((here / 'tests' / Path(pkg).name).glob('*_test.go')):
        if f.name.endswith('_bench_test.go') or hooks:
            overlay[str(target / f.name)] = str(f)
env = dict(os.environ, GOROOT=str(goroot), GOTOOLCHAIN='local', GOOS='linux', GOARCH='riscv64',
           GORISCV64='rva20u64', CGO_ENABLED='0', GOFLAGS='')
if args.gocache:
    env['GOCACHE'] = str(args.gocache)
with tempfile.NamedTemporaryFile('w', suffix='.json', delete=False) as f:
    json.dump({'Replace': overlay}, f)
try:
    for pkg in PACKAGES:
        out = args.output / (Path(pkg).name + '.test')
        subprocess.run([str(goroot / 'bin/go'), 'test', '-c', '-trimpath', '-overlay', f.name, '-o', str(out),
                        'vendor/golang.org/x/crypto/' + pkg], env=env, check=True)
        print(out)
finally:
    os.unlink(f.name)
