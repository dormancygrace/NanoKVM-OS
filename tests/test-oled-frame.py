#!/usr/bin/env python3
"""OLED_ShowFrame on both panels, with a simulated controller: after a lost
I2C transfer the next frame is complete again."""
from pathlib import Path
import subprocess, tempfile
repo = Path(__file__).resolve().parents[1]
lib = repo / 'support/kvm_system/main/lib'
source = r'''
#include "oled_ctrl.h"
#include "kvm_platform.h"
#include <assert.h>
#include <stdlib.h>

// The controller's RAM, written in page addressing mode.
static uint8_t ram[16][128];
static int page_at, column_at;
static int fail_data = 0; // data transfers still to drop

namespace kvms {
I2C::~I2C() {}
bool I2C::answers(int) { return true; }
int I2C::writeto(int, const uint8_t *data, int len)
{
    if (data[0] == OLED_DATA && fail_data > 0) {
        --fail_data;
        return -5;
    }
    for (int i = 1; i < len; i++) {
        const uint8_t b = data[i];
        if (data[0] == OLED_DATA) {
            ram[page_at][column_at] = b;
            column_at = (column_at + 1) % 128;
        } else if (b >= 0xb0 && b <= 0xbf) page_at = b & 0x0f;
        else if (b >= 0x10 && b <= 0x1f) column_at = (column_at & 0x0f) | (b & 0x0f) << 4;
        else if (b <= 0x0f) column_at = (column_at & 0xf0) | b;
    }
    return len;
}
uint64_t ticks_ms() { return 0; }
} // namespace kvms
bool OLED_IPWindowActive(void) { return false; }

static bool shows(const uint8_t *frame, bool narrow)
{
    const int width = narrow ? 64 : 128, pages = narrow ? 4 : 8;
    for (int page = 0; page < pages; page++)
        for (int x = 0; x < width; x++)
            if (ram[page + (narrow ? 8 : 0)][x + (narrow ? 32 : 0)] != frame[page * width + x]) return false;
    return true;
}

static void show(const uint8_t *frame, bool narrow, bool all)
{
    OledBatch batch; // as the OLED thread wraps a pass: its end flushes
    OLED_ShowFrame(frame, narrow ? 64 : 128, narrow ? 4 : 8, all);
}

int main(int argc, char **)
{
    const bool narrow = argc > 1;
    assert(oled_exist());
    OLED_state = 1;
    if (narrow) kvm_hw_ver = 2;
    static uint8_t a[1024], b[1024], c[1024];
    srand(7);
    for (int i = 0; i < 1024; i++) a[i] = rand();
    for (int i = 0; i < 1024; i++) b[i] = i % 97 ? a[i] : a[i] ^ 0x5a;
    // C leaves B's first changed byte, the one lost below, as it is.
    for (int i = 0; i < 1024; i++) c[i] = i % 89 != 5 ? b[i] : b[i] ^ 0x81;
    show(a, narrow, true);
    assert(shows(a, narrow));
    fail_data = 1; // the batch's first data transfer is lost
    show(b, narrow, false);
    assert(!shows(b, narrow));
    show(c, narrow, false); // C repeats most of B: it must not trust B was shown
    assert(shows(c, narrow));
    show(c, narrow, false);
    assert(shows(c, narrow));
    return 0;
}
'''
with tempfile.TemporaryDirectory() as tmp:
    test = Path(tmp) / 'test.cpp'
    test.write_text(source)
    binary = Path(tmp) / 'test'
    subprocess.run(['c++', '-std=gnu++17', '-Wall', '-Wextra', '-Werror', '-fsanitize=address,undefined',
                    '-I', str(lib / 'oled_ctrl'), '-I', str(lib / 'platform'),
                    str(test), str(lib / 'oled_ctrl/oled_ctrl.cpp'), str(lib / 'oled_ctrl/oled_detect.cpp'),
                    '-o', str(binary)], check=True)
    for panel in ([], ['narrow']):
        subprocess.run([str(binary), *panel], check=True)
print('PASS oled frames: a lost transfer is repaired by the next frame on both panels')
