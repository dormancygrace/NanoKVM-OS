#pragma once

// What the PicoClaw files share: the config documents (picoclaw_config.cpp), the runtime status and the stores
// (picoclaw.cpp), the error answers.

#include "ai/json.hpp"
#include "ai/picoclaw.hpp"
#include "common/time.hpp"

#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

namespace nkos::ai::picoclaw {

// --- Errors (errors.go) ---

inline constexpr const char *code_lock_held = "AI_LOCK_HELD";
inline constexpr const char *code_screenshot_failed = "SCREENSHOT_FAILED";
inline constexpr const char *code_screenshot_no_signal = "SCREENSHOT_NO_SIGNAL";
inline constexpr const char *code_invalid_action = "INVALID_ACTION";
inline constexpr const char *code_runtime_unavailable = "RUNTIME_UNAVAILABLE";
inline constexpr const char *code_runtime_start_failed = "RUNTIME_START_FAILED";
inline constexpr const char *code_session_id_missing = "SESSION_ID_MISSING";
inline constexpr const char *code_session_id_invalid = "SESSION_ID_INVALID";
inline constexpr const char *code_mode_conflict = "AI_MODE_CONFLICT";
inline constexpr const char *code_control_required = "CONTROL_REQUIRED";
inline constexpr const char *code_owned_by_mcp = "CONTROL_OWNED_BY_MCP";
inline constexpr const char *code_transitioning = "CONTROL_TRANSITIONING";

struct Error {
	int status = 200;
	std::string code, message, session_id;
	std::optional<int> index;
	std::string text() const { return code + ": " + message; }
};
inline Error make_error(const std::string &code, const std::string &message) { return Error{200, code, message, {}, {}}; }

// writeSuccess / writePicoclawErrorWithData (data: JSON, "" for none).
void write_success(http::Response &res, const std::string &data_json);
void write_error(http::Response &res, const Error &e, const std::string &data_json = {});

// --- Config documents (config_files.go, runtime_defaults.go, model_config.go) ---

std::string home(std::string &error);
std::string config_path(std::string &error);

struct ModelEntry {
	std::string model_name, model, api_base, api_key;
	std::vector<std::string> api_keys;
};
struct PicoSettings {
	std::string token;
	bool allow_token_query = false;
	long long ping_interval = 0, read_timeout = 0, write_timeout = 0, max_connections = 0;
};
struct ChannelEntry {
	std::string type;
	bool enabled = false;
	PicoSettings settings;
};
struct ConfigFile {
	std::string model_name, model, workspace;  // agents.defaults
	std::string gateway_host;
	long long gateway_port = 0;
	std::vector<ModelEntry> model_list;
	std::map<std::string, ChannelEntry> channels;
};

struct SecurityChannelSettings {
	std::string token;
};
struct SecurityChannel {
	std::string token;
	std::optional<SecurityChannelSettings> settings;
};
struct Security {
	std::map<std::string, std::vector<std::string>> model_list;  // name -> api_keys
	std::map<std::string, SecurityChannel> channel_list;
	bool has_model_list = false, has_channel_list = false;
};

struct Document {
	std::string config_path, security_path;
	ConfigFile config;
	json::Value raw;
	Security security;
	std::string save_config();
	std::string save_security();
	std::string resolved_gateway_token() const;
};

// loadPicoclawConfigDocument; not_exist set when config.json is missing.
std::optional<Document> load_document(std::string &error, bool *not_exist = nullptr);

struct GatewaySettings {
	std::string gateway_url, token;
	bool allow_token_query = false;
	int ping_interval_ms = 0, read_timeout_ms = 0;
	bool model_configured = false;
	std::string model_name, target_model_name;
};
std::optional<GatewaySettings> load_gateway_settings(std::string &error);

std::string ensure_startup_defaults(bool *not_exist = nullptr);
// updatePicoclawModelConfig: the model name, or error.
std::string update_model_config(const std::string &api_base, const std::string &api_key, const std::string &model, std::string &error);
std::string workspace_path(std::string &error);
std::string detect_agent_profile();
std::string apply_agent_profile(const std::string &profile);
// runPicoclawOnboardForConfig: the start script's onboard (picoclaw.cpp).
std::optional<Error> onboard_for_config();

// --- Runtime intent (/etc/kvm/picoclaw-runtime.json) ---

struct Intent {
	bool desired_running = false;
	std::string updated_at, updated_by, last_started_at, last_stopped_at, last_error;
	bool host_tools = false;
};
std::string intent_json(const Intent &i);
// Load; error set when the file cannot be read or parsed (intent then has the parse error as last_error).
Intent intent_load(std::string &error);
std::string intent_set_desired(bool desired, const std::string &by);
std::string intent_set_host_tools(bool enabled, const std::string &by);
std::string intent_set_last_error(const std::string &message);

// --- Installation (picoclaw_install.cpp with the runtime status of picoclaw.cpp) ---

struct InstallState {
	std::string stage;
	int progress = 0;
};
// setInstallProgress.
void runtime_update_install(const std::function<void(InstallState &)> &update);
// The end of an installation: installed, or failed with status (install_failed, install_timeout) and message.
void finish_install(bool ok, const std::string &status, const std::string &message);
// installRuntime's start: "" when the installation thread is to run, else the output to answer with.
std::string begin_install();
// RuntimeInstallResult with output.
std::string install_result_json(const std::string &output);

// --- Sessions (session_history.go) ---

void sessions_list(http::Request &request, http::Response &response);
void sessions_get(http::Request &request, http::Response &response);
void sessions_delete(http::Request &request, http::Response &response);

} // namespace nkos::ai::picoclaw
