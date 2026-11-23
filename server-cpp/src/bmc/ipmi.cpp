#include "bmc/ipmi.hpp"

#include "network/util.hpp"

#include <openssl/crypto.h>
#include <openssl/evp.h>
#include <openssl/hmac.h>
#include <openssl/rand.h>

#include <format>
#include <thread>

namespace nkos::bmc::ipmi {

namespace {

constexpr std::uint8_t rmcp_version = 0x06, class_asf = 0x06, class_ipmi = 0x07;
constexpr std::uint8_t auth_type_none = 0x00, auth_type_rmcp = 0x06;
constexpr std::uint8_t payload_ipmi = 0x00, payload_open_request = 0x10, payload_open_response = 0x11,
                       payload_rakp1 = 0x12, payload_rakp2 = 0x13, payload_rakp3 = 0x14, payload_rakp4 = 0x15,
                       payload_encrypted = 0x80, payload_authenticated = 0x40;
constexpr std::uint8_t net_fn_chassis = 0x00, net_fn_app = 0x06;
constexpr std::uint8_t priv_operator = 3, priv_admin = 4;
constexpr std::size_t max_sessions = 8;
constexpr std::int64_t second = 1'000'000'000;
constexpr std::int64_t session_timeout = 60 * second;
constexpr std::int64_t pending_timeout = 10 * second;
// Logins (RAKP message 1) per client and minute: plenty for a fence agent, few for a hash harvester.
constexpr int max_failures = 20;
constexpr std::int64_t fail_window = 60 * second;
// UDP source addresses are easy to forge: the login counters are bounded.
constexpr std::size_t max_tracked = 1024;
constexpr std::size_t max_events = 32;

// RAKP status codes (IPMI v2.0, table 13-15).
constexpr std::uint8_t status_ok = 0x00, status_no_resources = 0x01, status_invalid_session = 0x02,
                       status_invalid_role = 0x09, status_unauthorized_name = 0x0D, status_invalid_integrity = 0x0F,
                       status_invalid_auth_alg = 0x10, status_invalid_integrity_alg = 0x11;
// Completion codes.
constexpr std::uint8_t cc_ok = 0x00, cc_node_busy = 0xC0, cc_invalid_command = 0xC1, cc_request_too_short = 0xC7,
                       cc_out_of_range = 0xC9, cc_invalid_field = 0xCC, cc_insufficient_priv = 0xD4,
                       cc_not_in_this_state = 0xD5, cc_priv_exceeded = 0x81;

constexpr std::uint8_t auth_hmac_sha1 = 0x01, auth_hmac_sha256 = 0x03;
constexpr std::uint8_t integrity_hmac_sha1_96 = 0x01, integrity_hmac_sha256_128 = 0x04;
constexpr std::uint8_t conf_aes_cbc_128 = 0x01;

Bytes rmcp_header(std::uint8_t cls) { return {rmcp_version, 0x00, 0xFF, cls}; }

std::uint32_t le32(const Bytes &b, std::size_t at)
{
	return static_cast<std::uint32_t>(b[at]) | static_cast<std::uint32_t>(b[at + 1]) << 8 |
	       static_cast<std::uint32_t>(b[at + 2]) << 16 | static_cast<std::uint32_t>(b[at + 3]) << 24;
}

Bytes le32(std::uint32_t v)
{
	return {static_cast<std::uint8_t>(v), static_cast<std::uint8_t>(v >> 8), static_cast<std::uint8_t>(v >> 16),
	        static_cast<std::uint8_t>(v >> 24)};
}

void append(Bytes &out, const Bytes &more) { out.insert(out.end(), more.begin(), more.end()); }

std::uint8_t checksum(Bytes::const_iterator begin, Bytes::const_iterator end)
{
	std::uint8_t sum = 0;
	for (auto it = begin; it != end; ++it) sum = static_cast<std::uint8_t>(sum + *it);
	return static_cast<std::uint8_t>(-sum);
}

bool equal(const Bytes &a, const Bytes &b) { return a.size() == b.size() && CRYPTO_memcmp(a.data(), b.data(), a.size()) == 0; }

Bytes auth_capabilities()
{
	// Channel 1; IPMI v2.0 extended data; no v1.5 authentication types; non-null user names only; IPMI v2.0.
	return {0x01, 0x80, 0x04, 0x02, 0x00, 0x00, 0x00, 0x00};
}

// The host of addr without its port (net.SplitHostPort), or addr.
bool split(const std::string &addr, std::string &host)
{
	std::string port;
	return network::split_host_port(addr, host, port);
}

std::string host_of(const std::string &addr)
{
	std::string host;
	return split(addr, host) ? host : addr;
}

// The login counter of a source: its host, and for IPv6 its /64.
std::string client_key(const std::string &addr)
{
	std::string host;
	if (!split(addr, host)) host.clear();
	auto ip = network::parse_addr(host);
	if (ip && ip->is6() && !ip->is4in6()) {
		network::Addr a = *ip;
		a.zone.clear();
		return network::Prefix{a, 64}.masked().str();
	}
	return host;
}

bool decrypt(const Bytes &key, const Bytes &data, Bytes &out)
{
	if (data.size() < 32 || data.size() % 16 != 0) return false;
	EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
	Bytes plain(data.size() - 16 + 16);
	int n = 0, m = 0;
	bool ok = ctx && EVP_DecryptInit_ex(ctx, EVP_aes_128_cbc(), nullptr, key.data(), data.data()) == 1 &&
	          EVP_CIPHER_CTX_set_padding(ctx, 0) == 1 &&
	          EVP_DecryptUpdate(ctx, plain.data(), &n, data.data() + 16, static_cast<int>(data.size() - 16)) == 1 &&
	          EVP_DecryptFinal_ex(ctx, plain.data() + n, &m) == 1;
	EVP_CIPHER_CTX_free(ctx);
	if (!ok) return false;
	plain.resize(static_cast<std::size_t>(n + m));
	std::size_t pad = plain.back();
	if (pad >= 16 || pad + 1 > plain.size()) return false;
	plain.resize(plain.size() - 1 - pad);
	out = std::move(plain);
	return true;
}

} // namespace

// The worker state shared with the threads: they outlive the server while an action still runs.
struct Server::Work {
	std::mutex mu;
	std::condition_variable cv;
	std::deque<std::string> actions;
	std::deque<Event> events;
	bool action_running = false, event_running = false, finished = false;
	std::shared_ptr<Power> power;
	std::function<void(const std::string &)> log;
	std::function<void(const Event &)> on_event;
	std::thread action_thread, event_thread;
};

Bytes Server::Suite::mac(const Bytes &key, std::initializer_list<const Bytes *> parts) const
{
	Bytes data;
	for (const Bytes *p : parts) append(data, *p);
	Bytes out(EVP_MAX_MD_SIZE);
	unsigned len = 0;
	HMAC(sha256 ? EVP_sha256() : EVP_sha1(), key.data(), static_cast<int>(key.size()), data.data(), data.size(), out.data(), &len);
	out.resize(len);
	return out;
}

static const std::vector<std::array<std::uint8_t, 3>> suite_ids = {{3, auth_hmac_sha1, integrity_hmac_sha1_96},
                                                                 {17, auth_hmac_sha256, integrity_hmac_sha256_128}};

const Server::Suite *Server::suite_for(std::uint8_t auth, std::uint8_t integrity, std::uint8_t conf)
{
	static const Suite suites[] = {{3, auth_hmac_sha1, integrity_hmac_sha1_96, false, 12, 12},
	                               {17, auth_hmac_sha256, integrity_hmac_sha256_128, true, 16, 16}};
	for (const auto &s : suites)
		if (s.auth == auth && s.integrity == integrity && conf == conf_aes_cbc_128) return &s;
	return nullptr;
}

Server::Server() : work_(std::make_shared<Work>())
{
	now = [] {
		return static_cast<std::int64_t>(
			std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::system_clock::now().time_since_epoch()).count());
	};
	random = [](std::uint8_t *b, std::size_t n) { RAND_bytes(b, static_cast<int>(n)); };
	auto work = work_;
	// Power actions run one at a time after the reply: a ForceOff holds the button for seconds, longer than ipmitool
	// waits for an answer.
	work->action_thread = std::thread([work] {
		std::unique_lock lock(work->mu);
		for (;;) {
			work->cv.wait(lock, [&] { return work->finished || !work->actions.empty(); });
			if (work->actions.empty()) return;
			std::string action = work->actions.front();
			work->actions.pop_front();
			work->action_running = true;
			auto power = work->power;
			auto log = work->log;
			lock.unlock();
			Error err = power ? power->act(action) : Error::message("no power control");
			if (err && log) log("IPMI power action failed: " + err.text);
			lock.lock();
			work->action_running = false;
			work->cv.notify_all();
		}
	});
	// Notifications in the order of the logins, one at a time.
	work->event_thread = std::thread([work] {
		std::unique_lock lock(work->mu);
		for (;;) {
			work->cv.wait(lock, [&] { return work->finished || !work->events.empty(); });
			if (work->events.empty()) return;
			Event e = work->events.front();
			work->events.pop_front();
			work->event_running = true;
			auto on_event = work->on_event;
			lock.unlock();
			if (on_event) on_event(e);
			lock.lock();
			work->event_running = false;
			work->cv.notify_all();
		}
	});
}

Server::~Server()
{
	finish();
	// An action still running ends on its own thread.
	if (work_->action_thread.joinable()) work_->action_thread.detach();
}

void Server::finish()
{
	{
		std::lock_guard lock(work_->mu);
		work_->finished = true;
		work_->cv.notify_all();
	}
	// Pending events are delivered before a replacement server starts reporting logins.
	if (work_->event_thread.joinable()) work_->event_thread.join();
}

void Server::drain()
{
	std::unique_lock lock(work_->mu);
	work_->cv.wait(lock, [&] {
		return work_->actions.empty() && !work_->action_running && work_->events.empty() && !work_->event_running;
	});
}

void Server::logf(const std::string &message)
{
	if (log) log(message);
}

void Server::event(const Event &e)
{
	if (!on_event) return;
	std::lock_guard lock(work_->mu);
	work_->on_event = on_event;
	if (work_->finished) return;
	if (work_->events.size() >= max_events) {
		logf("IPMI: notification queue full, " + e.kind + " from " + e.address + " not reported");
		return;
	}
	work_->events.push_back(e);
	work_->cv.notify_all();
}

Stats Server::stats()
{
	std::lock_guard lock(mu_);
	std::int64_t t = now();
	Stats s{0, 0, last_command_, last_command_at_};
	for (const auto &[id, se] : sessions_)
		if (se.active && t - se.last_used <= session_timeout) s.sessions++;
	for (const auto &[host, f] : failed_)
		if (f.count >= max_failures && t - f.since <= fail_window) s.locked_out++;
	return s;
}

Bytes Server::handle(const Bytes &pkt, const std::string &addr)
{
	if (pkt.size() < 5 || pkt[0] != rmcp_version) return {};
	Bytes rest(pkt.begin() + 4, pkt.end());
	switch (pkt[3] & 0x1F) {
	case class_asf: {
		// An ASF presence ping, which some tools send first.
		if (rest.size() < 8 || le32({rest[3], rest[2], rest[1], rest[0]}, 0) != 4542 || rest[4] != 0x80) return {};
		Bytes pong = rmcp_header(class_asf);
		append(pong, {0x00, 0x00, 0x11, 0xBE, 0x40, rest[5], 0x00, 0x10});
		append(pong, {0x00, 0x00, 0x11, 0xBE, 0, 0, 0, 0, 0x81, 0x00, 0, 0, 0, 0, 0, 0});
		return pong;
	}
	case class_ipmi:
		if (pkt[4] == auth_type_none) return v15(rest);
		if (pkt[4] == auth_type_rmcp) return v20(rest, addr);
		return {};
	}
	return {};
}

bool Server::parse_message(const Bytes &m, Request &out)
{
	if (m.size() < 7 || checksum(m.begin(), m.begin() + 2) != m[2] || checksum(m.begin() + 3, m.end() - 1) != m.back())
		return false;
	out.net_fn = m[1] >> 2;
	out.rs_lun = m[1] & 3;
	out.rq_addr = m[3];
	out.rq_seq_lun = m[4];
	out.cmd = m[5];
	out.data.assign(m.begin() + 6, m.end() - 1);
	return true;
}

Bytes Server::Request::reply(std::uint8_t cc, const Bytes &more) const
{
	Bytes head = {rq_addr, static_cast<std::uint8_t>((net_fn | 1) << 2 | (rq_seq_lun & 3))};
	head.push_back(checksum(head.begin(), head.end()));
	Bytes body = {0x20, static_cast<std::uint8_t>((rq_seq_lun & ~3) | rs_lun), cmd, cc};
	append(body, more);
	std::uint8_t sum = checksum(body.begin(), body.end());
	append(head, body);
	head.push_back(sum);
	return head;
}

std::pair<std::uint8_t, Bytes> Server::cipher_suites(const Bytes &data)
{
	if (data.size() < 3) return {cc_request_too_short, {}};
	if (data[1] != payload_ipmi) return {cc_invalid_field, {}};
	Bytes records;
	for (const auto &s : suite_ids)
		append(records, {0xC0, s[0], s[1], static_cast<std::uint8_t>(0x40 | s[2]), static_cast<std::uint8_t>(0x80 | conf_aes_cbc_128)});
	std::size_t start = static_cast<std::size_t>(data[2] & 0x3F) * 16;
	if (start > records.size()) return {cc_out_of_range, {}};
	std::size_t end = std::min(start + 16, records.size());
	Bytes out = {0x01};
	out.insert(out.end(), records.begin() + static_cast<long>(start), records.begin() + static_cast<long>(end));
	return {cc_ok, out};
}

// The pre-session commands ipmitool sends in IPMI v1.5 packets before an RMCP+ session.
Bytes Server::v15(const Bytes &p)
{
	if (p.size() < 10 || p[5] || p[6] || p[7] || p[8]) return {};
	std::size_t length = p[9];
	if (p.size() < 10 + length) return {};
	Request req;
	if (!parse_message(Bytes(p.begin() + 10, p.begin() + 10 + static_cast<long>(length)), req) || req.net_fn != net_fn_app) return {};
	Bytes msg;
	if (req.cmd == 0x38) msg = req.reply(cc_ok, auth_capabilities());
	else if (req.cmd == 0x54) {
		auto [cc, data] = cipher_suites(req.data);
		msg = req.reply(cc, data);
	} else msg = req.reply(cc_insufficient_priv);
	Bytes out = rmcp_header(class_ipmi);
	append(out, {auth_type_none, 0, 0, 0, 0, 0, 0, 0, 0, static_cast<std::uint8_t>(msg.size())});
	append(out, msg);
	return out;
}

void Server::evict_oldest_failure()
{
	if (failed_.empty()) return;
	auto oldest = failed_.begin();
	for (auto it = failed_.begin(); it != failed_.end(); ++it)
		if (it->second.since < oldest->second.since) oldest = it;
	failed_.erase(oldest);
}

bool Server::locked_out(const std::string &addr)
{
	std::string host = client_key(addr);
	auto it = failed_.find(host);
	if (it == failed_.end()) return false;
	if (now() - it->second.since > fail_window) {
		failed_.erase(it);
		return false;
	}
	return it->second.count >= max_failures;
}

void Server::record_failure(const std::string &addr)
{
	std::string host = client_key(addr);
	auto it = failed_.find(host);
	if (it == failed_.end() || now() - it->second.since > fail_window) {
		if (it == failed_.end() && failed_.size() >= max_tracked) evict_oldest_failure();
		failed_[host] = Failures{0, now()};
		it = failed_.find(host);
	}
	if (++it->second.count == max_failures)
		// Refused until the window that started at since ends.
		event(Event{"lockout", "", host, it->second.count, static_cast<int>((fail_window + 60 * second - 1) / (60 * second))});
}

void Server::expire_every(std::int64_t interval)
{
	std::int64_t t = now();
	if (last_expire_ == 0 || t - last_expire_ >= interval || t < last_expire_) {
		last_expire_ = t;
		expire();
	}
}

void Server::expire()
{
	std::int64_t t = now();
	for (auto it = failed_.begin(); it != failed_.end();) it = t - it->second.since > fail_window ? failed_.erase(it) : std::next(it);
	while (failed_.size() > max_tracked) evict_oldest_failure();
	for (auto it = sessions_.begin(); it != sessions_.end();) {
		std::int64_t limit = it->second.active ? session_timeout : pending_timeout;
		it = t - it->second.last_used > limit ? sessions_.erase(it) : std::next(it);
	}
}

std::uint32_t Server::new_id()
{
	for (;;) {
		std::uint8_t b[4];
		random(b, 4);
		std::uint32_t id = le32(Bytes(b, b + 4), 0);
		if (id != 0 && !sessions_.count(id)) return id;
	}
}

// An RMCP+ packet (p starts at the authentication type byte).
Bytes Server::v20(const Bytes &p, const std::string &addr)
{
	if (p.size() < 12) return {};
	std::uint8_t ptype = p[1] & 0x3F;
	std::uint32_t session_id = le32(p, 2), seq = le32(p, 6);
	std::size_t length = static_cast<std::size_t>(p[10]) | static_cast<std::size_t>(p[11]) << 8;
	if (p.size() < 12 + length) return {};
	Bytes payload(p.begin() + 12, p.begin() + 12 + static_cast<long>(length));
	std::lock_guard lock(mu_);
	expire_every(second);
	switch (ptype) {
	case payload_open_request: return open_session(payload, addr);
	case payload_rakp1: return rakp1(payload, addr);
	case payload_rakp3: return rakp3(payload, addr);
	case payload_ipmi: {
		// ipmitool without -C asks for the cipher suites this way, before it opens a session.
		if (session_id == 0) return pre_session_message(payload);
		auto it = sessions_.find(session_id);
		if (it == sessions_.end() || !it->second.active || it->second.addr != addr) return {};
		return message(it->second, p, seq, length);
	}
	}
	return {};
}

Bytes Server::pre_session(std::uint8_t type, const Bytes &payload)
{
	Bytes out = rmcp_header(class_ipmi);
	append(out, {auth_type_rmcp, type, 0, 0, 0, 0, 0, 0, 0, 0});
	out.push_back(static_cast<std::uint8_t>(payload.size()));
	out.push_back(static_cast<std::uint8_t>(payload.size() >> 8));
	append(out, payload);
	return out;
}

Bytes Server::pre_session_message(const Bytes &p)
{
	Request req;
	if (!parse_message(p, req) || req.net_fn != net_fn_app) return {};
	Bytes msg;
	if (req.cmd == 0x38) msg = req.reply(cc_ok, auth_capabilities());
	else if (req.cmd == 0x54) {
		auto [cc, data] = cipher_suites(req.data);
		msg = req.reply(cc, data);
	} else msg = req.reply(cc_insufficient_priv);
	return pre_session(payload_ipmi, msg);
}

Bytes Server::open_session(const Bytes &p, const std::string &addr)
{
	if (p.size() < 32) return {};
	std::uint8_t tag = p[0];
	std::uint32_t console_id = le32(p, 4);
	auto fail = [&](std::uint8_t status) {
		Bytes reply = {tag, status, 0, 0};
		append(reply, le32(console_id));
		return pre_session(payload_open_response, reply);
	};
	if (p[8] != 0x00 || p[16] != 0x01 || p[24] != 0x02) return fail(status_invalid_auth_alg);
	const Suite *su = suite_for(p[12] & 0x3F, p[20] & 0x3F, p[28] & 0x3F);
	if (!su) {
		if ((p[12] & 0x3F) != auth_hmac_sha1 && (p[12] & 0x3F) != auth_hmac_sha256) return fail(status_invalid_auth_alg);
		return fail(status_invalid_integrity_alg);
	}
	std::uint8_t max_priv = p[1] & 0x0F;
	if (max_priv == 0 || max_priv > priv_admin) max_priv = priv_admin;
	if (sessions_.size() >= max_sessions) {
		// A login left halfway makes room, so that unfinished logins cannot lock others out; established sessions stay.
		auto oldest = sessions_.end();
		for (auto it = sessions_.begin(); it != sessions_.end(); ++it)
			if (!it->second.active && (oldest == sessions_.end() || it->second.last_used < oldest->second.last_used)) oldest = it;
		if (oldest == sessions_.end()) return fail(status_no_resources);
		sessions_.erase(oldest);
	}
	Session se;
	se.managed_id = new_id();
	se.console_id = console_id;
	se.suite = su;
	se.max_priv = max_priv;
	se.last_used = now();
	se.addr = addr;
	sessions_[se.managed_id] = se;
	Bytes reply = {tag, status_ok, max_priv, 0};
	append(reply, le32(console_id));
	append(reply, le32(se.managed_id));
	append(reply, {0x00, 0, 0, 0x08, su->auth, 0, 0, 0});
	append(reply, {0x01, 0, 0, 0x08, su->integrity, 0, 0, 0});
	append(reply, {0x02, 0, 0, 0x08, conf_aes_cbc_128, 0, 0, 0});
	return pre_session(payload_open_response, reply);
}

// K_UID: the password padded to the 20 bytes of its field.
Bytes Server::user_key() const
{
	Bytes key(20, 0);
	std::copy_n(password.begin(), std::min<std::size_t>(password.size(), 20), key.begin());
	return key;
}

Bytes Server::rakp1(const Bytes &p, const std::string &addr)
{
	if (p.size() < 28) return {};
	std::uint8_t tag = p[0];
	std::uint32_t managed_id = le32(p, 4);
	auto it = sessions_.find(managed_id);
	if (it == sessions_.end() || it->second.active || it->second.addr != addr) {
		Bytes reply = {tag, status_invalid_session, 0, 0, 0, 0, 0, 0};
		return pre_session(payload_rakp2, reply);
	}
	Session &se = it->second;
	std::uint32_t console_id = se.console_id;
	auto fail = [&](std::uint8_t status) {
		sessions_.erase(managed_id);
		Bytes reply = {tag, status, 0, 0};
		append(reply, le32(console_id));
		return pre_session(payload_rakp2, reply);
	};
	std::size_t name_len = p[27];
	if (name_len > 16 || p.size() < 28 + name_len) return fail(status_unauthorized_name);
	std::copy(p.begin() + 8, p.begin() + 24, se.rm.begin());
	se.role = p[24];
	se.name.assign(p.begin() + 28, p.begin() + 28 + static_cast<long>(name_len));
	std::uint8_t priv = se.role & 0x0F;
	if (priv == 0 || priv > se.max_priv) return fail(status_invalid_role);
	// Every RAKP message 1 counts: each message 2 hands out a password hash to brute-force offline.
	if (locked_out(addr)) return fail(status_unauthorized_name);
	record_failure(addr);
	if (!equal(se.name, Bytes(username.begin(), username.end())) || password.empty()) return fail(status_unauthorized_name);
	random(se.rc.data(), 16);
	se.last_used = now();
	Bytes console = le32(se.console_id), managed = le32(se.managed_id), guid_b(guid.begin(), guid.end());
	Bytes role_len = {se.role, static_cast<std::uint8_t>(name_len)};
	Bytes code = se.suite->mac(user_key(), {&console, &managed, &se.rm, &se.rc, &guid_b, &role_len, &se.name});
	Bytes reply = {tag, status_ok, 0, 0};
	append(reply, console);
	append(reply, se.rc);
	append(reply, guid_b);
	append(reply, code);
	return pre_session(payload_rakp2, reply);
}

Bytes Server::rakp3(const Bytes &p, const std::string &addr)
{
	if (p.size() < 8) return {};
	std::uint8_t tag = p[0], status = p[1];
	std::uint32_t managed_id = le32(p, 4);
	auto it = sessions_.find(managed_id);
	if (it == sessions_.end() || it->second.active || it->second.addr != addr || it->second.name.empty()) {
		Bytes reply = {tag, status_invalid_session, 0, 0, 0, 0, 0, 0};
		return pre_session(payload_rakp4, reply);
	}
	Session &se = it->second;
	if (status != status_ok) {
		sessions_.erase(it);
		return {};
	}
	Bytes console = le32(se.console_id);
	Bytes role_len = {se.role, static_cast<std::uint8_t>(se.name.size())};
	Bytes want = se.suite->mac(user_key(), {&se.rc, &console, &role_len, &se.name});
	if (!equal(Bytes(p.begin() + 8, p.end()), want)) {
		sessions_.erase(it);
		record_failure(addr);
		Bytes reply = {tag, status_invalid_integrity, 0, 0};
		append(reply, console);
		return pre_session(payload_rakp4, reply);
	}
	se.sik = se.suite->mac(user_key(), {&se.rm, &se.rc, &role_len, &se.name});
	Bytes one(20, 0x01), two(20, 0x02);
	// The constants are 20 bytes also with SHA-256, as ipmitool and BMCs use them.
	se.k1 = se.suite->mac(se.sik, {&one});
	se.k2 = se.suite->mac(se.sik, {&two});
	se.active = true;
	se.priv = se.role & 0x0F;
	se.last_used = now();
	// The full address: the /64 would make every host of a network look like one already seen.
	event(Event{"login", std::string(se.name.begin(), se.name.end()), host_of(addr), 0, 0});
	Bytes managed = le32(se.managed_id), guid_b(guid.begin(), guid.end());
	Bytes icv = se.suite->mac(se.sik, {&se.rm, &managed, &guid_b});
	icv.resize(se.suite->rakp4_len);
	Bytes reply = {tag, status_ok, 0, 0};
	append(reply, console);
	append(reply, icv);
	return pre_session(payload_rakp4, reply);
}

// Rejects replays: a sequence number already seen, or more than 15 behind the highest.
bool Server::Session::accept_seq(std::uint32_t seq)
{
	if (seq == 0) return false;
	if (seq > seq_in) {
		std::uint32_t shift = seq - seq_in;
		seen = shift >= 32 ? 0 : seen << shift;
		seen |= 1;
		seq_in = seq;
		return true;
	}
	if (seq_in - seq < 16) {
		std::uint32_t bit = 1u << (seq_in - seq);
		if (seen & bit) return false;
		seen |= bit;
		return true;
	}
	return false;
}

Bytes Server::message(Session &se, const Bytes &p, std::uint32_t seq, std::size_t length)
{
	std::uint8_t ptype = p[1];
	if (!(ptype & payload_authenticated) || !(ptype & payload_encrypted)) return {};
	std::size_t n = se.suite->integ_len;
	if (p.size() < 12 + length + 2 + n) return {};
	Bytes signed_part(p.begin(), p.end() - static_cast<long>(n));
	Bytes mac = se.suite->mac(se.k1, {&signed_part});
	mac.resize(n);
	if (signed_part.back() != 0x07 || !equal(Bytes(p.end() - static_cast<long>(n), p.end()), mac)) return {};
	if (!se.accept_seq(seq)) return {};
	Bytes plain;
	if (!decrypt(se.k2, Bytes(p.begin() + 12, p.begin() + 12 + static_cast<long>(length)), plain)) return {};
	Request req;
	if (!parse_message(plain, req)) return {};
	se.last_used = now();
	Bytes msg = command(se, req);
	if (msg.empty()) return {};
	Bytes reply = sealed(se, msg);
	if (req.net_fn == net_fn_app && req.cmd == 0x3C && req.data.size() >= 4 && le32(req.data, 0) == se.managed_id)
		sessions_.erase(se.managed_id);
	return reply;
}

// The payload padded as IPMI v2.0, 13.29 requires (bytes 1, 2, 3... and the pad length), after a random IV.
bool Server::encrypt(const Bytes &key, const Bytes &plain, Bytes &out)
{
	std::size_t pad = (16 - (plain.size() + 1) % 16) % 16;
	Bytes data = plain;
	for (std::size_t i = 1; i <= pad; i++) data.push_back(static_cast<std::uint8_t>(i));
	data.push_back(static_cast<std::uint8_t>(pad));
	out.assign(16 + data.size(), 0);
	random(out.data(), 16);
	EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
	int n = 0, m = 0;
	bool ok = ctx && EVP_EncryptInit_ex(ctx, EVP_aes_128_cbc(), nullptr, key.data(), out.data()) == 1 &&
	          EVP_CIPHER_CTX_set_padding(ctx, 0) == 1 &&
	          EVP_EncryptUpdate(ctx, out.data() + 16, &n, data.data(), static_cast<int>(data.size())) == 1 &&
	          EVP_EncryptFinal_ex(ctx, out.data() + 16 + n, &m) == 1;
	EVP_CIPHER_CTX_free(ctx);
	return ok;
}

// A reply encrypted and signed in the session.
Bytes Server::sealed(Session &se, const Bytes &msg)
{
	Bytes payload;
	if (!encrypt(se.k2, msg, payload)) return {};
	se.seq_out++;
	Bytes out = {auth_type_rmcp, static_cast<std::uint8_t>(payload_ipmi | payload_encrypted | payload_authenticated)};
	append(out, le32(se.console_id));
	append(out, le32(se.seq_out));
	out.push_back(static_cast<std::uint8_t>(payload.size()));
	out.push_back(static_cast<std::uint8_t>(payload.size() >> 8));
	append(out, payload);
	// The signed part (from the authentication type to the next header) is a multiple of 4 bytes.
	std::size_t pad = (4 - (out.size() + 2) % 4) % 4;
	for (std::size_t i = 0; i < pad; i++) out.push_back(0xFF);
	out.push_back(static_cast<std::uint8_t>(pad));
	out.push_back(0x07);
	Bytes mac = se.suite->mac(se.k1, {&out});
	mac.resize(se.suite->integ_len);
	append(out, mac);
	Bytes packet = rmcp_header(class_ipmi);
	append(packet, out);
	return packet;
}

Bytes Server::command(Session &se, const Request &req)
{
	if (req.net_fn == net_fn_app && req.cmd == 0x01) // Get Device ID
		return req.reply(cc_ok, {0x20, 0x01, static_cast<std::uint8_t>(firmware_major & 0x7F), firmware_minor, 0x02, 0x80, 0, 0, 0, 0, 0});
	if (req.net_fn == net_fn_app && req.cmd == 0x38) return req.reply(cc_ok, auth_capabilities());
	if (req.net_fn == net_fn_app && req.cmd == 0x54) { // Get Channel Cipher Suites
		auto [cc, data] = cipher_suites(req.data);
		return req.reply(cc, data);
	}
	if (req.net_fn == net_fn_app && req.cmd == 0x3B) { // Set Session Privilege Level
		if (req.data.empty()) return req.reply(cc_request_too_short);
		std::uint8_t level = req.data[0] & 0x0F;
		if (level == 0) return req.reply(cc_ok, {se.priv});
		if (level > se.max_priv || level > (se.role & 0x0F)) return req.reply(cc_priv_exceeded);
		se.priv = level;
		return req.reply(cc_ok, {level});
	}
	if (req.net_fn == net_fn_app && req.cmd == 0x3C) return req.reply(cc_ok); // Close Session
	if (req.net_fn == net_fn_chassis && req.cmd == 0x01) { // Get Chassis Status
		bool on = false, known = false;
		if (!power || power->status(on, known) || !known) return req.reply(cc_not_in_this_state);
		std::uint8_t state = 0x60;  // power restore policy unknown
		if (on) state |= 0x01;
		return req.reply(cc_ok, {state, 0x00, 0x00});
	}
	if (req.net_fn == net_fn_chassis && req.cmd == 0x02) return chassis_control(se, req);
	return req.reply(cc_invalid_command);
}

Bytes Server::chassis_control(Session &se, const Request &req)
{
	if (se.priv < priv_operator) return req.reply(cc_insufficient_priv);
	if (req.data.empty()) return req.reply(cc_request_too_short);
	std::string action, name;
	switch (req.data[0] & 0x0F) {
	case 0x00: action = "ForceOff", name = "power off"; break;
	case 0x01: action = "On", name = "power on"; break;
	case 0x02: action = "PowerCycle", name = "power cycle"; break;
	case 0x03: action = "ForceRestart", name = "reset"; break;
	case 0x05: action = "GracefulShutdown", name = "soft off"; break;
	default: return req.reply(cc_invalid_field);
	}
	bool can_power = false, can_reset = false;
	if (!power || power->available(can_power, can_reset) || !can_power || ((req.data[0] & 0x0F) == 0x03 && !can_reset))
		return req.reply(cc_not_in_this_state);
	{
		std::lock_guard lock(work_->mu);
		work_->power = power;
		work_->log = log;
		if (work_->finished || !work_->actions.empty()) return req.reply(cc_node_busy);
		work_->actions.push_back(action);
		work_->cv.notify_all();
	}
	last_command_ = name;
	last_command_at_ = now();
	return req.reply(cc_ok);
}

} // namespace nkos::bmc::ipmi
