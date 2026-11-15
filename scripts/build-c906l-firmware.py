#!/usr/bin/env python3
"""Build the program of the SG2002's small core (C906L) for nanokvm_c906l.

platform/modules/c906l/firmware/ with the GHASH of gen_ghash_table.py (one
8-bit table per key), compiled with the smallcore CPU profile (no vector unit)
as a static PIE at 0; start.S applies its R_RISCV_RELATIVE relocations, the
only kind allowed here. The output is the raw image the module embeds.
"""
import argparse
import re
import shlex
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
sys.path.insert(0, str(ROOT / 'firmware/cpu/go-crypto-c906'))
import nanokvm_cpu_profile  # noqa: E402
import gen_ghash_table  # noqa: E402

STACK = 4096


def run(cmd, **kw):
    subprocess.run(cmd, check=True, **kw)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cross', required=True, help='toolchain prefix, e.g. .../riscv64-buildroot-linux-musl-')
    p.add_argument('--output', required=True, type=Path)
    a = p.parse_args()
    src = ROOT / 'platform/modules/c906l'
    header = (src / 'c906l.h').read_text()
    program_max = int(re.search(r'#define C906L_PROGRAM_MAX\s+(0x[0-9a-f]+)', header).group(1), 16)
    flags = nanokvm_cpu_profile.flags('smallcore') + [
        '-mcmodel=medany', '-fPIE', '-ffreestanding', '-fno-stack-protector', '-ffunction-sections',
        '-fdata-sections', '-fno-tree-loop-distribute-patterns', '-Wall', '-Werror', '-I' + str(src)]
    with tempfile.TemporaryDirectory() as t:
        t = Path(t)
        (t / 'ghash1.S').write_text(gen_ghash_table.gen(1, 'ghash1', 'plain'))
        objs = []
        for s in (src / 'firmware/start.S', src / 'firmware/main.c', t / 'ghash1.S'):
            o = t / (s.name + '.o')
            run([a.cross + 'gcc', *flags, '-c', str(s), '-o', str(o)])
            objs.append(str(o))
        elf = t / 'c906l.elf'
        run([a.cross + 'gcc', *nanokvm_cpu_profile.flags('smallcore'), '-static-pie', '-nostdlib', '-nostartfiles',
             '-Wl,--gc-sections,--no-relax,-z,norelro,--no-warn-rwx-segments,--build-id=none',
             f'-Wl,--defsym=STACK={STACK},--defsym=PROGRAM_MAX={program_max}',
             '-T', str(src / 'firmware/fw.ld'), '-o', str(elf), *objs, '-lgcc'])
        relocs = subprocess.run([a.cross + 'readelf', '-W', '-r', str(elf)], check=True, capture_output=True,
                                text=True).stdout
        kinds = set(re.findall(r'^\s*[0-9a-f]+\s+[0-9a-f]+\s+(R_RISCV_\w+)', relocs, re.M))
        if kinds - {'R_RISCV_RELATIVE'}:
            sys.exit('c906l: relocations start.S does not apply: ' + ', '.join(sorted(kinds)))
        a.output.parent.mkdir(parents=True, exist_ok=True)
        run([a.cross + 'objcopy', '-O', 'binary', str(elf), str(a.output)])
    print('c906l program:', a.output, a.output.stat().st_size, 'bytes;', shlex.join(nanokvm_cpu_profile.flags('smallcore')))


if __name__ == '__main__':
    main()
