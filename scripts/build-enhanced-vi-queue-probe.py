#!/usr/bin/env python3
"""Exercise actual VI queue ownership code with a sleeping hardware consumer."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

root=Path(__file__).resolve().parents[1]
src=Path(os.environ['NANOKVM_OSDRV_SOURCE']).resolve()/'interdrv'
out=Path(os.environ['NANOKVM_VI_QUEUE_PROBE_OUTPUT']).resolve()
out.mkdir(parents=True,exist_ok=True)
inputs={}
def read(p):
    data=(src/p).read_bytes(); inputs[p]=hashlib.sha256(data).hexdigest(); return data.decode()
def block(text,pattern):
    m=re.search(pattern,text,re.M)
    if not m: raise RuntimeError(pattern)
    end=m.end();depth=1
    while depth:
        depth+=(text[end]=='{')-(text[end]=='}');end+=1
    return text[m.start():end]
c=read('vi/chip/cv181x/vi.c'); h=read('vi/chip/cv181x/vi.h')
u=read('include/chip/cv181x/uapi/linux/vi_isp.h')
t=read('include/chip/cv181x/uapi/linux/vi_tun_cfg.h')
types=block(t,r'^enum cvi_isp_raw \{')+';\n'
types+=block(u,r'^struct cvi_isp_snr_update \{')+';\n'
for n in ['_isp_snr_i2c_node','_isp_crop_node']:
    types+=block(h,r'^struct '+n+r' \{')+';\n'
types+=block(h,r'^struct _isp_snr_cfg_queue \{')+' isp_snr_i2c_queue[ISP_PRERAW_VIRT_MAX], isp_crop_queue[ISP_PRERAW_VIRT_MAX];\n'
(out/'vi-queue-types.h').write_text(types)
code='static DEFINE_MUTEX(snr_cfg_lock);\nstatic bool snr_cfg_initialized, snr_cfg_ready;\n'
for n in ['_isp_snr_cfg_reset','_isp_snr_cfg_enq','_isp_snr_cfg_deq_and_fire']:
    code+=block(c,r'^static (?:void|int) '+n+r'\([^;]*?\n\{')+'\n'
(out/'vi-queue-actual.h').write_text(code)
cc=os.environ.get('CC','cc')
flags=['-std=gnu11','-O1','-g','-Wall','-Wextra','-Werror','-Wno-unused-parameter','-pthread']
if 'NANOKVM_BUILDROOT_OUTPUT' in os.environ:
    cc=str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT'])/'host/bin/riscv64-buildroot-linux-musl-gcc')
    flags+=['-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector','-mabi=lp64d','-mtune=thead-c906','-mno-fence-tso']
else:
    flags+=['-fsanitize=address,undefined','-fno-omit-frame-pointer']
subprocess.run([cc,*flags,'-I'+str(out),'-I'+str(src/'include/chip/cv181x/uapi'),
    '-I'+str(src/'include/common/uapi'),str(root/'firmware/probes/vi-queue-contract.c'),
    '-o',str(out/'vi-queue-contract')],check=True)
(out/'sources.json').write_text(json.dumps(inputs,indent=2)+'\n')
print(out/'vi-queue-contract')
