#pragma once

// The host watchdog's decisions, as the Go server's hostwatchdog package, and the BMC power actions it presses
// (pkg/bmc Power: reset and cycle). The watchdog of a server's BMC without a timer the host resets: the computer
// sensors agent's reports over the USB network arm it, their silence makes it due only while nothing else explains
// the silence (power off, a new USB enumeration, sleep), and the caller asks the host (ARP) before it acts.

#include <cstdint>
#include <functional>
#include <string>
#include <utility>

namespace nkos::usb::watchdog {

inline constexpr const char *off = "off", *notify = "notify", *reset = "reset", *cycle = "cycle";
inline constexpr int default_minutes = 5, min_minutes = 2, max_minutes = 60;

struct Config {
	std::string action = off;
	long long minutes = default_minutes;
	bool operator==(const Config &) const = default;
};
std::string validate(const Config &c);
// Load: a missing, unreadable or invalid file is off after 5 minutes.
Config load(const std::string &path);
std::string save(const std::string &path, const Config &c);
std::string config_json(const Config &c);

// Times in nanoseconds of the wall clock, 0 for none.
using Nanos = std::int64_t;

struct Signs {
	Nanos now = 0;
	bool watching = false;
	Nanos report = 0;
	std::string report_link;
	bool power_on = false, power_known = false;
	bool configured = false;
	std::string link;
};

class Watchdog {
public:
	bool armed() const { return armed_ && !decided_; }
	bool step(const Signs &s, Nanos silence);
	bool still_due(const Signs &s, Nanos silence) const;
	void decided() { decided_ = true; }
	void reopen() { decided_ = false; }

private:
	bool watchable(const Signs &s) const;
	bool armed_ = false, decided_ = false;
	Nanos report_ = 0;
	std::string link_;
};

// What asking the host came to: alive, silent (the only evidence of a hang) or unknown, and why.
inline constexpr int arp_requests = 3;
std::pair<std::string, std::string> arp_verdict(int sent, bool answered, const std::string &error);

// The ATX lines as the BMC actions see them.
struct ATXState {
	bool power_button = false, reset_button = false, power_led = false, power_on = false;
};
// bmc.Power over hooks: state and press return "" or the error; sleep waits (and may fail).
struct Power {
	std::function<std::string(ATXState &)> state;
	std::function<std::string(const std::string &button, int ms)> press;
	std::function<std::string(int ms)> sleep;
	int hold_ms = 5000;  // DefaultHoldSeconds: the power menu's long press
	std::string reset_host();  // Power.Reset (ForceRestart)
	std::string cycle_host();  // Power.Cycle (PowerCycle)
};

} // namespace nkos::usb::watchdog
