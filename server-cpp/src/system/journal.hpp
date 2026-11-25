#pragma once

// The device's event journal, as the Go server's journal package: what happened to the host (power, picture, a
// crash on its screen), who logged in from where, who pressed a button or changed the virtual drive's medium, and
// every setting a signed-in user changed. A file of JSON lines (/var/lib/nanokvm/events.jsonl), 500 entries kept,
// at most 30 added a minute (what is left out is counted in an entry of its own). Any thread.

#include "http/router.hpp"

#include <chrono>
#include <cstdint>
#include <map>
#include <string>
#include <vector>

namespace nkos::system::journal {

inline constexpr int max_entries = 500;

struct Entry {
	std::uint64_t seq = 0;
	std::chrono::system_clock::time_point time{};
	std::string kind, severity, message;
	std::map<std::string, std::string> vars;
	std::string details;
};

// Records an event; seq is the journal's, time now unless set.
void add(Entry entry);
// The newest entries, newest first, at most limit (0: all).
std::vector<Entry> entries(int limit);

// An event of kind ("action", "settings", "system") by actor ("NanoKVM" when empty), severity info.
void record(const std::string &kind, const std::string &message, std::map<std::string, std::string> vars,
            const std::string &actor);
// Something done to the host.
inline void action(const std::string &message, std::map<std::string, std::string> vars, const std::string &actor)
{
	record("action", message, std::move(vars), actor);
}

// The start of the server, with the OS version and, on the first start of a boot, why the chip came on.
void record_start();

// Records every successful request of a signed-in user that changes something: a setting ("settings.changed") or
// an action on NanoKVM itself ("system.action"), with who did it.
void watch_changes(http::Router &router);

} // namespace nkos::system::journal
