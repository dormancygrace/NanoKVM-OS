#!/usr/bin/env python3
"""OLED layouts: parsing limits, drawing, and the built-in layouts drawing
exactly what the roaming status drew before them."""
from pathlib import Path
import subprocess, tempfile
repo = Path(__file__).resolve().parents[1]
lib = repo / 'support/kvm_system/main/lib'
source = r'''
#include "oled_layout.h"
#include "oled_font.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>

// The roaming status as oled_ui drew it, in display pages.
static void put_string(uint8_t *frame, int width, int x, int page, const char *text, int font)
{
    for (; *text; text++) {
        char chr = *text;
        if (font == 4 && chr >= 'a' && chr <= 'z') chr = chr - 'a' + 'A';
        unsigned index = (unsigned char)chr - ' ';
        if (font == 4) {
            if (index >= sizeof(oled_asc2_0804) / 4) index = 0;
            for (int i = 0; i < 4; i++) if (x + i < width) frame[page * width + x + i] = oled_asc2_0804[index][i];
            x += 4;
        } else if (font == 8) {
            if (index >= sizeof(oled_asc2_0806) / 6) index = 0;
            for (int i = 0; i < 6; i++) if (x + i < width) frame[page * width + x + i] = oled_asc2_0806[index][i];
            x += 6;
        } else {
            if (index >= sizeof(oled_asc2_1608) / 16) index = 0;
            for (int i = 0; i < 8; i++) if (x + i < width) {
                frame[page * width + x + i] = oled_asc2_1608[index][i];
                frame[(page + 1) * width + x + i] = oled_asc2_1608[index][8 + i];
            }
            x += 8;
        }
    }
}

static void roaming(bool narrow, const OledValues &v, unsigned step, uint8_t *frame)
{
    memset(frame, 0, OLED_FRAME_BYTES);
    const unsigned width = narrow ? 64 : 128, pages = narrow ? 4 : 8, font = narrow ? 4 : 8;
    const unsigned block_width = narrow ? 60 : 120, block_pages = narrow ? 3 : 4;
    const unsigned x = (step * 13) % (width - block_width + 1);
    const unsigned page = step % (pages - block_pages + 1);
    char lines[3][32] = {};
    snprintf(lines[0], sizeof(lines[0]), "%-15.15s", v.ip.c_str());
    snprintf(lines[1], sizeof(lines[1]), "%-4s %4dX%-4d", v.codec.c_str(), v.width, v.height);
    snprintf(lines[2], sizeof(lines[2]), "%3d FPS E%c W%c  ", v.fps, v.eth ? '+' : '-', v.wifi ? '+' : '-');
    for (unsigned i = 0; i < 3; ++i) {
        lines[i][15] = 0;
        put_string(frame, width, x, page + (narrow || i == 0 ? i : i + 1), lines[i], !narrow && i == 0 ? 16 : font);
    }
}

static bool pixel(const uint8_t *frame, int width, int x, int y) { return frame[(y / 8) * width + x] >> (y % 8) & 1; }

static bool parses(const char *text)
{
    OledLayout layout;
    return oled_layout_parse(text, &layout, nullptr);
}

int main()
{
    uint8_t frame[OLED_FRAME_BYTES], expected[OLED_FRAME_BYTES];
    OledValues states[3];
    states[0].ip = "--"; states[0].codec = "VID";
    states[1].ip = "192.168.100.200"; states[1].codec = "H264"; states[1].width = 1920; states[1].height = 1080;
    states[1].fps = 60; states[1].eth = true;
    states[2].ip = "10.0.0.7"; states[2].codec = "MJPG"; states[2].width = 640; states[2].height = 480;
    states[2].fps = 7; states[2].wifi = true;
    for (int narrow = 0; narrow < 2; narrow++) {
        OledLayout layout;
        std::string error;
        assert(oled_layout_parse(oled_layout_default(narrow), &layout, &error));
        assert(layout.narrow == bool(narrow));
        for (const OledValues &v : states)
            for (unsigned step = 1; step < 12; step++) {
                oled_layout_render(layout, v, step, frame);
                roaming(narrow, v, step, expected);
                assert(!memcmp(frame, expected, OLED_FRAME_BYTES));
            }
    }

    OledValues v;
    v.hostname = "kvm-1234"; v.fps = 5;
    assert(oled_layout_expand("{hostname}|{fps:3}|{fps:-3}|{hostname:3}|{nope}", v) == "kvm-1234|  5|5  |kvm|{nope}");

    // Rotation, the image bit order, a right-aligned text box, conditions.
    OledLayout layout;
    assert(oled_layout_parse("nanokvm-oled 1 wide\nrotation 180\nline 0 0 0 0 1 -\n", &layout, nullptr));
    oled_layout_render(layout, v, 0, frame);
    assert(pixel(frame, 128, 127, 63) && !pixel(frame, 128, 0, 0));
    assert(oled_layout_parse("nanokvm-oled 1 wide\nrotation 90\nline 0 0 0 0 1 -\nline 63 127 63 127 1 -\n", &layout, nullptr));
    oled_layout_render(layout, v, 0, frame);
    assert(pixel(frame, 128, 127, 0) && pixel(frame, 128, 0, 63));
    assert(oled_layout_parse("nanokvm-oled 1 narrow\nimage 2 1 9 2 1 - 80804000\n", &layout, nullptr));
    oled_layout_render(layout, v, 0, frame);
    assert(pixel(frame, 64, 2, 1) && pixel(frame, 64, 10, 1) && pixel(frame, 64, 3, 2) && !pixel(frame, 64, 2, 2));
    assert(oled_layout_parse("nanokvm-oled 1 wide\nrect 0 0 128 64 1 1 -\ntext 0 0 8 right 12 0 !eth  A\n", &layout, nullptr));
    oled_layout_render(layout, v, 0, frame);
    assert(pixel(frame, 128, 0, 0) && pixel(frame, 128, 127, 63));
    assert(!pixel(frame, 128, 8, 1) && pixel(frame, 128, 2, 1)); // "A" at 6..11, cleared pixels
    v.eth = true;
    oled_layout_render(layout, v, 0, frame);
    assert(pixel(frame, 128, 8, 1));
    assert(oled_layout_parse("nanokvm-oled 1 wide\nburnin shift\nline 0 0 0 0 1 -\n", &layout, nullptr));
    oled_layout_render(layout, v, 2, frame);
    assert(pixel(frame, 128, 1, 1) && !pixel(frame, 128, 0, 0));

    // What the server must never get past: the parser refuses it too.
    assert(!parses(""));
    assert(!parses("nanokvm-oled 2 wide\n"));
    assert(!parses("nanokvm-oled 1 wide\nrotation 45\n"));
    assert(!parses("nanokvm-oled 1 wide\ntext 0 0 12 left 0 1 - x\n"));
    assert(!parses("nanokvm-oled 1 wide\ntext 0 0 8 middle 0 1 - x\n"));
    assert(!parses("nanokvm-oled 1 wide\ntext 0 0 8 left 0 1 ethernet x\n"));
    assert(!parses("nanokvm-oled 1 wide\nline 0 0 0 999 1 -\n"));
    assert(!parses("nanokvm-oled 1 wide\nrect 0 0 0 4 0 1 -\n"));
    assert(!parses("nanokvm-oled 1 wide\nimage 0 0 9 2 1 - 808040\n"));
    assert(!parses("nanokvm-oled 1 wide\nimage 0 0 8 1 1 - 0x\n"));
    assert(!parses("nanokvm-oled 1 wide\nline 0 0 1 1 1 - extra\n"));
    std::string long_text = "nanokvm-oled 1 wide\ntext 0 0 8 left 0 1 - " + std::string(129, 'x') + "\n";
    assert(!parses(long_text.c_str()));
    std::string many = "nanokvm-oled 1 wide\n";
    for (int i = 0; i < 64; i++) many += "line 0 0 1 1 1 -\n";
    assert(parses(many.c_str()));
    many += "line 0 0 1 1 1 -\n";
    assert(!parses(many.c_str()));
    puts("PASS oled layouts: built-in layouts draw the roaming status; parsing limits; drawing");
    return 0;
}
'''
with tempfile.TemporaryDirectory() as tmp:
    test = Path(tmp) / 'test.cpp'
    test.write_text(source)
    binary = Path(tmp) / 'test'
    subprocess.run(['c++', '-std=gnu++17', '-Wall', '-Wextra', '-Werror', '-fsanitize=address,undefined',
                    '-I', str(lib / 'oled_layout'), '-I', str(lib / 'oled_ctrl'),
                    str(test), str(lib / 'oled_layout/oled_layout.cpp'), '-o', str(binary)], check=True)
    subprocess.run([str(binary)], check=True)
