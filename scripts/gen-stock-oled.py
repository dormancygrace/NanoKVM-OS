#!/usr/bin/env python3
"""The OLED screens of the stock migration.

usage: gen-stock-oled.py OUTPUT_DIR

Writes PANEL-STEP.bin for both panels (wide: Cube/Full, narrow: PCIe), in the
image format of gen-oled-boot-logo.py, which stock-oled (firmware/stock-install/
oled.c) sends to the panel. prepare and restart are shown on stock by
firmware/stock-install/install.sh, the others by the recovery boot's init."""
import importlib.util
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('logo', REPO / 'scripts/gen-oled-boot-logo.py')
logo = importlib.util.module_from_spec(spec)
spec.loader.exec_module(logo)

FOOTER = 'Keep power on'
# Step: the wide panel's two lines and footer. The narrow panel shows them in
# capitals (its font has no others).
STEPS = {
    'prepare': ('Preparing', 'Copying files', FOOTER),
    'restart': ('Prepared', 'Restarting', FOOTER),
    'check': ('Checking files', '', FOOTER),
    'boot': ('Step 1 of 3', 'Boot partition', FOOTER),
    'root': ('Step 2 of 3', 'System files', FOOTER),
    'table': ('Step 3 of 3', 'Partitions', FOOTER),
    'installed': ('Installed', 'Restarting', ''),
    'stock': ('Not installed', 'Back to stock', 'See the status file'),
    'resume': ('Stopped', 'Power cycle resumes', 'See the status file'),
    'card': ('Stopped', 'Use a card reader', 'See the status file'),
}
# The narrow panel's shorter words where the wide ones do not fit.
NARROW = {'Power cycle resumes': 'Power cycle', 'See the status file': 'See status file',
          'Use a card reader': 'Use card reader', 'Checking files': 'Checking',
          'Not installed': 'Stopped'}
ADDRESS = {'wide': 0x3D, 'narrow': 0x3C}
# At 16 pixels the computed mark breaks up; this is the hand-drawn one of
# kvm_system's PCIe screen (rows of two bytes, the high bit on the left).
MARK16 = bytes.fromhex('0000000007000e001ce038707038e39c738e3b871c0e0e1c0738007000e00000')
MARK16_PIXELS = [(x, y) for y in range(16) for x in range(16)
                 if MARK16[y * 2 + x // 8] & 0x80 >> x % 8]


def frame(panel, lines):
    if panel == 'wide':
        glyphs, advance, width = logo.font('oled_asc2_0806', 6), 6, 128
        screen = logo.Frame(128, 64)
        screen.image(0, 0, logo.mark(24))
        rows = [(28, 0, 'NanoKVM OS'), (28, 8, 'Migration'), (0, 32, lines[0]), (0, 40, lines[1]),
                (0, 56, lines[2])]
    else:
        glyphs, advance, width = logo.font('oled_asc2_0804', 4), 4, 64
        screen = logo.Frame(64, 32)
        screen.image(0, 0, MARK16_PIXELS)
        lines = [NARROW.get(line, line).upper() for line in lines]
        rows = [(18, 0, 'NANOKVM OS'), (18, 8, lines[0]), (0, 16, lines[1]), (0, 24, lines[2])]
    for x, y, text in rows:
        if x + len(text) * advance > width:
            raise ValueError(f'{panel}: "{text}" does not fit')
        screen.text(x, y, text, glyphs, advance)
    return screen


def main():
    out = Path(sys.argv[1])
    out.mkdir(parents=True, exist_ok=True)
    for panel, address in ADDRESS.items():
        for step, lines in STEPS.items():
            (out / f'{panel}-{step}.bin').write_bytes(logo.image(panel, address, frame(panel, lines)))


if __name__ == '__main__':
    main()
