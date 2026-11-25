#include "notify/notify.hpp"

namespace nkos::notify {

namespace {

// HDMI changes this soon after a power change are part of it; after capture starts, the signal settles first.
constexpr auto hdmi_after_power = std::chrono::minutes(2);
constexpr auto hdmi_settle = std::chrono::seconds(10);

} // namespace

bool Debounce::update(bool value, std::chrono::steady_clock::time_point now)
{
	if (!known) {
		known = true;
		state = pending = value;
		return false;
	}
	if (value == state) {
		pending = value;
		return false;
	}
	if (value != pending) {
		pending = value;
		since = now;
		return false;
	}
	if (now - since < hold) return false;
	state = value;
	return true;
}

void HostWatch::step(std::chrono::steady_clock::time_point now)
{
	using TP = std::chrono::steady_clock::time_point;
	// Power, always: the journal records power changes whether or not a channel takes them.
	if (auto on = power(); !on) {
		// Starting afresh, a LED that never changes again does not count.
		power_state.reset();
		power_at = TP{};
	} else if (power_state.update(*on, now)) {
		power_at = now;
		if (*on) emit({std::string(kind_power), Severity::info, "power.on"});
		else emit({std::string(kind_power), Severity::warning, "power.off"});
	}
	// HDMI while capture runs, for whatever reason.
	auto signal = hdmi();
	if (!signal) {
		hdmi_state.reset();
		hdmi_since = TP{};
		return;
	}
	if (hdmi_since == TP{}) hdmi_since = now;
	if (now - hdmi_since < hdmi_settle || !hdmi_state.update(*signal, now)) return;
	// A host that is off or has just changed its power sends no news by losing or regaining its picture. Only a
	// power LED seen to change counts: one that is not wired reads off for good.
	if (power_at != TP{} && power_state.known && (!power_state.state || now - power_at < hdmi_after_power)) return;
	if (*signal) emit({std::string(kind_hdmi), Severity::info, "hdmi.restored"});
	else emit({std::string(kind_hdmi), Severity::warning, "hdmi.lost"});
}

} // namespace nkos::notify
