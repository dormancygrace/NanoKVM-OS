#include "extensions/zerotier.hpp"

#include "extensions/netbird.hpp"
#include "extensions/vpn.hpp"
#include "network/util.hpp"
#include "osupdate/apkpkg.hpp"
#include "osupdate/godecode.hpp"
#include "osupdate/goerr.hpp"
#include "osupdate/osupdate.hpp"

#include <algorithm>
#include <cerrno>
#include <functional>
#include <mutex>
#include <sys/stat.h>

namespace nkos::extensions::zerotier {

namespace {

// The NanoKVM add-on: ZeroTier's MPL-2.0 agent built for musl, without the source-available network controller. It
// keeps ZeroTier's own names and home, so an identity made by another installation carries over.
constexpr const char *package_name = "nanokvm-zerotier";
constexpr const char *one_path = "/usr/sbin/zerotier-one";
constexpr const char *cli_path = "/usr/sbin/zerotier-cli";
constexpr const char *service_name = "zerotier-one";
constexpr int command_timeout = 30;

// The node identity (its private key and address), the joined networks and their settings, the local configuration
// and the caches. The device stays a member of each network until it is removed at the network's controller.
const std::vector<std::string> zerotier_state{"/var/lib/zerotier-one"};

// The service's link in the default runlevel: Start adds it and Stop removes it.
const std::string runlevel_link = std::string("/etc/runlevels/default/") + service_name;
const std::string openrc_started_dir = "/run/openrc/started";

std::mutex operation_mu;

const std::string missing_program = std::string("the ") + package_name + " package is installed, but " + one_path +
                                    " is missing; uninstall ZeroTier and install it again";

// errors.Join: the texts that are not empty, one per line.
std::string join_errors(const std::vector<std::string> &errors)
{
	std::string joined;
	for (const auto &e : errors)
		if (!e.empty()) joined += (joined.empty() ? "" : "\n") + e;
	return joined;
}

// ValidNetworkID: a network ID from the page, trimmed and in lower case; 16 hexadecimal digits (the controller's
// address and a network number). Nothing else reaches zerotier-cli.
bool valid_network_id(const std::string &value, std::string &id)
{
	id = network::trim_space(value);
	for (auto &c : id)
		if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
	if (id.size() != 16) return false;
	return std::all_of(id.begin(), id.end(), [](char c) { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'); });
}

// osupdate.Installed: the named packages the installed database records; an unreadable database, or one that
// records no package at all, is an error.
bool installed_packages(const std::vector<std::string> &names, std::vector<std::string> &present, std::string &error)
{
	auto found = osupdate::installed(names, error);
	present = found ? std::move(*found) : std::vector<std::string>{};
	return found.has_value();
}

// recorded: the installed database records the package; an unreadable database does not.
bool recorded()
{
	std::vector<std::string> present;
	std::string error;
	return installed_packages({package_name}, present, error) && !present.empty();
}

std::string not_installed() { return recorded() ? missing_program : "ZeroTier is not installed"; }

bool is_installed()
{
	struct stat st{};
	return ::stat(one_path, &st) == 0 && S_ISREG(st.st_mode) && (st.st_mode & 0111) != 0;
}

// runProgramOutput: combined output (trimmed); "<name> timed out" at the limit. exit_error: the program ran and
// failed (an *exec.ExitError).
std::string run_program_output(int timeout, const std::vector<std::string> &argv, std::string &text, bool *exit_error = nullptr)
{
	Command c;
	c.argv = argv;
	c.timeout = std::chrono::seconds(timeout);
	c.group = true;
	c.capture = Command::Capture::combined;
	auto r = run(c);
	text = network::trim_space(r.out);
	if (exit_error) *exit_error = false;
	if (r.deadline) return argv[0] + " timed out";
	if (exit_error) *exit_error = r.exited;
	if (!r.error.empty() && !text.empty()) return r.error + ": " + text;
	return r.error;
}

std::string run_program(int timeout, const std::vector<std::string> &argv)
{
	std::string text;
	return run_program_output(timeout, argv, text);
}

// runProgramJSON: stdout only, so that a warning on stderr cannot break the parse; stderr explains a failure.
std::string run_program_json(int timeout, const std::vector<std::string> &argv, std::string &output)
{
	Command c;
	c.argv = argv;
	c.timeout = std::chrono::seconds(timeout);
	c.group = true;
	c.capture = Command::Capture::output;
	c.keep_stderr = true;
	auto r = run(c);
	output.clear();
	if (r.deadline) return argv[0] + " timed out";
	if (!r.error.empty()) {
		if (auto text = network::trim_space(r.err_out + "\n" + r.out); !text.empty()) return r.error + ": " + text;
		return r.error;
	}
	output = std::move(r.out);
	return "";
}

// startDaemon: the service through OpenRC without the server's OOM protection, which it would otherwise inherit.
std::string start_daemon(const std::string &action)
{
	return run_program(command_timeout, network::unprotected("rc-service", {service_name, action}));
}

// openrcMayRun: false for a service without its started link, as for NetBird: rc-service status need not run for it.
bool openrc_may_run()
{
	struct stat st{};
	if (::lstat(openrc_started_dir.c_str(), &st) != 0) return true;
	return !(::lstat((openrc_started_dir + "/" + service_name).c_str(), &st) != 0 && errno == ENOENT);
}

// ServiceRunning asks OpenRC, whether or not the binary is there: a damaged installation can still run, and is
// stopped before it is removed. A stuck OpenRC times out rather than hold every status request.
bool service_running(std::string &error)
{
	error.clear();
	if (!openrc_may_run()) return false;
	std::string text;
	bool exit_error = false;
	error = run_program_output(command_timeout, {"rc-service", service_name, "status"}, text, &exit_error);
	if (exit_error) error.clear();
	return error.empty() && !exit_error;
}

bool in_runlevel(std::string &error)
{
	error.clear();
	struct stat st{};
	if (::lstat(runlevel_link.c_str(), &st) == 0) return true;
	if (errno != ENOENT) error = goerr::path_error("lstat", runlevel_link, errno);
	return false;
}

std::string cli_start()
{
	if (!is_installed()) return not_installed();
	if (auto e = run_program(command_timeout, {"rc-update", "add", service_name, "default"}); !e.empty()) return e;
	std::string error;
	bool running = service_running(error);
	if (!error.empty() || running) return error;
	return start_daemon("start");
}

std::string cli_restart()
{
	if (!is_installed()) return not_installed();
	if (auto e = run_program(command_timeout, {"rc-update", "add", service_name, "default"}); !e.empty()) return e;
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return error;
	return start_daemon(running ? "restart" : "start");
}

// Stop stops the service and takes it out of the boot runlevel. A service that is not in the runlevel is not an
// error; failing to remove it is.
std::string cli_stop()
{
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return error;
	if (running)
		if (auto e = run_program(command_timeout, {"rc-service", service_name, "stop"}); !e.empty()) return e;
	bool enabled = in_runlevel(error);
	if (!error.empty() || !enabled) return error;
	return run_program(command_timeout, {"rc-update", "del", service_name, "default"});
}

// stopForRemoval is Stop before the package is removed. restore puts the service back as it was, for a removal that
// fails; a failed Stop puts it back at once.
std::string stop_for_removal(std::function<std::string()> &restore)
{
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return error;
	bool enabled = in_runlevel(error);
	if (!error.empty()) return error;
	restore = [running, enabled] {
		std::vector<std::string> errors;
		std::string e;
		if (enabled) {
			bool now = in_runlevel(e);
			if (!e.empty() || !now) {
				errors.push_back(e);
				errors.push_back(run_program(command_timeout, {"rc-update", "add", service_name, "default"}));
			}
		}
		if (running) {
			bool now = service_running(e);
			if (!e.empty() || !now) {
				errors.push_back(e);
				errors.push_back(start_daemon("start"));
			}
		}
		return join_errors(errors);
	};
	if (auto e = cli_stop(); !e.empty()) {
		auto joined = join_errors({e, restore()});
		restore = nullptr;
		return joined;
	}
	return "";
}

// finishUninstall runs under the package lock once the package is gone or was never installed. A ZeroTier still
// there is another installation, whose identity and networks are not ours to delete.
std::string finish_uninstall(bool purge)
{
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return error;
	if (running || is_installed())
		return std::string("a ZeroTier that is not from the ") + package_name +
		       " package is still installed; its identity and networks were kept";
	if (purge)
		if (auto e = vpn::remove_state(zerotier_state); !e.empty()) return "its identity and networks could not be deleted: " + e;
	return "";
}

struct NodeInfo {
	std::string address, version;
	bool online = false;
};

struct NetworkInfo {
	std::string id, name, status, type, device;
	std::vector<std::string> addresses;
};

// parseInfo: json.Unmarshal into {address, online, version}; an address is required.
bool parse_info(const std::string &output, NodeInfo &info)
{
	auto doc = godecode::parse_document(output);
	if (!doc) return false;
	using K = godecode::Value::Kind;
	if (doc->kind != K::object && doc->kind != K::null) return false;
	bool good = true;
	for (const auto &[k, v] : doc->members) {
		if (godecode::key_matches(k, "address")) good = godecode::get(v, info.address) && good;
		else if (godecode::key_matches(k, "online")) good = godecode::get(v, info.online) && good;
		else if (godecode::key_matches(k, "version")) good = godecode::get(v, info.version) && good;
	}
	return good && !info.address.empty();
}

// parseNetworks: json.Unmarshal into a list of {id, name, status, type, portDeviceName, assignedAddresses}; a
// network without addresses has an empty list.
bool parse_networks(const std::string &output, std::vector<NetworkInfo> &networks)
{
	networks.clear();
	auto doc = godecode::parse_document(output);
	if (!doc) return false;
	using K = godecode::Value::Kind;
	if (doc->kind == K::null) return true;
	if (doc->kind != K::array) return false;
	bool good = true;
	for (const auto &item : doc->items) {
		NetworkInfo n;
		if (item.kind == K::object) {
			std::optional<std::vector<std::string>> addresses;
			for (const auto &[k, v] : item.members) {
				if (godecode::key_matches(k, "id")) good = godecode::get(v, n.id) && good;
				else if (godecode::key_matches(k, "name")) good = godecode::get(v, n.name) && good;
				else if (godecode::key_matches(k, "status")) good = godecode::get(v, n.status) && good;
				else if (godecode::key_matches(k, "type")) good = godecode::get(v, n.type) && good;
				else if (godecode::key_matches(k, "portDeviceName")) good = godecode::get(v, n.device) && good;
				else if (godecode::key_matches(k, "assignedAddresses")) good = godecode::get_strings(v, addresses) && good;
			}
			if (addresses) n.addresses = std::move(*addresses);
		} else if (item.kind != K::null) {
			good = false;
		}
		networks.push_back(std::move(n));
	}
	return good;
}

// firstIPv4: the address the VPN list shows, the first IPv4 address (net.ParseCIDR) of the first network that is up.
std::string first_ipv4(const std::vector<NetworkInfo> &networks)
{
	for (const auto &n : networks) {
		if (n.status != "OK") continue;
		for (const auto &value : n.addresses)
			if (value.find('/') != std::string::npos)
				if (auto ip = netbird::ipv4_of(value); !ip.empty()) return ip;
	}
	return "";
}

// installedVersion: x.y.z from "zerotier-one -v". A stopped service reports it on every status poll; it only changes
// when the program is replaced.
std::string installed_version()
{
	static ToolCache cache(
		[](const std::string &path, const std::vector<std::string> &args) {
			std::vector<std::string> argv{path};
			argv.insert(argv.end(), args.begin(), args.end());
			std::string text;
			auto err = run_program_output(5, argv, text);
			return std::pair{text, err};
		},
		std::chrono::minutes(1));
	auto [out, err] = cache.output(one_path, {"-v"});
	// [0-9]+\.[0-9]+\.[0-9]+, the leftmost match.
	auto digit = [](char c) { return c >= '0' && c <= '9'; };
	for (std::size_t start = 0; start < out.size(); start++) {
		std::size_t i = start;
		bool good = true;
		for (int part = 0; part < 3 && good; part++) {
			if (part > 0) {
				if (i >= out.size() || out[i] != '.') good = false;
				else i++;
			}
			std::size_t from = i;
			while (i < out.size() && digit(out[i])) i++;
			if (i == from) good = false;
		}
		if (good) return out.substr(start, i - start);
	}
	return "";
}

struct Operation {
	bool held = operation_mu.try_lock();
	~Operation()
	{
		if (held) operation_mu.unlock();
	}
};

const Reply busy_reply{-1, "another ZeroTier operation is in progress", {}};

// proto.GetZerotierStatusRsp: address, ip, version and networks are omitted when empty.
std::string status_json(const std::string &state, const std::string &address = {}, const std::string &ip = {},
                        const std::string &version = {}, const std::vector<NetworkInfo> &networks = {})
{
	std::string out = "{\"state\":" + q(state);
	if (!address.empty()) out += ",\"address\":" + q(address);
	if (!ip.empty()) out += ",\"ip\":" + q(ip);
	if (!version.empty()) out += ",\"version\":" + q(version);
	if (!networks.empty()) {
		out += ",\"networks\":[";
		bool first = true;
		for (const auto &n : networks) {
			out += std::string(first ? "" : ",") + "{\"id\":" + q(n.id) + ",\"name\":" + q(n.name) + ",\"status\":" + q(n.status) +
			       ",\"type\":" + q(n.type) + ",\"device\":" + q(n.device) + ",\"addresses\":" + json_strings(n.addresses) + "}";
			first = false;
		}
		out += "]";
	}
	return out + "}";
}

Reply run_action(const std::string &failure, std::string (*action)())
{
	Operation op;
	if (!op.held) return busy_reply;
	if (auto e = action(); !e.empty()) return fail(-1, failure + e);
	return ok();
}

// Join and Leave: joining only asks the network's controller; an administrator may still have to authorize the
// device there.
Reply network_action(const std::string &body, const std::string &failure, const char *verb)
{
	network::RequestData data;
	data.method = "POST";
	data.body = body;
	auto b = network::Binder::bind(data, true);
	std::string network;
	b.str("network", "network", network);
	if (!b.ok()) return fail(-1, "invalid request");
	std::string id;
	if (!valid_network_id(network, id)) return fail(-1, "a ZeroTier network ID is 16 hexadecimal digits");
	Operation op;
	if (!op.held) return busy_reply;
	if (auto e = run_program(command_timeout, {cli_path, verb, id}); !e.empty()) return fail(-1, failure + e);
	return ok();
}

} // namespace

Reply install()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (!is_installed()) {
		if (auto e = apkpkg::install_only({package_name}); !e.empty()) return fail(-1, "ZeroTier installation failed: " + e);
		// apk adds nothing for a package it records, even with its files missing.
		if (!is_installed()) return fail(-1, "ZeroTier installation failed: " + missing_program);
	}
	// Installed, not started: the page offers to start it, and starting is what adds it to the boot runlevel.
	return ok();
}

Reply uninstall(const std::string &body)
{
	bool purge = vpn::purge(body);
	Operation op;
	if (!op.held) return busy_reply;
	// All under the package lock: the installed database, not the binary, decides whether ZeroTier is installed,
	// and the state is deleted only once it is not. ZeroTier is stopped only once the removal can go ahead: while
	// another update holds the lock, it keeps running. A failed removal starts it again, and puts it back in the
	// boot runlevel, as it was.
	std::string finish_error;
	bool removed = false;
	auto prepare = [] {
		apkpkg::Prepared p;
		p.error = stop_for_removal(p.undo);
		return p;
	};
	auto e = apkpkg::remove_after(prepare, [&] { return finish_error = finish_uninstall(purge); }, {package_name}, removed);
	if (e.empty()) return ok();
	if (!finish_error.empty() && removed) return fail(-1, "ZeroTier was removed, but " + finish_error);
	if (!finish_error.empty()) return fail(-1, "ZeroTier uninstall failed: " + finish_error);
	return fail(-1, "ZeroTier removal failed: " + e);
}

Reply start() { return run_action("ZeroTier start failed: ", cli_start); }
Reply restart() { return run_action("ZeroTier restart failed: ", cli_restart); }
Reply stop() { return run_action("ZeroTier stop failed: ", cli_stop); }

Reply join(const std::string &body) { return network_action(body, "ZeroTier could not join the network: ", "join"); }
Reply leave(const std::string &body) { return network_action(body, "ZeroTier could not leave the network: ", "leave"); }

Reply get_status()
{
	if (!is_installed()) {
		// A recorded package without its program is installed, and damaged: it is not running, and the page offers
		// Uninstall.
		return ok(status_json(recorded() ? "notRunning" : "notInstall"));
	}
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return fail(-1, "ZeroTier service status failed: " + error);
	if (!running) return ok(status_json("notRunning", "", "", installed_version()));
	std::string output;
	NodeInfo info;
	if (!run_program_json(command_timeout, {cli_path, "-j", "info"}, output).empty() || !parse_info(output, info)) {
		// Started but not yet answering (it reads its identity first), or crashed and waiting to be respawned.
		return ok(status_json("connecting", "", "", installed_version()));
	}
	std::vector<NetworkInfo> networks;
	if (auto e = run_program_json(command_timeout, {cli_path, "-j", "listnetworks"}, output); !e.empty())
		return fail(-1, "ZeroTier network list unavailable: " + e);
	if (!parse_networks(output, networks)) return fail(-1, "ZeroTier network list unavailable: invalid ZeroTier network list");
	std::string version = info.version.empty() ? installed_version() : info.version;
	return ok(status_json(info.online ? "running" : "connecting", info.address, first_ipv4(networks), version, networks));
}

} // namespace nkos::extensions::zerotier
