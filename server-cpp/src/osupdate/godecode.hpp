#pragma once

// JSON read the way Go's encoding/json reads it, for files and requests whose Go counterpart decodes into structs:
// strict syntax (Go's scanner), strings with invalid UTF-8 or lone surrogates turned into U+FFFD, object keys matched
// to field names without regard to case (bytes.EqualFold), the last of duplicate keys winning, null leaving a value
// as it was (or clearing a pointer, slice or []byte), and a member of the wrong type an error that does not stop the
// other members from being read (json.Unmarshal's UnmarshalTypeError).

#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace nkos::godecode {

struct Value {
	enum class Kind { null, boolean, number, string, array, object };
	Kind kind = Kind::null;
	bool flag = false;
	std::string text;  // a number's literal, or a string's decoded text
	std::vector<Value> items;
	std::vector<std::pair<std::string, Value>> members;
};

// One value from the start of data (leading whitespace skipped); end is the offset after it. False on a syntax
// error.
bool parse(std::string_view data, Value &out, std::size_t &end);

// json.Unmarshal's input: one value and nothing but whitespace after it.
std::optional<Value> parse_document(std::string_view data);
// The same with Go's error text ("unexpected end of JSON input", "invalid character 'x' after top-level value", ...).
std::optional<Value> unmarshal(std::string_view data, std::string &error);
// json.Decoder.Decode's first value (Gin's ShouldBindJSON): what follows it is not read; the error is "EOF" for
// no value, "unexpected EOF" for a value cut short, else the scanner's text.
std::optional<Value> decode_first(std::string_view data, std::string &error);
// The JSON type as Go's UnmarshalTypeError names it ("string", "number", "bool", "array", "object").
std::string kind_name(const Value &v);

// Only JSON whitespace (space, tab, CR, LF).
bool only_space(std::string_view s);

// Go's match of an object key to a field name (ASCII fields: EqualFold, with the Kelvin sign and the long s).
bool key_matches(std::string_view key, std::string_view field);

// Decoding into Go types. Each returns false for a value of another JSON type (the target is left as it was);
// null leaves the target unchanged, except where noted.
bool get(const Value &v, std::string &out);
bool get(const Value &v, bool &out);
// Integers: Go parses the literal with strconv.ParseInt/ParseUint (no fraction or exponent) and checks the range.
bool get_int(const Value &v, std::int64_t &out, std::int64_t min, std::int64_t max);
bool get_uint(const Value &v, std::uint64_t &out, std::uint64_t max);
// []byte: a base64 string (StdEncoding, CR and LF ignored); null clears it (nullopt = Go's nil).
bool get_bytes(const Value &v, std::optional<std::string> &out);
// []string: null clears it; an element of another type is an error and stays "".
bool get_strings(const Value &v, std::optional<std::vector<std::string>> &out);
// time.Time: a string in RFC 3339 form (Go's strict parse); the text is kept.
bool get_time(const Value &v, std::string &out);

} // namespace nkos::godecode
