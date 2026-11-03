// stock-oled BOARD IMAGE: show one screen of the migration's recovery boot.
//
// IMAGE is a gen-stock-oled.py image (the format of gen-oled-boot-logo.py,
// which U-Boot's nkoled draws): "NKOL", version 1, a class, the I2C address,
// a reserved byte, then records of a control byte (0x00 commands, 0x40
// data), a length and that many bytes, each sent as one I2C write.
//
// The recovery DTB has no OLED bus for Beta and PCIe, so their panel is driven
// as U-Boot drives it: GPIO bit-banged I2C through /dev/mem, SCL XGPIOA[15],
// SDA XGPIOA[27], reset XGPIOA[22] (active low); open drain is the direction
// register. Alpha's panel is on i2c1, which the recovery DTB enables.
// The screen is a courtesy: any failure leaves the migration to go on.
#include <errno.h>
#include <fcntl.h>
#include <linux/i2c-dev.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

#define FMUX_BASE 0x03001000
#define GPIOA_BASE 0x03020000
#define GPIO_DR 0x00
#define GPIO_DDR 0x04
#define GPIO_EXT 0x50
#define SCL 15
#define SDA 27
#define RST 22
#define HALF_NS 2000

static volatile uint32_t *fmux, *gpioa;
static int i2c = -1;

static void wait_ns(long ns)
{
    struct timespec start, now;
    clock_gettime(CLOCK_MONOTONIC, &start);
    do
        clock_gettime(CLOCK_MONOTONIC, &now);
    while ((now.tv_sec - start.tv_sec) * 1000000000L + now.tv_nsec - start.tv_nsec < ns);
}

static void gpio_bit(unsigned reg, unsigned bit, int set)
{
    uint32_t v = gpioa[reg / 4];
    gpioa[reg / 4] = set ? v | 1u << bit : v & ~(1u << bit);
}

// Low drives the line, high releases it to the board's pull-up.
static void line(unsigned bit, int high)
{
    gpio_bit(GPIO_DDR, bit, !high);
    wait_ns(HALF_NS);
}

static void start(void)
{
    line(SDA, 1);
    line(SCL, 1);
    line(SDA, 0);
    line(SCL, 0);
}

static void stop(void)
{
    line(SDA, 0);
    line(SCL, 1);
    line(SDA, 1);
}

// True when the byte was acknowledged.
static int put(uint8_t byte)
{
    for (int i = 7; i >= 0; i--) {
        line(SDA, byte >> i & 1);
        line(SCL, 1);
        line(SCL, 0);
    }
    line(SDA, 1);
    line(SCL, 1);
    int ack = !(gpioa[GPIO_EXT / 4] & 1u << SDA);
    line(SCL, 0);
    return ack;
}

static int transfer(uint8_t address, uint8_t control, const uint8_t *data, unsigned len)
{
    if (i2c >= 0) {
        uint8_t buffer[256];
        buffer[0] = control;
        if (len)
            memcpy(buffer + 1, data, len);
        return ioctl(i2c, I2C_SLAVE, address) == 0 && write(i2c, buffer, len + 1) == (ssize_t)len + 1;
    }
    start();
    int ok = put(address << 1) && put(control);
    while (ok && len--)
        ok = put(*data++);
    stop();
    return ok;
}

static int gpio_setup(void)
{
    int fd = open("/dev/mem", O_RDWR | O_SYNC);
    if (fd < 0)
        return 0;
    void *f = mmap(NULL, 4096, PROT_READ | PROT_WRITE, MAP_SHARED, fd, FMUX_BASE);
    void *g = mmap(NULL, 4096, PROT_READ | PROT_WRITE, MAP_SHARED, fd, GPIOA_BASE);
    close(fd);
    if (f == MAP_FAILED || g == MAP_FAILED)
        return 0;
    fmux = f;
    gpioa = g;
    // Pins to GPIO (function 3) with Linux's pad settings.
    fmux[0x03c / 4] = 3;
    fmux[0x058 / 4] = 3;
    fmux[0x050 / 4] = 3;
    fmux[0x908 / 4] = 0x48;
    fmux[0x924 / 4] = 0x44;
    fmux[0x91c / 4] = 0x48;
    // Idle bus: both lines released, driving 0 once made outputs.
    gpio_bit(GPIO_DDR, SCL, 0);
    gpio_bit(GPIO_DDR, SDA, 0);
    gpio_bit(GPIO_DR, SCL, 0);
    gpio_bit(GPIO_DR, SDA, 0);
    // A reset pulse, then the controller out of reset as Linux keeps it.
    gpio_bit(GPIO_DR, RST, 0);
    gpio_bit(GPIO_DDR, RST, 1);
    wait_ns(1000000);
    gpio_bit(GPIO_DR, RST, 1);
    wait_ns(1000000);
    return 1;
}

int main(int argc, char **argv)
{
    static uint8_t image[4096];
    if (argc != 3) {
        fprintf(stderr, "usage: stock-oled alpha|beta|pcie IMAGE\n");
        return 2;
    }
    FILE *file = fopen(argv[2], "rb");
    size_t size = file ? fread(image, 1, sizeof(image), file) : 0;
    if (file)
        fclose(file);
    if (size < 8 || memcmp(image, "NKOL", 4) || image[4] != 1) {
        fprintf(stderr, "stock-oled: %s is not an image\n", argv[2]);
        return 1;
    }
    if (!strcmp(argv[1], "alpha")) {
        i2c = open("/dev/i2c-1", O_RDWR);
        if (i2c < 0) {
            fprintf(stderr, "stock-oled: /dev/i2c-1: %s\n", strerror(errno));
            return 1;
        }
    } else if (strcmp(argv[1], "beta") && strcmp(argv[1], "pcie")) {
        fprintf(stderr, "stock-oled: unknown board %s\n", argv[1]);
        return 2;
    } else if (!gpio_setup()) {
        fprintf(stderr, "stock-oled: /dev/mem: %s\n", strerror(errno));
        return 1;
    }
    // The other of the two addresses in use if this one does not answer.
    uint8_t address = image[6];
    if (!transfer(address, 0x00, NULL, 0))
        address ^= 1;
    for (size_t at = 8; at < size; at += 2 + image[at + 1]) {
        if (size - at < 2 || size - at < 2u + image[at + 1] || (image[at] != 0x00 && image[at] != 0x40)) {
            fprintf(stderr, "stock-oled: malformed record at %zu\n", at);
            return 1;
        }
        if (!transfer(address, image[at], image + at + 2, image[at + 1])) {
            fprintf(stderr, "stock-oled: no answer at 0x%02x\n", address);
            return 1;
        }
    }
    return 0;
}
