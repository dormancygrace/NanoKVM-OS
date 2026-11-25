#include "system/cron.hpp"

#include <cctype>
#include <sstream>
#include <strings.h>

namespace nkos::system::cron {

namespace {

const char *const months[] = {"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"};
const char *const weekdays[] = {"sun", "mon", "tue", "wed", "thu", "fri", "sat"};

// Fills mask as crond's ParseField does: comma lists of *, a number, a three-letter name, a range a-b (wrapping past
// the end, as 22-2 for hours), each with an optional step /n counted from its start. Numbers are digits only.
bool field(const std::string &text, bool *mask, int mod, int off, const char *const *names, int name_count)
{
	std::size_t p = 0;
	auto number = [&](int &v) {
		std::size_t start = p;
		long long value = 0;
		while (p < text.size() && std::isdigit(static_cast<unsigned char>(text[p]))) {
			if (value < 1000000000) value = value * 10 + (text[p] - '0');
			p++;
		}
		v = static_cast<int>(value);
		return p > start && value < 1000000000;
	};
	while (true) {
		int n1 = -1, n2 = -1;
		while (true) {
			if (p < text.size() && text[p] == '*') {
				n1 = 0;
				n2 = mod - 1;
				p++;
			} else if (p < text.size() && std::isdigit(static_cast<unsigned char>(text[p]))) {
				int v = 0;
				number(v);
				if (n1 < 0) n1 = v + off;
				else n2 = v + off;
			} else {
				bool found = false;
				for (int i = 0; i < name_count; i++) {
					if (text.size() - p >= 3 && strncasecmp(text.c_str() + p, names[i], 3) == 0) {
						p += 3;
						if (n1 < 0) n1 = i;
						else n2 = i;
						found = true;
						break;
					}
				}
				if (!found) return false;
			}
			if (p < text.size() && text[p] == '-' && n2 < 0) {
				p++;
				continue;
			}
			break;
		}
		if (n2 < 0) n2 = n1;
		int step = 1;
		if (p < text.size() && text[p] == '/') {
			p++;
			int v = 0;
			if (!number(v) || v < 1) return false;
			step = v;
		}
		if (n1 < 0 || n1 >= mod || n2 < 0 || n2 >= mod) return false;
		// From n1 to n2, wrapping, every step-th value from n1 on.
		for (int n = n1 - 1, s0 = 1;;) {
			n = (n + 1) % mod;
			if (--s0 == 0) {
				mask[n] = true;
				s0 = step;
			}
			if (n == n2) break;
		}
		if (p < text.size() && text[p] == ',') {
			p++;
			continue;
		}
		break;
	}
	return p == text.size();
}

template <std::size_t N>
bool all(const std::array<bool, N> &mask)
{
	for (bool v : mask)
		if (!v) return false;
	return true;
}

// Go's strconv.Quote for the ASCII a cron field is likely to hold; other bytes as \x escapes.
std::string quote(const std::string &s)
{
	std::string out = "\"";
	for (unsigned char c : s) {
		if (c == '"' || c == '\\') {
			out += '\\';
			out += static_cast<char>(c);
		} else if (c >= 0x20 && c < 0x7f) {
			out += static_cast<char>(c);
		} else {
			static const char hex[] = "0123456789abcdef";
			out += "\\x";
			out += hex[c >> 4];
			out += hex[c & 15];
		}
	}
	return out + "\"";
}

} // namespace

std::optional<Schedule> parse(const std::string &expr, std::string &error)
{
	std::istringstream in(expr);
	std::vector<std::string> fields;
	for (std::string f; in >> f;) fields.push_back(f);
	if (fields.size() != 5) {
		error = "a schedule has five fields: minute, hour, day of month, month, weekday";
		return std::nullopt;
	}
	Schedule s;
	struct F {
		const char *name;
		bool *mask;
		int mod, off;
		const char *const *names;
		int count;
	} spec[] = {
		{"minute", s.minute.data(), 60, 0, nullptr, 0},
		{"hour", s.hour.data(), 24, 0, nullptr, 0},
		{"day of month", s.dom.data(), 32, 0, nullptr, 0},
		{"month", s.month.data(), 12, -1, months, 12},
		{"weekday", s.dow.data(), 7, 0, weekdays, 7},
	};
	for (int i = 0; i < 5; i++) {
		if (!field(fields[i], spec[i].mask, spec[i].mod, spec[i].off, spec[i].names, spec[i].count)) {
			error = std::string(spec[i].name) + ": crond cannot read " + quote(fields[i]);
			return std::nullopt;
		}
	}
	// FixDayDow: a day field that selects everything gives way to a restricted other one.
	bool week_used = !all(s.dow), days_used = !all(s.dom);
	if (week_used != days_used) {
		if (week_used) s.dom.fill(false);
		else s.dow.fill(false);
	}
	return s;
}

std::vector<std::int64_t> Schedule::next(std::int64_t from, int count, const tz::Zone &zone) const
{
	std::vector<std::int64_t> runs;
	auto floor_minute = [](std::int64_t t) { return t / 60 * 60 - (t % 60 < 0 ? 60 : 0); };
	std::int64_t t = floor_minute(from) + 60;
	// from.AddDate(5, 0, 0) in the zone: the same local date and time five years on (Feb 29 rolls to Mar 1).
	tz::Local f = zone.local(from);
	int y = f.year + 5, m = f.month, d = f.day;
	std::int64_t days = tz::days_from_civil(y, m, 1) + d - 1;  // normalised as Go's Date does
	std::int64_t local_limit = days * 86400 + f.hour * 3600 + f.minute * 60 + f.second;
	std::int64_t limit = local_limit - f.offset;
	limit -= zone.local(limit).offset - f.offset;
	while (static_cast<int>(runs.size()) < count && t < limit) {
		tz::Local l = zone.local(t);
		if (!month[l.month - 1] || (!dom[l.day] && !dow[l.weekday]) || !hour[l.hour]) {
			std::int64_t step = t + (60 - l.minute) * 60;
			if (auto change = zone.next_change(t); change && *change > t && *change < step) step = *change;
			t = step;
			continue;
		}
		if (minute[l.minute]) runs.push_back(t);
		t += 60;
	}
	return runs;
}

} // namespace nkos::system::cron
