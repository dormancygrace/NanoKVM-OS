#include "http/server.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "http/websocket.hpp"

#include <libwebsockets.h>
#include <openssl/err.h>
#include <openssl/pem.h>
#include <openssl/ssl.h>

#include <algorithm>
#include <cctype>
#include <chrono>
#include <arpa/inet.h>
#include <net/if.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <csignal>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <map>
#include <memory>
#include <string>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::http {

namespace {

enum class Role { serve, redirect };

// All listeners run on one lws service thread. Count physical clients for their entire
// lifetime, including after a protocol upgrade. HTTP/2 child streams share the same slot.
constexpr std::size_t max_connections = 256, max_connections_per_source = 64;
std::map<lws *, std::string> counted_clients;
std::map<std::string, std::size_t> source_counts;

std::string connection_source(const sockaddr_storage &addr)
{
	const unsigned char *v4;
	if (addr.ss_family == AF_INET) {
		v4 = reinterpret_cast<const unsigned char *>(&reinterpret_cast<const sockaddr_in &>(addr).sin_addr);
	} else if (addr.ss_family == AF_INET6) {
		const auto &v6 = reinterpret_cast<const sockaddr_in6 &>(addr).sin6_addr;
		if (IN6_IS_ADDR_LOOPBACK(&v6)) return {};
		if (!IN6_IS_ADDR_V4MAPPED(&v6)) return {reinterpret_cast<const char *>(v6.s6_addr), 8};
		v4 = v6.s6_addr + 12;
	} else {
		return {};
	}
	if (v4[0] == 127) return {};
	return {reinterpret_cast<const char *>(v4), 4};
}

bool connection_full(const std::string &source)
{
	if (source.empty()) return false;
	auto it = source_counts.find(source);
	return counted_clients.size() >= max_connections ||
	       (it != source_counts.end() && it->second >= max_connections_per_source);
}

// lws refuses a body longer than its vhost's limit before the request reaches the router, and its default is
// 100 MiB: an image upload must pass. The limits that apply are the routes' own, enforced in this file for bodies
// kept in memory and by the upload sinks for streamed ones.
constexpr std::uint64_t max_request_body = std::uint64_t{1} << 40;

struct VhostData {
	Role role;
	const Router *router;
	int https_port;
};

// One request in flight on a connection (HTTP/1.1) or stream (HTTP/2).
struct Session {
	Request request;
	Response response;
	size_t sent = 0;
	bool head = false;
	bool body_expected = false;
	bool body_paused = false;  // pause_request_body holds the body
	std::size_t body_limit = Router::default_body_limit;
	std::shared_ptr<BodySink> sink;  // a streamed body (upload route)
	bool sink_ended = false;
	int fd = -1;             // a file being sent
	off_t remaining = 0;     // its bytes still to send
	~Session()
	{
		if (fd >= 0) ::close(fd);
	}
};

struct Pss {
	Session *session;
	WebSocket *ws;                         // an established WebSocket
	std::shared_ptr<WsHandler> *upgrade;   // the handler of an accepted upgrade, until it is established
	Request *upgrade_request;
};

static_assert(LWS_PRE <= websocket_headroom, "the message headroom must hold the WebSocket header");

using SteadyClock = std::chrono::steady_clock;

std::map<std::uint64_t, WebSocket *> sockets;
std::uint64_t next_socket_id = 1;
bool ticking = false;

// Pings and read timeouts of the WebSockets, once a second while there are any.
void tick()
{
	if (sockets.empty()) {
		ticking = false;
		return;
	}
	auto now = SteadyClock::now();
	for (auto &[id, ws] : sockets) {
		if (now - ws->last_rx_ > ws->read_timeout) {
			lws_set_timeout(ws->wsi_, PENDING_TIMEOUT_KILLED_BY_PARENT, LWS_TO_KILL_ASYNC);
			continue;
		}
		if (now - ws->last_ping_ >= ws->ping_period) {
			ws->last_ping_ = now;
			ws->ping_due_ = true;
			lws_callback_on_writable(ws->wsi_);
		}
	}
	loop::after(std::chrono::seconds(1), tick);
}

void drop_upgrade(Pss *pss)
{
	delete pss->upgrade;
	pss->upgrade = nullptr;
	delete pss->upgrade_request;
	pss->upgrade_request = nullptr;
}

int write_ws(lws *wsi, WebSocket &ws)
{
	// Messages queued before close() still go out first (the probe's instance message); the handler is not asked.
	if (ws.closing_ && ws.queue_.empty()) {
		lws_close_reason(wsi, static_cast<lws_close_status>(ws.close_code_),
		                 reinterpret_cast<unsigned char *>(ws.close_reason_.data()), ws.close_reason_.size());
		return -1;
	}
	if (ws.ping_due_ && !ws.closing_) {
		ws.ping_due_ = false;
		// One byte of payload: lws does not report an empty pong (RECEIVE_PONG), so the read timeout would close
		// a socket whose browser answers every ping.
		unsigned char buf[LWS_PRE + 1];
		buf[LWS_PRE] = 'p';
		if (lws_write(wsi, buf + LWS_PRE, 1, LWS_WRITE_PING) < 0) return -1;
		lws_callback_on_writable(wsi);
		return 0;
	}
	std::optional<Message> m;
	if (!ws.queue_.empty()) {
		m = std::move(ws.queue_.front());
		ws.queue_.pop_front();
	} else {
		m = ws.handler_->next(ws);
		if (ws.closing_) return write_ws(wsi, ws);
	}
	if (!m) return 0;
	if (lws_write(wsi, m->storage->data() + m->offset, m->size, m->binary ? LWS_WRITE_BINARY : LWS_WRITE_TEXT) < 0)
		return -1;
	if (!ws.queue_.empty() || ws.closing_ || ws.ping_due_ || ws.handler_->ready(ws)) lws_callback_on_writable(wsi);
	return 0;
}

// Descriptors watched with watch_fd, by their watch (the wsi's opaque user data): a descriptor number is reused
// once lws closed it, a watch never.
struct Watched {
	lws *wsi = nullptr;
	FdCallbacks callbacks;
};
std::map<FdWatch, Watched> watched;
FdWatch next_watch = 1;
lws_vhost *serving_vhost;
// The pair the HTTPS vhost was created with (its identity for lws_tls_cert_updated).
std::string vhost_crt, vhost_key;
std::function<bool(const Request &, Response &)> plain_http_handler;
// The HTTPS vhost's TLS context (LWS_CALLBACK_OPENSSL_LOAD_EXTRA_SERVER_VERIFY_CERTS), for the chain of a pair served
// at run time.
SSL_CTX *vhost_ssl_ctx;

Watched *watched_of(lws *wsi)
{
	auto it = watched.find(reinterpret_cast<FdWatch>(lws_get_opaque_user_data(wsi)));
	return it == watched.end() ? nullptr : &it->second;
}

// The open requests by id, for complete(): a pending request is answered while its connection exists.
std::map<std::uint64_t, std::pair<lws *, Session *>> open_requests;
std::uint64_t next_id = 1;

void forget(Pss *pss)
{
	if (!pss || !pss->session) return;
	if (auto stream = pss->session->response.stream; stream && !stream->closed_) {
		stream->closed_ = true;
		stream->wsi_ = nullptr;
		if (stream->on_close) stream->on_close();
	}
	if (pss->session->sink && !pss->session->sink_ended) pss->session->sink->aborted();
	open_requests.erase(pss->session->request.id);
	delete pss->session;
	pss->session = nullptr;
}

volatile std::sig_atomic_t stopping = 0;
lws_context *context;

void on_signal(int) { stopping = 1; if (context) lws_cancel_service(context); }

const char *method_names[] = {"GET", "POST", "OPTIONS", "PUT", "PATCH", "DELETE", "CONNECT", "HEAD"};

std::string peer_address(lws *wsi)
{
	char ip[64] = {};
	lws_get_peer_simple(wsi, ip, sizeof ip);
	// An IPv4 client on the dual-stack socket: its plain address, as Go reports it.
	std::string peer = ip;
	if (peer.starts_with("::ffff:") && peer.find('.') != std::string::npos) peer.erase(0, 7);
	return peer;
}

void query_escape(std::string &out, std::string_view text)
{
	static const char hex[] = "0123456789ABCDEF";
	for (unsigned char c : text) {
		if (std::isalnum(c) || c == '-' || c == '.' || c == '_' || c == '~') {
			out += static_cast<char>(c);
		} else {
			out += '%';
			out += hex[c >> 4];
			out += hex[c & 15];
		}
	}
}

// lws keeps the query as one fragment per argument, split at '&' and already decoded ('+' and %XX). Request::query
// is a raw query that its readers split and decode once, so each argument's name and value are encoded again here:
// joining the decoded fragments would turn a decoded '&', '+' or '%' of a value into syntax. A fragment of any length
// is taken, and an empty one ("a=1&&b=2") does not end the list.
std::string query_of(lws *wsi)
{
	std::string out, frag;
	bool first = true;
	for (int i = 0; i < 4096; i++) {
		int length = lws_hdr_fragment_length(wsi, WSI_TOKEN_HTTP_URI_ARGS, i);
		frag.assign(static_cast<std::size_t>(std::max(length, 0)) + 1, '\0');
		int n = lws_hdr_copy_fragment(wsi, frag.data(), static_cast<int>(frag.size()), WSI_TOKEN_HTTP_URI_ARGS, i);
		if (n < 0) break;
		if (n == 0) continue;
		std::string_view argument(frag.data(), static_cast<std::size_t>(n));
		if (!first) out += '&';
		first = false;
		auto eq = argument.find('=');
		query_escape(out, argument.substr(0, eq));
		if (eq != std::string_view::npos) {
			out += '=';
			query_escape(out, argument.substr(eq + 1));
		}
	}
	return out;
}

bool append_header(lws *wsi, unsigned char **p, unsigned char *end, const std::string &name, const std::string &value)
{
	std::string n = name + ":";
	return lws_add_http_header_by_name(wsi, reinterpret_cast<const unsigned char *>(n.c_str()),
	                                   reinterpret_cast<const unsigned char *>(value.data()),
	                                   static_cast<int>(value.size()), p, end) == 0;
}

int respond(lws *wsi, Session &s);

// A single "bytes=" range as net/http's ServeContent reads it; false when the header asks for no satisfiable range.
// Several ranges are answered with the whole file (browsers and download resumes ask for one).
bool parse_range(const std::string &header, off_t size, off_t &start, off_t &length, bool &partial)
{
	partial = false;
	start = 0;
	length = size;
	if (header.empty()) return true;
	if (!header.starts_with("bytes=") || header.find(',') != std::string::npos) return true;
	std::string spec = header.substr(6);
	auto dash = spec.find('-');
	if (dash == std::string::npos) return true;
	std::string a = spec.substr(0, dash), b = spec.substr(dash + 1);
	auto digits = [](const std::string &v) { return !v.empty() && v.find_first_not_of("0123456789") == std::string::npos; };
	if (a.empty()) {
		if (!digits(b)) return true;
		off_t n = std::atoll(b.c_str());
		if (n == 0 || size == 0) return false;
		if (n > size) n = size;
		start = size - n;
		length = n;
	} else {
		if (!digits(a) || (!b.empty() && !digits(b))) return true;
		start = std::atoll(a.c_str());
		if (start >= size) return false;
		off_t last = b.empty() ? size - 1 : std::min<off_t>(std::atoll(b.c_str()), size - 1);
		if (last < start) return true;
		length = last - start + 1;
	}
	partial = true;
	return true;
}

// Ends a response that has headers only: an empty final write carries END_STREAM on HTTP/2, which the headers
// alone do not (the browser saw the stream cancelled).
int finish_without_body(lws *wsi)
{
	unsigned char buf[LWS_PRE + 1];
	if (lws_write(wsi, buf + LWS_PRE, 0, LWS_WRITE_HTTP_FINAL) < 0) return -1;
	return lws_http_transaction_completed(wsi) ? -1 : 0;
}

int finish_headers(lws *wsi, Session &s, unsigned status, const char *type, unsigned long long length,
                   const std::vector<std::pair<std::string, std::string>> &extra)
{
	unsigned char buf[LWS_PRE + 4096], *start = buf + LWS_PRE, *p = start, *end = buf + sizeof buf;
	if (lws_add_http_common_headers(wsi, status, type, length, &p, end)) return -1;
	for (const auto &[name, value] : s.response.headers)
		if (!append_header(wsi, &p, end, name, value)) return -1;
	for (const auto &[name, value] : extra)
		if (!append_header(wsi, &p, end, name, value)) return -1;
	return lws_finalize_write_http_header(wsi, start, &p, end) ? -1 : 0;
}

int respond_file(lws *wsi, Session &s)
{
	Response &r = s.response;
	s.fd = ::open(r.file.c_str(), O_RDONLY | O_CLOEXEC);
	struct stat st{};
	if (s.fd < 0 || ::fstat(s.fd, &st) != 0) {
		r.file.clear();
		r.text(500, "500 Internal Server Error");
		return respond(wsi, s);
	}
	off_t start = 0, length = st.st_size;
	bool partial = false;
	std::vector<std::pair<std::string, std::string>> extra{{"Accept-Ranges", "bytes"}};
	if (!r.file_encoding.empty()) extra.emplace_back("Content-Encoding", r.file_encoding);
	if (!parse_range(s.request.header("range"), st.st_size, start, length, partial)) {
		extra.emplace_back("Content-Range", "bytes */" + std::to_string(st.st_size));
		r.file.clear();
		r.body = "invalid range: failed to overlap\n";
		if (finish_headers(wsi, s, 416, "text/plain; charset=utf-8", r.body.size(), extra)) return -1;
		if (s.head) return finish_without_body(wsi);
		lws_callback_on_writable(wsi);
		return 0;
	}
	if (partial)
		extra.emplace_back("Content-Range", "bytes " + std::to_string(start) + "-" + std::to_string(start + length - 1) +
		                                        "/" + std::to_string(st.st_size));
	std::string type = r.content_type.empty() ? "application/octet-stream" : r.content_type;
	if (finish_headers(wsi, s, partial ? 206 : 200, type.c_str(), static_cast<unsigned long long>(length), extra)) return -1;
	if (s.head || length == 0) return finish_without_body(wsi);
	if (::lseek(s.fd, start, SEEK_SET) != start) return -1;
	s.remaining = length;
	lws_callback_on_writable(wsi);
	return 0;
}

int write_file(lws *wsi, Session &s)
{
	constexpr size_t chunk = 16384;
	unsigned char buf[LWS_PRE + chunk];
	size_t want = static_cast<size_t>(std::min<off_t>(chunk, s.remaining));
	ssize_t n = ::read(s.fd, buf + LWS_PRE, want);
	if (n <= 0) return -1;
	s.remaining -= n;
	bool last = s.remaining == 0;
	if (lws_write(wsi, buf + LWS_PRE, static_cast<size_t>(n), last ? LWS_WRITE_HTTP_FINAL : LWS_WRITE_HTTP) != n) return -1;
	if (last) {
		::close(s.fd);
		s.fd = -1;
		return lws_http_transaction_completed(wsi) ? -1 : 0;
	}
	lws_callback_on_writable(wsi);
	return 0;
}

// Sends the response; returns -1 to close, 0 to continue.
int respond(lws *wsi, Session &s)
{
	Response &r = s.response;
	if (!r.file.empty()) return respond_file(wsi, s);
	if (r.stream) {
		// Aborted before it was attached (its session ended while the response was prepared): nothing of it is
		// sent. The stream is closed for its producer and the connection goes.
		if (r.stream->aborted_) {
			auto stream = r.stream;
			stream->closed_ = true;
			stream->queue_.clear();
			if (stream->on_close) stream->on_close();
			return -1;
		}
		unsigned char buf[LWS_PRE + 4096], *start = buf + LWS_PRE, *p = start, *end = buf + sizeof buf;
		const char *type = r.content_type.empty() ? nullptr : r.content_type.c_str();
		if (lws_add_http_common_headers(wsi, static_cast<unsigned>(r.status), type, LWS_ILLEGAL_HTTP_CONTENT_LEN, &p, end))
			return -1;
		for (const auto &[name, value] : r.headers)
			if (!append_header(wsi, &p, end, name, value)) return -1;
		if (lws_finalize_write_http_header(wsi, start, &p, end)) return -1;
		r.stream->wsi_ = wsi;
		r.stream->headers_sent_ = true;
		if (!r.stream->queue_.empty() || r.stream->ended_) lws_callback_on_writable(wsi);
		return 0;
	}
	unsigned char buf[LWS_PRE + 4096], *start = buf + LWS_PRE, *p = start, *end = buf + sizeof buf;
	const char *type = r.content_type.empty() ? nullptr : r.content_type.c_str();
	if (lws_add_http_common_headers(wsi, static_cast<unsigned>(r.status), type, r.body.size(), &p, end)) return -1;
	for (const auto &[name, value] : r.headers)
		if (!append_header(wsi, &p, end, name, value)) return -1;
	if (lws_finalize_write_http_header(wsi, start, &p, end)) return -1;
	if (s.head || r.body.empty()) return finish_without_body(wsi);
	lws_callback_on_writable(wsi);
	return 0;
}

int write_body(lws *wsi, Session &s)
{
	constexpr size_t chunk = 16384;
	std::string &body = s.response.body;
	size_t n = std::min(chunk, body.size() - s.sent);
	bool last = s.sent + n == body.size();
	unsigned char buf[LWS_PRE + chunk];
	std::memcpy(buf + LWS_PRE, body.data() + s.sent, n);
	if (lws_write(wsi, buf + LWS_PRE, n, last ? LWS_WRITE_HTTP_FINAL : LWS_WRITE_HTTP) != static_cast<int>(n)) return -1;
	s.sent += n;
	if (last) return lws_http_transaction_completed(wsi) ? -1 : 0;
	lws_callback_on_writable(wsi);
	return 0;
}

// Go's redirectHost: the request host without its port, the HTTPS port added unless 443, IPv6 in brackets.
std::string redirect_host(const std::string &request_host, int https_port)
{
	std::string host = request_host;
	if (!host.empty() && host.front() == '[') {
		auto close = host.find(']');
		if (close != std::string::npos) host = host.substr(1, close - 1);
	} else if (std::count(host.begin(), host.end(), ':') == 1) {
		host = host.substr(0, host.find(':'));
	}
	bool v6 = host.find(':') != std::string::npos;
	if (https_port != 443) return (v6 ? "[" + host + "]" : host) + ":" + std::to_string(https_port);
	return v6 ? "[" + host + "]" : host;
}

void handle(lws *wsi, Session &s, const VhostData &vd)
{
	if (vd.role == Role::redirect) {
		if (plain_http_handler && plain_http_handler(s.request, s.response)) return;
		std::string uri = s.request.path;
		if (!s.request.query.empty()) uri += "?" + s.request.query;
		s.response.redirect(307, "https://" + redirect_host(s.request.header("host"), vd.https_port) + uri);
		return;
	}
	vd.router->dispatch(s.request, s.response);
	if (!s.response.pending) vd.router->finished(s.request, s.response);
	(void)wsi;
}

int callback(lws *wsi, enum lws_callback_reasons reason, void *user, void *in, size_t len)
{
	auto *pss = static_cast<Pss *>(user);
	switch (reason) {
	case LWS_CALLBACK_FILTER_NETWORK_CONNECTION: {
		// lws asks before adoption and TLS, then synchronously adopts before its next accept.
		auto *args = static_cast<lws_filter_network_conn_args *>(user);
		return connection_full(connection_source(args->cli_addr)) ? 1 : 0;
	}
	case LWS_CALLBACK_SERVER_NEW_CLIENT_INSTANTIATED: {
		if (lws_get_network_wsi(wsi) != wsi || counted_clients.contains(wsi)) return 0;
		sockaddr_storage peer{};
		socklen_t size = sizeof peer;
		if (::getpeername(lws_get_socket_fd(wsi), reinterpret_cast<sockaddr *>(&peer), &size) != 0) return -1;
		auto source = connection_source(peer);
		if (!source.empty()) {
			if (connection_full(source)) return -1;
			counted_clients.emplace(wsi, source);
			++source_counts[source];
		}
		return 0;
	}
	case LWS_CALLBACK_WSI_DESTROY: {
		auto client = counted_clients.find(wsi);
		if (client != counted_clients.end()) {
			auto count = source_counts.find(client->second);
			if (--count->second == 0) source_counts.erase(count);
			counted_clients.erase(client);
		}
		return 0;
	}
	case LWS_CALLBACK_OPENSSL_LOAD_EXTRA_SERVER_VERIFY_CERTS:
		vhost_ssl_ctx = static_cast<SSL_CTX *>(user);
		return 0;
	case LWS_CALLBACK_HTTP: {
		auto *vd = static_cast<VhostData *>(lws_get_vhost_user(lws_get_vhost(wsi)));
		if (!vd || !pss) return -1;
		forget(pss);
		pss->session = new Session;
		Session &s = *pss->session;
		s.request.id = next_id++;
		open_requests[s.request.id] = {wsi, pss->session};
		const char *uri = nullptr;
		int uri_len = 0;
		int m = lws_http_get_uri_and_method(wsi, const_cast<char **>(&uri), &uri_len);
		if (m < 0 || m >= static_cast<int>(std::size(method_names))) return -1;
		s.request.wsi = wsi;
		s.request.method = method_names[m];
		s.request.path.assign(uri, static_cast<size_t>(uri_len));
		s.request.query = query_of(wsi);
		s.request.tls = lws_is_ssl(wsi);
		s.request.peer = peer_address(wsi);
		s.head = s.request.method == "HEAD";
		(void)in;
		(void)len;
		// HTTP/2 bodies can omit Content-Length. The body-method dispatch reports completion even when
		// the request ends in HEADERS or an empty DATA frame.
		char length[24] = {};
		bool declared = lws_hdr_copy(wsi, length, sizeof length, WSI_TOKEN_HTTP_CONTENT_LENGTH) > 0;
		long long declared_length = declared ? std::atoll(length) : 0;
		bool http2 = lws_get_network_wsi(wsi) != wsi;
		bool takes_body = s.request.method == "POST" || s.request.method == "PUT" ||
		                  s.request.method == "PATCH" || s.request.method == "DELETE";
		if (declared ? declared_length > 0
		             : http2 ? takes_body : lws_hdr_total_length(wsi, WSI_TOKEN_HTTP_TRANSFER_ENCODING) > 0) {
			s.body_expected = true;
			if (vd->role == Role::serve && vd->router->is_upload(s.request.method, s.request.path)) {
				s.sink = vd->router->dispatch_upload(s.request, s.response);
				if (!s.sink) {
					// Refused from its headers: answered now, the body is not read.
					s.body_expected = false;
					vd->router->finished(s.request, s.response);
					return respond(wsi, s);
				}
				s.response.pending = true;
				return 0;
			}
			// A body the route will not use is not read: the route refuses the request from its headers, or the
			// declared length is beyond what the route takes. The request is handled now, without its body.
			if (vd->role == Role::serve) {
				s.body_limit = vd->router->body_limit(s.request);
				bool admitted = vd->router->admits(s.request);
				if (admitted && declared && static_cast<unsigned long long>(declared_length) > s.body_limit)
					s.request.body_over_limit = true;
				if (admitted && !s.request.body_over_limit) return 0;
				s.body_expected = false;
			} else {
				return 0;
			}
		}
		handle(wsi, s, *vd);
		if (s.response.pending) return 0;
		return respond(wsi, s);
	}
	case LWS_CALLBACK_EVENT_WAIT_CANCELLED:
		loop::drain();
		return 0;
	case LWS_CALLBACK_FILTER_PROTOCOL_CONNECTION: {
		auto *vd = static_cast<VhostData *>(lws_get_vhost_user(lws_get_vhost(wsi)));
		if (!vd || !pss || vd->role != Role::serve) return 1;
		drop_upgrade(pss);
		auto request = std::make_unique<Request>();
		const char *uri = nullptr;
		int uri_len = 0;
		if (lws_http_get_uri_and_method(wsi, const_cast<char **>(&uri), &uri_len) < 0 || !uri) return 1;
		request->wsi = wsi;
		request->method = "GET";
		request->path.assign(uri, static_cast<size_t>(uri_len));
		request->query = query_of(wsi);
		request->tls = lws_is_ssl(wsi);
		request->peer = peer_address(wsi);
		Response refusal;
		auto handler = vd->router->dispatch_ws(*request, refusal);
		if (!handler) {
			log::debug("websocket {} refused: {} {}", request->path, refusal.status, refusal.body);
			return 1;
		}
		pss->upgrade = new std::shared_ptr<WsHandler>(std::move(handler));
		pss->upgrade_request = request.release();
		return 0;
	}
	case LWS_CALLBACK_ESTABLISHED: {
		if (!pss || !pss->upgrade) return -1;
		forget(pss);
		auto *ws = new WebSocket(wsi, std::move(*pss->upgrade_request), std::move(*pss->upgrade));
		drop_upgrade(pss);
		ws->request_.wsi = nullptr;  // the upgrade request's headers are gone; only its values remain
		pss->ws = ws;
		sockets[ws->id_] = ws;
		if (!ticking) {
			ticking = true;
			loop::after(std::chrono::seconds(1), tick);
		}
		ws->handler_->opened(*ws);
		return 0;
	}
	case LWS_CALLBACK_RECEIVE: {
		if (!pss || !pss->ws) return -1;
		WebSocket &ws = *pss->ws;
		ws.last_rx_ = SteadyClock::now();
		if (lws_is_first_fragment(wsi)) ws.partial_.clear();
		if (ws.partial_.size() + len > ws.max_message) {
			lws_close_reason(wsi, LWS_CLOSE_STATUS_MESSAGE_TOO_LARGE, nullptr, 0);
			return -1;
		}
		ws.partial_.append(static_cast<const char *>(in), len);
		if (lws_is_final_fragment(wsi) && !ws.closing_) {
			std::string data = std::move(ws.partial_);
			ws.partial_.clear();
			ws.handler_->received(ws, data, lws_frame_is_binary(wsi));
		}
		return 0;
	}
	case LWS_CALLBACK_RECEIVE_PONG:
		if (pss && pss->ws && (!pss->ws->pong_counts || pss->ws->pong_counts())) pss->ws->last_rx_ = SteadyClock::now();
		return 0;
	case LWS_CALLBACK_SERVER_WRITEABLE:
		if (!pss || !pss->ws) return 0;
		return write_ws(wsi, *pss->ws);
	case LWS_CALLBACK_CLOSED:
		if (pss && pss->ws) {
			WebSocket *ws = pss->ws;
			pss->ws = nullptr;
			sockets.erase(ws->id_);
			ws->handler_->closed(*ws);
			delete ws;
		}
		if (pss) drop_upgrade(pss);
		forget(pss);
		return 0;
	case LWS_CALLBACK_HTTP_BODY:
		if (pss && pss->session && pss->session->sink) {
			pss->session->sink->data(std::string_view(static_cast<const char *>(in), len));
			return 0;
		}
		if (pss && pss->session && pss->session->body_expected && len > 0) {
			Session &s = *pss->session;
			if (s.request.body.size() + len <= s.body_limit) {
				s.request.body.append(static_cast<const char *>(in), len);
				return 0;
			}
			// Longer than the route takes, without a declared length to tell so from the headers: the rest is not
			// kept and the request is handled as one over its limit.
			auto *vd = static_cast<VhostData *>(lws_get_vhost_user(lws_get_vhost(wsi)));
			if (!vd) return -1;
			s.body_expected = false;
			s.request.body.clear();
			s.request.body.shrink_to_fit();
			s.request.body_over_limit = true;
			handle(wsi, s, *vd);
			if (s.response.pending) return 0;
			return respond(wsi, s);
		}
		return 0;
	case LWS_CALLBACK_HTTP_BODY_COMPLETION: {
		auto *vd = static_cast<VhostData *>(lws_get_vhost_user(lws_get_vhost(wsi)));
		if (!vd || !pss || !pss->session) return -1;
		// Also sent for a request without a body, which was answered already.
		if (!pss->session->body_expected) return 0;
		pss->session->body_expected = false;
		if (pss->session->sink) {
			pss->session->sink_ended = true;
			pss->session->sink->end(pss->session->request);
			return 0;
		}
		handle(wsi, *pss->session, *vd);
		if (pss->session->response.pending) return 0;
		return respond(wsi, *pss->session);
	}
	case LWS_CALLBACK_HTTP_WRITEABLE:
		if (!pss || !pss->session) return 0;
		// Nothing to write before the response exists: the body is still read, or the answer comes later. HTTP/2
		// calls this whenever the peer opens its window (curl does at once), and an empty body written now would
		// end the stream before its headers.
		if (pss->session->body_expected || pss->session->response.pending) return 0;
		if (pss->session->fd >= 0) return write_file(wsi, *pss->session);
		if (auto stream = pss->session->response.stream) {
			if (!stream->queue_.empty()) {
				// In 16 KB writes (an HTTP/2 frame's default maximum) within the peer's window, as many as the
				// connection takes in this callback.
				constexpr std::size_t chunk = 16384;
				unsigned char buf[LWS_PRE + chunk];
				do {
					const std::string &data = stream->queue_.front();
					std::size_t n = std::min(chunk, data.size() - stream->front_sent_);
					lws_fileofs_t allowance = lws_get_peer_write_allowance(wsi);
					if (allowance == 0) break;
					if (allowance > 0) n = std::min(n, static_cast<std::size_t>(allowance));
					std::memcpy(buf + LWS_PRE, data.data() + stream->front_sent_, n);
					if (lws_write(wsi, buf + LWS_PRE, n, LWS_WRITE_HTTP) != static_cast<int>(n)) return -1;
					stream->front_sent_ += n;
					stream->written_ += n;
					if (stream->front_sent_ == data.size()) {
						stream->queue_.pop_front();
						stream->front_sent_ = 0;
					}
				} while (!stream->queue_.empty() && !lws_send_pipe_choked(wsi));
				if (!stream->queue_.empty() || stream->ended_) lws_callback_on_writable(wsi);
				return 0;
			}
			if (stream->ended_) return finish_without_body(wsi);
			return 0;
		}
		return write_body(wsi, *pss->session);
	case LWS_CALLBACK_HTTP_FILE_COMPLETION:
		return lws_http_transaction_completed(wsi) ? -1 : 0;
	case LWS_CALLBACK_HTTP_DROP_PROTOCOL:
	case LWS_CALLBACK_CLOSED_HTTP:
		forget(pss);
		if (pss) drop_upgrade(pss);
		return 0;
	default:
		break;
	}
	return lws_callback_http_dummy(wsi, reason, user, in, len);
}

int fd_callback(lws *wsi, enum lws_callback_reasons reason, void *user, void *in, size_t len)
{
	switch (reason) {
	case LWS_CALLBACK_RAW_RX_FILE: {
		// A copy: the callback may end its own watch.
		if (auto *w = watched_of(wsi)) {
			auto on_readable = w->callbacks.readable;
			if (on_readable) on_readable();
		}
		return 0;
	}
	case LWS_CALLBACK_RAW_WRITEABLE_FILE:
		if (auto *w = watched_of(wsi)) {
			auto on_writable = w->callbacks.writable;
			if (on_writable) on_writable();
		}
		return 0;
	case LWS_CALLBACK_RAW_CLOSE_FILE: {
		// The far end hung up (unwatch_fd erased its watch first and hears nothing).
		auto id = reinterpret_cast<FdWatch>(lws_get_opaque_user_data(wsi));
		auto it = watched.find(id);
		if (it == watched.end()) return 0;
		auto on_closed = std::move(it->second.callbacks.closed);
		watched.erase(it);
		if (on_closed) on_closed();
		return 0;
	}
	default:
		return lws_callback_http_dummy(wsi, reason, user, in, len);
	}
}

lws_protocols protocols[] = {
	// A message goes to send() whole: with tx_packet_size 0 lws sends rx_buffer_size (4 KiB) at a time and buffers
	// the rest, a video frame then costing dozens of TLS records, copies and loop turns (2.3x the CPU at 19 Mbit/s).
	{"http", callback, sizeof(Pss), 0, 0, nullptr, 8u << 20},
	{"nkos-fd", fd_callback, 0, 0, 0, nullptr, 0},
	LWS_PROTOCOL_LIST_TERM,
};

} // namespace

void Stream::write(std::string data)
{
	if (closed_ || ended_) return;
	queue_.push_back(std::move(data));
	if (wsi_ && headers_sent_) lws_callback_on_writable(wsi_);
}

void Stream::abort()
{
	if (closed_) return;
	if (!wsi_) {
		aborted_ = true;
		return;
	}
	lws_set_timeout(wsi_, PENDING_TIMEOUT_KILLED_BY_PARENT, LWS_TO_KILL_ASYNC);
}

void WebSocket::abort()
{
	closing_ = true;
	lws_set_timeout(wsi_, PENDING_TIMEOUT_KILLED_BY_PARENT, LWS_TO_KILL_ASYNC);
}

void Stream::end()
{
	if (closed_ || ended_) return;
	ended_ = true;
	if (wsi_ && headers_sent_) lws_callback_on_writable(wsi_);
}

Message make_message(std::string_view data, bool binary)
{
	Message m;
	m.storage = std::make_shared<std::vector<unsigned char>>(websocket_headroom + data.size());
	std::memcpy(m.storage->data() + websocket_headroom, data.data(), data.size());
	m.offset = websocket_headroom;
	m.size = data.size();
	m.binary = binary;
	return m;
}

WebSocket::WebSocket(lws *wsi, Request request, std::shared_ptr<WsHandler> handler)
	: wsi_(wsi), request_(std::move(request)), handler_(std::move(handler)), id_(next_socket_id++),
	  last_rx_(SteadyClock::now()), last_ping_(SteadyClock::now())
{
}

void WebSocket::send(Message message)
{
	if (closing_) return;
	queue_.push_back(std::move(message));
	lws_callback_on_writable(wsi_);
}

void WebSocket::wake()
{
	if (!closing_) lws_callback_on_writable(wsi_);
}

void WebSocket::close(int code, std::string reason)
{
	if (closing_) return;
	closing_ = true;
	close_code_ = code;
	close_reason_ = std::move(reason);
	if (close_reason_.size() > 123) close_reason_.resize(123);
	lws_callback_on_writable(wsi_);
}

FdWatch watch_fd(int fd, std::function<void()> on_readable)
{
	return watch_fd(fd, FdCallbacks{std::move(on_readable), {}, {}});
}

FdWatch watch_fd(int fd, FdCallbacks callbacks)
{
	if (!serving_vhost) return 0;
	lws_sock_file_fd_type desc{};
	desc.filefd = fd;
	lws *wsi = lws_adopt_descriptor_vhost(serving_vhost, LWS_ADOPT_RAW_FILE_DESC, desc, "nkos-fd", nullptr);
	if (!wsi) return 0;
	FdWatch id = next_watch++;
	lws_set_opaque_user_data(wsi, reinterpret_cast<void *>(id));
	watched[id] = Watched{wsi, std::move(callbacks)};
	return id;
}

void pause_fd(FdWatch watch, bool paused)
{
	auto it = watched.find(watch);
	if (it != watched.end()) lws_rx_flow_control(it->second.wsi, paused ? 0 : 1);
}

void want_writable_fd(FdWatch watch)
{
	auto it = watched.find(watch);
	if (it != watched.end()) lws_callback_on_writable(it->second.wsi);
}

void unwatch_fd(FdWatch watch, std::function<void()> after_close)
{
	auto it = watched.find(watch);
	if (it == watched.end()) return;
	lws *wsi = it->second.wsi;
	if (after_close) {
		// The close callback comes before lws frees the wsi and closes the descriptor: posted, after_close runs
		// once this turn of the loop (and with it the close) is over.
		it->second.callbacks = FdCallbacks{{}, {}, [after_close = std::move(after_close)] { loop::post(after_close); }};
	} else {
		watched.erase(it);
	}
	lws_set_timeout(wsi, PENDING_TIMEOUT_KILLED_BY_PARENT, LWS_TO_KILL_ASYNC);
}

std::optional<std::string> ingress_interface(const Request &request)
{
	if (!request.wsi) return std::nullopt;
	lws *net = lws_get_network_wsi(request.wsi);
	int fd = lws_get_socket_fd(net ? net : request.wsi);
	if (fd < 0) return std::nullopt;
	int family = 0;
	socklen_t len = sizeof family;
	if (::getsockopt(fd, SOL_SOCKET, SO_DOMAIN, &family, &len) != 0) return std::nullopt;
	int level = IPPROTO_IP, enable = IP_PKTINFO, options = IP_PKTOPTIONS, report = IP_PKTINFO;
	if (family == AF_INET6) {
		level = IPPROTO_IPV6;
		enable = IPV6_RECVPKTINFO;
		options = 6;  // IPV6_2292PKTOPTIONS
		report = IPV6_PKTINFO;
	}
	int on = 1;
	if (::setsockopt(fd, level, enable, &on, sizeof on) != 0) return std::nullopt;
	alignas(cmsghdr) unsigned char buf[512];
	socklen_t size = sizeof buf;
	if (::getsockopt(fd, level, options, buf, &size) != 0) return std::nullopt;
	msghdr msg{};
	msg.msg_control = buf;
	msg.msg_controllen = size;
	for (cmsghdr *c = CMSG_FIRSTHDR(&msg); c; c = CMSG_NXTHDR(&msg, c)) {
		if (c->cmsg_level != level || c->cmsg_type != report) continue;
		unsigned index = 0;
		if (family == AF_INET6 && c->cmsg_len >= CMSG_LEN(sizeof(in6_pktinfo)))
			index = reinterpret_cast<in6_pktinfo *>(CMSG_DATA(c))->ipi6_ifindex;
		else if (family == AF_INET && c->cmsg_len >= CMSG_LEN(sizeof(in_pktinfo)))
			index = static_cast<unsigned>(reinterpret_cast<in_pktinfo *>(CMSG_DATA(c))->ipi_ifindex);
		char name[IF_NAMESIZE];
		if (index > 0 && ::if_indextoname(index, name)) return std::string(name);
	}
	return std::nullopt;
}

bool needs_loopback_listener(std::string host)
{
	auto a = host.find_first_not_of(" \t[]"), b = host.find_last_not_of(" \t[]");
	host = a == std::string::npos ? std::string() : host.substr(a, b - a + 1);
	if (host.empty()) return false;
	in6_addr v6{};
	in_addr v4{};
	if (inet_pton(AF_INET, host.c_str(), &v4) == 1) {
		std::uint32_t ip = ntohl(v4.s_addr);
		return (ip >> 24) != 127 && ip != 0;
	}
	if (inet_pton(AF_INET6, host.c_str(), &v6) == 1) {
		if (IN6_IS_ADDR_V4MAPPED(&v6)) {
			std::uint32_t ip = static_cast<std::uint32_t>(v6.s6_addr[12]) << 24 | v6.s6_addr[13] << 16 | v6.s6_addr[14] << 8 | v6.s6_addr[15];
			return (ip >> 24) != 127 && ip != 0;
		}
		return !IN6_IS_ADDR_LOOPBACK(&v6) && !IN6_IS_ADDR_UNSPECIFIED(&v6);
	}
	std::string lower;
	for (char c : host) lower += static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return lower != "localhost";
}

void on_plain_http(std::function<bool(const Request &, Response &)> handler) { plain_http_handler = std::move(handler); }

void use_certificate_files(std::string crt, std::string key)
{
	vhost_crt = std::move(crt);
	vhost_key = std::move(key);
}

bool serve_certificate(const std::string &cert_pem, const std::string &key_pem)
{
	if (!context || vhost_crt.empty()) return false;
	if (lws_tls_cert_updated(context, vhost_crt.c_str(), vhost_key.c_str(), cert_pem.data(), cert_pem.size(),
	                         key_pem.data(), key_pem.size()) != 0)
		return false;
	// lws took the first certificate of the PEM only: the others are the chain, sent after it.
	if (vhost_ssl_ctx) {
		SSL_CTX_clear_chain_certs(vhost_ssl_ctx);
		BIO *bio = BIO_new_mem_buf(cert_pem.data(), static_cast<int>(cert_pem.size()));
		int n = 0;
		while (X509 *x = bio ? PEM_read_bio_X509(bio, nullptr, nullptr, nullptr) : nullptr) {
			if (n++ == 0 || SSL_CTX_add0_chain_cert(vhost_ssl_ctx, x) != 1) X509_free(x);
		}
		ERR_clear_error();
		BIO_free(bio);
	}
	return true;
}

bool current_certificate_pem(std::string &cert, std::string &key)
{
	if (!vhost_ssl_ctx) return false;
	X509 *leaf = SSL_CTX_get0_certificate(vhost_ssl_ctx);
	EVP_PKEY *private_key = SSL_CTX_get0_privatekey(vhost_ssl_ctx);
	if (!leaf || !private_key || SSL_CTX_check_private_key(vhost_ssl_ctx) != 1) return false;
	std::unique_ptr<BIO, decltype(&BIO_free)> certificates(BIO_new(BIO_s_mem()), BIO_free);
	std::unique_ptr<BIO, decltype(&BIO_free)> secret(BIO_new(BIO_s_mem()), BIO_free);
	if (!certificates || !secret || PEM_write_bio_X509(certificates.get(), leaf) != 1) return false;
	STACK_OF(X509) *chain = nullptr;
	SSL_CTX_get0_chain_certs(vhost_ssl_ctx, &chain);
	if (!chain) SSL_CTX_get_extra_chain_certs_only(vhost_ssl_ctx, &chain);
	for (int i = 0; i < sk_X509_num(chain); ++i)
		if (PEM_write_bio_X509(certificates.get(), sk_X509_value(chain, i)) != 1) return false;
	if (PEM_write_bio_PKCS8PrivateKey(secret.get(), private_key, nullptr, nullptr, 0, nullptr, nullptr) != 1) return false;
	char *bytes;
	const auto cert_len = BIO_get_mem_data(certificates.get(), &bytes);
	if (cert_len <= 0) return false;
	std::string cert_out(bytes, static_cast<size_t>(cert_len));
	const auto key_len = BIO_get_mem_data(secret.get(), &bytes);
	if (key_len <= 0) return false;
	key.assign(bytes, static_cast<size_t>(key_len));
	cert = std::move(cert_out);
	return true;
}

bool request_open(std::uint64_t id) { return open_requests.count(id) != 0; }

bool pause_request_body(std::uint64_t id, bool pause)
{
	auto it = open_requests.find(id);
	if (it == open_requests.end()) return false;
	auto [wsi, session] = it->second;
	if (session->body_paused == pause || (pause && !session->body_expected)) return true;
	if (lws_http_server_pause_body(wsi, pause ? 1 : 0)) return false;
	session->body_paused = pause;
	return true;
}

// The router of the serving vhost, for hooks of responses answered later.
const Router *active_router = nullptr;

void complete(std::uint64_t id, Response response)
{
	loop::post([id, response = std::move(response)]() mutable {
		auto it = open_requests.find(id);
		if (it == open_requests.end()) return;
		auto [wsi, session] = it->second;
		// A streaming upload may fail before its request body ends. Let the final response
		// write immediately, even if the peer is still waiting to send the rest.
		if (session->body_expected && session->sink) {
			session->body_expected = false;
			session->sink->aborted();
			session->sink.reset();
		}
		// lws wants the body released before the response starts.
		if (session->body_paused) {
			session->body_paused = false;
			lws_http_server_pause_body(wsi, 0);
		}
		session->response = std::move(response);
		session->response.pending = false;
		if (active_router) active_router->finished(session->request, session->response);
		if (respond(wsi, *session) < 0) lws_set_timeout(wsi, PENDING_TIMEOUT_KILLED_BY_PARENT, LWS_TO_KILL_ASYNC);
	});
}

int run(const config::Config &config, const Router &router, std::function<void()> on_ready, std::function<void()> on_stop)
{
	lws_set_log_level(LLL_ERR | LLL_WARN, nullptr);
	lws_context_creation_info info{};
	info.options = LWS_SERVER_OPTION_DO_SSL_GLOBAL_INIT | LWS_SERVER_OPTION_EXPLICIT_VHOSTS;
	info.port = CONTEXT_PORT_NO_LISTEN;
	context = lws_create_context(&info);
	if (!context) {
		log::error("cannot create the event loop");
		return 1;
	}
	loop::attach(context);
	std::signal(SIGTERM, on_signal);
	std::signal(SIGINT, on_signal);
	std::signal(SIGPIPE, SIG_IGN);

	const bool https = config.proto == "https";
	static VhostData serve{Role::serve, nullptr, 0}, redirect{Role::redirect, nullptr, 0};
	serve.router = &router;
	active_router = &router;
	redirect.https_port = config.port.https;
	config::Cert cert = config.cert.with_defaults();
	if (!vhost_crt.empty()) {
		cert.crt = vhost_crt;
		cert.key = vhost_key;
	}
	vhost_crt = cert.crt;
	vhost_key = cert.key;
	const char *iface = config.host.empty() ? nullptr : config.host.c_str();

	if (https) {
		lws_context_creation_info v{};
		v.vhost_name = "https";
		v.port = config.port.https;
		v.iface = iface;
		v.protocols = protocols;
		v.user = &serve;
		v.options = LWS_SERVER_OPTION_DO_SSL_GLOBAL_INIT;
		v.ssl_cert_filepath = cert.crt.c_str();
		v.ssl_private_key_filepath = cert.key.c_str();
		v.alpn = "h2,http/1.1";
		v.tls1_3_plus_cipher_list = "TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384";
		v.ssl_options_set = SSL_OP_CIPHER_SERVER_PREFERENCE;
		v.timeout_secs = 15;  // ReadHeaderTimeout
		v.max_http_body_size = max_request_body;
		serving_vhost = lws_create_vhost(context, &v);
		if (!serving_vhost) {
			log::error("cannot listen on HTTPS port {}", config.port.https);
			lws_context_destroy(context);
			return 1;
		}
		log::info("HTTPS listening on port {}", config.port.https);
	}
	lws_context_creation_info h{};
	h.vhost_name = "http";
	h.port = config.port.http;
	h.iface = iface;
	h.protocols = protocols;
	h.user = https ? &redirect : &serve;
	h.timeout_secs = 15;
	h.max_http_body_size = max_request_body;
	lws_vhost *http_vhost = lws_create_vhost(context, &h);
	if (!https) serving_vhost = http_vhost;
	if (!http_vhost) {
		log::error("cannot listen on HTTP port {}", config.port.http);
		lws_context_destroy(context);
		return 1;
	}
	log::info("HTTP listening on port {}{}", config.port.http, https ? " (redirects to HTTPS)" : "");
	// main.go (NeedsDedicatedLoopbackListener): a server bound to one address also serves the plain-HTTP port on
	// 127.0.0.1, where PicoClaw calls its loopback routes.
	if (needs_loopback_listener(config.host)) {
		lws_context_creation_info l = h;
		l.vhost_name = "loopback";
		l.iface = "127.0.0.1";
		if (!lws_create_vhost(context, &l)) {
			log::error("cannot listen on HTTP port {} of 127.0.0.1", config.port.http);
			lws_context_destroy(context);
			return 1;
		}
	}

	if (on_ready) on_ready();
	while (!stopping)
		if (lws_service(context, 0) < 0) break;
	if (on_stop) on_stop();
	loop::detach();
	lws_context_destroy(context);
	context = nullptr;
	serving_vhost = nullptr;
	log::info("server stopped");
	return 0;
}

} // namespace nkos::http
