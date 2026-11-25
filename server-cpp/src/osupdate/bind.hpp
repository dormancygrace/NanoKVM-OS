#pragma once

// Gin's ShouldBindJSON of the update routes' request structs (json.Decoder.Decode of the first value): Go's error
// text ("EOF", "unexpected EOF", the scanner's, an UnmarshalTypeError naming the struct field), or "" with the
// fields set.

#include "osupdate/godecode.hpp"

#include <string>
#include <vector>

namespace nkos::osupdate {

// Gin's ShouldBindJSON into a struct of string or bool fields: Go's error text, or "" with the fields set.
struct BindField {
	const char *name;
	std::string *text = nullptr;  // a string field
	bool *flag = nullptr;         // a bool field
	bool *present = nullptr;      // set when a non-null value was read (a *bool field)
};

inline std::string bind(const std::string &body, const char *struct_name, const char *type_name, std::vector<BindField> fields)
{
	std::string error;
	auto doc = godecode::decode_first(body, error);
	if (!doc) return error;
	using K = godecode::Value::Kind;
	if (doc->kind == K::null) return "";
	if (doc->kind != K::object) return "json: cannot unmarshal " + godecode::kind_name(*doc) + " into Go value of type " + type_name;
	std::string first;
	for (const auto &[k, v] : doc->members)
		for (auto &f : fields) {
			if (!godecode::key_matches(k, f.name)) continue;
			bool ok = f.text ? godecode::get(v, *f.text) : godecode::get(v, *f.flag);
			// A *bool is allocated for any value but null, also when the value then does not fit.
			if (f.present) *f.present = v.kind != K::null;
			if (!ok && first.empty())
				first = "json: cannot unmarshal " + godecode::kind_name(v) + " into Go struct field " + struct_name + "." + f.name +
				        " of type " + (f.text ? "string" : "bool");
			break;
		}
	return first;
}

} // namespace nkos::osupdate
