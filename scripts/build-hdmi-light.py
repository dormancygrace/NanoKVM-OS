#!/usr/bin/env python3
"""Build the complete HDMI-light media bundle; never install or edit a device.

Requires prepared NANOKVM_MPI_SOURCE, NANOKVM_SENSOR_SOURCE,
NANOKVM_INIH_SOURCE, NANOKVM_BUILDROOT_OUTPUT and an empty or new
NANOKVM_HDMI_LIGHT_OUTPUT directory. NANOKVM_MPI_THIRDPARTY_OUTPUT supplies
the prepared BIN build's json-c/miniz license notices. Production builds
use this same six-library bundle.
"""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import sys
from nanokvm_mmf_link import VENDOR_LIBRARIES, audit_bundle

repo = Path(__file__).resolve().parent.parent
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
output = Path(os.environ['NANOKVM_HDMI_LIGHT_OUTPUT']).resolve()
cross = str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve()
            / 'host/bin/riscv64-buildroot-linux-musl-')
notices = Path(os.environ['NANOKVM_MPI_THIRDPARTY_OUTPUT']).resolve() / 'stage/share/licenses'
for path in (notices / 'json_c/COPYING', notices / 'miniz/LICENSE'):
    if not path.is_file():
        raise SystemExit('Prepared third-party notice missing: ' + str(path))
if output.exists() and any(output.iterdir()):
    raise SystemExit('Use an empty dedicated output directory for this experiment')
for key in ('NANOKVM_MPI_SOURCE', 'NANOKVM_SENSOR_SOURCE', 'NANOKVM_INIH_SOURCE',
            'NANOKVM_BUILDROOT_OUTPUT'):
    source = Path(os.environ[key]).resolve()
    if output == source or output in source.parents or source in output.parents:
        raise SystemExit('Experiment output must be separate from prepared inputs')
output.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, NANOKVM_MMF_PROFILE='hdmi-light',
           NANOKVM_MMF_OUTPUT=str(output / 'mmf'),
           NANOKVM_CAPTURE_OUTPUT=str(output / 'capture'))
for script in ('build-mmf.py', 'build-capture.py'):
    subprocess.run([sys.executable, str(repo / 'scripts' / script)], env=env, check=True)
bundle = output / 'dl_lib'
bundle.mkdir()
inputs = [output / 'mmf/libkvm_mmf.so', output / 'capture/libkvm.so']
inputs += [mpi / 'lib' / ('lib' + n + '.so') for n in VENDOR_LIBRARIES]
for path in inputs:
    subprocess.run([cross + 'strip', '--strip-unneeded', '-o', str(bundle / path.name), str(path)], check=True)
closure = audit_bundle(cross, bundle)
shutil.copytree(output / 'mmf/licenses', output / 'licenses')
shutil.copytree(notices, output / 'licenses', dirs_exist_ok=True)
# Third-party code remains under its original licenses, including static code.
for name in ('LICENSE', 'LICENSE.txt', 'COPYING'):
    if (mpi / name).is_file():
        shutil.copy2(mpi / name, output / ('cvi-mpi-' + name))
manifest = {
    'profile': 'hdmi-light', 'hardware_qualified': False,
    'files': {p.name: {'bytes': p.stat().st_size,
                     'sha256': hashlib.sha256(p.read_bytes()).hexdigest()}
              for p in sorted(bundle.glob('*.so'))},
    'elf_dependencies': closure,
    'note': 'Complete HDMI-light media set, not an overlay for a running process. '
            'No algorithm removal, ISA rewriting, RTOS dependency or device installation.',
}
manifest['total_bytes'] = sum(p['bytes'] for p in manifest['files'].values())
(output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print('HDMI-light media bundle:', manifest['total_bytes'], 'bytes; hardware qualification is separate.')
