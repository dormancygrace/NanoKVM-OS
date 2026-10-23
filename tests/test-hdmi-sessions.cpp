#include "hdmi_receiver.hpp"
#include "geometry_gate.hpp"
#include <atomic>
#include <cassert>
#include <cstdio>
#include <future>
#include <map>
#include <thread>
#include <vector>
#include <sys/wait.h>

using nanokvm::ReceiverKind;
using nanokvm::ReceiverState;
struct Bus {
    unsigned calls = 0, fail_at = 0, second_fail_at = 0, cleanup_attempts = 0;
    uint8_t bank = 0, pointer = 0;
    std::map<unsigned, uint8_t> registers;
    bool fail() {
        ++calls;
        if (calls == fail_at || calls == second_fail_at) {
            errno = calls == fail_at ? ENXIO : EIO;
            return true;
        }
        return false;
    }
    bool writeto(int addr, const uint8_t *p, size_t n) {
        assert(addr == 0x2b && p && n);
        if (n == 2 && p[0] == 0xee && p[1] == 0) ++cleanup_attempts;
        if (fail()) return false;
        if (n == 2 && p[0] == 0xff) bank = p[1];
        else if (n == 1) pointer = p[0];
        else { assert(n == 2); registers[(bank << 8) | p[0]] = p[1]; }
        return true;
    }
    bool readfrom(int addr, uint8_t *p, size_t n) {
        assert(addr == 0x2b && n);
        if (fail()) return false;
        for (size_t i = 0; i < n; ++i) p[i] = registers[(bank << 8) | (pointer + i)];
        return true;
    }
    void pair(unsigned reg, uint16_t value) {
        registers[reg] = value >> 8; registers[reg + 1] = value & 0xff;
    }
    static Bus fixture(ReceiverKind kind) {
        Bus io;
        if (kind == ReceiverKind::UXC) {
            io.registers[0x86a3] = 0x55; io.pair(0x85ea, 1920); io.pair(0x85f0, 1080);
        } else if (kind == ReceiverKind::C) {
            io.pair(0xd28b, 960); io.pair(0xd296, 1080);
            io.pair(0xc238, 1920); io.pair(0xc206, 1080);
        } else { io.pair(0xe08c, 960); io.pair(0xe08e, 1080); }
        return io;
    }
};

static void snapshots() {
    for (auto kind : {ReceiverKind::C, ReceiverKind::UXC, ReceiverKind::D}) {
        auto io = Bus::fixture(kind);
        nanokvm::HdmiReceiver<Bus> receiver(io);
        auto good = receiver.snapshot(kind);
        assert(good.state == ReceiverState::Ready && good.width == 1920 && good.height == 1080);
        assert(good.error == 0 && good.cleanup_error == 0);
        assert(io.registers[0x80ee] == 0);
        const auto operations = io.calls;
        for (unsigned failure = 1; failure <= operations; ++failure) {
            auto broken = Bus::fixture(kind); broken.fail_at = failure;
            nanokvm::HdmiReceiver<Bus> probe(broken);
            const auto sample = probe.snapshot(kind);
            assert(sample.state == ReceiverState::IoError && sample.error == ENXIO);
            if (kind != ReceiverKind::D) {
                // The last normal write is cleanup. All earlier faults must
                // still attempt it and leave access released on success.
                if (!sample.cleanup_error) {
                    assert(broken.cleanup_attempts > 0);
                    assert(broken.registers[0x80ee] == 0);
                } else {
                    // Cleanup can fail at its bank-selection write before EE.
                    assert(broken.calls == failure);
                    assert(sample.cleanup_error == ENXIO);
                }
            }
        }
        auto zero = Bus::fixture(kind);
        if (kind == ReceiverKind::UXC) zero.registers[0x86a3] = 0x88;
        else if (kind == ReceiverKind::C) { zero.pair(0xd28b, 0); zero.pair(0xd296, 0); }
        else { zero.pair(0xe08c, 0); zero.pair(0xe08e, 0); }
        nanokvm::HdmiReceiver<Bus> absent(zero);
        assert(absent.snapshot(kind).state == ReceiverState::NoSignal);
    }
    for (auto dims : {std::pair<int,int>{0,1080}, {960,0}, {65535,1080}}) {
        Bus io; io.pair(0xe08c, dims.first); io.pair(0xe08e, dims.second);
        nanokvm::HdmiReceiver<Bus> receiver(io);
        assert(receiver.snapshot(ReceiverKind::D).state == ReceiverState::Settling);
    }
    auto io = Bus::fixture(ReceiverKind::UXC);
    io.registers[0x86a3] = 0;
    nanokvm::HdmiReceiver<Bus> unknown(io);
    assert(unknown.snapshot(ReceiverKind::UXC).state == ReceiverState::Settling);
    auto partial = Bus::fixture(ReceiverKind::UXC); partial.pair(0x85ea, 0);
    nanokvm::HdmiReceiver<Bus> incomplete(partial);
    assert(incomplete.snapshot(ReceiverKind::UXC).state == ReceiverState::Settling);
    auto double_failure = Bus::fixture(ReceiverKind::UXC);
    // Initial bank selection fails, then release's bank selection fails.
    double_failure.fail_at = 1; double_failure.second_fail_at = 2;
    nanokvm::HdmiReceiver<Bus> cleanup(double_failure);
    auto sample = cleanup.snapshot(ReceiverKind::UXC);
    assert(sample.state == ReceiverState::IoError);
    assert(sample.error == ENXIO && sample.cleanup_error == EIO);
    puts("PASS C/UXC/D: every I2C operation fault, cleanup/original error, disconnect, settling and D partial sizes");
}

static void admission() {
    nanokvm::ReceiverAccess access;
    std::promise<void> entered, release;
    auto release_future = release.get_future();
    std::thread reader([&] {
        auto lease = access.read(); assert(lease);
        entered.set_value(); release_future.wait();
    });
    entered.get_future().wait();
    std::promise<bool> timeout;
    auto start = std::chrono::steady_clock::now();
    std::thread blocked([&] {
        auto control = access.control(std::chrono::milliseconds(30));
        timeout.set_value(static_cast<bool>(control));
    });
    assert(!timeout.get_future().get());
    blocked.join();
    assert(std::chrono::steady_clock::now() - start < std::chrono::seconds(2));
    std::promise<void> paused;
    std::thread maintainer([&] {
        auto control = access.control(); assert(control);
        control.pause(); paused.set_value();
    });
    release.set_value(); reader.join();
    paused.get_future().wait(); maintainer.join();
    { auto refused = access.read(); assert(!refused && refused.state == ReceiverState::Paused); }
    { auto control = access.control(); assert(control); control.resume(); }
    { auto resumed = access.read(); assert(resumed); }
    // A separately opened fd (even in the same process) excludes capture.
    {
        nanokvm::ReceiverFileLock external; assert(external);
        auto refused = access.read(); assert(!refused && refused.state == ReceiverState::Busy);
    }
    { auto resumed = access.read(); assert(resumed); }
    // Verify that a separate process uses the same cooperative ownership.
    {
        auto lease = access.read(); assert(lease);
        pid_t pid = fork(); assert(pid >= 0);
        if (pid == 0) {
            int fd = nanokvm_receiver_lock(0);
            _exit(fd < 0 && errno == EBUSY ? 0 : 1);
        }
        int status = 0; assert(waitpid(pid, &status, 0) == pid);
        assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);
    }
    puts("PASS receiver admission: drains active read, bounded timeout, paused rejection, resume and cross-process exclusion");
}

// Production poll helper is inserted by the runner; no register logic copied.
static nanokvm::ReceiverAccess receiver_access;
static std::atomic<bool> receiver_retry_pending{false};
static Bus LT6911_i2c = Bus::fixture(ReceiverKind::UXC);
static nanokvm::HdmiReceiver<Bus> hdmi_receiver(LT6911_i2c);
static struct {
    int hdmi_version = 1, reopen_cam_flag = 0;
    uint8_t hw_version = 'p', fresh_frame_count = 0;
} kvmv_cfg;
static int signal_active = 1, publications = 0, mode_switches = 0;
static nanokvm::GeometryGate test_geometry(1920, 1080);
static uint8_t hdmi_capture_enabled = 1;
static constexpr uint8_t fresh_frame_discard_count = 5;
static bool reset_asserted = false, reset_error = false;
static unsigned reset_calls = 0;
static void set_hdmi_capture_enabled(uint8_t enabled) {
    hdmi_capture_enabled = enabled;
    signal_active = 0;
}
static int set_owned_hdmi_reset(bool asserted) {
    ++reset_calls;
    if (reset_error) { errno = EREMOTEIO; return -1; }
    reset_asserted = asserted;
    return 0;
}
static constexpr uint8_t NEW_RES = 1, UNKNOWN_RES = 3;
static void set_hdmi_detection_state(int value) { signal_active = value; }
static uint8_t detect_csi_res(const nanokvm::ReceiverSnapshot &sample) {
    assert(sample.state == ReceiverState::Ready && sample.width && sample.height);
    nanokvm::ReceiverFileLock sensor_probe;
    assert(sensor_probe); // publication must not retain the device flock
    test_geometry.publish(sample.width, sample.height);
    ++publications; return NEW_RES;
}
static void set_hdmi_mode(int value) { assert(value == 1); ++mode_switches; }
// PRODUCTION_CONTROL
// PRODUCTION_POLL

static void controls() {
    std::promise<void> entered, release;
    auto leave = release.get_future();
    std::thread reader([&] {
        auto lease = receiver_access.read(); assert(lease);
        entered.set_value(); leave.wait();
    });
    entered.get_future().wait();
    auto pending = std::async(std::launch::async, [] { return kvmv_edid_maintenance(1); });
    assert(pending.wait_for(std::chrono::milliseconds(10)) == std::future_status::timeout);
    release.set_value(); reader.join();
    assert(pending.get() == 0 && hdmi_capture_enabled == 0);
    { auto refused = receiver_access.read(); assert(refused.state == ReceiverState::Paused); }
    assert(kvmv_edid_maintenance(1) == -1 && errno == EBUSY);
    assert(kvmv_edid_maintenance(0) == 0 && hdmi_capture_enabled == 1);
    assert(kvmv_edid_maintenance(0) == -1 && errno == EINVAL);
    {
        nanokvm::ReceiverFileLock external; assert(external);
        auto calls = reset_calls;
        const auto started = std::chrono::steady_clock::now();
        assert(kvmv_hdmi_control(0) == -1 && errno == EBUSY);
        const auto waited = std::chrono::steady_clock::now() - started;
        assert(waited >= std::chrono::milliseconds(1800) && waited < std::chrono::seconds(4));
        assert(reset_calls == calls && hdmi_capture_enabled == 1);
    }
    assert(kvmv_hdmi_control(0) == 0 && reset_asserted && !hdmi_capture_enabled);
    { auto refused = receiver_access.read(); assert(refused.state == ReceiverState::Paused); }
    reset_error = true;
    assert(kvmv_hdmi_control(1) == -1 && reset_asserted && !hdmi_capture_enabled);
    { auto refused = receiver_access.read(); assert(refused.state == ReceiverState::Paused); }
    reset_error = false;
    assert(kvmv_hdmi_control(1) == 0 && !reset_asserted && hdmi_capture_enabled);
    { auto resumed = receiver_access.read(); assert(resumed); }
    // A stuck detector must time out without asserting reset or changing the
    // previously enabled capture. Keep the owner on another thread.
    std::promise<void> stuck_entered, unstuck;
    auto unblock = unstuck.get_future();
    std::thread stuck([&] {
        auto lease = receiver_access.read(); assert(lease);
        stuck_entered.set_value(); unblock.wait();
    });
    stuck_entered.get_future().wait();
    auto calls = reset_calls;
    auto start = std::chrono::steady_clock::now();
    assert(kvmv_hdmi_control(0) == -1 && errno == ETIMEDOUT);
    assert(std::chrono::steady_clock::now() - start < std::chrono::seconds(4));
    assert(reset_calls == calls && hdmi_capture_enabled == 1);
    unstuck.set_value(); stuck.join();
    // A short sensor session must delay reset, rather than reject it.
    std::promise<void> sensor_entered, sensor_release;
    auto sensor_done = sensor_release.get_future();
    std::thread sensor([&] {
        nanokvm::ReceiverFileLock owner; assert(owner);
        sensor_entered.set_value(); sensor_done.wait();
    });
    sensor_entered.get_future().wait();
    auto waiting_reset = std::async(std::launch::async, [] { return kvmv_hdmi_control(0); });
    assert(waiting_reset.wait_for(std::chrono::milliseconds(30)) == std::future_status::timeout);
    sensor_release.set_value(); sensor.join();
    assert(waiting_reset.get() == 0 && reset_asserted && !hdmi_capture_enabled);
    assert(kvmv_hdmi_control(1) == 0 && !reset_asserted && hdmi_capture_enabled);
    signal_active = 1;
    puts("PASS production maintenance/reset: active read drained, nested pause refused, state restored, external ownership respected, reset failure retained pause and stuck reader timed out");
}

static void publication() {
    assert(!poll_hdmi_receiver(false));
    assert(signal_active && publications == 1);
    auto baseline = LT6911_i2c;
    for (int n = 1; n <= 3; ++n) {
        LT6911_i2c.fail_at = LT6911_i2c.calls + 1;
        assert(!poll_hdmi_receiver(true));
        assert(publications == 1 && mode_switches == 0 && receiver_retry_pending.load());
        assert(signal_active == (n < 3));
    }
    LT6911_i2c = baseline; LT6911_i2c.fail_at = 0;
    assert(!poll_hdmi_receiver(false));
    assert(signal_active && publications == 2 && !receiver_retry_pending.load());
    LT6911_i2c.registers[0x86a3] = 0x88;
    assert(!poll_hdmi_receiver(false));
    assert(!signal_active && publications == 2);
    { auto control = receiver_access.control(); assert(control); control.pause(); }
    auto calls = LT6911_i2c.calls;
    assert(!poll_hdmi_receiver(true));
    assert(LT6911_i2c.calls == calls && publications == 2);
    { auto control = receiver_access.control(); assert(control); control.resume(); }
    puts("PASS production poll: transient IO preserves geometry/signal, repeated IO withdraws signal, recovery and pause perform no stale publication");
}

static void failure_generations() {
    auto fail_poll = [] {
        LT6911_i2c.fail_at = LT6911_i2c.calls + 1;
        assert(!poll_hdmi_receiver(false));
    };
    auto healthy = [&] {
        LT6911_i2c = Bus::fixture(ReceiverKind::UXC);
        assert(!poll_hdmi_receiver(false) && signal_active);
    };
    for (bool reset : {false, true}) {
        healthy();
        fail_poll(); fail_poll(); assert(signal_active);
        // No poll observes the paused state: only lifecycle generation can
        // distinguish the fresh read sequence from the pre-maintenance one.
        if (reset) {
            assert(kvmv_hdmi_control(0) == 0);
            assert(kvmv_hdmi_control(1) == 0);
        } else {
            assert(kvmv_edid_maintenance(1) == 0);
            assert(kvmv_edid_maintenance(0) == 0);
        }
        set_hdmi_detection_state(1); // another health publisher may report VI
        fail_poll(); assert(signal_active);
        fail_poll(); assert(signal_active);
        fail_poll(); assert(!signal_active);
    }
    healthy();
    fail_poll(); fail_poll(); assert(signal_active);
    {
        nanokvm::ReceiverFileLock owner; assert(owner);
        const auto calls = LT6911_i2c.calls;
        assert(!poll_hdmi_receiver(false) && LT6911_i2c.calls == calls);
    }
    // Mere contention does not forgive actual failed samples.
    fail_poll(); assert(!signal_active);
    healthy();
    puts("PASS failure lifecycle: fresh three-poll allowance after unobserved EDID/reset pause; contention does not erase real errors");
}

static void publication_during_lease() {
    LT6911_i2c = Bus::fixture(ReceiverKind::UXC);
    auto before = test_geometry.snapshot();
    auto lease = test_geometry.lease(before); assert(lease);
    LT6911_i2c.pair(0x85ea, 1280); LT6911_i2c.pair(0x85f0, 720);
    auto publisher = std::async(std::launch::async, [] { return poll_hdmi_receiver(false); });
    assert(publisher.wait_for(std::chrono::seconds(1)) == std::future_status::ready);
    assert(!publisher.get());
    assert(test_geometry.snapshot().width == before.width);
    // The admission mutex was released even though frame visibility is pending.
    auto reset = std::async(std::launch::async, [] { return kvmv_hdmi_control(0); });
    assert(reset.wait_for(std::chrono::seconds(1)) == std::future_status::ready);
    assert(reset.get() == 0);
    lease.release();
    assert(test_geometry.snapshot().width == 1280 && test_geometry.snapshot().height == 720);
    assert(kvmv_hdmi_control(1) == 0);
    puts("PASS real GeometryGate: publish/reset complete while frame lease remains held; visibility changes after release");
}

int main() { snapshots(); admission(); controls(); publication(); failure_generations(); publication_during_lease(); }
