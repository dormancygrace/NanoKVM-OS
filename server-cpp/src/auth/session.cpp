#include "auth/session.hpp"

#include "api/response.hpp"
#include "auth/store.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"

#include <cctype>
#include <set>
#include <arpa/inet.h>
#include <cerrno>
#include <charconv>
#include <chrono>
#include <cstdio>
#include <cstring>
#include <map>
#include <optional>
#include <unistd.h>

namespace nkos::auth {

namespace {

using Clock = std::chrono::system_clock;
using TimePoint = Clock::time_point;

// --- addresses ---------------------------------------------------------------------------------------------------

struct Addr {
	bool v4 = false;
	unsigned char b[16] = {};
};

std::string strip_brackets(std::string s)
{
	if (!s.empty() && s.front() == '[') s.erase(0, 1);
	if (!s.empty() && s.back() == ']') s.pop_back();
	return s;
}

// netip.ParseAddr: an IPv4 literal is IPv4; an IPv6 literal (also ::ffff:a.b.c.d) is IPv6.
std::optional<Addr> parse_addr(const std::string &text)
{
	Addr a;
	if (inet_pton(AF_INET, text.c_str(), a.b) == 1) {
		a.v4 = true;
		return a;
	}
	if (inet_pton(AF_INET6, text.c_str(), a.b) == 1) return a;
	return std::nullopt;
}

bool prefix_contains(const std::string &prefix, const Addr &addr)
{
	auto slash = prefix.find('/');
	if (slash == std::string::npos) return false;
	auto net = parse_addr(prefix.substr(0, slash));
	if (!net || net->v4 != addr.v4) return false;
	// The whole suffix must be a decimal length: a typo must not read as /0 and trust every address.
	int bits = -1;
	const char *first = prefix.data() + slash + 1, *last = prefix.data() + prefix.size();
	auto [end, ec] = std::from_chars(first, last, bits);
	int max = addr.v4 ? 32 : 128;
	if (first == last || ec != std::errc{} || end != last || bits < 0 || bits > max) return false;
	for (int i = 0; i < bits; i++) {
		int byte = i / 8, bit = 7 - i % 8;
		if (((net->b[byte] >> bit) & 1) != ((addr.b[byte] >> bit) & 1)) return false;
	}
	return true;
}

// isTrustedProxy: remote is "host" or "host:port"; trusted entries are prefixes or single addresses.
bool trusted_proxy(const std::string &remote)
{
	auto addr = parse_addr(strip_brackets(remote));
	if (!addr) return false;
	for (const auto &entry : config::get().security.trusted_proxies) {
		std::string value = entry;
		while (!value.empty() && value.front() == ' ') value.erase(0, 1);
		while (!value.empty() && value.back() == ' ') value.pop_back();
		if (value.empty()) continue;
		if (prefix_contains(value, *addr)) return true;
		auto single = parse_addr(value);
		if (single && single->v4 == addr->v4 && std::memcmp(single->b, addr->b, 16) == 0) return true;
	}
	return false;
}

std::string single_forwarded(const std::string &value)
{
	if (value.find(',') != std::string::npos) return {};
	std::string v = value;
	while (!v.empty() && (v.front() == ' ' || v.front() == '\t')) v.erase(0, 1);
	while (!v.empty() && (v.back() == ' ' || v.back() == '\t')) v.pop_back();
	return v;
}

std::string lower(std::string s)
{
	for (auto &c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return s;
}

// --- cookies -----------------------------------------------------------------------------------------------------

std::optional<std::string> cookie(const http::Request &request, const std::string &name)
{
	for (const auto &header : request.header_values("cookie")) {
		std::string_view rest = header;
		while (!rest.empty()) {
			auto semi = rest.find(';');
			std::string_view part = rest.substr(0, semi);
			while (!part.empty() && part.front() == ' ') part.remove_prefix(1);
			auto eq = part.find('=');
			if (eq != std::string_view::npos && part.substr(0, eq) == name) {
				std::string_view value = part.substr(eq + 1);
				if (value.size() >= 2 && value.front() == '"' && value.back() == '"') value = value.substr(1, value.size() - 2);
				return http::url_decode(value);
			}
			if (semi == std::string_view::npos) break;
			rest = rest.substr(semi + 1);
		}
	}
	return std::nullopt;
}

// net/http Cookie.String() as gin's SetCookie makes it: SameSite=Strict, path "/", HttpOnly.
std::string cookie_line(const std::string &name, const std::string &value, int max_age, bool secure)
{
	std::string line = name + "=" + value + "; Path=/";
	if (max_age > 0) line += "; Max-Age=" + std::to_string(max_age);
	else if (max_age < 0) line += "; Max-Age=0";
	line += "; HttpOnly";
	if (secure) line += "; Secure";
	line += "; SameSite=Strict";
	return line;
}

// --- idle sign-out -----------------------------------------------------------------------------------------------

constexpr const char *idle_limit_file = "/etc/kvm/idle-signout";

struct IdleClock {
	TimePoint last;
	bool over = false;
	TimePoint token_until;
};

struct Idle {
	bool loaded = false;
	std::chrono::seconds limit{0};
	TimePoint since = Clock::now();
	TimePoint dead_before{};
	std::map<std::string, IdleClock> clocks;
} idle;

bool valid_idle_limit(int seconds)
{
	for (int v : idle_limits)
		if (v == seconds) return true;
	return false;
}

void load_idle_limit()
{
	if (idle.loaded) return;
	idle.loaded = true;
	FILE *f = std::fopen(idle_limit_file, "r");
	if (!f) return;
	char buf[32] = {};
	size_t n = std::fread(buf, 1, sizeof buf - 1, f);
	std::fclose(f);
	std::string text(buf, n);
	while (!text.empty() && std::isspace(static_cast<unsigned char>(text.back()))) text.pop_back();
	while (!text.empty() && std::isspace(static_cast<unsigned char>(text.front()))) text.erase(0, 1);
	char *end = nullptr;
	long seconds = std::strtol(text.c_str(), &end, 10);
	if (text.empty() || *end != '\0' || !valid_idle_limit(static_cast<int>(seconds))) {
		log::warn("ignoring {}: not a limit that can be set", idle_limit_file);
		return;
	}
	idle.limit = std::chrono::seconds(seconds);
}

TimePoint from_seconds(std::int64_t s) { return TimePoint(std::chrono::seconds(s)); }

// idleStateOf: alive, and the time left.
std::pair<bool, std::chrono::nanoseconds> idle_state(const std::string &key, TimePoint issued, TimePoint until, bool touch,
                                                     TimePoint now)
{
	load_idle_limit();
	auto it = idle.clocks.find(key);
	if (it != idle.clocks.end() && it->second.over) return {false, {}};
	if (it == idle.clocks.end() && issued != TimePoint{} && issued <= idle.dead_before) return {false, {}};
	if (idle.limit.count() == 0) return {true, {}};
	if (it == idle.clocks.end()) {
		for (auto c = idle.clocks.begin(); c != idle.clocks.end();)
			c = c->second.token_until <= now ? idle.clocks.erase(c) : std::next(c);
		TimePoint start = idle.since;
		if (issued > start) start = issued;
		if (start > now) start = now;
		it = idle.clocks.emplace(key, IdleClock{start, false, until}).first;
	}
	auto left = std::chrono::duration_cast<std::chrono::nanoseconds>(idle.limit) - (now - it->second.last);
	if (left.count() <= 0) {
		it->second.over = true;
		return {false, {}};
	}
	if (touch) {
		it->second.last = now;
		left = idle.limit;
	}
	return {true, left};
}

// --- recent authentication, revocation ---------------------------------------------------------------------------

constexpr auto recent_window = std::chrono::minutes(5);
std::map<std::string, TimePoint> recent;
std::vector<std::function<void(const std::string &)>> revoke_hooks;

} // namespace

bool recent_auth(const http::Request &request)
{
	if (!request.auth) return false;
	if (!request.auth->has_token) return true;  // authentication disabled
	auto it = recent.find(request.auth->session_key);
	return it != recent.end() && Clock::now() - it->second < recent_window;
}

namespace {

// passwordChangeRoutes.
bool password_change_route(const http::Request &r)
{
	static const std::pair<const char *, const char *> routes[] = {
		{"GET", "/api/auth/account"}, {"GET", "/api/auth/password"}, {"POST", "/api/auth/password"},
		{"POST", "/api/auth/logout"}, {"GET", "/api/auth/idle"},     {"POST", "/api/auth/activity"},
		{"GET", "/api/vm/web-title"},
	};
	for (auto [m, p] : routes)
		if (r.method == m && r.path == p) return true;
	return false;
}

// bearerToken: (token, supplied). A malformed header counts as supplied with no token.
std::pair<std::string, bool> bearer(const std::string &header)
{
	if (header.empty()) return {"", false};
	constexpr std::string_view prefix = "bearer ";
	if (header.size() <= prefix.size() || lower(header.substr(0, prefix.size())) != prefix) return {"", true};
	std::string token = header.substr(prefix.size());
	while (!token.empty() && std::isspace(static_cast<unsigned char>(token.front()))) token.erase(0, 1);
	while (!token.empty() && std::isspace(static_cast<unsigned char>(token.back()))) token.pop_back();
	if (token.empty() || token.find_first_of(" \t\r\n") != std::string::npos) return {"", true};
	return {token, true};
}

} // namespace

std::string client_address(const http::Request &request)
{
	std::string peer = request.peer;
	if (!trusted_proxy(peer)) return peer;
	std::vector<std::string> entries;
	for (const auto &value : request.header_values("x-forwarded-for")) {
		std::string_view rest = value;
		for (;;) {
			auto comma = rest.find(',');
			entries.emplace_back(rest.substr(0, comma));
			if (comma == std::string_view::npos) break;
			rest = rest.substr(comma + 1);
		}
	}
	for (auto it = entries.rbegin(); it != entries.rend(); ++it) {
		std::string e = *it;
		while (!e.empty() && std::isspace(static_cast<unsigned char>(e.front()))) e.erase(0, 1);
		while (!e.empty() && std::isspace(static_cast<unsigned char>(e.back()))) e.pop_back();
		auto addr = parse_addr(e);
		if (!addr) return peer;
		// Unmap ::ffff:a.b.c.d to a.b.c.d.
		static const unsigned char mapped[12] = {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff};
		if (!addr->v4 && std::memcmp(addr->b, mapped, 12) == 0) {
			char text[INET_ADDRSTRLEN];
			inet_ntop(AF_INET, addr->b + 12, text, sizeof text);
			e = text;
		} else {
			char text[INET6_ADDRSTRLEN];
			inet_ntop(addr->v4 ? AF_INET : AF_INET6, addr->b, text, sizeof text);
			e = text;
		}
		if (!trusted_proxy(e)) return e;
	}
	return peer;
}

std::pair<std::string, std::string> external_address(const http::Request &request)
{
	std::string host = request.header("host");
	std::string scheme = lower(config::get().proto);
	if (scheme != "https") scheme = "http";
	if (request.tls) scheme = "https";
	if (!trusted_proxy(request.peer)) return {host, scheme};
	std::string forwarded = single_forwarded(request.header("x-forwarded-host"));
	if (!forwarded.empty() && forwarded.find_first_of("/?#@ \t\r\n") == std::string::npos) host = forwarded;
	std::string proto = lower(single_forwarded(request.header("x-forwarded-proto")));
	if (proto == "http" || proto == "https") scheme = proto;
	return {host, scheme};
}

std::string session_cookie_name(const http::Request &request)
{
	return external_address(request).second == "https" ? cookie_name : http_cookie_name;
}

void set_session_cookie(const http::Request &request, http::Response &response, const std::string &token)
{
	std::string name = session_cookie_name(request);
	bool secure = name == cookie_name;
	if (secure) response.add_header("Set-Cookie", cookie_line(http_cookie_name, "", -1, true));
	else response.add_header("Set-Cookie", cookie_line(cookie_name, "", -1, false));
	response.add_header("Set-Cookie", cookie_line(name, token, static_cast<int>(config::get().jwt.refresh_token_duration), secure));
}

void clear_session_cookie(const http::Request &request, http::Response &response)
{
	bool secure = session_cookie_name(request) == cookie_name;
	for (const char *name : {cookie_name, http_cookie_name}) response.add_header("Set-Cookie", cookie_line(name, "", -1, secure));
}

bool authenticate(http::Request &request, bool &idle_over)
{
	idle_over = false;
	const auto &conf = config::get();
	if (conf.authentication == "disable") {
		request.auth = http::Auth{"admin", "admin", false, false, 0, {}, 0, 0};
		return true;
	}
	auto [raw, supplied] = bearer(request.header("authorization"));
	if (!supplied) {
		std::string name = session_cookie_name(request);
		auto value = cookie(request, name);
		if (!value && name == http_cookie_name) value = cookie(request, cookie_name);
		if (!value) return false;
		raw = *value;
	}
	if (raw.empty()) return false;
	auto token = parse(raw);
	if (!token) return false;
	std::string error;
	auto user = store().validate_token(token->username, token->token_version, error);
	if (!user) {
		log::debug("validate session for \"{}\": {}", token->username, error);
		return false;
	}
	std::string key = session_key(*token);
	TimePoint issued = token->issued ? from_seconds(token->issued) : TimePoint{};
	if (!idle_state(key, issued, from_seconds(token->expires), supplied, Clock::now()).first) {
		idle_over = true;
		return false;
	}
	request.auth = http::Auth{user->username, user->role, user->must_change_password, true, token->token_version,
	                          key, token->issued, token->expires};
	return true;
}

namespace {

// The guard's checks; false with the refusal in res.
bool passes(const Guard &guard, http::Request &req, http::Response &res)
{
	bool idle_over = false;
	if (!authenticate(req, idle_over)) {
		if (idle_over) clear_session_cookie(req, res);
		api::plain_json(res, 401, "unauthorized");
		return false;
	}
	if (req.auth->must_change_password && !password_change_route(req)) {
		res.json(403, "{\"code\":-10,\"msg\":\"password change required\"}");
		return false;
	}
	if (!guard.roles.empty() && std::find(guard.roles.begin(), guard.roles.end(), req.auth->role) == guard.roles.end()) {
		api::plain_json(res, 403, "forbidden");
		return false;
	}
	if (guard.recent_auth && !recent_auth(req)) {
		res.json(403, "{\"code\":-11,\"msg\":\"confirm your password to continue\"}");
		return false;
	}
	return true;
}

} // namespace

http::CheckedHandler guarded(http::Handler handler, Guard guard)
{
	return {[handler = std::move(handler), guard](http::Request &req, http::Response &res) {
		        if (passes(guard, req, res)) handler(req, res);
	        },
	        [guard](http::Request &req) {
		        http::Response refusal;
		        return passes(guard, req, refusal);
	        }};
}

http::UploadRoute guarded_upload(http::UploadRoute route, Guard guard)
{
	return [route = std::move(route), guard = std::move(guard)](http::Request &req, http::Response &res) {
		bool passed = false;
		guarded([&passed](http::Request &, http::Response &) { passed = true; }, guard)(req, res);
		return passed ? route(req, res) : nullptr;
	};
}

int idle_limit_seconds()
{
	load_idle_limit();
	return static_cast<int>(idle.limit.count());
}

std::optional<std::string> set_idle_limit(int seconds)
{
	if (!valid_idle_limit(seconds)) return std::string("not a limit that can be set");
	auto prior = std::chrono::seconds(idle_limit_seconds());
	auto next = std::chrono::seconds(seconds);
	if (prior == next) return std::nullopt;
	if (seconds == 0) {
		if (::unlink(idle_limit_file) != 0 && errno != ENOENT) return std::string(std::strerror(errno));
	} else if (!config::atomic_write(idle_limit_file, std::to_string(seconds) + "\n", 0644)) {
		return std::string(std::strerror(errno));
	}
	auto now = Clock::now();
	if (prior.count() > 0) {
		for (auto &[key, clock] : idle.clocks)
			if (now - clock.last >= prior) clock.over = true;
		auto dead = now - prior;
		if (idle.since <= dead && dead > idle.dead_before) idle.dead_before = dead;
	}
	idle.limit = next;
	idle.since = now;
	for (auto it = idle.clocks.begin(); it != idle.clocks.end();) {
		if (it->second.token_until <= now) {
			it = idle.clocks.erase(it);
			continue;
		}
		if (!it->second.over) it->second.last = now;
		++it;
	}
	return std::nullopt;
}

std::pair<int, int> idle_status(const http::Request &request, bool touch)
{
	int seconds = idle_limit_seconds();
	if (!request.auth || !request.auth->has_token || seconds == 0) return {seconds, 0};
	auto [alive, left] = idle_state(request.auth->session_key, request.auth->issued ? from_seconds(request.auth->issued) : TimePoint{},
	                                from_seconds(request.auth->expires), touch, Clock::now());
	(void)alive;
	auto secs = std::chrono::duration_cast<std::chrono::seconds>(left + std::chrono::nanoseconds(999'999'999)).count();
	return {seconds, static_cast<int>(secs)};
}

void mark_recent_auth(const http::Request &request)
{
	if (!request.auth || !request.auth->has_token) return;
	mark_recent_auth_key(request.auth->session_key);
}

void mark_recent_auth_key(const std::string &session_key)
{
	auto now = Clock::now();
	for (auto it = recent.begin(); it != recent.end();)
		it = now - it->second >= recent_window ? recent.erase(it) : std::next(it);
	recent[session_key] = now;
}

// --- WebSockets: origin, guard, session registry -------------------------------------------------------------------

namespace {

struct OpenSession {
	std::string username;
	std::uint64_t token_version = 0;
	bool has_token = false;
	std::string idle_key;
	std::int64_t issued = 0, expires = 0;
	bool stream = false;
	std::function<void()> end;  // closes the connection; it leaves the registry from its own close path
};

constexpr auto recheck_interval = std::chrono::seconds(5);
std::map<std::uint64_t, OpenSession> open_sessions;
bool rechecking = false;
// Streamed HTTP responses share the registry with WebSockets; their ids have the top bit, which no socket id has.
std::uint64_t next_stream_id = 0;

void recheck();

void add_session(std::uint64_t id, OpenSession session)
{
	open_sessions[id] = std::move(session);
	if (!rechecking) {
		rechecking = true;
		loop::after(recheck_interval, recheck);
	}
}

// Ending a session closes its connection, which removes it from the registry: run on copies, after the walk.
void end_sessions(const std::vector<std::function<void()>> &ended)
{
	for (const auto &end : ended) end();
}

// Every session whose token has expired or the store no longer accepts, or whose idle limit is over, ends; the store is asked
// once per distinct token. It catches what revoke_user_sessions does not: an account file reset or edited.
void recheck()
{
	if (open_sessions.empty()) {
		rechecking = false;
		return;
	}
	std::map<std::pair<std::string, std::uint64_t>, bool> valid;
	auto now = Clock::now();
	std::vector<std::function<void()>> ended;
	for (auto &[id, s] : open_sessions) {
		// The token's own expiry holds whatever the activity and whether or not idle sign-out is on.
		if (s.has_token && s.expires != 0 && now >= from_seconds(s.expires)) {
			ended.push_back(s.end);
			continue;
		}
		if (s.stream && !s.idle_key.empty()) {
			TimePoint issued = s.issued ? from_seconds(s.issued) : TimePoint{};
			if (!idle_state(s.idle_key, issued, from_seconds(s.expires), false, now).first) {
				ended.push_back(s.end);
				continue;
			}
		}
		if (!s.has_token) continue;
		auto key = std::make_pair(s.username, s.token_version);
		auto it = valid.find(key);
		if (it == valid.end()) {
			std::string error;
			it = valid.emplace(key, store().validate_token(s.username, s.token_version, error).has_value()).first;
		}
		if (!it->second) ended.push_back(s.end);
	}
	end_sessions(ended);
	loop::after(recheck_interval, recheck);
}

// Registers the connection while it is open and passes everything on.
struct RegisteredHandler : http::WsHandler {
	std::shared_ptr<http::WsHandler> inner;
	OpenSession session;
	std::uint64_t id = 0;

	void opened(http::WebSocket &ws) override
	{
		id = ws.id();
		session.end = [&ws] { ws.close(session_revoked_close_code, "session expired or revoked"); };
		add_session(id, session);
		inner->opened(ws);
	}
	void received(http::WebSocket &ws, std::string_view data, bool binary) override { inner->received(ws, data, binary); }
	std::optional<http::Message> next(http::WebSocket &ws) override { return inner->next(ws); }
	bool ready(http::WebSocket &ws) override { return inner->ready(ws); }
	void closed(http::WebSocket &ws) override
	{
		open_sessions.erase(id);
		inner->closed(ws);
	}
};

std::pair<std::string, std::string> split_host_port(const std::string &value)
{
	if (!value.empty() && value.front() == '[') {
		auto close = value.find(']');
		if (close != std::string::npos && close + 1 < value.size() && value[close + 1] == ':')
			return {value.substr(1, close - 1), value.substr(close + 2)};
		std::string host = value;
		std::erase(host, '[');
		std::erase(host, ']');
		return {host, ""};
	}
	auto colon = value.find(':');
	if (colon != std::string::npos && value.find(':', colon + 1) == std::string::npos)
		return {value.substr(0, colon), value.substr(colon + 1)};
	return {value, ""};
}

std::string origin_scheme(const std::string &scheme)
{
	std::string s = lower(scheme);
	if (s == "ws") return "http";
	if (s == "wss") return "https";
	return s;
}

std::string default_port(const std::string &scheme)
{
	std::string s = lower(scheme);
	return s == "https" || s == "wss" ? "443" : "80";
}

struct Origin {
	std::string scheme, host;
};

// An origin as url.Parse reads it and validOriginURL accepts it: http(s), a host, no user, no path beyond "/",
// no query or fragment.
std::optional<Origin> parse_origin(const std::string &text)
{
	auto sep = text.find("://");
	if (sep == std::string::npos) return std::nullopt;
	Origin o{text.substr(0, sep), ""};
	std::string rest = text.substr(sep + 3);
	if (rest.find_first_of("?#") != std::string::npos) return std::nullopt;
	auto slash = rest.find('/');
	std::string path = slash == std::string::npos ? "" : rest.substr(slash);
	o.host = rest.substr(0, slash);
	std::string s = lower(o.scheme);
	if ((s != "http" && s != "https") || o.host.empty() || o.host.find('@') != std::string::npos ||
	    (path != "" && path != "/"))
		return std::nullopt;
	return o;
}

bool equal_origin(const Origin &origin, const std::string &request_host, const std::string &request_scheme)
{
	if (origin_scheme(origin.scheme) != origin_scheme(request_scheme)) return false;
	auto [ohost, oport] = split_host_port(origin.host);
	auto [host, port] = split_host_port(request_host);
	if (lower(ohost) != lower(host)) return false;
	if (oport.empty()) oport = default_port(origin.scheme);
	if (port.empty()) port = default_port(request_scheme);
	return oport == port;
}

} // namespace

namespace {

std::string trimmed(const std::string &s)
{
	auto f = s.find_first_not_of(" \t");
	if (f == std::string::npos) return "";
	return s.substr(f, s.find_last_not_of(" \t") - f + 1);
}

// AllowedOrigin: the origin is one of security.allowedOrigins.
bool allowed_origin(const std::string &text)
{
	auto origin = parse_origin(trimmed(text));
	if (!origin) return false;
	for (const auto &allowed : config::get().security.allowed_origins) {
		auto parsed = parse_origin(trimmed(allowed));
		if (parsed && equal_origin(*origin, parsed->host, parsed->scheme)) return true;
	}
	return false;
}

bool device_host(const http::Request &request)
{
	if (trusted_proxy(request.peer)) return true;
	auto [h, port] = split_host_port(request.header("host"));
	std::string host = h;
	if (host.size() >= 2 && host.front() == '[' && host.back() == ']') host = host.substr(1, host.size() - 2);
	host = lower(host);
	while (!host.empty() && host.back() == '.') host.pop_back();
	unsigned char addr[16];
	if (inet_pton(AF_INET, host.c_str(), addr) == 1 || inet_pton(AF_INET6, host.c_str(), addr) == 1) return true;
	if (host == "localhost" || (host.size() > 10 && host.ends_with(".localhost"))) return true;
	char name[256] = {};
	if (gethostname(name, sizeof name - 1) == 0 && name[0]) {
		std::string n = lower(name);
		if (host == n || host == n + ".local") return true;
	}
	for (const auto &allowed : config::get().security.allowed_origins) {
		auto parsed = parse_origin(trimmed(allowed));
		if (!parsed) continue;
		auto [ah, ap] = split_host_port(parsed->host);
		std::string a = ah;
		if (a.size() >= 2 && a.front() == '[' && a.back() == ']') a = a.substr(1, a.size() - 2);
		if (lower(a) == host) return true;
	}
	return false;
}

void refuse(http::Response &res, int status, const char *json)
{
	res.json(status, json);
	res.done = true;
}

} // namespace

void add_unauthenticated_protections(http::Router &router)
{
	// RequireDeviceHost: over HTTPS the certificate does not match a rebound name.
	router.use([](http::Request &req, http::Response &res) {
		if (req.tls || device_host(req)) return;
		refuse(res, 421,
		       R"({"code":-1,"msg":"unknown host name: open the device by its address or add the name to security.allowedOrigins"})");
	});
	// AllowedOriginCORS (rs/cors with AllowOriginFunc = AllowedOrigin, every header allowed).
	router.use([](http::Request &req, http::Response &res) {
		std::string origin = req.header("origin");
		bool preflight = req.method == "OPTIONS" && !req.header("access-control-request-method").empty();
		if (preflight) {
			res.set_header("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers");
			std::string method = req.header("access-control-request-method");
			for (auto &c : method) c = static_cast<char>(std::toupper(static_cast<unsigned char>(c)));
			static const std::set<std::string> methods{"HEAD", "GET", "POST", "PUT", "PATCH", "DELETE"};
			if (!origin.empty() && allowed_origin(origin) && methods.count(method)) {
				res.set_header("Access-Control-Allow-Origin", origin);
				res.set_header("Access-Control-Allow-Methods", method);
				if (std::string h = req.header("access-control-request-headers"); !h.empty())
					res.set_header("Access-Control-Allow-Headers", h);
			}
			res.status = 204;
			res.done = true;
			return;
		}
		res.set_header("Vary", "Origin");
		if (!origin.empty() && allowed_origin(origin)) res.set_header("Access-Control-Allow-Origin", origin);
	});
	// RejectCrossOriginWrites.
	router.use([](http::Request &req, http::Response &res) {
		std::string site = lower(trimmed(req.header("sec-fetch-site")));
		bool cross_site = site == "cross-site";
		if (req.method == "GET" || req.method == "HEAD" || req.method == "OPTIONS") {
			// A page at the previous network address discovers this public ID-only probe across origins.
			bool public_probe = req.method == "GET" && req.path == "/api/network/trial/probe";
			std::string origin = trimmed(req.header("origin"));
			bool allowed_read = !origin.empty() && allowed_origin(origin);
			if (cross_site && req.path.starts_with("/api/") && !public_probe && !allowed_read)
				refuse(res, 403, R"({"code":-1,"msg":"cross-origin request refused"})");
			return;
		}
		if (!check_websocket_origin(req) || (req.header("origin").empty() && cross_site))
			refuse(res, 403, R"({"code":-1,"msg":"cross-origin request refused"})");
	});
}

bool check_websocket_origin(const http::Request &request)
{
	std::string text = request.header("origin");
	auto first = text.find_first_not_of(" \t");
	if (first == std::string::npos) return true;
	text = text.substr(first, text.find_last_not_of(" \t") - first + 1);
	auto origin = parse_origin(text);
	if (!origin) return false;
	auto [host, scheme] = external_address(request);
	if (equal_origin(*origin, host, scheme)) return true;
	for (const auto &allowed : config::get().security.allowed_origins) {
		std::string a = allowed;
		auto f = a.find_first_not_of(" \t");
		if (f == std::string::npos) continue;
		a = a.substr(f, a.find_last_not_of(" \t") - f + 1);
		auto parsed = parse_origin(a);
		if (parsed && equal_origin(*origin, parsed->host, parsed->scheme)) return true;
	}
	return false;
}

http::WsRoute guarded_ws(http::WsRoute route, Guard guard)
{
	guard.stream = true;
	return [route = std::move(route), guard = std::move(guard)](http::Request &req,
	                                                            http::Response &res) -> std::shared_ptr<http::WsHandler> {
		if (!check_websocket_origin(req)) {
			res.text(403, "Forbidden");
			return nullptr;
		}
		bool allowed = false;
		guarded([&allowed](http::Request &, http::Response &) { allowed = true; }, guard)(req, res);
		if (!allowed) return nullptr;
		auto inner = route(req, res);
		if (!inner) return nullptr;
		auto h = std::make_shared<RegisteredHandler>();
		h->inner = std::move(inner);
		const http::Auth &a = *req.auth;
		h->session = OpenSession{a.username, a.token_version, a.has_token, a.has_token ? a.session_key : "",
		                         a.issued, a.expires, true, nullptr};
		return h;
	};
}

std::uint64_t join_session(const http::Request &request, std::function<void()> end)
{
	const http::Auth &a = *request.auth;
	std::uint64_t id = (std::uint64_t{1} << 63) | ++next_stream_id;
	add_session(id, OpenSession{a.username, a.token_version, a.has_token, a.has_token ? a.session_key : "", a.issued,
	                            a.expires, true, std::move(end)});
	return id;
}

void leave_session(std::uint64_t id) { open_sessions.erase(id); }

void revoke_user_sessions(const std::string &username)
{
	std::vector<std::function<void()>> ended;
	for (auto &[id, s] : open_sessions)
		if (s.username == username) ended.push_back(s.end);
	end_sessions(ended);
	for (const auto &hook : revoke_hooks) hook(username);
}

void on_user_revoked(std::function<void(const std::string &)> hook) { revoke_hooks.push_back(std::move(hook)); }

} // namespace nkos::auth
