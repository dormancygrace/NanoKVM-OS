#pragma once
/* Banked LT6911 transfers: one adapter transaction, including bank selection.
 * No transfer is replayed: a failed write may already have reached flash. */
#include <errno.h>
#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include <sys/ioctl.h>
#include <linux/i2c.h>
#include <linux/i2c-dev.h>

static inline int nanokvm_i2c_capable(int fd)
{
    unsigned long funcs = 0;
    if (ioctl(fd, I2C_FUNCS, &funcs) < 0) return -1;
    if (!(funcs & I2C_FUNC_I2C)) { errno = EOPNOTSUPP; return -1; }
    return 0;
}
static inline int nanokvm_i2c_transfer(int fd, struct i2c_msg *msgs, unsigned count)
{
    struct i2c_rdwr_ioctl_data transfer = {msgs, count};
    int result = ioctl(fd, I2C_RDWR, &transfer);
    if (result == (int)count) return 0;
    if (result >= 0) errno = EIO;
    return -1;
}
static inline int nanokvm_i2c_read(int fd, uint8_t address, uint8_t bank,
    uint8_t reg, uint8_t *data, size_t len)
{
    /* Register maps and EDID FIFO reads fit in one 256-byte bank. Stage data:
     * a failed/short ioctl must never publish partially overwritten values. */
    if (!data || !len || len > 256 || address > 0x7f) { errno = EINVAL; return -1; }
    uint8_t page[2] = {0xff, bank}, result[256];
    struct i2c_msg msgs[3] = {
        {address, 0, 2, page},
        {address, 0, 1, &reg},
        {address, I2C_M_RD, (uint16_t)len, result}
    };
    if (nanokvm_i2c_transfer(fd, msgs, 3) < 0) return -1;
    memcpy(data, result, len);
    return 0;
}
static inline int nanokvm_i2c_write(int fd, uint8_t address, uint8_t bank,
    uint8_t reg, const uint8_t *data, size_t len)
{
    if (!data || !len || len > 256 || address > 0x7f) { errno = EINVAL; return -1; }
    uint8_t page[2] = {0xff, bank}, payload[257];
    payload[0] = reg;
    memcpy(payload + 1, data, len);
    struct i2c_msg msgs[2] = {
        {address, 0, 2, page},
        {address, 0, (uint16_t)(len + 1), payload}
    };
    return nanokvm_i2c_transfer(fd, msgs, 2);
}
