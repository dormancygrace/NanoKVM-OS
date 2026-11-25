// An image on an HTTP(S) server read by byte ranges, as the Go server's remotemedia/http.go: at most two MiB of it
// in memory (the read cache), the URL and credentials kept out of every answer. Its own libcurl handle rather than
// common/http_client: the connection stays open between the 1 MiB reads, no proxy is used, addresses of the device
// itself are refused after name resolution (for the first URL and every redirect), redirects are checked one by one
// (at most four, never from HTTPS to HTTP, credentials only to the first origin) and the final URL is pinned, and
// the first TLS offer is ChaCha20-Poly1305 alone on TLS 1.2 (AES-GCM costs this CPU half as much again), falling
// back to the library's defaults for a server that cannot do it.

#include "storage/internal.hpp"

#include "common/base64.hpp"
#include "common/log.hpp"

#include <curl/curl.h>

#include <arpa/inet.h>
#include <cstring>
#include <ctime>
#include <ifaddrs.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

namespace nkos::storage {

namespace {

constexpr auto http_read_timeout = std::chrono::seconds(8);
constexpr auto http_read_budget = std::chrono::seconds(16);
const char *err_local_address = "Image URLs cannot point to this device or a link-local address";

std::once_flag curl_init;

std::string http_origin(const go::Url &u) { return go::to_lower(u.scheme + "://" + u.host); }

std::string trim_value(std::string_view s)
{
	while (!s.empty() && (s.front() == ' ' || s.front() == '\t')) s.remove_prefix(1);
	while (!s.empty() && (s.back() == ' ' || s.back() == '\t' || s.back() == '\r' || s.back() == '\n')) s.remove_suffix(1);
	return std::string(s);
}

bool parse_uint(std::string_view s, std::uint64_t &v)
{
	if (s.empty() || s.size() > 20) return false;
	v = 0;
	for (char c : s) {
		if (c < '0' || c > '9') return false;
		std::uint64_t d = static_cast<std::uint64_t>(c - '0');
		if (v > (UINT64_MAX - d) / 10) return false;
		v = v * 10 + d;
	}
	return true;
}


// http.ParseTime: RFC 1123 with GMT, RFC 850, ANSI C.
bool http_time(const std::string &s)
{
	for (const char *format : {"%a, %d %b %Y %H:%M:%S GMT", "%A, %d-%b-%y %H:%M:%S GMT", "%a %b %e %H:%M:%S %Y"}) {
		std::tm tm{};
		const char *end = ::strptime(s.c_str(), format, &tm);
		if (end && *end == '\0') return true;
	}
	return false;
}

struct Failure {
	bool retry = false, expired = false, suites = false;
};

// The device's own addresses, loopback, unspecified, link-local and multicast addresses are refused after DNS.
bool refused_address(const sockaddr *sa)
{
	if (sa->sa_family == AF_INET) {
		auto *in = reinterpret_cast<const sockaddr_in *>(sa);
		std::uint32_t a = ntohl(in->sin_addr.s_addr);
		if ((a >> 24) == 127 || a == 0 || (a >> 16) == 0xa9fe || (a >> 28) == 0xe) return true;
	} else if (sa->sa_family == AF_INET6) {
		auto *in6 = reinterpret_cast<const sockaddr_in6 *>(sa);
		const auto &a = in6->sin6_addr;
		if (IN6_IS_ADDR_LOOPBACK(&a) || IN6_IS_ADDR_UNSPECIFIED(&a) || IN6_IS_ADDR_LINKLOCAL(&a) || IN6_IS_ADDR_MULTICAST(&a))
			return true;
		if (IN6_IS_ADDR_V4MAPPED(&a)) {
			sockaddr_in v4{};
			v4.sin_family = AF_INET;
			std::memcpy(&v4.sin_addr, a.s6_addr + 12, 4);
			if (refused_address(reinterpret_cast<const sockaddr *>(&v4))) return true;
		}
	} else {
		return true;
	}
	ifaddrs *list = nullptr;
	if (::getifaddrs(&list) != 0) return true;
	bool own = false;
	for (ifaddrs *i = list; i && !own; i = i->ifa_next) {
		if (!i->ifa_addr || i->ifa_addr->sa_family != sa->sa_family) continue;
		if (sa->sa_family == AF_INET)
			own = std::memcmp(&reinterpret_cast<const sockaddr_in *>(i->ifa_addr)->sin_addr,
			                  &reinterpret_cast<const sockaddr_in *>(sa)->sin_addr, 4) == 0;
		else
			own = std::memcmp(&reinterpret_cast<const sockaddr_in6 *>(i->ifa_addr)->sin6_addr,
			                  &reinterpret_cast<const sockaddr_in6 *>(sa)->sin6_addr, 16) == 0;
	}
	::freeifaddrs(list);
	return own;
}

class HttpSource : public Source {
public:
	std::shared_ptr<Context> ctx;
	CURL *curl = nullptr;
	bool chacha = true, fresh = false;
	go::Url url, original;
	std::string auth_origin, username, password, etag, modified;
	std::unique_ptr<Window> window;
	std::once_flag closed;

	~HttpSource() override
	{
		close();
		if (curl) curl_easy_cleanup(curl);
	}

	Error read_block(std::uint64_t offset, std::uint32_t length, std::span<const char> &data) override
	{
		return window->read(offset, length, data);
	}

	void close() override
	{
		std::call_once(closed, [this] {
			ctx->cancel();
			window->close();
		});
	}

	void set_transport(bool use_chacha)
	{
		chacha = use_chacha;
		fresh = true;
	}

	Error fetch(const std::shared_ptr<Context> &parent, std::uint64_t offset, std::span<char> target, bool probe);
	Error fetch_once(const std::shared_ptr<Context> &attempt, const go::Url &target_url, std::uint64_t offset,
	                 std::span<char> target, bool probe, Failure *&failure, Failure &storage, go::Url &final_url);
};

struct Exchange {
	HttpSource *source;
	const std::shared_ptr<Context> *ctx;
	std::span<char> target;
	bool probe;
	// The response being received.
	int status = 0;
	std::vector<std::pair<std::string, std::string>> headers;
	bool redirect = false, accepted = false, oversized = false, refused = false, handshake_failed = false;
	std::size_t received = 0;
	Error failure;
	Failure kind;
	bool has_kind = false;
	std::uint64_t total = 0;

	std::string header(std::string_view name) const
	{
		for (const auto &[k, v] : headers)
			if (go::equal_fold(k, name)) return v;
		return {};
	}
	bool has_header(std::string_view name) const
	{
		for (const auto &[k, v] : headers)
			if (go::equal_fold(k, name)) return true;
		return false;
	}
};

Error status_failure(int code, Failure &f)
{
	f = {};
	switch (code) {
	case 200: return Error::of("Image server does not support byte ranges (HTTP 206 required)");
	case 401: case 403:
		f.expired = true;
		return Error::of("Image server denied access; check credentials or the signed URL");
	case 404: case 410:
		f.expired = code == 410;
		return Error::of("Remote image was not found or is no longer available");
	case 412: return Error::of("Remote image changed; disconnect and reconnect it");
	case 416: return Error::of("Remote image size or byte range changed; disconnect and reconnect it");
	default:
		f.retry = code == 408 || (code >= 500 && code <= 599);
		return Error::of("Image server returned HTTP " + std::to_string(code));
	}
}

struct Call {
	Exchange *x;
	std::uint64_t offset;
};

size_t on_header(char *data, size_t size, size_t count, void *user)
{
	auto *c = static_cast<Call *>(user);
	Exchange &x = *c->x;
	std::string_view line(data, size * count);
	if (line.starts_with("HTTP/")) {
		x.headers.clear();
		x.redirect = x.accepted = false;
		auto fields = go::fields(line);
		x.status = fields.size() > 1 ? std::atoi(fields[1].c_str()) : 0;
		return size * count;
	}
	if (line == "\r\n" || line == "\n") {
		if (x.status >= 100 && x.status < 200) return size * count;
		bool moved = x.status == 301 || x.status == 302 || x.status == 303 || x.status == 307 || x.status == 308;
		if (moved && x.has_header("Location")) {
			x.redirect = true;
			return size * count;
		}
		HttpSource &s = *x.source;
		if (x.status != 206) {
			x.failure = status_failure(x.status, x.kind);
			x.has_kind = true;
			return 0;
		}
		std::uint64_t start = 0, end = 0, total = 0;
		bool ok = parse_content_range(x.header("Content-Range"), start, end, total);
		std::int64_t length = -1;
		if (x.has_header("Content-Length")) {
			std::uint64_t v = 0;
			if (parse_uint(x.header("Content-Length"), v)) length = static_cast<std::int64_t>(v);
		}
		if (!ok || start != c->offset || end - start + 1 != x.target.size() ||
		    (length >= 0 && length != static_cast<std::int64_t>(x.target.size()))) {
			x.failure = Error::of("Image server returned an incorrect byte range");
			return 0;
		}
		if (std::string enc = x.header("Content-Encoding"); !enc.empty() && !go::equal_fold(enc, "identity")) {
			x.failure = Error::of("Compressed HTTP image responses are not supported");
			return 0;
		}
		if (x.probe) {
			if (auto e = validate_image(total)) {
				x.failure = e;
				return 0;
			}
		} else if (total != s.size || (!s.etag.empty() && x.header("ETag") != s.etag) ||
		           (s.etag.empty() && !s.modified.empty() && x.header("Last-Modified") != s.modified)) {
			x.failure = Error::of("Remote image changed; disconnect and reconnect it");
			return 0;
		}
		x.total = total;
		x.accepted = true;
		return size * count;
	}
	auto colon = line.find(':');
	if (colon != std::string_view::npos) x.headers.emplace_back(trim_value(line.substr(0, colon)), trim_value(line.substr(colon + 1)));
	return size * count;
}

size_t on_body(char *data, size_t size, size_t count, void *user)
{
	auto *c = static_cast<Call *>(user);
	Exchange &x = *c->x;
	std::size_t n = size * count;
	if (x.redirect || !x.accepted) return n;
	std::size_t room = x.target.size() - x.received;
	if (n > room) {
		std::memcpy(x.target.data() + x.received, data, room);
		x.received += room;
		x.oversized = true;
		return 0;
	}
	std::memcpy(x.target.data() + x.received, data, n);
	x.received += n;
	return n;
}

int on_progress(void *user, curl_off_t, curl_off_t, curl_off_t, curl_off_t)
{
	auto *c = static_cast<Call *>(user);
	return (*c->x->ctx)->done() ? 1 : 0;
}

curl_socket_t on_open_socket(void *user, curlsocktype, curl_sockaddr *address)
{
	auto *c = static_cast<Call *>(user);
	if (refused_address(&address->addr)) {
		c->x->refused = true;
		return CURL_SOCKET_BAD;
	}
	return ::socket(address->family, address->socktype | SOCK_CLOEXEC, address->protocol);
}

} // namespace

Error HttpSource::fetch_once(const std::shared_ptr<Context> &attempt, const go::Url &target_url, std::uint64_t offset,
                             std::span<char> target, bool probe, Failure *&failure, Failure &kind, go::Url &final_url)
{
	failure = nullptr;
	go::Url current = target_url;
	auto deadline = attempt->deadline();
	for (int via = 0;; via++) {
		Exchange x{this, &attempt, target, probe};
		Call call{&x, offset};
		curl_easy_reset(curl);
		std::string full = current.scheme + "://" + current.host + current.request_uri();
		curl_slist *headers = nullptr;
		headers = curl_slist_append(headers, ("Range: bytes=" + std::to_string(offset) + "-" +
		                                      std::to_string(offset + target.size() - 1)).c_str());
		headers = curl_slist_append(headers, "Accept-Encoding: identity");
		if (!username.empty() && http_origin(current) == auth_origin)
			headers = curl_slist_append(headers, ("Authorization: Basic " + base64::encode(username + ":" + password)).c_str());
		if (!etag.empty()) headers = curl_slist_append(headers, ("If-Match: " + etag).c_str());
		else if (!modified.empty()) headers = curl_slist_append(headers, ("If-Unmodified-Since: " + modified).c_str());
		headers = curl_slist_append(headers, "Expect:");
		long left = static_cast<long>(std::chrono::duration_cast<std::chrono::milliseconds>(deadline - SteadyClock::now()).count());
		if (left < 1) left = 1;
		curl_easy_setopt(curl, CURLOPT_URL, full.c_str());
		curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
		curl_easy_setopt(curl, CURLOPT_PROXY, "");
		curl_easy_setopt(curl, CURLOPT_PROTOCOLS_STR, "http,https");
		curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 0L);
		curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
		curl_easy_setopt(curl, CURLOPT_USERAGENT, "Go-http-client/1.1");
		curl_easy_setopt(curl, CURLOPT_HTTP_CONTENT_DECODING, 0L);
		curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS, left);
		curl_easy_setopt(curl, CURLOPT_CONNECTTIMEOUT_MS, left);
		curl_easy_setopt(curl, CURLOPT_HEADERFUNCTION, on_header);
		curl_easy_setopt(curl, CURLOPT_HEADERDATA, &call);
		curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, on_body);
		curl_easy_setopt(curl, CURLOPT_WRITEDATA, &call);
		curl_easy_setopt(curl, CURLOPT_XFERINFOFUNCTION, on_progress);
		curl_easy_setopt(curl, CURLOPT_XFERINFODATA, &call);
		curl_easy_setopt(curl, CURLOPT_NOPROGRESS, 0L);
		curl_easy_setopt(curl, CURLOPT_OPENSOCKETFUNCTION, on_open_socket);
		curl_easy_setopt(curl, CURLOPT_OPENSOCKETDATA, &call);
		curl_easy_setopt(curl, CURLOPT_TCP_KEEPALIVE, 1L);
		curl_easy_setopt(curl, CURLOPT_TCP_KEEPIDLE, 30L);
		curl_easy_setopt(curl, CURLOPT_TCP_KEEPINTVL, 30L);
		if (fresh) {
			curl_easy_setopt(curl, CURLOPT_FRESH_CONNECT, 1L);
			fresh = false;
		}
		if (chacha) {
			curl_easy_setopt(curl, CURLOPT_SSLVERSION, CURL_SSLVERSION_TLSv1_2 | CURL_SSLVERSION_MAX_TLSv1_2);
			curl_easy_setopt(curl, CURLOPT_SSL_CIPHER_LIST, "ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305");
		} else {
			curl_easy_setopt(curl, CURLOPT_SSLVERSION, CURL_SSLVERSION_TLSv1_2);
		}
		CURLcode rc = curl_easy_perform(curl);
		curl_slist_free_all(headers);
		if (ctx->canceled()) return Error::of("context canceled", "context");
		if (x.failure) {
			if (x.has_kind) {
				kind = x.kind;
				failure = &kind;
			}
			return x.failure;
		}
		if (x.refused) return Error::of(err_local_address);
		if (x.oversized) {
			kind = Failure{};
			failure = &kind;
			return Error::of("Image server returned an oversized or incomplete block");
		}
		if (rc == CURLE_OK && x.redirect) {
			// CheckRedirect: at most four redirects, a valid image URL, never from HTTPS to HTTP.
			char *location = nullptr;
			curl_easy_getinfo(curl, CURLINFO_REDIRECT_URL, &location);
			auto next = location ? go::parse_url(location) : std::nullopt;
			if (via + 1 >= 5 || !next || !valid_http_url(*next) || (current.scheme == "https" && next->scheme != "https")) {
				kind = Failure{};
				failure = &kind;
				return Error::of("Remote HTTP request failed; check the connection and TLS certificate");
			}
			current = *next;
			continue;
		}
		if (x.accepted) {
			if (rc == CURLE_OK && x.received == target.size()) {
				final_url = current;
				if (probe) {
					size = x.total;
					if (std::string tag = x.header("ETag"); strong_http_etag(tag)) etag = tag;
					if (std::string m = x.header("Last-Modified"); !m.empty() && http_time(m)) modified = m;
				}
				return {};
			}
			kind = Failure{.retry = true};
			failure = &kind;
			return Error::of("Image server returned a short or interrupted block");
		}
		// No response accepted: the request failed. A refused TLS handshake (35) may be the cipher offer; a
		// certificate failure is not retried.
		kind = Failure{};
		switch (rc) {
		case CURLE_COULDNT_RESOLVE_HOST: case CURLE_COULDNT_CONNECT: case CURLE_OPERATION_TIMEDOUT:
		case CURLE_GOT_NOTHING: case CURLE_SEND_ERROR: case CURLE_RECV_ERROR: case CURLE_PARTIAL_FILE:
		case CURLE_HTTP2: case CURLE_HTTP2_STREAM:
			kind.retry = true;
			break;
		case CURLE_SSL_CONNECT_ERROR:
			kind.suites = true;
			break;
		default:
			break;
		}
		failure = &kind;
		return Error::of("Remote HTTP request failed; check the connection and TLS certificate");
	}
}

Error HttpSource::fetch(const std::shared_ptr<Context> &parent, std::uint64_t offset, std::span<char> target, bool probe)
{
	auto budget = Context::make(parent, SteadyClock::now() + http_read_budget);
	go::Url target_url = url;
	for (int attempt = 0;; attempt++) {
		auto once = Context::make(budget, SteadyClock::now() + http_read_timeout);
		Failure storage, *failure = nullptr;
		go::Url final_url;
		Error err = fetch_once(once, target_url, offset, target, probe, failure, storage, final_url);
		if (!err) {
			// The pinned target changes only after a whole, checked block.
			url = final_url;
			return {};
		}
		if (ctx->done()) return Error::of("context canceled", "context");
		if (budget->done()) return Error::of("Remote HTTP read timed out");
		if (failure && failure->suites && chacha) {
			// Servers without a ChaCha20 suite answer with an alert, a reset or a close. The default suites are
			// offered for the rest of the mount; this does not use up an attempt.
			set_transport(false);
			attempt = -1;
			continue;
		}
		if (attempt == 1 || !failure) return err;
		if (failure->expired && !probe && target_url.string() != original.string()) {
			// A renewed signed URL must still name the pinned image.
			if (etag.empty() && modified.empty())
				return Error::of("Signed image URL expired without an image validator; disconnect and reconnect it");
			target_url = original;
		} else if (!failure->retry) {
			return err;
		}
		fresh = true;
	}
}

std::unique_ptr<Source> open_http(const std::shared_ptr<Context> &parent, const HttpOptions &o, Error &error)
{
	if ((error = validate_http_options(o))) return nullptr;
	std::call_once(curl_init, [] { curl_global_init(CURL_GLOBAL_DEFAULT); });
	auto s = std::make_unique<HttpSource>();
	s->ctx = Context::make(parent);
	s->url = s->original = *go::parse_url(o.url);
	s->auth_origin = http_origin(s->url);
	s->username = o.username;
	s->password = o.password;
	s->curl = curl_easy_init();
	HttpSource *raw = s.get();
	s->window = std::make_unique<Window>(s->ctx, [raw](const std::shared_ptr<Context> &ctx, SteadyClock::time_point,
	                                                    std::uint64_t offset, std::span<char> target) {
		return raw->fetch(ctx, offset, target, false);
	}, nullptr, nullptr);
	// Probe a real range: HEAD and Accept-Ranges alone do not show that the server can read at random, and HEAD
	// may be disabled.
	s->set_transport(true);
	if ((error = s->fetch(s->ctx, 0, std::span<char>(s->window->buffers[0], 2048), true))) {
		s->close();
		return nullptr;
	}
	s->window->size = s->size;
	s->window->start = 0;
	s->window->valid = 2048;
	std::string name = go::base(s->url.path);
	bool control = false;
	for (char32_t r : go::runes(name)) control = control || go::is_control(r);
	if (!go::valid_utf8(name) || name.size() > 200 || control || !go::equal_fold(go::ext(name), ".iso")) name = "remote.iso";
	s->name = name;
	return s;
}

} // namespace nkos::storage
