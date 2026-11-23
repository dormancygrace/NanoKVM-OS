#pragma once

// Go's strings functions as the update and recovery code uses them on package lists, apk output and settings files:
// strings.Fields and strings.TrimSpace split and trim on Unicode white space (unicode.IsSpace), not only ASCII;
// strings.Split keeps empty parts.

#include <string>
#include <string_view>
#include <vector>

namespace nkos::gostr {

// The length of the Unicode white space rune at s[i] (unicode.IsSpace), or 0.
inline std::size_t space_at(std::string_view s, std::size_t i)
{
	unsigned char c = static_cast<unsigned char>(s[i]);
	if (c == ' ' || (c >= '\t' && c <= '\r')) return 1;
	if (c < 0x80) return 0;
	auto is = [&](std::string_view seq) { return s.substr(i, seq.size()) == seq; };
	if (is("\xc2\x85") || is("\xc2\xa0")) return 2;  // U+0085, U+00A0
	if (is("\xe1\x9a\x80")) return 3;                  // U+1680
	if (c == 0xe2 && i + 2 < s.size()) {
		unsigned char b1 = static_cast<unsigned char>(s[i + 1]), b2 = static_cast<unsigned char>(s[i + 2]);
		if (b1 == 0x80 && ((b2 >= 0x80 && b2 <= 0x8a) || b2 == 0xa8 || b2 == 0xa9 || b2 == 0xaf)) return 3;  // U+2000-200A, 2028, 2029, 202F
		if (b1 == 0x81 && b2 == 0x9f) return 3;  // U+205F
	}
	if (is("\xe3\x80\x80")) return 3;  // U+3000
	return 0;
}

// The length of the rune at s[i] for stepping (invalid bytes are one byte each).
inline std::size_t rune_length(std::string_view s, std::size_t i)
{
	unsigned char c = static_cast<unsigned char>(s[i]);
	std::size_t n = c < 0x80 ? 1 : c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 1;
	if (i + n > s.size()) return 1;
	for (std::size_t k = 1; k < n; k++)
		if ((static_cast<unsigned char>(s[i + k]) & 0xc0) != 0x80) return 1;
	return n;
}

inline std::vector<std::string> fields(std::string_view s)
{
	std::vector<std::string> out;
	std::size_t i = 0, start = std::string_view::npos;
	while (i < s.size()) {
		std::size_t sp = space_at(s, i);
		if (sp) {
			if (start != std::string_view::npos) out.emplace_back(s.substr(start, i - start)), start = std::string_view::npos;
			i += sp;
		} else {
			if (start == std::string_view::npos) start = i;
			i += rune_length(s, i);
		}
	}
	if (start != std::string_view::npos) out.emplace_back(s.substr(start));
	return out;
}

inline std::string trim_space(std::string_view s)
{
	std::size_t i = 0;
	while (i < s.size()) {
		std::size_t sp = space_at(s, i);
		if (!sp) break;
		i += sp;
	}
	std::size_t end = s.size();
	// Trailing: walk back over complete space runes.
	for (;;) {
		bool trimmed = false;
		for (std::size_t n : {1, 2, 3}) {
			if (end >= i + n && space_at(s, end - n) == n) {
				end -= n;
				trimmed = true;
				break;
			}
		}
		if (!trimmed) break;
	}
	return std::string(s.substr(i, end - i));
}

inline std::vector<std::string> split(std::string_view s, std::string_view sep)
{
	std::vector<std::string> out;
	std::size_t start = 0;
	for (;;) {
		std::size_t at = s.find(sep, start);
		if (at == std::string_view::npos) break;
		out.emplace_back(s.substr(start, at - start));
		start = at + sep.size();
	}
	out.emplace_back(s.substr(start));
	return out;
}

inline std::string join(const std::vector<std::string> &parts, std::string_view sep)
{
	std::string out;
	for (std::size_t i = 0; i < parts.size(); i++) {
		if (i) out += sep;
		out += parts[i];
	}
	return out;
}

// strings.FieldsFunc(s, func(r) bool { return strings.ContainsRune(set, r) }) for an ASCII set.
inline std::vector<std::string> fields_ascii(std::string_view s, std::string_view set)
{
	std::vector<std::string> out;
	std::size_t start = std::string_view::npos;
	for (std::size_t i = 0; i < s.size(); i++) {
		if (set.find(s[i]) != std::string_view::npos) {
			if (start != std::string_view::npos) out.emplace_back(s.substr(start, i - start)), start = std::string_view::npos;
		} else if (start == std::string_view::npos) {
			start = i;
		}
	}
	if (start != std::string_view::npos) out.emplace_back(s.substr(start));
	return out;
}

inline bool has_prefix(std::string_view s, std::string_view p) { return s.substr(0, p.size()) == p; }
inline bool has_suffix(std::string_view s, std::string_view p) { return s.size() >= p.size() && s.substr(s.size() - p.size()) == p; }
inline bool contains(std::string_view s, std::string_view p) { return s.find(p) != std::string_view::npos; }

// strings.ToLower as far as a search for an ASCII word can tell: ASCII letters, and the Kelvin sign (U+212A),
// whose lower case is "k".
inline std::string to_lower_ascii(std::string_view s)
{
	std::string out;
	for (std::size_t i = 0; i < s.size(); i++) {
		char c = s[i];
		if (s.substr(i, 3) == "\xe2\x84\xaa") {
			out += 'k';
			i += 2;
			continue;
		}
		out += c >= 'A' && c <= 'Z' ? static_cast<char>(c | 0x20) : c;
	}
	return out;
}

} // namespace nkos::gostr
