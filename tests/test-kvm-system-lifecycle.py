#!/usr/bin/env python3
"""Run the real board-service main against deterministic thread/device doubles."""
from pathlib import Path
import os
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
main = repo / 'support/sg2002/kvm_system/main'
source = (main / 'src/main.cpp').read_text()
entry = source[source.index('int main(int argc, char* argv[])'):]
# Only system interactions are doubled. The production startup, SIGINT handler
# and shutdown remain unchanged; nothing opens a device or executes a command.
preamble = r"""
#include "config.h"
#include <cassert>
#include <cerrno>
kvm_sys_state_t kvm_sys_state;
kvm_oled_state_t kvm_oled_state;
uint8_t OLED_state = 0, kvm_hw_ver = 0;
static int mode, joins, sleeps;
static bool display_started;
void *thread_sys_handle(void *) { return nullptr; }
void *thread_oled_handle(void *) { return nullptr; }
void *thread_key_handle(void *) { return nullptr; }
void Production_testing_patch() { assert(false); }
int oled_exist() { return mode != 0; }
uint8_t watchdog_sf_is_open() { return 0; }
int check_watchdog() { assert(false); return 0; }
__attribute__((constructor)) static void setup() {
    mode = atoi(getenv("NKOS_LIFECYCLE_MODE"));
    atexit([] {
        assert(joins == (display_started ? 1 : 0));
    });
}
static int fake_system(const char *) { return 0; }
static int fake_access(const char *, int) { return -1; }
static int fake_create(pthread_t *id, const pthread_attr_t *,
                       void *(*start)(void *), void *) {
    *id = pthread_self();
    if (start == thread_oled_handle) {
        if (mode == 1) return EAGAIN;
        display_started = true;
    }
    return 0;
}
static int fake_join(pthread_t id, void **) {
    assert(display_started && pthread_equal(id, pthread_self()));
    ++joins;
    kvm_sys_state.oled_thread_running = -1;
    return 0;
}
namespace kvms {
void sleep_ms(uint64_t ms) {
    if (++sleeps > 3) {
        fputs("FAIL: shutdown waited for an absent or already-finished OLED thread\n", stderr);
        exit(42);
    }
    if (ms == 1000) {
        raise(SIGINT);
        // A running OLED thread may finish before main reaches its cleanup.
        if (mode == 3) kvm_sys_state.oled_thread_running = -1;
    } else if (display_started && mode == 2) {
        kvm_sys_state.oled_thread_running = -1;
    }
}
}
#define system fake_system
#define access fake_access
#define pthread_create fake_create
#define pthread_join fake_join
"""
with tempfile.TemporaryDirectory(prefix='nkos-system-lifecycle-') as directory:
    out = Path(directory)
    (out / 'test.cpp').write_text(preamble + entry)
    includes = [main / 'include', repo / 'support/sg2002/additional/kvm/include']
    includes += sorted(p for p in (main / 'lib').iterdir() if p.is_dir())
    subprocess.run(['g++', '-std=c++17', '-O1', '-g', '-pthread',
                    '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-no-pie',
                    *['-I' + str(p) for p in includes], str(out / 'test.cpp'),
                    '-o', str(out / 'test')], check=True)
    for mode, label in enumerate(('no OLED', 'OLED thread creation failure',
                                  'running OLED thread', 'already finished OLED thread')):
        subprocess.run([str(out / 'test')], env={**os.environ, 'NKOS_LIFECYCLE_MODE': str(mode)},
                       check=True, timeout=5)
        print(label + ': pass')
