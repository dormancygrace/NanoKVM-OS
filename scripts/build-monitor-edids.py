#!/usr/bin/env python3
"""Generate preferred-mode HDMI monitor profiles; never accesses hardware."""
import argparse
import hashlib
import json
from pathlib import Path

# width, height, pixel clock kHz, hblank, hfront, hsync, vblank, vfront, vsync, flags
MODES = [
    (800, 600, 40000, 256, 40, 128, 28, 1, 4, 0x1e),
    (1280, 720, 74250, 370, 110, 40, 30, 5, 5, 0x1e),
    (1920, 1080, 148500, 280, 88, 44, 45, 4, 5, 0x1e),
    (2560, 1440, 120750, 160, 48, 32, 41, 3, 5, 0x1a),
]


def profile(original, mode):
    if len(original) != 256 or original[:8] != bytes.fromhex('00ffffffffffff00'):
        raise ValueError('Expected a 256-byte EDID')
    if original[126] != 1 or any(sum(original[i:i+128]) % 256 for i in (0, 128)):
        raise ValueError('Invalid source extension count/checksums')
    if original[128:130] != bytes([2, 3]):
        raise ValueError('Expected CTA revision 3')
    w, h, clock, hb, hf, hs, vb, vf, vs, flags = mode
    d = bytearray(18)
    d[:2] = (clock // 10).to_bytes(2, 'little')
    d[2:5] = bytes([w & 255, hb & 255, ((w >> 8) << 4) | (hb >> 8)])
    d[5:8] = bytes([h & 255, vb & 255, ((h >> 8) << 4) | (vb >> 8)])
    d[8:12] = bytes([hf & 255, hs & 255, ((vf & 15) << 4) | (vs & 15),
                     ((hf >> 8) << 6) | ((hs >> 8) << 4) | ((vf >> 4) << 2) | (vs >> 4)])
    d[12:17] = original[66:71]
    d[17] = flags
    data = bytearray(original)
    data[24] = (data[24] | 2) & ~1  # Preferred DTD; no default GTF mode inference.
    data[35:54] = original[35:54]  # Preserve BIOS/legacy fallback timings.
    data[54:72] = d
    for pos in range(72, 126, 18):
        # Keep identity text only; remove additional timings/range inference.
        if data[pos:pos+5] not in (bytes.fromhex('000000ff00'), bytes.fromhex('000000fc00')):
            data[pos:pos+18] = bytes.fromhex('0000001000') + bytes(13)
    # Keep HDMI identity/audio capabilities, not other advertised video modes.
    blocks = bytearray()
    end = original[130]
    if not 4 <= end <= 127:
        raise ValueError('Invalid CTA data block boundary')
    pos = 132
    while pos < 128 + end:
        tag, length = original[pos] >> 5, original[pos] & 31
        block = original[pos:pos+1+length]
        if len(block) != 1+length or pos+1+length > 128+end:
            raise ValueError('Truncated CTA block')
        if tag in (1, 4):
            blocks.extend(block)
        elif tag == 3:
            if block[1:4] != bytes.fromhex('030c00') or length != 5:
                raise ValueError('Review vendor block before filtering video modes')
            blocks.extend(block)
        elif tag != 2:
            raise ValueError('Review unsupported CTA data block')
        pos += 1 + length
    data[128:256] = bytes(128)
    data[128:132] = bytes([2, 3, 4 + len(blocks), original[131] & 0xf0])
    data[132:132+len(blocks)] = blocks
    # CTA fallback detailed timings. Keep QHD exclusive to the QHD profile.
    offset = 132 + len(blocks)
    for fallback in MODES:
        if fallback[1] == h or fallback[1] > h:
            continue
        # Reuse the timing encoding from a single preferred profile.
        fw, fh, fc, fhb, fhf, fhs, fvb, fvf, fvs, ff = fallback
        timing = bytearray(18)
        timing[:2] = (fc // 10).to_bytes(2, 'little')
        timing[2:5] = bytes([fw & 255, fhb & 255, ((fw >> 8) << 4) | (fhb >> 8)])
        timing[5:8] = bytes([fh & 255, fvb & 255, ((fh >> 8) << 4) | (fvb >> 8)])
        timing[8:12] = bytes([fhf & 255, fhs & 255, ((fvf & 15) << 4) | (fvs & 15),
                              ((fhf >> 8) << 6) | ((fhs >> 8) << 4) | ((fvf >> 4) << 2) | (fvs >> 4)])
        timing[12:17] = original[66:71]
        timing[17] = ff
        if offset + 18 > 255:
            raise ValueError('CTA fallback timings do not fit')
        data[offset:offset+18] = timing
        offset += 18
    for pos in (0, 128):
        data[pos+127] = (-sum(data[pos:pos+127])) & 255
    return bytes(data)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--input', type=Path, required=True)
    p.add_argument('--output', type=Path, required=True)
    a = p.parse_args()
    original = a.input.read_bytes()
    a.output.mkdir(parents=True, exist_ok=True)
    (a.output / 'NanoKVM-monitor-480.bin').unlink(missing_ok=True)
    result = []
    for mode in MODES:
        data = profile(original, mode)
        target = a.output / ('NanoKVM-monitor-' + str(mode[1]) + '.bin')
        target.write_bytes(data)
        result.append(dict(file=target.name, width=mode[0], height=mode[1],
                           refresh_hz=(mode[2]//10)*10000/((mode[0]+mode[3])*(mode[1]+mode[6])),
                           sha256=hashlib.sha256(data).hexdigest()))
    (a.output / 'monitor-profiles.json').write_text(json.dumps(result, indent=2) + '\n')


if __name__ == '__main__':
    main()
