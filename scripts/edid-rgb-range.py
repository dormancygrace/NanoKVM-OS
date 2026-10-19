#!/usr/bin/env python3
"""Declare the RGB quantization range selectable in monitor EDID profiles.

Without a CTA Video Capability Data Block a source must send limited-range RGB
(16-235) in CE video formats such as 1920x1080 at 60 Hz. The HDMI receivers
convert RGB to YCbCr as if it were full range, so black arrived as 16 and the
picture came out pale in every codec. With QS=1 the source may send full-range
RGB and says so in its AVI InfoFrame; NVIDIA then does by default. QY stays 0:
the capture expects limited-range YCbCr from the receiver. IT and CE formats
are declared always underscanned: the device captures the whole raster.

Edits the given files in place; never accesses hardware.
"""
import argparse
from pathlib import Path

VCDB_TAG, VCDB_EXT = 7, 0
CAPABILITY = 0x4A  # QS=1, QY=0, S_PT=00 (no data), S_IT=10, S_CE=10


def _checksum_ok(block):
    return sum(block) % 256 == 0


def with_rgb_range(original):
    data = bytearray(original)
    if len(data) != 256 or data[:8] != bytes.fromhex('00ffffffffffff00'):
        raise ValueError('Expected a 256-byte EDID')
    if data[126] != 1 or not (_checksum_ok(data[:128]) and _checksum_ok(data[128:])):
        raise ValueError('Invalid extension count or checksums')
    if data[128:130] != bytes([2, 3]):
        raise ValueError('Expected CTA revision 3')
    end = data[130]
    if not 4 <= end <= 127:
        raise ValueError('Invalid CTA data block boundary')
    pos = 4
    while pos < end:
        tag, length = data[128+pos] >> 5, data[128+pos] & 31
        if pos + 1 + length > end:
            raise ValueError('Truncated CTA block')
        if tag == VCDB_TAG and length >= 2 and data[128+pos+1] == VCDB_EXT:
            if data[128+pos+2] != CAPABILITY:
                raise ValueError('Review the existing Video Capability Data Block')
            return bytes(data)
        pos += 1 + length
    # Detailed timings follow the data blocks; a zero pixel clock ends them.
    dtd = end
    while dtd + 18 <= 127 and data[128+dtd:128+dtd+2] != bytes(2):
        dtd += 18
    if any(data[128+dtd:255]):
        raise ValueError('Unexpected bytes after the detailed timings')
    if dtd + 3 > 127:
        raise ValueError('No room for the Video Capability Data Block')
    timings = data[128+end:128+dtd]
    vcdb = bytes([(VCDB_TAG << 5) | 2, VCDB_EXT, CAPABILITY])
    data[128+end:255] = (vcdb + timings).ljust(127 - end, b'\0')
    data[130] = end + len(vcdb)
    data[255] = (-sum(data[128:255])) & 255
    return bytes(data)


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument('files', type=Path, nargs='+')
    for path in p.parse_args().files:
        try:
            path.write_bytes(with_rgb_range(path.read_bytes()))
        except ValueError as e:
            raise SystemExit(f'{path}: {e}')


if __name__ == '__main__':
    main()
