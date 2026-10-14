#pragma once
#include <mutex>

// Parked VPSS outputs while nobody reads frames, and the capture restarts that
// can recreate them.
//
// Contract
//  * The parked flag and every enable/disable of the outputs change only under
//    this mutex: park and resume (set), and a capture restart (restart), which
//    recreates the outputs enabled and parks them again if parking is in
//    force. The caller also holds vi_mutex across every mutation and frame
//    operation; this mutex alone does not protect outstanding frame lifetimes.
//  * Lock order: vi_mutex, then this mutex. Nothing takes vi_mutex inside it.
//    The callbacks run MMF operations and must not call back into CapturePark.
namespace nanokvm {
class CapturePark {
public:
    // Runs restart(); on success, parked outputs are disabled again.
    template <class Restart, class SetEnabled>
    int restart(Restart &&restart, SetEnabled &&set_enabled) {
        std::lock_guard<std::mutex> lock(mutex_);
        const int result = restart();
        if (result == 0 && parked_) set_enabled(false);
        return result;
    }

    // Parking retires what holds output buffers first (retire), then disables
    // the outputs; a failure leaves them enabled and the capture not parked.
    // Resuming enables them. Either is a no-op when already in that state.
    template <class Retire, class SetEnabled>
    int set(bool park, Retire &&retire, SetEnabled &&set_enabled) {
        std::lock_guard<std::mutex> lock(mutex_);
        if (parked_ == park) return 0;
        if (park) {
            if (const int retired = retire()) return retired;
            if (const int disabled = set_enabled(false)) {
                set_enabled(true);
                return disabled;
            }
            parked_ = true;
            return 0;
        }
        if (const int enabled = set_enabled(true)) return enabled;
        parked_ = false;
        return 0;
    }

    bool parked() {
        std::lock_guard<std::mutex> lock(mutex_);
        return parked_;
    }

private:
    std::mutex mutex_;
    bool parked_ = false;
};
} // namespace nanokvm
