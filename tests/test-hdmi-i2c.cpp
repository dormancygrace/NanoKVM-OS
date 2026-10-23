// Exercise the real C++ transport and shared Linux ioctl helper.
#include <cassert>
#include <initializer_list>
#include <cerrno>
#include <cstdarg>
#include <cstdint>
#include <cstring>
#include <cstdio>
#include <fcntl.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <linux/i2c.h>
#include <linux/i2c-dev.h>
static unsigned opens, closes, transfers, queries;
static int result = -2, failure = 0;
static bool capable = true, query_failure = false;
static int test_open(const char *path, int flags, ...) {
    assert(strcmp(path, "/dev/i2c-4") == 0 && (flags & O_CLOEXEC)); ++opens; return 42;
}
static int test_close(int fd) { assert(fd == 42); ++closes; return 0; }
static int test_ioctl(int fd, unsigned long request, ...) {
    assert(fd == 42);
    va_list args; va_start(args, request);
    if (request == I2C_FUNCS) {
        if (query_failure) { errno = ENOTTY; va_end(args); return -1; }
        *va_arg(args, unsigned long *) = capable ? I2C_FUNC_I2C : 0;
        ++queries; va_end(args); return 0;
    }
    assert(request == I2C_RDWR);
    auto *t = va_arg(args, i2c_rdwr_ioctl_data *);
    va_end(args); ++transfers;
    assert(t->nmsgs == 2 || t->nmsgs == 3);
    assert(t->msgs[0].addr == 0x2b && t->msgs[0].flags == 0 && t->msgs[0].len == 2);
    assert(t->msgs[0].buf[0] == 0xff && t->msgs[0].buf[1] == 0x85);
    assert(t->msgs[1].addr == 0x2b && t->msgs[1].flags == 0 && t->msgs[1].buf[0] == 0xea);
    if (t->nmsgs == 3) {
        assert(t->msgs[1].len == 1 && t->msgs[2].addr == 0x2b &&
            t->msgs[2].flags == I2C_M_RD && t->msgs[2].len == 2);
        memset(t->msgs[2].buf, 0xa5, t->msgs[2].len); // even failed ioctl mutates staging
    } else {
        assert(t->msgs[1].len == 3 && t->msgs[1].buf[1] == 0x12 && t->msgs[1].buf[2] == 0x34);
    }
    if (failure) { errno = failure; return -1; }
    return result == -2 ? int(t->nmsgs) : result;
}
#define open test_open
#define close test_close
#define ioctl test_ioctl
#include "../support/sg2002/additional/kvm/src/kvm_i2c.cpp"
#undef open
#undef close
#undef ioctl
int main() {
    uint8_t bytes[2] = {0x12, 0x34};
    {
        nanokvm::I2c io(4);
        assert(!opens && !queries);
        assert(io.read_register(0x2b, 0x85, 0xea, bytes, 2) && bytes[0] == 0xa5 && bytes[1] == 0xa5);
        assert(opens == 1 && queries == 1);
        for (int short_count = 0; short_count < 3; ++short_count) {
            result = short_count; bytes[0] = bytes[1] = 0x5a; auto before = transfers;
            errno = ENOENT;
            assert(!io.read_register(0x2b, 0x85, 0xea, bytes, 2) && errno == EIO);
            assert(bytes[0] == 0x5a && bytes[1] == 0x5a && transfers == before + 1);
        }
        result = -2;
        for (int error : {ENXIO, EREMOTEIO, EINTR}) {
            failure = error; bytes[0] = bytes[1] = 0x5a; auto before = transfers;
            assert(!io.read_register(0x2b, 0x85, 0xea, bytes, 2) && errno == error);
            assert(bytes[0] == 0x5a && bytes[1] == 0x5a && transfers == before + 1);
        }
        failure = 0; bytes[0] = 0x12; bytes[1] = 0x34;
        assert(io.write_register(0x2b, 0x85, 0xea, bytes, 2));
        for (int short_count = 0; short_count < 2; ++short_count) {
            result = short_count; auto before = transfers;
            assert(!io.write_register(0x2b, 0x85, 0xea, bytes, 2) && errno == EIO);
            assert(transfers == before + 1);
        }
        result = -2; failure = EINTR; auto before = transfers;
        assert(!io.write_register(0x2b, 0x85, 0xea, bytes, 2) && errno == EINTR);
        assert(transfers == before + 1); failure = 0;
        before = transfers;
        assert(!io.read_register(0x80, 0x85, 0xea, bytes, 2) && errno == EINVAL);
        assert(!io.read_register(0x2b, 0x85, 0xea, nullptr, 2) && errno == EINVAL);
        assert(!io.read_register(0x2b, 0x85, 0xea, bytes, 0) && errno == EINVAL);
        assert(!io.read_register(0x2b, 0x85, 0xea, bytes, 257) && errno == EINVAL);
        assert(transfers == before);
    }
    assert(closes == 1);
    capable = false;
    {
        nanokvm::I2c io(4); auto before = transfers;
        assert(!io.read_register(0x2b, 0x85, 0xea, bytes, 2) && errno == EOPNOTSUPP);
        assert(transfers == before && closes == 2);
        capable = true;
        assert(io.read_register(0x2b, 0x85, 0xea, bytes, 2));
    }
    assert(closes == 3);
    query_failure = true;
    {
        nanokvm::I2c io(4);
        assert(!io.read_register(0x2b, 0x85, 0xea, bytes, 2) && errno == ENOTTY);
        assert(closes == 4);
    }
    puts("PASS real I2c/shared helper: atomic message layout, lazy capability query, short/negative/EINTR no replay, unchanged failed output, validation and unsupported adapter");
}
