#pragma once
#include <cstddef>
#include <cstdint>
namespace nanokvm {
// Only the Linux master operations used by the HDMI bridge. No bus scan or
// hardware access during construction. A caller owns the whole bank sequence.
class I2c {
public:
    constexpr explicit I2c(int bus) : bus_(bus) {}
    ~I2c();
    I2c(const I2c &) = delete;
    I2c &operator=(const I2c &) = delete;
    bool writeto(int address, const uint8_t *bytes, size_t length);
    bool readfrom(int address, uint8_t *bytes, size_t length);
private:
    bool select(int address);
    int bus_, fd_ = -1;
};
}
