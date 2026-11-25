#include "usb/host_watchdog_core.hpp"

#include "common/power_action.hpp"
#include "config/config.hpp"
#include "usb/gojson_read.hpp"
#include "usb/gotext.hpp"

#include <cerrno>
#include <format>
#include <fstream>
#include <mutex>

namespace nkos::usb::watchdog {

namespace {

constexpr int press_ms = 800;  // a short press, as the page's power and reset buttons

const char *err_no_atx = "this board has no ATX power control";
const char *err_no_reset = "this board has no reset line";
const char *err_still_on = "the host did not switch off";
const char *err_action_busy = "another BMC power action is in progress";


} // namespace

std::string validate(const Config &c)
{
	if (c.action != off && c.action != notify && c.action != reset && c.action != cycle)
		return "action is off, notify, reset or cycle";
	if (c.minutes < min_minutes || c.minutes > max_minutes) return "the silence is 2 to 60 minutes";
	return {};
}

Config load(const std::string &path)
{
	Config off_config;
	std::ifstream in(path, std::ios::binary);
	if (!in) return off_config;
	std::string data((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
	Config c;
	c.action.clear();
	c.minutes = 0;
	std::string error;
	bool ok = goj::unmarshal(data, "Config", [&](goj::Reader &r) {
		r.object("Config", "hostwatchdog.Config", {
			{"action", [&](goj::Reader &x) { x.string(c.action); }},
			{"minutes", [&](goj::Reader &x) { x.integer(c.minutes); }},
		});
	}, error);
	if (!ok || !validate(c).empty()) return off_config;
	return c;
}

std::string config_json(const Config &c)
{
	return std::format(R"({{"action":{},"minutes":{}}})", gotext::json_string(c.action), c.minutes);
}

std::string save(const std::string &path, const Config &c)
{
	if (auto e = validate(c); !e.empty()) return e;
	if (!config::atomic_write(path, config_json(c) + "\n", 0644)) return "write " + path + ": " + gotext::errno_text(errno);
	return {};
}

bool Watchdog::watchable(const Signs &s) const
{
	return s.watching && s.configured && !s.link.empty() && !(s.power_known && !s.power_on);
}

bool Watchdog::step(const Signs &s, Nanos silence)
{
	if (!watchable(s)) {
		armed_ = false;
		return false;
	}
	if (s.report > report_) {
		// The link the report came over, not the one seen now: a report of the link before a new enumeration must
		// not arm the watchdog for the link after it.
		report_ = s.report;
		link_ = s.report_link;
		armed_ = true;
		decided_ = false;
	}
	if (!armed_ || decided_) return false;
	if (s.link != link_) {
		// Enumerated anew since the report: the host restarted or was plugged in again. Its next report arms again.
		armed_ = false;
		return false;
	}
	return s.now - report_ >= silence;
}

bool Watchdog::still_due(const Signs &s, Nanos silence) const
{
	return watchable(s) && armed_ && s.link == link_ && s.report == report_ && s.now - report_ >= silence;
}

std::pair<std::string, std::string> arp_verdict(int sent, bool answered, const std::string &error)
{
	if (answered) return {"alive", ""};
	if (!error.empty()) return {"unknown", "ARP on the USB network: " + error};
	if (sent < arp_requests)
		return {"unknown", std::format("ARP on the USB network: only {} of {} requests were sent", sent, arp_requests)};
	return {"silent", ""};
}

std::string Power::reset_host()
{
	std::unique_lock lock(power_action_mutex(), std::try_to_lock);
	if (!lock.owns_lock()) return err_action_busy;
	ATXState st;
	if (auto e = state(st); !e.empty()) return e;
	if (!st.reset_button) return err_no_reset;
	return press("reset", press_ms);
}

std::string Power::cycle_host()
{
	std::unique_lock lock(power_action_mutex(), std::try_to_lock);
	if (!lock.owns_lock()) return err_action_busy;
	ATXState st;
	if (auto e = state(st); !e.empty()) return e;
	if (!st.power_button) return err_no_atx;
	// A host known to be off is only switched on.
	if (st.power_led && !st.power_on) return press("power", press_ms);
	if (auto e = press("power", hold_ms); !e.empty()) return e;
	bool led = st.power_led;
	if (led) {
		// A host that stays on was not cycled; a short press now would only ask its system to shut down.
		bool is_off = false;
		for (int i = 0; i < 40 && !is_off; i++) {
			if (i > 0)
				if (auto e = sleep(250); !e.empty()) return e;
			if (auto e = state(st); !e.empty()) return e;
			is_off = !st.power_on;
		}
		if (!is_off) return err_still_on;
	}
	if (auto e = sleep(2000); !e.empty()) return e;
	return press("power", press_ms);
}

} // namespace nkos::usb::watchdog
