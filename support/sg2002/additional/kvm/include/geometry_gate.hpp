#pragma once
#include <cstdint>
#include <mutex>
#include <utility>
#include "mjpeg_policy.hpp"

// Ownership of the detected HDMI input geometry between the detection thread
// (the only writer) and the capture owner (kvmv_read_img, under vi_mutex).
//
// Contract
//  * The geometry {width, height} and a monotonically increasing epoch live
//    here and nowhere else. kvmv_cfg has no copy; a copy readable without this
//    mutex would be a plain concurrent access. Every reader takes ONE snapshot
//    per capture pass and derives everything it uses (stream size, whether
//    MJPEG is allowed, the output frame rate) from it.
//  * The detector publishes only through publish(). The epoch advances exactly
//    when the geometry changes.
//  * An operation that creates, reconfigures or submits to the JPEG channel
//    runs under a Lease taken with the snapshot it was derived from. lease()
//    refuses (empty Lease) when the epoch has moved, and while a lease is held
//    the geometry cannot change: a publish() that arrives meanwhile is recorded
//    as pending and becomes visible when the last lease ends. The guarded
//    operation therefore sees the geometry that was checked, and the change is
//    ordered after it. Checking the epoch and then acting, without the lease,
//    would leave a window between the check and the call; the lease closes it.
//
// Why a lease and not vi_mutex (publish under vi_mutex)
//  * The detector never takes vi_mutex, and kvmv_read_img holds it for the
//    whole call, including waits for frames and the encoder. Publishing under
//    it would delay detection by a full read and, if a reader sleeps with it
//    held, indefinitely; the detector must keep reporting.
//  * Here the mutex is a leaf lock held for a few instructions and never
//    across an operation, so there is no lock order to get wrong (vi_mutex may
//    be held outside it; nothing is acquired inside it) and publish() never
//    blocks on a lease. Detection is not starved; only the visibility of a
//    change waits for the guarded operation, which is bounded by one VPSS
//    configuration or one JPEG encode.
//
// What it cannot do: it orders the detector REPORT against the guarded
// operations. The physical input can still change at any moment, and the
// detector sees it only when it polls the bridge chip.
namespace nanokvm {

struct GeometrySnapshot {
    uint16_t width = 0;
    uint16_t height = 0;
    uint64_t epoch = 0;
};

class GeometryGate {
public:
    class Lease {
    public:
        Lease() = default;
        Lease(Lease &&other) noexcept : gate_(other.gate_) { other.gate_ = nullptr; }
        Lease &operator=(Lease &&other) noexcept {
            if (this != &other) { release(); gate_ = other.gate_; other.gate_ = nullptr; }
            return *this;
        }
        Lease(const Lease &) = delete;
        Lease &operator=(const Lease &) = delete;
        ~Lease() { release(); }
        explicit operator bool() const { return gate_ != nullptr; }
        void release() {
            if (gate_ != nullptr) { gate_->end_lease(); gate_ = nullptr; }
        }
    private:
        friend class GeometryGate;
        explicit Lease(GeometryGate *gate) : gate_(gate) {}
        GeometryGate *gate_ = nullptr;
    };

    // 0x0 is the unknown input: nothing is captured until a size is detected.
    explicit GeometryGate(uint16_t width = 0, uint16_t height = 0) {
        current_.width = width;
        current_.height = height;
    }

    GeometrySnapshot snapshot() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return current_;
    }

    // Detector side. Never waits for a lease. Returns whether the geometry
    // differs from the latest one published (visible or pending).
    bool publish(uint16_t width, uint16_t height) {
        std::lock_guard<std::mutex> lock(mutex_);
        const uint16_t latest_width = pending_valid_ ? pending_width_ : current_.width;
        const uint16_t latest_height = pending_valid_ ? pending_height_ : current_.height;
        const bool changed = latest_width != width || latest_height != height;
        if (leases_ > 0) {
            pending_width_ = width;
            pending_height_ = height;
            pending_valid_ = true;
        } else {
            apply(width, height);
        }
        return changed;
    }

    // Empty when the epoch moved since the snapshot was taken.
    Lease lease(const GeometrySnapshot &snapshot) {
        std::lock_guard<std::mutex> lock(mutex_);
        if (snapshot.epoch != current_.epoch) return Lease();
        ++leases_;
        return Lease(this);
    }

private:
    void apply(uint16_t width, uint16_t height) {
        if (width != current_.width || height != current_.height) {
            current_.width = width;
            current_.height = height;
            ++current_.epoch;
        }
    }
    void end_lease() {
        std::lock_guard<std::mutex> lock(mutex_);
        if (--leases_ == 0 && pending_valid_) {
            apply(pending_width_, pending_height_);
            pending_valid_ = false;
        }
    }

    mutable std::mutex mutex_;
    GeometrySnapshot current_;
    unsigned leases_ = 0;
    bool pending_valid_ = false;
    uint16_t pending_width_ = 0;
    uint16_t pending_height_ = 0;
};

enum class JpegVerdict { Allowed, Blocked, Stale };

struct JpegAdmission {
    JpegVerdict verdict;
    GeometryGate::Lease lease;   // held while the guarded operation runs
};

struct NoHook { void operator()() const {} };

// The decision kvmv_read_img takes before it creates, reconfigures or submits
// to the JPEG channel. Allowed carries the lease: keep it until the operation
// has returned. Blocked: the snapshot is larger than the encoder can take.
// Stale: the geometry changed after the snapshot; the caller releases what it
// holds and starts the pass again from a new snapshot. `between` runs after the
// policy check and before the lease; it is the seam where a test moves the
// detector.
template <class Between>
JpegAdmission admit_jpeg_step(GeometryGate &gate, const GeometrySnapshot &snapshot, Between &&between) {
    if (!mjpeg_input_allowed(snapshot.width, snapshot.height))
        return {JpegVerdict::Blocked, GeometryGate::Lease()};
    between();
    GeometryGate::Lease lease = gate.lease(snapshot);
    if (!lease) return {JpegVerdict::Stale, GeometryGate::Lease()};
    return {JpegVerdict::Allowed, std::move(lease)};
}

inline JpegAdmission admit_jpeg_step(GeometryGate &gate, const GeometrySnapshot &snapshot) {
    return admit_jpeg_step(gate, snapshot, NoHook());
}

}
