#include <atomic>
#include <cassert>
#include <cstdlib>
#include <cstring>
#include <deque>
#include <vector>
#include "hdmi_events.hpp"

using namespace nanokvm;
struct Read {
    std::vector<gpio_v2_line_event> events;
    int error = 0;
    ssize_t bytes = -2;
};
struct FakeOps {
    static inline int target = 4, chip_error = 0, info_error = 0, request_error = 0;
    static inline int busy = 0, nonblock_error = 0, line_fd = 100;
    static inline bool missing = false, denied = false;
    static inline unsigned opened = 0, requested = 0, reads = 0;
    static inline std::vector<int> closed;
    static inline std::deque<Read> input;
    static void reset() {
        target = 4; chip_error = info_error = request_error = busy = nonblock_error = 0;
        line_fd = 100; missing = denied = false;
        opened = requested = reads = 0; closed.clear(); input.clear();
    }
    static int open(const char *path) {
        unsigned index = 99; char tail;
        assert(std::sscanf(path, "/dev/gpiochip%u%c", &index, &tail) == 1 && index < 32);
        opened++;
        if (denied || missing || index > 8) { errno = denied ? EACCES : ENOENT; return -1; }
        return int(index) + 10;
    }
    static int control(int fd, unsigned long op, void *arg) {
        if (op == GPIO_GET_CHIPINFO_IOCTL) {
            if (fd == target + 10 && chip_error) { errno = chip_error; return -1; }
            auto *info = static_cast<gpiochip_info *>(arg);
            std::snprintf(info->label, sizeof(info->label), "%s",
                          fd == target + 10 ? "5021000.gpio" : "3020000.gpio");
            info->lines = 32; return 0;
        }
        assert(fd == target + 10);
        if (op == GPIO_V2_GET_LINEINFO_IOCTL) {
            auto *line = static_cast<gpio_v2_line_info *>(arg);
            assert(line->offset == 1);
            if (info_error) { errno = info_error; return -1; }
            line->flags = busy ? GPIO_V2_LINE_FLAG_USED : GPIO_V2_LINE_FLAG_INPUT;
            return 0;
        }
        assert(op == GPIO_V2_GET_LINE_IOCTL);
        auto *request = static_cast<gpio_v2_line_request *>(arg);
        assert(request->num_lines == 1 && request->offsets[0] == 1 && request->event_buffer_size == 64);
        assert(request->config.flags == (GPIO_V2_LINE_FLAG_INPUT |
               GPIO_V2_LINE_FLAG_EDGE_RISING | GPIO_V2_LINE_FLAG_EDGE_FALLING));
        assert(request->config.num_attrs == 0);
        assert(!std::strcmp(request->consumer, "nanokvm-hdmi-int"));
        requested++;
        if (request_error) { errno = request_error; return -1; }
        request->fd = line_fd; return 0;
    }
    static bool nonblocking(int fd) {
        assert(fd == line_fd);
        if (nonblock_error) { errno = nonblock_error; return false; }
        return true;
    }
    static void close(int fd) { closed.push_back(fd); }
    static ssize_t read(int fd, void *data, size_t size) {
        assert(fd == line_fd && size == 64 * sizeof(gpio_v2_line_event)); reads++;
        if (input.empty()) { errno = EAGAIN; return -1; }
        Read result = input.front(); input.pop_front();
        if (result.error) { errno = result.error; return -1; }
        if (result.bytes != -2) return result.bytes;
        assert(result.events.size() <= 64);
        std::memcpy(data, result.events.data(), result.events.size() * sizeof(gpio_v2_line_event));
        return ssize_t(result.events.size() * sizeof(gpio_v2_line_event));
    }
};
using Source = GpioEdges<FakeOps>;
using Events = HdmiEvents<Source>;
static gpio_v2_line_event edge(uint32_t sequence, bool rising) {
    gpio_v2_line_event event{};
    event.seqno = event.line_seqno = sequence; event.offset = 1;
    event.id = rising ? GPIO_V2_LINE_EVENT_RISING_EDGE : GPIO_V2_LINE_EVENT_FALLING_EDGE;
    return event;
}
static void queue(uint32_t sequence, bool rising) {
    FakeOps::input.push_back({{edge(sequence, rising)}});
}
static unsigned closed(int fd) {
    unsigned count = 0; for (int value : FakeOps::closed) count += value == fd;
    return count;
}
static void transport_tests() {
    FakeOps::reset();
    {
        Source source; assert(source.connect() && source.active() && !source.error());
        assert(FakeOps::opened == 5 && FakeOps::requested == 1);
        assert(source.connect() && FakeOps::requested == 1);
        queue(1, true); queue(2, false);
        auto batch = source.drain(); assert(batch.rising && batch.falling && !batch.lost);
        assert(!source.drain().lost);
        queue(4, false); // overflow/resynchronization, without closing a usable fd
        batch = source.drain(); assert(batch.lost && batch.falling && source.active());
        auto event = edge(5, true); event.line_seqno = 9;
        FakeOps::input.push_back({{event}});
        assert(source.drain().lost);
    }
    assert(closed(100) == 1);
    for (int error : {ENOTTY, EBUSY, EIO}) {
        FakeOps::reset(); FakeOps::request_error = error;
        Source source; assert(!source.connect() && source.error() == error);
        assert(!source.active() && closed(14) == 1 && !closed(100));
    }
    FakeOps::reset(); FakeOps::busy = 1;
    { Source source; assert(!source.connect() && source.error() == EBUSY && !FakeOps::requested); }
    FakeOps::reset(); FakeOps::info_error = EIO;
    { Source source; assert(!source.connect() && source.error() == EIO); }
    FakeOps::reset(); FakeOps::chip_error = EIO;
    { Source source; assert(!source.connect() && source.error() == EIO && !FakeOps::requested); }
    FakeOps::reset(); FakeOps::denied = true;
    { Source source; assert(!source.connect() && source.error() == EACCES && FakeOps::opened == 32); }
    FakeOps::reset(); FakeOps::missing = true;
    { Source source; assert(!source.connect() && source.error() == ENODEV); }
    FakeOps::reset(); FakeOps::nonblock_error = EIO;
    { Source source; assert(!source.connect() && source.error() == EIO && closed(100) == 1); }
    FakeOps::reset(); FakeOps::target = 7; FakeOps::line_fd = 0;
    { Source source; assert(source.connect() && source.active() && FakeOps::opened == 8); }
    assert(closed(0) == 1);

    for (ssize_t bad : {ssize_t(0), ssize_t(1), ssize_t(65 * sizeof(gpio_v2_line_event))}) {
        FakeOps::reset(); Source source; assert(source.connect());
        FakeOps::input.push_back({{}, 0, bad});
        assert(source.drain().lost && !source.active() && source.error() == EPROTO);
        assert(closed(100) == 1);
    }
    for (bool wrong_offset : {false, true}) {
        FakeOps::reset(); Source source; assert(source.connect());
        auto event = edge(1, true);
        if (wrong_offset) event.offset = 2; else event.id = 99;
        FakeOps::input.push_back({{event}});
        assert(source.drain().lost && !source.active() && source.error() == EPROTO);
    }
    FakeOps::reset();
    {
        Source source; assert(source.connect());
        for (int i = 0; i < 4; i++) FakeOps::input.push_back({{}, EINTR});
        assert(!source.drain().lost && FakeOps::reads == 4 && source.active());
        queue(1, true); assert(source.drain().rising);
        FakeOps::input.push_back({{}, EIO});
        assert(source.drain().lost && !source.active() && source.error() == EIO);
        assert(source.connect()); queue(1, false);
        assert(!source.drain().lost); // sequence restarts with the new request
    }
    FakeOps::reset();
    {
        Source source; assert(source.connect());
        for (uint32_t first = 1; first <= 256; first += 64) {
            Read batch;
            for (uint32_t n = first; n < first + 64; n++) batch.events.push_back(edge(n, n % 2));
            FakeOps::input.push_back(batch);
        }
        queue(257, true);
        auto batch = source.drain();
        assert(batch.rising && batch.falling && !batch.lost && FakeOps::reads == 4);
        assert(FakeOps::input.size() == 1 && source.drain().rising);
        queue(UINT32_MAX, true); assert(source.drain().lost);
        queue(0, false); assert(!source.drain().lost); // uint32 wrap is contiguous
    }
    puts("PASS GPIO-v2: dynamic chip, input-only request, all errors, fd0, close, sequence gaps, wrap and bounded storms");
}

static std::atomic<bool> receiver_retry_pending{false};
struct Call { bool automatic, initial, event; };
static std::vector<Call> calls;
static bool fail_poll = false, change_mode = false;
static bool poll_hdmi_receiver(bool automatic, bool initial, bool event) {
    calls.push_back({automatic, initial, event});
    receiver_retry_pending = fail_poll;
    return change_mode;
}
// PRODUCTION_SERVICE

static void schedule_tests() {
    for (auto kind : {ReceiverKind::C, ReceiverKind::UXC, ReceiverKind::D}) {
        FakeOps::reset(); calls.clear(); fail_poll = change_mode = false;
        receiver_retry_pending = false;
        Events events;
        events.observe(kind, 0); assert(events.active());
        assert(!service_hdmi_events(events, 0));
        assert(calls.size() == 1 && !calls.back().automatic && calls.back().event && !calls.back().initial);
        for (uint64_t now : {100U, 1000U, 10000U}) {
            events.observe(kind, now); service_hdmi_events(events, now);
        }
        assert(calls.size() == 1); // no healthy periodic reads, for every model
        const bool rising = kind == ReceiverKind::C;
        queue(1, !rising);
        events.observe(kind, 10100); service_hdmi_events(events, 10100);
        assert(calls.size() == 1); // wrong edge is ignored
        queue(2, rising); queue(3, rising);
        events.observe(kind, 10200); change_mode = true;
        assert(service_hdmi_events(events, 10200));
        assert(calls.size() == 2 && calls.back().automatic && calls.back().event);
        change_mode = false;
        // A real edge survives repeated failed/paused/settling attempts.
        queue(4, rising); fail_poll = true;
        events.observe(kind, 10300); service_hdmi_events(events, 10300);
        assert(calls.size() == 3 && calls.back().automatic && receiver_retry_pending);
        events.observe(kind, 11299); service_hdmi_events(events, 11299);
        assert(calls.size() == 3);
        events.observe(kind, 11300); service_hdmi_events(events, 11300);
        assert(calls.size() == 4 && calls.back().automatic);
        fail_poll = false;
        events.observe(kind, 12300); service_hdmi_events(events, 12300);
        assert(calls.size() == 5 && calls.back().automatic && !receiver_retry_pending);
        events.observe(kind, 20000); service_hdmi_events(events, 20000);
        assert(calls.size() == 5);
        // External reset/resume asks for recovery, without requiring a new edge.
        receiver_retry_pending = true;
        service_hdmi_events(events, 20100);
        assert(calls.size() == 6 && !calls.back().automatic && !calls.back().event);
        // An overflow can hide the applicable edge: resync even on the wrong edge.
        queue(9, !rising); events.observe(kind, 21000);
        service_hdmi_events(events, 21000);
        assert(calls.size() == 7 && !calls.back().automatic && calls.back().event);
        // Manual mode: drain continuously, consume only when normal mode returns.
        queue(10, rising); events.observe(kind, 22000);
        events.observe(kind, 23000); assert(calls.size() == 7);
        service_hdmi_events(events, 23000);
        assert(calls.size() == 8 && calls.back().automatic);
        // Source disappearance triggers resync/fallback, then re-acquisition.
        FakeOps::input.push_back({{}, EIO}); FakeOps::missing = true;
        events.observe(kind, 24000); assert(!events.active());
        service_hdmi_events(events, 24000);
        assert(calls.size() == 9 && calls.back().event);
        for (uint64_t now : {24100U, 24999U}) {events.observe(kind, now); service_hdmi_events(events, now);}
        assert(calls.size() == 9);
        events.observe(kind, 25000); service_hdmi_events(events, 25000);
        assert(calls.size() == 10 && !calls.back().event);
        const unsigned opened = FakeOps::opened;
        events.observe(kind, 28999); assert(FakeOps::opened == opened);
        FakeOps::missing = false;
        events.observe(kind, 29000); assert(events.active() && FakeOps::requested == 2);
        service_hdmi_events(events, 29000);
        assert(calls.size() == 11 && calls.back().event);
        events.observe(kind, 60000); service_hdmi_events(events, 60000);
        assert(calls.size() == 11);
    }
    // Busy GPIO at startup uses the previous 1s fallback; becoming available
    // subscribes before a new snapshot, closing the recovery race.
    FakeOps::reset(); FakeOps::busy = 1; calls.clear(); receiver_retry_pending = false;
    {
        Events events;
        events.observe(ReceiverKind::UXC, 0); service_hdmi_events(events, 0);
        assert(!events.active() && calls.size() == 1);
        events.observe(ReceiverKind::UXC, 999); service_hdmi_events(events, 999);
        assert(calls.size() == 1);
        events.observe(ReceiverKind::UXC, 1000); service_hdmi_events(events, 1000);
        assert(calls.size() == 2);
        FakeOps::busy = 0;
        events.observe(ReceiverKind::UXC, 5000); service_hdmi_events(events, 5000);
        assert(events.active() && calls.size() == 3 && calls.back().event);
        events.observe(ReceiverKind::UXC, 10000); service_hdmi_events(events, 10000);
        assert(calls.size() == 3);
    }
    puts("PASS production service: C rising, UXC/D falling, quiet stable input, coalescing, retained edges, retries, loss, fallback and reacquisition");
}
static void vi_recovery_tests() {
    for (auto kind : {ReceiverKind::C, ReceiverKind::UXC, ReceiverKind::D}) {
        FakeOps::reset(); calls.clear(); fail_poll = change_mode = false;
        receiver_retry_pending = false;
        Events events;
        events.observe(kind, 0); service_hdmi_events(events, 0);
        assert(events.active() && calls.size() == 1);
        // Healthy, unavailable/uninitialized and invalid VI reports do not
        // turn the independent 10s VI publication into receiver polling.
        uint64_t now = 10000;
        for (uint8_t code : {0, 1, 8, 255}) {
            service_hdmi_events(events, now, true, code); now += 10000;
        }
        assert(calls.size() == 1 && FakeOps::input.empty());
        // A stale mismatch must not be replayed every detector iteration.
        service_hdmi_events(events, now, false, 3);
        assert(calls.size() == 1);
        // Active GPIO, no edges, but fresh VI mismatch/no frames: resnapshot
        // once, update checked geometry, then return to quiet operation.
        for (uint8_t code = 2; code <= 7; ++code) {
            const auto before = calls.size();
            service_hdmi_events(events, now, true, code);
            assert(calls.size() == before + 1 && calls.back().event &&
                   !calls.back().automatic && !receiver_retry_pending);
            for (unsigned delta : {100U, 1000U, 9999U}) {
                events.observe(kind, now + delta);
                service_hdmi_events(events, now + delta, false, code);
            }
            assert(calls.size() == before + 1 && events.active());
            now += 10000;
        }
        // A fault resync is retained across the healthy 100ms coalescing gap.
        service_hdmi_events(events, now, true, 3);
        const auto before = calls.size();
        service_hdmi_events(events, now + 50, true, 4);
        assert(calls.size() == before);
        fail_poll = true;
        now += 100;
        service_hdmi_events(events, now);
        assert(calls.size() == before + 1 && receiver_retry_pending);
        service_hdmi_events(events, now + 999);
        assert(calls.size() == before + 1);
        fail_poll = false;
        service_hdmi_events(events, now + 1000);
        assert(calls.size() == before + 2 && calls.back().event &&
               !receiver_retry_pending);
        service_hdmi_events(events, now + 10000, true, 1);
        assert(calls.size() == before + 2);
    }
    puts("PASS silent INT: fresh VI faults recover all models, healthy/stale reports stay quiet, resync survives coalescing and I2C failure");
}
int main() { transport_tests(); schedule_tests(); vi_recovery_tests(); }
