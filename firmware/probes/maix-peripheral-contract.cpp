// Exercise real MaixCDK I2C code without opening any hardware device.
#include "maix_i2c.hpp"
#include "maix_fs.hpp"
#include <cassert>
#include <cerrno>
#include <cstdarg>
#include <cstring>
#include <linux/i2c-dev.h>
#include <unistd.h>

static int next_fd = 0, closes = 0, ioctls = 0, reads = 0, writes = 0;
static bool fail_open = false, fail_ioctl = false, short_io = false;
static bool missing_directory = true;

namespace maix::fs {
std::vector<std::string> *listdir(const std::string &, bool, bool) {
    if (missing_directory) return nullptr;
    return new std::vector<std::string>{"i2c-5", "i2c-1", "i2c-4", "tty"};
}
}

extern "C" int __wrap_open(const char *path, int, ...) {
    assert(std::strcmp(path, "/dev/i2c-4") == 0);
    if (fail_open) { errno = ENOENT; return -1; }
    return next_fd;
}
extern "C" int __wrap_close(int fd) {
    assert(fd == next_fd);
    ++closes;
    return 0;
}
extern "C" int __wrap_ioctl(int fd, unsigned long request, ...) {
    assert(fd == next_fd && request == I2C_SLAVE);
    va_list args;
    va_start(args, request);
    int addr = va_arg(args, int);
    va_end(args);
    assert(addr == 0x2b);
    ++ioctls;
    return fail_ioctl ? -1 : 0;
}
extern "C" ssize_t __wrap_read(int fd, void *buf, size_t len) {
    assert(fd == next_fd);
    ++reads;
    std::memset(buf, 0x5a, len);
    return short_io ? static_cast<ssize_t>(len) - 1 : len;
}
extern "C" ssize_t __wrap_write(int fd, const void *buf, size_t len) {
    assert(fd == next_fd && (buf || !len));
    ++writes;
    return short_io ? static_cast<ssize_t>(len) - 1 : len;
}

int main() {
    using namespace maix;
    namespace i2c = maix::peripheral::i2c;
    assert(i2c::list_devices().empty());
    missing_directory = false;
    assert(i2c::list_devices() == std::vector<int>({1, 4, 5}));
    for (int fd : {0, 17}) {
        next_fd = fd;
        int previous_closes = closes;
        {
            i2c::I2C bus(4, i2c::Mode::MASTER);
            uint8_t bytes[] = {0x12, 0x34};
            assert(bus.writeto(0x2b, bytes, 2) == 2);
            Bytes *data = bus.readfrom(0x2b, 6);
            assert(data && data->size() == 6 && data->data[5] == 0x5a);
            delete data;
            int previous_ioctls = ioctls;
            for (int addr : {-1, 128}) {
                assert(bus.writeto(addr, bytes, 2) == -err::ERR_ARGS);
                assert(bus.readfrom(addr, 6) == nullptr);
            }
            assert(bus.writeto(0x2b, nullptr, 1) == -err::ERR_ARGS);
            assert(bus.writeto(0x2b, bytes, -1) == -err::ERR_ARGS);
            assert(bus.readfrom(0x2b, 0) == nullptr);
            assert(bus.readfrom(0x2b, -1) == nullptr);
            assert(ioctls == previous_ioctls);
            fail_ioctl = true;
            int previous_reads = reads, previous_writes = writes;
            assert(bus.readfrom(0x2b, 6) == nullptr);
            assert(bus.writeto(0x2b, bytes, 2) == -err::ERR_IO);
            assert(reads == previous_reads && writes == previous_writes);
            fail_ioctl = false;
            short_io = true;
            assert(bus.readfrom(0x2b, 6) == nullptr);
            assert(bus.writeto(0x2b, bytes, 2) == -err::ERR_IO);
            short_io = false;
        }
        assert(closes == previous_closes + 1);
    }
    fail_open = true;
    bool threw = false;
    int previous_closes = closes;
    try { i2c::I2C bus(4, i2c::Mode::MASTER); }
    catch (const err::Exception &) { threw = true; }
    assert(threw && closes == previous_closes);
    puts("I2C contracts: PASS (fd 0, errors, ownership, argument validation)");
}
