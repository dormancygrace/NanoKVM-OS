#include "extensions/tailscale.hpp"

#include "common/log.hpp"
#include "extensions/vpn.hpp"
#include "osupdate/apkpkg.hpp"
#include "osupdate/godecode.hpp"
#include "osupdate/goerr.hpp"

#include <atomic>
#include <cerrno>
#include <condition_variable>
#include <csignal>
#include <memory>
#include <mutex>
#include <poll.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

namespace nkos::extensions::tailscale {

std::string local_api_socket = "/var/run/tailscale/tailscaled.sock";

namespace {

constexpr const char *tailscale_path = "/usr/bin/tailscale";
constexpr const char *tailscaled_path = "/usr/sbin/tailscaled";
constexpr int service_timeout = 60;  // rc-service start/stop/restart
constexpr int command_timeout = 30;  // up, down, logout, status
constexpr int login_url_wait = 30;   // until login prints its URL
// upWait: how long "tailscale up" waits for tailscaled to run; without it up blocks on a node that needs a login.
constexpr const char *up_wait = "--timeout=20s";

struct TsStatus {
	std::string backend_state, host_name, tailnet;
	std::vector<std::string> ips;
};

// A started "tailscale login" (loginProcess).
struct LoginProcess {
	int pid = -1;
	std::atomic<bool> done{false};
};

std::mutex cli_mu;
std::shared_ptr<LoginProcess> login_process;  // under cli_mu

std::mutex operation_mu;
std::atomic<bool> operation_active{false};

// command: a CLI or rc-service call with a time limit; at the limit its whole process group ends (rc-service runs
// openrc-run and start-stop-daemon below it).
std::string command(int timeout, const std::vector<std::string> &argv, bool *exited = nullptr)
{
	Command c;
	c.argv = argv;
	c.timeout = std::chrono::seconds(timeout);
	c.group = true;
	c.wait_delay = std::chrono::seconds(2);
	c.capture = Command::Capture::none;
	auto r = run(c);
	if (exited) *exited = r.exited && !r.deadline;
	if (r.deadline) {
		std::string args;
		for (std::size_t i = 1; i < argv.size(); i++) args += (i > 1 ? " " : "") + argv[i];
		return argv[0] + " " + args + ": no answer in " + go_duration(timeout);
	}
	return r.error;
}

// probe: true when the check exits 0, false when it exits with a status, an error when it gave no answer.
bool probe(const std::vector<std::string> &argv, std::string &error)
{
	bool exited = false;
	error = command(command_timeout, argv, &exited);
	if (error.empty()) return true;
	if (exited) error.clear();
	return false;
}

bool tailscaled_running(std::string &error)
{
	bool running = probe({"pidof", "tailscaled"}, error);
	if (!error.empty()) error = "cannot tell whether tailscaled runs: " + error;
	return running;
}

void stop_login_locked()
{
	if (login_process && !login_process->done.load()) ::kill(login_process->pid, SIGKILL);
	login_process.reset();
}

bool executable_file(const char *path)
{
	struct stat st{};
	return ::stat(path, &st) == 0 && S_ISREG(st.st_mode) && (st.st_mode & 0111) != 0;
}

bool is_installed() { return executable_file(tailscale_path) && executable_file(tailscaled_path); }

// ensureNetbirdStopped: Tailscale starts only when NetBird is known to be stopped.
std::string ensure_netbird_stopped()
{
	std::string error;
	bool running = probe({"rc-service", "netbird", "status"}, error);
	if (!error.empty()) return "cannot tell whether NetBird runs: " + error;
	if (running) return "stop NetBird before starting Tailscale";
	return "";
}

// startDaemon: tailscaled through OpenRC without the server's OOM protection.
std::string start_daemon(const std::string &action)
{
	return command(service_timeout, network::unprotected("rc-service", {"tailscale", action}));
}

std::string cli_start()
{
	std::lock_guard lock(cli_mu);
	if (!is_installed()) return "Tailscale is not installed";
	if (auto e = ensure_netbird_stopped(); !e.empty()) return e;
	if (auto e = command(command_timeout, {"rc-update", "add", "tailscale", "default"}); !e.empty()) return e;
	std::string error;
	bool running = tailscaled_running(error);
	if (!error.empty() || running) return error;
	return start_daemon("start");
}

std::string cli_restart()
{
	std::lock_guard lock(cli_mu);
	if (!is_installed()) return "Tailscale is not installed";
	if (auto e = ensure_netbird_stopped(); !e.empty()) return e;
	if (auto e = command(command_timeout, {"rc-update", "add", "tailscale", "default"}); !e.empty()) return e;
	std::string error;
	bool running = tailscaled_running(error);
	if (!error.empty()) return error;
	return start_daemon(running ? "restart" : "start");
}

std::string cli_stop()
{
	std::lock_guard lock(cli_mu);
	stop_login_locked();
	// Uninstall removes the packages once Stop succeeds: a probe that gave no answer is not "not running".
	std::string error;
	bool running = tailscaled_running(error);
	if (!error.empty()) return error;
	if (running)
		if (auto e = command(service_timeout, {"rc-service", "tailscale", "stop"}); !e.empty()) return e;
	(void)command(command_timeout, {"rc-update", "del", "tailscale", "default"});
	return "";
}

std::string cli_logout()
{
	std::lock_guard lock(cli_mu);
	stop_login_locked();
	return command(command_timeout, {tailscale_path, "logout"});
}

// TsStatus from JSON as encoding/json fills it: false on a type error.
bool decode_status(const godecode::Value &doc, TsStatus &s)
{
	using K = godecode::Value::Kind;
	bool good = true;
	auto object = [&](const godecode::Value &v, const std::function<void(const std::string &, const godecode::Value &)> &f) {
		if (v.kind == K::null) return;
		if (v.kind != K::object) {
			good = false;
			return;
		}
		for (const auto &[k, m] : v.members) f(k, m);
	};
	object(doc, [&](const std::string &k, const godecode::Value &v) {
		if (godecode::key_matches(k, "BackendState")) {
			good = godecode::get(v, s.backend_state) && good;
		} else if (godecode::key_matches(k, "Self")) {
			object(v, [&](const std::string &k2, const godecode::Value &v2) {
				if (godecode::key_matches(k2, "HostName")) {
					good = godecode::get(v2, s.host_name) && good;
				} else if (godecode::key_matches(k2, "TailscaleIPs")) {
					std::optional<std::vector<std::string>> ips;
					if (!godecode::get_strings(v2, ips)) good = false;
					s.ips = ips ? *ips : std::vector<std::string>{};
				}
			});
		} else if (godecode::key_matches(k, "CurrentTailnet")) {
			object(v, [&](const std::string &k2, const godecode::Value &v2) {
				if (godecode::key_matches(k2, "Name")) good = godecode::get(v2, s.tailnet) && good;
			});
		}
	});
	return good;
}

const std::string status_url = "http://local-tailscaled.sock/localapi/v0/status?peers=false";

// localStatus: the status tailscale status --json prints, from tailscaled's local API. unreachable: nothing listens
// on the socket.
std::optional<TsStatus> local_status(std::string &error, bool &unreachable)
{
	unreachable = false;
	auto get_error = [&](const std::string &e) { return "Get \"" + status_url + "\": " + e; };
	int fd = ::socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
	if (fd < 0) {
		error = get_error("dial unix " + local_api_socket + ": socket: " + goerr::errno_text(errno));
		return std::nullopt;
	}
	sockaddr_un addr{};
	addr.sun_family = AF_UNIX;
	std::snprintf(addr.sun_path, sizeof addr.sun_path, "%s", local_api_socket.c_str());
	if (::connect(fd, reinterpret_cast<sockaddr *>(&addr), sizeof addr) != 0) {
		int e = errno;
		::close(fd);
		std::string dial = "dial unix " + local_api_socket + ": connect: " + goerr::errno_text(e);
		if (e == ENOENT || e == ECONNREFUSED) {
			unreachable = true;
			error = get_error("tailscaled is not reachable: " + dial);
		} else {
			error = get_error(dial);
		}
		return std::nullopt;
	}
	auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(10);
	std::string request = "GET /localapi/v0/status?peers=false HTTP/1.1\r\nHost: local-tailscaled.sock\r\n"
	                      "User-Agent: Go-http-client/1.1\r\nConnection: close\r\n\r\n";
	std::string response;
	bool timed_out = false;
	for (std::size_t sent = 0; sent < request.size();) {
		ssize_t n = ::send(fd, request.data() + sent, request.size() - sent, MSG_NOSIGNAL);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) break;
		sent += static_cast<std::size_t>(n);
	}
	while (response.size() < (5u << 20)) {
		auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - std::chrono::steady_clock::now()).count();
		if (left <= 0) {
			timed_out = true;
			break;
		}
		pollfd p{fd, POLLIN, 0};
		if (::poll(&p, 1, static_cast<int>(left)) <= 0) continue;
		char buf[16384];
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) break;
		response.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	auto head_end = response.find("\r\n\r\n");
	if (timed_out || head_end == std::string::npos) {
		error = get_error(timed_out ? "context deadline exceeded (Client.Timeout exceeded while awaiting headers)" : "EOF");
		return std::nullopt;
	}
	std::string head = response.substr(0, head_end), body = response.substr(head_end + 4);
	auto line_end = head.find("\r\n");
	std::string status_line = head.substr(0, line_end);
	auto sp = status_line.find(' ');
	std::string status = sp == std::string::npos ? "" : status_line.substr(sp + 1);
	if (status.substr(0, 3) != "200") {
		error = "tailscaled local API: " + status;
		return std::nullopt;
	}
	// A chunked body is joined; a body with a length ends there.
	std::string lower = head;
	for (auto &c : lower) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	if (lower.find("\r\ntransfer-encoding: chunked") != std::string::npos) {
		std::string joined;
		std::size_t i = 0;
		while (i < body.size()) {
			auto eol = body.find("\r\n", i);
			if (eol == std::string::npos) break;
			std::size_t size = std::strtoul(body.substr(i, eol - i).c_str(), nullptr, 16);
			if (size == 0) break;
			joined += body.substr(eol + 2, size);
			i = eol + 2 + size + 2;
		}
		body = joined;
	}
	if (body.size() > (4u << 20)) body.resize(4u << 20);
	auto doc = godecode::decode_first(body, error);
	if (!doc) return std::nullopt;
	TsStatus s;
	if (!decode_status(*doc, s)) {
		error = "json: cannot unmarshal into Go value of type tailscale.TsStatus";
		return std::nullopt;
	}
	return s;
}

std::optional<TsStatus> cli_status(std::string &error)
{
	Command c;
	c.argv = {tailscale_path, "status", "--json"};
	c.timeout = std::chrono::seconds(command_timeout);
	c.wait_delay = std::chrono::seconds(2);
	c.capture = Command::Capture::combined;
	auto r = run(c);
	if (!r.ok()) {
		error = r.error;
		return std::nullopt;
	}
	auto brace = r.out.find('{');
	if (brace == std::string::npos) {
		error = "unknown output";
		return std::nullopt;
	}
	auto doc = godecode::unmarshal(r.out.substr(brace), error);
	if (!doc) return std::nullopt;
	TsStatus s;
	if (!decode_status(*doc, s)) {
		error = "json: cannot unmarshal into Go value of type tailscale.TsStatus";
		return std::nullopt;
	}
	return s;
}

// Status: the local API, and the CLI only when the API gives no usable answer.
std::optional<TsStatus> cli_status_any(std::string &error)
{
	bool unreachable = false;
	auto s = local_status(error, unreachable);
	if (s || unreachable) return s;
	error.clear();
	return cli_status(error);
}

// Login: starts "tailscale login" and returns the URL it prints; the process stays to finish the login.
std::string cli_login(std::string &url)
{
	std::unique_lock lock(cli_mu);
	if (auto e = ensure_netbird_stopped(); !e.empty()) return e;
	stop_login_locked();
	auto child = spawn({tailscale_path, "login", "--accept-dns=false", "--timeout=10m"}, false, false, true);
	if (child.pid < 0) return child.error;
	auto process = std::make_shared<LoginProcess>();
	process->pid = child.pid;
	login_process = process;
	std::thread([process] {
		int status = 0;
		while (::waitpid(process->pid, &status, 0) < 0 && errno == EINTR) {
		}
		process->done.store(true);
		std::lock_guard l(cli_mu);
		if (login_process == process) login_process.reset();
	}).detach();

	struct Found {
		std::mutex mu;
		std::condition_variable cv;
		bool ready = false;
		std::string url, error;
	};
	auto found = std::make_shared<Found>();
	std::thread([fd = child.err, found] {
		std::string line;
		bool sent = false;
		char c;
		while (true) {
			ssize_t n = ::read(fd, &c, 1);
			if (n < 0 && errno == EINTR) continue;
			if (n > 0) line += c;
			if (sent) {
				if (n <= 0) break;
				continue;  // keep draining so the process never blocks on a full pipe
			}
			if (n <= 0 || c == '\n') {
				std::string u = find_url(line);
				line.clear();
				if (!u.empty() || n <= 0) {
					std::lock_guard l(found->mu);
					found->ready = true;
					if (!u.empty()) found->url = u;
					else found->error = "EOF";
					found->cv.notify_all();
					sent = true;
					if (n <= 0) break;
				}
			}
		}
		::close(fd);
	}).detach();
	std::unique_lock wait(found->mu);
	if (!found->cv.wait_for(wait, std::chrono::seconds(login_url_wait), [&] { return found->ready; })) {
		stop_login_locked();
		return "tailscale login printed no URL in " + go_duration(login_url_wait);
	}
	if (!found->error.empty()) {
		stop_login_locked();
		return found->error;
	}
	url = found->url;
	return "";
}

bool begin_operation()
{
	if (!operation_mu.try_lock()) return false;
	operation_active.store(true);
	return true;
}

void end_operation()
{
	operation_active.store(false);
	operation_mu.unlock();
}

struct Operation {
	bool held = begin_operation();
	~Operation()
	{
		if (held) end_operation();
	}
};

const Reply busy_reply{-1, "another Tailscale operation is in progress", {}};

} // namespace

Reply install()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (!is_installed())
		if (auto e = apkpkg::install_community({"tailscale", "tailscale-openrc"}); !e.empty())
			return fail(-1, "Tailscale installation failed: " + e);
	// Installed, not started: the page offers to start it, and starting is what adds it to the boot runlevel.
	return ok();
}

Reply uninstall(const std::string &body)
{
	bool purge = vpn::purge(body);
	Operation op;
	if (!op.held) return busy_reply;
	// The node key, login and preferences, and the daemon's log.
	const std::vector<std::string> state{"/var/lib/tailscale", "/var/log/tailscaled.log"};
	if (!is_installed()) {
		if (purge)
			if (auto e = vpn::remove_state(state); !e.empty()) return fail(-1, "Tailscale login and settings could not be deleted: " + e);
		return ok();
	}
	if (purge) {
		// Sign out first: the node key expires and the device leaves the tailnet.
		std::string ignored;
		if (tailscaled_running(ignored))
			if (auto e = cli_logout(); !e.empty()) log::warn("Tailscale: sign-out before uninstall failed: {}", e);
	}
	if (auto e = cli_stop(); !e.empty()) return fail(-1, "Tailscale could not be stopped; package was kept: " + e);
	if (auto e = apkpkg::run("remove", "tailscale-openrc"); !e.empty()) return fail(-1, "Tailscale OpenRC removal failed: " + e);
	if (auto e = apkpkg::run("remove", "tailscale"); !e.empty()) return fail(-1, "Tailscale removal failed: " + e);
	if (purge)
		if (auto e = vpn::remove_state(state); !e.empty())
			return fail(-1, "Tailscale was removed, but its login and settings could not be deleted: " + e);
	return ok();
}

Reply start()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (auto e = cli_start(); !e.empty()) return fail(-1, "start failed: " + e);
	return ok();
}

Reply restart()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (auto e = cli_restart(); !e.empty()) return fail(-1, "restart failed: " + e);
	return ok();
}

Reply stop()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (auto e = cli_stop(); !e.empty()) return fail(-1, "stop failed: " + e);
	return ok();
}

Reply up()
{
	std::string e;
	{
		std::lock_guard lock(cli_mu);
		e = ensure_netbird_stopped();
		if (e.empty()) e = command(command_timeout, {tailscale_path, "up", "--accept-dns=false", up_wait});
	}
	if (!e.empty()) return fail(-1, "Tailscale up failed: " + e);
	return ok();
}

Reply down()
{
	std::string e;
	{
		std::lock_guard lock(cli_mu);
		e = command(command_timeout, {tailscale_path, "down"});
	}
	if (!e.empty()) return fail(-1, "Tailscale down failed: " + e);
	return ok();
}

Reply login()
{
	std::string error;
	auto status = cli_status_any(error);
	if (!status) {
		error = cli_start();
		if (error.empty()) status = cli_status_any(error);
	}
	if (!status) return fail(-1, "unknown status: " + error);
	if (status->backend_state == "Running") return ok("{\"url\":\"\"}");
	std::string url;
	if (auto e = cli_login(url); !e.empty()) return fail(-2, "login failed: " + e);
	log::debug("Tailscale login URL issued: {}", !url.empty());
	return ok("{\"url\":" + q(url) + "}");
}

Reply logout()
{
	if (auto e = cli_logout(); !e.empty()) return fail(-1, "logout failed: " + e);
	return ok();
}

Reply get_status(const std::optional<network::IP16> &local)
{
	bool busy = operation_active.load();
	auto rsp = [&](const std::string &state, const std::string &name, const std::string &ip, const std::string &account, bool here) {
		return ok("{\"state\":" + q(state) + (busy ? ",\"busy\":true" : "") + ",\"name\":" + q(name) + ",\"ip\":" + q(ip) +
		          ",\"account\":" + q(account) + (here ? ",\"thisConnection\":true" : "") + "}");
	};
	if (!is_installed()) return rsp("notInstall", "", "", "", false);
	std::string error;
	auto status = cli_status_any(error);
	if (!status) return rsp("notRunning", "", "", "", false);
	static const std::map<std::string, std::string> states{
		{"NoState", "notRunning"}, {"Starting", "notRunning"}, {"NeedsLogin", "notLogin"}, {"NeedsMachineAuth", "notLogin"},
		{"InUseOtherUser", "notLogin"}, {"Running", "running"}, {"Stopped", "stopped"}};
	auto state = states.find(status->backend_state);
	if (state == states.end()) return fail(-1, "unknown state");
	std::string ipv4;
	bool here = false;
	for (const auto &value : status->ips) {
		auto ip = network::parse_ip(value);
		if (ip && network::ip_is_v4(*ip)) ipv4 = network::ip_string(*ip);
		if (ip && local && *ip == *local) here = true;
	}
	return rsp(state->second, status->host_name, ipv4, status->tailnet, here);
}

} // namespace nkos::extensions::tailscale
