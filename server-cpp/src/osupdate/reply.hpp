#pragma once

// updateReply of the Go server's router/os_update.go: {"code":0,"msg":"success","data":...} or, for an error,
// {"code":-1,"msg":error,"data":null}, with msg written as encoding/json writes it (apk output and lock errors
// can hold <, > and & or cut UTF-8).

#include "common/gojson.hpp"
#include "http/router.hpp"

#include <string>

namespace nkos::osupdate {

inline void reply(http::Response &res, const std::string &data_json, const std::string &error)
{
	if (!error.empty()) {
		res.json(200, "{\"code\":-1,\"msg\":" + gojson::quote(error) + ",\"data\":null}");
		return;
	}
	res.json(200, "{\"code\":0,\"msg\":\"success\",\"data\":" + (data_json.empty() ? std::string("null") : data_json) + "}");
}

} // namespace nkos::osupdate
