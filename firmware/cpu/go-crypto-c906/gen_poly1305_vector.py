#!/usr/bin/env python3
"""Generate the C906 vector Poly1305 kernel (radix 2^26, five blocks per group).

The C906 multiplies operands below 2^32 at one per cycle, but a multiply with
a wider operand blocks the pipeline for about four cycles, so the radix-2^64
CRYPTOGAMS code spends 40 of its 76 cycles per block in 10 multiplies. This
kernel keeps h and r in 26-bit limbs, where every product has operands below
2^32, and hashes five blocks per group:

- four blocks in XTheadVector lanes (VLEN 128, SEW 32): 25 th.vwmulu/th.vwmaccu
  (32x32 -> 64 bits, four lanes) per group, the carries in SEW 64, narrowed back
  with th.vnsrl;
- a fifth block in scalar registers (mul/th.mula), interleaved with the vector
  instructions: the in-order core issues scalar instructions while a vector
  instruction occupies the 64-bit vector datapath.

Every group multiplies all five accumulators by r^5, except the last one, where
lanes 0-3 multiply by r^5, r^4, r^3, r^2 and the scalar lane by r; the lanes
are then summed. The scalar code also splits the next group's four vector
blocks into limbs in a stack buffer (XTheadVector loads need aligned addresses;
the C906 reads misaligned scalar words fast), so the vector code only loads and
adds them.

    blocks(r, msg, groups, h)    groups >= 1, padbit 1
    r: 5 u64 limbs of the clamped key, radix 2^26
    h: 5 u64 limbs, radix 2^26, each below 2^27 on entry; on exit carried,
       h[1] below 2^26 + 2^7, the others below 2^26.

The prologue computes r^2..r^5 (about 200 scalar instructions). The body
uses neither ra (Go does not save it in leaf functions) nor x27 (Go's g), gp
or tp, and keeps sp. Its frame is FRAME bytes at base(sp): the limb buffer,
the saved h and r pointers, and the lane powers r^5..r^2. Each ABI wraps it.

The generator executes the kernel on random and extreme inputs against a
big-integer Poly1305 and checks, on every instruction, that scalar
multiplies have operands below 2^32 and that no 32-bit vector sum wraps. GNU as
assembles the kernel; every word must decode back to its mnemonic.

Usage: gen_poly1305_vector.py --binutils PREFIX --go (--output FILE | --check FILE)
"""
import argparse
import random
import re
import subprocess
import sys
import tempfile
from pathlib import Path

M26, M32, M64 = (1 << 26) - 1, (1 << 32) - 1, (1 << 64) - 1
P = (1 << 130) - 5
GROUP = 80
LANES = 96                      # the lane powers follow the limb buffer and the pointers
FRAME = LANES + 80

# Scalar registers. The r and h pointers move to the frame, freeing a0 as T2.
MSG, END = 'a1', 'a2'                 # a0 (r) and a3 (h) go to the frame
KM, KHI = 'a4', 'a5'
VL = 'a3'                              # 4 after the prologue: vl stays 4 at any VLEN
H = ['a6', 'a7', 't0', 't1', 't2']
R = ['s0', 's1', 's2', 's3', 's4']
S = [None, 's5', 's6', 's7', 's8']           # 5 * r
D = ['s9', 's10', 't3', 't4', 't5']
T1, T2 = 't6', 'a0'
# Vector registers.
VH = ['v4', 'v5', 'v6', 'v7', 'v8']
VR = ['v9', 'v10', 'v11', 'v12', 'v13']
VS = [None, 'v14', 'v15', 'v16', 'v17']
VD = ['v18', 'v20', 'v22', 'v24', 'v26']     # SEW 64 products, LMUL 2
VM = ['v0', 'v1', 'v2', 'v3', 'v28']        # message limbs
# Byte offset, extraction of the 26-bit limbs of a 16-byte block.
LIMBS = [(0, 'th.extu {t},{t},25,0'), (3, 'th.extu {t},{t},27,2'), (6, 'th.extu {t},{t},29,4'),
         (9, 'srliw {t},{t},6'), (12, 'srliw {t},{t},8')]


def term(i, j):
    """d_i gets h_j times r_(i-j), or s_(i-j+5) = 5 r_(i-j+5) where it wraps."""
    return ('r', i - j) if i >= j else ('s', i - j + 5)


def split_block(base, src, lane):
    """Limbs of the block at src(MSG) into buf[limb][lane]."""
    out = []
    for n, (off, ext) in enumerate(LIMBS):
        t = T1 if n % 2 == 0 else T2
        out += [f'lw {t},{src + off}({MSG})', ext.format(t=t), f'sw {t},{base + 16 * n + 4 * lane}(sp)']
    return out


def scalar_block(base, split_next):
    s = []
    for i, (off, ext) in enumerate(LIMBS):
        s += [f'lw {T1},{64 + off}({MSG})', ext.format(t=T1), f'add {H[i]},{H[i]},{T1}']
    s.append(f'add {H[4]},{H[4]},{KHI}')
    prods, first = [], [True] * 5
    for j in range(5):              # column order: consecutive products feed different d_i
        for i in range(5):
            kind, k = term(i, j)
            rr = R[k] if kind == 'r' else S[k]
            prods.append(f"{'mul' if first[i] else 'th.mula'} {D[i]},{H[j]},{rr}")
            first[i] = False
    if split_next:                  # T1/T2 are free between the products
        split = [x for lane in range(4) for x in split_block(base, GROUP + 16 * lane, lane)]
        merged, k = [], 0
        for n, p in enumerate(prods):
            merged.append(p)
            while k < len(split) and k < (n + 1) * len(split) / len(prods):
                merged.append(split[k])
                k += 1
        prods = merged + split[k:]
    s += prods
    for i in range(4):
        s += [f'srli {T1},{D[i]},26', f'th.extu {H[i]},{D[i]},25,0', f'add {D[i + 1]},{D[i + 1]},{T1}']
    s += [f'srli {T1},{D[4]},26', f'th.extu {H[4]},{D[4]},25,0', f'th.addsl {T1},{T1},{T1},2',
          f'add {H[0]},{H[0]},{T1}', f'srli {T1},{H[0]},26', f'th.extu {H[0]},{H[0]},25,0',
          f'add {H[1]},{H[1]},{T1}']
    return s


def vector_block(base):
    """(instruction, cycles) of the four-lane part of a group."""
    v = []

    def a(x, c=2):
        v.append((x, c))
    for i in range(5):
        a(f'addi {T2},sp,{base + 16 * i}', 0)
        a(f'th.vle.v {VM[i]},({T2})')
    for i in range(5):
        a(f'th.vadd.vv {VH[i]},{VH[i]},{VM[i]}')
    a(f'th.vadd.vx {VH[4]},{VH[4]},{KHI}')
    first = [True] * 5
    for j in range(5):
        for i in range(5):
            kind, k = term(i, j)
            rr = VR[k] if kind == 'r' else VS[k]
            a(f"{'th.vwmulu.vv' if first[i] else 'th.vwmaccu.vv'} {VD[i]},{VH[j]},{rr}", 4)
            first[i] = False
    a(f'th.vsetvli zero,{VL},e64,m2', 1)
    tmp = ['v28', 'v30']
    for i in range(4):
        a(f'th.vsrl.vi {tmp[i % 2]},{VD[i]},26', 4)
        a(f'th.vadd.vv {VD[i + 1]},{VD[i + 1]},{tmp[i % 2]}', 4)
    a(f'th.vsetvli zero,{VL},e32,m1', 1)
    for i in range(5):
        a(f'th.vnsrl.vi {VH[i]},{VD[i]},0', 4)
    a(f'th.vnsrl.vi v28,{VD[4]},26', 4)
    for i in range(5):
        a(f'th.vand.vx {VH[i]},{VH[i]},{KM}')
    a(f'th.vadd.vv {VH[0]},{VH[0]},v28')
    a('th.vsll.vi v28,v28,2')
    a(f'th.vadd.vv {VH[0]},{VH[0]},v28')
    a(f'th.vsrl.vi v29,{VH[0]},26', 4)
    a(f'th.vand.vx {VH[0]},{VH[0]},{KM}')
    a(f'th.vadd.vv {VH[1]},{VH[1]},v29', 4)
    return v


def group(base, split_next):
    """Vector and scalar parts interleaved by vector cycles."""
    vec, sca = vector_block(base), scalar_block(base, split_next)
    total = sum(c for _, c in vec)
    out, k = [], 0
    cum = 0
    for ins, c in vec:
        out.append(ins)
        cum += c
        # Not between an address and its vector load: both use T2.
        if ins.startswith('addi'):
            continue
        while k < len(sca) and (k + 0.5) * total / len(sca) <= cum:
            out.append(sca[k])
            k += 1
    out += sca[k:]
    # The vector loads read the buffer before the scalar code refills it.
    last_vle = max(i for i, x in enumerate(out) if x.startswith('th.vle.v'))
    t2_uses = [i for i, x in enumerate(out) if re.search(rf'\b{T2}\b', x) and not x.startswith(('th.vle.v', f'addi {T2},sp'))]
    stores = [i for i, x in enumerate(out) if x.startswith('sw ')]
    assert not t2_uses or min(t2_uses) > last_vle
    assert not stores or min(stores) > last_vle
    return out


def mul26(a, b, bs, out):
    """out = a * b mod 2^130 - 5 (bs = 5 * b[1..4]); out is carried, out[1] < 2^26 + 2^7."""
    m, first = [], [True] * 5
    for j in range(5):
        for i in range(5):
            kind, k = term(i, j)
            m.append(f"{'mul' if first[i] else 'th.mula'} {out[i]},{a[j]},{b[k] if kind == 'r' else bs[k]}")
            first[i] = False
    for i in range(4):
        m += [f'srli {T1},{out[i]},26', f'th.extu {out[i]},{out[i]},25,0', f'add {out[i + 1]},{out[i + 1]},{T1}']
    m += [f'srli {T1},{out[4]},26', f'th.extu {out[4]},{out[4]},25,0', f'th.addsl {T1},{T1},{T1},2',
          f'add {out[0]},{out[0]},{T1}', f'srli {T1},{out[0]},26', f'th.extu {out[0]},{out[0]},25,0',
          f'add {out[1]},{out[1]},{T1}']
    return m


def powers(base):
    """r^2..r^5 from r (in R, S): r^5 stays in R/S, the lane table
    buf2[limb][lane] = r^5, r^4, r^3, r^2 goes to the frame. a0, a3, a4 and a5
    are free here (their values are in the frame or set later)."""
    spare = [None, 'a3', 'a4', 'a5', 'a0']

    def store(regs, lane):
        return [f'sw {x},{base + LANES + 16 * i + 4 * lane}(sp)' for i, x in enumerate(regs)]
    p = mul26(R, R, S, H) + store(H, 3)                  # r^2 in H
    p += mul26(H, R, S, D) + store(D, 2)                 # r^3 in D
    p += [f'th.addsl {spare[i]},{H[i]},{H[i]},2' for i in range(1, 5)]
    p += mul26(H, H, spare, D) + store(D, 1)             # r^4 in D
    p += mul26(D, R, S, H) + store(H, 0)                 # r^5 in H
    p += [f'addi {R[i]},{H[i]},0' for i in range(5)]
    p += [f'th.addsl {S[i]},{R[i]},{R[i]},2' for i in range(1, 5)]
    return p


def kernel(base):
    """The body: arguments in a0 (r), a1 (msg), a2 (groups), a3 (h)."""
    k = []
    k += [f'sd a3,{base + 80}(sp)', f'sd a0,{base + 88}(sp)']
    k += [f'ld {R[i]},{8 * i}(a0)' for i in range(5)]
    k += [f'th.addsl {S[i]},{R[i]},{R[i]},2' for i in range(1, 5)]
    k += powers(base)
    k += [f'li {KM},0x3ffffff', f'li {KHI},0x1000000', f'li {VL},4']
    for lane in range(4):
        k += split_block(base, 16 * lane, lane)
    k.append(f'th.vsetvli zero,{VL},e32,m1')
    k += [f'th.vmv.v.x {VR[i]},{R[i]}' for i in range(5)]
    k += [f'th.vmv.v.x {VS[i]},{S[i]}' for i in range(1, 5)]
    k.append(f'ld {T2},{base + 80}(sp)')            # the powers used a3
    for i in range(5):
        k += [f'ld {T1},{8 * i}({T2})', f'th.vmv.v.i {VH[i]},0', f'th.vmv.s.x {VH[i]},{T1}', f'li {H[i]},0']
    k += [f'addi {END},{END},-1', f'li {T1},{GROUP}', f'mul {END},{END},{T1}', f'add {END},{END},{MSG}',
          f'beq {MSG},{END},2f', '1:']
    k += group(base, True)
    k += [f'addi {MSG},{MSG},{GROUP}', f'bne {MSG},{END},1b', '2:']
    k.append(f'ld {T1},{base + 88}(sp)')
    k += [f'ld {R[i]},{8 * i}({T1})' for i in range(5)]
    k += [f'th.addsl {S[i]},{R[i]},{R[i]},2' for i in range(1, 5)]
    for i in range(5):
        k += [f'addi {T2},sp,{base + LANES + 16 * i}', f'th.vle.v {VR[i]},({T2})']
    for i in range(1, 5):
        k += [f'th.vsll.vi {VS[i]},{VR[i]},2', f'th.vadd.vv {VS[i]},{VS[i]},{VR[i]}']
    k += group(base, False)
    for i in range(5):              # h_i = scalar lane + the four vector lanes
        k += [f'th.vmv.s.x v0,{H[i]}', f'th.vredsum.vs v0,{VH[i]},v0', f'th.vmv.x.s {H[i]},v0']
    for i in range(4):
        k += [f'srli {T1},{H[i]},26', f'th.extu {H[i]},{H[i]},25,0', f'add {H[i + 1]},{H[i + 1]},{T1}']
    k += [f'srli {T1},{H[4]},26', f'th.extu {H[4]},{H[4]},25,0', f'th.addsl {T1},{T1},{T1},2',
          f'add {H[0]},{H[0]},{T1}', f'srli {T1},{H[0]},26', f'th.extu {H[0]},{H[0]},25,0',
          f'add {H[1]},{H[1]},{T1}']
    k.append(f'ld {T1},{base + 80}(sp)')
    k += [f'sd {H[i]},{8 * i}({T1})' for i in range(5)]
    return k


# ---------------------------------------------------------------- simulator

class Fault(Exception):
    pass


def execute(prog, regs, mem):
    """Run the kernel; regs: name -> value, mem: bytearray-like dict addr -> byte."""
    labels = {x[:-1]: n for n, x in enumerate(prog) if x.endswith(':')}
    vr = {f'v{i}': [0] * 4 for i in range(32)}
    sew = None

    def r(x):
        return 0 if x == 'zero' else regs[x]

    def w(x, v):
        if x != 'zero':
            regs[x] = v & M64

    def addr(m):
        off, base = m.rstrip(')').split('(')
        return (r(base) + int(off or 0)) & M64

    def load(p, n):
        return int.from_bytes(bytes(mem[p + i] for i in range(n)), 'little')

    def store(p, n, v):
        for i, b in enumerate((v & ((1 << 8 * n) - 1)).to_bytes(n, 'little')):
            mem[p + i] = b

    def vget64(name):             # SEW 64, LMUL 2: elements 0,1 in vN, 2,3 in vN+1
        n = int(name[1:])
        assert n % 2 == 0
        a, b = vr[f'v{n}'], vr[f'v{n + 1}']
        return [a[0] | a[1] << 32, a[2] | a[3] << 32, b[0] | b[1] << 32, b[2] | b[3] << 32]

    def vset64(name, vals):
        n = int(name[1:])
        flat = []
        for x in vals:
            flat += [x & M32, (x >> 32) & M32]
        vr[f'v{n}'], vr[f'v{n + 1}'] = flat[:4], flat[4:]

    def check32(vals, what):
        for x in vals:
            if x > M32:
                raise Fault(f'32-bit vector overflow in {what}')
        return vals

    pc = 0
    while pc < len(prog):
        ins = prog[pc]
        pc += 1
        if ins.endswith(':'):
            continue
        op, _, rest = ins.partition(' ')
        a = rest.split(',') if rest else []
        if op == 'th.vsetvli':
            if a[1] == 'zero' or r(a[1]) != 4:
                raise Fault(f'vl is not 4: {ins}')     # x0 would mean VLMAX
            sew = {'e32': 32, 'e64': 64}[a[2]]
            continue
        if op.startswith('th.v'):
            assert sew is not None
            if op == 'th.vle.v':
                p = addr(a[1])
                if p % 4:
                    raise Fault('misaligned vector load')
                vr[a[0]] = [load(p + 4 * i, 4) for i in range(4)]
            elif sew == 64 and op == 'th.vsrl.vi':
                vset64(a[0], [x >> int(a[2]) for x in vget64(a[1])])
            elif sew == 64 and op == 'th.vadd.vv':
                vals = [x + y for x, y in zip(vget64(a[1]), vget64(a[2]))]
                if any(x > M64 for x in vals):
                    raise Fault('64-bit vector overflow')
                vset64(a[0], vals)
            elif sew != 32:
                raise ValueError(f'{op} at SEW {sew}')
            elif op in ('th.vwmulu.vv', 'th.vwmaccu.vv'):
                prod = [x * y for x, y in zip(vr[a[1]], vr[a[2]])]
                acc = vget64(a[0]) if op == 'th.vwmaccu.vv' else [0] * 4
                vals = [x + y for x, y in zip(acc, prod)]
                if any(x > M64 for x in vals):
                    raise Fault('64-bit accumulator overflow')
                vset64(a[0], vals)
            elif op == 'th.vnsrl.vi':
                sh = int(a[2])
                vals = [x >> sh for x in vget64(a[1])]
                if sh and any(x > M32 for x in vals):
                    raise Fault('narrowed carry does not fit 32 bits')
                vr[a[0]] = [x & M32 for x in vals]
            elif op == 'th.vadd.vv':
                vr[a[0]] = check32([x + y for x, y in zip(vr[a[1]], vr[a[2]])], ins)
            elif op == 'th.vadd.vx':
                vr[a[0]] = check32([x + r(a[2]) for x in vr[a[1]]], ins)
            elif op == 'th.vand.vx':
                vr[a[0]] = [x & r(a[2]) & M32 for x in vr[a[1]]]
            elif op == 'th.vsrl.vi':
                vr[a[0]] = [x >> int(a[2]) for x in vr[a[1]]]
            elif op == 'th.vsll.vi':
                vr[a[0]] = check32([x << int(a[2]) for x in vr[a[1]]], ins)
            elif op == 'th.vmv.v.x':
                if r(a[1]) > M32:
                    raise Fault('broadcast above 32 bits')
                vr[a[0]] = [r(a[1])] * 4
            elif op == 'th.vmv.v.i':
                vr[a[0]] = [int(a[1]) & M32] * 4
            elif op == 'th.vmv.s.x':
                if r(a[1]) > M32:
                    raise Fault('element above 32 bits')
                vr[a[0]] = [r(a[1])] + vr[a[0]][1:]
            elif op == 'th.vredsum.vs':
                vr[a[0]] = check32([vr[a[2]][0] + sum(vr[a[1]])], ins) + vr[a[0]][1:]
            elif op == 'th.vmv.x.s':
                if vr[a[1]][0] >> 31:
                    raise Fault('vmv.x.s of a negative element')
                w(a[0], vr[a[1]][0])
            else:
                raise ValueError(op)
            continue
        if op == 'lw':
            x = load(addr(a[1]), 4)
            w(a[0], x - (1 << 32) if x >> 31 else x)
        elif op == 'ld':
            w(a[0], load(addr(a[1]), 8))
        elif op == 'sw':
            store(addr(a[1]), 4, r(a[0]))
        elif op == 'sd':
            store(addr(a[1]), 8, r(a[0]))
        elif op == 'li':
            w(a[0], int(a[1], 0))
        elif op == 'th.extu':
            msb, lsb = int(a[2]), int(a[3])
            w(a[0], (r(a[1]) >> lsb) & ((1 << (msb - lsb + 1)) - 1))
        elif op == 'srliw':
            w(a[0], (r(a[1]) & M32) >> int(a[2]))
        elif op == 'srli':
            w(a[0], r(a[1]) >> int(a[2]))
        elif op == 'addi':
            w(a[0], r(a[1]) + int(a[2]))
        elif op == 'add':
            w(a[0], r(a[1]) + r(a[2]))
        elif op == 'th.addsl':
            w(a[0], r(a[1]) + (r(a[2]) << int(a[3])))
        elif op in ('mul', 'th.mula'):
            x, y = r(a[1]), r(a[2])
            if x > M32 or y > M32:
                raise Fault(f'multiply operand above 2^32: {ins}')
            w(a[0], x * y + (r(a[0]) if op == 'th.mula' else 0))
        elif op in ('beq', 'bne'):
            if (r(a[0]) == r(a[1])) == (op == 'beq'):
                pc = labels[a[2][:-1]] + 1      # local labels 1b, 2f: each is defined once
        else:
            raise ValueError(op)


def limbs(x):
    return [(x >> (26 * i)) & M26 for i in range(4)] + [x >> 104]


def run_case(prog, base, rnd, r, h, msg, groups):
    sp = 0x7000_0000
    mem = {}
    key_addr, h_addr, msg_addr = 0x1000, 0x2000, 0x3000 + rnd.randrange(16)
    for i, b in enumerate(msg):
        mem[msg_addr + i] = b
    for i, x in enumerate(limbs(r)):
        for j, b in enumerate(x.to_bytes(8, 'little')):
            mem[key_addr + 8 * i + j] = b
    for i, x in enumerate(h):
        for j, b in enumerate(x.to_bytes(8, 'little')):
            mem[h_addr + 8 * i + j] = b
    for i in range(base + FRAME):
        mem[sp + i] = 0xa5
    regs = {x: 0x5a5a for x in ['ra', 'gp', 'tp', 's11'] + H + R + S[1:] + D + [T1, KM, KHI]}
    regs.update(sp=sp, a0=key_addr, a1=msg_addr, a2=groups, a3=h_addr)
    saved = {x: regs[x] for x in ['ra', 'gp', 'tp', 's11', 'sp']}
    execute(prog, regs, mem)
    assert all(regs[x] == v for x, v in saved.items()), 'ra, gp, tp, s11 or sp changed'
    out = [int.from_bytes(bytes(mem[h_addr + 8 * i + j] for j in range(8)), 'little') for i in range(5)]
    want = sum(x << (26 * i) for i, x in enumerate(h))
    for n in range(5 * groups):
        want = (want + int.from_bytes(msg[16 * n:16 * n + 16], 'little') + (1 << 128)) * r % P
    got = sum(x << (26 * i) for i, x in enumerate(out))
    if got % P != want:
        raise Fault('wrong result')
    if out[1] >= M26 + 1 + (1 << 7) or any(out[i] > M26 for i in (0, 2, 3, 4)):
        raise Fault(f'output limbs not carried: {out}')


def selftest(prog, base):
    rnd = random.Random(1305)
    rmax = 0x0ffffffc0ffffffc0ffffffc0fffffff
    # Negative control: without the 2^128 pad bit of the scalar lane the
    # results must be wrong.
    broken = [x for x in prog if x != f'add {H[4]},{H[4]},{KHI}']
    assert len(broken) == len(prog) - 2
    try:
        run_case(broken, base, rnd, rmax, [0] * 5, rnd.randbytes(2 * GROUP), 2)
    except Fault:
        pass
    else:
        sys.exit('self-test: the negative control passed')
    cases = 0
    for t in range(400):
        r = rmax if t % 3 == 0 else rnd.getrandbits(128) & rmax
        groups = 1 + t % 7
        if t % 4 == 0:      # extreme: all-ones message, h limbs at their entry bound
            msg = b'\xff' * (GROUP * groups)
            h = [(1 << 27) - 1] * 5
        else:
            msg = rnd.randbytes(GROUP * groups)
            h = [rnd.randrange(1 << 27) for _ in range(5)]
        run_case(prog, base, rnd, r, h, msg, groups)
        cases += 1
    return cases


# ---------------------------------------------------------------- assembly

ISA = 'rv64gc_xtheadba_xtheadbb_xtheadmac_xtheadvector'


def assemble(prog, binutils):
    """(word, mnemonic) per instruction; every word must decode to its mnemonic."""
    text = '.option norvc\n.text\nk:\n' + ''.join(x + '\n' if x.endswith(':') else '\t' + x + '\n' for x in prog)
    with tempfile.TemporaryDirectory() as d:
        src, obj = Path(d) / 'k.s', Path(d) / 'k.o'
        src.write_text(text)
        subprocess.run([binutils + 'as', f'-march={ISA}', '-o', str(obj), str(src)], check=True)
        dump = subprocess.run([binutils + 'objdump', '-d', str(obj)],
                              check=True, capture_output=True, text=True).stdout
    out = []
    for addr, word, ins in re.findall(r'^\s*([0-9a-f]+):\s+([0-9a-f]{8})\s+(.*)$', dump, re.M):
        ins = ' '.join(ins.split('#')[0].split())
        ins = re.sub(r'\s*<[^>]*>$', '', ins)
        m = re.match(r'(b\w*|j|jal)\s+(.*?)([0-9a-f]+)$', ins)
        if m:
            ins = f'{m.group(1)} {m.group(2)}.{int(m.group(3), 16) - int(addr, 16):+d}'
        out.append((int(word, 16), ins))
    if re.search(r'^\s*[0-9a-f]+:\s+[0-9a-f]{4}\s', dump, re.M):
        sys.exit('the kernel has 16-bit instructions')
    for w, ins in out:
        if re.search(r'\b(ra|gp|tp|s11|x1|x3|x4|x27)\b', ins):
            sys.exit(f'kernel uses a reserved register: {ins}')
        if ins.startswith('th.v') and ins.split()[0] not in {x.split()[0] for x in prog}:
            sys.exit(f'unexpected vector instruction {ins}')
    return out


GO_HEADER = '''\
// Code generated by firmware/cpu/go-crypto-c906/gen_poly1305_vector.py. DO NOT EDIT.

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

#include "textflag.h"

// poly1305BlocksVector hashes groups of five 16-byte blocks (padbit 1) into h,
// in radix 2^26: four blocks in XTheadVector lanes and one in scalar
// registers per group (see gen_poly1305_vector.py and README.md). r and h are
// 26-bit limbs; h's limbs are below 2^27 on entry and carried on exit. The
// frame holds the limb buffer of the next group, the h and r pointers and
// r^5..r^2, which the prologue computes. It needs XTheadVector with vector
// state enabled.
//
// func poly1305BlocksVector(r *[5]uint64, msg *byte, groups int, h *[5]uint64)
TEXT ·poly1305BlocksVector(SB), NOSPLIT, ${frame}-32
	MOV	r+0(FP), X10
	MOV	msg+8(FP), X11
	MOV	groups+16(FP), X12
	MOV	h+24(FP), X13
'''


def render_go(words):
    text = GO_HEADER.replace('{frame}', str(FRAME))
    text += ''.join(f'\tWORD\t$0x{w:08x}\t// {i}\n' for w, i in words)
    return text + '\tRET\n'


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('--binutils', required=True)
    p.add_argument('--go', action='store_true', help='Go ABI0 (sum_vector_riscv64.s)')
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument('--output', type=Path)
    g.add_argument('--check', type=Path)
    a = p.parse_args()
    if not a.go:
        p.error('--go is the only output so far')
    base = 8                    # Go: locals start at 8(SP); 0(SP) is the return address slot
    prog = kernel(base)
    try:
        cases = selftest(prog, base)
    except Fault as e:
        sys.exit(f'self-test: {e}')
    text = render_go(assemble(prog, a.binutils))
    if a.check:
        if a.check.read_text() != text:
            sys.exit(f'{a.check} is not up to date')
        print(f'{a.check}: up to date ({cases} simulated cases)')
    else:
        a.output.write_text(text)
        print(f'{a.output}: written ({cases} simulated cases)')


if __name__ == '__main__':
    main()
