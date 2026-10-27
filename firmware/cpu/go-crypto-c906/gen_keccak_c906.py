#!/usr/bin/env python3
"""Generate Keccak-f[1600] for the T-Head C906 from CRYPTOGAMS keccak1600-riscv.pl.

CRYPTOGAMS (BSD-3-Clause, see CRYPTOGAMS-LICENSE) keeps all 25 lanes in
registers and uses lane complementing when Zbb is absent. The C906 has no
Zbb `andn`, but XTheadBb has `th.srri`, so this generator takes the no-Zbb
round and replaces each shift/shift/or rotate with one `th.srri`.

For Go, x27 (g) must not be touched: the lane CRYPTOGAMS keeps there moves
to x1, and the round-constant pointer moves from x1 into the frame.

Outputs:
  --go FILE    Go assembly (keccakF1600C906), th.srri as WORD encodings
  --gnu FILE   GNU assembly of the same code, for host/qemu differential tests
  --check FILE regenerate the Go output and compare it with FILE
"""
import argparse
import hashlib
import re
import subprocess
import sys
import tempfile
from pathlib import Path

# https://github.com/dot-asm/cryptogams/blob/<commit>/riscv/keccak1600-riscv.pl
CRYPTOGAMS_COMMIT = '4acc4a308eab6049d1cf1c7ba61b353a36ce0e2d'
CRYPTOGAMS_SHA256 = '4b3d783ff0c651b00ac32eb13decc1f4c054d457f143f477be3f0f944718dcc5'

ALLOWED = {'xor', 'or', 'and', 'not', 'ld', 'sd', 'add', 'addi', 'bne', 'th.srri'}


def preprocess(perl, cc):
    """Run the perlasm and the C preprocessor for the no-Zbb, fast-misaligned path."""
    with tempfile.TemporaryDirectory() as tmp:
        s = Path(tmp) / 'keccak.S'
        subprocess.run(['perl', str(perl), '64', str(s)], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        # -undef: the same output from a host cpp or a riscv64 compiler.
        out = subprocess.run([cc, '-E', '-P', '-undef', '-x', 'assembler-with-cpp',
                              '-D__riscv', '-D__riscv_xlen=64', '-D__SIZEOF_POINTER__=8',
                              '-D__riscv_misaligned_fast', str(s)],
                             check=True, capture_output=True, text=True).stdout
    core, on = [], False
    for line in out.splitlines():
        if line.startswith('__KeccakF1600:'):
            on = True
            continue
        if on and re.match(r'\s*\.size\s+__KeccakF1600', line):
            break
        if on:
            core.append(line)
    if not core:
        sys.exit('__KeccakF1600 not found')
    return core


def parse(lines):
    """Return [('label', name)] / [(op, [args])] with comments stripped."""
    out = []
    for line in lines:
        line = line.split('#', 1)[0].strip()
        if not line:
            continue
        if line.endswith(':'):
            out.append(('label', line[:-1]))
            continue
        op, _, rest = line.partition(' ')
        args = [a.strip() for a in rest.split(',')] if rest.strip() else []
        out.append((op.strip(), args))
    return out


def imm(text):
    return int(eval(text, {}, {}))  # CRYPTOGAMS writes immediates such as 64-44


def writes(ins):
    op, a = ins
    if op in ('sd', 'bne', 'label', 'ret'):
        return None
    return a[0] if a else None


def reads(ins):
    op, a = ins
    if op == 'label':
        return set()
    if op == 'sd':
        return {a[0], a[1].split('(')[1].rstrip(')')}
    if op == 'ld':
        return {a[1].split('(')[1].rstrip(')')}
    if op == 'bne':
        return {a[0], a[1]}
    return {x for x in a[1:] if re.fullmatch(r'x\d+', x)}


def dead_after(code, idx, reg):
    """True if reg is written before it is read after code[idx] (to the loop end)."""
    for ins in code[idx + 1:]:
        if ins[0] == 'label':
            continue
        if reg in reads(ins):
            return False
        if writes(ins) == reg:
            return True
    return False


def fuse_rotl1_xor(code):
    """Theta: add T,S,S / xor D,D,X / srl U,S,63 / xor D,D,U  ->  th.srri V,S,63 / xor D,D,X'.

    Two layouts occur: the shifted halves are XORed into D one after the
    other, either with the left half in a temporary (A == T, X == T) or in D
    itself (A == D). The rotate result goes to the register that held the
    left half; the srl temporary must be dead afterwards.
    """
    code = list(code)
    fused = 0
    k = 0
    while k + 3 < len(code):
        (o1, a1), (o2, a2), (o3, a3), (o4, a4) = code[k:k + 4]
        if (o1 == 'add' and a1[1] == a1[2] and o2 == 'xor' and o3 in ('srl', 'srli')
                and imm(a3[2]) == 63 and a3[1] == a1[1] and o4 == 'xor' and a4[2] == a3[0]
                and a4[0] == a4[1] == a2[0] == a2[1]):
            left, src, d, x, u = a1[0], a1[1], a2[0], a2[2], a3[0]
            if left == x and left != d:
                # T = S<<1; D ^= T; U = S>>63; D ^= U   ->  T = rotl(S,1); D ^= T
                new = [('th.srri', [left, src, '63']), ('xor', [d, d, left])]
                changed = {u} - {left}
            elif left == d:
                # D = S<<1; D ^= X; U = S>>63; D ^= U   ->  D = rotl(S,1); D ^= X
                if x == src:
                    k += 1
                    continue
                new = [('th.srri', [d, src, '63']), ('xor', [d, d, x])]
                changed = {u}
            else:
                k += 1
                continue
            for reg in changed:
                if not dead_after(code, k + 3, reg):
                    sys.exit(f'rotl1 temporary {reg} is live after {code[k:k + 4]}')
            code[k:k + 4] = new
            fused += 1
        k += 1
    return code, fused


def fuse_rotates(code):
    """Replace sll/add + srl + or rotate idioms by th.srri, verifying data flow."""
    code, fused = fuse_rotl1_xor(code)
    code = list(code)
    i = 0
    while i < len(code):
        op, a = code[i]
        if op != 'or' or a[0] != a[1]:
            i += 1
            continue
        d, t = a[0], a[2]
        # Find the shift-left (sll d, s, r or add d, s, s) and srl t, s, 64-r before i.
        left = right = None
        for j in range(i - 1, max(-1, i - 40), -1):
            pj, aj = code[j]
            if left is None and writes(code[j]) == d:
                if pj in ('sll', 'slli'):
                    left = (j, aj[1], imm(aj[2]))
                elif pj == 'add' and aj[1] == aj[2]:
                    left = (j, aj[1], 1)
                else:
                    break
            if right is None and writes(code[j]) == t:
                if pj in ('srl', 'srli'):
                    right = (j, aj[1], imm(aj[2]))
                else:
                    break
            if left and right:
                break
        if not (left and right) or left[1] != right[1] or left[2] + right[2] != 64:
            i += 1
            continue
        (jl, src, r), (jr, _, _) = left, right
        lo, hi = min(jl, jr), max(jl, jr)
        # Source must keep its value between the two shifts, except when the
        # srl itself overwrites it last (t == src, the srl after the sll).
        ok = True
        for k in range(lo + 1, i):
            if k in (jl, jr):
                continue
            w, rd = writes(code[k]), reads(code[k])
            if w in (d, t) or (k < hi and w == src) or d in rd or t in rd:
                ok = False
                break
        if t == src and jr < jl:
            ok = False  # srl clobbered the source before the sll read it
        if not ok:
            sys.exit(f'unsafe rotate pattern at {code[i]}')
        # Without the srl, t keeps its old value (the unshifted source when
        # t == src); that is only safe if t is dead after the or.
        if t != d and not dead_after(code, i, t):
            sys.exit(f'rotate temporary {t} is live after {code[i]}')
        code[jl] = ('th.srri', [d, src, str(64 - r)])
        for k in sorted((jr, i), reverse=True):
            del code[k]
        fused += 1
        i = jl + 1
    return code, fused


# ---- encodings ---------------------------------------------------------

def th_srri(rd, rs1, sh):
    return (0b000100 << 26) | (sh << 20) | (rs1 << 15) | (0b001 << 12) | (rd << 7) | 0x0b


def regno(x):
    return int(x[1:])


def check_encodings(text, binutils):
    """Assemble every th.srri mnemonic with GNU as and compare it with our WORD."""
    words = re.findall(r'WORD\t\$(0x[0-9a-f]+)\t// th\.srri X(\d+), X(\d+), (\d+)', text)
    with tempfile.TemporaryDirectory() as tmp:
        src, obj = Path(tmp) / 'e.s', Path(tmp) / 'e.o'
        src.write_text(''.join(f'th.srri x{rd}, x{rs}, {sh}\n' for _, rd, rs, sh in words))
        subprocess.run([binutils + 'as', '-march=rv64gc_xtheadbb', '-o', str(obj), str(src)], check=True)
        dump = subprocess.run([binutils + 'objdump', '-d', '-M', 'no-aliases', str(obj)],
                              check=True, capture_output=True, text=True).stdout
    got = re.findall(r'^\s+[0-9a-f]+:\s+([0-9a-f]{8})\s', dump, re.M)
    want = [w[2:] for w, *_ in words]
    if got != want:
        sys.exit(f'th.srri encodings differ from GNU as: {list(zip(want, got))[:4]}')
    return len(set(want))


# ---- Go output ---------------------------------------------------------

FRAME = 128          # CRYPTOGAMS scratch 0..79 plus our slots
PTR, END, LRSAVE, STATE = 80, 88, 96, 104
GO_REG = {f'x{n}': f'X{n}' for n in range(32)}


def go_lines(core):
    """Translate the fused core into Go assembly; x27 -> x1, iotas pointer in the frame."""
    out = []
    ren = lambda r: 'X1' if r == 'x27' else GO_REG[r]
    body = core
    # Drop CRYPTOGAMS' prologue/epilogue around .Loop; we build our own.
    start = next(k for k, ins in enumerate(body) if ins == ('label', '.Loop'))
    stop = next(k for k, ins in enumerate(body) if ins[0] == 'bne')
    loop = body[start + 1:stop]
    # Tail of the loop: the iotas load/advance through x1 must become frame based.
    new = []
    for op, a in loop:
        if op == 'ld' and a[1] == '0(x1)':
            continue          # replaced below
        if op == 'add' and a[0] == 'x1' and a[1] == 'x1':
            continue
        if op == 'ld' and a[1] == '80+8(x2)':
            continue          # end pointer, replaced below
        if op == 'xor' and a == ['x7', 'x7', 'x28']:
            continue          # A[0][0] ^= iota, emitted below
        new.append((op, a))
    for op, a in new:
        if op == 'label':
            out.append(f'{a}:')
        elif op == 'th.srri':
            rd, rs, sh = a
            rdg = 1 if rd == 'x27' else regno(rd)
            rsg = 1 if rs == 'x27' else regno(rs)
            out.append(f'\tWORD\t${th_srri(rdg, rsg, int(sh)):#010x}\t// th.srri {ren(rd)}, {ren(rs)}, {sh}')
        elif op in ('xor', 'or', 'and'):
            out.append(f'\t{op.upper()}\t{ren(a[2])}, {ren(a[1])}, {ren(a[0])}')
        elif op == 'not':
            out.append(f'\tNOT\t{ren(a[1])}, {ren(a[0])}')
        elif op in ('ld', 'sd'):
            off, base = a[1].split('(')
            base = base.rstrip(')')
            if base != 'x2':
                sys.exit(f'unexpected base in {op} {a}')
            off = imm(off)
            if not 0 <= off < 80:
                sys.exit(f'unexpected frame offset {off}')
            mem = f'{off + 8}(X2)'    # Go keeps the saved LR slot at 0(X2)
            out.append(f'\tMOV\t{mem}, {ren(a[0])}' if op == 'ld' else f'\tMOV\t{ren(a[0])}, {mem}')
        else:
            sys.exit(f'untranslated instruction {op} {a}')
    return out


def go_file(core, fused):
    lanes = [f'X{r}' for r in range(7, 27)] + ['X1', 'X28', 'X29', 'X30', 'X31']
    complemented = [1, 2, 8, 12, 17, 20]
    f = lambda o: f'{o + 8}(X2)'
    L = []
    L.append('// Code generated by gen_keccak_c906.py from CRYPTOGAMS keccak1600-riscv.pl. DO NOT EDIT.')
    L.append(f'// Source: dot-asm/cryptogams {CRYPTOGAMS_COMMIT}, sha256 {CRYPTOGAMS_SHA256}')
    L.append('// Copyright (c) 2006, CRYPTOGAMS by <appro@openssl.org>. BSD-3-Clause; see CRYPTOGAMS-LICENSE.')
    L.append(f'// No-Zbb lane-complementing round; {fused} rotates fused into th.srri (XTheadBb).')
    L.append('// x27 (g) is untouched: its CRYPTOGAMS lane lives in X1, saved and restored here.')
    L.append('')
    L.append('//go:build !purego')
    L.append('')
    L.append('#include "textflag.h"')
    L.append('')
    L.append('// func keccakF1600C906(a *[200]byte)')
    L.append(f'TEXT ·keccakF1600C906(SB), NOSPLIT, ${FRAME}-8')
    L.append(f'\tMOV\tX1, {f(LRSAVE)}')
    L.append('\tMOV\ta+0(FP), X5')
    L.append(f'\tMOV\tX5, {f(STATE)}')
    L.append('\tMOV\t$·keccakC906RC(SB), X6')
    L.append(f'\tMOV\tX6, {f(PTR)}')
    L.append('\tADD\t$192, X6')
    L.append(f'\tMOV\tX6, {f(END)}')
    for k, r in enumerate(lanes):
        L.append(f'\tMOV\t{8 * k}(X5), {r}')
    for k in complemented:
        L.append(f'\tNOT\t{lanes[k]}, {lanes[k]}')
    # CRYPTOGAMS entry: offload A[4][0..1] (x27, x28) to 40/48.
    L.append(f'\tMOV\tX1, {f(40)}')
    L.append(f'\tMOV\tX28, {f(48)}')
    L.append('loop:')
    for line in go_lines(core):
        L.append(line.replace('.Loop:', 'loop:'))
    # Loop tail: A[0][0] ^= *iotas++, compare with end.
    L.append(f'\tMOV\t{f(PTR)}, X28')
    L.append('\tMOV\t0(X28), X6')
    L.append('\tADD\t$8, X28')
    L.append(f'\tMOV\tX28, {f(PTR)}')
    L.append(f'\tMOV\t{f(END)}, X5')
    L.append('\tXOR\tX6, X7, X7')
    L.append('\tBNE\tX28, X5, loop')
    # CRYPTOGAMS exit: reload A[4][0..1].
    L.append(f'\tMOV\t{f(40)}, X1')
    L.append(f'\tMOV\t{f(48)}, X28')
    for k in complemented:
        L.append(f'\tNOT\t{lanes[k]}, {lanes[k]}')
    L.append(f'\tMOV\t{f(STATE)}, X5')
    for k, r in enumerate(lanes):
        L.append(f'\tMOV\t{r}, {8 * k}(X5)')
    L.append(f'\tMOV\t{f(LRSAVE)}, X1')
    L.append('\tRET')
    L.append('')
    rc = [0x0000000000000001, 0x0000000000008082, 0x800000000000808A, 0x8000000080008000,
          0x000000000000808B, 0x0000000080000001, 0x8000000080008081, 0x8000000000008009,
          0x000000000000008A, 0x0000000000000088, 0x0000000080008009, 0x000000008000000A,
          0x000000008000808B, 0x800000000000008B, 0x8000000000008089, 0x8000000000008003,
          0x8000000000008002, 0x8000000000000080, 0x000000000000800A, 0x800000008000000A,
          0x8000000080008081, 0x8000000000008080, 0x0000000080000001, 0x8000000080008008]
    for k, v in enumerate(rc):
        L.append(f'DATA ·keccakC906RC+{8 * k}(SB)/8, ${v:#018x}')
    L.append('GLOBL ·keccakC906RC(SB), RODATA, $192')
    return '\n'.join(L) + '\n'


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('--print-url', action='store_true', help='print the pinned CRYPTOGAMS source URL and exit')
    p.add_argument('--cryptogams', type=Path, help='keccak1600-riscv.pl at the pinned commit')
    p.add_argument('--cc', default='cpp', help='C preprocessor driver (host cpp or a riscv64 compiler)')
    p.add_argument('--binutils', help='prefix of riscv64 GNU binutils, to verify the th.srri encodings')
    p.add_argument('--go', type=Path)
    p.add_argument('--check', type=Path)
    p.add_argument('--dump', action='store_true', help='print the fused core')
    a = p.parse_args()
    if a.print_url:
        print(f'https://raw.githubusercontent.com/dot-asm/cryptogams/{CRYPTOGAMS_COMMIT}/riscv/keccak1600-riscv.pl')
        return
    if not a.cryptogams:
        p.error('--cryptogams is required')
    digest = hashlib.sha256(a.cryptogams.read_bytes()).hexdigest()
    if digest != CRYPTOGAMS_SHA256:
        sys.exit(f'{a.cryptogams}: sha256 {digest}, pinned {CRYPTOGAMS_SHA256} (commit {CRYPTOGAMS_COMMIT})')
    core = parse(preprocess(a.cryptogams, a.cc))
    core, fused = fuse_rotates(core)
    loop_ops = [ins for ins in core if ins[0] != 'label']
    bad = {op for op, _ in loop_ops if op not in ALLOWED | {'ret', 'lla', 'slli'}}
    if bad:
        for k, ins in enumerate(core):
            if ins[0] in bad:
                print('\n'.join(f'  {n}: {core[n]}' for n in range(max(0, k - 6), min(len(core), k + 6))), file=sys.stderr)
                print('  ----', file=sys.stderr)
        sys.exit(f'unexpected instructions after fusion: {sorted(bad)}')
    print(f'fused {fused} rotates; core instructions {len(loop_ops)}', file=sys.stderr)
    if a.dump:
        for op, args in core:
            print(f'{op}:' if op == 'label' else f'\t{op}\t' + ', '.join(args))
    text = go_file(core, fused)
    if a.binutils:
        print(f'th.srri encodings match GNU as ({check_encodings(text, a.binutils)} distinct)', file=sys.stderr)
    if a.go:
        a.go.write_text(text)
    if a.check:
        if a.check.read_text() != text:
            sys.exit(f'{a.check} is stale; regenerate with gen_keccak_c906.py --go')
        print('check: up to date', file=sys.stderr)


if __name__ == '__main__':
    main()
