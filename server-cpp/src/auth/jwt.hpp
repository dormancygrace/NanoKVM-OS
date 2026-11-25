#pragma once

// Session tokens: HS256 JWTs with the claims of the Go server's middleware.Token (username, tokenVersion and the
// registered sub, iat, exp, jti), signed with the configured secret. Own HMAC-SHA256 on OpenSSL, no JWT library.

#include <cstdint>
#include <optional>
#include <string>

namespace nkos::auth {

struct Token {
	std::string username;
	std::uint64_t token_version = 0;
	std::string id;           // jti
	std::int64_t issued = 0;  // iat, seconds; 0 when absent
	std::int64_t expires = 0; // exp, seconds
};

// A new token for username, valid for the configured refresh duration.
std::string generate(const std::string &username, std::uint64_t token_version);

// The claims of a valid token: HS256 with the secret, exp present and not passed, nbf (if any) passed, and
// sub equal to a non-empty username with a non-zero tokenVersion. nullopt otherwise.
std::optional<Token> parse(const std::string &jwt);

// The session key of a token (idle clocks, recent authentication), as sessionKeyOf.
std::string session_key(const Token &token);

std::string base64url(const unsigned char *data, size_t len);
std::optional<std::string> base64url_decode(std::string_view s);

} // namespace nkos::auth
