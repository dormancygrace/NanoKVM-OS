#!/usr/bin/env python3
"""Build the HDMI capture diagnostic with the selected Enhanced libraries."""
from pathlib import Path
import os, subprocess, hashlib
repo = Path(__file__).resolve().parents[1]
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
mmf = Path(os.environ['NANOKVM_MMF_OUTPUT']).resolve()
buildroot = Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve()
output = Path(os.environ['NANOKVM_CAPTURE_OUTPUT']).resolve()
output.mkdir(parents=True, exist_ok=True)
cross = str(buildroot/'host/bin/riscv64-buildroot-linux-musl-')
dest = output/'hdmi-runtime'
subprocess.run([cross+'g++', '-std=gnu++17', '-Os', '-Wall', '-Wextra', '-Werror',
    '-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector', '-mtune=thead-c906', '-mno-fence-tso', '-mabi=lp64d',
    '-D__CV181X__', '-I'+str(repo/'support/sg2002/additional/kvm_mmf/include'),
    '-I'+str(mpi/'include'), str(repo/'firmware/probes/hdmi-runtime.cpp'),
    '-L'+str(mmf), '-L'+str(mpi/'lib'), '-lkvm_mmf',
    '-Wl,-rpath-link,'+str(mpi/'lib'), '-o', str(dest)], check=True)
if 'fence.tso' in subprocess.check_output([cross+'objdump', '-d', str(dest)], text=True):
    raise SystemExit('Unsupported fence.tso in hardware probe')
print(hashlib.sha256(dest.read_bytes()).hexdigest(), dest)
