#!/usr/bin/env python3
"""Host test of real MMF VI ownership with vendor stubs; never opens hardware."""
import argparse
from pathlib import Path
import resource
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--mpi', type=Path, required=True)
p.add_argument('--sensor', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
p.add_argument('--baseline', help='Optional old Git revision expected to fail the lease ownership assertion')
a = p.parse_args()
r = Path(__file__).resolve().parents[1]
a.output.mkdir(parents=True, exist_ok=False)
flags = ['g++', '-std=gnu++17', '-O1', '-g', '-Wall', '-Wextra', '-Werror',
         '-ffunction-sections', '-fdata-sections', '-Wl,--gc-sections',
         '-D__CV181X__', '-DOS_IS_LINUX', '-DNANOKVM_ENHANCED', '-DSENSOR_LONTIUM_LT6911']
flags += ['-DSENSOR'+str(i)+'_TYPE=LONTIUM_LT6911_2M_60FPS_8BIT' for i in range(3)]
flags += ['-I'+str(x.resolve()) for x in [a.sensor/'common', a.mpi/'include',
          a.mpi/'include/isp/cv181x', a.mpi/'sample/common', a.mpi/'component/panel/cv181x',
          r/'support/sg2002/additional/kvm_mmf/include', r/'firmware/mpi']]
cases = []
if a.baseline:
    old = subprocess.check_output(['git', 'show', a.baseline+':support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp'], cwd=r)
    old_path = a.output.resolve()/'before.cpp'
    old_path.write_bytes(old)
    cases.append(('before', ['-DEXPECT_OLD', '-DNANOKVM_MMF_SOURCE="'+str(old_path)+'"']))
cases.append(('after', []))
resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
for name, extra in cases:
    binary = a.output.resolve()/name
    subprocess.run(flags+extra+[str(r/'firmware/probes/mmf-vi-ownership-contract.cpp'), '-o', str(binary)], check=True)
    run = subprocess.run([str(binary)], capture_output=True, text=True)
    result = run.stdout+run.stderr+'\nexit='+str(run.returncode)+'\n'
    (a.output/(name+'.txt')).write_text(result)
    print(result)
    if name == 'before':
        if run.returncode == 0 or 'priv.vi_frame_valid[0]' not in run.stderr:
            raise SystemExit('Baseline did not reproduce the expected ownership failure')
    elif run.returncode != 0:
        raise SystemExit('MMF VI contract failed')
