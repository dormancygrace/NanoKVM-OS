#!/usr/bin/env python3
"""OLED layouts: parsing limits, drawing, conditions and pages, and the
built-in layouts."""
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

static bool pixel(const uint8_t *frame, int width, int x, int y) { return frame[(y / 8) * width + x] >> (y % 8) & 1; }

static bool parses(const char *text)
{
    OledLayout layout;
    return oled_layout_parse(text, &layout, nullptr);
}

int main()
{
    uint8_t frame[OLED_FRAME_BYTES];
    // The built-in layouts and boot logos are for their panels, and draw every
    // page of every state inside the frame (the sanitizers watch).
    for (int narrow = 0; narrow < 2; narrow++) {
        OledLayout layout;
        std::string error;
        assert(oled_layout_parse(oled_layout_default(narrow), &layout, &error));
        assert(layout.narrow == bool(narrow) && layout.pages == 2);
        assert(oled_layout_parse(oled_layout_splash(narrow), &layout, &error) && layout.narrow == bool(narrow));
        assert(oled_layout_parse(oled_layout_default(narrow), &layout, &error));
        for (int bits = 0; bits < 256; bits++) {
            OledValues v;
            v.eth = bits & 1; v.wifi = bits & 2; v.hdmi = bits & 4; v.hid = bits & 8;
            v.stream = bits & 16; v.wifi_module = bits & 32; v.atx = bits & 64; v.page = bits >> 7;
            v.ip = v.eth_ip = v.wifi_ip = "192.168.100.200"; v.hostname = std::string(40, 'h');
            for (unsigned step = 0; step < 4; step++) oled_layout_render(layout, v, step, frame);
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
    // Conditions joined by ",": all must hold. Pages: page{N} on page N only.
    assert(oled_layout_parse("nanokvm-oled 1 wide\npages 2 9\nline 0 0 0 0 1 eth,!wifi,page2\n", &layout, nullptr));
    assert(layout.pages == 2 && layout.page_seconds == 9);
    v = OledValues(); v.eth = true; v.page = 1;
    oled_layout_render(layout, v, 0, frame);
    assert(pixel(frame, 128, 0, 0));
    v.page = 0;
    oled_layout_render(layout, v, 0, frame);
    assert(!pixel(frame, 128, 0, 0));
    v.page = 1; v.wifi = true;
    oled_layout_render(layout, v, 0, frame);
    assert(!pixel(frame, 128, 0, 0));
    assert(parses("nanokvm-oled 1 wide\nline 0 0 0 0 1 eth,wifi,atx,!wifi_module\n"));
    assert(!parses("nanokvm-oled 1 wide\nline 0 0 0 0 1 eth,wifi,atx,hdmi,hid\n"));
    assert(!parses("nanokvm-oled 1 wide\nline 0 0 0 0 1 eth,\n"));
    assert(!parses("nanokvm-oled 1 wide\nline 0 0 0 0 1 eth,,wifi\n"));
    assert(!parses("nanokvm-oled 1 wide\npages 5 5\n"));
    assert(!parses("nanokvm-oled 1 wide\npages 2 0\n"));
    v.viewers = 3;
    assert(oled_layout_expand("{viewers:2}", v) == " 3");
    v.hostname = "kvm-1234"; v.fps = 5;

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
    // Variable widths: at most 32, written plainly; nothing a layout holds
    // makes the expanded text large.
    assert(parses("nanokvm-oled 1 wide\ntext 0 0 8 left 0 1 - {fps:32}{fps:-32}{nope:99999}\n"));
    for (const char *width : {"33", "-33", "1000000000", "-2147483648", "+3", "", "-", "3x", "003"}) {
        const std::string text = std::string("nanokvm-oled 1 wide\ntext 0 0 8 left 0 1 - {fps:") + width + "}\n";
        assert(!parses(text.c_str()));
    }
    // Given straight to the expansion, a width it would refuse is ignored.
    assert(oled_layout_expand("{fps:-2147483648}{fps:1000000000}", v) == "55");
    std::string wide_text;
    for (int i = 0; i < 4; i++) wide_text += "{hostname:32}";
    v.hostname = std::string(64, 'h');
    assert(oled_layout_expand(wide_text + wide_text + wide_text, v).size() == OLED_LAYOUT_MAX_EXPANDED);
    v.hostname = "kvm-1234";

    // The first usable source applies: a broken or other-panel preview falls
    // through to the saved layout, then to the built-in one.
    {
        const char *saved = "nanokvm-oled 1 wide\ntext 0 0 8 left 0 1 - SAVED\n";
        const char *garbage = "garbage", *narrow_preview = "nanokvm-oled 1 narrow\n";
        OledLayout picked;
        std::vector<std::pair<int, std::string>> errors;
        const char *order1[] = {garbage, saved};
        assert(oled_layout_pick(order1, 2, false, &picked, &errors) == 1 && picked.items[0].text == "SAVED");
        assert(errors.size() == 1 && errors[0].first == 0);
        const char *order2[] = {narrow_preview, saved};
        assert(oled_layout_pick(order2, 2, false, &picked, nullptr) == 1);
        const char *order3[] = {nullptr, garbage};
        assert(oled_layout_pick(order3, 2, false, &picked, nullptr) == -1 && picked.burnin == 's');
    }

    std::string many = "nanokvm-oled 1 wide\n";
    for (int i = 0; i < 128; i++) many += "line 0 0 1 1 1 -\n";
    assert(parses(many.c_str()));
    many += "line 0 0 1 1 1 -\n";
    assert(!parses(many.c_str()));
    puts("PASS oled layouts: built-in layouts; parsing limits; drawing; conditions and pages");
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
