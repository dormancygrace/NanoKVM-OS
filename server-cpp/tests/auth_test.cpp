// Authentication and authorization of the web API: session tokens, TOTP, password hashes, the account store, the
// login lockout and the request checks behind proxies. Ported from the Go server's middleware/jwt_test.go,
// client_address_test.go, session_cookie_test.go, authn/totp_test.go, authn/store_test.go,
// service/auth/brute_force_test.go and credential_format_test.go. Known answers: RFC 6238 appendix B; the Argon2id and
// bcrypt hashes were made by golang.org/x/crypto (argon2.IDKey, bcrypt.GenerateFromPassword).

#include "check.hpp"

#include "auth/brute_force.hpp"
#include "auth/jwt.hpp"
#include "auth/password.hpp"
#include "auth/session.hpp"
#include "auth/store.hpp"
#include "auth/totp.hpp"
#include "common/log.hpp"
#include "config/config.hpp"

#include <openssl/evp.h>
#include <openssl/hmac.h>

#include <cstdlib>
#include <ctime>
#include <filesystem>
#include <fstream>
#include <sys/stat.h>

using namespace nkos;

// brute_force reports a lockout to the notification service, which is not linked here.
namespace nkos::notify {
void login_locked_out(const std::string &, int, int) {}
}

namespace {

config::Config &conf() { return const_cast<config::Config &>(config::get()); }

std::string b64url(const std::string &s)
{
	std::string out(4 * ((s.size() + 2) / 3) + 1, '\0');
	out.resize(EVP_EncodeBlock(reinterpret_cast<unsigned char *>(out.data()), reinterpret_cast<const unsigned char *>(s.data()), static_cast<int>(s.size())));
	while (!out.empty() && out.back() == '=') out.pop_back();
	for (char &c : out) c = c == '+' ? '-' : c == '/' ? '_' : c;
	return out;
}

// A token signed with md and the configured secret.
std::string token(const std::string &header, const std::string &payload, const EVP_MD *md = EVP_sha256(),
                  const std::string &secret = conf().jwt.secret_key)
{
	std::string signing = b64url(header) + "." + b64url(payload);
	unsigned char mac[64];
	unsigned len = 0;
	HMAC(md, secret.data(), static_cast<int>(secret.size()), reinterpret_cast<const unsigned char *>(signing.data()), signing.size(), mac, &len);
	return signing + "." + b64url(std::string(reinterpret_cast<char *>(mac), len));
}

void jwt_tests()
{
	const std::string hs256 = R"({"alg":"HS256","typ":"JWT"})";
	auto claims = [](const std::string &user, const std::string &sub, int version, long exp, const std::string &extra = "") {
		return "{\"username\":\"" + user + "\",\"tokenVersion\":" + std::to_string(version) + ",\"sub\":\"" + sub +
		       "\",\"exp\":" + std::to_string(std::time(nullptr) + exp) + extra + "}";
	};
	auto own = auth::parse(auth::generate("admin", 7));
	CHECK(own && own->username == "admin" && own->token_version == 7);
	CHECK(auth::parse(token(hs256, claims("admin", "admin", 1, 3600))), "control: a well-formed token");
	struct Case {
		const char *name;
		std::string jwt;
	} rejected[] = {
	    {"HS384", token(R"({"alg":"HS384","typ":"JWT"})", claims("admin", "admin", 1, 3600), EVP_sha384())},
	    {"HS384 header, HS256 MAC", token(R"({"alg":"HS384","typ":"JWT"})", claims("admin", "admin", 1, 3600))},
	    {"alg none", b64url(R"({"alg":"none"})") + "." + b64url(claims("admin", "admin", 1, 3600)) + "."},
	    {"expired", token(hs256, claims("admin", "admin", 1, -60))},
	    {"no exp", token(hs256, R"({"username":"admin","tokenVersion":1,"sub":"admin"})")},
	    {"not yet valid", token(hs256, claims("admin", "admin", 1, 3600, ",\"nbf\":" + std::to_string(std::time(nullptr) + 600)))},
	    {"sub differs", token(hs256, claims("admin", "alice", 1, 3600))},
	    {"empty username", token(hs256, claims("", "", 1, 3600))},
	    {"token version 0", token(hs256, claims("admin", "admin", 0, 3600))},
	    {"other secret", token(hs256, claims("admin", "admin", 1, 3600), EVP_sha256(), "other-secret")},
	    {"payload swapped", [&] {
		     std::string t = token(hs256, claims("alice", "alice", 1, 3600));
		     auto a = t.find('.'), b = t.rfind('.');
		     return t.substr(0, a + 1) + b64url(claims("admin", "admin", 1, 3600)) + t.substr(b);
	     }()},
	    {"four parts", token(hs256, claims("admin", "admin", 1, 3600)) + ".x"},
	};
	for (auto &c : rejected) CHECK(!auth::parse(c.jwt), c.name);
}

void totp_tests()
{
	// RFC 6238 appendix B, SHA-1, the last six of the eight digits.
	const std::string secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
	struct {
		std::time_t t;
		const char *code;
	} rfc[] = {{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}};
	for (auto &v : rfc) CHECK(auth::totp::match(secret, v.code, v.t, 0) == v.t / 30, v.code);
	// 1111111109 and 1111111111 are adjacent steps: one step of skew either way, not two; a used step never again.
	CHECK(auth::totp::match(secret, "081804", 1111111111, 0) == 1111111109 / 30);
	CHECK(auth::totp::match(secret, "050471", 1111111109, 0) == 1111111111 / 30);
	CHECK(auth::totp::match(secret, "081804", 1111111111 + 60, 0) == 0);
	CHECK(auth::totp::match(secret, "050471", 1111111109 - 60, 0) == 0);
	CHECK(auth::totp::match(secret, "050471", 1111111111, 1111111111 / 30) == 0);
	CHECK(auth::totp::match(secret, " 050 471", 1111111111, 0) == 1111111111 / 30);
	for (const char *bad : {"", "05047", "0504711", "abcdef"}) CHECK(auth::totp::match(secret, bad, 1111111111, 0) == 0, bad);
}

void password_tests()
{
	const std::string argon = "$argon2id$v=19$m=19456,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$rk2Mi3E4dgRMg0fHaYaptWVZRKqs//6b6k3/nntqGJk";
	const std::string bcrypt = "$2a$04$.v.Q4NP88dVUhJbZBnRcme5oboDXaw1i.kUQadi4pZtkTQoa73dYW";
	CHECK(auth::verify_password(argon, "correct horse") && !auth::verify_password(argon, "correct horsf"));
	CHECK(auth::verify_password(bcrypt, "correct horse") && !auth::verify_password(bcrypt, "correct horsf"));
	CHECK(auth::hash_is_current(argon) && !auth::hash_is_current(bcrypt));
	for (const char *bad : {"", "correct horse", "$argon2id$v=19$m=19456,t=2,p=1$$", "$argon2i$v=19$m=19456,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$rk2Mi3E4dgRMg0fHaYaptWVZRKqs//6b6k3/nntqGJk"})
		CHECK(!auth::verify_password(bad, "correct horse"), bad);
	auto fresh = auth::hash_password("new password");
	CHECK(fresh && auth::verify_password(*fresh, "new password") && !auth::verify_password(*fresh, "new passwore"));
	// Passwords travel as entered: any other encoding is refused, not guessed at.
	CHECK(auth::decode_credential("p%41ss", "") == "p%41ss" && auth::decode_credential("p%41ss", "plain") == "p%41ss");
	for (const char *enc : {"aes", "base64", "PLAIN", "cryptojs"}) CHECK(!auth::decode_credential("x", enc), enc);
}

void store_tests(const std::string &dir)
{
	std::string e;
	{
		std::ofstream(dir + "/corrupt") << "not json";
		auth::Store s(dir + "/corrupt");
		auto u = s.authenticate("admin", "admin", e);
		CHECK(!u && !e.empty(), "a corrupt account file fails closed");
	}
	auth::Store s(dir + "/pwd");
	auto owner = s.authenticate("admin", "admin", e);
	CHECK(owner && owner->system_account, "default login: " + e);
	struct stat st{};
	CHECK(::stat((dir + "/pwd").c_str(), &st) == 0 && (st.st_mode & 0777) == 0600, "account file mode");

	CHECK(s.create("alice", "correct-horse", "user", e), e);
	e.clear();
	CHECK(!s.authenticate("alice", "wrong-password", e) && e.empty(), "a wrong password is a refusal, not an error");
	auto alice = s.get("alice", e);
	CHECK(alice && s.validate_token("alice", alice->token_version, e));
	CHECK(s.set_password("alice", "new-password", {}, {}, e), e);
	CHECK(!s.validate_token("alice", alice->token_version, e), "old token after a password change");
	CHECK(s.authenticate("alice", "new-password", e));
	bool changed = false;
	CHECK(s.update("admin", "alice", {.enabled = false}, changed, e));
	CHECK(!s.authenticate("alice", "new-password", e), "disabled account");
	CHECK(s.remove("admin", "alice", e) && !s.get("alice", e) && e == auth::errors::user_not_found);

	// The last admin and the device owner.
	CHECK(!s.update("admin", "admin", {.role = "user"}, changed, e) && e == auth::errors::self_modification);
	CHECK(!s.remove("admin", "admin", e) && e == auth::errors::self_delete);
	CHECK(s.create("second", "second-password", "admin", e), e);
	CHECK(!s.update("second", "admin", {.enabled = false}, changed, e) && e == auth::errors::system_account);
	CHECK(!s.update("second", "admin", {.role = "user"}, changed, e) && e == auth::errors::system_account);
	CHECK(!s.remove("second", "admin", e) && e == auth::errors::system_account);
	auto second = s.get("second", e);
	CHECK(!s.update("second", "admin", {.username = "owner"}, changed, e) && e == auth::errors::system_account_rename);
	CHECK(s.validate_token("admin", owner->token_version, e) && s.validate_token("second", second->token_version, e),
	      "a refused rename changes no session");

	// The owner's rename revokes its sessions and keeps it the owner.
	auto renamed = s.update("admin", "admin", {.username = "owner"}, changed, e);
	CHECK(renamed && changed && renamed->system_account && renamed->token_version != owner->token_version, e);
	CHECK(!s.validate_token("admin", owner->token_version, e) && !s.validate_token("owner", owner->token_version, e));
	CHECK(s.authenticate("owner", "admin", e));
	CHECK(!s.update("owner", "owner", {.role = "user"}, changed, e) && e == auth::errors::self_modification);
	CHECK(!s.update("second", "second", {.enabled = false}, changed, e) && e == auth::errors::self_modification);
	CHECK(s.remove("owner", "second", e), e);
}

void lockout_tests()
{
	// Defaults: 5 failures, 300 s lockout.
	auto locked = [](const std::string &key) { auto r = auth::check_login_attempt(key); return r && r->code == -5; };
	auto fail = [](const std::string &key, const std::string &user, int n) {
		for (int i = 0; i < n; i++) auth::record_login_failure(key, user);
	};
	const std::string ip = "192.0.2.10";
	for (int i = 0; i < 5; i++) {
		CHECK(!locked(ip), "before the threshold");
		auth::record_login_failure(ip, "");
	}
	CHECK(locked(ip), "the threshold locks the client");
	// A login clears only failures that were all its own account's.
	fail("192.0.2.20", "admin", 2), auth::clear_login_attempt("192.0.2.20", "viewer"), fail("192.0.2.20", "admin", 3);
	CHECK(locked("192.0.2.20"), "another account's login cleared the failures at admin");
	fail("192.0.2.21", "admin", 2), auth::clear_login_attempt("192.0.2.21", "admin"), fail("192.0.2.21", "admin", 4);
	CHECK(!locked("192.0.2.21"), "admin's own login did not clear its failures");
	fail("192.0.2.22", "viewer", 1), fail("192.0.2.22", "", 1), auth::clear_login_attempt("192.0.2.22", "viewer");
	fail("192.0.2.22", "viewer", 3);
	CHECK(locked("192.0.2.22"), "mixed failures were cleared");
	// An IPv6 client is its /64.
	CHECK(auth::login_key("2001:db8:1:2::5") == "2001:db8:1:2::/64" && auth::login_key("2001:db8:1:2:ffff::1") == "2001:db8:1:2::/64");
	CHECK(auth::login_key("192.0.2.1") == "192.0.2.1" && auth::login_key("::ffff:192.0.2.1") == "::ffff:192.0.2.1");
	// A table full of active lockouts refuses new clients and keeps every lockout (unlocked records are evicted).
	for (int i = 0; i < 3000; i++) fail("locked-" + std::to_string(i), "", 5);
	auto refused = auth::check_login_attempt("192.0.2.200");
	CHECK(refused && refused->code == -5, "a new client at saturation");
	CHECK(auth::record_login_failure("192.0.2.201", ""), "a new failure at saturation");
	CHECK(locked(ip) && locked("192.0.2.20") && locked("locked-0"), "lockouts kept at saturation");
}

http::Request request(std::string peer, std::vector<std::pair<std::string, std::string>> headers, bool tls = false)
{
	http::Request r;
	r.peer = std::move(peer);
	r.header_copy = std::move(headers);
	r.tls = tls;
	return r;
}

void request_tests()
{
	auto &sec = conf().security;
	sec.trusted_proxies = {"127.0.0.1/32", "::1/128", "10.0.0.5"};
	struct {
		const char *name, *peer;
		std::vector<std::string> forwarded;
		const char *want;
	} addresses[] = {
	    {"direct peer, forwarded ignored", "192.0.2.7", {"198.51.100.1"}, "192.0.2.7"},
	    {"proxy, its client", "127.0.0.1", {"100.64.0.9"}, "100.64.0.9"},
	    {"client-supplied entries to the left are skipped", "127.0.0.1", {"6.6.6.6, 100.64.0.9"}, "100.64.0.9"},
	    {"chained trusted proxies", "127.0.0.1", {"100.64.0.9", "10.0.0.5"}, "100.64.0.9"},
	    {"IPv6 client", "::1", {"2001:db8::7"}, "2001:db8::7"},
	    {"mapped IPv4", "127.0.0.1", {"::ffff:100.64.0.9"}, "100.64.0.9"},
	    {"malformed entry: the proxy", "127.0.0.1", {"100.64.0.9, nonsense"}, "127.0.0.1"},
	    {"no header: the proxy", "127.0.0.1", {}, "127.0.0.1"},
	};
	for (auto &c : addresses) {
		std::vector<std::pair<std::string, std::string>> h;
		for (auto &f : c.forwarded) h.emplace_back("X-Forwarded-For", f);
		CHECK(auth::client_address(request(c.peer, h)) == c.want, c.name);
	}

	conf().proto = "http";
	sec.trusted_proxies.clear();
	sec.allowed_origins.clear();
	struct {
		const char *origin, *host;
		bool want;
	} origins[] = {{"", "kvm.local", true}, {"http://kvm.local", "kvm.local", true}, {"http://KVM.local", "kvm.local", true},
	               {"http://kvm.local:8080", "kvm.local:8080", true}, {"http://kvm.local:8081", "kvm.local:8080", false},
	               {"https://kvm.local:80", "kvm.local:80", false}, {"http://evil.example", "kvm.local", false}};
	for (auto &c : origins) {
		std::vector<std::pair<std::string, std::string>> h{{"Host", c.host}};
		if (*c.origin) h.emplace_back("Origin", c.origin);
		CHECK(auth::check_websocket_origin(request("192.0.2.1", h)) == c.want, std::string(c.origin) + " on " + c.host);
	}
	sec.trusted_proxies = {"10.0.0.0/8"};
	std::vector<std::pair<std::string, std::string>> proxied{{"Host", "upstream:8080"}, {"Origin", "https://kvm.example"},
	                                                         {"X-Forwarded-Host", "kvm.example"}, {"X-Forwarded-Proto", "https"}};
	CHECK(auth::check_websocket_origin(request("10.1.2.3", proxied)), "trusted proxy");
	CHECK(!auth::check_websocket_origin(request("192.0.2.10", proxied)), "untrusted peer spoofing forwarded headers");
	sec.trusted_proxies = {"::1/128"};
	proxied = {{"Host", "upstream:8080"}, {"Origin", "https://[2001:db8::1]:8443"}, {"X-Forwarded-Host", "[2001:db8::1]:8443"}, {"X-Forwarded-Proto", "https"}};
	CHECK(auth::check_websocket_origin(request("::1", proxied)), "trusted IPv6 proxy, non-default port");
	proxied[2].second = "[2001:db8::1]:8443, attacker.example", proxied[3].second = "https, http";
	CHECK(!auth::check_websocket_origin(request("::1", proxied)), "several forwarded values");
	sec.trusted_proxies.clear();
	sec.allowed_origins = {"https://kvm.example:8443"};
	auto from = [](const char *origin) { return auth::check_websocket_origin(request("192.0.2.1", {{"Host", "upstream:8080"}, {"Origin", origin}})); };
	CHECK(from("https://kvm.example:8443") && !from("https://kvm.example") && !from("https://evil.example"), "allowed origins");
	sec.allowed_origins.clear();

	// The session cookie of a request: HTTPS keeps the original name; only a trusted proxy may say which scheme.
	sec.trusted_proxies = {"127.0.0.1/32"};
	const std::string http_name = auth::http_cookie_name, https_name = auth::cookie_name;
	struct {
		const char *name, *proto, *peer, *forwarded;
		bool tls;
		const std::string &want;
	} cookies[] = {
	    {"HTTP", "http", "192.0.2.1", "", false, http_name},
	    {"configured HTTPS", "https", "192.0.2.1", "", false, https_name},
	    {"actual TLS", "http", "192.0.2.1", "", true, https_name},
	    {"trusted TLS termination", "http", "127.0.0.1", "https", false, https_name},
	    {"untrusted peer cannot select HTTPS", "http", "192.0.2.1", "https", false, http_name},
	    {"trusted HTTP forwarding overrides configured HTTPS", "https", "127.0.0.1", "http", false, http_name},
	};
	for (auto &c : cookies) {
		conf().proto = c.proto;
		std::vector<std::pair<std::string, std::string>> h;
		if (*c.forwarded) h.emplace_back("X-Forwarded-Proto", c.forwarded);
		CHECK(auth::session_cookie_name(request(c.peer, h, c.tls)) == c.want, c.name);
	}
}

} // namespace

int main()
{
	std::string dir = std::filesystem::temp_directory_path() / "nkos-auth-XXXXXX";
	if (!::mkdtemp(dir.data())) return 2;
	log::set_level("error");
	jwt_tests();
	totp_tests();
	password_tests();
	store_tests(dir);
	lockout_tests();
	request_tests();
	std::filesystem::remove_all(dir);
	return finish("auth");
}
