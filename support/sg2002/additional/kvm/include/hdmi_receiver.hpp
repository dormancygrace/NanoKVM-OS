#pragma once
#include <cerrno>
#include <chrono>
#include <cstdint>
#include <cstring>
#include <mutex>
#include <string>
#include <utility>
#include "hdmi_diagnostics.hpp"
#include "../../../../../firmware/mpi/nanokvm_receiver_timing.h"
#include "../../../../../firmware/mpi/nanokvm_receiver_lock.h"

namespace nanokvm {
enum class ReceiverState { Ready, NoSignal, Settling, IoError, Busy, Paused };
struct ReceiverSnapshot {
    ReceiverState state = ReceiverState::IoError;
    uint16_t width = 0, height = 0;
    int error = 0, cleanup_error = 0;
};

class ReceiverFileLock {
    int fd_;
public:
    explicit ReceiverFileLock(unsigned timeout_ms = 0,
        const char *path = NANOKVM_RECEIVER_LOCK_PATH)
        : fd_(nanokvm_receiver_lock_at(path, timeout_ms)) {}
    ~ReceiverFileLock() { nanokvm_receiver_unlock(fd_); }
    ReceiverFileLock(const ReceiverFileLock &) = delete;
    ReceiverFileLock &operator=(const ReceiverFileLock &) = delete;
    explicit operator bool() const { return fd_ >= 0; }
};

/* The detector never takes vi_mutex. Admission, pause and reset instead share
 * this leaf lock. Holding it through publication prevents a paused detector
 * from publishing a sample taken before maintenance. */
class ReceiverAccess {
    std::timed_mutex mutex_;
    bool paused_ = false;
    uint64_t generation_ = 0;
    using Clock = std::chrono::steady_clock;
    Clock::time_point ready_at_{};
    Clock::time_point (*now_)();
    std::string path_;
public:
    explicit ReceiverAccess(const char *path = NANOKVM_RECEIVER_LOCK_PATH,
        Clock::time_point (*now)() = Clock::now) : now_(now), path_(path) {}
    class Read {
        std::unique_lock<std::timed_mutex> lock_;
        int fd_;
    public:
        const ReceiverState state;
        const int error;
        const uint64_t generation;
        Read(std::unique_lock<std::timed_mutex> lock, int fd, ReceiverState status,
            int err, uint64_t current_generation)
            : lock_(std::move(lock)), fd_(fd), state(status), error(err), generation(current_generation) {}
        Read(Read &&other) noexcept : lock_(std::move(other.lock_)),
            fd_(std::exchange(other.fd_, -1)), state(other.state), error(other.error), generation(other.generation) {}
        Read(const Read &) = delete;
        ~Read() { nanokvm_receiver_unlock(fd_); }
        void release_device() {
            nanokvm_receiver_unlock(std::exchange(fd_, -1));
        }
        explicit operator bool() const { return state == ReceiverState::Ready; }
    };
    class Control {
        std::unique_lock<std::timed_mutex> lock_;
        bool *paused_;
        uint64_t *generation_;
        Clock::time_point *ready_at_;
        Clock::time_point (*now_)();
    public:
        Control(std::unique_lock<std::timed_mutex> lock, bool *paused, uint64_t *generation,
            Clock::time_point *ready_at, Clock::time_point (*now)())
            : lock_(std::move(lock)), paused_(paused), generation_(generation),
              ready_at_(ready_at), now_(now) {}
        explicit operator bool() const { return lock_.owns_lock(); }
        void pause() { *paused_ = true; }
        void resume(std::chrono::milliseconds settle = std::chrono::milliseconds(0)) {
            ++*generation_; // a fresh attempt even if no poll observed the pause
            *ready_at_ = now_() + settle;
            *paused_ = false;
        }
    };
    Read read() {
        std::unique_lock<std::timed_mutex> lock(mutex_, std::try_to_lock);
        if (!lock.owns_lock()) return Read(std::move(lock), -1, ReceiverState::Busy, 0, 0);
        if (paused_) return Read(std::move(lock), -1, ReceiverState::Paused, 0, generation_);
        // No device ownership or I2C before the post-reset deadline. A planned
        // holdoff is not an unsuccessful poll and must not consume IO retries.
        if (now_() < ready_at_)
            return Read(std::move(lock), -1, ReceiverState::Settling, 0, generation_);
        int fd = nanokvm_receiver_lock_at(path_.c_str(), 0);
        int err = fd < 0 ? errno : 0;
        return Read(std::move(lock), fd, fd >= 0 ? ReceiverState::Ready :
            err == EBUSY ? ReceiverState::Busy : ReceiverState::IoError, err, generation_);
    }
    Control control(std::chrono::milliseconds timeout = std::chrono::milliseconds(NANOKVM_RECEIVER_CONTROL_TIMEOUT_MS)) {
        std::unique_lock<std::timed_mutex> lock(mutex_, std::defer_lock);
        (void)lock.try_lock_for(timeout); // Control exposes owns_lock().
        return Control(std::move(lock), &paused_, &generation_, &ready_at_, now_);
    }
};

/* UXC register operations use combined bank/pointer/data transfers.
 * Session admission still covers enable, the complete sample and cleanup. */
template<class Transport> class HdmiReceiver {
    Transport &io_;
    bool combined_ = false;
    int error_ = 0;
    static constexpr int address = 0x2b;
    bool failed() { error_ = errno ? errno : EIO; return false; }
    bool write8(uint8_t page, uint8_t reg, uint8_t value) {
        errno = 0;
        return io_.write_register(address, page, reg, &value, 1, combined_) || failed();
    }
    bool read(uint8_t page, uint8_t reg, uint8_t *bytes, size_t count) {
        errno = 0;
        return io_.read_register(address, page, reg, bytes, count, combined_) || failed();
    }
    bool read_individual(uint8_t page, uint8_t reg, uint8_t *bytes, size_t count) {
        // The C/D donor drivers read telemetry one register at a time. Avoid
        // assuming a new long auto-increment burst on unqualified firmware.
        for (size_t i = 0; i < count; ++i)
            if (!read(page, uint8_t(reg + i), bytes + i, 1)) return false;
        return true;
    }
    bool read16(uint8_t page, uint8_t reg, uint16_t &value) {
        uint8_t bytes[2];
        if (!read(page, reg, bytes, sizeof(bytes))) return false;
        value = (static_cast<uint16_t>(bytes[0]) << 8) | bytes[1];
        return true;
    }
    bool read_c_timing(uint8_t bytes[26]) {
        static constexpr uint8_t offsets[] = {0, 1, 2, 3, 4, 5, 6, 14, 15,
            16, 17, 18, 19, 22, 23, 24, 25};
        for (auto i : offsets)
            if (!read(0xd2, 0x86 + i, bytes + i, 1)) return false;
        return true;
    }
    bool read_d_telemetry(uint8_t bytes[23]) {
        static constexpr uint8_t offsets[] = {0, 1, 2, 3, 4, 8, 9, 10, 11,
            12, 13, 14, 15, 18, 19, 20, 21, 22};
        for (auto i : offsets)
            if (!read(0xe0, 0x80 + i, bytes + i, 1)) return false;
        return true;
    }
    ReceiverDiagnostics c_diagnostics() {
        ReceiverDiagnostics result;
        uint8_t firmware[5]{}, timing[26]{}, after[26]{};
        uint16_t csi_width = 0, csi_height = 0;
        bool ok = write8(0x80, 0xee, 1) && read_individual(0xd2, 0x0c, firmware, sizeof(firmware)) &&
            write8(0xd2, 0x83, 0x11);
        if (ok) {
            struct timespec delay = {0, 5000000};
            while (nanosleep(&delay, &delay) < 0)
                if (errno != EINTR) { ok = failed(); break; }
        }
        ok = ok && read_c_timing(timing) &&
            read16(0xc2, 0x38, csi_width) && read16(0xc2, 0x06, csi_height) &&
            read_c_timing(after);
        const int primary = error_;
        if (!write8(0x80, 0xee, 0)) result.cleanup_error = error_;
        if (!ok || result.cleanup_error) {
            result.state = DiagnosticState::IoError;
            result.error = primary ? primary : result.cleanup_error;
            return result;
        }
        result = decode_c_diagnostics(timing, firmware, csi_width, csi_height);
        if (memcmp(timing, after, sizeof(after)) != 0)
            result.state = DiagnosticState::Invalid;
        return result;
    }
    ReceiverDiagnostics d_diagnostics() {
        ReceiverDiagnostics result;
        uint8_t data[23]{}, after[8]{}, stream = 0;
        // E0 telemetry is firmware-owned. Do not open/hold the internal bus
        // or toggle E0B0 to "repair" a stream during diagnostic reads.
        if (!read_d_telemetry(data) || !read(0xe0, 0xb0, &stream, 1) ||
            !read_individual(0xe0, 0x88, after, sizeof(after))) {
            result.state = DiagnosticState::IoError;
            result.error = error_;
            return result;
        }
        result = decode_d_diagnostics(data, stream);
        if (memcmp(data + 8, after, sizeof(after)) != 0)
            result.state = DiagnosticState::Invalid;
        return result;
    }
    ReceiverSnapshot dimensions(ReceiverKind kind) {
        ReceiverSnapshot sample;
        uint16_t width = 0, height = 0;
        const uint8_t page = kind == ReceiverKind::C ? 0xc2 :
            kind == ReceiverKind::UXC ? 0x85 : 0xe0;
        const uint8_t hreg = kind == ReceiverKind::C ? 0x38 :
            kind == ReceiverKind::UXC ? 0xea : 0x8c;
        const uint8_t vreg = kind == ReceiverKind::C ? 0x06 :
            kind == ReceiverKind::UXC ? 0xf0 : 0x8e;
        if (!read16(page, vreg, height) || !read16(page, hreg, width)) return sample;
        if (width == UINT16_MAX || height == UINT16_MAX) {
            sample.state = ReceiverState::Settling; return sample;
        }
        if (kind == ReceiverKind::D) {
            if (width > UINT16_MAX / 2) { sample.state = ReceiverState::Settling; return sample; }
            width *= 2;
        }
        sample.width = width; sample.height = height;
        sample.state = width && height ? ReceiverState::Ready : ReceiverState::Settling;
        if (kind == ReceiverKind::D && !width && !height) sample.state = ReceiverState::NoSignal;
        return sample;
    }
    ReceiverSnapshot sample(ReceiverKind kind) {
        ReceiverSnapshot result;
        if (kind == ReceiverKind::D) return dimensions(kind);
        if (kind == ReceiverKind::UXC) {
            uint8_t signal;
            if (!read(0x86, 0xa3, &signal, 1)) return result;
            if (signal == 0x88) { result.state = ReceiverState::NoSignal; return result; }
            if (signal != 0x55) { result.state = ReceiverState::Settling; return result; }
        } else {
            uint16_t width, height;
            if (!write8(0xd2, 0x83, 0x11)) return result;
            /* Existing C measurement needs 5 ms before reading active sizes. */
            const struct timespec delay = {0, 5000000};
            nanosleep(&delay, nullptr);
            if (!read16(0xd2, 0x96, height) || !read16(0xd2, 0x8b, width)) return result;
            if (!width && !height) { result.state = ReceiverState::NoSignal; return result; }
            if (!width || !height || width == UINT16_MAX || height == UINT16_MAX) {
                result.state = ReceiverState::Settling; return result; }
        }
        return dimensions(kind);
    }
public:
    explicit HdmiReceiver(Transport &io) : io_(io) {}
    ReceiverIdentity identify(ReceiverKind kind) {
        combined_ = kind == ReceiverKind::UXC;
        ReceiverIdentity result;
        if (kind == ReceiverKind::Unknown) { result.error = EINVAL; return result; }
        error_ = 0;
        const uint8_t control = kind == ReceiverKind::D ? 0xe0 : 0x80;
        const uint8_t bank = kind == ReceiverKind::C ? 0xa0 :
            kind == ReceiverKind::UXC ? 0x81 : 0xe1;
        uint8_t bytes[2]{};
        const bool ok = write8(control, 0xee, 1) && read_individual(bank, 0x00, bytes, sizeof(bytes));
        const int primary = error_;
        // D's ID window also stalls its MCU: always release E0EE, including
        // failed enable/read paths. Firmware E080..83 is never an ID.
        if (!write8(control, 0xee, 0)) result.cleanup_error = error_;
        result.error = primary ? primary : result.cleanup_error;
        if (ok && !result.cleanup_error) {
            result.chip_id = receiver_chip_id(kind, bytes);
            result.model = identify_receiver(kind, result.chip_id);
        }
        return result;
    }
    ReceiverDiagnostics diagnostics(ReceiverKind kind) {
        ReceiverDiagnostics result;
        result.expected_model = kind;
        if (kind == ReceiverKind::Unknown) return result;
        const auto id = identify(kind);
        result.chip_id = id.chip_id;
        if (id.error) {
            result.state = DiagnosticState::IoError;
            result.error = id.error;
            result.cleanup_error = id.cleanup_error;
            return result;
        }
        if (id.model != kind || id.model == ReceiverKind::Unknown) {
            result.state = DiagnosticState::Invalid;
            return result;
        }
        error_ = 0;
        if (kind == ReceiverKind::C) result = c_diagnostics();
        else if (kind == ReceiverKind::D) result = d_diagnostics();
        else {
            uint8_t identity[9]{}, timing[18]{}, clock[3]{}, version = 0, color = 0, status = 0;
            uint16_t csi_width = 0, csi_height = 0;
            bool ok = write8(0x80, 0xee, 1) && write8(0x80, 0x10, 0) &&
                read(0x86, 0xa2, identity, sizeof(identity)) &&
                read(0x86, 0x70, timing, sizeof(timing)) &&
                read(0x87, 0x50, clock, sizeof(clock)) &&
                read(0xb0, 0xa2, &version, 1) && read(0xb0, 0x92, &color, 1) &&
                read16(0x85, 0xea, csi_width) && read16(0x85, 0xf0, csi_height) &&
                read(0x86, 0xa3, &status, 1);
            const int primary = error_;
            if (!write8(0x80, 0xee, 0)) result.cleanup_error = error_;
            if (!ok || result.cleanup_error) {
                result.state = DiagnosticState::IoError;
                result.error = primary ? primary : result.cleanup_error;
            } else {
                result = decode_uxc_diagnostics(identity, timing, clock, version, color,
                    status, csi_width, csi_height);
            }
        }
        result.model = id.model;
        result.expected_model = kind;
        result.chip_id = id.chip_id;
        return result;
    }
    ReceiverSnapshot snapshot(ReceiverKind kind) {
        combined_ = kind == ReceiverKind::UXC;
        error_ = 0;
        ReceiverSnapshot result;
        const bool external_control = kind != ReceiverKind::D;
        const bool enabled = !external_control || (write8(0x80, 0xee, 1) &&
            (kind != ReceiverKind::UXC || write8(0x80, 0x10, 0)));
        if (enabled) result = sample(kind);
        const int primary = error_;
        /* Even a failed enable may have reached the chip. Always try release;
         * no dimension/state may escape as valid if release failed. */
        if (external_control && !write8(0x80, 0xee, 0)) {
            result.cleanup_error = error_;
            result.state = ReceiverState::IoError;
        }
        result.error = primary ? primary : result.cleanup_error;
        if (result.state == ReceiverState::IoError && !result.error) result.error = EIO;
        return result;
    }
};
} // namespace nanokvm
