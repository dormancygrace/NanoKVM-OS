#include "usb/gojson_read.hpp"

#include "usb/gotext.hpp"

#include <algorithm>
#include <cerrno>
#include <cmath>
#include <cstdlib>
#include <cstring>

namespace nkos::usb::goj {

// The Go server's encoding/json is the json/v2 implementation with the v1 options (the toolchain's default
// GOEXPERIMENT jsonv2): jsontext reads the value (jsonwire's consume functions), and its errors are rewritten into
// v1's words (transformSyntacticError, transformUnmarshalError).

namespace {

constexpr std::size_t max_nesting_depth = 10000;

bool is_space(unsigned char c) { return c == ' ' || c == '\t' || c == '\r' || c == '\n'; }
bool is_hex(unsigned char c) { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'); }
int hex_value(unsigned char c) { return c <= '9' ? c - '0' : (c | 0x20) - 'a' + 10; }

// jsonwire.QuoteRune of the first rune of b.
std::string quote_first_rune(std::string_view b)
{
	std::size_t n;
	char32_t r = gotext::decode_rune(b, n);
	if (r == 0xfffd && n == 1) {
		static const char digits[] = "0123456789abcdef";
		unsigned char c = static_cast<unsigned char>(b[0]);
		std::string hex;
		if (c >= 16) hex += digits[c >> 4];
		hex += digits[c & 15];
		return "'\\x" + hex + "'";
	}
	return gotext::quote_rune(r);
}

// jsonwire.InvalidTextError.Error.
std::string invalid_text(const std::string &label, std::string_view what, const std::string &where)
{
	std::string quoted;
	if (gotext::rune_count(what) == 1) {
		quoted = quote_first_rune(what);
	} else {
		bool escape = false;
		for (std::size_t i = 0; i < what.size();) {
			std::size_t n;
			char32_t r = gotext::decode_rune(what.substr(i), n);
			if (r == '`' || r == 0xfffd || gotext::is_space(r) || !gotext::is_print(r)) escape = true;
			i += n;
		}
		quoted = escape ? gotext::quote(what) : "`" + std::string(what) + "`";
	}
	std::string msg = "invalid " + label + " " + quoted + " " + where;
	if (!msg.empty() && msg.back() == ' ') msg.pop_back();
	return msg;
}

std::string invalid_character(std::string_view rest, const std::string &where)
{
	std::size_t n;
	gotext::decode_rune(rest, n);
	return invalid_text("character", rest.substr(0, n), where);
}

std::string invalid_escape(std::string_view what) { return invalid_text("escape sequence", what, "in string"); }

// utf8.FullRune: p begins with a whole encoding (an invalid one counts as whole).
bool full_rune(std::string_view p)
{
	if (p.empty()) return false;
	auto at = [&](std::size_t i) { return static_cast<unsigned char>(p[i]); };
	unsigned char c = at(0);
	std::size_t size = 1;
	unsigned char lo = 0x80, hi = 0xbf;
	if (c >= 0xc2 && c <= 0xdf) size = 2;
	else if (c >= 0xe0 && c <= 0xef) {
		size = 3;
		if (c == 0xe0) lo = 0xa0;
		if (c == 0xed) hi = 0x9f;
	} else if (c >= 0xf0 && c <= 0xf4) {
		size = 4;
		if (c == 0xf0) lo = 0x90;
		if (c == 0xf4) hi = 0x8f;
	}
	if (p.size() >= size) return true;
	if (p.size() > 1 && (at(1) < lo || at(1) > hi)) return true;
	if (p.size() > 2 && (at(2) < 0x80 || at(2) > 0xbf)) return true;
	return false;
}

// transformSyntacticError's rewording of a jsontext error.
std::string v1_words(std::string msg)
{
	if (auto i = msg.find(" (expecting"); i != std::string::npos && msg.find(" in literal") == std::string::npos) msg.resize(i);
	static const std::pair<const char *, const char *> replace[] = {
		{"object name", "object key"},
		{"at start of value", "looking for beginning of value"},
		{"at start of string", "looking for beginning of object key string"},
		{"after object value", "after object key:value pair"},
		{"in number", "in numeric literal"},
	};
	std::string out;
	for (std::size_t i = 0; i < msg.size();) {
		bool done = false;
		for (auto &[from, to] : replace) {
			std::size_t len = std::strlen(from);
			if (msg.compare(i, len, from) == 0) {
				out += to;
				i += len;
				done = true;
				break;
			}
		}
		if (!done) out += msg[i++];
	}
	return out;
}

// jsontext's consumeValue over a complete input. An error is either the end of the input (eof) or a message.
struct Consumer {
	std::string_view b;
	bool eof = false;
	std::string error;

	bool fail(std::string msg)
	{
		error = std::move(msg);
		return false;
	}
	bool end()
	{
		eof = true;
		return false;
	}
	void space(std::size_t &pos)
	{
		while (pos < b.size() && is_space(static_cast<unsigned char>(b[pos]))) pos++;
	}

	bool literal(std::size_t &pos, std::string_view lit)
	{
		for (std::size_t i = 0; pos + i < b.size() && i < lit.size(); i++)
			if (b[pos + i] != lit[i])
				return fail(invalid_character(b.substr(pos + i), "in literal " + std::string(lit) + " (expecting " +
				                                                    gotext::quote_rune(static_cast<char32_t>(lit[i])) + ")"));
		if (b.size() - pos < lit.size()) return end();
		pos += lit.size();
		return true;
	}

	// ConsumeStringResumable without UTF-8 validation (AllowInvalidUTF8).
	bool string(std::size_t &pos)
	{
		if (pos >= b.size()) return end();
		if (b[pos] != '"') return fail(invalid_character(b.substr(pos), "at start of string (expecting '\"')"));
		std::string_view s = b.substr(pos);
		std::size_t n = 1;
		while (n < s.size()) {
			unsigned char c = static_cast<unsigned char>(s[n]);
			if (c < 0x80 && c >= ' ' && c != '\\' && c != '"') {
				n++;
				continue;
			}
			if (c == '"') {
				pos += n + 1;
				return true;
			}
			std::size_t rn;
			char32_t r = gotext::decode_rune(s.substr(n), rn);
			if (rn > 1) {
				n += rn;
			} else if (r == '\\') {
				if (s.size() < n + 2) return end();
				switch (s[n + 1]) {
				case '/': case '"': case '\\': case 'b': case 'f': case 'n': case 'r': case 't':
					n += 2;
					break;
				case 'u': {
					if (s.size() < n + 6) {
						bool prefix = true;
						for (std::size_t i = 2; n + i < s.size(); i++) prefix = prefix && is_hex(static_cast<unsigned char>(s[n + i]));
						if (prefix) return end();
						return fail(invalid_escape(s.substr(n)));
					}
					for (std::size_t i = 2; i < 6; i++)
						if (!is_hex(static_cast<unsigned char>(s[n + i]))) return fail(invalid_escape(s.substr(n, 6)));
					n += 6;
					break;
				}
				default:
					return fail(invalid_escape(s.substr(n, 2)));
				}
			} else if (r == 0xfffd) {
				// An invalid byte, or a sequence the input ends inside of.
				if (!full_rune(s.substr(n))) return end();
				n++;
			} else {
				return fail(invalid_character(s.substr(n), "in string (expecting non-control character)"));
			}
		}
		return end();
	}

	bool number(std::size_t &pos)
	{
		std::size_t n = pos;
		auto digit = [&](std::size_t i) { return i < b.size() && b[i] >= '0' && b[i] <= '9'; };
		if (b[n] == '-') n++;
		if (n >= b.size()) return end();
		if (b[n] == '0') n++;
		else if (b[n] >= '1' && b[n] <= '9')
			while (digit(n)) n++;
		else
			return fail(invalid_character(b.substr(n), "in number (expecting digit)"));
		if (n < b.size() && b[n] == '.') {
			n++;
			if (n >= b.size()) return end();
			if (!digit(n)) return fail(invalid_character(b.substr(n), "in number (expecting digit)"));
			while (digit(n)) n++;
		}
		if (n < b.size() && (b[n] == 'e' || b[n] == 'E')) {
			n++;
			if (n < b.size() && (b[n] == '-' || b[n] == '+')) n++;
			if (n >= b.size()) return end();
			if (!digit(n)) return fail(invalid_character(b.substr(n), "in number (expecting digit)"));
			while (digit(n)) n++;
		}
		pos = n;
		return true;
	}

	// One value at pos (after its leading space), nesting by an explicit stack. depth is the decoder's (1 at the
	// top level).
	bool value(std::size_t &pos, std::size_t depth)
	{
		std::vector<char> open;
		enum { at_value, at_name, after_value } next = at_value;
		for (;;) {
			if (next == at_value) {
				char c = b[pos];
				if (c == '{' || c == '[') {
					if (depth + open.size() == max_nesting_depth + 1) return fail("exceeded max depth");
					pos++;
					space(pos);
					if (pos >= b.size()) return end();
					if (b[pos] == (c == '{' ? '}' : ']')) {
						pos++;
						next = after_value;
						continue;
					}
					open.push_back(c);
					next = c == '{' ? at_name : at_value;
					continue;
				}
				bool ok;
				switch (c) {
				case 'n': ok = literal(pos, "null"); break;
				case 't': ok = literal(pos, "true"); break;
				case 'f': ok = literal(pos, "false"); break;
				case '"': ok = string(pos); break;
				default:
					if (c == '-' || (c >= '0' && c <= '9')) ok = number(pos);
					else return fail(invalid_character(b.substr(pos), "at start of value"));
				}
				if (!ok) return false;
				next = after_value;
				continue;
			}
			if (next == at_name) {
				space(pos);
				if (pos >= b.size()) return end();
				if (!string(pos)) return false;
				space(pos);
				if (pos >= b.size()) return end();
				if (b[pos] != ':') return fail(invalid_character(b.substr(pos), "after object name (expecting ':')"));
				pos++;
				space(pos);
				if (pos >= b.size()) return end();
				next = at_value;
				continue;
			}
			// after_value
			if (open.empty()) return true;
			space(pos);
			if (pos >= b.size()) return end();
			char c = b[pos];
			if (open.back() == '{') {
				if (c == ',') {
					pos++;
					next = at_name;
				} else if (c == '}') {
					pos++;
					open.pop_back();
				} else {
					return fail(invalid_character(b.substr(pos), "after object value (expecting ',' or '}')"));
				}
			} else {
				if (c == ',') {
					pos++;
					space(pos);
					if (pos >= b.size()) return end();
					next = at_value;
				} else if (c == ']') {
					pos++;
					open.pop_back();
				} else {
					return fail(invalid_character(b.substr(pos), "after array element (expecting ',' or ']')"));
				}
			}
		}
	}

	// ReadValue at the top level: the value's start and end, or false (eof with no value at all: empty).
	bool top(std::size_t &start, std::size_t &end_pos, bool &empty)
	{
		std::size_t pos = 0;
		empty = false;
		space(pos);
		if (pos >= b.size()) {
			empty = true;
			return end();
		}
		if (b[pos] == ':' || b[pos] == ',')
			return fail(invalid_character(b.substr(pos), "at start of value"));
		start = pos;
		if (!value(pos, 1)) return false;
		end_pos = pos;
		return true;
	}
};

} // namespace

std::string check_valid(std::string_view data)
{
	Consumer c;
	c.b = data;
	std::size_t start, end;
	bool empty;
	if (!c.top(start, end, empty)) return c.eof ? "unexpected end of JSON input" : v1_words(c.error);
	c.space(end);
	if (end < data.size()) return v1_words(invalid_character(data.substr(end), "after top-level value"));
	return {};
}

bool first_value(std::string_view data, std::size_t &end, std::string &error)
{
	Consumer c;
	c.b = data;
	std::size_t start;
	bool empty;
	if (!c.top(start, end, empty)) {
		error = c.eof ? (empty ? "EOF" : "unexpected EOF") : v1_words(c.error);
		return false;
	}
	return true;
}

bool more(std::string_view data, std::size_t end)
{
	for (std::size_t i = end; i < data.size(); i++) {
		unsigned char c = static_cast<unsigned char>(data[i]);
		if (is_space(c)) continue;
		return c != ']' && c != '}';
	}
	return false;
}

std::string Reader::unquote(std::string_view s)
{
	s = s.substr(1, s.size() - 2);
	std::string out;
	out.reserve(s.size());
	for (std::size_t r = 0; r < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[r]);
		if (c == '\\') {
			char e = s[r + 1];
			switch (e) {
			case 'b': out += '\b'; r += 2; continue;
			case 'f': out += '\f'; r += 2; continue;
			case 'n': out += '\n'; r += 2; continue;
			case 'r': out += '\r'; r += 2; continue;
			case 't': out += '\t'; r += 2; continue;
			case 'u': {
				auto u4 = [&](std::size_t at) -> long {
					if (s.size() < at + 6 || s[at] != '\\' || s[at + 1] != 'u') return -1;
					long v = 0;
					for (std::size_t i = at + 2; i < at + 6; i++) {
						if (!is_hex(static_cast<unsigned char>(s[i]))) return -1;
						v = v * 16 + hex_value(static_cast<unsigned char>(s[i]));
					}
					return v;
				};
				long rr = u4(r);
				r += 6;
				if (rr >= 0xd800 && rr < 0xe000) {
					long rr1 = u4(r);
					if (rr < 0xdc00 && rr1 >= 0xdc00 && rr1 < 0xe000) {
						r += 6;
						gotext::append_rune(out, static_cast<char32_t>(((rr - 0xd800) << 10 | (rr1 - 0xdc00)) + 0x10000));
						continue;
					}
					rr = 0xfffd;
				}
				gotext::append_rune(out, static_cast<char32_t>(rr));
				continue;
			}
			default: out += e; r += 2; continue;
			}
		}
		if (c < 0x80) {
			out += static_cast<char>(c);
			r++;
			continue;
		}
		std::size_t size;
		char32_t rune = gotext::decode_rune(s.substr(r), size);
		if (rune == 0xfffd && size == 1) out += "\xef\xbf\xbd";
		else out.append(s.substr(r, size));
		r += size;
	}
	return out;
}

void Reader::space()
{
	while (pos_ < data_.size() && is_space(static_cast<unsigned char>(data_[pos_]))) pos_++;
}

char Reader::peek()
{
	space();
	return pos_ < data_.size() ? data_[pos_] : '\0';
}

// A string, number or literal (the input is valid JSON).
std::string_view Reader::literal()
{
	space();
	std::size_t start = pos_;
	if (data_[pos_] == '"') {
		for (pos_++; data_[pos_] != '"'; pos_++)
			if (data_[pos_] == '\\') pos_++;
		pos_++;
	} else {
		while (pos_ < data_.size()) {
			char c = data_[pos_];
			if (c == ',' || c == '}' || c == ']' || c == ':' || is_space(static_cast<unsigned char>(c))) break;
			pos_++;
		}
	}
	return data_.substr(start, pos_ - start);
}

void Reader::skip()
{
	char c = peek();
	if (c != '{' && c != '[') {
		literal();
		return;
	}
	std::size_t depth = 0;
	for (; pos_ < data_.size(); pos_++) {
		c = data_[pos_];
		if (c == '"') {
			literal();
			pos_--;
		} else if (c == '{' || c == '[') {
			depth++;
		} else if (c == '}' || c == ']') {
			if (--depth == 0) {
				pos_++;
				return;
			}
		}
	}
}

void Reader::type_error(std::string value, std::string_view type)
{
	if (!error_.empty()) return;
	if (pointer_.empty()) {
		error_ = "json: cannot unmarshal " + value + " into Go value of type " + std::string(type);
		return;
	}
	// The JSON pointer of the value ('~' and '/' escaped), with dots for slashes.
	std::string field;
	for (auto &token : pointer_) {
		if (!field.empty()) field += '.';
		for (char c : token) {
			if (c == '~') field += "~0";
			else if (c == '/') field += "~1";
			else field += c;
		}
	}
	std::string last = field.substr(field.rfind('.') == std::string::npos ? 0 : field.rfind('.') + 1);
	bool index = !last.empty() && last.find_first_not_of("0123456789") == std::string::npos;
	error_ = "json: cannot unmarshal " + value + " into " + (index ? "" : "Go struct field ") + root_ + "." + field +
	         " of type " + std::string(type);
}

namespace {
const char *kind_of(char c)
{
	switch (c) {
	case '{': return "object";
	case '[': return "array";
	case '"': return "string";
	case 't': case 'f': return "bool";
	default: return "number";
	}
}

// The key matches the field name case-folded (strings.EqualFold): ASCII, and the two runes that fold into it, the
// Kelvin sign to K and the long s to S. Field names are ASCII.
bool fold_equal(std::string_view key, std::string_view name)
{
	std::size_t i = 0, j = 0;
	auto upper = [](unsigned char c) { return c >= 'a' && c <= 'z' ? static_cast<unsigned char>(c - 32) : c; };
	while (i < key.size() && j < name.size()) {
		unsigned char c = static_cast<unsigned char>(key[i]);
		unsigned char want = upper(static_cast<unsigned char>(name[j]));
		if (c < 0x80) {
			if (upper(c) != want) return false;
			i++;
		} else {
			std::size_t size;
			char32_t r = gotext::decode_rune(key.substr(i), size);
			if (!((r == 0x212a && want == 'K') || (r == 0x17f && want == 'S'))) return false;
			i += size;
		}
		j++;
	}
	return i == key.size() && j == name.size();
}
} // namespace

void Reader::object(std::string_view, std::string_view type, const std::vector<Field> &fields)
{
	char c = peek();
	if (c == 'n') return (void)literal();
	if (c != '{') {
		type_error(kind_of(c), type);
		return skip();
	}
	pos_++;
	for (;;) {
		c = peek();
		if (c == '}') {
			pos_++;
			break;
		}
		if (c == ',') {
			pos_++;
			continue;
		}
		std::string key = unquote(literal());
		space();
		pos_++;  // ':'
		const Field *match = nullptr;
		for (auto &field : fields)
			if (field.name == key) match = &field;
		if (!match)
			for (auto &field : fields)
				if (fold_equal(key, field.name)) {
					match = &field;
					break;
				}
		if (match) {
			pointer_.push_back(key);
			match->read(*this);
			pointer_.pop_back();
		} else {
			skip();
		}
	}
}

void Reader::map(std::string_view type, const std::function<void()> &on_make,
                 const std::function<void(const std::string &, Reader &)> &value)
{
	char c = peek();
	if (c != '{') {
		if (c == 'n') {
			literal();
			return;
		}
		type_error(kind_of(c), type);
		return skip();
	}
	on_make();
	pos_++;
	for (;;) {
		c = peek();
		if (c == '}') {
			pos_++;
			break;
		}
		if (c == ',') {
			pos_++;
			continue;
		}
		std::string key = unquote(literal());
		space();
		pos_++;
		pointer_.push_back(key);
		value(key, *this);
		pointer_.pop_back();
	}
}

void Reader::slice(std::string_view type, const std::function<void()> &on_null,
                   const std::function<void(std::size_t, Reader &)> &element, const std::function<void(std::size_t)> &done)
{
	char c = peek();
	if (c == 'n') {
		literal();
		return on_null();
	}
	if (c != '[') {
		type_error(kind_of(c), type);
		return skip();
	}
	pos_++;
	std::size_t i = 0;
	for (;;) {
		c = peek();
		if (c == ']') {
			pos_++;
			break;
		}
		if (c == ',') {
			pos_++;
			continue;
		}
		pointer_.push_back(std::to_string(i));
		element(i++, *this);
		pointer_.pop_back();
	}
	done(i);
}

void Reader::pointer_object(std::string_view go_struct, std::string_view type,
                            const std::function<std::vector<Field>()> &allocate, const std::function<void()> &on_null)
{
	char c = peek();
	if (c == 'n') {
		literal();
		return on_null();
	}
	auto fields = allocate();
	object(go_struct, type, fields);
}

void Reader::string(std::string &out, std::string_view type)
{
	char c = peek();
	if (c == 'n') return (void)literal();
	if (c != '"') {
		type_error(kind_of(c), type);
		return skip();
	}
	out = unquote(literal());
}

void Reader::integer(long long &out, std::string_view type)
{
	char c = peek();
	if (c == 'n') return (void)literal();
	if (c == '{' || c == '[' || c == '"' || c == 't' || c == 'f') {
		type_error(kind_of(c), type);
		return skip();
	}
	std::string text(literal());
	// jsonwire.ParseUint on the digits: an integer literal within int64.
	bool ok = text.find_first_of(".eE") == std::string::npos;
	long long v = 0;
	if (ok) {
		errno = 0;
		char *end = nullptr;
		v = std::strtoll(text.c_str(), &end, 10);
		ok = errno == 0 && end && *end == '\0';
	}
	if (!ok) return type_error("number " + text, type);
	out = v;
}

void Reader::boolean(bool &out, std::string_view type)
{
	char c = peek();
	if (c == 'n') return (void)literal();
	if (c != 't' && c != 'f') {
		type_error(kind_of(c), type);
		return skip();
	}
	out = literal() == "true";
}

void Reader::float64(double &out, std::string_view type)
{
	char c = peek();
	if (c == 'n') return (void)literal();
	if (c == '{' || c == '[' || c == '"' || c == 't' || c == 'f') {
		type_error(kind_of(c), type);
		return skip();
	}
	std::string text(literal());
	// strconv.ParseFloat: only an overflow is an error (an underflow is zero).
	double v = std::strtod(text.c_str(), nullptr);
	if (std::isinf(v)) return type_error("number " + text, type);
	out = v;
}

const Any *Any::get(std::string_view name) const
{
	for (auto &[k, v] : members)
		if (k == name) return &v;
	return nullptr;
}

void Reader::any(Any &out)
{
	out = {};
	char c = peek();
	if (c == 'n') {
		literal();
	} else if (c == 't' || c == 'f') {
		out.kind = Any::boolean;
		out.flag = literal() == "true";
	} else if (c == '"') {
		out.kind = Any::string;
		out.text = unquote(literal());
	} else if (c == '[') {
		out.kind = Any::array;
		pos_++;
		for (std::size_t i = 0;; i++) {
			c = peek();
			if (c == ']') {
				pos_++;
				break;
			}
			if (c == ',') {
				pos_++;
				continue;
			}
			out.items.emplace_back();
			pointer_.push_back(std::to_string(i));
			any(out.items.back());
			pointer_.pop_back();
		}
	} else if (c == '{') {
		out.kind = Any::object;
		pos_++;
		for (;;) {
			c = peek();
			if (c == '}') {
				pos_++;
				break;
			}
			if (c == ',') {
				pos_++;
				continue;
			}
			std::string key = unquote(literal());
			space();
			pos_++;
			Any v;
			pointer_.push_back(key);
			any(v);
			pointer_.pop_back();
			auto it = std::lower_bound(out.members.begin(), out.members.end(), key,
			                           [](const auto &m, const std::string &k) { return m.first < k; });
			if (it != out.members.end() && it->first == key) it->second = std::move(v);
			else out.members.emplace(it, key, std::move(v));
		}
	} else {
		out.kind = Any::number;
		std::string text(literal());
		out.value = std::strtod(text.c_str(), nullptr);
		if (std::isinf(out.value)) type_error("number " + text, "float64");
	}
}

void Reader::raw(std::string &out)
{
	space();
	std::size_t start = pos_;
	skip();
	out.assign(data_.substr(start, pos_ - start));
}

void Reader::pointer_boolean(std::optional<bool> &out)
{
	char c = peek();
	if (c == 'n') {
		literal();
		out.reset();
		return;
	}
	if (!out) out = false;
	boolean(*out);
}

bool unmarshal(std::string_view data, std::string_view root, const std::function<void(Reader &)> &read, std::string &error)
{
	error = check_valid(data);
	if (!error.empty()) return false;
	Reader r(data, root);
	read(r);
	error = r.error();
	return error.empty();
}

bool decode(std::string_view data, std::string_view root, const std::function<void(Reader &)> &read, std::size_t &end,
            std::string &error)
{
	if (!first_value(data, end, error)) return false;
	Reader r(data.substr(0, end), root);
	read(r);
	error = r.error();
	return error.empty();
}

std::optional<std::string> FormValues::get(std::string_view key) const
{
	for (auto &[k, v] : values)
		if (k == key) return v;
	return std::nullopt;
}

namespace {
// url.QueryUnescape.
bool unescape(std::string_view s, std::string &out)
{
	out.clear();
	for (std::size_t i = 0; i < s.size(); i++) {
		char c = s[i];
		if (c == '+') {
			out += ' ';
		} else if (c == '%') {
			if (i + 2 >= s.size() || !is_hex(static_cast<unsigned char>(s[i + 1])) || !is_hex(static_cast<unsigned char>(s[i + 2])))
				return false;
			out += static_cast<char>(hex_value(static_cast<unsigned char>(s[i + 1])) * 16 + hex_value(static_cast<unsigned char>(s[i + 2])));
			i += 2;
		} else {
			out += c;
		}
	}
	return true;
}
} // namespace

bool parse_form(std::string_view text, FormValues &out)
{
	bool ok = true;
	while (!text.empty()) {
		auto amp = text.find('&');
		std::string_view pair = text.substr(0, amp);
		text = amp == std::string_view::npos ? std::string_view() : text.substr(amp + 1);
		if (pair.find(';') != std::string_view::npos) {
			ok = false;
			continue;
		}
		if (pair.empty()) continue;
		auto eq = pair.find('=');
		std::string key, value;
		if (!unescape(pair.substr(0, eq), key) ||
		    !unescape(eq == std::string_view::npos ? std::string_view() : pair.substr(eq + 1), value)) {
			ok = false;
			continue;
		}
		out.values.emplace_back(std::move(key), std::move(value));
	}
	return ok;
}

std::string media_type(std::string_view content_type)
{
	// Gin's filterFlags: up to the first ' ' or ';'.
	auto cut = content_type.find_first_of(" ;");
	return std::string(content_type.substr(0, cut));
}

} // namespace nkos::usb::goj
