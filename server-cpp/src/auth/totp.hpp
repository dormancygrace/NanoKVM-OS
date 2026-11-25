#pragma once

// Two-factor authentication, as the Go server's authn/totp.go and store_totp.go: RFC 6238 codes (SHA-1, 6 digits,
// 30 s, one step of skew, a step never accepted twice), ten single-use recovery codes kept as SHA-256 hashes.

#include "auth/store.hpp"

#include <ctime>
#include <optional>
#include <string>
#include <vector>

namespace nkos::auth::totp {

inline constexpr const char *not_enabled = "two-factor authentication is not enabled";
inline constexpr const char *already_enabled = "two-factor authentication is already enabled";
inline constexpr const char *invalid_code = "invalid authentication code";
inline constexpr const char *account_changed = "the account changed; sign in again";

std::optional<std::string> new_secret();
std::string uri(const std::string &issuer, const std::string &account, const std::string &secret);
// The step of code for secret at now, after last_step; 0 when it does not match.
std::int64_t match(const std::string &secret, const std::string &code, std::time_t now, std::int64_t last_step);
// Recovery codes as shown ("xxxx-xxxx-xxxx") and their hashes; false on a random source failure.
bool new_recovery_codes(std::vector<std::string> &codes, std::vector<std::string> &hashes);

// Store operations; error carries one of the messages above or the store's.
bool enable(const std::string &username, const std::string &secret, std::int64_t step,
            const std::vector<std::string> &hashes, std::string &error);
// was: whether it was on.
bool disable(const std::string &username, bool &was, std::string &error);
bool replace_recovery_codes(const std::string &username, const std::vector<std::string> &hashes, std::string &error);
// The account after a valid code (the step is recorded) or recovery code (used up).
std::optional<User> verify(const std::string &username, std::uint64_t token_version, const std::string &code,
                           std::time_t now, bool &recovery, int &left, std::string &error);

} // namespace nkos::auth::totp
