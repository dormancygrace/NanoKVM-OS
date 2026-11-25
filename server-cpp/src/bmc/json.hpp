#pragma once

// A JSON value written as Go's encoding/json writes a map[string]any: object keys sorted by their bytes, strings with
// Go's escaping (common/gojson.hpp), float64 numbers in Go's format. For the Redfish resources, which the Go server
// builds as maps.

#include "common/gojson.hpp"
#include "network/util.hpp"

#include <cstdint>
#include <map>
#include <string>
#include <utility>
#include <vector>

namespace nkos::bmc {

struct Json {
	enum class Kind { null, boolean, integer, number, string, array, object } kind = Kind::null;
	bool flag = false;
	std::int64_t integer = 0;
	double number = 0;
	std::string text;
	std::vector<Json> items;
	std::map<std::string, Json> members;

	Json() = default;
	Json(std::nullptr_t) {}
	Json(bool b) : kind(Kind::boolean), flag(b) {}
	Json(int i) : kind(Kind::integer), integer(i) {}
	Json(std::int64_t i) : kind(Kind::integer), integer(i) {}
	Json(double d) : kind(Kind::number), number(d) {}
	Json(const char *s) : kind(Kind::string), text(s) {}
	Json(std::string s) : kind(Kind::string), text(std::move(s)) {}

	static Json object(std::initializer_list<std::pair<const std::string, Json>> list)
	{
		Json j;
		j.kind = Kind::object;
		j.members = std::map<std::string, Json>(list);
		return j;
	}
	static Json array(std::vector<Json> list = {})
	{
		Json j;
		j.kind = Kind::array;
		j.items = std::move(list);
		return j;
	}
	static Json strings(const std::vector<std::string> &list)
	{
		Json j = array();
		for (const auto &s : list) j.items.emplace_back(s);
		return j;
	}
	Json &operator[](const std::string &key) { return members[key]; }

	void write(std::string &out) const
	{
		switch (kind) {
		case Kind::null: out += "null"; break;
		case Kind::boolean: out += flag ? "true" : "false"; break;
		case Kind::integer: out += std::to_string(integer); break;
		case Kind::number: out += network::json_float(number); break;
		case Kind::string: out += gojson::quote(text); break;
		case Kind::array:
			out += '[';
			for (std::size_t i = 0; i < items.size(); i++) {
				if (i) out += ',';
				items[i].write(out);
			}
			out += ']';
			break;
		case Kind::object: {
			out += '{';
			bool first = true;
			for (const auto &[k, v] : members) {
				if (!first) out += ',';
				first = false;
				out += gojson::quote(k);
				out += ':';
				v.write(out);
			}
			out += '}';
			break;
		}
		}
	}
	std::string str() const
	{
		std::string out;
		write(out);
		return out;
	}
};

} // namespace nkos::bmc
