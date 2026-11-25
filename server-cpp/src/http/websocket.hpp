#pragma once

// WebSocket connections on the server's event loop. A route decides before the upgrade (authentication, origin,
// parameters) and returns the handler of the connection, or answers the request to refuse it. Messages leave in
// order: first those queued with send(), then whatever the handler offers in next(), one per writable callback,
// so a slow client holds back only its own data. A ping goes out every 15 s; a connection that sends nothing (not
// even a pong) for 30 s is closed, as the Go server's read deadline did. Loop thread only.

#include "http/router.hpp"

#include <chrono>
#include <cstddef>
#include <deque>
#include <functional>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::http {

// A message to send: storage holds at least websocket_headroom bytes before offset, where the WebSocket header is
// written in place, so one buffer can go to several connections.
inline constexpr std::size_t websocket_headroom = 32;
struct Message {
	std::shared_ptr<std::vector<unsigned char>> storage;
	std::size_t offset = 0, size = 0;
	bool binary = true;
};
Message make_message(std::string_view data, bool binary = true);

class WebSocket;

struct WsHandler {
	virtual ~WsHandler() = default;
	virtual void opened(WebSocket &) {}
	virtual void received(WebSocket &, std::string_view /*data*/, bool /*binary*/) {}
	// The next message when the send() queue is empty; nullopt when there is none now (wake() asks again).
	virtual std::optional<Message> next(WebSocket &) { return std::nullopt; }
	// Whether next() has a message now. A handler that knows says so: each writable callback asked for in vain
	// costs a turn of the event loop.
	virtual bool ready(WebSocket &) { return true; }
	// The connection is gone; the WebSocket must not be used after this returns.
	virtual void closed(WebSocket &) {}
};

class WebSocket {
public:
	void send(Message message);
	// The handler has something for next().
	void wake();
	// Sends a close frame with code and reason, then closes.
	void close(int code, std::string reason);
	// Drops the connection without a close handshake (a stalled client).
	void abort();
	const Request &request() const { return request_; }
	std::uint64_t id() const { return id_; }
	// Messages larger than this close the connection (1009).
	std::size_t max_message = 64 * 1024;
	// A ping goes out every ping_period; nothing received for read_timeout closes the connection. A pong counts as
	// received unless pong_counts says otherwise (a frozen background tab still answers pings).
	std::chrono::seconds ping_period{15};
	std::chrono::seconds read_timeout{30};
	std::function<bool()> pong_counts;
	// Whether the connection is still open (send/close are no-ops after closing).
	bool closing() const { return closing_; }

	// Internals for the server.
	WebSocket(lws *wsi, Request request, std::shared_ptr<WsHandler> handler);
	lws *wsi_;
	Request request_;
	std::shared_ptr<WsHandler> handler_;
	std::uint64_t id_;
	std::deque<Message> queue_;
	std::string partial_;
	bool closing_ = false, ping_due_ = false;
	int close_code_ = 1000;
	std::string close_reason_;
	std::chrono::steady_clock::time_point last_rx_, last_ping_;
};

// Decides an upgrade: the handler of the new connection, or nullptr with the response set to refuse it.
using WsRoute = std::function<std::shared_ptr<WsHandler>(Request &, Response &)>;

} // namespace nkos::http
