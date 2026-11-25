#pragma once

// Import of public SSH login keys for root, as the Go server's internal/sshkeys and service/vm/ssh_keys.go: every
// line of the input must be a supported OpenSSH public key without options, the keys are appended to
// ~/.ssh/authorized_keys atomically, existing lines (restrictions included) stay as they are and keys already there
// are skipped. Key parsing follows golang.org/x/crypto/ssh (ParseAuthorizedKey, the wire formats, the canonical
// MarshalAuthorizedKey form and SHA-256 fingerprints).

#include "http/router.hpp"

#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::system::ssh_keys {

inline constexpr size_t max_input = 32 * 1024;

struct PublicKey {
	std::string type;
	std::string wire;  // Marshal(): the key's wire encoding
	int rsa_bits = 0;  // ssh-rsa: the modulus length
};
struct Parsed {
	PublicKey key;
	std::string comment;
	std::vector<std::string> options;
};
// ssh.ParseAuthorizedKey for one line (no '\n'): nullopt when it holds no key.
std::optional<Parsed> parse_authorized_line(std::string_view line);
// ssh.FingerprintSHA256.
std::string fingerprint(const PublicKey &key);

struct Result {
	int added = 0, skipped = 0;
	std::vector<std::string> fingerprints;
};
// sshkeys.Import: the result, or invalid (sshkeys.ErrInvalid) or another error.
enum class Outcome { ok, invalid, failed };
Outcome import_keys(const std::string &home, std::string_view input, Result &result, std::string &error);
std::string result_json(const Result &result);

void add_routes(http::Router &router);

} // namespace nkos::system::ssh_keys
