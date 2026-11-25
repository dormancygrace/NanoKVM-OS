#pragma once

// A five-field cron line as BusyBox crond (1.37/1.38, miscutils/crond.c) reads it: the masks ParseField fills and
// FixDayDow adjusts, matched as flag_starting_jobs does. Where crond itself misbehaves (a step of 0, a month 0
// written outside its array) the expression is refused instead. As the Go server's cronexpr.go.

#include "common/tz.hpp"

#include <array>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace nkos::system::cron {

struct Schedule {
	std::array<bool, 60> minute{};
	std::array<bool, 24> hour{};
	// Indexed by day of month; index 0 exists as in crond, never matches, and makes "*" differ from "1-31".
	std::array<bool, 32> dom{};
	std::array<bool, 12> month{};  // 0 = January
	std::array<bool, 7> dow{};     // 0 = Sunday

	// Up to count run times (Unix seconds) after from, in zone, looking at most five years ahead. It walks elapsed
	// time as crond checks each minute's local time: a DST gap is crossed and a repeated hour is seen twice.
	std::vector<std::int64_t> next(std::int64_t from, int count, const tz::Zone &zone) const;
};

// The schedule, or nullopt with the error text.
std::optional<Schedule> parse(const std::string &expr, std::string &error);

} // namespace nkos::system::cron
