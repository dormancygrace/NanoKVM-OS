#include "system/time_settings.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/process.hpp"
#include "config/config.hpp"

#include <glaze/glaze.hpp>

#include <algorithm>
#include <arpa/inet.h>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <format>
#include <fstream>
#include <mutex>
#include <sstream>
#include <sys/stat.h>
#include <sys/time.h>
#include <unistd.h>

namespace nkos::system {

namespace {

constexpr const char *etc = "/etc";
constexpr const char *crond_zone_stale = "/run/nanokvm/crond-zone-stale";
constexpr const char *web_title_file = "/etc/kvm/web-title";

struct TimeConfig {
	std::vector<std::string> servers{"0.pool.ntp.org", "1.pool.ntp.org", "2.pool.ntp.org", "3.pool.ntp.org"};
	std::string timezone = "UTC";
	std::string format = "24";
	std::string dateFormat = "auto";
};

std::mutex store_mutex;

struct ClockReq {
	std::int64_t now = 0;
};

std::optional<std::string> read_file(const std::string &path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

std::string trim(const std::string &s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

std::vector<std::string> fields(const std::string &line)
{
	std::istringstream in(line);
	std::vector<std::string> out;
	std::string f;
	while (in >> f) out.push_back(f);
	return out;
}

std::vector<std::string> zones()
{
	std::vector<std::string> names{"UTC"};
	std::istringstream in(read_file("/usr/share/zoneinfo/zone.tab").value_or(""));
	std::string line;
	while (std::getline(in, line)) {
		auto f = fields(line);
		if (f.size() >= 3 && !f[0].starts_with("#")) names.push_back(f[2]);
	}
	std::sort(names.begin(), names.end());
	names.erase(std::unique(names.begin(), names.end()), names.end());
	return names;
}

// The zone's TZif data; an error message when the name is not a zone.
std::optional<std::string> zone_data(const std::string &name, std::string &error)
{
	if (name.empty() || name.find('\\') != std::string::npos || name.front() == '/' || name == ".." ||
	    name.starts_with("../") || name.find("/../") != std::string::npos || name.ends_with("/..") ||
	    name.find("//") != std::string::npos || name.find("/./") != std::string::npos || name.starts_with("./") ||
	    name.ends_with("/.") || name == ".") {
		error = "unknown time zone";
		return std::nullopt;
	}
	auto data = read_file("/usr/share/zoneinfo/" + name);
	if (!data) {
		error = "unknown time zone; install tzdata";
		return std::nullopt;
	}
	if (data->size() < 44 || data->compare(0, 4, "TZif") != 0) {
		error = "invalid time zone";
		return std::nullopt;
	}
	return data;
}

bool hostname_label(const std::string &l)
{
	if (l.empty() || l.size() > 63) return false;
	auto alnum = [](char c) { return std::isalnum(static_cast<unsigned char>(c)); };
	if (!alnum(l.front()) || !alnum(l.back())) return false;
	for (char c : l)
		if (!alnum(c) && c != '-') return false;
	return true;
}

std::optional<std::string> validate(const TimeConfig &c)
{
	if (c.format != "24" && c.format != "12") return "time format must be 24 or 12";
	if (c.dateFormat != "auto" && c.dateFormat != "dmy" && c.dateFormat != "mdy" && c.dateFormat != "ymd")
		return "date format must be auto, dmy, mdy or ymd";
	std::string error;
	if (!zone_data(c.timezone, error)) return error;
	if (c.servers.empty() || c.servers.size() > 6) return "choose between 1 and 6 NTP servers";
	std::vector<std::string> seen;
	for (const auto &server : c.servers) {
		if (server.empty() || server.size() > 253 || trim(server) != server) return "invalid NTP server";
		unsigned char buf[16];
		bool ip = ::inet_pton(AF_INET, server.c_str(), buf) == 1 || ::inet_pton(AF_INET6, server.c_str(), buf) == 1;
		if (!ip) {
			std::string host = server.ends_with(".") ? server.substr(0, server.size() - 1) : server;
			std::istringstream in(host);
			std::string label;
			while (std::getline(in, label, '.'))
				if (!hostname_label(label)) return "NTP server must be a hostname or IP address";
			if (host.empty() || host.ends_with(".")) return "NTP server must be a hostname or IP address";
		}
		std::string key = server.ends_with(".") ? server.substr(0, server.size() - 1) : server;
		for (auto &ch : key) ch = static_cast<char>(std::tolower(static_cast<unsigned char>(ch)));
		if (std::find(seen.begin(), seen.end(), key) != seen.end()) return "duplicate NTP server";
		seen.push_back(key);
	}
	return std::nullopt;
}

std::pair<TimeConfig, std::optional<std::string>> read_config()
{
	TimeConfig c;
	if (auto data = read_file(std::string(etc) + "/kvm/date-time.json")) {
		if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(c, *data); e) return {c, "invalid date-time.json"};
		if (c.dateFormat.empty()) c.dateFormat = "auto";
		return {c, validate(c)};
	}
	if (auto tz = read_file(std::string(etc) + "/timezone")) {
		std::string error;
		if (zone_data(trim(*tz), error)) c.timezone = trim(*tz);
	}
	if (auto conf = read_file(std::string(etc) + "/chrony.conf")) {
		std::vector<std::string> servers;
		std::istringstream in(*conf);
		std::string line;
		while (std::getline(in, line)) {
			auto f = fields(line);
			if (f.size() >= 2 && (f[0] == "server" || f[0] == "pool")) servers.push_back(f[1]);
		}
		if (!servers.empty()) c.servers = servers;
	}
	return {c, std::nullopt};
}

std::string server_config(const std::string &old_text, const std::vector<std::string> &servers)
{
	std::string old = old_text;
	if (old.empty()) old = "driftfile /var/lib/chrony/drift\nmakestep 1.0 3\nrtcsync\nport 0\ncmdport 0\nbindcmdaddress /run/chrony/chronyd.sock\n";
	std::vector<std::string> lines;
	for (const auto &h : servers) lines.push_back("server " + h + " iburst");
	while (!old.empty() && old.back() == '\n') old.pop_back();
	std::istringstream in(old);
	std::string line;
	while (std::getline(in, line)) {
		auto f = fields(line);
		if (!f.empty() && (f[0] == "server" || f[0] == "pool")) continue;
		lines.push_back(line);
	}
	std::string out;
	for (std::size_t i = 0; i < lines.size(); i++) out += (i ? "\n" : "") + lines[i];
	return out + "\n";
}

// Writes the files (each atomically), restores every one on a failure, restarts chrony when the servers changed.
std::optional<std::string> save_config(TimeConfig c)
{
	if (c.dateFormat.empty()) c.dateFormat = "auto";
	if (auto e = validate(c)) return e;
	std::lock_guard lock(store_mutex);
	auto [old, read_error] = read_config();
	if (read_error) return read_error;
	std::string error;
	auto tz = zone_data(c.timezone, error);
	std::string ntp_path = std::string(etc) + "/chrony.conf";
	std::string ntp = read_file(ntp_path).value_or("");
	std::string json;
	struct Pretty : glz::opts {
		std::uint8_t indentation_width = 2;
	};
	(void)glz::write<Pretty{{.prettify = true}}>(c, json);
	std::vector<std::pair<std::string, std::string>> files = {
		{std::string(etc) + "/localtime", *tz},
		{std::string(etc) + "/timezone", c.timezone + "\n"},
		{std::string(etc) + "/kvm/date-time.json", json + "\n"},
	};
	bool restart = old.servers != c.servers;
	if (restart) files.push_back({ntp_path, server_config(ntp, c.servers)});
	struct Backup {
		std::string path, data;
		bool missing;
	};
	std::vector<Backup> backups;
	auto rollback = [&] {
		for (auto it = backups.rbegin(); it != backups.rend(); ++it) {
			if (it->missing) ::unlink(it->path.c_str());
			else config::atomic_write(it->path, it->data, 0644);
		}
	};
	for (const auto &[path, data] : files) {
		auto before = read_file(path);
		backups.push_back({path, before.value_or(""), !before});
		if (!config::atomic_write(path, data, 0644)) {
			rollback();
			return std::string(std::strerror(errno));
		}
	}
	if (restart) {
		auto restart_chrony = [] {
			return process::run({"/bin/sh", "-c", "echo 0 >/proc/self/oom_score_adj; renice 0 \"$$\" >/dev/null; exec \"$0\" \"$@\"",
			                     "/etc/init.d/S49chronyd", "restart"},
			                    {.timeout = std::chrono::seconds(12)});
		};
		if (!restart_chrony().ok()) {
			rollback();
			restart_chrony();
			return std::string("NTP restart failed");
		}
	}
	return std::nullopt;
}

std::optional<bool> chrony_synchronized()
{
	auto r = process::run({"/usr/bin/chronyc", "-n", "-c", "-u", "root", "-h", "/run/chrony/chronyd.sock", "tracking"},
	                      {.timeout = std::chrono::seconds(2), .env = {"LC_ALL=C"}});
	if (!r.ok()) return std::nullopt;
	std::string line = trim(r.output);
	std::vector<std::string> f;
	std::istringstream in(line);
	std::string part;
	while (std::getline(in, part, ',')) f.push_back(part);
	if (line.find('\n') != std::string::npos || f.size() != 14) return std::nullopt;
	int stratum = std::atoi(f[2].c_str());
	if (stratum < 1 || stratum > 15 || f[0] == "00000000" || f[0] == "7F7F0101") return false;
	if (f[13] == "Normal" || f[13] == "Insert second" || f[13] == "Delete second") return true;
	if (f[13] == "Not synchronised") return false;
	return std::nullopt;
}

void date_time(http::Response &res)
{
	auto [cfg, error] = [] {
		std::lock_guard lock(store_mutex);
		return read_config();
	}();
	if (error) return api::err(res, -1, "Cannot read date and time settings");
	bool synced = chrony_synchronized().value_or(false);
	std::string config_json;
	(void)glz::write_json(cfg, config_json);
	std::string zones_json;
	(void)glz::write_json(zones(), zones_json);
	auto now = std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::system_clock::now().time_since_epoch()).count();
	api::send(res, 0, "success",
	          std::format(R"({{"config":{},"daemon":"chrony","now":{},"synchronized":{},"zones":{}}})", config_json, now,
	                      synced, zones_json));
}

// musl reads /etc/localtime once per process: a running crond keeps the old zone's times until it restarts. The mark
// in /run says what a failed attempt knew, for the next save.
std::optional<std::string> restart_crond_for_zone()
{
	std::string pending = trim(read_file(crond_zone_stale).value_or(""));
	auto mark = [](const char *state) {
		::mkdir("/run/nanokvm", 0755);
		std::ofstream(crond_zone_stale, std::ios::trunc) << state << "\n";
	};
	std::string action = "restart";
	auto status = process::run({"rc-service", "crond", "status"});
	if (status.started && status.exit_code == 3) {
		if (pending != "running") {
			::unlink(crond_zone_stale);
			return std::nullopt;
		}
		action = "start";
	} else if (!status.ok()) {
		if (pending != "running") mark("unknown");
		return "cannot ask whether cron runs";
	}
	auto r = process::run({"rc-service", "crond", action});
	if (!r.ok()) {
		mark("running");
		return "cron " + action + ": " + trim(r.output);
	}
	::unlink(crond_zone_stale);
	return std::nullopt;
}

} // namespace

void add_time_routes(http::Router &router)
{
	router.add("GET", "/api/vm/date-time", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) { date_time(out); });
	}));
	router.add("POST", "/api/vm/date-time", auth::guarded([](http::Request &req, http::Response &res) {
		TimeConfig cfg;
		if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(cfg, req.body); e || req.body.empty())
			return api::err(res, -1, "Invalid date and time settings");
		if (auto e = validate(cfg)) return api::err(res, -1, *e);
		http::defer(req, res, [cfg](http::Response &out) {
			std::string previous_zone = [] {
				std::lock_guard lock(store_mutex);
				return read_config().first.timezone;
			}();
			if (save_config(cfg)) return api::err(out, -2, "Cannot apply date and time settings; previous configuration was restored where possible");
			struct stat st{};
			if (previous_zone != cfg.timezone || ::stat(crond_zone_stale, &st) == 0) {
				if (auto e = restart_crond_for_zone()) {
					log::warn("restart crond for the new time zone: {}", *e);
					return api::err(out, -3, "The time zone is saved, but cron could not be restarted: scheduled tasks may still run at the previous zone's times. Save again to retry.");
				}
			}
			date_time(out);
		});
	}, {{"admin"}}));
	router.add("POST", "/api/vm/date-time/clock", auth::guarded([](http::Request &req, http::Response &res) {
		ClockReq r;
		if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body); e) return api::err(res, -1, "Invalid time");
		// Later than this OS can be built, earlier than any RTC it runs with keeps counting.
		if (r.now < 1735689600000LL || r.now >= 4102444800000LL) return api::err(res, -1, "Invalid time");
		http::defer(req, res, [now = r.now](http::Response &out) {
			if (chrony_synchronized().value_or(false)) return api::err(out, -1, "The clock is synchronised by NTP");
			timeval tv{static_cast<time_t>(now / 1000), static_cast<suseconds_t>(now % 1000 * 1000)};
			if (::settimeofday(&tv, nullptr) != 0) {
				log::error("set the clock: {}", std::strerror(errno));
				return api::err(out, -2, "Cannot set the clock");
			}
			struct stat st{};
			if (::stat("/dev/rtc0", &st) == 0) {
				if (!process::run({"hwclock", "-w", "-u"}, {.timeout = std::chrono::seconds(5)}).ok()) {
					log::warn("save the clock to the RTC failed");
					return api::err(out, -2, "System clock set, but could not save it to the RTC");
				}
			}
			date_time(out);
		});
	}, {{"admin"}}));

	router.add("GET", "/api/vm/web-title", auth::guarded([](http::Request &, http::Response &res) {
		auto data = read_file(web_title_file);
		if (!data) return api::err(res, -1, "read web title failed");
		std::erase(*data, '\n');
		api::send(res, 0, "success", std::format(R"({{"title":{}}})", api::quote(*data)));
	}));
	router.add("POST", "/api/vm/web-title", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<std::string> title;
		if (params) title = params->string("title", ok);
		if (!params || !ok) return api::err(res, -1, "invalid arguments");
		std::string t = title.value_or("");
		if (t.empty() || t == "NanoKVM OS") {
			if (::unlink(web_title_file) != 0) return api::err(res, -2, "reset failed");
		} else {
			std::ofstream out(web_title_file, std::ios::trunc);
			if (!(out << t)) return api::err(res, -3, "write failed");
		}
		api::ok(res);
	}, {{"admin"}}));
}

} // namespace nkos::system
