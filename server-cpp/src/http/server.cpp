#include "http/server.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "http/websocket.hpp"

#include <libwebsockets.h>
#include <openssl/ssl.h>

#include <algorithm>
#include <chrono>
#include <arpa/inet.h>
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

// Descriptors watched with watch_fd.
std::map<int, std::function<void()>> watched;
lws_vhost *serving_vhost;

// The open requests by id, for complete(): a pending request is answered while its connection exists.
std::map<std::uint64_t, std::pair<lws *, Session *>> open_requests;
std::uint64_t next_id = 1;

void forget(Pss *pss)
{
	if (!pss || !pss->session) return;
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

std::string query_of(lws *wsi)
{
	std::string out;
	char frag[1024];
	for (int i = 0;; i++) {
		int n = lws_hdr_copy_fragment(wsi, frag, sizeof frag, WSI_TOKEN_HTTP_URI_ARGS, i);
		if (n <= 0) break;
		if (!out.empty()) out += '&';
		out.append(frag, static_cast<size_t>(n));
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
		if (s.head) return lws_http_transaction_completed(wsi) ? -1 : 0;
		lws_callback_on_writable(wsi);
		return 0;
	}
	if (partial)
		extra.emplace_back("Content-Range", "bytes " + std::to_string(start) + "-" + std::to_string(start + length - 1) +
		                                        "/" + std::to_string(st.st_size));
	std::string type = r.content_type.empty() ? "application/octet-stream" : r.content_type;
	if (finish_headers(wsi, s, partial ? 206 : 200, type.c_str(), static_cast<unsigned long long>(length), extra)) return -1;
	if (s.head || length == 0) return lws_http_transaction_completed(wsi) ? -1 : 0;
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
	unsigned char buf[LWS_PRE + 4096], *start = buf + LWS_PRE, *p = start, *end = buf + sizeof buf;
	const char *type = r.content_type.empty() ? nullptr : r.content_type.c_str();
	if (lws_add_http_common_headers(wsi, static_cast<unsigned>(r.status), type, r.body.size(), &p, end)) return -1;
	for (const auto &[name, value] : r.headers)
		if (!append_header(wsi, &p, end, name, value)) return -1;
	if (lws_finalize_write_http_header(wsi, start, &p, end)) return -1;
	if (s.head || r.body.empty()) return lws_http_transaction_completed(wsi) ? -1 : 0;
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
		// Loopback-only internal routes and the ACME challenge come with their families.
		std::string uri = s.request.path;
		if (!s.request.query.empty()) uri += "?" + s.request.query;
		s.response.redirect(307, "https://" + redirect_host(s.request.header("host"), vd.https_port) + uri);
		return;
	}
	vd.router->dispatch(s.request, s.response);
	(void)wsi;
}

int callback(lws *wsi, enum lws_callback_reasons reason, void *user, void *in, size_t len)
{
	auto *pss = static_cast<Pss *>(user);
	switch (reason) {
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
		// A body is read before the request is handled.
		char length[24] = {};
		if (lws_hdr_copy(wsi, length, sizeof length, WSI_TOKEN_HTTP_CONTENT_LENGTH) > 0 ? std::atoll(length) > 0
		                                                                              : lws_hdr_total_length(wsi, WSI_TOKEN_HTTP_TRANSFER_ENCODING) > 0) {
			s.body_expected = true;
			return 0;
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
		if (pss && pss->session && pss->session->body_expected) {
			// Bodies of the routes in this skeleton are small; streaming bodies come with uploads.
			if (pss->session->request.body.size() + len > (8u << 20)) return -1;
			pss->session->request.body.append(static_cast<const char *>(in), len);
		}
		return 0;
	case LWS_CALLBACK_HTTP_BODY_COMPLETION: {
		auto *vd = static_cast<VhostData *>(lws_get_vhost_user(lws_get_vhost(wsi)));
		if (!vd || !pss || !pss->session) return -1;
		// Also sent for a request without a body, which was answered already.
		if (!pss->session->body_expected) return 0;
		pss->session->body_expected = false;
		handle(wsi, *pss->session, *vd);
		if (pss->session->response.pending) return 0;
		return respond(wsi, *pss->session);
	}
	case LWS_CALLBACK_HTTP_WRITEABLE:
		if (!pss || !pss->session) return 0;
		if (pss->session->fd >= 0) return write_file(wsi, *pss->session);
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
		auto it = watched.find(lws_get_socket_fd(wsi));
		if (it != watched.end()) it->second();
		return 0;
	}
	case LWS_CALLBACK_RAW_CLOSE_FILE:
		watched.erase(lws_get_socket_fd(wsi));
		return 0;
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

bool watch_fd(int fd, std::function<void()> on_readable)
{
	if (!serving_vhost) return false;
	watched[fd] = std::move(on_readable);
	lws_sock_file_fd_type desc{};
	desc.filefd = fd;
	if (!lws_adopt_descriptor_vhost(serving_vhost, LWS_ADOPT_RAW_FILE_DESC, desc, "nkos-fd", nullptr)) {
		watched.erase(fd);
		return false;
	}
	return true;
}

void complete(std::uint64_t id, Response response)
{
	loop::post([id, response = std::move(response)]() mutable {
		auto it = open_requests.find(id);
		if (it == open_requests.end()) return;
		auto [wsi, session] = it->second;
		session->response = std::move(response);
		session->response.pending = false;
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
	redirect.https_port = config.port.https;
	const config::Cert cert = config.cert.with_defaults();
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
	lws_vhost *http_vhost = lws_create_vhost(context, &h);
	if (!https) serving_vhost = http_vhost;
	if (!http_vhost) {
		log::error("cannot listen on HTTP port {}", config.port.http);
		lws_context_destroy(context);
		return 1;
	}
	log::info("HTTP listening on port {}{}", config.port.http, https ? " (redirects to HTTPS)" : "");

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
