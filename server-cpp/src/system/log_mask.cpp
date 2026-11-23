#include "system/log_mask.hpp"

#include "system/log_sanitize.hpp"

#include <array>
#include <cstdint>
#include <optional>

namespace nkos::system::logs {

namespace {

constexpr std::size_t npos = std::string_view::npos;

bool word(unsigned char c) { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'; }
bool digit(unsigned char c) { return c >= '0' && c <= '9'; }
bool hex(unsigned char c) { return digit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'); }
bool space(unsigned char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'; }  // RE2 \s

// RE2's \b: ASCII word characters on one side only.
bool boundary(std::string_view s, std::size_t i)
{
	bool before = i > 0 && word(static_cast<unsigned char>(s[i - 1]));
	bool after = i < s.size() && word(static_cast<unsigned char>(s[i]));
	return before != after;
}

std::size_t skip_space(std::string_view s, std::size_t i)
{
	while (i < s.size() && space(static_cast<unsigned char>(s[i]))) i++;
	return i;
}

// A lower-case ASCII literal under (?i) at i: its end, or npos. RE2 folds s with U+017F and k with U+212A.
std::size_t fold_literal(std::string_view s, std::size_t i, std::string_view lit)
{
	for (char p : lit) {
		if (i >= s.size()) return npos;
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c == static_cast<unsigned char>(p) || (p >= 'a' && p <= 'z' && c == static_cast<unsigned char>(p - 32))) {
			i++;
		} else if (p == 's' && s.substr(i, 2) == "\xc5\xbf") {
			i += 2;
		} else if (p == 'k' && s.substr(i, 3) == "\xe2\x84\xaa") {
			i += 3;
		} else {
			return npos;
		}
	}
	return i;
}

// (?i)\b[0-9a-f]{2}(?:[:-][0-9a-f]{2}){5}\b at i: its end, or npos.
std::size_t mac_at(std::string_view s, std::size_t i)
{
	if (!boundary(s, i) || i + 17 > s.size()) return npos;
	for (std::size_t k = 0; k < 17; k++) {
		unsigned char c = static_cast<unsigned char>(s[i + k]);
		bool ok = k % 3 == 2 ? (c == ':' || c == '-') : hex(c);
		if (!ok) return npos;
	}
	return boundary(s, i + 17) ? i + 17 : npos;
}

// The ends of (?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9]) at i, in RE2's order of preference, without repeats.
int octet_ends(std::string_view s, std::size_t i, std::size_t (&ends)[6])
{
	auto at = [&](std::size_t k) -> unsigned char { return i + k < s.size() ? static_cast<unsigned char>(s[i + k]) : 0; };
	int n = 0;
	auto add = [&](std::size_t e) {
		for (int k = 0; k < n; k++)
			if (ends[k] == e) return;
		ends[n++] = e;
	};
	if (at(0) == '2' && at(1) == '5' && at(2) >= '0' && at(2) <= '5') add(i + 3);
	if (at(0) == '2' && at(1) >= '0' && at(1) <= '4' && digit(at(2))) add(i + 3);
	if (at(0) == '1') {
		if (digit(at(1)) && digit(at(2))) add(i + 3);
		if (digit(at(1))) add(i + 2);
	}
	if (digit(at(0)) && digit(at(1))) add(i + 2);
	if (digit(at(0))) add(i + 1);
	return n;
}

std::size_t ipv4_from(std::string_view s, std::size_t i, int octet)
{
	std::size_t ends[6];
	int n = octet_ends(s, i, ends);
	for (int k = 0; k < n; k++) {
		std::size_t e = ends[k];
		if (octet == 3) {
			if (boundary(s, e)) return e;
		} else if (e < s.size() && s[e] == '.') {
			if (auto r = ipv4_from(s, e + 1, octet + 1); r != npos) return r;
		}
	}
	return npos;
}

// \b<octet>(?:\.<octet>){3}\b at i.
std::size_t ipv4_at(std::string_view s, std::size_t i)
{
	if (!boundary(s, i)) return npos;
	return ipv4_from(s, i, 0);
}

// (?i)(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}(?:%[a-z0-9]+)? at i.
std::size_t ipv6_candidate_at(std::string_view s, std::size_t i)
{
	auto hex_run = [&](std::size_t p) {
		std::size_t q = p;
		while (q < s.size() && q - p < 5 && hex(static_cast<unsigned char>(s[q]))) q++;
		return q - p;
	};
	std::size_t p = i;
	int groups = 0;
	while (groups < 7) {
		std::size_t run = hex_run(p);
		if (run > 4 || p + run >= s.size() || s[p + run] != ':') break;
		p += run + 1;
		groups++;
	}
	if (groups < 2) return npos;
	p += std::min<std::size_t>(hex_run(p), 4);
	if (p < s.size() && s[p] == '%') {
		std::size_t q = p + 1;
		for (;;) {
			unsigned char c = q < s.size() ? static_cast<unsigned char>(s[q]) : 0;
			if (digit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) q++;
			else if (s.substr(q, 2) == "\xc5\xbf") q += 2;
			else if (s.substr(q, 3) == "\xe2\x84\xaa") q += 3;
			else break;
		}
		if (q > p + 1) p = q;
	}
	return p;
}

// netip.ParseAddr for a text of hex digits and colons (parseIPv6 without a zone or an embedded IPv4 address).
std::optional<std::array<std::uint8_t, 16>> parse_ipv6(std::string_view s)
{
	std::array<std::uint8_t, 16> ip{};
	int ellipsis = -1;
	if (s.size() >= 2 && s[0] == ':' && s[1] == ':') {
		ellipsis = 0;
		s.remove_prefix(2);
		if (s.empty()) return ip;
	}
	int i = 0;
	while (i < 16) {
		std::size_t off = 0;
		std::uint32_t acc = 0;
		for (; off < s.size(); off++) {
			unsigned char c = static_cast<unsigned char>(s[off]);
			if (digit(c)) acc = (acc << 4) + (c - '0');
			else if (c >= 'a' && c <= 'f') acc = (acc << 4) + (c - 'a' + 10);
			else if (c >= 'A' && c <= 'F') acc = (acc << 4) + (c - 'A' + 10);
			else break;
			if (off > 3 || acc > 0xffff) return std::nullopt;
		}
		if (off == 0) return std::nullopt;
		ip[i] = static_cast<std::uint8_t>(acc >> 8);
		ip[i + 1] = static_cast<std::uint8_t>(acc);
		i += 2;
		s.remove_prefix(off);
		if (s.empty()) break;
		if (s[0] != ':' || s.size() == 1) return std::nullopt;
		s.remove_prefix(1);
		if (s[0] == ':') {
			if (ellipsis >= 0) return std::nullopt;
			ellipsis = i;
			s.remove_prefix(1);
			if (s.empty()) break;
		}
	}
	if (!s.empty()) return std::nullopt;
	if (i < 16) {
		if (ellipsis < 0) return std::nullopt;
		int n = 16 - i;
		for (int j = i - 1; j >= ellipsis; j--) ip[j + n] = ip[j];
		for (int j = ellipsis; j < ellipsis + n; j++) ip[j] = 0;
	} else if (ellipsis >= 0) {
		return std::nullopt;
	}
	return ip;
}

std::string mask_ipv6(std::string_view candidate)
{
	std::string_view bare = candidate.substr(0, candidate.find('%'));
	auto ip = parse_ipv6(bare);
	if (!ip) return std::string(candidate);
	bool zero = true;
	for (int k = 0; k < 15; k++)
		if ((*ip)[k]) zero = false;
	// Loopback (::1) or unspecified (::).
	if (zero && ((*ip)[15] == 0 || (*ip)[15] == 1)) return std::string(candidate);
	return "<ip6>";
}

// ReplaceAllStringFunc for a pattern without empty matches: at(s, i) is the end of a match at i, or npos.
template <typename At, typename Replace>
std::string replace_all(std::string_view s, At at, Replace replace)
{
	std::string out;
	out.reserve(s.size());
	std::size_t copied = 0;
	for (std::size_t i = 0; i < s.size();) {
		std::size_t end = at(s, i);
		if (end == npos) {
			i++;
			continue;
		}
		out.append(s.substr(copied, i - copied));
		out += replace(s.substr(i, end - i));
		copied = i = end;
	}
	out.append(s.substr(copied));
	return out;
}

// (connect to of ).*(\(\d+\)) -> ${1}<ssid>${2}: the name runs to the last "(length)" of the line.
std::string mask_aic(std::string_view s)
{
	static constexpr std::string_view prefix = "connect to of ";
	std::string out;
	std::size_t copied = 0, from = 0;
	for (;;) {
		std::size_t a = s.find(prefix, from);
		if (a == npos) break;
		std::size_t body = a + prefix.size();
		std::size_t line_end = s.find('\n', body);
		if (line_end == npos) line_end = s.size();
		std::size_t open = npos, close = npos;
		for (std::size_t j = line_end; j-- > body;) {
			if (s[j] != '(') continue;
			std::size_t k = j + 1;
			while (k < line_end && digit(static_cast<unsigned char>(s[k]))) k++;
			if (k > j + 1 && k < line_end && s[k] == ')') {
				open = j, close = k;
				break;
			}
		}
		if (open == npos) {
			from = a + 1;
			continue;
		}
		out.append(s.substr(copied, a - copied));
		out += prefix;
		out += "<ssid>";
		out.append(s.substr(open, close + 1 - open));
		copied = from = close + 1;
	}
	out.append(s.substr(copied));
	return out;
}

// (?im)^(\s*(?:ssid|essid)(?:\s*[:=]\s*|\s+)).*$ -> ${1}<ssid>
std::string mask_ssid_lines(std::string_view s)
{
	std::string out;
	std::size_t copied = 0;
	for (std::size_t p = 0; p <= s.size();) {
		std::size_t next = s.find('\n', p);
		std::size_t q = skip_space(s, p);
		std::size_t w = fold_literal(s, q, "ssid");
		if (w == npos) w = fold_literal(s, q, "essid");
		std::size_t value = npos;
		if (w != npos) {
			std::size_t a = skip_space(s, w);
			if (a < s.size() && (s[a] == ':' || s[a] == '=')) value = skip_space(s, a + 1);
			else if (a > w) value = a;
		}
		if (value != npos) {
			std::size_t end = s.find('\n', value);
			if (end == npos) end = s.size();
			out.append(s.substr(copied, value - copied));
			out += "<ssid>";
			copied = end;
			if (end == s.size()) break;
			p = end + 1;
			continue;
		}
		if (next == npos) break;
		p = next + 1;
	}
	out.append(s.substr(copied));
	return out;
}

// (?i)\b(e?ssid)(\s*[:=]\s*|\s+)('[^'\n]*'|"[^"\n]*"|[^\s,;\]\)]+) -> ${1}${2}<ssid>
std::size_t ssid_value_end(std::string_view s, std::size_t b)
{
	if (b >= s.size()) return npos;
	for (char q : {'\'', '"'}) {
		if (s[b] != q) continue;
		std::size_t k = b + 1;
		while (k < s.size() && s[k] != q && s[k] != '\n') k++;
		if (k < s.size() && s[k] == q) return k + 1;
	}
	std::size_t k = b;
	while (k < s.size()) {
		unsigned char c = static_cast<unsigned char>(s[k]);
		if (space(c) || c == ',' || c == ';' || c == ']' || c == ')') break;
		k++;
	}
	return k > b ? k : npos;
}

std::string mask_ssid_inline(std::string_view s)
{
	std::string out;
	std::size_t copied = 0;
	for (std::size_t p = 0; p < s.size();) {
		std::size_t w = npos;
		if (boundary(s, p)) {
			w = fold_literal(s, p, "essid");
			if (w == npos) w = fold_literal(s, p, "ssid");
		}
		std::size_t value = npos, end = npos;
		if (w != npos) {
			std::size_t a = skip_space(s, w);
			if (a < s.size() && (s[a] == ':' || s[a] == '=')) {
				std::size_t b = skip_space(s, a + 1);
				if ((end = ssid_value_end(s, b)) != npos) value = b;
			}
			if (value == npos && a > w && (end = ssid_value_end(s, a)) != npos) value = a;
		}
		if (value == npos) {
			p++;
			continue;
		}
		out.append(s.substr(copied, value - copied));
		out += "<ssid>";
		copied = p = end;
	}
	out.append(s.substr(copied));
	return out;
}

} // namespace

std::string mask_addresses(std::string_view text)
{
	std::string s = replace_all(text, mac_at, [](std::string_view) { return std::string("<mac>"); });
	s = replace_all(s, ipv4_at, [](std::string_view ip) {
		if (ip.starts_with("127.") || ip.starts_with("255.") || ip == "0.0.0.0") return std::string(ip);
		return std::string("<ip>");
	});
	s = replace_all(s, ipv6_candidate_at, mask_ipv6);
	s = mask_aic(s);
	s = mask_ssid_lines(s);
	return mask_ssid_inline(s);
}

std::string sanitize_file(std::string_view data)
{
	std::string owned;
	if (!data.empty() && data.back() != '\n') {
		owned.reserve(data.size() + 1);
		owned.append(data);
		owned += '\n';
		data = owned;
	}
	auto clean = sanitize_tail(data, false);
	if (clean.content.empty()) return "";
	return mask_addresses(clean.content) + "\n";
}

} // namespace nkos::system::logs
