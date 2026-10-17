#!/usr/bin/env python3
"""Check the built mkfs.exfat for toolchain path leakage and runtime dependencies."""
import argparse
from pathlib import Path
import re
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--images', type=Path, required=True)
a = p.parse_args()
binary = a.images / 'tools/exfatprogs/mkfs.exfat'
dynamic = subprocess.check_output(['readelf', '-d', str(binary)], text=True)
strings = subprocess.check_output(['readelf', '-p', '.dynstr', str(binary)], text=True)
needed = set(re.findall(r'\(NEEDED\).*\[(.*?)\]', dynamic))
assert needed == {'libblkid.so.1', 'libc.so'}, needed
assert not re.search(r'\((?:RUNPATH|RPATH)\)', dynamic), dynamic
# patchelf --remove-rpath can leave the old absolute path as an unused string.
for line in strings.splitlines():
    m = re.match(r'\s*\[\s*[0-9a-f]+\]\s+(.*)', line)
    assert not m or '/' not in m.group(1), line
print('mkfs.exfat: expected shared dependencies; no RPATH or retained build paths')
