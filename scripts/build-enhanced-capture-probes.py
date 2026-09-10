#!/usr/bin/env python3
"""Package actual loader checks and a CPU-only MMF contract probe; no install.

Uses the same NANOKVM_* variables as build-enhanced-capture.py. The contract
probe substitutes the MMF provider, while capture-load loads the real libraries.
"""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import tarfile
import tempfile
os.environ['PATH'] = '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin'
repo = Path(__file__).resolve().parent.parent
base = repo/'support/sg2002/additional'
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
mmf = Path(os.environ['NANOKVM_MMF_OUTPUT']).resolve()
output = Path(os.environ['NANOKVM_CAPTURE_OUTPUT']).resolve()
cross = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve()/'host/bin/riscv64-buildroot-linux-musl-')
flags = ['-std=gnu++17', '-Os', '-Wall', '-Wextra', '-Werror', '-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector',
         '-mtune=thead-c906', '-mno-fence-tso', '-mabi=lp64d', '-D__CV181X__']
includes = ['-I'+str(p) for p in [base/'kvm/include', base/'kvm_mmf/include', mpi/'include']]
subprocess.run([cross+'g++', *flags, *includes, str(repo/'firmware/probes/capture-contract.cpp'),
                str(base/'kvm/src/kvm_capture.cpp'), '-o', str(output/'capture-contract')], check=True)
subprocess.run([cross+'g++', *flags, *includes, str(repo/'firmware/probes/capture-load.cpp'),
                '-L'+str(mmf), '-lkvm_mmf', '-Wl,-rpath-link,'+str(mpi/'lib'), '-ldl',
                '-o', str(output/'capture-load')], check=True)
subprocess.run([cross+'g++', *flags, *includes, str(repo/'firmware/probes/frame-buffer-contract.cpp'),
                '-o', str(output/'frame-buffer-contract')], check=True)
files = {name: output/name for name in ['libkvm.so', 'capture-contract', 'capture-load', 'frame-buffer-contract']}
files.update({name: mmf/name for name in ['libkvm_mmf.so', 'mmf-config-probe']})
for name in ['sys','vi','vpss','vo','rgn','gdc','venc','vdec','misc','cvi_ive','isp','isp_algo','ae','awb','af','cvi_bin','cvi_bin_isp']:
    files['lib'+name+'.so'] = mpi/'lib'/('lib'+name+'.so')
for name in ['libc.so', 'libstdc++.so.6', 'libgcc_s.so.1']:
    path = Path(subprocess.check_output([cross+'g++', '-print-file-name='+name], text=True).strip()).resolve()
    if not path.is_file(): raise SystemExit('Runtime not found: '+name)
    files[name] = path
manifest = {}
with tempfile.TemporaryDirectory(prefix='capture-runtime-', dir=output) as temporary:
    stage = Path(temporary)
    for name, source in files.items():
        code = subprocess.check_output([cross+'objdump', '-d', str(source)], text=True)
        if 'fence.tso' in code: raise SystemExit('Unsupported instruction in '+name)
        dynamic = subprocess.check_output([cross+'readelf', '-d', str(source)], text=True)
        if 'opencv' in dynamic.lower() or 'maix' in dynamic.lower():
            raise SystemExit('Unexpected runtime dependency in '+name)
        shutil.copy2(source, stage/name)
        manifest[name] = hashlib.sha256((stage/name).read_bytes()).hexdigest()
    (stage/'SHA256SUMS').write_text(''.join(digest+'  '+name+'\n' for name,digest in manifest.items()))
    with tarfile.open(output/'capture-runtime.tar', 'w') as archive:
        for source in sorted(stage.iterdir()): archive.add(source, arcname=source.name)
(output/'runtime-manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
(output/'runtime-sources.json').write_text(json.dumps({name:str(path) for name,path in files.items()}, indent=2)+'\n')
print(f'Packaged {len(manifest)} verified ELF artifacts, no fence.tso. Run with the packaged libc loader.')