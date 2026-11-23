#include "common/tz.hpp"

#include <algorithm>
#include <cctype>
#include <fstream>
#include <iterator>

namespace nkos::tz {

std::int64_t days_from_civil(int y, int m, int d)
{
	y -= m <= 2;
	const std::int64_t era = (y >= 0 ? y : y - 399) / 400;
	const unsigned yoe = static_cast<unsigned>(y - era * 400);
	const unsigned doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
	const unsigned doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
	return era * 146097 + static_cast<std::int64_t>(doe) - 719468;
}

void civil_from_days(std::int64_t z, int &year, int &month, int &day)
{
	z += 719468;
	const std::int64_t era = (z >= 0 ? z : z - 146096) / 146097;
	const unsigned doe = static_cast<unsigned>(z - era * 146097);
	const unsigned yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
	const unsigned doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
	const unsigned mp = (5 * doy + 2) / 153;
	day = static_cast<int>(doy - (153 * mp + 2) / 5 + 1);
	month = static_cast<int>(mp < 10 ? mp + 3 : mp - 9);
	year = static_cast<int>(static_cast<std::int64_t>(yoe) + era * 400 + (month <= 2));
}

namespace {

std::int64_t floor_div(std::int64_t a, std::int64_t b) { return a / b - ((a % b != 0) && ((a < 0) != (b < 0))); }

bool leap(int y) { return (y % 4 == 0 && y % 100 != 0) || y % 400 == 0; }

std::uint32_t be32(const unsigned char *p) { return (std::uint32_t(p[0]) << 24) | (p[1] << 16) | (p[2] << 8) | p[3]; }
std::int64_t be64(const unsigned char *p)
{
	return static_cast<std::int64_t>((std::uint64_t(be32(p)) << 32) | be32(p + 4));
}

// The POSIX TZ string parser: std offset [dst [offset] [,start[/time],end[/time]]].
struct Reader {
	std::string_view s;
	std::size_t p = 0;

	bool name(std::string &out)
	{
		if (p < s.size() && s[p] == '<') {
			auto end = s.find('>', p);
			if (end == std::string_view::npos) return false;
			out = std::string(s.substr(p + 1, end - p - 1));
			p = end + 1;
			return out.size() >= 3;
		}
		std::size_t start = p;
		while (p < s.size() && std::isalpha(static_cast<unsigned char>(s[p]))) p++;
		out = std::string(s.substr(start, p - start));
		return out.size() >= 3;
	}
	bool number(int &out)
	{
		std::size_t start = p;
		out = 0;
		while (p < s.size() && std::isdigit(static_cast<unsigned char>(s[p]))) out = out * 10 + (s[p++] - '0');
		return p > start;
	}
	// [+-]hh[:mm[:ss]] in seconds.
	bool hms(int &out)
	{
		int sign = 1;
		if (p < s.size() && (s[p] == '+' || s[p] == '-')) sign = s[p++] == '-' ? -1 : 1;
		int h = 0, m = 0, sec = 0;
		if (!number(h)) return false;
		if (p < s.size() && s[p] == ':') {
			p++;
			if (!number(m)) return false;
			if (p < s.size() && s[p] == ':') {
				p++;
				if (!number(sec)) return false;
			}
		}
		out = sign * (h * 3600 + m * 60 + sec);
		return true;
	}
	bool when(Zone::Rule::When &w)
	{
		if (p < s.size() && s[p] == 'J') {
			p++;
			w.kind = 'J';
			if (!number(w.day)) return false;
		} else if (p < s.size() && s[p] == 'M') {
			p++;
			w.kind = 'M';
			if (!number(w.month) || p >= s.size() || s[p++] != '.' || !number(w.week) || p >= s.size() || s[p++] != '.' ||
			    !number(w.day))
				return false;
		} else {
			w.kind = 'D';
			if (!number(w.day)) return false;
		}
		w.time = 7200;
		if (p < s.size() && s[p] == '/') {
			p++;
			if (!hms(w.time)) return false;
		}
		return true;
	}
};

std::optional<Zone::Rule> parse_rule(std::string_view text)
{
	Reader r{text};
	Zone::Rule rule;
	int offset = 0;
	if (!r.name(rule.std.abbreviation) || !r.hms(offset)) return std::nullopt;
	rule.std.offset = -offset;
	if (r.p == text.size()) return rule;
	if (!r.name(rule.dst.abbreviation)) return std::nullopt;
	rule.has_dst = true;
	rule.dst.dst = true;
	rule.dst.offset = rule.std.offset + 3600;
	if (r.p < text.size() && text[r.p] != ',') {
		if (!r.hms(offset)) return std::nullopt;
		rule.dst.offset = -offset;
	}
	if (r.p == text.size()) {
		// No rule: the US rules of POSIX's default (M3.2.0,M11.1.0).
		rule.start = {'M', 3, 2, 0, 7200};
		rule.end = {'M', 11, 1, 0, 7200};
		return rule;
	}
	if (text[r.p++] != ',' || !r.when(rule.start) || r.p >= text.size() || text[r.p++] != ',' || !r.when(rule.end) ||
	    r.p != text.size())
		return std::nullopt;
	return rule;
}

// Seconds from the start of year to the local time of w.
std::int64_t when_in_year(int year, const Zone::Rule::When &w)
{
	std::int64_t day = 0;  // day of the year, 0-based
	if (w.kind == 'J') {
		day = w.day - 1;
		if (leap(year) && w.day >= 60) day++;
	} else if (w.kind == 'D') {
		day = w.day;
	} else {
		std::int64_t first = days_from_civil(year, w.month, 1);
		int first_weekday = static_cast<int>(((first % 7) + 11) % 7);  // 1970-01-01 was a Thursday
		int d = (w.day - first_weekday + 7) % 7;  // first such weekday of the month
		static const int month_days[] = {31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31};
		int length = month_days[w.month - 1] + (w.month == 2 && leap(year));
		d += 7 * (w.week - 1);
		while (d >= length) d -= 7;
		day = first - days_from_civil(year, 1, 1) + d;
	}
	return day * 86400 + w.time;
}

} // namespace

Zone Zone::utc()
{
	Zone z;
	z.types_.push_back(Type{0, false, "UTC"});
	return z;
}

Zone Zone::parse(std::string_view data)
{
	auto bytes = reinterpret_cast<const unsigned char *>(data.data());
	std::size_t size = data.size();
	auto header = [&](std::size_t at, std::uint32_t c[6]) {
		if (at + 44 > size || data.compare(at, 4, "TZif") != 0) return false;
		for (int i = 0; i < 6; i++) c[i] = be32(bytes + at + 20 + 4 * i);
		return true;
	};
	std::uint32_t c[6];  // isutcnt, isstdcnt, leapcnt, timecnt, typecnt, charcnt
	if (!header(0, c)) return utc();
	char version = data[4];
	std::size_t at = 44;
	int time_size = 4;
	if (version >= '2') {
		// Skip the 32-bit block to the 64-bit one.
		at += std::size_t(c[3]) * 4 + c[3] + std::size_t(c[4]) * 6 + c[5] + std::size_t(c[2]) * 8 + c[1] + c[0];
		if (!header(at, c)) return utc();
		at += 44;
		time_size = 8;
	}
	std::uint32_t timecnt = c[3], typecnt = c[4], charcnt = c[5];
	std::size_t need = std::size_t(timecnt) * time_size + timecnt + std::size_t(typecnt) * 6 + charcnt;
	if (typecnt == 0 || at + need > size) return utc();
	Zone z;
	for (std::uint32_t i = 0; i < timecnt; i++)
		z.times_.push_back(time_size == 8 ? be64(bytes + at + 8 * i) : static_cast<std::int32_t>(be32(bytes + at + 4 * i)));
	at += std::size_t(timecnt) * time_size;
	for (std::uint32_t i = 0; i < timecnt; i++) z.index_.push_back(std::min<std::uint8_t>(bytes[at + i], typecnt - 1));
	at += timecnt;
	std::size_t chars = at + std::size_t(typecnt) * 6;
	for (std::uint32_t i = 0; i < typecnt; i++) {
		const unsigned char *t = bytes + at + 6 * i;
		Type type;
		type.offset = static_cast<std::int32_t>(be32(t));
		type.dst = t[4] != 0;
		std::size_t a = t[5];
		if (a < charcnt) {
			std::string_view abbr = data.substr(chars + a, charcnt - a);
			type.abbreviation = std::string(abbr.substr(0, abbr.find('\0')));
		}
		z.types_.push_back(type);
	}
	if (version >= '2') {
		std::size_t footer = at + std::size_t(typecnt) * 6 + charcnt + std::size_t(c[2]) * 12 + c[1] + c[0];
		if (footer < size && data[footer] == '\n') {
			auto end = data.find('\n', footer + 1);
			if (end != std::string_view::npos && end > footer + 1) z.rule_ = parse_rule(data.substr(footer + 1, end - footer - 1));
		}
	}
	return z;
}

Zone Zone::device()
{
	std::ifstream f("/etc/localtime", std::ios::binary);
	if (!f) return utc();
	std::string data((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
	return parse(data);
}

Zone::Type Zone::rule_type(std::int64_t t) const
{
	const Rule &r = *rule_;
	if (!r.has_dst) return r.std;
	int year, month, day;
	civil_from_days(floor_div(t + r.std.offset, 86400), year, month, day);
	// Daylight time from start (local standard time) to end (local daylight time) of t's year; a start after the
	// end (southern hemisphere) means daylight time at both ends of the year.
	std::int64_t year_start = days_from_civil(year, 1, 1) * 86400;
	std::int64_t start = year_start + when_in_year(year, r.start) - r.std.offset;
	std::int64_t end = year_start + when_in_year(year, r.end) - r.dst.offset;
	bool in_dst = start < end ? (t >= start && t < end) : !(t >= end && t < start);
	return in_dst ? r.dst : r.std;
}

std::optional<std::int64_t> Zone::rule_change(std::int64_t t) const
{
	const Rule &r = *rule_;
	if (!r.has_dst) return std::nullopt;
	int year, month, day;
	civil_from_days(floor_div(t + r.std.offset, 86400), year, month, day);
	std::optional<std::int64_t> best;
	for (int y = year - 1; y <= year + 1; y++) {
		std::int64_t year_start = days_from_civil(y, 1, 1) * 86400;
		for (std::int64_t c : {year_start + when_in_year(y, r.start) - r.std.offset,
		                       year_start + when_in_year(y, r.end) - r.dst.offset})
			if (c > t && (!best || c < *best)) best = c;
	}
	return best;
}

const Zone::Type &Zone::type_at(std::int64_t t) const
{
	if (times_.empty() || t < times_.front()) {
		// Before the first transition: the first standard-time type (RFC 8536 uses type 0).
		return types_.front();
	}
	auto it = std::upper_bound(times_.begin(), times_.end(), t);
	return types_[index_[static_cast<std::size_t>(it - times_.begin()) - 1]];
}

Local Zone::local(std::int64_t t) const
{
	Type type = rule_ && (times_.empty() || t >= times_.back()) ? rule_type(t) : type_at(t);
	Local l;
	l.offset = type.offset;
	l.dst = type.dst;
	l.abbreviation = type.abbreviation;
	std::int64_t local = t + type.offset;
	std::int64_t days = floor_div(local, 86400);
	std::int64_t secs = local - days * 86400;
	civil_from_days(days, l.year, l.month, l.day);
	l.hour = static_cast<int>(secs / 3600);
	l.minute = static_cast<int>(secs / 60 % 60);
	l.second = static_cast<int>(secs % 60);
	l.weekday = static_cast<int>(((days % 7) + 11) % 7);
	return l;
}

std::optional<std::int64_t> Zone::next_change(std::int64_t t) const
{
	auto it = std::upper_bound(times_.begin(), times_.end(), t);
	if (it != times_.end()) return *it;
	if (rule_) return rule_change(t);
	return std::nullopt;
}

} // namespace nkos::tz
