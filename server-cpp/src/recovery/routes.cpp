#include "recovery/routes.hpp"

#include "auth/session.hpp"
#include "auth/store.hpp"
#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "osupdate/osupdate.hpp"
#include "osupdate/reply.hpp"
#include "recovery/recovery.hpp"

#include <atomic>
#include <thread>

namespace nkos::recovery {

namespace {

using osupdate::reply;

// One KDF or decoded archive at a time on a memory-constrained device.
std::atomic<bool> busy{false};

void handle(const std::string &action, const std::string &username, const std::string &body, http::Response &out)
{
	// http.MaxBytesReader: the base64 of the largest archive plus room for the rest of the request.
	constexpr std::size_t body_limit = max_archive * 4 / 3 + (64 << 10);
	Request req;
	if (body.size() > body_limit || !decode_request(body, req)) return reply(out, "", "invalid or excessive recovery request");
	std::string error;
	auto user = auth::store().authenticate(username, req.password, error);
	if (!error.empty() || !user || user->role != "admin") return reply(out, "", "current administrator password is required");
	if (action == "reboot" && live().pending()) return reply(out, "", reboot());
	auto lock = osupdate::lock(error);
	if (!lock) return reply(out, "", error);
	Engine e = live();
	if (action == "export") {
		auto a = e.snapshot(error);
		if (!a) return reply(out, "", error);
		auto data = seal(*a, req.archive_password, error);
		if (!data) return reply(out, "", error);
		return reply(out, "{\"archive\":" + gojson::quote(base64::encode(*data)) + "}", "");
	}
	if (!available()) return reply(out, "", "update nanokvm-base before restoring or resetting settings");
	std::optional<Archive> archive;
	if (req.options.action == "restore") {
		archive = open(req.archive.value_or(""), req.archive_password, error);
		if (!archive) return reply(out, "", error);
	}
	Plan plan;
	Preview preview;
	if (!e.prepare(archive ? &*archive : nullptr, req.options, plan, preview, error)) return reply(out, "", error);
	if (action == "preview") return reply(out, to_json(preview), "");
	if (action == "apply") {
		if (req.token.empty() || req.token != preview.token) return reply(out, "", "settings or selection changed; preview again");
		Error err = e.stage(plan);
		if (err.empty()) err = reboot();
		return reply(out, "{\"status\":" + to_json(e.status()) + "}", err);
	}
	out.status = 404;
	out.body.clear();
}

} // namespace

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/system/recovery", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			reply(out, "{\"available\":" + std::string(available() ? "true" : "false") + ",\"status\":" + to_json(live().status()) + "}", "");
		});
	}, admin));
	router.add("POST", "/api/system/recovery/:action", auth::guarded([](http::Request &req, http::Response &res) {
		res.set_header("Cache-Control", "no-store");
		bool expected = false;
		if (!busy.compare_exchange_strong(expected, true)) return reply(res, "", "another recovery request is running");
		res.pending = true;
		std::thread([id = req.id, headers = res.headers, action = req.param("action"), username = req.auth ? req.auth->username : "",
		             body = req.body] {
			http::Response out;
			out.headers = headers;
			handle(action, username, body, out);
			busy.store(false);
			http::complete(id, std::move(out));
		}).detach();
	}, admin));
}

} // namespace nkos::recovery
