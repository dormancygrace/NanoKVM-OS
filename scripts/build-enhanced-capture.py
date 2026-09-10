#!/usr/bin/env python3
"""Build the complete NanoKVM capture library without MaixCDK or OpenCV.

Requires NANOKVM_BUILDROOT_OUTPUT, NANOKVM_MPI_SOURCE, NANOKVM_MMF_OUTPUT,
and NANOKVM_CAPTURE_OUTPUT. Does not initialize or install hardware services.
"""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
os.environ['PATH'] = '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin'
repo = Path(__file__).resolve().parent.parent
base = repo / 'support/sg2002/additional/kvm'
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
mmf = Path(os.environ['NANOKVM_MMF_OUTPUT']).resolve()
output = Path(os.environ['NANOKVM_CAPTURE_OUTPUT']).resolve()
cross = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve() / 'host/bin/riscv64-buildroot-linux-musl-')
if subprocess.check_output([cross+'gcc', '-dumpfullversion'], text=True).strip() != '16.2.0':
    raise SystemExit('Expected Enhanced GCC 16.2')
flags = ['-std=gnu++17', '-Os', '-Wall', '-Wextra', '-Werror', '-fPIC',
         '-ffunction-sections', '-fdata-sections', '-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector',
         '-mtune=thead-c906', '-mno-fence-tso', '-mabi=lp64d', '-D__CV181X__', '-DNANOKVM_ENHANCED']
flags += ['-I'+str(p) for p in [base/'include', repo/'support/sg2002/additional/kvm_mmf/include', mpi/'include']]
sources = sorted((base/'src').glob('*.cpp'))
if {p.name for p in sources} != {'kvm_vision.cpp', 'vi_state_shared.cpp', 'kvm_capture.cpp', 'kvm_i2c.cpp'}:
    raise SystemExit('Review changed capture source set')
output.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix='capture-', dir=output) as tmp:
    tmp = Path(tmp)
    objects = []
    for source in sources:
        obj = tmp/(source.stem+'.o')
        subprocess.run([cross+'g++', *flags, '-MD', '-MF', str(tmp/(source.stem+'.d')),
                        '-c', str(source), '-o', str(obj)], check=True)
        objects.append(str(obj))
    dependencies = '\n'.join(p.read_text() for p in tmp.glob('*.d'))
    if 'opencv' in dependencies.lower() or 'maixcdk' in dependencies.lower():
        raise SystemExit('Forbidden transitive capture dependency')
    subprocess.run([cross+'g++', '-shared', '-Wl,-z,defs', '-Wl,-soname,libkvm.so',
                    '-Wl,--as-needed', *objects, '-L'+str(mmf), '-lkvm_mmf',
                    '-Wl,-rpath-link,'+str(mpi/'lib'), '-Wl,-Map,'+str(tmp/'capture.map'),
                    '-pthread', '-o', str(tmp/'libkvm.so')], check=True)
    link_map = (tmp/'capture.map').read_text()
    if 'opencv' in link_map.lower() or 'maixcdk' in link_map.lower():
        raise SystemExit('Forbidden capture link input')
    shutil.copy2(tmp/'libkvm.so', output/'libkvm.so')
    shutil.copy2(tmp/'capture.map', output/'capture.map')
    (output/'dependencies.d').write_text(dependencies)
manifest = {'libkvm.so': hashlib.sha256((output/'libkvm.so').read_bytes()).hexdigest()}
(output/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
print('Capture linked without OpenCV/MaixCDK. Hardware video qualification remains pending.')