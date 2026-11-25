#pragma once

// The server's log: one line per message on standard output, which OpenRC passes to syslog as it does for the Go
// server ("logger" in server.yaml: level debug/info/warn/error). Formatting happens only when the level is enabled.

#include <format>
#include <string_view>

namespace nkos::log {

enum class Level { debug, info, warn, error };

void set_level(Level level);
void set_level(std::string_view name);
bool enabled(Level level);
void write(Level level, std::string_view message);
// Every written line (with its newline) also goes to sink: the application log of the log viewer.
void set_sink(void (*sink)(std::string_view line));

template <typename... Args>
void debug(std::format_string<Args...> fmt, Args &&...args)
{
	if (enabled(Level::debug)) write(Level::debug, std::format(fmt, std::forward<Args>(args)...));
}

template <typename... Args>
void info(std::format_string<Args...> fmt, Args &&...args)
{
	if (enabled(Level::info)) write(Level::info, std::format(fmt, std::forward<Args>(args)...));
}

template <typename... Args>
void warn(std::format_string<Args...> fmt, Args &&...args)
{
	if (enabled(Level::warn)) write(Level::warn, std::format(fmt, std::forward<Args>(args)...));
}

template <typename... Args>
void error(std::format_string<Args...> fmt, Args &&...args)
{
	if (enabled(Level::error)) write(Level::error, std::format(fmt, std::forward<Args>(args)...));
}

} // namespace nkos::log
