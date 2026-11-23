// The MCP management routes of service/mcp/service.go: the switch, the API key and the host control switch.

#include "ai/mcp.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "common/log.hpp"

namespace nkos::ai::mcp {

namespace control = input::control;

Hooks &hooks()
{
	static Hooks h;
	return h;
}

namespace {

// proto.ParseFormRequest of {"enabled": bool} (required).
std::optional<bool> enabled_param(http::Request &req)
{
	auto params = api::Params::bind(req);
	if (!params) return std::nullopt;
	bool ok = true;
	auto enabled = params->boolean("enabled", ok);
	if (!ok || !enabled) return std::nullopt;
	return enabled;
}

std::string join(const std::vector<std::string> &errs)
{
	std::string out;
	for (const auto &e : errs)
		if (!e.empty()) out += (out.empty() ? "" : "\n") + e;
	return out;
}

} // namespace

void get_config(http::Request &, http::Response &res)
{
	res.set_header("Cache-Control", "no-store");
	Config config;
	std::string error;
	if (!load_config(config, error)) {
		log::error("failed to load MCP config: {}", error);
		return api::err(res, -1, "get MCP config failed");
	}
	auto status = control::mode_status(&error);
	if (!error.empty()) {
		log::error("failed to load AI control mode: {}", error);
		return api::err(res, -1, "get MCP config failed");
	}
	api::send(res, 0, "success", config_json(config, status));
}

void set_config(http::Request &req, http::Response &res)
{
	res.set_header("Cache-Control", "no-store");
	auto enabled = enabled_param(req);
	if (!enabled) return api::err(res, -1, "invalid arguments");
	auto &h = hooks();
	auto cancel_mcp = [] {
		control::cancel_operation();
		return std::string();
	};
	Config config;
	std::string error;
	if (*enabled) {
		if (ensure_api_key(config, error)) {
			error = control::switch_with_cleanup(
			    control::Mode::mcp,
			    [&] {
				    cancel_mcp();
				    return h.preempt_picoclaw ? h.preempt_picoclaw() : std::string();
			    },
			    [&] {
				    std::vector<std::string> errs;
				    if (h.stop_picoclaw) errs.push_back(h.stop_picoclaw());
				    if (h.release_hid) errs.push_back(h.release_hid());
				    return join(errs);
			    });
		}
	} else if (load_config(config, error)) {
		bool switched = false;
		error = control::switch_if_with_cleanup(control::Mode::mcp, control::Mode::off, cancel_mcp,
		                                        [&] { return h.release_hid ? h.release_hid() : std::string(); }, switched);
	}
	if (!error.empty()) {
		log::error("failed to switch MCP control mode: {}", error);
		return api::err(res, -2, "operation failed: " + error);
	}
	auto status = control::mode_status(&error);
	if (!error.empty()) {
		log::error("failed to load AI control mode: {}", error);
		return api::err(res, -2, "operation failed: " + error);
	}
	if (h.on_control_change) h.on_control_change(status);
	api::send(res, 0, "success", config_json(config, status));
}

void regenerate_key(http::Request &, http::Response &res)
{
	res.set_header("Cache-Control", "no-store");
	Config config;
	std::string error;
	if (!regenerate_api_key(config, error)) {
		log::error("failed to regenerate MCP API key: {}", error);
		return api::err(res, -1, "operation failed");
	}
	auto status = control::mode_status(&error);
	if (!error.empty()) {
		log::error("failed to load AI control mode: {}", error);
		return api::err(res, -1, "operation failed");
	}
	api::send(res, 0, "success", config_json(config, status));
}

void set_host_control_route(http::Request &req, http::Response &res)
{
	res.set_header("Cache-Control", "no-store");
	auto enabled = enabled_param(req);
	if (!enabled) return api::err(res, -1, "invalid arguments");
	Config config;
	std::string error;
	if (!set_host_control(*enabled, config, error)) {
		log::error("failed to save MCP host control: {}", error);
		return api::err(res, -2, "operation failed");
	}
	auto status = control::mode_status(&error);
	if (!error.empty()) {
		log::error("failed to load AI control mode: {}", error);
		return api::err(res, -2, "operation failed");
	}
	log::info("MCP host control tools {}", config.allow_host_control ? "allowed" : "forbidden");
	api::send(res, 0, "success", config_json(config, status));
}

} // namespace nkos::ai::mcp
