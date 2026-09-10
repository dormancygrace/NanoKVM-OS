#!/usr/bin/env python3
"""Package a standalone loader/JSON probe; never installs or starts hardware.

Requires NANOKVM_MPI_SOURCE, NANOKVM_BUILDROOT_OUTPUT,
NANOKVM_MPI_THIRDPARTY_OUTPUT and dedicated NANOKVM_MPI_PROBE_OUTPUT.
Invoke the packaged libc.so explicitly to avoid using the device's old musl.
"""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import tarfile

os.environ['PATH'] = os.environ.get('NANOKVM_HOST_PATH', '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
repo = Path(__file__).resolve().parent.parent
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
buildroot = Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve()
thirdparty = Path(os.environ['NANOKVM_MPI_THIRDPARTY_OUTPUT']).resolve()
output = Path(os.environ['NANOKVM_MPI_PROBE_OUTPUT']).resolve()
cross = str(buildroot / 'host/bin/riscv64-buildroot-linux-musl-')
output.mkdir(parents=True, exist_ok=True)
names = ['sys', 'vi', 'vpss', 'vo', 'rgn', 'gdc', 'venc', 'vdec', 'misc', 'cvi_ive',
         'isp', 'isp_algo', 'ae', 'awb', 'af', 'cvi_bin', 'cvi_bin_isp']
args = [cross + 'gcc', '-Os', '-Wall', '-Wextra', '-Werror',
        '-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector', '-mtune=thead-c906', '-mno-fence-tso', '-mabi=lp64d',
        '-D__CV181X__', '-I' + str(mpi / 'include'),
        '-I' + str(mpi / 'modules/isp/cv181x/isp_bin/inc'),
        '-I' + str(repo / 'firmware/mpi/compat/cvi_json-c'),
        '-I' + str(thirdparty / 'stage/include'), str(repo / 'firmware/probes/mpi-load-json.c'),
        '-L' + str(mpi / 'lib'), '-Wl,--no-as-needed', '-Wl,--start-group']
args += ['-l' + name for name in names]
args += ['-Wl,--end-group', '-o', str(output / 'mpi-load-json-probe')]
subprocess.run(args, check=True)
files = [output / 'mpi-load-json-probe']
for name in names:
    dest = output / ('lib' + name + '.so')
    shutil.copy2(mpi / 'lib' / dest.name, dest)
    subprocess.run([cross + 'strip', '--strip-debug', str(dest)], check=True)
    files.append(dest)
shutil.copy2(buildroot / 'target/lib/libc.so', output / 'libc.so')
files.append(output / 'libc.so')
manifest = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in files}
(output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
files.append(output / 'manifest.json')
with tarfile.open(output / 'mpi-load-probe.tar', 'w') as tar:
    for path in files:
        tar.add(path, arcname=path.name)
print('Packaged 17 DSOs and fresh musl; run ./libc.so --library-path . ./mpi-load-json-probe')
