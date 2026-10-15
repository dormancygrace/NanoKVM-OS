#!/usr/bin/env python3
"""Generate the SHA-1 or SHA-256 block of Go for T-Head cores.

Go builds riscv64 for rva20u64, where each RORW of Go's riscv64 SHA-1 and
SHA-256 assembly is three instructions (srliw, slliw, or) and each message
word is assembled from four byte loads. T-Head cores have XTheadBb and fast
misaligned loads: th.srriw rotates a word in one instruction and th.revw
reverses its bytes. This expands the macros of Go's file (from the pinned
GOROOT) and writes
  - RORW $k, rs, rd (or RORW $k, r)  as th.srriw rd, rs, k
  - the message word byte loads      as lwu + th.revw
as WORD directives, under the name blockC906. Each word comes from GNU as
(-march=rv64imac_xtheadbb) and must disassemble back to its mnemonic.

  python3 gen_sha_c906.py --binutils $B --goroot $GOROOT --algo sha256 [--check] sha256block_c906_riscv64.s
  python3 gen_sha_c906.py --binutils $B --goroot $GOROOT --algo sha1 [--check] sha1block_c906_riscv64.s
"""
import argparse
import re
import subprocess
import tempfile
from pathlib import Path

ALGOS = {
    # Go source, and the macro that loads message word index into the schedule.
    'sha256': ('crypto/internal/fips140/sha256/sha256block_riscv64.s', 'MSGSCHEDULE0'),
    'sha1': ('crypto/sha1/sha1block_riscv64.s', 'LOAD'),
}

p = argparse.ArgumentParser()
p.add_argument('--binutils', required=True, help='cross prefix, e.g. .../riscv64-buildroot-linux-musl-')
p.add_argument('--goroot', type=Path, required=True)
p.add_argument('--algo', choices=sorted(ALGOS), required=True)
p.add_argument('--check', action='store_true', help='compare with the file instead of writing it')
p.add_argument('output', type=Path)
a = p.parse_args()

rel, loader = ALGOS[a.algo]
src = (a.goroot / 'src' / rel).read_text()

# Message word index: lwu from p (misaligned allowed), byte reverse, store.
m = re.search(r'#define ' + loader + r'\(index\) \\\n(?:.*\\\n)*.*\n', src)
assert m, loader
src = src[:m.start()] + f'''#define {loader}(index) \\
	MOVWU	(index*4)(X29), X5; \\
	REVW_X5; \\
	MOVW	X5, (index*4)(X19)
''' + src[m.end():]

macros, body = {}, []
lines = src.split('\n')
i = 0
while i < len(lines):
    line = lines[i]
    if line.startswith('#define '):
        text = line
        while text.endswith('\\'):
            i += 1
            text = text[:-1] + '\n' + lines[i]
        head, _, mbody = text[len('#define '):].partition('\n')
        mm = re.match(r'(\w+)\(([^)]*)\)\s*$', head.strip())
        if mm:
            macros[mm.group(1)] = ([x.strip() for x in mm.group(2).split(',')], mbody)
        else:
            # An alias such as "#define FUNC4 FUNC2".
            alias, _, target = head.strip().partition(' ')
            assert target.strip() in macros, head
            macros[alias] = macros[target.strip()]
    else:
        body.append(line)
    i += 1


def expand(line):
    mm = re.match(r'\s*(\w+)\((.*)\)\s*;?\s*$', line)
    if not mm or mm.group(1) not in macros:
        return [line]
    params, mbody = macros[mm.group(1)]
    args = [x.strip() for x in mm.group(2).split(',')]
    assert len(args) == len(params), line
    for prm, arg in zip(params, args):
        mbody = re.sub(r'\b' + re.escape(prm) + r'\b', arg, mbody)
    out = []
    for part in mbody.replace('\n', ' ').split(';'):
        if part.strip():
            out.extend(expand('\t' + part.strip()))
    return out


def rorw(line):
    """(k, rs, rd) of RORW $k, Xrs, Xrd or RORW $k, Xr, else None."""
    m = re.match(r'\s*RORW\s+\$(\d+),\s*X(\d+)(?:,\s*X(\d+))?\s*$', line)
    if not m:
        return None
    rs = int(m.group(2))
    return int(m.group(1)), rs, int(m.group(3)) if m.group(3) else rs


expanded = [x for line in body for x in expand(line)]
assert not any(re.match(r'\s*(ROLW|ROL|ROR)\b', x) for x in expanded), 'unhandled rotate'
need = sorted({('srriw', rd, rs, k) for k, rs, rd in filter(None, map(rorw, expanded))} | {('revw', 5, 5, 0)})
with tempfile.TemporaryDirectory() as tmp:
    s, o = Path(tmp) / 'e.S', Path(tmp) / 'e.o'
    s.write_text(''.join(f'\tth.srriw x{rd}, x{rs}, {k}\n' if op == 'srriw' else f'\tth.revw x{rd}, x{rs}\n'
                         for op, rd, rs, k in need))
    subprocess.run([a.binutils + 'as', '-march=rv64imac_xtheadbb', '-o', str(o), str(s)], check=True)
    dump = subprocess.run([a.binutils + 'objdump', '-d', '-M', 'numeric', str(o)],
                          check=True, capture_output=True, text=True).stdout
words = re.findall(r'^\s*[0-9a-f]+:\s+([0-9a-f]{8})\s+(\S+)\s+(.*)$', dump, re.M)
assert len(words) == len(need), (len(words), len(need))
enc = {}
for (op, rd, rs, k), (word, mnem, operands) in zip(need, words):
    want = [f'x{rd}', f'x{rs}'] + ([str(k)] if op == 'srriw' else [])
    got = [x.strip() for x in operands.split(',')]
    assert mnem == f'th.{op}' and got == want, (op, rd, rs, k, mnem, operands)
    enc[(op, rd, rs, k)] = int(word, 16)

out = []
for line in expanded:
    r = rorw(line)
    if r:
        k, rs, rd = r
        out.append(f'\tWORD\t$0x{enc[("srriw", rd, rs, k)]:08x}\t// th.srriw X{rd}, X{rs}, {k}')
    elif line.strip() == 'REVW_X5':
        out.append(f'\tWORD\t$0x{enc[("revw", 5, 5, 0)]:08x}\t// th.revw X5, X5')
    else:
        out.append(line)
# Tables the original file defines (crypto/sha1's _K) stay there.
out = [x for x in out if not re.match(r'\s*(DATA|GLOBL)\s', x)]
text = '\n'.join(out)
assert len(re.findall(r'TEXT ·block\(SB\)', text)) == 1
text = text.replace('TEXT ·block(SB)', 'TEXT ·blockC906(SB)')
assert '//go:build !purego\n' in text
text = (f'// Code generated by gen_sha_c906.py --algo {a.algo} from Go\'s\n'
        f'// {rel}. DO NOT EDIT.\n'
        '// T-Head: RORW is th.srriw (XTheadBb); message words are lwu + th.revw.\n\n') + text
if a.check:
    if a.output.read_text() != text:
        raise SystemExit(f'{a.output} differs from the generated code')
    print(f'{a.output}: matches')
else:
    a.output.write_text(text)
    print(f'{a.output}: {sum("th.srriw" in x for x in out)} th.srriw, '
          f'{sum("th.revw" in x for x in out)} th.revw')
