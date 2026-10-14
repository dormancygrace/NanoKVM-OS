#ifndef KVM_PLATFORM_H_
#define KVM_PLATFORM_H_

// The few system services kvm_system needs: clocks, sleeping, the IPv4
// address of each interface and writes to an I2C device.

#include <cstdint>
#include <map>
#include <string>

namespace kvms {

uint64_t ticks_ms();         // CLOCK_MONOTONIC
uint64_t time_ms();          // CLOCK_REALTIME
void sleep_ms(uint64_t ms);

// Interface name -> dotted IPv4 address, without lo. An interface with
// several addresses keeps the last one getifaddrs() lists.
std::map<std::string, std::string> ip_address();

// One /dev/i2c-N, opened on first use. A missing bus is not fatal: every
// write then fails, and the caller runs without the device.
class I2C {
public:
    explicit I2C(int bus) : bus_(bus) {}
    ~I2C();
    I2C(const I2C &) = delete;
    I2C &operator=(const I2C &) = delete;
    // Returns len, or a negative errno.
    int writeto(int addr, const uint8_t *data, int len);
    // Whether a device acknowledges addr: one SMBus receive-byte, which
    // changes nothing on the OLED controller.
    bool answers(int addr);

private:
    bool open_bus();
    int bus_;
    int fd_ = -1;
    bool tried_ = false;
};

} // namespace kvms

#endif // KVM_PLATFORM_H_
