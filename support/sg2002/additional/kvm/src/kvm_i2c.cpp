#include "kvm_i2c.hpp"
#include <cerrno>
#include <cstdio>
#include <fcntl.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <linux/i2c-dev.h>
namespace nanokvm {
I2c::~I2c() { if (fd_ >= 0) close(fd_); }
bool I2c::select(int address) {
    if (address < 0 || address > 0x7f || bus_ < 0) return false;
    if (fd_ < 0) {
        char path[64];
        snprintf(path, sizeof(path), "/dev/i2c-%d", bus_);
        fd_ = open(path, O_RDWR | O_CLOEXEC);
        if (fd_ < 0) return false;
    }
    return ioctl(fd_, I2C_SLAVE, address) == 0;
}
bool I2c::writeto(int address, const uint8_t *bytes, size_t length) {
    if (!bytes || !length || length > 65535 || !select(address)) return false;
    ssize_t result;
    do { result = write(fd_, bytes, length); } while (result < 0 && errno == EINTR);
    // A short transaction must not be retried as a new register write.
    return result == static_cast<ssize_t>(length);
}
bool I2c::readfrom(int address, uint8_t *bytes, size_t length) {
    if (!bytes || !length || length > 65535 || !select(address)) return false;
    ssize_t result;
    do { result = read(fd_, bytes, length); } while (result < 0 && errno == EINTR);
    return result == static_cast<ssize_t>(length);
}
}
