#!/usr/bin/env python3
"""Build firmware/probes/venc-copy-bench against a built libkvm; no install.

Requires NANOKVM_BUILDROOT_OUTPUT, NANOKVM_MPI_SOURCE, NANOKVM_MMF_OUTPUT and
NANOKVM_CAPTURE_OUTPUT as set by platform/build.sh native; writes
NANOKVM_PROBE_OUTPUT/venc-copy-bench (default: NANOKVM_CAPTURE_OUTPUT).
On the device, with NanoKVM-Server stopped:
  LD_LIBRARY_PATH=/kvmapp/server/dl_lib ./venc-copy-bench 600 2560 1440 h265 4500 60 60
"""
from pathlib import Path
import hashlib
import os
import subprocess
os.environ['PATH'] = '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin'
repo = Path(__file__).resolve().parent.parent
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
mmf = Path(os.environ['NANOKVM_MMF_OUTPUT']).resolve()
capture = Path(os.environ['NANOKVM_CAPTURE_OUTPUT']).resolve()
output = Path(os.environ.get('NANOKVM_PROBE_OUTPUT', capture)).resolve()
output.mkdir(parents=True, exist_ok=True)
cross = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve()/'host/bin/riscv64-buildroot-linux-musl-')
dest = output/'venc-copy-bench'
subprocess.run([cross+'g++', '-std=gnu++17', '-O2', '-Wall', '-Wextra', '-Werror',
    '-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync',
    '-mtune=thead-c906', '-mno-fence-tso', '-mabi=lp64d',
    '-I'+str(repo/'support/sg2002/additional/kvm/include'), str(repo/'firmware/probes/venc-copy-bench.cpp'),
    '-L'+str(capture), '-lkvm', '-Wl,-rpath-link,'+str(mmf)+':'+str(mpi/'lib'),
    # The libraries' mmap and munmap calls must bind to the counting wrappers.
    '-Wl,--export-dynamic-symbol=mmap', '-Wl,--export-dynamic-symbol=munmap',
    '-static-libgcc', '-o', str(dest)], check=True)
if 'fence.tso' in subprocess.check_output([cross+'objdump', '-d', str(dest)], text=True):
    raise SystemExit('Unsupported fence.tso in venc-copy-bench')
symbols = subprocess.check_output([cross+'readelf', '--dyn-syms', '-W', str(dest)], text=True)
for name in ('mmap', 'munmap'):
    if not any(line.split()[-1] == name and ' UND ' not in line for line in symbols.splitlines() if line.split()):
        raise SystemExit(name+' is not exported; the libraries would bypass the counter')
print(hashlib.sha256(dest.read_bytes()).hexdigest(), dest)
