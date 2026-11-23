#include "extensions/netbird.hpp"

#include "common/log.hpp"
#include "extensions/gopath.hpp"
#include "extensions/vpn.hpp"
#include "network/util.hpp"
#include "osupdate/apkpkg.hpp"
#include "osupdate/godecode.hpp"

#include <cerrno>
#include <condition_variable>
#include <csignal>
#include <functional>
#include <memory>
#include <mutex>
#include <sys/stat.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

namespace nkos::extensions::netbird {

namespace {

constexpr const char *netbird_path = "/usr/bin/netbird";
constexpr const char *netbird_socket = "unix:///var/run/netbird.sock";
constexpr int command_timeout = 45;
constexpr int login_url_timeout = 60;
constexpr int login_stop_timeout = 5;

struct NbStatus {
	std::string fqdn, ip, daemon_version, management_url;
	bool management_connected = false, signal_connected = false;
};

// A running "netbird up" (activeLogin.process).
struct LoginProcess {
	int pid = -1;
	std::mutex mu;
	std::condition_variable cv;
	bool done = false;
};
std::mutex active_login_mu;
std::shared_ptr<LoginProcess> active_login;

std::mutex operation_mu;

bool is_installed()
{
	struct stat st{};
	return ::stat(netbird_path, &st) == 0 && S_ISREG(st.st_mode) && (st.st_mode & 0111) != 0;
}

// runProgramOutput: combined output (trimmed) and the error, "<name> timed out" at the limit.
std::string run_program_output(int timeout, const std::vector<std::string> &argv, std::string &text)
{
	Command c;
	c.argv = argv;
	c.timeout = std::chrono::seconds(timeout);
	c.group = true;
	c.capture = Command::Capture::combined;
	auto r = run(c);
	text = network::trim_space(r.out);
	if (r.deadline) return argv[0] + " timed out";
	if (!r.error.empty() && !text.empty()) return r.error + ": " + text;
	return r.error;
}

std::string run_program(int timeout, const std::vector<std::string> &argv)
{
	std::string text;
	return run_program_output(timeout, argv, text);
}

// startDaemon: the netbird service through OpenRC without the server's OOM protection.
std::string start_daemon(const std::string &action)
{
	return run_program(command_timeout, network::unprotected("rc-service", {"netbird", action}));
}

std::string openrc_started_dir = "/run/openrc/started";

// openrcMayRun: false for a service without its started link (rc-service status would exit non-zero).
bool openrc_may_run(const std::string &service)
{
	struct stat st{};
	if (::lstat(openrc_started_dir.c_str(), &st) != 0) return true;
	return !(::lstat((openrc_started_dir + "/" + service).c_str(), &st) != 0 && errno == ENOENT);
}

bool service_running(std::string &error)
{
	error.clear();
	if (!is_installed() || !openrc_may_run("netbird")) return false;
	Command c;
	c.argv = {"rc-service", "netbird", "status"};
	c.capture = Command::Capture::none;
	auto r = run(c);
	if (r.ok()) return true;
	if (!r.exited) error = r.error;
	return false;
}

std::string ensure_process_stopped(const std::string &process, const std::string &display)
{
	Command c;
	c.argv = {"pidof", process};
	c.capture = Command::Capture::none;
	if (run(c).ok()) return "stop " + display + " before starting NetBird";
	return "";
}

std::string stop_login(const std::shared_ptr<LoginProcess> &process)
{
	if (::kill(-process->pid, SIGKILL) != 0 && errno != ESRCH) return network::errno_text(errno);
	std::unique_lock lock(process->mu);
	if (!process->cv.wait_for(lock, std::chrono::seconds(login_stop_timeout), [&] { return process->done; }))
		return "NetBird login did not stop";
	return "";
}

std::string cancel_login()
{
	std::shared_ptr<LoginProcess> process;
	{
		std::lock_guard lock(active_login_mu);
		process = active_login;
	}
	if (!process) return "";
	return stop_login(process);
}

std::string cli_start()
{
	if (!is_installed()) return "netbird is not installed";
	if (auto e = ensure_process_stopped("tailscaled", "Tailscale"); !e.empty()) return e;
	if (auto e = run_program(command_timeout, {"rc-update", "add", "netbird", "default"}); !e.empty()) return e;
	std::string error;
	bool running = service_running(error);
	if (!error.empty() || running) return error;
	return start_daemon("start");
}

std::string cli_restart()
{
	if (!is_installed()) return "netbird is not installed";
	if (auto e = ensure_process_stopped("tailscaled", "Tailscale"); !e.empty()) return e;
	if (auto e = cancel_login(); !e.empty()) return e;
	if (auto e = run_program(command_timeout, {"rc-update", "add", "netbird", "default"}); !e.empty()) return e;
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return error;
	return start_daemon(running ? "restart" : "start");
}

std::string cli_stop()
{
	if (auto e = cancel_login(); !e.empty()) return "cancel active NetBird login: " + e;
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return error;
	if (running)
		if (auto e = run_program(command_timeout, {"rc-service", "netbird", "stop"}); !e.empty()) return e;
	(void)run_program(command_timeout, {"rc-update", "del", "netbird", "default"});
	return "";
}

std::string cli_login(std::string &url)
{
	if (auto e = ensure_process_stopped("tailscaled", "Tailscale"); !e.empty()) return e;
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return error;
	if (!running)
		if (auto e = cli_start(); !e.empty()) return e;

	std::shared_ptr<LoginProcess> process;
	Child child;
	{
		std::lock_guard lock(active_login_mu);
		if (active_login) return "NetBird login is already in progress";
		child = spawn({netbird_path, "up", "--daemon-addr", netbird_socket, "--no-browser"}, true, true, true);
		if (child.pid < 0) return child.error;
		process = std::make_shared<LoginProcess>();
		process->pid = child.pid;
		active_login = process;
	}
	struct Shared {
		std::mutex mu;
		std::condition_variable cv;
		std::string url;
		bool url_found = false, ended = false;
		std::string end_error;
		int readers = 2;
	};
	auto shared = std::make_shared<Shared>();
	// scanLoginURL: lines as bufio.Scanner reads them (a trailing \r dropped); the reader stops at the first URL.
	auto scan = [shared](int fd) {
		std::string line;
		char buf[4096];
		bool stop = false;
		while (!stop) {
			ssize_t n = ::read(fd, buf, sizeof buf);
			if (n < 0 && errno == EINTR) continue;
			bool eof = n <= 0;
			if (!eof) line.append(buf, static_cast<std::size_t>(n));
			std::size_t nl;
			while (!stop && ((nl = line.find('\n')) != std::string::npos || (eof && !line.empty()))) {
				std::string text = nl == std::string::npos ? line : line.substr(0, nl);
				line = nl == std::string::npos ? "" : line.substr(nl + 1);
				if (!text.empty() && text.back() == '\r') text.pop_back();
				if (auto u = find_url(text); !u.empty()) {
					std::lock_guard l(shared->mu);
					if (!shared->url_found) {
						shared->url_found = true;
						shared->url = u;
						shared->cv.notify_all();
					}
					stop = true;
				}
				if (line.size() > 64 * 1024) stop = true;  // a token longer than the Scanner's buffer
			}
			if (eof) break;
		}
		std::lock_guard l(shared->mu);
		shared->readers--;
		shared->cv.notify_all();
		return stop ? -1 : fd;
	};
	std::thread([=] {
		int out_fd = -1, err_fd = -1;
		std::thread second([&] { err_fd = scan(child.err); });
		out_fd = scan(child.out);
		second.join();
		int status = 0;
		while (::waitpid(child.pid, &status, 0) < 0 && errno == EINTR) {
		}
		::close(child.out);
		::close(child.err);
		(void)out_fd;
		(void)err_fd;
		{
			std::lock_guard l(active_login_mu);
			if (active_login == process) active_login.reset();
		}
		std::string e;
		if (WIFEXITED(status) && WEXITSTATUS(status) != 0) e = "exit status " + std::to_string(WEXITSTATUS(status));
		else if (WIFSIGNALED(status)) e = "signal: " + goerr::signal_text(WTERMSIG(status));
		{
			std::lock_guard l(shared->mu);
			shared->ended = true;
			shared->end_error = e;
			shared->cv.notify_all();
		}
		std::lock_guard l(process->mu);
		process->done = true;
		process->cv.notify_all();
	}).detach();

	std::unique_lock wait(shared->mu);
	if (!shared->cv.wait_for(wait, std::chrono::seconds(login_url_timeout), [&] { return shared->url_found || shared->ended; })) {
		wait.unlock();
		(void)stop_login(process);
		return "timed out waiting for NetBird login URL";
	}
	if (shared->url_found) {
		url = shared->url;
		return "";
	}
	if (shared->end_error.empty()) return "";
	return "netbird up failed: " + shared->end_error;
}

// parseStatus: the JSON from the first brace on.
bool parse_status(std::string output, NbStatus &s)
{
	if (auto i = output.find('{'); i != std::string::npos) output = output.substr(i);
	if (network::trim_space(output).empty()) return false;
	auto doc = godecode::parse_document(output);
	if (!doc) return false;
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
	object(*doc, [&](const std::string &k, const godecode::Value &v) {
		if (godecode::key_matches(k, "fqdn")) good = godecode::get(v, s.fqdn) && good;
		else if (godecode::key_matches(k, "netbirdIp")) good = godecode::get(v, s.ip) && good;
		else if (godecode::key_matches(k, "daemonVersion")) good = godecode::get(v, s.daemon_version) && good;
		else if (godecode::key_matches(k, "management"))
			object(v, [&](const std::string &k2, const godecode::Value &v2) {
				if (godecode::key_matches(k2, "url")) good = godecode::get(v2, s.management_url) && good;
				else if (godecode::key_matches(k2, "connected")) good = godecode::get(v2, s.management_connected) && good;
			});
		else if (godecode::key_matches(k, "signal"))
			object(v, [&](const std::string &k2, const godecode::Value &v2) {
				if (godecode::key_matches(k2, "connected")) good = godecode::get(v2, s.signal_connected) && good;
			});
	});
	return good;
}

std::string cli_status(NbStatus &s)
{
	std::string text;
	auto err = run_program_output(command_timeout, {netbird_path, "status", "--json", "--daemon-addr", netbird_socket}, text);
	if (parse_status(text, s)) return "";
	if (!err.empty()) return err;
	return "invalid NetBird status output";
}

// Stopped NetBird reports this version on every status poll; it only changes when the program is replaced.
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
	auto [out, err] = cache.output(netbird_path, {"version"});
	return vpn::find_version(out, true);
}

struct Operation {
	bool held = operation_mu.try_lock();
	~Operation()
	{
		if (held) operation_mu.unlock();
	}
};

const Reply busy_reply{-1, "another NetBird operation is in progress", {}};

std::string status_json(const std::string &state, const std::string &name, const std::string &ip, const std::string &version)
{
	return "{\"state\":" + q(state) + ",\"name\":" + q(name) + ",\"ip\":" + q(ip) + ",\"version\":" + q(version) + "}";
}

} // namespace

std::string ipv4_of(const std::string &value)
{
	// net.ParseCIDR: the address (unmasked) when the text is a prefix.
	if (auto slash = value.find('/'); slash != std::string::npos) {
		auto addr = network::parse_addr(value.substr(0, slash));
		std::string mask = value.substr(slash + 1);
		// dtoi: decimal digits below 0xFFFFFF, all of the text.
		long n = 0;
		std::size_t i = 0;
		for (; i < mask.size() && mask[i] >= '0' && mask[i] <= '9' && n < 0xffffff; i++) n = n * 10 + (mask[i] - '0');
		bool digits = i > 0 && i == mask.size() && n < 0xffffff;
		if (addr && addr->zone.empty() && digits && n <= addr->bit_len()) {
			auto ip = network::parse_ip(addr->str());
			if (addr->is4() || (ip && network::ip_is_v4(*ip))) return network::ip_string(*ip);
			return "";
		}
	}
	auto ip = network::parse_ip(value);
	if (ip && network::ip_is_v4(*ip)) return network::ip_string(*ip);
	return "";
}

Reply install()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (!is_installed())
		if (auto e = apkpkg::install_community({"netbird", "netbird-openrc"}); !e.empty())
			return fail(-1, "NetBird installation failed: " + e);
	return ok();
}

Reply uninstall(const std::string &body)
{
	bool purge = vpn::purge(body);
	Operation op;
	if (!op.held) return busy_reply;
	// The client's configuration with its private key and management URL, its state and its logs.
	const std::vector<std::string> state{"/etc/netbird", "/var/lib/netbird", "/var/log/netbird"};
	if (!is_installed()) {
		if (purge)
			if (auto e = vpn::remove_state(state); !e.empty()) return fail(-1, "NetBird login and settings could not be deleted: " + e);
		return ok();
	}
	if (auto e = cli_stop(); !e.empty()) return fail(-1, "NetBird could not be stopped; package was kept: " + e);
	if (auto e = apkpkg::run("remove", "netbird-openrc"); !e.empty()) return fail(-1, "NetBird OpenRC removal failed: " + e);
	if (auto e = apkpkg::run("remove", "netbird"); !e.empty()) return fail(-1, "NetBird removal failed: " + e);
	if (purge)
		if (auto e = vpn::remove_state(state); !e.empty())
			return fail(-1, "NetBird was removed, but its login and settings could not be deleted: " + e);
	return ok();
}

Reply start()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (auto e = cli_start(); !e.empty()) return fail(-1, "NetBird start failed: " + e);
	return ok();
}

Reply restart()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (auto e = cli_restart(); !e.empty()) return fail(-1, "NetBird restart failed: " + e);
	return ok();
}

Reply stop()
{
	Operation op;
	if (!op.held) return busy_reply;
	if (auto e = cli_stop(); !e.empty()) return fail(-1, "NetBird stop failed: " + e);
	return ok();
}

Reply login()
{
	Operation op;
	if (!op.held) return busy_reply;
	std::string url;
	if (auto e = cli_login(url); !e.empty()) return fail(-1, "NetBird login failed: " + e);
	log::debug("NetBird login URL issued: {}", !url.empty());
	return ok("{\"url\":" + q(url) + "}");
}

Reply down()
{
	Operation op;
	if (!op.held) return busy_reply;
	std::string e = cancel_login();
	if (e.empty()) e = run_program(command_timeout, {netbird_path, "down", "--daemon-addr", netbird_socket});
	if (!e.empty()) return fail(-1, "NetBird down failed: " + e);
	return ok();
}

Reply get_status()
{
	if (!is_installed()) return ok(status_json("notInstall", "", "", ""));
	std::string error;
	bool running = service_running(error);
	if (!error.empty()) return fail(-1, "NetBird service status failed: " + error);
	if (!running) return ok(status_json("notRunning", "", "", installed_version()));
	NbStatus s;
	if (auto e = cli_status(s); !e.empty()) return fail(-1, "NetBird status unavailable: " + e);
	std::string state = "notLogin";
	if (s.management_connected && s.signal_connected) state = "running";
	else if (!s.management_url.empty()) state = "stopped";
	std::string version = s.daemon_version.empty() ? installed_version() : s.daemon_version;
	return ok(status_json(state, s.fqdn, ipv4_of(s.ip), version));
}

} // namespace nkos::extensions::netbird
