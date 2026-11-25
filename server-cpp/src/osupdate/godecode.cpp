#include "osupdate/godecode.hpp"

#include "common/base64.hpp"
#include "common/json_depth.hpp"

#include <charconv>

namespace nkos::godecode {

namespace {

bool space(char c) { return c == ' ' || c == '\t' || c == '\r' || c == '\n'; }

void put_utf8(std::string &out, unsigned cp)
{
	if (cp < 0x80) {
		out += static_cast<char>(cp);
	} else if (cp < 0x800) {
		out += static_cast<char>(0xc0 | (cp >> 6));
		out += static_cast<char>(0x80 | (cp & 0x3f));
	} else if (cp < 0x10000) {
		out += static_cast<char>(0xe0 | (cp >> 12));
		out += static_cast<char>(0x80 | ((cp >> 6) & 0x3f));
		out += static_cast<char>(0x80 | (cp & 0x3f));
	} else {
		out += static_cast<char>(0xf0 | (cp >> 18));
		out += static_cast<char>(0x80 | ((cp >> 12) & 0x3f));
		out += static_cast<char>(0x80 | ((cp >> 6) & 0x3f));
		out += static_cast<char>(0x80 | (cp & 0x3f));
	}
}

// The length of a valid UTF-8 sequence at s[i], or 0.
std::size_t utf8_length(std::string_view s, std::size_t i)
{
	unsigned char c = static_cast<unsigned char>(s[i]);
	std::size_t n = c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 ? 2 : 0;
	if (c >= 0xf5) n = 0;
	if (n == 0 || i + n > s.size()) return 0;
	unsigned cp = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
	for (std::size_t k = 1; k < n; k++) {
		unsigned char cc = static_cast<unsigned char>(s[i + k]);
		if ((cc & 0xc0) != 0x80) return 0;
		cp = (cp << 6) | (cc & 0x3f);
	}
	if ((n == 3 && (cp < 0x800 || (cp >= 0xd800 && cp <= 0xdfff))) || (n == 4 && (cp < 0x10000 || cp > 0x10ffff)))
		return 0;
	return n;
}

int hex4(std::string_view s, std::size_t i)
{
	if (i + 4 > s.size()) return -1;
	int v = 0;
	for (std::size_t k = 0; k < 4; k++) {
		char c = s[i + k];
		int d = c >= '0' && c <= '9' ? c - '0' : c >= 'a' && c <= 'f' ? c - 'a' + 10 : c >= 'A' && c <= 'F' ? c - 'A' + 10 : -1;
		if (d < 0) return -1;
		v = v * 16 + d;
	}
	return v;
}

// The offending character as Go's decoder names it: the rune at that place quoted as strconv.QuoteRune does, or
// '\xNN' for a byte that does not start valid UTF-8.
std::string quote_char(std::string_view s, std::size_t pos)
{
	static const char hex[] = "0123456789abcdef";
	unsigned char c = static_cast<unsigned char>(s[pos]);
	std::string q;
	auto byte = [&](unsigned char b) { return std::string("\\x") + hex[b >> 4] + hex[b & 15]; };
	switch (c) {
	case '\'': q = "\\'"; break;
	case '\a': q = "\\a"; break;
	case '\b': q = "\\b"; break;
	case '\f': q = "\\f"; break;
	case '\n': q = "\\n"; break;
	case '\r': q = "\\r"; break;
	case '\t': q = "\\t"; break;
	case '\v': q = "\\v"; break;
	case '\\': q = "\\\\"; break;
	default:
		if (c < 0x20 || c == 0x7f) {
			q = byte(c);
		} else if (c < 0x80) {
			q = std::string(1, static_cast<char>(c));
		} else if (std::size_t n = utf8_length(s, pos)) {
			unsigned cp = n == 2 ? c & 31 : n == 3 ? c & 15 : c & 7;
			for (std::size_t k = 1; k < n; k++) cp = (cp << 6) | (static_cast<unsigned char>(s[pos + k]) & 0x3f);
			if (cp < 0xa1 || cp == 0xad) q = std::string("\\u00") + hex[cp >> 4] + hex[cp & 15];
			else q = std::string(s.substr(pos, n));
		} else {
			q = byte(c);
		}
	}
	return "'" + q + "'";
}

class Parser {
public:
	explicit Parser(std::string_view s) : s_(s) {}
	std::size_t pos = 0;
	std::string error;  // Go's SyntaxError text, or "" with eof set
	bool eof = false;   // the input ended inside the value

	void skip()
	{
		while (pos < s_.size() && space(s_[pos])) pos++;
	}

	bool value(Value &out, int depth)
	{
		if (depth >= max_json_depth) return fail("exceeded max depth");
		skip();
		if (pos >= s_.size()) return end();
		char c = s_[pos];
		if (c == '{') return object(out, depth);
		if (c == '[') return array(out, depth);
		if (c == '"') {
			out.kind = Value::Kind::string;
			return string(out.text);
		}
		if (c == '-' || (c >= '0' && c <= '9')) return number(out);
		if (c == 't') return literal("true", out, Value::Kind::boolean, true);
		if (c == 'f') return literal("false", out, Value::Kind::boolean, false);
		if (c == 'n') return literal("null", out, Value::Kind::null, false);
		return bad("looking for beginning of value");
	}

private:
	std::string_view s_;

	bool end() { return eof = true, false; }
	bool fail(std::string text) { return error = std::move(text), false; }
	bool bad(std::string_view context)
	{
		return fail("invalid character " + quote_char(s_, pos) + " " + std::string(context));
	}

	bool literal(std::string_view word, Value &out, Value::Kind kind, bool flag)
	{
		for (std::size_t k = 1; k < word.size(); k++) {
			if (pos + k >= s_.size()) return pos += k, end();
			if (s_[pos + k] != word[k]) {
				pos += k;
				return bad("in literal " + std::string(word) + " (expecting '" + std::string(1, word[k]) + "')");
			}
		}
		pos += word.size();
		out.kind = kind;
		out.flag = flag;
		return true;
	}

	bool object(Value &out, int depth)
	{
		out.kind = Value::Kind::object;
		pos++;
		skip();
		if (pos >= s_.size()) return end();
		if (s_[pos] == '}') return pos++, true;
		for (;;) {
			skip();
			if (pos >= s_.size()) return end();
			if (s_[pos] != '"') return bad("looking for beginning of object key string");
			std::string key;
			if (!string(key)) return false;
			skip();
			if (pos >= s_.size()) return end();
			if (s_[pos] != ':') return bad("after object key");
			pos++;
			Value v;
			if (!value(v, depth + 1)) return false;
			out.members.emplace_back(std::move(key), std::move(v));
			skip();
			if (pos >= s_.size()) return end();
			if (s_[pos] == ',') {
				pos++;
				continue;
			}
			if (s_[pos] == '}') return pos++, true;
			return bad("after object key:value pair");
		}
	}

	bool array(Value &out, int depth)
	{
		out.kind = Value::Kind::array;
		pos++;
		skip();
		if (pos >= s_.size()) return end();
		if (s_[pos] == ']') return pos++, true;
		for (;;) {
			Value v;
			if (!value(v, depth + 1)) return false;
			out.items.push_back(std::move(v));
			skip();
			if (pos >= s_.size()) return end();
			if (s_[pos] == ',') {
				pos++;
				continue;
			}
			if (s_[pos] == ']') return pos++, true;
			return bad("after array element");
		}
	}

	bool digit() const { return pos < s_.size() && s_[pos] >= '0' && s_[pos] <= '9'; }

	bool number(Value &out)
	{
		std::size_t start = pos;
		if (s_[pos] == '-') {
			pos++;
			if (pos >= s_.size()) return end();
			if (!digit()) return bad("in numeric literal");
		}
		if (s_[pos] == '0') {
			pos++;
		} else {
			while (digit()) pos++;
		}
		if (pos < s_.size() && s_[pos] == '.') {
			pos++;
			if (pos >= s_.size()) return end();
			if (!digit()) return bad("in numeric literal");
			while (digit()) pos++;
		}
		if (pos < s_.size() && (s_[pos] == 'e' || s_[pos] == 'E')) {
			pos++;
			if (pos < s_.size() && (s_[pos] == '+' || s_[pos] == '-')) pos++;
			if (pos >= s_.size()) return end();
			if (!digit()) return bad("in numeric literal");
			while (digit()) pos++;
		}
		out.kind = Value::Kind::number;
		out.text = std::string(s_.substr(start, pos - start));
		return true;
	}

	bool string(std::string &out)
	{
		pos++;
		for (;;) {
			if (pos >= s_.size()) return end();
			unsigned char c = static_cast<unsigned char>(s_[pos]);
			if (c == '"') return pos++, true;
			if (c < 0x20) return bad("in string");
			if (c == '\\') {
				pos++;
				if (pos >= s_.size()) return end();
				char e = s_[pos++];
				switch (e) {
				case '"': out += '"'; break;
				case '\\': out += '\\'; break;
				case '/': out += '/'; break;
				case 'b': out += '\b'; break;
				case 'f': out += '\f'; break;
				case 'n': out += '\n'; break;
				case 'r': out += '\r'; break;
				case 't': out += '\t'; break;
				case 'u': {
					for (std::size_t k = 0; k < 4; k++)
						if (pos + k >= s_.size()) return pos += k, end();
					for (std::size_t k = 0; k < 4; k++) {
						char h = s_[pos + k];
						if (!((h >= '0' && h <= '9') || (h >= 'a' && h <= 'f') || (h >= 'A' && h <= 'F')))
							return fail("invalid escape sequence `\\u" + std::string(s_.substr(pos, 4)) + "` in string");
					}
					int cp = hex4(s_, pos);
					pos += 4;
					if (cp >= 0xd800 && cp < 0xe000) {
						// A surrogate pair, or U+FFFD for a lone half (the next escape is then read on its own).
						int low = -1;
						if (cp < 0xdc00 && pos + 1 < s_.size() && s_[pos] == '\\' && s_[pos + 1] == 'u') low = hex4(s_, pos + 2);
						if (low >= 0xdc00 && low < 0xe000) {
							pos += 6;
							put_utf8(out, 0x10000 + ((static_cast<unsigned>(cp) - 0xd800) << 10) + (static_cast<unsigned>(low) - 0xdc00));
						} else {
							put_utf8(out, 0xfffd);
						}
					} else {
						put_utf8(out, static_cast<unsigned>(cp));
					}
					break;
				}
				default: return fail("invalid escape sequence `\\" + std::string(1, e) + "` in string");
				}
				continue;
			}
			if (c < 0x80) {
				out += static_cast<char>(c);
				pos++;
				continue;
			}
			std::size_t n = utf8_length(s_, pos);
			if (n == 0) {
				put_utf8(out, 0xfffd);
				pos++;
			} else {
				out.append(s_.substr(pos, n));
				pos += n;
			}
		}
	}
};

bool all_digits(std::string_view s)
{
	if (s.empty()) return false;
	for (char c : s)
		if (c < '0' || c > '9') return false;
	return true;
}

int days_in(int month, int year)
{
	static const int days[] = {31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31};
	if (month == 2 && year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)) return 29;
	return days[month - 1];
}

} // namespace

bool parse(std::string_view data, Value &out, std::size_t &end)
{
	Parser p(data);
	out = Value{};
	if (!p.value(out, 0)) return false;
	end = p.pos;
	return true;
}

std::optional<Value> parse_document(std::string_view data)
{
	std::string error;
	return unmarshal(data, error);
}

std::optional<Value> unmarshal(std::string_view data, std::string &error)
{
	Parser p(data);
	Value v;
	if (!p.value(v, 0)) {
		error = p.eof ? "unexpected end of JSON input" : p.error;
		return std::nullopt;
	}
	p.skip();
	if (p.pos < data.size()) {
		error = "invalid character " + quote_char(data, p.pos) + " after top-level value";
		return std::nullopt;
	}
	return v;
}

std::optional<Value> decode_first(std::string_view data, std::string &error)
{
	Parser p(data);
	p.skip();
	if (p.pos >= data.size()) {
		error = "EOF";
		return std::nullopt;
	}
	Value v;
	if (!p.value(v, 0)) {
		error = p.eof ? "unexpected EOF" : p.error;
		return std::nullopt;
	}
	return v;
}

std::string kind_name(const Value &v)
{
	switch (v.kind) {
	case Value::Kind::null: return "null";
	case Value::Kind::boolean: return "bool";
	case Value::Kind::number: return "number";
	case Value::Kind::string: return "string";
	case Value::Kind::array: return "array";
	case Value::Kind::object: return "object";
	}
	return "";
}

bool only_space(std::string_view s)
{
	for (char c : s)
		if (!space(c)) return false;
	return true;
}

bool key_matches(std::string_view key, std::string_view field)
{
	auto lower = [](char c) { return c >= 'A' && c <= 'Z' ? static_cast<char>(c | 0x20) : c; };
	std::size_t i = 0, j = 0;
	while (i < key.size() && j < field.size()) {
		unsigned char a = static_cast<unsigned char>(key[i]);
		if (a < 0x80) {
			if (lower(key[i]) != lower(field[j])) return false;
			i++, j++;
			continue;
		}
		// U+212A KELVIN SIGN folds to k, U+017F LATIN SMALL LETTER LONG S to s.
		if (key.substr(i, 3) == "\xe2\x84\xaa" && (field[j] == 'k' || field[j] == 'K')) {
			i += 3, j++;
			continue;
		}
		if (key.substr(i, 2) == "\xc5\xbf" && (field[j] == 's' || field[j] == 'S')) {
			i += 2, j++;
			continue;
		}
		return false;
	}
	return i == key.size() && j == field.size();
}

bool get(const Value &v, std::string &out)
{
	if (v.kind == Value::Kind::null) return true;
	if (v.kind != Value::Kind::string) return false;
	out = v.text;
	return true;
}

bool get(const Value &v, bool &out)
{
	if (v.kind == Value::Kind::null) return true;
	if (v.kind != Value::Kind::boolean) return false;
	out = v.flag;
	return true;
}

bool get_int(const Value &v, std::int64_t &out, std::int64_t min, std::int64_t max)
{
	if (v.kind == Value::Kind::null) return true;
	if (v.kind != Value::Kind::number) return false;
	std::int64_t x = 0;
	auto [end, ec] = std::from_chars(v.text.data(), v.text.data() + v.text.size(), x, 10);
	if (ec != std::errc{} || end != v.text.data() + v.text.size() || x < min || x > max) return false;
	out = x;
	return true;
}

bool get_uint(const Value &v, std::uint64_t &out, std::uint64_t max)
{
	if (v.kind == Value::Kind::null) return true;
	if (v.kind != Value::Kind::number || v.text.empty() || v.text[0] == '-') return false;
	std::uint64_t x = 0;
	auto [end, ec] = std::from_chars(v.text.data(), v.text.data() + v.text.size(), x, 10);
	if (ec != std::errc{} || end != v.text.data() + v.text.size() || x > max) return false;
	out = x;
	return true;
}

bool get_bytes(const Value &v, std::optional<std::string> &out)
{
	if (v.kind == Value::Kind::null) {
		out.reset();
		return true;
	}
	if (v.kind != Value::Kind::string) return false;
	auto data = base64::decode(v.text);
	if (!data) return false;
	out = std::move(*data);
	return true;
}

bool get_strings(const Value &v, std::optional<std::vector<std::string>> &out)
{
	if (v.kind == Value::Kind::null) {
		out.reset();
		return true;
	}
	if (v.kind != Value::Kind::array) return false;
	bool ok = true;
	std::vector<std::string> list;
	for (const auto &item : v.items) {
		std::string s;
		if (!get(item, s)) ok = false;
		list.push_back(std::move(s));
	}
	out = std::move(list);
	return ok;
}

bool get_time(const Value &v, std::string &out)
{
	if (v.kind == Value::Kind::null) return true;
	if (v.kind != Value::Kind::string) return false;
	std::string_view s = v.text;
	if (s.size() < 19) return false;
	auto num = [&](std::size_t at, std::size_t n, int lo, int hi, int &x) {
		auto part = s.substr(at, n);
		if (!all_digits(part)) return false;
		x = 0;
		for (char c : part) x = x * 10 + (c - '0');
		return x >= lo && x <= hi;
	};
	int year = 0, month = 0, day = 0, hour = 0, minute = 0, second = 0;
	if (!num(0, 4, 0, 9999, year) || !num(5, 2, 1, 12, month) || !num(8, 2, 1, 31, day) || day > days_in(month, year) ||
	    !num(11, 2, 0, 23, hour) || !num(14, 2, 0, 59, minute) || !num(17, 2, 0, 59, second))
		return false;
	if (s[4] != '-' || s[7] != '-' || s[10] != 'T' || s[13] != ':' || s[16] != ':') return false;
	std::string_view rest = s.substr(19);
	if (rest.size() >= 2 && rest[0] == '.' && rest[1] >= '0' && rest[1] <= '9') {
		std::size_t n = 2;
		while (n < rest.size() && rest[n] >= '0' && rest[n] <= '9') n++;
		rest.remove_prefix(n);
	}
	if (rest != "Z") {
		int h = 0, m = 0;
		if (rest.size() != 6 || (rest[0] != '+' && rest[0] != '-') || rest[3] != ':') return false;
		s = rest;
		if (!num(1, 2, 0, 23, h) || !num(4, 2, 0, 59, m)) return false;
	}
	out = v.text;
	return true;
}

} // namespace nkos::godecode
