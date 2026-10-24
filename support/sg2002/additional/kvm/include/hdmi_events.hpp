// SPDX-License-Identifier: GPL-3.0-or-later
#pragma once
#include "hdmi_model.hpp"
#include "internal/gpio_edges.hpp"

namespace nanokvm {
enum class HdmiPollReason { None, Initial, Edge, Resync, Retry, Fallback };
struct HdmiPollRequest {
    HdmiPollReason reason = HdmiPollReason::None;
    bool edge = false, resync = false;
    explicit operator bool() const { return reason != HdmiPollReason::None; }
};
inline const char *hdmi_poll_reason(HdmiPollReason reason) {
    switch (reason) {
    case HdmiPollReason::Initial: return "initial";
    case HdmiPollReason::Edge: return "edge";
    case HdmiPollReason::Resync: return "resync";
    case HdmiPollReason::Retry: return "retry";
    case HdmiPollReason::Fallback: return "fallback";
    default: return "none";
    }
}

// A successful initial/resync read closes the subscribe/snapshot race.
// Pending edges survive failed/paused/settling reads. No healthy timer reads.
class HdmiPollSchedule {
    bool available_ = false, initial_ = true, edge_ = false, lost_ = false;
    bool attempted_ = false, failed_ = false;
    uint64_t last_ = 0;
public:
    void observe(ReceiverKind kind, bool available, const GpioEdgeBatch &batch) {
        if (available != available_) lost_ = true;
        available_ = available;
        edge_ |= kind == ReceiverKind::C ? batch.rising : batch.falling;
        lost_ |= batch.lost;
    }
    HdmiPollRequest next(uint64_t now, bool retry) {
        const bool recovery = failed_ || retry;
        if (attempted_ && now - last_ < (recovery || !available_ ? 1000U : 100U))
            return {};
        if (!initial_ && !edge_ && !lost_ && !recovery && available_) return {};
        const auto reason = initial_ ? HdmiPollReason::Initial :
            edge_ ? HdmiPollReason::Edge : lost_ ? HdmiPollReason::Resync :
            recovery ? HdmiPollReason::Retry : HdmiPollReason::Fallback;
        attempted_ = true; last_ = now;
        return {reason, edge_, lost_ || initial_};
    }
    void completed(bool success) {
        failed_ = !success;
        if (success) initial_ = edge_ = lost_ = false;
    }
};

// The controller may be missing, busy, removed or re-enumerated. Retry GPIO
// acquisition slowly while the schedule retains the existing 1s I2C fallback.
template<class Source = GpioEdges<>> class HdmiEvents {
    Source source_;
    bool tried_ = false;
    uint64_t last_connect_ = 0;
    HdmiPollSchedule schedule_;
public:
    bool active() const { return source_.active(); }
    int error() const { return source_.error(); }
    void observe(ReceiverKind kind, uint64_t now) {
        const bool before = source_.active();
        if (!before && (!tried_ || now - last_connect_ >= 5000U)) {
            tried_ = true; last_connect_ = now;
            source_.connect();
        }
        const auto batch = source_.drain();
        if (before && !source_.active()) last_connect_ = now;
        schedule_.observe(kind, source_.active(), batch);
    }
    HdmiPollRequest next(uint64_t now, bool retry) { return schedule_.next(now, retry); }
    void completed(bool success) { schedule_.completed(success); }
};
} // namespace nanokvm
