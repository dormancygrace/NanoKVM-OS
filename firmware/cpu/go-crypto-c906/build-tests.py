#!/usr/bin/env python3
"""Build linux/riscv64 test binaries for the std-vendored x/crypto packages.

The upstream x/crypto tests (from an extracted golang.org/x/crypto module) and
the tests in tests/ are added with a go build overlay, so neither GOROOT is
modified. Cross-check tests that need the riscv64 implementation hooks are
only added when the GOROOT contains them. With the C906 Keccak permutation
in the GOROOT, the SHA-3 and ML-KEM test binaries are built as well.
"""
from pathlib import Path
import argparse, hashlib, json, os, subprocess, tempfile

PACKAGES = ['chacha20', 'chacha20poly1305', 'internal/poly1305']

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--goroot', type=Path, required=True)
parser.add_argument('--xcrypto', type=Path, required=True, help='extracted golang.org/x/crypto module with its tests')
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--gocache', type=Path)
parser.add_argument('--package', action='append', choices=PACKAGES,
                    help='Build only selected packages (repeatable; default: all)')
parser.add_argument('--poly1305-reference', action='store_true', help='compare the pinned upstream Poly1305 with existing fast unaligned loads')
args = parser.parse_args()
here = Path(__file__).resolve().parent
goroot = args.goroot.absolute()
args.output.mkdir(parents=True, exist_ok=True)
overlay = {}
for pkg in PACKAGES:
    target = goroot / 'src/vendor/golang.org/x/crypto' / pkg
    hooks = any(target.glob('*_riscv64.s'))  # the base GOROOT has none
    for f in sorted((args.xcrypto / pkg).glob('*_test.go')):
        overlay[str(target / f.name)] = str(f)
    for f in sorted((here / 'tests' / Path(pkg).name).glob('*_test.go')):
        if f.name.endswith('_bench_test.go') or hooks:
            overlay[str(target / f.name)] = str(f)
if args.poly1305_reference:
    if not (goroot / 'src/vendor/golang.org/x/crypto/internal/poly1305/sum_misaligned_riscv64.go').exists():
        parser.error('--poly1305-reference requires a prepared NanoKVM GOROOT')
    reference = (here / 'sum_riscv64.s').read_text()
    pin = json.loads((here.parent / 'sysmon-runtime/go-source-pin.json').read_text())
    if hashlib.sha256(reference.encode()).hexdigest() != pin['x_crypto_v0.57.0_sum_riscv64_s_sha256']:
        parser.error('Poly1305 reference differs from the pinned upstream source')
    anchor = '\tAND\t$7, X6, X28\n'
    assert reference.count(anchor) == 1
    reference = reference.replace(anchor, anchor +
        '\tMOVBU\t·useMisalignedLoads(SB), X29\n'
        '\tBEQZ\tX29, alignment_checked\n'
        '\tMOV\t$0, X28\n\n'
        'alignment_checked:\n')
    reference_path = args.output / 'poly1305-reference_riscv64.s'
    reference_path.write_text(reference)
    overlay[str(goroot / 'src/vendor/golang.org/x/crypto/internal/poly1305/sum_riscv64.s')] = str(reference_path.absolute())
STD = {}
sha3 = goroot / 'src/crypto/internal/fips140/sha3'
if (sha3 / 'keccak_c906_riscv64.s').exists():
    STD = {'crypto/internal/fips140/sha3': 'fips-sha3', 'crypto/sha3': 'sha3', 'crypto/mlkem': 'mlkem'}
    for f in sorted((here / 'tests/sha3').glob('*_test.go')):
        overlay[str(sha3 / f.name)] = str(f)
env = dict(os.environ, GOROOT=str(goroot), GOTOOLCHAIN='local', GOOS='linux', GOARCH='riscv64',
           GORISCV64='rva20u64', CGO_ENABLED='0', GOFLAGS='')
if args.gocache:
    env['GOCACHE'] = str(args.gocache)
with tempfile.NamedTemporaryFile('w', suffix='.json', delete=False) as f:
    json.dump({'Replace': overlay}, f)
try:
    for pkg in args.package or PACKAGES:
        out = args.output / (Path(pkg).name + '.test')
        subprocess.run([str(goroot / 'bin/go'), 'test', '-c', '-trimpath', '-overlay', f.name, '-o', str(out),
                        'vendor/golang.org/x/crypto/' + pkg], env=env, check=True)
        print(out)
    for pkg, name in STD.items():
        out = args.output / (name + '.test')
        subprocess.run([str(goroot / 'bin/go'), 'test', '-c', '-trimpath', '-overlay', f.name, '-o', str(out), pkg],
                       env=env, check=True)
        print(out)
finally:
    os.unlink(f.name)
