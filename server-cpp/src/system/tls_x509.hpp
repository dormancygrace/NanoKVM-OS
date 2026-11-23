#pragma once

// Certificates and keys as the Go server reads them (server/utils/cert.go, certstore.go): Go's encoding/pem,
// crypto/x509 certificate parsing, the PKCS#8/PKCS#1/SEC1 private key parsing of crypto/tls, tls.X509KeyPair and
// utils.ParseCertificatePair with their accept/reject decisions and error texts, DescribeCertificate, and the
// device's ECDSA P-256 self-signed pair. OpenSSL does the cryptography (hashes, signatures, curve arithmetic, key
// generation); the decisions follow Go's code.

#include <cstdint>
#include <expected>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::system::tls {

// The wall clock as Go's time.Now: seconds and nanoseconds since the epoch.
struct Instant {
	std::int64_t sec = 0;
	std::int64_t nsec = 0;
};
Instant now();

struct PemBlock {
	std::string type;
	std::map<std::string, std::string> headers;
	std::string bytes;
};
// pem.Decode: the next block of data and what follows it, or nullopt (rest is then data).
std::optional<PemBlock> pem_decode(std::string_view data, std::string_view &rest);
// pem.EncodeToMemory of a block without headers.
std::string pem_encode(std::string_view type, std::string_view bytes);

enum class KeyKind { unknown, rsa, ecdsa, ed25519, x25519, dsa, mldsa };

struct PublicKey {
	KeyKind kind = KeyKind::unknown;
	std::string n;       // RSA modulus, big-endian, no leading zeros
	std::int64_t e = 0;  // RSA public exponent
	int curve = 0;       // ECDSA: OpenSSL NID of the curve
	std::string point;   // ECDSA uncompressed point; Ed25519 key
};

struct Certificate {
	std::string raw, raw_tbs, raw_issuer, raw_subject, raw_spki, signature;
	int signature_algorithm = 0;
	std::string subject, issuer;        // pkix.Name.String()
	std::string subject_cn, issuer_cn;  // the last CommonName
	std::int64_t not_before = 0, not_after = 0;  // Unix seconds
	PublicKey key;
	std::vector<std::string> dns_names;
	std::vector<std::string> ip_addresses;  // 4 or 16 bytes each
};
// x509.ParseCertificate.
std::expected<Certificate, std::string> parse_certificate(std::string_view der);

struct PrivateKey {
	KeyKind kind = KeyKind::unknown;
	PublicKey pub;
	// What re-encodes the key for OpenSSL: RSA d, p, q, dp, dq, qinv (big-endian); ECDSA d (curve size); Ed25519 seed.
	std::string d, p, q, dp, dq, qinv;
	int primes = 2;
};
// crypto/tls parsePrivateKey (errors carry the "tls: " prefix).
std::expected<PrivateKey, std::string> parse_private_key(std::string_view der);

struct Pair {
	std::vector<std::string> chain;  // DER, the leaf first
	Certificate leaf;
	PrivateKey key;
};
// tls.X509KeyPair.
std::expected<Pair, std::string> x509_key_pair(std::string_view cert_pem, std::string_view key_pem);
// tls.LoadX509KeyPair.
std::expected<Pair, std::string> load_x509_key_pair(const std::string &crt, const std::string &key);

struct Parsed {
	Pair pair;
	std::string cert_out, key_out;  // only the certificates, only the key (PEM)
};
// utils.ParseCertificatePair and utils.LoadCertificatePair.
std::expected<Parsed, std::string> parse_certificate_pair(std::string_view cert_pem, std::string_view key_pem);
std::expected<Parsed, std::string> load_certificate_pair(const std::string &crt, const std::string &key);

// utils.DescribeCertificate as JSON (the CertificateInfo of the status), and the leaf's SHA-256 fingerprint.
std::string describe(const Pair &pair, Instant at);
std::string fingerprint(const Pair &pair);

// The private key as PKCS#8 PEM written by OpenSSL, for the TLS listener; nullopt when it cannot be encoded.
std::optional<std::string> serving_key_pem(const PrivateKey &key);

// utils.GenerateCertificateFiles: a new ECDSA P-256 self-signed pair, the key published before the certificate,
// each atomically. "" on success, else the error.
std::string generate_certificate_files(const std::string &crt, const std::string &key, Instant at);

// os.ReadFile with Go's error texts ("open /path: no such file or directory").
std::optional<std::string> read_file(const std::string &path, std::string &error);
// A Go *PathError text for errno.
std::string path_error(std::string_view op, const std::string &path, int err);
// Go's syscall.Errno text.
std::string errno_text(int err);

// Time formats: "2006-01-02" and RFC 3339 in UTC.
std::string format_date(std::int64_t unix);
std::string format_rfc3339(std::int64_t unix);

} // namespace nkos::system::tls
