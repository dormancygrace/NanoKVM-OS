#include "bmc/config.hpp"

#include "common/gojson.hpp"
#include "common/power_action.hpp"
#include "network/util.hpp"
#include "system/atx.hpp"
#include "system/gpio.hpp"
#include "system/hardware.hpp"
#include "system/journal.hpp"

#include <atomic>
#include <cctype>
#include <cstdio>
#include <sys/stat.h>
#include <cerrno>
#include <fcntl.h>
#include <random>
#include <thread>
#include <unistd.h>

namespace nkos::bmc {

namespace {

bool valid_username(const std::string &s)
{
	if (s.empty() || s.size() > 16) return false;
	for (unsigned char c : s)
		if (!std::isalnum(c) && c != '.' && c != '_' && c != '-') return false;
	return true;
}

// The runes of valid UTF-8 (the caller checked it).
std::u32string runes(const std::string &s)
{
	std::u32string out;
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		int n = c < 0x80 ? 1 : c >= 0xf0 ? 4 : c >= 0xe0 ? 3 : 2;
		char32_t cp = n == 1 ? c : n == 2 ? c & 31 : n == 3 ? c & 15 : c & 7;
		for (int k = 1; k < n && i + k < s.size(); k++) cp = (cp << 6) | (static_cast<unsigned char>(s[i + k]) & 0x3f);
		out += cp;
		i += static_cast<std::size_t>(n);
	}
	return out;
}


} // namespace

Error Error::of(Kind kind)
{
	switch (kind) {
	case no_atx: return {kind, "this board has no ATX power control"};
	case no_reset: return {kind, "this board has no reset line"};
	case still_on: return {kind, "the host did not switch off"};
	case busy: return {kind, "another BMC power action is in progress"};
	case unknown_action: return {kind, "unknown BMC power action"};
	default: return {};
	}
}

std::string validate(const Config &c)
{
	if (!valid_username(c.username)) return "the user name must have 1-16 letters, digits, '.', '_' or '-'";
	if ((c.redfish || c.ipmi) && c.password.size() < 8) return "the password must have at least 8 characters";
	if (c.password.size() > 20) return "the password must have at most 20 bytes (IPMI limit)";
	if (!network::valid_utf8(c.password)) return "the password must be valid UTF-8";
	for (char32_t r : runes(c.password))
		if (r < 0x20 || r == 0x7f) return "the password must not contain control characters";
	if (c.ipmi_port < 1 || c.ipmi_port > 65535) return "the IPMI port must be 1-65535";
	if (c.hold_seconds < 0 || c.hold_seconds > 30)
		return "the power button hold must be 1-30 s, or 0 for the power menu's long press";
	return {};
}

Config load(const std::string &path, std::string &error)
{
	error.clear();
	Config c;
	network::Error err;
	auto data = network::read_file(path, &err);
	if (!data) {
		if (!err.not_exist()) error = err.text;
		return c;
	}
	// json.Unmarshal into the defaults: keys matched exactly, else without regard to case.
	auto whole = network::parse_json_whole(*data);
	network::Binder b = network::Binder::bind({"PUT", "application/json", "", *data}, true);
	if (whole && b.ok()) {
		b.boolean("redfish", "", c.redfish);
		b.boolean("ipmi", "", c.ipmi);
		b.str("username", "", c.username);
		b.str("password", "", c.password);
		b.integer("ipmiPort", "", c.ipmi_port);
		b.integer("holdSeconds", "", c.hold_seconds);
		b.boolean("powerLedDisconnected", "", c.power_led_disconnected);
		b.boolean("virtualMedia", "", c.virtual_media);
	}
	if (!whole || !b.ok()) {
		error = "read " + path + ": the settings are not valid JSON";
		return Config{};
	}
	return c;
}

std::string marshal(const Config &c)
{
	std::string out = "{\n";
	out += "  \"redfish\": " + std::string(c.redfish ? "true" : "false") + ",\n";
	out += "  \"ipmi\": " + std::string(c.ipmi ? "true" : "false") + ",\n";
	out += "  \"username\": " + gojson::quote(c.username) + ",\n";
	out += "  \"password\": " + gojson::quote(c.password) + ",\n";
	out += "  \"ipmiPort\": " + std::to_string(c.ipmi_port) + ",\n";
	out += "  \"holdSeconds\": " + std::to_string(c.hold_seconds);
	if (c.power_led_disconnected) out += ",\n  \"powerLedDisconnected\": true";
	if (c.virtual_media) out += ",\n  \"virtualMedia\": true";
	return out + "\n}";
}

std::string save(const std::string &path, const Config &c)
{
	if (auto e = validate(c); !e.empty()) return e;
	std::string data = marshal(c) + "\n";
	std::string dir = network::dir_name(path);
	if (auto e = network::mkdir_all(dir, 0700)) return e.text;
	// os.CreateTemp(dir, ".config-"): a random suffix, created exclusively with mode 0600.
	std::string name;
	int fd = -1;
	std::random_device rd;
	for (int i = 0; i < 10000 && fd < 0; i++) {
		name = dir + "/.config-" + std::to_string(rd() % 4294967295u);
		fd = ::open(name.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd < 0 && errno != EEXIST) break;
	}
	if (fd < 0) return "createtemp " + dir + "/.config-*: " + network::errno_text(errno);
	std::string error;
	if (::fchmod(fd, 0600) != 0) error = network::path_error("chmod", name, errno).text;
	for (std::size_t done = 0; error.empty() && done < data.size();) {
		// This credential file must be seekable. A positional write cannot send its contents to a socket.
		ssize_t n = ::pwrite(fd, data.data() + done, data.size() - done, static_cast<off_t>(done));
		if (n <= 0) {
			if (n < 0 && errno == EINTR) continue;
			error = network::path_error("write", name, n < 0 ? errno : EIO).text;
			break;
		}
		done += static_cast<std::size_t>(n);
	}
	if (error.empty() && ::fsync(fd) != 0) error = network::path_error("sync", name, errno).text;
	if (::close(fd) != 0 && error.empty()) error = network::path_error("close", name, errno).text;
	if (error.empty()) {
		if (auto e = network::rename(name, path)) error = e.text;
	}
	::unlink(name.c_str());
	return error;
}

bool power_led_connected(const std::string &path)
{
	std::string error;
	Config c = load(path, error);
	return !error.empty() || !c.power_led_disconnected;
}

// --- the board's ATX lines ---------------------------------------------------------------------------------------

namespace {

// One press of a line, shared with the web page's ATX buttons.
std::string pulse(const std::string &device, std::chrono::milliseconds duration) { return system::atx::pulse_line(device, duration); }

struct BoardAtx : Atx {
	// The ATX lines while ATX is on (System > Device); none while it is off.
	static system::Hardware lines()
	{
		system::Hardware h = system::hardware();
		if (!system::atx::enabled()) h.gpio_power = h.gpio_reset = h.gpio_power_led = h.gpio_hdd_led = "";
		return h;
	}
	Error state(AtxState &out) override
	{
		auto h = lines();
		bool led_line = !h.gpio_power_led.empty();
		out = AtxState{};
		out.power_button = !h.gpio_power.empty();
		out.reset_button = !h.gpio_reset.empty();
		out.power_led = led_line && power_led_connected(config_path);
		if (led_line) {
			auto leds = system::gpio::current_leds();
			if (!leds) return Error::message("ATX LED monitor stopped");
			out.hdd = leds->hdd;
			if (out.power_led) out.power_on = leds->power;
		}
		return {};
	}
	Error press(const std::string &button, int duration_ms, const std::string &actor) override
	{
		auto h = lines();
		std::string device;
		if (button == "power") {
			device = h.gpio_power;
			if (device.empty()) return Error::of(Error::no_atx);
		} else if (button == "reset") {
			device = h.gpio_reset;
			if (device.empty()) return Error::of(Error::no_reset);
		} else {
			return Error::message("unknown button");
		}
		if (duration_ms < 100 || duration_ms > 60000) return Error::message("the press must last 100-60000 ms");
		if (auto e = pulse(device, std::chrono::milliseconds(duration_ms)); !e.empty()) return Error::message(e);
		char seconds[32];
		std::snprintf(seconds, sizeof seconds, "%.1f", duration_ms / 1000.0);
		system::journal::action("action.button", {{"button", device == h.gpio_reset ? "reset" : "power"}, {"seconds", seconds}},
		                        actor);
		return {};
	}
};

} // namespace

Atx &board_atx()
{
	static BoardAtx atx;
	return atx;
}

// --- power ---------------------------------------------------------------------------------------------------------

int Power::hold_ms() const
{
	auto hold = hold_.count() > 0 ? hold_ : std::chrono::milliseconds(default_hold_seconds * 1000);
	return static_cast<int>(hold.count());
}

void Power::wait(std::chrono::milliseconds d)
{
	if (sleep) sleep(d);
	else std::this_thread::sleep_for(d);
}

Error Power::checked_state(AtxState &state)
{
	if (auto e = atx_.state(state)) return e;
	if (!state.power_button) return Error::of(Error::no_atx);
	return {};
}

Error Power::status(bool &on, bool &known)
{
	AtxState s;
	if (auto e = atx_.state(s)) {
		on = known = false;
		return e;
	}
	on = s.power_on;
	known = s.power_led;
	return {};
}

Error Power::available(bool &power, bool &reset)
{
	AtxState s;
	auto e = atx_.state(s);
	power = s.power_button;
	reset = s.reset_button;
	return e;
}

Error Power::on()
{
	AtxState s;
	if (auto e = checked_state(s)) return e;
	if (s.power_led && s.power_on) return {};
	return atx_.press("power", press_ms, actor_);
}

Error Power::force_off()
{
	AtxState s;
	if (auto e = checked_state(s)) return e;
	if (s.power_led && !s.power_on) return {};
	return atx_.press("power", hold_ms(), actor_);
}

Error Power::soft()
{
	AtxState s;
	if (auto e = checked_state(s)) return e;
	if (s.power_led && !s.power_on) return {};
	return atx_.press("power", press_ms, actor_);
}

Error Power::push()
{
	AtxState s;
	if (auto e = checked_state(s)) return e;
	return atx_.press("power", press_ms, actor_);
}

Error Power::reset()
{
	AtxState s;
	if (auto e = atx_.state(s)) return e;
	if (!s.reset_button) return Error::of(Error::no_reset);
	return atx_.press("reset", press_ms, actor_);
}

Error Power::cycle()
{
	AtxState s;
	if (auto e = checked_state(s)) return e;
	if (s.power_led && !s.power_on) return atx_.press("power", press_ms, actor_);
	if (auto e = atx_.press("power", hold_ms(), actor_)) return e;
	if (s.power_led) {
		// A host that stays on was not cycled; a short press now would only ask its system to shut down.
		bool off = false;
		for (int i = 0; i < 40 && !off; i++) {
			if (i > 0) wait(std::chrono::milliseconds(250));
			if (auto e = atx_.state(s)) return e;
			off = !s.power_on;
		}
		if (!off) return Error::of(Error::still_on);
	}
	wait(std::chrono::seconds(2));
	return atx_.press("power", press_ms, actor_);
}

Error Power::act(const std::string &action)
{
	Error (Power::*run)() = nullptr;
	if (action == "On") run = &Power::on;
	else if (action == "ForceOff") run = &Power::force_off;
	else if (action == "GracefulShutdown") run = &Power::soft;
	else if (action == "PushPowerButton") run = &Power::push;
	else if (action == "ForceRestart") run = &Power::reset;
	else if (action == "PowerCycle") run = &Power::cycle;
	if (!run) return Error::of(Error::unknown_action);
	// One physical host: overlapping operations are refused rather than queued.
	std::unique_lock lock(power_action_mutex(), std::try_to_lock);
	if (!lock.owns_lock()) return Error::of(Error::busy);
	return (this->*run)();
}

} // namespace nkos::bmc
