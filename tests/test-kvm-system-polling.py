#!/usr/bin/env python3
"""Run the real board-service state loop against counting doubles: the
OLED-only state is polled only while a display shows it."""
from pathlib import Path
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
main = repo / 'support/kvm_system/main'
source = (main / 'src/main.cpp').read_text()
loop = source[source.index('static bool oled_shows_state(void)'):source.index('int main(int argc, char* argv[])')]
preamble = r"""
#include "config.h"
#include <cassert>
#include <map>
#include <string>
kvm_sys_state_t kvm_sys_state;
kvm_oled_state_t kvm_oled_state;
static std::map<std::string, int> calls;
static bool ip_window;
static int rounds, wake_at;
#define COUNT(name) void name(void) { ++calls[#name]; }
COUNT(kvm_observe_oled_ip_window)
COUNT(kvm_update_eth_state)
COUNT(kvm_update_wifi_state)
COUNT(kvm_update_usb_state)
COUNT(kvm_update_hdmi_state)
COUNT(kvm_update_stream_fps)
COUNT(kvm_update_hdmi_res)
COUNT(kvm_update_stream_type)
COUNT(kvm_update_stream_qlty)
COUNT(kvm_wifi_web_config_process)
COUNT(kvm_wifi_config_process)
int get_ping_allow_state(void) { return 1; }
bool OLED_IPWindowActive(void) { return ip_window; }
namespace kvms {
void sleep_ms(uint64_t) {
    // The OLED thread wakes the display while the state thread sleeps.
    if (--rounds == wake_at) kvm_oled_state.oled_sleep_state = 0;
    if (rounds == 0) kvm_sys_state.sys_thread_running = 0;
}
}
"""
test = r"""
static void run(int display, bool sleeping, bool window, int page, int wake = -1) {
    calls.clear();
    wake_at = wake;
    kvm_sys_state.oled_thread_running = display;
    kvm_oled_state.oled_sleep_state = sleeping;
    ip_window = window;
    kvm_sys_state.page = page;
    kvm_sys_state.sys_thread_running = 1;
    rounds = 3;
    thread_sys_handle(nullptr);
}
static const char *display_state[] = {"kvm_update_eth_state", "kvm_update_usb_state", "kvm_update_hdmi_state",
    "kvm_update_stream_fps", "kvm_update_hdmi_res", "kvm_update_stream_type", "kvm_update_stream_qlty"};
static void expect_main_page(bool shown) {
    assert(calls["kvm_observe_oled_ip_window"] == 3);
    assert(calls["kvm_update_wifi_state"] == 3);
    assert(calls["kvm_wifi_web_config_process"] == 3);
    for (const char *name : display_state) assert(calls[name] == (shown ? 3 : 0));
    assert(calls["kvm_wifi_config_process"] == 0);
}
int main() {
    run(1, false, false, 0); expect_main_page(true);   // display awake
    run(1, true, false, 0);  expect_main_page(false);  // asleep or oled_sleep -1
    run(1, false, true, 0);  expect_main_page(false);  // IP window shown instead
    run(0, false, false, 0); expect_main_page(false);  // no panel
    run(-1, false, false, 0); expect_main_page(false); // display thread ended
    // Woken after the first round: the next rounds poll it again.
    run(1, true, false, 0, 2);
    assert(calls["kvm_update_wifi_state"] == 3);
    for (const char *name : display_state) assert(calls[name] == 2);
    // The Wi-Fi configuration page is unchanged.
    run(1, true, false, 1);
    assert(calls["kvm_wifi_config_process"] == 3 && calls["kvm_update_wifi_state"] == 0);
    puts("kvm_system state polling: awake, asleep, IP window, no panel, wake, Wi-Fi page pass");
}
"""
with tempfile.TemporaryDirectory(prefix='nkos-system-polling-') as directory:
    out = Path(directory)
    (out / 'test.cpp').write_text(preamble + loop + test)
    includes = [main / 'include', repo / 'support/additional/kvm/include']
    includes += sorted(p for p in (main / 'lib').iterdir() if p.is_dir())
    subprocess.run(['g++', '-std=c++17', '-O1', '-g', '-pthread',
                    '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-no-pie',
                    *['-I' + str(p) for p in includes], str(out / 'test.cpp'),
                    '-o', str(out / 'test')], check=True)
    subprocess.run([str(out / 'test')], check=True, timeout=10)
