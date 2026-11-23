#pragma once

// JSON as Go's encoding/json reads and writes it, for the notification settings, the page's requests and the bodies
// sent to the services. Reading: a tree of the text (strings with invalid UTF-8 and lone surrogates as U+FFFD), then
// filled into structs with Go's rules (a key matches a field exactly or else ignoring case, unknown keys ignored,
// null leaves a scalar as it is and empties a list or map, any other type mismatch is an error). Writing: values
// built in order (struct fields as declared, map keys sorted), compact or indented as json.MarshalIndent.

#include <cstdint>
#include <initializer_list>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace nkos::notify::json {

struct Value {
	enum class Kind { null, boolean, number, string, array, object };
	Kind kind = Kind::null;
	bool boolean = false;
	std::string text;  // a string's contents or a number's literal
	std::vector<Value> items;
	std::vector<std::pair<std::string, Value>> members;
	bool is_null() const { return kind == Kind::null; }
};

// json.Decoder.Decode: the first value of text (what follows it is not read). False on a syntax error or no value.
bool decode_first(std::string_view text, Value &out);
// json.Unmarshal: text must be one value.
bool unmarshal(std::string_view text, Value &out);

// Filling: each returns false on a type mismatch (Go's UnmarshalTypeError); null leaves the target as it is.
bool get(const Value &v, std::string &out);
bool get(const Value &v, bool &out);
bool get(const Value &v, std::int64_t &out);
bool get(const Value &v, int &out);
// A list or map that may be nil: null makes it nullopt.
bool get(const Value &v, std::optional<std::vector<std::string>> &out);
bool get(const Value &v, std::optional<std::map<std::string, std::string>> &out);

// The members of an object for a struct: calls field(name, value) with the struct's field name matched (exactly,
// else ignoring ASCII case) among names; unknown keys are skipped. False when v is not an object or null, or
// field returns false.
template <typename F>
bool members(const Value &v, std::initializer_list<std::string_view> names, F &&field);

// ---- writing ----

struct Out {
	enum class Kind { raw, array, object };
	Kind kind = Kind::raw;
	std::string raw;  // an encoded scalar
	std::vector<Out> items;
	std::vector<std::pair<std::string, Out>> members;

	static Out null() { return lit("null"); }
	static Out lit(std::string raw);
	static Out str(std::string_view s);
	static Out boolean(bool b) { return lit(b ? "true" : "false"); }
	static Out number(std::int64_t n) { return lit(std::to_string(n)); }
	static Out array() { Out o; o.kind = Kind::array; return o; }
	static Out object() { Out o; o.kind = Kind::object; return o; }
	static Out strings(const std::vector<std::string> &list);
	// A []string that may be nil.
	static Out strings(const std::optional<std::vector<std::string>> &list);
	// A map[string]string (sorted keys), nil as null.
	static Out string_map(const std::optional<std::map<std::string, std::string>> &map);

	Out &add(Out item) { items.push_back(std::move(item)); return *this; }
	Out &set(std::string key, Out value) { members.emplace_back(std::move(key), std::move(value)); return *this; }
	// A map's members are sorted by key (as Go writes a map).
	Out &sort_keys();
};

// json.Marshal and json.MarshalIndent(v, "", "  ").
std::string marshal(const Out &v);
std::string marshal_indent(const Out &v);

bool fold_equal(std::string_view a, std::string_view b);

template <typename F>
bool members(const Value &v, std::initializer_list<std::string_view> names, F &&field)
{
	if (v.is_null()) return true;
	if (v.kind != Value::Kind::object) return false;
	for (const auto &[key, value] : v.members) {
		std::string_view match;
		for (auto n : names)
			if (n == key) match = n;
		if (match.empty())
			for (auto n : names)
				if (match.empty() && fold_equal(n, key)) match = n;
		if (!match.empty() && !field(match, value)) return false;
	}
	return true;
}

} // namespace nkos::notify::json
