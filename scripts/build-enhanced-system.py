#!/usr/bin/env python3
"""Build NanoKVM board service using pinned MaixCDK basic/peripheral sources.

Requires NANOKVM_MAIXCDK_SOURCE, NANOKVM_BUILDROOT_OUTPUT and
NANOKVM_SYSTEM_OUTPUT. Does not install or start the service.
"""
from pathlib import Path
import hashlib, json, os, shutil, subprocess, tempfile
os.environ['PATH']='/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin'
repo=Path(__file__).resolve().parents[1]
maix=Path(os.environ['NANOKVM_MAIXCDK_SOURCE']).resolve()
output=Path(os.environ['NANOKVM_SYSTEM_OUTPUT']).resolve()
cross=str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve()/'host/bin/riscv64-buildroot-linux-musl-')
if subprocess.check_output([cross+'gcc','-dumpfullversion'],text=True).strip()!='16.2.0':
    raise SystemExit('Expected Enhanced GCC 16.2')
subprocess.run(['git','merge-base','--is-ancestor',
    'b29c951647df74e4efa55fd4454efb37e4554be0','HEAD'],cwd=maix,check=True)
main=repo/'support/sg2002/kvm_system/main'
basic=maix/'components/basic'
peripheral=maix/'components/peripheral'
third=maix/'components/3rd_party'
# Link only the functions needed by the board service. No vendor binary input.
maix_sources=[basic/'src'/('maix_'+n+'.cpp') for n in ['time','err','log','fs','app','sys']]
maix_sources += [peripheral/'port/maixcam/maix_i2c.cpp',third/'ini/inifile2/src/inifile.cpp']
sources=maix_sources+sorted(p for p in main.rglob('*.c') if p.name != 'qrcmd.c')+sorted(main.rglob('*.cpp'))
sources += [repo/'support/sg2002/additional/kvm/src/vi_state_shared.cpp']
flags=['-Os','-g','-Wall','-Wextra','-ffunction-sections','-fdata-sections',
       '-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector','-mtune=thead-c906','-mno-fence-tso','-mabi=lp64d',
       '-DPLATFORM_MAIXCAM','-DNANOKVM_ENHANCED','-Werror=return-type','-Werror=uninitialized','-Werror=maybe-uninitialized']
output.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='system-',dir=output) as directory:
    tmp=Path(directory)
    (tmp/'global_config.h').write_text('#pragma once\n#define PROJECT_ID "nanokvm-enhanced"\n')
    (tmp/'global_build_info_version.h').write_text('#pragma once\n')
    includes=[tmp,basic/'include',peripheral/'include',third/'yaml/yaml-cpp/include',
              third/'ntp_client/ntp_client',third/'ini/inifile2/src',main/'include',
              repo/'support/sg2002/additional/kvm/include']
    includes += sorted(p for p in (main/'lib').iterdir() if p.is_dir())
    # Keep workstation paths out of assertion strings and debug data.
    path_flags = [f'-ffile-prefix-map={src}={name}' for src, name in [
        (repo, './nanokvm-os'), (maix, './maixcdk'),
        (Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve(), './toolchain'),
        (tmp, './build/system')]]
    inc=['-I'+str(p) for p in includes]
    objects=[]
    for i,source in enumerate(sources):
        obj=tmp/(str(i)+'.o')
        cpp=source.suffix=='.cpp'
        subprocess.run([cross+('g++' if cpp else 'gcc'),
            '-std='+('gnu++17' if cpp else 'gnu11'),*flags,*path_flags,*inc,'-MD','-MF',str(tmp/(str(i)+'.d')),
            '-c',str(source),'-o',str(obj)],check=True)
        objects.append(str(obj))
    subprocess.run([cross+'g++',*objects,'-Wl,--gc-sections','-Wl,-z,defs',
        '-Wl,-Map,'+str(tmp/'system.map'),'-pthread','-o',str(tmp/'kvm_system')],check=True)
    dependencies='\n'.join(p.read_text() for p in tmp.glob('*.d'))
    if 'opencv' in dependencies.lower(): raise SystemExit('Unexpected OpenCV dependency')
    subprocess.run([cross+'objcopy','--only-keep-debug','kvm_system','kvm_system.debug'],cwd=tmp,check=True)
    subprocess.run([cross+'strip','--strip-unneeded','kvm_system'],cwd=tmp,check=True)
    subprocess.run([cross+'objcopy','--add-gnu-debuglink=kvm_system.debug','kvm_system'],cwd=tmp,check=True)
    shutil.copy2(tmp/'kvm_system',output/'kvm_system')
    shutil.copy2(tmp/'kvm_system.debug',output/'kvm_system.debug')
    shutil.copy2(tmp/'system.map',output/'system.map')
    (output/'dependencies.d').write_text(dependencies)
manifest={'maixcdk_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=maix,text=True).strip(),
          'source_sha256':{str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in sources},
          'kvm_system_sha256':hashlib.sha256((output/'kvm_system').read_bytes()).hexdigest()}
(output/'licenses').mkdir(exist_ok=True)
shutil.copy2(maix/'LICENSE',output/'licenses/MaixCDK-LICENSE')
shutil.copy2(main/'lib/libqr/LICENSE',output/'licenses/libqr-LICENSE')
ini_notice=(third/'ini/inifile2/src/inifile.cpp').read_text().split('*/',1)[0]+'*/\n'
(output/'licenses/inifile2-LICENSE').write_text(ini_notice)
(output/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('Board service built with MaixCDK basic/peripheral source subset. Hardware qualification pending.')
