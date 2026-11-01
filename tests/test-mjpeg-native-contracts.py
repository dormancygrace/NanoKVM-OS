#!/usr/bin/env python3
"""Build and run the pure-logic native tests of the MJPEG and capture code on the host."""
import argparse
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--sanitize', action='store_true', help='Enable address/undefined sanitizers')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
args.output.mkdir(parents=True, exist_ok=False)
# Match musl target char signedness. Glibc fortify is unrelated to these host
# tests; target production builds keep their normal compiler flags.
flags = ['g++', '-std=gnu++17', '-pthread', '-O1', '-g', '-funsigned-char',
         '-U_FORTIFY_SOURCE', '-D_FORTIFY_SOURCE=0', '-Wall', '-Wextra', '-Werror']
if args.sanitize:
    flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer']
flags += ['-I' + str(repo / path) for path in
          ('support/additional/kvm/include', 'support/additional/kvm_mmf/include')]

tests = repo / 'support/additional/kvm/tests'
for case, source in (('policy', 'mjpeg_policy_test.cpp'), ('gate', 'geometry_gate_test.cpp'),
                     ('pacing', 'capture_pacing_test.cpp'), ('park', 'capture_park_test.cpp')):
    binary = args.output.resolve() / case
    subprocess.run(flags + [str(tests / source), '-o', str(binary)], check=True)
    result = subprocess.run([str(binary)], capture_output=True, text=True)
    (args.output / (case + '.txt')).write_text(
        result.stdout + result.stderr + f'exit={result.returncode}\n')
    print(result.stdout + result.stderr, end='', flush=True)
    result.check_returncode()
