// Forced interleavings of the real geometry gate and the real MJPEG admission
// decision (geometry_gate.hpp + mjpeg_policy.hpp), with a detector thread and a
// capture thread synchronised at the points of the race the review described:
// the input changes to 3840x2160 after the guard and before the submit.
#ifdef NDEBUG
#error "the checks are asserts"
#endif
#include <atomic>
#include <cassert>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <future>
#include <mutex>
#include <thread>
#include <vector>
#include "geometry_gate.hpp"

using namespace nanokvm;

namespace {
constexpr uint16_t qhd_w = 2560, qhd_h = 1440, uhd_w = 3840, uhd_h = 2160;

// A one-shot event two threads rendezvous on.
class Event {
public:
    void set() { { std::lock_guard<std::mutex> l(m_); on_ = true; } cv_.notify_all(); }
    void wait() { std::unique_lock<std::mutex> l(m_); cv_.wait(l, [&] { return on_; }); }
private:
    std::mutex m_; std::condition_variable cv_; bool on_ = false;
};

// The previous design: the pair is re-read at each gate and then the operation
// runs; nothing ties the checked size to the operation. Driven through the same
// interleaving below to show that the new test distinguishes the two.
struct LegacyCfg { std::atomic<uint16_t> width{0}, height{0}; };
template <class Hook, class Submit>
bool legacy_check_then_submit(LegacyCfg &cfg, Hook &&after_gates, Submit &&submit) {
    for (int gate = 0; gate < 2; ++gate)
        if (!mjpeg_input_allowed(cfg.width.load(), cfg.height.load())) return false;
    after_gates();
    submit();
    return true;
}

template <class F>
void must_finish(F &&f, const char *what) {
    std::future<void> done = std::async(std::launch::async, std::forward<F>(f));
    if (done.wait_for(std::chrono::seconds(10)) != std::future_status::ready) {
        std::fprintf(stderr, "deadlock: %s\n", what);
        std::abort();
    }
}

void basics() {
    GeometryGate gate;
    GeometrySnapshot absent = gate.snapshot();
    assert(absent.width == 0 && absent.height == 0 && absent.epoch == 0);
    assert(!gate.publish(0, 0));                    // no change, no new epoch
    assert(gate.snapshot().epoch == 0);
    assert(gate.publish(1920, 1080));
    assert(gate.snapshot().epoch == 1);
    assert(!gate.publish(1920, 1080));
    assert(gate.snapshot().epoch == 1);

    // A lease needs the current epoch.
    assert(!gate.lease(absent));
    GeometrySnapshot now = gate.snapshot();
    {
        GeometryGate::Lease lease = gate.lease(now);
        assert(lease);
        // Held: a publish is recorded but the geometry does not move.
        assert(gate.publish(qhd_w, qhd_h));
        GeometrySnapshot during = gate.snapshot();
        assert(during.width == 1920 && during.height == 1080 && during.epoch == now.epoch);
        assert(gate.lease(now));                    // leases nest; the temporary ends at once
        assert(gate.snapshot().epoch == now.epoch); // ...and does not apply the pending value
    }
    GeometrySnapshot after = gate.snapshot();
    assert(after.width == qhd_w && after.height == qhd_h && after.epoch == now.epoch + 1);

    // A -> B -> A while leased is no change at all.
    {
        GeometryGate::Lease lease = gate.lease(after);
        assert(gate.publish(uhd_w, uhd_h));
        assert(gate.publish(qhd_w, qhd_h));
    }
    assert(gate.snapshot().epoch == after.epoch);
    std::puts("gate basics ok");
}

// The flip comes between the policy check and the lease: the submit step must
// refuse on the epoch and never run.
void flip_between_check_and_lease() {
    GeometryGate gate(qhd_w, qhd_h);
    const GeometrySnapshot snap = gate.snapshot();
    assert(mjpeg_input_allowed(snap.width, snap.height));
    Event checked, flipped;
    std::thread detector([&] {
        checked.wait();
        gate.publish(uhd_w, uhd_h);
        flipped.set();
    });
    int submits = 0;
    JpegAdmission admission = admit_jpeg_step(gate, snap, [&] { checked.set(); flipped.wait(); });
    if (admission.verdict == JpegVerdict::Allowed) ++submits;
    detector.join();
    assert(admission.verdict == JpegVerdict::Stale);
    assert(!admission.lease && submits == 0);
    // A new pass takes a new snapshot, which the policy refuses.
    JpegAdmission next = admit_jpeg_step(gate, gate.snapshot());
    assert(next.verdict == JpegVerdict::Blocked && !next.lease);
    std::puts("flip before lease refused ok");
}

// The flip comes after the guard and before the submit: the lease keeps the
// checked geometry for the operation, publish() does not wait for it, and the
// change is ordered after the operation.
void flip_after_guard_before_submit() {
    GeometryGate gate(qhd_w, qhd_h);
    const GeometrySnapshot snap = gate.snapshot();
    Event guarded, flipped;
    bool publish_changed = false;
    std::thread detector([&] {
        guarded.wait();
        must_finish([&] { publish_changed = gate.publish(uhd_w, uhd_h); }, "publish while a lease is held");
        flipped.set();
    });

    JpegAdmission admission = admit_jpeg_step(gate, snap);
    assert(admission.verdict == JpegVerdict::Allowed && admission.lease);
    guarded.set();
    flipped.wait();   // the detector published, and returned, while the lease is held
    assert(publish_changed);
    // The "submit": it sees the checked geometry and the same epoch, never UHD.
    const GeometrySnapshot at_submit = gate.snapshot();
    assert(at_submit.width == qhd_w && at_submit.height == qhd_h && at_submit.epoch == snap.epoch);
    assert(mjpeg_input_allowed(at_submit.width, at_submit.height));
    admission.lease.release();
    detector.join();

    // After the operation the change is visible, and the old snapshot is stale.
    const GeometrySnapshot later = gate.snapshot();
    assert(later.width == uhd_w && later.height == uhd_h && later.epoch == snap.epoch + 1);
    assert(admit_jpeg_step(gate, snap).verdict == JpegVerdict::Stale);
    assert(admit_jpeg_step(gate, later).verdict == JpegVerdict::Blocked);

    // The same interleaving against the previous design: the pair is re-read at
    // both gates, the input flips after them, and the submit runs at UHD.
    LegacyCfg cfg;
    cfg.width = qhd_w; cfg.height = qhd_h;
    uint16_t submitted_width = 0;
    const bool legacy_submitted = legacy_check_then_submit(
        cfg, [&] { cfg.width = uhd_w; cfg.height = uhd_h; },
        [&] { submitted_width = cfg.width.load(); });
    assert(legacy_submitted && submitted_width == uhd_w);   // the hole the lease closes
    std::puts("flip after guard ordered after the submit ok");
}

// An absent input (0x0) is allowed, and it is checked again: a size detected
// before the submit makes the step stale.
void absent_input_is_rechecked() {
    GeometryGate gate;
    GeometrySnapshot absent = gate.snapshot();
    assert(mjpeg_input_allowed(absent.width, absent.height));
    assert(admit_jpeg_step(gate, absent).verdict == JpegVerdict::Allowed);

    JpegAdmission flipped = admit_jpeg_step(gate, absent, [&] { gate.publish(uhd_w, uhd_h); });
    assert(flipped.verdict == JpegVerdict::Stale && !flipped.lease);
    assert(admit_jpeg_step(gate, gate.snapshot()).verdict == JpegVerdict::Blocked);

    GeometryGate gate2;
    JpegAdmission to_fhd = admit_jpeg_step(gate2, gate2.snapshot(), [&] { gate2.publish(1920, 1080); });
    assert(to_fhd.verdict == JpegVerdict::Stale);        // retried from a new snapshot...
    assert(admit_jpeg_step(gate2, gate2.snapshot()).verdict == JpegVerdict::Allowed);   // ...and allowed
    std::puts("absent input rechecked ok");
}

// publish() from inside the guarded operation, on the same thread, must not
// deadlock (the gate mutex is never held across the operation).
void publish_inside_the_operation() {
    GeometryGate gate(qhd_w, qhd_h);
    must_finish([&] {
        JpegAdmission admission = admit_jpeg_step(gate, gate.snapshot());
        assert(admission.verdict == JpegVerdict::Allowed);
        gate.publish(1920, 1080);
        assert(gate.snapshot().width == qhd_w);
    }, "publish inside a lease");
    assert(gate.snapshot().width == 1920);
    std::puts("publish inside the operation ok");
}

// Two geometries whose pair is distinguishable from a torn one, alternated by
// the detector while readers snapshot and lease. Epoch parity fixes the pair.
void hammer() {
    GeometryGate gate(qhd_w, qhd_h);          // epoch 0 = QHD, odd epochs = UHD
    constexpr int flips = 200000;
    std::atomic<bool> stop{false};
    std::atomic<unsigned long> leased{0}, stale{0}, blocked{0};
    auto reader = [&] {
        uint64_t last_epoch = 0;
        while (!stop.load()) {
            const GeometrySnapshot snap = gate.snapshot();
            const bool qhd = snap.width == qhd_w && snap.height == qhd_h;
            const bool uhd = snap.width == uhd_w && snap.height == uhd_h;
            assert(qhd || uhd);                                   // never a torn pair
            assert(qhd == (snap.epoch % 2 == 0));
            assert(snap.epoch >= last_epoch);
            last_epoch = snap.epoch;
            JpegAdmission admission = admit_jpeg_step(gate, snap);
            if (admission.verdict == JpegVerdict::Allowed) {
                // Under the lease the geometry is the checked one, always QHD here.
                const GeometrySnapshot held = gate.snapshot();
                assert(held.epoch == snap.epoch && held.width == qhd_w && held.height == qhd_h);
                ++leased;
            } else if (admission.verdict == JpegVerdict::Stale) {
                ++stale;
            } else {
                assert(uhd);
                ++blocked;
            }
        }
    };
    std::vector<std::thread> readers;
    for (int i = 0; i < 3; ++i) readers.emplace_back(reader);
    must_finish([&] {
        for (int i = 0; i < flips; ++i) {
            if (i % 2 == 0) gate.publish(uhd_w, uhd_h);
            else gate.publish(qhd_w, qhd_h);
        }
        stop = true;
        for (auto &t : readers) t.join();
    }, "hammer");
    std::printf("hammer ok (leased=%lu stale=%lu blocked=%lu)\n", leased.load(), stale.load(), blocked.load());
}
}

int main() {
    basics();
    flip_between_check_and_lease();
    flip_after_guard_before_submit();
    absent_input_is_rechecked();
    publish_inside_the_operation();
    hammer();
    std::puts("geometry gate ok");
    return 0;
}
