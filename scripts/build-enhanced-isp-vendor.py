#!/usr/bin/env python3
"""Relink pinned Sophgo ISP/3A objects, then compile open ISP sources.

The vendor algorithms remain Xuantie GCC10 binaries, not GCC16 source builds.
Requires the same NANOKVM_* environment variables as build-enhanced-mpi.sh.
"""
from pathlib import Path
import hashlib
import json
import os
import re
import subprocess
import tempfile

os.environ['PATH'] = os.environ.get('NANOKVM_HOST_PATH', '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
repo = Path(__file__).resolve().parent.parent
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
cross = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve() / 'host/bin/riscv64-buildroot-linux-musl-')
osdrv = str(Path(os.environ['NANOKVM_OSDRV_SOURCE']).resolve())
kernel = str(Path(os.environ['NANOKVM_KERNEL_SOURCE']).resolve())
manifest = json.loads((repo / 'firmware/mpi/vendor-isp-objects.json').read_text())
if subprocess.check_output([cross + 'gcc', '-dumpfullversion'], text=True).strip() != '16.2.0':
    raise SystemExit('Expected the qualified GCC16.2 toolchain')
modules = {
    'modules/isp/cv181x/isp_algo': 'isp_algo',
    'modules/isp/algo/ae': 'ae',
    'modules/isp/algo/awb': 'awb',
    'modules/isp/algo/af': 'af',
}
groups = {module: [] for module in modules}
for entry in manifest['objects']:
    path = mpi / entry['path']
    if hashlib.sha256(path.read_bytes()).hexdigest() != entry['sha256']:
        raise SystemExit('Vendor object hash mismatch: ' + entry['path'])
    module = str(Path(entry['path']).parent.parent)
    groups[module].append(path)
for module, objects in groups.items():
    makefile = (mpi / module / 'Makefile').read_text()
    expected = re.findall(r'^SRCS_C\s*[+:]?=\s*\$\(SDIR\)/([^\s]+)\.c', makefile, re.M)
    actual = [path.name.split('.riscv64-')[0] for path in objects]
    if not expected or len(actual) != len(set(actual)) or set(expected) != set(actual):
        raise SystemExit('Vendor object set differs from Makefile: ' + module)

path_flags = ' '.join(f'-ffile-prefix-map={src}={name}' for src, name in [
    (mpi, './cvi_mpi'), (osdrv, './osdrv'), (kernel, './linux'),
    (Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve(), './toolchain')])
opt_flags = '-Os -march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector -mtune=thead-c906 -mno-fence-tso -mcmodel=medany -mabi=lp64d ' + path_flags
common = ['OPT_LEVEL=' + opt_flags, 'CROSS_COMPILE=' + cross, 'CHIP_ARCH=CV181X', 'ISP_SRC_RELEASE=0', '-B',
          'OSDRV_PATH=' + osdrv, 'KERNEL_PATH=' + kernel, '-j' + os.environ.get('JOBS', '8')]
(mpi / 'lib/3rd').mkdir(parents=True, exist_ok=True)
# Fresh output archives cannot retain members from a different toolchain.
# Explicit targets bypass destructive vendor sdk_release/prepare recipes.
with tempfile.TemporaryDirectory(prefix='enhanced-isp-', dir=mpi / 'lib') as stage:
    for module, name in modules.items():
        archive = str(Path(stage) / ('lib' + name + '.a'))
        shared = str(Path(stage) / ('lib' + name + '.so'))
        print('Relinking vendor algorithms (not recompiling):', module, flush=True)
        subprocess.run(['make', '-C', str(mpi / module), *common,
                        'OBJS=' + ' '.join(map(str, groups[module])),
                        'TARGET_A=' + archive, 'TARGET_SO=' + shared, archive, shared], check=True)
    for path in Path(stage).iterdir():
        path.replace(mpi / 'lib' / path.name)
subprocess.run(['make', '-C', str(mpi / 'modules/isp/cv181x/isp'), *common], check=True)
print('Open ISP compiled; vendor algorithms relinked. Runtime ABI and BIN closure remain pending.')
