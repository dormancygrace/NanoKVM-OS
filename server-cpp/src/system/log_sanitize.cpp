#include "system/log_sanitize.hpp"

#include <array>
#include <cstdint>
#include <vector>

namespace nkos::system::logs {

namespace {

constexpr std::string_view redacted_line = "[sensitive log entry hidden]";
constexpr std::string_view long_line = "[long log entry omitted]";
constexpr std::string_view replacement = "\xEF\xBF\xBD";

// One rune as Go's utf8.DecodeRuneInString takes it: (0xFFFD, 1) for an invalid byte.
std::pair<char32_t, std::size_t> decode(std::string_view s, std::size_t i)
{
	auto b = [&](std::size_t k) { return static_cast<unsigned char>(s[i + k]); };
	unsigned char c = b(0);
	std::size_t left = s.size() - i;
	auto cont = [&](std::size_t k, unsigned char lo = 0x80, unsigned char hi = 0xBF) { return k < left && b(k) >= lo && b(k) <= hi; };
	if (c < 0x80) return {c, 1};
	if (c >= 0xC2 && c <= 0xDF && cont(1)) return {static_cast<char32_t>((c & 0x1F) << 6 | (b(1) & 0x3F)), 2};
	if (c >= 0xE0 && c <= 0xEF) {
		unsigned char lo = c == 0xE0 ? 0xA0 : 0x80, hi = c == 0xED ? 0x9F : 0xBF;
		if (cont(1, lo, hi) && cont(2))
			return {static_cast<char32_t>((c & 0x0F) << 12 | (b(1) & 0x3F) << 6 | (b(2) & 0x3F)), 3};
	}
	if (c >= 0xF0 && c <= 0xF4) {
		unsigned char lo = c == 0xF0 ? 0x90 : 0x80, hi = c == 0xF4 ? 0x8F : 0xBF;
		if (cont(1, lo, hi) && cont(2) && cont(3))
			return {static_cast<char32_t>((c & 0x07) << 18 | (b(1) & 0x3F) << 12 | (b(2) & 0x3F) << 6 | (b(3) & 0x3F)), 4};
	}
	return {0xFFFD, 1};
}

// strings.ToValidUTF8(s, "�"): each run of invalid bytes becomes one replacement character.
std::string to_valid_utf8(std::string_view s)
{
	std::string out;
	out.reserve(s.size());
	bool invalid = false;
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x80) {
			out += s[i++];
			invalid = false;
			continue;
		}
		auto [r, size] = decode(s, i);
		if (size == 1) {
			i++;
			if (!invalid) {
				invalid = true;
				out += replacement;
			}
			continue;
		}
		invalid = false;
		out.append(s.substr(i, size));
		i += size;
	}
	return out;
}

// Go's unicode.Cf (Unicode 17.0): {lo, hi, stride}.
struct Range {
	char32_t lo, hi, stride;
};
constexpr Range format_chars[] = {
	{0xad, 0x600, 1363},  {0x601, 0x605, 1},   {0x61c, 0x6dd, 193}, {0x70f, 0x890, 385},    {0x891, 0x8e2, 81},
	{0x180e, 0x200b, 2045}, {0x200c, 0x200f, 1}, {0x202a, 0x202e, 1}, {0x2060, 0x2064, 1},   {0x2066, 0x206f, 1},
	{0xfeff, 0xfff9, 250}, {0xfffa, 0xfffb, 1},  {0x110bd, 0x110cd, 16}, {0x13430, 0x1343f, 1}, {0x1bca0, 0x1bca3, 1},
	{0x1d173, 0x1d17a, 1}, {0xe0001, 0xe0020, 31}, {0xe0021, 0xe007f, 1},
};

bool format_char(char32_t r)
{
	for (const auto &range : format_chars)
		if (r >= range.lo && r <= range.hi && (r - range.lo) % range.stride == 0) return true;
	return false;
}

// unicode.IsControl or Cf, except newline and tab.
bool filtered(char32_t r) { return r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r <= 0x9f) || format_char(r)); }

bool needs_rune_filter(std::string_view s)
{
	for (unsigned char c : s)
		if (c >= 0x7f || (c < 0x20 && c != '\n' && c != '\t')) return true;
	return false;
}

std::string filter_runes(std::string_view s)
{
	std::string out;
	out.reserve(s.size());
	for (std::size_t i = 0; i < s.size();) {
		auto [r, size] = decode(s, i);
		if (!filtered(r)) out.append(s.substr(i, size));
		i += size;
	}
	return out;
}

// terminalEscape: ESC [ params intermediates final, or ESC ] text (BEL | ESC \).
std::string strip_escapes(std::string_view s)
{
	std::string out;
	out.reserve(s.size());
	for (std::size_t i = 0; i < s.size();) {
		if (s[i] != '\x1b' || i + 1 >= s.size()) {
			out += s[i++];
			continue;
		}
		std::size_t j = i + 1;
		std::size_t end = 0;
		if (s[j] == '[') {
			std::size_t k = j + 1;
			while (k < s.size() && s[k] >= 0x30 && s[k] <= 0x3f) k++;
			while (k < s.size() && s[k] >= 0x20 && s[k] <= 0x2f) k++;
			if (k < s.size() && s[k] >= 0x40 && s[k] <= 0x7e) end = k + 1;
		} else if (s[j] == ']') {
			std::size_t k = j + 1;
			while (k < s.size() && s[k] != '\x07' && s[k] != '\x1b') k++;
			if (k < s.size() && s[k] == '\x07') end = k + 1;
			else if (k + 1 < s.size() && s[k] == '\x1b' && s[k + 1] == '\\') end = k + 2;
		}
		if (end) {
			i = end;
		} else {
			out += s[i++];
		}
	}
	return out;
}

bool re2_space(char32_t r) { return r == '\t' || r == '\n' || r == '\f' || r == '\r' || r == ' '; }
bool ascii_word(char32_t r) { return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'; }

// A rune as the patterns see it: its ASCII form under RE2's case folding (0 for none), and its classes.
struct Rune {
	char c;
	bool word, space;
};

std::vector<Rune> fold(std::string_view line)
{
	std::vector<Rune> out;
	for (std::size_t i = 0; i < line.size();) {
		auto [r, size] = decode(line, i);
		i += size;
		char c = 0;
		if (r < 0x80) c = static_cast<char>(r >= 'A' && r <= 'Z' ? r + 32 : r);
		else if (r == 0x212A) c = 'k';  // KELVIN SIGN folds with k
		else if (r == 0x017F) c = 's';  // LATIN SMALL LETTER LONG S folds with s
		out.push_back({c, ascii_word(r), re2_space(r)});
	}
	return out;
}

bool at(const std::vector<Rune> &s, std::size_t p, std::string_view lit)
{
	if (p + lit.size() > s.size()) return false;
	for (std::size_t k = 0; k < lit.size(); k++)
		if (s[p + k].c != lit[k]) return false;
	return true;
}

// sensitiveEntry, case-insensitive, anywhere in the line.
bool sensitive_entry(const std::vector<Rune> &s)
{
	static constexpr std::string_view words[] = {"password", "passwd", "passphrase", "secret", "token", "credential",
	                                              "authorization", "cookie", "bearer", "ecdsa-sha2", "ssh-rsa", "ssh-ed25519"};
	std::size_t n = s.size();
	for (std::size_t p = 0; p < n; p++) {
		for (auto w : words)
			if (at(s, p, w)) return true;
		if (at(s, p, "psk") && (p == 0 || !s[p - 1].word) && (p + 3 == n || !s[p + 3].word)) return true;
		for (std::string_view prefix : {"api", "auth", "private"}) {
			if (!at(s, p, prefix)) continue;
			std::size_t q = p + prefix.size();
			if (at(s, q, "key")) return true;
			if (q < n && (s[q].c == '_' || s[q].c == ' ' || s[q].c == '-') && at(s, q + 1, "key")) return true;
		}
		if (at(s, p, "http")) {
			std::size_t q = p + 4;
			for (std::size_t start : {q, q + 1}) {
				if (start == q + 1 && !(q < n && s[q].c == 's')) continue;
				if (!at(s, start, "://")) continue;
				std::size_t r = start + 3;
				// [^\s/]+@
				for (std::size_t k = r; k < n && !s[k].space && s[k].c != '/'; k++)
					if (s[k].c == '@' && k > r) return true;
				// [^\s]*(?:login\.tailscale\.com/a/|auth\.netbird\.io/)
				for (std::size_t k = r; k <= n; k++) {
					if (at(s, k, "login.tailscale.com/a/") || at(s, k, "auth.netbird.io/")) return true;
					if (k == n || s[k].space) break;
				}
			}
		}
		if (s[p].c == '?' || s[p].c == '&')
			for (std::string_view key : {"key=", "signature=", "sig="})
				if (at(s, p + 1, key)) return true;
		if (s[p].c == '"')
			for (std::string_view key : {"messages", "message", "prompt", "content", "payload", "body"})
				if (at(s, p + 1, key) && at(s, p + 1 + key.size(), "\"")) {
					std::size_t k = p + 2 + key.size();
					while (k < n && s[k].space) k++;
					if (k < n && s[k].c == ':') return true;
				}
	}
	return false;
}

// strings.ToLower as far as ASCII keywords can tell: ASCII letters, KELVIN SIGN and LATIN CAPITAL I WITH DOT
// ABOVE (their lowercase is ASCII); every other non-ASCII rune is one byte that matches nothing.
std::string lower_for_words(std::string_view line)
{
	std::string out;
	out.reserve(line.size());
	for (std::size_t i = 0; i < line.size();) {
		auto [r, size] = decode(line, i);
		i += size;
		if (r < 0x80) out += static_cast<char>(r >= 'A' && r <= 'Z' ? r + 32 : r);
		else if (r == 0x212A) out += 'k';
		else if (r == 0x0130) out += 'i';
		else out += '\x80';
	}
	return out;
}

bool sensitive_line(std::string_view line)
{
	static constexpr std::string_view words[] = {
		"password", "passwd", "passphrase", "psk", "secret", "token", "credential", "api_key", "api-key", "api key",
		"apikey", "auth_key", "auth-key", "auth key", "authkey", "authorization", "cookie", "bearer", "private_key",
		"private-key", "private key", "privatekey", "ssh-rsa", "ssh-ed25519", "ecdsa-sha2", "\"message", "\"prompt\"",
		"\"content\"", "\"payload\"", "\"body\"", "?key=", "&key=", "?signature=", "&signature=", "?sig=", "&sig="};
	std::string lowered = lower_for_words(line);
	for (auto w : words)
		if (lowered.find(w) != std::string::npos) return true;
	return lowered.find("http") != std::string::npos && sensitive_entry(fold(line));
}

// pemMarker's first match: -----\s*(?:BEGIN|END) [A-Z0-9 ]+-----
std::string_view pem_marker(std::string_view line)
{
	for (std::size_t i = line.find("-----"); i != std::string_view::npos; i = line.find("-----", i + 1)) {
		std::size_t j = i + 5;
		while (j < line.size() && re2_space(static_cast<unsigned char>(line[j]))) j++;
		std::size_t k;
		if (line.substr(j, 6) == "BEGIN ") k = j + 6;
		else if (line.substr(j, 4) == "END ") k = j + 4;
		else continue;
		std::size_t e = k;
		while (e < line.size() && ((line[e] >= 'A' && line[e] <= 'Z') || (line[e] >= '0' && line[e] <= '9') || line[e] == ' ')) e++;
		if (e > k && line.substr(e, 5) == "-----") return line.substr(i, e + 5 - i);
	}
	return {};
}

// encodedKeyLine: ^\s*[A-Za-z0-9+/]{32,}={0,3}\s*$
bool encoded_key_line(std::string_view line)
{
	std::size_t i = 0, n = line.size();
	while (i < n && re2_space(static_cast<unsigned char>(line[i]))) i++;
	std::size_t start = i;
	while (i < n && (std::isalnum(static_cast<unsigned char>(line[i])) || line[i] == '+' || line[i] == '/')) i++;
	if (i - start < 32) return false;
	std::size_t eq = 0;
	while (i < n && line[i] == '=' && eq < 3) i++, eq++;
	while (i < n && re2_space(static_cast<unsigned char>(line[i]))) i++;
	return i == n;
}

} // namespace

Sanitized sanitize_tail(std::string_view data, bool truncated)
{
	if (truncated) {
		auto nl = data.find('\n');
		data = nl == std::string_view::npos ? std::string_view{} : data.substr(nl + 1);
	}
	std::string text = to_valid_utf8(data);
	if (text.find('\x1b') != std::string::npos) text = strip_escapes(text);
	if (needs_rune_filter(text)) text = filter_runes(text);
	// An unfinished last entry is not shown: a secret's label may arrive with the next write.
	auto last = text.rfind('\n');
	text = last == std::string::npos ? std::string() : text.substr(0, last);
	if (text.empty()) return {"", 0, truncated};

	std::vector<std::string> lines;
	for (std::size_t start = 0;;) {
		auto nl = text.find('\n', start);
		lines.emplace_back(text.substr(start, nl == std::string::npos ? std::string::npos : nl - start));
		if (nl == std::string::npos) break;
		start = nl + 1;
	}
	bool in_pem = false;
	for (auto &line : lines) {
		std::string_view marker = line.find("-----") != std::string::npos ? pem_marker(line) : std::string_view{};
		bool hidden = in_pem || !marker.empty() || sensitive_line(line) || encoded_key_line(line);
		if (marker.find("BEGIN") != std::string_view::npos) in_pem = true;
		if (marker.find("END") != std::string_view::npos) in_pem = false;
		bool too_long = line.size() > 4096;
		if (too_long) truncated = true;
		if (hidden) line = redacted_line;
		else if (too_long) line = long_line;
	}
	if (lines.size() > max_lines) {
		lines.erase(lines.begin(), lines.end() - static_cast<long>(max_lines));
		truncated = true;
	}
	// Redaction may lengthen short entries: the byte bound applies again.
	std::size_t bytes = 0, start = lines.size();
	while (start > 0 && bytes + lines[start - 1].size() + 1 <= max_bytes) {
		start--;
		bytes += lines[start].size() + 1;
	}
	if (start > 0) truncated = true;
	Sanitized out;
	for (std::size_t i = start; i < lines.size(); i++) {
		if (i > start) out.content += '\n';
		out.content += lines[i];
	}
	out.lines = static_cast<int>(lines.size() - start);
	out.truncated = truncated;
	return out;
}

} // namespace nkos::system::logs
