#pragma once

// Times as the Go server writes them in JSON (time.Time's MarshalJSON, RFC 3339 with nanoseconds, trailing zeros of
// the fraction dropped, the local zone: "Z" or "+02:00"); the zero time is "0001-01-01T00:00:00Z".

#include <chrono>
#include <cstdio>
#include <ctime>
#include <string>

namespace nkos {

using WallClock = std::chrono::system_clock;

inline std::string rfc3339nano(WallClock::time_point t, bool utc = false)
{
	if (t == WallClock::time_point{}) return "0001-01-01T00:00:00Z";
	auto ns = std::chrono::duration_cast<std::chrono::nanoseconds>(t.time_since_epoch()).count();
	std::time_t secs = static_cast<std::time_t>(ns / 1'000'000'000);
	long frac = static_cast<long>(ns % 1'000'000'000);
	if (frac < 0) frac += 1'000'000'000, secs--;
	std::tm tm{};
	if (utc) gmtime_r(&secs, &tm);
	else localtime_r(&secs, &tm);
	char base[32];
	std::strftime(base, sizeof base, "%Y-%m-%dT%H:%M:%S", &tm);
	std::string out = base;
	if (frac) {
		char f[16];
		std::snprintf(f, sizeof f, ".%09ld", frac);
		std::string fs = f;
		while (fs.back() == '0') fs.pop_back();
		out += fs;
	}
	long off = utc ? 0 : tm.tm_gmtoff;
	if (off == 0) return out + "Z";
	char z[8];
	std::snprintf(z, sizeof z, "%c%02ld:%02ld", off < 0 ? '-' : '+', std::labs(off) / 3600, std::labs(off) % 3600 / 60);
	return out + z;
}

} // namespace nkos
