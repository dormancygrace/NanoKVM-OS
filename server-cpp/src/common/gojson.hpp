#pragma once

// A JSON string as Go 1.27's encoding/json (JSON v2) writes it: \b and \f as such, <, > and & as <, >, &, U+2028 and U+2029 escaped,
// invalid UTF-8 as a raw U+FFFD. For files the Go server also writes, so that both write the same bytes.

#include <charconv>
#include <cmath>
#include <string>
#include <string_view>

namespace nkos::gojson {

inline std::string quote(std::string_view s)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
	auto u = [&](unsigned c) {
		out += "\\u00";
		out += hex[c >> 4];
		out += hex[c & 15];
	};
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x80) {
			switch (c) {
			case '"': out += "\\\""; break;
			case '\\': out += "\\\\"; break;
			case '\n': out += "\\n"; break;
			case '\r': out += "\\r"; break;
			case '\t': out += "\\t"; break;
			case '\b': out += "\\b"; break;
			case '\f': out += "\\f"; break;
			case '<': case '>': case '&': u(c); break;
			default:
				if (c < 0x20) u(c);
				else out += static_cast<char>(c);
			}
			i++;
			continue;
		}
		// A UTF-8 sequence: its length and whether it is valid (shortest form, no surrogates, at most U+10FFFF).
		int n = c >= 0xf5 ? 0 : c >= 0xf0 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 ? 2 : 0;
		unsigned cp = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
		bool ok = n > 0 && i + n <= s.size();
		for (int k = 1; ok && k < n; k++) {
			unsigned char cc = static_cast<unsigned char>(s[i + k]);
			if ((cc & 0xc0) != 0x80) ok = false;
			cp = (cp << 6) | (cc & 0x3f);
		}
		if (ok && ((n == 3 && (cp < 0x800 || (cp >= 0xd800 && cp <= 0xdfff))) || (n == 4 && (cp < 0x10000 || cp > 0x10ffff))))
			ok = false;
		if (!ok) {
			out += "\xef\xbf\xbd";
			i++;
			continue;
		}
		if (cp == 0x2028 || cp == 0x2029) out += cp == 0x2028 ? "\\u2028" : "\\u2029";
		else out.append(s.substr(i, n));
		i += n;
	}
	return out + "\"";
}

// A float64 as encoding/json writes it: the shortest digits that read back the same; exponent form below 1e-6 and
// from 1e21 (e-07 written e-7), else plain decimal digits (1.2345678901234567e19 is 12345678901234567000).
inline std::string number(double f)
{
	char buf[64];
	double a = f < 0 ? -f : f;
	auto r = std::to_chars(buf, buf + sizeof buf, f, std::chars_format::scientific);
	std::string sci(buf, r.ptr);
	if (a != 0 && (a < 1e-6 || a >= 1e21)) {
		std::size_t n = sci.size();
		if (n >= 4 && sci[n - 4] == 'e' && sci[n - 3] == '-' && sci[n - 2] == '0') sci.erase(n - 2, 1);
		return sci;
	}
	if (a == 0) return std::signbit(f) ? "-0" : "0";
	// d.ddddde±XX -> the digits and the exponent.
	std::size_t e = sci.find('e');
	std::string mant = sci.substr(0, e);
	int exp = std::stoi(sci.substr(e + 1));
	bool neg = mant[0] == '-';
	if (neg) mant.erase(0, 1);
	std::string digits;
	for (char c : mant)
		if (c != '.') digits += c;
	std::string out;
	int point = exp + 1;  // digits before the decimal point
	if (point <= 0) {
		out = "0." + std::string(static_cast<std::size_t>(-point), '0') + digits;
	} else if (point >= static_cast<int>(digits.size())) {
		out = digits + std::string(static_cast<std::size_t>(point) - digits.size(), '0');
	} else {
		out = digits.substr(0, static_cast<std::size_t>(point)) + "." + digits.substr(static_cast<std::size_t>(point));
	}
	return neg ? "-" + out : out;
}

} // namespace nkos::gojson
