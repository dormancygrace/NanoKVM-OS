#!/usr/bin/env python3
"""Generate chacha_riscv64.s, the C906 ChaCha20 kernel for Go's vendored x/crypto.

Go's assembler has no XTheadVector (RVV 0.7.1) instructions, so every vector
instruction is emitted as WORD with its mnemonic in a comment. Each word is
computed twice: by the field encoder below, written from the T-Head
XTheadVector / RVV 0.7.1 instruction formats, and by GNU as from the Buildroot
toolchain (-march=rv64gc_xtheadvector). Generation fails if they differ or if
objdump does not decode a word back to the same th.* mnemonic.

Usage: gen_chacha_riscv64.py --binutils PREFIX (--output FILE | --check FILE)
PREFIX is e.g. .../host/bin/riscv64-buildroot-linux-musl- .
"""
from pathlib import Path
import argparse, re, subprocess, sys, tempfile

# Go register numbers -> ABI names used by GNU as / objdump.
ABI = {5: 't0', 6: 't1', 7: 't2', 8: 's0', 9: 's1', 10: 'a0', 11: 'a1', 12: 'a2', 13: 'a3',
       14: 'a4', 15: 'a5', 16: 'a6', 17: 'a7', 18: 's2', 19: 's3', 20: 's4', 21: 's5', 22: 's6',
       23: 's7', 24: 's8', 25: 's9', 26: 's10', 28: 't3', 29: 't4', 30: 't5'}

# ---------------------------------------------------------------- encoder ---
OP_V, LOAD_FP, STORE_FP = 0x57, 0x07, 0x27
OPIVV, OPMVV, OPIVI, OPIVX = 0, 2, 3, 4
FUNCT6 = {'vadd': 0b000000, 'vor': 0b001010, 'vxor': 0b001011, 'vmv': 0b010111,
          'vsll': 0b100101, 'vsrl': 0b101000}
SEW = {8: 0, 16: 1, 32: 2, 64: 3}
LMUL = {1: 0, 2: 1, 4: 2, 8: 3}


def opv(funct6, vm, vs2, rs1, funct3, vd):
    return funct6 << 26 | vm << 25 | vs2 << 20 | rs1 << 15 | funct3 << 12 | vd << 7 | OP_V


class V:
    """One vector instruction: GNU text plus the spec encoding."""

    def __init__(self, text, word):
        self.text, self.word = text, word


def vv(op, vd, vs2, vs1):
    return V(f'th.{op}.vv v{vd},v{vs2},v{vs1}', opv(FUNCT6[op], 1, vs2, vs1, OPIVV, vd))


def vi(op, vd, vs2, imm):
    assert 0 <= imm < 32  # vsll/vsrl take an unsigned 5 bit shift amount
    return V(f'th.{op}.vi v{vd},v{vs2},{imm}', opv(FUNCT6[op], 1, vs2, imm, OPIVI, vd))


def vx(op, vd, vs2, rs1):
    return V(f'th.{op}.vx v{vd},v{vs2},{ABI[rs1]}', opv(FUNCT6[op], 1, vs2, rs1, OPIVX, vd))


def vmv_v_x(vd, rs1):
    return V(f'th.vmv.v.x v{vd},{ABI[rs1]}', opv(FUNCT6['vmv'], 1, 0, rs1, OPIVX, vd))


def vmv_v_v(vd, vs1):
    return V(f'th.vmv.v.v v{vd},v{vs1}', opv(FUNCT6['vmv'], 1, 0, vs1, OPIVV, vd))


def vid(vd):
    # VMUNARY0 (funct6 010110), OPMVV, vs1 = 10001.
    return V(f'th.vid.v v{vd}', opv(0b010110, 1, 0, 0b10001, OPMVV, vd))


def vsetvli(rd, rs1, sew, lmul):
    # 0.7.1 vtype: vediv[6:5] = 0 (d1), vsew[4:2], vlmul[1:0]; bit 31 = 0.
    vtype = SEW[sew] << 2 | LMUL[lmul]
    return V(f'th.vsetvli {ABI[rd]},{ABI[rs1]},e{sew},m{lmul},d1',
             vtype << 20 | rs1 << 15 | 0b111 << 12 | rd << 7 | OP_V)


def mem(opcode, nf, mop, vreg, rs1, rs2):
    # nf[31:29] = fields - 1, mop[28:26], vm = 1, lumop/sumop or stride
    # register, width 111 (SEW-wide elements).
    return (nf - 1) << 29 | mop << 26 | 1 << 25 | rs2 << 20 | rs1 << 15 | 0b111 << 12 | vreg << 7 | opcode


def vle(vd, rs1):
    return V(f'th.vle.v v{vd},({ABI[rs1]})', mem(LOAD_FP, 1, 0b000, vd, rs1, 0))


def vse(vs3, rs1):
    return V(f'th.vse.v v{vs3},({ABI[rs1]})', mem(STORE_FP, 1, 0b000, vs3, rs1, 0))


def vssseg(nf, vs3, rs1, rs2):
    # Strided segment store: segment j holds element j of v(vs3)..v(vs3+nf-1)
    # at rs1 + j*rs2.
    return V(f'th.vssseg{nf}e.v v{vs3},({ABI[rs1]}),{ABI[rs2]}', mem(STORE_FP, nf, 0b010, vs3, rs1, rs2))

# -------------------------------------------------------------- generator ---

COLUMNS = [(0, 4, 8, 12), (1, 5, 9, 13), (2, 6, 10, 14), (3, 7, 11, 15)]
DIAGONALS = [(0, 5, 10, 15), (1, 6, 11, 12), (2, 7, 8, 13), (3, 4, 9, 14)]
SIGMA = [0x61707865, 0x3320646e, 0x79622d32, 0x6b206574]

# Scalar registers: the fifth block's state x0..x15 lives in X5..X20.
# X27 (g), X31 (assembler temporary) and X1..X4 are not used.
S = list(range(5, 21))
TMP, ROUNDS, DST, SRC, LEN, CTR, KEY, NONCE, BUF = 21, 22, 23, 24, 25, 26, 28, 29, 30
# Vector registers: v0..v15 state words (lane j = block j), v16..v19 rotate
# temporaries, v20 the initial counters.
VTMP, VCTR = 16, 20
NAME = 'xorKeyStreamC906'
BLOCKS = 5
SIZE = 64 * BLOCKS


def vector_round(quads):
    """One ChaCha round, four quarter rounds, for the four vector blocks."""
    out = []

    def step(add, xor, rot):
        for q in quads:
            out.append(vv('vadd', q[add[0]], q[add[0]], q[add[1]]))
        for q in quads:
            out.append(vv('vxor', q[xor], q[xor], q[add[0]]))
        # XTheadVector has no rotate: shift left, shift right, or.
        for i, q in enumerate(quads):
            out.append(vi('vsll', VTMP + i, q[xor], rot))
        for q in quads:
            out.append(vi('vsrl', q[xor], q[xor], 32 - rot))
        for i, q in enumerate(quads):
            out.append(vv('vor', q[xor], q[xor], VTMP + i))
    step((0, 1), 3, 16)
    step((2, 3), 1, 12)
    step((0, 1), 3, 8)
    step((2, 3), 1, 7)
    return out


def scalar_round(quads):
    """The same round for the scalar block, with base RV64I rotates."""
    out = []

    def step(add, xor, rot):
        for q in quads:
            out.append(f'ADDW\tX{S[q[add[1]]]}, X{S[q[add[0]]]}')
        for q in quads:
            out.append(f'XOR\tX{S[q[add[0]]]}, X{S[q[xor]]}')
        for q in quads:
            r = S[q[xor]]
            out.extend([f'SLLIW\t${rot}, X{r}, X{TMP}', f'SRLIW\t${32 - rot}, X{r}', f'OR\tX{TMP}, X{r}'])
    step((0, 1), 3, 16)
    step((2, 3), 1, 12)
    step((0, 1), 3, 8)
    step((2, 3), 1, 7)
    return out


class Asm:
    def __init__(self):
        self.lines = []
        self.vectors = []

    def __call__(self, *items):
        for it in items:
            if isinstance(it, V):
                self.vectors.append(it)
            self.lines.append(it)

    def label(self, name):
        self.lines.append(name + ':')

    def render(self):
        out = []
        for l in self.lines:
            if isinstance(l, V):
                out.append(f'\tWORD\t$0x{l.word:08x}\t// {l.text}')
            elif l.endswith(':') or l.startswith('TEXT') or l.startswith('//') or l == '':
                out.append(l)
            else:
                out.append('\t' + l)
        return '\n'.join(out) + '\n'


def initial(a, i, r):
    """Load initial state word i (the fifth block's counter for i = 12) into r."""
    if i < 4:
        a(f'MOV\t${SIGMA[i]:#x}, X{r}')
    elif i < 12:
        a(f'MOVWU\t{4 * (i - 4)}(X{KEY}), X{r}')
    elif i == 12:
        a(f'ADDW\t$4, X{CTR}, X{r}')
    else:
        a(f'MOVWU\t{4 * (i - 13)}(X{NONCE}), X{r}')


def build():
    a = Asm()
    a(f'// func {NAME}(dst, src []byte, key *[8]uint32, nonce *[3]uint32, counter *uint32)')
    a(f'TEXT ·{NAME}(SB), 0, ${SIZE}-72')
    a(f'MOV\tdst_base+0(FP), X{DST}', f'MOV\tsrc_base+24(FP), X{SRC}', f'MOV\tsrc_len+32(FP), X{LEN}',
      f'MOV\tkey+48(FP), X{KEY}', f'MOV\tnonce+56(FP), X{NONCE}', f'MOV\tcounter+64(FP), X{TMP}',
      f'MOVWU\t(X{TMP}), X{CTR}', f'ADD\t$8, X2, X{BUF}')  # BUF: SIZE bytes of key stream
    a(f'BEQZ\tX{LEN}, done')
    a.label('loop')
    a(f'MOV\t$4, X{TMP}', vsetvli(ROUNDS, TMP, 32, 1))
    # Blocks CTR..CTR+3 in the vector lanes, block CTR+4 in scalar registers.
    for i in range(16):
        initial(a, i, S[i])
        if i != 12:
            a(vmv_v_x(i, S[i]))
    a(vid(VCTR), vx('vadd', VCTR, VCTR, CTR), vmv_v_v(12, VCTR))
    a(f'MOV\t$10, X{ROUNDS}')
    a.label('round')
    # One vector, one scalar instruction: the C906 issues the scalar one while
    # the 64-bit vector datapath still works on the 128-bit vector one.
    vec = vector_round(COLUMNS) + vector_round(DIAGONALS)
    sca = scalar_round(COLUMNS) + scalar_round(DIAGONALS)
    assert len(vec) == len(sca) == 160
    for v, s in zip(vec, sca):
        a(v, s)
    a(f'SUB\t$1, X{ROUNDS}', f'BNEZ\tX{ROUNDS}, round')
    # Add the initial state.
    for i in range(16):
        initial(a, i, TMP)
        if i == 12:
            a(vv('vadd', 12, 12, VCTR))
        else:
            a(vx('vadd', i, i, TMP))
        a(f'ADDW\tX{TMP}, X{S[i]}')
    # Key stream to BUF: block j at 64*j. Lane j of v(i) is word i of block j.
    for i in range(16):
        a(f'MOVW\tX{S[i]}, {256 + 4 * i}(X{BUF})')
    a(f'MOV\t$64, X{ROUNDS}', vssseg(8, 0, BUF, ROUNDS), f'ADD\t$32, X{BUF}, X{TMP}', vssseg(8, 8, TMP, ROUNDS))
    # dst = src ^ key stream with byte elements, so any alignment is fine.
    a(f'MOV\t${SIZE}, X5', f'MOV\tX{BUF}, X6')
    a.label('xorloop')
    a(vsetvli(7, 5, 8, 8), vle(0, SRC), vle(8, 6), vv('vxor', 0, 0, 8), vse(0, DST))
    a(f'ADD\tX7, X{SRC}', f'ADD\tX7, X{DST}', 'ADD\tX7, X6', 'SUB\tX7, X5', 'BNEZ\tX5, xorloop')
    a(f'ADDW\t${BLOCKS}, X{CTR}', f'SUB\t${SIZE}, X{LEN}', f'BNEZ\tX{LEN}, loop')
    a.label('done')
    a(f'MOV\tcounter+64(FP), X{TMP}', f'MOVW\tX{CTR}, (X{TMP})', 'RET')
    return a


HEADER = '''\
// Code generated by firmware/cpu/go-crypto-c906/gen_chacha_riscv64.py. DO NOT EDIT.

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

// ChaCha20 for the T-Head C906 (SG2002): five blocks per iteration. Four
// blocks are in XTheadVector lanes (VLEN 128, SEW 32, LMUL 1) with one state
// word per vector register; the fifth block is in scalar registers, its
// instructions interleaved with the vector ones.
//
// Vector registers are not preserved across calls, so all vector state is
// set up and consumed within this call, which calls nothing. Assembly
// functions are not asynchronously preempted; the kernel saves vector state
// across context switches and signals.
//
// Go's assembler has no RVV 0.7.1 encodings, so vector instructions are WORDs.
// The generator checks each against GNU as/objdump -march=rv64gc_xtheadvector.

#include "textflag.h"

'''


def verify(prefix, vectors):
    unique = {}
    for v in vectors:
        assert unique.setdefault(v.text, v.word) == v.word
    with tempfile.TemporaryDirectory() as d:
        src = Path(d) / 'v.s'
        src.write_text(''.join(t + '\n' for t in unique))
        obj = Path(d) / 'v.o'
        subprocess.run([prefix + 'as', '-march=rv64gc_xtheadvector', '-o', str(obj), str(src)], check=True)
        dump = subprocess.run([prefix + 'objdump', '-d', str(obj)], check=True, capture_output=True, text=True).stdout
    got = [(int(m.group(1), 16), ' '.join(m.group(2).split()))
           for m in re.finditer(r'^\s*[0-9a-f]+:\s+([0-9a-f]{8})\s+(.*)$', dump, re.M)]
    if len(got) != len(unique):
        sys.exit(f'objdump returned {len(got)} instructions for {len(unique)}')
    for (text, word), (asword, astext) in zip(unique.items(), got):
        if word != asword:
            sys.exit(f'{text}: encoder {word:#010x}, GNU as {asword:#010x}')
        if astext.replace(' ', '') != text.replace(' ', ''):
            sys.exit(f'{text}: objdump decodes {word:#010x} as {astext}')
    return len(unique)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('--binutils', required=True, help='GNU binutils prefix with XTheadVector support')
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument('--output', type=Path)
    g.add_argument('--check', type=Path)
    args = p.parse_args()
    a = build()
    n = verify(args.binutils, a.vectors)
    text = HEADER + a.render()
    if args.check:
        if args.check.read_text() != text:
            sys.exit(f'{args.check} is not up to date')
        print(f'{args.check}: up to date, {n} distinct vector encodings verified')
    else:
        args.output.write_text(text)
        print(f'{args.output}: {len(a.vectors)} vector instructions, {n} distinct encodings verified')


if __name__ == '__main__':
    main()
