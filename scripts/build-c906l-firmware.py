#!/usr/bin/env python3
"""Build the program of the SG2002's small core (C906L) for nanokvm_c906l.

platform/modules/c906l/firmware/ with libopus (float, plain C: the C906L has
no vector unit) from the archive pinned in platform/sources.lock, compiled with the
smallcore CPU profile as a static PIE at 0; start.S applies its
R_RISCV_RELATIVE relocations, the only kind allowed here. The math functions
come from the toolchain's musl. The output is the raw image the module
embeds.
"""
import argparse
import hashlib
import re
import shlex
import subprocess
import sys
import tarfile
import tempfile
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
import nanokvm_cpu_profile  # noqa: E402

STACK = 256 * 1024		# Opus allocates its work arrays on the stack (VAR_ARRAYS)
OPUS_DEFINES = ['-DOPUS_BUILD', '-DVAR_ARRAYS', '-DHAVE_LRINTF', '-DHAVE_LRINT']


def run(cmd, **kw):
    subprocess.run(cmd, check=True, **kw)


def opus_sources(tree):
    out = []
    for mk, names in (('celt_sources.mk', ['CELT_SOURCES']), ('silk_sources.mk', ['SILK_SOURCES', 'SILK_SOURCES_FLOAT']),
                      ('opus_sources.mk', ['OPUS_SOURCES', 'OPUS_SOURCES_FLOAT'])):
        text = (tree / mk).read_text().replace('\\\n', ' ')
        for name in names:
            out += re.search(r'^' + name + r'\s*=(.*)$', text, re.M).group(1).split()
    return out


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cross', required=True, help='toolchain prefix, e.g. .../riscv64-buildroot-linux-musl-')
    p.add_argument('--opus-archive', required=True, type=Path)
    p.add_argument('--output', required=True, type=Path)
    p.add_argument('--jobs', type=int, default=8)
    a = p.parse_args()
    lock = next(line.split() for line in (ROOT / 'platform/sources.lock').read_text().splitlines()
                if line.split()[:1] == ['opus'])
    if hashlib.sha256(a.opus_archive.read_bytes()).hexdigest() != lock[2]:
        sys.exit('c906l: Opus archive hash mismatch')
    src = ROOT / 'platform/modules/c906l'
    header = (src / 'c906l.h').read_text()
    program_max = int(re.search(r'#define C906L_PROGRAM_MAX\s+(0x[0-9a-f]+)', header).group(1), 16)
    profile = nanokvm_cpu_profile.flags('smallcore')
    common = profile + ['-mcmodel=medany', '-fPIE', '-fno-stack-protector', '-ffunction-sections', '-fdata-sections',
                        '-fno-tree-loop-distribute-patterns']
    with tempfile.TemporaryDirectory() as t:
        t = Path(t)
        with tarfile.open(a.opus_archive) as tar:
            tar.extractall(t, filter='data')
        opus = next(t.glob('opus-*'))
        opus_flags = common + OPUS_DEFINES + [f'-ffile-prefix-map={opus}=./opus'] + \
            ['-I' + str(opus / d) for d in ('include', 'celt', 'silk', 'silk/float', '.')]
        own_flags = common + ['-Wall', '-Werror', '-I' + str(src), '-I' + str(opus / 'include')]
        jobs = [(own_flags, src / 'firmware/start.S'), (own_flags, src / 'firmware/main.c')]
        jobs += [(opus_flags, opus / f) for f in opus_sources(opus)]

        def compile_one(job):
            flags, s = job
            o = t / 'obj' / (str(s.relative_to(t if s.is_relative_to(t) else src)).replace('/', '_') + '.o')
            run([a.cross + 'gcc', *flags, '-c', str(s), '-o', str(o)])
            return str(o)
        (t / 'obj').mkdir()
        with ThreadPoolExecutor(a.jobs) as ex:
            objs = list(ex.map(compile_one, jobs))
        sysroot = subprocess.run([a.cross + 'gcc', '-print-sysroot'], check=True, capture_output=True,
                                 text=True).stdout.strip()
        elf = t / 'c906l.elf'
        run([a.cross + 'gcc', *profile, '-static-pie', '-nostdlib', '-nostartfiles',
             '-Wl,--gc-sections,--no-relax,-z,norelro,--no-warn-rwx-segments,--build-id=none',
             f'-Wl,--defsym=STACK={STACK},--defsym=PROGRAM_MAX={program_max}',
             '-T', str(src / 'firmware/fw.ld'), '-o', str(elf), *objs, sysroot + '/usr/lib/libc.a', '-lgcc'])
        relocs = subprocess.run([a.cross + 'readelf', '-W', '-r', str(elf)], check=True, capture_output=True,
                                text=True).stdout
        kinds = set(re.findall(r'^\s*[0-9a-f]+\s+[0-9a-f]+\s+(R_RISCV_\w+)', relocs, re.M))
        if kinds - {'R_RISCV_RELATIVE'}:
            sys.exit('c906l: relocations start.S does not apply: ' + ', '.join(sorted(kinds)))
        undefined = subprocess.run([a.cross + 'nm', '-u', str(elf)], check=True, capture_output=True, text=True).stdout
        if undefined.strip():
            sys.exit('c906l: undefined symbols:\n' + undefined)
        a.output.parent.mkdir(parents=True, exist_ok=True)
        run([a.cross + 'objcopy', '-O', 'binary', str(elf), str(a.output)])
    print('c906l program:', a.output, a.output.stat().st_size, 'bytes;', shlex.join(profile))


if __name__ == '__main__':
    main()
