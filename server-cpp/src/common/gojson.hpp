#pragma once

// A JSON string as Go's encoding/json writes it: <, > and & as <, >, &, U+2028 and U+2029 escaped,
// invalid UTF-8 as U+FFFD. For files the Go server also writes, so that both write the same bytes.

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
			case '<': case '>': case '&': u(c); break;
			default:
				if (c < 0x20) u(c);
				else out += static_cast<char>(c);
			}
			i++;
			continue;
		}
		// A UTF-8 sequence: its length and whether it is valid (shortest form, no surrogates, at most U+10FFFF).
		int n = c >= 0xf0 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 ? 2 : 0;
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
			out += "\\ufffd";
			i++;
			continue;
		}
		if (cp == 0x2028 || cp == 0x2029) out += cp == 0x2028 ? "\\u2028" : "\\u2029";
		else out.append(s.substr(i, n));
		i += n;
	}
	return out + "\"";
}

} // namespace nkos::gojson
