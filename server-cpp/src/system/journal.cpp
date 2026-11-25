#include "system/journal.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/time.hpp"
#include "config/config.hpp"

#include <glaze/glaze.hpp>

#include <cstdio>
#include <ctime>
#include <deque>
#include <fcntl.h>
#include <fstream>
#include <mutex>
#include <optional>
#include <sstream>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::system::journal {

namespace {

using Clock = std::chrono::steady_clock;

constexpr const char *path = "/var/lib/nanokvm/events.jsonl";
constexpr int max_per_minute = 30;
constexpr std::size_t max_details = 2000;
constexpr const char *boot_reason_nvmem = "/sys/bus/nvmem/devices/cv1800-rtc-reason/nvmem";
constexpr const char *boot_reason_mark = "/run/nanokvm-boot-reason";

// One line of the file, as Go's json.Marshal(Entry) writes it.
struct Line {
	std::uint64_t seq = 0;
	std::string time;
	std::string kind, severity, message;
	std::optional<std::map<std::string, std::string>> vars;
	std::optional<std::string> details;
};

std::mutex mutex;
bool loaded = false;
std::vector<Entry> list;
int lines = 0;              // lines in the file
bool needs_newline = false;  // a partial last line must not swallow the next entry
std::uint64_t seq = 0;
std::deque<Clock::time_point> recent;  // entries added in the last minute
int dropped = 0;

// RFC 3339 with an optional fraction and a zone (Z or +hh:mm), as Go writes time.Time.
std::optional<std::chrono::system_clock::time_point> parse_time(const std::string &s)
{
	std::tm tm{};
	int consumed = 0;
	if (std::sscanf(s.c_str(), "%4d-%2d-%2dT%2d:%2d:%2d%n", &tm.tm_year, &tm.tm_mon, &tm.tm_mday, &tm.tm_hour, &tm.tm_min,
	                &tm.tm_sec, &consumed) != 6)
		return std::nullopt;
	tm.tm_year -= 1900;
	tm.tm_mon -= 1;
	std::size_t at = static_cast<std::size_t>(consumed);
	long nanos = 0;
	if (at < s.size() && s[at] == '.') {
		long scale = 100000000;
		for (at++; at < s.size() && std::isdigit(static_cast<unsigned char>(s[at])); at++) {
			nanos += (s[at] - '0') * scale;
			scale /= 10;
		}
	}
	long offset = 0;
	if (at < s.size() && (s[at] == '+' || s[at] == '-')) {
		int h = 0, m = 0;
		if (std::sscanf(s.c_str() + at + 1, "%2d:%2d", &h, &m) != 2) return std::nullopt;
		offset = (s[at] == '-' ? -1 : 1) * (h * 3600L + m * 60L);
	} else if (at >= s.size() || s[at] != 'Z') {
		return std::nullopt;
	}
	time_t secs = timegm(&tm) - offset;
	return std::chrono::system_clock::from_time_t(secs) + std::chrono::nanoseconds(nanos);
}

std::string to_line(const Entry &e)
{
	Line l{e.seq, rfc3339nano(e.time), e.kind, e.severity, e.message, std::nullopt, std::nullopt};
	if (!e.vars.empty()) l.vars = e.vars;
	if (!e.details.empty()) l.details = e.details;
	std::string out;
	(void)glz::write_json(l, out);
	return out;
}

void trim()
{
	if (list.size() > static_cast<std::size_t>(max_entries)) list.erase(list.begin(), list.end() - max_entries);
}

void load()
{
	if (loaded) return;
	loaded = true;
	std::ifstream in(path, std::ios::binary);
	if (!in) return;
	std::stringstream ss;
	ss << in.rdbuf();
	std::string data = ss.str();
	needs_newline = !data.empty() && data.back() != '\n';
	std::size_t start = 0;
	while (start < data.size()) {
		std::size_t end = data.find('\n', start);
		if (end == std::string::npos) end = data.size();
		std::string text = data.substr(start, end - start);
		start = end + 1;
		lines++;
		Line l;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(l, text) || l.message.empty()) continue;
		Entry e{l.seq, parse_time(l.time).value_or(std::chrono::system_clock::time_point{}), l.kind, l.severity, l.message,
		        l.vars.value_or(std::map<std::string, std::string>{}), l.details.value_or("")};
		list.push_back(std::move(e));
		seq = std::max(seq, l.seq);
	}
	trim();
}

bool write(const Entry &e)
{
	if (lines >= 2 * max_entries) {
		std::string all;
		for (const auto &kept : list) all += to_line(kept) + "\n";
		lines = static_cast<int>(list.size());
		if (!config::atomic_write(path, all, 0600)) return false;
		needs_newline = false;
		return true;
	}
	::mkdir("/var/lib/nanokvm", 0755);
	int fd = ::open(path, O_WRONLY | O_APPEND | O_CREAT | O_CLOEXEC, 0600);
	if (fd < 0) return false;
	std::string line = (needs_newline ? "\n" : "") + to_line(e) + "\n";
	bool ok = ::write(fd, line.data(), line.size()) == static_cast<ssize_t>(line.size());
	if (ok) {
		needs_newline = false;
		lines++;
		ok = ::fsync(fd) == 0;
	}
	::close(fd);
	return ok;
}

void append(Entry e)
{
	e.seq = ++seq;
	list.push_back(e);
	trim();
	if (!write(e)) log::error("event journal: cannot write {}", path);
}

// RTC_ST_ON_REASON: why the chip last came on, once per boot ("" after the first call or when unreadable).
std::string boot_reason()
{
	struct stat st{};
	if (::stat(boot_reason_mark, &st) == 0) return "";
	std::ifstream in(boot_reason_nvmem, std::ios::binary);
	unsigned char raw[4];
	bool ok = static_cast<bool>(in.read(reinterpret_cast<char *>(raw), 4));
	if (std::FILE *f = std::fopen(boot_reason_mark, "w")) std::fclose(f);
	::chmod(boot_reason_mark, 0600);
	if (!ok) return "";
	std::uint32_t value = raw[0] | raw[1] << 8 | raw[2] << 16 | static_cast<std::uint32_t>(raw[3]) << 24;
	std::uint32_t inputs = value >> 16;
	if ((value & 0xf) == 0) return "power";
	if (inputs & 0x800) return "watchdog";
	if (inputs & 0x200) return "overheat";
	if (inputs & 0x500) return "restart";
	return "";
}

// Requests that change something but are no change of a setting (Go's journalSkipped): route patterns, "*" at an
// end covers what starts or ends with the rest.
const std::vector<std::string> skipped = {
	"/api/auth/login", "/api/auth/login/totp", "/api/auth/logout",
	"/api/auth/activity",
	"/api/hid/paste", "/api/hid/sequence", "/api/hid/reset",
	"/api/vm/gpio", "/api/atx*", "/api/network/wol",
	"/api/storage/image/mount", "/api/storage/remote/*",
	"/api/storage/user-disk/files/*", "/api/storage/image/delete",
	"/api/download/*",
	"/api/stream/state", "/api/stream/mjpeg/detect/stop",
	"/api/network/wifi/verify",
	"/api/vm/serial-console/test", "/api/vm/schedule/preview", "/api/vm/schedule/run",
	"/api/vm/script/run", "/api/vm/video/retry", "/api/vm/tls/reload",
	"/api/vm/oled/preview",
	"*/preview", "*/check", "*/test", "*/oauth/*", "*/verify",
	"/api/os/update/notice/check",
	"/api/vm/system/shutdown",
	"/api/system/recovery/export",
	"/api/picoclaw/actions", "/api/picoclaw/mcp", "/api/picoclaw/load-image",
	"/api/picoclaw/sessions/*", "/api/picoclaw/runtime/session",
};

const std::map<std::string, std::string> system_actions = {
	{"/api/vm/system/reboot", "reboot"},
	{"/api/vm/hdmi/reset", "HDMI reset"},
};

bool skips(const std::string &route)
{
	for (const auto &pattern : skipped) {
		bool lead = pattern.front() == '*', trail = pattern.back() == '*';
		std::string core = pattern.substr(lead ? 1 : 0, pattern.size() - (lead ? 1 : 0) - (trail ? 1 : 0));
		if (lead && trail) {
			if (route.find(core) != std::string::npos) return true;
		} else if (lead) {
			if (route.ends_with(core)) return true;
		} else if (trail) {
			if (route.starts_with(core)) return true;
		} else if (route == pattern) {
			return true;
		}
	}
	return false;
}

// A JSON answer with a code other than 0 at its start.
bool failed(const std::string &body)
{
	std::string_view head(body.data(), std::min<std::size_t>(body.size(), 256));
	if (head.empty() || head[0] != '{') return false;
	auto i = head.find("\"code\":");
	if (i == std::string_view::npos) return false;
	auto rest = head.substr(i + 7);
	while (!rest.empty() && rest[0] == ' ') rest.remove_prefix(1);
	return !rest.empty() && rest[0] != '0';
}

// A route in words: "/api/vm/video/watermark" is "video › watermark", ":parameters" by their values.
std::string setting_name(const http::Request &req, const std::string &route)
{
	std::vector<std::string> parts;
	std::string rest = route.starts_with("/api/") ? route.substr(5) : route;
	for (std::size_t start = 0;;) {
		auto end = rest.find('/', start);
		parts.push_back(rest.substr(start, end == std::string::npos ? std::string::npos : end - start));
		if (end == std::string::npos) break;
		start = end + 1;
	}
	std::string out;
	for (std::size_t i = 0; i < parts.size(); i++) {
		std::string part = parts[i];
		if (part.empty() || (i == 0 && part == "vm" && parts.size() > 1)) continue;
		if (part.starts_with(":")) part = req.param(part.substr(1));
		for (auto &c : part)
			if (c == '-') c = ' ';
		if (!out.empty()) out += " \xe2\x80\xba ";
		out += part;
	}
	return out;
}

} // namespace

void add(Entry e)
{
	std::lock_guard lock(mutex);
	load();
	auto now = Clock::now();
	while (!recent.empty() && now - recent.front() >= std::chrono::minutes(1)) recent.pop_front();
	if (recent.size() >= static_cast<std::size_t>(max_per_minute)) {
		dropped++;
		return;
	}
	if (dropped > 0) {
		append(Entry{0, std::chrono::system_clock::now(), "journal", "warning", "journal.dropped", {{"count", std::to_string(dropped)}}, ""});
		dropped = 0;
	}
	if (e.time == std::chrono::system_clock::time_point{}) e.time = std::chrono::system_clock::now();
	if (e.details.size() > max_details) e.details.resize(max_details);
	recent.push_back(now);
	append(std::move(e));
}

std::vector<Entry> entries(int limit)
{
	std::lock_guard lock(mutex);
	load();
	std::size_t n = limit <= 0 || static_cast<std::size_t>(limit) > list.size() ? list.size() : static_cast<std::size_t>(limit);
	return std::vector<Entry>(list.rbegin(), list.rbegin() + static_cast<long>(n));
}

void record(const std::string &kind, const std::string &message, std::map<std::string, std::string> vars, const std::string &actor)
{
	vars["actor"] = actor.empty() ? "NanoKVM" : actor;
	add(Entry{0, {}, kind, "info", message, std::move(vars), ""});
}

void record_start()
{
	std::string version = "?";
	if (std::ifstream in("/kvmapp/version"); in) {
		std::string v((std::istreambuf_iterator<char>(in)), {});
		auto a = v.find_first_not_of(" \t\r\n"), b = v.find_last_not_of(" \t\r\n");
		version = a == std::string::npos ? "" : v.substr(a, b - a + 1);
	}
	std::string message = "system.started", severity = "info";
	if (std::string reason = boot_reason(); !reason.empty()) {
		message += "." + reason;
		if (reason == "watchdog" || reason == "overheat") severity = "warning";
	}
	add(Entry{0, {}, "system", severity, message, {{"version", version}, {"actor", "NanoKVM"}}, ""});
}

void watch_changes(http::Router &router)
{
	router.after([](const http::Request &req, const http::Response &res) {
		if (req.method != "POST" && req.method != "PUT" && req.method != "DELETE" && req.method != "PATCH") return;
		const std::string &route = req.route;
		// Only the action slot selects a preview/check operation; other parameters stay literal.
		std::string operation = route;
		if (auto at = operation.find(":action"); at != std::string::npos) operation.replace(at, 7, req.param("action"));
		if (route.empty() || !route.starts_with("/api/") || skips(operation) || res.status != 200) return;
		if (!req.auth || req.auth->username.empty()) return;
		if (failed(res.body)) return;
		std::string actor = req.auth->username;
		// The file write ends with an fsync: not on the loop thread.
		if (auto it = system_actions.find(route); it != system_actions.end()) {
			loop::blocking([actor, action = it->second] { record("system", "system.action", {{"action", action}}, actor); });
			return;
		}
		std::string setting = setting_name(req, route);
		if (req.method == "DELETE") setting += " (removed)";
		loop::blocking([actor, setting] { record("settings", "settings.changed", {{"setting", setting}}, actor); });
	});
}

} // namespace nkos::system::journal
