#!/usr/bin/env python3
"""Run the production USB poller with ASan/UBSan and real file I/O."""
from pathlib import Path
import os
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
s = (repo / 'support/sg2002/kvm_system/main/lib/system_state/system_state.cpp').read_text()
function = s[s.index('void kvm_update_usb_state()'):s.index('void kvm_update_hdmi_state()')]
function = function.replace('"/sys/class/udc/4340000.usb/state"', 'state_path')
prefix = r"""
#include <cassert>
#include <cstdio>
#include <cstdint>
#include <string>
#include <unistd.h>
static const char *state_path;
static struct { int usb_state, hid_state, udisk_state; } kvm_sys_state;
#define USB_HID0_LINK "hid0"
#define USB_HID1_LINK "hid1"
#define USB_HID2_LINK "hid2"
static bool path_exists(const char *) { return true; }
"""
harness = r"""
int main(int argc, char **argv) {
    assert(argc == 2);
    const std::string path = std::string(argv[1]) + "/state";
    state_path = path.c_str();
    const std::string states[] = {"configured\n", "not attached\n", "addressed\n",
        "", std::string(4096, 'c'), std::string(100000, 'n'), "suspended\n"};
    const int expected[] = {1, 0, -1, -1, 1, 0, -1};
    for (unsigned i = 0; i < sizeof(expected)/sizeof(expected[0]); ++i) {
        FILE *fp = fopen(state_path, "w"); assert(fp);
        assert(fwrite(states[i].data(), 1, states[i].size(), fp) == states[i].size());
        assert(fclose(fp) == 0);
        kvm_sys_state = {1, 1, 1};
        kvm_update_usb_state();
        assert(kvm_sys_state.usb_state == expected[i]);
        assert(kvm_sys_state.hid_state == (expected[i] == 1));
        assert(kvm_sys_state.udisk_state == (expected[i] == 1));
    }
    assert(unlink(state_path) == 0);
    kvm_update_usb_state(); // missing file
    assert(kvm_sys_state.usb_state == -1 && !kvm_sys_state.hid_state && !kvm_sys_state.udisk_state);
    state_path = argv[1]; // fopen succeeds, fgetc on a directory fails
    kvm_update_usb_state();
    assert(kvm_sys_state.usb_state == -1 && !kvm_sys_state.hid_state && !kvm_sys_state.udisk_state);
}
"""
with tempfile.TemporaryDirectory() as tmp:
    cpp = Path(tmp) / 'usb.cpp'
    cpp.write_text(prefix + function + harness)
    binary = Path(tmp) / 'usb'
    subprocess.run([os.environ.get('CXX', 'g++'), '-std=c++11', '-Wall', '-Wextra', '-Werror',
                    '-g', '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-no-pie',
                    str(cpp), '-o', str(binary)], check=True)
    subprocess.run([str(binary), tmp], check=True, timeout=20)
print('PASS: USB normal, oversized, empty, missing and read-error inputs under ASan/UBSan')
