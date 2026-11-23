#include "osupdate/routes.hpp"

#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/loop.hpp"
#include "osupdate/bind.hpp"
#include "osupdate/osupdate.hpp"
#include "osupdate/reply.hpp"

#include <atomic>
#include <memory>
#include <thread>

namespace nkos::osupdate {

namespace {

const char *name_struct = "struct { Name string \"json:\\\"name\\\"\" }";

// A request answered from a thread of its own: apk runs and package locks can take minutes.
void slow(http::Request &req, http::Response &res, std::function<void(http::Response &)> fn)
{
	res.pending = true;
	std::thread([id = req.id, headers = res.headers, fn = std::move(fn)] {
		http::Response out;
		out.headers = headers;
		fn(out);
		http::complete(id, std::move(out));
	}).detach();
}

// Polled from the search's thread: whether the request's connection went away.
std::function<bool()> request_gone(std::uint64_t id)
{
	auto gone = std::make_shared<std::atomic<bool>>(false);
	return [id, gone] {
		loop::post([id, gone] {
			if (!http::request_open(id)) gone->store(true);
		});
		return gone->load();
	};
}

void check_in_background()
{
	std::thread([] { (void)check_updates(true); }).detach();
}

} // namespace

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	auto add = [&](const char *method, const std::string &path, http::Handler handler) {
		router.add(method, "/api/os/update" + path, auth::guarded(std::move(handler), admin));
	};

	add("GET", "", [](http::Request &req, http::Response &res) {
		slow(req, res, [](http::Response &out) {
			// Pages up to 3.1 read alpine.enabled without a check.
			std::string data = "{\"alpine\":{\"enabled\":false},\"apk\":" + to_json(get_apk_status()) + ",\"channel\":" +
			                   to_json(get_channel()) + ",\"components\":" + to_json(get_components()) +
			                   ",\"installed\":{\"version\":" + gojson::quote(installed_version()) + "},\"notice\":" +
			                   to_json(get_notice()) + "}";
			reply(out, data, "");
		});
	});
	// The menu polls this for its update badge: a file read, no apk run.
	add("GET", "/notice", [](http::Request &req, http::Response &res) {
		slow(req, res, [](http::Response &out) { reply(out, to_json(get_notice()), ""); });
	});
	add("PUT", "/notice", [](http::Request &req, http::Response &res) {
		bool enabled = false, present = false;
		if (!bind(req.body, "", "struct { Enabled *bool \"json:\\\"enabled\\\"\" }", {{"enabled", nullptr, &enabled, &present}}).empty() ||
		    !present)
			return reply(res, "", "enabled is required");
		slow(req, res, [enabled](http::Response &out) { reply(out, "", set_notice_enabled(enabled)); });
	});
	add("POST", "/notice/check", [](http::Request &, http::Response &res) {
		check_in_background();
		reply(res, "", "");
	});
	add("POST", "/channel", [](http::Request &req, http::Response &res) {
		std::string channel;
		if (auto e = bind(req.body, "", "struct { Channel string \"json:\\\"channel\\\"\" }", {{"channel", &channel}}); !e.empty())
			return reply(res, "", e);
		slow(req, res, [channel](http::Response &out) {
			Error e = set_channel(channel);
			if (e.empty()) check_in_background();
			reply(out, to_json(get_channel()), e);
		});
	});
	add("POST", "/apk/:action", [](http::Request &req, http::Response &res) {
		slow(req, res, [action = req.param("action")](http::Response &out) { reply(out, "", start_apk(action)); });
	});
	add("GET", "/software", [](http::Request &req, http::Response &res) {
		slow(req, res, [](http::Response &out) {
			Error e;
			auto installed = list_installed_software(e);
			std::string data = "{\"indexes\":" + to_json(get_software_index_status()) + ",\"installed\":" +
			                   (installed ? to_json(*installed) : std::string("null")) + ",\"operation\":" +
			                   to_json(get_software_status()) + "}";
			reply(out, data, e);
		});
	});
	add("GET", "/software/status", [](http::Request &req, http::Response &res) {
		slow(req, res, [](http::Response &out) {
			reply(out, "{\"indexes\":" + to_json(get_software_index_status()) + ",\"operation\":" + to_json(get_software_status()) + "}", "");
		});
	});
	add("GET", "/software/index-schedule", [](http::Request &req, http::Response &res) {
		slow(req, res, [](http::Response &out) {
			Error e;
			auto schedule = get_index_schedule(e);
			std::string data = "{\"schedule\":" + to_json(schedule) + ",\"timezone\":" +
			                   gojson::quote(index_schedule_timezone()) + "}";
			reply(out, data, e);
		});
	});
	add("PUT", "/software/index-schedule", [](http::Request &req, http::Response &res) {
		IndexSchedule s{false, ""};
		if (auto e = bind(req.body, "IndexSchedule", "osupdate.IndexSchedule", {{"enabled", nullptr, &s.enabled}, {"time", &s.time}});
		    !e.empty())
			return reply(res, "", e);
		slow(req, res, [s](http::Response &out) { reply(out, "", set_index_schedule(s)); });
	});
	add("GET", "/software/search", [](http::Request &req, http::Response &res) {
		slow(req, res, [query = req.query_value("query"), gone = request_gone(req.id)](http::Response &out) {
			Error e;
			auto packages = search_software(query, gone, e);
			reply(out, "{\"packages\":" + (packages ? to_json(*packages) : std::string("null")) + "}", e);
		});
	});
	add("GET", "/software/updates", [](http::Request &req, http::Response &res) {
		slow(req, res, [](http::Response &out) {
			Error e;
			auto updates = list_software_updates(e);
			reply(out, "{\"updates\":" + (updates ? to_json(*updates) : std::string("null")) + "}", e);
		});
	});
	add("POST", "/software/remove/preview", [](http::Request &req, http::Response &res) {
		std::string name;
		if (auto e = bind(req.body, "", name_struct, {{"name", &name}}); !e.empty()) return reply(res, "", e);
		slow(req, res, [name](http::Response &out) {
			Error e;
			auto preview = preview_software_removal(name, e);
			reply(out, "{\"preview\":" + gojson::quote(preview.value_or("")) + "}", e);
		});
	});
	add("POST", "/software/:action", [](http::Request &req, http::Response &res) {
		std::string name;
		if (auto e = bind(req.body, "", name_struct, {{"name", &name}}); !e.empty()) return reply(res, "", e);
		slow(req, res, [action = req.param("action"), name](http::Response &out) { reply(out, "", start_software(action, name)); });
	});
}

void start() { start_notice_schedule(); }

} // namespace nkos::osupdate
