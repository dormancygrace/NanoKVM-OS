#!/usr/bin/env python3
"""Choose the OLED bus, address and layout for every board with a fake I2C bus."""
from pathlib import Path
import subprocess, tempfile
repo = Path(__file__).resolve().parents[1]
lib = repo / 'support/sg2002/kvm_system/main/lib/oled_ctrl'
source = r'''
#include "oled_detect.h"
#include <assert.h>
#include <set>
#include <utility>

static std::set<std::pair<int, int>> present;
static int asked;
static bool answers(int bus, int address, void *) { ++asked; return present.count({bus, address}) != 0; }

static bool find(OledBoard board, const char *manual, OledTarget *target)
{
    asked = 0;
    return oled_detect(board, manual, answers, nullptr, target);
}

int main()
{
    OledTarget t;
    // Board identity: the device tree decides, /etc/kvm/hw is the fallback.
    assert(oled_board("lite", "beta\n") == OledBoard::Lite);
    assert(oled_board("pcie", "beta") == OledBoard::Pcie);
    assert(oled_board("detect", "pcie\n") == OledBoard::Pcie);
    assert(oled_board(nullptr, "beta\n") == OledBoard::Beta);
    assert(oled_board(nullptr, "alpha\n") == OledBoard::Alpha);
    assert(oled_board(nullptr, nullptr) == OledBoard::Alpha);
    assert(oled_board(nullptr, "") == OledBoard::Alpha);

    // Stock panels.
    present = {{1, 0x3D}};
    assert(find(OledBoard::Alpha, nullptr, &t) && t.bus == 1 && t.address == 0x3D && !t.narrow);
    present = {{5, 0x3D}};
    assert(find(OledBoard::Beta, nullptr, &t) && t.bus == 5 && t.address == 0x3D && !t.narrow && asked == 1);
    present = {{5, 0x3C}};
    assert(find(OledBoard::Pcie, nullptr, &t) && t.address == 0x3C && t.narrow && asked == 1);

    // Alpha never tries bus 5, nor 0x3C: A27 is its host reset line.
    present = {{1, 0x3C}, {5, 0x3D}};
    assert(!find(OledBoard::Alpha, nullptr, &t) && asked == 1);

    // The other address is the fallback; the layout stays the board's.
    present = {{5, 0x3C}};
    assert(find(OledBoard::Beta, nullptr, &t) && t.address == 0x3C && !t.narrow && asked == 2);
    assert(find(OledBoard::Lite, nullptr, &t) && t.address == 0x3C && !t.narrow);
    present = {{5, 0x3D}};
    assert(find(OledBoard::Pcie, nullptr, &t) && t.address == 0x3D && t.narrow && asked == 2);
    assert(find(OledBoard::Lite, nullptr, &t) && t.address == 0x3D && !t.narrow && asked == 1);

    // Nothing answers: no OLED.
    present = {};
    assert(!find(OledBoard::Lite, nullptr, &t) && asked == 2);
    assert(!find(OledBoard::Pcie, nullptr, &t));

    // /etc/kvm/oled.
    present = {{5, 0x3D}};
    assert(!find(OledBoard::Beta, "off\n", &t) && asked == 0);
    present = {{5, 0x3C}, {5, 0x3D}};
    assert(find(OledBoard::Lite, "address=0x3c\n", &t) && t.address == 0x3C && !t.narrow && asked == 1);
    present = {{5, 0x3D}};
    assert(!find(OledBoard::Lite, "address=0x3c\n", &t) && asked == 1);
    present = {{3, 0x3C}};
    assert(find(OledBoard::Lite, "# my module\nbus=3\r\naddress=60\nlayout=narrow\n", &t) &&
           t.bus == 3 && t.address == 0x3C && t.narrow);
    present = {{5, 0x3D}};
    assert(find(OledBoard::Pcie, "layout=wide", &t) && t.address == 0x3D && !t.narrow);
    // A malformed file is ignored as a whole.
    for (const char *bad : {"bus=x\n", "address=0x99\n", "layout=tall\n", "colour=red\n", "bus\n"}) {
        present = {{5, 0x3D}};
        assert(find(OledBoard::Beta, bad, &t) && t.bus == 5 && t.address == 0x3D);
    }
    assert(find(OledBoard::Beta, "", &t) && t.address == 0x3D);
    return 0;
}
'''
with tempfile.TemporaryDirectory() as directory:
    d = Path(directory)
    (d / 'test.cpp').write_text(source)
    subprocess.run(['g++', '-std=gnu++17', '-Wall', '-Wextra', '-Werror', '-fsanitize=address,undefined',
                    '-I', str(lib), str(d / 'test.cpp'), str(lib / 'oled_detect.cpp'), '-o', str(d / 'test')],
                   check=True)
    subprocess.run([str(d / 'test')], check=True)
print('OLED detection test passed')
