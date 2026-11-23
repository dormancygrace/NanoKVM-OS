#pragma once

// JSON values as Go's encoding/json sees them when it decodes into interface{} (map[string]any, []any, float64,
// string, bool, nil) and writes them back: the AI families keep files other programs own (PicoClaw's config.json),
// answer JSON-RPC with arbitrary ids and params, and validate tool arguments the way the Go MCP SDK does, so they
// work on generic values. Objects keep their keys in the order read; sort_keys() gives Go's map order. Numbers keep
// the text they were read from beside their float64 value.

#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace nkos::ai::json {

struct Value {
	enum class Kind { null, boolean, number, string, array, object };
	Kind kind = Kind::null;
	bool b = false;
	double num = 0;
	std::string str;  // string value, or the literal of a number
	std::vector<Value> arr;
	std::vector<std::pair<std::string, Value>> obj;
	std::size_t offset = 0, end = 0;  // where the value was read (parse), for error messages

	Value() = default;
	static Value null() { return {}; }
	static Value boolean(bool v);
	static Value number(double v);
	static Value string(std::string v);
	static Value array(std::vector<Value> items = {});
	static Value object();

	bool is_null() const { return kind == Kind::null; }
	bool is_object() const { return kind == Kind::object; }
	bool is_array() const { return kind == Kind::array; }
	bool is_string() const { return kind == Kind::string; }
	bool is_number() const { return kind == Kind::number; }
	bool is_bool() const { return kind == Kind::boolean; }

	// Object members (the last one of a duplicated key, as Go keeps).
	const Value *get(std::string_view key) const;
	Value *get(std::string_view key);
	// Sets or replaces a member (replaced in place, new keys at the end).
	Value &set(std::string_view key, Value v);
	bool erase(std::string_view key);
};

struct ParseError {
	enum class Kind { eof, syntax } kind = Kind::syntax;
	std::size_t offset = 0;
	std::string message;  // encoding/json's text: "unexpected end of JSON input", "invalid character 'x' ..."
};

// Strict JSON, as encoding/json accepts it: one value, surrounding white space only. Invalid UTF-8 in strings
// becomes U+FFFD; duplicate keys stay (get() finds the last).
std::optional<ParseError> parse(std::string_view text, Value &out);
// json.Decoder's Decode: the first value; what follows it is left unread.
std::optional<ParseError> parse_first(std::string_view text, Value &out);
// json.Compact of valid JSON (white space outside strings removed).
std::string compact(std::string_view raw);

// json.Marshal (html: <, >, & escaped) and json.MarshalIndent(v, "", indent).
std::string marshal(const Value &v, bool html = true);
std::string marshal_indent(const Value &v, std::string_view indent = "  ", bool html = true);
// A JSON string as encoding/json writes it (html: escape <, >, &).
std::string quote(std::string_view s, bool html = true);

// Sorts object keys recursively (Go writes maps in key order); the last duplicate of a key wins.
void sort_keys(Value &v);

// Go's formatting of a float64: json.Marshal, fmt %v, fmt %f and big.Rat's String of SetFloat64.
std::string float_json(double f);
std::string float_v(double f);
std::string float_f(double f);
std::string float_rat(double f);
// fmt's %v of a decoded interface{} value (map[a:1 b:[x y]], <nil>, true, ...).
std::string go_v(const Value &v);
// strconv.Quote.
std::string go_quote(std::string_view s);

// Runes of a UTF-8 string as Go ranges over it (an invalid byte is one U+FFFD).
std::vector<char32_t> runes(std::string_view s);
void append_utf8(std::string &out, char32_t r);
std::string utf8(const std::vector<char32_t> &runes);

// strings.TrimSpace (Unicode white space) and strings.ToLower of ASCII-only use.
std::string trim_space(std::string_view s);
std::string to_lower(std::string_view s);
std::string to_upper(std::string_view s);

} // namespace nkos::ai::json
