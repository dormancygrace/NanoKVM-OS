#include "ai/json.hpp"

#include "common/json_depth.hpp"

#include <algorithm>
#include <charconv>
#include <cmath>
#include <cstring>
#include <map>

namespace nkos::ai::json {

Value Value::boolean(bool v)
{
	Value r;
	r.kind = Kind::boolean;
	r.b = v;
	return r;
}

Value Value::number(double v)
{
	Value r;
	r.kind = Kind::number;
	r.num = v;
	r.str = float_json(v);
	return r;
}

Value Value::string(std::string v)
{
	Value r;
	r.kind = Kind::string;
	r.str = std::move(v);
	return r;
}

Value Value::array(std::vector<Value> items)
{
	Value r;
	r.kind = Kind::array;
	r.arr = std::move(items);
	return r;
}

Value Value::object()
{
	Value r;
	r.kind = Kind::object;
	return r;
}

const Value *Value::get(std::string_view key) const
{
	if (kind != Kind::object) return nullptr;
	for (auto it = obj.rbegin(); it != obj.rend(); ++it)
		if (it->first == key) return &it->second;
	return nullptr;
}

Value *Value::get(std::string_view key)
{
	return const_cast<Value *>(static_cast<const Value *>(this)->get(key));
}

Value &Value::set(std::string_view key, Value v)
{
	if (kind != Kind::object) {
		*this = object();
	}
	for (auto it = obj.rbegin(); it != obj.rend(); ++it)
		if (it->first == key) {
			it->second = std::move(v);
			return it->second;
		}
	obj.emplace_back(std::string(key), std::move(v));
	return obj.back().second;
}

bool Value::erase(std::string_view key)
{
	auto n = obj.size();
	std::erase_if(obj, [&](const auto &m) { return m.first == key; });
	return obj.size() != n;
}

// --- UTF-8 ---

void append_utf8(std::string &out, char32_t r)
{
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

namespace {

// One rune at s[i]: its value and length; invalid: U+FFFD and length 1 (utf8.DecodeRuneInString).
std::pair<char32_t, std::size_t> decode(std::string_view s, std::size_t i)
{
	unsigned char c = static_cast<unsigned char>(s[i]);
	if (c < 0x80) return {c, 1};
	int n = c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 0;
	if (c >= 0xf5) n = 0;
	if (n == 0 || i + static_cast<std::size_t>(n) > s.size()) {
		// A truncated sequence may still be valid as far as it goes; Go reports one byte either way.
		return {0xfffd, 1};
	}
	char32_t r = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
	for (int k = 1; k < n; k++) {
		unsigned char d = static_cast<unsigned char>(s[i + static_cast<std::size_t>(k)]);
		if ((d & 0xc0) != 0x80) return {0xfffd, 1};
		r = (r << 6) | (d & 0x3f);
	}
	if ((n == 3 && (r < 0x800 || (r >= 0xd800 && r <= 0xdfff))) || (n == 4 && (r < 0x10000 || r > 0x10ffff)))
		return {0xfffd, 1};
	return {r, static_cast<std::size_t>(n)};
}

} // namespace

std::vector<char32_t> runes(std::string_view s)
{
	std::vector<char32_t> out;
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode(s, i);
		out.push_back(r);
		i += n;
	}
	return out;
}

std::string utf8(const std::vector<char32_t> &rs)
{
	std::string out;
	for (char32_t r : rs) append_utf8(out, r);
	return out;
}

namespace {

bool unicode_space(char32_t r)
{
	switch (r) {
	case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xa0: case 0x1680:
	case 0x2028: case 0x2029: case 0x202f: case 0x205f: case 0x3000:
		return true;
	default:
		return r >= 0x2000 && r <= 0x200a;
	}
}

} // namespace

std::string trim_space(std::string_view s)
{
	std::size_t a = 0, b = s.size();
	while (a < b) {
		auto [r, n] = decode(s, a);
		if (!unicode_space(r) || (r == 0xfffd && n == 1 && static_cast<unsigned char>(s[a]) >= 0x80)) break;
		a += n;
	}
	while (b > a) {
		// Step back over one rune.
		std::size_t start = b - 1;
		while (start > a && (static_cast<unsigned char>(s[start]) & 0xc0) == 0x80 && b - start < 4) start--;
		auto [r, n] = decode(s, start);
		if (start + n != b) {
			start = b - 1;
			r = 0xfffd;
		}
		if (!unicode_space(r) || (r == 0xfffd && static_cast<unsigned char>(s[start]) >= 0x80)) break;
		b = start;
	}
	return std::string(s.substr(a, b - a));
}

std::string to_lower(std::string_view s)
{
	std::string out(s);
	for (auto &c : out)
		if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
	return out;
}

std::string to_upper(std::string_view s)
{
	std::string out(s);
	for (auto &c : out)
		if (c >= 'a' && c <= 'z') c = static_cast<char>(c - 'a' + 'A');
	return out;
}

// --- Parsing ---

namespace {

struct Parser {
	std::string_view s;
	std::size_t i = 0;
	std::optional<ParseError> error;

	void fail_eof()
	{
		if (!error) error = ParseError{ParseError::Kind::eof, s.size(), "unexpected end of JSON input"};
	}
	void fail_char(std::string_view context)
	{
		if (error) return;
		std::string ch;
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c == '\'') ch = "'\\''";
		else if (c == '"') ch = "'\"'";
		else if (c < 0x20 || c >= 0x7f) {
			char buf[8];
			std::snprintf(buf, sizeof buf, "'\\x%02x'", c);
			ch = buf;
			if (c >= 0x80) {
				auto [r, n] = decode(s, i);
				(void)n;
				std::string q = "'";
				append_utf8(q, r);
				ch = q + "'";
			}
		} else ch = std::string("'") + static_cast<char>(c) + "'";
		error = ParseError{ParseError::Kind::syntax, i, "invalid character " + ch + " " + std::string(context)};
	}
	void ws()
	{
		while (i < s.size() && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r')) i++;
	}
	bool value(Value &v, int depth)
	{
		ws();
		if (i >= s.size()) {
			fail_eof();
			return false;
		}
		if (depth >= max_json_depth) {
			error = ParseError{ParseError::Kind::syntax, i, "exceeded max depth"};
			return false;
		}
		v.offset = i;
		char c = s[i];
		bool ok;
		switch (c) {
		case '{': ok = object(v, depth); break;
		case '[': ok = array(v, depth); break;
		case '"':
			v.kind = Value::Kind::string;
			ok = string(v.str);
			break;
		case 't': ok = literal("true", v, Value::Kind::boolean, true); break;
		case 'f': ok = literal("false", v, Value::Kind::boolean, false); break;
		case 'n': ok = literal("null", v, Value::Kind::null, false); break;
		default:
			if (c == '-' || (c >= '0' && c <= '9')) ok = number(v);
			else {
				fail_char("looking for beginning of value");
				ok = false;
			}
		}
		v.end = i;
		return ok;
	}
	bool literal(std::string_view word, Value &v, Value::Kind kind, bool b)
	{
		for (std::size_t k = 0; k < word.size(); k++, i++) {
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			if (s[i] != word[k]) {
				fail_char(std::string("in literal ") + std::string(word) + " (expecting '" + word[k] + "')");
				return false;
			}
		}
		v.kind = kind;
		v.b = b;
		return true;
	}
	bool number(Value &v)
	{
		std::size_t start = i;
		if (s[i] == '-') {
			i++;
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			if (!(s[i] >= '0' && s[i] <= '9')) {
				fail_char("in numeric literal");
				return false;
			}
		}
		if (s[i] == '0') i++;
		else while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		if (i < s.size() && s[i] == '.') {
			i++;
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			if (!(s[i] >= '0' && s[i] <= '9')) {
				fail_char("after decimal point in numeric literal");
				return false;
			}
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		}
		if (i < s.size() && (s[i] == 'e' || s[i] == 'E')) {
			i++;
			if (i < s.size() && (s[i] == '+' || s[i] == '-')) i++;
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			if (!(s[i] >= '0' && s[i] <= '9')) {
				fail_char("in exponent of numeric literal");
				return false;
			}
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		}
		v.kind = Value::Kind::number;
		v.str = std::string(s.substr(start, i - start));
		double d = 0;
		auto r = std::from_chars(v.str.data(), v.str.data() + v.str.size(), d);
		if (r.ec == std::errc::result_out_of_range) d = v.str[0] == '-' ? -HUGE_VAL : HUGE_VAL;
		v.num = d;
		return true;
	}
	bool hex4(char32_t &out)
	{
		out = 0;
		for (int k = 0; k < 4; k++, i++) {
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			char c = s[i];
			int d = c >= '0' && c <= '9' ? c - '0' : c >= 'a' && c <= 'f' ? c - 'a' + 10 : c >= 'A' && c <= 'F' ? c - 'A' + 10 : -1;
			if (d < 0) {
				fail_char("in \\u hexadecimal character escape");
				return false;
			}
			out = out * 16 + static_cast<char32_t>(d);
		}
		return true;
	}
	bool string(std::string &out)
	{
		i++;  // opening quote
		out.clear();
		while (true) {
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			unsigned char c = static_cast<unsigned char>(s[i]);
			if (c == '"') {
				i++;
				return true;
			}
			if (c < 0x20) {
				fail_char("in string literal");
				return false;
			}
			if (c == '\\') {
				i++;
				if (i >= s.size()) {
					fail_eof();
					return false;
				}
				char e = s[i];
				switch (e) {
				case '"': out += '"'; i++; break;
				case '\\': out += '\\'; i++; break;
				case '/': out += '/'; i++; break;
				case 'b': out += '\b'; i++; break;
				case 'f': out += '\f'; i++; break;
				case 'n': out += '\n'; i++; break;
				case 'r': out += '\r'; i++; break;
				case 't': out += '\t'; i++; break;
				case 'u': {
					i++;
					char32_t r;
					if (!hex4(r)) return false;
					if (r >= 0xd800 && r < 0xdc00) {
						// A surrogate pair, or U+FFFD for a lone one.
						if (i + 1 < s.size() && s[i] == '\\' && s[i + 1] == 'u') {
							std::size_t save = i;
							i += 2;
							char32_t r2;
							if (!hex4(r2)) return false;
							if (r2 >= 0xdc00 && r2 < 0xe000) {
								r = 0x10000 + ((r - 0xd800) << 10) + (r2 - 0xdc00);
							} else {
								r = 0xfffd;
								i = save;
							}
						} else {
							r = 0xfffd;
						}
					} else if (r >= 0xdc00 && r < 0xe000) {
						r = 0xfffd;
					}
					append_utf8(out, r);
					break;
				}
				default:
					fail_char("in string escape code");
					return false;
				}
				continue;
			}
			if (c < 0x80) {
				out += static_cast<char>(c);
				i++;
				continue;
			}
			auto [r, n] = decode(s, i);
			if (r == 0xfffd && n == 1) out += "\xef\xbf\xbd";
			else out.append(s.substr(i, n));
			i += n;
		}
	}
	bool object(Value &v, int depth)
	{
		v.kind = Value::Kind::object;
		i++;
		ws();
		if (i >= s.size()) {
			fail_eof();
			return false;
		}
		if (s[i] == '}') {
			i++;
			return true;
		}
		while (true) {
			ws();
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			if (s[i] != '"') {
				fail_char("looking for beginning of object key string");
				return false;
			}
			std::string key;
			if (!string(key)) return false;
			ws();
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			if (s[i] != ':') {
				fail_char("after object key");
				return false;
			}
			i++;
			Value member;
			if (!value(member, depth + 1)) return false;
			v.obj.emplace_back(std::move(key), std::move(member));
			ws();
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			if (s[i] == ',') {
				i++;
				continue;
			}
			if (s[i] == '}') {
				i++;
				return true;
			}
			fail_char("after object key:value pair");
			return false;
		}
	}
	bool array(Value &v, int depth)
	{
		v.kind = Value::Kind::array;
		i++;
		ws();
		if (i >= s.size()) {
			fail_eof();
			return false;
		}
		if (s[i] == ']') {
			i++;
			return true;
		}
		while (true) {
			Value item;
			if (!value(item, depth + 1)) return false;
			v.arr.push_back(std::move(item));
			ws();
			if (i >= s.size()) {
				fail_eof();
				return false;
			}
			if (s[i] == ',') {
				i++;
				continue;
			}
			if (s[i] == ']') {
				i++;
				return true;
			}
			fail_char("after array element");
			return false;
		}
	}
};

} // namespace

std::optional<ParseError> parse(std::string_view text, Value &out)
{
	Parser p;
	p.s = text;
	out = Value();
	if (!p.value(out, 0)) return p.error;
	p.ws();
	if (p.i < text.size()) {
		p.fail_char("after top-level value");
		return p.error;
	}
	return std::nullopt;
}

std::optional<ParseError> parse_first(std::string_view text, Value &out)
{
	Parser p;
	p.s = text;
	out = Value();
	if (!p.value(out, 0)) return p.error;
	return std::nullopt;
}

std::string compact(std::string_view raw)
{
	std::string out;
	bool in_string = false;
	for (std::size_t i = 0; i < raw.size(); i++) {
		char c = raw[i];
		if (in_string) {
			out += c;
			if (c == '\\' && i + 1 < raw.size()) out += raw[++i];
			else if (c == '"') in_string = false;
			continue;
		}
		if (c == ' ' || c == '\t' || c == '\n' || c == '\r') continue;
		if (c == '"') in_string = true;
		out += c;
	}
	return out;
}

// --- Writing ---

std::string quote(std::string_view s, bool html)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
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
			default:
				if (c < 0x20 || (html && (c == '<' || c == '>' || c == '&'))) {
					out += "\\u00";
					out += hex[c >> 4];
					out += hex[c & 15];
				} else {
					out += static_cast<char>(c);
				}
			}
			i++;
			continue;
		}
		auto [r, n] = decode(s, i);
		if (r == 0xfffd && n == 1) out += "\\ufffd";
		else if (r == 0x2028) out += "\\u2028";
		else if (r == 0x2029) out += "\\u2029";
		else out.append(s.substr(i, n));
		i += n;
	}
	return out + "\"";
}

namespace {

void write(std::string &out, const Value &v, bool html, std::string_view indent, int level)
{
	auto newline = [&](int l) {
		if (indent.empty()) return;
		out += '\n';
		for (int k = 0; k < l; k++) out += indent;
	};
	switch (v.kind) {
	case Value::Kind::null: out += "null"; break;
	case Value::Kind::boolean: out += v.b ? "true" : "false"; break;
	case Value::Kind::number: out += float_json(v.num); break;
	case Value::Kind::string: out += quote(v.str, html); break;
	case Value::Kind::array:
		if (v.arr.empty()) {
			out += "[]";
			break;
		}
		out += '[';
		for (std::size_t k = 0; k < v.arr.size(); k++) {
			if (k) out += ',';
			newline(level + 1);
			write(out, v.arr[k], html, indent, level + 1);
		}
		newline(level);
		out += ']';
		break;
	case Value::Kind::object:
		if (v.obj.empty()) {
			out += "{}";
			break;
		}
		out += '{';
		for (std::size_t k = 0; k < v.obj.size(); k++) {
			if (k) out += ',';
			newline(level + 1);
			out += quote(v.obj[k].first, html);
			out += indent.empty() ? ":" : ": ";
			write(out, v.obj[k].second, html, indent, level + 1);
		}
		newline(level);
		out += '}';
		break;
	}
}

} // namespace

std::string marshal(const Value &v, bool html)
{
	std::string out;
	write(out, v, html, {}, 0);
	return out;
}

std::string marshal_indent(const Value &v, std::string_view indent, bool html)
{
	std::string out;
	write(out, v, html, indent, 0);
	return out;
}

void sort_keys(Value &v)
{
	if (v.kind == Value::Kind::array) {
		for (auto &x : v.arr) sort_keys(x);
		return;
	}
	if (v.kind != Value::Kind::object) return;
	std::map<std::string, Value> m;
	for (auto &[k, x] : v.obj) m[k] = std::move(x);
	v.obj.clear();
	for (auto &[k, x] : m) {
		sort_keys(x);
		v.obj.emplace_back(k, std::move(x));
	}
}

// --- Go's number formatting ---

namespace {

// Shortest round-trip digits and decimal exponent: value = 0.d1d2... * 10^dp.
void shortest(double f, std::string &digits, int &dp)
{
	char buf[64];
	auto r = std::to_chars(buf, buf + sizeof buf, std::abs(f), std::chars_format::scientific);
	std::string sci(buf, r.ptr);
	auto e = sci.find('e');
	std::string mant = sci.substr(0, e);
	int exp = std::stoi(sci.substr(e + 1));
	digits.clear();
	for (char c : mant)
		if (c != '.') digits += c;
	while (digits.size() > 1 && digits.back() == '0') digits.pop_back();
	dp = exp + 1;
}

std::string exp_form(double f, const std::string &digits, int dp, bool two_digit_exp)
{
	std::string out = f < 0 ? "-" : "";
	out += digits[0];
	if (digits.size() > 1) out += "." + digits.substr(1);
	int e = dp - 1;
	out += e < 0 ? "e-" : "e+";
	int a = std::abs(e);
	if (two_digit_exp && a < 10) out += '0';
	out += std::to_string(a);
	return out;
}

std::string fixed_form(double f, const std::string &digits, int dp)
{
	std::string out = f < 0 ? "-" : "";
	if (dp <= 0) {
		out += "0." + std::string(static_cast<std::size_t>(-dp), '0') + digits;
	} else if (static_cast<std::size_t>(dp) >= digits.size()) {
		out += digits + std::string(static_cast<std::size_t>(dp) - digits.size(), '0');
	} else {
		out += digits.substr(0, static_cast<std::size_t>(dp)) + "." + digits.substr(static_cast<std::size_t>(dp));
	}
	return out;
}

} // namespace

std::string float_json(double f)
{
	if (f == 0) return std::signbit(f) ? "-0" : "0";
	std::string digits;
	int dp;
	shortest(f, digits, dp);
	double a = std::abs(f);
	if (a < 1e-6 || a >= 1e21) {
		// strconv 'e', then e-07 cleaned to e-7.
		return exp_form(f, digits, dp, false);
	}
	return fixed_form(f, digits, dp);
}

std::string float_v(double f)
{
	if (std::isnan(f)) return "NaN";
	if (std::isinf(f)) return f > 0 ? "+Inf" : "-Inf";
	if (f == 0) return std::signbit(f) ? "-0" : "0";
	std::string digits;
	int dp;
	shortest(f, digits, dp);
	// %v is strconv's shortest 'g': the exponent form below 1e-4 and from 1e6 on.
	int e = dp - 1;
	if (e < -4 || e >= 6) return exp_form(f, digits, dp, true);
	return fixed_form(f, digits, dp);
}

std::string float_f(double f)
{
	char buf[512];
	std::snprintf(buf, sizeof buf, "%.6f", f);
	return buf;
}

std::string float_rat(double f)
{
	if (f == 0) return "0/1";
	int exp;
	double m = std::frexp(std::abs(f), &exp);  // f = m * 2^exp, m in [0.5, 1)
	// 53-bit integer mantissa.
	unsigned __int128 num = static_cast<unsigned __int128>(std::ldexp(m, 53));
	int e = exp - 53;
	unsigned __int128 den = 1;
	if (e >= 0) {
		if (e > 74) return float_v(f);  // beyond 128 bits: not met by the values used here
		num <<= e;
	} else {
		while (e < 0 && (num & 1) == 0) {
			num >>= 1;
			e++;
		}
		if (-e > 126) return float_v(f);
		den <<= -e;
	}
	auto str = [](unsigned __int128 v) {
		if (v == 0) return std::string("0");
		std::string s;
		while (v) {
			s += static_cast<char>('0' + static_cast<int>(v % 10));
			v /= 10;
		}
		std::reverse(s.begin(), s.end());
		return s;
	};
	return std::string(f < 0 ? "-" : "") + str(num) + "/" + str(den);
}

std::string go_v(const Value &v)
{
	switch (v.kind) {
	case Value::Kind::null: return "<nil>";
	case Value::Kind::boolean: return v.b ? "true" : "false";
	case Value::Kind::number: return float_v(v.num);
	case Value::Kind::string: return v.str;
	case Value::Kind::array: {
		std::string out = "[";
		for (std::size_t k = 0; k < v.arr.size(); k++) {
			if (k) out += ' ';
			out += go_v(v.arr[k]);
		}
		return out + "]";
	}
	case Value::Kind::object: {
		Value sorted = v;
		sort_keys(sorted);
		std::string out = "map[";
		for (std::size_t k = 0; k < sorted.obj.size(); k++) {
			if (k) out += ' ';
			out += sorted.obj[k].first + ":" + go_v(sorted.obj[k].second);
		}
		return out + "]";
	}
	}
	return {};
}

std::string go_quote(std::string_view s)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode(s, i);
		if (r == 0xfffd && n == 1) {
			unsigned char c = static_cast<unsigned char>(s[i]);
			out += "\\x";
			out += hex[c >> 4];
			out += hex[c & 15];
			i++;
			continue;
		}
		i += n;
		switch (r) {
		case '"': out += "\\\""; continue;
		case '\\': out += "\\\\"; continue;
		case '\a': out += "\\a"; continue;
		case '\b': out += "\\b"; continue;
		case '\f': out += "\\f"; continue;
		case '\n': out += "\\n"; continue;
		case '\r': out += "\\r"; continue;
		case '\t': out += "\\t"; continue;
		case '\v': out += "\\v"; continue;
		default: break;
		}
		if (r < 0x20 || r == 0x7f) {
			out += "\\x";
			out += hex[r >> 4];
			out += hex[r & 15];
		} else if (r == 0xfeff || (r >= 0x80 && r < 0xa0) || r == 0xad || r == 0x2028 || r == 0x2029 ||
		           (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2060 && r <= 0x206f)) {
			// Not printable for strconv.IsPrint: escaped.
			char buf[16];
			std::snprintf(buf, sizeof buf, r > 0xffff ? "\\U%08x" : "\\u%04x", static_cast<unsigned>(r));
			out += buf;
		} else {
			append_utf8(out, r);
		}
	}
	return out + "\"";
}

} // namespace nkos::ai::json
