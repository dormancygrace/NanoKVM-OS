# SPDX-License-Identifier: GPL-3.0-or-later
"""Synthetic pictures of screens for the screen-state classifier.

    python3 synth.py OUT COUNT [SEED]

Writes OUT/<class>/<n>.png at the classifier's input size (classes below).
Each screen is drawn at half the size of a common resolution and scaled
down the way VPSS scales the capture: the whole frame, whatever its aspect.
Nothing here is a copy of a real screen; the layouts follow what the
screens look like (colours, text blocks, where the big text sits).
"""
import math
import os
import random
import sys
from multiprocessing import Pool

from PIL import Image, ImageDraw, ImageFilter, ImageFont

# The classes, in the model's output order.
CLASSES = ['normal', 'bsod', 'console', 'blank', 'firmware']
# The model's input, width x height.
INPUT = (224, 128)
RESOLUTIONS = [(1920, 1080), (1280, 720), (1366, 768), (1600, 900), (1024, 768),
               (1280, 1024), (1440, 900), (1280, 800), (800, 600), (2560, 1440), (720, 400)]

FONT_DIR = '/usr/share/fonts/truetype/dejavu/'
SANS = [FONT_DIR + f for f in ('DejaVuSans.ttf', 'DejaVuSans-Bold.ttf', 'DejaVuSerif.ttf', 'DejaVuSerif-Bold.ttf')]
MONO = [FONT_DIR + f for f in ('DejaVuSansMono.ttf', 'DejaVuSansMono-Bold.ttf')]
CJK = ['/usr/share/fonts/truetype/arphic-gbsn00lp/gbsn00lp.ttf', '/usr/share/fonts/truetype/arphic-bsmi00lp/bsmi00lp.ttf']

WORDS = ('the of and to in is for that on with as was at by from this be are or system error device '
         'driver memory process file user service network update windows linux kernel module boot '
         'start stop failed loading mount root disk power usb pci interrupt task thread cpu address').split()
CYRILLIC = 'система ошибка устройство драйвер память процесс файл служба сеть обновление загрузка'.split()
LOG_SOURCES = ['usb 1-1', 'ata1', 'EXT4-fs (sda1)', 'systemd[1]', 'eth0', 'nvme0n1', 'i915 0000:00:02.0',
               'pci 0000:00:1f.3', 'ACPI', 'Bluetooth', 'audit', 'xhci_hcd', 'amdgpu', 'r8169', 'kvm']


def font(paths, size):
    return ImageFont.truetype(random.choice(paths), max(6, int(size)))


def sentence(n=None, cyrillic=False):
    n = n or random.randint(3, 12)
    words = CYRILLIC if cyrillic and random.random() < 0.7 else WORDS
    s = ' '.join(random.choice(words) for _ in range(n))
    return s[:1].upper() + s[1:]


def hexword(n=8):
    return ''.join(random.choice('0123456789abcdef') for _ in range(n))


def jitter(c, d=12):
    return tuple(max(0, min(255, v + random.randint(-d, d))) for v in c)


def rounded(d, box, radius, **kw):
    """A rounded rectangle whose corners fit it, however small it is drawn."""
    x0, y0, x1, y1 = (round(v) for v in box)
    radius = int(max(0, min(radius, (x1 - x0 - 1) / 2, (y1 - y0 - 1) / 2)))
    if x1 > x0 and y1 > y0:
        d.rounded_rectangle([x0, y0, x1, y1], radius, **kw)


def log_line(t):
    kind = random.random()
    if kind < 0.5:
        return f'[{t:12.6f}] {random.choice(LOG_SOURCES)}: {sentence().lower()}'
    if kind < 0.7:
        return f'[  OK  ] {random.choice(["Started", "Reached target", "Mounted", "Finished"])} {sentence(4)}.'
    if kind < 0.85:
        return ' '.join(f'{random.choice(["RAX", "RBX", "RCX", "RDX", "RSI", "RDI", "RBP", "R08"])}: {hexword(16)}' for _ in range(3))
    return f' {random.choice(["? ", ""])}{random.choice(WORDS)}_{random.choice(WORDS)}+0x{hexword(3)}/0x{hexword(3)}'


PANIC_LINES = [
    'Kernel panic - not syncing: VFS: Unable to mount root fs on unknown-block(0,0)',
    'Kernel panic - not syncing: Attempted to kill init! exitcode=0x0000000b',
    'Kernel panic - not syncing: Fatal exception in interrupt',
    'BUG: kernel NULL pointer dereference, address: 0000000000000008',
    'Oops: 0000 [#1] PREEMPT SMP NOPTI',
    'general protection fault, probably for non-canonical address 0xdead000000000100',
    'watchdog: BUG: soft lockup - CPU#2 stuck for 22s!',
    'Call Trace:', ' <TASK>', ' </TASK>', '---[ end Kernel panic - not syncing: Fatal exception ]---',
]


def screen_console(img, d, w, h):
    """A text console: kernel messages, a panic, a login prompt, a boot log."""
    bg = random.choice([(0, 0, 0)] * 6 + [(12, 12, 12), (0, 0, 40), (30, 30, 30)])
    d.rectangle([0, 0, w, h], fill=bg)
    rows = random.choice([25, 30, 37, 40, 45, 50, 60, 67])
    size = h / rows
    f = font(MONO, size * 0.85)
    fg = random.choice([(170, 170, 170)] * 4 + [(255, 255, 255), (200, 200, 200), (0, 200, 0), (220, 220, 120)])
    fill = random.choice([1.0, 1.0, 1.0, 0.8, 0.5, 0.3, 0.12])
    n = max(1, int(rows * fill))
    start = 0 if random.random() < 0.6 else random.randint(0, rows - n)
    t = random.uniform(0, 3000)
    panic = random.random() < 0.5
    for i in range(n):
        t += random.uniform(0, 0.5)
        if panic and i > n * 0.5 and random.random() < 0.3:
            line = random.choice(PANIC_LINES)
        elif random.random() < 0.08:
            line = f'{random.choice(["nanokvm", "server", "localhost", "debian"])} login: '
        else:
            line = log_line(t)
        colour = fg
        if line.startswith('[  OK'):
            colour = jitter(fg, 20)
        d.text((size * 0.2, (start + i) * size), line[:int(w / (size * 0.5))], font=f, fill=colour)
    if random.random() < 0.2:  # the drm panic banner of newer kernels, or a penguin logo
        x, y = random.randint(0, w // 2), random.randint(0, h // 3)
        d.rectangle([x, y, x + w // 6, y + h // 8], fill=jitter((60, 60, 60), 40))


def qr(d, x, y, s):
    d.rectangle([x, y, x + s, y + s], fill=(255, 255, 255))
    cell = s / 25
    for i in range(25):
        for j in range(25):
            if random.random() < 0.5:
                d.rectangle([x + i * cell, y + j * cell, x + (i + 1) * cell, y + (j + 1) * cell], fill=(0, 0, 0))


def screen_bsod(img, d, w, h):
    style = random.random()
    cyr = random.random() < 0.25
    if style < 0.18:  # Windows XP/7: VGA blue, a page of fixed-width text
        d.rectangle([0, 0, w, h], fill=jitter((0, 0, 170), 6))
        rows = random.choice([25, 30, 37, 50])
        f = font(MONO, h / rows * 0.85)
        lines = ['A problem has been detected and Windows has been shut down to prevent damage',
                 'to your computer.', '', sentence(6, cyr), sentence(9, cyr), sentence(7, cyr), '',
                 'Technical information:', '',
                 f'*** STOP: 0x000000{hexword(2).upper()} (0x{hexword(8).upper()},0x{hexword(8).upper()},0x00000000,0x00000000)',
                 '', 'Collecting data for crash dump ...', 'Beginning dump of physical memory',
                 f'Dumping physical memory to disk:  {random.randint(5, 100)}']
        for i, line in enumerate(lines[:rows]):
            d.text((w * 0.01, h / rows * (i + 1)), line, font=f, fill=(255, 255, 255))
        return
    # Windows 8, 10, 11: a sad face, two big lines, a percentage, a QR code.
    colour = random.choices([(0, 120, 215), (16, 115, 170), (0, 103, 184), (16, 124, 16), (0, 0, 0), (9, 9, 9)],
                            weights=[5, 2, 2, 1, 2, 1])[0]
    d.rectangle([0, 0, w, h], fill=jitter(colour, 8))
    x = w * random.uniform(0.07, 0.12)
    y = h * random.uniform(0.08, 0.28)
    modern_black = colour[0] < 20 and colour[2] < 20
    big = h * random.uniform(0.04, 0.06)
    if not modern_black:
        d.text((x, y), ':(', font=font(SANS, big * 3.2), fill=(255, 255, 255))
        y += big * 4.2
    paths = CJK if random.random() < 0.1 else SANS
    f = font(paths, big)
    first = random.choice(['Your PC ran into a problem and needs to restart. We\'re just',
                           'Your device ran into a problem and needs to restart.',
                           'На вашем ПК возникла проблема, и его необходимо перезагрузить.' if paths is SANS else '你的设备遇到问题，需要重启。'])
    d.text((x, y), first, font=f, fill=(255, 255, 255))
    y += big * 1.4
    if random.random() < 0.7:
        d.text((x, y), sentence(8, cyr).lower(), font=f, fill=(255, 255, 255))
        y += big * 2.2
    d.text((x, y), f'{random.randint(0, 100)}% complete', font=f, fill=(255, 255, 255))
    y += big * 2.4
    small = font(SANS, big * 0.42)
    if not modern_black or random.random() < 0.3:
        s = big * 2.6
        qr(d, x, y, s)
        tx = x + s + big * 0.5
    else:
        tx = x
    for i in range(random.randint(2, 5)):
        line = random.choice(['For more information about this issue and possible fixes, visit',
                              'https://www.windows.com/stopcode', 'If you call a support person, give them this info:',
                              f'Stop code: {random.choice(["CRITICAL_PROCESS_DIED", "IRQL_NOT_LESS_OR_EQUAL", "MEMORY_MANAGEMENT", "PAGE_FAULT_IN_NONPAGED_AREA", "SYSTEM_SERVICE_EXCEPTION", "DPC_WATCHDOG_VIOLATION"])}',
                              f'What failed: {random.choice(WORDS)}.sys'])
        d.text((tx, y + i * big * 0.6), line, font=small, fill=(255, 255, 255))


def screen_blank(img, d, w, h):
    kind = random.random()
    if kind < 0.45:
        d.rectangle([0, 0, w, h], fill=jitter((0, 0, 0), 6))
    elif kind < 0.65:
        d.rectangle([0, 0, w, h], fill=tuple(random.randint(0, 255) for _ in range(3)))
    else:  # a monitor's or capture's "no signal" box on black, or a lone cursor
        d.rectangle([0, 0, w, h], fill=(0, 0, 0))
        if random.random() < 0.5:
            f = font(SANS, h * random.uniform(0.03, 0.06))
            text = random.choice(['No Signal', 'NO SIGNAL', 'Check signal cable', 'Нет сигнала', 'Out of range'])
            bw, bh = w * random.uniform(0.2, 0.35), h * random.uniform(0.08, 0.15)
            x, y = random.uniform(0, w - bw), random.uniform(0, h - bh)
            d.rectangle([x, y, x + bw, y + bh], fill=jitter((40, 40, 40), 30), outline=(200, 200, 200))
            d.text((x + bw * 0.1, y + bh * 0.25), text, font=f, fill=(230, 230, 230))
        else:
            s = h / 40
            d.rectangle([s, s, s * 1.5, s * 1.8], fill=(170, 170, 170))


def tui_box(d, x0, y0, x1, y1, bg, fg, f, rows, shadow=True):
    if shadow:
        d.rectangle([x0 + 8, y0 + 8, x1 + 8, y1 + 8], fill=(0, 0, 0))
    d.rectangle([x0, y0, x1, y1], fill=bg, outline=fg)
    step = (y1 - y0) / (rows + 2)
    for i in range(rows):
        d.text((x0 + step, y0 + step * (i + 1)), sentence(random.randint(2, 6)), font=f, fill=fg)


def screen_firmware(img, d, w, h):
    """BIOS setup, GRUB, text installers: blue or grey screens that are not a crash."""
    kind = random.random()
    rows = random.choice([25, 30, 37])
    f = font(MONO, h / rows * 0.8)
    if kind < 0.3:  # BIOS setup: a blue or grey field with a highlighted menu and a help column
        bg = random.choice([(0, 0, 170), (0, 0, 128), (170, 170, 170), (50, 50, 140), (0, 40, 110)])
        d.rectangle([0, 0, w, h], fill=bg)
        d.rectangle([0, 0, w, h / rows * 2], fill=random.choice([(0, 170, 170), (170, 170, 170), (0, 0, 0)]))
        d.text((w * 0.3, h / rows * 0.4), random.choice(['Aptio Setup Utility', 'BIOS SETUP UTILITY', 'PhoenixBIOS Setup Utility']), font=f, fill=(255, 255, 255))
        d.rectangle([w * 0.7, h / rows * 2, w * 0.71, h], fill=(255, 255, 255))
        for i in range(random.randint(8, rows - 4)):
            y = h / rows * (3 + i)
            fill = (255, 255, 255) if random.random() < 0.8 else (255, 255, 0)
            d.text((w * 0.03, y), sentence(random.randint(1, 4)), font=f, fill=fill)
            d.text((w * 0.4, y), f'[{random.choice(["Enabled", "Disabled", "Auto", hexword(4)])}]', font=f, fill=fill)
        tui_box(d, w * 0.72, h * 0.15, w * 0.98, h * 0.6, bg, (255, 255, 255), f, 6, shadow=False)
    elif kind < 0.55:  # a text installer (newt): blue background, grey dialog with a shadow
        d.rectangle([0, 0, w, h], fill=random.choice([(0, 0, 170), (30, 60, 160), (0, 80, 140)]))
        d.text((w * 0.01, h * 0.01), sentence(3), font=f, fill=(255, 255, 255))
        x0, y0 = w * random.uniform(0.1, 0.25), h * random.uniform(0.15, 0.3)
        tui_box(d, x0, y0, w - x0, h - y0, (200, 200, 200), (0, 0, 0), f, random.randint(4, 10))
    elif kind < 0.8:  # GRUB: a framed menu on black, one line highlighted
        d.rectangle([0, 0, w, h], fill=(0, 0, 0))
        fg = random.choice([(170, 170, 170), (255, 255, 255)])
        d.text((w * 0.3, h * 0.04), 'GNU GRUB  version 2.12', font=f, fill=fg)
        d.rectangle([w * 0.04, h * 0.12, w * 0.96, h * 0.65], outline=fg)
        for i in range(random.randint(2, 7)):
            y = h * 0.14 + i * h / rows
            if i == 0:
                d.rectangle([w * 0.045, y, w * 0.955, y + h / rows], fill=fg)
            d.text((w * 0.06, y), random.choice(['*', ' ']) + sentence(4), font=f, fill=(0, 0, 0) if i == 0 else fg)
        for i in range(3):
            d.text((w * 0.08, h * 0.7 + i * h / rows), sentence(10), font=f, fill=fg)
    else:  # a POST screen: a logo, a few lines, a key hint
        d.rectangle([0, 0, w, h], fill=(0, 0, 0))
        d.rectangle([w * 0.35, h * 0.3, w * 0.65, h * 0.5], fill=jitter((200, 30, 30), 80))
        g = font(SANS, h * 0.025)
        d.text((w * 0.02, h * 0.92), random.choice(['Press DEL or F2 to enter setup', 'Press F12 for boot menu']), font=g, fill=(200, 200, 200))


def background(d, w, h):
    kind = random.random()
    if kind < 0.35:  # a gradient, blue more often than not (a common wallpaper)
        a = random.choice([(0, 90, 200), (10, 40, 120), (30, 30, 60), tuple(random.randint(0, 255) for _ in range(3))])
        b = tuple(random.randint(0, 255) for _ in range(3))
        for y in range(0, h, 4):
            t = y / h
            d.rectangle([0, y, w, y + 4], fill=tuple(int(a[i] * (1 - t) + b[i] * t) for i in range(3)))
    elif kind < 0.6:  # blobs: a photo seen from far away
        d.rectangle([0, 0, w, h], fill=tuple(random.randint(0, 255) for _ in range(3)))
        for _ in range(random.randint(10, 60)):
            x, y, r = random.uniform(0, w), random.uniform(0, h), random.uniform(w * 0.02, w * 0.3)
            d.ellipse([x - r, y - r, x + r, y + r], fill=tuple(random.randint(0, 255) for _ in range(3)))
    else:
        d.rectangle([0, 0, w, h], fill=tuple(random.randint(0, 255) for _ in range(3)))


def window(d, x0, y0, x1, y1, f):
    dark = random.random() < 0.4
    bg = jitter((30, 30, 34) if dark else (250, 250, 250), 15)
    fg = (220, 220, 220) if dark else (30, 30, 30)
    d.rectangle([x0, y0, x1, y1], fill=bg, outline=jitter((120, 120, 120), 40))
    bar = (y1 - y0) * random.uniform(0.04, 0.08)
    d.rectangle([x0, y0, x1, y0 + bar], fill=jitter((60, 60, 70) if dark else (225, 225, 230), 25))
    kind = random.random()
    y = y0 + bar * 1.5
    line = f.size * 1.4
    if kind < 0.3:  # a page: header, pictures, paragraphs
        d.rectangle([x0, y, x1, y + bar * 1.5], fill=tuple(random.randint(0, 255) for _ in range(3)))
        y += bar * 2
        while y < y1 - line:
            if random.random() < 0.2:
                hh = min(y1 - y, (y1 - y0) * 0.2)
                d.rectangle([x0 + 10, y, x0 + (x1 - x0) * 0.4, y + hh], fill=tuple(random.randint(0, 255) for _ in range(3)))
                y += hh + line
            d.text((x0 + 10, y), sentence(random.randint(5, 14)), font=f, fill=fg)
            y += line
    elif kind < 0.6:  # code or a terminal in a window: coloured monospace
        while y < y1 - line:
            colour = random.choice([fg, (86, 156, 214), (206, 145, 120), (106, 153, 85), (197, 134, 192)])
            d.text((x0 + 10 + random.randint(0, 4) * 20, y), log_line(random.uniform(0, 100))[:60], font=f, fill=colour)
            y += line
    else:  # a list or a table
        while y < y1 - line:
            d.rectangle([x0 + 10, y + 2, x0 + 10 + line * 0.8, y + line * 0.8], fill=tuple(random.randint(0, 255) for _ in range(3)))
            d.text((x0 + 20 + line, y), sentence(random.randint(2, 6)), font=f, fill=fg)
            y += line * random.uniform(1.2, 2)


def browser(d, w, h):
    """A maximised browser or app, often in a dark theme: tabs, an address
    bar, then a page of mixed proportional text, cards and buttons."""
    dark = random.random() < 0.6
    bg = jitter((32, 33, 36) if dark else (255, 255, 255), 14)
    fg = (232, 234, 237) if dark else (32, 33, 36)
    accent = random.choice([(138, 180, 248), (26, 115, 232), (52, 168, 83), (234, 67, 53), (251, 188, 4)])
    d.rectangle([0, 0, w, h], fill=bg)
    bar = h * random.uniform(0.035, 0.05)
    d.rectangle([0, 0, w, bar * 2], fill=jitter((53, 54, 58) if dark else (222, 225, 230), 10))
    for i in range(random.randint(1, 8)):  # tabs
        rounded(d, [i * w * 0.12 + 8, 4, (i + 1) * w * 0.12, bar], max(1, min(6, (bar - 4) / 2)),
                            fill=bg if i == 0 else jitter((53, 54, 58) if dark else (222, 225, 230), 10))
    rounded(d, [w * 0.08, bar * 1.15, w * 0.9, bar * 1.85], 12, fill=jitter((32, 33, 36) if dark else (241, 243, 244), 8))
    d.text((w * 0.1, bar * 1.25), 'https://' + random.choice(WORDS) + '.com/' + random.choice(WORDS), font=font(SANS, bar * 0.4), fill=fg)
    y = bar * 2.6
    while y < h * 0.95:
        kind = random.random()
        size = h * random.choice([0.015, 0.018, 0.02, 0.025, 0.035, 0.05, 0.08])
        x = w * random.uniform(0.05, 0.4)
        if kind < 0.5:
            d.text((x, y), sentence(random.randint(2, 10)), font=font(SANS, size), fill=random.choice([fg, fg, accent]))
            y += size * random.uniform(1.4, 2.5)
        elif kind < 0.75:  # a card or a widget, a big number in it sometimes
            cw, ch = w * random.uniform(0.2, 0.6), h * random.uniform(0.1, 0.35)
            rounded(d, [x, y, x + cw, y + ch], 10, fill=jitter((48, 49, 52) if dark else (248, 249, 250), 10),
                                outline=jitter((95, 99, 104) if dark else (218, 220, 224), 10))
            if random.random() < 0.5:
                d.text((x + cw * 0.15, y + ch * 0.25), f'{random.randint(0, 99):02d}:{random.randint(0, 59):02d}.{random.randint(0, 99):02d}',
                       font=font(SANS, ch * 0.35), fill=fg)
            y += ch + size
        else:  # buttons or a picture
            for i in range(random.randint(1, 4)):
                rounded(d, [x + i * w * 0.1, y, x + i * w * 0.1 + w * 0.08, y + size * 2], 16, fill=accent)
            y += size * 3
    if random.random() < 0.6:  # the taskbar under it
        tb = h * 0.045
        d.rectangle([0, h - tb, w, h], fill=jitter((28, 28, 28) if dark else (243, 243, 243), 8))
        for i in range(random.randint(5, 16)):
            rounded(d, [w * 0.3 + i * tb * 1.2, h - tb * 0.85, w * 0.3 + i * tb * 1.2 + tb * 0.7, h - tb * 0.15], 4,
                                fill=tuple(random.randint(40, 255) for _ in range(3)))


def screen_normal(img, d, w, h):
    """Desktops and applications, blue wallpapers and dark editors among them."""
    if random.random() < 0.35:
        browser(d, w, h)
        return
    if random.random() < 0.25:  # one application full screen
        window(d, 0, 0, w, h, font(SANS + MONO, h / random.uniform(40, 70)))
    else:
        background(d, w, h)
        for _ in range(random.randint(0, 6)):
            x0, y0 = random.uniform(-w * 0.1, w * 0.8), random.uniform(0, h * 0.7)
            x1, y1 = x0 + random.uniform(w * 0.2, w * 0.8), y0 + random.uniform(h * 0.2, h * 0.8)
            window(d, x0, y0, x1, y1, font(SANS + MONO, h / random.uniform(45, 80)))
        if random.random() < 0.3:  # icons on the desktop
            s = h / 20
            for i in range(random.randint(3, 15)):
                x, y = s * 0.5, s * (0.5 + 1.6 * i)
                d.rectangle([x, y, x + s, y + s], fill=tuple(random.randint(0, 255) for _ in range(3)))
    if random.random() < 0.8:  # a taskbar or a panel
        bh = h * random.uniform(0.03, 0.05)
        y = h - bh if random.random() < 0.7 else 0
        d.rectangle([0, y, w, y + bh], fill=jitter(random.choice([(32, 32, 32), (240, 240, 240), (20, 30, 60)]), 15))
        for i in range(random.randint(3, 12)):
            d.rectangle([w * 0.3 + i * bh * 1.3, y + bh * 0.15, w * 0.3 + i * bh * 1.3 + bh * 0.7, y + bh * 0.85],
                        fill=tuple(random.randint(0, 255) for _ in range(3)))
    if random.random() < 0.15:  # a sign-in screen: a blurred picture, a box in the middle
        img.paste(img.filter(ImageFilter.GaussianBlur(20)))
        rounded(d, [w * 0.4, h * 0.45, w * 0.6, h * 0.52], 6, fill=(255, 255, 255))
        d.ellipse([w * 0.46, h * 0.25, w * 0.54, h * 0.25 + w * 0.08], fill=jitter((200, 200, 200), 40))


DRAW = {'normal': screen_normal, 'bsod': screen_bsod, 'console': screen_console,
        'blank': screen_blank, 'firmware': screen_firmware}
# Draws per class: the hard ones get more.
WEIGHTS = {'normal': 4, 'bsod': 2, 'console': 2, 'blank': 1, 'firmware': 2}


def make(job):
    out, index, seed = job
    random.seed(seed)
    label = random.choices(CLASSES, weights=[WEIGHTS[c] for c in CLASSES])[0]
    w, h = random.choice(RESOLUTIONS)
    w, h = w // 2, h // 2
    img = Image.new('RGB', (w, h))
    DRAW[label](img, ImageDraw.Draw(img), w, h)
    # Scaled like the capture; then a little noise in level and colour.
    small = img.resize(INPUT, random.choice([Image.BILINEAR, Image.BOX, Image.LANCZOS]))
    if random.random() < 0.5:
        gain = random.uniform(0.85, 1.1)
        small = small.point(lambda v: max(0, min(255, int(v * gain + random.uniform(-6, 6)))))
    small.save(os.path.join(out, label, f'{index}.png'))
    return label


def main():
    out, count = sys.argv[1], int(sys.argv[2])
    seed = int(sys.argv[3]) if len(sys.argv) > 3 else 1
    for c in CLASSES:
        os.makedirs(os.path.join(out, c), exist_ok=True)
    jobs = [(out, i, seed * 1_000_003 + i) for i in range(count)]
    with Pool() as pool:
        labels = pool.map(make, jobs, chunksize=64)
    print({c: labels.count(c) for c in CLASSES})


if __name__ == '__main__':
    main()
