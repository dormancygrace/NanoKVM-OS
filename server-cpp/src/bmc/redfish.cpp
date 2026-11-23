#include "bmc/redfish.hpp"

#include "bmc/json.hpp"
#include "common/base64.hpp"
#include "network/util.hpp"

#include <openssl/crypto.h>
#include <openssl/rand.h>

#include <algorithm>
#include <cctype>

namespace nkos::bmc::redfish {

namespace {

constexpr const char *root = "/redfish/v1";
const std::string root_s = root;
const std::string system_path = root_s + "/Systems/1";
const std::string chassis_path = root_s + "/Chassis/1";
const std::string manager_path = root_s + "/Managers/1";
const std::string sessions_path = root_s + "/SessionService/Sessions";
const std::string reset_action_path = system_path + "/Actions/ComputerSystem.Reset";
const std::string thermal_path = chassis_path + "/Thermal";
const std::string power_path = chassis_path + "/Power";
const std::string sensors_path = chassis_path + "/Sensors";
const std::string media_suffix = "/VirtualMedia";
const std::string insert_suffix = "/1/Actions/VirtualMedia.InsertMedia";
const std::string eject_suffix = "/1/Actions/VirtualMedia.EjectMedia";

constexpr std::size_t max_sessions = 8;
constexpr std::int64_t second = 1'000'000'000;
constexpr std::int64_t session_timeout = 30 * 60 * second;
// After this many failed logins from one client within fail_window, its logins are refused for fail_window.
constexpr int max_failures = 5;
constexpr std::int64_t fail_window = 60 * second;
// Bounds the login counters: a client that never comes back leaves its entry, and IPv6 gives a client many addresses.
constexpr std::size_t max_tracked = 1024;
// An InsertMedia answers when the computer has the medium, or with the reason it did not connect.
constexpr auto insert_timeout = std::chrono::seconds(45);

Json link(const std::string &path) { return Json::object({{"@odata.id", path}}); }

void write_json(Response &w, int status, const std::string &json)
{
	w.set("Content-Type", "application/json; charset=utf-8");
	w.set("OData-Version", "4.0");
	w.set("Cache-Control", "no-store");
	w.status = status;
	w.body = json + "\n";
}

// The Redfish error format (Base message registry).
void write_error(Response &w, int status, const std::string &id, const std::string &message)
{
	Json info = Json::object({{"@odata.type", "#Message.v1_1_1.Message"},
	                          {"MessageId", "Base.1.15." + id},
	                          {"Message", message},
	                          {"Severity", "Warning"}});
	Json body = Json::object({{"error", Json::object({{"code", "Base.1.15." + id},
	                                                  {"message", message},
	                                                  {"@Message.ExtendedInfo", Json::array({info})}})}});
	write_json(w, status, body.str());
}

Json collection(const std::string &path, const std::string &type, const std::string &name,
                const std::vector<std::string> &members)
{
	Json list = Json::array();
	for (const auto &m : members) list.items.push_back(link(m));
	return Json::object({{"@odata.id", path},
	                     {"@odata.type", type},
	                     {"Name", name},
	                     {"Members", list},
	                     {"Members@odata.count", static_cast<int>(members.size())}});
}

bool equal_fold_ascii(std::string_view a, std::string_view b)
{
	if (a.size() != b.size()) return false;
	for (std::size_t i = 0; i < a.size(); i++)
		if (std::tolower(static_cast<unsigned char>(a[i])) != std::tolower(static_cast<unsigned char>(b[i]))) return false;
	return true;
}

std::string lower(std::string s)
{
	for (auto &c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return s;
}

// subtle.ConstantTimeCompare.
bool same(const std::string &a, const std::string &b)
{
	return a.size() == b.size() && CRYPTO_memcmp(a.data(), b.data(), a.size()) == 0;
}

// path.Base.
std::string path_base(std::string p)
{
	if (p.empty()) return ".";
	while (p.size() > 1 && p.back() == '/') p.pop_back();
	if (p == "/") return "/";
	auto slash = p.rfind('/');
	return slash == std::string::npos ? p : p.substr(slash + 1);
}

// A request body decoded as json.NewDecoder(http.MaxBytesReader(body, limit)).Decode does: the first JSON value,
// which must end within the limit.
std::optional<network::Binder> decode(const std::string &body, std::size_t limit)
{
	network::RequestData data{"POST", "application/json", "", body.substr(0, std::min(body.size(), limit))};
	auto b = network::Binder::bind(data, true);
	if (!b.ok()) return std::nullopt;
	return b;
}

std::string power_state(bool on) { return on ? "On" : "Off"; }

struct SensorType {
	const char *reading_type, *units;
	double factor;
};
// The agent's kinds in the Sensor schema; a kind Redfish has no reading type for is not served.
const std::map<std::string, SensorType> sensor_types = {
	{"temperature", {"Temperature", "Cel", 1}},
	{"voltage", {"Voltage", "V", 1}},
	{"current", {"Current", "A", 1}},
	{"power", {"Power", "W", 1}},
	{"fan", {"Rotational", "{rev}/min", 1}},
	{"control", {"Percent", "%", 1}},
	{"load", {"Percent", "%", 1}},
	{"level", {"Percent", "%", 1}},
	{"humidity", {"Humidity", "%", 1}},
	{"clock", {"Frequency", "Hz", 1e6}},
	{"frequency", {"Frequency", "Hz", 1}},
	{"energy", {"EnergyWh", "W.h", 1e-3}},
	{"flow", {"LiquidFlowLPM", "L/min", 1.0 / 60}},
};

Json health(bool fresh)
{
	if (!fresh) return Json::object({{"State", "UnavailableOffline"}});
	return Json::object({{"State", "Enabled"}, {"Health", "OK"}});
}

// Whether path is in a VirtualMedia collection, and under which resource.
std::optional<std::string> media_base(const std::string &path)
{
	for (const auto &parent : {system_path, manager_path}) {
		std::string base = parent + media_suffix;
		if (path == base || path.starts_with(base + "/")) return base;
	}
	return std::nullopt;
}

} // namespace

void Response::set(const std::string &name, const std::string &value)
{
	for (auto &[k, v] : headers)
		if (k == name) {
			v = value;
			return;
		}
	headers.emplace_back(name, value);
}

Handler::Handler(std::string username, std::string password, Info info, std::unique_ptr<Power> power)
	: username_(std::move(username)), password_(std::move(password)), info_(std::move(info)), power_(std::move(power))
{
	now = [] {
		return static_cast<std::int64_t>(
			std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::steady_clock::now().time_since_epoch()).count());
	};
	random = [](unsigned char *b, std::size_t n) { RAND_bytes(b, static_cast<int>(n)); };
}

// The requester's address, from the connection; IPv6 clients count per /64, as for the web login.
std::string Handler::client(const Request &r) const
{
	std::string host = r.peer;
	auto addr = network::parse_addr(host);
	if (addr && addr->is4in6()) return addr->unmap().str();
	if (addr && addr->is6()) {
		network::Addr a = *addr;
		a.zone.clear();
		return network::Prefix{a, 64}.masked().str();
	}
	return host;
}

std::string Handler::random_hex(std::size_t n)
{
	std::string b(n, '\0');
	random(reinterpret_cast<unsigned char *>(b.data()), n);
	return network::hex_encode(b);
}

void Handler::expire()
{
	std::int64_t t = now();
	for (auto it = failed_.begin(); it != failed_.end();)
		it = t - it->second.since > fail_window ? failed_.erase(it) : std::next(it);
	while (failed_.size() >= max_tracked) {
		auto oldest = failed_.begin();
		for (auto it = failed_.begin(); it != failed_.end(); ++it)
			if (it->second.since < oldest->second.since) oldest = it;
		failed_.erase(oldest);
	}
}

// Reserves one login attempt of addr (0: refused while locked out). The attempt is counted before the credentials
// are compared, in the same critical section as the check.
std::uint64_t Handler::begin_login(const std::string &addr)
{
	std::lock_guard lock(mu_);
	auto it = failed_.find(addr);
	if (it != failed_.end() && now() - it->second.since > fail_window) {
		failed_.erase(it);
		it = failed_.end();
	}
	if (it != failed_.end() && it->second.count >= max_failures) return 0;
	if (it == failed_.end()) {
		expire();
		it = failed_.emplace(addr, Failures{0, now(), false, ++serial_}).first;
	}
	it->second.count++;
	return it->second.serial;
}

void Handler::login_succeeded(const std::string &user, const std::string &addr, std::uint64_t reservation)
{
	bool first;
	{
		std::lock_guard lock(mu_);
		// Only the attempt reserved is handed back, never a newer window's.
		if (auto it = failed_.find(addr); it != failed_.end() && it->second.serial == reservation)
			if (--it->second.count <= 0) failed_.erase(it);
		first = !logged_in_.count(addr);
		if (first) {
			if (logged_in_.size() >= max_tracked) logged_in_.clear();
			logged_in_[addr] = true;
		}
	}
	if (first && on_login) on_login(user, addr);
}

void Handler::login_failed(const std::string &addr, std::uint64_t reservation)
{
	bool lockout = false;
	{
		std::lock_guard lock(mu_);
		auto it = failed_.find(addr);
		lockout = it != failed_.end() && it->second.serial == reservation && it->second.count >= max_failures &&
		          !it->second.reported;
		if (lockout) it->second.reported = true;
	}
	if (lockout && on_lockout) on_lockout(addr, max_failures, std::chrono::seconds(fail_window / second));
}

bool Handler::locked_out(const std::string &addr)
{
	std::lock_guard lock(mu_);
	auto it = failed_.find(addr);
	if (it == failed_.end()) return false;
	if (now() - it->second.since > fail_window) {
		failed_.erase(it);
		return false;
	}
	return it->second.count >= max_failures;
}

bool Handler::credentials_match(const std::string &user, const std::string &password) const
{
	bool user_ok = same(user, username_);
	bool pass_ok = same(password, password_);
	return user_ok && pass_ok && !password_.empty();
}

// HTTP Basic, or the X-Auth-Token of a session.
bool Handler::authenticate(const Request &r, Response &w)
{
	std::string addr = client(r);
	if (std::string token = r.header("x-auth-token"); !token.empty()) {
		bool found = false;
		{
			std::lock_guard lock(mu_);
			auto it = sessions_.find(token);
			if (it != sessions_.end() && now() - it->second.last_used > session_timeout) {
				sessions_.erase(it);
				it = sessions_.end();
			}
			if (it != sessions_.end()) {
				it->second.last_used = now();
				found = true;
			}
		}
		if (found) return true;
	} else if (std::string auth = r.header("authorization");
	           auth.size() >= 6 && equal_fold_ascii(std::string_view(auth).substr(0, 6), "Basic ")) {
		if (auto decoded = base64::decode(auth.substr(6))) {
			auto colon = decoded->find(':');
			if (colon != std::string::npos) {
				std::string user = decoded->substr(0, colon), password = decoded->substr(colon + 1);
				std::uint64_t reservation = begin_login(addr);
				if (!reservation) {
					write_error(w, 429, "GeneralError", "Too many failed logins; try again later.");
					return false;
				}
				if (credentials_match(user, password)) {
					login_succeeded(user, addr, reservation);
					return true;
				}
				login_failed(addr, reservation);
			}
		}
	}
	w.set("WWW-Authenticate", "Basic realm=\"NanoKVM BMC\"");
	write_error(w, 401, "NoValidSession", "Authentication is required.");
	return false;
}

bool Handler::sensors_on()
{
	if (!sensors) return false;
	std::vector<Reading> readings;
	bool fresh = false, on = false;
	sensors->readings(readings, fresh, on);
	return on;
}

void Handler::get(const Request &r, Response &w, const std::function<std::string()> &body)
{
	if (r.method != "GET" && r.method != "HEAD") {
		w.set("Allow", "GET, HEAD");
		write_error(w, 405, "OperationNotAllowed", "The operation is not allowed on this resource.");
		return;
	}
	write_json(w, 200, body());
}

Response Handler::serve(const Request &r)
{
	Response w;
	// State-changing requests a browser could send from another site with cached Basic credentials: 403 when marked
	// cross-site, 415 for a POST or PATCH that is not JSON (a cross-origin form needs no preflight).
	if (r.method != "GET" && r.method != "HEAD" && r.method != "OPTIONS") {
		if (equal_fold_ascii(network::trim_space(r.header("sec-fetch-site")), "cross-site")) {
			write_error(w, 403, "InsufficientPrivilege", "Requests from another site are refused.");
			return w;
		}
		if (r.method == "POST" || r.method == "PATCH") {
			std::string type = r.header("content-type");
			if (!equal_fold_ascii(network::trim_space(type.substr(0, type.find(';'))), "application/json")) {
				write_error(w, 415, "GeneralError", "The request body must be application/json.");
				return w;
			}
		}
	}
	std::string path = r.path;
	if (!path.empty() && path.back() == '/') path.pop_back();
	if (path == "/redfish") {
		get(r, w, [] { return Json::object({{"v1", root_s + "/"}}).str(); });
		return w;
	}
	if (path == root_s) {
		get(r, w, [this] {
			return Json::object({{"@odata.id", root_s + "/"},
			                     {"@odata.type", "#ServiceRoot.v1_15_0.ServiceRoot"},
			                     {"Id", "RootService"},
			                     {"Name", "NanoKVM Redfish Service"},
			                     {"RedfishVersion", "1.15.0"},
			                     {"UUID", info_.uuid},
			                     {"Systems", link(root_s + "/Systems")},
			                     {"Chassis", link(root_s + "/Chassis")},
			                     {"Managers", link(root_s + "/Managers")},
			                     {"SessionService", link(root_s + "/SessionService")},
			                     {"Links", Json::object({{"Sessions", link(sessions_path)}})}})
			    .str();
		});
		return w;
	}
	if (path == root_s + "/odata") {
		get(r, w, [] {
			Json services = Json::array();
			for (const char *name : {"Systems", "Chassis", "Managers", "SessionService"})
				services.items.push_back(Json::object({{"name", name}, {"kind", "Singleton"}, {"url", root_s + "/" + name}}));
			return Json::object({{"@odata.context", root_s + "/$metadata"}, {"value", services}}).str();
		});
		return w;
	}
	if (path == sessions_path && r.method == "POST") {
		create_session(r, w);
		return w;
	}
	if (!authenticate(r, w)) return w;
	if (path == root_s + "/Systems") {
		get(r, w, [] {
			return collection(root_s + "/Systems", "#ComputerSystemCollection.ComputerSystemCollection",
			                  "Computer System Collection", {system_path})
			    .str();
		});
	} else if (path == system_path) {
		get(r, w, [this] { return system_json(); });
	} else if (path == reset_action_path) {
		reset(r, w);
	} else if (path == root_s + "/Chassis") {
		get(r, w, [] {
			return collection(root_s + "/Chassis", "#ChassisCollection.ChassisCollection", "Chassis Collection", {chassis_path})
			    .str();
		});
	} else if (path == chassis_path) {
		get(r, w, [this] { return chassis_json(); });
	} else if (sensors_on() && (path == thermal_path || path == power_path || path == sensors_path ||
	                            path.starts_with(sensors_path + "/"))) {
		sensors_route(r, w, path);
	} else if (path == root_s + "/Managers") {
		get(r, w, [] {
			return collection(root_s + "/Managers", "#ManagerCollection.ManagerCollection", "Manager Collection", {manager_path})
			    .str();
		});
	} else if (path == manager_path) {
		get(r, w, [this] { return manager_json(); });
	} else if (auto base = media ? media_base(path) : std::nullopt) {
		media_route(r, w, *base, path);
	} else if (path == root_s + "/SessionService") {
		get(r, w, [] {
			return Json::object({{"@odata.id", root_s + "/SessionService"},
			                     {"@odata.type", "#SessionService.v1_1_9.SessionService"},
			                     {"Id", "SessionService"},
			                     {"Name", "Session Service"},
			                     {"ServiceEnabled", true},
			                     {"SessionTimeout", static_cast<int>(session_timeout / second)},
			                     {"Sessions", link(sessions_path)}})
			    .str();
		});
	} else if (path == sessions_path) {
		get(r, w, [this] { return session_collection(); });
	} else if (path.starts_with(sessions_path + "/")) {
		session(r, w, path.substr(sessions_path.size() + 1));
	} else {
		write_error(w, 404, "ResourceMissingAtURI", "The resource at the URI " + r.path + " was not found.");
	}
	return w;
}

std::string Handler::system_json()
{
	Json types = Json::array();
	bool power = false, reset = false;
	if (!power_->available(power, reset) && power) {
		types = Json::strings({"On", "ForceOff", "GracefulShutdown", "PowerCycle", "PushPowerButton"});
		if (reset) types.items.emplace_back("ForceRestart");
	}
	Json system = Json::object({
		{"@odata.id", system_path},
		{"@odata.type", "#ComputerSystem.v1_20_0.ComputerSystem"},
		{"Id", "1"},
		{"Name", "Host"},
		{"SystemType", "Physical"},
		{"Description", "The computer connected to the NanoKVM"},
		{"Status", Json::object({{"State", "Enabled"}, {"Health", "OK"}})},
		{"Actions", Json::object({{"#ComputerSystem.Reset", Json::object({{"target", reset_action_path},
		                                                                 {"ResetType@Redfish.AllowableValues", types}})}})},
		{"Links", Json::object({{"Chassis", Json::array({link(chassis_path)})}, {"ManagedBy", Json::array({link(manager_path)})}})},
	});
	if (media) system["VirtualMedia"] = link(system_path + media_suffix);
	// Without a power LED the state is not known; Redfish has no "unknown" value, so the property is left out.
	bool on = false, known = false;
	if (!power_->status(on, known) && known) system["PowerState"] = power_state(on);
	return system.str();
}

std::string Handler::chassis_json()
{
	Json chassis = Json::object({
		{"@odata.id", chassis_path},
		{"@odata.type", "#Chassis.v1_23_0.Chassis"},
		{"Id", "1"},
		{"Name", "Host chassis"},
		{"ChassisType", "Other"},
		{"Status", Json::object({{"State", "Enabled"}, {"Health", "OK"}})},
		{"Links", Json::object({{"ComputerSystems", Json::array({link(system_path)})},
		                        {"ManagedBy", Json::array({link(manager_path)})}})},
	});
	bool on = false, known = false;
	if (!power_->status(on, known) && known) chassis["PowerState"] = power_state(on);
	if (sensors_on()) {
		chassis["Thermal"] = link(thermal_path);
		chassis["Power"] = link(power_path);
		chassis["Sensors"] = link(sensors_path);
	}
	return chassis.str();
}

std::string Handler::manager_json()
{
	Json manager = Json::object({
		{"@odata.id", manager_path},
		{"@odata.type", "#Manager.v1_18_0.Manager"},
		{"Id", "1"},
		{"Name", "NanoKVM"},
		{"ManagerType", "BMC"},
		{"Model", "NanoKVM"},
		{"FirmwareVersion", info_.firmware_version},
		{"UUID", info_.uuid},
		{"Status", Json::object({{"State", "Enabled"}, {"Health", "OK"}})},
		{"Links", Json::object({{"ManagerForServers", Json::array({link(system_path)})},
		                        {"ManagerForChassis", Json::array({link(chassis_path)})}})},
	});
	if (media) manager["VirtualMedia"] = link(manager_path + media_suffix);
	return manager.str();
}

void Handler::reset(const Request &r, Response &w)
{
	if (r.method != "POST") {
		w.set("Allow", "POST");
		write_error(w, 405, "OperationNotAllowed", "Use POST for actions.");
		return;
	}
	auto body = decode(r.body, 4096);
	std::string type;
	if (body) body->str("ResetType", "", type);
	if (!body || !body->ok()) {
		write_error(w, 400, "MalformedJSON", "The request body is not valid JSON.");
		return;
	}
	// Redfish: an action without a ResetType uses the default, On.
	if (type.empty()) type = "On";
	static const std::map<std::string, std::string> actions = {
		{"On", "On"}, {"ForceOn", "On"}, {"ForceOff", "ForceOff"}, {"GracefulShutdown", "GracefulShutdown"},
		{"ForceRestart", "ForceRestart"}, {"PowerCycle", "PowerCycle"}, {"PushPowerButton", "PushPowerButton"},
	};
	auto action = actions.find(type);
	if (action == actions.end()) {
		write_error(w, 400, "ActionParameterNotSupported", "The value " + type + " for ResetType is not supported.");
		return;
	}
	// The presses finish even if the client disconnects.
	Error err = power_->act(action->second);
	if (err.kind == Error::busy) {
		w.set("Retry-After", "1");
		write_error(w, 503, "ServiceTemporarilyUnavailable", err.text);
	} else if (err.kind == Error::no_atx || err.kind == Error::no_reset) {
		write_error(w, 400, "ActionNotSupported", err.text);
	} else if (err) {
		write_error(w, 500, "GeneralError", "The power action failed.");
	} else {
		w.status = 204;
	}
}

std::string Handler::session_collection()
{
	std::vector<std::string> ids;
	{
		std::lock_guard lock(mu_);
		for (const auto &[token, s] : sessions_) ids.push_back(sessions_path + "/" + s.id);
	}
	return collection(sessions_path, "#SessionCollection.SessionCollection", "Session Collection", ids).str();
}

static Json session_resource(const std::string &id, const std::string &user)
{
	return Json::object({{"@odata.id", sessions_path + "/" + id},
	                     {"@odata.type", "#Session.v1_7_0.Session"},
	                     {"Id", id},
	                     {"Name", "User Session"},
	                     {"UserName", user}});
}

void Handler::create_session(const Request &r, Response &w)
{
	std::string addr = client(r);
	if (locked_out(addr)) {
		write_error(w, 429, "GeneralError", "Too many failed logins; try again later.");
		return;
	}
	auto body = decode(r.body, 4096);
	std::string user, password;
	if (body) {
		body->str("UserName", "", user);
		body->str("Password", "", password);
	}
	if (!body || !body->ok()) {
		write_error(w, 400, "MalformedJSON", "The request body is not valid JSON.");
		return;
	}
	std::uint64_t reservation = begin_login(addr);
	if (!reservation) {
		write_error(w, 429, "GeneralError", "Too many failed logins; try again later.");
		return;
	}
	if (!credentials_match(user, password)) {
		login_failed(addr, reservation);
		write_error(w, 401, "NoValidSession", "Invalid user name or password.");
		return;
	}
	login_succeeded(user, addr, reservation);
	Session s{random_hex(8), random_hex(24), now()};
	{
		std::lock_guard lock(mu_);
		std::int64_t t = now();
		for (auto it = sessions_.begin(); it != sessions_.end();)
			it = t - it->second.last_used > session_timeout ? sessions_.erase(it) : std::next(it);
		if (sessions_.size() >= max_sessions) {
			// The least recently used session makes room.
			auto oldest = sessions_.begin();
			for (auto it = sessions_.begin(); it != sessions_.end(); ++it)
				if (it->second.last_used < oldest->second.last_used) oldest = it;
			sessions_.erase(oldest);
		}
		sessions_[s.token] = s;
	}
	w.set("X-Auth-Token", s.token);
	w.set("Location", sessions_path + "/" + s.id);
	write_json(w, 201, session_resource(s.id, user).str());
}

void Handler::session(const Request &r, Response &w, const std::string &id)
{
	std::optional<Session> found;
	{
		std::lock_guard lock(mu_);
		for (const auto &[token, s] : sessions_)
			if (s.id == id) found = s;
	}
	if (!found) {
		write_error(w, 404, "ResourceMissingAtURI", "The session was not found.");
		return;
	}
	if (r.method == "GET" || r.method == "HEAD") {
		write_json(w, 200, session_resource(found->id, username_).str());
	} else if (r.method == "DELETE") {
		{
			std::lock_guard lock(mu_);
			sessions_.erase(found->token);
		}
		w.status = 204;
	} else {
		w.set("Allow", "GET, HEAD, DELETE");
		write_error(w, 405, "OperationNotAllowed", "The operation is not allowed on this resource.");
	}
}

// --- virtual media -------------------------------------------------------------------------------------------------

void Handler::media_route(const Request &r, Response &w, const std::string &base, const std::string &at)
{
	if (at == base) {
		get(r, w, [&] {
			return collection(base, "#VirtualMediaCollection.VirtualMediaCollection", "Virtual Media Collection", {base + "/1"})
			    .str();
		});
	} else if (at == base + "/1") {
		get(r, w, [&] { return medium_json(base); });
	} else if (at == base + insert_suffix) {
		insert_media(r, w);
	} else if (at == base + eject_suffix) {
		media_action(r, w, [this] { return media->eject(); });
	} else {
		write_error(w, 404, "ResourceMissingAtURI", "The resource at the URI " + r.path + " was not found.");
	}
}

std::string Handler::medium_json(const std::string &base)
{
	Json medium = Json::object({
		{"@odata.id", base + "/1"},
		{"@odata.type", "#VirtualMedia.v1_6_0.VirtualMedia"},
		{"Id", "1"},
		{"Name", "Virtual drive"},
		{"Description", "The NanoKVM's virtual USB drive: a CD/DVD drive for an ISO image, a disk for a disk image"},
		{"MediaTypes", Json::strings({"CD", "DVD", "USBStick"})},
		// The image is read as the computer asks for it, never copied.
		{"TransferMethod", "Stream"},
		{"Image", nullptr},
		{"ImageName", nullptr},
		{"Inserted", false},
		{"WriteProtected", true},
		{"ConnectedVia", "NotConnected"},
		{"Actions", Json::object({
			{"#VirtualMedia.InsertMedia", Json::object({
				{"target", base + insert_suffix},
				{"TransferProtocolType@Redfish.AllowableValues", Json::strings({"HTTP", "HTTPS", "CIFS"})},
				{"TransferMethod@Redfish.AllowableValues", Json::strings({"Stream"})},
			})},
			{"#VirtualMedia.EjectMedia", Json::object({{"target", base + eject_suffix}})},
		})},
	});
	Medium current;
	if (!media->medium(current).empty()) {
		medium["Status"] = Json::object({{"State", "UnavailableOffline"}});
		return medium.str();
	}
	medium["Status"] = Json::object({{"State", "Enabled"}, {"Health", "OK"}});
	if (!current.inserted) return medium.str();
	medium["Image"] = current.image;
	medium["ImageName"] = path_base(current.image);
	medium["Inserted"] = true;
	medium["WriteProtected"] = current.cd;
	medium["ConnectedVia"] = current.via;
	std::string scheme = lower(current.image.substr(0, current.image.find("://")));
	if (current.image.find("://") == std::string::npos) scheme = lower(current.image);
	if (scheme == "http") medium["TransferProtocolType"] = "HTTP";
	else if (scheme == "https") medium["TransferProtocolType"] = "HTTPS";
	else if (scheme == "smb") medium["TransferProtocolType"] = "CIFS";
	return medium.str();
}

// An action that changes the medium, carried out even if the client disconnects.
void Handler::media_action(const Request &r, Response &w, const std::function<MediaError()> &action)
{
	if (r.method != "POST") {
		w.set("Allow", "POST");
		write_error(w, 405, "OperationNotAllowed", "Use POST for actions.");
		return;
	}
	MediaError err = action();
	if (err.in_use) write_error(w, 409, "ResourceInUse", err.text);
	else if (err) write_error(w, 400, "ActionParameterValueError", err.text);
	else w.status = 204;
}

void Handler::insert_media(const Request &r, Response &w)
{
	media_action(r, w, [&]() -> MediaError {
		auto body = decode(r.body, 8192);
		MediaRequest request;
		std::string protocol, method;
		std::optional<bool> inserted;
		if (body) {
			body->str("Image", "", request.image);
			body->str("UserName", "", request.username);
			body->str("Password", "", request.password);
			body->str("TransferProtocolType", "", protocol);
			body->str("TransferMethod", "", method);
			bool b = false;
			if (body->boolean("Inserted", "", b)) inserted = b;
			if (body->boolean("WriteProtected", "", b)) request.write_protected = b;
		}
		if (!body || !body->ok()) return {false, "The request body is not valid JSON."};
		if (request.image.empty()) return {false, "The Image parameter is required."};
		if (inserted && !*inserted) return {false, "Inserted must be true: an image is put into the drive when it is given."};
		if (!method.empty() && method != "Stream")
			return {false, "TransferMethod must be Stream: the image is not copied to the NanoKVM."};
		if (!protocol.empty()) {
			auto cut = request.image.find("://");
			std::string scheme = cut == std::string::npos ? request.image : request.image.substr(0, cut);
			static const std::map<std::string, std::string> want = {{"HTTP", "http"}, {"HTTPS", "https"}, {"CIFS", "smb"}};
			auto it = want.find(protocol);
			if (it == want.end() || !network::valid_utf8(scheme) || lower(scheme) != it->second || scheme.size() != it->second.size())
				return {false, "TransferProtocolType must be HTTP, HTTPS or CIFS and match the image's address (http://, "
				               "https://, smb://)."};
		}
		Medium current;
		if (auto e = media->medium(current); !e.empty()) return {false, e};
		if (current.inserted) return {true, media_in_use};
		return media->insert(request, insert_timeout);
	});
}

// --- sensors -------------------------------------------------------------------------------------------------------

void Handler::sensors_route(const Request &r, Response &w, const std::string &at)
{
	std::vector<Reading> readings;
	bool fresh = false, on = false;
	sensors->readings(readings, fresh, on);
	// An entry of the arrays of Thermal and Power: named by its place in the array; RelatedItem links the sensor's
	// lasting resource in Sensors.
	auto member = [](const std::string &parent, const std::string &array, std::size_t index, const Reading &reading,
	                 const std::string &value) {
		Json m = Json::object({{"@odata.id", parent + "#/" + array + "/" + std::to_string(index)},
		                       {"MemberId", std::to_string(index)},
		                       {"Name", reading.name},
		                       {value, reading.value},
		                       {"Status", health(true)}});
		if (!reading.context.empty()) m["PhysicalContext"] = reading.context;
		if (sensor_types.count(reading.kind)) m["RelatedItem"] = Json::array({link(sensors_path + "/" + reading.id)});
		return m;
	};
	if (at == thermal_path) {
		Json temperatures = Json::array(), fans = Json::array();
		for (const auto &reading : readings) {
			if (reading.kind == "temperature") {
				temperatures.items.push_back(member(thermal_path, "Temperatures", temperatures.items.size(), reading, "ReadingCelsius"));
			} else if (reading.kind == "fan") {
				Json fan = member(thermal_path, "Fans", fans.items.size(), reading, "Reading");
				fan["ReadingUnits"] = "RPM";
				fans.items.push_back(fan);
			}
		}
		get(r, w, [&] {
			return Json::object({{"@odata.id", thermal_path},
			                     {"@odata.type", "#Thermal.v1_7_1.Thermal"},
			                     {"Id", "Thermal"},
			                     {"Name", "Host temperatures and fans"},
			                     {"Status", health(fresh)},
			                     {"Temperatures", temperatures},
			                     {"Fans", fans}})
			    .str();
		});
	} else if (at == power_path) {
		Json voltages = Json::array();
		for (const auto &reading : readings)
			if (reading.kind == "voltage")
				voltages.items.push_back(member(power_path, "Voltages", voltages.items.size(), reading, "ReadingVolts"));
		// No PowerControl: the agent reads the power of single parts, not what the computer draws.
		get(r, w, [&] {
			return Json::object({{"@odata.id", power_path},
			                     {"@odata.type", "#Power.v1_7_1.Power"},
			                     {"Id", "Power"},
			                     {"Name", "Host voltages"},
			                     {"Voltages", voltages}})
			    .str();
		});
	} else if (at == sensors_path) {
		std::vector<std::string> members;
		for (const auto &reading : readings)
			if (sensor_types.count(reading.kind)) members.push_back(sensors_path + "/" + reading.id);
		get(r, w, [&] { return collection(sensors_path, "#SensorCollection.SensorCollection", "Host sensors", members).str(); });
	} else {
		std::string id = at.substr(sensors_path.size() + 1);
		for (const auto &reading : readings) {
			auto kind = sensor_types.find(reading.kind);
			if (kind == sensor_types.end() || reading.id != id) continue;
			get(r, w, [&] {
				Json sensor = Json::object({{"@odata.id", at},
				                            {"@odata.type", "#Sensor.v1_7_0.Sensor"},
				                            {"Id", id},
				                            {"Name", reading.name},
				                            {"ReadingType", kind->second.reading_type},
				                            {"ReadingUnits", kind->second.units},
				                            {"Reading", reading.value * kind->second.factor},
				                            {"Status", health(true)}});
				if (!reading.context.empty()) sensor["PhysicalContext"] = reading.context;
				return sensor.str();
			});
			return;
		}
		write_error(w, 404, "ResourceMissingAtURI", "The resource at the URI " + r.path + " was not found.");
	}
}

} // namespace nkos::bmc::redfish
