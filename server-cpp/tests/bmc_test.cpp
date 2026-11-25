// The BMC protocols' own authentication: IPMI RMCP+ (RAKP with cipher suites 3 and 17, session integrity,
// confidentiality, replay, privilege, lockout per client and IPv6 /64) and Redfish (Basic, sessions, lockout, forged
// X-Forwarded-For, cross-site writes). Ported from the Go server's internal/ipmi/server_test.go, limits_test.go and
// internal/bmc/redfish/redfish_test.go. The IPMI console is written from the IPMI v2.0 specification on raw OpenSSL
// calls; the buttons are a recording double of bmc::Atx (the interface's purpose).

#include "check.hpp"

#include "bmc/ipmi.hpp"
#include "bmc/redfish.hpp"
#include "common/log.hpp"

#include <openssl/evp.h>
#include <openssl/hmac.h>
#include <openssl/rand.h>

#include <cstring>
#include <mutex>

using namespace nkos;
using Bytes = std::vector<std::uint8_t>;

namespace {

struct Board : bmc::Atx {
	bool led = true, on = true;
	std::mutex mu;
	std::vector<std::string> presses;
	bmc::Error state(bmc::AtxState &s) override { s = {true, true, led, on, false}; return {}; }
	bmc::Error press(const std::string &button, int, const std::string &) override
	{
		std::lock_guard lock(mu);
		presses.push_back(button);
		return {};
	}
};

std::unique_ptr<bmc::Power> power_of(Board &board)
{
	auto p = std::make_unique<bmc::Power>(board, std::chrono::milliseconds(0), "test");
	p->sleep = [](std::chrono::milliseconds) {};
	return p;
}

Bytes operator+(Bytes a, const Bytes &b) { return a.insert(a.end(), b.begin(), b.end()), a; }
Bytes le32(std::uint32_t v) { return {Bytes::value_type(v), Bytes::value_type(v >> 8), Bytes::value_type(v >> 16), Bytes::value_type(v >> 24)}; }
Bytes text(const std::string &s) { return Bytes(s.begin(), s.end()); }
std::uint32_t u32(const Bytes &b, std::size_t at) { return b[at] | b[at + 1] << 8 | b[at + 2] << 16 | std::uint32_t(b[at + 3]) << 24; }
std::uint8_t checksum(const Bytes &b, std::size_t from, std::size_t to)
{
	std::uint8_t sum = 0;
	for (std::size_t i = from; i < to; i++) sum += b[i];
	return std::uint8_t(-sum);
}

struct Suite {
	std::uint8_t id, auth, integrity;
	const EVP_MD *(*md)();
	std::size_t rakp4_len, integ_len;
	Bytes mac(const Bytes &key, const Bytes &data) const
	{
		Bytes out(EVP_MAX_MD_SIZE);
		unsigned len = 0;
		HMAC(md(), key.data(), int(key.size()), data.data(), data.size(), out.data(), &len);
		return out.resize(len), out;
	}
};
const Suite suites[] = {{3, 1, 1, EVP_sha1, 12, 12}, {17, 3, 4, EVP_sha256, 16, 16}};

Bytes rmcp(std::uint8_t ptype, std::uint32_t session, std::uint32_t seq, const Bytes &payload)
{
	return Bytes{0x06, 0, 0xFF, 0x07, 0x06, ptype} + le32(session) + le32(seq) +
	       Bytes{std::uint8_t(payload.size()), std::uint8_t(payload.size() >> 8)} + payload;
}

Bytes aes_cbc(const Bytes &key, const Bytes &iv, const Bytes &in, bool enc)
{
	Bytes out(in.size() + 16);
	int n = 0, m = 0;
	EVP_CIPHER_CTX *c = EVP_CIPHER_CTX_new();
	EVP_CipherInit_ex(c, EVP_aes_128_cbc(), nullptr, key.data(), iv.data(), enc);
	EVP_CIPHER_CTX_set_padding(c, 0);
	EVP_CipherUpdate(c, out.data(), &n, in.data(), int(in.size()));
	EVP_CipherFinal_ex(c, out.data() + n, &m);
	EVP_CIPHER_CTX_free(c);
	return out.resize(std::size_t(n + m)), out;
}

constexpr std::uint8_t status_ok = 0x00, status_unauthorized_name = 0x0D, status_invalid_integrity = 0x0F;

// A minimal RMCP+ console.
struct Console {
	bmc::ipmi::Server &s;
	const Suite &su;
	std::string user, password, addr = "192.0.2.9:40000";
	std::uint32_t id = 0xA0A1A2A3, sid = 0, seq = 0;
	Bytes rm = text("console random.."), rc, k1, k2, last;

	Bytes send(std::uint8_t ptype, const Bytes &payload)
	{
		Bytes r = s.handle(rmcp(ptype, 0, 0, payload), addr);
		return r.size() > 16 ? Bytes(r.begin() + 16, r.end()) : Bytes{0, 0xFF};
	}
	std::uint8_t login()
	{
		Bytes open = Bytes{0x01, 0x04, 0, 0} + le32(id) +
		             Bytes{0, 0, 0, 8, su.auth, 0, 0, 0, 1, 0, 0, 8, su.integrity, 0, 0, 0, 2, 0, 0, 8, 0x01, 0, 0, 0};
		Bytes r = send(0x10, open);
		if (r[1] != status_ok) return r[1];
		sid = u32(r, 8);
		const std::uint8_t role = 0x14;  // administrator, name-only lookup
		Bytes who = Bytes{role, std::uint8_t(user.size())} + text(user);
		r = send(0x12, Bytes{0x02, 0, 0, 0} + le32(sid) + rm + Bytes{role, 0, 0, std::uint8_t(user.size())} + text(user));
		if (r[1] != status_ok) return r[1];
		rc.assign(r.begin() + 8, r.begin() + 24);
		Bytes guid(r.begin() + 24, r.begin() + 40), key = text(password);
		key.resize(20);
		// A wrong password shows here (ipmitool gives up at this point).
		if (Bytes(r.begin() + 40, r.end()) != su.mac(key, le32(id) + le32(sid) + rm + rc + guid + who)) return status_invalid_integrity;
		r = send(0x14, Bytes{0x03, 0, 0, 0} + le32(sid) + su.mac(key, rc + le32(id) + who));
		if (r[1] != status_ok) return r[1];
		Bytes sik = su.mac(key, rm + rc + who);
		Bytes check = su.mac(sik, rm + le32(sid) + guid);
		check.resize(su.rakp4_len);
		CHECK(Bytes(r.begin() + 8, r.end()) == check, "RAKP 4 check value");
		k1 = su.mac(sik, Bytes(20, 1));
		k2 = su.mac(sik, Bytes(20, 2));
		return status_ok;
	}
	// The completion code and data of a command in the session; 0xFF when nothing answers.
	std::pair<std::uint8_t, Bytes> command(std::uint8_t net_fn, std::uint8_t cmd, const Bytes &data = {})
	{
		Bytes msg{0x20, std::uint8_t(net_fn << 2), 0};
		msg[2] = checksum(msg, 0, 2);
		msg = msg + Bytes{0x81, 0x04, cmd} + data;
		msg.push_back(checksum(msg, 3, msg.size()));
		std::size_t pad = (16 - (msg.size() + 1) % 16) % 16;
		Bytes plain = msg;
		for (std::size_t i = 1; i <= pad; i++) plain.push_back(std::uint8_t(i));
		plain.push_back(std::uint8_t(pad));
		Bytes iv(16), key16(k2.begin(), k2.begin() + 16);
		RAND_bytes(iv.data(), 16);
		Bytes pkt = rmcp(0xC0, sid, ++seq, iv + aes_cbc(key16, iv, plain, true));
		pkt.erase(pkt.begin(), pkt.begin() + 4);
		std::size_t integrity_pad = (4 - (pkt.size() + 2) % 4) % 4;
		pkt.insert(pkt.end(), integrity_pad, 0xFF);
		pkt = pkt + Bytes{std::uint8_t(integrity_pad), 0x07};
		Bytes icv = su.mac(k1, pkt);
		pkt = pkt + Bytes(icv.begin(), icv.begin() + long(su.integ_len));
		last = Bytes{0x06, 0, 0xFF, 0x07} + pkt;
		Bytes reply = s.handle(last, addr);
		if (reply.size() < 4 + 12 + su.integ_len) return {0xFF, {}};
		Bytes p(reply.begin() + 4, reply.end());
		Bytes body(p.begin(), p.end() - long(su.integ_len)), mac = su.mac(k1, body);
		CHECK(Bytes(p.end() - long(su.integ_len), p.end()) == Bytes(mac.begin(), mac.begin() + long(su.integ_len)), "reply integrity");
		CHECK(u32(p, 2) == id, "reply to this session");
		std::size_t length = p[10] | p[11] << 8;
		Bytes enc(p.begin() + 12, p.begin() + 12 + long(length));
		Bytes out = aes_cbc(key16, Bytes(enc.begin(), enc.begin() + 16), Bytes(enc.begin() + 16, enc.end()), false);
		out.resize(out.size() - 1 - out.back());
		CHECK(checksum(out, 0, 2) == out[2] && checksum(out, 3, out.size() - 1) == out.back(), "reply checksums");
		return {out[6], Bytes(out.begin() + 7, out.end() - 1)};
	}
};

void ipmi_tests()
{
	auto server = [](Board &board) {
		auto s = std::make_unique<bmc::ipmi::Server>();
		s->username = "admin", s->password = "correct-horse";
		std::memcpy(s->guid.data(), "0123456789abcdef", 16);
		s->power = power_of(board);
		return s;
	};
	for (const Suite &su : suites) {
		Board board;
		auto s = server(board);
		Console c{*s, su, "admin", "correct-horse"};
		std::string name = "suite " + std::to_string(su.id);
		CHECK(c.login() == status_ok, name + " login");
		CHECK(c.command(0x06, 0x3B, {0x04}).first == 0x00, name + " set privilege");
		auto [cc, data] = c.command(0x00, 0x01);
		CHECK(cc == 0x00 && !data.empty() && (data[0] & 1), name + " chassis status");
		CHECK(c.command(0x00, 0x02, {0x00}).first == 0x00, name + " power down");
		s->drain();
		CHECK(board.presses == std::vector<std::string>{"power"}, name + " pressed");
		CHECK(s->handle(c.last, c.addr).empty(), name + " a replayed packet is dropped");
		CHECK(c.command(0x06, 0x3C, le32(c.sid)).first == 0x00, name + " close session");
		CHECK(c.command(0x00, 0x01).first == 0xFF, name + " a closed session still answers");
	}
	Board board;
	auto s = server(board);
	CHECK(Console(*s, suites[1], "admin", "wrong-password").login() == status_invalid_integrity, "wrong password");
	CHECK(Console(*s, suites[1], "nobody", "correct-horse").login() == status_unauthorized_name, "unknown user");
	Console user{*s, suites[0], "admin", "correct-horse"};
	CHECK(user.login() == status_ok && user.command(0x06, 0x3B, {0x02}).first == 0x00, "lower privilege");
	CHECK(user.command(0x00, 0x02, {0x01}).first == 0xD4, "user-level chassis control");
	s->drain();
	CHECK(board.presses.empty(), "a refused command pressed a button");

	// Only the suites with authentication, integrity and confidentiality are offered (Get Channel Cipher Suites).
	Bytes msg{0x20, 0x06 << 2, 0};
	msg[2] = checksum(msg, 0, 2);
	msg = msg + Bytes{0x81, 0x00, 0x54, 0x0E, 0x00, 0x80};
	msg.push_back(checksum(msg, 3, msg.size()));
	Bytes reply = s->handle(Bytes{0x06, 0, 0xFF, 0x07, 0, 0, 0, 0, 0, 0, 0, 0, 0, std::uint8_t(msg.size())} + msg, "192.0.2.9:40000");
	CHECK(reply.size() > 23 && Bytes(reply.begin() + 22, reply.end() - 1) == Bytes({0xC0, 3, 1, 0x41, 0x81, 0xC0, 17, 3, 0x44, 0x81}), "cipher suites");

	// Failed logins lock out the client, an IPv6 client by its /64, for a minute (RAKP 1 hands out a password hash).
	for (int i = 0; i < 20; i++) Console(*s, suites[0], "admin", "guess").login();
	CHECK(Console(*s, suites[0], "admin", "correct-horse").login() == status_unauthorized_name, "locked out client logged in");
	Board b6;
	auto s6 = server(b6);
	std::int64_t now = 1800000000LL * 1000000000;
	s6->now = [&] { return now; };
	auto rakp1 = [&](const std::string &addr) {
		Console c{*s6, suites[0], "admin", "", addr};
		Bytes r = c.send(0x10, Bytes{1, 4, 0, 0} + le32(1234) + Bytes{0, 0, 0, 8, 1, 0, 0, 0, 1, 0, 0, 8, 1, 0, 0, 0, 2, 0, 0, 8, 1, 0, 0, 0});
		if (r[1] != status_ok) return r[1];
		return c.send(0x12, Bytes{2, 0, 0, 0} + le32(u32(r, 8)) + Bytes(16) + Bytes{0x14, 0, 0, 5} + text("admin"))[1];
	};
	for (int i = 0; i < 20; i++) CHECK(rakp1("[2001:db8:1:2::" + std::to_string(i + 1) + "]:40000") == status_ok, "attempt " + std::to_string(i));
	CHECK(rakp1("[2001:db8:1:2::ffff]:40000") == status_unauthorized_name, "same /64 escaped the lockout");
	CHECK(rakp1("[2001:db8:1:3::1]:40000") == status_ok, "another /64 was blocked");
	now += 60LL * 1000000000 + 1;
	CHECK(rakp1("[2001:db8:1:2::ffff]:40000") == status_ok, "the lockout did not expire");
}

std::string basic(const std::string &credentials)
{
	std::string out(4 * ((credentials.size() + 2) / 3) + 1, '\0');
	out.resize(EVP_EncodeBlock(reinterpret_cast<unsigned char *>(out.data()), reinterpret_cast<const unsigned char *>(credentials.data()), int(credentials.size())));
	return "Basic " + out;
}

std::string header(const bmc::redfish::Response &w, std::string name)
{
	for (auto &[k, v] : w.headers)
		if (strcasecmp(k.c_str(), name.c_str()) == 0) return v;
	return "";
}

void redfish_tests()
{
	using bmc::redfish::Handler;
	const std::string admin = basic("admin:correct-horse"), system = "/redfish/v1/Systems/1",
	                  sessions = "/redfish/v1/SessionService/Sessions", login = R"({"UserName":"admin","Password":"correct-horse"})";
	auto serve = [](Handler &h, std::string method, std::string path, std::map<std::string, std::string> headers = {}, std::string body = "") {
		if (!body.empty()) headers.emplace("content-type", "application/json");
		return h.serve({std::move(method), std::move(path), std::move(body), "192.0.2.7", std::move(headers)});
	};
	Board board;
	Handler h("admin", "correct-horse", {}, power_of(board));
	CHECK(serve(h, "GET", "/redfish/v1/").status == 200, "the service root is open");
	CHECK(serve(h, "GET", system).status == 401, "unauthenticated system");
	CHECK(serve(h, "GET", system, {{"authorization", basic("admin:wrong")}}).status == 401, "wrong password");
	CHECK(serve(h, "GET", system, {{"authorization", admin}}).status == 200, "Basic");
	auto w = serve(h, "POST", sessions, {}, login);
	std::string token = header(w, "X-Auth-Token"), location = header(w, "Location");
	CHECK(w.status == 201 && !token.empty() && location.starts_with(sessions + "/"), "session login");
	CHECK(serve(h, "GET", "/redfish/v1/Managers/1", {{"x-auth-token", token}}).status == 200, "with the token");
	CHECK(serve(h, "GET", "/redfish/v1/Managers/1", {{"x-auth-token", token + "x"}}).status == 401, "another token");
	CHECK(serve(h, "DELETE", location, {{"x-auth-token", token}}).status == 204, "logout");
	CHECK(serve(h, "GET", "/redfish/v1/Managers/1", {{"x-auth-token", token}}).status == 401, "after logout");

	// A cross-site form cannot drive power actions with cached Basic credentials.
	const std::string reset = system + "/Actions/ComputerSystem.Reset", off = R"({"ResetType":"ForceOff"})";
	auto form = [&](std::map<std::string, std::string> headers) {
		headers.emplace("authorization", admin);
		return h.serve({"POST", reset, off, "192.0.2.7", std::move(headers)}).status;
	};
	CHECK(form({{"content-type", "text/plain"}}) == 415, "text/plain POST");
	CHECK(form({{"content-type", "application/json"}, {"sec-fetch-site", "cross-site"}}) == 403, "cross-site POST");
	CHECK(board.presses.empty(), "refused requests pressed buttons");
	CHECK(form({{"content-type", "application/json; charset=utf-8"}}) == 204 && board.presses.size() == 1, "JSON POST");

	// Five failures lock the client out for a minute, whatever X-Forwarded-For says; it is heard of once.
	Handler l("admin", "correct-horse", {}, power_of(board));
	std::vector<std::string> heard;
	l.on_lockout = [&](const std::string &addr, int, std::chrono::seconds) { heard.push_back(addr); };
	l.on_login = [&](const std::string &user, const std::string &addr) { heard.push_back(user + "@" + addr); };
	serve(l, "GET", system, {{"authorization", admin}});
	serve(l, "GET", system, {{"authorization", admin}});
	for (int i = 0; i < 5; i++)
		serve(l, "GET", system, {{"authorization", basic("admin:guess")}, {"x-forwarded-for", "203.0.113." + std::to_string(i)}});
	CHECK((heard == std::vector<std::string>{"admin@192.0.2.7", "192.0.2.7"}), "heard");
	CHECK(serve(l, "GET", system, {{"authorization", admin}, {"x-forwarded-for", "203.0.113.99"}}).status == 429, "locked out client");
	CHECK(serve(l, "POST", sessions, {}, login).status == 429, "locked out session login");
}

} // namespace

int main()
{
	log::set_level("error");
	ipmi_tests();
	redfish_tests();
	return finish("bmc");
}
