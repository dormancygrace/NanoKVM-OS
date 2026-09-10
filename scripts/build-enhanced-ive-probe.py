#!/usr/bin/env python3
"""Build a CPU-only probe from the actual patched IVE helper definitions."""
import os
import pathlib
import re
import subprocess
root = pathlib.Path(__file__).resolve().parents[1]
osdrv = pathlib.Path(os.environ['NANOKVM_OSDRV_SOURCE']).resolve()
out = pathlib.Path(os.environ['NANOKVM_IVE_PROBE_OUTPUT']).resolve()
out.mkdir(parents=True, exist_ok=True)

def function(path, name):
    source = path.read_text()
    match = re.search(r'^(?:static )?(?:int|long) '+name+r'\([^;]*?\n\{', source, re.M)
    if not match:
        raise RuntimeError('Missing exact helper: '+name)
    start = match.start()
    cursor = match.end()
    depth = 1
    while depth:
        depth += (source[cursor] == '{') - (source[cursor] == '}')
        cursor += 1
    return source[start:cursor]+'\n'

ive = osdrv/'interdrv/ive'
helpers = function(ive/'common/cvi_ive_interface.c', 'ive_ioctl_arg_size')
helpers += function(ive/'hal/cv181x/cvi_ive_platform.c', 'stcandicorner_workaround')
(out/'ive-actual-helpers.h').write_text(helpers)
cc = os.environ.get('CC', 'cc')
flags = ['-std=gnu11', '-Wall', '-Wextra', '-Werror', '-O1', '-g']
if 'NANOKVM_BUILDROOT_OUTPUT' in os.environ:
    cc = str(pathlib.Path(os.environ['NANOKVM_BUILDROOT_OUTPUT'])/'host/bin/riscv64-buildroot-linux-musl-gcc')
    flags += ['-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector', '-mabi=lp64d', '-mtune=thead-c906', '-mno-fence-tso']
else:
    flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer']
subprocess.run([cc, *flags, '-I'+str(out),
    '-I'+str(osdrv/'interdrv/include/common/uapi'),
    '-I'+str(osdrv/'interdrv/include/chip/cv181x/uapi'),
    str(root/'firmware/probes/ive-init-contract.c'), '-o', str(out/'ive-init-contract')], check=True)
print(out/'ive-init-contract')
