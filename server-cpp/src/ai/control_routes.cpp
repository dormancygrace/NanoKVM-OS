// The AI control mode routes of router/control.go: GET /api/ai/control/status and PUT /api/ai/control/mode.

#include "ai/control_routes.hpp"

#include "ai/json.hpp"
#include "ai/picoclaw.hpp"
#include "common/log.hpp"

namespace nkos::ai::control_routes {

namespace control = input::control;

namespace {

void fail(http::Response &res, int code, const std::string &message)
{
	res.json(200, "{\"code\":" + std::to_string(code) + ",\"message\":" + json::quote(message) + "}");
}

void ok(http::Response &res, const std::string &data)
{
	res.json(200, "{\"code\":0,\"data\":" + data + ",\"msg\":\"success\"}");
}

std::string released(const control::ModeStatus &status, const std::string &warning)
{
	return "{\"cleanup_warning\":" + json::quote(warning) + ",\"closed_sessions\":0,\"control\":" + control::mode_status_json(status) +
	       ",\"released\":true,\"runtime\":" + picoclaw::runtime_status_json() + "}";
}

} // namespace

std::function<std::string()> release_hid;

void status(http::Request &, http::Response &res)
{
	std::string error;
	auto s = control::mode_status(&error);
	if (!error.empty()) return fail(res, -1, error);
	ok(res, control::mode_status_json(s));
}

void set_mode(http::Request &req, http::Response &res)
{
	// ShouldBindJSON of {"mode": string}.
	json::Value body;
	std::string mode_text;
	bool valid = false;
	if (!json::parse(req.body, body) && (body.is_object() || body.is_null())) {
		valid = true;
		for (const auto &[k, x] : body.obj) {
			if (json::to_lower(k) != "mode" || x.is_null()) continue;
			if (!x.is_string()) valid = false;
			else mode_text = x.str;
		}
	}
	if (!valid) return fail(res, -1, "invalid arguments");
	auto mode = control::parse_mode(mode_text);
	if (!mode) return fail(res, -1, "invalid control mode");
	std::string error;
	auto previous = control::mode_status(&error);
	if (!error.empty()) {
		log::warn("AI control mode request failed before switch: {}", error);
		return fail(res, -2, error);
	}
	auto preempt = [&] {
		control::cancel_operation();
		if (*mode == control::Mode::mcp) return picoclaw::preempt_control_leases_for_mcp();
		picoclaw::cancel_active_control_operations();
		return std::string();
	};
	auto cleanup = [&] {
		if (*mode == control::Mode::mcp)
			if (auto e = picoclaw::stop_runtime_for_mcp(); !e.empty()) return e;
		return release_hid ? release_hid() : std::string();
	};
	error = control::switch_with_cleanup(*mode, preempt, cleanup);
	if (!error.empty()) {
		if (*mode == control::Mode::off) {
			std::string serr;
			auto s = control::mode_status(&serr);
			if (serr.empty() && s.mode == control::Mode::off) {
				picoclaw::cancel_active_control_operations();
				picoclaw::preserve_runtime_for_chat_only("control_release_chat_only");
				picoclaw::publish_control_mode_changed(s, "ai_control_mode");
				log::warn("AI control mode request completed with cleanup warning: {}", error);
				return ok(res, released(s, error));
			}
		}
		log::warn("AI control mode request failed: {}", error);
		return fail(res, -2, error);
	}
	auto s = control::mode_status(&error);
	if (!error.empty()) {
		log::warn("AI control mode request failed after switch: {}", error);
		return fail(res, -2, error);
	}
	if (*mode == control::Mode::off) {
		picoclaw::cancel_active_control_operations();
		picoclaw::preserve_runtime_for_chat_only("control_release_chat_only");
	}
	picoclaw::publish_control_mode_changed(s, "ai_control_mode");
	log::info("AI control mode request completed: {} -> {}", control::mode_name(previous.mode), control::mode_name(s.mode));
	if (*mode == control::Mode::off) return ok(res, released(s, ""));
	ok(res, control::mode_status_json(s));
}

} // namespace nkos::ai::control_routes
