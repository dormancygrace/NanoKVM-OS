#include "system/failsafe.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "system/logs.hpp"

#include <glaze/glaze.hpp>

#include <algorithm>
#include <cerrno>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <sstream>
#include <sys/stat.h>
#include <unistd.h>

extern char **environ;

namespace nkos::system::failsafe {

namespace {

constexpr std::size_t max_record_bytes = 16480;
constexpr int threshold = 3;
constexpr long window_seconds = 600;

std::string dir;
State state;

std::string directory()
{
	if (const char *p = std::getenv("NANOKVM_RECOVERY_DIR"); p && *p) return p;
	return "/run/nanokvm/recovery";
}

long uptime_seconds()
{
	std::ifstream in("/proc/uptime");
	double v = -1;
	if (!(in >> v)) return -1;
	return static_cast<long>(v);
}

std::optional<std::string> read_record(const std::string &path)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0 || static_cast<std::size_t>(st.st_size) > max_record_bytes) return std::nullopt;
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

// A crash of the video: within the window, while it was starting or running, ended by a signal (the server writes
// no crash report of its own; OOM kills (137) and intentional TERM/QUIT are not counted).
bool recent_video_crash(const std::string &record, long now)
{
	auto nl = record.find('\n');
	if (nl == std::string::npos) return false;
	std::istringstream header(record.substr(0, nl));
	long at = -1;
	int status = 0;
	std::string phase, extra;
	if (!(header >> at >> status >> phase) || (header >> extra)) return false;
	if (at < 0 || now < at || now - at > window_seconds || status == 0) return false;
	if (phase != "initializing" && phase != "ready") return false;
	return status == 139 || status == 134 || status == 135 || status == 132;
}

struct Crash {
	long uptime = 0;
	int status = 0;
	std::string phase;
	std::string report;
};

void write_active()
{
	std::ofstream(dir + "/active", std::ios::trunc) << "1\n";
	::chmod((dir + "/active").c_str(), 0600);
}

} // namespace

bool init()
{
	dir = directory();
	::mkdir(dir.c_str(), 0700);
	long now = uptime_seconds();
	for (int i = 1; i <= 8; i++)
		if (auto record = read_record(dir + "/exit-" + std::to_string(i) + ".txt"))
			if (recent_video_crash(*record, now)) state.crashes++;
	struct stat st{};
	state.active = ::stat((dir + "/active").c_str(), &st) == 0 || state.crashes >= threshold;
	if (state.active) {
		write_active();
		log::warn("video recovery active; management remains available");
	}
	return state.active;
}

State status() { return state; }

std::string status_json()
{
	return std::format(R"({{"active":{},"crashes":{},"retrying":{}}})", state.active, state.crashes, state.retrying);
}

void video_phase(const char *phase)
{
	std::string path = dir + "/video-phase";
	std::ofstream out(path, std::ios::trunc);
	if (!out) {
		log::warn("video phase: {}", std::strerror(errno));
		return;
	}
	out << phase;
	::chmod(path.c_str(), 0600);
}

bool video_disabled() { return state.active; }

bool require_video(http::Response &response)
{
	if (!state.active) return true;
	response.json(503, R"({"code":503,"msg":"video recovery is active"})");
	return false;
}

void add_routes(http::Router &router)
{
	router.add("GET", "/api/vm/video/crashes", auth::guarded([](http::Request &, http::Response &res) {
		std::vector<Crash> records;
		for (int i = 1; i <= 8; i++) {
			auto data = read_record(dir + "/exit-" + std::to_string(i) + ".txt");
			if (!data) continue;
			auto nl = data->find('\n');
			if (nl == std::string::npos) continue;
			Crash c;
			std::istringstream header(data->substr(0, nl));
			if (!(header >> c.uptime >> c.status >> c.phase)) continue;
			c.report = data->substr(nl + 1);
			records.push_back(std::move(c));
		}
		std::stable_sort(records.begin(), records.end(), [](const Crash &a, const Crash &b) { return a.uptime > b.uptime; });
		api::ok(res, records);
	}, {{"admin"}}));
	router.add("POST", "/api/vm/video/retry", auth::guarded([](http::Request &, http::Response &res) {
		if (!state.active || state.retrying) return api::err(res, 1, "video retry is unavailable");
		state.retrying = true;
		// The acknowledgement reaches the browser before the server replaces itself.
		loop::after(std::chrono::seconds(1), [] {
			bool cleared = true;
			for (int i = 0; i <= 8; i++) {
				std::string name = i ? "exit-" + std::to_string(i) + ".txt" : "active";
				if (::unlink((dir + "/" + name).c_str()) != 0 && errno != ENOENT) cleared = false;
			}
			if (cleared) {
				char path[4096];
				ssize_t n = ::readlink("/proc/self/exe", path, sizeof path - 1);
				if (n > 0) {
					path[n] = 0;
					char *argv[] = {path, nullptr};
					// The listeners and every other descriptor end with this process.
					// The stderr pipe's reader does not survive the exec.
					logs::restore_native_stderr();
					process::cloexec_inherited();
					::execve(path, argv, environ);
				}
				log::error("video recovery restart failed: {}", std::strerror(errno));
			}
			// Exec returns only on failure: recovery stays, another attempt is possible.
			write_active();
			state.retrying = false;
		});
		api::ok(res);
	}, {{"admin"}}));
}

} // namespace nkos::system::failsafe
