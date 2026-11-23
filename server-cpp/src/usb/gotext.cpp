#include "usb/gotext.hpp"

#include <algorithm>
#include <charconv>
#include <cmath>
#include <utility>

namespace nkos::usb::gotext {

namespace {

constexpr std::pair<char32_t, char32_t> print_ranges[] = {
#include "usb/unicode_print.inc"
};

constexpr char hex_digits[] = "0123456789abcdef";

} // namespace

char32_t decode_rune(std::string_view s, std::size_t &size)
{
	if (s.empty()) {
		size = 0;
		return 0xfffd;
	}
	auto b = [&](std::size_t i) { return static_cast<unsigned char>(s[i]); };
	unsigned char c = b(0);
	size = 1;
	if (c < 0x80) return c;
	// The second byte's range depends on the first (shortest form, no surrogates, at most U+10FFFF).
	unsigned char lo = 0x80, hi = 0xbf;
	int n;
	char32_t r;
	if (c >= 0xc2 && c <= 0xdf) n = 2, r = c & 0x1f;
	else if (c >= 0xe0 && c <= 0xef) {
		n = 3, r = c & 0x0f;
		if (c == 0xe0) lo = 0xa0;
		if (c == 0xed) hi = 0x9f;
	} else if (c >= 0xf0 && c <= 0xf4) {
		n = 4, r = c & 0x07;
		if (c == 0xf0) lo = 0x90;
		if (c == 0xf4) hi = 0x8f;
	} else {
		return 0xfffd;
	}
	if (s.size() < static_cast<std::size_t>(n) || b(1) < lo || b(1) > hi) return 0xfffd;
	r = (r << 6) | (b(1) & 0x3f);
	for (int i = 2; i < n; i++) {
		if (b(i) < 0x80 || b(i) > 0xbf) return 0xfffd;
		r = (r << 6) | (b(i) & 0x3f);
	}
	size = static_cast<std::size_t>(n);
	return r;
}

void append_rune(std::string &out, char32_t r)
{
	if (r > 0x10ffff || (r >= 0xd800 && r <= 0xdfff)) r = 0xfffd;
	if (r < 0x80) out += static_cast<char>(r);
	else if (r < 0x800) {
		out += static_cast<char>(0xc0 | (r >> 6));
		out += static_cast<char>(0x80 | (r & 0x3f));
	} else if (r < 0x10000) {
		out += static_cast<char>(0xe0 | (r >> 12));
		out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
		out += static_cast<char>(0x80 | (r & 0x3f));
	} else {
		out += static_cast<char>(0xf0 | (r >> 18));
		out += static_cast<char>(0x80 | ((r >> 12) & 0x3f));
		out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
		out += static_cast<char>(0x80 | (r & 0x3f));
	}
}

bool valid_utf8(std::string_view s)
{
	for (std::size_t i = 0; i < s.size();) {
		std::size_t n;
		if (decode_rune(s.substr(i), n) == 0xfffd && n == 1) return false;
		i += n;
	}
	return true;
}

std::size_t rune_count(std::string_view s)
{
	std::size_t count = 0;
	for (std::size_t i = 0; i < s.size(); count++) {
		std::size_t n;
		decode_rune(s.substr(i), n);
		i += n;
	}
	return count;
}

bool is_print(char32_t r)
{
	auto it = std::upper_bound(std::begin(print_ranges), std::end(print_ranges), r,
	                           [](char32_t v, const std::pair<char32_t, char32_t> &range) { return v < range.first; });
	return it != std::begin(print_ranges) && r <= std::prev(it)->second;
}

bool is_space(char32_t r)
{
	switch (r) {
	case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xa0: case 0x1680: case 0x2028:
	case 0x2029: case 0x202f: case 0x205f: case 0x3000:
		return true;
	}
	return r >= 0x2000 && r <= 0x200a;
}

std::string trim_space(std::string_view s)
{
	std::size_t start = 0;
	while (start < s.size()) {
		std::size_t n;
		char32_t r = decode_rune(s.substr(start), n);
		if (!is_space(r)) break;
		start += n;
	}
	std::size_t end = s.size();
	while (end > start) {
		// The last rune: back up to its first byte (at most four bytes), as utf8.DecodeLastRuneInString.
		std::size_t begin = end - 1;
		std::size_t limit = end >= 4 ? end - 4 : 0;
		while (begin > limit && begin > start && (static_cast<unsigned char>(s[begin]) & 0xc0) == 0x80) begin--;
		std::size_t n;
		char32_t r = decode_rune(s.substr(begin, end - begin), n);
		if (begin + n != end) r = 0xfffd, n = 1, begin = end - 1;
		if (!is_space(r)) break;
		end = begin;
	}
	return std::string(s.substr(start, end - start));
}

namespace {

std::string quote_with(std::string_view s, char q)
{
	std::string out(1, q);
	for (std::size_t i = 0; i < s.size();) {
		std::size_t width;
		char32_t r = decode_rune(s.substr(i), width);
		if (width == 1 && r == 0xfffd) {
			unsigned char c = static_cast<unsigned char>(s[i]);
			out += "\\x";
			out += hex_digits[c >> 4];
			out += hex_digits[c & 15];
			i++;
			continue;
		}
		i += width;
		if (r == static_cast<char32_t>(q) || r == '\\') {
			out += '\\';
			out += static_cast<char>(r);
			continue;
		}
		if (is_print(r)) {
			append_rune(out, r);
			continue;
		}
		switch (r) {
		case '\a': out += "\\a"; continue;
		case '\b': out += "\\b"; continue;
		case '\f': out += "\\f"; continue;
		case '\n': out += "\\n"; continue;
		case '\r': out += "\\r"; continue;
		case '\t': out += "\\t"; continue;
		case '\v': out += "\\v"; continue;
		}
		if (r < ' ' || r == 0x7f) {
			out += "\\x";
			out += hex_digits[r >> 4];
			out += hex_digits[r & 15];
		} else if (r < 0x10000) {
			out += "\\u";
			for (int shift = 12; shift >= 0; shift -= 4) out += hex_digits[(r >> shift) & 15];
		} else {
			out += "\\U";
			for (int shift = 28; shift >= 0; shift -= 4) out += hex_digits[(r >> shift) & 15];
		}
	}
	return out + q;
}

} // namespace

std::string quote(std::string_view s) { return quote_with(s, '"'); }

std::string quote_rune(char32_t r)
{
	if (r > 0x10ffff || (r >= 0xd800 && r <= 0xdfff)) r = 0xfffd;
	std::string s;
	append_rune(s, r);
	return quote_with(s, '\'');
}

std::string json_string(std::string_view s)
{
	std::string out = "\"";
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x80) {
			i++;
			switch (c) {
			case '\\': out += "\\\\"; continue;
			case '"': out += "\\\""; continue;
			case '\b': out += "\\b"; continue;
			case '\f': out += "\\f"; continue;
			case '\n': out += "\\n"; continue;
			case '\r': out += "\\r"; continue;
			case '\t': out += "\\t"; continue;
			}
			if (c < 0x20 || c == '<' || c == '>' || c == '&') {
				out += "\\u00";
				out += hex_digits[c >> 4];
				out += hex_digits[c & 15];
			} else {
				out += static_cast<char>(c);
			}
			continue;
		}
		std::size_t n;
		char32_t r = decode_rune(s.substr(i), n);
		// Go 1.27's encoder (json/v2 underneath) replaces invalid UTF-8 with U+FFFD.
		if (r == 0xfffd && n == 1) out += "\xef\xbf\xbd";
		else if (r == 0x2028 || r == 0x2029) out += r == 0x2028 ? "\\u2028" : "\\u2029";
		else out.append(s.substr(i, n));
		i += n;
	}
	return out + "\"";
}

std::string errno_text(int err)
{
	static const std::pair<int, const char *> table[] = {
		{1, "operation not permitted"}, {2, "no such file or directory"}, {3, "no such process"},
		{4, "interrupted system call"}, {5, "input/output error"}, {6, "no such device or address"},
		{7, "argument list too long"}, {8, "exec format error"}, {9, "bad file descriptor"}, {10, "no child processes"},
		{11, "resource temporarily unavailable"}, {12, "cannot allocate memory"}, {13, "permission denied"},
		{14, "bad address"}, {15, "block device required"}, {16, "device or resource busy"}, {17, "file exists"},
		{18, "invalid cross-device link"}, {19, "no such device"}, {20, "not a directory"}, {21, "is a directory"},
		{22, "invalid argument"}, {23, "too many open files in system"}, {24, "too many open files"},
		{25, "inappropriate ioctl for device"}, {26, "text file busy"}, {27, "file too large"},
		{28, "no space left on device"}, {29, "illegal seek"}, {30, "read-only file system"}, {31, "too many links"},
		{32, "broken pipe"}, {33, "numerical argument out of domain"}, {34, "numerical result out of range"},
		{35, "resource deadlock avoided"}, {36, "file name too long"}, {37, "no locks available"},
		{38, "function not implemented"}, {39, "directory not empty"}, {40, "too many levels of symbolic links"},
		{90, "message too long"}, {93, "protocol not supported"}, {95, "operation not supported"},
		{97, "address family not supported by protocol"}, {98, "address already in use"},
		{99, "cannot assign requested address"}, {100, "network is down"}, {101, "network is unreachable"},
		{103, "software caused connection abort"}, {104, "connection reset by peer"}, {105, "no buffer space available"},
		{107, "transport endpoint is not connected"}, {110, "connection timed out"}, {111, "connection refused"},
		{112, "host is down"}, {113, "no route to host"}, {116, "stale file handle"}, {121, "remote I/O error"},
		{122, "disk quota exceeded"}, {123, "no medium found"}, {125, "operation canceled"},
	};
	for (auto &[n, text] : table)
		if (n == err) return text;
	return "errno " + std::to_string(err);
}

std::string pad_right(std::string_view s, std::size_t width)
{
	std::size_t runes = rune_count(s);
	std::string out(s);
	if (runes < width) out.append(width - runes, ' ');
	return out;
}

std::string json_float(double f)
{
	char buf[64];
	double abs = std::fabs(f);
	bool exp = abs != 0 && (abs < 1e-6 || abs >= 1e21);
	auto r = std::to_chars(buf, buf + sizeof buf, f, exp ? std::chars_format::scientific : std::chars_format::fixed);
	std::string out(buf, r.ptr);
	if (exp) {
		std::size_t n = out.size();
		if (n >= 4 && out[n - 4] == 'e' && out[n - 3] == '-' && out[n - 2] == '0') out.erase(n - 2, 1);
	}
	return out;
}

} // namespace nkos::usb::gotext
