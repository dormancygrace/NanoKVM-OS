#include <assert.h>
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <sys/types.h>
#include <unistd.h>
#include <sys/wait.h>
#include <time.h>
static unsigned calls, fail_at, bank_writes, closes;
static int short_transfer;
static uint8_t hardware_bank = 0x85;
static ssize_t test_write(int fd, const void *data, size_t len);
static ssize_t test_read(int fd, void *data, size_t len);
static int test_close(int fd);
static void test_perror(const char *text) { (void)text; }
#define write test_write
#define read test_read
#define close test_close
#define perror test_perror
#define main edid_program_main
#include "../support/sg2002/nanokvm_update_edid/nanokvm_update_edid.c"
#undef main
#undef perror
#undef close
#undef read
#undef write

static int failure(size_t len, ssize_t *result) {
    ++calls;
    if (calls != fail_at) return 0;
    errno = ENXIO;
    *result = short_transfer ? (ssize_t)len - 1 : -1;
    return 1;
}
static ssize_t test_write(int fd, const void *data, size_t len) {
    (void)fd;
    const uint8_t *bytes = data;
    ssize_t ret;
    if (len == 2 && bytes[0] == 0xff) ++bank_writes;
    if (failure(len, &ret)) return ret;
    if (len == 2 && bytes[0] == 0xff) hardware_bank = bytes[1];
    return (ssize_t)len;
}
static ssize_t test_read(int fd, void *data, size_t len) {
    (void)fd;
    ssize_t ret;
    if (failure(len, &ret)) return ret;
    memset(data, 0x55, len);
    return (ssize_t)len;
}
static int test_close(int fd) { (void)fd; ++closes; return 0; }
static int invoke(int which) {
    uint8_t data[8] = {0};
    switch (which) {
    case 0: return i2c_write_byte(0x80, 0x30, 0x11);
    case 1: return i2c_write_bytes(0x80, 0x30, data, sizeof(data));
    case 2: return i2c_read_byte(0x80, 0x30, data);
    default: return i2c_read_bytes(0x80, 0x30, data, sizeof(data));
    }
}
static void reset(void) {
    calls = fail_at = bank_writes = closes = 0;
    old_offset = 0xff; hardware_bank = 0x85; client = 9; short_transfer = 0;
}
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
        reset(); assert(invoke(which) == 0);
        assert(old_offset == 0x80 && hardware_bank == 0x80 && bank_writes == 1);
        unsigned operations = calls;
        assert(invoke(which) == 0 && bank_writes == 1);
        for (unsigned op = 1; op <= operations; ++op) {
            for (int partial = 0; partial < 2; ++partial) {
                reset(); fail_at = op; short_transfer = partial;
                assert(invoke(which) == -1);
                assert(old_offset == 0xff);
                unsigned selects = bank_writes;
                fail_at = 0;
                assert(invoke(which) == 0 && bank_writes == selects + 1);
                assert(old_offset == 0x80 && hardware_bank == 0x80);
            }
        }
    }
    reset();
    assert(finish_receiver(CHIP_LT6911UXC, 0) == 0);
    assert(closes == 1 && client == -1 && old_offset == 0xff);
    reset(); fail_at = 2;
    assert(finish_receiver(CHIP_LT6911UXC, 0) == -1);
    assert(closes == 1 && client == -1 && old_offset == 0xff);
    reset(); fail_at = 2; errno = EREMOTEIO;
    assert(finish_receiver(CHIP_LT6911UXC, -1) == -1 && errno == EREMOTEIO);
    assert(closes == 1);
    // Each UXC read first selects bank, then enables external control. A failed
    // enable must stop before any flash-controller/FIFO operation.
    reset(); fail_at = 3;
    uint8_t edid[EDID_BUFFER_SIZE];
    assert(lt6911uxc_edid_read(edid, sizeof(edid)) == -1 && calls == 3);
    puts("PASS all four EDID helpers: complete/short/negative transfers, cache invalidation and reselect; cleanup failure propagation and enable refusal");
}
