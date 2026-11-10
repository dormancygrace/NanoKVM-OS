"""Go assembler output for the C906 generators (gen_aes_c906.py, gen_ghash_table.py).

The generators emit a small subset of GNU assembly. translate() rewrites those
lines in Go assembler syntax; the T-Head instructions, which Go's assembler
does not know, become WORD directives with the mnemonic as a comment, and
check_words() compares every such WORD with GNU as.

Go's riscv64 ABI0 has no callee-saved registers, so functions use any register
except X2 (SP), X3, X4, X27 (g) and X31 (the assembler's temporary).
"""
import re
import subprocess
import tempfile

ABI = ['zero', 'ra', 'sp', 'gp', 'tp', 't0', 't1', 't2', 's0', 's1'] + ['a%d' % i for i in range(8)] + \
      ['s%d' % i for i in range(2, 12)] + ['t3', 't4', 't5', 't6']
FORBIDDEN = {'sp', 'gp', 'tp', 's11', 't6'}       # X2, X3, X4, X27 (g), X31 (TMP)


def num(reg):
    return ABI.index(reg)


def th_word(op, a):
    """Encoding of a T-Head instruction (custom-0 opcode) with GNU operand names."""
    if op == 'th.extu':                 # rd, rs1, msb, lsb
        return int(a[2]) << 26 | int(a[3]) << 20 | num(a[1]) << 15 | 3 << 12 | num(a[0]) << 7 | 0x0b
    if op == 'th.rev':                  # rd, rs1
        return 0b1000001 << 25 | num(a[1]) << 15 | 1 << 12 | num(a[0]) << 7 | 0x0b
    if op == 'th.revw':                 # rd, rs1
        return 0b1001000 << 25 | num(a[1]) << 15 | 1 << 12 | num(a[0]) << 7 | 0x0b
    if op in ('th.lrbu', 'th.lrwu', 'th.lrd'):      # rd, rs1, rs2, imm2
        top = {'th.lrbu': 0b10000, 'th.lrwu': 0b11000, 'th.lrd': 0b01100}[op]
        return top << 27 | int(a[3]) << 25 | num(a[2]) << 20 | num(a[1]) << 15 | 4 << 12 | num(a[0]) << 7 | 0x0b
    raise ValueError(op)


def translate(lines, rename, labels):
    """GNU lines -> Go lines. rename maps GNU register names (t6 must be renamed); labels maps numeric labels."""
    out = []

    def r(name):
        name = rename.get(name, name)
        if name in FORBIDDEN:
            raise ValueError('register %s cannot be used in Go assembly' % name)
        return name

    def x(name):
        return 'X%d' % num(r(name))
    for line in lines:
        line = line.rstrip()
        m = re.fullmatch(r'(\d+):', line)
        if m:
            out.append(labels[m[1]] + ':')
            continue
        if not line.startswith('\t') or line.startswith('\t.'):
            raise ValueError('unexpected line: ' + line)
        op, _, rest = line.strip().partition('\t' if '\t' in line.strip() else ' ')
        a = [v.strip() for v in rest.split(',')] if rest.strip() else []
        mem = re.fullmatch(r'(-?\d+)\((\w+)\)', a[1]) if len(a) > 1 else None
        if op.startswith('th.'):
            g = [r(v) if v in ABI else v for v in a]
            out.append('\tWORD\t$0x%08x\t// %s %s' % (th_word(op, g), op, ', '.join(g)))
        elif op in ('xor', 'or', 'add'):
            out.append('\t%s\t%s, %s, %s' % (op.upper(), x(a[2]), x(a[1]), x(a[0])))
        elif op in ('addi', 'andi', 'slli', 'srli'):
            go = {'addi': 'ADD', 'andi': 'AND', 'slli': 'SLL', 'srli': 'SRL'}[op]
            out.append('\t%s\t$%d, %s, %s' % (go, int(a[2]), x(a[1]), x(a[0])))
        elif op in ('lwu', 'ld'):
            out.append('\t%s\t%s(%s), %s' % ('MOVWU' if op == 'lwu' else 'MOV', mem[1], x(mem[2]), x(a[0])))
        elif op in ('sw', 'sd'):
            out.append('\t%s\t%s, %s(%s)' % ('MOVW' if op == 'sw' else 'MOV', x(a[0]), mem[1], x(mem[2])))
        elif op == 'li':
            out.append('\tMOV\t$%d, %s' % (int(a[1]), x(a[0])))
        elif op in ('bnez', 'beqz'):
            out.append('\t%s\t%s, %s' % (op.upper(), x(a[0]), labels[a[1][:-1]]))
        elif op == 'bne':
            out.append('\tBNE\t%s, %s, %s' % (x(a[0]), x(a[1]), labels[a[2][:-1]]))
        elif op == 'ret':
            out.append('\tRET')
        else:
            raise ValueError('unknown instruction: ' + line)
    return out


def check_words(text, binutils):
    """Every WORD of the Go text against GNU as; the number of distinct instructions checked."""
    words = sorted(set(re.findall(r'WORD\t\$(0x[0-9a-f]{8})\t// (th\.[^\n]+)', text)))
    with tempfile.TemporaryDirectory() as d:
        src = d + '/w.S'
        with open(src, 'w') as f:
            f.write('\t.option arch, +xtheadbb, +xtheadmemidx\n' + ''.join('\t%s\n' % m for _, m in words))
        subprocess.run([binutils + 'as', '-march=rv64gc', '-o', d + '/w.o', src], check=True)
        dump = subprocess.run([binutils + 'objdump', '-d', '-M', 'no-aliases', d + '/w.o'],
                              check=True, capture_output=True, text=True).stdout
    got = re.findall(r'^\s*[0-9a-f]+:\s+([0-9a-f]{8})\s', dump, re.M)
    assert len(got) == len(words), (len(got), len(words))
    for (w, m), g in zip(words, got):
        if int(w, 16) != int(g, 16):
            raise SystemExit('encoding of "%s": generator %s, GNU as %s' % (m, w, g))
    return len(words)
