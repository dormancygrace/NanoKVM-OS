#pragma once

// Requests, responses and routing. A handler fills a Response: a status and headers plus either a body, a file to
// send (Range, HEAD and conditional requests handled by the server), or nothing more. Middlewares run in order
// before the route and may finish the response.

#include <cstdint>
#include <functional>
#include <deque>
#include <map>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

struct lws;

namespace nkos::http {

struct WsHandler;

// The signed-in account of a request, set by auth::authenticate.
struct Auth {
	std::string username;
	std::string role;
	bool must_change_password = false;
	bool has_token = false;         // false when authentication is disabled
	std::uint64_t token_version = 0;
	std::string session_key;
	std::int64_t issued = 0, expires = 0;
};

struct Request {
	lws *wsi = nullptr;
	std::uint64_t id = 0;  // the request's handle for complete()
	std::string method;  // "GET", "POST", ...
	std::string path;    // decoded path, without the query
	std::string query;   // raw query, without '?'
	std::string body;
	bool tls = false;
	std::string peer;    // remote address, without port
	std::map<std::string, std::string> params;  // ":name" path segments of the matched route
	std::string route;   // the matched route's pattern ("" when none matched)

	std::optional<Auth> auth;

	std::string param(std::string_view name) const;
	// The value of a query parameter (first occurrence, %-decoded); empty when absent.
	std::string query_value(std::string_view name) const;

	// A request header; names are case-insensitive. Empty when absent.
	std::string header(std::string_view name) const;
	std::vector<std::string> header_values(std::string_view name) const;
};

// A response body written while the request stays open (server-sent events, progress): headers go out without a
// length, write() queues data, end() finishes. Loop thread only. closed() is set and on_close runs when the client
// goes away first.
struct Stream {
	void write(std::string data);
	void end();
	// Drops the connection now (a stalled client).
	void abort();
	bool closed() const { return closed_; }
	// Bytes written so far (progress of a slow client).
	std::uint64_t written() const { return written_; }
	std::function<void()> on_close;

	// Internals for the server.
	lws *wsi_ = nullptr;
	std::deque<std::string> queue_;
	std::size_t front_sent_ = 0;  // bytes of queue_.front() already written
	std::uint64_t written_ = 0;
	bool ended_ = false, closed_ = false, headers_sent_ = false;
};

struct Response {
	int status = 200;
	std::vector<std::pair<std::string, std::string>> headers;
	std::string content_type;
	std::string body;
	std::string file;          // a file to send instead of the body
	std::string file_encoding; // Content-Encoding of that file (precompressed asset)
	bool done = false;         // the response is complete; later middlewares and routes are skipped
	bool pending = false;      // the handler answers later with complete(); the request stays open
	std::shared_ptr<Stream> stream;  // a streamed body (see Stream) instead of body

	void set_header(std::string_view name, std::string_view value);
	void add_header(std::string_view name, std::string_view value);
	void text(int status, std::string_view body, std::string_view type = "text/plain; charset=utf-8");
	void json(int status, std::string body);
	void redirect(int status, std::string_view location);
};

using Handler = std::function<void(Request &, Response &)>;

// Finishes a pending request (Response::pending) with response. Safe from any thread; a request whose
// connection has closed meanwhile is dropped.
void complete(std::uint64_t id, Response response);

// Whether a pending request's connection is still open (a long handler stops when the client went away).
bool request_open(std::uint64_t id);

// Answers the request from fn, run on the blocking pool (loop::blocking): for handlers that run commands or read
// slow files. fn gets a fresh Response with the headers already set on res.
void defer(Request &request, Response &res, std::function<void(Response &)> fn);

// URL decoding of a query or form component ("+" is a space).
std::string url_decode(std::string_view s);

// A request body taken as it arrives (http/upload.hpp): data() for each piece on the loop thread, then end() once it
// is all in; the answer is given with complete(request.id). aborted() when the client went away before end().
struct BodySink {
	virtual ~BodySink() = default;
	virtual void data(std::string_view chunk) = 0;
	virtual void end(Request &request) = 0;
	virtual void aborted() {}
};
using UploadRoute = std::function<std::shared_ptr<BodySink>(Request &, Response &)>;

class Router {
public:
	void use(Handler middleware);
	void add(std::string_view method, std::string_view path, Handler handler);
	// Runs once a response is final (answered by the handler or later with complete()), on the loop thread.
	void after(std::function<void(const Request &, const Response &)> hook);
	void finished(const Request &request, const Response &response) const;
	// A WebSocket route: decides the upgrade (http/websocket.hpp).
	void ws(std::string_view path, std::function<std::shared_ptr<WsHandler>(Request &, Response &)> route);
	// A route whose body is streamed: decided from the headers, before the body (the middlewares see no body).
	void upload(std::string_view method, std::string_view path, UploadRoute route);
	bool is_upload(const std::string &method, const std::string &path) const;
	// The middlewares, then the upload route: its sink, or nullptr with the answer in response.
	std::shared_ptr<BodySink> dispatch_upload(Request &request, Response &response) const;
	// Runs the middlewares, then the matching route, or a 404 like Gin's.
	void dispatch(Request &request, Response &response) const;
	// The same for an upgrade request: the handler of the connection, or nullptr with the refusal in response.
	std::shared_ptr<WsHandler> dispatch_ws(Request &request, Response &response) const;

private:
	struct Route {
		std::string method, path;
		Handler handler;
	};
	static bool match(const std::string &pattern, const std::string &path, std::map<std::string, std::string> &params);
	std::vector<Handler> middlewares_;
	std::vector<std::function<void(const Request &, const Response &)>> after_;
	std::vector<Route> routes_;
	std::vector<std::pair<std::string, std::function<std::shared_ptr<WsHandler>(Request &, Response &)>>> ws_routes_;
	struct Upload {
		std::string method, path;
		UploadRoute route;
	};
	std::vector<Upload> uploads_;
};

} // namespace nkos::http
