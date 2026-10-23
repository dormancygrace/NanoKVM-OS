#include "kvm_i2c.hpp"
#include <cerrno>
#include <cstdio>
#include <fcntl.h>
#include <unistd.h>
#include "../../../../../firmware/mpi/nanokvm_i2c.h"
namespace nanokvm {
I2c::~I2c() { if (fd_ >= 0) close(fd_); }
bool I2c::open_bus() {
    if (bus_ < 0) { errno = EINVAL; return false; }
    if (fd_ >= 0) return true;
    char path[64];
    snprintf(path, sizeof(path), "/dev/i2c-%d", bus_);
    fd_ = open(path, O_RDWR | O_CLOEXEC);
    if (fd_ < 0) return false;
    if (nanokvm_i2c_capable(fd_) < 0) {
        int saved = errno; close(fd_); fd_ = -1; errno = saved;
        return false;
    }
    return true;
}
bool I2c::read_register(int address, uint8_t bank, uint8_t reg,
    uint8_t *bytes, size_t length) {
    if (address < 0 || address > 0x7f) { errno = EINVAL; return false; }
    return open_bus() && nanokvm_i2c_read(fd_, address, bank, reg, bytes, length) == 0;
}
bool I2c::write_register(int address, uint8_t bank, uint8_t reg,
    const uint8_t *bytes, size_t length) {
    if (address < 0 || address > 0x7f) { errno = EINVAL; return false; }
    return open_bus() && nanokvm_i2c_write(fd_, address, bank, reg, bytes, length) == 0;
}
}
