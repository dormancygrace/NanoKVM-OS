#include "recovery/yaml_v3.hpp"

#include "common/yaml_parser.hpp"

#include <algorithm>
#include <charconv>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <functional>
#include <limits>
#include <memory>
#include <vector>

namespace nkos::recovery::yamlv3 {

namespace {

// ---- The parsed document (yaml.Node) ----

struct Raw {
	enum class Kind { scalar, seq, map };
	Kind kind = Kind::scalar;
	std::string text;
	bool string = false;  // quoted, block scalar or !!str: never resolved to another type
	std::size_t line = 0;
	std::vector<Raw> items;                    // seq
	std::vector<std::pair<Raw, Raw>> entries;  // map
};

std::string str(ryml::csubstr s) { return s.str ? std::string(s.str, s.len) : std::string(); }

std::size_t line_of(const ryml::Tree &tree, const ryml::Parser &parser, ryml::id_type id)
{
	ryml::Location loc = tree.location(parser, id);
	return loc.line == ryml::npos ? 0 : loc.line + 1;
}

bool tagged_string(ryml::csubstr tag)
{
	std::string t = str(tag);
	return t == "!!str" || t == "<tag:yaml.org,2002:str>" || t == "tag:yaml.org,2002:str";
}

Raw build(const ryml::Tree &tree, const ryml::Parser &parser, ryml::id_type id)
{
	Raw r;
	r.line = line_of(tree, parser, id);
	if (tree.is_map(id)) {
		r.kind = Raw::Kind::map;
		for (ryml::id_type child = tree.first_child(id); child != ryml::NONE; child = tree.next_sibling(child)) {
			Raw key;
			key.line = line_of(tree, parser, child);
			key.text = str(tree.key(child));
			key.string = tree.is_key_quoted(child) || (tree.has_key_tag(child) && tagged_string(tree.key_tag(child)));
			// libyaml refuses a block mapping entry that starts with ":" (no key); rapidyaml reads an empty key.
			if (key.text.empty() && !key.string && !tree.has_key_tag(child)) throw yaml::ParseError("did not find expected key");
			r.entries.emplace_back(std::move(key), build(tree, parser, child));
		}
	} else if (tree.is_seq(id)) {
		r.kind = Raw::Kind::seq;
		for (ryml::id_type child = tree.first_child(id); child != ryml::NONE; child = tree.next_sibling(child))
			r.items.push_back(build(tree, parser, child));
	} else if (tree.has_val(id)) {
		r.text = str(tree.val(id));
		r.string = tree.is_val_quoted(id) || (tree.has_val_tag(id) && tagged_string(tree.val_tag(id)));
	}
	return r;
}

// Whether a node or one below it has an anchor or is an alias (before the tree resolves them).
bool has_anchors(const ryml::Tree &tree, ryml::id_type id)
{
	if (tree.has_anchor(id) || tree.is_ref(id)) return true;
	for (ryml::id_type child = tree.first_child(id); child != ryml::NONE; child = tree.next_sibling(child))
		if (has_anchors(tree, child)) return true;
	return false;
}

// The document's root, or nullopt with error set; empty is set for a document without content. anchors, when
// given, is set for a document with an anchor or an alias anywhere.
std::optional<Raw> parse(std::string_view text, std::string &error, bool &empty, bool *anchors = nullptr)
{
	std::optional<Raw> result;
	empty = false;
	try {
		ryml::EventHandlerTree handler(yaml::parser_callbacks());
		ryml::Parser parser(&handler, ryml::ParserOptions().locations(true));
		ryml::Tree tree = ryml::parse_in_arena(&parser, ryml::csubstr(text.data(), text.size()));
		if (anchors) *anchors = has_anchors(tree, tree.root_id());
		tree.resolve();
		ryml::id_type root = tree.root_id();
		if (tree.is_stream(root)) {
			root = tree.first_child(root);
			if (root == ryml::NONE) empty = true;
		}
		if (!empty && !tree.is_map(root) && !tree.is_seq(root) && (!tree.has_val(root) || tree.val(root).str == nullptr))
			empty = true;
		if (!empty) result = build(tree, parser, root);
	} catch (const yaml::ParseError &e) {
		error = e.what();
	}
	if (!result && !empty && error.empty()) error = "yaml: invalid document";
	return result;
}

// ---- Values (what yaml.v3 decodes into an interface{}) ----

struct Node {
	// The order is reflect.Kind's, which yaml.v3's key sort uses between kinds (a nil key of a map[interface{}]
	// interface{} stays an Interface).
	enum class Kind { null = 20, boolean = 1, integer = 2, uinteger = 11, floating = 14, map = 21, seq = 23, string = 24, time = 25 };
	Kind kind = Kind::null;
	bool flag = false;
	std::int64_t i = 0;
	std::uint64_t u = 0;
	double f = 0;
	std::string s;  // string, or a time as RFC 3339 with nanoseconds
	std::vector<Node> items;
	std::vector<std::pair<Node, Node>> entries;
};

// strconv.ParseInt/ParseUint(s, 0, 64) after yaml.v3 removed the underscores: sign, 0x/0o/0b prefixes, a leading
// 0 for octal.
bool parse_integer(std::string_view s, bool is_signed, std::int64_t &i, std::uint64_t &u)
{
	bool negative = false;
	if (!s.empty() && (s[0] == '+' || s[0] == '-')) {
		if (!is_signed && s[0] == '-') return false;
		if (!is_signed && s[0] == '+') return false;
		negative = s[0] == '-';
		s.remove_prefix(1);
	}
	int base = 10;
	if (s.size() >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')) base = 16, s.remove_prefix(2);
	else if (s.size() >= 2 && s[0] == '0' && (s[1] == 'o' || s[1] == 'O')) base = 8, s.remove_prefix(2);
	else if (s.size() >= 2 && s[0] == '0' && (s[1] == 'b' || s[1] == 'B')) base = 2, s.remove_prefix(2);
	else if (s.size() >= 2 && s[0] == '0') base = 8, s.remove_prefix(1);
	if (s.empty()) return false;
	std::uint64_t v = 0;
	auto [end, ec] = std::from_chars(s.data(), s.data() + s.size(), v, base);
	if (ec != std::errc{} || end != s.data() + s.size()) return false;
	if (!is_signed) {
		u = v;
		return true;
	}
	if (negative) {
		if (v > static_cast<std::uint64_t>(INT64_MAX) + 1) return false;
		i = static_cast<std::int64_t>(0 - v);
		return true;
	}
	if (v > static_cast<std::uint64_t>(INT64_MAX)) return false;
	i = static_cast<std::int64_t>(v);
	return true;
}

// ^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$
bool yaml_style_float(std::string_view s)
{
	std::size_t p = 0;
	auto digits = [&] {
		std::size_t start = p;
		while (p < s.size() && s[p] >= '0' && s[p] <= '9') p++;
		return p - start;
	};
	if (p < s.size() && (s[p] == '+' || s[p] == '-')) p++;
	if (p < s.size() && s[p] == '.') {
		p++;
		if (!digits()) return false;
	} else {
		if (!digits()) return false;
		if (p < s.size() && s[p] == '.') {
			p++;
			digits();
		}
	}
	if (p < s.size() && (s[p] == 'e' || s[p] == 'E')) {
		p++;
		if (p < s.size() && (s[p] == '+' || s[p] == '-')) p++;
		if (!digits()) return false;
	}
	return p == s.size();
}

// strconv.ParseFloat(s, 64) for the decimal forms yaml.v3 hands it: false when out of range.
bool parse_float(std::string_view s, double &out)
{
	if (!s.empty() && s[0] == '+') s.remove_prefix(1);
	auto [end, ec] = std::from_chars(s.data(), s.data() + s.size(), out);
	if (end != s.data() + s.size()) return false;
	if (ec == std::errc::result_out_of_range) {
		// Go: overflow is an error (±Inf, ErrRange); an underflow rounds to zero or a denormal.
		std::string text(s);
		double v = std::strtod(text.c_str(), nullptr);
		if (std::isinf(v)) return false;
		out = v;
		return true;
	}
	return ec == std::errc{};
}

int days_in(int month, int year)
{
	static const int days[] = {31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31};
	if (month == 2 && year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)) return 29;
	return days[month - 1];
}

// yaml.v3's parseTimestamp (time.Parse with "2006-1-2T15:4:5.999999999Z07:00", the same with "t",
// "2006-1-2 15:4:5.999999999" and "2006-1-2"), formatted as time.RFC3339Nano.
std::optional<std::string> parse_timestamp(std::string_view s)
{
	std::size_t i = 0;
	while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
	if (i != 4 || i == s.size() || s[i] != '-') return std::nullopt;
	std::size_t p = 0;
	auto num = [&](std::size_t min_digits, std::size_t max_digits, int &v) {
		std::size_t start = p;
		v = 0;
		while (p < s.size() && p - start < max_digits && s[p] >= '0' && s[p] <= '9') v = v * 10 + (s[p++] - '0');
		return p - start >= min_digits;
	};
	int year = 0, month = 0, day = 0, hour = 0, minute = 0, second = 0;
	if (!num(4, 4, year) || p >= s.size() || s[p++] != '-' || !num(1, 2, month) || p >= s.size() || s[p++] != '-' ||
	    !num(1, 2, day))
		return std::nullopt;
	if (month < 1 || month > 12 || day < 1 || day > days_in(month, year)) return std::nullopt;
	char buf[64];
	if (p == s.size()) {
		std::snprintf(buf, sizeof buf, "%04d-%02d-%02dT00:00:00Z", year, month, day);
		return std::string(buf);
	}
	char sep = s[p++];
	if (sep != 'T' && sep != 't' && sep != ' ') return std::nullopt;
	if (!num(1, 2, hour) || p >= s.size() || s[p++] != ':' || !num(1, 2, minute) || p >= s.size() || s[p++] != ':' ||
	    !num(1, 2, second))
		return std::nullopt;
	if (hour > 23 || minute > 59 || second > 59) return std::nullopt;
	std::string frac;
	if (p < s.size() && s[p] == '.') {
		std::size_t start = ++p;
		while (p < s.size() && s[p] >= '0' && s[p] <= '9') p++;
		if (p == start) return std::nullopt;
		frac = std::string(s.substr(start, std::min<std::size_t>(p - start, 9)));
		while (!frac.empty() && frac.back() == '0') frac.pop_back();
	}
	std::string zone;
	if (sep == ' ') {
		if (p != s.size()) return std::nullopt;
		zone = "Z";
	} else {
		if (p == s.size()) return std::nullopt;
		if (s[p] == 'Z' && p + 1 == s.size()) {
			zone = "Z";
		} else {
			if (s.size() - p != 6 || (s[p] != '+' && s[p] != '-') || s[p + 3] != ':') return std::nullopt;
			int zh = 0, zm = 0;
			std::size_t save = p;
			p++;
			if (!num(2, 2, zh)) return std::nullopt;
			p++;
			if (!num(2, 2, zm)) return std::nullopt;
			if (zh > 23 || zm > 59) return std::nullopt;
			zone = zh == 0 && zm == 0 ? "Z" : std::string(s.substr(save, 6));
		}
	}
	std::snprintf(buf, sizeof buf, "%04d-%02d-%02dT%02d:%02d:%02d", year, month, day, hour, minute, second);
	std::string out = buf;
	if (!frac.empty()) out += "." + frac;
	return out + zone;
}

// yaml.v3's resolve("", in) for a plain scalar.
Node resolve(std::string_view in)
{
	Node n;
	if (in.empty() || in == "~" || in == "null" || in == "Null" || in == "NULL") return n;
	if (in == "true" || in == "True" || in == "TRUE") return n.kind = Node::Kind::boolean, n.flag = true, n;
	if (in == "false" || in == "False" || in == "FALSE") return n.kind = Node::Kind::boolean, n.flag = false, n;
	if (in == ".nan" || in == ".NaN" || in == ".NAN")
		return n.kind = Node::Kind::floating, n.f = std::numeric_limits<double>::quiet_NaN(), n;
	if (in == ".inf" || in == ".Inf" || in == ".INF" || in == "+.inf" || in == "+.Inf" || in == "+.INF")
		return n.kind = Node::Kind::floating, n.f = std::numeric_limits<double>::infinity(), n;
	if (in == "-.inf" || in == "-.Inf" || in == "-.INF")
		return n.kind = Node::Kind::floating, n.f = -std::numeric_limits<double>::infinity(), n;
	n.kind = Node::Kind::string;
	n.s = std::string(in);
	char c = in[0];
	if (c == '.') {
		double f = 0;
		if (parse_float(in, f)) n = Node{}, n.kind = Node::Kind::floating, n.f = f;
		return n;
	}
	if (!(c == '+' || c == '-' || (c >= '0' && c <= '9'))) return n;
	if (auto t = parse_timestamp(in)) {
		Node r;
		r.kind = Node::Kind::time;
		r.s = *t;
		return r;
	}
	std::string plain;
	for (char ch : in)
		if (ch != '_') plain += ch;
	std::int64_t i = 0;
	std::uint64_t u = 0;
	double f = 0;
	Node r;
	if (parse_integer(plain, true, i, u)) return r.kind = Node::Kind::integer, r.i = i, r;
	if (parse_integer(plain, false, i, u)) return r.kind = Node::Kind::uinteger, r.u = u, r;
	if (yaml_style_float(plain) && parse_float(plain, f)) return r.kind = Node::Kind::floating, r.f = f, r;
	auto base_prefixed = [&](std::string_view prefix, int base) -> std::optional<Node> {
		if (plain.size() < prefix.size() || plain.compare(0, prefix.size(), prefix) != 0) return std::nullopt;
		std::string_view digits = std::string_view(plain).substr(prefix.size());
		if (digits.empty()) return std::nullopt;
		bool neg = prefix[0] == '-', sign = neg;
		if (digits[0] == '+' || digits[0] == '-') {
			// ParseInt(plain[2:], base) takes a sign after "0b" ("0b-1"); "-" + plain[3:] cannot have a second one,
			// and ParseUint takes none.
			if (neg) return std::nullopt;
			neg = digits[0] == '-';
			sign = true;
			digits.remove_prefix(1);
		}
		std::uint64_t v = 0;
		auto [end, ec] = std::from_chars(digits.data(), digits.data() + digits.size(), v, base);
		if (ec != std::errc{} || end != digits.data() + digits.size() || digits.empty()) return std::nullopt;
		Node x;
		if (neg) {
			if (v > static_cast<std::uint64_t>(INT64_MAX) + 1) return std::nullopt;
			x.kind = Node::Kind::integer, x.i = static_cast<std::int64_t>(0 - v);
		} else if (v <= static_cast<std::uint64_t>(INT64_MAX)) {
			x.kind = Node::Kind::integer, x.i = static_cast<std::int64_t>(v);
		} else if (!sign) {
			x.kind = Node::Kind::uinteger, x.u = v;
		} else {
			return std::nullopt;
		}
		return x;
	};
	if (auto x = base_prefixed("0b", 2)) return *x;
	if (auto x = base_prefixed("-0b", 2)) return *x;
	if (auto x = base_prefixed("0o", 8)) return *x;
	if (auto x = base_prefixed("-0o", 8)) return *x;
	return n;
}

Node scalar_value(const Raw &raw)
{
	if (raw.string) {
		Node n;
		n.kind = Node::Kind::string;
		n.s = raw.text;
		return n;
	}
	return resolve(raw.text);
}

std::string go_quote(std::string_view s)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
	for (unsigned char c : s) {
		if (c == '"' || c == '\\') out += '\\', out += static_cast<char>(c);
		else if (c == '\n') out += "\\n";
		else if (c == '\t') out += "\\t";
		else if (c == '\r') out += "\\r";
		else if (c < 0x20 || c == 0x7f) out += "\\x", out += hex[c >> 4], out += hex[c & 15];
		else out += static_cast<char>(c);
	}
	return out + "\"";
}

struct Decoder {
	std::vector<std::string> errors;

	// d.mapping's check of duplicate keys (same kind, same text).
	bool unique(const Raw &map)
	{
		std::size_t before = errors.size();
		for (std::size_t i = 0; i < map.entries.size(); i++)
			for (std::size_t j = i + 1; j < map.entries.size(); j++) {
				const Raw &a = map.entries[i].first, &b = map.entries[j].first;
				if (a.kind == b.kind && a.text == b.text)
					errors.push_back("line " + std::to_string(b.line) + ": mapping key " + go_quote(b.text) +
					                 " already defined at line " + std::to_string(a.line));
			}
		return errors.size() == before;
	}

	// Into an interface{}: false when the value is dropped.
	bool any(const Raw &raw, Node &out)
	{
		switch (raw.kind) {
		case Raw::Kind::scalar: out = scalar_value(raw); return true;
		case Raw::Kind::seq:
			out.kind = Node::Kind::seq;
			for (const auto &item : raw.items) {
				Node n;
				any(item, n);
				out.items.push_back(std::move(n));
			}
			return true;
		case Raw::Kind::map:
			if (!unique(raw)) return false;
			out.kind = Node::Kind::map;
			for (const auto &[key, value] : raw.entries) {
				Node k;
				if (key.kind != Raw::Kind::scalar) continue;
				k = scalar_value(key);
				Node v;
				any(value, v);
				out.entries.emplace_back(std::move(k), std::move(v));
			}
			return true;
		}
		return false;
	}
};

// ---- yaml.v3's key order (sorter.go) ----

bool is_number(const Node &n, double &f)
{
	switch (n.kind) {
	case Node::Kind::integer: f = static_cast<double>(n.i); return true;
	case Node::Kind::uinteger: f = static_cast<double>(n.u); return true;
	case Node::Kind::floating: f = n.f; return true;
	case Node::Kind::boolean: f = n.flag ? 1 : 0; return true;
	default: return false;
	}
}

std::u32string runes(std::string_view s)
{
	std::u32string out;
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		std::size_t n = c < 0x80 ? 1 : c >= 0xf0 ? 4 : c >= 0xe0 ? 3 : 2;
		char32_t cp = n == 1 ? c : n == 2 ? c & 31 : n == 3 ? c & 15 : c & 7;
		for (std::size_t k = 1; k < n && i + k < s.size(); k++) cp = (cp << 6) | (static_cast<unsigned char>(s[i + k]) & 0x3f);
		out += cp;
		i += n;
	}
	return out;
}

// unicode.IsDigit / IsLetter for the keys of a settings file: ASCII, and Latin-1 and other BMP letters by a coarse
// rule (any non-ASCII rune that is not a digit counts as a letter).
bool rune_digit(char32_t r) { return r >= '0' && r <= '9'; }
bool rune_letter(char32_t r) { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= 0xc0 && r != 0xd7 && r != 0xf7); }

bool key_less(const Node &a, const Node &b)
{
	double af = 0, bf = 0;
	bool aok = is_number(a, af), bok = is_number(b, bf);
	if (aok && bok) {
		if (af != bf) return af < bf;
		if (a.kind != b.kind) return a.kind < b.kind;
		switch (a.kind) {
		case Node::Kind::integer: return a.i < b.i;
		case Node::Kind::uinteger: return a.u < b.u;
		case Node::Kind::floating: return a.f < b.f;
		default: return !a.flag && b.flag;
		}
	}
	if (a.kind != Node::Kind::string || b.kind != Node::Kind::string) return a.kind < b.kind;
	std::u32string ar = runes(a.s), br = runes(b.s);
	bool digits = false;
	for (std::size_t i = 0; i < ar.size() && i < br.size(); i++) {
		if (ar[i] == br[i]) {
			digits = rune_digit(ar[i]);
			continue;
		}
		bool al = rune_letter(ar[i]), bl = rune_letter(br[i]);
		if (al && bl) return ar[i] < br[i];
		if (al || bl) return digits ? al : bl;
		std::int64_t an = 0, bn = 0;
		if (ar[i] == '0' || br[i] == '0') {
			for (std::size_t j = i; j-- > 0 && rune_digit(ar[j]);) {
				if (ar[j] != '0') {
					an = 1, bn = 1;
					break;
				}
			}
		}
		std::size_t ai = i, bi = i;
		for (; ai < ar.size() && rune_digit(ar[ai]); ai++) an = an * 10 + static_cast<std::int64_t>(ar[ai] - '0');
		for (; bi < br.size() && rune_digit(br[bi]); bi++) bn = bn * 10 + static_cast<std::int64_t>(br[bi] - '0');
		if (an != bn) return an < bn;
		if (ai != bi) return ai < bi;
		return ar[i] < br[i];
	}
	return ar.size() < br.size();
}

// ---- strconv.FormatFloat(f, 'g', -1, 64) ----

std::string format_float(double f)
{
	if (std::isnan(f)) return ".nan";
	if (std::isinf(f)) return f > 0 ? ".inf" : "-.inf";
	char buf[64];
	auto [end, ec] = std::to_chars(buf, buf + sizeof buf, f, std::chars_format::scientific);
	(void)ec;
	std::string sci(buf, end);
	bool neg = !sci.empty() && sci[0] == '-';
	if (neg) sci.erase(0, 1);
	auto e = sci.find('e');
	std::string mant = sci.substr(0, e);
	int exp10 = std::stoi(sci.substr(e + 1));
	std::string digits;
	for (char c : mant)
		if (c != '.') digits += c;
	if (digits == "0") exp10 = 0;
	int nd = static_cast<int>(digits.size());
	int dp = digits == "0" ? 1 : exp10 + 1;
	if (digits == "0") dp = 1;
	std::string out = neg ? "-" : "";
	int x = dp - 1;
	if (x < -4 || x >= 6 + 0 * nd) {
		// %e with nd-1 fraction digits
		out += digits[0];
		if (nd > 1) out += "." + digits.substr(1);
		out += 'e';
		out += x < 0 ? '-' : '+';
		int ax = x < 0 ? -x : x;
		if (ax < 10) out += '0';
		out += std::to_string(ax);
		return out;
	}
	// %f with max(nd-dp, 0) fraction digits
	if (dp <= 0) {
		out += "0." + std::string(static_cast<std::size_t>(-dp), '0') + digits;
	} else if (dp >= nd) {
		out += digits + std::string(static_cast<std::size_t>(dp - nd), '0');
	} else {
		out += digits.substr(0, static_cast<std::size_t>(dp)) + "." + digits.substr(static_cast<std::size_t>(dp));
	}
	return out;
}

// ---- The emitter (emitterc.go, block style, no comments, anchors or tags but !!binary) ----

bool valid_utf8(std::string_view s)
{
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x80) {
			i++;
			continue;
		}
		std::size_t n = c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 0;
		if (n == 0 || i + n > s.size()) return false;
		char32_t cp = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
		for (std::size_t k = 1; k < n; k++) {
			unsigned char cc = static_cast<unsigned char>(s[i + k]);
			if ((cc & 0xc0) != 0x80) return false;
			cp = (cp << 6) | (cc & 0x3f);
		}
		if ((n == 3 && (cp < 0x800 || (cp >= 0xd800 && cp <= 0xdfff))) || (n == 4 && (cp < 0x10000 || cp > 0x10ffff)))
			return false;
		i += n;
	}
	return true;
}

std::string base64_lines(std::string_view s)
{
	static const char table[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	std::string in;
	std::size_t i = 0;
	for (; i + 3 <= s.size(); i += 3) {
		unsigned v = (static_cast<unsigned char>(s[i]) << 16) | (static_cast<unsigned char>(s[i + 1]) << 8) | static_cast<unsigned char>(s[i + 2]);
		in += table[v >> 18], in += table[(v >> 12) & 63], in += table[(v >> 6) & 63], in += table[v & 63];
	}
	if (std::size_t rest = s.size() - i) {
		unsigned v = static_cast<unsigned char>(s[i]) << 16;
		if (rest == 2) v |= static_cast<unsigned char>(s[i + 1]) << 8;
		in += table[v >> 18], in += table[(v >> 12) & 63];
		in += rest == 2 ? table[(v >> 6) & 63] : '=';
		in += '=';
	}
	const std::size_t line = 70;
	std::size_t lines = in.size() / line + 1;
	std::string out;
	for (std::size_t k = 0; k < in.size(); k += line) {
		out += in.substr(k, line);
		if (lines > 1) out += '\n';
	}
	return out;
}

class Emitter {
public:
	std::string out;

	void document(const Node &root)
	{
		emit_node(root, true, false, false, false);
		// Document end: a final line break.
		write_indent();
	}

private:
	enum class State { other, seq_item, map_key, map_simple_value, map_value };
	enum class Style { plain, single, dbl, literal };

	static constexpr int best_indent = 4;
	int indent = -1;
	std::vector<int> indents;
	std::vector<State> states{State::other};
	int column = 0;
	int flow_level = 0;
	bool whitespace = true;
	bool indention = true;
	bool root_context = false, simple_key_context = false;

	// The scalar being emitted (analysis results).
	struct Scalar {
		std::string value;
		std::string tag;  // "!!binary" or empty
		Style style = Style::plain;
		bool multiline = false, flow_plain_allowed = false, block_plain_allowed = false, single_quoted_allowed = false,
		     block_allowed = false;
	};

	// The bytes at i, padded with NULs so that the look-ahead of the C code stays in range.
	static unsigned char at(const std::string &v, std::size_t i) { return i < v.size() ? static_cast<unsigned char>(v[i]) : 0; }
	static std::size_t width(unsigned char b) { return (b & 0x80) == 0 ? 1 : (b & 0xe0) == 0xc0 ? 2 : (b & 0xf0) == 0xe0 ? 3 : (b & 0xf8) == 0xf0 ? 4 : 0; }
	static bool is_space(const std::string &v, std::size_t i) { return at(v, i) == ' '; }
	static bool is_blank(const std::string &v, std::size_t i) { return at(v, i) == ' ' || at(v, i) == '\t'; }
	static bool is_break(const std::string &v, std::size_t i)
	{
		unsigned char b = at(v, i);
		return b == '\r' || b == '\n' || (b == 0xc2 && at(v, i + 1) == 0x85) ||
		       (b == 0xe2 && at(v, i + 1) == 0x80 && (at(v, i + 2) == 0xa8 || at(v, i + 2) == 0xa9));
	}
	static bool is_blankz(const std::string &v, std::size_t i) { return is_blank(v, i) || is_break(v, i) || at(v, i) == 0; }
	static bool is_printable(const std::string &v, std::size_t i)
	{
		unsigned char b = at(v, i), b1 = at(v, i + 1), b2 = at(v, i + 2);
		return b == 0x0a || (b >= 0x20 && b <= 0x7e) || (b == 0xc2 && b1 >= 0xa0) || (b > 0xc2 && b < 0xed) ||
		       (b == 0xed && b1 < 0xa0) || b == 0xee ||
		       (b == 0xef && !(b1 == 0xbb && b2 == 0xbf) && !(b1 == 0xbf && (b2 == 0xbe || b2 == 0xbf)));
	}
	static bool is_bom(const std::string &v) { return at(v, 0) == 0xef && at(v, 1) == 0xbb && at(v, 2) == 0xbf; }

	void put(char c)
	{
		out += c;
		column++;
	}
	void put_break()
	{
		out += '\n';
		column = 0;
		indention = true;
	}
	void write(const std::string &s, std::size_t &i)
	{
		std::size_t w = width(at(s, i));
		if (w == 0) w = 1;
		out.append(s, i, w);
		column++;
		i += w;
	}
	void write_all(const std::string &s)
	{
		for (std::size_t i = 0; i < s.size();) write(s, i);
	}
	void write_break(const std::string &s, std::size_t &i)
	{
		if (at(s, i) == '\n') {
			put_break();
			i++;
		} else {
			write(s, i);
			column = 0;
			indention = true;
		}
	}
	void write_indent()
	{
		int ind = indent < 0 ? 0 : indent;
		if (!indention || column > ind || (column == ind && !whitespace)) put_break();
		while (column < ind) put(' ');
		whitespace = true;
	}
	void write_indicator(const std::string &indicator, bool need_whitespace, bool is_whitespace, bool is_indention)
	{
		if (need_whitespace && !whitespace) put(' ');
		write_all(indicator);
		whitespace = is_whitespace;
		indention = indention && is_indention;
	}
	void increase_indent(bool flow, bool indentless)
	{
		indents.push_back(indent);
		if (indent < 0) {
			indent = flow ? best_indent : 0;
		} else if (!indentless) {
			if (states.back() == State::seq_item) indent += 2;
			else indent = best_indent * ((indent + best_indent) / best_indent);
		}
	}
	void pop_indent()
	{
		indent = indents.back();
		indents.pop_back();
	}

	void analyze(Scalar &sc)
	{
		const std::string &value = sc.value;
		if (value.empty()) {
			sc.multiline = false;
			sc.flow_plain_allowed = false;
			sc.block_plain_allowed = true;
			sc.single_quoted_allowed = true;
			sc.block_allowed = false;
			return;
		}
		bool block_indicators = false, flow_indicators = false, line_breaks = false, special_characters = false,
		     tab_characters = false, leading_space = false, leading_break = false, trailing_space = false,
		     trailing_break = false, break_space = false, space_break = false, preceded_by_whitespace = true,
		     followed_by_whitespace = false, previous_space = false, previous_break = false;
		if (value.size() >= 3 && ((value[0] == '-' && value[1] == '-' && value[2] == '-') ||
		                          (value[0] == '.' && value[1] == '.' && value[2] == '.'))) {
			block_indicators = true;
			flow_indicators = true;
		}
		for (std::size_t i = 0, w = 0; i < value.size(); i += w) {
			w = width(at(value, i));
			if (w == 0) w = 1;
			followed_by_whitespace = i + w >= value.size() || is_blank(value, i + w);
			char c = value[i];
			if (i == 0) {
				switch (c) {
				case '#': case ',': case '[': case ']': case '{': case '}': case '&': case '*': case '!': case '|':
				case '>': case '\'': case '"': case '%': case '@': case '`':
					flow_indicators = block_indicators = true;
					break;
				case '?': case ':':
					flow_indicators = true;
					if (followed_by_whitespace) block_indicators = true;
					break;
				case '-':
					if (followed_by_whitespace) flow_indicators = block_indicators = true;
					break;
				}
			} else {
				switch (c) {
				case ',': case '?': case '[': case ']': case '{': case '}': flow_indicators = true; break;
				case ':':
					flow_indicators = true;
					if (followed_by_whitespace) block_indicators = true;
					break;
				case '#':
					if (preceded_by_whitespace) flow_indicators = block_indicators = true;
					break;
				}
			}
			if (c == '\t') tab_characters = true;
			else if (!is_printable(value, i)) special_characters = true;
			if (is_space(value, i)) {
				if (i == 0) leading_space = true;
				if (i + w == value.size()) trailing_space = true;
				if (previous_break) break_space = true;
				previous_space = true;
				previous_break = false;
			} else if (is_break(value, i)) {
				line_breaks = true;
				if (i == 0) leading_break = true;
				if (i + w == value.size()) trailing_break = true;
				if (previous_space) space_break = true;
				previous_space = false;
				previous_break = true;
			} else {
				previous_space = false;
				previous_break = false;
			}
			preceded_by_whitespace = is_blankz(value, i);
		}
		sc.multiline = line_breaks;
		sc.flow_plain_allowed = sc.block_plain_allowed = sc.single_quoted_allowed = sc.block_allowed = true;
		if (leading_space || leading_break || trailing_space || trailing_break) sc.flow_plain_allowed = sc.block_plain_allowed = false;
		if (trailing_space) sc.block_allowed = false;
		if (break_space) sc.flow_plain_allowed = sc.block_plain_allowed = sc.single_quoted_allowed = false;
		if (space_break || tab_characters || special_characters)
			sc.flow_plain_allowed = sc.block_plain_allowed = sc.single_quoted_allowed = false;
		if (space_break || special_characters) sc.block_allowed = false;
		if (line_breaks) sc.flow_plain_allowed = sc.block_plain_allowed = false;
		if (flow_indicators) sc.flow_plain_allowed = false;
		if (block_indicators) sc.block_plain_allowed = false;
	}

	void select_style(Scalar &sc, Style requested)
	{
		Style style = requested;
		if (simple_key_context && sc.multiline) style = Style::dbl;
		if (style == Style::plain) {
			if ((flow_level > 0 && !sc.flow_plain_allowed) || (flow_level == 0 && !sc.block_plain_allowed)) style = Style::single;
			if (sc.value.empty() && (flow_level > 0 || simple_key_context)) style = Style::single;
		}
		if (style == Style::single && !sc.single_quoted_allowed) style = Style::dbl;
		if (style == Style::literal && (!sc.block_allowed || flow_level > 0 || simple_key_context)) style = Style::dbl;
		sc.style = style;
	}

	void write_plain(const std::string &value)
	{
		if (!value.empty() && !whitespace) put(' ');
		bool breaks = false;
		for (std::size_t i = 0; i < value.size();) {
			if (is_space(value, i)) {
				write(value, i);
			} else if (is_break(value, i)) {
				if (!breaks && at(value, i) == '\n') put_break();
				write_break(value, i);
				breaks = true;
			} else {
				if (breaks) write_indent();
				write(value, i);
				indention = false;
				breaks = false;
			}
		}
		if (!value.empty()) whitespace = false;
		indention = false;
	}

	void write_single(const std::string &value)
	{
		write_indicator("'", true, false, false);
		bool breaks = false;
		for (std::size_t i = 0; i < value.size();) {
			if (is_space(value, i)) {
				write(value, i);
			} else if (is_break(value, i)) {
				if (!breaks && at(value, i) == '\n') put_break();
				write_break(value, i);
				breaks = true;
			} else {
				if (breaks) write_indent();
				if (at(value, i) == '\'') put('\'');
				write(value, i);
				indention = false;
				breaks = false;
			}
		}
		write_indicator("'", false, false, false);
		whitespace = false;
		indention = false;
	}

	void write_double(const std::string &value)
	{
		static const char hex[] = "0123456789ABCDEF";
		write_indicator("\"", true, false, false);
		for (std::size_t i = 0; i < value.size();) {
			unsigned char octet = at(value, i);
			if (!is_printable(value, i) || is_bom(value) || is_break(value, i) || octet == '"' || octet == '\\') {
				std::size_t w = 1;
				char32_t v = octet & 0x7f;
				if ((octet & 0x80) == 0) w = 1, v = octet & 0x7f;
				else if ((octet & 0xe0) == 0xc0) w = 2, v = octet & 0x1f;
				else if ((octet & 0xf0) == 0xe0) w = 3, v = octet & 0x0f;
				else if ((octet & 0xf8) == 0xf0) w = 4, v = octet & 0x07;
				for (std::size_t k = 1; k < w; k++) v = (v << 6) + (at(value, i + k) & 0x3f);
				i += w;
				put('\\');
				switch (v) {
				case 0x00: put('0'); break;
				case 0x07: put('a'); break;
				case 0x08: put('b'); break;
				case 0x09: put('t'); break;
				case 0x0a: put('n'); break;
				case 0x0b: put('v'); break;
				case 0x0c: put('f'); break;
				case 0x0d: put('r'); break;
				case 0x1b: put('e'); break;
				case 0x22: put('"'); break;
				case 0x5c: put('\\'); break;
				case 0x85: put('N'); break;
				case 0xa0: put('_'); break;
				case 0x2028: put('L'); break;
				case 0x2029: put('P'); break;
				default: {
					int digits = 0;
					if (v <= 0xff) put('x'), digits = 2;
					else if (v <= 0xffff) put('u'), digits = 4;
					else put('U'), digits = 8;
					for (int k = (digits - 1) * 4; k >= 0; k -= 4) put(hex[(v >> k) & 0x0f]);
				}
				}
			} else {
				write(value, i);
			}
		}
		write_indicator("\"", false, false, false);
		whitespace = false;
		indention = false;
	}

	void write_literal(const std::string &value)
	{
		write_indicator("|", true, false, false);
		// Block scalar hints.
		if (is_space(value, 0) || is_break(value, 0)) write_indicator(std::string(1, static_cast<char>('0' + best_indent)), false, false, false);
		std::string chomp;
		if (value.empty()) {
			chomp = "-";
		} else {
			std::size_t i = value.size() - 1;
			while ((at(value, i) & 0xc0) == 0x80) i--;
			if (!is_break(value, i)) {
				chomp = "-";
			} else if (i == 0) {
				chomp = "+";
			} else {
				i--;
				while ((at(value, i) & 0xc0) == 0x80) i--;
				if (is_break(value, i)) chomp = "+";
			}
		}
		if (!chomp.empty()) write_indicator(chomp, false, false, false);
		whitespace = true;
		bool breaks = true;
		for (std::size_t i = 0; i < value.size();) {
			if (is_break(value, i)) {
				write_break(value, i);
				breaks = true;
			} else {
				if (breaks) write_indent();
				write(value, i);
				indention = false;
				breaks = false;
			}
		}
	}

	void emit_scalar(Scalar sc, Style requested)
	{
		analyze(sc);
		select_style(sc, requested);
		if (!sc.tag.empty()) {
			// process_tag: "!!" then "binary".
			if (!whitespace) put(' ');
			write_all("!!");
			whitespace = false;
			indention = false;
			write_all("binary");
		}
		increase_indent(true, false);
		switch (sc.style) {
		case Style::plain: write_plain(sc.value); break;
		case Style::single: write_single(sc.value); break;
		case Style::dbl: write_double(sc.value); break;
		case Style::literal: write_literal(sc.value); break;
		}
		pop_indent();
	}

	bool resolves_to_string(const std::string &s)
	{
		Node n = resolve(s);
		return n.kind == Node::Kind::string;
	}

	static bool old_bool(const std::string &s)
	{
		static const char *const words[] = {"y", "Y", "yes", "Yes", "YES", "on", "On", "ON",
		                                    "n", "N", "no", "No", "NO", "off", "Off", "OFF"};
		for (const char *w : words)
			if (s == w) return true;
		return false;
	}

	// ^[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+(?:\.[0-9_]*)?$
	static bool base60_float(const std::string &s)
	{
		if (s.empty()) return false;
		char c = s[0];
		if (!(c == '+' || c == '-' || (c >= '0' && c <= '9')) || s.find(':') == std::string::npos) return false;
		std::size_t p = 0;
		if (s[p] == '+' || s[p] == '-') p++;
		if (p >= s.size() || s[p] < '0' || s[p] > '9') return false;
		p++;
		while (p < s.size() && ((s[p] >= '0' && s[p] <= '9') || s[p] == '_')) p++;
		int groups = 0;
		while (p < s.size() && s[p] == ':') {
			p++;
			std::size_t start = p;
			while (p < s.size() && s[p] >= '0' && s[p] <= '9' && p - start < 2) p++;
			std::size_t n = p - start;
			if (n == 0) return false;
			if (n == 2 && s[start] > '5') {
				// [0-5]?[0-9]: two digits need a first digit 0-5; a single digit is any.
				p = start + 1;
				n = 1;
			}
			groups++;
		}
		if (groups == 0) return false;
		if (p < s.size() && s[p] == '.') {
			p++;
			while (p < s.size() && ((s[p] >= '0' && s[p] <= '9') || s[p] == '_')) p++;
		}
		return p == s.size();
	}

	void emit_string(const std::string &s)
	{
		Scalar sc;
		Style style;
		if (!valid_utf8(s)) {
			sc.tag = "!!binary";
			sc.value = base64_lines(s);
			style = sc.value.find('\n') != std::string::npos ? Style::literal : Style::plain;
			emit_scalar(std::move(sc), style);
			return;
		}
		sc.value = s;
		bool can_plain = resolves_to_string(s) && !(base60_float(s) || old_bool(s));
		if (s.find('\n') != std::string::npos) style = Style::literal;
		else if (can_plain) style = Style::plain;
		else style = Style::dbl;
		emit_scalar(std::move(sc), style);
	}

	void emit_plain(std::string s)
	{
		Scalar sc;
		sc.value = std::move(s);
		emit_scalar(std::move(sc), Style::plain);
	}

	void emit_node(const Node &n, bool root, bool sequence, bool mapping, bool simple_key)
	{
		(void)sequence;
		(void)mapping;
		root_context = root;
		simple_key_context = simple_key;
		switch (n.kind) {
		case Node::Kind::null: emit_plain("null"); break;
		case Node::Kind::boolean: emit_plain(n.flag ? "true" : "false"); break;
		case Node::Kind::integer: emit_plain(std::to_string(n.i)); break;
		case Node::Kind::uinteger: emit_plain(std::to_string(n.u)); break;
		case Node::Kind::floating: emit_plain(format_float(n.f)); break;
		case Node::Kind::time: emit_plain(n.s); break;
		case Node::Kind::string: emit_string(n.s); break;
		case Node::Kind::seq: emit_sequence(n); break;
		case Node::Kind::map: emit_mapping(n); break;
		}
	}

	void emit_sequence(const Node &n)
	{
		if (flow_level > 0 || n.items.empty()) {
			// An empty flow sequence: "[]".
			write_indicator("[", true, true, false);
			increase_indent(true, false);
			flow_level++;
			flow_level--;
			pop_indent();
			if (column == 0) write_indent();
			write_indicator("]", false, false, false);
			return;
		}
		increase_indent(false, false);
		for (const auto &item : n.items) {
			write_indent();
			write_indicator("-", true, false, true);
			states.push_back(State::seq_item);
			emit_node(item, false, true, false, false);
			states.pop_back();
		}
		pop_indent();
	}

	bool simple_key(const Node &key)
	{
		// check_simple_key for a scalar key: single line, at most 128 bytes (with its tag).
		std::string text;
		switch (key.kind) {
		case Node::Kind::null: text = "null"; break;
		case Node::Kind::boolean: text = key.flag ? "true" : "false"; break;
		case Node::Kind::integer: text = std::to_string(key.i); break;
		case Node::Kind::uinteger: text = std::to_string(key.u); break;
		case Node::Kind::floating: text = format_float(key.f); break;
		case Node::Kind::time: text = key.s; break;
		case Node::Kind::string:
			if (!valid_utf8(key.s)) {
				text = base64_lines(key.s);
				if (text.find('\n') != std::string::npos) return false;
				return text.size() + 2 + 6 <= 128;
			}
			text = key.s;
			break;
		case Node::Kind::seq: return key.items.empty();
		case Node::Kind::map: return key.entries.empty();
		}
		Scalar sc;
		sc.value = text;
		analyze(sc);
		if (sc.multiline) return false;
		return text.size() <= 128;
	}

	void emit_mapping(const Node &n)
	{
		if (flow_level > 0 || n.entries.empty()) {
			write_indicator("{", true, true, false);
			increase_indent(true, false);
			pop_indent();
			write_indicator("}", false, false, false);
			return;
		}
		std::vector<const std::pair<Node, Node> *> order;
		for (const auto &e : n.entries) order.push_back(&e);
		std::stable_sort(order.begin(), order.end(), [](auto *a, auto *b) { return key_less(a->first, b->first); });
		increase_indent(false, false);
		for (const auto *e : order) {
			write_indent();
			if (simple_key(e->first)) {
				states.push_back(State::map_simple_value);
				emit_node(e->first, false, false, true, true);
				states.pop_back();
				write_indicator(":", false, false, false);
			} else {
				write_indicator("?", true, false, true);
				states.push_back(State::map_value);
				emit_node(e->first, false, false, true, false);
				states.pop_back();
				write_indent();
				write_indicator(":", true, false, true);
			}
			states.push_back(State::map_key);
			emit_node(e->second, false, false, true, false);
			states.pop_back();
		}
		pop_indent();
	}
};

// ---- Struct decoding for the port check ----

// Into an int: true when set (or left as is without error); false on a type error.
bool decode_int(const Raw &raw, std::int64_t &out)
{
	if (raw.kind != Raw::Kind::scalar) return false;
	Node n = scalar_value(raw);
	switch (n.kind) {
	case Node::Kind::null: return true;
	case Node::Kind::integer: out = n.i; return true;
	case Node::Kind::uinteger:
		if (n.u > static_cast<std::uint64_t>(INT64_MAX)) return false;
		out = static_cast<std::int64_t>(n.u);
		return true;
	case Node::Kind::floating:
		// float64 <= MaxInt64, then int64(f) (truncation; -Inf and below give MinInt64).
		if (std::isnan(n.f) || !(n.f <= 9223372036854775807.0)) return false;
		out = n.f < -9223372036854775808.0 ? INT64_MIN : static_cast<std::int64_t>(n.f);
		return true;
	default: return false;
	}
}

bool null_scalar(const Raw &raw) { return raw.kind == Raw::Kind::scalar && scalar_value(raw).kind == Node::Kind::null; }

} // namespace

bool valid_ports(std::string_view data)
{
	std::string error;
	bool empty = false;
	auto root = parse(data, error, empty);
	if (!root) return false;  // a syntax error, or no document: ports 0
	if (null_scalar(*root)) return false;
	if (root->kind != Raw::Kind::map) return false;
	Decoder d;
	if (!d.unique(*root)) return false;
	std::int64_t http = 0, https = 0;
	bool ok = true;
	for (const auto &[key, value] : root->entries) {
		if (key.kind != Raw::Kind::scalar || null_scalar(key) || key.text != "port") continue;
		if (null_scalar(value)) continue;
		if (value.kind != Raw::Kind::map) {
			ok = false;
			continue;
		}
		if (!d.unique(value)) {
			ok = false;
			continue;
		}
		for (const auto &[k, v] : value.entries) {
			if (k.kind != Raw::Kind::scalar || null_scalar(k)) continue;
			if (k.text == "http" && !decode_int(v, http)) ok = false;
			if (k.text == "https" && !decode_int(v, https)) ok = false;
		}
	}
	return ok && http >= 1 && http <= 65535 && https >= 1 && https <= 65535;
}

std::optional<std::string> reset_web_settings(std::string_view data, std::string &error, bool &panicked)
{
	panicked = false;
	std::string parse_err;
	bool empty = false;
	auto root = parse(data, parse_err, empty);
	if (!root) {
		if (empty) {
			panicked = true;
			error = "assignment to entry in nil map";
			return std::nullopt;
		}
		error = "yaml: " + parse_err;
		return std::nullopt;
	}
	if (null_scalar(*root)) {
		panicked = true;
		error = "assignment to entry in nil map";
		return std::nullopt;
	}
	if (root->kind != Raw::Kind::map) {
		std::string tag = root->kind == Raw::Kind::seq ? "!!seq" : "!!str";
		std::string value;
		if (root->kind == Raw::Kind::scalar) {
			Node n = scalar_value(*root);
			switch (n.kind) {
			case Node::Kind::boolean: tag = "!!bool"; break;
			case Node::Kind::integer: case Node::Kind::uinteger: tag = "!!int"; break;
			case Node::Kind::floating: tag = "!!float"; break;
			case Node::Kind::time: tag = "!!timestamp"; break;
			default: break;
			}
			value = root->text.size() > 10 ? " `" + root->text.substr(0, 7) + "...`" : " `" + root->text + "`";
		}
		error = "yaml: unmarshal errors:\n  line " + std::to_string(root->line) + ": cannot unmarshal " + tag + value +
		        " into map[string]interface {}";
		return std::nullopt;
	}
	Decoder d;
	Node map;
	map.kind = Node::Kind::map;
	if (d.unique(*root)) {
		for (const auto &[key, value] : root->entries) {
			// The keys of a map[string]any: the scalar's text; a null or non-scalar key drops the entry.
			if (key.kind != Raw::Kind::scalar || null_scalar(key)) continue;
			Node k;
			k.kind = Node::Kind::string;
			k.s = key.text;
			Node v;
			d.any(value, v);
			// A later duplicate cannot occur (checked above); replace an earlier entry of the same text.
			auto it = std::find_if(map.entries.begin(), map.entries.end(), [&](auto &e) { return e.first.s == k.s; });
			if (it != map.entries.end()) it->second = std::move(v);
			else map.entries.emplace_back(std::move(k), std::move(v));
		}
	}
	if (!d.errors.empty()) {
		error = "yaml: unmarshal errors:";
		for (const auto &e : d.errors) error += "\n  " + e;
		return std::nullopt;
	}
	std::erase_if(map.entries, [](auto &e) { return e.first.s == "jwt"; });
	Node enable;
	enable.kind = Node::Kind::string;
	enable.s = "enable";
	auto it = std::find_if(map.entries.begin(), map.entries.end(), [](auto &e) { return e.first.s == "authentication"; });
	if (it != map.entries.end()) {
		it->second = enable;
	} else {
		Node key;
		key.kind = Node::Kind::string;
		key.s = "authentication";
		map.entries.emplace_back(std::move(key), std::move(enable));
	}
	Emitter e;
	e.document(map);
	return e.out;
}

namespace {

// fmt.Sprint of a key of a map[interface{}]interface{} as far as a key rule can tell them apart.
std::string key_text(const Node &key)
{
	switch (key.kind) {
	case Node::Kind::string: return key.s;
	case Node::Kind::boolean: return key.flag ? "true" : "false";
	case Node::Kind::integer: return std::to_string(key.i);
	case Node::Kind::uinteger: return std::to_string(key.u);
	case Node::Kind::floating: return format_float(key.f);
	case Node::Kind::null: return "<nil>";
	default: return key.s;
	}
}

void redact_node(Node &n, const std::function<bool(std::string_view)> &secret)
{
	if (n.kind == Node::Kind::map) {
		for (auto &[key, value] : n.entries) {
			if (secret(key_text(key))) {
				value = Node{};
				value.kind = Node::Kind::string;
				value.s = "<hidden>";
			} else {
				redact_node(value, secret);
			}
		}
	} else if (n.kind == Node::Kind::seq) {
		for (auto &item : n.items) redact_node(item, secret);
	}
}

} // namespace

std::optional<std::string> redact(std::string_view data, const std::function<bool(std::string_view key)> &secret)
{
	// rapidyaml reads "key:<TAB>value" as one plain scalar where yaml.v3 reads a mapping, which would leave a
	// credential's value unhidden. yaml.v3 never writes a tab outside an escape: such a file is refused.
	if (data.find('\t') != std::string_view::npos) return std::nullopt;
	std::string error;
	bool empty = false, anchors = false;
	auto root = parse(data, error, empty, &anchors);
	// No document decodes to nil.
	if (!root && empty && error.empty()) return std::string("null\n");
	// A syntax error or an anchor: refused.
	if (!root || anchors) return std::nullopt;
	Decoder d;
	Node value;
	d.any(*root, value);
	if (!d.errors.empty()) return std::nullopt;
	redact_node(value, secret);
	Emitter e;
	e.document(value);
	return e.out;
}

} // namespace nkos::recovery::yamlv3
