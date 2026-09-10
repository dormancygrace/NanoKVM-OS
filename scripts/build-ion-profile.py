#!/usr/bin/env python3
"""Create an isolated 48/62/64 MiB ION FIT profile without changing kernel sources.

The existing diagnostic kernel, initramfs and installed-loader layout are reused.
No deployment or bootloader update is performed by this script.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--kernel', type=Path, required=True)
parser.add_argument('--initramfs', type=Path, required=True)
parser.add_argument('--mkimage', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--mib', type=int, choices=[48, 62, 64], required=True)
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
root = args.output.resolve()
root.mkdir(parents=True, exist_ok=False)
kernel = args.kernel.resolve()
image_relative = Path('arch/riscv/boot/Image')
dtb_relative = Path('arch/riscv/boot/dts/sophgo/sg2002-nanokvm-enhanced.dtb')
staged_kernel = root/'kernel-input'
(staged_kernel/dtb_relative.parent).mkdir(parents=True)
(staged_kernel/image_relative).symlink_to(kernel/image_relative)
dtb = staged_kernel/dtb_relative
shutil.copyfile(kernel/dtb_relative, dtb)

def command(*argv):
    return subprocess.check_output(argv, text=True).strip()

before = command('dtc', '-q', '-I', 'dtb', '-O', 'dts', str(dtb))
original = int(command('fdtget', '-t', 'x', str(dtb), '/reserved-memory/ion', 'size'), 16)
assert original == 48*1024*1024, f'Unexpected baseline ION size: {original}'
memory = command('fdtget', '-t', 'x', str(dtb), '/memory@80000000', 'reg')
assert memory == '80000000 10000000', f'Review board memory layout: {memory}'
subprocess.run(['fdtput', '-t', 'x', str(dtb), '/reserved-memory/ion', 'size', f'{args.mib*1024*1024:x}'], check=True)
after = command('dtc', '-q', '-I', 'dtb', '-O', 'dts', str(dtb))
# The normalized tree must differ in this property only (not clocks, reserved
# firmware memory, devices, interrupts, boot arguments or driver bindings).
needle = '\t\t\tsize = <0x3000000>;'
assert before.count(needle) == 1
assert after == before.replace(needle, f'\t\t\tsize = <0x{args.mib*1024*1024:x}>;')
(root/'board-before.dts').write_text(before+'\n')
(root/'board-after.dts').write_text(after+'\n')
environment = dict(os.environ, NANOKVM_KERNEL_OUTPUT=str(staged_kernel),
    NANOKVM_INITRAMFS_OUTPUT=str(args.initramfs.resolve()), NANOKVM_FIT_OUTPUT=str(root/'fit'),
    NANOKVM_MKIMAGE=str(args.mkimage.resolve()), NANOKVM_KERNEL_COMPRESSION='zstd')
with (root/'build.log').open('w') as log:
    subprocess.run(['python3', str(repo/'scripts/build-enhanced-fit.py')], env=environment,
                   stdout=log, stderr=subprocess.STDOUT, check=True)
manifest = json.loads((root/'fit/manifest.json').read_text())
manifest.update(ion_profile_mib=args.mib, baseline_ion_bytes=original,
    ordinary_ram_reduction_bytes=args.mib*1024*1024-original,
    normalized_dtb_only_ion_size_changed=True,
    source_kernel_sha256=hashlib.sha256((kernel/image_relative).read_bytes()).hexdigest(),
    baseline_dtb_sha256=hashlib.sha256((kernel/dtb_relative).read_bytes()).hexdigest(),
    note='Dynamic reserved-memory allocation; actual heap base and size require boot verification.')
(root/'profile.json').write_text(json.dumps(manifest, indent=2)+'\n')
print(json.dumps(manifest, indent=2))
