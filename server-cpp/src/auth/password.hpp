#pragma once

// Password hashes. New hashes are Argon2id (OpenSSL's ARGON2ID KDF; 19 MiB, 2 passes, 1 lane: about 650 ms on the
// C906) in the PHC string format. bcrypt hashes of earlier releases ($2a$/$2b$/$2y$, by musl's crypt) still verify
// and are rehashed after a successful login.

#include <optional>
#include <string>
#include <string_view>

namespace nkos::auth {

// "$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>" (unpadded standard base64). nullopt on a KDF failure.
std::optional<std::string> hash_password(std::string_view password);

// Whether password matches hash, in any of the supported forms. Slow: run it off the event loop.
bool verify_password(const std::string &hash, std::string_view password);

// Whether hash is in the current form (Argon2id with the current parameters).
bool hash_is_current(const std::string &hash);

// The password of an API request: "plain" preserves every byte; omitted or "cryptojs" uses the legacy wire format.
std::optional<std::string> decode_credential(std::string_view value, std::string_view encoding);

} // namespace nkos::auth
