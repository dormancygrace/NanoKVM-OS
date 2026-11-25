#pragma once

// Login rate limiting, as the Go server's service/auth/brute_force.go: per client (an IPv6 /64 counts as one), a
// lockout after security.loginMaxFailures failures within security.loginLockoutDuration, one login at a time per
// client (a second one waits up to 10 s), at most 3000 records. Loop thread only.

#include <functional>
#include <optional>
#include <string>

namespace nkos::auth {

struct Refusal {
	int code;
	std::string message;
};

// The client's key: its address, or the /64 prefix of an IPv6 address.
std::string login_key(const std::string &address);

// A locked or saturated client is refused.
std::optional<Refusal> check_login_attempt(const std::string &key);

// Admits one login of key at a time: done(nullopt) once admitted (call release_login(key) at the end), or
// done(refusal) after waiting 10 s or when locked.
void begin_login_attempt(const std::string &key, std::function<void(std::optional<Refusal>)> done);
void release_login(const std::string &key);

// A failed login; a refusal when the record table is full of active lockouts.
std::optional<Refusal> record_login_failure(const std::string &key, const std::string &username);
// A successful login of username clears the record, unless other accounts failed from that client.
void clear_login_attempt(const std::string &key, const std::string &username);

} // namespace nkos::auth
