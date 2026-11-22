#pragma once

// Password hashes. New hashes are Argon2id (OpenSSL's ARGON2ID KDF; 19 MiB, 2 passes, 1 lane: about 650 ms on the
// C906) in the PHC string format. Hashes of earlier releases still verify: bcrypt ($2a$/$2b$/$2y$, by musl's crypt)
// and the reversibly encrypted CryptoJS form of the first releases; both are rehashed after a successful login.

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

// CryptoJS AES ("Salted__" + PBKDF from MD5) with the fixed NanoKVM key, as the web UI sends passwords and as early
// releases stored them; also accepted %-encoded. nullopt when it does not decrypt.
std::optional<std::string> decrypt_cryptojs(std::string_view data);

// utils.DecodeCredentialPassword: "plain" as is, "" or "cryptojs" decrypted; nullopt for another encoding.
std::optional<std::string> decode_credential(std::string_view value, std::string_view encoding);

} // namespace nkos::auth
