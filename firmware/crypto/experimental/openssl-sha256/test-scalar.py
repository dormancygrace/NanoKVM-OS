#!/usr/bin/env python3
"""Run the actual patched SHA wrapper and scalar assembly on QEMU's C906 CPU."""
import argparse
from pathlib import Path
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True, type=Path, help='applied, header-generated OpenSSL tree')
    parser.add_argument('--cross-prefix', default='riscv64-linux-gnu-')
    parser.add_argument('--negative-control', action='store_true')
    args = parser.parse_args()
    source = args.source.resolve()
    wrapper = source / 'crypto/sha/sha_riscv.c'
    assembly = source / 'crypto/sha/sha256-c906.S'
    with tempfile.TemporaryDirectory(prefix='sha256-scalar-test-') as temporary:
        work = Path(temporary)
        binary = work / 'test'
        # No libc: a distro's newer-ISA libc must not mask C906 compatibility.
        command = [args.cross_prefix + 'gcc', '-O2', '-static', '-nostdlib', '-ffreestanding',
                   '-fno-builtin', '-fno-stack-protector', '-fno-pie', '-no-pie',
                   '-msmall-data-limit=0', '-mno-relax', '-march=rv64gc_xtheadbb', '-mabi=lp64d',
                   '-ffunction-sections', '-fdata-sections', '-Wl,--gc-sections',
                   '-I' + str(source / 'include'), '-I' + str(source),
                   str(HERE / 'test-scalar.c'), str(HERE / 'test-scalar-abi.S'),
                   str(wrapper), str(source / 'crypto/sha/sha256_sg2002.c'), str(assembly),
                   '-o', str(binary)]
        def execute(extra=()):
            subprocess.run(command + list(extra), check=True)
            return subprocess.run(['qemu-riscv64', '-cpu', 'thead-c906', str(binary)],
                                  timeout=60, check=False).returncode
        if execute() != 0:
            raise SystemExit('SHA-256 C906 arithmetic/ABI/continuation test failed')
        print('PASS: 2096 scalar cases (0-130 blocks, 16 offsets), 1344 real-wrapper DMA fault cases; '
              'callee-saved registers, schedule wipe, context/input guards, independent abc KAT')
        if args.negative_control:
            if execute(['-DNEGATIVE_DIGEST']) != 1:
                raise SystemExit('negative expected digest was not detected')
            for name, path, old, new in (
                ('wrapper input advancement', wrapper, 'completed * SHA256_CBLOCK', '0'),
                ('callee-saved register', assembly, 'ld x8, 64(sp)', 'li x8, 0'),
                ('schedule wipe', assembly, 'sd zero, 0(sp)', 'nop'),
            ):
                original = path.read_text()
                if original.count(old) != 1:
                    raise SystemExit('negative-control anchor changed: ' + name)
                try:
                    path.write_text(original.replace(old, new))
                    if execute() != 1:
                        raise SystemExit('negative control was not detected: ' + name)
                finally:
                    path.write_text(original)
                print('PASS negative control: ' + name)
            print('PASS negative control: expected digest')


if __name__ == '__main__':
    main()
