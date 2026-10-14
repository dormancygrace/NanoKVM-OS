#!/usr/bin/env python3
"""Build a probe using the exact supplied kernel ABI; never rebuild the core."""
import argparse,json,subprocess,shutil
from pathlib import Path
from variants import generate
p=argparse.ArgumentParser();p.add_argument('--kernel',type=Path,required=True);p.add_argument('--kernel-build',type=Path,required=True)
p.add_argument('--output',type=Path,required=True);p.add_argument('--cross',required=True);p.add_argument('--cflags',required=True)
a=p.parse_args()
a.kernel=a.kernel.resolve();a.kernel_build=a.kernel_build.resolve();a.output=a.output.resolve()
if not (a.kernel_build/'include/config/kernel.release').is_file() or not (a.kernel_build/'Module.symvers').is_file():
    p.error('a configured and fully built matching kernel is required')
a.output.mkdir(parents=True,exist_ok=False)
generate(a.kernel,a.output)
for name in ['probe.c','protocol.h','client.c']:shutil.copyfile(Path(__file__).parent/name,a.output/name)
objects=['probe.o']+[f'v{v}/{file}.o' for v in range(1,11) for file in ['lz4_compress','lz4_decompress']]
(a.output/'Makefile').write_text('obj-m := c906_lz4.o\nc906_lz4-y := '+' '.join(objects)+'\n')
cmd=['make','-C',str(a.kernel),'O='+str(a.kernel_build),'M='+str(a.output),'ARCH=riscv','CROSS_COMPILE='+a.cross,'CC='+a.cross+'gcc','KCFLAGS='+a.cflags,'-j4','modules']
with (a.output/'build.log').open('w') as log:
    r=subprocess.run(cmd,stdout=log,stderr=subprocess.STDOUT)
if r.returncode:
    print((a.output/'build.log').read_text()[-5000:]);raise SystemExit(r.returncode)
subprocess.run([a.cross+'gcc','-O2','-static','-march=rv64imafdc_zicsr_zifencei','-mabi=lp64d',str(a.output/'client.c'),'-o',str(a.output/'lz4-client')],check=True)
(a.output/'contract.json').write_text(json.dumps({'command':cmd,'scope':'isolated LZ4 candidates, actual production library reference; no core/runtime replacement'},indent=2)+'\n')
print('PASS matching LZ4 probe/client built')
