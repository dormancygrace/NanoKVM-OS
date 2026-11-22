#include "auth/totp.hpp"

#include <openssl/crypto.h>
#include <openssl/evp.h>
#include <openssl/hmac.h>
#include <openssl/rand.h>

#include <cctype>
#include <cstdio>
#include <cstring>

namespace nkos::auth::totp {

namespace {

constexpr int digits = 6, period = 30, skew = 1;
constexpr size_t secret_size = 20, recovery_count = 10, recovery_bytes = 7;

const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

std::string base32(const unsigned char *data, size_t len)
{
	std::string out;
	unsigned buffer = 0;
	int bits = 0;
	for (size_t i = 0; i < len; i++) {
		buffer = buffer << 8 | data[i];
		bits += 8;
		while (bits >= 5) {
			out += alphabet[buffer >> (bits - 5) & 31];
			bits -= 5;
		}
	}
	if (bits > 0) out += alphabet[buffer << (5 - bits) & 31];
	return out;
}

// base32 without padding, upper case (StdEncoding.WithPadding(NoPadding)).
std::optional<std::vector<unsigned char>> unbase32(const std::string &s)
{
	std::vector<unsigned char> out;
	unsigned buffer = 0;
	int bits = 0;
	for (char c : s) {
		const char *p = c ? std::strchr(alphabet, c) : nullptr;
		if (!p) return std::nullopt;
		buffer = buffer << 5 | static_cast<unsigned>(p - alphabet);
		bits += 5;
		if (bits >= 8) {
			out.push_back(static_cast<unsigned char>(buffer >> (bits - 8) & 0xff));
			bits -= 8;
		}
	}
	// A length that no byte count produces is invalid, as in Go.
	size_t mod = s.size() % 8;
	if (mod == 1 || mod == 3 || mod == 6) return std::nullopt;
	return out;
}

std::string code_at(const std::vector<unsigned char> &key, std::int64_t step)
{
	unsigned char counter[8];
	for (int i = 7; i >= 0; i--, step >>= 8) counter[i] = static_cast<unsigned char>(step & 0xff);
	unsigned char sum[20];
	unsigned len = 0;
	HMAC(EVP_sha1(), key.data(), static_cast<int>(key.size()), counter, sizeof counter, sum, &len);
	int offset = sum[len - 1] & 0x0f;
	std::uint32_t value = (static_cast<std::uint32_t>(sum[offset]) << 24 | sum[offset + 1] << 16 | sum[offset + 2] << 8 |
	                       sum[offset + 3]) & 0x7fffffff;
	char text[16];
	std::snprintf(text, sizeof text, "%06u", value % 1000000);
	return text;
}

// normalizeCode: spaces and dashes out, upper case to lower.
std::string normalize(const std::string &code)
{
	std::string t = code;
	while (!t.empty() && std::isspace(static_cast<unsigned char>(t.front()))) t.erase(0, 1);
	while (!t.empty() && std::isspace(static_cast<unsigned char>(t.back()))) t.pop_back();
	std::string out;
	for (char c : t) {
		if (c == ' ' || c == '-') continue;
		out += c >= 'A' && c <= 'Z' ? static_cast<char>(c + 'a' - 'A') : c;
	}
	return out;
}

std::string recovery_hash(const std::string &normalized)
{
	std::string input = "nanokvm-recovery:" + normalized;
	unsigned char sum[32];
	EVP_Digest(input.data(), input.size(), sum, nullptr, EVP_sha256(), nullptr);
	static const char hex[] = "0123456789abcdef";
	std::string out;
	for (unsigned char b : sum) {
		out += hex[b >> 4];
		out += hex[b & 15];
	}
	return out;
}

int match_recovery(const std::vector<std::string> &hashes, const std::string &code)
{
	std::string c = normalize(code);
	if (c.size() != 12) return -1;
	std::string want = recovery_hash(c);
	int found = -1;
	for (size_t i = 0; i < hashes.size(); i++)
		if (hashes[i].size() == want.size() && CRYPTO_memcmp(hashes[i].data(), want.data(), want.size()) == 0)
			found = static_cast<int>(i);
	return found;
}

std::string escape_query(const std::string &s)
{
	std::string out;
	for (unsigned char c : s) {
		if (std::isalnum(c) || c == '-' || c == '_' || c == '.' || c == '~') out += static_cast<char>(c);
		else if (c == ' ') out += '+';
		else {
			char buf[4];
			std::snprintf(buf, sizeof buf, "%%%02X", c);
			out += buf;
		}
	}
	return out;
}

// url.PathEscape of a path segment.
std::string escape_path(const std::string &s)
{
	std::string out;
	for (unsigned char c : s) {
		if (std::isalnum(c) || std::strchr("-_.~$&+:=@", c)) out += static_cast<char>(c);
		else {
			char buf[4];
			std::snprintf(buf, sizeof buf, "%%%02X", c);
			out += buf;
		}
	}
	return out;
}

} // namespace

std::optional<std::string> new_secret()
{
	unsigned char key[secret_size];
	if (RAND_bytes(key, sizeof key) != 1) return std::nullopt;
	return base32(key, sizeof key);
}

std::string uri(const std::string &issuer, const std::string &account, const std::string &secret)
{
	// url.Values.Encode: keys sorted.
	return "otpauth://totp/" + escape_path(issuer + ":" + account) + "?algorithm=SHA1&digits=" + std::to_string(digits) +
	       "&issuer=" + escape_query(issuer) + "&period=" + std::to_string(period) + "&secret=" + escape_query(secret);
}

std::int64_t match(const std::string &secret, const std::string &code, std::time_t now, std::int64_t last_step)
{
	std::string c = normalize(code);
	if (c.size() != digits) return 0;
	auto key = unbase32(secret);
	if (!key) return 0;
	std::int64_t current = static_cast<std::int64_t>(now) / period;
	for (std::int64_t step = current - skew; step <= current + skew; step++) {
		if (step <= last_step) continue;
		std::string expected = code_at(*key, step);
		if (CRYPTO_memcmp(expected.data(), c.data(), c.size()) == 0) return step;
	}
	return 0;
}

bool new_recovery_codes(std::vector<std::string> &codes, std::vector<std::string> &hashes)
{
	codes.clear();
	hashes.clear();
	while (codes.size() < recovery_count) {
		unsigned char raw[recovery_bytes];
		if (RAND_bytes(raw, sizeof raw) != 1) return false;
		std::string text = base32(raw, sizeof raw).substr(0, 12);
		for (auto &ch : text) ch = static_cast<char>(std::tolower(static_cast<unsigned char>(ch)));
		codes.push_back(text.substr(0, 4) + "-" + text.substr(4, 4) + "-" + text.substr(8));
		hashes.push_back(recovery_hash(text));
	}
	return true;
}

bool enable(const std::string &username, const std::string &secret, std::int64_t step,
            const std::vector<std::string> &hashes, std::string &error)
{
	return store()
		.modify(username,
		        [&](User &u, bool &save) -> std::optional<std::string> {
			        if (u.totp) return std::string(already_enabled);
			        auto key = unbase32(secret);
			        if (!key || key->size() < 10) return std::string(invalid_code);
			        u.totp = Totp{secret, step, hashes};
			        save = true;
			        return std::nullopt;
		        },
		        error)
		.has_value();
}

bool disable(const std::string &username, bool &was, std::string &error)
{
	was = false;
	return store()
		.modify(username,
		        [&](User &u, bool &save) -> std::optional<std::string> {
			        was = u.totp.has_value();
			        u.totp.reset();
			        save = was;
			        return std::nullopt;
		        },
		        error)
		.has_value();
}

bool replace_recovery_codes(const std::string &username, const std::vector<std::string> &hashes, std::string &error)
{
	return store()
		.modify(username,
		        [&](User &u, bool &save) -> std::optional<std::string> {
			        if (!u.totp) return std::string(not_enabled);
			        u.totp->recovery_hashes = hashes;
			        save = true;
			        return std::nullopt;
		        },
		        error)
		.has_value();
}

std::optional<User> verify(const std::string &username, std::uint64_t token_version, const std::string &code,
                           std::time_t now, bool &recovery, int &left, std::string &error)
{
	recovery = false;
	auto user = store().modify(username,
	                           [&](User &u, bool &save) -> std::optional<std::string> {
		                           if (u.token_version != token_version) return std::string(account_changed);
		                           if (!u.enabled) return std::string(invalid_code);
		                           if (!u.totp) return std::string(not_enabled);
		                           if (auto step = match(u.totp->secret, code, now, u.totp->last_step); step > 0) {
			                           u.totp->last_step = step;
			                           save = true;
			                           return std::nullopt;
		                           }
		                           if (int i = match_recovery(u.totp->recovery_hashes, code); i >= 0) {
			                           u.totp->recovery_hashes.erase(u.totp->recovery_hashes.begin() + i);
			                           recovery = true;
			                           save = true;
			                           return std::nullopt;
		                           }
		                           return std::string(invalid_code);
	                           },
	                           error);
	if (!user) return std::nullopt;
	left = user->totp ? static_cast<int>(user->totp->recovery_hashes.size()) : 0;
	return user;
}

} // namespace nkos::auth::totp
