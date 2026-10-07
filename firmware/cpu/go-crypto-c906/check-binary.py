#!/usr/bin/env python3
"""Check a generated XTheadVector kernel as linked into a Go linux/riscv64 binary.

The function's bytes are disassembled by GNU objdump as XTheadVector code and
its th.* instructions must equal, in order, the WORD comments of its source:
xorKeyStreamC906 and chacha_riscv64.s by default, else --symbol and --source.
Usage: check-binary.py --binutils PREFIX [--symbol SYM --source FILE] BINARY
"""
from pathlib import Path
import argparse, re, subprocess, sys, tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--binutils', required=True)
p.add_argument('--symbol', default='vendor/golang.org/x/crypto/chacha20.xorKeyStreamC906')
p.add_argument('--source', type=Path, default=Path(__file__).parent / 'chacha_riscv64.s')
p.add_argument('binary', type=Path)
args = p.parse_args()
SYMBOL = args.symbol
run = lambda *a: subprocess.run(a, check=True, capture_output=True, text=True).stdout

syms = run(args.binutils + 'nm', '-S', str(args.binary))
# The assembly function is SYMBOL.abi0 when an ABIInternal wrapper named SYMBOL
# exists (for a function value), else SYMBOL.
m = (re.search(r'^([0-9a-f]+) ([0-9a-f]+) [tT] ' + re.escape(SYMBOL) + r'\.abi0$', syms, re.M) or
     re.search(r'^([0-9a-f]+) ([0-9a-f]+) [tT] ' + re.escape(SYMBOL) + r'$', syms, re.M))
if not m:
    sys.exit(f'{SYMBOL} not found in {args.binary}')
addr, size = int(m.group(1), 16), int(m.group(2), 16)
sections = run(args.binutils + 'readelf', '-SW', str(args.binary))
text = re.search(r'\]\s+\.text\s+\S+\s+([0-9a-f]+)\s+([0-9a-f]+)\s+([0-9a-f]+)', sections)
vaddr, offset = int(text.group(1), 16), int(text.group(2), 16)
code = args.binary.read_bytes()[offset + addr - vaddr:][:size]
with tempfile.TemporaryDirectory() as d:
    src = Path(d) / 'f.s'
    (Path(d) / 'f.bin').write_bytes(code)
    src.write_text(f'.option norvc\n.incbin "{d}/f.bin"\n')
    obj = Path(d) / 'f.o'
    run(args.binutils + 'as', '-march=rv64gc_xtheadvector', '-o', str(obj), str(src))
    dump = run(args.binutils + 'objdump', '-D', '-j', '.text', str(obj))
got = [' '.join(x.split()) for x in re.findall(r'^\s*[0-9a-f]+:\s+[0-9a-f]+\s+(th\.\S+\s+\S+)', dump, re.M)]
want = [' '.join(x.split()) for x in re.findall(r'WORD\t\$0x[0-9a-f]+\t// (.*)$',
                                               args.source.read_text(), re.M)]
if got != want:
    for i, (g, w) in enumerate(zip(got, want)):
        if g != w:
            sys.exit(f'instruction {i}: binary has {g!r}, source {w!r}')
    sys.exit(f'{len(got)} th.* instructions in the binary, {len(want)} in the source')
print(f'{args.binary}: {SYMBOL} at {addr:#x}, {size} bytes, {len(got)} XTheadVector instructions match the source')
