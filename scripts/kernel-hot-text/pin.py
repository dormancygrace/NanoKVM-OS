#!/usr/bin/env python3
"""Pin the measured order of platform/kernel/hot-text.lds to the offsets it had in a measured kernel.

The order alone does not hold the layout: when a call becomes short enough for linker relaxation, or a function
is compiled a few bytes shorter, every function after it moves, and the I-cache sets of the hot paths change.
This rewrites each entry of the list to start at the offset
from the first entry that it has in VMLINUX ('. = MAX(., __hot_list + OFFSET);'; MAX because ld also sizes the
sections before relaxation, when they are longer). A function that grows past its slot pushes the next one and
the layout must be measured again.

Entries from the first slot ('__hot_<name> = .;', placed by hand after the measured list) on are copied as
they are. Assembly objects ('*:path.o(.text)') take the address most of their symbols share."""
import argparse
import re
import subprocess
from pathlib import Path


def run(*cmd):
    return subprocess.run(cmd, capture_output=True, text=True, check=True).stdout


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument('--kbuild', type=Path, required=True, help='build directory (O=) of the measured kernel')
    ap.add_argument('--cross', required=True, help='toolchain prefix, e.g. .../riscv64-buildroot-linux-musl-')
    ap.add_argument('--list', type=Path, required=True, help='hot-text.lds to pin (unpinned or pinned)')
    ap.add_argument('--out', type=Path, required=True)
    a = ap.parse_args()

    addr = {}
    for line in run(a.cross + 'nm', '-n', str(a.kbuild / 'vmlinux')).splitlines():
        f = line.split()
        if len(f) == 3 and f[1] in 'tT':
            addr.setdefault(f[2], int(f[0], 16))

    def where(entry):
        m = re.search(r'\*\(\.text\.(.+?)\)\)$', entry)
        if m:
            return addr.get(m[1])
        m = re.search(r'\*:(\S+?)\(\.text\)\)$', entry)
        if not m:
            return None
        found = []
        for line in run(a.cross + 'objdump', '-t', str(a.kbuild / m[1])).splitlines():
            f = line.split()
            if len(f) >= 6 and f[3] == '.text' and f[-1] in addr:
                found.append(addr[f[-1]])
        # a name can also be an alias of a C function elsewhere (memcpy of __memcpy)
        return min(set(found), key=lambda x: (-found.count(x), x)) if found else None

    text = a.list.read_text().splitlines()
    first = next(i for i, l in enumerate(text) if 'KEEP(' in l)
    # Retain the start of the list (its anchor and __hot_list) and the hand-placed slots verbatim.
    out = text[:first]
    if not any(l.strip() == '__hot_list = .;' for l in out):
        out.append('\t\t__hot_list = .;')
    base = last = None
    for i in range(first, len(text)):
        l = text[i]
        if re.match(r'\s*__hot_(?!list\b)\w+ = \.;$', l):
            out.extend(text[i:])
            break
        if re.match(r'\s*\. = (MAX\(\., __hot_list \+ 0x[0-9a-f]+\)|ALIGN\(\. \+ 62, 64\));$', l):
            continue
        if 'KEEP(' not in l:
            out.append(l)
            continue
        x = where(l)
        if base is None:
            base = last = x
        elif x is not None and x > last:
            out.append(f'\t\t. = MAX(., __hot_list + 0x{x - base:x});')
            last = x
        out.append(l)
    a.out.write_text('\n'.join(out) + '\n')


if __name__ == '__main__':
    main()
