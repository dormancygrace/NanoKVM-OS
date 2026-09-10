#!/usr/bin/env python3
"""Host contract test of actual MMF functions with fake vendor endpoints."""
from pathlib import Path
import argparse
import hashlib
import json
import os
import resource
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--baseline-ref', help='Also require this old Git version to fail the contract')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
mpi = Path(os.environ['NANOKVM_MPI_SOURCE'])
sensor = Path(os.environ['NANOKVM_SENSOR_SOURCE'])
inih = Path(os.environ['NANOKVM_INIH_SOURCE'])
includes = [mpi/'include', mpi/'include/isp/cv181x', mpi/'sample/common',
            mpi/'component/panel/cv181x', sensor/'common', inih,
            repo/'support/sg2002/additional/kvm_mmf/include', repo/'firmware/mpi']
flags = ['-std=gnu++17', '-O1', '-ffunction-sections', '-fdata-sections',
         '-D__CV181X__', '-DOS_IS_LINUX', '-DNANOKVM_ENHANCED', '-DSENSOR_LONTIUM_LT6911']
flags += ['-DSENSOR'+str(i)+'_TYPE=LONTIUM_LT6911_2M_60FPS_8BIT' for i in range(3)]
flags += ['-I'+str(p) for p in includes]
out = args.output.resolve()
out.mkdir(parents=True, exist_ok=True)
fixture = repo/'firmware/probes/venc-pending-contract.cpp'
relative = 'support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp'

def run(name, test):
    binary = out/name
    built = subprocess.run([os.environ.get('CXX', 'g++'), *flags, str(test),
                            '-Wl,--gc-sections', '-o', str(binary)], capture_output=True, text=True)
    (out/(name+'-build.log')).write_text(built.stdout+built.stderr)
    if built.returncode:
        raise SystemExit(built.stderr[-4000:])
    def no_core():
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    tested = subprocess.run([str(binary)], capture_output=True, text=True,
                            timeout=10, preexec_fn=no_core)
    (out/(name+'-run.log')).write_text(tested.stdout+tested.stderr)
    return tested.returncode

result = {'scope': 'host fake-SDK contract; no hardware qualification',
          'source_sha256': hashlib.sha256((repo/relative).read_bytes()).hexdigest(),
          'fixture_sha256': hashlib.sha256(fixture.read_bytes()).hexdigest()}
result['current_exit'] = run('current', fixture)
if result['current_exit'] != 0:
    raise SystemExit((out/'current-run.log').read_text())
if args.baseline_ref:
    baseline = subprocess.check_output(['git', 'show', args.baseline_ref+':'+relative], cwd=repo)
    (out/'baseline-mmf.cpp').write_bytes(baseline)
    test = fixture.read_text().replace('../../'+relative, str(out/'baseline-mmf.cpp'))
    (out/'baseline-contract.cpp').write_text(test)
    result['baseline_ref'] = args.baseline_ref
    result['baseline_source_sha256'] = hashlib.sha256(baseline).hexdigest()
    result['baseline_exit'] = run('baseline', out/'baseline-contract.cpp')
    if result['baseline_exit'] == 0:
        raise SystemExit('Baseline unexpectedly passed; the regression test does not distinguish it')
(out/'result.json').write_text(json.dumps(result, indent=2)+'\n')
print(json.dumps(result, indent=2))
