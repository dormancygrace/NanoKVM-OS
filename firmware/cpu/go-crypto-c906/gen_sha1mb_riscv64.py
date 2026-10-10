#!/usr/bin/env python3
"""Generate server/internal/sha1mb/sha1mb_riscv64.s, the C906 multi-buffer SHA-1.

sha1x4 hashes four independent block streams at once: lane j of XTheadVector
register v(k) holds word k of stream j (VLEN 128, SEW 32, LMUL 1). The streams
are described by per-lane segment queues built in Go (sha1mb.go); see the
header of the generated file for the protocol.

Vector instructions are WORDs, encoded and checked against GNU as/objdump by
the encoder of gen_chacha_riscv64.py (same directory).

Usage: gen_sha1mb_riscv64.py --binutils PREFIX (--output FILE | --check FILE)
"""
from pathlib import Path
import argparse, itertools, sys

from gen_chacha_riscv64 import Asm, vv, vi, vx, vmv_v_v, vid, vsetvli, vle, vse, verify

# How the message words get big-endian: 'scalar' byte loads in the staging
# copy (shipped), or 'vrgather' (measurement only): the staging copies
# little-endian words and the vector unit swaps them with th.vrgather.vv.
BSWAP = 'scalar'

# ------------------------------------------------------------- registers ---
T = [5, 6, 7, 8]          # staging temporaries; T[3] also collects "any" flags
P = [9, 10, 11, 12]       # lane source pointers for the block being staged
NXT, CUR = 13, 14         # staging buffer being filled / being hashed
K = [15, 16, 17, 18]      # round constants
NSTEPS, FR, SCRATCH, MSGS, SEGS, INIT = 19, 20, 21, 22, 23, 24
A0, A1, A2, A3, A4 = 25, 26, 28, 29, 30  # temporaries outside the rounds
# X27 (g), X31 (assembler temporary) and X1..X4 are not used.

W = list(range(16))               # message schedule ring, v0..v15
STATE = [16, 17, 18, 19, 20]      # a..e
H = [21, 22, 23, 24, 25]          # chaining value
TMPS = [26, 27, 28, 29, 30, 31]
IDX = 31                          # byte index of the vrgather variant

# ----------------------------------------------------------- frame layout ---
# A staging buffer: words W[t] of the four lanes at 16*t (lane j at +4*j),
# then per-lane metadata for that step.
START = 256   # 4 x uint32: segment flags & 3 if the block starts a message
FIN = 272     # 4 x uint64: address of the 20-byte tag to write after it, or 0
ANY = 304     # uint64: nonzero if any START or FIN is set
BUFSIZE = 320
BUFA, BUFB = 0, BUFSIZE
SPILL = 2 * BUFSIZE                 # 5 x 16 bytes: H spilled for lane fixups
CURS = SPILL + 80                   # 4 lane cursors of 32 bytes
C_PTR, C_REM, C_NEXT, C_END, C_FLAGS, C_TAG = 0, 8, 12, 16, 20, 24
FRAME = CURS + 4 * 32
# seg (sha1mb.go): part, off, n, flags, tag, pad (uint32 each).
S_PART, S_OFF, S_N, S_FLAGS, S_TAG, SEGSIZE = 0, 4, 8, 12, 16, 24
KS = [0x5A827999, 0x6ED9EBA1, 0x8F1BBCDC, 0xCA62C1D6]
NAME = 'sha1x4'

# ------------------------------------------------------------ vector ops ---


class Op:
    def __init__(self, v, dst, srcs):
        self.v, self.dst, self.srcs = v, dst, srcs


def o_vv(op, d, a, b):
    return Op(vv(op, d, a, b), d, [a, b])


def o_vi(op, d, a, imm):
    return Op(vi(op, d, a, imm), d, [a])


def o_vx(op, d, a, r):
    return Op(vx(op, d, a, r), d, [a])


def step_ops(dist):
    """Vector ops of one 64-byte step (80 rounds) in priority order."""
    ops = []
    tmp = itertools.cycle(TMPS)
    st = list(STATE)
    ops += [Op(vmv_v_v(st[i], H[i]), st[i], [H[i]]) for i in range(5)]

    def sched(u):
        # W[u] = rol1(W[u-3] ^ W[u-8] ^ W[u-14] ^ W[u-16]), in W[u-16]'s register.
        r, x, y = W[u % 16], next(tmp), next(tmp)
        ops.extend([o_vv('vxor', x, W[(u - 3) % 16], W[(u - 8) % 16]),
                    o_vv('vxor', r, r, W[(u - 14) % 16]),
                    o_vv('vxor', r, r, x),
                    o_vi('vsll', y, r, 1),
                    o_vi('vsrl', r, r, 31),
                    o_vv('vor', r, r, y)])

    for t in range(80):
        a, b, c, d, e = st
        if 16 <= t + dist < 80:
            sched(t + dist)
        ops.append(o_vv('vadd', e, e, W[t % 16]))
        ops.append(o_vx('vadd', e, e, K[t // 20]))
        x, y = next(tmp), next(tmp)
        if t < 20:    # f = d ^ (b & (c ^ d))
            ops += [o_vv('vxor', x, c, d), o_vv('vand', x, x, b), o_vv('vxor', x, x, d),
                    o_vv('vadd', e, e, x)]
        elif 40 <= t < 60:   # f = (b & c) + (d & (b ^ c)), the terms are disjoint
            ops += [o_vv('vand', x, b, c), o_vv('vadd', e, e, x),
                    o_vv('vxor', y, b, c), o_vv('vand', y, y, d), o_vv('vadd', e, e, y)]
        else:         # f = b ^ c ^ d
            ops += [o_vv('vxor', x, b, c), o_vv('vxor', x, x, d), o_vv('vadd', e, e, x)]
        # e += rol5(a): the two shifted halves are disjoint, so add both.
        x, y = next(tmp), next(tmp)
        ops += [o_vi('vsrl', x, a, 27), o_vv('vadd', e, e, x),
                o_vi('vsll', y, a, 5), o_vv('vadd', e, e, y)]
        # b = rol30(b): shift left, shift right, or (no vector rotate).
        x = next(tmp)
        ops += [o_vi('vsll', x, b, 30), o_vi('vsrl', b, b, 2), o_vv('vor', b, b, x)]
        st = [e, a, b, c, d]
    assert st == STATE
    ops += [o_vv('vadd', H[i], H[i], st[i]) for i in range(5)]
    return ops


def schedule(ops, lat):
    """List-schedule ops for an in-order core with one 64-bit vector datapath:
    every op occupies it for 2 cycles, a result is usable lat cycles after its
    producer issued. Returns the new order and the estimated cycles."""
    n = len(ops)
    preds = [dict() for _ in range(n)]
    last_w, readers = {}, {}
    for i, o in enumerate(ops):
        def dep(k, d):
            preds[i][k] = max(preds[i].get(k, 0), d)
        for r in o.srcs:
            if r in last_w:
                dep(last_w[r], lat)
        r = o.dst
        if r in last_w:
            dep(last_w[r], 1)
        for k in readers.get(r, []):
            dep(k, 1)
        last_w[r], readers[r] = i, []
        for s in o.srcs:
            if s != r:
                readers.setdefault(s, []).append(i)
    succs = [[] for _ in range(n)]
    for i, p in enumerate(preds):
        for k in p:
            succs[k].append(i)
    npred = [len(p) for p in preds]
    ready = [0] * n
    avail = [i for i in range(n) if npred[i] == 0]
    out, t = [], 0
    while avail:
        best = min(avail, key=lambda i: (max(ready[i], t), i))
        avail.remove(best)
        s = max(ready[best], t)
        out.append(ops[best])
        t = s + 2
        for k in succs[best]:
            ready[k] = max(ready[k], s + preds[k][best])
            npred[k] -= 1
            if npred[k] == 0:
                avail.append(k)
    assert len(out) == n
    return out, t

# ------------------------------------------------------------ scalar code ---


def stage_ops():
    """Copy the four blocks at P[0..3] to NXT as big-endian words, transposed."""
    out = []
    for t in range(16):
        for j in range(4):
            p = P[j]
            if BSWAP == 'vrgather':
                # Little-endian; misaligned scalar loads are fast on the C906.
                out += [f'MOVWU\t{4 * t}(X{p}), X{T[0]}', f'MOVW\tX{T[0]}, {16 * t + 4 * j}(X{NXT})']
                continue
            out += [f'MOVBU\t{4 * t}(X{p}), X{T[0]}', f'MOVBU\t{4 * t + 1}(X{p}), X{T[1]}',
                    f'MOVBU\t{4 * t + 2}(X{p}), X{T[2]}', f'MOVBU\t{4 * t + 3}(X{p}), X{T[3]}',
                    f'SLL\t$24, X{T[0]}', f'SLL\t$16, X{T[1]}', f'SLL\t$8, X{T[2]}',
                    f'OR\tX{T[1]}, X{T[0]}', f'OR\tX{T[3]}, X{T[2]}', f'OR\tX{T[2]}, X{T[0]}',
                    f'MOVW\tX{T[0]}, {16 * t + 4 * j}(X{NXT})']
    return out


def advance(a, sfx):
    """Point P[j] at lane j's next block, record its START/FIN metadata in NXT."""
    anyr = T[3]
    a(f'MOV\tX0, X{anyr}')
    for j in range(4):
        c = CURS + 32 * j

        def L(s):
            return f'{s}{j}{sfx}'
        a(f'MOVWU\t{c + C_REM}(X{FR}), X{A0}', f'BNEZ\tX{A0}, {L("cont")}')
        a(f'MOVWU\t{c + C_NEXT}(X{FR}), X{A1}', f'MOVWU\t{c + C_END}(X{FR}), X{A2}')
        a(f'BEQ\tX{A1}, X{A2}, {L("idle")}')
        # Next segment: A4 = segs + 24*next.
        a(f'SLL\t$3, X{A1}, X{A3}', f'SLL\t$4, X{A1}, X{A4}', f'ADD\tX{A3}, X{A4}', f'ADD\tX{SEGS}, X{A4}')
        a(f'ADD\t$1, X{A1}', f'MOVW\tX{A1}, {c + C_NEXT}(X{FR})')
        a(f'MOVWU\t{S_PART}(X{A4}), X{A3}', f'MOVWU\t{S_OFF}(X{A4}), X{A1}', f'MOVWU\t{S_N}(X{A4}), X{A0}')
        a(f'MOV\tX{SCRATCH}, X{A2}')
        # part == ^0 means scratch; else the data pointer of msgs[part/3][part%3],
        # the slice header at msgs + 24*part.
        a(f'ADDW\t$1, X{A3}, X{T[0]}', f'BEQZ\tX{T[0]}, {L("scr")}')
        a(f'SLL\t$3, X{A3}, X{T[0]}', f'SLL\t$4, X{A3}, X{T[1]}', f'ADD\tX{T[0]}, X{T[1]}',
          f'ADD\tX{MSGS}, X{T[1]}', f'MOV\t(X{T[1]}), X{A2}')
        a.label(L('scr'))
        a(f'ADD\tX{A2}, X{A1}, X{P[j]}')
        a(f'MOVWU\t{S_FLAGS}(X{A4}), X{A3}', f'MOVW\tX{A3}, {c + C_FLAGS}(X{FR})')
        a(f'MOVWU\t{S_TAG}(X{A4}), X{T[0]}', f'MOV\ttags+40(FP), X{T[1]}', f'ADD\tX{T[1]}, X{T[0]}',
          f'MOV\tX{T[0]}, {c + C_TAG}(X{FR})')
        a(f'AND\t$3, X{A3}', f'MOVW\tX{A3}, {START + 4 * j}(X{NXT})', f'OR\tX{A3}, X{anyr}')
        a(f'JMP\t{L("have")}')
        a.label(L('cont'))
        a(f'MOV\t{c + C_PTR}(X{FR}), X{P[j]}', f'MOVW\tX0, {START + 4 * j}(X{NXT})')
        a.label(L('have'))
        a(f'ADD\t$64, X{P[j]}, X{T[0]}', f'MOV\tX{T[0]}, {c + C_PTR}(X{FR})')
        a(f'SUB\t$1, X{A0}', f'MOVW\tX{A0}, {c + C_REM}(X{FR})')
        # The last block of a segment with a tag: write the tag after it.
        a(f'MOV\tX0, X{T[1]}', f'BNEZ\tX{A0}, {L("nofin")}')
        a(f'MOVWU\t{c + C_FLAGS}(X{FR}), X{T[0]}', f'AND\t$4, X{T[0]}', f'BEQZ\tX{T[0]}, {L("nofin")}')
        a(f'MOV\t{c + C_TAG}(X{FR}), X{T[1]}', f'OR\tX{T[1]}, X{anyr}')
        a.label(L('nofin'))
        a(f'MOV\tX{T[1]}, {FIN + 8 * j}(X{NXT})', f'JMP\t{L("done")}')
        # Queue empty: hash the zero block at scratch+0, never start or finish.
        a.label(L('idle'))
        a(f'MOV\tX{SCRATCH}, X{P[j]}', f'MOVW\tX0, {START + 4 * j}(X{NXT})', f'MOV\tX0, {FIN + 8 * j}(X{NXT})')
        a.label(L('done'))
    a(f'MOV\tX{anyr}, {ANY}(X{NXT})')


def fixup(a, sfx):
    """Between steps: write tags of lanes finished in CUR, set the chaining
    value of lanes starting a message in NXT (ipad, or opad for an outer block
    whose words 0..4 are the inner hash)."""
    a(f'MOV\t{ANY}(X{CUR}), X{A0}', f'MOV\t{ANY}(X{NXT}), X{A1}', f'OR\tX{A0}, X{A1}',
      f'BEQZ\tX{A1}, nofix{sfx}')
    a(f'ADD\t${SPILL}, X{FR}, X{A4}')
    for i in range(5):
        a(vse(H[i], A4))
        if i < 4:
            a(f'ADD\t$16, X{A4}')
    for j in range(4):
        def L(s):
            return f'fix{s}{j}{sfx}'
        a(f'MOV\t{FIN + 8 * j}(X{CUR}), X{A0}', f'BEQZ\tX{A0}, {L("nofin")}')
        for i in range(5):
            sp = SPILL + 16 * i + 4 * j
            a(f'MOVWU\t{sp}(X{FR}), X{A1}',
              f'SRL\t$24, X{A1}, X{A2}', f'MOVB\tX{A2}, {4 * i}(X{A0})',
              f'SRL\t$16, X{A1}, X{A2}', f'MOVB\tX{A2}, {4 * i + 1}(X{A0})',
              f'SRL\t$8, X{A1}, X{A2}', f'MOVB\tX{A2}, {4 * i + 2}(X{A0})',
              f'MOVB\tX{A1}, {4 * i + 3}(X{A0})')
        a.label(L('nofin'))
        a(f'MOVWU\t{START + 4 * j}(X{NXT}), X{A0}', f'BEQZ\tX{A0}, {L("nostart")}')
        a(f'AND\t$2, X{A0}', f'BEQZ\tX{A0}, {L("inner")}')
        for i in range(5):
            sp, w = SPILL + 16 * i + 4 * j, 16 * i + 4 * j
            a(f'MOVWU\t{sp}(X{FR}), X{A1}')
            if BSWAP == 'vrgather':  # staged words are swapped after loading
                a(f'SRL\t$24, X{A1}, X{A2}', f'MOVB\tX{A2}, {w}(X{NXT})',
                  f'SRL\t$16, X{A1}, X{A2}', f'MOVB\tX{A2}, {w + 1}(X{NXT})',
                  f'SRL\t$8, X{A1}, X{A2}', f'MOVB\tX{A2}, {w + 2}(X{NXT})',
                  f'MOVB\tX{A1}, {w + 3}(X{NXT})')
            else:
                a(f'MOVW\tX{A1}, {w}(X{NXT})')
            a(f'MOVWU\t{20 + 4 * i}(X{INIT}), X{A1}', f'MOVW\tX{A1}, {sp}(X{FR})')
        a(f'JMP\t{L("nostart")}')
        a.label(L('inner'))
        for i in range(5):
            a(f'MOVWU\t{4 * i}(X{INIT}), X{A1}', f'MOVW\tX{A1}, {SPILL + 16 * i + 4 * j}(X{FR})')
        a.label(L('nostart'))
    a(f'ADD\t${SPILL}, X{FR}, X{A4}')
    for i in range(5):
        a(vle(H[i], A4))
        if i < 4:
            a(f'ADD\t$16, X{A4}')
    a.label(f'nofix{sfx}')


def swap(a):
    a(f'MOV\tX{CUR}, X{A0}', f'MOV\tX{NXT}, X{CUR}', f'MOV\tX{A0}, X{NXT}')


def build(lat, dist, stage=True):
    a = Asm()
    a(f'// func {NAME}(msgs *Message, scratch *byte, segs *seg, lanes *[4][2]uint32, init *[2][5]uint32, tags *[20]byte, nsteps int)')
    a(f'TEXT ·{NAME}(SB), 0, ${FRAME}-56')
    a(f'MOV\tmsgs+0(FP), X{MSGS}', f'MOV\tscratch+8(FP), X{SCRATCH}', f'MOV\tsegs+16(FP), X{SEGS}',
      f'MOV\tinit+32(FP), X{INIT}', f'MOV\tnsteps+48(FP), X{NSTEPS}')
    a(f'BEQZ\tX{NSTEPS}, done')
    a(f'ADD\t$8, X2, X{FR}', f'MOV\tlanes+24(FP), X{A0}')
    for j in range(4):
        c = CURS + 32 * j
        a(f'MOVWU\t{8 * j}(X{A0}), X{A1}', f'MOVW\tX{A1}, {c + C_NEXT}(X{FR})',
          f'MOVWU\t{8 * j + 4}(X{A0}), X{A1}', f'MOVW\tX{A1}, {c + C_END}(X{FR})',
          f'MOVW\tX0, {c + C_REM}(X{FR})')
    if BSWAP == 'vrgather':
        # Byte index 4k+3-i for byte 4k+i: vid ^ 3.
        a(f'MOV\t$16, X{A0}', vsetvli(A1, A0, 8, 1), vid(IDX), vi('vxor', IDX, IDX, 3))
    a(f'MOV\t$4, X{A0}', vsetvli(A1, A0, 32, 1))
    for i in range(4):
        a(f'MOV\t${KS[i]:#x}, X{K[i]}')
    # Stage step 0 into BUFA. BUFB, the "current" buffer of the first fixup,
    # has no tags to write.
    a(f'ADD\t${BUFA}, X{FR}, X{NXT}', f'ADD\t${BUFB}, X{FR}, X{CUR}')
    for j in range(4):
        a(f'MOV\tX0, {FIN + 8 * j}(X{CUR})')
    a(f'MOV\tX0, {ANY}(X{CUR})')
    advance(a, 'p')
    for s in stage_ops():
        a(s)
    fixup(a, 'p')
    swap(a)

    vops, cycles = schedule(step_ops(dist), lat)
    sops = stage_ops() if stage else []
    a.label('loop')
    # W[0..15] of the current step: two LMUL 8 loads.
    a(f'MOV\t$32, X{A0}', vsetvli(A1, A0, 32, 8), vle(0, CUR), f'ADD\t$128, X{CUR}, X{A0}', vle(8, A0))
    if BSWAP == 'vrgather':
        a(f'MOV\t$16, X{A0}', vsetvli(A1, A0, 8, 1))
        for t in range(16):
            x = TMPS[t % 2]
            a(vv('vrgather', x, W[t], IDX), vmv_v_v(W[t], x))
    a(f'MOV\t$4, X{A0}', vsetvli(A1, A0, 32, 1))
    advance(a, 'l')
    # 80 rounds; the scalar staging of the next step fills the issue slots
    # between vector instructions, which occupy the 64-bit datapath for two
    # cycles each.
    k = 0
    for i, o in enumerate(vops):
        a(o.v)
        while k < len(sops) and k * len(vops) < (i + 1) * len(sops):
            a(sops[k])
            k += 1
    assert k == len(sops)
    fixup(a, 'l')
    swap(a)
    a(f'SUB\t$1, X{NSTEPS}', f'BEQZ\tX{NSTEPS}, done', 'JMP\tloop')
    a.label('done')
    a('RET')
    return a, len(vops), len(sops), cycles


HEADER = '''\
// Code generated by firmware/cpu/go-crypto-c906/gen_sha1mb_riscv64.py. DO NOT EDIT.

//go:build riscv64 && !purego

// Multi-buffer SHA-1 for the T-Head C906 (SG2002): four block streams in
// XTheadVector lanes (VLEN 128, SEW 32, LMUL 1), one SHA-1 word per vector
// register: v0-v15 message schedule, v16-v20 a-e, v21-v25 chaining value,
// v26-v31 temporaries. Rotates are shift left, shift right, or/add.
//
// Each step hashes one 64-byte block per lane. While the vector unit runs
// the 80 rounds, scalar code interleaved with them copies the next step's
// four blocks into a stack buffer as big-endian words, transposed (word t of
// lane j at 16*t+4*j), so the next step loads W with two unit-stride loads.
// The byte loads need no alignment. Lane j takes its blocks from a queue of
// segments segs[lanes[j][0]:lanes[j][1]] (see seg in sha1mb.go); an empty
// queue hashes a zero block. Between steps, for lanes whose block was the
// last of a segment with a tag, the chaining value is written big-endian to
// tags[seg.tag:]; lanes starting a message get the ipad state, or for an outer
// block the opad state with the inner hash as message words 0-4.
//
// Vector registers are not preserved across calls: all vector state is set
// up and consumed within this call, which calls nothing. Assembly functions
// are not asynchronously preempted; the kernel saves vector state across
// context switches and signals.
//
// Go's assembler has no RVV 0.7.1 encodings, so vector instructions are WORDs.
// The generator checks each against GNU as/objdump -march=rv64gc_xtheadvector.

#include "textflag.h"

'''


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('--binutils', required=True, help='GNU binutils prefix with XTheadVector support')
    p.add_argument('--lat', type=int, default=6, help='result latency assumed by the scheduler (cycles)')
    p.add_argument('--dist', type=int, default=4, help='rounds between computing W[t] and using it')
    p.add_argument('--no-stage', action='store_true', help='omit the staging copy (measurement only)')
    p.add_argument('--bswap', choices=['scalar', 'vrgather'], default='scalar',
                   help='byte swap in the scalar staging copy or with th.vrgather.vv (measurement only)')
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument('--output', type=Path)
    g.add_argument('--check', type=Path)
    args = p.parse_args()
    global BSWAP, TMPS
    BSWAP = args.bswap
    if BSWAP == 'vrgather':
        TMPS = TMPS[:-1]  # v31 holds the byte index
    a, nv, ns, cycles = build(args.lat, args.dist, not args.no_stage)
    n = verify(args.binutils, a.vectors)
    text = HEADER + a.render()
    if args.check:
        if args.check.read_text() != text:
            sys.exit(f'{args.check} is not up to date')
        print(f'{args.check}: up to date, {n} distinct vector encodings verified')
    else:
        args.output.write_text(text)
        print(f'{args.output}: {len(a.vectors)} vector instructions ({nv} per step, '
              f'~{cycles} datapath cycles, {ns} scalar staging), {n} distinct encodings verified')


if __name__ == '__main__':
    main()
