#!/usr/bin/env python3
"""Build MPI BIN against pinned upstream JSON/miniz using current headers.

Requires NANOKVM_{MPI,OSDRV,KERNEL,JSON_C,MINIZ}_SOURCE,
NANOKVM_BUILDROOT_OUTPUT, and a dedicated NANOKVM_MPI_THIRDPARTY_OUTPUT.
Run after core MPI and ISP builds. This does not install a firmware image.
"""
from pathlib import Path
import json
import os
import shutil
import subprocess
import tempfile

os.environ['PATH'] = os.environ.get('NANOKVM_HOST_PATH', '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
repo = Path(__file__).resolve().parent.parent
pins = json.loads((repo / 'firmware/sources.json').read_text())
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
out = Path(os.environ['NANOKVM_MPI_THIRDPARTY_OUTPUT']).resolve()
stage = out / 'stage'
cross = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve() / 'host/bin/riscv64-buildroot-linux-musl-')
cmake = os.environ.get('NANOKVM_CMAKE', 'cmake')
jobs = os.environ.get('JOBS', '8')
flags = '-Os -fPIC -march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector -mtune=thead-c906 -mno-fence-tso -mabi=lp64d'
path_flags = ' '.join(f'-ffile-prefix-map={Path(src).resolve()}={name}' for src, name in [
    (mpi, './cvi_mpi'), (out, './build/mpi-bin'), (repo, './nanokvm-os'),
    (os.environ['NANOKVM_OSDRV_SOURCE'], './osdrv'),
    (os.environ['NANOKVM_KERNEL_SOURCE'], './linux'),
    (os.environ['NANOKVM_JSON_C_SOURCE'], './json-c'),
    (os.environ['NANOKVM_MINIZ_SOURCE'], './miniz'),
    (os.environ['NANOKVM_BUILDROOT_OUTPUT'], './toolchain')])
flags += ' ' + path_flags
sources = [
    ('json_c', 'NANOKVM_JSON_C_SOURCE', ['-DBUILD_TESTING=OFF', '-DBUILD_APPS=OFF', '-DDISABLE_EXTRA_LIBS=ON']),
    ('miniz', 'NANOKVM_MINIZ_SOURCE', ['-DBUILD_EXAMPLES=OFF', '-DBUILD_TESTS=OFF']),
]
if subprocess.check_output([cross + 'gcc', '-dumpfullversion'], text=True).strip() != '16.2.0':
    raise SystemExit('Expected the qualified GCC16.2 toolchain')
out.mkdir(parents=True, exist_ok=True)
for name, variable, options in sources:
    source = Path(os.environ[variable]).resolve()
    commit = subprocess.check_output(['git', '-C', str(source), 'rev-parse', 'HEAD'], text=True).strip()
    if commit != pins[name]['commit']:
        raise SystemExit('Wrong source pin for ' + name)
    if subprocess.check_output(['git', '-C', str(source), 'status', '--porcelain', '--untracked-files=no'], text=True):
        raise SystemExit('Tracked source changes in ' + name)
    build = out / (name + '-' + pins[name]['version'])
    subprocess.run([cmake, '-S', str(source), '-B', str(build),
        '-DCMAKE_SYSTEM_NAME=Linux', '-DCMAKE_SYSTEM_PROCESSOR=riscv64',
        '-DCMAKE_C_COMPILER=' + cross + 'gcc', '-DCMAKE_AR=' + cross + 'ar',
        '-DCMAKE_RANLIB=' + cross + 'ranlib', '-DCMAKE_C_FLAGS=' + flags,
        '-DCMAKE_BUILD_TYPE=MinSizeRel', '-DCMAKE_POSITION_INDEPENDENT_CODE=ON',
        '-DBUILD_SHARED_LIBS=OFF', '-DCMAKE_INSTALL_LIBDIR=lib',
        '-DCMAKE_INSTALL_PREFIX=' + str(stage), *options], check=True)
    subprocess.run([cmake, '--build', str(build), '-j' + jobs], check=True)
    subprocess.run([cmake, '--install', str(build)], check=True)
    license_dir = stage / 'share/licenses' / name
    license_dir.mkdir(parents=True, exist_ok=True)
    license_file = 'COPYING' if name == 'json_c' else 'LICENSE'
    shutil.copy2(source / license_file, license_dir / license_file)

compat = repo / 'firmware/mpi/compat'
common = ['CROSS_COMPILE=' + cross, 'CHIP_ARCH=CV181X', 'ISP_SRC_RELEASE=0',
    'OSDRV_PATH=' + str(Path(os.environ['NANOKVM_OSDRV_SOURCE']).resolve()),
    'KERNEL_PATH=' + str(Path(os.environ['NANOKVM_KERNEL_SOURCE']).resolve()),
    'TRD_INCLUDE_PATH=' + str(compat), 'TRD_LIB_INCLUDE_PATH=' + str(compat),
    # Keep mpi_param.mk's warning checks, OS/chip defines, and dependency flags.
    'OPT_LEVEL=' + flags + ' -I' + str(stage / 'include'), '-j' + jobs]
(mpi / 'lib').mkdir(exist_ok=True)
# Fresh extraction and output trees prevent stale archive members or objects.
# Explicit output targets bypass the racy/destructive vendor prepare recipes.
with tempfile.TemporaryDirectory(prefix='bin-link-', dir=out) as temp:
    temp = Path(temp)
    objects = temp / 'thirdparty-objects'
    objects.mkdir()
    members = set()
    for archive in ['libjson-c.a', 'libminiz.a']:
        path = stage / 'lib' / archive
        current = subprocess.check_output([cross + 'ar', 't', str(path)], text=True).splitlines()
        if any(Path(name).name != name for name in current) or len(current) != len(set(current)) or members.intersection(current):
            raise SystemExit('Unsafe or colliding archive member names')
        members.update(current)
        subprocess.run([cross + 'ar', 'x', str(path)], cwd=objects, check=True)
    results = []
    for module, name in [('modules/bin', 'cvi_bin'), ('modules/isp/cv181x/isp_bin', 'cvi_bin_isp')]:
        outputs = temp / name
        outputs.mkdir()
        archive = outputs / ('lib' + name + '.a')
        shared = outputs / ('lib' + name + '.so')
        subprocess.run(['make', '-C', str(mpi / module), *common, 'ODIR=' + str(outputs / 'obj'),
            'TMP_FOLDER_LIB=' + str(objects), 'TARGET_A=' + str(archive), 'TARGET_SO=' + str(shared),
            str(archive), str(shared)], check=True)
        results.extend([archive, shared])
    for path in results:
        shutil.copy2(path, mpi / 'lib' / path.name)
print('BIN and ISP_BIN compiled against pinned json-c/miniz. Hardware configuration qualification is separate.')
