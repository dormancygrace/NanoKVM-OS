// Forced interleavings of the real CapturePark (capture_park.hpp): a detector
// thread restarting capture while parked, and a viewer resuming meanwhile, at
// the points of the race the review described.
#ifdef NDEBUG
#error "the checks are asserts"
#endif
#include <atomic>
#include <cassert>
#include <chrono>
#include <condition_variable>
#include <cstdio>
#include <cstdlib>
#include <functional>
#include <future>
#include <mutex>
#include <thread>
#include "capture_park.hpp"

using namespace nanokvm;

namespace {
// A one-shot event two threads rendezvous on.
class Event {
public:
    void set() { { std::lock_guard<std::mutex> l(m_); on_ = true; } cv_.notify_all(); }
    void wait() { std::unique_lock<std::mutex> l(m_); cv_.wait(l, [&] { return on_; }); }
private:
    std::mutex m_; std::condition_variable cv_; bool on_ = false;
};

// The VPSS outputs: enabled or not, with an optional pause inside a disable.
struct Outputs {
    std::atomic<bool> enabled{true};
    std::atomic<int> disable_error{0}, retire_error{0}, retired{0};
    std::function<void()> on_disable;
    int set_enabled(bool value) {
        if (!value) {
            if (on_disable) on_disable();
            if (const int error = disable_error.load()) return error;
        }
        enabled = value;
        return 0;
    }
    int retire() { ++retired; return retire_error.load(); }
};

int set(CapturePark &park, Outputs &out, bool parked) {
    return park.set(parked, [&] { return out.retire(); }, [&](bool on) { return out.set_enabled(on); });
}

// A restart recreates the outputs enabled; pause() runs after that.
int restart(CapturePark &park, Outputs &out, const std::function<void()> &pause = {}) {
    return park.restart([&] { out.enabled = true; if (pause) pause(); return 0; },
                        [&](bool on) { return out.set_enabled(on); });
}

template <class F>
void must_finish(F &&f, const char *what) {
    std::future<void> done = std::async(std::launch::async, std::forward<F>(f));
    if (done.wait_for(std::chrono::seconds(10)) != std::future_status::ready) {
        std::fprintf(stderr, "deadlock: %s\n", what);
        std::abort();
    }
}

// The previous design: an atomic flag, the restart outside any lock that the
// resume takes. Driven through the review's interleaving below to show that
// the test distinguishes the two.
struct Legacy {
    std::mutex vi_mutex;
    std::atomic<bool> parked{false};
    void restart(Outputs &out) {
        out.enabled = true;
        if (parked.load()) out.set_enabled(false);
    }
    void set(Outputs &out, bool park) {
        std::lock_guard<std::mutex> lock(vi_mutex);
        if (parked.load() == park) return;
        out.set_enabled(!park);
        parked = park;
    }
};

void basics() {
    CapturePark park;
    Outputs out;
    assert(!park.parked());
    assert(set(park, out, false) == 0 && out.enabled);   // resume when running: no-op
    assert(set(park, out, true) == 0);
    assert(park.parked() && !out.enabled && out.retired == 1);
    assert(set(park, out, true) == 0 && out.retired == 1); // park when parked: no-op
    assert(set(park, out, false) == 0);
    assert(!park.parked() && out.enabled);

    // A restart while running leaves the outputs enabled; while parked, disabled.
    assert(restart(park, out) == 0 && out.enabled);
    assert(set(park, out, true) == 0);
    assert(restart(park, out) == 0 && !out.enabled && park.parked());
    assert(set(park, out, false) == 0 && out.enabled && !park.parked());

    // A failed restart changes nothing here.
    assert(park.restart([] { return -1; }, [&](bool on) { return out.set_enabled(on); }) == -1);
    assert(out.enabled && !park.parked());
}

void failures() {
    CapturePark park;
    Outputs out;
    // The encoder could not be retired: nothing disabled, not parked.
    out.retire_error = 5;
    assert(set(park, out, true) == 5);
    assert(out.enabled && !park.parked());
    out.retire_error = 0;
    // The disable failed: enabled again, not parked; a later park can succeed.
    out.disable_error = 7;
    assert(set(park, out, true) == 7);
    assert(out.enabled && !park.parked());
    out.disable_error = 0;
    assert(set(park, out, true) == 0 && !out.enabled && park.parked());
}

// Review: parked; the detector restarts and is about to disable the new
// outputs again when the first viewer resumes. The resume must wait for the
// restart and then enable what it left disabled. Paused once after recreating
// the outputs and once inside the disable itself.
void viewer_resumes_during_restart(bool pause_in_disable) {
    CapturePark park;
    Outputs out;
    assert(set(park, out, true) == 0);
    Event in_restart, finish_restart;
    auto pause = [&] { in_restart.set(); finish_restart.wait(); };
    bool first = true;
    if (pause_in_disable)
        out.on_disable = [&] { if (first) { first = false; pause(); } };
    std::thread detector([&] {
        assert(restart(park, out, pause_in_disable ? std::function<void()>{} : pause) == 0);
    });
    in_restart.wait();
    std::future<int> viewer = std::async(std::launch::async, [&] { return set(park, out, false); });
    // The resume is ordered after the restart, not interleaved with it.
    assert(viewer.wait_for(std::chrono::milliseconds(200)) == std::future_status::timeout);
    finish_restart.set();
    must_finish([&] { detector.join(); }, "detector restart");
    assert(viewer.get() == 0);
    assert(out.enabled && !park.parked());
    // And a read's resume afterwards stays a no-op on enabled outputs.
    assert(set(park, out, false) == 0 && out.enabled);
}

// The opposite order: the viewer resumes first, a later restart leaves the
// outputs enabled.
void restart_after_resume() {
    CapturePark park;
    Outputs out;
    assert(set(park, out, true) == 0);
    assert(set(park, out, false) == 0);
    assert(restart(park, out) == 0);
    assert(out.enabled && !park.parked());
}

// The legacy design loses the transition in the same interleaving.
void legacy_loses_resume() {
    Legacy legacy;
    Outputs out;
    legacy.set(out, true);
    Event in_disable, finish_disable;
    bool first = true;
    out.on_disable = [&] { if (first) { first = false; in_disable.set(); finish_disable.wait(); } };
    std::thread detector([&] { legacy.restart(out); });
    in_disable.wait();
    legacy.set(out, false);                 // the viewer's resume returns at once
    finish_disable.set();
    detector.join();
    assert(!legacy.parked && !out.enabled); // resumed by the flag, outputs disabled
    legacy.set(out, false);                 // and a retry does not recover
    assert(!out.enabled);
}
} // namespace

int main() {
    basics();
    failures();
    viewer_resumes_during_restart(false);
    viewer_resumes_during_restart(true);
    restart_after_resume();
    legacy_loses_resume();
    std::puts("capture park ok");
    return 0;
}
