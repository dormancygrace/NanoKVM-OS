#!/usr/bin/env python3
"""The OLED boot logo images that U-Boot's nkoled command draws.

usage: gen-oled-boot-logo.py OUTPUT_DIR

Writes one image per board whose panel is on GPIO I2C (BOARD.bin for beta,
lite and pcie). Alpha has its panel on a hardware I2C controller that nkoled
does not drive, so it gets none and boots without a logo.

An image is "NKOL", version 1, board class 0, the I2C address, four zero
bytes, then records: a control byte (0x00 commands, 0x40 data), a length and
that many bytes. The records are the commands kvm_system sends
(support/kvm_system/main/lib/oled_ctrl/oled_ctrl.cpp: OLED_Init, the
orientation, the position commands of OLED_Set_Pos), so the
panel is set up the same way before and after Linux starts."""
import math
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
FONT = REPO / 'support/kvm_system/main/lib/oled_ctrl/oled_font.h'

# OLED_Init without its trailing clear and display on.
INIT = [0xAE, 0x00, 0x10, 0x40, 0x81, 0xCF, 0xA1, 0xC8, 0xA6, 0xA8, 0x3F, 0xD3, 0x00,
        0xD5, 0x80, 0xD9, 0xF1, 0xDA, 0x12, 0xDB, 0x40, 0x20, 0x02, 0x8D, 0x14, 0xA4, 0xA6]
WIDE_TURN = [0xC8, 0xA1]   # OLED_DisplayTurn(0)
NARROW_TURN = [0xA0, 0xC0]  # OLED_Revolve()
DISPLAY_ON = [0x8D, 0x14, 0xAF]
# The narrow (PCIe) panel is a 64x32 window: OLED_Set_Pos adds 32 to the
# column and 8 to the page.
BOARDS = {'beta': 'wide', 'lite': 'wide', 'pcie': 'narrow'}

# The NanoKVM OS mark (web/public/nanokvm-os-connection.svg): two chevrons
# drawn with a 10-unit round stroke and a rounded square, in a 64x64 box.
CHEVRONS = [[(25, 8), (5, 28), (25, 48)], [(39, 16), (59, 36), (39, 56)]]
STROKE = 10
SQUARE = (26, 26, 12, 12, 3)
MARK_BOX = (0, 3, 64, 61)


def font(name, width):
    text = FONT.read_text()
    body = re.search(name + r'\[\]\[' + str(width) + r'\]\s*=\s*\{(.*?)\};', text, re.S)
    rows = re.findall(r'\{([^{}]*)\}', re.sub(r'//[^\n]*|/\*.*?\*/', '', body[1], flags=re.S))
    return [[int(v, 16) for v in re.findall(r'0x[0-9A-Fa-f]{2}', row)] for row in rows]


def in_mark(x, y):
    def segment(a, b):
        (ax, ay), (bx, by) = a, b
        dx, dy = bx - ax, by - ay
        t = max(0.0, min(1.0, ((x - ax) * dx + (y - ay) * dy) / (dx * dx + dy * dy)))
        return math.hypot(x - ax - t * dx, y - ay - t * dy)
    if any(segment(a, b) <= STROKE / 2 for line in CHEVRONS for a, b in zip(line, line[1:])):
        return True
    sx, sy, sw, sh, r = SQUARE
    cx, cy = min(max(x, sx + r), sx + sw - r), min(max(y, sy + r), sy + sh - r)
    return sx <= x <= sx + sw and sy <= y <= sy + sh and math.hypot(x - cx, y - cy) <= r


def mark(size, samples=6):
    """The mark in a size x size square: a pixel is lit when half of it is."""
    x0, y0, x1, y1 = MARK_BOX
    scale = size / max(x1 - x0, y1 - y0)
    ox = x0 - (size / scale - (x1 - x0)) / 2
    oy = y0 - (size / scale - (y1 - y0)) / 2
    lit = []
    for py in range(size):
        for px in range(size):
            hits = sum(in_mark(ox + (px + (i + .5) / samples) / scale, oy + (py + (j + .5) / samples) / scale)
                       for i in range(samples) for j in range(samples))
            if 2 * hits >= samples * samples:
                lit.append((px, py))
    return lit


class Frame:
    def __init__(self, width, height):
        self.width, self.pages = width, height // 8
        self.bytes = bytearray(width * self.pages)

    def pixel(self, x, y):
        self.bytes[y // 8 * self.width + x] |= 1 << (y % 8)

    def image(self, x, y, pixels):
        for px, py in pixels:
            self.pixel(x + px, y + py)

    def text(self, y, string, glyphs, advance):
        """string centred, on page y / 8."""
        x = (self.width - len(string) * advance) // 2
        for char in string:
            column = glyphs[ord(char) - ord(' ')]
            self.bytes[y // 8 * self.width + x:y // 8 * self.width + x + advance] = bytes(column[:advance])
            x += advance


def logo(panel):
    if panel == 'wide':
        frame = Frame(128, 64)
        frame.image(44, 2, mark(40))
        frame.text(48, 'NanoKVM OS', font('oled_asc2_0806', 6), 6)
    else:
        # 32 rows leave no room for legible text beside the mark.
        frame = Frame(64, 32)
        frame.image(16, 0, mark(32))
    return frame


def image(panel):
    out = bytearray(b'NKOL' + bytes([1, 0, 0x3C, 0]))

    def record(control, data):
        for i in range(0, len(data), 255):
            chunk = data[i:i + 255]
            out.extend(bytes([control, len(chunk)]) + bytes(chunk))
    record(0x00, INIT + (WIDE_TURN if panel == 'wide' else NARROW_TURN) + [0xA6])
    # The logo frame covers all the panel shows, so the RAM is not cleared
    # first (OLED_Clear): that would double the bytes sent before Linux.
    frame = logo(panel)
    column, first_page = (0, 0xB0) if panel == 'wide' else (32, 0xB8)
    for page in range(frame.pages):
        record(0x00, [first_page + page, 0x10 | column >> 4, column & 0x0F])
        record(0x40, frame.bytes[page * frame.width:(page + 1) * frame.width])
    record(0x00, DISPLAY_ON)
    return bytes(out)


def main():
    out = Path(sys.argv[1])
    out.mkdir(parents=True, exist_ok=True)
    for board, panel in BOARDS.items():
        (out / f'{board}.bin').write_bytes(image(panel))


if __name__ == '__main__':
    main()
