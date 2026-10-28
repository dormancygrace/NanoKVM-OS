#!/usr/bin/env python3
"""Root-only real loop-device bounds test using a disposable sparse card file.

Run explicitly: sudo python3 tests/test-stock-layout-loop.py --output /tmp/qa
Requires cc, losetup, blockdev and mkfs.fat. Never opens a physical disk.
"""
import argparse
import hashlib
import os
from pathlib import Path
import struct
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('root is required for bounded loop device mappings')
    args.output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='loop-fixture-', dir=args.output.resolve()) as temporary:
        root = Path(temporary)
        helper = root / 'layout'
        subprocess.run(['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-DSTOCK_LAYOUT_TEST',
                        str(REPO / 'firmware/stock-install/layout.c'), '-o', str(helper)], check=True)
        mbr = bytearray(512)
        mbr[510:] = b'\x55\xaa'
        parts = [(1, 32768, 0x0c), (32769, 3145728, 0x83), (3180544, 1048576, 7)]
        for i, (start, size, kind) in enumerate(parts):
            mbr[450 + i*16] = kind
            struct.pack_into('<II', mbr, 454 + i*16, start, size)
        disk = root / 'card.img'
        with disk.open('wb') as stream:
            stream.write(mbr)
            stream.truncate(4 * 1024**3)
            for sector in [parts[0][0], parts[0][0]+parts[0][1]-1, parts[2][0], parts[2][0]+parts[2][1]-1]:
                stream.seek(sector*512)
                stream.write(b'protected' * 56 + b'XXXXXXXX')
        saved = root / 'mbr.old'
        saved.write_bytes(mbr)
        plan = list(map(int, subprocess.check_output([str(helper), 'plan', str(disk)]).split()))
        loops = []
        try:
            for command, size, offset in [('map-boot', 131072, plan[6]), ('map-root', plan[8], plan[7])]:
                loop = subprocess.check_output([str(helper), command, str(disk), str(saved)], text=True).strip()
                assert loop.startswith('/dev/loop') and loop[len('/dev/loop'):].isdigit(), loop
                loops.append(loop)
                actual = int(subprocess.check_output(['blockdev', '--getsize64', loop]))
                assert actual == size*512, (actual, size)
                marker = hashlib.sha256(command.encode()).digest() * 16
                with open(loop, 'r+b', buffering=0) as stream:
                    stream.write(marker)
                    stream.seek(size*512-512)
                    stream.write(marker)
                    stream.seek(size*512)
                    try:
                        stream.write(marker)
                        raise AssertionError('loop allowed a write outside its bound')
                    except OSError:
                        pass
                    os.fsync(stream.fileno())
                with disk.open('rb') as stream:
                    stream.seek(offset*512)
                    assert stream.read(512) == marker
                    stream.seek((offset+size-1)*512)
                    assert stream.read(512) == marker
            subprocess.run(['mkfs.fat', '-F', '16', loops[0]], check=True, stdout=subprocess.DEVNULL)
            with disk.open('rb') as stream:
                for sector in [parts[0][0], parts[0][0]+parts[0][1]-1, parts[2][0], parts[2][0]+parts[2][1]-1]:
                    stream.seek(sector*512)
                    assert stream.read(512) == b'protected' * 56 + b'XXXXXXXX'
            subprocess.run([str(helper), 'commit', str(disk), str(saved)], check=True)
            with disk.open('rb') as stream:
                final = stream.read(512)
            assert final[478:] == mbr[478:]
            print('PASS: real bounded boot/root loops, boundary writes rejected, FAT format, old boot/data intact, MBR commit')
        finally:
            for loop in reversed(loops):
                subprocess.run(['losetup', '-d', loop], check=True)


if __name__ == '__main__':
    main()
