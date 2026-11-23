#include "video/videogate.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"

#include <algorithm>
#include <chrono>
#include <vector>

namespace nkos::video::videogate {

struct Hold::State {
	bool granted = false, released = false;
	std::function<void()> on_granted;
};

namespace {

// How long a hold waits for cancelled background work before it logs that the video still waits; it then goes on
// waiting.
constexpr std::chrono::seconds yield_wait{5};

int holds = 0;
Work *active = nullptr;
// Never destroyed: holds kept by other statics end after this file's statics at exit.
std::vector<std::shared_ptr<Hold::State>> &waiting = *new std::vector<std::shared_ptr<Hold::State>>;

void warn_while_waiting(std::weak_ptr<Hold::State> weak, std::chrono::seconds waited)
{
	loop::after(yield_wait, [weak = std::move(weak), waited] {
		auto state = weak.lock();
		if (!state || state->granted || state->released) return;
		auto now = waited + yield_wait;
		log::warn("video waits for background TPU work to end ({}s)", now.count());
		warn_while_waiting(state, now);
	});
}

} // namespace

Work::Work() : cancel_(std::make_shared<std::atomic<bool>>(false)) {}

Work::~Work() { end(); }

void Work::end()
{
	if (ended_) return;
	ended_ = true;
	cancel_->store(true);
	if (active != this) return;
	active = nullptr;
	// Every hold that waited for this work goes on.
	auto granted = std::move(waiting);
	waiting.clear();
	for (auto &state : granted) {
		state->granted = true;
		loop::post([weak = std::weak_ptr<Hold::State>(state)] {
			auto s = weak.lock();
			if (!s || s->released || !s->on_granted) return;
			auto fn = std::move(s->on_granted);
			fn();
		});
	}
}

std::shared_ptr<Work> begin()
{
	if (holds > 0 || active) return nullptr;
	auto work = std::make_shared<Work>();
	active = work.get();
	return work;
}

Hold::~Hold()
{
	state_->released = true;
	state_->on_granted = nullptr;
	std::erase(waiting, state_);
	holds--;
}

bool Hold::granted() const { return state_->granted; }

std::unique_ptr<Hold> hold(std::function<void()> granted)
{
	holds++;
	auto state = std::make_shared<Hold::State>();
	std::unique_ptr<Hold> h(new Hold(state));
	if (!active) {
		state->granted = true;
		return h;
	}
	active->cancel();
	state->on_granted = std::move(granted);
	waiting.push_back(state);
	warn_while_waiting(state, std::chrono::seconds(0));
	return h;
}

} // namespace nkos::video::videogate
