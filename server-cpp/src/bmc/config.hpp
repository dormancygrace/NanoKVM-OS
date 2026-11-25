#pragma once

// What Redfish and IPMI share, as the Go server's pkg/bmc: the settings file (/etc/kvm/bmc/config.json, root only:
// with IPMI on the password is kept as written, since RMCP+ uses it as the key of its handshake) and the power logic
// that maps BMC power requests to presses of the host's buttons.

#include <chrono>
#include <functional>
#include <mutex>
#include <string>

namespace nkos::bmc {

inline constexpr const char *config_path = "/etc/kvm/bmc/config.json";
inline constexpr int default_ipmi_port = 623;
// The power menu's long press (web/src/pages/desktop/menu/power/power-long.tsx).
inline constexpr int default_hold_seconds = 5;
// A short press, as the web page's power and reset buttons.
inline constexpr int press_ms = 800;

struct Config {
	bool redfish = false;
	bool ipmi = false;
	std::string username = "admin";
	std::string password;
	long long ipmi_port = default_ipmi_port;
	// How long a forced power-off holds the power button; 0 is the power menu's long press.
	long long hold_seconds = 0;
	// The power LED header is not wired to the host: the power state is unknown and every request presses.
	bool power_led_disconnected = false;
	// Redfish also offers the virtual drive.
	bool virtual_media = false;
};

// Empty when valid, else the message.
std::string validate(const Config &c);
// The settings file; a missing file gives the defaults. Empty error on success.
Config load(const std::string &path, std::string &error);
// Validates, then writes the file atomically (0600, MarshalIndent with two spaces and a newline).
std::string save(const std::string &path, const Config &c);
// The file's JSON, as the Go server writes it.
std::string marshal(const Config &c);
// Unreadable settings trust the LED, as before the setting existed.
bool power_led_connected(const std::string &path);

// The host's power lines as the BMC protocols see them.
struct AtxState {
	bool power_button = false, reset_button = false, power_led = false;
	bool power_on = false;  // the power LED; meaningful only with power_led
	bool hdd = false;
};

// An error of the power logic; kind tells the sentinel errors apart.
struct Error {
	enum Kind { none, no_atx, no_reset, still_on, busy, unknown_action, other } kind = none;
	std::string text;
	explicit operator bool() const { return kind != none; }
	static Error of(Kind kind);
	static Error message(std::string text) { return {other, std::move(text)}; }
};

// The host's buttons and LED: the board's lines, or a double in a check.
struct Atx {
	virtual ~Atx() = default;
	virtual Error state(AtxState &out) = 0;
	// Holds a button ("power", "reset") and returns when it is released; actor is who asked (journal).
	virtual Error press(const std::string &button, int duration_ms, const std::string &actor) = 0;
};

// The board's ATX lines with the ATX setting (System > Device), as the Go server's vm.ATX: the same GPIO lines and
// LED monitor as the web page. config is the settings file holding "power LED connected".
Atx &board_atx();

// Power maps BMC power requests to button presses; both protocols use it. With a power LED a request that is already
// met presses nothing; without one, every request presses. One action at a time across every Power.
class Power {
public:
	Power(Atx &atx, std::chrono::milliseconds hold, std::string actor) : atx_(atx), hold_(hold), actor_(std::move(actor)) {}
	// Whether the host is on, and whether that is known (a board with a power LED).
	Error status(bool &on, bool &known);
	// Whether the board can switch the host at all.
	Error available(bool &power, bool &reset);
	// "On", "ForceOff", "GracefulShutdown", "PushPowerButton", "ForceRestart", "PowerCycle".
	Error act(const std::string &action);

	// For checks: the pause between the steps of a power cycle.
	std::function<void(std::chrono::milliseconds)> sleep;

private:
	Error checked_state(AtxState &state);
	int hold_ms() const;
	void wait(std::chrono::milliseconds d);
	Error on();
	Error force_off();
	Error soft();
	Error push();
	Error reset();
	Error cycle();
	Atx &atx_;
	std::chrono::milliseconds hold_;
	std::string actor_;
};

} // namespace nkos::bmc
