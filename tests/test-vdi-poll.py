#!/usr/bin/env python3
# Exercise actual VDI polls with deterministic clock/register/sleep stubs.
import argparse
from pathlib import Path
import subprocess
import tempfile
parser = argparse.ArgumentParser()
parser.add_argument("--source", type=Path, required=True)
args = parser.parse_args()
source = args.source.read_text()
helper = source[source.index("struct vdi_poll_deadline {"):source.index("vdi_info_t *s_vdi_info")]
poll = source[source.index("int vdi_wait_vpu_busy("):source.index("int vdi_wait_interrupt(")]
pre = r'''
#include <assert.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
typedef uint64_t u64;
typedef uint32_t Uint32;
#define NSEC_PER_MSEC 1000000ULL
#define TRUE 1
#define FALSE 0
#define VPU_PRODUCT_CODE_REGISTER 1
#define PRODUCT_CODE_W_SERIES(x) ((x) == 1)
#define PRODUCT_CODE_NOT_W_SERIES(x) ((x) == 2)
#define W4_VCPU_CUR_PC 2
#define BIT_CUR_PC 3
#define READ_ONCE(x) (x)
#define min(a,b) ((a)<(b)?(a):(b))
#define CVI_VC_ERR(...) ((void)pc)
static u64 now_ns;
static unsigned int busy_poll_sleep_us, reads, finish, clocks, sleeps;
static bool atomic_context, irq_off;
static u64 ktime_get_ns(void) { clocks++; return now_ns; }
static bool in_atomic(void) { return atomic_context; }
static bool irqs_disabled(void) { return irq_off; }
static Uint32 vdi_read_register(unsigned long core, unsigned int reg) {
    (void)core;
    if (reg == VPU_PRODUCT_CODE_REGISTER) return 1;
    now_ns += 100;
    return ++reads < finish;
}
static Uint32 vdi_fio_read_register(unsigned long core, unsigned int reg) {
    return vdi_read_register(core, reg);
}
static void usleep_range(unsigned int low, unsigned int high) {
    (void)high; sleeps++; now_ns += (u64)low * 1000;
}
static void reset(unsigned int delay, unsigned int ready) {
    now_ns = 1000000000ULL; reads = clocks = sleeps = 0;
    busy_poll_sleep_us = delay; finish = ready;
    atomic_context = irq_off = false;
}
'''
post = r'''
int main(void) {
    reset(1000, 10000000);
    assert(vdi_wait_vpu_busy(0, 10, 4) == -1);
    assert(now_ns - 1000000000ULL <= 12000000ULL);
    assert(sleeps <= 12);
    for (unsigned int n = 1; n <= 64; n++) {
        reset(0, n);
        assert(vdi_wait_vpu_busy(0, 10, 4) == 0);
        assert(clocks == 0 && sleeps == 0);
    }
    reset(0, 10000000);
    assert(vdi_wait_vpu_busy(0, 1, 4) == -1);
    assert(now_ns - 1000000000ULL >= NSEC_PER_MSEC);
    assert(now_ns - 1000000000ULL <= NSEC_PER_MSEC + 12800);
    for (int timeout = -1; timeout <= 0; timeout++) {
        reset(1000, 200);
        assert(vdi_wait_vpu_busy(0, timeout, 4) == 0);
        assert(clocks == 0);
    }
    for (int context = 0; context < 2; context++) {
        reset(1000, 10000000);
        atomic_context = context == 0; irq_off = context == 1;
        assert(vdi_wait_vpu_busy(0, 1, 4) == -1);
        assert(sleeps == 0);
        assert(now_ns - 1000000000ULL <= NSEC_PER_MSEC + 12800);
    }
    reset(1000, 3);
    assert(vdi_wait_vpu_busy(0, 10, 4) == 0);
    assert(sleeps == 2);
    puts("PASS: fast polls, stuck hardware, sleeping deadlines, unlimited waits, atomic/IRQ-off polls");
}
'''
with tempfile.TemporaryDirectory() as temporary:
    root = Path(temporary)
    (root / "poll.c").write_text(pre + helper + poll + post)
    subprocess.run(["cc", "-std=c11", "-Wall", "-Wextra", "-Werror",
                    str(root / "poll.c"), "-o", str(root / "poll")], check=True)
    subprocess.run([str(root / "poll")], check=True, timeout=10)
