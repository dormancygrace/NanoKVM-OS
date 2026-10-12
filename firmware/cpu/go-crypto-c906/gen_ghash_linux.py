#!/usr/bin/env python3
"""Generate ghash-riscv64-c906.S, scalar GHASH for the Linux kernel on the C906.

    void ghash_blocks_c906(struct polyval_elem *acc, const u64 kp[40],
                           const u8 *data, size_t nblocks);

acc is the POLYVAL-form accumulator of lib/crypto/gf128hash.c (lo, hi as
little-endian u64), data the GHASH blocks, any alignment. Per block, as
ghash_blocks_generic(): acc ^= the byte-swapped block; acc = acc * H * x^-128.
nblocks may be 0.

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

The generator executes the whole generated function (prologue, block loop,
epilogue) on random keys and messages against a bit-serial GHASH, checks that
every multiply operand is below 2^32 and that sp, ra and s0-s11 are preserved.

Usage: gen_ghash_linux.py (--output FILE | --check FILE) [--cases N]
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
    return HEADER + '\n'.join(lines) + '\n' + FOOTER, n, cycles


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


def execute(text, regs, mem):
    """Run the function in text; mem maps byte addresses to byte values."""
    prog, labels = [], {}
    for line in text.splitlines():
        line = line.split('/*')[0].strip()
        if not line or line.startswith(('#', '.option', '.text', 'SYM_FUNC', '*')):
            continue
        if line.endswith(':'):
            labels[line[:-1]] = len(prog)
            continue
        op, _, args = line.partition('\t')
        prog.append((op, [a.strip() for a in args.split(',')] if args else []))

    def r(x):
        return 0 if x == 'zero' else regs[x]

    def w(x, v):
        if x != 'zero':
            regs[x] = v & M64

    def addr(m):
        off, base = m.rstrip(')').split('(')
        return (r(base) + int(off)) & M64

    pc, steps = 0, 0
    while True:
        op, a = prog[pc]
        pc += 1
        steps += 1
        if op == 'ret':
            return steps
        if op == 'ld':
            p = addr(a[1])
            w(a[0], int.from_bytes(bytes(mem[p + i] for i in range(8)), 'little'))
        elif op == 'sd':
            p = addr(a[1])
            for i, byte in enumerate(r(a[0]).to_bytes(8, 'little')):
                mem[p + i] = byte
        elif op == 'th.rev':
            w(a[0], int.from_bytes(r(a[1]).to_bytes(8, 'little'), 'big'))
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


def selftest(text, cases):
    rnd = random.Random(20261012)
    for case in range(cases):
        raw = b'\xff' * 16 if case % 7 == 0 else rnd.randbytes(16)
        nblocks = case % 5 if case < 10 else 1 + rnd.randrange(6)
        data = rnd.randbytes(16 * nblocks)
        if case % 11 == 0:
            data = b'\xff' * len(data)
        acc0 = rnd.getrandbits(128)
        lo, hi = polyval_key(raw)
        ACC, KPB, DB, SP = 0x1000, 0x2000, 0x8003, 0x20000      # data misaligned
        mem = {}
        for base, words in ((ACC, [acc0 & M64, acc0 >> 64]), (KPB, kp_table(lo, hi))):
            for i, v in enumerate(words):
                for k, byte in enumerate(v.to_bytes(8, 'little')):
                    mem[base + 8 * i + k] = byte
        for i, byte in enumerate(data):
            mem[DB + i] = byte
        regs = {name: rnd.getrandbits(64) for name in ABI[1:]}
        regs.update({'sp': SP, 'a0': ACC, 'a1': KPB, 'a2': DB, 'a3': nblocks})
        before = dict(regs)
        execute(text, regs, mem)
        for name in ['sp', 'ra'] + [f's{i}' for i in range(12)]:
            assert regs[name] == before[name], f'case {case}: {name} not preserved'
        got = int.from_bytes(bytes(mem[ACC + i] for i in range(16)), 'little')
        want = ghash_ref(int.from_bytes(raw, 'big'), acc0, data)
        if got != want:
            sys.exit(f'self-test case {case}: got {got:032x}, want {want:032x}')
        leftovers = [p for p in mem if SP - FRAME <= p < SP and SP - FRAME + C_SLOT[0] <= p and mem[p]]
        assert nblocks == 0 or not leftovers, f'case {case}: product slots not wiped'
    return cases


def from_patch(patch):
    """The generated file as added by a kernel patch."""
    marker = 'diff --git a/lib/crypto/riscv/ghash-riscv64-c906.S'
    lines = patch.split(marker, 1)[1].split('\ndiff --git ', 1)[0].splitlines()
    start = next(i for i, l in enumerate(lines) if l.startswith('@@'))
    return ''.join(l[1:] + '\n' for l in lines[start + 1:] if l.startswith('+'))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument('--output', type=Path)
    g.add_argument('--check', type=Path)
    p.add_argument('--cases', type=int, default=200, help='self-test cases (default 200)')
    args = p.parse_args()
    text, n, cycles = render()
    cases = selftest(text, args.cases)
    if args.check:
        current = args.check.read_text()
        if args.check.suffix == '.patch':
            current = from_patch(current)
        if current != text:
            sys.exit(f'{args.check} is not up to date')
        print(f'{args.check}: up to date; {cases} self-test cases against a bit-serial GHASH')
    else:
        args.output.write_text(text)
        print(f'{args.output}: {n} instructions per block, {cycles} scheduled cycles; '
              f'{cases} self-test cases against a bit-serial GHASH')


if __name__ == '__main__':
    main()
