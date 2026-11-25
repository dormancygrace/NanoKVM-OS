#pragma once

// Request parameters as Gin's ShouldBind reads them: a JSON object for application/json, form fields for a form
// body, the query for GET. A field of the wrong JSON type fails the binding, as Go's json.Unmarshal does.

#include "http/router.hpp"

#include <map>
#include <optional>
#include <string>

namespace nkos::api {

class Params {
public:
	// nullopt: the body does not parse (or a JSON body is not an object).
	static std::optional<Params> bind(const http::Request &request);

	// The field as a string; ok=false when present with another type.
	std::optional<std::string> string(const std::string &name, bool &ok) const;
	std::optional<bool> boolean(const std::string &name, bool &ok) const;
	std::optional<long long> integer(const std::string &name, bool &ok) const;

private:
	enum class Kind { string, boolean, number, other };
	struct Field {
		Kind kind;
		std::string text;
		bool flag = false;
		double number = 0;
	};
	std::map<std::string, Field> fields_;
	bool form_ = false;  // form fields are strings: booleans and numbers parse from text
};

} // namespace nkos::api
