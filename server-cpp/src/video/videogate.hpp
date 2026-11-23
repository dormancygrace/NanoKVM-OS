#pragma once

// Background work on the TPU gives way to video, as the Go server's internal/videogate. The OCR's models take the
// memory a viewer's video needs, so whatever wakes the capture or starts an encoder for a viewer holds the gate first
// (hold): that cancels the background work, and the video goes on only once the work has ended; no background work
// starts while a hold is on (begin). Loop thread only, except Work::cancelled and the cancel flag.

#include <atomic>
#include <functional>
#include <memory>

namespace nkos::video::videogate {

// Background work admitted by begin. Cancelled when video holds the gate; end once the work and its resources are
// gone (the destructor ends it too).
class Work {
public:
	Work();
	~Work();
	Work(const Work &) = delete;
	Work &operator=(const Work &) = delete;
	bool cancelled() const { return cancel_->load(); }
	// For a blocking thread (process::Options::cancel).
	const std::shared_ptr<std::atomic<bool>> &cancel_flag() const { return cancel_; }
	void cancel() { cancel_->store(true); }
	void end();

private:
	std::shared_ptr<std::atomic<bool>> cancel_;
	bool ended_ = false;
};

// Starts background work; nullptr while video holds the gate or other background work runs.
std::shared_ptr<Work> begin();

// A hold of the gate, released when destroyed. granted() at once when no background work ran; otherwise the work was
// cancelled and the callback given to hold runs (on the loop, later) once it has ended. A hold destroyed before that
// never calls it.
class Hold {
public:
	~Hold();
	Hold(const Hold &) = delete;
	Hold &operator=(const Hold &) = delete;
	bool granted() const;

	struct State;

private:
	friend std::unique_ptr<Hold> hold(std::function<void()> granted);
	explicit Hold(std::shared_ptr<State> state) : state_(std::move(state)) {}
	std::shared_ptr<State> state_;
};

std::unique_ptr<Hold> hold(std::function<void()> granted);

} // namespace nkos::video::videogate
