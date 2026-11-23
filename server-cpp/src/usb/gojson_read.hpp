#pragma once

// JSON read as the Go server's encoding/json reads it into its structs, where the result or the error text shows:
// the scanner's syntax errors, json.Unmarshal (the whole input) and json.Decoder.Decode/More (the first value),
// field names matched exactly or case-folded, the first type error kept with its struct.field context while the rest
// is still decoded, null leaving a value alone, strings with invalid UTF-8 or lone surrogates read as U+FFFD, and
// slices reusing their elements. No recursion on what the input nests: unknown values are skipped by scanning.

#include <cstddef>
#include <functional>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::usb::goj {

// Go's checkValid/scanner over data: "" when it is one JSON value (with surrounding space), else the SyntaxError
// text ("invalid character 'x' after object key", "unexpected end of JSON input").
std::string check_valid(std::string_view data);

// Decoder.Decode's framing over a complete input: the end of the first value (end), or the error ("EOF",
// "unexpected EOF", a syntax error).
bool first_value(std::string_view data, std::size_t &end, std::string &error);
// Decoder.More after a Decode that ended at end.
bool more(std::string_view data, std::size_t end);

class Reader;

// A value decoded into Go's `any`: numbers as float64, objects as maps (a repeated name keeps the last value).
struct Any {
	enum Kind { null, boolean, number, string, array, object } kind = null;
	bool flag = false;
	double value = 0;
	std::string text;
	std::vector<Any> items;
	std::vector<std::pair<std::string, Any>> members;  // sorted by name, unique
	bool operator==(const Any &) const = default;
	const Any *get(std::string_view name) const;
};

// A struct field: its JSON name and how its value is read.
struct Field {
	std::string_view name;
	std::function<void(Reader &)> read;
};

// Reads one value (validated beforehand) into typed targets. root is the name of the target's type ("Report") for
// the errors' Struct.Field context.
class Reader {
public:
	explicit Reader(std::string_view value, std::string_view root = {}) : data_(value), root_(root) {}

	// The first type error (Go's UnmarshalTypeError text, with the root type and the value's JSON pointer as its
	// dotted field path); empty when there was none.
	const std::string &error() const { return error_; }

	// A struct (go_struct: its name, unused now that errors name the root; type: e.g. "hostsensors.Report"). null
	// leaves it; another kind is a type error.
	void object(std::string_view go_struct, std::string_view type, const std::vector<Field> &fields);
	// A map of string keys (type: e.g. "map[string]bool"); value reads each element into a fresh zero value, then
	// the caller stores it under key. A null map leaves it; an object makes it non-nil (on_make).
	void map(std::string_view type, const std::function<void()> &on_make,
	         const std::function<void(const std::string &key, Reader &)> &value);
	// A slice: on_null for null, element(i) for each element in order, then done(n) with the count.
	void slice(std::string_view type, const std::function<void()> &on_null, const std::function<void(std::size_t, Reader &)> &element,
	           const std::function<void(std::size_t)> &done);
	// A pointer to a struct (Go allocates it for any value but null): allocate runs then, and returns the fields.
	void pointer_object(std::string_view go_struct, std::string_view type, const std::function<std::vector<Field>()> &allocate,
	                    const std::function<void()> &on_null);

	void string(std::string &out, std::string_view type = "string");
	void integer(long long &out, std::string_view type = "int");
	void boolean(bool &out, std::string_view type = "bool");
	void float64(double &out, std::string_view type = "float64");
	// *bool: null sets it to nil; any other value allocates it first.
	void pointer_boolean(std::optional<bool> &out);
	void skip();
	// any (reverses its input's nesting with the call stack: for trusted input only).
	void any(Any &out);
	// json.RawMessage: the value's text as it stands.
	void raw(std::string &out);
	bool is_null() { return peek() == 'n'; }
	// A type error for a value read by hand (Go's UnmarshalTypeError text).
	void fail_type(std::string value, std::string_view type) { type_error(std::move(value), type); }

	// Go's unquote of a JSON string literal (with its quotes).
	static std::string unquote(std::string_view literal);

private:
	void space();
	char peek();
	std::string_view literal();
	void type_error(std::string value, std::string_view type);

	std::string_view data_;
	std::string root_;
	std::size_t pos_ = 0;
	std::string error_;
	std::vector<std::string> pointer_;
};

// json.Unmarshal: false with Go's error text (syntax first, then the first type error). read reads the value.
bool unmarshal(std::string_view data, std::string_view root, const std::function<void(Reader &)> &read, std::string &error);
// json.NewDecoder(data).Decode: the first value only; end is where it ended (for more()).
bool decode(std::string_view data, std::string_view root, const std::function<void(Reader &)> &read, std::size_t &end,
            std::string &error);

// Gin's ShouldBind/ShouldBindJSON for a body: binding picks JSON for "application/json" (as Gin compares it: the
// media type exactly, parameters cut off) or always (json_only), the form for the rest. form gets the
// url-encoded body and query merged (the body's value first) and returns whether binding succeeded.
struct FormValues {
	std::vector<std::pair<std::string, std::string>> values;
	// The first value of a key; nullopt when absent.
	std::optional<std::string> get(std::string_view key) const;
};
// url.ParseQuery's rules (no semicolons, valid escapes); false when Go's ParseForm fails.
bool parse_form(std::string_view text, FormValues &out);
std::string media_type(std::string_view content_type);

} // namespace nkos::usb::goj
