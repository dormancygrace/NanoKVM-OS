#pragma once

// Requests, responses and routing. A handler fills a Response: a status and headers plus either a body, a file to
// send (Range, HEAD and conditional requests handled by the server), or nothing more. Middlewares run in order
// before the route and may finish the response.

#include <functional>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

struct lws;

namespace nkos::http {

struct Request {
	lws *wsi = nullptr;
	std::string method;  // "GET", "POST", ...
	std::string path;    // decoded path, without the query
	std::string query;   // raw query, without '?'
	std::string body;
	bool tls = false;
	std::string peer;    // remote address, without port

	// A request header; names are case-insensitive. Empty when absent.
	std::string header(std::string_view name) const;
	std::vector<std::string> header_values(std::string_view name) const;
};

struct Response {
	int status = 200;
	std::vector<std::pair<std::string, std::string>> headers;
	std::string content_type;
	std::string body;
	std::string file;          // a file to send instead of the body
	std::string file_encoding; // Content-Encoding of that file (precompressed asset)
	bool done = false;         // the response is complete; later middlewares and routes are skipped

	void set_header(std::string_view name, std::string_view value);
	void add_header(std::string_view name, std::string_view value);
	void text(int status, std::string_view body, std::string_view type = "text/plain; charset=utf-8");
	void json(int status, std::string body);
	void redirect(int status, std::string_view location);
};

using Handler = std::function<void(Request &, Response &)>;

class Router {
public:
	void use(Handler middleware);
	void add(std::string_view method, std::string_view path, Handler handler);
	// Runs the middlewares, then the matching route, or a 404 like Gin's.
	void dispatch(Request &request, Response &response) const;

private:
	struct Route {
		std::string method, path;
		Handler handler;
	};
	std::vector<Handler> middlewares_;
	std::vector<Route> routes_;
};

} // namespace nkos::http
