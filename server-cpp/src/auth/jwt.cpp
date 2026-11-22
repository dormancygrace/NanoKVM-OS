#include "auth/jwt.hpp"

#include "config/config.hpp"

#include <glaze/glaze.hpp>
#include <openssl/crypto.h>
#include <openssl/hmac.h>
#include <openssl/rand.h>

#include <cmath>
#include <ctime>

namespace nkos::auth {

namespace {

struct Header {
	std::string alg;
	std::optional<std::string> typ;
};

struct Claims {
	std::optional<std::string> username;
	std::optional<std::uint64_t> tokenVersion;
	std::optional<std::string> sub;
	std::optional<double> exp;
	std::optional<double> nbf;
	std::optional<double> iat;
	std::optional<std::string> jti;
};

constexpr glz::opts read_opts{.error_on_unknown_keys = false};

std::string hmac(const std::string &key, std::string_view data)
{
	unsigned char out[32];
	unsigned len = 0;
	HMAC(EVP_sha256(), key.data(), static_cast<int>(key.size()), reinterpret_cast<const unsigned char *>(data.data()),
	     data.size(), out, &len);
	return base64url(out, len);
}

std::string json_string(std::string_view s)
{
	std::string out;
	(void)glz::write_json(std::string(s), out);
	return out;
}

} // namespace

std::string base64url(const unsigned char *data, size_t len)
{
	static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
	std::string out;
	for (size_t i = 0; i < len; i += 3) {
		unsigned v = data[i] << 16 | (i + 1 < len ? data[i + 1] << 8 : 0) | (i + 2 < len ? data[i + 2] : 0);
		out += alphabet[v >> 18 & 63];
		out += alphabet[v >> 12 & 63];
		if (i + 1 < len) out += alphabet[v >> 6 & 63];
		if (i + 2 < len) out += alphabet[v & 63];
	}
	return out;
}

std::optional<std::string> base64url_decode(std::string_view s)
{
	auto value = [](char c) -> int {
		if (c >= 'A' && c <= 'Z') return c - 'A';
		if (c >= 'a' && c <= 'z') return c - 'a' + 26;
		if (c >= '0' && c <= '9') return c - '0' + 52;
		if (c == '-') return 62;
		if (c == '_') return 63;
		return -1;
	};
	// RawURLEncoding: no padding; a length of 1 modulo 4 is never valid.
	if (s.size() % 4 == 1) return std::nullopt;
	std::string out;
	unsigned acc = 0;
	int bits = 0;
	for (char c : s) {
		int v = value(c);
		if (v < 0) return std::nullopt;
		acc = acc << 6 | static_cast<unsigned>(v);
		bits += 6;
		if (bits >= 8) {
			bits -= 8;
			out += static_cast<char>(acc >> bits & 0xff);
		}
	}
	// Trailing bits must be zero (Go's decoder is strict).
	if (bits > 0 && (acc & ((1u << bits) - 1)) != 0) return std::nullopt;
	return out;
}

std::string generate(const std::string &username, std::uint64_t token_version)
{
	const auto &conf = config::get();
	std::int64_t now = std::time(nullptr);
	unsigned char raw[16];
	std::string jti;
	if (RAND_bytes(raw, sizeof raw) == 1) {
		static const char hex[] = "0123456789abcdef";
		for (unsigned char b : raw) {
			jti += hex[b >> 4];
			jti += hex[b & 15];
		}
	}
	// The field order of the Go token: username, tokenVersion, sub, exp, iat, jti (empty ones omitted).
	std::string payload = "{\"username\":" + json_string(username) + ",\"tokenVersion\":" + std::to_string(token_version) +
	                      ",\"sub\":" + json_string(username) +
	                      ",\"exp\":" + std::to_string(now + static_cast<std::int64_t>(conf.jwt.refresh_token_duration)) +
	                      ",\"iat\":" + std::to_string(now);
	if (!jti.empty()) payload += ",\"jti\":" + json_string(jti);
	payload += "}";
	static const std::string header = "{\"alg\":\"HS256\",\"typ\":\"JWT\"}";
	std::string signing = base64url(reinterpret_cast<const unsigned char *>(header.data()), header.size()) + "." +
	                      base64url(reinterpret_cast<const unsigned char *>(payload.data()), payload.size());
	return signing + "." + hmac(conf.jwt.secret_key, signing);
}

std::optional<Token> parse(const std::string &jwt)
{
	auto first = jwt.find('.');
	auto second = first == std::string::npos ? std::string::npos : jwt.find('.', first + 1);
	if (second == std::string::npos || jwt.find('.', second + 1) != std::string::npos) return std::nullopt;
	std::string_view signing(jwt.data(), second);
	std::string expected = hmac(config::get().jwt.secret_key, signing);
	std::string_view given(jwt.data() + second + 1, jwt.size() - second - 1);
	if (expected.size() != given.size() || CRYPTO_memcmp(expected.data(), given.data(), given.size()) != 0)
		return std::nullopt;

	auto header_json = base64url_decode(std::string_view(jwt.data(), first));
	auto payload_json = base64url_decode(std::string_view(jwt.data() + first + 1, second - first - 1));
	if (!header_json || !payload_json) return std::nullopt;
	Header header;
	if (glz::read<read_opts>(header, *header_json) || header.alg != "HS256") return std::nullopt;
	Claims claims;
	if (glz::read<read_opts>(claims, *payload_json)) return std::nullopt;

	double now = static_cast<double>(std::time(nullptr));
	if (!claims.exp || !std::isfinite(*claims.exp) || now >= *claims.exp) return std::nullopt;  // WithExpirationRequired
	if (claims.nbf && now < *claims.nbf) return std::nullopt;
	if (!claims.username || claims.username->empty() || !claims.sub || *claims.sub != *claims.username ||
	    !claims.tokenVersion || *claims.tokenVersion == 0)
		return std::nullopt;
	Token t;
	t.username = *claims.username;
	t.token_version = *claims.tokenVersion;
	t.id = claims.jti.value_or("");
	t.issued = claims.iat ? static_cast<std::int64_t>(*claims.iat) : 0;
	t.expires = static_cast<std::int64_t>(*claims.exp);
	return t;
}

std::string session_key(const Token &token)
{
	std::string key = token.username + '\0' + std::to_string(token.token_version) + '\0';
	if (!token.id.empty()) return key + "id:" + token.id;
	// Go: IssuedAt in nanoseconds.
	return key + std::to_string(token.issued * 1000000000LL);
}

} // namespace nkos::auth
