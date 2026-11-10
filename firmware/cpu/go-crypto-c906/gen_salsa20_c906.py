#!/usr/bin/env python3
"""Generate salsa20_c906.S, Salsa20 for the T-Head C906 in the RustDesk add-on.

The five-block design of the ChaCha kernels (gen_chacha_riscv64.py,
gen_chacha_linux.py): four blocks in XTheadVector lanes (VLEN 128, SEW 32,
LMUL 1), one state word per vector register, and a fifth block in scalar
registers, its instructions interleaved with the vector ones.

    void nanokvm_salsa20_xor_c906(const u32 state[16], const u8 *in, u8 *out,
                                  size_t groups, u32 counter);

XORs groups * 320 bytes of in with the key stream of blocks counter,
counter + 1, ... into out (in may equal out). The state supplies the
constants, the key, the nonce and the high counter word (state[9]); its own
state[8] is not read, and the low counter word does not carry into the high
one: the caller's messages are shorter than 2^32 blocks. The key stream
buffer on the stack is wiped before returning. Vector state must be enabled
(NanoKVM kernel patch 0078 keeps it enabled for every user thread).

Salsa20's quarter round rotates a sum and XORs it into a third word, so each
step needs a temporary: v16..v19 hold the four sums, v20..v23 the rotated
ones (shift right by 32 - k, then th.vmacc.vx adds the sum times 2^k), one
scalar register serves the scalar block (th.srriw rotates in one
instruction).

Vector and XTheadBb instructions are .4byte words with their mnemonics in
comments, so any RV64GC assembler takes the file, LLVM's included. Every
word is checked against GNU as/objdump -march=rv64gc_xtheadvector_xtheadbb.

Usage: gen_salsa20_c906.py --binutils PREFIX (--output FILE | --check FILE)
"""
from pathlib import Path
import argparse
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import gen_chacha_riscv64 as G  # noqa: E402
from gen_chacha_riscv64 import V, vv, vx, vi, vmacc_vx, srriw, vmv_v_x, vmv_v_v, vid, vsetvli, vle, vse, vssseg, verify  # noqa: E402

G.ABI.update({0: 'zero', 1: 'ra', 2: 'sp', 27: 's11', 31: 't6'})
R = G.ABI
STATE, IN, OUT, GROUPS, CTR = 10, 11, 12, 13, 14               # a0..a4
S = [5, 6, 7, 8, 9, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25]  # t0-t2 s0 s1 a5-a7 s2-s9: the fifth block
ST = 1                                                          # ra: the scalar block's temporary
TMP, ROUNDS, XREM, XVL, XKS = 28, 29, 26, 27, 31                # t3 t4 s10 s11 t6
SAVED = [1, 8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27]       # ra s0-s11
BLOCKS = 5
SIZE = 64 * BLOCKS
FRAME = (SIZE + 8 * len(SAVED) + 15) & ~15
VT, VU, VCTR = [16, 17, 18, 19], [20, 21, 22, 23], 24
NAME = 'nanokvm_salsa20_xor_c906'
assert len(set(S + [ST, TMP, ROUNDS, XREM, XVL, XKS, STATE, IN, OUT, GROUPS, CTR])) == 27

# th.vmacc.vx multipliers while the rounds run; the registers serve the XOR
# loop otherwise.
MULREG = {7: TMP, 9: XREM, 13: XVL, 18: XKS}
COLUMNS = [(0, 4, 8, 12), (5, 9, 13, 1), (10, 14, 2, 6), (15, 3, 7, 11)]
ROWS = [(0, 1, 2, 3), (5, 6, 7, 4), (10, 11, 8, 9), (15, 12, 13, 14)]
# y1 ^= (y0 + y3) <<< 7; y2 ^= (y1 + y0) <<< 9; y3 ^= (y2 + y1) <<< 13; y0 ^= (y3 + y2) <<< 18
STEPS = [(1, 0, 3, 7), (2, 1, 0, 9), (3, 2, 1, 13), (0, 3, 2, 18)]
WORDS = len(COLUMNS) * len(STEPS) * 2


def vector_round(quads):
    out = []
    for dst, a, b, k in STEPS:
        out += [vv('vadd', VT[i], q[a], q[b]) for i, q in enumerate(quads)]
        out += [vi('vsrl', VU[i], VT[i], 32 - k) for i in range(4)]
        out += [vmacc_vx(VU[i], MULREG[k], VT[i]) for i in range(4)]
        out += [vv('vxor', q[dst], q[dst], VU[i]) for i, q in enumerate(quads)]
    return out


def scalar_round(quads):
    out = []
    for dst, a, b, k in STEPS:
        for q in quads:
            out += [f'addw\t{R[ST]}, {R[S[q[a]]]}, {R[S[q[b]]]}', srriw(ST, ST, 32 - k),
                    f'xor\t{R[S[q[dst]]]}, {R[S[q[dst]]]}, {R[ST]}']
    return out


def double_round():
    """Vector and scalar instructions interleaved evenly: the C906 issues
    scalar ones while its 64-bit vector datapath works on a 128-bit one."""
    vec = vector_round(COLUMNS) + vector_round(ROWS)
    sca = scalar_round(COLUMNS) + scalar_round(ROWS)
    assert len(vec) == 128 and len(sca) == 96
    out, k = [], 0
    for n, v in enumerate(vec):
        out.append(v)
        while k < len(sca) and k * len(vec) < (n + 1) * len(sca):
            out.append(sca[k])
            k += 1
    return out + sca[k:]


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
                out.append(f'\t.4byte\t0x{line.word:08x}\t# {line.text}')
            elif line.endswith(':') or line.startswith(('#', '.')) or line == '':
                out.append(line)
            else:
                out.append('\t' + line)
        return '\n'.join(out) + '\n'


def initial(a, i, r):
    """Initial state word i into r; for the counter the fifth block's."""
    if i == 8:
        a(f'addiw\t{R[r]}, {R[CTR]}, 4')
    else:
        a(f'lw\t{R[r]}, {4 * i}({R[STATE]})')


def build():
    a = Asm()
    a(f'.globl\t{NAME}', f'.type\t{NAME}, @function', '.p2align\t4')
    a.label(NAME)
    a(f'addi\tsp, sp, -{FRAME}')
    for k, reg in enumerate(SAVED):
        a(f'sd\t{R[reg]}, {SIZE + 8 * k}(sp)')
    a(f'beqz\t{R[GROUPS]}, .Lnk_salsa20_done')
    a.label('.Lnk_salsa20_group')
    a(f'li\t{R[TMP]}, 4', vsetvli(ROUNDS, TMP, 32, 1))
    # Blocks CTR..CTR+3 in the vector lanes, block CTR+4 in scalar registers.
    for i in range(16):
        initial(a, i, S[i])
        if i != 8:
            a(vmv_v_x(i, S[i]))
    a(vid(VCTR), vx('vadd', VCTR, VCTR, CTR), vmv_v_v(8, VCTR))
    a(f'li\t{R[ROUNDS]}, 10')
    for rot, reg in MULREG.items():
        a(f'li\t{R[reg]}, {1 << rot}')
    a.label('.Lnk_salsa20_round')
    a(*double_round())
    a(f'addi\t{R[ROUNDS]}, {R[ROUNDS]}, -1', f'bnez\t{R[ROUNDS]}, .Lnk_salsa20_round')
    # Add the initial state.
    for i in range(16):
        initial(a, i, TMP)
        a(vv('vadd', 8, 8, VCTR) if i == 8 else vx('vadd', i, i, TMP))
        a(f'addw\t{R[S[i]]}, {R[S[i]]}, {R[TMP]}')
    # Key stream to the stack: block j at 64*j; lane j of v(i) is word i of block j.
    for i in range(16):
        a(f'sw\t{R[S[i]]}, {256 + 4 * i}(sp)')
    a(f'li\t{R[ROUNDS]}, 64', vssseg(8, 0, 2, ROUNDS), f'addi\t{R[TMP]}, sp, 32', vssseg(8, 8, TMP, ROUNDS))
    # out = in ^ key stream with byte elements, so any alignment is fine.
    a(f'li\t{R[XREM]}, {SIZE}', f'mv\t{R[XKS]}, sp')
    a.label('.Lnk_salsa20_xor')
    a(vsetvli(XVL, XREM, 8, 8), vle(0, IN), vle(8, XKS), vv('vxor', 0, 0, 8), vse(0, OUT))
    a(f'add\t{R[IN]}, {R[IN]}, {R[XVL]}', f'add\t{R[OUT]}, {R[OUT]}, {R[XVL]}',
      f'add\t{R[XKS]}, {R[XKS]}, {R[XVL]}', f'sub\t{R[XREM]}, {R[XREM]}, {R[XVL]}', f'bnez\t{R[XREM]}, .Lnk_salsa20_xor')
    a(f'addiw\t{R[CTR]}, {R[CTR]}, {BLOCKS}', f'addi\t{R[GROUPS]}, {R[GROUPS]}, -1', f'bnez\t{R[GROUPS]}, .Lnk_salsa20_group')
    # Wipe the key stream from the stack.
    a(f'li\t{R[XREM]}, {SIZE}', f'mv\t{R[XKS]}, sp')
    a.label('.Lnk_salsa20_wipe')
    a(vsetvli(XVL, XREM, 8, 8), vmv_v_x(0, 0), vse(0, XKS))
    a(f'add\t{R[XKS]}, {R[XKS]}, {R[XVL]}', f'sub\t{R[XREM]}, {R[XREM]}, {R[XVL]}', f'bnez\t{R[XREM]}, .Lnk_salsa20_wipe')
    a.label('.Lnk_salsa20_done')
    for k, reg in enumerate(SAVED):
        a(f'ld\t{R[reg]}, {SIZE + 8 * k}(sp)')
    a(f'addi\tsp, sp, {FRAME}', 'ret', f'.size\t{NAME}, . - {NAME}')
    return a


HEADER = '''\
# SPDX-License-Identifier: AGPL-3.0-only
# Generated by firmware/cpu/go-crypto-c906/gen_salsa20_c906.py (NanoKVM OS).
# Do not edit.
#
# Salsa20 for the T-Head C906: five 64-byte blocks per iteration. Four blocks
# are in XTheadVector lanes (VLEN 128, SEW 32, LMUL 1), one state word per
# vector register; the fifth block is in scalar registers, its instructions
# interleaved with the vector ones. XTheadVector has no rotate: a vector
# rotate left by k is a shift right by 32 - k plus th.vmacc.vx of the word
# times 2^k. The scalar block rotates with XTheadBb th.srriw.
#
# void nanokvm_salsa20_xor_c906(const u32 state[16], const u8 *in, u8 *out,
#                               size_t groups, u32 counter);
#
# XORs groups * 320 bytes with the key stream of blocks counter, counter + 1,
# ...; state[8] is not read and the counter does not carry into state[9].
# Vector and XTheadBb instructions are .4byte words, checked by the
# generator against GNU as/objdump -march=rv64gc_xtheadvector_xtheadbb.

\t.text
'''


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
        print(f'{args.check}: up to date, {n} distinct encodings verified')
    else:
        args.output.write_text(text)
        print(f'{args.output}: {len(a.vectors)} encoded instructions, {n} distinct encodings verified')


if __name__ == '__main__':
    main()
