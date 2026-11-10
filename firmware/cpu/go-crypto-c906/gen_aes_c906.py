#!/usr/bin/env python3
"""Generate the AES block functions for the T-Head C906.

    void NAME_encrypt(const u32 rndkeys[], int nrounds, u8 out[16], const u8 in[16]);
    void NAME_decrypt(const u32 inv_rndkeys[], int nrounds, u8 out[16], const u8 in[16]);

rndkeys are the nrounds + 1 round keys as little-endian words; inv_rndkeys are
the round keys of the Equivalent Inverse Cipher (reversed, InvMixColumns on all
but the first and the last), as lib/crypto/aes.c of Linux prepares them. in and
out may have any alignment and may be the same.

The C906 executes about one instruction per cycle here, so the code is built
for the instruction count: four tables of 1 KiB per direction, th.extu to cut a
byte out and th.lrwu to load the table word by index (XTheadBb, XTheadMemIdx).
A column takes 13 instructions, a round 52. Rounds are emitted in pairs so two
register sets swap roles without moves; nrounds - 1 is odd, so one round runs
before the loop. Two columns are interleaved: a loaded value is used no sooner
than two instructions later. The last round takes S(x) as byte 0 of the third
encryption table, and the inverse S-box from 256 bytes after the decryption
tables.

Table lookups depend on the key and the data: the code is not constant-time.

The generator runs what it emits against its own AES, which it first checks
with the FIPS 197 examples (--selftest), with an interpreter of the
instructions it uses.

Usage: gen_aes_c906.py --name NAME [--flavor linux|plain]
       (--output FILE | --check PATCH PATH) [--selftest CASES]
"""
import argparse
import random
import re
import sys

RK, CNT, OUT, IN = 'a0', 'a1', 'a2', 'a3'
T = ['a4', 'a5', 'a6', 'a7']
A = ['t0', 't1', 't2', 't3']
B = ['t4', 't5', 't6', 'a3']      # a3 (in) is free once the block is loaded
X, Y = 's0', 's1'


def xtime(v):
    return (v << 1) ^ (0x11b if v & 0x80 else 0)


def gmul(a, b):
    r = 0
    while b:
        if b & 1:
            r ^= a
        a, b = xtime(a), b >> 1
    return r


def sboxes():
    inv = [0] * 256
    for a in range(1, 256):
        for b in range(1, 256):
            if gmul(a, b) == 1:
                inv[a] = b
                break
    s = []
    for x in range(256):
        q = inv[x]
        v = q
        for n in (1, 2, 3, 4):
            v ^= ((q << n) | (q >> (8 - n))) & 0xff
        s.append(v ^ 0x63)
    si = [0] * 256
    for x, v in enumerate(s):
        si[v] = x
    return s, si


SBOX, INV_SBOX = sboxes()


def rol32(v, n):
    return ((v << n) | (v >> (32 - n))) & 0xffffffff if n else v


# Table j is table 0 rotated left by 8 * j bits. Table 0 of a direction holds
# the column the round makes of one S-box output: bytes (2s, s, s, 3s) to
# encrypt, (14s, 9s, 13s, 11s) to decrypt, lowest byte first.
ENC0 = [gmul(s, 2) | s << 8 | s << 16 | gmul(s, 3) << 24 for s in SBOX]
DEC0 = [gmul(s, 14) | gmul(s, 9) << 8 | gmul(s, 13) << 16 | gmul(s, 11) << 24 for s in INV_SBOX]


def tables(first):
    return [[rol32(v, 8 * j) for v in first] for j in range(4)]


def expand(key):
    """(rndkeys, inv_rndkeys) of lib/crypto/aes.c for a 16, 24 or 32 byte key."""
    nk = len(key) // 4
    rounds = nk + 6
    w = [int.from_bytes(key[4 * i:4 * i + 4], 'little') for i in range(nk)]
    rc = 1

    def subw(v):
        return SBOX[v & 255] | SBOX[v >> 8 & 255] << 8 | SBOX[v >> 16 & 255] << 16 | SBOX[v >> 24] << 24
    for i in range(nk, 4 * (rounds + 1)):
        t = w[i - 1]
        if i % nk == 0:
            t = subw(t >> 8 | (t & 255) << 24) ^ rc
            rc = xtime(rc)
        elif nk > 6 and i % nk == 4:
            t = subw(t)
        w.append(w[i - nk] ^ t)

    def inv_mix(v):
        b = [v >> (8 * i) & 255 for i in range(4)]
        return sum((gmul(b[i], 14) ^ gmul(b[(i + 1) % 4], 11) ^ gmul(b[(i + 2) % 4], 13) ^ gmul(b[(i + 3) % 4], 9)) << (8 * i)
                   for i in range(4))
    inv = w[4 * rounds:]
    for r in range(rounds - 1, 0, -1):
        inv += [inv_mix(v) for v in w[4 * r:4 * r + 4]]
    return w, inv + w[:4], rounds


def reference(keys, rounds, block, decrypt):
    """The block function in plain Python, from the tables."""
    tab = tables(DEC0 if decrypt else ENC0)
    box = INV_SBOX if decrypt else SBOX
    step = 3 if decrypt else 1
    w = [int.from_bytes(block[4 * i:4 * i + 4], 'little') ^ keys[i] for i in range(4)]
    for r in range(1, rounds):
        w = [keys[4 * r + i] ^ tab[0][w[i] & 255] ^ tab[1][w[(i + step) % 4] >> 8 & 255]
             ^ tab[2][w[(i + 2 * step) % 4] >> 16 & 255] ^ tab[3][w[(i + 3 * step) % 4] >> 24] for i in range(4)]
    w = [keys[4 * rounds + i] ^ box[w[i] & 255] ^ box[w[(i + step) % 4] >> 8 & 255] << 8
         ^ box[w[(i + 2 * step) % 4] >> 16 & 255] << 16 ^ box[w[(i + 3 * step) % 4] >> 24] << 24 for i in range(4)]
    return b''.join(v.to_bytes(4, 'little') for v in w)


def function(name, decrypt, flavor):
    o = []

    def e(s):
        o.append('\t' + s)
    step = 3 if decrypt else 1
    label = name + ('_dec_tables' if decrypt else '_enc_tables')

    def columns(src, dst, rkoff, last):
        # last: one byte per lookup, from byte 0 of table 2 (encryption: S)
        # or from the 256 bytes that T[0] then points at (decryption).
        base = T[0] if decrypt else T[2]
        shift = 0 if decrypt else 2
        for j, k in ((0, 1), (2, 3)):
            e(f'th.extu {dst[j]}, {src[j]}, 7, 0')
            e(f'th.extu {dst[k]}, {src[k]}, 7, 0')
            if last:
                e(f'th.lrbu {dst[j]}, {base}, {dst[j]}, {shift}')
                e(f'th.lrbu {dst[k]}, {base}, {dst[k]}, {shift}')
            else:
                e(f'th.lrwu {dst[j]}, {T[0]}, {dst[j]}, 2')
                e(f'th.lrwu {dst[k]}, {T[0]}, {dst[k]}, 2')
            for b in (1, 2, 3):
                e(f'th.extu {X}, {src[(j + b * step) % 4]}, {8 * b + 7}, {8 * b}')
                e(f'th.extu {Y}, {src[(k + b * step) % 4]}, {8 * b + 7}, {8 * b}')
                if last:
                    e(f'th.lrbu {X}, {base}, {X}, {shift}')
                    e(f'th.lrbu {Y}, {base}, {Y}, {shift}')
                    e(f'slli {X}, {X}, {8 * b}')
                    e(f'slli {Y}, {Y}, {8 * b}')
                    e(f'or {dst[j]}, {dst[j]}, {X}')
                    e(f'or {dst[k]}, {dst[k]}, {Y}')
                else:
                    e(f'th.lrwu {X}, {T[b]}, {X}, 2')
                    e(f'th.lrwu {Y}, {T[b]}, {Y}, 2')
                    e(f'xor {dst[j]}, {dst[j]}, {X}')
                    e(f'xor {dst[k]}, {dst[k]}, {Y}')
            e(f'lwu {X}, {rkoff + 4 * j}({RK})')
            e(f'lwu {Y}, {rkoff + 4 * k}({RK})')
            e(f'xor {dst[j]}, {dst[j]}, {X}')
            e(f'xor {dst[k]}, {dst[k]}, {Y}')

    if flavor == 'linux':
        o.append(f'SYM_FUNC_START({name})')
    else:
        o.append(f'\t.globl {name}\n\t.type {name}, @function\n{name}:')
    e('addi sp, sp, -16')
    e('sd s0, 0(sp)')
    e('sd s1, 8(sp)')
    e(f'la {T[0]}, {label}')
    for j in range(4):
        e(f'lwu {A[j]}, {4 * j}({IN})')
    for j in range(1, 4):
        e(f'addi {T[j]}, {T[j - 1]}, 1024')
    for j, k in ((0, 1), (2, 3)):
        e(f'lwu {X}, {4 * j}({RK})')
        e(f'lwu {Y}, {4 * k}({RK})')
        e(f'xor {A[j]}, {A[j]}, {X}')
        e(f'xor {A[k]}, {A[k]}, {Y}')
    e(f'srli {CNT}, {CNT}, 1')
    e(f'addi {CNT}, {CNT}, -1')         # pairs of rounds after the first: (nrounds - 2) / 2
    columns(A, B, 16, False)
    e(f'addi {RK}, {RK}, 32')
    o.append('1:')
    columns(B, A, 0, False)
    columns(A, B, 16, False)
    e(f'addi {CNT}, {CNT}, -1')
    e(f'addi {RK}, {RK}, 32')
    e(f'bnez {CNT}, 1b')
    if decrypt:
        e(f'addi {T[0]}, {T[3]}, 1024')  # the inverse S-box follows the four tables
    columns(B, A, 0, True)
    for j in range(4):
        e(f'sw {A[j]}, {4 * j}({OUT})')
    e('ld s0, 0(sp)')
    e('ld s1, 8(sp)')
    e('addi sp, sp, 16')
    e('ret')
    o.append(f'SYM_FUNC_END({name})' if flavor == 'linux' else f'\t.size {name}, .-{name}')
    return o, label


def data(label, decrypt):
    o = [f'\t.balign 64\n\t.type {label}, @object\n{label}:']
    for tab in tables(DEC0 if decrypt else ENC0):
        for i in range(0, 256, 8):
            o.append('\t.word ' + ', '.join('0x%08x' % v for v in tab[i:i + 8]))
    if decrypt:
        for i in range(0, 256, 16):
            o.append('\t.byte ' + ', '.join('0x%02x' % v for v in INV_SBOX[i:i + 16]))
    o.append(f'\t.size {label}, .-{label}')
    return o


HEAD = '''%s
/*
 * AES block encryption and decryption for the T-Head C906.
 *
 * Generated by gen_aes_c906.py of NanoKVM OS
 * (firmware/cpu/go-crypto-c906); do not edit.
 *
 * Four tables of 1 KiB per direction; th.extu cuts a byte out, th.lrwu loads
 * the table word by index: 52 instructions per round. Not constant-time.
 */
'''


def gen(name, flavor='plain'):
    o = [(HEAD % ('/* SPDX-License-Identifier: GPL-2.0-only */' if flavor == 'linux'
                  else '/* SPDX-License-Identifier: GPL-3.0-only */')).rstrip('\n')]
    if flavor == 'linux':
        o.append('#include <linux/linkage.h>\n')
    o.append('\t.text\n\t.option arch, +xtheadbb, +xtheadmemidx\n\t.balign 4')
    blocks = []
    for decrypt in (False, True):
        body, label = function(name + ('_decrypt' if decrypt else '_encrypt'), decrypt, flavor)
        o += body
        o.append('')
        blocks.append((label, decrypt))
    o.append('\t.section .rodata')
    for label, decrypt in blocks:
        o += data(name + ('_decrypt' if decrypt else '_encrypt') + ('_dec_tables' if decrypt else '_enc_tables'), decrypt)
    return '\n'.join(o) + '\n'


REGS = ['zero', 'ra', 'sp', 'gp', 'tp', 't0', 't1', 't2', 's0', 's1'] + ['a%d' % i for i in range(8)] + \
       ['s%d' % i for i in range(2, 12)] + ['t3', 't4', 't5', 't6']
M64 = (1 << 64) - 1


def simulate(text, name, keys, rounds, block):
    """Run NAME of the emitted text on (keys, rounds, block); the 16 bytes written to out."""
    lines = text.split('\n')
    start = next(i for i, l in enumerate(lines) if l.startswith(name + ':') or l == f'SYM_FUNC_START({name})')
    mem = {}

    def put(addr, raw):
        for i, v in enumerate(raw):
            mem[addr + i] = v

    def get(addr, n):
        return int.from_bytes(bytes(mem[addr + i] for i in range(n)), 'little')
    labels = {}
    addr = 0x100000
    current = None
    for l in lines:
        m = re.fullmatch(r'(\w+_tables):', l)
        if m:
            current = m[1]
            labels[current] = addr
        elif current and l.startswith('\t.word '):
            for v in l[7:].split(','):
                put(addr, int(v, 16).to_bytes(4, 'little'))
                addr += 4
        elif current and l.startswith('\t.byte '):
            for v in l[7:].split(','):
                put(addr, bytes([int(v, 16)]))
                addr += 1
    krk, kin, kout, stack = 0x10000, 0x20001, 0x30003, 0x50000   # in and out unaligned
    put(krk, b''.join(v.to_bytes(4, 'little') for v in keys))
    put(kin, block)
    r = {n: 0xdead0000 + i for i, n in enumerate(REGS)}
    r.update(zero=0, sp=stack, a0=krk, a1=rounds, a2=kout, a3=kin)
    saved = {n: r[n] for n in ('s0', 's1', 'sp')}
    pc = start + 1
    loaded = {}           # register -> number of instructions since it was loaded
    count = 0
    while True:
        l = lines[pc]
        pc += 1
        if not l.startswith('\t') or l.startswith('\t.'):
            continue
        op, _, rest = l.strip().partition(' ')
        a = [x.strip() for x in rest.split(',')] if rest else []
        count += 1
        if op == 'ret':
            break
        uses = [x for x in a[1:] if x in r] + ([a[0]] if op in ('sw', 'sd', 'bnez') else [])
        for reg in uses:
            if loaded.get(reg) == 0:
                raise AssertionError(f'{name}: {reg} used right after its load: {l.strip()}')
        loaded = {k: v + 1 for k, v in loaded.items()}
        mm = re.fullmatch(r'(-?\d+)\((\w+)\)', a[1]) if len(a) > 1 else None
        if op == 'addi':
            r[a[0]] = (r[a[1]] + int(a[2])) & M64
        elif op in ('xor', 'or'):
            r[a[0]] = r[a[1]] ^ r[a[2]] if op == 'xor' else r[a[1]] | r[a[2]]
        elif op == 'slli':
            r[a[0]] = (r[a[1]] << int(a[2])) & M64
        elif op == 'srli':
            r[a[0]] = r[a[1]] >> int(a[2])
        elif op == 'la':
            r[a[0]] = labels[a[1]]
        elif op == 'th.extu':
            msb, lsb = int(a[2]), int(a[3])
            r[a[0]] = (r[a[1]] >> lsb) & ((1 << (msb - lsb + 1)) - 1)
        elif op in ('th.lrwu', 'th.lrbu'):
            r[a[0]] = get(r[a[1]] + (r[a[2]] << int(a[3])), 4 if op == 'th.lrwu' else 1)
            loaded[a[0]] = 0
        elif op in ('lwu', 'ld'):
            r[a[0]] = get(r[mm[2]] + int(mm[1]), 4 if op == 'lwu' else 8)
            loaded[a[0]] = 0
        elif op in ('sw', 'sd'):
            n = 4 if op == 'sw' else 8
            put(r[mm[2]] + int(mm[1]), (r[a[0]] & ((1 << (8 * n)) - 1)).to_bytes(n, 'little'))
        elif op == 'bnez':
            if r[a[0]]:
                pc = max(i for i in range(pc) if lines[i] == '1:')
        else:
            raise AssertionError('unknown instruction: ' + l)
        r['zero'] = 0
    for n, v in saved.items():
        assert r[n] == v, f'{name}: {n} not restored'
    return bytes(mem[kout + i] for i in range(16)), count


def selftest(cases, text, name):
    # FIPS 197 appendix C: the reference itself.
    plain = bytes.fromhex('00112233445566778899aabbccddeeff')
    for n, want in ((16, '69c4e0d86a7b0430d8cdb78070b4c55a'), (24, 'dda97ca4864cdfe06eaf70a0ec0d7191'),
                    (32, '8ea2b7ca516745bfeafc49904b496089')):
        keys, inv, rounds = expand(bytes(range(n)))
        got = reference(keys, rounds, plain, False)
        assert got.hex() == want, ('FIPS 197', n, got.hex())
        assert reference(inv, rounds, got, True) == plain, ('FIPS 197 inverse', n)
    rng = random.Random(906)
    counts = set()
    for case in range(cases):
        n = (16, 24, 32)[case % 3]
        keys, inv, rounds = expand(rng.randbytes(n))
        block = rng.randbytes(16)
        got, c = simulate(text, name + '_encrypt', keys, rounds, block)
        assert got == reference(keys, rounds, block, False), ('encrypt', case)
        back, d = simulate(text, name + '_decrypt', inv, rounds, got)
        assert back == block, ('decrypt', case)
        counts.add((n * 8, c, d))
    for bits, c, d in sorted(counts):
        print(f'AES-{bits}: {c} instructions to encrypt a block, {d} to decrypt', file=sys.stderr)
    print(f'selftest: {cases} cases pass', file=sys.stderr)


def patch_file(patch, path):
    """The lines of the new file PATH in PATCH."""
    lines = open(patch).read().split('\n')
    start = lines.index('+++ b/' + path)
    out = []
    for l in lines[start + 2:]:
        if not l.startswith('+'):
            break
        out.append(l[1:])
    return '\n'.join(out) + '\n'


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--name', required=True)
    ap.add_argument('--flavor', choices=['linux', 'plain'], default='plain')
    ap.add_argument('--output')
    ap.add_argument('--check', nargs=2, metavar=('PATCH', 'PATH'), help='the file PATH added by PATCH must be the generated assembly')
    ap.add_argument('--selftest', type=int, metavar='CASES')
    a = ap.parse_args()
    text = gen(a.name, a.flavor)
    if a.selftest:
        selftest(a.selftest, text, a.name)
    if a.check:
        patch, path = a.check
        if patch_file(patch, path) != text:
            sys.exit(f'{patch}: {path} is not the generated file; regenerate it')
        print(f'{patch}: {path} is up to date')
    if a.output:
        with open(a.output, 'w') as f:
            f.write(text)


if __name__ == '__main__':
    main()
