#!/usr/bin/env python3
"""Test the actual pinned MaixCDK I2C source with wrapped hardware syscalls."""
from pathlib import Path
import os, subprocess
os.environ['PATH']='/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin'
repo=Path(__file__).resolve().parents[1]
maix=Path(os.environ['NANOKVM_MAIXCDK_SOURCE']).resolve()
out=Path(os.environ['NANOKVM_MAIX_PROBE_OUTPUT']).resolve()
out.mkdir(parents=True,exist_ok=True)
(out/'global_config.h').write_text('#pragma once\n#define PROJECT_ID "nanokvm-maix-probe"\n')
(out/'global_build_info_version.h').write_text('#pragma once\n')
flags=['-std=gnu++17','-O1','-g','-ffunction-sections','-fdata-sections']
if 'NANOKVM_BUILDROOT_OUTPUT' in os.environ:
    cc=str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT'])/'host/bin/riscv64-buildroot-linux-musl-g++')
    flags+=['-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector','-mtune=thead-c906','-mno-fence-tso','-mabi=lp64d']
else:
    cc='g++'
    flags+=['-fsanitize=address,undefined','-fno-omit-frame-pointer']
flags+=['-I'+str(p) for p in [out,maix/'components/basic/include',maix/'components/peripheral/include']]
sources=[repo/'firmware/probes/maix-peripheral-contract.cpp',
         maix/'components/peripheral/port/maixcam/maix_i2c.cpp',
         maix/'components/basic/src/maix_err.cpp',maix/'components/basic/src/maix_log.cpp']
subprocess.run([cc,*flags,*map(str,sources),'-Wl,--gc-sections',
    '-Wl,--wrap=open,--wrap=close,--wrap=ioctl,--wrap=read,--wrap=write',
    '-o',str(out/'maix-peripheral-contract')],check=True)
print(out/'maix-peripheral-contract')
