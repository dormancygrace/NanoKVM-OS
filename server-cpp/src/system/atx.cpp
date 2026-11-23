#include "system/atx.hpp"

#include "system/oled_state.hpp"
#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "http/router.hpp"
#include "config/config.hpp"
#include "system/gpio.hpp"
#include "system/hardware.hpp"

#include <cerrno>
#include <cstring>
#include <format>
#include <fstream>
#include <map>
#include <memory>
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
	oled::publish_atx(enabled());
	start_monitor();
}

void stop() { gpio::stop_led_monitor(); }

namespace {

// One event stream of the LEDs: a snapshot now and at every change ("data: null" while they cannot be read), a
// keepalive every 15 s; it ends when ATX is switched off.
struct LedEvents : std::enable_shared_from_this<LedEvents> {
	std::shared_ptr<http::Stream> stream;
	int subscription = 0;
	std::uint64_t generation = 0;

	void send()
	{
		auto s = gpio::stream_leds();
		stream->write(s ? std::format("data: {{\"pwr\":{},\"hdd\":{}}}\n\n", s->power, s->hdd) : "data: null\n\n");
	}
	void start()
	{
		auto weak = weak_from_this();
		subscription = gpio::subscribe_leds([weak] {
			if (auto self = weak.lock()) {
				if (!gpio::led_monitor_running()) {
					self->stream->write("data: null\n\n");
					self->stream->end();
					return self->stop();
				}
				self->send();
			}
		});
		stream->on_close = [weak] {
			if (auto self = weak.lock()) self->stop();
		};
		send();
		keepalive();
	}
	void keepalive()
	{
		loop::after(std::chrono::seconds(15), [weak = weak_from_this(), g = generation] {
			auto self = weak.lock();
			if (!self || self->generation != g || self->stream->closed()) return;
			self->stream->write(": keepalive\n\n");
			self->keepalive();
		});
	}
	void stop()
	{
		generation++;
		if (subscription) gpio::unsubscribe_leds(subscription);
		subscription = 0;
		stream->on_close = nullptr;
	}
};

std::map<LedEvents *, std::shared_ptr<LedEvents>> led_streams;

} // namespace

void add_routes(http::Router &router)
{
	router.add("GET", "/api/vm/gpio/events", auth::guarded([](http::Request &, http::Response &res) {
		if (!enabled()) {
			res.status = 204;
			res.done = true;
			return;
		}
		auto events = std::make_shared<LedEvents>();
		events->stream = std::make_shared<http::Stream>();
		res.content_type = "text/event-stream";
		res.set_header("Cache-Control", "no-store");
		res.set_header("X-Accel-Buffering", "no");
		res.stream = events->stream;
		res.done = true;
		led_streams[events.get()] = events;
		events->start();
		// The stream owns itself until it closes.
		auto close = events->stream->on_close;
		events->stream->on_close = [raw = events.get(), close] {
			if (close) close();
			led_streams.erase(raw);
		};
	}, {{}, false, true}));
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
		oled::publish_atx(enabled());
		if (*on) start_monitor();
		else gpio::stop_led_monitor();
		log::info("ATX power control {}", *on ? "on" : "off");
		api::send(res, 0, "success", std::format(R"({{"enabled":{}}})", enabled()));
	}, {{"admin"}}));
}

} // namespace nkos::system::atx
