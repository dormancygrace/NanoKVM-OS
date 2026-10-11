#!/usr/bin/env python3
"""Build the serial console helper disk: a 1440 KiB FAT12 image that holds
nanokvm-serial-console.sh as setup.sh and a readme.txt, for the NanoKVM's USB
mass storage. The managed computer mounts it and runs the script with no
network. Written in Python so the image is byte-for-byte reproducible (fixed
timestamps and volume id) and needs no mtools on the build host."""
import argparse
from pathlib import Path
import struct

SECTOR = 512
TOTAL_SECTORS = 2880           # 1440 KiB, the layout of a 3.5" floppy: no partition table
RESERVED_SECTORS = 1
FATS = 2
ROOT_ENTRIES = 224
SECTORS_PER_FAT = 9
MEDIA = 0xF0
ROOT_SECTORS = ROOT_ENTRIES * 32 // SECTOR
DATA_SECTOR = RESERVED_SECTORS + FATS * SECTORS_PER_FAT + ROOT_SECTORS
DATA_CLUSTERS = TOTAL_SECTORS - DATA_SECTOR
LABEL = b'NANOKVM    '
VOLUME_ID = 0x4E4B564D         # "NKVM"
FAT_DATE = ((2026 - 1980) << 9) | (1 << 5) | 1   # 2026-01-01
FAT_TIME = 0
ATTR_ARCHIVE, ATTR_LABEL = 0x20, 0x08
NT_LOWER_BASE, NT_LOWER_EXT = 0x08, 0x10

README = """NanoKVM USB serial console helper
=================================

This disk comes from the NanoKVM's virtual USB drive. setup.sh prepares a
login console on the NanoKVM's USB serial port (CDC ACM, /dev/ttyACMn) on
the computer the NanoKVM is plugged into. Run it on that computer as root:

    sh /mnt/setup.sh            report what it found and what it would change
    sh /mnt/setup.sh apply      make the changes (with a confirmation)
    sh /mnt/setup.sh undo       remove them again

Replace /mnt with the directory this disk is mounted on. The script only
reads unless you run apply; apply keeps backups and a manifest in
/var/backups/nanokvm-serial-console. It refuses when it cannot work safely
(container, read-only or immutable system, no cdc_acm driver, unknown init
system) and prints the manual steps instead.

Then open the USB serial console from the NanoKVM's Terminal menu and press
Enter: the login prompt of this computer appears.
"""


# The same stub mkfs.fat writes: a BIOS that tries to boot from this disk
# (a USB drive first in its boot order) prints the message, waits for a key
# and goes on with the next device, instead of running the zeros after the
# BPB. The jump at byte 0 lands on it at 0x3E; the text follows at 0x5B.
BOOT_CODE = (b'\x0e\x1f\xbe\x5b\x7c\xac\x22\xc0\x74\x0b\x56\xb4\x0e\xbb\x07\x00\xcd\x10'
             b'\x5e\xeb\xf0\x32\xe4\xcd\x16\xcd\x19\xeb\xfe'
             b'This is not a bootable disk. It holds the NanoKVM serial console\r\n'
             b'helper. Remove it from the boot order and press any key ...\r\n')


def boot_sector():
    bpb = struct.pack('<3s8sHBHBHHBHHHII', b'\xEB\x3C\x90', b'NANOKVM ', SECTOR, 1, RESERVED_SECTORS,
                      FATS, ROOT_ENTRIES, TOTAL_SECTORS, MEDIA, SECTORS_PER_FAT, 18, 2, 0, 0)
    ebpb = struct.pack('<BBBI11s8s', 0, 0, 0x29, VOLUME_ID, LABEL, b'FAT12   ')
    assert len(bpb + ebpb) == 0x3E
    return (bpb + ebpb + BOOT_CODE).ljust(SECTOR - 2, b'\0') + b'\x55\xAA'


def fat12(entries):
    out = bytearray()
    for i in range(0, len(entries), 2):
        a = entries[i]
        b = entries[i + 1] if i + 1 < len(entries) else 0
        out += bytes([a & 0xFF, ((a >> 8) & 0x0F) | ((b & 0x0F) << 4), (b >> 4) & 0xFF])
    return bytes(out).ljust(SECTORS_PER_FAT * SECTOR, b'\0')


def dir_entry(name, ext, attr, ntres, cluster, size):
    return struct.pack('<8s3sBBBHHHHHHHI', name, ext, attr, ntres, 0, FAT_TIME, FAT_DATE, FAT_DATE, 0,
                       FAT_TIME, FAT_DATE, cluster, size)


def build(files):
    """files: list of (short name 8.3 as 'NAME.EXT', shown lowercase, content bytes)."""
    fat = [0xF00 | MEDIA, 0xFFF]
    root = bytearray(dir_entry(LABEL[:8], LABEL[8:], ATTR_LABEL, 0, 0, 0))
    data = bytearray()
    next_cluster = 2
    for short, content in files:
        base, _, ext = short.partition('.')
        assert base.isupper() and ext.isupper() and len(base) <= 8 and len(ext) <= 3, short
        count = (len(content) + SECTOR - 1) // SECTOR
        if count == 0:
            first = 0
        else:
            first = next_cluster
            fat.extend(list(range(first + 1, first + count)) + [0xFFF])
            next_cluster += count
            data += content.ljust(count * SECTOR, b'\0')
        root += dir_entry(base.encode().ljust(8), ext.encode().ljust(3), ATTR_ARCHIVE,
                          NT_LOWER_BASE | NT_LOWER_EXT, first, len(content))
    assert next_cluster - 2 <= DATA_CLUSTERS, 'files do not fit on the helper disk'
    image = bytearray(TOTAL_SECTORS * SECTOR)
    image[0:SECTOR] = boot_sector()
    for n in range(FATS):
        start = (RESERVED_SECTORS + n * SECTORS_PER_FAT) * SECTOR
        image[start:start + SECTORS_PER_FAT * SECTOR] = fat12(fat)
    start = (RESERVED_SECTORS + FATS * SECTORS_PER_FAT) * SECTOR
    image[start:start + len(root)] = root
    start = DATA_SECTOR * SECTOR
    image[start:start + len(data)] = data
    return bytes(image)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--script', type=Path, required=True, help='nanokvm-serial-console.sh')
    p.add_argument('--output', type=Path, required=True)
    a = p.parse_args()
    script = a.script.read_bytes()
    assert script.startswith(b'#!/bin/sh'), 'not the helper script'
    image = build([('SETUP.SH', script), ('README.TXT', README.encode())])
    a.output.parent.mkdir(parents=True, exist_ok=True)
    a.output.write_bytes(image)


if __name__ == '__main__':
    main()
