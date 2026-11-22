#include "common/log.hpp"

#include <atomic>
#include <cstdio>
#include <ctime>
#include <mutex>

namespace nkos::log {

namespace {
std::atomic<Level> current{Level::info};
std::mutex out;

const char *label(Level level)
{
	switch (level) {
	case Level::debug: return "debug";
	case Level::info: return "info";
	case Level::warn: return "warn";
	case Level::error: return "error";
	}
	return "info";
}
} // namespace

void set_level(Level level) { current.store(level, std::memory_order_relaxed); }

void set_level(std::string_view name)
{
	if (name == "debug") set_level(Level::debug);
	else if (name == "warn" || name == "warning") set_level(Level::warn);
	else if (name == "error") set_level(Level::error);
	else set_level(Level::info);
}

bool enabled(Level level) { return level >= current.load(std::memory_order_relaxed); }

void write(Level level, std::string_view message)
{
	timespec ts{};
	clock_gettime(CLOCK_REALTIME, &ts);
	tm t{};
	gmtime_r(&ts.tv_sec, &t);
	char stamp[32];
	std::strftime(stamp, sizeof stamp, "%Y-%m-%dT%H:%M:%S", &t);
	std::lock_guard lock(out);
	std::fprintf(stdout, "time=\"%s.%03ldZ\" level=%s msg=\"%.*s\"\n", stamp, ts.tv_nsec / 1000000, label(level),
	             static_cast<int>(message.size()), message.data());
	std::fflush(stdout);
}

} // namespace nkos::log
