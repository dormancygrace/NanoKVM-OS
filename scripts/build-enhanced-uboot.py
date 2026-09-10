#!/usr/bin/env python3
"""Build the SD-autoboot U-Boot from the patched SG2002 source tree."""
from pathlib import Path
import argparse, os, shutil, subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
p.add_argument('--cross-compile', required=True)
p.add_argument('--jobs', type=int, default=os.cpu_count())
a = p.parse_args()
repo = Path(__file__).resolve().parents[1]
source, output = a.source.resolve(), a.output.resolve()
if source == output:
    p.error('Use a separate build output directory')
if output.exists() and any(output.iterdir()):
    p.error('Output directory must be new or empty')
output.mkdir(parents=True, exist_ok=True)
shutil.copyfile(repo / 'firmware/uboot/nanokvm_enhanced_defconfig', output / '.config')
make = ['make', '-C', str(source), 'O=' + str(output), 'ARCH=riscv',
        'CROSS_COMPILE=' + a.cross_compile]
subprocess.run(make + ['olddefconfig'], check=True)
subprocess.run(make + ['-j' + str(a.jobs)], check=True)
print(output / 'u-boot.bin')
