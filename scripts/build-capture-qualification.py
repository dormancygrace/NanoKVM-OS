#!/usr/bin/env python3
"""Build the capture qualification programs against a built libkvm; no install.

firmware/probes/dual-stream-runtime.cpp runs JPEG next to H.264 or H.265 at
1080p and 1440p; firmware/probes/mjpeg-runtime.cpp runs MJPEG in NV21 and NV16
at several sizes and JPEG qualities. Both open the capture exclusively and
write sample frames to /tmp/nanokvm-dual-stream-qualification and
/run/nanokvm-mjpeg-qualification, which must exist.

Requires NANOKVM_BUILDROOT_OUTPUT, NANOKVM_MPI_SOURCE, NANOKVM_MMF_OUTPUT and
NANOKVM_CAPTURE_OUTPUT as set by platform/build.sh native; writes
NANOKVM_PROBE_OUTPUT/{dual-stream-runtime,mjpeg-runtime,vpss-demand-runtime} (default:
NANOKVM_CAPTURE_OUTPUT). On the device, with NanoKVM-Server stopped:
  LD_LIBRARY_PATH=/kvmapp/server/dl_lib ./dual-stream-runtime
  LD_LIBRARY_PATH=/kvmapp/server/dl_lib ./mjpeg-runtime nv21|nv16 WIDTH HEIGHT
  LD_LIBRARY_PATH=/kvmapp/server/dl_lib ./vpss-demand-runtime /run/nanokvm-vpss-qualification

vpss-demand-runtime switches direct H.265/H.264, MJPEG, scaled/thinned output
and park/resume on a 2560x1440@60 input; its output directory must exist.
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
for name in ('dual-stream-runtime', 'mjpeg-runtime', 'vpss-demand-runtime'):
    dest = output/name
    subprocess.run([cross+'g++', '-std=gnu++17', '-O2', '-Wall', '-Wextra', '-Werror',
        '-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync',
        '-mtune=thead-c906', '-mno-fence-tso', '-mabi=lp64d', '-D__CV181X__',
        '-I'+str(repo/'support/sg2002/additional/kvm/include'),
        '-I'+str(repo/'support/sg2002/additional/kvm_mmf/include'), '-I'+str(mpi/'include'),
        str(repo/'firmware/probes'/(name+'.cpp')),
        '-L'+str(capture), '-lkvm', '-L'+str(mmf), '-lkvm_mmf', '-Wl,-rpath-link,'+str(mmf)+':'+str(mpi/'lib'),
        '-static-libgcc', '-o', str(dest)], check=True)
    if 'fence.tso' in subprocess.check_output([cross+'objdump', '-d', str(dest)], text=True):
        raise SystemExit('Unsupported fence.tso in '+name)
    print(hashlib.sha256(dest.read_bytes()).hexdigest(), dest)
