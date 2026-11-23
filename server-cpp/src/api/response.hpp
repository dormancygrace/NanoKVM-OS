#pragma once

// The API's JSON answers, as the Go server's proto.Response: {"code": 0, "msg": "success", "data": ...} with HTTP 200,
// code 0 for success and a negative code with a message for an error; "data" is null when there is none.

#include "common/gojson.hpp"
#include "http/router.hpp"

#include <glaze/glaze.hpp>

#include <string>
#include <string_view>

namespace nkos::api {

// A message as gin writes it (encoding/json: <, > and & escaped).
inline std::string quote(std::string_view s) { return gojson::quote(s); }

inline void send(http::Response &res, int code, std::string_view msg, std::string_view data_json)
{
	std::string body = "{\"code\":" + std::to_string(code) + ",\"msg\":" + quote(msg) + ",\"data\":";
	body += data_json.empty() ? std::string_view("null") : data_json;
	body += "}";
	res.json(200, std::move(body));
}

inline void ok(http::Response &res) { send(res, 0, "success", {}); }

template <typename T>
void ok(http::Response &res, const T &data)
{
	std::string json;
	(void)glz::write<glz::opts{.skip_null_members = true}>(data, json);
	send(res, 0, "success", json);
}

inline void err(http::Response &res, int code, std::string_view msg) { send(res, code, msg, {}); }

// Gin's c.JSON(status, "text"): a JSON string body.
inline void plain_json(http::Response &res, int status, std::string_view text) { res.json(status, quote(text)); }

} // namespace nkos::api
