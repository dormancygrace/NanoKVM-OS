#include "notify/json.hpp"

#include "common/gojson.hpp"
#include "common/json_depth.hpp"
#include "notify/gostd.hpp"

#include <algorithm>
#include <charconv>

namespace nkos::notify::json {

namespace {

struct Parser {
	std::string_view s;
	std::size_t i = 0;
	int depth = 0;

	void ws()
	{
		while (i < s.size() && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r')) i++;
	}
	bool literal(std::string_view word)
	{
		if (s.substr(i, word.size()) != word) return false;
		i += word.size();
		return true;
	}
	static int hex(char c)
	{
		if (c >= '0' && c <= '9') return c - '0';
		if (c >= 'a' && c <= 'f') return c - 'a' + 10;
		if (c >= 'A' && c <= 'F') return c - 'A' + 10;
		return -1;
	}
	bool hex4(std::size_t at, char32_t &out)
	{
		if (at + 4 > s.size()) return false;
		out = 0;
		for (std::size_t k = 0; k < 4; k++) {
			int h = hex(s[at + k]);
			if (h < 0) return false;
			out = out << 4 | static_cast<char32_t>(h);
		}
		return true;
	}
	bool string(std::string &out)
	{
		i++;  // "
		for (;;) {
			if (i >= s.size()) return false;
			unsigned char c = static_cast<unsigned char>(s[i]);
			if (c == '"') {
				i++;
				return true;
			}
			if (c < 0x20) return false;
			if (c == '\\') {
				if (i + 1 >= s.size()) return false;
				char e = s[i + 1];
				i += 2;
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
					char32_t r;
					if (!hex4(i, r)) return false;
					i += 4;
					if (r >= 0xd800 && r < 0xe000) {
						// A surrogate pair, else U+FFFD.
						char32_t lo;
						if (r < 0xdc00 && i + 6 <= s.size() && s[i] == '\\' && s[i + 1] == 'u' && hex4(i + 2, lo) &&
						    lo >= 0xdc00 && lo < 0xe000) {
							r = 0x10000 + ((r - 0xd800) << 10) + (lo - 0xdc00);
							i += 6;
						} else {
							r = 0xFFFD;
						}
					}
					gostd::append_rune(out, r);
					break;
				}
				default:
					return false;
				}
				continue;
			}
			if (c < 0x80) {
				out += static_cast<char>(c);
				i++;
				continue;
			}
			auto [r, n] = gostd::decode_rune(s, i);
			if (n == 1) gostd::append_rune(out, 0xFFFD);
			else out.append(s, i, n);
			i += n;
		}
	}
	bool number(std::string &out)
	{
		std::size_t start = i;
		if (i < s.size() && s[i] == '-') i++;
		if (i >= s.size()) return false;
		if (s[i] == '0') {
			i++;
		} else if (s[i] >= '1' && s[i] <= '9') {
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		} else {
			return false;
		}
		if (i < s.size() && s[i] == '.') {
			i++;
			if (i >= s.size() || s[i] < '0' || s[i] > '9') return false;
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		}
		if (i < s.size() && (s[i] == 'e' || s[i] == 'E')) {
			i++;
			if (i < s.size() && (s[i] == '+' || s[i] == '-')) i++;
			if (i >= s.size() || s[i] < '0' || s[i] > '9') return false;
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		}
		out.assign(s.substr(start, i - start));
		return true;
	}
	bool value(Value &v)
	{
		ws();
		if (i >= s.size()) return false;
		if (++depth > max_json_depth) return false;
		bool ok = true;
		switch (s[i]) {
		case '{': {
			v.kind = Value::Kind::object;
			i++;
			ws();
			if (i < s.size() && s[i] == '}') {
				i++;
				break;
			}
			for (;;) {
				ws();
				if (i >= s.size() || s[i] != '"') return false;
				std::string key;
				if (!string(key)) return false;
				ws();
				if (i >= s.size() || s[i] != ':') return false;
				i++;
				Value member;
				if (!value(member)) return false;
				v.members.emplace_back(std::move(key), std::move(member));
				ws();
				if (i < s.size() && s[i] == ',') {
					i++;
					continue;
				}
				if (i < s.size() && s[i] == '}') {
					i++;
					break;
				}
				return false;
			}
			break;
		}
		case '[': {
			v.kind = Value::Kind::array;
			i++;
			ws();
			if (i < s.size() && s[i] == ']') {
				i++;
				break;
			}
			for (;;) {
				Value item;
				if (!value(item)) return false;
				v.items.push_back(std::move(item));
				ws();
				if (i < s.size() && s[i] == ',') {
					i++;
					continue;
				}
				if (i < s.size() && s[i] == ']') {
					i++;
					break;
				}
				return false;
			}
			break;
		}
		case '"':
			v.kind = Value::Kind::string;
			ok = string(v.text);
			break;
		case 't':
			v.kind = Value::Kind::boolean;
			v.boolean = true;
			ok = literal("true");
			break;
		case 'f':
			v.kind = Value::Kind::boolean;
			ok = literal("false");
			break;
		case 'n':
			ok = literal("null");
			break;
		default:
			v.kind = Value::Kind::number;
			ok = number(v.text);
		}
		depth--;
		return ok;
	}
};

} // namespace

bool decode_first(std::string_view text, Value &out)
{
	Parser p{text};
	out = {};
	if (!p.value(out)) return false;
	// A number at the top level needs a delimiter after it.
	if (out.kind == Value::Kind::number && p.i < text.size()) {
		char c = text[p.i];
		if (c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '[' && c != '{' && c != '"') return false;
	}
	return true;
}

bool unmarshal(std::string_view text, Value &out)
{
	Parser p{text};
	out = {};
	if (!p.value(out)) return false;
	p.ws();
	return p.i == text.size();
}

bool fold_equal(std::string_view a, std::string_view b)
{
	if (a.size() != b.size()) return false;
	for (std::size_t k = 0; k < a.size(); k++) {
		char x = a[k], y = b[k];
		if (x >= 'A' && x <= 'Z') x = static_cast<char>(x + 32);
		if (y >= 'A' && y <= 'Z') y = static_cast<char>(y + 32);
		if (x != y) return false;
	}
	return true;
}

bool get(const Value &v, std::string &out)
{
	if (v.is_null()) return true;
	if (v.kind != Value::Kind::string) return false;
	out = v.text;
	return true;
}

bool get(const Value &v, bool &out)
{
	if (v.is_null()) return true;
	if (v.kind != Value::Kind::boolean) return false;
	out = v.boolean;
	return true;
}

bool get(const Value &v, std::int64_t &out)
{
	if (v.is_null()) return true;
	if (v.kind != Value::Kind::number) return false;
	std::int64_t n = 0;
	auto [end, ec] = std::from_chars(v.text.data(), v.text.data() + v.text.size(), n);
	if (ec != std::errc() || end != v.text.data() + v.text.size()) return false;
	out = n;
	return true;
}

bool get(const Value &v, int &out)
{
	std::int64_t n = out;
	if (!get(v, n)) return false;
	if (n < INT32_MIN || n > INT32_MAX) {
		// Go's int is 64 bits on the device; the services' numbers fit 32.
		if (sizeof(int) < 8) return false;
	}
	out = static_cast<int>(n);
	return true;
}

bool get(const Value &v, std::optional<std::vector<std::string>> &out)
{
	if (v.is_null()) {
		out.reset();
		return true;
	}
	if (v.kind != Value::Kind::array) return false;
	std::vector<std::string> list;
	for (const auto &item : v.items) {
		std::string s;
		if (!get(item, s)) return false;
		list.push_back(std::move(s));
	}
	out = std::move(list);
	return true;
}

bool get(const Value &v, std::optional<std::map<std::string, std::string>> &out)
{
	if (v.is_null()) {
		out.reset();
		return true;
	}
	if (v.kind != Value::Kind::object) return false;
	if (!out) out.emplace();
	for (const auto &[key, value] : v.members) {
		std::string s;
		if (!get(value, s)) return false;
		(*out)[key] = std::move(s);
	}
	return true;
}

Out Out::lit(std::string raw)
{
	Out o;
	o.raw = std::move(raw);
	return o;
}

Out Out::str(std::string_view s) { return lit(gojson::quote(s)); }

Out Out::strings(const std::vector<std::string> &list)
{
	Out o = array();
	for (const auto &s : list) o.add(str(s));
	return o;
}

Out Out::strings(const std::optional<std::vector<std::string>> &list)
{
	return list ? strings(*list) : null();
}

Out Out::string_map(const std::optional<std::map<std::string, std::string>> &map)
{
	if (!map) return null();
	Out o = object();
	for (const auto &[k, v] : *map) o.set(k, str(v));
	return o;
}

Out &Out::sort_keys()
{
	std::stable_sort(members.begin(), members.end(), [](const auto &a, const auto &b) { return a.first < b.first; });
	return *this;
}

namespace {

void write(const Out &v, std::string &out, int indent)
{
	auto newline = [&](int level) {
		if (indent < 0) return;
		out += '\n';
		out.append(static_cast<std::size_t>(level) * 2, ' ');
	};
	switch (v.kind) {
	case Out::Kind::raw:
		out += v.raw;
		return;
	case Out::Kind::array:
		if (v.items.empty()) {
			out += "[]";
			return;
		}
		out += '[';
		for (std::size_t k = 0; k < v.items.size(); k++) {
			if (k) out += ',';
			newline(indent + 1);
			write(v.items[k], out, indent < 0 ? -1 : indent + 1);
		}
		newline(indent);
		out += ']';
		return;
	case Out::Kind::object:
		if (v.members.empty()) {
			out += "{}";
			return;
		}
		out += '{';
		for (std::size_t k = 0; k < v.members.size(); k++) {
			if (k) out += ',';
			newline(indent + 1);
			out += gojson::quote(v.members[k].first);
			out += indent < 0 ? ":" : ": ";
			write(v.members[k].second, out, indent < 0 ? -1 : indent + 1);
		}
		newline(indent);
		out += '}';
		return;
	}
}

} // namespace

std::string marshal(const Out &v)
{
	std::string out;
	write(v, out, -1);
	return out;
}

std::string marshal_indent(const Out &v)
{
	std::string out;
	write(v, out, 0);
	return out;
}

} // namespace nkos::notify::json
