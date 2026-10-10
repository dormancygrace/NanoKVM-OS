#!/usr/bin/env python3
"""Fault injection of real JPEG and capture code on the host; no hardware."""
import argparse
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--mpi', type=Path, required=True)
parser.add_argument('--sensor', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--sanitize', action='store_true', help='Enable address/undefined sanitizers')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
args.output.mkdir(parents=True, exist_ok=False)
# Match musl target char signedness. Glibc fortify is unrelated to these host
# lifetime tests; target production builds keep their normal compiler flags.
flags = ['g++', '-std=gnu++17', '-O1', '-g', '-funsigned-char',
         '-U_FORTIFY_SOURCE', '-D_FORTIFY_SOURCE=0', '-Wall', '-Wextra', '-Werror',
         '-ffunction-sections', '-fdata-sections', '-Wl,--gc-sections',
         '-D__CV181X__', '-DOS_IS_LINUX', '-DNANOKVM_ENHANCED', '-DSENSOR_LONTIUM_LT6911']
if args.sanitize:
    flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer']
flags += [f'-DSENSOR{i}_TYPE=LONTIUM_LT6911_2M_60FPS_8BIT' for i in range(3)]
includes = [args.sensor / 'common', args.mpi / 'include', args.mpi / 'include/isp/cv181x',
            args.mpi / 'sample/common', args.mpi / 'component/panel/cv181x',
            repo / 'support/sg2002/additional/kvm/include',
            repo / 'support/sg2002/additional/kvm_mmf/include', repo / 'firmware/mpi']
flags += ['-I' + str(path.resolve()) for path in includes]
for case in ['quality', 'sink', 'capture', 'pacing']:
    binary = args.output.resolve() / case
    sources = [repo / f'firmware/probes/mjpeg-{case}-contract.cpp']
    if case == 'pacing':
        sources = [repo / 'support/sg2002/additional/kvm/tests/capture_pacing_test.cpp']
    if case == 'capture':
        sources = [repo / 'firmware/probes/capture-contract.cpp',
                   repo / 'support/sg2002/additional/kvm/src/kvm_capture.cpp']
    subprocess.run(flags + [str(path) for path in sources] + ['-o', str(binary)], check=True)
    result = subprocess.run([str(binary)], capture_output=True, text=True)
    (args.output / (case + '.txt')).write_text(
        result.stdout + result.stderr + f'exit={result.returncode}\n')
    print(result.stdout + result.stderr, end='', flush=True)
    result.check_returncode()
