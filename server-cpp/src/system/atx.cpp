#include "system/atx.hpp"
#include "usb/core_hooks.hpp"

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

#include "input/ws.hpp"
#include "system/journal.hpp"

#include <atomic>
#include <cerrno>
#include <chrono>
#include <mutex>
#include <thread>
#include <cstring>
#include <format>
#include <fstream>
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

// One press at a time; the line a press holds is let go at shutdown.
std::atomic<bool> pressing{false};
std::mutex held_mutex;
gpio::Line *held = nullptr;
bool stopped = false;

// "" or the error. Blocking: the press lasts its full length.
std::string pulse(const std::string &device, std::chrono::milliseconds duration)
{
	if (duration.count() <= 0 || duration > std::chrono::minutes(1)) return "ATX duration must be between 1 ns and 60 seconds";
	if (pressing.exchange(true)) return "another ATX pulse is in progress";
	struct Done {
		~Done() { pressing = false; }
	} done;
	std::string error;
	auto line = gpio::Line::open(device, true, error);
	if (!line) return error;
	{
		std::lock_guard lock(held_mutex);
		if (stopped) return "the server is stopping";
		held = &*line;
		if (!line->set(true, error)) {
			std::string release_error;
			line->set(false, release_error);
			held = nullptr;
			return error;
		}
	}
	// A started press runs its full length: a long press cut short only presses briefly.
	std::this_thread::sleep_for(duration);
	std::lock_guard lock(held_mutex);
	held = nullptr;
	if (!line->set(false, error)) return error;
	return "";
}

} // namespace

std::string pulse_line(const std::string &device, std::chrono::milliseconds duration) { return pulse(device, duration); }

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

void stop()
{
	gpio::stop_led_monitor();
	// No button stays pressed when the server ends.
	std::lock_guard lock(held_mutex);
	stopped = true;
	std::string error;
	if (held && !held->set(false, error)) log::error("cannot release the ATX buttons: {}", error);
}

namespace {

// One event stream of the LEDs: a snapshot now and at every change ("data: null" while they cannot be read), a
// keepalive every 15 s; it ends when ATX is switched off.
struct LedEvents : std::enable_shared_from_this<LedEvents> {
	std::shared_ptr<http::Stream> stream;
	int subscription = 0;

	void send()
	{
		auto s = gpio::stream_leds();
		stream->write(s ? std::format("data: {{\"pwr\":{},\"hdd\":{}}}\n\n", s->power, s->hdd) : "data: null\n\n");
	}
	void start(const http::Request &request)
	{
		subscription = gpio::subscribe_leds([self = shared_from_this()] {
			if (!gpio::led_monitor_running()) {
				self->stream->write("data: null\n\n");
				self->stream->end();
				return self->stop();
			}
			self->send();
		});
		auto session = auth::join_session(request, [stream = stream] { stream->abort(); });
		stream->on_close = [weak = weak_from_this(), session] {
			auth::leave_session(session);
			if (auto self = weak.lock()) self->stop();
		};
		send();
		keepalive();
	}
	void keepalive()
	{
		loop::after(std::chrono::seconds(15), [weak = weak_from_this()] {
			auto self = weak.lock();
			if (!self || !self->subscription || self->stream->closed()) return;
			self->stream->write(": keepalive\n\n");
			self->keepalive();
		});
	}
	void stop()
	{
		if (subscription) gpio::unsubscribe_leds(subscription);
		subscription = 0;
	}
};

} // namespace

void add_routes(http::Router &router)
{
	router.add("GET", "/api/vm/gpio/events", auth::guarded([](http::Request &req, http::Response &res) {
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
		events->start(req);
	}, {{}, false, true}));
	router.add("GET", "/api/vm/atx", auth::guarded([](http::Request &, http::Response &res) {
		// gin.H: keys in order.
		api::send(res, 0, "success",
		          std::format(R"({{"available":{},"board":{},"default":{},"enabled":{}}})", available(),
		                      api::quote(hardware().board), board_default(), enabled()));
	}));
	router.add("GET", "/api/vm/gpio", auth::guarded([](http::Request &, http::Response &res) {
		if (!enabled()) return api::err(res, -1, "ATX power control is off (System > Device)");
		auto s = gpio::current_leds();
		if (!s) return api::err(res, -2, "failed to read ATX LEDs: ATX LED monitor stopped");
		api::send(res, 0, "success", std::format(R"({{"pwr":{},"hdd":{}}})", s->power, s->hdd));
	}));
	router.add("POST", "/api/vm/gpio", auth::guarded([](http::Request &req, http::Response &res) {
		if (!input::ws::allows_input_lease(req.header("x-nanokvm-input-lease")))
			return api::err(res, -4, "another session holds input control");
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<std::string> type;
		long long duration_ms = 0;
		if (params) {
			type = params->string("type", ok);
			duration_ms = params->integer("duration", ok).value_or(0);
		}
		if (!params || !ok || !type || type->empty() || duration_ms < 0)
			return api::err(res, -1, "invalid arguments: Key: 'SetGpioReq.Type' Error:Field validation for 'Type' failed on the 'required' tag");
		std::string device;
		if (*type == "power") device = enabled() ? hardware().gpio_power : "";
		else if (*type == "reset") device = enabled() ? hardware().gpio_reset : "";
		else return api::err(res, -2, "invalid power event: " + *type);
		if (device.empty()) return api::err(res, -2, "ATX power control is off (System > Device)");
		if (duration_ms > 60000) return api::err(res, -1, "ATX duration must not exceed 60000 ms");
		auto duration = std::chrono::milliseconds(duration_ms > 0 ? duration_ms : 800);
		std::string actor = req.auth ? req.auth->username + " (web)" : "";
		bool reset = device == hardware().gpio_reset;
		http::defer(req, res, [device, duration, actor, reset](http::Response &out) {
			if (auto e = pulse(device, duration); !e.empty()) return api::err(out, -3, "operation failed: " + e);
			journal::action("action.button",
			                {{"button", reset ? "reset" : "power"}, {"seconds", std::format("{:.1f}", duration.count() / 1000.0)}}, actor);
			api::ok(out);
		});
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
