#include <assert.h>
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdarg.h>
#include <string.h>
#include <sys/types.h>
#include <sys/ioctl.h>
#include <unistd.h>
#include <fcntl.h>
#include <time.h>
#include <sys/wait.h>
#include <linux/i2c.h>
#include <linux/i2c-dev.h>
static unsigned calls, fail_at, closes;
static int short_count = -1, reset_failure;
static unsigned sleeps;
static int sleep_mode;
static unsigned expected_sleep_ms = 250;
static int test_open(const char *path, int flags, ...);
static ssize_t test_write(int fd, const void *data, size_t len);
static off_t test_lseek(int fd, off_t offset, int whence);
static int test_nanosleep(const struct timespec *delay, struct timespec *remaining);
static int test_ioctl(int fd, unsigned long request, ...);
static int test_close(int fd);
static void test_perror(const char *text) { (void)text; }
#define open test_open
#define write test_write
#define lseek test_lseek
#define nanosleep test_nanosleep
#define ioctl test_ioctl
#define close test_close
#define perror test_perror
#define main edid_program_main
#include "../support/sg2002/nanokvm_update_edid/nanokvm_update_edid.c"
#undef main
#undef perror
#undef close
#undef ioctl
#undef open
#undef write
#undef lseek
#undef nanosleep
static int test_ioctl(int fd, unsigned long request, ...) {
    (void)fd;
    va_list args; va_start(args, request);
    assert(request == I2C_RDWR);
    struct i2c_rdwr_ioctl_data *t = va_arg(args, struct i2c_rdwr_ioctl_data *);
    va_end(args);
    assert(t->nmsgs == 2 || t->nmsgs == 3);
    assert(t->msgs[0].addr == I2C_ADDRESS && t->msgs[0].flags == 0 &&
        t->msgs[0].len == 2 && t->msgs[0].buf[0] == 0xff);
    if (t->nmsgs == 3) {
        assert(t->msgs[1].len == 1 && t->msgs[1].flags == 0 && t->msgs[2].flags == I2C_M_RD);
        memset(t->msgs[2].buf, 0x55, t->msgs[2].len);
    }
    ++calls;
    if (calls == fail_at) {
        errno = ENXIO;
        return short_count < 0 ? -1 : short_count;
    }
    return t->nmsgs;
}
static int test_close(int fd) { (void)fd; ++closes; return 0; }
static int test_open(const char *path, int flags, ...) {
    (void)flags;
    if (strcmp(path, "/sys/bus/platform/devices/hdmi-reset/pulse") == 0) return 8;
    return open(path, flags, 0600); // real lock-file IO in contention tests
}
static ssize_t test_write(int fd, const void *data, size_t len) {
    assert(fd == 8 && len == 1 && *(const char *)data == '1');
    if (reset_failure) { errno = EREMOTEIO; return -1; }
    return 1;
}
static off_t test_lseek(int fd, off_t offset, int whence) {
    assert(fd == 8 && offset == 0 && whence == SEEK_SET); return 0;
}
static int test_nanosleep(const struct timespec *delay, struct timespec *remaining) {
    if (delay->tv_sec == 0 && delay->tv_nsec == 2000000L)
        return nanosleep(delay, remaining); // real lock polling, not reset holdoff
    assert(delay->tv_nsec >= 0 && delay->tv_nsec < 1000000000L);
    ++sleeps;
    if (sleep_mode == 1 && sleeps == 1) {
        assert(delay->tv_sec * 1000 + delay->tv_nsec / 1000000 == expected_sleep_ms);
        remaining->tv_sec = 0; remaining->tv_nsec = 100000000L;
        errno = EINTR; return -1;
    }
    assert(delay->tv_sec * 1000 + delay->tv_nsec / 1000000 ==
        (sleep_mode == 1 ? 100U : expected_sleep_ms));
    if (sleep_mode == 2) { errno = EINVAL; return -1; }
    return 0;
}

static int invoke(int which) {
    uint8_t data[8] = {0};
    switch (which) {
    case 0: return i2c_write_byte(0x80, 0x30, 0x11);
    case 1: return i2c_write_bytes(0x80, 0x30, data, sizeof(data));
    case 2: return i2c_read_byte(0x80, 0x30, data);
    default: return i2c_read_bytes(0x80, 0x30, data, sizeof(data));
    }
}
static void reset(void) { calls = fail_at = closes = 0; client = 9; short_count = -1; }
static long elapsed_ms(const struct timespec *start) {
    struct timespec now; assert(clock_gettime(CLOCK_MONOTONIC, &now) == 0);
    return (now.tv_sec - start->tv_sec) * 1000 + (now.tv_nsec - start->tv_nsec) / 1000000;
}
static void maintenance_waits(void) {
    int owner = nanokvm_receiver_lock(0); assert(owner >= 0);
    int ready[2]; assert(pipe(ready) == 0);
    pid_t child = fork(); assert(child >= 0);
    if (child == 0) {
        close(owner); close(ready[0]);
        assert(write(ready[1], "R", 1) == 1);
        struct timespec start; assert(clock_gettime(CLOCK_MONOTONIC, &start) == 0);
        int fd = maintenance_lock();
        _exit(fd >= 0 && elapsed_ms(&start) >= 20 && elapsed_ms(&start) < 1800 ? 0 : 1);
    }
    close(ready[1]); char marker;
    assert(read(ready[0], &marker, 1) == 1); close(ready[0]);
    usleep(50000);
    nanokvm_receiver_unlock(owner); close(owner);
    int status; assert(waitpid(child, &status, 0) == child);
    assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);
    owner = nanokvm_receiver_lock(0); assert(owner >= 0);
    child = fork(); assert(child >= 0);
    if (child == 0) {
        close(owner);
        struct timespec start; assert(clock_gettime(CLOCK_MONOTONIC, &start) == 0);
        int fd = maintenance_lock();
        long waited = elapsed_ms(&start);
        _exit(fd < 0 && errno == EBUSY && waited >= 1800 && waited < 4000 ? 0 : 1);
    }
    assert(waitpid(child, &status, 0) == child);
    assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);
    nanokvm_receiver_unlock(owner); close(owner);
    puts("PASS production EDID maintenance lock: short owner drains, long owner times out within2s");
}
int main(void) {
    maintenance_waits();
    for (int which = 0; which < 4; ++which) {
        reset(); assert(invoke(which) == 0 && calls == 1);
        assert(invoke(which) == 0 && calls == 2); // every ioctl includes bank
        int messages = which < 2 ? 2 : 3;
        for (int partial = -1; partial < messages; ++partial) {
            reset(); fail_at = 1; short_count = partial;
            assert(invoke(which) == -1 && calls == 1);
            assert(errno == (partial < 0 ? ENXIO : EIO));
            fail_at = 0; assert(invoke(which) == 0 && calls == 2);
        }
    }
    reset();
    uint8_t whole[256] = {0};
    assert(i2c_write_bytes(0x80, 0x30, whole, sizeof(whole)) == 0);
    assert(i2c_read_bytes(0x80, 0x30, whole, sizeof(whole)) == 0);
    assert(whole[0] == 0x55 && whole[255] == 0x55);
    unsigned before_invalid = calls;
    assert(i2c_read_bytes(0x80, 0x30, whole, 257) == -1 && errno == EINVAL);
    assert(i2c_write_bytes(0x80, 0x30, whole, 257) == -1 && errno == EINVAL);
    assert(i2c_write_bytes(0x80, 0x30, NULL, 1) == -1 && errno == EINVAL);
    assert(i2c_read_bytes(0x80, 0x30, whole, 0) == -1 && errno == EINVAL);
    assert(calls == before_invalid);
    reset(); fail_at = 1;
    uint8_t out[8]; memset(out, 0xa5, sizeof(out));
    assert(i2c_read_bytes(0x80, 0x30, out, sizeof(out)) == -1);
    for (unsigned i = 0; i < sizeof(out); ++i) assert(out[i] == 0xa5);
    reset(); assert(finish_receiver(CHIP_LT6911UXC, 0) == 0 && closes == 1 && client == -1);
    reset(); fail_at = 1;
    assert(finish_receiver(CHIP_LT6911UXC, 0) == -1 && closes == 1 && client == -1);
    reset(); fail_at = 1; errno = EREMOTEIO;
    assert(finish_receiver(CHIP_LT6911UXC, -1) == -1 && errno == EREMOTEIO && closes == 1);
    reset(); fail_at = 1;
    uint8_t edid[EDID_BUFFER_SIZE];
    assert(lt6911uxc_edid_read(edid, sizeof(edid)) == -1 && calls == 1);
    reset(); sleeps = 0;
    assert(NanoKVM_PCIe_HDMI_Reset(CHIP_LT6911UXC) == 0 && sleeps == 1 && calls == 0);
    reset_failure = 1;
    assert(NanoKVM_PCIe_HDMI_Reset(CHIP_LT6911UXC) == -1 && errno == EREMOTEIO && sleeps == 1);
    reset_failure = 0; sleeps = 0; sleep_mode = 1;
    assert(NanoKVM_PCIe_HDMI_Reset(CHIP_LT6911UXC) == 0 && sleeps == 2 && calls == 0);
    sleep_mode = 2;
    assert(NanoKVM_PCIe_HDMI_Reset(CHIP_LT6911UXC) == -1 && errno == EINVAL);
    for (chip_version_t chip = CHIP_LT6911UXC; chip <= CHIP_LT6911D; ++chip) {
        expected_sleep_ms = chip == CHIP_LT6911UXC ? 250 : 2500;
        for (sleep_mode = 0; sleep_mode < 3; ++sleep_mode) {
            sleeps = 0; reset_failure = 0;
            assert(NanoKVM_PCIe_HDMI_Reset(chip) == (sleep_mode == 2 ? -1 : 0));
            assert(sleeps == (sleep_mode == 1 ? 2U : 1U));
            sleeps = 0; reset_failure = 1;
            assert(NanoKVM_PCIe_HDMI_Reset(chip) == -1 && errno == EREMOTEIO && sleeps == 0);
        }
    }
    puts("PASS EDID reset: per-model normalized waits, EINTR resume, sleep error and no wait after reset failure");
    puts("PASS four real EDID helpers: one banked ioctl each, every short count, negative errors, unchanged failed read, cleanup propagation, enable refusal");
}
