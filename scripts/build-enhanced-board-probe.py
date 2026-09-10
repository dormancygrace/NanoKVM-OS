#!/usr/bin/env python3
"""CPU contract checks of extracted driver code; kernel services are mocked."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

root = Path(__file__).resolve().parents[1]
src = Path(os.environ['NANOKVM_OSDRV_SOURCE']).resolve()/'interdrv'
out = Path(os.environ['NANOKVM_BOARD_PROBE_OUTPUT']).resolve()
out.mkdir(parents=True, exist_ok=True)
inputs = {}

def read(path):
    data = (src/path).read_bytes()
    inputs[str(path)] = hashlib.sha256(data).hexdigest()
    return data.decode()

def definition(text, pattern):
    match = re.search(pattern, text, re.M)
    if not match:
        raise RuntimeError('Missing definition: '+pattern)
    end, depth = match.end(), 1
    while depth:
        depth += (text[end] == '{') - (text[end] == '}')
        end += 1
    return text[match.start():end]

def struct(text, name):
    return definition(text, r'^struct '+name+r' \{')+';\n'

def function(text, name):
    return definition(text, r'^(?:static )?(?:int|long) '+name+r'\([^;]*?\n\{')+'\n'

uapi = read(Path('include/chip/cv181x/uapi/linux/vi_snsr.h'))
cifapi = read(Path('include/chip/cv181x/uapi/linux/cif_uapi.h'))
kapi = read(Path('include/common/kapi/snsr_i2c.h'))
sensor = read(Path('snsr_i2c/common/snsr_i2c.c'))
base = read(Path('base/base.c'))
cif = read(Path('cif/chip/cv181x/cif.c'))
rgn = read(Path('rgn/chip/cv181x/rgn.c'))
decl = struct(uapi, 'isp_i2c_data')
decl += ''.join(struct(cifapi, n) for n in ['sns_i2c_attr', 'addr_data_seq', 'sns_i2c_info'])
decl += re.sub(r'^#include.*\n', '', kapi, flags=re.M)
(out/'board-actual-types.h').write_text(decl)
code = sensor[sensor.index('enum {'):sensor.index('static int cvi_snsr_i2c_probe(')]
code += base[base.index('enum vip_sys_cmm {'):base.index('int base_rm_module_cb(')]
code += function(cif, 'sensor_standby_restart')
code += function(rgn, 'rgn_open') + function(rgn, 'rgn_release')
(out/'board-actual-helpers.h').write_text(code)
cc = os.environ.get('CC', 'cc')
flags = ['-std=gnu11', '-Wall', '-Wextra', '-Werror', '-Wno-unused-parameter', '-O1', '-g', '-pthread']
if 'NANOKVM_BUILDROOT_OUTPUT' in os.environ:
    cc = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT'])/'host/bin/riscv64-buildroot-linux-musl-gcc')
    flags += ['-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector', '-mabi=lp64d', '-mtune=thead-c906', '-mno-fence-tso']
else:
    flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer']
subprocess.run([cc, *flags, '-I'+str(out), str(root/'firmware/probes/board-driver-contract.c'),
                '-o', str(out/'board-driver-contract')], check=True)
(out/'sources.json').write_text(json.dumps(inputs, indent=2)+'\n')
print(out/'board-driver-contract')
