#include "kvm_platform.h"

#include <arpa/inet.h>
#include <cerrno>
#include <cstdio>
#include <ctime>
#include <fcntl.h>
#include <ifaddrs.h>
#include <linux/i2c-dev.h>
#include <linux/i2c.h>
#include <netinet/in.h>
#include <sys/ioctl.h>
#include <unistd.h>

namespace kvms {

static uint64_t clock_ms(clockid_t clock)
{
    struct timespec ts;
    clock_gettime(clock, &ts);
    return (uint64_t)ts.tv_sec * 1000 + (uint64_t)ts.tv_nsec / 1000000;
}

uint64_t ticks_ms() { return clock_ms(CLOCK_MONOTONIC); }
uint64_t time_ms() { return clock_ms(CLOCK_REALTIME); }

void sleep_ms(uint64_t ms)
{
    struct timespec ts;
    ts.tv_sec = (time_t)(ms / 1000);
    ts.tv_nsec = (long)(ms % 1000) * 1000000;
    while (nanosleep(&ts, &ts) == -1 && errno == EINTR) {
    }
}

std::map<std::string, std::string> ip_address()
{
    std::map<std::string, std::string> addresses;
    struct ifaddrs *list;
    if (getifaddrs(&list) == -1)
        return addresses;
    for (struct ifaddrs *ifa = list; ifa; ifa = ifa->ifa_next) {
        if (!ifa->ifa_addr || ifa->ifa_addr->sa_family != AF_INET || std::string(ifa->ifa_name) == "lo")
            continue;
        char text[INET_ADDRSTRLEN];
        const struct in_addr *in = &((const struct sockaddr_in *)ifa->ifa_addr)->sin_addr;
        if (inet_ntop(AF_INET, in, text, sizeof(text)))
            addresses[ifa->ifa_name] = text;
    }
    freeifaddrs(list);
    return addresses;
}

I2C::~I2C()
{
    if (fd_ >= 0)
        close(fd_);
}

bool I2C::open_bus()
{
    if (!tried_) {
        tried_ = true;
        char path[32];
        snprintf(path, sizeof(path), "/dev/i2c-%d", bus_);
        fd_ = open(path, O_RDWR | O_CLOEXEC);
    }
    return fd_ >= 0;
}

bool I2C::answers(int addr)
{
    if (addr < 0 || addr > 0x7f || !open_bus() || ioctl(fd_, I2C_SLAVE, addr) != 0)
        return false;
    union i2c_smbus_data data;
    struct i2c_smbus_ioctl_data request = {I2C_SMBUS_READ, 0, I2C_SMBUS_BYTE, &data};
    return ioctl(fd_, I2C_SMBUS, &request) == 0;
}

int I2C::writeto(int addr, const uint8_t *data, int len)
{
    if (addr < 0 || addr > 0x7f || len < 0 || (len && !data))
        return -EINVAL;
    if (!open_bus())
        return -ENODEV;
    if (ioctl(fd_, I2C_SLAVE, addr) != 0)
        return -errno;
    const ssize_t written = write(fd_, data, (size_t)len);
    if (written != len)
        return written < 0 ? -errno : -EIO;
    return len;
}

} // namespace kvms
