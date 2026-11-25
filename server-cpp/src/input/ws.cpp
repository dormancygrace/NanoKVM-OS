#include "input/ws.hpp"

#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "http/websocket.hpp"
#include "input/attention.hpp"
#include "input/control.hpp"
#include "input/hid.hpp"
#include "input/jiggler.hpp"
#include "video/capture_status.hpp"

#include <glaze/glaze.hpp>
#include <openssl/crypto.h>
#include <openssl/rand.h>

#include <array>
#include <set>

namespace nkos::input::ws {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

enum : unsigned char { heartbeat = 0, keyboard_event = 1, mouse_event = 2, control_event = 3, hold_keepalive = 4,
	                   visibility_event = 5 };

constexpr std::size_t max_client_message = 4 << 10;
constexpr auto heartbeat_timeout = 90s;       // also how long a silent page counts as alive
constexpr auto hold_release_timeout = 300ms;

struct Client;
void send_event(Client &c, const std::string &type, const std::string &data);

struct Hold {
	bool enabled = false, closed = false;
	std::array<bool, 3> held{};
	std::uint64_t generation = 0;
};

struct Client : http::WsHandler, std::enable_shared_from_this<Client> {
	http::WebSocket *ws = nullptr;
	std::shared_ptr<control::ManualSession> manual = control::ManualSession::create();
	std::unique_ptr<hid::Lane> lane = std::make_unique<hid::Lane>();
	std::string lease;
	bool control_enabled = false, manual_view_only = false, page_visible = true;
	Clock::time_point last_message = Clock::now();
	Hold hold;

	void opened(http::WebSocket &socket) override;
	void received(http::WebSocket &socket, std::string_view data, bool binary) override;
	void closed(http::WebSocket &socket) override;

	bool page_alive(Clock::time_point now) const { return now - last_message < heartbeat_timeout; }
	void set_control_enabled(bool enabled)
	{
		control_enabled = enabled;
		send_control_status();
	}
	void send_control_status()
	{
		if (!ws) return;
		std::string payload = control_enabled ? R"({"enabled":true,"lease":")" + lease + "\"}" : R"({"enabled":false})";
		send_event(*this, "control", payload);
	}
	void revoke_input()
	{
		manual->revoke();
		// Reports a writer may already have taken: an all-released state before another session takes over.
		hid::release_all();
	}
	void queue_manual_report(control::ReportKind kind, std::vector<std::uint8_t> report, bool held, bool start_cooldown);
	void note_held(control::ReportKind kind, bool held)
	{
		hold.held[static_cast<int>(kind)] = held;
		arm_hold();
	}
	void arm_hold();
	void expire_hold(std::uint64_t generation);
};

// --- manager ----------------------------------------------------------------------------------------------------

std::set<Client *> clients;
Client *controller = nullptr;
std::string external_lease;
std::function<void()> external_release;
bool external_cleanup = false;
Clock::time_point manual_input_at{};

bool can_control(const Client *c) { return controller == c; }

void add_client(Client *c)
{
	clients.insert(c);
	if (!controller && external_lease.empty()) controller = c;
	c->set_control_enabled(controller == c);
}

void remove_client(Client *c)
{
	if (!clients.erase(c)) return;
	bool was = controller == c;
	if (was) {
		controller = nullptr;
		c->revoke_input();
	}
	Client *next = nullptr;
	if (!controller && external_lease.empty() && clients.size() == 1) {
		Client *remaining = *clients.begin();
		// A frozen background tab, kept open only by pongs, does not inherit control: the tab that reloads takes it.
		if (!remaining->manual_view_only && remaining->page_alive(Clock::now())) next = controller = remaining;
	}
	c->control_enabled = false;
	if (next) next->set_control_enabled(true);
}

void set_control(Client *c, bool enabled)
{
	if (!clients.count(c)) {
		c->set_control_enabled(false);
		return;
	}
	c->manual_view_only = !enabled;
	if (!enabled) {
		if (controller != c) return c->set_control_enabled(false);
		controller = nullptr;
		c->revoke_input();
		return c->set_control_enabled(false);
	}
	if (auto release = std::move(external_release)) {
		external_lease.clear();
		external_release = nullptr;
		external_cleanup = true;
		release();
		external_cleanup = false;
	}
	Client *previous = controller;
	if (previous == c) return c->set_control_enabled(true);
	if (previous) previous->revoke_input();
	controller = c;
	if (previous) previous->set_control_enabled(false);
	c->set_control_enabled(true);
}

struct EventMessage {
	std::string type;
	std::string data;
};

std::string event_json(const std::string &type, const std::string &data)
{
	using Message = EventMessage;
	std::string out;
	(void)glz::write_json(Message{type, data}, out);
	return out;
}

void send_event(Client &c, const std::string &type, const std::string &data)
{
	if (c.ws && !c.ws->closing()) c.ws->send(http::make_message(event_json(type, data), false));
}

void broadcast(const std::string &type, const std::string &data)
{
	auto message = http::make_message(event_json(type, data), false);
	for (auto *c : clients)
		if (c->ws && !c->ws->closing()) c->ws->send(message);
}

bool keyboard_report_held(const std::vector<std::uint8_t> &r)
{
	if (r.size() != 8) return false;
	if (r[0]) return true;
	for (std::size_t i = 2; i < 8; i++)
		if (r[i]) return true;
	return false;
}

bool mouse_report_starts_cooldown(const std::vector<std::uint8_t> &r)
{
	if (r.size() != 5 && r.size() != 7) return true;
	if (r[0]) return true;
	return r[r.size() - 2] || r[r.size() - 1];
}

// --- client -----------------------------------------------------------------------------------------------------

void Client::opened(http::WebSocket &socket)
{
	ws = &socket;
	ws->max_message = max_client_message;
	ws->ping_period = std::chrono::duration_cast<std::chrono::seconds>(heartbeat_timeout) / 3;
	ws->read_timeout = std::chrono::duration_cast<std::chrono::seconds>(heartbeat_timeout);
	// A pong renews the deadline only for a page that has its own held-input watchdog: a legacy page needs the
	// application timeout to release keys after it froze.
	ws->pong_counts = [self = weak_from_this()] {
		auto c = self.lock();
		return c && c->hold.enabled && !c->hold.closed;
	};
	unsigned char raw[16];
	RAND_bytes(raw, sizeof raw);
	static const char hex[] = "0123456789abcdef";
	for (unsigned char b : raw) lease += hex[b >> 4], lease += hex[b & 15];
	add_client(this);
	hid::open();
	for (const auto &status : video::capture_status::latest()) send_event(*this, video::capture_status::event, status);
	send_event(*this, hid::led_status_event, hid::led_status_json(hid::led_status()));
	send_control_status();
	// A page counts as visible until it says otherwise.
	page_visible = true;
	attention::page_shown();
	log::debug("websocket connected");
}

void Client::received(http::WebSocket &, std::string_view data, bool)
{
	last_message = Clock::now();
	if (data.empty()) return;
	auto type = static_cast<unsigned char>(data[0]);
	std::vector<std::uint8_t> report(data.begin() + 1, data.end());
	switch (type) {
	case heartbeat:
		send_event(*this, "heartbeat", "");
		break;
	case hold_keepalive:
		if (data.size() == 1) {
			hold.enabled = true;
			arm_hold();
		}
		break;
	case visibility_event:
		if (data.size() == 2 && (data[1] == 1) != page_visible) {
			page_visible = data[1] == 1;
			if (page_visible) attention::page_shown();
			else attention::page_hidden();
		}
		break;
	case keyboard_event:
		// Input from any page shows somebody is there, with control or not.
		attention::note_input(Clock::now());
		if (!can_control(this)) break;
		if (report.size() != 8) {
			log::debug("invalid manual keyboard report");
			break;
		}
		queue_manual_report(control::ReportKind::keyboard, report, keyboard_report_held(report), true);
		break;
	case mouse_event: {
		attention::note_input(Clock::now());
		if (!can_control(this)) break;
		report = hid::normalize_mouse_report(std::move(report));
		if (report.size() != 5 && report.size() != 7) {
			log::debug("invalid manual mouse report");
			break;
		}
		auto kind = report.size() == 7 ? control::ReportKind::absolute_mouse : control::ReportKind::relative_mouse;
		bool held = report[0] != 0, cooldown = mouse_report_starts_cooldown(report);
		queue_manual_report(kind, std::move(report), held, cooldown);
		break;
	}
	case control_event:
		if (data.size() != 2 || (data[1] != 0 && data[1] != 1)) {
			log::debug("invalid manual control request");
			break;
		}
		set_control(this, data[1] == 1);
		break;
	default:
		break;
	}
}

void Client::queue_manual_report(control::ReportKind kind, std::vector<std::uint8_t> report, bool held, bool start_cooldown)
{
	if (hold.closed) return;
	auto reservation = manual->reserve(kind, held, start_cooldown, control::allow_manual);
	if (!reservation) {
		log::debug("manual HID input dropped: the control mode is changing");
		return;
	}
	if (!can_control(this)) {
		reservation->complete(false);
		return;
	}
	hid::Report r;
	r.data = std::move(report);
	r.reservation = reservation;
	r.reset = [session = manual](control::ReportKind k) { session->reset(k); };
	if (kind == control::ReportKind::keyboard) lane->keyboard(std::move(r));
	else lane->mouse(std::move(r));
	note_held(kind, held);
	manual_input_at = Clock::now();
	jiggler::note_input();
}

void Client::arm_hold()
{
	hold.generation++;
	if (hold.closed || !hold.enabled || hold.held == std::array<bool, 3>{}) return;
	loop::after(hold_release_timeout, [self = weak_from_this(), g = hold.generation] {
		if (auto c = self.lock()) c->expire_hold(g);
	});
}

// No keepalive for 300 ms while keys were held: the page froze or lost its network. Queued reports become invalid
// and the hardware is released before the lease goes, so nothing else can interleave.
void Client::expire_hold(std::uint64_t generation)
{
	if (hold.closed || generation != hold.generation || hold.held == std::array<bool, 3>{}) return;
	hold.held = {};
	arm_hold();
	if (!can_control(this)) return;
	if (manual->active()) {
		manual->revoke();
		hid::release_all();
	}
}

void Client::closed(http::WebSocket &)
{
	hold.closed = true;
	hold.held = {};
	hold.generation++;
	remove_client(this);
	manual->close();
	lane.reset();  // releases what this page's lanes still hold
	if (page_visible) attention::page_hidden();
	ws = nullptr;
	log::debug("websocket disconnected");
}

bool subscribed = false;

} // namespace

void add_routes(http::Router &router)
{
	if (!subscribed) {
		subscribed = true;
		video::capture_status::subscribe([](const std::string &json) { broadcast(video::capture_status::event, json); });
		hid::on_led_status([](const hid::LedStatus &status) { broadcast(hid::led_status_event, hid::led_status_json(status)); });
	}
	router.ws("/api/ws", auth::guarded_ws([](http::Request &, http::Response &) -> std::shared_ptr<http::WsHandler> {
		return std::make_shared<Client>();
	}));
}

bool allows_input_lease(const std::string &lease)
{
	if (external_cleanup) return false;
	auto equal = [](const std::string &a, const std::string &b) {
		return !a.empty() && a.size() == b.size() && CRYPTO_memcmp(a.data(), b.data(), a.size()) == 0;
	};
	if (!external_lease.empty()) return equal(lease, external_lease);
	if (!controller) return true;
	return equal(lease, controller->lease);
}

bool acquire_external_input(const std::string &lease, std::function<void()> release)
{
	if (lease.empty() || !release || controller || !external_lease.empty()) return false;
	external_lease = lease;
	external_release = std::move(release);
	return true;
}

void release_external_input(const std::string &lease)
{
	if (external_lease == lease) {
		external_lease.clear();
		external_release = nullptr;
	}
}

Clock::time_point last_manual_input() { return manual_input_at; }

} // namespace nkos::input::ws
