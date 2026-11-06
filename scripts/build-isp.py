#!/usr/bin/env python3
"""Compile the open ISP library: its runtime and the MIPI receiver code.

The LT6911 output bypasses the ISP's image processing, so the closed ISP/3A
algorithm objects are not built or linked (platform/native/cvi_mpi/0017). Requires the same NANOKVM_* environment variables as
build-mpi.sh.
"""
from nanokvm_cpu_profile import flags as cpu_flags, record as record_cpu_profile
from pathlib import Path
import os
import subprocess

os.environ['PATH'] = os.environ.get('NANOKVM_HOST_PATH', '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
cross = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve() / 'host/bin/riscv64-buildroot-linux-musl-')
osdrv = str(Path(os.environ['NANOKVM_OSDRV_SOURCE']).resolve())
kernel = str(Path(os.environ['NANOKVM_KERNEL_SOURCE']).resolve())
if subprocess.check_output([cross + 'gcc', '-dumpfullversion'], text=True).strip() != '16.2.0':
    raise SystemExit('Expected the qualified GCC16.2 toolchain')
path_flags = ' '.join(f'-ffile-prefix-map={src}={name}' for src, name in [
    (mpi, './cvi_mpi'), (osdrv, './osdrv'), (kernel, './linux'),
    (Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve(), './toolchain')])
opt_flags = ' '.join(cpu_flags()) + ' -mcmodel=medany ' + path_flags
record_cpu_profile(mpi / 'cpu-profile-isp.json', cross + 'gcc', effective_flags=opt_flags.split())
common = ['OPT_LEVEL=' + opt_flags, 'CROSS_COMPILE=' + cross, 'CHIP_ARCH=CV181X', 'ISP_SRC_RELEASE=0', '-B',
          'OSDRV_PATH=' + osdrv, 'KERNEL_PATH=' + kernel, '-j' + os.environ.get('JOBS', '8')]
(mpi / 'lib/3rd').mkdir(parents=True, exist_ok=True)
subprocess.run(['make', '-C', str(mpi / 'modules/isp/cv181x/isp'), *common], check=True)
print('Open ISP compiled; no closed ISP/3A objects.')
