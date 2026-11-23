#include "input/control.hpp"

#include <fstream>
#include <mutex>
#include <sys/stat.h>

namespace nkos::input::control {

namespace {

using Clock = std::chrono::steady_clock;

constexpr const char *mode_file = "/etc/kvm/ai-control.mode";
constexpr auto manual_cooldown = std::chrono::seconds(2);

struct ModeCache {
	bool loaded = false, exists = false;
	off_t size = 0;
	struct timespec mtime{};
	ModeStatus status;
} cache;

// Coordinator state.
int manual_sessions = 0;
Clock::time_point manual_until{};
// Holders of a stable mode (AcquireStable); a mode transition waits for them.
int stable_holders = 0;

std::mutex write_lane;

void begin_manual() { manual_sessions++; }

void end_manual(bool start_cooldown)
{
	if (manual_sessions > 0) manual_sessions--;
	if (start_cooldown && manual_sessions == 0) manual_until = Clock::now() + manual_cooldown;
}

std::string trim(std::string s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

} // namespace

const char *mode_name(Mode mode)
{
	switch (mode) {
	case Mode::mcp: return "mcp";
	case Mode::picoclaw: return "picoclaw";
	default: return "off";
	}
}

ModeStatus mode_status()
{
	struct stat st{};
	bool exists = ::stat(mode_file, &st) == 0;
	if (cache.loaded && exists == cache.exists &&
	    (!exists || (st.st_size == cache.size && st.st_mtim.tv_sec == cache.mtime.tv_sec &&
	                 st.st_mtim.tv_nsec == cache.mtime.tv_nsec)))
		return cache.status;
	bool transitioning = cache.status.transitioning;
	cache = {};
	cache.loaded = true;
	cache.exists = exists;
	cache.status.transitioning = transitioning;
	if (!exists) {
		cache.status.mode = Mode::picoclaw;
		return cache.status;
	}
	cache.size = st.st_size;
	cache.mtime = st.st_mtim;
	std::ifstream in(mode_file);
	std::string text((std::istreambuf_iterator<char>(in)), {});
	text = trim(text);
	if (text == "off") cache.status.mode = Mode::off;
	else if (text == "mcp") cache.status.mode = Mode::mcp;
	else if (text == "picoclaw") cache.status.mode = Mode::picoclaw;
	else {
		cache.status.mode = Mode::off;
		cache.status.last_error = "invalid AI control mode \"" + text + "\"";
	}
	return cache.status;
}

bool Reservation::valid() const
{
	if (deadline_ != Clock::time_point{} && Clock::now() >= deadline_) return false;
	return session_ && generation_ != 0 && session_->live_.load() == generation_;
}

void Reservation::complete(bool success)
{
	if (done_ || !session_) return;
	done_ = true;
	session_->complete(generation_, kind_, held_, success, start_cooldown_);
}

std::shared_ptr<ManualSession> ManualSession::create() { return std::shared_ptr<ManualSession>(new ManualSession); }

ManualSession::~ManualSession()
{
	if (active_) end(true);
}

bool ManualSession::release_report(ReportKind kind, bool held) const
{
	if (held) return false;
	if (kind == ReportKind::keyboard) return held_[0];
	return held_[1] || held_[2];
}

std::shared_ptr<Reservation> ManualSession::reserve(ReportKind kind, bool held, bool start_cooldown,
                                                    const std::function<bool(Mode)> &allow)
{
	if (closed_) return nullptr;
	auto status = mode_status();
	auto make = [&] {
		auto r = std::make_shared<Reservation>();
		r->session_ = shared_from_this();
		r->generation_ = generation_;
		r->kind_ = kind;
		r->held_ = held;
		r->start_cooldown_ = start_cooldown;
		return r;
	};
	if (active_) {
		bool allowed = !status.transitioning && (!allow || allow(status.mode));
		// A release always passes: keys must come up.
		if (!allowed && !release_report(kind, held)) return nullptr;
		pending_++;
		start_cooldown = start_cooldown || release_report(kind, held);
		return make();
	}
	if (status.transitioning) return nullptr;
	if (allow && !allow(status.mode)) return nullptr;
	// AI input in flight is cancelled by its owner when it sees manual_active().
	begin_manual();
	stable_holders++;
	active_ = true;
	generation_++;
	live_ = generation_;
	pending_ = 1;
	return make();
}

void ManualSession::complete(std::uint64_t generation, ReportKind kind, bool held, bool success, bool start_cooldown)
{
	if (!active_ || generation != generation_) return;
	if (pending_ > 0) pending_--;
	if (start_cooldown) cooldown_on_idle_ = true;
	if (success) held_[static_cast<int>(kind)] = held;
	else held_[0] = held_[1] = held_[2] = false;
	finish_if_idle();
}

void ManualSession::finish_if_idle()
{
	if (!active_ || pending_ > 0 || held_[0] || held_[1] || held_[2]) return;
	bool cooldown = cooldown_on_idle_;
	active_ = false;
	live_ = 0;
	cooldown_on_idle_ = false;
	end(cooldown);
}

void ManualSession::end(bool cooldown)
{
	if (stable_holders > 0) stable_holders--;
	end_manual(cooldown);
}

void ManualSession::reset(ReportKind kind)
{
	if (!active_) return;
	held_[static_cast<int>(kind)] = false;
	if (!active_ || pending_ > 0 || held_[0] || held_[1] || held_[2]) return;
	active_ = false;
	live_ = 0;
	cooldown_on_idle_ = false;
	end(true);
}

void ManualSession::revoke()
{
	generation_++;
	if (!active_) return;
	pending_ = 0;
	held_[0] = held_[1] = held_[2] = false;
	cooldown_on_idle_ = false;
	active_ = false;
	live_ = 0;
	end(false);
}

void ManualSession::close()
{
	if (closed_) return;
	closed_ = true;
	pending_ = 0;
	held_[0] = held_[1] = held_[2] = false;
	cooldown_on_idle_ = false;
	bool was = active_;
	active_ = false;
	live_ = 0;
	generation_++;
	if (was) end(true);
}

void lock_write_lane() { write_lane.lock(); }
void unlock_write_lane() { write_lane.unlock(); }

bool manual_active() { return manual_sessions > 0 || Clock::now() < manual_until; }

} // namespace nkos::input::control
