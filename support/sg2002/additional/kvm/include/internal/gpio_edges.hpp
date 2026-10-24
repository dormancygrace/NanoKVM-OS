// SPDX-License-Identifier: GPL-3.0-or-later
#pragma once
#include <cerrno>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <linux/gpio.h>
#include <sys/ioctl.h>
#include <unistd.h>

namespace nanokvm {
struct GpioEdgeBatch {
    bool rising = false, falling = false, lost = false;
};
struct GpioEdgeSyscalls {
    static int open(const char *path) { return ::open(path, O_RDONLY | O_CLOEXEC); }
    static int control(int fd, unsigned long op, void *arg) { return ::ioctl(fd, op, arg); }
    static ssize_t read(int fd, void *data, size_t size) { return ::read(fd, data, size); }
    static void close(int fd) { ::close(fd); }
    static bool nonblocking(int fd) {
        const int flags = ::fcntl(fd, F_GETFL);
        return flags >= 0 && ::fcntl(fd, F_SETFL, flags | O_NONBLOCK) == 0;
    }
};

// Owned by the detector thread. No output, bias, polarity or pad-mux changes.
// Match the controller label: gpiochip numbers are assigned dynamically.
template<class Ops = GpioEdgeSyscalls> class GpioEdges {
    int fd_ = -1, error_ = 0;
    uint32_t sequence_ = 0, line_sequence_ = 0;
    static constexpr unsigned offset = 1;
    bool fail(int error) {
        if (fd_ >= 0) Ops::close(fd_);
        fd_ = -1;
        error_ = error ? error : EIO;
        return false;
    }
public:
    GpioEdges() = default;
    GpioEdges(const GpioEdges &) = delete;
    GpioEdges &operator=(const GpioEdges &) = delete;
    ~GpioEdges() { if (fd_ >= 0) Ops::close(fd_); }
    bool active() const { return fd_ >= 0; }
    int error() const { return error_; }

    bool connect() {
        if (active()) return true;
        int last_error = ENODEV;
        for (unsigned index = 0; index < 32; ++index) {
            char path[64];
            std::snprintf(path, sizeof(path), "/dev/gpiochip%u", index);
            const int chip = Ops::open(path);
            if (chip < 0) {
                if (errno != ENOENT && errno != ENODEV) last_error = errno;
                continue;
            }
            gpiochip_info info{};
            if (Ops::control(chip, GPIO_GET_CHIPINFO_IOCTL, &info) < 0) {
                last_error = errno; Ops::close(chip); continue;
            }
            if (std::strcmp(info.label, "5021000.gpio")) { Ops::close(chip); continue; }
            gpio_v2_line_info line{};
            line.offset = offset;
            if (Ops::control(chip, GPIO_V2_GET_LINEINFO_IOCTL, &line) < 0) {
                const int saved = errno; Ops::close(chip); return fail(saved);
            }
            if (line.flags & GPIO_V2_LINE_FLAG_USED) {
                Ops::close(chip); return fail(EBUSY);
            }
            gpio_v2_line_request request{};
            request.offsets[0] = offset;
            request.num_lines = 1;
            request.event_buffer_size = 64;
            std::snprintf(request.consumer, sizeof(request.consumer), "nanokvm-hdmi-int");
            request.config.flags = GPIO_V2_LINE_FLAG_INPUT |
                GPIO_V2_LINE_FLAG_EDGE_RISING | GPIO_V2_LINE_FLAG_EDGE_FALLING;
            const int status = Ops::control(chip, GPIO_V2_GET_LINE_IOCTL, &request);
            const int saved = errno;
            Ops::close(chip);
            if (status < 0) return fail(saved);
            fd_ = request.fd;
            if (!Ops::nonblocking(fd_)) return fail(errno);
            sequence_ = line_sequence_ = 0;
            error_ = 0;
            return true;
        }
        return fail(last_error);
    }

    GpioEdgeBatch drain() {
        GpioEdgeBatch result;
        if (!active()) return result;
        // Bound work even during an interrupt storm; coalesce the batch.
        for (unsigned attempt = 0; attempt < 4; ++attempt) {
            gpio_v2_line_event events[64];
            errno = 0;
            const ssize_t bytes = Ops::read(fd_, events, sizeof(events));
            if (bytes < 0) {
                if (errno == EINTR) continue;
                if (errno == EAGAIN || errno == EWOULDBLOCK) break;
                result.lost = true; fail(errno); break;
            }
            if (!bytes || size_t(bytes) > sizeof(events) || bytes % sizeof(events[0])) {
                result.lost = true; fail(EPROTO); break;
            }
            for (size_t i = 0; i < size_t(bytes) / sizeof(events[0]); ++i) {
                const auto &event = events[i];
                if (event.offset != offset || (event.id != GPIO_V2_LINE_EVENT_RISING_EDGE &&
                                               event.id != GPIO_V2_LINE_EVENT_FALLING_EDGE)) {
                    result.lost = true; fail(EPROTO); return result;
                }
                if (event.seqno != sequence_ + 1 || event.line_seqno != line_sequence_ + 1)
                    result.lost = true;
                sequence_ = event.seqno; line_sequence_ = event.line_seqno;
                result.rising |= event.id == GPIO_V2_LINE_EVENT_RISING_EDGE;
                result.falling |= event.id == GPIO_V2_LINE_EVENT_FALLING_EDGE;
            }
        }
        return result;
    }
};
} // namespace nanokvm
