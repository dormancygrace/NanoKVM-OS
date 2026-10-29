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
    subprocess.run(['g++', '-std=gnu++17', '-Wall', '-Wextra', '-Werror', '-fsanitize=address,undefined', '-no-pie',
                    '-I', str(lib), str(d / 'test.cpp'), str(lib / 'oled_detect.cpp'), '-o', str(d / 'test')],
                   check=True)
    subprocess.run([str(d / 'test')], check=True)
print('OLED detection test passed')

# Exercise the production startup/file reader and real I2C adapter. Only libc
# file paths and device syscalls are redirected; no host /etc or I2C is touched.
platform = lib.parent / 'platform'
ctrl = (lib / 'oled_ctrl.cpp').read_text()
startup = ctrl[ctrl.index('// Chosen by oled_exist()'):ctrl.index('// 坐标设置')]
integration = r"""
#include "oled_ctrl.h"
#include "oled_detect.h"
#include "kvm_platform.h"
#include <cassert>
#include <cstdarg>
#include <cstdio>
#include <cstring>
#include <fstream>
#include <set>
#include <utility>
#include <vector>
#include <mutex>
#include <fcntl.h>
#include <time.h>
#include <linux/i2c-dev.h>
#include <linux/i2c.h>
#include <unistd.h>

static std::string root;
static std::set<std::pair<int, int>> devices;
static std::vector<std::pair<int, int>> probes;
static int opened, closed, writes, selected, active_bus;
static bool open_fails, select_fails, read_fails;
static FILE *override_stream;
extern "C" FILE *__real_fopen(const char *, const char *);
extern "C" int __real_ferror(FILE *);
extern "C" FILE *__wrap_fopen(const char *path, const char *mode) {
    const char *name = nullptr;
    if (!strcmp(path, "/proc/device-tree/sipeed,board-revision")) name = "revision";
    else if (!strcmp(path, "/etc/kvm/hw")) name = "hw";
    else if (!strcmp(path, "/etc/kvm/oled")) name = "oled";
    assert(name);
    FILE *fp = __real_fopen((root + "/" + name).c_str(), mode);
    if (!strcmp(name, "oled")) override_stream = fp;
    return fp;
}
extern "C" int __wrap_ferror(FILE *fp) {
    return read_fails && fp == override_stream ? 1 : __real_ferror(fp);
}
extern "C" int __wrap_open(const char *path, int flags, ...) {
    ++opened;
    assert(flags == (O_RDWR | O_CLOEXEC));
    assert(sscanf(path, "/dev/i2c-%d", &active_bus) == 1);
    return open_fails ? -1 : 0; // fd 0 must be treated as valid and closed.
}
extern "C" int __wrap_close(int fd) {
    assert(fd == 0); ++closed; return 0;
}
extern "C" int __wrap_ioctl(int fd, unsigned long request, ...) {
    assert(fd == 0);
    va_list args;
    va_start(args, request);
    int result;
    if (request == I2C_SLAVE) {
        selected = va_arg(args, int);
        result = select_fails ? -1 : 0;
    } else {
        assert(request == I2C_SMBUS);
        auto *transfer = va_arg(args, struct i2c_smbus_ioctl_data *);
        assert(transfer->read_write == I2C_SMBUS_READ && transfer->command == 0 &&
               transfer->size == I2C_SMBUS_BYTE && transfer->data);
        probes.emplace_back(active_bus, selected);
        result = devices.count({active_bus, selected}) ? 0 : -1;
    }
    va_end(args);
    return result;
}
extern "C" ssize_t __wrap_write(int fd, const void *data, size_t size) {
    assert(fd == 0 && size == 2);
    const auto *bytes = static_cast<const uint8_t *>(data);
    assert(bytes[0] == 0x40 && bytes[1] == 0x55);
    ++writes;
    return size;
}
// The drawing helpers that follow the slice are not part of this test.
#pragma GCC diagnostic ignored "-Wunused-function"
""" + startup + r"""
static void put(const char *name, const std::string &data) {
    std::ofstream file(root + "/" + name, std::ios::binary);
    file.write(data.data(), data.size());
    assert(file.good());
}
static void reset() {
    delete oled_i2c;
    oled_i2c = nullptr;
    OLED_state = 0;
    opened = closed = writes = 0;
    open_fails = select_fails = read_fails = false;
    override_stream = nullptr;
    probes.clear();
    devices = {{5, 0x3c}, {5, 0x3d}};
    put("revision", std::string("lite") + '\0');
    put("hw", "beta\n");
    put("oled", "");
}
static void expect(int bus, int address, bool narrow, size_t count = 1) {
    assert(oled_exist() == 1);
    assert(opened == 1 && probes.size() == count && writes == 0);
    assert(probes.back() == std::make_pair(bus, address));
    assert((kvm_hw_ver == 2) == narrow);
    OLED_state = 1;
    oled_write_register(0x40, 0x55);
    assert(writes == 1 && selected == address && opened == 1);
}
int main(int argc, char **argv) {
    assert(argc == 2);
    root = argv[1];
    reset(); expect(5, 0x3d, false);
    reset(); put("revision", std::string("pcie") + '\0'); expect(5, 0x3c, true);
    reset(); devices = {{5, 0x3c}}; expect(5, 0x3c, false, 2);
    reset(); put("revision", "pcie"); devices = {{5, 0x3d}}; expect(5, 0x3d, true, 2);
    reset(); put("revision", "alpha"); devices = {{1, 0x3d}, {5, 0x3c}}; expect(1, 0x3d, false);
    reset(); put("revision", "alpha"); assert(!oled_exist());
    assert(probes.size() == 1 && probes[0] == std::make_pair(1, 0x3d));
    reset(); assert(unlink((root + "/revision").c_str()) == 0);
    put("hw", "pcie\n"); expect(5, 0x3c, true);
    reset(); put("revision", std::string("pcie") + '\0' + "junk"); expect(5, 0x3d, false);
    reset(); put("oled", "bus=3\naddress=0x3c\nlayout=narrow\n");
    devices = {{3, 0x3c}}; expect(3, 0x3c, true);
    reset(); put("oled", "off\n"); assert(!oled_exist() && !opened && probes.empty());
    reset(); devices.clear(); assert(!oled_exist() && opened == 1 && probes.size() == 2 && !writes);
    reset(); put("oled", "off\ninvalid=true\n"); expect(5, 0x3d, false);
    reset(); put("oled", std::string("off\n") + '\0' + "invalid=true"); expect(5, 0x3d, false);
    reset(); put("oled", std::string("off\n") + '\0'); expect(5, 0x3d, false);
    reset(); put("oled", "off\n#" + std::string(4091, 'x'));
    assert(!oled_exist() && !opened); // Exactly 4096 bytes is a complete valid file.
    reset(); put("oled", "off\n#" + std::string(4092, 'x')); expect(5, 0x3d, false);
    reset(); put("oled", "off\n"); read_fails = true; expect(5, 0x3d, false);
    reset(); assert(unlink((root + "/oled").c_str()) == 0); expect(5, 0x3d, false);
    reset(); put("hw", std::string("pcie") + '\0' + "junk"); put("revision", "unknown");
    devices = {{1, 0x3d}}; expect(1, 0x3d, false);
    reset(); open_fails = true; assert(!oled_exist() && opened == 1 && probes.empty());
    reset(); select_fails = true; assert(!oled_exist() && opened == 1 && probes.empty());
    reset();
    {
        kvms::I2C bus(5);
        assert(!bus.answers(-1) && !bus.answers(128) && !opened);
        assert(bus.answers(0x3d) && opened == 1 && !writes);
    }
    assert(closed == 1);
    puts("OLED startup/file/I2C integration passed");
}
"""
with tempfile.TemporaryDirectory(prefix='nkos-oled-integration-') as directory:
    d = Path(directory)
    (d / 'test.cpp').write_text(integration)
    subprocess.run(['g++', '-std=c++17', '-Wall', '-Wextra', '-Werror',
                    '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-no-pie',
                    '-I', str(lib), '-I', str(platform), str(d / 'test.cpp'),
                    str(lib / 'oled_detect.cpp'), str(platform / 'kvm_platform.cpp'),
                    *['-Wl,--wrap=' + name for name in ('fopen', 'ferror', 'open', 'close', 'ioctl', 'write')],
                    '-o', str(d / 'test')], check=True)
    subprocess.run([str(d / 'test'), str(d)], check=True)
