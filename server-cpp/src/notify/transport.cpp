#include "notify/transport.hpp"

#include "common/base64.hpp"

#include <curl/curl.h>
#include <openssl/err.h>
#include <openssl/ssl.h>
#include <openssl/x509v3.h>

#include <arpa/inet.h>
#include <cerrno>
#include <chrono>
#include <cstring>
#include <fcntl.h>
#include <mutex>
#include <netdb.h>
#include <poll.h>
#include <sys/socket.h>
#include <unistd.h>

namespace nkos::notify::transport {

namespace {

// sendTimeout: one attempt of one channel.
constexpr long send_timeout_ms = 20000;
constexpr char ca_bundle[] = "/etc/ssl/certs/ca-certificates.crt";

std::once_flag curl_once;

struct Body {
	std::string *out;
	std::size_t limit;
	bool cut = false;
};

size_t on_body(char *data, size_t size, size_t count, void *user)
{
	auto *b = static_cast<Body *>(user);
	size_t n = size * count;
	std::size_t room = b->limit - std::min(b->limit, b->out->size());
	b->out->append(data, std::min(n, room));
	if (n > room) {
		// The rest is not read, as Go's LimitReader leaves it.
		b->cut = true;
		return 0;
	}
	return n;
}

std::string curl_error(CURLcode code, const char *buffer)
{
	return buffer && *buffer ? std::string(buffer) : std::string(curl_easy_strerror(code));
}

// JoinHostPort.
std::string host_port(const std::string &host, const std::string &port)
{
	return host.find(':') != std::string::npos ? "[" + host + "]:" + port : host + ":" + port;
}

// ---- SMTP ----

// The server's replies as libcurl reads them: the first refusal (a reply of 400 or more: its code, lines and the
// command it answered: "" for the greeting) and whether the EHLO reply offered STARTTLS.
struct SmtpTrace {
	int code = 0;
	std::vector<std::string> lines;
	std::string phase;  // the command of the refusal
	std::string command;  // the last command sent ("AUTH" through its exchange)
	std::vector<std::string> pending;
	bool starttls = false;
	bool auth_sent = false;
	std::string challenge;  // a 334 continuation of the AUTH exchange (XOAUTH2's error details)
};

int on_debug(CURL *, curl_infotype type, char *data, size_t size, void *user)
{
	auto *t = static_cast<SmtpTrace *>(user);
	if (type == CURLINFO_HEADER_OUT) {
		std::string_view out(data, size);
		std::string verb(out.substr(0, out.find_first_of(" \r\n")));
		for (char &c : verb) c = static_cast<char>(c >= 'a' && c <= 'z' ? c - 32 : c);
		static const char *verbs[] = {"EHLO", "HELO", "STARTTLS", "AUTH", "MAIL", "RCPT", "DATA", "QUIT", "RSET", "NOOP"};
		for (const char *v : verbs)
			if (verb == v) t->command = verb;
		if (t->command == "AUTH") t->auth_sent = true;
		return 0;
	}
	if (type != CURLINFO_HEADER_IN) return 0;
	std::string_view in(data, size);
	while (!in.empty()) {
		auto nl = in.find('\n');
		std::string_view line = in.substr(0, nl);
		in = nl == std::string_view::npos ? std::string_view{} : in.substr(nl + 1);
		while (!line.empty() && (line.back() == '\r' || line.back() == '\n')) line.remove_suffix(1);
		if (line.size() < 3) continue;
		std::string text(line.size() > 4 ? line.substr(4) : std::string_view{});
		if (t->command == "EHLO" && (text == "STARTTLS" || text.starts_with("STARTTLS "))) t->starttls = true;
		t->pending.push_back(text);
		if (line.size() == 3 || line[3] == ' ') {
			int code = std::atoi(std::string(line.substr(0, 3)).c_str());
			if (code == 334 && t->command == "AUTH") t->challenge = text;
			if (code >= 400 && t->code == 0) {
				t->code = code;
				t->lines = std::move(t->pending);
				t->phase = t->command;
			}
			t->pending.clear();
		}
	}
	return 0;
}

// textproto.Error: "%03d %q" with the reply's lines.
std::string reply_text(const SmtpTrace &t)
{
	std::string msg;
	for (std::size_t i = 0; i < t.lines.size(); i++) msg += (i ? "\n" : "") + t.lines[i];
	char code[8];
	std::snprintf(code, sizeof code, "%03d", t.code);
	return std::string(code) + " " + gostd::quote(msg);
}

struct Upload {
	const std::string *data;
	const SmtpTrace *trace;
	bool login = false;  // a login is set: the message goes only after one
	bool unauthenticated = false;
	std::size_t at = 0;
};

size_t on_read(char *buffer, size_t size, size_t count, void *user)
{
	auto *u = static_cast<Upload *>(user);
	if (u->login && !u->trace->auth_sent) {
		// libcurl skips the login when the server offers none; Go's client logs in anyway and is refused. The
		// message never goes without the login the channel has.
		u->unauthenticated = true;
		return CURL_READFUNC_ABORT;
	}
	size_t n = std::min(size * count, u->data->size() - u->at);
	std::memcpy(buffer, u->data->data() + u->at, n);
	u->at += n;
	return n;
}

Result send_email(const Channel &ch, const Rendered &r, OAuth &oauth)
{
	std::string host = ch.get("host"), security = ch.get("security");
	bool oauth2 = ch.get("auth") == "oauth2";
	std::string user, token;
	if (oauth2) {
		if (security != "starttls" && security != "tls") return permanent("a sign-in needs STARTTLS or TLS");
		user = ch.get("username");
		if (user.empty()) user = ch.get("oauthAccount");
		if (user.empty()) return permanent("the user name is missing");
		if (Result err = oauth.access_token(ch, token)) return err;
	} else {
		user = ch.get("username");
		if (!user.empty() && security != "starttls" && security != "tls") return permanent("a login needs STARTTLS or TLS");
	}
	// "From" may carry a display name: the envelope takes the address alone.
	auto sender = gostd::mail::parse_address(ch.get("from"));
	if (!sender) return permanent("mail: invalid address");
	std::vector<std::string> to = split_list(ch.get("to"));
	std::string message = email_message(*sender, to, r);

	std::call_once(curl_once, [] { curl_global_init(CURL_GLOBAL_DEFAULT); });
	CURL *curl = curl_easy_init();
	if (!curl) return Error{"cannot start an SMTP session"};
	std::string url = (security == "tls" ? "smtps://" : "smtp://") + host_port(host, ch.get("port")) + "/" + hello_name();
	char errbuf[CURL_ERROR_SIZE] = {};
	SmtpTrace trace;
	Upload upload{&message, &trace, oauth2 || !user.empty()};
	curl_slist *rcpt = nullptr;
	for (const auto &a : to) rcpt = curl_slist_append(rcpt, a.c_str());
	std::string from = "<" + sender->address + ">";
	curl_easy_setopt(curl, CURLOPT_URL, url.c_str());
	curl_easy_setopt(curl, CURLOPT_PROTOCOLS_STR, "smtp,smtps");
	curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
	curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS, send_timeout_ms);
	curl_easy_setopt(curl, CURLOPT_ERRORBUFFER, errbuf);
	curl_easy_setopt(curl, CURLOPT_USE_SSL, security == "starttls" ? static_cast<long>(CURLUSESSL_ALL) : static_cast<long>(CURLUSESSL_NONE));
	if (ch.flag("skipVerify")) {
		// The user's choice, for an internal server with a certificate of its own.
		curl_easy_setopt(curl, CURLOPT_SSL_VERIFYPEER, 0L);
		curl_easy_setopt(curl, CURLOPT_SSL_VERIFYHOST, 0L);
	}
	if (oauth2) {
		curl_easy_setopt(curl, CURLOPT_USERNAME, user.c_str());
		curl_easy_setopt(curl, CURLOPT_XOAUTH2_BEARER, token.c_str());
		curl_easy_setopt(curl, CURLOPT_LOGIN_OPTIONS, "AUTH=XOAUTH2");
	} else if (!user.empty()) {
		std::string password = ch.get("password");
		curl_easy_setopt(curl, CURLOPT_USERNAME, user.c_str());
		curl_easy_setopt(curl, CURLOPT_PASSWORD, password.c_str());
		// PLAIN, or LOGIN for servers that offer only it (older Exchange).
		curl_easy_setopt(curl, CURLOPT_LOGIN_OPTIONS, "AUTH=PLAIN;AUTH=LOGIN");
	}
	// The credentials go with the AUTH command, as Go's smtp client sends them.
	curl_easy_setopt(curl, CURLOPT_SASL_IR, 1L);
	curl_easy_setopt(curl, CURLOPT_MAIL_FROM, from.c_str());
	curl_easy_setopt(curl, CURLOPT_MAIL_RCPT, rcpt);
	curl_easy_setopt(curl, CURLOPT_UPLOAD, 1L);
	curl_easy_setopt(curl, CURLOPT_READFUNCTION, on_read);
	curl_easy_setopt(curl, CURLOPT_READDATA, &upload);
	curl_easy_setopt(curl, CURLOPT_INFILESIZE_LARGE, static_cast<curl_off_t>(message.size()));
	curl_easy_setopt(curl, CURLOPT_VERBOSE, 1L);
	curl_easy_setopt(curl, CURLOPT_DEBUGFUNCTION, on_debug);
	curl_easy_setopt(curl, CURLOPT_DEBUGDATA, &trace);
	CURLcode code = curl_easy_perform(curl);
	curl_slist_free_all(rcpt);
	curl_easy_cleanup(curl);
	if (code == CURLE_OK) return std::nullopt;
	if (upload.unauthenticated) {
		Error e = permanent("smtp: server doesn't support AUTH");
		// A sign-in the server refused is shown on the page, as Go's refused XOAUTH2.
		if (oauth2) oauth.forget(ch);
		e.auth = oauth2;
		return e;
	}
	if (code == CURLE_USE_SSL_FAILED && !trace.starttls && trace.code == 0) return permanent("the server does not offer STARTTLS");
	if (code == CURLE_LOGIN_DENIED && trace.code == 0) {
		// A refused XOAUTH2 token comes with a 334 of error details, after which libcurl ends the session (Go answers
		// it and gets the server's 5xx); or the server offers none of the sign-ins (Go tries its own and is refused).
		std::string text = curl_error(code, errbuf);
		if (oauth2 && !trace.challenge.empty()) {
			std::string details = base64::decode(trace.challenge).value_or(trace.challenge);
			text = "the mail server refused the sign-in: " + details;
		}
		Error e = permanent(text);
		if (oauth2) oauth.forget(ch);
		e.auth = oauth2;
		return e;
	}
	if (trace.code == 0) return Error{curl_error(code, errbuf)};
	Error e{reply_text(trace)};
	// The server's 5xx answers to a login, the sender, a recipient or the message are permanent; 4xx are temporary
	// by the protocol, and a refused greeting, EHLO or STARTTLS is retried.
	bool refusal = trace.phase == "AUTH" || trace.phase == "MAIL" || trace.phase == "RCPT" || trace.phase == "DATA";
	e.permanent = refusal && trace.code >= 500;
	if (trace.phase == "AUTH" && oauth2) {
		// A refused token is dropped, so the next attempt renews it.
		oauth.forget(ch);
		e.auth = e.permanent;
	}
	return e;
}

// ---- syslog ----

std::string errno_text(int e) { return std::strerror(e); }

// Connects to host:port (tcp or udp), trying the addresses in turn until the deadline; -1 and error otherwise.
int dial(const std::string &host, const std::string &port, bool udp, std::chrono::steady_clock::time_point deadline,
         std::string &error)
{
	std::string network = udp ? "udp" : "tcp";
	addrinfo hints{};
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = udp ? SOCK_DGRAM : SOCK_STREAM;
	addrinfo *list = nullptr;
	if (int rc = getaddrinfo(host.c_str(), port.c_str(), &hints, &list); rc != 0) {
		error = "dial " + network + ": lookup " + host + ": " + gai_strerror(rc);
		return -1;
	}
	int fd = -1;
	for (addrinfo *a = list; a && fd < 0; a = a->ai_next) {
		int s = ::socket(a->ai_family, a->ai_socktype | SOCK_CLOEXEC | SOCK_NONBLOCK, a->ai_protocol);
		if (s < 0) {
			error = "dial " + network + " " + host_port(host, port) + ": socket: " + errno_text(errno);
			continue;
		}
		int rc = ::connect(s, a->ai_addr, a->ai_addrlen);
		if (rc != 0 && errno == EINPROGRESS) {
			auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - std::chrono::steady_clock::now()).count();
			pollfd p{s, POLLOUT, 0};
			rc = left > 0 ? ::poll(&p, 1, static_cast<int>(left)) : 0;
			if (rc == 1) {
				int so = 0;
				socklen_t len = sizeof so;
				getsockopt(s, SOL_SOCKET, SO_ERROR, &so, &len);
				rc = so ? (errno = so, -1) : 0;
			} else {
				errno = rc == 0 ? ETIMEDOUT : errno;
				rc = -1;
			}
		}
		if (rc != 0) {
			error = "dial " + network + " " + host_port(host, port) + ": connect: " + errno_text(errno);
			::close(s);
			continue;
		}
		fd = s;
	}
	freeaddrinfo(list);
	return fd;
}

bool wait_fd(int fd, short events, std::chrono::steady_clock::time_point deadline)
{
	auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - std::chrono::steady_clock::now()).count();
	if (left <= 0) return false;
	pollfd p{fd, events, 0};
	return ::poll(&p, 1, static_cast<int>(left)) == 1;
}

std::string ssl_error_text()
{
	unsigned long e = ERR_get_error();
	char buf[256];
	ERR_error_string_n(e, buf, sizeof buf);
	return e ? buf : "TLS handshake failed";
}

Result send_syslog(const Channel &ch, const Rendered &r)
{
	auto deadline = std::chrono::steady_clock::now() + std::chrono::milliseconds(send_timeout_ms);
	std::string protocol = ch.get("protocol"), host = ch.get("host");
	std::string msg = syslog_message(r, protocol);
	std::string error;
	int fd = dial(host, ch.get("port"), protocol != "tls" && protocol != "tcp", deadline, error);
	if (fd < 0) return Error{error};
	Result result;
	if (protocol == "tls") {
		SSL_CTX *ctx = SSL_CTX_new(TLS_client_method());
		SSL *ssl = ctx && SSL_CTX_set_min_proto_version(ctx, TLS1_2_VERSION) == 1 ? SSL_new(ctx) : nullptr;
		bool ok = ssl != nullptr;
		if (ok) {
			if (!ch.flag("skipVerify")) {
				SSL_CTX_load_verify_locations(ctx, ca_bundle, nullptr);
				SSL_set_verify(ssl, SSL_VERIFY_PEER, nullptr);
				unsigned char ip[16];
				X509_VERIFY_PARAM *param = SSL_get0_param(ssl);
				if (inet_pton(AF_INET, host.c_str(), ip) == 1 || inet_pton(AF_INET6, host.c_str(), ip) == 1)
					X509_VERIFY_PARAM_set1_ip_asc(param, host.c_str());
				else
					X509_VERIFY_PARAM_set1_host(param, host.c_str(), 0);
			}
			unsigned char ip[16];
			if (inet_pton(AF_INET, host.c_str(), ip) != 1 && inet_pton(AF_INET6, host.c_str(), ip) != 1)
				SSL_set_tlsext_host_name(ssl, host.c_str());
			SSL_set_fd(ssl, fd);
			for (;;) {
				int rc = SSL_connect(ssl);
				if (rc == 1) break;
				int e = SSL_get_error(ssl, rc);
				if ((e == SSL_ERROR_WANT_READ && wait_fd(fd, POLLIN, deadline)) ||
				    (e == SSL_ERROR_WANT_WRITE && wait_fd(fd, POLLOUT, deadline)))
					continue;
				result = Error{"tls: " + (e == SSL_ERROR_WANT_READ || e == SSL_ERROR_WANT_WRITE ? std::string("i/o timeout") : ssl_error_text())};
				ok = false;
				break;
			}
		} else {
			result = Error{"tls: " + ssl_error_text()};
		}
		for (std::size_t at = 0; ok && at < msg.size();) {
			int n = SSL_write(ssl, msg.data() + at, static_cast<int>(msg.size() - at));
			if (n > 0) {
				at += static_cast<std::size_t>(n);
				continue;
			}
			int e = SSL_get_error(ssl, n);
			if ((e == SSL_ERROR_WANT_READ && wait_fd(fd, POLLIN, deadline)) ||
			    (e == SSL_ERROR_WANT_WRITE && wait_fd(fd, POLLOUT, deadline)))
				continue;
			result = Error{"write: " + ssl_error_text()};
			ok = false;
		}
		if (ok) SSL_shutdown(ssl);
		if (ssl) SSL_free(ssl);
		if (ctx) SSL_CTX_free(ctx);
	} else {
		for (std::size_t at = 0; at < msg.size();) {
			ssize_t n = ::send(fd, msg.data() + at, msg.size() - at, MSG_NOSIGNAL);
			if (n >= 0) {
				at += static_cast<std::size_t>(n);
				continue;
			}
			if ((errno == EAGAIN || errno == EWOULDBLOCK) && wait_fd(fd, POLLOUT, deadline)) continue;
			result = Error{"write: " + errno_text(errno)};
			break;
		}
	}
	::close(fd);
	return result;
}

Result send_raw(const Channel &ch, const Rendered &r, Delivery *d, OAuth &oauth)
{
	if (ch.type == "email") return send_email(ch, r, oauth);
	if (ch.type == "syslog") return send_syslog(ch, r);
	return send_http(ch, r, d, perform);
}

} // namespace

HttpAnswer perform(const HttpRequest &request)
{
	std::call_once(curl_once, [] { curl_global_init(CURL_GLOBAL_DEFAULT); });
	HttpAnswer answer;
	CURL *curl = curl_easy_init();
	if (!curl) {
		answer.error = "cannot start an HTTP request";
		return answer;
	}
	char errbuf[CURL_ERROR_SIZE] = {};
	Body body{&answer.body, request.max_body};
	curl_slist *headers = nullptr;
	for (const auto &[k, v] : request.headers) headers = curl_slist_append(headers, (k + ": " + v).c_str());
	// No "Expect: 100-continue" pause and no "Accept: */*" of libcurl's own.
	headers = curl_slist_append(headers, "Expect:");
	bool has_accept = false;
	for (const auto &[k, v] : request.headers) has_accept = has_accept || k == "Accept";
	if (!has_accept) headers = curl_slist_append(headers, "Accept:");
	curl_easy_setopt(curl, CURLOPT_URL, request.url.c_str());
	curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
	curl_easy_setopt(curl, CURLOPT_PROTOCOLS_STR, "http,https");
	curl_easy_setopt(curl, CURLOPT_REDIR_PROTOCOLS_STR, "http,https");
	curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 1L);
	curl_easy_setopt(curl, CURLOPT_MAXREDIRS, 10L);
	curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS, send_timeout_ms);
	curl_easy_setopt(curl, CURLOPT_ERRORBUFFER, errbuf);
	curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
	curl_easy_setopt(curl, CURLOPT_ACCEPT_ENCODING, "");
	curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, on_body);
	curl_easy_setopt(curl, CURLOPT_WRITEDATA, &body);
	// A POST that is redirected with 301, 302 or 303 becomes a GET, as Go's client does it.
	if (request.method != "POST") curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, request.method.c_str());
	curl_easy_setopt(curl, CURLOPT_POSTFIELDS, request.body.data());
	curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE_LARGE, static_cast<curl_off_t>(request.body.size()));
	if (request.skip_verify) {
		curl_easy_setopt(curl, CURLOPT_SSL_VERIFYPEER, 0L);
		curl_easy_setopt(curl, CURLOPT_SSL_VERIFYHOST, 0L);
	}
	CURLcode code = curl_easy_perform(curl);
	long status = 0;
	curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &status);
	if (code == CURLE_OK || (code == CURLE_WRITE_ERROR && body.cut)) answer.status = static_cast<int>(status);
	else answer.error = curl_error(code, errbuf);
	curl_slist_free_all(headers);
	curl_easy_cleanup(curl);
	return answer;
}

Result send_channel(const Channel &ch, const Rendered &r, Delivery *delivery, OAuth &oauth)
{
	Result err = send_raw(ch, r, delivery, oauth);
	if (!err) return err;
	return redact(ch, *err);
}

} // namespace nkos::notify::transport
