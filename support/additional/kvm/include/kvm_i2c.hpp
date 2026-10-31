#pragma once
#include <cstddef>
#include <cstdint>
namespace nanokvm {
// Qualified UXC operations use I2C_RDWR; C/D retain STOP-delimited transfers.
// The caller still owns the full enable/read/cleanup register session.
class I2c {
public:
    constexpr explicit I2c(int bus) : bus_(bus) {}
    ~I2c();
    I2c(const I2c &) = delete;
    I2c &operator=(const I2c &) = delete;
    bool read_register(int address, uint8_t bank, uint8_t reg, uint8_t *bytes, size_t length, bool combined = true);
    bool write_register(int address, uint8_t bank, uint8_t reg, const uint8_t *bytes, size_t length, bool combined = true);
private:
    bool select(int address, bool combined);
    bool combined_capable_ = false;
    int bus_, fd_ = -1;
    int selected_ = -1; // retain I2C_SLAVE busy-address protection per fd
};
}
