// Hostname, mDNS, SSH and reboot, as the Go server's service/vm hostname.go, mdns.go, ssh.go and system.go.

#include "system/device_settings.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/password.hpp"
#include "auth/session.hpp"
#include "auth/store.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "config/config.hpp"

#include <cerrno>
#include <format>
#include <fstream>
#include <mutex>
#include <sstream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>
#include <vector>

namespace nkos::system {

namespace {

constexpr const char *boot_hostname = "/boot/hostname";
constexpr const char *etc_hostname = "/etc/hostname";
constexpr const char *etc_hosts = "/etc/hosts";
constexpr const char *avahi_pid = "/run/avahi-daemon/pid";
constexpr const char *mdns_marker = "/etc/kvm/mdns_disabled";
constexpr const char *mdns_runlevel = "/etc/runlevels/default/nanokvm-mdns";
constexpr const char *ssh_script = "/etc/init.d/S50sshd";
constexpr const char *ssh_stop_flag = "/etc/kvm/ssh_stop";
constexpr const char *ssh_rescue_marker = "/run/nanokvm/ssh-rescue";
// Daemons the server starts run at the normal OOM adjustment and nice value (oomscore.ResetPrefix).
constexpr const char *reset_prefix = "echo 0 >/proc/self/oom_score_adj; renice 0 \"$$\" >/dev/null; ";

std::optional<std::string> read(const char *path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

bool exists(const char *path)
{
	struct stat st{};
	return ::stat(path, &st) == 0;
}

std::string trim(const std::string &s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return "";
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

std::vector<std::string> unprotected(std::vector<std::string> argv)
{
	std::vector<std::string> out = {"/bin/sh", "-c", std::string(reset_prefix) + "exec \"$0\" \"$@\""};
	out.insert(out.end(), argv.begin(), argv.end());
	return out;
}

// --- hostname ---------------------------------------------------------------------------------------------------

std::optional<std::string> invalid_hostname(const std::string &name)
{
	if (name.empty() || name.size() > 253) return "hostname must be 1-253 characters";
	std::size_t start = 0;
	for (;;) {
		auto dot = name.find('.', start);
		std::string label = name.substr(start, dot == std::string::npos ? std::string::npos : dot - start);
		auto alnum = [](char c) { return std::isalnum(static_cast<unsigned char>(c)) != 0; };
		bool ok = !label.empty() && label.size() <= 63 && alnum(label.front()) && alnum(label.back()) &&
		          std::all_of(label.begin(), label.end(), [&](char c) { return alnum(c) || c == '-'; });
		if (!ok) return "hostname labels may contain only letters, digits and inner hyphens (1-63 characters)";
		if (dot == std::string::npos) break;
		start = dot + 1;
	}
	return std::nullopt;
}

std::vector<std::string> fields(const std::string &s)
{
	std::vector<std::string> out;
	std::istringstream in(s);
	for (std::string f; in >> f;) out.push_back(f);
	return out;
}

// Renames oldName to newName in the host name columns of /etc/hosts, keeping comments.
std::string replace_host_entry(const std::string &hosts, const std::string &old_name, const std::string &new_name)
{
	if (old_name.empty()) return hosts;
	std::vector<std::string> lines;
	for (std::size_t start = 0;;) {
		auto nl = hosts.find('\n', start);
		lines.push_back(hosts.substr(start, nl == std::string::npos ? std::string::npos : nl - start));
		if (nl == std::string::npos) break;
		start = nl + 1;
	}
	for (auto &line : lines) {
		auto hash = line.find('#');
		std::string body = line.substr(0, hash), comment = hash == std::string::npos ? "" : line.substr(hash + 1);
		auto f = fields(body);
		if (f.size() < 2) continue;
		bool changed = false;
		for (std::size_t j = 1; j < f.size(); j++)
			if (f[j] == old_name) f[j] = new_name, changed = true;
		if (!changed) continue;
		std::string joined;
		for (std::size_t j = 0; j < f.size(); j++) joined += (j ? "\t" : "") + f[j];
		if (hash != std::string::npos) joined += "\t#" + comment;
		line = joined;
	}
	std::string out;
	for (std::size_t i = 0; i < lines.size(); i++) out += (i ? "\n" : "") + lines[i];
	return out;
}

// --- mDNS ---------------------------------------------------------------------------------------------------

std::mutex mdns_mutex;

std::string avahi_daemon_pid()
{
	auto content = read(avahi_pid);
	if (!content) return "";
	std::string v = *content;
	std::erase(v, '\n');
	return v;
}

bool sync_mdns_runlevel(bool enabled)
{
	struct stat st{};
	bool present = ::lstat(mdns_runlevel, &st) == 0;
	if (!present && errno != ENOENT) return false;
	if (enabled && !present) return process::run({"rc-update", "add", "nanokvm-mdns", "default"}).ok();
	if (!enabled && present) return process::run({"rc-update", "del", "nanokvm-mdns", "default"}).ok();
	return true;
}

void set_mdns(http::Request &req, http::Response &res, bool enabled)
{
	http::defer(req, res, [enabled](http::Response &out) {
		std::lock_guard lock(mdns_mutex);
		bool ok;
		if (enabled) {
			ok = ::unlink(mdns_marker) == 0 || errno == ENOENT;
		} else {
			std::ofstream f(mdns_marker, std::ios::trunc);
			ok = static_cast<bool>(f << "1\n");
			f.close();
			::chmod(mdns_marker, 0600);
		}
		ok = ok && sync_mdns_runlevel(enabled);
		ok = ok && process::run(unprotected({"rc-service", "avahi-daemon", enabled ? "start" : "stop"})).ok();
		if (!ok) return api::err(out, -1, "failed to change mDNS state");
		api::ok(out);
	});
}

// After a boot the saved choice and the service's runlevel agree again (an update may have added the service).
void reconcile_mdns()
{
	std::lock_guard lock(mdns_mutex);
	struct stat st{};
	bool enabled = ::stat(mdns_marker, &st) != 0;
	if (enabled && errno != ENOENT) {
		log::warn("mDNS setting unreadable");
		return;
	}
	bool missing = ::lstat(mdns_runlevel, &st) != 0;
	if (missing && errno != ENOENT) {
		log::warn("mDNS runlevel unreadable");
		return;
	}
	if (!sync_mdns_runlevel(enabled)) {
		log::warn("failed to update the mDNS service runlevel");
		return;
	}
	if (enabled && missing && avahi_daemon_pid().empty())
		if (!process::run(unprotected({"rc-service", "avahi-daemon", "start"})).ok()) log::warn("failed to start mDNS");
}

// --- SSH ----------------------------------------------------------------------------------------------------

bool ssh_enabled() { return !exists(ssh_stop_flag); }

} // namespace

void start_device_settings()
{
	loop::after(std::chrono::seconds(30), [] { loop::blocking(reconcile_mdns); });
}

void add_device_settings_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	auth::Guard admin_recent{{"admin"}, true};

	router.add("GET", "/api/vm/hostname", auth::guarded([](http::Request &, http::Response &res) {
		auto data = read(etc_hostname);
		if (!data) return api::err(res, -1, "read Hostname failed");
		std::string name = *data;
		std::erase(name, '\n');
		api::send(res, 0, "success", "{\"hostname\":" + api::quote(name) + "}");
	}));
	router.add("POST", "/api/vm/hostname", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<std::string> hostname;
		if (params) hostname = params->string("hostname", ok);
		if (!params || !ok || !hostname || hostname->empty()) return api::err(res, -1, "invalid arguments");
		if (auto e = invalid_hostname(*hostname)) return api::err(res, -1, *e);
		http::defer(req, res, [name = *hostname](http::Response &out) {
			auto current = read(etc_hostname);
			if (!current) return api::err(out, -1, "read Hostname failed");
			std::string old_name = trim(*current);
			if (old_name != name) {
				auto hosts = read(etc_hosts);
				if (!hosts) return api::err(out, -1, "read Hosts failed");
				if (!config::atomic_write(etc_hosts, replace_host_entry(*hosts, old_name, name), 0644))
					return api::err(out, -2, "failed to write data");
			}
			if (!config::atomic_write(boot_hostname, name, 0644)) return api::err(out, -2, "failed to write data");
			if (!config::atomic_write(etc_hostname, name, 0644)) return api::err(out, -3, "failed to write data");
			api::ok(out);
			log::debug("set Hostname: {}", name);
			// Applied after the answer, as the Go server does.
			loop::post([] {
				loop::blocking([] {
					if (!process::run({"hostname", "-F", etc_hostname}).ok()) log::warn("apply hostname failed");
				});
			});
		});
	}, admin));

	router.add("GET", "/api/vm/mdns", auth::guarded([](http::Request &, http::Response &res) {
		api::send(res, 0, "success", std::string("{\"enabled\":") + (avahi_daemon_pid().empty() ? "false" : "true") + "}");
	}, admin));
	router.add("POST", "/api/vm/mdns/enable", auth::guarded([](http::Request &req, http::Response &res) { set_mdns(req, res, true); }, admin));
	router.add("POST", "/api/vm/mdns/disable", auth::guarded([](http::Request &req, http::Response &res) { set_mdns(req, res, false); }, admin));

	router.add("GET", "/api/vm/ssh", auth::guarded([](http::Request &, http::Response &res) {
		bool enabled = ssh_enabled();
		bool rescue = !enabled && exists(ssh_rescue_marker);
		api::send(res, 0, "success", std::format(R"({{"enabled":{},"rescue":{}}})", enabled, rescue));
	}, admin));
	router.add("POST", "/api/vm/ssh/enable", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<std::string> password, encoding;
		if (params) {
			password = params->string("password", ok);
			encoding = params->string("passwordEncoding", ok);
		}
		if (!params || !ok || !password || password->empty()) return api::err(res, -1, "a new root password is required");
		auto plain = auth::decode_credential(*password, encoding.value_or(""));
		std::string lowered = plain ? trim(*plain) : "";
		for (auto &c : lowered) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
		if (!plain || lowered == "root") return api::err(res, -1, "invalid root password");
		if (auto e = auth::validate_password(*plain)) return api::err(res, -1, *e);
		http::defer(req, res, [plain = *plain](http::Response &out) {
			// passwd reads the new password twice from standard input.
			auto r = process::run({"passwd", "root"}, {.input = plain + "\n" + plain + "\n"});
			if (!r.ok()) {
				log::error("failed to change root password before enabling SSH");
				return api::err(out, -1, "could not set root password");
			}
			if (!process::run({"sh", "-c", std::string(reset_prefix) + ssh_script + " permanent_on"}).ok()) {
				log::error("failed to run SSH script");
				return api::err(out, -1, "operation failed");
			}
			::unlink(ssh_rescue_marker);
			api::ok(out);
		});
	}, admin_recent));
	router.add("POST", "/api/vm/ssh/disable", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			if (!process::run({"sh", "-c", std::string(ssh_script) + " permanent_off"}).ok()) {
				log::error("failed to run SSH script");
				return api::err(out, -1, "operation failed");
			}
			::unlink(ssh_rescue_marker);
			api::ok(out);
		});
	}, admin));

	router.add("POST", "/api/vm/system/reboot", auth::guarded([](http::Request &req, http::Response &res) {
		log::info("reboot system...");
		http::defer(req, res, [](http::Response &out) {
			if (!process::run({"reboot"}).ok()) {
				log::error("failed to reboot");
				return api::err(out, -1, "operation failed");
			}
			api::ok(out);
		});
	}, admin));
}

} // namespace nkos::system
