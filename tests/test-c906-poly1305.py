#!/usr/bin/env python3
"""Build and execute the prepared-toolchain scalar Poly1305 on RISC-V QEMU."""
from pathlib import Path
import argparse
import os
import subprocess

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--base', required=True, type=Path)
parser.add_argument('--xcrypto', required=True, type=Path)
parser.add_argument('--output', required=True, type=Path)
parser.add_argument('--qemu', default='qemu-riscv64')
args = parser.parse_args()
output = args.output.resolve()
if output.exists():
    parser.error('output must be a new directory')
output.mkdir(parents=True)
goroot = output / 'goroot'
env = dict(os.environ, GOTOOLCHAIN='local', GOMAXPROCS='2')
subprocess.run(['python3', str(ROOT / 'firmware/cpu/sysmon-runtime/prepare.py'),
                '--base', str(args.base.resolve()), '--output', str(goroot)],
               env=env, check=True, stdout=subprocess.DEVNULL)
subprocess.run([str(goroot / 'bin/go'), 'vet',
                'vendor/golang.org/x/crypto/internal/poly1305'],
               env=dict(env, GOROOT=str(goroot), GOOS='linux', GOARCH='riscv64',
                        GORISCV64='rva20u64', CGO_ENABLED='0', GOFLAGS=''), check=True)
for variant in ['selected', 'reference']:
    target = output / variant
    cmd = ['python3', str(ROOT / 'firmware/cpu/go-crypto-c906/build-tests.py'),
           '--goroot', str(goroot), '--xcrypto', str(args.xcrypto.resolve()),
           '--output', str(target), '--package', 'internal/poly1305']
    if variant == 'reference':
        cmd.append('--poly1305-reference')
    subprocess.run(cmd, env=env, check=True)
    binary = target / 'poly1305.test'
    symbols = subprocess.check_output([str(goroot / 'bin/go'), 'tool', 'nm', '-size',
                                       str(binary)], env=env, text=True)
    entries = [line.split() for line in symbols.splitlines()]
    sizes = [int(parts[1]) for parts in entries if len(parts) == 4 and
             parts[3] in ('vendor/golang.org/x/crypto/internal/poly1305.update',
                          'vendor/golang.org/x/crypto/internal/poly1305.update.abi0')]
    assert 506 in sizes, (variant, sizes, 'rederive the pinned placement contract')
    # Disable standard RVV explicitly; no XTheadVector instructions are needed.
    subprocess.run([args.qemu, '-cpu', 'rv64,v=false', str(binary),
                    '-test.v', '-test.timeout=5m'], env=env, check=True)
    if variant == 'selected':
        result = subprocess.run([args.qemu, '-cpu', 'rv64,v=false', str(binary),
                                 '-test.run=^TestC906Reduction$', '-c906.negative'],
                                env=env, text=True, capture_output=True)
        assert result.returncode != 0 and 'tag/key mismatch' in result.stdout, result
print('PASS prepared RISC-V Poly1305: both paths, oracle, guards, negative control and layout')
