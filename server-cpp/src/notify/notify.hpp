#pragma once

// Notifications, as the Go server's service/notify and router/notify.go: the device's events (host power, HDMI
// signal, login security, the host watchdog, a crash on the host's screen) go to the channels set on the
// Notifications page (Telegram, e-mail, chat webhooks, push services, syslog) and to the journal. Producers call
// emit from any thread; it never blocks them.

#include "http/router.hpp"
#include "notify/model.hpp"

#include <functional>
#include <optional>
#include <string>

namespace nkos::notify {

// Queues an event for the device's channels (and the journal).
void emit(Event e);
// Whether an enabled channel takes events of kind, so a producer can skip work nobody would hear about.
bool wants(std::string_view kind);

// The lockout of an address after failures failed logins; logins from it are refused for minutes.
void login_locked_out(const std::string &address, int failures, int minutes);
// An address made attempts logins and further ones are refused for minutes (the IPMI limit).
void login_attempts_limited(const std::string &address, int attempts, int minutes);
// A login from an address the user has not logged in from before (the first login of a user is remembered without
// a report). Reads and writes /etc/kvm/notify/addresses.json off the caller's thread.
void login_succeeded(const std::string &user, const std::string &address);

// GET/PUT /api/notify/config, POST /api/notify/preview, /api/notify/oauth/start, /api/notify/oauth/finish,
// /api/notify/test (administrators).
void add_routes(http::Router &router);

// The host watch (StartNotifyWatch): power LED and HDMI signal changes, sampled every second on the loop thread.
// hdmi_signal: the signal while capture runs, nullopt while capture is off or stopped for idling. Loop thread.
void start_host_watch(std::function<std::optional<bool>()> hdmi_signal);

// The debouncing of the host watch (a new state counts once it has held for hold; the first sample is the baseline).
struct Debounce {
	std::chrono::milliseconds hold{};
	bool known = false, state = false, pending = false;
	std::chrono::steady_clock::time_point since{};
	bool update(bool value, std::chrono::steady_clock::time_point now);
	void reset() { known = false; }
};

// hostWatch: turns samples of the power LED and the HDMI signal into events.
struct HostWatch {
	std::function<std::optional<bool>()> power, hdmi;
	std::function<void(Event)> emit;
	Debounce power_state{std::chrono::seconds(5)}, hdmi_state{std::chrono::seconds(10)};
	std::chrono::steady_clock::time_point power_at{}, hdmi_since{};
	void step(std::chrono::steady_clock::time_point now);
};

} // namespace nkos::notify
