#!/usr/bin/env python3
"""Build the NanoKVM MMF wrapper against patched current MPI and sensor sources.

Requires NANOKVM_MPI_SOURCE, NANOKVM_SENSOR_SOURCE, NANOKVM_INIH_SOURCE,
NANOKVM_BUILDROOT_OUTPUT and a dedicated NANOKVM_MMF_OUTPUT. No device install.
"""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import tempfile

os.environ['PATH'] = os.environ.get('NANOKVM_HOST_PATH', '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
repo = Path(__file__).resolve().parent.parent
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
sensor = Path(os.environ['NANOKVM_SENSOR_SOURCE']).resolve()
inih = Path(os.environ['NANOKVM_INIH_SOURCE']).resolve()
output = Path(os.environ['NANOKVM_MMF_OUTPUT']).resolve()
cross = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve() / 'host/bin/riscv64-buildroot-linux-musl-')
pins = json.loads((repo / 'firmware/sources.json').read_text())
if subprocess.check_output([cross + 'gcc', '-dumpfullversion'], text=True).strip() != '16.2.0':
    raise SystemExit('Expected Enhanced GCC16.2')
if subprocess.check_output(['git', '-C', str(inih), 'rev-parse', 'HEAD'], text=True).strip() != pins['inih']['commit']:
    raise SystemExit('Wrong inih pin')
if subprocess.check_output(['git', '-C', str(inih), 'status', '--porcelain', '--untracked-files=no'], text=True):
    raise SystemExit('Tracked source changes in inih')
includes = [mpi / 'include', mpi / 'include/isp/cv181x', mpi / 'sample/common',
            mpi / 'component/panel/cv181x', sensor / 'common', inih,
            repo / 'support/sg2002/additional/kvm_mmf/include', repo / 'firmware/mpi']
flags = ['-Os', '-Wall', '-Wextra', '-Werror', '-fPIC', '-ffunction-sections', '-fdata-sections',
         '-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector', '-mtune=thead-c906', '-mno-fence-tso', '-mabi=lp64d',
         '-D__CV181X__', '-DOS_IS_LINUX', '-DNANOKVM_ENHANCED', '-DSENSOR_LONTIUM_LT6911']
flags += [f'-ffile-prefix-map={src}={name}' for src, name in [
    (mpi, './cvi_mpi'), (sensor, './sensor-support'), (inih, './inih'),
    (repo, './nanokvm-os'),
    (Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve(), './toolchain')]]
flags += ['-DSENSOR' + str(i) + '_TYPE=LONTIUM_LT6911_2M_60FPS_8BIT' for i in range(3)]
flags += ['-I' + str(path) for path in includes]
sources = sorted((mpi / 'sample/common').glob('*.c'))
sources += [sensor / 'common/sensor_list.c']
sources += sorted((sensor / 'sensor/cv182x/lontium_lt6911').glob('*.c'))
sources += [inih / 'ini.c', repo / 'firmware/mpi/nanokvm_capture_size.c',
            repo / 'support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp']
if len(sources) != 20 or len({path.stem for path in sources}) != len(sources):
    raise SystemExit('Unexpected MMF source set; review before updating the build')
names = ['sys', 'vi', 'vpss', 'vo', 'rgn', 'gdc', 'venc', 'vdec', 'misc', 'cvi_ive',
         'isp', 'isp_algo', 'ae', 'awb', 'af', 'cvi_bin', 'cvi_bin_isp']
output.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix='mmf-objects-', dir=output) as temp:
    temp = Path(temp)
    objects = []
    for source in sources:
        obj = temp / (source.stem + '.o')
        cpp = source.suffix == '.cpp'
        print('Compile', source.name, flush=True)
        subprocess.run([cross + ('g++' if cpp else 'gcc'), '-std=' + ('gnu++17' if cpp else 'gnu11'),
                        *flags, '-c', str(source), '-o', str(obj)], check=True)
        objects.append(obj)
    shared = temp / 'libkvm_mmf.so'
    subprocess.run([cross + 'g++', '-shared', '-Wl,-z,defs', '-Wl,-soname,libkvm_mmf.so',
                    '-L' + str(mpi / 'lib'), '-Wl,--no-as-needed', '-Wl,--start-group',
                    *map(str, objects), *['-l' + name for name in names],
                    '-Wl,--end-group', '-Wl,--as-needed', '-o', str(shared)], check=True)
    shutil.copy2(shared, output / shared.name)
subprocess.run([cross + 'gcc', '-std=gnu11', *flags, str(repo / 'firmware/probes/mmf-config.c'),
                '-L' + str(output), '-lkvm_mmf', '-Wl,-rpath-link,' + str(mpi / 'lib'),
                '-o', str(output / 'mmf-config-probe')], check=True)
license_dir = output / 'licenses/inih'
license_dir.mkdir(parents=True, exist_ok=True)
shutil.copy2(inih / 'LICENSE.txt', license_dir / 'LICENSE.txt')
manifest = {name: hashlib.sha256((output / name).read_bytes()).hexdigest()
            for name in ['libkvm_mmf.so', 'mmf-config-probe']}
(output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print('MMF compiled and linked. Hardware qualification is separate; LT6911UXC identification is supported.')
