#!/usr/bin/env python3
"""Generate platform/kernel/hot-text.lds, the order of the hot kernel functions (docs/KERNEL-HOT-TEXT.md).

Inputs: the ftrace function profiles of the paths (profile.sh on the device, debug kernel) and the build
directory of a release kernel with the hot-text patch (names, sizes, call graph, objects).

A path's functions are the ones that ran on every pass (hits, less those of the baseline run, at least half
the passes). Functions ftrace cannot see (lib/, arch/riscv/lib/ and the other objects built without it, and
assembly) join a path when a function of the path calls them directly, transitively. Functions on two or more
system-call paths form the shared block; then come each path's remaining functions, in PATHS order. The
shared block and the pipe path get a spare cache line after each function: measured, a pipe round trip took
21 % less time than with the functions packed, the other paths the same or better.

The pipe round trip is sensitive to small differences in this list (a few functions more or less in the spaced
part moved it by 15 %): measure a new list on the device (docs/KERNEL-HOT-TEXT.md) before replacing the one in
the repository."""
import argparse, bisect, json, re, subprocess, sys
from collections import defaultdict
from pathlib import Path

# path, minimum hits per pass, baseline. System-call paths: profile.sh runs 2000 passes (fault: 12800 pages,
# 128 per munmap), baseline "nop" (start and exit of the driver). Network: 12 MiB of 1448-byte segments in a
# 22 s window, a third of the segments, baseline "idle" (22 s of the idle system).
SEG = 12 * 2**20 // 1448
PATHS = [('pipe', 1000, 'nop'), ('read', 1000, 'nop'), ('write', 1000, 'nop'), ('stat', 1000, 'nop'),
         ('open', 1000, 'nop'), ('getppid', 1000, 'nop'), ('fault', 6400, 'nop'),
         ('ethtx', SEG // 3, 'idle'), ('ethrx', SEG // 3, 'idle'), ('wifitx', SEG // 3, 'idle'),
         ('wifirx', SEG // 3, 'idle')]
SMALL = [p for p, _, b in PATHS if b == 'nop']
SPACED = {'shared', 'pipe'}
# objects built without ftrace (their Makefiles remove CC_FLAGS_FTRACE)
UNTRACED = re.compile(r'^(lib/|arch/riscv/lib/|arch/riscv/kernel/(alternative|cpufeature|patch|sbi|sbi_ecall|'
                      r'return_address)\.o$|arch/riscv/mm/(init|cacheflush)\.o$|kernel/irq_work\.o$)')
COLD = re.compile(r'panic|_bug|warn|printk|__stack_chk_fail|dump|report|oops|die|^__ubsan|fortify')


def run(*cmd):
    return subprocess.run(cmd, capture_output=True, text=True, check=True).stdout


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument('--kbuild', type=Path, required=True, help='kernel build directory (O=)')
    ap.add_argument('--profiles', type=Path, required=True, help='directory of fprof-PATH.txt')
    ap.add_argument('--cross', required=True, help='toolchain prefix, e.g. .../riscv64-buildroot-linux-musl-')
    ap.add_argument('--out', type=Path, required=True)
    a = ap.parse_args()
    vml = a.kbuild / 'vmlinux'

    size, addr = {}, {}
    for line in run(a.cross + 'nm', '-nS', str(vml)).splitlines():
        f = line.split()
        if len(f) == 4 and f[2] in 'tT':
            size.setdefault(f[3], int(f[1], 16)); addr.setdefault(f[3], int(f[0], 16))
        elif len(f) == 3:
            addr.setdefault(f[2], int(f[0], 16))
    names = sorted(size)

    def resolve(n):  # trace_stat cuts names at 30 characters
        if n in size or len(n) < 30:
            return n
        i = bisect.bisect_left(names, n); c = []
        while i < len(names) and names[i].startswith(n):
            c.append(names[i]); i += 1
        return c[0] if len(c) == 1 else n

    def load(p):
        h = {}
        for line in open(a.profiles / f'fprof-{p}.txt'):
            m = re.match(r'\s+(\S+)\s+(\d+)\s', line)
            if m and m[1] != 'Function':
                n = resolve(m[1])
                if n in size:
                    h[n] = h.get(n, 0) + int(m[2])
        return h

    # direct callees (jal, auipc+jalr, tail jumps to a function start)
    starts = {v: k for k, v in addr.items() if k in size}
    cg, cur = defaultdict(set), None
    for line in run(a.cross + 'objdump', '-d', '--no-show-raw-insn', '-j', '.text', str(vml)).splitlines():
        m = re.match(r'^[0-9a-f]+ <([^>]+)>:', line)
        if m:
            if not m[1].startswith('.L'):
                cur = m[1]
            continue
        m = re.match(r'^\s*[0-9a-f]+:\s+(\S+)\s+(.*)$', line)
        if m and cur and (m[1] in ('jal', 'j', 'jalr') or m[1].startswith('b')):
            t = re.search(r'([0-9a-f]+) <([^>+]+)>', m[2])
            if t and int(t[1], 16) in starts and starts[int(t[1], 16)] != cur:
                cg[cur].add(starts[int(t[1], 16)])

    members = run(a.cross + 'ar', 't', str(a.kbuild / 'vmlinux.a')).split() + \
        run(a.cross + 'ar', 't', str(a.kbuild / 'lib/lib.a')).split() + \
        run(a.cross + 'ar', 't', str(a.kbuild / 'arch/riscv/lib/lib.a')).split()
    # thin archives list their members by the path given to ar: relative to the build directory
    members = [str(Path(o).relative_to(a.kbuild)) if Path(o).is_absolute() else o for o in members]
    untraced, asm = set(), {}
    for o in members:
        if not o.startswith('arch/riscv/') and not UNTRACED.match(o):
            continue
        for line in run(a.cross + 'objdump', '-t', str(a.kbuild / o)).splitlines():
            f = line.split()
            if len(f) >= 6 and f[2] == 'F' and f[3].startswith('.text'):
                if UNTRACED.match(o):
                    untraced.add(f[-1])
                if f[3] == '.text':  # assembly or explicit .text: placed by object
                    asm[f[-1]] = o
    untraced |= set(asm)

    def closure(fs):
        out, todo = set(), list(fs)
        while todo:
            for c in cg.get(todo.pop(), ()):
                if c in untraced and c not in out and c in size and not COLD.search(c):
                    out.add(c); todo.append(c)
        return out

    base = {b: load(b) for b in ('nop', 'idle')}
    sets, hits = {}, {}
    for p, th, b in PATHS:
        h = {n: v - base[b].get(n, 0) for n, v in load(p).items()}
        hits[p] = h
        sets[p] = {n for n, v in h.items() if v >= th}
        sets[p] |= closure(sets[p])
    # placed by vmlinux.lds.S before the list: exception entry, noinstr text, scheduler core
    pre = {n for n in size if addr['_stext'] <= addr[n] < addr['__hot_entry_end']}
    count = defaultdict(int)
    for p in SMALL:
        for n in sets[p] - pre:
            count[n] += 1
    shared = sorted((n for n, c in count.items() if c >= 2), key=lambda n: (-count[n], n))
    shared += sorted(closure(pre) - pre - set(shared))
    chunks = [('shared', shared)]
    placed = set(shared) | pre
    for p, _, _ in PATHS:
        own = sorted(sets[p] - placed, key=lambda n: (-hits[p].get(n, 0), n))
        placed |= set(own); chunks.append((p, own))

    kib = lambda ns: sum(size[n] for n in ns) / 1024
    lines, objs = [], set()
    for p, ns in chunks:
        print(f'{p:8} {len(ns):4} functions {kib(ns):6.1f} KiB', file=sys.stderr)
        lines.append(f'\t\t/* {p} */')
        for n in ns:
            if n in asm:
                if asm[n] in objs:
                    continue
                objs.add(asm[n]); entry = f'*:{asm[n]}(.text)'
            else:
                entry = f'*(.text.{n})'
            if p in SPACED:
                lines.append('\t\t. = ALIGN(. + 62, 64);')
            # KEEP: the order must not move when --gc-sections drops a
            # listed function that has no caller in this configuration.
            lines.append(f'\t\tKEEP({entry})')
    a.out.write_text('/* SPDX-License-Identifier: GPL-2.0-only */\n'
                     '/* Measured order of the hot kernel functions (docs/KERNEL-HOT-TEXT.md). */\n'
                     + '\n'.join(lines) + '\n')


if __name__ == '__main__':
    main()
