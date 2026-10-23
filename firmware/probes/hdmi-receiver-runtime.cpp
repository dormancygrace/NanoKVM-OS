// Exclusive receiver bench probe. Stop nanokvm-app; never run beside capture.
// Optional second argument: c, d or ux (default ux for existing bench scripts).
// diagnostics: checked firmware/lanes/HDMI timings and independent CSI size.
// reset-ready: three real reset cycles, with no I2C before the deadline.
#include "hdmi_receiver.hpp"
#include "kvm_i2c.hpp"
#include <cstdio>
#include <cstring>
#include <unistd.h>
using Clock = std::chrono::steady_clock;
struct CountedBus {
    nanokvm::I2c bus{4};
    unsigned calls = 0;
    bool read_register(int a, uint8_t b, uint8_t r, uint8_t *p, size_t n, bool combined) {
        ++calls; return bus.read_register(a, b, r, p, n, combined);
    }
    bool write_register(int a, uint8_t b, uint8_t r, const uint8_t *p, size_t n, bool combined) {
        ++calls; return bus.write_register(a, b, r, p, n, combined);
    }
};
static bool reset(bool asserted) {
    FILE *f = fopen("/sys/bus/platform/devices/hdmi-reset/asserted", "w");
    if (!f) return false;
    bool ok = fputc(asserted ? '1' : '0', f) != EOF;
    return fclose(f) == 0 && ok;
}
static double ms(Clock::time_point start) {
    return std::chrono::duration<double, std::milli>(Clock::now() - start).count();
}
int main(int argc, char **argv) {
    setvbuf(stdout, nullptr, _IONBF, 0);
    if ((argc != 2 && argc != 3) ||
        (strcmp(argv[1], "diagnostics") && strcmp(argv[1], "reset-ready"))) return 2;
    auto kind = nanokvm::ReceiverKind::UXC;
    if (argc == 3) {
        if (!strcmp(argv[2], "c")) kind = nanokvm::ReceiverKind::C;
        else if (!strcmp(argv[2], "d")) kind = nanokvm::ReceiverKind::D;
        else if (strcmp(argv[2], "ux")) return 2;
    }
    const unsigned settle_ms = nanokvm::receiver_settle_ms(kind);
    nanokvm::ReceiverAccess access;
    CountedBus bus;
    nanokvm::HdmiReceiver<CountedBus> receiver(bus);
    bool ok = true;
    {
        auto lease = access.read();
        if (!lease) return 1;
        auto d = receiver.diagnostics(kind);
        puts(nanokvm::format_receiver_diagnostics(d).c_str());
        ok = d.state == nanokvm::DiagnosticState::Ready && d.model == kind &&
            d.width && d.height;
    }
    for (int cycle = 0; ok && !strcmp(argv[1], "reset-ready") && cycle < 3; ++cycle) {
        Clock::time_point released;
        {
            auto control = access.control();
            nanokvm::ReceiverFileLock owner;
            if (!control || !owner) return 1;
            control.pause();
            if (!reset(true)) { reset(false); return 1; }
            usleep(50000);
            if (!reset(false)) { reset(false); return 1; }
            released = Clock::now();
            bus.calls = 0;
            control.resume(std::chrono::milliseconds(settle_ms));
        }
        double first_io = -1, ready = -1;
        unsigned held = 0, attempts = 0;
        while (ms(released) < 12000) {
            auto lease = access.read();
            if (lease.state == nanokvm::ReceiverState::Settling) {
                if (bus.calls) return 1;
                ++held;
            } else if (lease) {
                if (first_io < 0) first_io = ms(released);
                ++attempts;
                auto sample = receiver.snapshot(kind);
                if (sample.state == nanokvm::ReceiverState::Ready) {
                    auto d = receiver.diagnostics(kind);
                    if (d.state == nanokvm::DiagnosticState::Ready) {
                        ready = ms(released);
                        puts(nanokvm::format_receiver_diagnostics(d).c_str());
                        break;
                    }
                }
            } else if (lease.state == nanokvm::ReceiverState::IoError) return 1;
            usleep(2000);
        }
        ok = held && first_io >= settle_ms && ready >= first_io;
        printf("READINESS cycle=%d holdoff_ms=%u first_io_ms=%.3f ready_ms=%.3f held=%u attempts=%u status=%s\n",
            cycle + 1, settle_ms, first_io, ready, held, attempts, ok ? "PASS" : "FAIL");
    }
    printf("RESULT status=%s\n", ok ? "PASS" : "FAIL");
    return ok ? 0 : 1;
}
