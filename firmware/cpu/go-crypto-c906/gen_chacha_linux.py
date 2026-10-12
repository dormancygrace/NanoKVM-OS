#!/usr/bin/env python3
"""Generate chacha-riscv64-xtheadvector.S, ChaCha for the Linux kernel on the C906.

The same five-block kernel as the Go one (gen_chacha_riscv64.py): four blocks
in XTheadVector lanes, a fifth in scalar registers, their instructions
interleaved one to one. This output follows the kernel's C calling convention
and lib/crypto's ChaCha interface:

    void chacha_xtheadvector(struct chacha_state *state, const u8 *in, u8 *out,
                             size_t ngroups, int nrounds);

It processes ngroups * 320 bytes (in may equal out), takes the constants, key,
counter and nonce from the state, supports any even nrounds (20 for ChaCha20,
12 for ChaCha12) and advances state->x[12] by 5 per group. The caller holds
kernel_vector_begin(). The key stream buffer on the stack is wiped before
returning.

Vector instructions are emitted as .4byte words with their mnemonics in
comments, so the kernel needs no assembler support for XTheadVector. Every
word is checked against GNU as/objdump -march=rv64gc_xtheadvector.

Usage: gen_chacha_linux.py --binutils PREFIX (--output FILE | --check FILE)
For --check, FILE may also be platform/kernel/0040-*.patch.
"""
from pathlib import Path
import argparse
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import gen_chacha_riscv64 as G  # noqa: E402

G.ABI.update({0: 'zero', 1: 'ra', 2: 'sp', 27: 's11', 31: 't6'})
from gen_chacha_riscv64 import (COLUMNS, DIAGONALS, V, vector_round, vv, vx,  # noqa: E402
                                vmv_v_x, vmv_v_v, vid, vsetvli, vle, vse, vssseg, verify)

R = G.ABI
STATE, IN, OUT, NGROUPS, NROUNDS = 10, 11, 12, 13, 14          # a0..a4
# The fifth block's state words.
S = [5, 6, 7, 8, 9, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25]  # t0-t2 s0 s1 a5-a7 s2-s9
TMP, ROUNDS, CTR, XREM, XVL, XKS = 28, 29, 30, 26, 27, 31       # t3 t4 t5 s10 s11 t6
SAVED = [8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27]           # s0-s11
BLOCKS = 5
SIZE = 64 * BLOCKS
FRAME = SIZE + 8 * len(SAVED)                                    # 416, 16-byte aligned
VCTR = 20
NAME = 'chacha_xtheadvector'
assert FRAME % 16 == 0 and len(set(S + [TMP, ROUNDS, CTR, XREM, XVL, XKS, STATE, IN, OUT, NGROUPS, NROUNDS])) == 27


def scalar_round(quads):
    """One ChaCha round for the scalar block, with base RV64I rotates."""
    out = []

    def step(add, xor, rot):
        for q in quads:
            a, b = R[S[q[add[0]]]], R[S[q[add[1]]]]
            out.append(f'addw\t{a}, {a}, {b}')
        for q in quads:
            x, a = R[S[q[xor]]], R[S[q[add[0]]]]
            out.append(f'xor\t{x}, {x}, {a}')
        for q in quads:
            r = R[S[q[xor]]]
            out.extend([f'slliw\t{R[TMP]}, {r}, {rot}', f'srliw\t{r}, {r}, {32 - rot}', f'or\t{r}, {r}, {R[TMP]}'])
    step((0, 1), 3, 16)
    step((2, 3), 1, 12)
    step((0, 1), 3, 8)
    step((2, 3), 1, 7)
    return out


class Asm:
    def __init__(self):
        self.lines, self.vectors = [], []

    def __call__(self, *items):
        for it in items:
            if isinstance(it, V):
                self.vectors.append(it)
            self.lines.append(it)

    def label(self, name):
        self.lines.append(name + ':')

    def render(self):
        out = []
        for line in self.lines:
            if isinstance(line, V):
                out.append(f'\t.4byte\t0x{line.word:08x}\t/* {line.text} */')
            elif line.endswith(':') or line.startswith(('SYM_', '/*', '#')) or line == '':
                out.append(line)
            else:
                out.append('\t' + line)
        return '\n'.join(out) + '\n'


def initial(a, i, r):
    """Initial state word i into r; for i = 12 the fifth block's counter."""
    if i == 12:
        a(f'addiw\t{R[r]}, {R[CTR]}, 4')
    else:
        a(f'lw\t{R[r]}, {4 * i}({R[STATE]})')


def build():
    a = Asm()
    a(f'SYM_FUNC_START({NAME})')
    a(f'addi\tsp, sp, -{FRAME}')
    for k, reg in enumerate(SAVED):
        a(f'sd\t{R[reg]}, {SIZE + 8 * k}(sp)')
    a(f'lw\t{R[CTR]}, 48({R[STATE]})')
    a(f'beqz\t{R[NGROUPS]}, .Ldone')
    a.label('.Lgroup')
    a(f'li\t{R[TMP]}, 4', vsetvli(ROUNDS, TMP, 32, 1))
    # Blocks CTR..CTR+3 in the vector lanes, block CTR+4 in scalar registers.
    for i in range(16):
        initial(a, i, S[i])
        if i != 12:
            a(vmv_v_x(i, S[i]))
    a(vid(VCTR), vx('vadd', VCTR, VCTR, CTR), vmv_v_v(12, VCTR))
    a(f'srli\t{R[ROUNDS]}, {R[NROUNDS]}, 1')
    a.label('.Ldoubleround')
    # One vector, one scalar instruction: the C906 issues the scalar one while
    # its 64-bit vector datapath still works on the 128-bit vector one.
    vec = vector_round(COLUMNS) + vector_round(DIAGONALS)
    sca = scalar_round(COLUMNS) + scalar_round(DIAGONALS)
    assert len(vec) == len(sca) == 160
    for v, s in zip(vec, sca):
        a(v, s)
    a(f'addi\t{R[ROUNDS]}, {R[ROUNDS]}, -1', f'bnez\t{R[ROUNDS]}, .Ldoubleround')
    # Add the initial state.
    for i in range(16):
        initial(a, i, TMP)
        a(vv('vadd', 12, 12, VCTR) if i == 12 else vx('vadd', i, i, TMP))
        a(f'addw\t{R[S[i]]}, {R[S[i]]}, {R[TMP]}')
    # Key stream to the stack: block j at 64*j; lane j of v(i) is word i of block j.
    for i in range(16):
        a(f'sw\t{R[S[i]]}, {256 + 4 * i}(sp)')
    a(f'li\t{R[ROUNDS]}, 64', vssseg(8, 0, 2, ROUNDS), f'addi\t{R[TMP]}, sp, 32', vssseg(8, 8, TMP, ROUNDS))
    # out = in ^ key stream with byte elements, so any alignment is fine.
    a(f'li\t{R[XREM]}, {SIZE}', f'mv\t{R[XKS]}, sp')
    a.label('.Lxor')
    a(vsetvli(XVL, XREM, 8, 8), vle(0, IN), vle(8, XKS), vv('vxor', 0, 0, 8), vse(0, OUT))
    a(f'add\t{R[IN]}, {R[IN]}, {R[XVL]}', f'add\t{R[OUT]}, {R[OUT]}, {R[XVL]}',
      f'add\t{R[XKS]}, {R[XKS]}, {R[XVL]}', f'sub\t{R[XREM]}, {R[XREM]}, {R[XVL]}', f'bnez\t{R[XREM]}, .Lxor')
    a(f'addiw\t{R[CTR]}, {R[CTR]}, {BLOCKS}', f'addi\t{R[NGROUPS]}, {R[NGROUPS]}, -1', f'bnez\t{R[NGROUPS]}, .Lgroup')
    a(f'sw\t{R[CTR]}, 48({R[STATE]})')
    # Wipe the key stream from the stack.
    a(f'li\t{R[XREM]}, {SIZE}', f'mv\t{R[XKS]}, sp')
    a.label('.Lwipe')
    a(vsetvli(XVL, XREM, 8, 8), vmv_v_x(0, 0), vse(0, XKS))
    a(f'add\t{R[XKS]}, {R[XKS]}, {R[XVL]}', f'sub\t{R[XREM]}, {R[XREM]}, {R[XVL]}', f'bnez\t{R[XREM]}, .Lwipe')
    a.label('.Ldone')
    for k, reg in enumerate(SAVED):
        a(f'ld\t{R[reg]}, {SIZE + 8 * k}(sp)')
    a(f'addi\tsp, sp, {FRAME}', 'ret')
    a(f'SYM_FUNC_END({NAME})')
    return a


HEADER = '''\
/* SPDX-License-Identifier: GPL-2.0-only OR BSD-3-Clause */
/*
 * Generated by firmware/cpu/go-crypto-c906/gen_chacha_linux.py
 * (NanoKVM OS). Do not edit.
 *
 * ChaCha for the T-Head C906: five 64-byte blocks per iteration. Four blocks
 * are in XTheadVector lanes (VLEN 128, SEW 32, LMUL 1), one state word per
 * vector register; the fifth block is in scalar registers, its instructions
 * interleaved with the vector ones. XTheadVector has no rotate, so rotates
 * are shift left, shift right, or.
 *
 * void chacha_xtheadvector(struct chacha_state *state, const u8 *in, u8 *out,
 *                          size_t ngroups, int nrounds);
 *
 * Processes ngroups * 320 bytes, advances state->x[12] by 5 per group. The
 * caller holds kernel_vector_begin(). Vector instructions are .4byte words,
 * checked by the generator against GNU as/objdump -march=rv64gc_xtheadvector.
 */

#include <linux/linkage.h>

\t.text
'''


def from_patch(patch):
    """The generated file as added by a kernel patch."""
    marker = 'diff --git a/lib/crypto/riscv/chacha-riscv64-xtheadvector.S'
    lines = patch.split(marker, 1)[1].split('\ndiff --git ', 1)[0].splitlines()
    start = next(i for i, l in enumerate(lines) if l.startswith('@@'))
    return ''.join(l[1:] + '\n' for l in lines[start + 1:] if l.startswith('+'))


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
        current = args.check.read_text()
        if args.check.suffix == '.patch':
            current = from_patch(current)
        if current != text:
            sys.exit(f'{args.check} is not up to date')
        print(f'{args.check}: up to date, {n} distinct vector encodings verified')
    else:
        args.output.write_text(text)
        print(f'{args.output}: {len(a.vectors)} vector instructions, {n} distinct encodings verified')


if __name__ == '__main__':
    main()
