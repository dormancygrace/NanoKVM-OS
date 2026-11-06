#!/usr/bin/env python3
"""Build the NanoKVM MMF wrapper against patched current MPI and sensor sources.

Requires NANOKVM_MPI_SOURCE, NANOKVM_SENSOR_SOURCE, NANOKVM_INIH_SOURCE,
NANOKVM_BUILDROOT_OUTPUT and a dedicated NANOKVM_MMF_OUTPUT. No device install.
"""
from nanokvm_cpu_profile import flags as cpu_flags, record as record_cpu_profile
from nanokvm_mmf_link import link_hdmi_light
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
# platform/build.sh checks the source trees against platform/sources.lock.
if subprocess.check_output([cross + 'gcc', '-dumpfullversion'], text=True).strip() != '16.2.0':
    raise SystemExit('Expected GCC 16.2')
includes = [mpi / 'include', mpi / 'include/isp/cv181x', mpi / 'sample/common',
            mpi / 'component/panel/cv181x', sensor / 'common', inih,
            repo / 'support/additional/kvm_mmf/include', repo / 'firmware/mpi']
flags = ['-Wall', '-Wextra', '-Werror', '-fPIC', '-ffunction-sections', '-fdata-sections',
         *cpu_flags(),
         '-D__CV181X__', '-DOS_IS_LINUX', '-DNANOKVM_OS', '-DSENSOR_LONTIUM_LT6911']
# GCC uses the last matching map: specific inputs override the enclosing checkout.
flags += [f'-ffile-prefix-map={src}={name}' for src, name in [
    (repo, './nanokvm-os'),
    (mpi, './cvi_mpi'), (sensor, './sensor-support'), (inih, './inih'),
    (Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve(), './toolchain')]]
flags += ['-DSENSOR' + str(i) + '_TYPE=LONTIUM_LT6911_2M_60FPS_8BIT' for i in range(3)]
flags += ['-I' + str(path) for path in includes]
sources = sorted((mpi / 'sample/common').glob('*.c'))
sources += [sensor / 'common/sensor_list.c']
sources += sorted((sensor / 'sensor/cv182x/lontium_lt6911').glob('*.c'))
sources += [inih / 'ini.c', repo / 'firmware/mpi/nanokvm_capture_size.c',
            repo / 'firmware/mpi/nanokvm_vi_buffering.c',
            repo / 'support/additional/kvm_mmf/src/kvm_mmf.cpp']
if len(sources) != 21 or len({path.stem for path in sources}) != len(sources):
    raise SystemExit('Unexpected MMF source set; review before updating the build')
output.mkdir(parents=True, exist_ok=True)
record_cpu_profile(output/'cpu-profile.json', cross+'gcc', effective_flags=flags)
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
    link_hdmi_light(cross, mpi, objects, shared, output)
    shutil.copy2(shared, output / shared.name)
license_dir = output / 'licenses/inih'
license_dir.mkdir(parents=True, exist_ok=True)
shutil.copy2(inih / 'LICENSE.txt', license_dir / 'LICENSE.txt')
manifest = {'libkvm_mmf.so': hashlib.sha256((output / 'libkvm_mmf.so').read_bytes()).hexdigest()}
(output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print('MMF compiled and linked. Hardware qualification is separate; LT6911UXC identification is supported.')
