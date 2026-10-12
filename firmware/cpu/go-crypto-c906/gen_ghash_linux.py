#!/usr/bin/env python3
"""Generate ghash-riscv64-c906.S, GHASH for the Linux kernel on the C906.

    void ghash_blocks_c906(struct polyval_elem *acc, const u64 kp[40],
                           const u8 *data, size_t nblocks);
    void ghash_blocks_c906_v(struct polyval_elem *acc, const u64 kp[112],
                             const u8 *data, size_t ngroups);

acc is the POLYVAL-form accumulator of lib/crypto/gf128hash.c (lo, hi as
little-endian u64), data the GHASH blocks, any alignment. Per block, as
ghash_blocks_generic(): acc ^= the byte-swapped block; acc = acc * H * x^-128.
nblocks and ngroups may be 0. The second function hashes groups of five
blocks, four in XTheadVector lanes and one in scalar registers (see the
section "Five blocks per group" below); the caller holds kernel_vector_begin().

The C906 multiplier takes about four cycles per mul/mulhu, unpipelined, when an
operand has bits above 31, and one pipelined cycle when both fit in 32 bits.
So the carryless 128x128 product is built from nine 32x32 carryless products
(Karatsuba on 64-bit halves, then on 32-bit halves), each emulated with sixteen
integer multiplies of operands with their bits spread four apart ("holes", as
in BearSSL's ctmul32): a 32-bit operand has at most 8 bits per hole class, so no
column overflows into the next one and no correction is needed. All multiply
operands are below 2^32, which also keeps the multiply timing independent of
the data. The key operand's hole parts are precomputed by ghash_preparekey():

    kp[0..3]        O1 O2 O4 O8 = 0x1111.., 0x2222.., 0x4444.., 0x8888.. (64-bit)
    kp[4 + 4q + j]  key operand q (32 bits) & O_(2^j)                (j = 0..3)

for q = 3p + r, p = 0 (H.lo), 1 (H.hi), 2 (H.lo ^ H.hi) and r = 0 (low 32 bits),
1 (high 32 bits), 2 (low ^ high). No data-dependent branches, loads or table
lookups. The instructions of a block are list-scheduled for the C906's
latencies on fixed registers. Byte swaps use XTheadBb th.rev, which every
T-Head core with XTheadVector implements (C906, C910, C920).

The generator executes both generated functions (prologue, loop, epilogue) on
random keys and messages against a bit-serial GHASH, checks that every scalar
multiply operand is below 2^32, that sp, ra and s0-s11 are preserved and that
the stack slots holding products and blocks are wiped; th.vmv.x.s is run both
sign- and zero-extending. Vector instructions are .4byte words with their
mnemonics in comments, so the kernel needs no assembler support for
XTheadVector; each encoding is checked against GNU as/objdump.

Usage: gen_ghash_linux.py --binutils PREFIX (--output FILE | --check FILE) [--cases N]
For --check, FILE may also be platform/kernel/0041-*.patch.
"""
from pathlib import Path
import argparse
import random
import sys

LAT = {'mul': 3, 'ld': 3, 'th.rev': 2}
ALU_TO_MUL = 1     # cycles before a multiply may use an ALU result
NAME = 'ghash_blocks_c906'


class Ins:
    def __init__(self, op, dst, srcs, text, mem=None):
        self.op, self.dst, self.srcs, self.text, self.mem = op, dst, srcs, text, mem


def alu(op, d, a, b):
    return Ins(op, d, [a, b], f'{op}\t{d}, {a}, {b}')


def alui(op, d, a, imm):
    return Ins(op, d, [a], f'{op}\t{d}, {a}, {imm}')


def ld(d, off, base, mem=None):
    return Ins('ld', d, [base], f'ld\t{d}, {off}({base})', mem)


def sd(s, off, base, mem):
    return Ins('sd', None, [s, base], f'sd\t{s}, {off}({base})', mem)


def rev(d, s):
    return Ins('th.rev', d, [s], f'th.rev\t{d}, {s}')


def schedule(block):
    """Greedy list scheduling, critical path first, on fixed registers.

    Read-after-write, write-after-read and write-after-write order is kept for
    registers and for the stack slots named in Ins.mem."""
    n = len(block)
    deps = [[] for _ in range(n)]
    last_w, reads = {}, {}

    def touched(ins):
        srcs, dst = list(ins.srcs), ins.dst
        if ins.mem:
            if ins.op == 'ld':
                srcs.append(ins.mem)
            else:
                dst = ins.mem
        return srcs, dst

    for i, ins in enumerate(block):
        srcs, dst = touched(ins)
        for r in srcs:
            if r in last_w:
                w = block[last_w[r]]
                lat = LAT.get(w.op, 1)
                if ins.op == 'mul' and w.op not in LAT:
                    lat = ALU_TO_MUL
                deps[i].append((last_w[r], lat))
        if dst:
            for j in reads.get(dst, []):
                deps[i].append((j, 0))
            if dst in last_w:
                deps[i].append((last_w[dst], 1))
        for r in srcs:
            reads.setdefault(r, []).append(i)
        if dst:
            last_w[dst] = i
            reads[dst] = []
    succ = [[] for _ in range(n)]
    for i in range(n):
        for p, lat in deps[i]:
            succ[p].append((i, lat))
    prio = [0] * n
    for i in reversed(range(n)):
        prio[i] = LAT.get(block[i].op, 1) + max((prio[s] for s, _ in succ[i]), default=0)
    done_at, order, remaining, cycle = {}, [], set(range(n)), 0
    while remaining:
        ready = [i for i in remaining
                 if all(p in done_at and done_at[p] + lat <= cycle for p, lat in deps[i])]
        if ready:
            pick = max(ready, key=lambda i: (prio[i], -i))
            done_at[pick] = cycle
            order.append(block[pick])
            remaining.remove(pick)
        cycle += 1
    return order, cycle


# ra and x5-x31; a0 (the accumulator pointer) is spilled to the stack.
KP, DATA, END = 'a1', 'a2', 'a3'
O = ['a4', 'a5', 'a6', 'a7']                 # hole masks, loaded once
ALO, AHI = 't0', 't1'                        # accumulator
X, XC = 't2', 's0'                           # 64-bit operand, current 32-bit operand
R0, R1 = 's1', 's2'                          # result of the current 64x64 product
A = ['s3', 's4', 's5', 's6']                 # data operand hole parts
B = ['s7', 's8', 's9', 's10']                # key operand hole parts
GACC, MID = 's11', 't3'
P = ['t4', 't5', 't6', 'ra', 'a0']           # product temporaries
SAVED = ['ra', 's0', 's1', 's2', 's3', 's4', 's5', 's6', 's7', 's8', 's9', 's10', 's11']
ACC_SLOT = 8 * len(SAVED)
C_SLOT = [ACC_SLOT + 8 + 8 * k for k in range(4)]   # c0..c3 of the 256-bit product
FRAME = ACC_SLOT + 8 + 8 * 4

# Column k of the hole product collects a_i * b_j with i + j = k (mod 4).
PAIRS = {k: [(i, (k - i) % 4) for i in range(4)] for k in range(4)}


def clmul32(dst, a_reg, q, ptmp):
    """dst = clmul32(a, key operand q); a_reg (< 2^32) is preserved."""
    out = [alu('and', A[i], a_reg, O[i]) for i in range(4)]
    out += [ld(B[j], 32 + 32 * q + 8 * j, KP) for j in range(4)]
    for k in range(4):
        prods = []
        for n, (i, j) in enumerate(PAIRS[k]):
            t = next(ptmp)
            out.append(alu('mul', t, A[i], B[j]))
            prods.append(t)
            if n == 1:
                out.append(alu('xor', GACC, prods[0], prods[1]))
            elif n > 1:
                out.append(alu('xor', GACC, GACC, t))
        if k == 0:
            out.append(alu('and', dst, GACC, O[0]))
        else:
            out.append(alu('and', GACC, GACC, O[k]))
            out.append(alu('xor', dst, dst, GACC))
    return out


def clmul64(x_reg, p, ptmp):
    """(R0 lo, R1 hi) = clmul64(x, key part p); x_reg is preserved."""
    out = [alui('slli', XC, x_reg, 32), alui('srli', XC, XC, 32)]     # x0
    out += clmul32(R0, XC, 3 * p + 0, ptmp)
    out.append(alui('srli', XC, x_reg, 32))                           # x1
    out += clmul32(R1, XC, 3 * p + 1, ptmp)
    out += [alui('slli', MID, x_reg, 32), alui('srli', MID, MID, 32),
            alu('xor', XC, XC, MID)]                                  # x0 ^ x1
    out += clmul32(MID, XC, 3 * p + 2, ptmp)
    out += [alu('xor', MID, MID, R0), alu('xor', MID, MID, R1)]       # middle
    t = next(ptmp)
    out += [alui('slli', t, MID, 32), alu('xor', R0, R0, t)]          # lo ^= mid << 32
    t = next(ptmp)
    out += [alui('srli', t, MID, 32), alu('xor', R1, R1, t)]          # hi ^= mid >> 32
    return out


def fold(ptmp, terms):
    """c_k ^= reg for (k, reg) in terms, through the stack slots."""
    out = []
    for k, reg in terms:
        t = next(ptmp)
        out += [ld(t, C_SLOT[k], 'sp', f'c{k}'), alu('xor', t, t, reg),
                sd(t, C_SLOT[k], 'sp', f'c{k}')]
    return out


def block():
    b = []
    ptmp = iter(P * 1000)
    b += [ld(P[0], 8, DATA), ld(P[1], 0, DATA), rev(P[0], P[0]), rev(P[1], P[1]),
          alu('xor', ALO, ALO, P[0]), alu('xor', AHI, AHI, P[1])]
    # Karatsuba: c = l + (m + l + h) x^64 + h x^128, with l = lo * H.lo,
    # h = hi * H.hi, m = (lo ^ hi) * (H.lo ^ H.hi); each folded when ready.
    b.append(alu('xor', X, ALO, AHI))
    b += clmul64(X, 2, ptmp)                               # m
    b += [sd(R0, C_SLOT[1], 'sp', 'c1'), sd(R1, C_SLOT[2], 'sp', 'c2')]
    b += clmul64(ALO, 0, ptmp)                             # l
    b.append(sd(R0, C_SLOT[0], 'sp', 'c0'))
    b += fold(ptmp, [(1, R0), (1, R1), (2, R1)])
    b += clmul64(AHI, 1, ptmp)                             # h
    b.append(sd(R1, C_SLOT[3], 'sp', 'c3'))
    b += fold(ptmp, [(1, R0), (2, R0), (2, R1)])
    # Reduction as in polyval_mul_generic(); the result lands in c2, c3,
    # which are the accumulator registers.
    c = [X, R1, ALO, AHI]
    b += [ld(c[k], C_SLOT[k], 'sp', f'c{k}') for k in range(4)]
    for lo_c, mid, hi_c in ((c[0], c[1], c[2]), (c[1], c[2], c[3])):
        for s in (63, 62, 57):
            r = next(ptmp)
            b += [alui('slli', r, lo_c, s), alu('xor', mid, mid, r)]
        b.append(alu('xor', hi_c, hi_c, lo_c))
        for s in (1, 2, 7):
            r = next(ptmp)
            b += [alui('srli', r, lo_c, s), alu('xor', hi_c, hi_c, r)]
    b.append(alui('addi', DATA, DATA, 16))
    return b


def build():
    body, cycles = schedule(block())
    lines = [f'SYM_FUNC_START({NAME})',
             f'\tbeqz\ta3, .Lnone',
             f'\taddi\tsp, sp, -{FRAME}']
    lines += [f'\tsd\t{r}, {8 * k}(sp)' for k, r in enumerate(SAVED)]
    lines += [f'\tsd\ta0, {ACC_SLOT}(sp)',
              f'\tslli\t{END}, a3, 4', f'\tadd\t{END}, {END}, {DATA}',
              f'\tld\t{ALO}, 0(a0)', f'\tld\t{AHI}, 8(a0)']
    lines += [f'\tld\t{O[i]}, {8 * i}({KP})' for i in range(4)]
    lines += ['.Lblock:'] + ['\t' + i.text for i in body]
    lines += [f'\tbltu\t{DATA}, {END}, .Lblock',
              f'\tld\ta0, {ACC_SLOT}(sp)',
              f'\tsd\t{ALO}, 0(a0)', f'\tsd\t{AHI}, 8(a0)']
    lines += [f'\tld\t{r}, {8 * k}(sp)' for k, r in enumerate(SAVED)]
    # The c0..c3 slots held products of key and data; wipe them.
    lines += [f'\tsd\tzero, {s}(sp)' for s in C_SLOT]
    lines += [f'\taddi\tsp, sp, {FRAME}', '.Lnone:', '\tret', f'SYM_FUNC_END({NAME})']
    return lines, len(body), cycles


# --- Five blocks per group: four in XTheadVector lanes, one scalar ------------
#
# acc' = (acc ^ B0) H^5 + B1 H^4 + B2 H^3 + B3 H^2 + B4 H, products in the
# POLYVAL dot sense, summed unreduced and reduced once. Lane l (e32, vl = 4)
# multiplies its block by H^(5-l) with the same nine 32x32 hole products as the
# scalar code, using vmul.vv and vmulhu.vv for the low and high halves of each
# 32x32 integer product (two cycles each on the C906, whatever the values).
# The scalar block B4 * H runs the scalar code, its instructions interleaved one
# to one with the vector ones: the C906 issues a scalar instruction in the
# second cycle of a vector one at no cost. kp continues after the 40 scalar
# words with the lane key parts:
#
#     byte 320 + 16 (4q + j) + 4l   key operand q of H^(5-l), 32 bits, & O_(2^j)
#
# Byte swaps and acc ^ B0 are done by the scalar unit into a stack buffer that
# four strided vector loads read as one 32-bit word of all four blocks each.

VNAME = 'ghash_blocks_c906_v'
GROUP = 5 * 16
PV = ['t4', 't5', 't6', 'ra']               # scalar temporaries; a0 addresses vector loads
VADDR, VSTRIDE = 'a0', 't3'
VD = [1, 2, 3, 4]                           # data words w0..w3 of the four lanes
VXC = 5                                     # current 32-bit operand
VA = [6, 7, 8, 9]                           # its hole parts
VB = [10, 11, 12, 13]                       # key hole parts
VT = [14, 15, 16, 17]                       # products
VZ = [(18, 19), (20, 21)]                   # one hole class, low and high halves
VLO, VHI = 22, 23                           # one 32x32 product
VR = list(range(24, 32))                    # the 256-bit sum, eight 32-bit words
VFRAME_BUF = FRAME                          # staging buffer, 64 bytes, after the scalar frame
VFRAME = FRAME + 64
VLAT = {'th.vmul.vv': 6, 'th.vmulhu.vv': 6, 'th.vle.v': 6, 'th.vlse.v': 6}
# Word offsets of a 32x32 product inside the 256-bit one: Karatsuba levels
# (64-bit: low, high, middle; 128-bit: low, high, middle).
OFF64 = {0: (0, 1), 1: (2, 1), 2: (1,)}
OFF128 = {0: (0, 2), 1: (4, 2), 2: (2,)}

_G = None


def encoder():
    """The XTheadVector encoder of gen_chacha_riscv64.py, plus the operations used here."""
    global _G
    if _G is None:
        sys.path.insert(0, str(Path(__file__).resolve().parent))
        import gen_chacha_riscv64 as G
        G.ABI.update({0: 'zero', 1: 'ra', 2: 'sp', 27: 's11', 31: 't6'})
        _G = G
    return _G


def vreg(name):
    return {v: k for k, v in encoder().ABI.items()}[name]


def V(op, vd, *args):
    """A vector instruction as a schedulable Ins carrying its encoding."""
    G = encoder()
    if op in ('vmul', 'vmulhu', 'vredxor'):
        f6 = {'vmul': 0b100101, 'vmulhu': 0b100100, 'vredxor': 0b000011}[op]
        sfx = 'vs' if op == 'vredxor' else 'vv'
        vs2, vs1 = args
        v = G.V(f'th.{op}.{sfx} v{vd},v{vs2},v{vs1}', G.opv(f6, 1, vs2, vs1, G.OPMVV, vd))
        dst, srcs = f'v{vd}', [f'v{vs2}', f'v{vs1}']
    elif op == 'vxor':
        vs2, vs1 = args
        v = G.vv('vxor', vd, vs2, vs1)
        dst, srcs = f'v{vd}', [f'v{vs2}', f'v{vs1}']
    elif op == 'vand.vx':
        vs2, rs1 = args
        v = G.vx('vand', vd, vs2, vreg(rs1))
        dst, srcs = f'v{vd}', [f'v{vs2}', rs1]
    elif op == 'vmv.v.i':
        (imm,) = args
        v = G.V(f'th.vmv.v.i v{vd},{imm}', G.opv(0b010111, 1, 0, imm & 31, G.OPIVI, vd))
        dst, srcs = f'v{vd}', []
    elif op == 'vle':
        (rs1,) = args
        v = G.vle(vd, vreg(rs1))
        dst, srcs = f'v{vd}', [rs1]
    elif op == 'vlse':
        rs1, rs2 = args
        v = G.V(f'th.vlse.v v{vd},({rs1}),{rs2}', G.mem(G.LOAD_FP, 1, 0b010, vd, vreg(rs1), vreg(rs2)))
        dst, srcs = f'v{vd}', [rs1, rs2]
    elif op == 'vmv.x.s':
        (vs2,) = args             # vd is the scalar destination here
        v = G.V(f'th.vmv.x.s {vd},v{vs2}', G.opv(0b001100, 1, vs2, 0, G.OPMVV, vreg(vd)))
        dst, srcs = vd, [f'v{vs2}']
    elif op == 'vsetvli':
        rs1, = args
        v = G.vsetvli(vreg(vd), vreg(rs1), 32, 1)
        dst, srcs = None, [rs1]
    else:
        raise ValueError(op)
    ins = Ins(v.text.split()[0], dst, srcs, f'.4byte\t0x{v.word:08x}\t/* {v.text} */')
    ins.v = v
    return ins


def key_load(vd, q, j):
    """vd = lane key parts (q, j): the address through a0, then the load."""
    ins = V('vle', vd, VADDR)
    ins.pre = alui('addi', VADDR, KP, 320 + 16 * (4 * q + j))
    ins.srcs = [KP]
    return ins


def vschedule(block):
    """List scheduling of the vector stream: one issue every two cycles."""
    n = len(block)
    deps = [[] for _ in range(n)]
    last_w, reads = {}, {}
    for i, ins in enumerate(block):
        for r in ins.srcs:
            if r in last_w:
                deps[i].append((last_w[r], VLAT.get(block[last_w[r]].op, 2)))
        if ins.dst:
            for j in reads.get(ins.dst, []):
                deps[i].append((j, 0))
            if ins.dst in last_w:
                deps[i].append((last_w[ins.dst], 2))
        for r in ins.srcs:
            reads.setdefault(r, []).append(i)
        if ins.dst:
            last_w[ins.dst] = i
            reads[ins.dst] = []
    succ = [[] for _ in range(n)]
    for i in range(n):
        for p, lat in deps[i]:
            succ[p].append((i, lat))
    prio = [0] * n
    for i in reversed(range(n)):
        prio[i] = VLAT.get(block[i].op, 2) + max((prio[s] for s, _ in succ[i]), default=0)
    done_at, order, remaining, cycle = {}, [], set(range(n)), 0
    while remaining:
        ready = [i for i in remaining
                 if all(p in done_at and done_at[p] + lat <= cycle for p, lat in deps[i])]
        if ready:
            pick = max(ready, key=lambda i: (prio[i], -i))
            done_at[pick] = cycle
            order.append(block[pick])
            remaining.remove(pick)
            cycle += 2
        else:
            cycle += 1
    return order, cycle


def vclmul32(op_reg, q, z):
    """(VLO, VHI) = clmul32(lanes of op_reg, lane key operand q)."""
    out = [V('vand.vx', VA[i], op_reg, O[i]) for i in range(4)]
    out += [key_load(VB[j], q, j) for j in range(4)]
    t = 0
    for k in range(4):
        zl, zh = VZ[k % 2]
        for n, (i, j) in enumerate(PAIRS[k]):
            for half, zr in (('vmul', zl), ('vmulhu', zh)):
                if n == 0:
                    out.append(V(half, zr, VA[i], VB[j]))
                else:
                    out.append(V(half, VT[t % 4], VA[i], VB[j]))
                    out.append(V('vxor', zr, zr, VT[t % 4]))
                    t += 1
        if k == 0:
            out += [V('vand.vx', VLO, zl, O[0]), V('vand.vx', VHI, zh, O[0])]
        else:
            out += [V('vand.vx', zl, zl, O[k]), V('vand.vx', zh, zh, O[k]),
                    V('vxor', VLO, VLO, zl), V('vxor', VHI, VHI, zh)]
    return out


def vbody():
    """The four lane products, summed into VR[0..7] (not reduced)."""
    out = [V('vmv.v.i', r, 0) for r in VR]
    d = VD
    # Operand words for (Karatsuba half p, word r): low, high, middle.
    srcs = {(0, 0): [d[0]], (0, 1): [d[1]], (0, 2): [d[0], d[1]],
            (1, 0): [d[2]], (1, 1): [d[3]], (1, 2): [d[2], d[3]],
            (2, 0): [d[0], d[2]], (2, 1): [d[1], d[3]], (2, 2): [d[0], d[1], d[2], d[3]]}
    for p in (0, 1, 2):
        for r in (0, 1, 2):
            s = srcs[(p, r)]
            if len(s) == 1:
                op_reg = s[0]
            else:
                op_reg = VXC
                out.append(V('vxor', VXC, s[0], s[1]))
                for extra in s[2:]:
                    out.append(V('vxor', VXC, VXC, extra))
            out += vclmul32(op_reg, 3 * p + r, None)
            for o128 in OFF128[p]:
                for o64 in OFF64[r]:
                    o = o128 + o64
                    out += [V('vxor', VR[o], VR[o], VLO), V('vxor', VR[o + 1], VR[o + 1], VHI)]
    return out


def scalar_product(off):
    """c0..c3 slots = block at DATA + off times H, not reduced (as block())."""
    b = []
    ptmp = iter(PV * 1000)
    b += [ld(PV[0], off + 8, DATA), ld(PV[1], off, DATA), rev(ALO, PV[0]), rev(AHI, PV[1])]
    b.append(alu('xor', X, ALO, AHI))
    b += clmul64(X, 2, ptmp)
    b += [sd(R0, C_SLOT[1], 'sp', 'c1'), sd(R1, C_SLOT[2], 'sp', 'c2')]
    b += clmul64(ALO, 0, ptmp)
    b.append(sd(R0, C_SLOT[0], 'sp', 'c0'))
    b += fold(ptmp, [(1, R0), (1, R1), (2, R1)])
    b += clmul64(AHI, 1, ptmp)
    b.append(sd(R1, C_SLOT[3], 'sp', 'c3'))
    b += fold(ptmp, [(1, R0), (2, R0), (2, R1)])
    return b


def staging():
    """Byte-swapped blocks B0..B3 (B0 ^ acc) to the buffer, then the four word loads."""
    s = []
    for l in range(4):
        lo, hi = PV[0], PV[1]
        s += [ld(lo, 16 * l + 8, DATA), ld(hi, 16 * l, DATA), rev(lo, lo), rev(hi, hi)]
        if l == 0:
            s += [alu('xor', lo, lo, ALO), alu('xor', hi, hi, AHI)]
        s += [sd(lo, VFRAME_BUF + 16 * l, 'sp', f'b{l}'), sd(hi, VFRAME_BUF + 16 * l + 8, 'sp', f'b{l}h')]
    s, _ = schedule(s)
    lines = ['\t' + i.text for i in s]
    lines.append(f'\tli\t{VSTRIDE}, 16')
    for k in range(4):
        lines.append(f'\taddi\t{VADDR}, sp, {VFRAME_BUF + 4 * k}')
        lines.append('\t' + V('vlse', VD[k], VADDR, VSTRIDE).text)
    return lines, [V('vlse', VD[k], VADDR, VSTRIDE) for k in range(4)]


def tail():
    """Lane sums, the scalar product added, one reduction into ALO, AHI."""
    lines, vec = [], []

    def emit(x):
        if hasattr(x, 'v'):
            vec.append(x)
        lines.append('\t' + x.text)
    emit(V('vmv.v.i', VXC, 0))
    for k in range(8):
        emit(V('vredxor', VA[0] + k, VR[k], VXC))
    c = [X, R1, ALO, AHI]
    for m in range(4):
        e, o, t = PV[0], PV[1], PV[2]
        emit(V('vmv.x.s', e, VA[0] + 2 * m))
        emit(V('vmv.x.s', o, VA[0] + 2 * m + 1))
        # vmv.x.s may sign-extend the 32-bit element: keep the low word only.
        for ins in (alui('slli', e, e, 32), alui('srli', e, e, 32), alui('slli', o, o, 32),
                    alu('or', c[m], e, o), ld(t, C_SLOT[m], 'sp'), alu('xor', c[m], c[m], t)):
            emit(ins)
    ptmp = iter(PV * 1000)
    red = []
    for lo_c, mid, hi_c in ((c[0], c[1], c[2]), (c[1], c[2], c[3])):
        for s in (63, 62, 57):
            r = next(ptmp)
            red += [alui('slli', r, lo_c, s), alu('xor', mid, mid, r)]
        red.append(alu('xor', hi_c, hi_c, lo_c))
        for s in (1, 2, 7):
            r = next(ptmp)
            red += [alui('srli', r, lo_c, s), alu('xor', hi_c, hi_c, r)]
    red, _ = schedule(red)
    for ins in red:
        emit(ins)
    return lines, vec


def vbuild():
    vb, vcycles = vschedule(vbody())
    sb, _ = schedule(scalar_product(64))
    stage_lines, stage_vec = staging()
    tail_lines, tail_vec = tail()
    vectors = [i.v for i in vb] + [i.v for i in stage_vec] + [i.v for i in tail_vec]
    setvl = V('vsetvli', 'zero', VSTRIDE)
    vectors.append(setvl.v)
    body = []
    for k in range(max(len(vb), len(sb))):
        if k < len(vb):
            if hasattr(vb[k], 'pre'):
                body.append('\t' + vb[k].pre.text)
            body.append('\t' + vb[k].text)
        if k < len(sb):
            body.append('\t' + sb[k].text)
    lines = [f'SYM_FUNC_START({VNAME})',
             '\tbeqz\ta3, .Lvnone',
             f'\taddi\tsp, sp, -{VFRAME}']
    lines += [f'\tsd\t{r}, {8 * k}(sp)' for k, r in enumerate(SAVED)]
    lines += [f'\tsd\ta0, {ACC_SLOT}(sp)',
              f'\tslli\t{VSTRIDE}, a3, 4', f'\tslli\t{END}, a3, 6', f'\tadd\t{END}, {END}, {VSTRIDE}',
              f'\tadd\t{END}, {END}, {DATA}',
              f'\tld\t{ALO}, 0(a0)', f'\tld\t{AHI}, 8(a0)']
    lines += [f'\tld\t{O[i]}, {8 * i}({KP})' for i in range(4)]
    lines += [f'\tli\t{VSTRIDE}, 4', '\t' + setvl.text]
    lines += ['.Lvgroup:'] + stage_lines + body + tail_lines
    lines += [f'\taddi\t{DATA}, {DATA}, {GROUP}',
              f'\tbltu\t{DATA}, {END}, .Lvgroup',
              f'\tld\ta0, {ACC_SLOT}(sp)',
              f'\tsd\t{ALO}, 0(a0)', f'\tsd\t{AHI}, 8(a0)']
    lines += [f'\tld\t{r}, {8 * k}(sp)' for k, r in enumerate(SAVED)]
    # Products of key and data, and the byte-swapped blocks: wipe them.
    lines += [f'\tsd\tzero, {s}(sp)' for s in C_SLOT]
    lines += [f'\tsd\tzero, {VFRAME_BUF + 8 * k}(sp)' for k in range(8)]
    lines += [f'\taddi\tsp, sp, {VFRAME}', '.Lvnone:', '\tret', f'SYM_FUNC_END({VNAME})']
    stats = (len(vb), len(sb), len(stage_lines), len(tail_lines), vcycles)
    return lines, vectors, stats


HEADER = '''\
/* SPDX-License-Identifier: GPL-2.0-only OR BSD-3-Clause */
/*
 * Generated by firmware/cpu/go-crypto-c906/gen_ghash_linux.py
 * (NanoKVM OS). Do not edit.
 *
 * GHASH for the T-Head C906 in scalar code. The 128x128 carryless product is
 * nine 32x32 ones (two levels of Karatsuba), each sixteen integer multiplies
 * of operands with their bits four apart, all below 2^32: one cycle each on
 * the C906 instead of about four for wider operands, and data-independent
 * timing. Reduction as in polyval_mul_generic().
 *
 * void ghash_blocks_c906(struct polyval_elem *acc, const u64 kp[40],
 *                        const u8 *data, size_t nblocks);
 *
 * kp is the key split by ghash_preparekey_arch(), see the generator.
 * Byte swaps use XTheadBb th.rev.
 */

#include <linux/linkage.h>

\t.text
\t.option push
\t.option arch, +xtheadbb
'''
FOOTER = '\t.option pop\n'


def render():
    lines, n, cycles = build()
    vlines, vectors, vstats = vbuild()
    text = HEADER + '\n'.join(lines) + '\n\n' + '\n'.join(vlines) + '\n' + FOOTER
    return text, (n, cycles), vectors, vstats


# --- Self-test: run the generated function ------------------------------------

M64 = (1 << 64) - 1
M32 = (1 << 32) - 1
OM = [0x1111111111111111, 0x2222222222222222, 0x4444444444444444, 0x8888888888888888]


def gf_mul(x, y):
    """GHASH multiplication (NIST SP 800-38D), bit-serial."""
    z, v = 0, y
    for i in range(127, -1, -1):
        if (x >> i) & 1:
            z ^= v
        v = (v >> 1) ^ (0xE1 << 120) if v & 1 else v >> 1
    return z


def ghash_ref(h, y, data):
    for i in range(0, len(data), 16):
        y = gf_mul(y ^ int.from_bytes(data[i:i + 16], 'big'), h)
    return y


def polyval_key(raw):
    """ghash_key_to_polyval()."""
    hi, lo = int.from_bytes(raw[0:8], 'big'), int.from_bytes(raw[8:16], 'big')
    carry = hi >> 63
    hi = ((hi << 1) | (lo >> 63)) & M64
    lo = ((lo << 1) & M64) ^ carry
    return lo, hi ^ ((0xc2 << 56) if carry else 0)


def kp_table(lo, hi):
    """ghash_preparekey_arch()'s kp."""
    kp = list(OM)
    for part in (lo, hi, lo ^ hi):
        for op in (part & M32, part >> 32, (part & M32) ^ (part >> 32)):
            kp += [op & OM[j] for j in range(4)]
    return kp


def execute(text, regs, mem, entry=NAME, vregs=None, vmv_sign=True):
    """Run the function entry in text; mem maps byte addresses to byte values.

    Vector instructions are executed from the mnemonics in the comments of
    their .4byte words (the encodings are checked against GNU as separately).
    vmv_sign: th.vmv.x.s sign-extends (True) or zero-extends the element."""
    prog, labels = [], {}
    for line in text.splitlines():
        line = line.strip()
        if line.startswith('SYM_FUNC_START('):
            labels[line[len('SYM_FUNC_START('):-1]] = len(prog)
            continue
        if line.startswith('.4byte'):
            line = line.split('/*')[1].split('*/')[0].strip()
            op, _, args = line.partition(' ')
            prog.append((op, [x.strip() for x in args.split(',')]))
            continue
        line = line.split('/*')[0].strip()
        if not line or line.startswith(('#', '.option', '.text', 'SYM_FUNC', '*')):
            continue
        if line.endswith(':'):
            labels[line[:-1]] = len(prog)
            continue
        op, _, args = line.partition('\t')
        prog.append((op, [x.strip() for x in args.split(',')] if args else []))
    vregs = vregs if vregs is not None else {}
    vl = None

    def r(x):
        return 0 if x == 'zero' else regs[x]

    def w(x, v):
        if x != 'zero':
            regs[x] = v & M64

    def addr(m):
        off, base = m.rstrip(')').split('(')
        return (r(base) + int(off or 0)) & M64

    def load32(p):
        return int.from_bytes(bytes(mem[p + i] for i in range(4)), 'little')

    pc, steps = labels[entry], 0
    while True:
        op, a = prog[pc]
        pc += 1
        steps += 1
        if op == 'ret':
            return steps
        if op.startswith('th.v'):
            if op == 'th.vsetvli':
                assert a[2:] == ['e32', 'm1', 'd1'] and r(a[1]) == 4, a
                vl = 4
                continue
            assert vl == 4, 'vector instruction before vsetvli'
            if op == 'th.vle.v':
                p = addr(a[1])
                vregs[a[0]] = [load32(p + 4 * i) for i in range(4)]
            elif op == 'th.vlse.v':
                p, stride = addr(a[1]), r(a[2])
                vregs[a[0]] = [load32(p + stride * i) for i in range(4)]
            elif op == 'th.vmv.v.i':
                vregs[a[0]] = [int(a[1]) & M32] * 4
            elif op == 'th.vand.vx':
                vregs[a[0]] = [x & r(a[2]) & M32 for x in vregs[a[1]]]
            elif op in ('th.vxor.vv', 'th.vmul.vv', 'th.vmulhu.vv'):
                f = {'th.vxor.vv': lambda x, y: x ^ y, 'th.vmul.vv': lambda x, y: (x * y) & M32,
                     'th.vmulhu.vv': lambda x, y: (x * y) >> 32}[op]
                vregs[a[0]] = [f(x, y) for x, y in zip(vregs[a[1]], vregs[a[2]])]
            elif op == 'th.vredxor.vs':
                acc = vregs[a[2]][0]
                for x in vregs[a[1]]:
                    acc ^= x
                vregs[a[0]] = [acc] + [0xdeadbeef] * 3      # the other elements are not used
            elif op == 'th.vmv.x.s':
                x = vregs[a[1]][0]
                w(a[0], x - (1 << 32) if vmv_sign and x >> 31 else x)
            else:
                raise ValueError(op)
            continue
        if op == 'ld':
            p = addr(a[1])
            w(a[0], int.from_bytes(bytes(mem[p + i] for i in range(8)), 'little'))
        elif op == 'sd':
            p = addr(a[1])
            for i, byte in enumerate(r(a[0]).to_bytes(8, 'little')):
                mem[p + i] = byte
        elif op == 'th.rev':
            w(a[0], int.from_bytes(r(a[1]).to_bytes(8, 'little'), 'big'))
        elif op == 'li':
            w(a[0], int(a[1], 0))
        elif op in ('slli', 'srli', 'addi'):
            x, imm = r(a[1]), int(a[2])
            w(a[0], x + imm if op == 'addi' else x << imm if op == 'slli' else x >> imm)
        elif op == 'beqz':
            if r(a[0]) == 0:
                pc = labels[a[1]]
        elif op == 'bltu':
            if r(a[0]) < r(a[1]):
                pc = labels[a[2]]
        else:
            x, y = r(a[1]), r(a[2])
            if op == 'mul':
                assert x <= M32 and y <= M32, f'multiply operand above 2^32: {op} {a}'
            w(a[0], {'and': x & y, 'or': x | y, 'xor': x ^ y, 'add': x + y,
                     'sub': x - y, 'mul': x * y}[op])


ABI = ['zero', 'ra', 'sp', 'gp', 'tp', 't0', 't1', 't2', 's0', 's1', 'a0', 'a1', 'a2',
       'a3', 'a4', 'a5', 'a6', 'a7', 's2', 's3', 's4', 's5', 's6', 's7', 's8', 's9',
       's10', 's11', 't3', 't4', 't5', 't6']


def clmul(a, b):
    r = 0
    while b:
        if b & 1:
            r ^= a
        a <<= 1
        b >>= 1
    return r


def polyval_dot(a, b):
    """a * b * x^-128 in POLYVAL's field, as polyval_mul_generic()."""
    p = clmul(a[0] | a[1] << 64, b[0] | b[1] << 64)
    c = [(p >> (64 * k)) & M64 for k in range(4)]
    for lo, mid, hi in ((0, 1, 2), (1, 2, 3)):
        x = c[lo]
        c[mid] ^= ((x << 63) ^ (x << 62) ^ (x << 57)) & M64
        c[hi] ^= x ^ (x >> 1) ^ (x >> 2) ^ (x >> 7)
    return c[2], c[3]


def kp_vector_table(lo, hi):
    """ghash_preparekey_arch()'s kp with the lane key parts: 112 words."""
    powers = [None, (lo, hi)]
    for _ in range(4):
        powers.append(polyval_dot(powers[-1], (lo, hi)))
    out = bytearray()
    for q in range(9):
        p, r = divmod(q, 3)
        for j in range(4):
            for lane in range(4):
                plo, phi = powers[5 - lane]
                part = (plo, phi, plo ^ phi)[p]
                op = (part & M32, part >> 32, (part & M32) ^ (part >> 32))[r]
                out += (op & OM[j] & M32).to_bytes(4, 'little')
    return kp_table(lo, hi) + [int.from_bytes(out[i:i + 8], 'little') for i in range(0, len(out), 8)]


def run_case(text, entry, rnd, raw, acc0, data, units, kp, frame, wipe):
    ACC, KPB, DB, SP = 0x1000, 0x2000, 0x8003, 0x20000      # data misaligned
    mem = {}
    for base, words in ((ACC, [acc0 & M64, acc0 >> 64]), (KPB, kp)):
        for i, v in enumerate(words):
            for k, byte in enumerate(v.to_bytes(8, 'little')):
                mem[base + 8 * i + k] = byte
    for i, byte in enumerate(data):
        mem[DB + i] = byte
    regs = {name: rnd.getrandbits(64) for name in ABI[1:]}
    regs.update({'sp': SP, 'a0': ACC, 'a1': KPB, 'a2': DB, 'a3': units})
    vregs = {f'v{i}': [rnd.getrandbits(32) for _ in range(4)] for i in range(32)}
    before = dict(regs)
    execute(text, regs, mem, entry, vregs, vmv_sign=rnd.random() < 0.5)
    for name in ['sp', 'ra'] + [f's{i}' for i in range(12)]:
        assert regs[name] == before[name], f'{entry}: {name} not preserved'
    got = int.from_bytes(bytes(mem[ACC + i] for i in range(16)), 'little')
    want = ghash_ref(int.from_bytes(raw, 'big'), acc0, data)
    if got != want:
        sys.exit(f'self-test {entry}, {units} units: got {got:032x}, want {want:032x}')
    if units:
        left = [p for p in range(SP - frame + wipe[0], SP - frame + wipe[1]) if mem.get(p)]
        assert not left, f'{entry}: products or blocks left on the stack'


def selftest(text, cases):
    rnd = random.Random(20261012)
    for case in range(cases):
        raw = b'\xff' * 16 if case % 7 == 0 else rnd.randbytes(16)
        lo, hi = polyval_key(raw)
        acc0 = rnd.getrandbits(128)
        # Scalar: blocks of 16 bytes.
        nblocks = case % 5 if case < 10 else 1 + rnd.randrange(6)
        data = b'\xff' * (16 * nblocks) if case % 11 == 0 else rnd.randbytes(16 * nblocks)
        run_case(text, NAME, rnd, raw, acc0, data, nblocks, kp_table(lo, hi), FRAME,
                 (C_SLOT[0], FRAME))
        # Vector: groups of five blocks.
        ngroups = case % 3 if case < 6 else 1 + rnd.randrange(3)
        data = b'\xff' * (GROUP * ngroups) if case % 11 == 0 else rnd.randbytes(GROUP * ngroups)
        run_case(text, VNAME, rnd, raw, acc0, data, ngroups, kp_vector_table(lo, hi), VFRAME,
                 (C_SLOT[0], VFRAME))
    return cases


def from_patch(patch):
    """The generated file as added by a kernel patch."""
    marker = 'diff --git a/lib/crypto/riscv/ghash-riscv64-c906.S'
    lines = patch.split(marker, 1)[1].split('\ndiff --git ', 1)[0].splitlines()
    start = next(i for i, l in enumerate(lines) if l.startswith('@@'))
    return ''.join(l[1:] + '\n' for l in lines[start + 1:] if l.startswith('+'))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('--binutils', required=True, help='GNU binutils prefix with XTheadVector support')
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument('--output', type=Path)
    g.add_argument('--check', type=Path)
    p.add_argument('--cases', type=int, default=200, help='self-test cases (default 200)')
    args = p.parse_args()
    text, (n, cycles), vectors, vstats = render()
    encodings = encoder().verify(args.binutils, vectors)
    cases = selftest(text, args.cases)
    done = (f'{cases} self-test cases of each function against a bit-serial GHASH, '
            f'{encodings} distinct vector encodings verified')
    if args.check:
        current = args.check.read_text()
        if args.check.suffix == '.patch':
            current = from_patch(current)
        if current != text:
            sys.exit(f'{args.check} is not up to date')
        print(f'{args.check}: up to date; {done}')
    else:
        args.output.write_text(text)
        nv, ns, nstage, ntail, vcycles = vstats
        print(f'{args.output}: scalar {n} instructions per block ({cycles} scheduled cycles); '
              f'per group of five blocks {nv} vector and {ns} scalar body instructions '
              f'({vcycles} vector cycles), {nstage} staging and {ntail} tail lines; {done}')


if __name__ == '__main__':
    main()
