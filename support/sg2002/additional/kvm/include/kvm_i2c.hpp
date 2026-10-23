#pragma once
#include <cstddef>
#include <cstdint>
namespace nanokvm {
// Each operation includes the bank and register pointer in one I2C_RDWR.
// The caller still owns the full enable/read/cleanup register session.
class I2c {
public:
    constexpr explicit I2c(int bus) : bus_(bus) {}
    ~I2c();
    I2c(const I2c &) = delete;
    I2c &operator=(const I2c &) = delete;
    bool read_register(int address, uint8_t bank, uint8_t reg, uint8_t *bytes, size_t length);
    bool write_register(int address, uint8_t bank, uint8_t reg, const uint8_t *bytes, size_t length);
private:
    bool open_bus();
    int bus_, fd_ = -1;
};
}
