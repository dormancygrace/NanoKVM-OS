#include "notify/gostd.hpp"

#include "common/base64.hpp"

#include <algorithm>
#include <arpa/inet.h>
#include <cstdio>
#include <ctime>
#include <format>
#include <sys/random.h>

namespace nkos::notify::gostd {

namespace {

struct Range {
	char32_t lo, hi;
};
struct LowerRange {
	char32_t lo, hi;
	int step, delta;
};
#include "notify/unicode_tables.inc"

bool is_print(char32_t r)
{
	auto it = std::upper_bound(std::begin(print_ranges), std::end(print_ranges), r,
	                           [](char32_t v, const Range &x) { return v < x.lo; });
	if (it == std::begin(print_ranges)) return false;
	--it;
	return r <= it->hi;
}

char32_t to_lower_rune(char32_t r)
{
	if (r < 0x80) return r >= 'A' && r <= 'Z' ? r + 32 : r;
	auto it = std::upper_bound(std::begin(lower_ranges), std::end(lower_ranges), r,
	                           [](char32_t v, const LowerRange &x) { return v < x.lo; });
	if (it == std::begin(lower_ranges)) return r;
	--it;
	int step = it->step ? it->step : 1;
	if (r > it->hi || (r - it->lo) % step) return r;
	return static_cast<char32_t>(static_cast<int>(r) + it->delta);
}

constexpr char lowerhex[] = "0123456789abcdef";
constexpr char upperhex[] = "0123456789ABCDEF";

} // namespace

std::pair<char32_t, std::size_t> decode_rune(std::string_view s, std::size_t i)
{
	unsigned char c = static_cast<unsigned char>(s[i]);
	if (c < 0x80) return {c, 1};
	int n = c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 0;
	if (n == 0 || i + n > s.size()) return {0xFFFD, 1};
	char32_t r = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
	for (int k = 1; k < n; k++) {
		unsigned char cc = static_cast<unsigned char>(s[i + k]);
		if ((cc & 0xc0) != 0x80) return {0xFFFD, 1};
		r = (r << 6) | (cc & 0x3f);
	}
	if ((n == 3 && (r < 0x800 || (r >= 0xd800 && r <= 0xdfff))) || (n == 4 && (r < 0x10000 || r > 0x10ffff)))
		return {0xFFFD, 1};
	return {r, static_cast<std::size_t>(n)};
}

void append_rune(std::string &out, char32_t r)
{
	if (r > 0x10ffff || (r >= 0xd800 && r <= 0xdfff)) r = 0xFFFD;
	if (r < 0x80) {
		out += static_cast<char>(r);
	} else if (r < 0x800) {
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

std::size_t rune_count(std::string_view s)
{
	std::size_t n = 0;
	for (std::size_t i = 0; i < s.size(); i += decode_rune(s, i).second) n++;
	return n;
}

bool is_space(char32_t r)
{
	switch (r) {
	case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xA0: case 0x1680: case 0x2028:
	case 0x2029: case 0x202F: case 0x205F: case 0x3000:
		return true;
	}
	return r >= 0x2000 && r <= 0x200A;
}

std::string trim_space(std::string_view s)
{
	std::size_t start = 0;
	while (start < s.size()) {
		auto [r, n] = decode_rune(s, start);
		if (!is_space(r)) break;
		start += n;
	}
	std::size_t end = s.size();
	while (end > start) {
		// The last rune: back to its first byte.
		std::size_t b = end - 1;
		while (b > start && (static_cast<unsigned char>(s[b]) & 0xc0) == 0x80 && end - b < 4) b--;
		auto [r, n] = decode_rune(s, b);
		if (b + n != end) {
			// Not a whole rune: the last byte alone is invalid, not a space.
			break;
		}
		if (!is_space(r)) break;
		end = b;
	}
	return std::string(s.substr(start, end - start));
}

std::vector<std::string> fields(std::string_view s)
{
	std::vector<std::string> out;
	std::string cur;
	bool in = false;
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		if (is_space(r)) {
			if (in) out.push_back(std::move(cur)), cur.clear();
			in = false;
		} else {
			cur.append(s, i, n);
			in = true;
		}
		i += n;
	}
	if (in) out.push_back(std::move(cur));
	return out;
}

std::string to_lower(std::string_view s)
{
	std::string out;
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		if (r == 0xFFFD && n == 1 && static_cast<unsigned char>(s[i]) >= 0x80) {
			// strings.ToLower writes an invalid byte as U+FFFD.
			append_rune(out, 0xFFFD);
		} else {
			append_rune(out, to_lower_rune(r));
		}
		i += n;
	}
	return out;
}

bool contains_any(std::string_view s, std::string_view chars)
{
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		for (std::size_t j = 0; j < chars.size();) {
			auto [c, m] = decode_rune(chars, j);
			if (c == r) return true;
			j += m;
		}
		i += n;
	}
	return false;
}

std::string quote(std::string_view s)
{
	std::string out = "\"";
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		if (n == 1 && r == 0xFFFD) {
			unsigned char c = static_cast<unsigned char>(s[i]);
			out += "\\x";
			out += lowerhex[c >> 4];
			out += lowerhex[c & 15];
			i++;
			continue;
		}
		i += n;
		if (r == '"' || r == '\\') {
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
		char buf[16];
		if (r < ' ' || r == 0x7f) std::snprintf(buf, sizeof buf, "\\x%02x", static_cast<unsigned>(r));
		else if (r < 0x10000) std::snprintf(buf, sizeof buf, "\\u%04x", static_cast<unsigned>(r));
		else std::snprintf(buf, sizeof buf, "\\U%08x", static_cast<unsigned>(r));
		out += buf;
	}
	return out + "\"";
}

std::string replace(std::string_view s, const std::vector<std::pair<std::string_view, std::string_view>> &pairs)
{
	std::string out;
	for (std::size_t i = 0; i < s.size();) {
		bool matched = false;
		for (const auto &[from, to] : pairs) {
			if (s.substr(i, from.size()) == from) {
				out += to;
				i += from.size();
				matched = true;
				break;
			}
		}
		if (!matched) out += s[i++];
	}
	return out;
}

// ---- net/url ----

namespace url {

namespace {

enum class Mode { path, path_segment, host, zone, user_password, query_component, fragment };

bool should_escape(unsigned char c, Mode mode)
{
	if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) return false;
	if (mode == Mode::host || mode == Mode::zone) {
		switch (c) {
		case '!': case '$': case '&': case '\'': case '(': case ')': case '*': case '+': case ',': case ';': case '=':
		case ':': case '[': case ']': case '<': case '>': case '"':
			return false;
		}
	}
	switch (c) {
	case '-': case '_': case '.': case '~':
		return false;
	case '$': case '&': case '+': case ',': case '/': case ':': case ';': case '=': case '?': case '@':
		switch (mode) {
		case Mode::path: return c == '?';
		case Mode::path_segment: return c == '/' || c == ';' || c == ',' || c == '?';
		case Mode::user_password: return c == '@' || c == '/' || c == '?' || c == ':';
		case Mode::query_component: return true;
		case Mode::fragment: return false;
		default: break;
		}
	}
	if (mode == Mode::fragment) {
		switch (c) {
		case '!': case '(': case ')': case '*':
			return false;
		}
	}
	return true;
}

bool ishex(char c) { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'); }

unsigned char unhex(char c)
{
	if (c >= '0' && c <= '9') return static_cast<unsigned char>(c - '0');
	if (c >= 'a' && c <= 'f') return static_cast<unsigned char>(c - 'a' + 10);
	return static_cast<unsigned char>(c - 'A' + 10);
}

std::optional<std::string> unescape(std::string_view s, Mode mode)
{
	int n = 0;
	bool has_plus = false;
	for (std::size_t i = 0; i < s.size();) {
		switch (s[i]) {
		case '%':
			n++;
			if (i + 2 >= s.size() || !ishex(s[i + 1]) || !ishex(s[i + 2])) return std::nullopt;
			if (mode == Mode::host && unhex(s[i + 1]) < 8 && s.substr(i, 3) != "%25") return std::nullopt;
			if (mode == Mode::zone) {
				unsigned char v = static_cast<unsigned char>(unhex(s[i + 1]) << 4 | unhex(s[i + 2]));
				if (s.substr(i, 3) != "%25" && v != ' ' && should_escape(v, Mode::host)) return std::nullopt;
			}
			i += 3;
			break;
		case '+':
			has_plus = mode == Mode::query_component;
			i++;
			break;
		default:
			if ((mode == Mode::host || mode == Mode::zone) && static_cast<unsigned char>(s[i]) < 0x80 &&
			    should_escape(static_cast<unsigned char>(s[i]), mode))
				return std::nullopt;
			i++;
		}
	}
	if (n == 0 && !has_plus) return std::string(s);
	std::string t;
	for (std::size_t i = 0; i < s.size(); i++) {
		if (s[i] == '%') {
			t += static_cast<char>(unhex(s[i + 1]) << 4 | unhex(s[i + 2]));
			i += 2;
		} else if (s[i] == '+') {
			t += mode == Mode::query_component ? ' ' : '+';
		} else {
			t += s[i];
		}
	}
	return t;
}

std::string escape(std::string_view s, Mode mode)
{
	std::string t;
	for (unsigned char c : s) {
		if (c == ' ' && mode == Mode::query_component) {
			t += '+';
		} else if (should_escape(c, mode)) {
			t += '%';
			t += upperhex[c >> 4];
			t += upperhex[c & 15];
		} else {
			t += static_cast<char>(c);
		}
	}
	return t;
}

bool valid_encoded(std::string_view s, Mode mode)
{
	for (unsigned char c : s) {
		switch (c) {
		case '!': case '$': case '&': case '\'': case '(': case ')': case '*': case '+': case ',': case ';': case '=':
		case ':': case '@': case '[': case ']': case '%':
			break;
		default:
			if (should_escape(c, mode)) return false;
		}
	}
	return true;
}

bool valid_optional_port(std::string_view port)
{
	if (port.empty()) return true;
	if (port[0] != ':') return false;
	for (char c : port.substr(1))
		if (c < '0' || c > '9') return false;
	return true;
}

bool valid_userinfo(std::string_view s)
{
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		i += n;
		if ((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) continue;
		switch (r) {
		case '-': case '.': case '_': case ':': case '~': case '!': case '$': case '&': case '\'': case '(': case ')':
		case '*': case '+': case ',': case ';': case '=': case '%': case '@':
			continue;
		}
		return false;
	}
	return true;
}

// netip.ParseAddr for a bracketed host: an IPv6 address with an optional zone; false also for IPv4.
bool ipv6_literal(const std::string &host)
{
	std::string addr = host;
	if (auto pct = addr.find('%'); pct != std::string::npos) {
		if (pct + 1 == addr.size()) return false;
		addr = addr.substr(0, pct);
	}
	if (addr.find(':') == std::string::npos) return false;
	unsigned char buf[16];
	return inet_pton(AF_INET6, addr.c_str(), buf) == 1;
}

std::optional<std::string> parse_host(std::string_view scheme, std::string_view host)
{
	auto open = host.rfind('[');
	if (open != std::string_view::npos && open > 0) return std::nullopt;
	if (open == 0) {
		auto close = host.rfind(']');
		if (close == std::string_view::npos) return std::nullopt;
		std::string_view colon_port = host.substr(close + 1);
		if (!valid_optional_port(colon_port)) return std::nullopt;
		auto port = unescape(colon_port, Mode::host);
		if (!port) return std::nullopt;
		std::string_view hostname = host.substr(1, close - 1);
		std::string unescaped;
		if (auto zone = hostname.find("%25"); zone != std::string_view::npos) {
			auto a = unescape(hostname.substr(0, zone), Mode::host);
			auto b = unescape(hostname.substr(zone), Mode::zone);
			if (!a || !b) return std::nullopt;
			unescaped = *a + *b;
		} else {
			auto a = unescape(hostname, Mode::host);
			if (!a) return std::nullopt;
			unescaped = *a;
		}
		if (!ipv6_literal(unescaped)) return std::nullopt;
		return "[" + unescaped + "]" + *port;
	}
	if (auto i = host.find(':'); i != std::string_view::npos) {
		auto last = host.rfind(':');
		// Go 1.26's urlstrictcolons: http and https take the first colon, other schemes the last.
		if (last != i && scheme != "http" && scheme != "https") i = last;
		if (!valid_optional_port(host.substr(i))) return std::nullopt;
	}
	return unescape(host, Mode::host);
}

} // namespace

std::string query_escape(std::string_view s) { return escape(s, Mode::query_component); }
std::string path_escape(std::string_view s) { return escape(s, Mode::path_segment); }
std::optional<std::string> query_unescape(std::string_view s) { return unescape(s, Mode::query_component); }

std::string URL::escaped_path() const
{
	if (!raw_path.empty() && valid_encoded(raw_path, Mode::path)) {
		auto p = unescape(raw_path, Mode::path);
		if (p && *p == path) return raw_path;
	}
	if (path == "*") return "*";
	return escape(path, Mode::path);
}

std::string URL::request_uri() const
{
	std::string result = opaque;
	if (result.empty()) {
		result = escaped_path();
		if (result.empty()) result = "/";
	} else if (result.starts_with("//")) {
		result = scheme + ":" + result;
	}
	if (force_query || !raw_query.empty()) result += "?" + raw_query;
	return result;
}

std::optional<URL> parse(std::string_view raw)
{
	std::string_view rest = raw, frag;
	bool has_frag = false;
	if (auto h = raw.find('#'); h != std::string_view::npos) {
		rest = raw.substr(0, h);
		frag = raw.substr(h + 1);
		has_frag = true;
	}
	URL u;
	for (unsigned char c : rest)
		if (c < ' ' || c == 0x7f) return std::nullopt;
	if (rest == "*") {
		u.path = "*";
	} else {
		// getScheme
		std::size_t i = 0;
		bool scheme_found = false;
		for (; i < rest.size(); i++) {
			char c = rest[i];
			if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) continue;
			if ((c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.') {
				if (i == 0) break;
				continue;
			}
			if (c == ':') {
				if (i == 0) return std::nullopt;
				scheme_found = true;
			}
			break;
		}
		if (scheme_found) {
			for (char c : rest.substr(0, i)) u.scheme += static_cast<char>(c >= 'A' && c <= 'Z' ? c + 32 : c);
			rest = rest.substr(i + 1);
		}
		if (rest.ends_with("?") && std::count(rest.begin(), rest.end(), '?') == 1) {
			u.force_query = true;
			rest.remove_suffix(1);
		} else if (auto q = rest.find('?'); q != std::string_view::npos) {
			u.raw_query = std::string(rest.substr(q + 1));
			rest = rest.substr(0, q);
		}
		if (!rest.starts_with("/")) {
			if (!u.scheme.empty()) {
				u.opaque = std::string(rest);
				goto fragment;
			}
			std::string_view segment = rest.substr(0, rest.find('/'));
			if (segment.find(':') != std::string_view::npos) return std::nullopt;
		}
		if ((!u.scheme.empty() || !rest.starts_with("///")) && rest.starts_with("//")) {
			std::string_view authority = rest.substr(2);
			rest = {};
			if (auto slash = authority.find('/'); slash != std::string_view::npos) {
				rest = authority.substr(slash);
				authority = authority.substr(0, slash);
			}
			auto at = authority.rfind('@');
			auto host = parse_host(u.scheme, at == std::string_view::npos ? authority : authority.substr(at + 1));
			if (!host) return std::nullopt;
			if (at != std::string_view::npos) {
				std::string_view userinfo = authority.substr(0, at);
				if (!valid_userinfo(userinfo)) return std::nullopt;
				auto colon = userinfo.find(':');
				if (!unescape(userinfo.substr(0, colon), Mode::user_password)) return std::nullopt;
				if (colon != std::string_view::npos && !unescape(userinfo.substr(colon + 1), Mode::user_password))
					return std::nullopt;
			}
			u.host = *host;
		}
		auto path = unescape(rest, Mode::path);
		if (!path) return std::nullopt;
		u.path = *path;
		if (escape(u.path, Mode::path) != rest) u.raw_path = std::string(rest);
	}
fragment:
	if (has_frag && !frag.empty()) {
		auto f = unescape(frag, Mode::fragment);
		if (!f) return std::nullopt;
		u.fragment = *f;
	}
	return u;
}

Values parse_query(std::string_view query)
{
	Values m;
	if (std::count(query.begin(), query.end(), '&') + 1 > 10000) return m;
	while (!query.empty()) {
		std::string_view key = query;
		if (auto amp = query.find('&'); amp != std::string_view::npos) {
			key = query.substr(0, amp);
			query = query.substr(amp + 1);
		} else {
			query = {};
		}
		if (key.find(';') != std::string_view::npos || key.empty()) continue;
		std::string_view value;
		if (auto eq = key.find('='); eq != std::string_view::npos) {
			value = key.substr(eq + 1);
			key = key.substr(0, eq);
		}
		auto k = query_unescape(key);
		if (!k) continue;
		auto v = query_unescape(value);
		if (!v) continue;
		m[*k].push_back(*v);
	}
	return m;
}

std::string encode(const Values &values)
{
	std::string out;
	for (const auto &[k, vs] : values) {
		std::string key = query_escape(k);
		for (const auto &v : vs) {
			if (!out.empty()) out += '&';
			out += key + "=" + query_escape(v);
		}
	}
	return out;
}

} // namespace url

// ---- mime ----

namespace mime {

namespace {

constexpr std::size_t max_content_len = 75 - 10 - 2;  // maxEncodedWordLen - len("=?UTF-8?q?") - len("?=")

bool needs_encoding(std::string_view s)
{
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		if ((r < ' ' || r > '~') && r != '\t') return true;
		i += n;
	}
	return false;
}

bool hexdigit(char c) { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'); }

int hexval(char c) { return c <= '9' ? c - '0' : c <= 'F' ? c - 'A' + 10 : c - 'a' + 10; }

bool is_utf8(std::string_view charset)
{
	if (charset.size() != 5) return false;
	std::string l;
	for (char c : charset) l += static_cast<char>(c >= 'A' && c <= 'Z' ? c + 32 : c);
	return l == "utf-8";
}

void write_q(std::string &buf, std::string_view s)
{
	for (unsigned char b : s) {
		if (b == ' ') buf += '_';
		else if (b >= '!' && b <= '~' && b != '=' && b != '?' && b != '_') buf += static_cast<char>(b);
		else {
			buf += '=';
			buf += upperhex[b >> 4];
			buf += upperhex[b & 15];
		}
	}
}

} // namespace

std::string q_encode(std::string_view charset, std::string_view s)
{
	if (!needs_encoding(s)) return std::string(s);
	std::string open = "=?" + std::string(charset) + "?q?";
	std::string buf = open;
	if (!is_utf8(charset)) {
		write_q(buf, s);
		return buf + "?=";
	}
	std::size_t current = 0;
	for (std::size_t i = 0; i < s.size();) {
		unsigned char b = static_cast<unsigned char>(s[i]);
		std::size_t rune_len, enc_len;
		if (b >= ' ' && b <= '~' && b != '=' && b != '?' && b != '_') {
			rune_len = enc_len = 1;
		} else {
			rune_len = decode_rune(s, i).second;
			enc_len = 3 * rune_len;
		}
		if (current + enc_len > max_content_len) {
			buf += "?= " + open;
			current = 0;
		}
		write_q(buf, s.substr(i, rune_len));
		current += enc_len;
		i += rune_len;
	}
	return buf + "?=";
}

std::string b_encode(std::string_view charset, std::string_view s)
{
	if (!needs_encoding(s)) return std::string(s);
	std::string open = "=?" + std::string(charset) + "?b?";
	std::string buf = open;
	std::size_t max_base64 = max_content_len / 4 * 3;  // base64.StdEncoding.DecodedLen(maxContentLen)
	if (!is_utf8(charset) || (s.size() + 2) / 3 * 4 <= max_content_len) return buf + base64::encode(s) + "?=";
	std::size_t current = 0, last = 0;
	for (std::size_t i = 0; i < s.size();) {
		std::size_t n = decode_rune(s, i).second;
		if (current + n <= max_base64) {
			current += n;
		} else {
			buf += base64::encode(s.substr(last, i - last));
			buf += "?= " + open;
			last = i;
			current = n;
		}
		i += n;
	}
	return buf + base64::encode(s.substr(last)) + "?=";
}

std::string quoted_printable(std::string_view text)
{
	constexpr std::size_t line_max = 76;
	std::string out, line;
	bool cr = false;
	auto flush = [&] {
		out += line;
		line.clear();
	};
	auto crlf = [&] {
		line += "\r\n";
		flush();
	};
	auto soft = [&] {
		line += '=';
		crlf();
	};
	auto encode = [&](unsigned char b) {
		if (line_max - 1 - line.size() < 3) soft();
		line += '=';
		line += upperhex[b >> 4];
		line += upperhex[b & 15];
	};
	auto check_last = [&] {
		if (line.empty()) return;
		unsigned char b = static_cast<unsigned char>(line.back());
		if (b == ' ' || b == '\t') {
			line.pop_back();
			encode(b);
		}
	};
	auto write = [&](unsigned char b) {
		if (b == '\n' || b == '\r') {
			if (cr && b == '\n') {
				cr = false;
				return;
			}
			if (b == '\r') cr = true;
			check_last();
			crlf();
			return;
		}
		if (line.size() == line_max - 1) soft();
		line += static_cast<char>(b);
		cr = false;
	};
	for (unsigned char b : text) {
		if ((b >= '!' && b <= '~' && b != '=') || b == ' ' || b == '\t' || b == '\n' || b == '\r') write(b);
		else encode(b);
	}
	check_last();
	flush();
	return out;
}

namespace {

// mime.WordDecoder.Decode with the net/mail decoder: utf-8, iso-8859-1 and us-ascii; other charsets fail with
// charset set.
std::optional<std::string> decode_word(std::string_view word, bool &charset_error)
{
	charset_error = false;
	if (word.size() < 8 || !word.starts_with("=?") || !word.ends_with("?=") ||
	    std::count(word.begin(), word.end(), '?') != 4)
		return std::nullopt;
	word = word.substr(2, word.size() - 4);
	auto q1 = word.find('?');
	std::string_view charset = word.substr(0, q1), text = word.substr(q1 + 1);
	if (charset.empty()) return std::nullopt;
	auto q2 = text.find('?');
	std::string_view encoding = text.substr(0, q2);
	text = q2 == std::string_view::npos ? std::string_view{} : text.substr(q2 + 1);
	if (encoding.size() != 1) return std::nullopt;
	std::string content;
	if (encoding[0] == 'B' || encoding[0] == 'b') {
		auto d = base64::decode(text);
		if (!d) return std::nullopt;
		content = *d;
	} else if (encoding[0] == 'Q' || encoding[0] == 'q') {
		for (std::size_t i = 0; i < text.size(); i++) {
			char c = text[i];
			if (c == '_') {
				content += ' ';
			} else if (c == '=') {
				if (i + 2 >= text.size() || !hexdigit(text[i + 1]) || !hexdigit(text[i + 2])) return std::nullopt;
				content += static_cast<char>(hexval(text[i + 1]) << 4 | hexval(text[i + 2]));
				i += 2;
			} else if ((c <= '~' && c >= ' ') || c == '\n' || c == '\r' || c == '\t') {
				content += c;
			} else {
				return std::nullopt;
			}
		}
	} else {
		return std::nullopt;
	}
	std::string lower;
	for (char c : charset) lower += static_cast<char>(c >= 'A' && c <= 'Z' ? c + 32 : c);
	if (lower == "utf-8") return content;
	std::string out;
	if (lower == "iso-8859-1") {
		for (unsigned char c : content) append_rune(out, c);
		return out;
	}
	if (lower == "us-ascii") {
		for (unsigned char c : content) {
			if (c >= 0x80) append_rune(out, 0xFFFD);
			else out += static_cast<char>(c);
		}
		return out;
	}
	charset_error = true;
	return std::nullopt;
}

} // namespace

} // namespace mime

// ---- net/mail ----

namespace mail {

namespace {

bool is_multibyte(char32_t r) { return r >= 0x80; }
bool is_vchar(char32_t r) { return (r >= '!' && r <= '~') || is_multibyte(r); }
bool is_wsp(char32_t r) { return r == ' ' || r == '\t'; }
bool is_atext(char32_t r, bool dot)
{
	switch (r) {
	case '.': return dot;
	case '(': case ')': case '<': case '>': case '[': case ']': case ':': case ';': case '@': case '\\': case ',':
	case '"':
		return false;
	}
	return is_vchar(r);
}
bool is_qtext(char32_t r) { return r != '\\' && r != '"' && is_vchar(r); }
bool is_dtext(char32_t r) { return r != '[' && r != ']' && r != '\\' && is_vchar(r); }

std::string quote_string(std::string_view s)
{
	std::string b = "\"";
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		i += n;
		if (is_qtext(r) || is_wsp(r)) {
			append_rune(b, r);
		} else if (is_vchar(r)) {
			b += '\\';
			append_rune(b, r);
		}
	}
	return b + "\"";
}

bool parse_ip(const std::string &s, bool &v4)
{
	unsigned char buf[16];
	if (inet_pton(AF_INET, s.c_str(), buf) == 1) {
		v4 = true;
		return true;
	}
	if (s.find('%') == std::string::npos && inet_pton(AF_INET6, s.c_str(), buf) == 1) {
		static const unsigned char mapped[12] = {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff};
		v4 = std::equal(mapped, mapped + 12, buf);
		return true;
	}
	return false;
}

struct Parser {
	std::string_view s;
	bool error = false;

	bool empty() const { return s.empty(); }
	char peek() const { return s[0]; }
	bool consume(char c)
	{
		if (empty() || peek() != c) return false;
		s.remove_prefix(1);
		return true;
	}
	void skip_space()
	{
		while (!s.empty() && (s[0] == ' ' || s[0] == '\t')) s.remove_prefix(1);
	}
	std::optional<std::string> consume_comment()
	{
		int depth = 1;
		std::string comment;
		while (!empty() && depth != 0) {
			if (peek() == '\\' && s.size() > 1) s.remove_prefix(1);
			else if (peek() == '(') depth++;
			else if (peek() == ')') depth--;
			if (depth > 0) comment += s[0];
			s.remove_prefix(1);
		}
		if (depth != 0) return std::nullopt;
		return comment;
	}
	bool skip_cfws()
	{
		skip_space();
		while (consume('(')) {
			if (!consume_comment()) return false;
			skip_space();
		}
		return true;
	}
	std::optional<std::string> consume_quoted_string()
	{
		std::size_t i = 1;
		std::string out;
		bool escaped = false;
		for (;;) {
			if (i >= s.size()) return std::nullopt;
			auto [r, n] = decode_rune(s, i);
			if (n == 1 && r == 0xFFFD) return std::nullopt;
			if (escaped) {
				if (!is_vchar(r) && !is_wsp(r)) return std::nullopt;
				append_rune(out, r);
				escaped = false;
			} else if (is_qtext(r) || is_wsp(r)) {
				append_rune(out, r);
			} else if (r == '"') {
				break;
			} else if (r == '\\') {
				escaped = true;
			} else {
				return std::nullopt;
			}
			i += n;
		}
		s.remove_prefix(i + 1);
		return out;
	}
	std::optional<std::string> consume_atom(bool dot, bool permissive)
	{
		std::size_t i = 0;
		for (;;) {
			if (i >= s.size()) break;
			auto [r, n] = decode_rune(s, i);
			if (n == 1 && r == 0xFFFD) return std::nullopt;
			if (!is_atext(r, dot)) break;
			i += n;
		}
		if (i == 0) return std::nullopt;
		std::string atom(s.substr(0, i));
		s.remove_prefix(i);
		if (!permissive && (atom.starts_with(".") || atom.find("..") != std::string::npos || atom.ends_with(".")))
			return std::nullopt;
		return atom;
	}
	std::optional<std::string> consume_domain_literal()
	{
		if (!consume('[')) return std::nullopt;
		std::string_view dtext = s;
		std::size_t len = 0;
		for (;;) {
			if (empty()) return std::nullopt;
			if (peek() == ']') break;
			auto [r, n] = decode_rune(s, 0);
			if (n == 1 && r == 0xFFFD) return std::nullopt;
			if (!is_dtext(r)) return std::nullopt;
			len += n;
			s.remove_prefix(n);
		}
		std::string text(dtext.substr(0, len));
		if (!consume(']')) return std::nullopt;
		bool v4 = false;
		if (text.starts_with("IPv6:")) {
			if (!parse_ip(text.substr(5), v4)) return std::nullopt;
		} else if (!parse_ip(text, v4) || !v4) {
			return std::nullopt;
		}
		return "[" + text + "]";
	}
	// consumeAddrSpec; restores the input when it fails.
	std::optional<std::string> consume_addr_spec()
	{
		std::string_view orig = s;
		auto fail = [&] {
			s = orig;
			return std::nullopt;
		};
		skip_space();
		if (empty()) return fail();
		std::optional<std::string> local;
		if (peek() == '"') {
			local = consume_quoted_string();
			if (local && local->empty()) local.reset();
		} else {
			local = consume_atom(true, false);
		}
		if (!local || !consume('@')) return fail();
		skip_space();
		if (empty()) return fail();
		auto domain = peek() == '[' ? consume_domain_literal() : consume_atom(true, false);
		if (!domain) return fail();
		return *local + "@" + *domain;
	}
	// decodeRFC2047Word: the word, whether it was encoded; error on an unknown charset.
	std::string decode_rfc2047(const std::string &word, bool &encoded, bool &failed)
	{
		bool charset_error = false;
		auto d = mime::decode_word(word, charset_error);
		failed = false;
		if (d) {
			encoded = true;
			return *d;
		}
		if (charset_error) {
			encoded = true;
			failed = true;
			return word;
		}
		encoded = false;
		return word;
	}
	std::optional<std::string> consume_phrase()
	{
		std::vector<std::string> words;
		std::string sb;
		bool failed = false;
		for (;;) {
			if (!words.empty() && !skip_cfws()) return std::nullopt;
			skip_space();
			if (empty()) break;
			bool encoded = false;
			std::optional<std::string> word;
			if (peek() == '"') {
				word = consume_quoted_string();
			} else {
				word = consume_atom(true, true);
				if (word) {
					bool bad = false;
					word = decode_rfc2047(*word, encoded, bad);
					if (bad) word.reset();
				}
			}
			if (!word) {
				failed = true;
				break;
			}
			if (encoded) {
				sb += *word;
			} else if (!sb.empty()) {
				words.push_back(sb);
				sb.clear();
				words.push_back(*word);
			} else {
				words.push_back(*word);
			}
		}
		if (!sb.empty()) words.push_back(sb);
		if (failed && words.empty()) return std::nullopt;
		std::string phrase;
		for (std::size_t i = 0; i < words.size(); i++) phrase += (i ? " " : "") + words[i];
		return phrase;
	}
	std::optional<std::string> consume_display_name_comment()
	{
		if (!consume('(')) return std::nullopt;
		auto comment = consume_comment();
		if (!comment) return std::nullopt;
		std::vector<std::string> words;
		std::string cur;
		for (char c : *comment) {
			if (c == ' ' || c == '\t') {
				if (!cur.empty()) words.push_back(cur), cur.clear();
			} else {
				cur += c;
			}
		}
		if (!cur.empty()) words.push_back(cur);
		std::string out;
		for (std::size_t i = 0; i < words.size(); i++) {
			bool encoded = false, bad = false;
			std::string d = decode_rfc2047(words[i], encoded, bad);
			if (bad) return std::nullopt;
			out += (i ? " " : "") + (encoded ? d : words[i]);
		}
		return out;
	}
	// parseAddress: the addresses (a group may hold several or none); nullopt on an error.
	std::optional<std::vector<Address>> parse_address(bool handle_group)
	{
		skip_space();
		if (empty()) return std::nullopt;
		if (auto spec = consume_addr_spec()) {
			std::string display;
			skip_space();
			if (!empty() && peek() == '(') {
				auto d = consume_display_name_comment();
				if (!d) return std::nullopt;
				display = *d;
			}
			return std::vector<Address>{{display, *spec}};
		}
		std::string display;
		if (peek() != '<') {
			auto d = consume_phrase();
			if (!d) return std::nullopt;
			display = *d;
		}
		skip_space();
		if (handle_group && consume(':')) return consume_group_list();
		if (!consume('<')) return std::nullopt;
		auto spec = consume_addr_spec();
		if (!spec || !consume('>')) return std::nullopt;
		return std::vector<Address>{{display, *spec}};
	}
	std::optional<std::vector<Address>> consume_group_list()
	{
		std::vector<Address> group;
		skip_space();
		if (consume(';')) {
			if (!skip_cfws()) return std::nullopt;
			return group;
		}
		for (;;) {
			skip_space();
			auto addrs = parse_address(false);
			if (!addrs) return std::nullopt;
			group.insert(group.end(), addrs->begin(), addrs->end());
			if (!skip_cfws()) return std::nullopt;
			if (consume(';')) {
				if (!skip_cfws()) return std::nullopt;
				break;
			}
			if (!consume(',')) return std::nullopt;
		}
		return group;
	}
};

} // namespace

std::optional<Address> parse_address(std::string_view text)
{
	Parser p{text};
	auto addrs = p.parse_address(true);
	if (!addrs || !p.skip_cfws() || !p.empty() || addrs->size() != 1) return std::nullopt;
	return (*addrs)[0];
}

std::string Address::string() const
{
	auto at = address.rfind('@');
	std::string local = at == std::string::npos ? address : address.substr(0, at);
	std::string domain = at == std::string::npos ? "" : address.substr(at + 1);
	bool quote_local = false;
	for (std::size_t i = 0; i < local.size();) {
		auto [r, n] = decode_rune(local, i);
		if (!is_atext(r, false)) {
			if (!(r == '.' && i > 0 && local[i - 1] != '.' && i < local.size() - 1)) {
				quote_local = true;
				break;
			}
		}
		i += n;
	}
	if (quote_local) local = quote_string(local);
	std::string s = "<" + local + "@" + domain + ">";
	if (name.empty()) return s;
	bool printable = true;
	for (std::size_t i = 0; i < name.size();) {
		auto [r, n] = decode_rune(name, i);
		if ((!is_vchar(r) && !is_wsp(r)) || is_multibyte(r)) {
			printable = false;
			break;
		}
		i += n;
	}
	if (printable) return quote_string(name) + " " + s;
	if (contains_any(name, "\"#$%&'(),.:;<>@[]^`{|}~")) return mime::b_encode("utf-8", name) + " " + s;
	return mime::q_encode("utf-8", name) + " " + s;
}

} // namespace mail

// ---- mime/multipart ----

Multipart::Multipart(std::string boundary) : boundary_(std::move(boundary))
{
	if (boundary_.empty()) boundary_ = random_hex(30);
}

std::string Multipart::form_data_content_type() const
{
	std::string b = boundary_;
	if (b.find_first_of("()<>@,;:\\\"/[]?= ") != std::string::npos) b = "\"" + b + "\"";
	return "multipart/form-data; boundary=" + b;
}

void Multipart::part(std::vector<std::pair<std::string, std::string>> header, std::string_view content)
{
	body_ += first_ ? "--" + boundary_ + "\r\n" : "\r\n--" + boundary_ + "\r\n";
	first_ = false;
	std::stable_sort(header.begin(), header.end(), [](const auto &a, const auto &b) { return a.first < b.first; });
	for (const auto &[k, v] : header) body_ += k + ": " + v + "\r\n";
	body_ += "\r\n";
	body_ += content;
}

namespace {
std::string escape_quotes(std::string_view s)
{
	return replace(s, {{"\\", "\\\\"}, {"\"", "\\\""}, {"\r", "%0D"}, {"\n", "%0A"}});
}
} // namespace

void Multipart::field(std::string_view name, std::string_view value)
{
	part({{"Content-Disposition", "form-data; name=\"" + escape_quotes(name) + "\""}}, value);
}

void Multipart::file(std::string_view field, std::string_view filename, std::string_view data)
{
	part({{"Content-Disposition", "form-data; name=\"" + escape_quotes(field) + "\"; filename=\"" + escape_quotes(filename) + "\""},
	      {"Content-Type", "application/octet-stream"}},
	     data);
}

std::string Multipart::close()
{
	body_ += "\r\n--" + boundary_ + "--\r\n";
	return std::move(body_);
}

// ---- time ----

namespace {
std::tm broken(Clock::time_point t, bool utc)
{
	std::time_t secs = Clock::to_time_t(t);
	// to_time_t truncates toward zero; Go's layouts take the second the instant is in.
	if (t < Clock::from_time_t(secs)) secs--;
	std::tm tm{};
	if (utc) gmtime_r(&secs, &tm);
	else localtime_r(&secs, &tm);
	return tm;
}

std::string zone_offset(const std::tm &tm, bool colon)
{
	long off = tm.tm_gmtoff;
	int a = static_cast<int>((off < 0 ? -off : off) / 60);
	char sign = off < 0 ? '-' : '+';
	return colon ? std::format("{}{:02}:{:02}", sign, a / 60, a % 60) : std::format("{}{:02}{:02}", sign, a / 60, a % 60);
}
} // namespace

std::string format_datetime_zone(Clock::time_point t)
{
	std::tm tm = broken(t, false);
	char buf[64];
	std::strftime(buf, sizeof buf, "%Y-%m-%d %H:%M:%S ", &tm);
	std::string out = buf;
	// Go writes the zone's abbreviation; a zone without one (abbreviation empty) as -0700.
	if (tm.tm_zone && *tm.tm_zone) out += tm.tm_zone;
	else out += zone_offset(tm, false);
	return out;
}

std::string format_rfc3339(Clock::time_point t, bool utc)
{
	std::tm tm = broken(t, utc);
	char buf[32];
	std::strftime(buf, sizeof buf, "%Y-%m-%dT%H:%M:%S", &tm);
	return buf + (tm.tm_gmtoff == 0 ? std::string("Z") : zone_offset(tm, true));
}

std::string format_rfc1123z(Clock::time_point t)
{
	static const char *days[] = {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"};
	static const char *months[] = {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"};
	std::tm tm = broken(t, false);
	char buf[64];
	std::snprintf(buf, sizeof buf, "%s, %02d %s %04d %02d:%02d:%02d ", days[tm.tm_wday], tm.tm_mday, months[tm.tm_mon],
	              tm.tm_year + 1900, tm.tm_hour, tm.tm_min, tm.tm_sec);
	return buf + zone_offset(tm, false);
}

std::string format_clock(Clock::time_point t)
{
	std::tm tm = broken(t, false);
	char buf[8];
	std::snprintf(buf, sizeof buf, "%02d:%02d", tm.tm_hour, tm.tm_min);
	return buf;
}

// ---- random and base64url ----

std::string random_hex(std::size_t n)
{
	std::string bytes(n, '\0');
	for (std::size_t got = 0; got < n;) {
		ssize_t r = getrandom(bytes.data() + got, n - got, 0);
		if (r > 0) got += static_cast<std::size_t>(r);
	}
	std::string out;
	for (unsigned char c : bytes) {
		out += lowerhex[c >> 4];
		out += lowerhex[c & 15];
	}
	return out;
}

std::string base64url_raw(std::string_view data)
{
	std::string s = base64::encode(data);
	while (!s.empty() && s.back() == '=') s.pop_back();
	for (char &c : s) {
		if (c == '+') c = '-';
		else if (c == '/') c = '_';
	}
	return s;
}

std::optional<std::string> base64url_raw_decode(std::string_view text)
{
	std::string s;
	for (char c : text) {
		if (c == '+' || c == '/' || c == '=' || c == '\r' || c == '\n') return std::nullopt;
		s += c == '-' ? '+' : c == '_' ? '/' : c;
	}
	if (s.size() % 4 == 1) return std::nullopt;
	while (s.size() % 4) s += '=';
	return base64::decode(s);
}

std::string random_base64url(std::size_t n)
{
	std::string bytes(n, '\0');
	for (std::size_t got = 0; got < n;) {
		ssize_t r = getrandom(bytes.data() + got, n - got, 0);
		if (r > 0) got += static_cast<std::size_t>(r);
	}
	return base64url_raw(bytes);
}

} // namespace nkos::notify::gostd
