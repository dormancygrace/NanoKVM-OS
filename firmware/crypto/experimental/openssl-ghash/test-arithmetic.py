#!/usr/bin/env python3
"""Test real GHASH glue/assembly; translate only draft vector opcodes for QEMU."""
import argparse
from pathlib import Path
import re
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent


def rvv1(source):
    # This is an arithmetic test, NOT proof of native RVV 0.7.1 encodings.
    # generate-ghash.py separately verifies those exact words with GNU binutils.
    plain = {'vand.vx', 'vmul.vv', 'vmulhu.vv', 'vmv.v.i', 'vmv.x.s',
             'vredxor.vs', 'vxor.vv'}
    count = 0
    def translate(match):
        nonlocal count
        count += 1
        mnemonic, operands = match[1].split(None, 1)
        if mnemonic == 'vsetvli':
            if not operands.endswith('e32,m1,d1'):
                raise ValueError('unexpected vector configuration: ' + operands)
            operands = operands.removesuffix(',d1') + ',ta,ma'
        elif mnemonic in ('vle.v', 'vlse.v'):
            mnemonic = {'vle.v': 'vle32.v', 'vlse.v': 'vlse32.v'}[mnemonic]
        elif mnemonic not in plain:
            raise ValueError('unreviewed vector mapping: ' + mnemonic)
        return mnemonic + ' ' + operands
    result = re.sub(r'\.4byte\s+0x[0-9a-f]+\s*/\* th\.(v[^*]+?) \*/', translate, source)
    if count == 0 or '.4byte' in result:
        raise ValueError('untranslated draft-vector instruction')
    return result.replace('.option arch, +xtheadbb', '.option arch, +xtheadbb,+v')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--cross-prefix', default='riscv64-linux-gnu-')
    parser.add_argument('--negative-control', action='store_true')
    args = parser.parse_args()
    source = args.source.resolve()
    glue = source / 'crypto/modes/gcm_c906.c'
    with tempfile.TemporaryDirectory(prefix='ghash-arithmetic-test-') as temporary:
        work = Path(temporary)
        assembly = work / 'ghash-rvv.S'
        assembly.write_text(rvv1((source / 'crypto/modes/ghash-c906.S').read_text()))
        start = work / 'start.S'
        start.write_text('.text\n.globl _start\n_start:\ncall main\nli a7,93\necall\n.section .note.GNU-stack,"",@progbits\n')
        binary = work / 'test'
        command = [args.cross_prefix + 'gcc', '-O2', '-static', '-nostdlib', '-ffreestanding',
                   '-fno-builtin', '-fno-stack-protector', '-fno-pie', '-no-pie',
                   '-msmall-data-limit=0', '-mno-relax', '-march=rv64gc_xtheadbb', '-mabi=lp64d',
                   '-I' + str(source / 'include'), '-I' + str(source),
                   str(HERE / 'test-arithmetic.c'), str(glue), str(assembly), str(start),
                   '-o', str(binary)]
        def execute(extra=()):
            subprocess.run(command + list(extra), check=True)
            return subprocess.run(['qemu-riscv64', '-cpu', 'max,vlen=128,xtheadbb=true',
                                   str(binary)], timeout=120, check=False).returncode
        if execute() != 0:
            raise SystemExit('GHASH arithmetic or scalar-remainder mismatch')
        print('PASS: 3520 GHASH cases, 40 keys, 11 lengths, 8 offsets, all 0-4 block tails; '
              'real key layout/Xi conversion, input/context/table guards, independent GCM KAT')
        if args.negative_control:
            if execute(['-DNEGATIVE_DIGEST']) != 1:
                raise SystemExit('negative expected digest was not detected')
            original = glue.read_text()
            for name, old, new in (
                ('scalar tail offset', 'in + groups * 80, blocks', 'in + groups * 80 - 16, blocks'),
                ('scalar tail omission', 'if (blocks != 0)', 'if (blocks == 99)'),
                ('key layout', 'table[8].hi, h1 = table[8].lo', 'table[7].hi, h1 = table[7].lo'),
                ('Xi byte swap', '__builtin_bswap64(xi[1])', 'xi[1]'),
            ):
                if original.count(old) != 1:
                    raise SystemExit('negative-control anchor changed: ' + name)
                try:
                    glue.write_text(original.replace(old, new))
                    if execute() != 1:
                        raise SystemExit('negative control was not detected: ' + name)
                finally:
                    glue.write_text(original)
                print('PASS negative control: ' + name)
            print('PASS negative control: expected digest')
        print('QEMU uses translated RVV 1.0 opcodes; native XTheadVector encoding is checked separately')


if __name__ == '__main__':
    main()
