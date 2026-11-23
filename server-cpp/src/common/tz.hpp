#pragma once

// A time zone from TZif data (RFC 8536, version 2 and later: the 64-bit block and the POSIX TZ footer for times
// after the last transition). The device's zone is /etc/localtime read when asked: the C library loads its zone once
// and would miss a change made in System > Date and time (the Go server's deviceLocation does the same).

#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::tz {

struct Local {
	int year = 1970, month = 1, day = 1;  // month 1-12, day 1-31
	int hour = 0, minute = 0, second = 0;
	int weekday = 4;                      // 0 = Sunday
	int offset = 0;                       // seconds east of UTC
	bool dst = false;
	std::string abbreviation;
};

class Zone {
public:
	// UTC when data does not parse.
	static Zone parse(std::string_view data);
	static Zone utc();
	// /etc/localtime as it is now (UTC when it cannot be read).
	static Zone device();

	Local local(std::int64_t unix_seconds) const;
	// The next instant after unix_seconds where the offset or abbreviation changes, if any.
	std::optional<std::int64_t> next_change(std::int64_t unix_seconds) const;

	struct Type {
		int offset = 0;
		bool dst = false;
		std::string abbreviation;
	};
	struct Rule {  // a POSIX TZ string
		Type std, dst;
		bool has_dst = false;
		// Start and end of daylight time: kind 'J' (Julian 1-365, no Feb 29), 'D' (0-365), 'M' (month.week.day).
		struct When {
			char kind = 'M';
			int month = 0, week = 0, day = 0;
			int time = 7200;  // seconds of local time
		} start, end;
	};

private:
	std::vector<std::int64_t> times_;  // transitions
	std::vector<std::uint8_t> index_;  // their types
	std::vector<Type> types_;
	std::optional<Rule> rule_;

	const Type &type_at(std::int64_t t) const;
	Type rule_type(std::int64_t t) const;
	std::optional<std::int64_t> rule_change(std::int64_t t) const;
};

// Days since 1970-01-01 of a civil date, and back.
std::int64_t days_from_civil(int year, int month, int day);
void civil_from_days(std::int64_t days, int &year, int &month, int &day);

} // namespace nkos::tz
