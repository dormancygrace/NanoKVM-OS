#!/usr/bin/env python3
"""OLED writes: only bytes the display RAM does not hold yet go out, in one
I2C transfer per run of commands or data, and the display RAM ends up as with
every byte sent. The bus is emulated from the transfers the production
oled_ctrl.cpp makes."""
from pathlib import Path
import subprocess, tempfile
repo=Path(__file__).resolve().parents[1]
main=repo/'support/sg2002/kvm_system/main'
with tempfile.TemporaryDirectory() as directory:
    d=Path(directory)
    (d/'test.cpp').write_text(r"""
#include "oled_ctrl.h"
#include "kvm_platform.h"
#include <assert.h>
#include <mutex>
#include <thread>
#include <vector>
// The controller: page addressing, as the display is initialized.
struct Display {
    uint8_t ram[16][256] = {};
    int page = 0, column = 0;
    size_t transfers = 0, bytes = 0;
    void transfer(const uint8_t *data, int len) {
        transfers++;
        bytes += len;
        for (int i = 1; i < len; i++) {
            const uint8_t b = data[i];
            if (data[0] == OLED_DATA) {
                ram[page][column] = b;
                column = column + 1 < 128 ? column + 1 : 0;
            } else if (b >= 0xb0 && b <= 0xbf) page = b - 0xb0;
            else if (b >= 0x10 && b <= 0x1f) column = (column & 0x0f) | (b & 0x0f) << 4;
            else if (b <= 0x0f) column = (column & 0xf0) | b;
            else assert(!"unexpected command while drawing");
        }
    }
};
static Display display;
static std::mutex bus;
static bool fail_next;
kvms::I2C::~I2C() {}
bool kvms::I2C::answers(int) { return true; }
int kvms::I2C::writeto(int addr, const uint8_t *data, int len)
{
    assert(addr > 0 && addr < 0x80 && len >= 2 && len <= 129);
    assert(data[0] == OLED_CMD || data[0] == OLED_DATA);
    std::lock_guard<std::mutex> lock(bus);
    if (fail_next) {
        fail_next = false;
        return -1;
    }
    display.transfer(data, len);
    return len;
}
bool OLED_IPWindowActive(void) { return false; }
void OLED_ShowChar(uint8_t x, uint8_t y, char chr, uint8_t sizey);

// Draws once with the RAM copy, once with everything sent; same RAM.
template <typename Draw> static void same_as_full(Draw draw)
{
    draw();
    Display shadowed = display;
    OLED_ForgetRAM();
    draw();
    assert(memcmp(shadowed.ram, display.ram, sizeof(display.ram)) == 0);
}

int main()
{
    assert(oled_exist() == 1);
    OLED_state = 1;
    kvm_hw_ver = 0;

    // A first clear sends every byte: per page, its position and 128 bytes.
    OLED_ForgetRAM();
    display = Display();
    memset(display.ram, 0x5a, sizeof(display.ram));
    OLED_Clear();
    assert(display.transfers == 16 && display.bytes == 8 * (1 + 3 + 1 + 128));
    for (int page = 0; page < 8; page++)
        for (int column = 0; column < 128; column++) assert(display.ram[page][column] == 0);

    // The same screen again: nothing goes out.
    display.transfers = 0;
    OLED_Clear();
    OLED_ShowString(10, 2, "NanoKVM", 8);
    const size_t written = display.transfers;
    OLED_ShowString(10, 2, "NanoKVM", 8);
    OLED_ShowString(10, 2, "NanoKVM", 8);
    assert(display.transfers == written);
    {
        OledBatch batch;
        OLED_ShowString(10, 2, "NanoKVM", 8);
        OLED_Clear(); // now differs: the string is cleared
    }

    // Changing one character of a line: only its differing columns go out.
    OLED_ShowString(0, 4, "FPS  60", 8);
    display.transfers = display.bytes = 0;
    OLED_ShowString(0, 4, "FPS  61", 8);
    assert(display.transfers == 2 && display.bytes < 2 + 3 + 2 + 6);

    // RAM contents match a full redraw for every kind of drawing.
    same_as_full([] { OLED_ShowString(0, 0, "ABC xyz 123", 8); });
    same_as_full([] { OLED_ShowString(0, 1, "large", 16); });
    same_as_full([] { OLED_ShowString(4, 6, "TINY text~", 4); });

    // The 4-pixel font has no lower case: letters are drawn upper case, and a
    // character no font has is a space (the index ran past the tables).
    {
        OLED_ShowString(0, 5, "wifi?p=ab", 4);
        Display lower = display;
        OLED_ShowString(0, 5, "WIFI?P=AB", 4);
        assert(memcmp(lower.ram, display.ram, sizeof(display.ram)) == 0);
        OLED_ShowChar(0, 6, '', 8);
        OLED_ShowChar(6, 6, ' ', 8);
        for (int k = 0; k < 6; k++) assert(display.ram[6][k] == display.ram[6][6 + k]);
    }
    same_as_full([] { OLED_ShowStringTurn(0, 3, "inverted", 8); });
    same_as_full([] { OLED_ShowString_AlignRight(127, 7, "right", 8); });
    same_as_full([] { OLED_ShowLogo(); OLED_Showline(); OLED_ShowKVMState(0x0f, 1); });
    same_as_full([] { OLED_Fill(); OLED_ShowString(30, 3, "on white", 8); });
    kvm_hw_ver = 2; // the narrow display: shifted pages and columns
    same_as_full([] { OLED_Clear(); OLED_ShowString(0, 0, "PCIe", 4); OLED_ShowNum(42, 3, 60, 3, 4); });
    kvm_hw_ver = 0;

    // A line drawn past column 127 wraps to column 0 of its page, as the
    // controller's cursor does; drawing at column 0 again sees those bytes.
    {
        OLED_Clear();
        OLED_ShowString(0, 6, "AB", 8);
        OLED_ShowChar(124, 6, 'Z', 8); // its last two columns land on the 'A'
        for (int k = 0; k < 6; k++) assert(display.ram[6][(124 + k) % 128] == oled_asc2_0806['Z' - ' '][k]);
        OLED_ShowString(0, 6, "AB", 8);
        for (int k = 0; k < 6; k++) assert(display.ram[6][k] == oled_asc2_0806['A' - ' '][k]);
    }
    {
        auto draw = [] {
            OledBatch batch;
            OLED_ShowString(0, 7, "AB", 8);
            OLED_ShowString(112, 7, "WRAPPED", 8);
            OLED_ShowString(0, 7, "AB", 8);
        };
        draw();
        Display shadowed = display;
        OLED_ForgetRAM();
        draw();
        assert(memcmp(shadowed.ram, display.ram, sizeof(display.ram)) == 0);
        assert(display.ram[7][0] == oled_asc2_0806['A' - ' '][0]);
    }

    // A transfer that failed is sent again on the next draw.
    OLED_ShowString(0, 3, "before", 8);
    fail_next = true;
    OLED_ShowString(0, 3, "after!", 8);
    OLED_ShowString(0, 3, "after!", 8);
    {
        Display drawn = display;
        OLED_ForgetRAM();
        OLED_ShowString(0, 3, "after!", 8);
        assert(memcmp(drawn.ram, display.ram, sizeof(display.ram)) == 0);
    }

    // Two threads drawing alternately: each character lands whole in its place.
    std::thread a([] { for (int i = 0; i < 200; i++) OLED_ShowChar(0, 0, i % 2 ? 'Y' : 'X', 8); });
    std::thread b([] { for (int i = 0; i < 200; i++) OLED_ShowChar(60, 4, i % 2 ? 'P' : 'O', 8); });
    a.join();
    b.join();
    for (int k = 0; k < 6; k++) {
        assert(display.ram[0][k] == oled_asc2_0806['Y' - ' '][k]);
        assert(display.ram[4][60 + k] == oled_asc2_0806['P' - ' '][k]);
    }
    puts("OLED writes: unchanged bytes skipped, runs in one transfer, RAM as with every byte sent PASS");
}
""")
    flags=['g++','-std=c++17','-O1','-g','-pthread','-Wall','-Wextra',
           '-fsanitize=address,undefined','-fno-omit-frame-pointer',
           '-I',str(main/'include'),'-I',str(main/'lib/oled_ctrl'),'-I',str(main/'lib/platform'),
           '-I',str(main/'lib/system_state'),'-I',str(main/'lib/oled_ui')]
    sources=[d/'test.cpp',main/'lib/oled_ctrl/oled_ctrl.cpp',main/'lib/oled_ctrl/oled_detect.cpp']
    subprocess.run(flags+[str(s) for s in sources]+['-o',str(d/'test')],check=True)
    subprocess.run([str(d/'test')],check=True)
