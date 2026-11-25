#pragma once

// Standard base64 with padding, as Go's base64.StdEncoding: decoding skips \r and \n and refuses anything else that
// is not of the alphabet, a missing or misplaced '=', and data after the padding.

#include <optional>
#include <string>
#include <string_view>

namespace nkos::base64 {

inline std::string encode(std::string_view data)
{
	static const char table[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	std::string out;
	out.reserve((data.size() + 2) / 3 * 4);
	std::size_t i = 0;
	for (; i + 3 <= data.size(); i += 3) {
		unsigned v = (static_cast<unsigned char>(data[i]) << 16) | (static_cast<unsigned char>(data[i + 1]) << 8) |
		             static_cast<unsigned char>(data[i + 2]);
		out += table[v >> 18];
		out += table[(v >> 12) & 63];
		out += table[(v >> 6) & 63];
		out += table[v & 63];
	}
	if (std::size_t rest = data.size() - i) {
		unsigned v = static_cast<unsigned char>(data[i]) << 16;
		if (rest == 2) v |= static_cast<unsigned char>(data[i + 1]) << 8;
		out += table[v >> 18];
		out += table[(v >> 12) & 63];
		out += rest == 2 ? table[(v >> 6) & 63] : '=';
		out += '=';
	}
	return out;
}

inline std::optional<std::string> decode(std::string_view text)
{
	auto value = [](char c) -> int {
		if (c >= 'A' && c <= 'Z') return c - 'A';
		if (c >= 'a' && c <= 'z') return c - 'a' + 26;
		if (c >= '0' && c <= '9') return c - '0' + 52;
		if (c == '+') return 62;
		if (c == '/') return 63;
		return -1;
	};
	std::string clean;
	clean.reserve(text.size());
	for (char c : text)
		if (c != '\r' && c != '\n') clean += c;
	if (clean.size() % 4) return std::nullopt;
	std::string out;
	out.reserve(clean.size() / 4 * 3);
	for (std::size_t i = 0; i < clean.size(); i += 4) {
		bool last = i + 4 == clean.size();
		int a = value(clean[i]), b = value(clean[i + 1]);
		if (a < 0 || b < 0) return std::nullopt;
		char c3 = clean[i + 2], c4 = clean[i + 3];
		if (c3 == '=') {
			if (!last || c4 != '=') return std::nullopt;
			out += static_cast<char>((a << 2) | (b >> 4));
			break;
		}
		int c = value(c3);
		if (c < 0) return std::nullopt;
		if (c4 == '=') {
			if (!last) return std::nullopt;
			out += static_cast<char>((a << 2) | (b >> 4));
			out += static_cast<char>(((b & 15) << 4) | (c >> 2));
			break;
		}
		int d = value(c4);
		if (d < 0) return std::nullopt;
		out += static_cast<char>((a << 2) | (b >> 4));
		out += static_cast<char>(((b & 15) << 4) | (c >> 2));
		out += static_cast<char>(((c & 3) << 6) | d);
	}
	return out;
}

} // namespace nkos::base64
