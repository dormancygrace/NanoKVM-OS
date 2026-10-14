#!/usr/bin/env python3
"""Host contract test of libvenc's stream pack mappings; no hardware.

Compiles modules/venc/src/cvi_venc.c and modules/sys/src/devmem.c of the
prepared NANOKVM_MPI_SOURCE tree (platform/build.sh native) with
firmware/probes/venc-pack-map-contract.c, which fakes the encoder driver and
/dev/mem, and runs it with persistent windows and with
NANOKVM_VENC_PACK_WINDOWS=0. A hang fails through the fixture's alarm and
the timeout here.
"""
from pathlib import Path
import argparse
import os
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument('--output', type=Path, help='Keep the binary and logs here')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
sources = [repo/'firmware/probes/venc-pack-map-contract.c', mpi/'modules/venc/src/cvi_venc.c',
           mpi/'modules/sys/src/devmem.c']
flags = ['-std=gnu11', '-O1', '-g', '-Wall', '-Wno-unused-function', '-U_FORTIFY_SOURCE', '-D__CV181X__',
         '-I'+str(mpi/'include'), '-I'+str(mpi/'modules/sys/include'), '-pthread']

def run(out, name, extra, env):
    binary = out/name
    subprocess.run([os.environ.get('CC', 'gcc'), *flags, *extra, *map(str, sources), '-o', str(binary)], check=True)
    for mode, mode_env in (('windows', {}), ('single', {'NANOKVM_VENC_PACK_WINDOWS': '0'})):
        result = subprocess.run([str(binary), mode], env={**os.environ, **env, **mode_env},
                                capture_output=True, text=True, timeout=300)
        print(result.stdout + result.stderr, end='')
        if result.returncode:
            raise SystemExit(f'{name} {mode}: exit {result.returncode}')

with tempfile.TemporaryDirectory() as temporary:
    out = args.output.resolve() if args.output else Path(temporary)
    out.mkdir(parents=True, exist_ok=True)
    run(out, 'venc-pack-map-contract', [], {})
print('venc pack map contract passed')
