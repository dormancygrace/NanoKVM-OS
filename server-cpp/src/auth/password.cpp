#include "auth/password.hpp"
#include "http/router.hpp"

#include <openssl/core_names.h>
#include <openssl/crypto.h>
#include <openssl/evp.h>
#include <openssl/kdf.h>
#include <openssl/rand.h>

#include <crypt.h>
#include <algorithm>
#include <climits>
#include <memory>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <vector>

namespace nkos::auth {

namespace {

constexpr std::uint32_t memory_kib = 19456, passes = 2, lanes = 1;
constexpr size_t salt_len = 16, hash_len = 32;

std::string b64(const unsigned char *data, size_t len, bool pad)
{
	static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	std::string out;
	for (size_t i = 0; i < len; i += 3) {
		unsigned v = data[i] << 16 | (i + 1 < len ? data[i + 1] << 8 : 0) | (i + 2 < len ? data[i + 2] : 0);
		out += alphabet[v >> 18 & 63];
		out += alphabet[v >> 12 & 63];
		if (i + 1 < len) out += alphabet[v >> 6 & 63];
		else if (pad) out += '=';
		if (i + 2 < len) out += alphabet[v & 63];
		else if (pad) out += '=';
	}
	return out;
}

std::optional<std::vector<unsigned char>> unb64(std::string_view s)
{
	std::vector<unsigned char> out;
	unsigned acc = 0;
	int bits = 0;
	for (char c : s) {
		if (c == '=') break;
		int v = c >= 'A' && c <= 'Z' ? c - 'A' : c >= 'a' && c <= 'z' ? c - 'a' + 26 : c >= '0' && c <= '9' ? c - '0' + 52
		      : c == '+' ? 62 : c == '/' ? 63 : -1;
		if (v < 0) return std::nullopt;
		acc = acc << 6 | static_cast<unsigned>(v);
		bits += 6;
		if (bits >= 8) {
			bits -= 8;
			out.push_back(static_cast<unsigned char>(acc >> bits & 0xff));
		}
	}
	return out;
}

// CryptoJS encodes "Salted__", an eight-byte salt and PKCS#7-padded AES-CBC ciphertext in standard Base64.
std::optional<std::string> decrypt_legacy(std::string_view wire)
{
	if (wire.empty()) return std::string{};
	std::string encoded(wire);
	std::erase(encoded, '\r');
	std::erase(encoded, '\n');
	if (encoded.size() < 44 || encoded.size() > INT_MAX || encoded.size() % 4 != 0) return std::nullopt;
	const size_t padding = encoded.ends_with("==") ? 2 : encoded.ends_with("=") ? 1 : 0;
	constexpr std::string_view alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	if (std::string_view(encoded).substr(0, encoded.size() - padding).find_first_not_of(alphabet) != std::string_view::npos)
		return std::nullopt;
	std::vector<unsigned char> raw(encoded.size() / 4 * 3);
	int n = EVP_DecodeBlock(raw.data(), reinterpret_cast<const unsigned char *>(encoded.data()), static_cast<int>(encoded.size()));
	if (n < 0) return std::nullopt;
	raw.resize(static_cast<size_t>(n) - padding);
	if (raw.size() < 32 || (raw.size() - 16) % 16 || std::memcmp(raw.data(), "Salted__", 8) != 0) return std::nullopt;

	// This public passphrase is the original API's wire format. HTTPS protects credentials in transit.
	static constexpr unsigned char passphrase[] = "nanokvm-sipeed-2024";
	unsigned char key[32], iv[16];
	if (EVP_BytesToKey(EVP_aes_256_cbc(), EVP_md5(), raw.data() + 8, passphrase, sizeof passphrase - 1, 1, key, iv) != 32)
		return std::nullopt;
	std::unique_ptr<EVP_CIPHER_CTX, decltype(&EVP_CIPHER_CTX_free)> ctx(EVP_CIPHER_CTX_new(), EVP_CIPHER_CTX_free);
	if (!ctx || EVP_DecryptInit_ex(ctx.get(), EVP_aes_256_cbc(), nullptr, key, iv) != 1) return std::nullopt;
	std::string plain(raw.size() - 16, '\0');
	auto *out = reinterpret_cast<unsigned char *>(plain.data());
	int len = 0, tail = 0;
	if (EVP_DecryptUpdate(ctx.get(), out, &len, raw.data() + 16, static_cast<int>(raw.size() - 16)) != 1 ||
	    EVP_DecryptFinal_ex(ctx.get(), out + len, &tail) != 1) return std::nullopt;
	plain.resize(static_cast<size_t>(len + tail));
	return plain;
}

bool argon2id(std::string_view password, const unsigned char *salt, size_t salt_size, std::uint32_t m, std::uint32_t t,
              std::uint32_t p, unsigned char *out, size_t out_len)
{
	EVP_KDF *kdf = EVP_KDF_fetch(nullptr, "ARGON2ID", nullptr);
	if (!kdf) return false;
	EVP_KDF_CTX *ctx = EVP_KDF_CTX_new(kdf);
	EVP_KDF_free(kdf);
	if (!ctx) return false;
	std::uint32_t threads = 1, version = 0x13;
	OSSL_PARAM params[] = {
		OSSL_PARAM_construct_octet_string(OSSL_KDF_PARAM_PASSWORD, const_cast<char *>(password.data()), password.size()),
		OSSL_PARAM_construct_octet_string(OSSL_KDF_PARAM_SALT, const_cast<unsigned char *>(salt), salt_size),
		OSSL_PARAM_construct_uint32(OSSL_KDF_PARAM_ITER, &t),
		OSSL_PARAM_construct_uint32(OSSL_KDF_PARAM_ARGON2_MEMCOST, &m),
		OSSL_PARAM_construct_uint32(OSSL_KDF_PARAM_ARGON2_LANES, &p),
		OSSL_PARAM_construct_uint32(OSSL_KDF_PARAM_THREADS, &threads),
		OSSL_PARAM_construct_uint32(OSSL_KDF_PARAM_ARGON2_VERSION, &version),
		OSSL_PARAM_construct_end(),
	};
	bool ok = EVP_KDF_derive(ctx, out, out_len, params) == 1;
	EVP_KDF_CTX_free(ctx);
	return ok;
}

// "$argon2id$v=19$m=M,t=T,p=P$salt$hash"
struct Phc {
	std::uint32_t m = 0, t = 0, p = 0;
	std::vector<unsigned char> salt, hash;
};

std::optional<Phc> parse_phc(const std::string &s)
{
	if (!s.starts_with("$argon2id$v=19$m=")) return std::nullopt;
	Phc phc;
	unsigned m = 0, t = 0, p = 0;
	int consumed = 0;
	if (std::sscanf(s.c_str(), "$argon2id$v=19$m=%u,t=%u,p=%u$%n", &m, &t, &p, &consumed) != 3 || consumed == 0)
		return std::nullopt;
	std::string_view rest(s.c_str() + consumed);
	auto dollar = rest.find('$');
	if (dollar == std::string_view::npos) return std::nullopt;
	auto salt = unb64(rest.substr(0, dollar));
	auto hash = unb64(rest.substr(dollar + 1));
	if (!salt || !hash || salt->size() < 8 || hash->size() < 16 || m < 8 || t < 1 || p < 1 || m > (1u << 22)) return std::nullopt;
	phc.m = m, phc.t = t, phc.p = p, phc.salt = std::move(*salt), phc.hash = std::move(*hash);
	return phc;
}

} // namespace

std::optional<std::string> hash_password(std::string_view password)
{
	unsigned char salt[salt_len], out[hash_len];
	if (RAND_bytes(salt, sizeof salt) != 1) return std::nullopt;
	if (!argon2id(password, salt, sizeof salt, memory_kib, passes, lanes, out, sizeof out)) return std::nullopt;
	return "$argon2id$v=19$m=" + std::to_string(memory_kib) + ",t=" + std::to_string(passes) + ",p=" + std::to_string(lanes) +
	       "$" + b64(salt, sizeof salt, false) + "$" + b64(out, sizeof out, false);
}

bool hash_is_current(const std::string &hash)
{
	auto phc = parse_phc(hash);
	return phc && phc->m == memory_kib && phc->t == passes && phc->p == lanes;
}

bool verify_password(const std::string &hash, std::string_view password)
{
	if (auto phc = parse_phc(hash)) {
		std::vector<unsigned char> out(phc->hash.size());
		if (!argon2id(password, phc->salt.data(), phc->salt.size(), phc->m, phc->t, phc->p, out.data(), out.size())) return false;
		return CRYPTO_memcmp(out.data(), phc->hash.data(), out.size()) == 0;
	}
	if (hash.starts_with("$2a$") || hash.starts_with("$2b$") || hash.starts_with("$2y$")) {
		// bcrypt uses the first 72 bytes; Go's CompareHashAndPassword refuses longer passwords outright.
		if (password.size() > 72) return false;
		crypt_data data{};
		std::string pw(password);
		const char *result = crypt_r(pw.c_str(), hash.c_str(), &data);
		return result && std::strlen(result) == hash.size() && CRYPTO_memcmp(result, hash.data(), hash.size()) == 0;
	}
	return false;
}

std::optional<std::string> decode_credential(std::string_view value, std::string_view encoding)
{
	if (encoding == "plain") return std::string(value);
	if (encoding.empty() || encoding == "cryptojs") {
		if (auto plain = decrypt_legacy(value)) return plain;
		// Form parsing already unescapes the value; the older JSON clients percent-encode their Base64.
		if (value.find('%') != std::string_view::npos) return decrypt_legacy(http::url_decode(value));
	}
	return std::nullopt;
}

} // namespace nkos::auth
