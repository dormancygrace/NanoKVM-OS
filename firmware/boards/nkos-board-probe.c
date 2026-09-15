// SPDX-License-Identifier: GPL-3.0-or-later
// First-boot-only probe. The detection DT leaves Wi-Fi off and A27 unclaimed.
#define _DEFAULT_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/gpio.h>
#include <linux/i2c.h>
#include <linux/i2c-dev.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <unistd.h>

static int lines = -1, reset_line = -1, memfd = -1;
static volatile uint32_t *mux;
static uint32_t saved_mux[3];
static uint32_t saved_pull[2];
static const unsigned pull_offsets[] = {0x908 / 4, 0x924 / 4};
static const unsigned offsets[] = {0x3c / 4, 0x58 / 4, 0x50 / 4};

static void cleanup(void)
{
    if (mux) {
        for (unsigned i = 0; i < 2; i++) mux[pull_offsets[i]] = saved_pull[i];
        for (unsigned i = 0; i < 3; i++) mux[offsets[i]] = saved_mux[i];
        munmap((void *)mux, 4096);
    }
    if (lines >= 0) close(lines);
    if (reset_line >= 0) close(reset_line);
    if (memfd >= 0) close(memfd);
}

static int gpiochip(void)
{
    for (int i = 0; i < 16; i++) {
        char path[64];
        snprintf(path, sizeof(path), "/dev/gpiochip%d", i);
        int fd = open(path, O_RDONLY | O_CLOEXEC);
        if (fd < 0) continue;
        struct gpiochip_info info = {0};
        if (!ioctl(fd, GPIO_GET_CHIPINFO_IOCTL, &info) &&
            !strcmp(info.label, "3020000.gpio")) return fd;
        close(fd);
    }
    errno = ENODEV;
    return -1;
}

static int beta_setup(void)
{
    int chip = gpiochip();
    if (chip < 0) return -1;
    struct gpio_v2_line_request req = {0};
    req.offsets[0] = 22;
    req.num_lines = 1;
    strcpy(req.consumer, "nkos-oled-probe-reset");
    req.config.flags = GPIO_V2_LINE_FLAG_OUTPUT;
    req.config.num_attrs = 1;
    req.config.attrs[0].attr.id = GPIO_V2_LINE_ATTR_ID_OUTPUT_VALUES;
    req.config.attrs[0].attr.values = 1;
    req.config.attrs[0].mask = 1;
    if (ioctl(chip, GPIO_V2_GET_LINE_IOCTL, &req)) { close(chip); return -1; }
    reset_line = req.fd;
    memset(&req, 0, sizeof(req));
    req.offsets[0] = 15; // SCL
    req.offsets[1] = 27; // SDA; never requested after a positive Alpha probe.
    req.num_lines = 2;
    strcpy(req.consumer, "nkos-oled-probe");
    req.config.flags = GPIO_V2_LINE_FLAG_OUTPUT | GPIO_V2_LINE_FLAG_OPEN_DRAIN;
    req.config.num_attrs = 1;
    req.config.attrs[0].attr.id = GPIO_V2_LINE_ATTR_ID_OUTPUT_VALUES;
    req.config.attrs[0].attr.values = 3;
    req.config.attrs[0].mask = 3;
    int rc = ioctl(chip, GPIO_V2_GET_LINE_IOCTL, &req);
    close(chip);
    if (rc) return -1;
    lines = req.fd;
    memfd = open("/dev/mem", O_RDWR | O_SYNC | O_CLOEXEC);
    if (memfd < 0) return -1;
    void *map = mmap(NULL, 4096, PROT_READ | PROT_WRITE, MAP_SHARED, memfd, 0x03001000);
    if (map == MAP_FAILED) return -1;
    mux = map;
    for (unsigned i = 0; i < 3; i++) { saved_mux[i] = mux[offsets[i]]; mux[offsets[i]] = 3; }
    // Lite has no OLED daughterboard pull-ups. Match pinctrl-cv18xx's pull bits.
    for (unsigned i = 0; i < 2; i++) {
        saved_pull[i] = mux[pull_offsets[i]];
        mux[pull_offsets[i]] = (saved_pull[i] & ~(1u << 3)) | (1u << 2);
    }
    usleep(20000);
    return 0;
}

static int drive(unsigned scl, unsigned sda)
{
    struct gpio_v2_line_values v = {.bits = scl | (sda << 1), .mask = 3};
    if (ioctl(lines, GPIO_V2_LINE_SET_VALUES_IOCTL, &v)) return -1;
    usleep(10);
    return 0;
}

static int sample(void)
{
    struct gpio_v2_line_values v = {.mask = 3};
    if (ioctl(lines, GPIO_V2_LINE_GET_VALUES_IOCTL, &v)) return -1;
    return (int)(v.bits & 3);
}

static int high(unsigned sda)
{
    if (drive(1, sda)) return -1;
    for (int i = 0; i < 100; i++) {
        int v = sample();
        if (v < 0) return -1;
        if (v & 1) return v;
        usleep(10);
    }
    errno = ETIMEDOUT;
    return -1;
}

static int beta_probe(unsigned addr)
{
    if (high(1) != 3) { errno = EBUSY; return -1; }
    if (drive(1, 0) || drive(0, 0)) return -1;
    unsigned byte = (addr << 1) | 1; // SMBus receive-byte, matching i2cdetect -r.
    for (int i = 7; i >= 0; i--) {
        unsigned bit = (byte >> i) & 1;
        if (drive(0, bit) || high(bit) < 0 || drive(0, bit)) return -1;
    }
    if (drive(0, 1)) return -1;
    int ack = high(1);
    if (ack < 0 || drive(0, 1)) return -1;
    int found = !(ack & 2);
    if (found) {
        for (int i = 0; i < 8; i++) {
            if (high(1) < 0 || drive(0, 1)) return -1;
        }
        if (high(1) < 0 || drive(0, 1)) return -1; // Master NACK.
    }
    if (drive(0, 0) || high(0) < 0 || drive(1, 1)) return -1;
    return found;
}

static int alpha_probe(void)
{
    int fd = open("/dev/i2c-1", O_RDWR | O_CLOEXEC);
    if (fd < 0) return -1;
    union i2c_smbus_data data;
    struct i2c_smbus_ioctl_data request = {
        .read_write = I2C_SMBUS_READ, .command = 0,
        .size = I2C_SMBUS_BYTE, .data = &data
    };
    int rc = ioctl(fd, I2C_SLAVE, 0x3d);
    if (!rc) rc = ioctl(fd, I2C_SMBUS, &request);
    int error = errno;
    close(fd);
    if (!rc) return 1;
    if (error == ENXIO || error == EREMOTEIO) return 0;
    errno = error;
    return -1;
}

static const char *select_profile(int (*alpha_fn)(void), int (*setup_fn)(void),
                                  int (*beta_fn)(unsigned))
{
    int alpha = alpha_fn();
    if (alpha < 0) return NULL;
    if (alpha) return "alpha";
    if (setup_fn()) return NULL;
    int cube = beta_fn(0x3d);
    int pcie = cube >= 0 ? beta_fn(0x3c) : -1;
    if (cube < 0 || pcie < 0 || (cube && pcie)) return NULL;
    return cube ? "beta" : pcie ? "pcie" : "lite";
}

int main(void)
{
    char board[32] = {0};
    int fd = open("/sys/firmware/devicetree/base/sipeed,board-revision", O_RDONLY);
    if (fd < 0 || read(fd, board, sizeof(board)-1) <= 0 || strcmp(board, "detect")) {
        fputs("Refusing probe outside the first-boot detection DT\n", stderr);
        if (fd >= 0) close(fd);
        return 1;
    }
    close(fd);
    atexit(cleanup);
    const char *profile = select_profile(alpha_probe, beta_setup, beta_probe);
    if (!profile) {
        fputs("Ambiguous or failed OLED bus probe\n", stderr);
        return 1;
    }
    // No display is a supported base/Lite configuration, never evidence of PCIe.
    puts(profile);
    return 0;
}
