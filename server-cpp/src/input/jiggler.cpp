#include "input/jiggler.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "input/control.hpp"
#include "input/hid.hpp"

#include <glaze/glaze.hpp>

#include <chrono>
#include <cstdio>
#include <format>
#include <functional>
#include <fstream>
#include <memory>
#include <string>
#include <sys/stat.h>

namespace nkos::input::jiggler {

namespace {

using namespace std::chrono_literals;
using Clock = std::chrono::steady_clock;

constexpr const char *config_file = "/etc/kvm/mouse-jiggler";
constexpr auto interval = 15s;
constexpr auto move_time = 100ms;

struct {
	bool enabled = false, running = false, stopped = false;
	std::string mode = "relative";
	Clock::time_point last_updated = Clock::now();
	std::uint64_t generation = 0;
	std::unique_ptr<hid::Lane> lane;
} state;

void update()
{
	if (state.running) state.last_updated = Clock::now();
}

// done runs on the loop thread once the report is written or dropped.
void write_mouse(std::vector<std::uint8_t> report, const std::shared_ptr<control::Cancel> &operation, std::function<void()> done = {})
{
	if (!state.lane) {
		if (done) done();
		return;
	}
	hid::Report r;
	r.data = std::move(report);
	// Manual input cancels the jiggle: its reports stop at the write lane.
	r.reservation = control::Reservation::for_operation(operation);
	r.complete_reservation = false;
	if (done) r.complete = [done](bool) { done(); };
	state.lane->mouse(std::move(r));
}

// One jiggle: there and, 100 ms later, back unless manual input has started meanwhile (it then owns the pointer).
// The jiggle takes the AI operation slot (BeginBackground): it is skipped while MCP or manual input works.
void move()
{
	if (control::manual_active() || control::mode_status().transitioning) return;
	std::string busy;
	auto operation = control::begin_operation(control::OperationKind::hid, busy);
	if (!operation) return;
	bool absolute = state.mode == "absolute";
	write_mouse(absolute ? std::vector<std::uint8_t>{0x00, 0x00, 0x3f, 0x00, 0x3f, 0x00}
	                     : std::vector<std::uint8_t>{0x00, 0x0a, 0x0a, 0x00},
	            operation);
	loop::after(move_time, [absolute, operation] {
		// The slot is freed once the move back is written: ending it earlier would cancel that report.
		if (control::manual_active()) return control::end_operation(operation);
		write_mouse(absolute ? std::vector<std::uint8_t>{0x00, 0xff, 0x3f, 0xff, 0x3f, 0x00}
		                     : std::vector<std::uint8_t>{0x00, 0xf6, 0xf6, 0x00},
		            operation, [operation] { control::end_operation(operation); });
	});
}

void tick(std::uint64_t generation)
{
	if (generation != state.generation || state.stopped) return;
	if (!state.enabled) {
		state.running = false;
		return;
	}
	if (Clock::now() - state.last_updated > interval) {
		move();
		update();
	}
	loop::after(interval, [generation] { tick(generation); });
}

void run()
{
	if (!state.enabled || state.running || state.stopped) return;
	state.running = true;
	update();
	auto generation = ++state.generation;
	loop::after(interval, [generation] { tick(generation); });
}

struct JigglerJson {
	bool enabled;
	std::string mode;
};

} // namespace

void start()
{
	state.lane = std::make_unique<hid::Lane>();
	std::ifstream in(config_file);
	if (!in) return;
	std::string mode((std::istreambuf_iterator<char>(in)), {});
	std::erase(mode, '\n');
	if (!mode.empty()) state.mode = mode;
	state.enabled = true;
	run();
}

void stop()
{
	state.stopped = true;
	state.generation++;
	state.lane.reset();
}

void note_input() { update(); }

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/mouse-jiggler", auth::guarded([](http::Request &, http::Response &res) {
		std::string out;
		(void)glz::write_json(JigglerJson{state.enabled, state.mode}, out);
		api::send(res, 0, "success", out);
	}, admin));
	router.add("POST", "/api/vm/mouse-jiggler/", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<bool> enabled;
		std::optional<std::string> mode;
		if (params) {
			enabled = params->boolean("enabled", ok);
			mode = params->string("mode", ok);
		}
		if (!params || !ok) return api::err(res, -1, "invalid arguments");
		if (enabled.value_or(false)) {
			std::ofstream out(config_file, std::ios::trunc);
			if (!(out << mode.value_or(""))) return api::err(res, -2, "operation failed");
			out.close();
			::chmod(config_file, 0644);
			state.enabled = true;
			state.mode = mode.value_or("");
			run();
		} else {
			if (std::remove(config_file) != 0) return api::err(res, -2, "operation failed");
			state.enabled = false;
			state.mode = "relative";
		}
		api::ok(res);
		log::debug("set mouse jiggler: {}", enabled.value_or(false));
	}, admin));
}

} // namespace nkos::input::jiggler
