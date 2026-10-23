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
    int final_status = -1;
    unsigned change_on_read = 0, change_on_match = 1, matched_reads = 0;
    std::vector<unsigned> writes;
    bool expected_combined = false, forbid_writes = false;
    std::vector<unsigned> reads;
    std::map<unsigned, uint8_t> registers;
    bool fail() {
        ++calls;
        if (calls == fail_at || calls == second_fail_at) {
            errno = calls == fail_at ? ENXIO : EIO;
            return true;
        }
        return false;
    }
    bool write_register(int addr, uint8_t page, uint8_t reg, const uint8_t *p, size_t n, bool combined) {
        assert(combined == expected_combined);
        assert(!forbid_writes); // live D must never open the MCU register window
        assert(addr == 0x2b && p && n == 1);
        if (reg == 0xee && p[0] == 0) ++cleanup_attempts;
        writes.push_back((unsigned(page) << 16) | (unsigned(reg) << 8) | p[0]);
        if (fail()) return false;
        registers[(page << 8) | reg] = p[0];
        return true;
    }
    bool read_register(int addr, uint8_t page, uint8_t reg, uint8_t *p, size_t n, bool combined) {
        assert(combined == expected_combined);
        assert(addr == 0x2b && p && n);
        reads.push_back((unsigned(page) << 8) | reg);
        if (fail()) return false;
        for (size_t i = 0; i < n; ++i) p[i] = registers[(page << 8) | (reg + i)];
        if (((unsigned(page) << 8) | reg) == change_on_read &&
            ++matched_reads >= change_on_match) p[0] ^= 1;
        if (page == 0x86 && reg == 0xa3 && n == 1 && final_status >= 0) p[0] = final_status;
        return true;
    }
    void pair(unsigned reg, uint16_t value) {
        registers[reg] = value >> 8; registers[reg + 1] = value & 0xff;
    }
    static Bus fixture(ReceiverKind kind) {
        Bus io;
        io.expected_combined = kind == ReceiverKind::UXC;
        if (kind == ReceiverKind::UXC) {
            io.pair(0x8100, 0x1704);
            io.registers[0x86a3] = 0x55; io.pair(0x85ea, 1920); io.pair(0x85f0, 1080);
            io.registers[0x86a2] = 4; io.registers[0x86a9] = 3;
            io.registers[0x8670] = 3; io.registers[0x8671] = 5;
            io.pair(0x8672, 16); io.registers[0x8674] = 33; io.registers[0x8675] = 3;
            io.pair(0x8676, 40); io.pair(0x8678, 24);
            io.pair(0x867a, 1481); io.pair(0x867c, 1360);
            io.pair(0x867e, 1440); io.pair(0x8680, 1280);
            io.registers[0x8750] = 0x33; io.registers[0x8751] = 0x46;
            io.registers[0x8752] = 0x8c;
        } else if (kind == ReceiverKind::C) {
            io.pair(0xa000, 0x1605);
            io.pair(0xd28b, 960); io.pair(0xd296, 1080);
            io.pair(0xc238, 1920); io.pair(0xc206, 1080);
            io.pair(0xd289, 1100); io.pair(0xd29e, 1125);
            io.pair(0xd294, 22); io.pair(0xd29c, 44); io.pair(0xd298, 74);
            io.registers[0xd286] = 5; io.registers[0xd287] = 36; io.registers[0xd288] = 4;
            const uint8_t fw[] = {0x49, 0x25, 3, 3, 1};
            for (unsigned i = 0; i < sizeof(fw); ++i) io.registers[0xd20c + i] = fw[i];
        } else {
            io.pair(0xe100, 0x2102);
            io.pair(0xe08c, 960); io.pair(0xe08e, 1080);
            io.pair(0xe088, 1100); io.pair(0xe08a, 1125);
            io.registers[0xe080] = 0x25; io.registers[0xe081] = 5;
            io.registers[0xe082] = 0x13; io.registers[0xe083] = 1;
            io.registers[0xe084] = 1; io.registers[0xe095] = 4;
            io.registers[0xe096] = 7; io.registers[0xe0b0] = 1;
            io.registers[0xe092] = 1; io.registers[0xe093] = 2; io.registers[0xe094] = 3;
        }
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
static auto fake_now = std::chrono::steady_clock::time_point{};
static auto test_now() { return fake_now; }
static nanokvm::ReceiverAccess receiver_access(NANOKVM_RECEIVER_LOCK_PATH, test_now);
static std::atomic<bool> receiver_retry_pending{false};
static std::atomic<bool> receiver_diagnostics_pending{true};
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
// PRODUCTION_DIAGNOSTICS
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
    { auto held = receiver_access.read(); assert(held.state == ReceiverState::Settling); }
    fake_now += std::chrono::milliseconds(250);
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
    { auto held = receiver_access.read(); assert(held.state == ReceiverState::Settling); }
    auto i2c_calls = LT6911_i2c.calls;
    auto published = publications;
    for (int n = 0; n < 5; ++n) assert(!poll_hdmi_receiver(false));
    assert(LT6911_i2c.calls == i2c_calls && publications == published);
    fake_now += std::chrono::milliseconds(249);
    { auto held = receiver_access.read(); assert(held.state == ReceiverState::Settling); }
    fake_now += std::chrono::milliseconds(1);
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
    fake_now += std::chrono::milliseconds(NANOKVM_RECEIVER_SETTLE_MS);
    signal_active = 1;
    puts("PASS production maintenance/reset: active read drained, nested pause refused, state restored, external ownership respected, reset failure retained pause and stuck reader timed out");
}

static void publication() {
    assert(!poll_hdmi_receiver(false));
    assert(signal_active && publications == 1);
    auto baseline = LT6911_i2c;
    for (int n = 1; n <= 3; ++n) {
        LT6911_i2c.fail_at = LT6911_i2c.calls + 1;
        auto calls = LT6911_i2c.calls;
        assert(!poll_hdmi_receiver(true));
        // Failed enable plus its required cleanup, no optional telemetry.
        assert(LT6911_i2c.calls == calls + 2);
        assert(publications == 1 && mode_switches == 0 && receiver_retry_pending.load());
        assert(signal_active == (n < 3));
    }
    LT6911_i2c = baseline; LT6911_i2c.fail_at = 0;
    assert(!poll_hdmi_receiver(false));
    assert(signal_active && publications == 2 && !receiver_retry_pending.load());
    // A real IRQ must refresh timing telemetry even with unchanged CSI size.
    LT6911_i2c.registers[0x8752] = 0x8d;
    auto before_irq = LT6911_i2c.calls;
    assert(!poll_hdmi_receiver(false, false, true));
    assert(signal_active && publications == 3);
    auto with_diag = LT6911_i2c.calls - before_irq;
    before_irq = LT6911_i2c.calls;
    assert(!poll_hdmi_receiver(false));
    assert(signal_active && publications == 4 && LT6911_i2c.calls - before_irq < with_diag);
    // Optional telemetry fails after a good sample: geometry/signal survive.
    auto fixture = Bus::fixture(ReceiverKind::UXC);
    nanokvm::HdmiReceiver<Bus> measured(fixture);
    assert(measured.snapshot(ReceiverKind::UXC).state == ReceiverState::Ready);
    LT6911_i2c.fail_at = LT6911_i2c.calls + fixture.calls + 1;
    receiver_diagnostics_pending = true;
    assert(!poll_hdmi_receiver(false));
    assert(signal_active && publications == 5);
    LT6911_i2c.fail_at = 0;
    LT6911_i2c.registers[0x86a3] = 0x88;
    assert(!poll_hdmi_receiver(false));
    assert(!signal_active && publications == 5);
    { auto control = receiver_access.control(); assert(control); control.pause(); }
    auto calls = LT6911_i2c.calls;
    assert(!poll_hdmi_receiver(true));
    assert(LT6911_i2c.calls == calls && publications == 5);
    { auto control = receiver_access.control(); assert(control); control.resume(); }
    puts("PASS production poll: transient IO preserves geometry/signal, repeated IO withdraws signal, recovery and pause perform no stale publication");
}

static void diagnostics() {
    auto io = Bus::fixture(ReceiverKind::UXC);
    nanokvm::HdmiReceiver<Bus> receiver(io);
    auto good = receiver.diagnostics(ReceiverKind::UXC);
    using nanokvm::DiagnosticState;
    assert(good.state == DiagnosticState::Ready && good.firmware == 0x300 && good.lanes == 4 &&
        good.chip_id == 0x0417 && good.identity_checked);
    assert(good.width == 2560 && good.height == 1440);
    assert(good.htotal == 2720 && good.vtotal == 1481);
    assert(good.hsync == 32 && good.hfront == 48 && good.hback == 80);
    assert(good.vsync == 5 && good.vfront == 3 && good.vback == 33);
    assert(good.csi_width == 1920 && good.csi_height == 1080 && good.tmds_raw == 0x33468c);
    auto text = nanokvm::format_receiver_diagnostics(good);
    assert(text.find("HDMI=2560x1440") != std::string::npos);
    assert(text.find("CSI=1920x1080") != std::string::npos);
    auto operations = io.calls;
    for (unsigned op = 1; op <= operations; ++op) {
        auto broken = Bus::fixture(ReceiverKind::UXC); broken.fail_at = op;
        nanokvm::HdmiReceiver<Bus> probe(broken);
        auto d = probe.diagnostics(ReceiverKind::UXC);
        assert(d.state == DiagnosticState::IoError && d.error == ENXIO);
        assert(!d.firmware && !d.width && !d.csi_width);
        assert(broken.cleanup_attempts > 0);
    }
    for (auto reg : {0x86a2, 0x86a3, 0x867c, 0x867a, 0x8673}) {
        auto invalid = Bus::fixture(ReceiverKind::UXC); invalid.registers[reg] = 0;
        nanokvm::HdmiReceiver<Bus> probe(invalid);
        assert(probe.diagnostics(ReceiverKind::UXC).state == DiagnosticState::Invalid);
    }
    auto changed = Bus::fixture(ReceiverKind::UXC); changed.final_status = 0x88;
    nanokvm::HdmiReceiver<Bus> changing(changed);
    assert(changing.diagnostics(ReceiverKind::UXC).state == DiagnosticState::Invalid);
    auto before = io.calls;
    assert(receiver.diagnostics(ReceiverKind::Unknown).state == DiagnosticState::Unsupported && io.calls == before);
    nanokvm::DiagnosticSchedule schedule;
    auto now = std::chrono::steady_clock::time_point{};
    assert(schedule.due(1, now)); schedule.completed(good, now);
    assert(!schedule.due(1, now + std::chrono::hours(1))); // stable: no periodic IO
    assert(schedule.due(2, now)); auto failed = good; failed.state = DiagnosticState::IoError;
    schedule.completed(failed, now);
    assert(!schedule.due(2, now + std::chrono::milliseconds(4999)));
    assert(schedule.due(2, now + std::chrono::seconds(5)));
    schedule.completed(good, now + std::chrono::seconds(5));
    schedule.reset(); assert(schedule.due(2, now)); // reset/maintenance generation
    auto invalid = good; invalid.state = DiagnosticState::Invalid;
    schedule.completed(invalid, now);
    assert(!schedule.due(2, now + std::chrono::hours(1)));
    assert(schedule.due(3, now)); // transition still refreshes invalid/no-signal
    schedule.completed(invalid, now);
    schedule.reset(); assert(schedule.due(3, now)); // same-size real IRQ
    puts("PASS UXC diagnostics: firmware/lanes/HDMI vs CSI, every IO/cleanup fault, timing rejection, unknown model, event/retry scheduling");
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
        fake_now += std::chrono::milliseconds(NANOKVM_RECEIVER_SETTLE_MS);
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

static void cd_diagnostics() {
    fake_now += std::chrono::seconds(3); // finish preceding reset holdoff
    using nanokvm::DiagnosticState;
    for (auto kind : {ReceiverKind::C, ReceiverKind::D}) {
        auto io = Bus::fixture(kind);
        nanokvm::HdmiReceiver<Bus> receiver(io);
        auto id = receiver.identify(kind);
        assert(id.model == kind && !id.error && !id.cleanup_error);
        assert(id.chip_id == (kind == ReceiverKind::C ? 0x0516 : 0x2102));
        io.writes.clear();
        io.forbid_writes = kind == ReceiverKind::D;
        auto good = receiver.diagnostics(kind);
        assert(good.state == DiagnosticState::Ready && good.model == kind);
        assert(good.identity_checked == (kind == ReceiverKind::C));
        assert(good.width == 1920 && good.height == 1080);
        assert(good.htotal == 2200 && good.vtotal == 1125);
        assert(good.firmware_size == (kind == ReceiverKind::C ? 5 : 4));
        auto text = nanokvm::format_receiver_diagnostics(good);
        assert(text.find(nanokvm::receiver_name(kind)) != std::string::npos);
        assert(text.find("fw_raw=") != std::string::npos);
        if (kind == ReceiverKind::C) {
            assert(good.hsync == 44 && good.hfront == 88 && good.hback == 148);
            assert(good.vsync == 5 && good.vfront == 4 && good.vback == 36);
            assert(text.find("lanes=unavailable") != std::string::npos);
            io.pair(0xc238, 1280); io.pair(0xc206, 720);
            auto scaled = receiver.diagnostics(kind);
            assert(scaled.state == DiagnosticState::Ready && scaled.width == 1920 &&
                scaled.csi_width == 1280 && scaled.csi_height == 720);
        } else {
            assert(good.lanes == 4 && good.color == 7 && good.byte_clock_raw == 0x010203);
            assert(!good.csi_width && !good.hsync);
            assert(text.find("HDMI_CSI_independent=unavailable") != std::string::npos);
            for (unsigned event : {0U, 2U, 3U, 255U}) {
                io.registers[0xe084] = event; // event is not cable status
                assert(receiver.diagnostics(kind).state == DiagnosticState::Ready);
            }
            assert(io.writes.empty());
            assert(text.find("chip_id=exclusive-probe-only") != std::string::npos);
        }
        auto clean = Bus::fixture(kind);
        nanokvm::HdmiReceiver<Bus> count(clean);
        assert(count.diagnostics(kind).state == DiagnosticState::Ready);
        for (unsigned failure = 1; failure <= clean.calls; ++failure) {
            auto broken = Bus::fixture(kind); broken.fail_at = failure;
            nanokvm::HdmiReceiver<Bus> probe(broken);
            auto failed = probe.diagnostics(kind);
            assert(failed.state == DiagnosticState::IoError && failed.error == ENXIO);
            assert(!failed.width && !failed.firmware_size && !failed.csi_width);
            if (kind == ReceiverKind::C) assert(broken.cleanup_attempts);
            else assert(broken.cleanup_attempts == 0 && broken.writes.empty());
        }
        auto broken = Bus::fixture(kind); broken.fail_at = 2; broken.second_fail_at = 3;
        nanokvm::HdmiReceiver<Bus> double_fault(broken);
        auto failure = double_fault.identify(kind);
        assert(failure.error == ENXIO && failure.cleanup_error == EIO &&
            failure.model == ReceiverKind::Unknown);
        for (uint8_t fw : {uint8_t(0), uint8_t(0xff), uint8_t(0x91)}) {
            auto changed = Bus::fixture(kind);
            for (unsigned i = 0; i < (kind == ReceiverKind::C ? 5U : 4U); ++i)
                changed.registers[(kind == ReceiverKind::C ? 0xd20c : 0xe080) + i] = fw;
            nanokvm::HdmiReceiver<Bus> probe(changed);
            auto d = probe.diagnostics(kind);
            assert(d.model == kind && d.state == DiagnosticState::Ready);
            assert(d.firmware_raw[0] == fw);
        }
        auto unknown = Bus::fixture(kind);
        unknown.pair(kind == ReceiverKind::C ? 0xa000 : 0xe100, 0x1234);
        nanokvm::HdmiReceiver<Bus> unknown_id(unknown);
        auto d = kind == ReceiverKind::D ? unknown_id.exclusive_diagnostics(kind, 1) :
            unknown_id.diagnostics(kind);
        assert(d.model == ReceiverKind::Unknown && d.state == DiagnosticState::Invalid);
        assert(unknown.calls == 4 && !d.firmware_size); // no timing-map guesses
        assert(nanokvm::format_receiver_diagnostics(d).find("model=unknown") != std::string::npos);
        auto changed = Bus::fixture(kind);
        changed.change_on_read = kind == ReceiverKind::C ? 0xd286 : 0xe088;
        changed.change_on_match = 2;
        nanokvm::HdmiReceiver<Bus> incoherent(changed);
        assert(incoherent.diagnostics(kind).state == DiagnosticState::Invalid);
        for (unsigned reg : kind == ReceiverKind::C ?
            std::vector<unsigned>{0xd289, 0xd28b, 0xd294, 0xd296, 0xd298, 0xd29c, 0xd29e} :
            std::vector<unsigned>{0xe088, 0xe08a, 0xe08c, 0xe08e}) {
            auto invalid = Bus::fixture(kind); invalid.pair(reg, UINT16_MAX);
            nanokvm::HdmiReceiver<Bus> probe(invalid);
            assert(probe.diagnostics(kind).state == DiagnosticState::Invalid);
        }
        for (unsigned reg : kind == ReceiverKind::C ?
            std::vector<unsigned>{0xd289, 0xd28b, 0xd296, 0xd29e} :
            std::vector<unsigned>{0xe088, 0xe08a, 0xe08c, 0xe08e}) {
            auto invalid = Bus::fixture(kind); invalid.pair(reg, 0);
            nanokvm::HdmiReceiver<Bus> probe(invalid);
            assert(probe.diagnostics(kind).state == DiagnosticState::Invalid);
        }
        if (kind == ReceiverKind::D) {
            for (auto fault : {std::pair<unsigned,unsigned>{0xe095, 3},
                {0xe095, 0}, {0xe096, 13}, {0xe0b0, 0}}) {
                auto invalid = Bus::fixture(kind); invalid.registers[fault.first] = fault.second;
                nanokvm::HdmiReceiver<Bus> probe(invalid);
                assert(probe.diagnostics(kind).state == DiagnosticState::Invalid);
            }
            auto overflow = Bus::fixture(kind); overflow.pair(0xe08c, 32768);
            nanokvm::HdmiReceiver<Bus> probe(overflow);
            assert(probe.diagnostics(kind).state == DiagnosticState::Invalid);
        }
        // A valid capture sample survives failed or incoherent optional telemetry.
        kvmv_cfg.hdmi_version = kind == ReceiverKind::C ? 0 : 3;
        LT6911_i2c = Bus::fixture(kind);
        auto sample_io = LT6911_i2c;
        nanokvm::HdmiReceiver<Bus> sample_count(sample_io);
        assert(sample_count.snapshot(kind).state == ReceiverState::Ready);
        LT6911_i2c.fail_at = sample_io.calls + 1;
        receiver_diagnostics_pending = true;
        auto before = publications;
        assert(!poll_hdmi_receiver(false) && signal_active && publications == before + 1);
    }
    kvmv_cfg.hdmi_version = 1;
    puts("PASS C/D: separate hardware ID/firmware, raw unknown firmware, checked timings/lanes, no stream writes, every IO/cleanup fault and optional telemetry isolation");
}

static void model_windows() {
    for (auto kind : {ReceiverKind::C, ReceiverKind::D, ReceiverKind::UXC}) {
        kvmv_cfg.hdmi_version = kind == ReceiverKind::C ? 0 : kind == ReceiverKind::D ? 3 : 1;
        const auto reset_window = kind == ReceiverKind::UXC ? 250U : 2500U;
        assert(nanokvm::receiver_settle_ms(kind) == reset_window);
        for (bool reset : {true, false}) {
            const auto expected = reset ? reset_window : 250U;
            if (reset) {
                assert(kvmv_hdmi_control(0) == 0 && kvmv_hdmi_control(1) == 0);
            } else {
                const auto resets = reset_calls;
                assert(kvmv_edid_maintenance(1) == 0 && kvmv_edid_maintenance(0) == 0);
                assert(reset_calls == resets); // no reset in the Cube/Lite maintenance API
            }
            LT6911_i2c = Bus::fixture(kind);
            auto before = publications;
            for (unsigned ms = 0; ms < expected; ++ms) {
                assert(!poll_hdmi_receiver(false, false, ms == 100));
                assert(LT6911_i2c.calls == 0 && publications == before);
                fake_now += std::chrono::milliseconds(1);
            }
            assert(!poll_hdmi_receiver(false));
            assert(signal_active && LT6911_i2c.calls && publications == before + 1);
        }
    }
    for (auto kind : {ReceiverKind::C, ReceiverKind::D}) {
        kvmv_cfg.hdmi_version = kind == ReceiverKind::C ? 0 : 3;
        assert(kvmv_hdmi_control(0) == 0 && kvmv_hdmi_control(1) == 0);
        fake_now += std::chrono::milliseconds(100);
        assert(kvmv_edid_maintenance(1) == 0 && kvmv_edid_maintenance(0) == 0);
        LT6911_i2c = Bus::fixture(kind);
        for (unsigned ms = 100; ms < 2500; ++ms) {
            assert(!poll_hdmi_receiver(false));
            assert(!LT6911_i2c.calls);
            fake_now += std::chrono::milliseconds(1);
        }
        assert(!poll_hdmi_receiver(false) && signal_active && LT6911_i2c.calls);
    }
    puts("PASS reset2500/2500/250ms; no-reset maintenance250ms; overlapping maintenance preserves reset deadline; no early I2C/publication and IRQ retained");
}

static void d_read_only_production() {
    fake_now += std::chrono::seconds(3);
    kvmv_cfg.hdmi_version = 3;
    LT6911_i2c = Bus::fixture(ReceiverKind::D);
    LT6911_i2c.forbid_writes = true;
    LT6911_i2c.registers[0xe0ee] = 0;
    // Reproduce the reviewed operation-6 fault after the same two snapshot
    // reads. In the old path this failed E0EE close and left MCU ownership open.
    LT6911_i2c.fail_at = 6;
    receiver_diagnostics_pending = true;
    auto before = publications;
    assert(!poll_hdmi_receiver(false));
    assert(signal_active && publications == before + 1);
    assert(LT6911_i2c.registers[0xe0ee] == 0 && LT6911_i2c.writes.empty());
    LT6911_i2c.fail_at = 0;
    for (int poll = 0; poll < 100; ++poll) {
        assert(!poll_hdmi_receiver(false)); // no further HDMI IRQ
        assert(signal_active && publications == before + poll + 2);
        assert(LT6911_i2c.registers[0xe0ee] == 0 && LT6911_i2c.writes.empty());
    }
    for (unsigned irq = 0; irq < 4; ++irq)
        assert(!poll_hdmi_receiver(false, false, true));
    assert(kvmv_edid_maintenance(1) == 0 && kvmv_edid_maintenance(0) == 0);
    fake_now += std::chrono::milliseconds(250);
    assert(!poll_hdmi_receiver(false));
    assert(kvmv_hdmi_control(0) == 0 && kvmv_hdmi_control(1) == 0);
    fake_now += std::chrono::milliseconds(2500);
    assert(!poll_hdmi_receiver(false));
    assert(LT6911_i2c.registers[0xe0ee] == 0 && LT6911_i2c.writes.empty());
    for (auto reg : LT6911_i2c.reads) assert((reg >> 8) == 0xe0); // no internal E1 ID reads
    kvmv_cfg.hdmi_version = 1;
    puts("PASS real D poll: old close-fault6,100 stale healthy no-IRQ polls,IRQ,resume/reset all keep E0EE closed with zero writes/internal-ID reads");
}

static void exclusive_d_identity() {
    using nanokvm::DiagnosticState;
    auto io = Bus::fixture(ReceiverKind::D);
    nanokvm::HdmiReceiver<Bus> receiver(io);
    auto first = receiver.exclusive_diagnostics(ReceiverKind::D, 7);
    assert(first.state == DiagnosticState::Ready && first.identity_checked && first.chip_id == 0x2102);
    assert(io.writes.size() == 2 && io.registers[0xe0ee] == 0);
    assert(nanokvm::format_receiver_diagnostics(first).find("chip_id=0x2102") != std::string::npos);
    for (unsigned i = 0; i < 5; ++i)
        assert(receiver.exclusive_diagnostics(ReceiverKind::D, 7).state == DiagnosticState::Ready);
    assert(io.writes.size() == 2); // cached once per successful reset generation
    assert(receiver.exclusive_diagnostics(ReceiverKind::D, 8).state == DiagnosticState::Ready);
    assert(io.writes.size() == 4 && io.cleanup_attempts == 2);
    for (unsigned failure = 1; failure <= 4; ++failure) {
        auto broken = Bus::fixture(ReceiverKind::D); broken.fail_at = failure;
        nanokvm::HdmiReceiver<Bus> probe(broken);
        auto d = probe.exclusive_diagnostics(ReceiverKind::D, 1);
        assert(d.state == DiagnosticState::IoError && d.error == ENXIO && !d.firmware_size);
        assert(broken.cleanup_attempts == 1);
        auto calls = broken.calls;
        broken.fail_at = 0;
        auto repeated = probe.exclusive_diagnostics(ReceiverKind::D, 1);
        assert(repeated.state == DiagnosticState::IoError && broken.calls == calls);
        if (failure == 4) assert(d.cleanup_error == ENXIO);
        // Only a newly completed reset permits another internal-ID attempt.
        assert(probe.exclusive_diagnostics(ReceiverKind::D, 2).state == DiagnosticState::Ready);
        assert(broken.registers[0xe0ee] == 0);
    }
    puts("PASS exclusive D probe: hardware ID cached per reset generation; each ID/cleanup fault aborts and is not retried or followed by telemetry");
}

int main() { snapshots(); admission(); controls(); publication(); failure_generations(); publication_during_lease(); diagnostics(); cd_diagnostics(); model_windows(); d_read_only_production(); exclusive_d_identity(); }
