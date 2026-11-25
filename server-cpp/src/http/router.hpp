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
	// The body is longer than the route may take (Router::set_body_limit): it was not read, body is empty.
	bool body_over_limit = false;
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
	// A copy for another thread: the named headers kept (read from header_copy once wsi is gone). Loop thread.
	Request detached(std::initializer_list<std::string_view> names) const;
	std::vector<std::pair<std::string, std::string>> header_copy;
};

// A response body written while the request stays open (server-sent events, progress): headers go out without a
// length, write() queues data, end() finishes. Loop thread only. closed() is set and on_close runs when the client
// goes away first.
struct Stream {
	void write(std::string data);
	void end();
	// Drops the connection now (a stalled client). On a stream whose response has not started it is remembered:
	// the response is then not sent and the connection is dropped instead.
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
	bool ended_ = false, closed_ = false, headers_sent_ = false, aborted_ = false;
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

// A handler with the part of it that needs no body (authentication, role). The server asks check from the request's
// headers and does not read the body of a request the handler is going to refuse.
struct CheckedHandler {
	Handler handler;
	std::function<bool(Request &)> check;
	void operator()(Request &request, Response &response) const { handler(request, response); }
	operator Handler() const { return handler; }
};

// Finishes a pending request (Response::pending) with response. Safe from any thread; a request whose
// connection has closed meanwhile is dropped.
void complete(std::uint64_t id, Response response);

// Whether a pending request's connection is still open (a long handler stops when the client went away).
bool request_open(std::uint64_t id);

// Holds or resumes the reception of a request's body while its consumer cannot take more (a file being opened, a
// slow disk): HTTP/1 stops reading the socket, an HTTP/2 stream gets no more credit while its connection's other
// streams go on. What the peer was already allowed to send still arrives (at most 256 KiB on HTTP/2). The time
// held does not count against the body's timeouts. A request that is gone or whose body is over is left alone
// (false for one that is gone); the server resumes by itself before it answers. Loop thread.
bool pause_request_body(std::uint64_t id, bool pause);

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
	void add(std::string_view method, std::string_view path, CheckedHandler handler);
	// From the headers, before the body is read: false when the matching route's check refuses the request. It is
	// then dispatched at once, without its body, and the handler gives its refusal.
	bool admits(Request &request) const;
	// How much of a request's body the server keeps for its route; a longer one is not read and the request is
	// dispatched with body_over_limit set. Without a limit function every body may be default_body_limit long.
	static constexpr std::size_t default_body_limit = 1u << 20;
	void set_body_limit(std::function<std::size_t(const Request &)> limit);
	std::size_t body_limit(const Request &request) const;
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
		std::function<bool(Request &)> check;
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
	std::function<std::size_t(const Request &)> body_limit_;
};

} // namespace nkos::http
