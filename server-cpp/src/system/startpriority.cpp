#include "system/startpriority.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"

#include <cerrno>
#include <chrono>
#include <cstdlib>
#include <cstring>
#include <dirent.h>
#include <fstream>
#include <functional>
#include <iterator>
#include <sstream>
#include <sys/resource.h>
#include <thread>

namespace nkos::system::startpriority {

namespace {

using namespace std::chrono_literals;

constexpr int boost = -10, steady = -5;
constexpr auto after_viewer = 5s;
constexpr auto limit = 1min;

bool raised = false, shortened = false, released = false;
std::uint64_t timer_generation = 0;
std::chrono::steady_clock::time_point deadline;

// A thread's nice value (field 19 of its stat line); 0 when it is gone.
int nice_of(int tid)
{
	std::ifstream f("/proc/self/task/" + std::to_string(tid) + "/stat");
	std::string line((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
	auto paren = line.rfind(')');
	if (paren == std::string::npos) return 0;
	std::istringstream in(line.substr(paren + 1));
	std::string field;
	for (int i = 0; i < 17 && in >> field; i++) {
		if (i == 16) return std::atoi(field.c_str());
	}
	return 0;
}

// Sets value on every thread that wants it, until a pass changes nothing (threads created meanwhile inherit the old
// value). Empty, or the error.
std::string set_all(int value, const std::function<bool(int)> &want)
{
	std::string last;
	for (int pass = 0; pass < 8; pass++) {
		bool changed = false;
		DIR *dir = ::opendir("/proc/self/task");
		if (!dir) return std::strerror(errno);
		while (dirent *e = ::readdir(dir)) {
			char *end = nullptr;
			long tid = std::strtol(e->d_name, &end, 10);
			if (!*e->d_name || *end) continue;
			if (nice_of(static_cast<int>(tid)) == value || (want && !want(static_cast<int>(tid)))) continue;
			if (::setpriority(PRIO_PROCESS, static_cast<id_t>(tid), value) == 0) changed = true;
			else if (errno != ESRCH) last = std::strerror(errno);
		}
		::closedir(dir);
		if (!last.empty() || !changed) return last;
	}
	return "threads still changing priority after 8 passes";
}

void release()
{
	if (!raised || released) return;
	auto error = set_all(steady, [](int tid) { return nice_of(tid) == boost; });
	if (!error.empty()) {
		log::warn("start priority release: {}; retrying", error);
		std::uint64_t g = ++timer_generation;
		loop::after(1s, [g] {
			if (g == timer_generation) release();
		});
		return;
	}
	released = true;
	log::info("start priority released; steady target nice {}", steady);
}

void schedule(std::chrono::steady_clock::duration in)
{
	std::uint64_t g = ++timer_generation;
	loop::post([g, in] {
		loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(in), [g] {
			if (g == timer_generation) release();
		});
	});
}

} // namespace

void raise()
{
	if (raised) return;
	std::ofstream("/proc/self/oom_score_adj") << -300;
	deadline = std::chrono::steady_clock::now() + limit;
	// Even after a partial failure: the release resets only boosted threads.
	if (auto error = set_all(boost, {}); !error.empty()) log::warn("start priority: {}", error);
	raised = true;
	schedule(limit);
}

void viewer_connected()
{
	if (!raised || released || shortened) return;
	shortened = true;
	if (deadline - std::chrono::steady_clock::now() > after_viewer) schedule(after_viewer);
}

} // namespace nkos::system::startpriority
