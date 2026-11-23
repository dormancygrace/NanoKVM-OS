#include "notify/notify.hpp"

#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "notify/service.hpp"
#include "notify/transport.hpp"
#include "system/atx.hpp"
#include "system/gpio.hpp"
#include "system/journal.hpp"

#include <fstream>
#include <iterator>
#include <thread>

namespace nkos::notify {

namespace {

constexpr char addresses_path[] = "/etc/kvm/notify/addresses.json";
constexpr char bmc_config_path[] = "/etc/kvm/bmc/config.json";

Service &service()
{
	static Service *s = [] {
		auto *svc = new Service(config_path, addresses_path);
		svc->send = [svc](const Channel &ch, const Rendered &r, Delivery *d) {
			return transport::send_channel(ch, r, d, svc->oauth);
		};
		svc->oauth.post = transport::perform;
		svc->oauth.revoke_later = [](const OAuthClient &c, const std::string &token) {
			std::thread([c, token] { revoke(c, token, transport::perform); }).detach();
		};
		svc->record = [](const Event &e) {
			system::journal::add({0, e.time, e.kind, severity_name(e.severity), e.message, e.vars, e.details});
		};
		return svc;
	}();
	return *s;
}

// proto.Response, written as Go writes it.
void respond(http::Response &res, int code, std::string_view msg, const std::string &data = "null")
{
	res.json(200, "{\"code\":" + std::to_string(code) + ",\"msg\":" + gojson::quote(msg) + ",\"data\":" + data + "}");
}

void ok(http::Response &res, const json::Out &data) { respond(res, 0, "success", json::marshal(data)); }

// Answers from a thread of its own: a sign-in or a test waits up to 30 s on an outside service, longer than the
// shared blocking pool should be held.
void answer_later(http::Request &req, http::Response &res, std::function<void(http::Response &)> fn)
{
	res.pending = true;
	std::thread([id = req.id, headers = res.headers, fn = std::move(fn)] {
		http::Response out;
		out.headers = headers;
		fn(out);
		http::complete(id, std::move(out));
	}).detach();
}

const auth::Guard admin{{"admin"}};

// The BMC page's "power LED connected" setting (bmc.Load(...).PowerLEDDisconnected), reread every 30 s.
bool power_led_unconnected()
{
	static std::chrono::steady_clock::time_point at{};
	static bool unconnected = false;
	auto now = std::chrono::steady_clock::now();
	if (at != std::chrono::steady_clock::time_point{} && now - at < std::chrono::seconds(30)) return unconnected;
	at = now;
	unconnected = false;
	std::ifstream in(bmc_config_path, std::ios::binary);
	if (!in) return unconnected;
	std::string data((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
	json::Value v;
	bool flag = false;
	if (json::unmarshal(data, v) &&
	    json::members(v, {"powerLedDisconnected"}, [&](std::string_view, const json::Value &x) { return json::get(x, flag); }))
		unconnected = flag;
	return unconnected;
}

// notifyPowerLED: the power LED, nullopt when it cannot be read or does not count (ATX off, LED not wired).
std::optional<bool> power_led()
{
	if (!system::atx::enabled() || power_led_unconnected()) return std::nullopt;
	auto leds = system::gpio::current_leds();
	if (!leds) return std::nullopt;
	return leds->power;
}

} // namespace

void emit(Event e) { service().emit(std::move(e)); }

bool wants(std::string_view kind) { return service().wants(kind); }

void login_locked_out(const std::string &address, int failures, int minutes)
{
	emit({std::string(kind_security), Severity::warning, "security.lockout",
	      {{"failures", std::to_string(failures)}, {"address", address}, {"minutes", std::to_string(minutes)}}});
}

void login_attempts_limited(const std::string &address, int attempts, int minutes)
{
	emit({std::string(kind_security), Severity::warning, "security.attempt-limit",
	      {{"attempts", std::to_string(attempts)}, {"address", address}, {"minutes", std::to_string(minutes)}}});
}

void login_succeeded(const std::string &user, const std::string &address)
{
	if (address.empty()) return;
	// The addresses are kept also while no channel wants these events, so turning them on later does not trust the
	// next login, which may be an attacker's.
	std::thread([user, address] {
		std::string error;
		bool known = service().remember_address(user, address, error);
		// A new address is still reported when it could not be stored.
		if (!error.empty()) log::warn("notifications: login addresses: {}", error);
		if (!known)
			emit({std::string(kind_security), Severity::warning, "security.new-address", {{"user", user}, {"address", address}}});
	}).detach();
}

void add_routes(http::Router &router)
{
	service();
	router.add("GET", "/api/notify/config", auth::guarded([](http::Request &, http::Response &res) {
		res.set_header("Cache-Control", "no-store");
		ok(res, service().status());
	}, admin));
	router.add("PUT", "/api/notify/config", auth::guarded([](http::Request &req, http::Response &res) {
		Settings settings;
		if (!bind_settings(req.body, settings)) return respond(res, -1, "invalid configuration");
		http::defer(req, res, [settings = std::move(settings)](http::Response &out) mutable {
			if (auto e = service().configure(std::move(settings)); !e.empty()) return respond(out, -1, e);
			ok(out, service().status());
		});
	}, admin));
	// Writes every message with sample values, as a template on the page (saved or not) or the standard one would.
	router.add("POST", "/api/notify/preview", auth::guarded([](http::Request &req, http::Response &res) {
		PreviewRequest request;
		if (!bind_preview(req.body, request)) return respond(res, -1, "invalid template");
		std::string error;
		json::Out previews = service().preview(request, error);
		if (!error.empty()) return respond(res, -1, error);
		ok(res, previews);
	}, admin));
	// Starts the OAuth sign-in of an e-mail channel as it is on the page.
	router.add("POST", "/api/notify/oauth/start", auth::guarded([](http::Request &req, http::Response &res) {
		ChannelUpdate update;
		if (!bind_channel_update(req.body, update)) return respond(res, -1, "invalid channel");
		answer_later(req, res, [update = std::move(update)](http::Response &out) {
			Error error;
			auto start = service().start_sign_in(update, error);
			if (!start) return respond(out, -2, error.text);
			ok(out, start->json());
		});
	}, admin));
	// Completes it: Google's with the pasted address, Microsoft's by asking whether the code was entered.
	router.add("POST", "/api/notify/oauth/finish", auth::guarded([](http::Request &req, http::Response &res) {
		json::Value v;
		std::string id, code;
		if (!json::decode_first(req.body, v) ||
		    !json::members(v, {"id", "code"}, [&](std::string_view f, const json::Value &x) { return json::get(x, f == "id" ? id : code); }))
			return respond(res, -1, "invalid sign-in");
		answer_later(req, res, [id, code](http::Response &out) {
			Error error;
			auto result = service().finish_sign_in(id, code, error);
			if (!result) return respond(out, -2, error.text);
			out.set_header("Cache-Control", "no-store");
			ok(out, result->json());
		});
	}, admin));
	// Sends a test message through one channel as it is on the page.
	router.add("POST", "/api/notify/test", auth::guarded([](http::Request &req, http::Response &res) {
		ChannelUpdate update;
		if (!bind_channel_update(req.body, update)) return respond(res, -1, "invalid channel");
		answer_later(req, res, [update = std::move(update)](http::Response &out) {
			if (Result err = service().test_channel(update)) return respond(out, -2, err->text);
			respond(out, 0, "success");
		});
	}, admin));
}

void start_host_watch(std::function<std::optional<bool>()> hdmi_signal)
{
	static HostWatch *watch = nullptr;
	if (watch) return;
	watch = new HostWatch{power_led, std::move(hdmi_signal), [](Event e) { emit(std::move(e)); }};
	static std::function<void()> tick = [] {
		watch->step(std::chrono::steady_clock::now());
		loop::after(std::chrono::seconds(1), tick);
	};
	loop::after(std::chrono::seconds(1), tick);
}

} // namespace nkos::notify
