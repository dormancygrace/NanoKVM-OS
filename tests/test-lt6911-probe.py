#!/usr/bin/env python3
"""Apply the production sensor patches and execute the complete probe."""
from functools import lru_cache
import hashlib
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parents[1]
REL = "sensor/cv182x/lontium_lt6911/lt6911_sensor_ctl.c"


REVISION = "e2824e1d07f516c41238e123fefbab71a34843bc"
SOURCE_SHA256 = "225f5b78a51f890a6f8ea3adca92e1644ed644cfdd0cf6061e4bffdaaa1ea1a8"
SOURCE_URL = f"https://raw.githubusercontent.com/sophgo/SensorSupportList/{REVISION}/{REL}"


@lru_cache(maxsize=1)
def upstream_source():
    """Use a local download or fetch the pinned vendor file; never vendor it."""
    lock = (ROOT / "platform/sources.lock").read_text().splitlines()
    sensor = next(line.split() for line in lock if line.startswith("sensors "))
    if sensor[2] != REVISION:
        raise AssertionError("sensor revision changed; refresh the probe fixture pin")
    local = os.environ.get("NANOKVM_LT6911_SOURCE")
    if local:
        data = Path(local).read_bytes()
    else:
        with urlopen(SOURCE_URL, timeout=30) as response:
            data = response.read(1024 * 1024 + 1)
    if len(data) > 1024 * 1024 or hashlib.sha256(data).hexdigest() != SOURCE_SHA256:
        raise AssertionError("LT6911 source does not match the pinned SHA-256")
    return data


def patched_sensor(directory):
    target = directory / REL
    target.parent.mkdir(parents=True)
    target.write_bytes(upstream_source())
    for patch in sorted((ROOT / "firmware/sensor/patches").glob("*.patch")):
        # Apply the actual ctl hunks, including earlier error/session patches.
        # Other sensor files have separate cross-build coverage.
        sections = re.split(r"(?=^--- a/)", patch.read_text(), flags=re.M)
        selected = [part.split("\ndiff --git ", 1)[0].split("\n-- \n", 1)[0]
                    for part in sections if part.startswith(f"--- a/{REL}\n+++ b/{REL}\n")]
        if not selected:
            continue
        proc = subprocess.run(["patch", "--batch", "--fuzz=0", "-p1", "-d", str(directory)],
                              input="".join(part.rstrip("\n") + "\n" for part in selected), text=True, capture_output=True)
        if proc.returncode:
            raise AssertionError(f"{patch.name}: {proc.stdout}{proc.stderr}")
    return target


class ProbeTests(unittest.TestCase):
    def test_pinned_fixture(self):
        # Git blob identity from the pinned, unmodified upstream file.
        fixture = upstream_source()
        self.assertEqual(hashlib.sha1(b"blob " + str(len(fixture)).encode() + b"\0" + fixture).hexdigest(),
                         "c364820b6ebae8fd5a557d13d95ac9898004b6c7")

    def test_actual_probe_and_faults(self):
        with tempfile.TemporaryDirectory(prefix="nkos-lt6911-probe-") as directory:
            tmp = Path(directory)
            ctl = patched_sensor(tmp)
            inc = tmp / "include"
            (inc / "linux").mkdir(parents=True)
            (inc / "linux/cvi_comm_video.h").write_text("")
            (inc / "lt6911_cmos_ex.h").write_text("")
            (inc / "cvi_sns_ctrl.h").write_text("""
#include <stdint.h>
typedef int VI_PIPE;
typedef unsigned char CVI_U8;
typedef unsigned int CVI_U32;
typedef union { signed char s8I2cDev; } ISP_SNS_COMMBUS_U;
#define VI_MAX_PIPE_NUM 2
#define CVI_SUCCESS 0
#define CVI_FAILURE (-1)
#define CVI_DBG_ERR 1
#define CVI_TRACE_SNS(level, ...) ((void)0)
""")
            (inc / "nanokvm_receiver_lock.h").write_text("""
#define NANOKVM_RECEIVER_CONTROL_TIMEOUT_MS 2000
static int nanokvm_receiver_lock(int ms) { assert(ms == 2000); return 7; }
static void nanokvm_receiver_unlock(int fd) { assert(fd == 7); }
""")
            code = r'''
#include <assert.h>
#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <syslog.h>
#include <sys/types.h>
#include <unistd.h>
#include <linux/i2c.h>
#include <linux/i2c-dev.h>

static const char *model;
static int writes, sleeps, opens, closes, init_error, read_error;
static unsigned bank, reg;
static char chip;
static FILE *test_fopen(const char *path, const char *mode) {
    assert(!strcmp(path, "/etc/kvm/hdmi_version") && !strcmp(mode, "r"));
    if (!model) { errno = ENOENT; return NULL; }
    FILE *f = tmpfile(); assert(f);
    fputs(model, f); rewind(f); return f;
}
int test_system(const char *command) {
    (void)command;
    writes++;
    assert(!"sensor probe must not invoke a shell or rewrite SoC pinmux");
    return -1;
}
static int test_usleep(useconds_t us) {
    assert(us == 50);
    assert(opens == 0);
    sleeps++;
    return 0;
}
static int test_open(const char *path, int flags, ...) {
    assert(!strcmp(path, "/dev/i2c-4") && flags == O_RDWR);
    assert(sleeps == 1);
    opens++;
    return init_error ? -1 : 42;
}
static int test_ioctl(int fd, unsigned long request, ...) {
    assert(fd == 42 && request == I2C_SLAVE_FORCE);
    return 0;
}
static int test_close(int fd) { assert(fd == 42); closes++; return 0; }
static ssize_t test_write(int fd, const void *data, size_t size) {
    assert(fd == 42 && sleeps == 1);
    const unsigned char *p = data;
    assert(size == 1 || size == 2);
    reg = p[0];
    if (size == 2 && reg == 0xff) bank = p[1];
    return (ssize_t)size;
}
static ssize_t test_read(int fd, void *data, size_t size) {
    assert(fd == 42 && sleeps == 1);
    if (read_error) return -1;
    unsigned char *p = data;
    memset(p, 0, size);
    if (chip == 'd') {
        assert(bank == 0xe0 && size == 2 && (reg == 0x8c || reg == 0x8e));
        p[0] = reg == 0x8c ? 0x07 : 0x04;
        p[1] = reg == 0x8c ? 0x80 : 0x38;
    } else {
        assert(size == 1);
        if (chip == 'c' && bank == 0xa0 && reg <= 1) p[0] = reg ? 0x05 : 0x16;
        if (chip == 'u' && bank == 0x81 && reg <= 1) p[0] = reg ? 0x04 : 0x17;
    }
    return (ssize_t)size;
}
static void test_syslog(int priority, const char *format, ...) {
    (void)priority; (void)format;
}
#define fopen test_fopen
#define system test_system
#define usleep test_usleep
#define open test_open
#define ioctl test_ioctl
#define close test_close
#define write test_write
#define read test_read
#define syslog test_syslog
#define NANOKVM_OS 1
#include "SENSOR_CTL"
ISP_SNS_COMMBUS_U g_aunLt6911_BusInfo[VI_MAX_PIPE_NUM] = {{4}, {-1}};

static void reset_case(const char *variant, char identity) {
    if (g_fd[0] >= 0) lt6911_i2c_exit(0);
    model = variant; chip = identity;
    writes = sleeps = opens = closes = init_error = read_error = 0;
}
int main(void) {
    const char *models[] = {"d\n", "c\n", "ux\n", "ue\n", "", "unknown", NULL};
    for (unsigned i = 0; i < sizeof(models) / sizeof(models[0]); i++) {
        char identity = i == 0 ? 'd' : i == 1 ? 'c' : 'u';
        reset_case(models[i], identity);
        assert(lt6911_probe(0) == CVI_SUCCESS);
        assert(writes == 0 && sleeps == 1 && opens == 1 && closes == 0);
        /* A repeated capture probe preserves the open I2C session. */
        sleeps = opens = 0;
        assert(lt6911_probe(0) == CVI_SUCCESS);
        assert(writes == 0 && sleeps == 1 && opens == 0 && closes == 0);
        reset_case(models[i], identity);
        init_error = 1;
        assert(lt6911_probe(0) == CVI_FAILURE);
        assert(writes == 0 && sleeps == 1 && opens == 1 && g_fd[0] == -1);
        reset_case(models[i], identity);
        read_error = 1;
        assert(lt6911_probe(0) == CVI_FAILURE);
        assert(writes == 0 && closes == 1 && g_fd[0] == -1);
        reset_case(models[i], identity);
        assert(lt6911_probe(0) == CVI_SUCCESS && writes == 0);
    }
    reset_case("c", 'x');
    assert(lt6911_probe(0) == CVI_FAILURE);
    assert(writes == 0 && closes == 1);
    puts("PASS: actual C/D/UXC probes, repeated probes, I2C failures/cleanup/retry, no shell MMIO writes");
}

'''.replace("SENSOR_CTL", str(ctl))
            source, binary = tmp / "test.c", tmp / "test"
            source.write_text(code)
            flags = ["-fsanitize=address,undefined", "-fno-omit-frame-pointer"] if os.getenv("NANOKVM_SANITIZE") else []
            subprocess.run(["cc", "-std=gnu11", "-Wall", "-Wextra", "-Werror", *flags,
                            "-I", str(inc), str(source), "-o", str(binary)], check=True)
            subprocess.run([str(binary)], check=True)


if __name__ == "__main__":
    unittest.main()
