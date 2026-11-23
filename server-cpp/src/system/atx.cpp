#include "system/atx.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "config/config.hpp"
#include "system/gpio.hpp"
#include "system/hardware.hpp"

#include <cerrno>
#include <cstring>
#include <format>
#include <fstream>
#include <sys/stat.h>

namespace nkos::system::atx {

namespace {

constexpr const char *setting_file = "/etc/kvm/atx";

bool board_default() { return hardware().board == "pcie"; }

bool write_setting(bool on) { return config::atomic_write(setting_file, on ? "on\n" : "off\n", 0644); }

void start_monitor()
{
	if (enabled()) gpio::start_led_monitor(hardware().gpio_power_led, hardware().gpio_hdd_led);
}

} // namespace

bool available() { return !hardware().gpio_power.empty(); }

bool enabled()
{
	if (!available()) return false;
	std::ifstream in(setting_file);
	std::string text;
	std::getline(in, text);
	while (!text.empty() && (text.back() == ' ' || text.back() == '\r' || text.back() == '\t')) text.pop_back();
	if (text == "on") return true;
	if (text == "off") return false;
	return board_default();
}

void start()
{
	struct stat st{};
	if (available() && ::stat(setting_file, &st) != 0 && errno == ENOENT && !write_setting(board_default()))
		log::error("ATX setting: {}", std::strerror(errno));
	start_monitor();
}

void stop() { gpio::stop_led_monitor(); }

void add_routes(http::Router &router)
{
	router.add("GET", "/api/vm/atx", auth::guarded([](http::Request &, http::Response &res) {
		// gin.H: keys in order.
		api::send(res, 0, "success",
		          std::format(R"({{"available":{},"board":{},"default":{},"enabled":{}}})", available(),
		                      api::quote(hardware().board), board_default(), enabled()));
	}));
	router.add("POST", "/api/vm/atx", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = req.header("content-type").starts_with("application/json") ? api::Params::bind(req) : std::nullopt;
		std::optional<bool> on;
		if (params) on = params->boolean("enabled", ok);
		if (!params || !ok || !on) return api::err(res, -1, "enabled is required");
		if (*on && !available()) return api::err(res, -2, "this board has no known ATX lines");
		if (!write_setting(*on)) {
			log::error("ATX setting: {}", std::strerror(errno));
			return api::err(res, -3, "the setting could not be saved");
		}
		if (*on) start_monitor();
		else gpio::stop_led_monitor();
		log::info("ATX power control {}", *on ? "on" : "off");
		api::send(res, 0, "success", std::format(R"({{"enabled":{}}})", enabled()));
	}, {{"admin"}}));
}

} // namespace nkos::system::atx
