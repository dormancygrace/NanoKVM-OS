// The PicoClaw service (service/picoclaw): the runtime's state and lifecycle, the session lock, control operations,
// screenshots and HID actions for the agent, its MCP tools, and the gateway relay's bookkeeping.

#include "ai/picoclaw_internal.hpp"

#include "ai/files.hpp"
#include "ai/keys.hpp"
#include "api/request.hpp"
#include "common/base64.hpp"
#include "common/log.hpp"

#include <algorithm>
#include <cerrno>
#include <cmath>
#include <dirent.h>
#include <filesystem>
#include <openssl/rand.h>
#include <set>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>

namespace nkos::ai::picoclaw {

using json::Value;
namespace control = input::control;
using Steady = std::chrono::steady_clock;

Platform &platform()
{
	static Platform p;
	return p;
}

void write_success(http::Response &res, const std::string &data_json)
{
	res.json(200, "{\"code\":0,\"data\":" + (data_json.empty() ? std::string("null") : data_json) + ",\"msg\":\"success\"}");
}

void write_error(http::Response &res, const Error &e, const std::string &data_json)
{
	// gin.H: keys in order.
	std::string out = "{\"code\":" + json::quote(e.code);
	if (!data_json.empty()) out += ",\"data\":" + data_json;
	if (e.index) out += ",\"index\":" + std::to_string(*e.index);
	out += ",\"message\":" + json::quote(e.message);
	if (!e.session_id.empty()) out += ",\"session_id\":" + json::quote(e.session_id);
	res.json(e.status ? e.status : 200, out + "}");
}

bool internal_token(std::string &token, std::string &error)
{
	static std::mutex mu;
	static std::string cached;
	std::lock_guard lock(mu);
	if (!cached.empty()) {
		token = cached;
		return true;
	}
	std::string data;
	int e = read_file(internal_token_file, data);
	if (e == 0) {
		std::string t = json::trim_space(data);
		if (!t.empty()) {
			cached = token = t;
			return true;
		}
	} else if (e != ENOENT) {
		error = "open " + internal_token_file + ": " + errno_text(e);
		return false;
	}
	unsigned char raw[32];
	RAND_bytes(raw, sizeof raw);
	static const char hex[] = "0123456789abcdef";
	std::string t;
	for (unsigned char c : raw) {
		t += hex[c >> 4];
		t += hex[c & 15];
	}
	auto slash = internal_token_file.rfind('/');
	std::string dir = internal_token_file.substr(0, slash);
	::mkdir(dir.c_str(), 0755);
	if (auto err = write_file(internal_token_file, t + "\n", 0600); !err.empty()) {
		error = err;
		return false;
	}
	cached = token = t;
	return true;
}

namespace {

constexpr auto status_refresh = std::chrono::seconds(2);
constexpr auto lock_duration = std::chrono::minutes(30);
constexpr auto start_timeout = std::chrono::seconds(15);
constexpr auto stop_timeout = std::chrono::seconds(15);
constexpr auto onboard_timeout = std::chrono::seconds(60);
constexpr auto start_wait = std::chrono::milliseconds(1500);
constexpr auto stop_wait = std::chrono::milliseconds(500);
constexpr auto ready_poll = std::chrono::milliseconds(500);
constexpr auto gateway_exit_wait = std::chrono::seconds(3);
constexpr auto max_control_operation = std::chrono::seconds(35);
constexpr int max_wait_ms = 30000;
constexpr const char *cause_mode_switch = "PicoClaw control operation canceled by control mode switch";
constexpr const char *cause_timeout = "PicoClaw control operation timed out";
constexpr const char *session_header = "X-PicoClaw-Session-ID";

// --- Stores ---

struct RuntimeStatus {
	bool ready = false, installed = false, installing = false;
	int install_progress = 0;
	std::string install_stage, install_path, agent_profile;
	bool model_configured = false;
	std::string model_name, status, config_error, last_error;
	WallClock::time_point checked_at{};
	std::string current_session;
	bool restoring = false;
	// Filled in for answers.
	Intent intent;
	std::string control_mode;
	bool transitioning = false;
	control::ModeStatus control;
	bool can_control = false, chat = false, read_only_tools = false, device_write = false;
};

std::string status_json(const RuntimeStatus &s)
{
	auto b = [](bool v) { return v ? "true" : "false"; };
	std::string out = std::string("{\"ready\":") + b(s.ready) + ",\"installed\":" + b(s.installed) + ",\"installing\":" + b(s.installing);
	if (s.install_progress) out += ",\"install_progress\":" + std::to_string(s.install_progress);
	if (!s.install_stage.empty()) out += ",\"install_stage\":" + json::quote(s.install_stage);
	if (!s.install_path.empty()) out += ",\"install_path\":" + json::quote(s.install_path);
	if (!s.agent_profile.empty()) out += ",\"agent_profile\":" + json::quote(s.agent_profile);
	out += std::string(",\"model_configured\":") + b(s.model_configured);
	if (!s.model_name.empty()) out += ",\"model_name\":" + json::quote(s.model_name);
	out += ",\"status\":" + json::quote(s.status);
	if (!s.config_error.empty()) out += ",\"config_error\":" + json::quote(s.config_error);
	if (!s.last_error.empty()) out += ",\"last_error\":" + json::quote(s.last_error);
	out += ",\"checked_at\":\"" + rfc3339nano(s.checked_at) + "\"";
	if (!s.current_session.empty()) out += ",\"current_session\":" + json::quote(s.current_session);
	if (s.restoring) out += ",\"restoring\":true";
	out += ",\"runtime_intent\":" + intent_json(s.intent);
	out += ",\"control_mode\":" + json::quote(s.control_mode);
	if (s.transitioning) out += ",\"transitioning\":true";
	out += ",\"control\":{\"mode\":" + json::quote(control::mode_name(s.control.mode)) + ",\"transitioning\":" + b(s.control.transitioning) +
	       ",\"can_control\":" + b(s.can_control);
	if (!s.control.last_error.empty()) out += ",\"last_error\":" + json::quote(s.control.last_error);
	out += ",\"changed_at\":\"" + rfc3339nano(s.control.changed_at) + "\"}";
	out += std::string(",\"capabilities\":{\"chat\":") + b(s.chat) + ",\"read_only_tools\":" + b(s.read_only_tools) + ",\"device_write\":" + b(s.device_write) + "}}";
	return out;
}

std::mutex runtime_mu;
RuntimeStatus runtime_state = [] {
	RuntimeStatus s;
	s.status = "checking";
	s.install_path = binary_path;
	return s;
}();

RuntimeStatus runtime_get()
{
	std::lock_guard lock(runtime_mu);
	return runtime_state;
}
void runtime_set(RuntimeStatus s)
{
	std::lock_guard lock(runtime_mu);
	s.install_path = binary_path;
	runtime_state = s;
}
template <typename F>
void runtime_update(F &&f)
{
	std::lock_guard lock(runtime_mu);
	f(runtime_state);
	runtime_state.install_path = binary_path;
}
bool lifecycle_pending(const RuntimeStatus &s) { return s.restoring || s.status == "starting" || s.status == "restoring" || s.status == "stopping"; }
void runtime_set_from_probe(RuntimeStatus s)
{
	std::lock_guard lock(runtime_mu);
	if (lifecycle_pending(runtime_state) && !s.ready) {
		runtime_state.checked_at = s.checked_at;
		runtime_state.current_session = s.current_session;
		runtime_state.install_path = binary_path;
		return;
	}
	s.install_path = binary_path;
	runtime_state = s;
}

// The gateway connection settings (ConfigStore).
struct GatewayConfig {
	std::string gateway_url = "ws://127.0.0.1:18790/pico/ws";
	int connect_timeout_ms = 10000, read_timeout_ms = 60000, write_timeout_ms = 10000, ping_interval_ms = 30000;
	int max_message_bytes = 1024 * 1024;
	bool allow_token_query = false;
	std::string token;
};
std::mutex config_mu;
std::optional<GatewayConfig> gateway_config;

GatewayConfig default_gateway_config()
{
	GatewayConfig c;
	std::string error;
	if (auto s = load_gateway_settings(error)) {
		c.gateway_url = s->gateway_url;
		c.token = s->token;
		c.allow_token_query = s->allow_token_query;
		if (s->ping_interval_ms > 0) c.ping_interval_ms = s->ping_interval_ms;
		if (s->read_timeout_ms > 0) c.read_timeout_ms = s->read_timeout_ms;
	}
	return c;
}
GatewayConfig config_get()
{
	{
		std::lock_guard lock(config_mu);
		if (gateway_config) return *gateway_config;
	}
	auto c = default_gateway_config();
	std::lock_guard lock(config_mu);
	if (!gateway_config) gateway_config = c;
	return *gateway_config;
}
void config_set(const GatewayConfig &c)
{
	std::lock_guard lock(config_mu);
	gateway_config = c;
}

// SessionLock.
std::mutex lock_mu;
std::string lock_owner;
Steady::time_point lock_acquired{}, lock_expires{};

void lock_clear_expired(Steady::time_point now)
{
	if (lock_owner.empty()) return;
	if (lock_expires == Steady::time_point{} || now < lock_expires) return;
	lock_owner.clear();
	lock_acquired = lock_expires = {};
}
std::optional<Error> lock_acquire(const std::string &id, bool &acquired)
{
	acquired = false;
	if (id.empty()) return make_error(code_session_id_invalid, "invalid PicoClaw session");
	std::lock_guard lock(lock_mu);
	auto now = Steady::now();
	lock_clear_expired(now);
	if (lock_owner.empty() || lock_owner == id) {
		acquired = lock_owner.empty();
		lock_owner = id;
		if (lock_acquired == Steady::time_point{}) lock_acquired = now;
		lock_expires = now + lock_duration;
		return std::nullopt;
	}
	auto e = make_error(code_lock_held, "another PicoClaw session is running");
	e.session_id = lock_owner;
	return e;
}
bool lock_renew(const std::string &id)
{
	if (id.empty()) return false;
	std::lock_guard lock(lock_mu);
	if (lock_owner != id) return false;
	lock_expires = Steady::now() + lock_duration;
	return true;
}
bool lock_release(const std::string &id)
{
	std::lock_guard lock(lock_mu);
	if (lock_owner.empty()) return true;
	if (!id.empty() && lock_owner != id) return false;
	lock_owner.clear();
	lock_acquired = lock_expires = {};
	return true;
}
bool lock_release_owned(const std::string &id)
{
	if (id.empty()) return false;
	std::lock_guard lock(lock_mu);
	lock_clear_expired(Steady::now());
	if (lock_owner != id) return false;
	lock_owner.clear();
	lock_acquired = lock_expires = {};
	return true;
}
std::string lock_get_owner()
{
	std::lock_guard lock(lock_mu);
	lock_clear_expired(Steady::now());
	return lock_owner;
}

// SessionManager.
struct GatewaySession {
	std::string id;
	std::string state = "created";
	std::shared_ptr<Link> downstream, upstream;
	std::once_flag closed;
};
std::mutex sessions_mu;
std::map<std::string, std::shared_ptr<GatewaySession>> sessions;

std::shared_ptr<GatewaySession> session_get(const std::string &id)
{
	std::lock_guard lock(sessions_mu);
	auto it = sessions.find(id);
	return it == sessions.end() ? nullptr : it->second;
}
std::vector<std::shared_ptr<GatewaySession>> session_snapshot()
{
	std::lock_guard lock(sessions_mu);
	std::vector<std::shared_ptr<GatewaySession>> out;
	for (auto &[k, v] : sessions) out.push_back(v);
	return out;
}
void session_set_state(const std::string &id, const std::string &state)
{
	std::lock_guard lock(sessions_mu);
	if (auto it = sessions.find(id); it != sessions.end()) it->second->state = state;
}
void session_remove(const std::string &id)
{
	std::lock_guard lock(sessions_mu);
	sessions.erase(id);
}

// controlOperationTracker.
std::mutex ops_mu;
std::uint64_t next_op = 0;
std::map<std::uint64_t, std::shared_ptr<control::Cancel>> ops;

struct Operation {
	std::shared_ptr<control::Cancel> cancel;
	std::uint64_t id = 0;
	~Operation()
	{
		if (!cancel) return;
		cancel->cancel("context canceled");
		std::lock_guard lock(ops_mu);
		ops.erase(id);
	}
};
std::unique_ptr<Operation> begin_operation(bool timed)
{
	auto op = std::make_unique<Operation>();
	op->cancel = timed ? std::make_shared<control::Cancel>(Steady::now() + max_control_operation, cause_timeout) : std::make_shared<control::Cancel>();
	std::lock_guard lock(ops_mu);
	op->id = ++next_op;
	ops[op->id] = op->cancel;
	return op;
}

std::optional<Error> control_operation_error(const control::Cancel &c)
{
	std::string cause = c.cause();
	if (cause.empty()) return std::nullopt;
	if (cause == cause_mode_switch) return make_error(code_mode_conflict, "PicoClaw control operation canceled because the control mode is switching");
	if (cause == cause_timeout) return make_error(code_invalid_action, "PicoClaw control operation exceeded the 35 second limit");
	return make_error(code_invalid_action, "PicoClaw control operation was canceled");
}
std::optional<Error> lifecycle_error(const control::Cancel *c)
{
	if (!c) return std::nullopt;
	std::string cause = c->cause();
	if (cause.empty()) return std::nullopt;
	if (cause == cause_mode_switch) return make_error(code_mode_conflict, "PicoClaw runtime lifecycle canceled because the control mode is switching");
	return make_error(code_runtime_unavailable, "PicoClaw runtime lifecycle was canceled");
}
std::optional<Error> wait_operation(const control::Cancel &c, std::chrono::milliseconds delay)
{
	if (auto e = control_operation_error(c)) return e;
	if (delay.count() <= 0) return std::nullopt;
	if (!c.sleep(delay)) return control_operation_error(c);
	return std::nullopt;
}

// Capture leases of running tasks.
std::mutex lease_mu;
std::map<std::string, std::function<void()>> leases;
std::map<std::string, std::function<void()>> lease_timers;

std::mutex lifecycle_mu;  // runtimeLifecycleMu

// --- Helpers ---

std::string session_header_value(const http::Request &req) { return req.header(session_header); }

bool path_exists(const std::string &p)
{
	struct stat st{};
	return ::stat(p.c_str(), &st) == 0;
}

// isPicoclawInstalled: (installed, error).
bool installed_state(bool &installed, std::string &error)
{
	struct stat st{};
	if (::stat(binary_path.c_str(), &st) == 0) {
		installed = !S_ISDIR(st.st_mode);
		return true;
	}
	if (errno == ENOENT) {
		installed = false;
		return true;
	}
	error = "stat " + binary_path + ": " + errno_text(errno);
	return false;
}

std::string start_script(std::string &error)
{
	if (path_exists(kvmapp_script)) return kvmapp_script;
	if (path_exists(etc_init_script)) return etc_init_script;
	error = "picoclaw start script not found: " + etc_init_script + " or " + kvmapp_script;
	return {};
}

bool gateway_cmdline(const std::string &data)
{
	std::vector<std::string> args;
	std::size_t start = 0;
	while (start <= data.size()) {
		auto z = data.find('\0', start);
		std::string part = json::trim_space(data.substr(start, z == std::string::npos ? std::string::npos : z - start));
		if (!part.empty()) args.push_back(part);
		if (z == std::string::npos) break;
		start = z + 1;
	}
	if (args.size() < 2) return false;
	auto base = [](const std::string &p) {
		auto s = p.rfind('/');
		return s == std::string::npos ? p : p.substr(s + 1);
	};
	return base(args[0]) == base(binary_path) && args[1] == "gateway";
}

int gateway_process(int pid, std::string &error)
{
	if (pid <= 0) return 0;
	std::string data;
	int e = read_file("/proc/" + std::to_string(pid) + "/cmdline", data);
	if (e == ENOENT || e == ESRCH) return 0;
	if (e != 0) {
		error = "open /proc/" + std::to_string(pid) + "/cmdline: " + errno_text(e);
		return -1;
	}
	return gateway_cmdline(data) ? 1 : 0;
}

// runtimeGatewayPID.
int gateway_pid(std::string &error)
{
	std::string h = home(error);
	if (!error.empty()) return 0;
	std::string pid_path = h + "/.picoclaw.pid";
	std::string data;
	int e = read_file(pid_path, data);
	if (e == 0) {
		long pid = std::strtol(json::trim_space(data).c_str(), nullptr, 10);
		std::string t = json::trim_space(data);
		bool numeric = !t.empty() && t.find_first_not_of("0123456789-+") == std::string::npos;
		if (numeric && pid > 0) {
			int r = gateway_process(static_cast<int>(pid), error);
			if (r < 0) return 0;
			if (r > 0) return static_cast<int>(pid);
		}
		::unlink(pid_path.c_str());
	} else if (e != ENOENT) {
		::unlink(pid_path.c_str());
	}
	DIR *d = ::opendir("/proc");
	if (!d) return 0;
	int found = 0;
	while (struct dirent *ent = ::readdir(d)) {
		std::string name = ent->d_name;
		if (name.find_first_not_of("0123456789") != std::string::npos) continue;
		int pid = std::atoi(name.c_str());
		if (pid <= 0) continue;
		std::string ignored;
		if (gateway_process(pid, ignored) > 0) {
			found = pid;
			break;
		}
	}
	::closedir(d);
	if (found > 0) {
		::mkdir(h.c_str(), 0755);
		(void)write_file(pid_path, std::to_string(found) + "\n", 0600);
	}
	return found;
}

bool runtime_running(bool &running, std::string &error)
{
	error.clear();
	int pid = gateway_pid(error);
	running = pid > 0;
	return error.empty();
}

// url.QueryEscape.
std::string query_escape(const std::string &s)
{
	static const char hex[] = "0123456789ABCDEF";
	std::string out;
	for (unsigned char c : s) {
		if (std::isalnum(c) || c == '-' || c == '_' || c == '.' || c == '~') out += static_cast<char>(c);
		else if (c == ' ') out += '+';
		else {
			out += '%';
			out += hex[c >> 4];
			out += hex[c & 15];
		}
	}
	return out;
}

bool build_gateway_url(const GatewayConfig &cfg, const std::string &session_id, std::string &url, std::string &error)
{
	const std::string &u = cfg.gateway_url;
	auto colon = u.find("://");
	std::string scheme = colon == std::string::npos ? "" : json::to_lower(u.substr(0, colon));
	if (colon == std::string::npos && u.find(':') != std::string::npos && u.find(':') < u.find('/')) scheme = u.substr(0, u.find(':'));
	if (scheme != "ws" && scheme != "wss") {
		error = "invalid gateway url scheme: " + scheme;
		return false;
	}
	std::string rest = u.substr(colon + 3);
	std::string fragment;
	if (auto h = rest.find('#'); h != std::string::npos) rest.resize(h);
	std::string query;
	if (auto q = rest.find('?'); q != std::string::npos) {
		query = rest.substr(q + 1);
		rest.resize(q);
	}
	auto slash = rest.find('/');
	std::string host = rest.substr(0, slash), path = slash == std::string::npos ? "" : rest.substr(slash);
	if (host.empty()) {
		error = "invalid gateway url host";
		return false;
	}
	std::map<std::string, std::vector<std::string>> values;
	std::size_t start = 0;
	while (start < query.size()) {
		auto amp = query.find('&', start);
		std::string pair = query.substr(start, amp == std::string::npos ? std::string::npos : amp - start);
		if (!pair.empty()) {
			auto eq = pair.find('=');
			values[http::url_decode(pair.substr(0, eq))].push_back(eq == std::string::npos ? "" : http::url_decode(pair.substr(eq + 1)));
		}
		if (amp == std::string::npos) break;
		start = amp + 1;
	}
	values["session_id"] = {session_id};
	if (!cfg.token.empty() && cfg.allow_token_query) values["token"] = {cfg.token};
	std::string encoded;
	for (const auto &[k, vs] : values)
		for (const auto &v : vs) encoded += (encoded.empty() ? "" : "&") + query_escape(k) + "=" + query_escape(v);
	url = scheme + "://" + host + path + "?" + encoded;
	return true;
}

struct ProbeError {
	std::string status, config_error, last_error, message;
};

std::optional<ProbeError> probe_gateway(const GatewayConfig &cfg)
{
	std::string url, error;
	if (!build_gateway_url(cfg, "runtime-probe", url, error)) return ProbeError{"config_error", error, error, "gateway config is invalid"};
	auto timeout = std::chrono::milliseconds(cfg.connect_timeout_ms > 0 ? cfg.connect_timeout_ms : 10000);
	if (!platform().probe) return ProbeError{"unavailable", {}, "gateway probe is unavailable", "gateway websocket is unavailable"};
	int status = platform().probe(url, cfg.token, timeout, error);
	if (status == 0) return std::nullopt;
	ProbeError p{"unavailable", {}, error, "gateway websocket is unavailable"};
	if (status < 0) return p;
	if (status == 401 || status == 403) {
		p.status = "config_error";
		p.config_error = "gateway authentication failed";
		p.message = "gateway authentication failed";
	} else if (status == 404) {
		p.last_error = p.message = "gateway pico channel is unavailable";
	} else {
		p.last_error = "gateway websocket handshake failed: HTTP " + std::to_string(status);
		p.message = "gateway websocket handshake failed";
	}
	return p;
}

std::optional<Error> sync_config();
std::optional<Error> ensure_ready(bool allow_overwrite, const control::Cancel *cancel);
void apply_disabled_intent_status();

// runPicoclawOnboardContext.
std::optional<Error> run_onboard(const control::Cancel *cancel, std::string &output)
{
	std::string error;
	std::string script = start_script(error);
	if (script.empty()) return make_error(code_runtime_unavailable, error);
	if (auto e = lifecycle_error(cancel)) return e;
	std::string err;
	int code = platform().run ? platform().run(script + " onboard", onboard_timeout, output, err) : -1;
	output = json::trim_space(output);
	if (code != 0) {
		if (auto e = lifecycle_error(cancel)) return e;
		if (output.empty()) output = err;
		return make_error(code_runtime_unavailable, "failed to initialize picoclaw config");
	}
	return std::nullopt;
}

RuntimeStatus base_status(bool installed, const std::string &status)
{
	RuntimeStatus s;
	s.installed = installed;
	s.install_path = binary_path;
	s.status = status;
	s.checked_at = WallClock::now();
	s.current_session = lock_get_owner();
	return s;
}

std::optional<Error> ensure_ready(bool allow_overwrite, const control::Cancel *cancel)
{
	if (auto e = lifecycle_error(cancel)) return e;
	auto set_status = [&](const RuntimeStatus &s) {
		if (allow_overwrite) runtime_set(s);
		else runtime_set_from_probe(s);
	};
	auto current = runtime_get();
	if (current.installing) return make_error(code_runtime_unavailable, "picoclaw installation is in progress");
	if (!allow_overwrite && lifecycle_pending(current)) return make_error(code_runtime_unavailable, "picoclaw runtime lifecycle operation is pending");
	if (!allow_overwrite) {
		std::string error;
		auto intent = intent_load(error);
		if (!error.empty() || !intent.desired_running) {
			apply_disabled_intent_status();
			if (!error.empty()) return make_error(code_runtime_unavailable, error);
			return make_error(code_runtime_unavailable, "picoclaw runtime is disabled");
		}
	}
	bool installed = false;
	std::string error;
	if (!installed_state(installed, error)) {
		auto s = base_status(false, "not_installed");
		s.last_error = error;
		set_status(s);
		return make_error(code_runtime_unavailable, "failed to check picoclaw installation");
	}
	if (!installed) {
		set_status(base_status(false, "not_installed"));
		return make_error(code_runtime_unavailable, "picoclaw is not installed");
	}
	std::string path = config_path(error);
	if (!error.empty()) {
		auto s = base_status(true, "model_not_configured");
		s.last_error = error;
		set_status(s);
		return make_error(code_runtime_unavailable, "picoclaw model is not configured");
	}
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0) {
		int e = errno;
		if (e == ENOENT) {
			if (auto le = lifecycle_error(cancel)) return le;
			std::string output;
			if (auto oe = run_onboard(cancel, output)) {
				auto s = base_status(true, "model_not_configured");
				s.last_error = oe->message;
				set_status(s);
				return make_error(code_runtime_unavailable, "picoclaw model is not configured");
			}
			if (auto le = lifecycle_error(cancel)) return le;
			if (::stat(path.c_str(), &st) != 0) {
				set_status(base_status(true, "model_not_configured"));
				return make_error(code_runtime_unavailable, "picoclaw model is not configured");
			}
		} else {
			auto s = base_status(true, "config_error");
			s.config_error = s.last_error = "stat " + path + ": " + errno_text(e);
			set_status(s);
			return make_error(code_runtime_unavailable, "gateway config is invalid");
		}
	}
	if (auto e = sync_config()) return e;
	auto settings = load_gateway_settings(error);
	if (!settings) return make_error(code_runtime_unavailable, error);
	if (!settings->model_configured) {
		auto s = base_status(true, "model_not_configured");
		s.model_name = settings->target_model_name;
		set_status(s);
		return make_error(code_runtime_unavailable, "picoclaw model is not configured");
	}
	bool running = false;
	if (!runtime_running(running, error)) {
		auto s = base_status(true, "unavailable");
		s.model_configured = true;
		s.model_name = settings->model_name;
		s.last_error = error;
		set_status(s);
		return make_error(code_runtime_unavailable, "failed to check picoclaw runtime");
	}
	if (!running) {
		auto s = base_status(true, "stopped");
		s.model_configured = true;
		s.model_name = settings->model_name;
		set_status(s);
		return make_error(code_runtime_unavailable, "picoclaw runtime is stopped");
	}
	if (auto p = probe_gateway(config_get())) {
		auto s = base_status(true, p->status);
		s.model_configured = true;
		s.model_name = settings->model_name;
		s.config_error = p->config_error;
		s.last_error = p->last_error;
		set_status(s);
		return make_error(code_runtime_unavailable, p->message);
	}
	auto s = base_status(true, "ready");
	s.ready = true;
	s.install_progress = 100;
	s.model_configured = true;
	s.model_name = settings->model_name;
	set_status(s);
	return std::nullopt;
}

std::optional<Error> sync_config()
{
	bool installed = false;
	std::string ierr;
	bool known = installed_state(installed, ierr);
	auto config_failed = [&](const std::string &message) {
		runtime_update([&](RuntimeStatus &s) {
			s.ready = false;
			s.status = "config_error";
			s.config_error = s.last_error = message;
			if (known) s.installed = installed;
			s.checked_at = WallClock::now();
		});
		return make_error(code_runtime_unavailable, message);
	};
	std::string error;
	std::string path = config_path(error);
	if (!error.empty()) return config_failed(error);
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0 && errno != ENOENT) return config_failed("failed to stat picoclaw config: stat " + path + ": " + errno_text(errno));
	auto settings = load_gateway_settings(error);
	if (!settings) return config_failed(error);
	auto cfg = config_get();
	cfg.gateway_url = settings->gateway_url;
	cfg.token = settings->token;
	cfg.allow_token_query = settings->allow_token_query;
	if (settings->ping_interval_ms > 0) cfg.ping_interval_ms = settings->ping_interval_ms;
	if (settings->read_timeout_ms > 0) cfg.read_timeout_ms = settings->read_timeout_ms;
	config_set(cfg);
	runtime_update([&](RuntimeStatus &s) {
		s.installed = true;
		s.model_configured = settings->model_configured;
		s.model_name = settings->model_configured ? settings->model_name : settings->target_model_name;
		s.config_error.clear();
		if (s.status == "config_error" && s.ready) s.status = "ready";
		s.checked_at = WallClock::now();
	});
	return std::nullopt;
}

void apply_disabled_intent_status()
{
	bool installed = false;
	std::string ierr, serr;
	bool known = installed_state(installed, ierr);
	auto settings = load_gateway_settings(serr);
	runtime_update([&](RuntimeStatus &s) {
		s.ready = false;
		s.restoring = false;
		s.current_session.clear();
		s.checked_at = WallClock::now();
		if (known) s.installed = installed;
		if (known && !installed) {
			s.model_configured = false;
			s.model_name.clear();
			s.status = "not_installed";
			s.last_error.clear();
			return;
		}
		if (settings) {
			s.model_configured = settings->model_configured;
			s.model_name = settings->model_name;
			if (!settings->model_configured) {
				s.model_name = settings->target_model_name;
				s.status = "model_not_configured";
				return;
			}
		}
		s.status = "stopped";
		if (s.last_error == "picoclaw runtime is stopped") s.last_error.clear();
	});
}

RuntimeStatus apply_status(RuntimeStatus s)
{
	std::string error;
	auto mode = control::mode_status(&error);
	if (!error.empty()) {
		s.control_mode = "off";
		s.intent = {};
		return s;
	}
	s.control_mode = control::mode_name(mode.mode);
	s.transitioning = mode.transitioning;
	bool can = mode.mode == control::Mode::picoclaw && !mode.transitioning;
	bool usable = s.ready && s.installed && s.model_configured && !s.installing;
	bool chat = usable && mode.mode != control::Mode::mcp && !mode.transitioning;
	s.control = mode;
	s.can_control = can;
	s.chat = s.read_only_tools = chat;
	s.device_write = can;
	std::string ierr;
	s.intent = intent_load(ierr);
	if (!ierr.empty() && s.intent.last_error.empty()) s.intent.last_error = ierr;
	s.restoring = s.restoring || s.status == "restoring";
	return s;
}

std::string current_status_json(bool with_profile = false)
{
	auto s = apply_status(runtime_get());
	if (with_profile) s.agent_profile = detect_agent_profile();
	return status_json(s);
}

Error control_write_error(control::Mode expected)
{
	std::string error;
	auto status = control::mode_status(&error);
	if (!error.empty()) return make_error(code_runtime_unavailable, error);
	std::string message = "PicoClaw does not own device control", code = code_control_required;
	if (status.transitioning) {
		code = code_transitioning;
		message = "device control is switching";
	} else if (expected == control::Mode::picoclaw) {
		if (status.mode == control::Mode::mcp) {
			code = code_owned_by_mcp;
			message = "external MCP owns device control";
		} else if (status.mode == control::Mode::off) {
			code = code_control_required;
			message = "PicoClaw device control is not enabled";
		} else {
			code = code_mode_conflict;
		}
	}
	return make_error(code, message);
}

std::unique_ptr<control::Hold> acquire_control(Error &err)
{
	std::string error;
	auto hold = control::acquire_write(control::Mode::picoclaw, error);
	if (!hold) err = control_write_error(control::Mode::picoclaw);
	return hold;
}

void set_intent_desired(bool desired, const std::string &by)
{
	if (auto e = intent_set_desired(desired, by); !e.empty()) log::warn("failed to persist PicoClaw runtime intent: {}", e);
}
void set_intent_error(const std::string &message)
{
	if (message.empty()) return;
	if (auto e = intent_set_last_error(message); !e.empty()) log::warn("failed to persist PicoClaw runtime intent error: {}", e);
}

// --- Lifecycle ---

std::optional<Error> stop_runtime_cmd()
{
	std::string error;
	auto settings = load_gateway_settings(error);
	std::string script = start_script(error);
	RuntimeStatus s;
	s.installed = true;
	if (settings) {
		s.model_configured = settings->model_configured;
		s.model_name = settings->model_name;
	}
	if (script.empty()) {
		s.status = "unavailable";
		s.last_error = error;
		s.checked_at = WallClock::now();
		runtime_set(s);
		return make_error(code_runtime_start_failed, error);
	}
	std::string output, err;
	int code = platform().run ? platform().run(script + " stop", stop_timeout, output, err) : -1;
	output = json::trim_space(output);
	if (code != 0) {
		s.status = "error";
		s.last_error = output.empty() ? err : output;
		s.checked_at = WallClock::now();
		runtime_set(s);
		return make_error(code_runtime_start_failed, "failed to stop picoclaw runtime");
	}
	std::this_thread::sleep_for(stop_wait);
	s.status = "stopped";
	s.checked_at = WallClock::now();
	runtime_set(s);
	return std::nullopt;
}

std::string stop_and_verify(bool force)
{
	auto running = [](std::string &e) {
		bool r = false;
		runtime_running(r, e);
		return r;
	};
	std::string error;
	if (!force) {
		bool r = running(error);
		if (!error.empty()) return "check PicoClaw runtime: " + error;
		if (!r) {
			runtime_update([](RuntimeStatus &s) {
				s.ready = false;
				s.status = "stopped";
				s.current_session.clear();
				s.checked_at = WallClock::now();
			});
			return {};
		}
	}
	std::string stop_error;
	if (auto e = stop_runtime_cmd()) stop_error = "stop PicoClaw runtime: " + e->text();
	auto deadline = Steady::now() + stop_timeout;
	for (;;) {
		bool r = running(error);
		if (!error.empty()) return (stop_error.empty() ? "" : stop_error + "\n") + "verify PicoClaw stopped: " + error;
		if (!r) break;
		if (Steady::now() >= deadline) return (stop_error.empty() ? "" : stop_error + "\n") + "PicoClaw runtime is still running";
		std::this_thread::sleep_for(std::chrono::milliseconds(100));
	}
	if (!stop_error.empty()) log::warn("PicoClaw stop command returned an error after the runtime stopped: {}", stop_error);
	runtime_update([](RuntimeStatus &s) {
		s.ready = false;
		s.status = "stopped";
		s.current_session.clear();
		s.checked_at = WallClock::now();
	});
	return {};
}

void release_capture_lease(const std::string &key)
{
	std::function<void()> release, timer;
	{
		std::lock_guard lock(lease_mu);
		if (auto it = leases.find(key); it != leases.end()) {
			release = it->second;
			leases.erase(it);
		}
		if (auto it = lease_timers.find(key); it != lease_timers.end()) {
			timer = it->second;
			lease_timers.erase(it);
		}
	}
	if (timer) timer();
	if (release) release();
}

void release_session_leases(const std::string &session_id)
{
	if (session_id.empty()) return;
	std::string prefix = "task:" + session_id + ":";
	std::vector<std::string> keys;
	{
		std::lock_guard lock(lease_mu);
		for (const auto &[k, v] : leases)
			if (k.rfind(prefix, 0) == 0) keys.push_back(k);
	}
	for (const auto &k : keys) release_capture_lease(k);
}

void release_gateway_session(const std::string &id)
{
	bool release_hid = control::mode_status().mode == control::Mode::picoclaw;
	if (!lock_release_owned(id)) return;
	if (release_hid && platform().release_hid)
		if (auto e = platform().release_hid(); !e.empty()) log::error("failed to release HID state for PicoClaw session {}: {}", id, e);
}

void cleanup_media_dir()
{
	const char *tmp = std::getenv("TMPDIR");
	std::string dir = std::string(tmp && *tmp ? tmp : "/tmp") + "/picoclaw_media";
	struct stat st{};
	if (::stat(dir.c_str(), &st) != 0) return;
	std::error_code ec;
	std::filesystem::remove_all(dir, ec);
}

void close_session(const std::shared_ptr<GatewaySession> &session, int code, const std::string &reason)
{
	if (!session) return;
	std::call_once(session->closed, [&] {
		bool had_downstream = session->downstream != nullptr;
		release_session_leases(session->id);
		session_set_state(session->id, "closing");
		if (session->upstream) session->upstream->close(1000, "relay closing");
		if (session->downstream) session->downstream->close(code, reason);
		if (had_downstream) cleanup_media_dir();
		release_gateway_session(session->id);
		session_set_state(session->id, "closed");
		session_remove(session->id);
		runtime_update([&](RuntimeStatus &s) {
			s.current_session = lock_get_owner();
			if (code == 4005 || code == 4002) {
				s.ready = false;
				if (code == 4005) s.status = "unavailable";
				s.last_error = reason;
				s.checked_at = WallClock::now();
			}
		});
	});
}

std::string stop_and_close_sessions(int code, const std::string &reason)
{
	for (auto &s : session_snapshot()) close_session(s, code, reason);
	lock_release("");
	return stop_and_verify(false);
}

std::optional<Error> start_runtime_cmd(const control::Cancel *cancel, std::string &command, std::string &output)
{
	command.clear();
	output.clear();
	if (auto e = lifecycle_error(cancel)) return e;
	bool installed = false;
	std::string error;
	if (!installed_state(installed, error) || !installed) {
		std::string message = installed ? error : (error.empty() ? "picoclaw is not installed" : error);
		runtime_update([&](RuntimeStatus &s) {
			s.ready = false;
			s.installed = false;
			s.model_configured = false;
			s.model_name.clear();
			s.status = "not_installed";
			s.last_error = message;
			s.checked_at = WallClock::now();
		});
		return make_error(code_runtime_start_failed, message);
	}
	auto unavailable = [&](const std::string &status, const std::string &message, bool config) {
		runtime_update([&](RuntimeStatus &s) {
			s.ready = false;
			s.installed = true;
			s.status = status;
			if (config) s.config_error = message;
			s.last_error = message;
			s.checked_at = WallClock::now();
		});
		return make_error(code_runtime_start_failed, message);
	};
	std::string script = start_script(error);
	if (script.empty()) return unavailable("unavailable", error, false);
	if (auto e = ensure_startup_defaults(); !e.empty()) return unavailable("config_error", e, true);
	// detectGatewayPortConflict.
	bool running = false;
	if (!runtime_running(running, error)) return unavailable("unavailable", "check PicoClaw gateway process: " + error, false);
	if (!running && platform().port_in_use) {
		if (auto settings = load_gateway_settings(error)) {
			std::string url = settings->gateway_url;
			auto hp = url.substr(url.find("://") + 3);
			hp = hp.substr(0, hp.find('/'));
			if (hp.find(':') == std::string::npos) hp += url.rfind("wss", 0) == 0 ? ":443" : ":80";
			auto timeout = std::chrono::milliseconds(config_get().connect_timeout_ms > 0 ? config_get().connect_timeout_ms : 2000);
			if (platform().port_in_use(hp, timeout)) return unavailable("unavailable", "gateway port " + hp + " is already in use by another process", false);
		}
	}
	command = script + " start";
	std::string err;
	int code = platform().run ? platform().run("echo 0 >/proc/self/oom_score_adj; renice 0 \"$$\" >/dev/null; " + command, start_timeout, output, err) : -1;
	output = json::trim_space(output);
	if (code != 0) {
		if (auto e = lifecycle_error(cancel)) return e;
		runtime_update([&](RuntimeStatus &s) {
			s.ready = false;
			s.installed = true;
			s.status = "unavailable";
			s.last_error = output.empty() ? err : output;
			s.checked_at = WallClock::now();
		});
		return make_error(code_runtime_start_failed, "failed to start picoclaw runtime");
	}
	if (cancel && !cancel->sleep(start_wait)) return lifecycle_error(cancel);
	if (!cancel) std::this_thread::sleep_for(start_wait);
	// waitForRuntimeReadyContext.
	auto deadline = Steady::now() + start_timeout;
	std::optional<Error> last;
	for (;;) {
		if (auto e = lifecycle_error(cancel)) return e;
		last = ensure_ready(true, cancel);
		if (!last) return std::nullopt;
		if (Steady::now() > deadline) break;
		if (cancel) {
			if (!cancel->sleep(ready_poll)) return lifecycle_error(cancel);
		} else {
			std::this_thread::sleep_for(ready_poll);
		}
	}
	Error start_error = make_error(code_runtime_start_failed, last ? last->message : "gateway is unavailable");
	std::string failure = "unavailable";
	if (last && last->code == code_mode_conflict) {
		start_error = *last;
		failure = "starting";
	} else if (auto e = stop_and_verify(true); !e.empty()) {
		failure = "error";
		start_error.message += "; failed to stop partially started runtime: " + e;
	}
	runtime_update([&](RuntimeStatus &s) {
		s.ready = false;
		s.status = failure;
		s.last_error = start_error.message;
		s.current_session.clear();
		s.checked_at = WallClock::now();
	});
	return start_error;
}

std::optional<Error> stop_gateway_and_check()
{
	if (auto e = stop_runtime_cmd()) return e;
	auto deadline = Steady::now() + gateway_exit_wait;
	for (;;) {
		bool running = false;
		std::string error;
		if (!runtime_running(running, error)) return make_error(code_runtime_unavailable, "check picoclaw gateway process: " + error);
		if (!running) return std::nullopt;
		if (Steady::now() > deadline) return make_error(code_runtime_unavailable, "the picoclaw gateway is still running");
		std::this_thread::sleep_for(std::chrono::milliseconds(50));
	}
}

std::optional<Error> stop_live_gateway()
{
	bool running = false;
	std::string error;
	if (!runtime_running(running, error)) return make_error(code_runtime_unavailable, "check picoclaw gateway process: " + error);
	if (!running) return std::nullopt;
	return stop_gateway_and_check();
}

// applyConfigChange: false when the error was written.
bool apply_config_change(http::Response &res, const std::string &what, const std::string &source, bool revoke,
                         const std::function<std::optional<Error>()> &change)
{
	auto ready = [](const RuntimeStatus &s) { return s.ready || s.status == "ready"; };
	bool restart = ready(runtime_get());
	std::unique_ptr<control::Hold> hold;
	auto acquire = [&] {
		Error err;
		hold = acquire_control(err);
		if (hold) return true;
		if (revoke) {
			restart = false;
			return true;
		}
		write_error(res, err, "{\"status\":" + current_status_json() + "}");
		return false;
	};
	if (restart && !acquire()) return false;
	std::lock_guard lifecycle(lifecycle_mu);
	if (!hold && ready(runtime_get())) {
		restart = true;
		if (!acquire()) return false;
	}
	if (auto err = change()) {
		if (revoke)
			if (auto stop = stop_live_gateway())
				err = make_error(err->code, err->message + "; the running picoclaw gateway could not be stopped: " + stop->message);
		write_error(res, *err);
		return false;
	}
	if (restart) {
		if (auto stop = stop_gateway_and_check()) {
			write_error(res, make_error(code_runtime_unavailable, what + " saved, but failed to restart picoclaw runtime: " + stop->message));
			return false;
		}
		std::string command, output;
		if (auto start = start_runtime_cmd(nullptr, command, output)) {
			write_error(res, make_error(code_runtime_unavailable, what + " saved, but failed to restart picoclaw runtime: " + start->message));
			return false;
		}
		set_intent_desired(true, source);
		return true;
	}
	if (revoke)
		if (auto stop = stop_live_gateway()) {
			write_error(res, make_error(code_runtime_unavailable, what + " saved, but the running picoclaw gateway could not be stopped: " + stop->message));
			return false;
		}
	(void)sync_config();
	(void)ensure_ready(true, nullptr);
	return true;
}

// --- Screenshots and actions ---

void fit_within(std::uint16_t sw, std::uint16_t sh, std::uint16_t mw, std::uint16_t mh, std::uint16_t &w, std::uint16_t &h)
{
	w = sw;
	h = sh;
	if (sw == 0 || sh == 0 || (mw == 0 && mh == 0)) return;
	int width = sw, height = sh, lw = width, lh = height;
	if (mw > 0 && lw > mw) {
		lw = mw;
		lh = height * lw / width;
	}
	if (mh > 0 && lh > mh) {
		lh = mh;
		lw = width * lh / height;
	}
	if (lw <= 0) lw = 1;
	if (lh <= 0) lh = 1;
	w = static_cast<std::uint16_t>(lw);
	h = static_cast<std::uint16_t>(lh);
}

struct ScreenshotQuery {
	std::string format;
	std::uint16_t width = 0, height = 0, quality = 0;
};

struct Meta {
	std::string image_base64;
	std::uint16_t source_width = 0, source_height = 0, capture_width = 0, capture_height = 0;
};

std::optional<Error> capture_screenshot(const control::Cancel &cancel, const ScreenshotQuery &q, std::string &data, Meta &meta)
{
	std::uint16_t sw = 0, sh = 0, quality = 0;
	if (platform().screen) platform().screen(sw, sh, quality);
	std::uint16_t width = sw, height = sh;
	if (q.format == "base64") {
		fit_within(sw, sh, 960, 540, width, height);
		if (quality == 0 || quality > 60) quality = 60;
	}
	if (q.width > 0 && q.height > 0) {
		width = q.width;
		height = q.height;
	} else if (q.width > 0) {
		fit_within(width, height, q.width, 0, width, height);
	} else if (q.height > 0) {
		fit_within(width, height, 0, q.height, width, height);
	}
	if (q.quality > 0) quality = q.quality;
	std::unique_ptr<Platform::ReadLease> lease;
	if (platform().read_lease) {
		lease = platform().read_lease(cancel);
		if (!lease) return make_error(code_screenshot_failed, "screenshot capture canceled");
	}
	bool claim = lease != nullptr;
	for (int attempt = 0; attempt < 30; attempt++) {
		if (cancel.cancelled()) return make_error(code_screenshot_failed, "screenshot capture canceled");
		std::string jpeg;
		int result = platform().read_mjpeg ? platform().read_mjpeg(width, height, quality, jpeg) : -1;
		if (result == -8) return make_error(code_screenshot_failed, "MJPEG is unavailable while the HDMI input is 3840x2160: use H.264 or H.265");
		if (result == 5 || result == -3 || result == -4 || result == -5) {
			if (attempt < 29) {
				if (!cancel.sleep(std::chrono::milliseconds(100))) return make_error(code_screenshot_failed, "screenshot capture canceled");
				continue;
			}
			return make_error(code_screenshot_no_signal, "no HDMI signal or frame unavailable");
		}
		if (result < 0 || jpeg.empty()) return make_error(code_screenshot_failed, "failed to capture screenshot");
		if (claim && lease->claim_fresh()) {
			claim = false;
			continue;
		}
		data = std::move(jpeg);
		meta.source_width = sw;
		meta.source_height = sh;
		meta.capture_width = width;
		meta.capture_height = height;
		return std::nullopt;
	}
	return make_error(code_screenshot_failed, "failed to capture screenshot");
}

std::string meta_json(const Meta &m)
{
	std::string out = "{";
	if (!m.image_base64.empty()) out += "\"image_base64\":" + json::quote(m.image_base64) + ",";
	return out + "\"source_width\":" + std::to_string(m.source_width) + ",\"source_height\":" + std::to_string(m.source_height) +
	       ",\"capture_width\":" + std::to_string(m.capture_width) + ",\"capture_height\":" + std::to_string(m.capture_height) +
	       ",\"format\":\"jpeg\"}";
}

struct Point {
	std::optional<double> x, y;
};
struct Action {
	std::string action, button, text, direction;
	std::optional<double> x, y;
	std::optional<Point> from, to;
	std::vector<std::string> keys;
	long long amount = 0, duration_ms = 0;
};

// json.Unmarshal into Action (case-insensitive members, the last wins); false on a type error.
bool decode_action(const Value &v, Action &a)
{
	if (!v.is_object()) return v.is_null();
	bool ok = true;
	auto num = [&](const Value &x, std::optional<double> &out) {
		if (x.is_null()) out.reset();
		else if (x.is_number()) out = x.num;
		else ok = false;
	};
	auto integer = [&](const Value &x, long long &out) {
		if (x.is_null()) return;
		if (!x.is_number() || x.str.find_first_of(".eE") != std::string::npos) ok = false;
		else out = std::strtoll(x.str.c_str(), nullptr, 10);
	};
	auto str = [&](const Value &x, std::string &out) {
		if (x.is_null()) return;
		if (!x.is_string()) ok = false;
		else out = x.str;
	};
	auto point = [&](const Value &x, std::optional<Point> &out) {
		if (x.is_null()) {
			out.reset();
			return;
		}
		if (!x.is_object()) {
			ok = false;
			return;
		}
		Point p = out ? *out : Point{};
		for (const auto &[k, y] : x.obj) {
			if (json::to_lower(k) == "x") num(y, p.x);
			else if (json::to_lower(k) == "y") num(y, p.y);
		}
		out = p;
	};
	for (const auto &[k, x] : v.obj) {
		std::string key = json::to_lower(k);
		if (key == "action") str(x, a.action);
		else if (key == "x") num(x, a.x);
		else if (key == "y") num(x, a.y);
		else if (key == "from") point(x, a.from);
		else if (key == "to") point(x, a.to);
		else if (key == "button") str(x, a.button);
		else if (key == "text") str(x, a.text);
		else if (key == "direction") str(x, a.direction);
		else if (key == "amount") integer(x, a.amount);
		else if (key == "duration_ms") integer(x, a.duration_ms);
		else if (key == "keys") {
			// HotkeyKeys: a list, or a comma-separated string.
			if (x.is_null()) a.keys.clear();
			else if (x.is_array()) {
				std::vector<std::string> keys;
				for (const auto &item : x.arr) {
					if (!item.is_string() && !item.is_null()) {
						ok = false;
						break;
					}
					std::string t = json::trim_space(item.str);
					if (!t.empty()) keys.push_back(t);
				}
				a.keys = keys;
			} else if (x.is_string()) {
				a.keys.clear();
				if (!json::trim_space(x.str).empty()) {
					std::size_t start = 0;
					while (start <= x.str.size()) {
						auto comma = x.str.find(',', start);
						std::string t = json::trim_space(x.str.substr(start, comma == std::string::npos ? std::string::npos : comma - start));
						if (!t.empty()) a.keys.push_back(t);
						if (comma == std::string::npos) break;
						start = comma + 1;
					}
				}
			} else {
				ok = false;
			}
		}
	}
	return ok;
}

std::uint16_t hid_coord(double v)
{
	v = std::max(0.0, std::min(1.0, v));
	return static_cast<std::uint16_t>(static_cast<std::uint16_t>(std::floor(0x7fff * v)) + 1);
}

int mouse(double x, double y, std::uint8_t buttons, int wheel)
{
	auto ax = hid_coord(x), ay = hid_coord(y);
	platform().hid->post(HidDevice::absolute, {buttons, static_cast<std::uint8_t>(ax & 0xff), static_cast<std::uint8_t>(ax >> 8),
	                                           static_cast<std::uint8_t>(ay & 0xff), static_cast<std::uint8_t>(ay >> 8),
	                                           static_cast<std::uint8_t>(static_cast<std::int8_t>(wheel))});
	return 1;
}

int keyboard(std::vector<std::uint8_t> report)
{
	platform().hid->post(HidDevice::keyboard, std::move(report));
	return 1;
}

void release_all_hid()
{
	platform().hid->post(HidDevice::keyboard, {0, 0, 0, 0, 0, 0, 0, 0});
	platform().hid->post(HidDevice::relative, {0, 0, 0, 0});
	platform().hid->post(HidDevice::absolute, {0, 0, 0, 0, 0, 0});
}

std::optional<Error> point_of(const std::optional<double> &x, const std::optional<double> &y, double &ox, double &oy)
{
	if (!x || !y) return make_error(code_invalid_action, "action requires x and y");
	if (*x < 0 || *x > 1 || *y < 0 || *y > 1) return make_error(code_invalid_action, "coordinates must be within [0,1]");
	ox = *x;
	oy = *y;
	return std::nullopt;
}

std::optional<Error> button_of(const std::string &b, std::uint8_t &bit)
{
	std::string n = json::to_lower(json::trim_space(b));
	if (n.empty() || n == "left") bit = 1;
	else if (n == "right") bit = 2;
	else if (n == "middle") bit = 4;
	else if (n == "back") bit = 8;
	else if (n == "forward") bit = 16;
	else return make_error(code_invalid_action, "invalid mouse button");
	return std::nullopt;
}

std::optional<Error> execute_action(const control::Cancel &c, const Action &a, int &writes)
{
	writes = 0;
	if (auto e = control_operation_error(c)) return e;
	std::string kind = json::to_lower(json::trim_space(a.action));
	double x = 0, y = 0;
	std::uint8_t button = 0;
	if (kind == "click") {
		if (auto e = point_of(a.x, a.y, x, y)) return e;
		if (auto e = button_of(a.button, button)) return e;
		writes += mouse(x, y, 0, 0);
		writes += mouse(x, y, button, 0);
		if (auto e = wait_operation(c, std::chrono::milliseconds(40))) return e;
		writes += mouse(x, y, 0, 0);
		return std::nullopt;
	}
	if (kind == "move") {
		if (auto e = point_of(a.x, a.y, x, y)) return e;
		writes += mouse(x, y, 0, 0);
		return std::nullopt;
	}
	if (kind == "wait") {
		if (a.duration_ms < 0) return make_error(code_invalid_action, "wait duration must be >= 0");
		if (a.duration_ms > max_wait_ms) return make_error(code_invalid_action, "wait duration must be <= 30000 milliseconds");
		return wait_operation(c, std::chrono::milliseconds(a.duration_ms));
	}
	if (kind == "drag") {
		double fx, fy, tx, ty;
		if (!a.from) return make_error(code_invalid_action, "action requires point coordinates");
		if (auto e = point_of(a.from->x, a.from->y, fx, fy)) return e;
		if (!a.to) return make_error(code_invalid_action, "action requires point coordinates");
		if (auto e = point_of(a.to->x, a.to->y, tx, ty)) return e;
		if (auto e = button_of(a.button, button)) return e;
		writes += mouse(fx, fy, 0, 0);
		writes += mouse(fx, fy, button, 0);
		for (int step = 1; step <= 10; step++) {
			if (auto e = control_operation_error(c)) return e;
			double r = static_cast<double>(step) / 10;
			writes += mouse(fx + (tx - fx) * r, fy + (ty - fy) * r, button, 0);
		}
		writes += mouse(tx, ty, 0, 0);
		return std::nullopt;
	}
	if (kind == "scroll") {
		x = y = 0.5;
		if (a.x || a.y)
			if (auto e = point_of(a.x, a.y, x, y)) return e;
		long long amount = a.amount == 0 ? 1 : a.amount;
		if (amount < 0) return make_error(code_invalid_action, "scroll amount must be > 0");
		std::string dir = json::to_lower(json::trim_space(a.direction));
		int wheel;
		if (dir.empty() || dir == "up") wheel = 1;
		else if (dir == "down") wheel = -1;
		else return make_error(code_invalid_action, "invalid scroll direction");
		for (long long i = 0; i < amount; i++) {
			if (auto e = control_operation_error(c)) return e;
			writes += mouse(x, y, 0, wheel);
			writes += mouse(x, y, 0, 0);
			if (auto e = wait_operation(c, std::chrono::milliseconds(20))) return e;
		}
		return std::nullopt;
	}
	if (kind == "type") {
		if (a.text.empty()) return make_error(code_invalid_action, "type requires text");
		for (char32_t r : json::runes(a.text)) {
			if (auto e = control_operation_error(c)) return e;
			std::uint8_t mods, code;
			if (!us_char(r, mods, code)) {
				writes = 0;
				return make_error(code_invalid_action, "unsupported character in type action");
			}
			writes += keyboard({mods, 0, code, 0, 0, 0, 0, 0});
			writes += keyboard({0, 0, 0, 0, 0, 0, 0, 0});
			if (auto e = wait_operation(c, std::chrono::milliseconds(30))) return e;
		}
		return std::nullopt;
	}
	if (kind == "hotkey") {
		if (a.keys.empty()) return make_error(code_invalid_action, "hotkey requires at least one key");
		std::vector<std::uint8_t> report(8, 0);
		std::size_t next = 2;
		for (const auto &k : a.keys) {
			std::uint8_t code;
			bool modifier;
			std::string error;
			if (!picoclaw_key(k, code, modifier, error)) return make_error(code_invalid_action, error);
			if (modifier) {
				report[0] |= code;
				continue;
			}
			if (next >= report.size()) return make_error(code_invalid_action, "hotkey supports at most 6 non-modifier keys");
			report[next++] = code;
		}
		writes += keyboard(report);
		if (auto e = wait_operation(c, std::chrono::milliseconds(40))) return e;
		writes += keyboard({0, 0, 0, 0, 0, 0, 0, 0});
		return std::nullopt;
	}
	return make_error(code_invalid_action, "unknown or invalid action");
}

std::optional<Error> execute_actions(const control::Cancel &c, const std::string &session_id, const std::vector<Action> &list,
                                     std::string &result_json)
{
	auto started = Steady::now();
	if (list.empty()) return make_error(code_invalid_action, "empty actions");
	int total = 0;
	for (std::size_t i = 0; i < list.size(); i++) {
		auto fail = [&](Error e) {
			e.index = static_cast<int>(i);
			release_all_hid();
			return e;
		};
		if (auto e = control_operation_error(c)) return fail(*e);
		bool acquired;
		if (auto e = lock_acquire(session_id, acquired)) return fail(*e);
		int writes = 0;
		if (auto e = execute_action(c, list[i], writes)) return fail(*e);
		total += writes;
	}
	auto ms = std::chrono::duration_cast<std::chrono::milliseconds>(Steady::now() - started).count();
	std::string action = list.size() > 1 ? "batch" : list[0].action;
	result_json = "{\"action\":" + json::quote(action) + ",\"duration_ms\":" + std::to_string(ms) + ",\"hid_writes\":" + std::to_string(total);
	result_json += ",\"executed_actions\":" + std::to_string(list.size()) + "}";
	return std::nullopt;
}

// A gateway message (picoGatewayMessage): payload members sorted.
std::string gateway_message(const std::string &type, const std::string &id, const std::string &session_id, long long timestamp, Value payload)
{
	std::string out = "{\"type\":" + json::quote(type);
	if (!id.empty()) out += ",\"id\":" + json::quote(id);
	if (!session_id.empty()) out += ",\"session_id\":" + json::quote(session_id);
	if (timestamp) out += ",\"timestamp\":" + std::to_string(timestamp);
	if (payload.is_object() && !payload.obj.empty()) {
		json::sort_keys(payload);
		out += ",\"payload\":" + json::marshal(payload);
	}
	return out + "}";
}

long long now_ms()
{
	if (platform().now_ms) return platform().now_ms();
	return std::chrono::duration_cast<std::chrono::milliseconds>(WallClock::now().time_since_epoch()).count();
}

std::string new_uuid()
{
	if (platform().uuid) return platform().uuid();
	unsigned char b[16];
	RAND_bytes(b, sizeof b);
	b[6] = (b[6] & 0x0f) | 0x40;
	b[8] = (b[8] & 0x3f) | 0x80;
	char out[37];
	std::snprintf(out, sizeof out, "%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x", b[0], b[1], b[2], b[3], b[4],
	              b[5], b[6], b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15]);
	return out;
}

// requireActiveGatewaySession.
std::shared_ptr<GatewaySession> active_session(const http::Request &req, std::string &session_id, std::optional<Error> &err)
{
	session_id = json::trim_space(session_header_value(req));
	if (session_id.empty()) session_id = json::trim_space(lock_get_owner());
	if (session_id.empty()) {
		err = make_error(code_session_id_missing, "missing X-PicoClaw-Session-ID");
		return nullptr;
	}
	auto session = session_get(session_id);
	if (!session) {
		err = make_error(code_runtime_unavailable, "picoclaw session is not connected");
		err->session_id = session_id;
		return nullptr;
	}
	std::string state;
	std::shared_ptr<Link> up;
	{
		std::lock_guard lock(sessions_mu);
		state = session->state;
		up = session->upstream;
	}
	if (state != "active" || !up) {
		err = make_error(code_runtime_unavailable, "picoclaw session is not active");
		err->session_id = session_id;
		return nullptr;
	}
	return session;
}

// --- The agent's MCP (JSON-RPC over POST) ---

const char *tool_definitions_all =
    R"json([{"description":"Capture the current HDMI frame from the downstream remote host as a base64-encoded JPEG image.","inputSchema":{"properties":{"height":{"description":"Target height in pixels (optional)","type":"integer"},"quality":{"description":"JPEG quality 1-100 (optional, default: 60)","type":"integer"},"width":{"description":"Target width in pixels (optional, default: 960)","type":"integer"}},"type":"object"},"name":"kvm_screenshot"},{"description":"Send one or more HID actions (click, type, hotkey, scroll, drag, move, wait) to the downstream remote host. Use normalized [0,1] coordinates for mouse actions.","inputSchema":{"properties":{"actions":{"description":"Array of action objects. Each requires an 'action' field (click, move, type, hotkey, scroll, drag, wait).","items":{"properties":{"action":{"type":"string"},"amount":{"type":"integer"},"button":{"type":"string"},"direction":{"type":"string"},"duration_ms":{"description":"Wait duration in milliseconds, up to 30000","maximum":30000,"minimum":0,"type":"integer"},"from":{"properties":{"x":{"type":"number"},"y":{"type":"number"}},"type":"object"},"keys":{"items":{"type":"string"},"type":"array"},"text":{"type":"string"},"to":{"properties":{"x":{"type":"number"},"y":{"type":"number"}},"type":"object"},"x":{"type":"number"},"y":{"type":"number"}},"required":["action"],"type":"object"},"type":"array"}},"required":["actions"],"type":"object"},"name":"kvm_actions"})json";
const char *tool_definition_text =
    R"json({"description":"Read the text on the downstream remote host's screen (OCR on the NanoKVM's TPU; needs text recognition models, inputs up to 1920x1080; takes 3-15 s). Returns the lines in reading order and their pieces with boxes in normalized [0,1] coordinates, as kvm_actions uses: click the middle of a box to click that text.","inputSchema":{"properties":{"area":{"description":"Read only this part of the screen (normalized): it is read at full resolution, so small text is found","properties":{"h":{"type":"number"},"w":{"type":"number"},"x":{"type":"number"},"y":{"type":"number"}},"required":["x","y","w","h"],"type":"object"},"language":{"description":"latin (English and European languages), chinese (Simplified and Traditional Chinese, Japanese, English) or cyrillic (Russian, Ukrainian and others, with English); omitted, latin","enum":["latin","chinese","cyrillic"],"type":"string"}},"type":"object"},"name":"kvm_screen_text"}])json";

std::string rpc_response(const std::string &id_raw, const std::string &result, const std::string &error = {})
{
	std::string out = "{\"jsonrpc\":\"2.0\",\"id\":" + (id_raw.empty() ? std::string("null") : id_raw);
	if (!result.empty()) out += ",\"result\":" + result;
	if (!error.empty()) out += ",\"error\":" + error;
	return out + "}";
}
std::string rpc_error(int code, const std::string &message, const std::string &data = {})
{
	std::string out = "{\"code\":" + std::to_string(code) + ",\"message\":" + json::quote(message);
	if (!data.empty()) out += ",\"data\":" + data;
	return out + "}";
}
std::string tool_error(const std::string &message)
{
	return "{\"content\":[{\"text\":" + json::quote(message) + ",\"type\":\"text\"}],\"isError\":true}";
}

} // namespace

// --- Exported service functions ---

void reset_state()
{
	{
		std::lock_guard lock(runtime_mu);
		runtime_state = {};
		runtime_state.status = "checking";
		runtime_state.install_path = binary_path;
	}
	{
		auto c = default_gateway_config();
		std::lock_guard lock(config_mu);
		gateway_config = c;
	}
	{
		std::lock_guard lock(lock_mu);
		lock_owner.clear();
		lock_acquired = lock_expires = {};
	}
	{
		std::lock_guard lock(sessions_mu);
		sessions.clear();
	}
	{
		std::lock_guard lock(lease_mu);
		leases.clear();
		lease_timers.clear();
	}
	std::lock_guard lock(ops_mu);
	ops.clear();
}

bool blocks_manual_input() { return !lock_get_owner().empty(); }

bool take_session_lock(const std::string &session_id)
{
	bool acquired;
	return !lock_acquire(session_id, acquired);
}

std::string mode_status_json(const control::ModeStatus &s) { return control::mode_status_json(s); }

std::string runtime_status_json() { return current_status_json(); }

int cancel_active_control_operations()
{
	std::vector<std::shared_ptr<control::Cancel>> list;
	{
		std::lock_guard lock(ops_mu);
		for (auto &[id, c] : ops) list.push_back(c);
	}
	for (auto &c : list) c->cancel(cause_mode_switch);
	log::info("picoclaw control operations canceled for control mode switch: {}", list.size());
	return static_cast<int>(list.size());
}

std::string preempt_control_leases_for_mcp()
{
	cancel_active_control_operations();
	for (auto &s : session_snapshot()) close_session(s, 4006, "external MCP enabled");
	lock_release("");
	return {};
}

std::string stop_runtime_for_mcp()
{
	auto current = runtime_get();
	bool needs_stop = current.ready || current.status == "ready" || lifecycle_pending(current);
	bool running = false;
	std::string error;
	if (!runtime_running(running, error)) {
		if (!needs_stop) {
			set_intent_desired(false, "mcp_preempt");
			return {};
		}
		return "check PicoClaw runtime for MCP: " + error;
	}
	if (!running && !needs_stop) {
		set_intent_desired(false, "mcp_preempt");
		return {};
	}
	std::lock_guard lifecycle(lifecycle_mu);
	runtime_update([](RuntimeStatus &s) {
		s.ready = false;
		s.restoring = false;
		s.status = "stopping";
		s.last_error.clear();
		s.checked_at = WallClock::now();
	});
	if (auto e = stop_and_verify(false); !e.empty()) return "stop PicoClaw runtime for MCP: " + e;
	set_intent_desired(false, "mcp_preempt");
	return {};
}

void preserve_runtime_for_chat_only(const std::string &source)
{
	if (!runtime_get().ready && session_snapshot().empty()) return;
	set_intent_desired(true, source);
}

void publish_control_mode_changed(const control::ModeStatus &status, const std::string &source)
{
	Value payload = Value::object();
	payload.set("mode", Value::string(control::mode_name(status.mode)));
	payload.set("transitioning", Value::boolean(status.transitioning));
	payload.set("can_control", Value::boolean(status.mode == control::Mode::picoclaw && !status.transitioning));
	if (!status.last_error.empty()) payload.set("last_error", Value::string(status.last_error));
	if (status.changed_at != WallClock::time_point{}) payload.set("changed_at", Value::string(rfc3339nano(status.changed_at)));
	if (!source.empty()) payload.set("source", Value::string(source));
	std::string message = gateway_message("control.mode_changed", {}, {}, now_ms(), payload);
	for (auto &s : session_snapshot()) {
		std::shared_ptr<Link> down;
		std::string state;
		{
			std::lock_guard lock(sessions_mu);
			down = s->downstream;
			state = s->state;
		}
		if (state == "active" && down) down->send_text(message);
	}
}

// --- Route handlers ---

void get_runtime_status(http::Request &, http::Response &res)
{
	// startRuntimeProbeLoop: from the first status request on, the runtime is checked every 30 s.
	static std::once_flag probe_loop_once;
	std::call_once(probe_loop_once, [] {
		if (!probe_loop) return;
		std::thread([] {
			for (;;) {
				std::this_thread::sleep_for(std::chrono::seconds(30));
				(void)ensure_ready(false, nullptr);
			}
		}).detach();
	});
	std::string error;
	(void)control::mode_status(&error);
	if (!error.empty()) return write_error(res, make_error(code_runtime_unavailable, error));
	auto status = runtime_get();
	if (!status.installing && (status.checked_at == WallClock::time_point{} || WallClock::now() - status.checked_at >= status_refresh)) {
		(void)ensure_ready(false, nullptr);
	}
	bool installed = false;
	std::string ierr;
	if (installed_state(installed, ierr)) runtime_update([&](RuntimeStatus &s) { s.installed = installed; });
	write_success(res, current_status_json(true));
}

void get_runtime_session(http::Request &, http::Response &res)
{
	write_success(res, "{\"checked_at\":\"" + rfc3339nano(WallClock::now()) + "\",\"current_session\":" + json::quote(lock_get_owner()) + "}");
}

void release_runtime_session(http::Request &req, http::Response &res)
{
	std::string id = json::trim_space(session_header_value(req));
	if (id.empty()) return write_error(res, make_error(code_session_id_missing, "missing X-PicoClaw-Session-ID"));
	if (auto s = session_get(id)) close_session(s, 1000, "session released");
	release_session_leases(id);
	std::string owner = lock_get_owner();
	runtime_update([&](RuntimeStatus &s) { s.current_session = owner; });
	write_success(res, "{\"current_session\":" + json::quote(owner) + ",\"released\":true}");
}

void update_model_config(http::Request &req, http::Response &res)
{
	Value body;
	bool ok = !json::parse(req.body, body) && (body.is_object() || body.is_null());
	std::string model, base, key;
	if (ok)
		for (const auto &[k, x] : body.obj) {
			std::string n = json::to_lower(k);
			std::string *target = n == "model" ? &model : n == "api_base" ? &base : n == "api_key" ? &key : nullptr;
			if (!target || x.is_null()) continue;
			if (!x.is_string()) ok = false;
			else *target = x.str;
		}
	if (!ok) return write_error(res, make_error(code_invalid_action, "invalid model config payload"));
	std::string model_name;
	bool applied = apply_config_change(res, "model config", "model_config", false, [&]() -> std::optional<Error> {
		std::string error;
		model_name = picoclaw::update_model_config(json::trim_space(base), json::trim_space(key), json::trim_space(model), error);
		if (!error.empty()) return make_error(code_runtime_unavailable, error);
		if (auto e = ensure_startup_defaults(); !e.empty())
			return make_error(code_runtime_unavailable, "model config saved, but failed to persist picoclaw defaults: " + e);
		return std::nullopt;
	});
	if (!applied) return;
	write_success(res, "{\"model_name\":" + json::quote(model_name) + ",\"status\":" + current_status_json() + "}");
}

void update_host_tools(http::Request &req, http::Response &res)
{
	Value body;
	bool has = false, on = false;
	bool ok = !json::parse(req.body, body) && (body.is_object() || body.is_null());
	if (ok)
		for (const auto &[k, x] : body.obj) {
			if (json::to_lower(k) != "enabled") continue;
			if (x.is_null()) has = false;
			else if (x.is_bool()) {
				has = true;
				on = x.b;
			} else {
				ok = false;
			}
		}
	if (!ok || !has) return write_error(res, make_error(code_invalid_action, "invalid host tools payload"));
	bool applied = apply_config_change(res, "host tools setting", "host_tools", !on, [&]() -> std::optional<Error> {
		if (auto e = intent_set_host_tools(on, "host_tools"); !e.empty())
			return make_error(code_runtime_unavailable, "failed to store the host tools setting: " + e);
		bool not_exist = false;
		if (auto e = ensure_startup_defaults(&not_exist); !e.empty() && !not_exist)
			return make_error(code_runtime_unavailable, "host tools setting saved, but failed to persist picoclaw defaults: " + e);
		return std::nullopt;
	});
	if (!applied) return;
	write_success(res, std::string("{\"host_tools\":") + (on ? "true" : "false") + ",\"status\":" + current_status_json() + "}");
}

void update_agent_profile(http::Request &req, http::Response &res)
{
	Value body;
	std::string profile;
	bool ok = !json::parse(req.body, body) && (body.is_object() || body.is_null());
	if (ok)
		for (const auto &[k, x] : body.obj) {
			if (json::to_lower(k) != "profile" || x.is_null()) continue;
			if (!x.is_string()) ok = false;
			else profile = x.str;
		}
	if (!ok) return write_error(res, make_error(code_invalid_action, "invalid agent profile payload"));
	profile = json::trim_space(profile);
	if (profile != "default" && profile != "kvm") return write_error(res, make_error(code_invalid_action, "invalid agent profile"));
	std::lock_guard lifecycle(lifecycle_mu);
	if (auto e = apply_agent_profile(profile); !e.empty()) return write_error(res, make_error(code_runtime_unavailable, e));
	(void)sync_config();
	(void)ensure_ready(true, nullptr);
	write_success(res, "{\"profile\":" + json::quote(profile) + ",\"status\":" + current_status_json(true) + "}");
}

void runtime_update_install(const std::function<void(InstallState &)> &update)
{
	InstallState st;
	update(st);
	runtime_update([&](RuntimeStatus &s) {
		s.ready = false;
		s.installed = false;
		s.installing = true;
		s.install_progress = st.progress;
		s.install_stage = st.stage;
		s.status = "installing";
		s.last_error.clear();
		s.checked_at = WallClock::now();
	});
}

void finish_install(bool ok, const std::string &status, const std::string &message)
{
	RuntimeStatus s;
	s.installed = ok;
	s.install_progress = ok ? 100 : 0;
	s.install_stage = status;
	s.status = status;
	s.last_error = message;
	s.checked_at = WallClock::now();
	runtime_set(s);
}

std::string begin_install()
{
	std::lock_guard lifecycle(lifecycle_mu);
	if (runtime_get().installing) return "picoclaw installation is already in progress";
	bool installed = false;
	std::string error;
	if (installed_state(installed, error) && installed) {
		RuntimeStatus s;
		s.installed = true;
		s.install_progress = 100;
		s.install_stage = "installed";
		if (auto settings = load_gateway_settings(error)) {
			s.model_configured = settings->model_configured;
			s.model_name = settings->model_name;
		}
		s.status = "installed";
		s.checked_at = WallClock::now();
		runtime_set(s);
		return "picoclaw is already installed";
	}
	RuntimeStatus s;
	s.installing = true;
	s.install_stage = "preparing";
	s.status = "installing";
	s.checked_at = WallClock::now();
	runtime_set(s);
	return {};
}

std::string install_result_json(const std::string &output)
{
	auto current = runtime_get();
	std::string out = std::string("{\"installed\":") + (current.installed && !current.installing ? "true" : "false") +
	                  ",\"binary\":" + json::quote(binary_path) + ",\"download\":" + json::quote(download_url);
	if (!output.empty()) out += ",\"output\":" + json::quote(output);
	return out + ",\"status\":" + current_status_json() + "}";
}

void list_sessions(http::Request &req, http::Response &res) { sessions_list(req, res); }
void get_session(http::Request &req, http::Response &res) { sessions_get(req, res); }
void delete_session(http::Request &req, http::Response &res) { sessions_delete(req, res); }

void start_runtime(http::Request &, http::Response &res)
{
	log::info("picoclaw runtime start requested");
	Error err;
	auto hold = acquire_control(err);
	if (!hold) return write_error(res, err, "{\"status\":" + current_status_json() + "}");
	auto op = begin_operation(false);
	std::lock_guard lifecycle(lifecycle_mu);
	if (auto e = lifecycle_error(op->cancel.get())) return write_error(res, *e, "{\"status\":" + current_status_json() + "}");
	if (runtime_get().installing)
		return write_error(res, make_error(code_runtime_unavailable, "picoclaw installation is in progress"), "{\"status\":" + current_status_json() + "}");
	if (!ensure_ready(true, op->cancel.get())) {
		set_intent_desired(true, "web");
		return write_success(res, "{\"started\":true,\"command\":\"\",\"status\":" + current_status_json() + "}");
	}
	runtime_update([](RuntimeStatus &s) {
		s.ready = false;
		s.restoring = false;
		s.status = "starting";
		s.last_error.clear();
		s.checked_at = WallClock::now();
	});
	std::string command, output;
	auto data = [&] {
		return "{\"command\":" + json::quote(command) + ",\"output\":" + json::quote(output) + ",\"status\":" + current_status_json() + "}";
	};
	if (auto e = start_runtime_cmd(op->cancel.get(), command, output)) {
		if (e->code == code_mode_conflict)
			runtime_update([&](RuntimeStatus &s) {
				s.ready = false;
				s.restoring = false;
				if (lifecycle_pending(s)) s.status = "stopped";
				s.last_error = e->message;
				s.current_session.clear();
				s.checked_at = WallClock::now();
			});
		set_intent_error(e->message);
		return write_error(res, *e, data());
	}
	if (!control::require_write(control::Mode::picoclaw).empty()) {
		auto e = control_write_error(control::Mode::picoclaw);
		set_intent_error(e.message);
		return write_error(res, e, data());
	}
	set_intent_desired(true, "web");
	std::string out = "{\"started\":true,\"command\":" + json::quote(command);
	if (!output.empty()) out += ",\"output\":" + json::quote(output);
	write_success(res, out + ",\"status\":" + current_status_json() + "}");
}

void stop_runtime(http::Request &, http::Response &res)
{
	log::info("picoclaw runtime stop requested");
	bool switched = false;
	auto &p = platform();
	std::string err = control::switch_if_with_cleanup(control::Mode::picoclaw, control::Mode::off, [] {
		cancel_active_control_operations();
		return std::string();
	}, [&p] { return p.release_hid ? p.release_hid() : std::string(); }, switched);
	if (!err.empty()) return write_error(res, make_error(code_runtime_start_failed, err), "{\"status\":" + current_status_json() + "}");
	if (switched) publish_control_mode_changed(control::mode_status(), "runtime_stop");
	std::lock_guard lifecycle(lifecycle_mu);
	runtime_update([](RuntimeStatus &s) {
		s.ready = false;
		s.restoring = false;
		s.status = "stopping";
		s.last_error.clear();
		s.checked_at = WallClock::now();
	});
	set_intent_desired(false, "web");
	if (auto e = stop_and_close_sessions(4007, "PicoClaw runtime stopped"); !e.empty()) {
		set_intent_error(e);
		return write_error(res, make_error(code_runtime_start_failed, e), "{\"status\":" + current_status_json() + "}");
	}
	write_success(res, "{\"started\":false,\"command\":\"\",\"status\":" + current_status_json() + "}");
}

void uninstall_runtime(http::Request &, http::Response &res)
{
	std::string output = "picoclaw uninstalled successfully";
	if (runtime_get().installing)
		return write_error(res, make_error(code_runtime_start_failed, "cannot uninstall while installation is in progress"));
	bool switched = false;
	auto &p = platform();
	std::string err = control::switch_if_with_cleanup(control::Mode::picoclaw, control::Mode::off, [] {
		cancel_active_control_operations();
		return std::string();
	}, [&p] { return p.release_hid ? p.release_hid() : std::string(); }, switched);
	if (!err.empty())
		return write_error(res, make_error(code_runtime_start_failed, "control release failed before uninstall: " + err),
		                   "{\"status\":" + current_status_json() + "}");
	if (switched) publish_control_mode_changed(control::mode_status(), "runtime_uninstall");
	std::lock_guard lifecycle(lifecycle_mu);
	auto current = runtime_get();
	if (current.installing) return write_error(res, make_error(code_runtime_start_failed, "cannot uninstall while installation is in progress"));
	bool running = false;
	std::string error;
	if (!runtime_running(running, error)) return write_error(res, make_error(code_runtime_unavailable, "failed to check picoclaw runtime status"));
	if (running || current.ready || current.status == "ready" || lifecycle_pending(current)) {
		runtime_update([](RuntimeStatus &s) {
			s.ready = false;
			s.restoring = false;
			s.status = "stopping";
			s.last_error.clear();
			s.checked_at = WallClock::now();
		});
		if (auto e = stop_and_close_sessions(4007, "PicoClaw runtime stopped"); !e.empty())
			output = "picoclaw uninstalled successfully (stop failed before uninstall: " + e + ")";
	}
	set_intent_desired(false, "web");
	std::string path = config_path(error);
	std::string dir = error.empty() ? path.substr(0, path.rfind('/')) : "/root/.picoclaw";
	std::error_code ec;
	std::filesystem::remove_all(dir, ec);
	::unlink(binary_path.c_str());
	std::filesystem::remove_all(cache_dir, ec);
	RuntimeStatus s;
	s.install_path = binary_path;
	s.status = "not_installed";
	s.checked_at = WallClock::now();
	runtime_set(s);
	write_success(res, "{\"installed\":false,\"binary\":" + json::quote(binary_path) + ",\"download\":" + json::quote(download_url) +
	                       ",\"output\":" + json::quote(output) + ",\"status\":" + current_status_json() + "}");
}

// --- Loopback routes ---

void screenshot(http::Request &req, http::Response &res)
{
	ScreenshotQuery q;
	q.format = req.query_value("format");
	auto num = [&](const char *name, std::uint16_t &out) {
		std::string v = req.query_value(name);
		if (v.empty()) return true;
		char *end = nullptr;
		unsigned long n = std::strtoul(v.c_str(), &end, 10);
		if (*end || v[0] == '-' || n > 65535) return false;
		out = static_cast<std::uint16_t>(n);
		return true;
	};
	if (!num("width", q.width) || !num("height", q.height) || !num("quality", q.quality))
		return write_error(res, make_error(code_invalid_action, "invalid screenshot query"));
	std::string id = session_header_value(req);
	if (id.empty()) return write_error(res, make_error(code_session_id_missing, "missing X-PicoClaw-Session-ID"));
	bool acquired = false;
	if (auto e = lock_acquire(id, acquired)) return write_error(res, *e);
	struct Release {
		bool on;
		std::string id;
		~Release()
		{
			if (on) lock_release(id);
		}
	} release{acquired, id};
	control::Cancel cancel;
	std::string data;
	Meta meta;
	if (auto e = capture_screenshot(cancel, q, data, meta)) return write_error(res, *e);
	if (q.format == "base64") {
		meta.image_base64 = base64::encode(data);
		return write_success(res, meta_json(meta));
	}
	res.status = 200;
	res.content_type = "image/jpeg";
	res.body = std::move(data);
	res.done = true;
}

void actions(http::Request &req, http::Response &res)
{
	Error err;
	auto hold = acquire_control(err);
	if (!hold) return write_error(res, err);
	auto op = begin_operation(true);
	std::string id = session_header_value(req);
	if (id.empty()) return write_error(res, make_error(code_session_id_missing, "missing X-PicoClaw-Session-ID"));
	bool acquired = false;
	if (auto e = lock_acquire(id, acquired)) return write_error(res, *e);
	struct Release {
		bool on;
		std::string id;
		~Release()
		{
			if (on) lock_release(id);
		}
	} release{acquired, id};
	if (req.body.empty()) return write_error(res, make_error(code_invalid_action, "empty action payload"));
	Value body;
	std::vector<Action> list;
	bool parsed = !json::parse(req.body, body);
	// An ActionBatch with actions, else one Action.
	if (parsed && body.is_object()) {
		const Value *batch = nullptr;
		for (const auto &[k, x] : body.obj)
			if (json::to_lower(k) == "actions") batch = &x;
		bool ok = true;
		if (batch && batch->is_array()) {
			for (const auto &item : batch->arr) {
				Action a;
				if (!decode_action(item, a)) ok = false;
				list.push_back(a);
			}
		} else if (batch && !batch->is_null()) {
			ok = false;
		}
		if (!ok || list.empty()) {
			list.clear();
			Action one;
			if (!decode_action(body, one) || one.action.empty()) return write_error(res, make_error(code_invalid_action, "invalid action payload"));
			list.push_back(one);
		}
	} else {
		return write_error(res, make_error(code_invalid_action, "invalid action payload"));
	}
	std::string result;
	if (auto e = execute_actions(*op->cancel, id, list, result)) return write_error(res, *e);
	write_success(res, result);
}

void load_image(http::Request &req, http::Response &res)
{
	Value body;
	std::string path, prompt, filename;
	bool ok = !json::parse(req.body, body) && (body.is_object() || body.is_null());
	if (ok)
		for (const auto &[k, x] : body.obj) {
			std::string n = json::to_lower(k);
			std::string *t = n == "path" ? &path : n == "prompt" ? &prompt : n == "filename" ? &filename : nullptr;
			if (!t || x.is_null()) continue;
			if (!x.is_string()) ok = false;
			else *t = x.str;
		}
	if (!ok) return write_error(res, make_error(code_invalid_action, "invalid load image payload"));
	std::string session_id;
	std::optional<Error> err;
	auto session = active_session(req, session_id, err);
	if (!session) return write_error(res, *err);
	std::string source = json::trim_space(path);
	if (source.empty()) return write_error(res, make_error(code_invalid_action, "image path is required"));
	std::string user_prompt = json::trim_space(prompt);
	if (user_prompt.empty()) user_prompt = "Describe the image briefly.";
	std::string args = "{\"path\":" + json::quote(source) + "}";
	std::string instruction = "Call `load_image` first with this exact argument object:\n```json\n" + args +
	                          "\n```\n\nDo not ask the user to re-upload the image. Do not modify the path. Do not use `read_file` or "
	                          "any other tool to inspect this image directly.\n\nAfter `load_image` succeeds, treat the loaded image as "
	                          "the current task input and continue with this request:\n" + user_prompt;
	Value payload = Value::object();
	payload.set("content", Value::string(instruction));
	std::string message_id = new_uuid();
	std::string message = gateway_message("message.send", message_id, session_id, now_ms(), payload);
	std::shared_ptr<Link> up;
	{
		std::lock_guard lock(sessions_mu);
		up = session->upstream;
	}
	if (!up || !up->send_text(message))
		return write_error(res, make_error(code_runtime_unavailable, "failed to deliver image instruction to picoclaw"));
	write_success(res, "{\"accepted\":true,\"instructed_at\":\"" + rfc3339nano(WallClock::now()) + "\",\"message_id\":" + json::quote(message_id) +
	                       ",\"path\":" + json::quote(source) + ",\"session_id\":" + json::quote(session_id) +
	                       ",\"user_prompt\":" + json::quote(user_prompt) + "}");
}

void mcp_handler(http::Request &req, http::Response &res)
{
	// ShouldBindJSON into jsonRPCRequest: the first JSON value of the body; jsonrpc and method strings, id and params
	// raw. A type error still leaves the id read.
	Value msg;
	auto perr = json::parse_first(req.body, msg);
	std::string id_raw;
	bool ok = !perr && (msg.is_object() || msg.is_null());
	std::string version, method;
	const Value *params = nullptr;
	if (!perr && msg.is_object())
		for (const auto &[k, x] : msg.obj) {
			std::string n = json::to_lower(k);
			if (n == "jsonrpc" || n == "method") {
				if (x.is_null()) continue;
				if (!x.is_string()) ok = false;
				else (n == "jsonrpc" ? version : method) = x.str;
			} else if (n == "params") {
				params = &x;
			} else if (n == "id") {
				id_raw = json::compact(req.body.substr(x.offset, x.end - x.offset));
			}
		}
	if (!ok) return res.json(400, rpc_response(perr ? std::string() : id_raw, {}, rpc_error(-32700, "parse error")));
	if (version != "2.0") return res.json(200, rpc_response(id_raw, {}, rpc_error(-32600, "invalid request: jsonrpc must be 2.0")));
	auto *text = platform().text;
	bool text_on = text && text->read_text && text->text_available && text->text_available();
	if (method == "initialize")
		return res.json(200, rpc_response(id_raw, R"({"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"nanokvm","version":"1.0.0"}})"));
	if (method == "ping") return res.json(200, rpc_response(id_raw, "{}"));
	if (method == "tools/list") {
		std::string list = tool_definitions_all;
		std::string tools = text_on ? list + "," + tool_definition_text : list + "]";
		return res.json(200, rpc_response(id_raw, "{\"tools\":" + tools + "}"));
	}
	if (method != "tools/call")
		return res.json(200, rpc_response(id_raw, {}, rpc_error(-32601, "method not found: " + method)));
	// json.Unmarshal of the params: absent fails, null leaves the name empty.
	std::string name;
	const Value *args = nullptr;
	bool params_ok = params && (params->is_object() || params->is_null());
	if (params_ok && params->is_object())
		for (const auto &[k, x] : params->obj) {
			std::string n = json::to_lower(k);
			if (n == "name") {
				if (x.is_string()) name = x.str;
				else if (!x.is_null()) params_ok = false;
			} else if (n == "arguments") {
				args = &x;
			}
		}
	if (!params_ok) return res.json(200, rpc_response(id_raw, {}, rpc_error(-32602, "invalid params")));
	auto session_id_of = [&] {
		std::string id = session_header_value(req);
		return id.empty() ? lock_get_owner() : id;
	};
	if (name == "kvm_screenshot") {
		ScreenshotQuery q;
		q.format = "base64";
		// json.Unmarshal skips a field it cannot set and sets the others.
		if (args && args->is_object())
			for (const auto &[k, x] : args->obj) {
				std::string n = json::to_lower(k);
				std::uint16_t *t = n == "width" ? &q.width : n == "height" ? &q.height : n == "quality" ? &q.quality : nullptr;
				if (!t || x.is_null()) continue;
				if (x.is_number() && x.num >= 0 && x.num <= 65535 && x.str.find_first_of(".eE") == std::string::npos)
					*t = static_cast<std::uint16_t>(x.num);
			}
		control::Cancel cancel;
		std::string data;
		Meta meta;
		if (auto e = capture_screenshot(cancel, q, data, meta)) return res.json(200, rpc_response(id_raw, tool_error(e->message)));
		std::string b64 = base64::encode(data);
		std::string sid;
		std::optional<Error> serr;
		if (auto session = active_session(req, sid, serr)) {
			std::shared_ptr<Link> down;
			{
				std::lock_guard lock(sessions_mu);
				down = session->downstream;
			}
			if (down) {
				Value payload = Value::object();
				payload.set("content", Value::string("screenshot captured"));
				payload.set("image_base64", Value::string(b64));
				payload.set("mime_type", Value::string("image/jpeg"));
				if (!down->send_text(gateway_message("observation", new_uuid(), sid, now_ms(), payload)))
					log::warn("failed to deliver picoclaw observation message");
			}
		}
		std::string result = "{\"content\":[{\"text\":\"screenshot captured\",\"type\":\"text\"},{\"data\":" + json::quote(b64) +
		                     ",\"mimeType\":\"image/jpeg\",\"type\":\"image\"}],\"meta\":{\"capture_height\":" + std::to_string(meta.capture_height) +
		                     ",\"capture_width\":" + std::to_string(meta.capture_width) + ",\"source_height\":" + std::to_string(meta.source_height) +
		                     ",\"source_width\":" + std::to_string(meta.source_width) + "}}";
		return res.json(200, rpc_response(id_raw, result));
	}
	if (name == "kvm_screen_text") {
		if (!text || !text->read_text) return res.json(200, rpc_response(id_raw, tool_error("reading the screen text needs the models of a language (Settings > AI > Text recognition)")));
		std::string language;
		std::optional<mcp::ScreenBox> area;
		if (args && !args->is_null()) {
			bool good = args->is_object();
			if (good)
				for (const auto &[k, x] : args->obj) {
					std::string n = json::to_lower(k);
					if (n == "language") {
						if (x.is_string()) language = x.str;
						else if (!x.is_null()) good = false;
					} else if (n == "area") {
						if (x.is_null()) {
							area.reset();
							continue;
						}
						if (!x.is_object()) {
							good = false;
							continue;
						}
						mcp::ScreenBox b = area ? *area : mcp::ScreenBox{};
						for (const auto &[f, y] : x.obj) {
							std::string m = json::to_lower(f);
							double *t = m == "x" ? &b.x : m == "y" ? &b.y : m == "w" ? &b.w : m == "h" ? &b.h : nullptr;
							if (!t || y.is_null()) continue;
							if (!y.is_number()) good = false;
							else *t = y.num;
						}
						area = b;
					}
				}
			if (!good) return res.json(200, rpc_response(id_raw, tool_error("invalid screen text arguments")));
		}
		if (area && (area->x < 0 || area->y < 0 || area->w <= 0 || area->h <= 0 || area->x + area->w > 1 || area->y + area->h > 1))
			return res.json(200, rpc_response(id_raw, tool_error("area must be non-empty and lie within the screen")));
		mcp::ScreenText t;
		std::string error;
		if (!text->read_text(language, area, t, error)) return res.json(200, rpc_response(id_raw, tool_error(error)));
		Value out = Value::object();
		out.set("ok", Value::boolean(true));
		out.set("language", Value::string(t.language));
		out.set("width", Value::number(t.width));
		out.set("height", Value::number(t.height));
		out.set("text", Value::string(t.text));
		Value pieces = Value::array();
		for (const auto &p : t.pieces) {
			Value box = Value::object();
			box.set("x", Value::number(p.box.x));
			box.set("y", Value::number(p.box.y));
			box.set("w", Value::number(p.box.w));
			box.set("h", Value::number(p.box.h));
			Value piece = Value::object();
			piece.set("text", Value::string(p.text));
			piece.set("score", Value::number(p.score));
			piece.set("box", box);
			pieces.arr.push_back(piece);
		}
		out.set("pieces", t.pieces_null && t.pieces.empty() ? Value::null() : pieces);
		// ScreenTextResult in field order (embedded ScreenText after ok).
		std::string body = json::marshal(out);
		return res.json(200, rpc_response(id_raw, "{\"content\":[{\"text\":" + json::quote(body) + ",\"type\":\"text\"}]}"));
	}
	if (name == "kvm_actions") {
		Error err;
		auto hold = acquire_control(err);
		if (!hold) {
			std::string error;
			auto status = control::mode_status(&error);
			std::string mode = error.empty() ? control::mode_name(status.mode) : "";
			bool transitioning = error.empty() && status.transitioning;
			std::string data = "{\"control_mode\":" + json::quote(mode) + ",\"reason\":" + json::quote(err.code) +
			                   ",\"transitioning\":" + (transitioning ? "true" : "false") + "}";
			return res.json(200, rpc_response(id_raw, {}, rpc_error(-32003, err.message, data)));
		}
		auto op = begin_operation(true);
		std::vector<Action> list;
		bool good = args && args->is_object();
		if (good) {
			for (const auto &[k, x] : args->obj) {
				if (json::to_lower(k) != "actions") continue;
				list.clear();
				if (x.is_null()) continue;
				if (!x.is_array()) {
					good = false;
					continue;
				}
				for (const auto &item : x.arr) {
					Action a;
					if (!decode_action(item, a)) good = false;
					list.push_back(a);
				}
			}
		} else if (args && args->is_null()) {
			good = true;
		}
		if (!good) return res.json(200, rpc_response(id_raw, tool_error("invalid actions payload")));
		if (list.empty()) return res.json(200, rpc_response(id_raw, tool_error("empty actions array")));
		std::string result;
		if (auto e = execute_actions(*op->cancel, session_id_of(), list, result)) return res.json(200, rpc_response(id_raw, tool_error(e->message)));
		return res.json(200, rpc_response(id_raw, "{\"content\":[{\"text\":" + json::quote(result) + ",\"type\":\"text\"}]}"));
	}
	return res.json(200, rpc_response(id_raw, {}, rpc_error(-32602, "unknown tool: " + name)));
}

// --- The gateway relay ---

void relay_settings(int &read_timeout_ms, int &ping_interval_ms, int &max_message_bytes)
{
	auto c = config_get();
	read_timeout_ms = c.read_timeout_ms;
	ping_interval_ms = c.ping_interval_ms;
	max_message_bytes = c.max_message_bytes;
}

void update_task_lease(const std::string &source, const std::string &session_id, const std::string &data)
{
	if (session_id.empty() || data.empty()) return;
	Value v;
	if (json::parse(data, v) || !v.is_object()) return;
	std::string id, type;
	long long max_runtime = 0;
	for (const auto &[k, x] : v.obj) {
		std::string n = json::to_lower(k);
		if (n == "id" && x.is_string()) id = x.str;
		else if (n == "type" && x.is_string()) type = x.str;
		else if (n == "payload" && x.is_object())
			for (const auto &[f, y] : x.obj)
				if (json::to_lower(f) == "max_runtime_ms" && y.is_number()) max_runtime = static_cast<long long>(y.num);
	}
	std::string key = "task:" + session_id + ":" + (id.empty() ? "default" : id);
	if (source == "downstream" && type == "message.send") {
		auto duration = std::chrono::milliseconds(120000);
		if (max_runtime > 0) duration = std::chrono::milliseconds(std::min<long long>(max_runtime, 1800000));
		std::function<void()> old_timer;
		{
			std::lock_guard lock(lease_mu);
			if (!leases[key] && platform().task_lease) leases[key] = platform().task_lease();
			if (auto it = lease_timers.find(key); it != lease_timers.end()) old_timer = it->second;
		}
		if (old_timer) old_timer();
		std::function<void()> timer;
		if (platform().after) timer = platform().after(duration, [key] { release_capture_lease(key); });
		std::lock_guard lock(lease_mu);
		lease_timers[key] = timer;
	} else if ((source == "downstream" && type == "message.cancel") ||
	           (source == "upstream" && (type == "typing.stop" || type == "message.create" || type == "message.update" || type == "error"))) {
		if (id.empty()) release_session_leases(session_id);
		else release_capture_lease(key);
	}
}

std::optional<Refusal> admit_gateway(const std::string &requested, std::string &session_out)
{
	std::string error;
	auto mode = control::mode_status(&error);
	auto refuse = [](int status, const Error &e) {
		std::string body = "{\"code\":" + json::quote(e.code) + ",\"message\":" + json::quote(e.message);
		if (!e.session_id.empty()) body += ",\"session_id\":" + json::quote(e.session_id);
		return Refusal{status, body + "}"};
	};
	if (!error.empty()) return refuse(200, make_error(code_runtime_unavailable, error));
	if (mode.transitioning || mode.mode == control::Mode::mcp) return refuse(409, control_write_error(control::Mode::picoclaw));
	std::string id = json::trim_space(requested);
	if (id.empty()) id = new_uuid();
	bool acquired;
	if (auto e = lock_acquire(id, acquired)) return Refusal{409, "{\"code\":\"AI_LOCK_HELD\",\"message\":" + json::quote(e->message) + "}"};
	{
		std::lock_guard lock(sessions_mu);
		if (auto it = sessions.find(id); it != sessions.end()) {
			if (it->second->state == "closing" || it->second->state == "closed") {
				sessions.erase(it);
			} else {
				return Refusal{409, "{\"code\":\"AI_LOCK_HELD\",\"message\":\"session is already connected\"}"};
			}
		}
		auto s = std::make_shared<GatewaySession>();
		s->id = id;
		s->state = "connecting";
		sessions[id] = s;
	}
	session_out = id;
	return std::nullopt;
}

bool connect_gateway(const std::string &id)
{
	std::string error;
	auto session = session_get(id);
	if (auto e = sync_config()) {
		close_session(session, 4002, e->message);
		return false;
	}
	auto cfg = config_get();
	std::string url;
	if (!build_gateway_url(cfg, id, url, error)) {
		runtime_update([&](RuntimeStatus &s) {
			s.ready = false;
			s.status = "config_error";
			s.config_error = s.last_error = error;
			s.checked_at = WallClock::now();
			s.current_session = lock_get_owner();
		});
		close_session(session, 4002, error);
		return false;
	}
	int status = -1;
	std::shared_ptr<Link> up;
	if (platform().connect) {
		up = platform().connect(url, cfg.token, std::chrono::milliseconds(cfg.connect_timeout_ms),
		                        [id](const std::string &data, bool binary) {
			                        auto s = session_get(id);
			                        if (!s) return;
			                        if (binary) return close_session(s, 4005, "upstream sent unsupported binary message");
			                        if (!lock_renew(id)) return close_session(s, 4004, "session lock lost");
			                        update_task_lease("upstream", id, data);
			                        std::shared_ptr<Link> down;
			                        {
				                        std::lock_guard lock(sessions_mu);
				                        down = s->downstream;
			                        }
			                        if (!down || !down->send_text(data)) close_session(s, 4005, "relay closed: downstream write failed");
		                        },
		                        [id](int code, const std::string &reason) {
			                        auto s = session_get(id);
			                        if (!s) return;
			                        // An upstream end: a normal close is reported as the gateway gone.
			                        int c = code == 1000 || code == 1001 || code == 1005 || code == 1006 || code == 0 ? 4005 : code;
			                        close_session(s, c, reason.empty() ? "closed" : reason);
		                        },
		                        [id] { return lock_renew(id); }, status, error);
	}
	if (!up) {
		runtime_update([&](RuntimeStatus &s) {
			s.ready = false;
			s.status = "unavailable";
			s.config_error.clear();
			s.last_error = error;
			s.checked_at = WallClock::now();
			s.current_session = lock_get_owner();
			if (status == 401) {
				s.status = "config_error";
				s.config_error = "gateway authentication failed";
			}
		});
		close_session(session, 4002, "gateway is unavailable");
		return false;
	}
	runtime_update([&](RuntimeStatus &s) {
		s.ready = true;
		s.status = "ready";
		s.config_error.clear();
		s.last_error.clear();
		s.checked_at = WallClock::now();
		s.current_session = lock_get_owner();
	});
	{
		std::lock_guard lock(sessions_mu);
		auto it = sessions.find(id);
		// The page went away while the gateway was dialled: the new connection is not needed.
		if (it == sessions.end() || it->second != session || it->second->state == "closing" || it->second->state == "closed") {
			up->close(1000, "relay closing");
			return false;
		}
		session->upstream = up;
	}
	return true;
}

std::optional<Refusal> open_gateway(const std::string &requested, std::string &session_out)
{
	if (auto refusal = admit_gateway(requested, session_out)) return refusal;
	if (!connect_gateway(session_out)) return Refusal{200, {}};
	return std::nullopt;
}

void set_downstream(const std::string &id, std::shared_ptr<Link> downstream)
{
	std::lock_guard lock(sessions_mu);
	if (auto it = sessions.find(id); it != sessions.end()) it->second->downstream = std::move(downstream);
}

void attach_downstream(const std::string &id, std::shared_ptr<Link> downstream)
{
	std::lock_guard lock(sessions_mu);
	if (auto it = sessions.find(id); it != sessions.end()) {
		it->second->downstream = std::move(downstream);
		it->second->state = "active";
	}
}

void downstream_message(const std::string &id, const std::string &data, bool binary)
{
	auto s = session_get(id);
	if (!s) return;
	if (!lock_renew(id)) return close_session(s, 4004, "session lock lost");
	update_task_lease("downstream", id, data);
	(void)binary;
	std::shared_ptr<Link> up;
	{
		std::lock_guard lock(sessions_mu);
		up = s->upstream;
	}
	if (!up || !up->send_text(data)) close_session(s, 1000, "relay closed: upstream write failed");
}

void downstream_closed(const std::string &id, int code, const std::string &reason)
{
	auto s = session_get(id);
	if (!s) return;
	int c = code == 1000 || code == 1001 || code == 1005 || code == 1006 || code == 0 ? 1000 : code;
	close_session(s, c, reason.empty() ? "closed" : reason);
}

void abandon_gateway(const std::string &id)
{
	auto s = session_get(id);
	if (!s) return;
	std::shared_ptr<Link> up;
	{
		std::lock_guard lock(sessions_mu);
		up = s->upstream;
	}
	if (up) up->close(1000, "relay closing");
	release_gateway_session(id);
	session_set_state(id, "closed");
	session_remove(id);
}

// --- Restore at start (reconcileRuntimeIntent) ---

namespace {

void reconcile_disabled(const std::string &source)
{
	auto status = runtime_get();
	bool running = false;
	std::string error;
	if (!runtime_running(running, error)) {
		set_intent_error(error);
		runtime_update([&](RuntimeStatus &s) {
			s.ready = false;
			s.restoring = false;
			s.status = "unavailable";
			s.last_error = error;
			s.checked_at = WallClock::now();
		});
		return;
	}
	if (!running && !status.ready && !lifecycle_pending(status)) {
		apply_disabled_intent_status();
		return;
	}
	runtime_update([](RuntimeStatus &s) {
		s.ready = false;
		s.restoring = false;
		s.status = "stopping";
		s.last_error.clear();
		s.checked_at = WallClock::now();
	});
	if (auto e = stop_and_close_sessions(4007, "PicoClaw runtime disabled"); !e.empty()) {
		set_intent_error(e);
		runtime_update([&](RuntimeStatus &s) {
			s.ready = false;
			s.restoring = false;
			s.status = "unavailable";
			s.last_error = e;
			s.checked_at = WallClock::now();
		});
		return;
	}
	apply_disabled_intent_status();
	(void)source;
}

void reconcile(const std::string &source)
{
	std::lock_guard lifecycle(lifecycle_mu);
	std::string error;
	auto intent = intent_load(error);
	if (!error.empty()) return set_intent_error(error);
	auto mode = control::mode_status(&error);
	if (!error.empty()) return set_intent_error(error);
	if (!intent.desired_running) return reconcile_disabled(source);
	if (mode.mode == control::Mode::mcp) {
		runtime_update([](RuntimeStatus &s) {
			s.ready = false;
			s.status = "blocked_by_mcp";
			s.checked_at = WallClock::now();
		});
		return;
	}
	if (mode.mode != control::Mode::picoclaw) {
		runtime_update([](RuntimeStatus &s) {
			s.ready = false;
			s.status = "stopped";
			s.checked_at = WallClock::now();
		});
		return;
	}
	Error err;
	auto hold = acquire_control(err);
	if (!hold) return set_intent_error(err.message);
	intent = intent_load(error);
	if (!error.empty()) return set_intent_error(error);
	if (!intent.desired_running) return;
	runtime_update([](RuntimeStatus &s) {
		s.ready = false;
		s.restoring = true;
		s.status = "restoring";
		s.last_error.clear();
		s.checked_at = WallClock::now();
	});
	if (!ensure_ready(true, nullptr)) {
		runtime_update([](RuntimeStatus &s) {
			s.restoring = false;
			s.status = "ready";
			s.checked_at = WallClock::now();
		});
		return;
	}
	auto current = runtime_get();
	if (current.status == "not_installed" || current.status == "model_not_configured" || current.status == "config_error") {
		std::string message = current.last_error.empty() ? current.config_error : current.last_error;
		if (message.empty()) message = "PicoClaw runtime restore prerequisites are not satisfied";
		set_intent_error(message);
		runtime_update([&](RuntimeStatus &s) {
			s.restoring = false;
			s.last_error = message;
			s.checked_at = WallClock::now();
		});
		return;
	}
	std::string command, output;
	if (auto e = start_runtime_cmd(nullptr, command, output)) {
		set_intent_error(e->message);
		runtime_update([&](RuntimeStatus &s) {
			s.restoring = false;
			if (s.status == "restoring") s.status = "unavailable";
			s.last_error = e->message;
			s.checked_at = WallClock::now();
		});
		return;
	}
	intent = intent_load(error);
	if (!error.empty()) return set_intent_error(error);
	if (!intent.desired_running) return;
	if (!control::require_write(control::Mode::picoclaw).empty()) return set_intent_error(control_write_error(control::Mode::picoclaw).message);
	set_intent_desired(true, "restore");
	runtime_update([](RuntimeStatus &s) {
		s.restoring = false;
		s.checked_at = WallClock::now();
	});
	log::info("PicoClaw runtime restored from persisted intent");
}

} // namespace

std::optional<Error> onboard_for_config()
{
	std::string output;
	return run_onboard(nullptr, output);
}

void start()
{
	(void)config_get();
	std::thread([] { reconcile("startup"); }).detach();
}

// The installation (runtime_install.go) lives in picoclaw_install.cpp.

} // namespace nkos::ai::picoclaw
