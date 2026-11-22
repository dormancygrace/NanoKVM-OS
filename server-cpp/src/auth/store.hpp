#pragma once

// The web accounts, /etc/kvm/pwd, as the Go server's authn.Store keeps them: users with a password hash, a role,
// an enabled flag, a token version that revokes their sessions when it changes, the device owner (systemAccount),
// a forced password change, and TOTP. Thread-safe. Calls that hash a password are slow: run them on the worker.

#include <cstdint>
#include <functional>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

namespace nkos::auth {

inline constexpr const char *account_file = "/etc/kvm/pwd";

struct Totp {
	std::string secret;               // base32, no padding
	std::int64_t last_step = 0;
	std::vector<std::string> recovery_hashes;
};

struct User {
	std::string username;
	std::string password_hash;
	std::string role;  // "admin" or "user"
	bool enabled = true;
	std::uint64_t token_version = 0;
	bool must_change_password = false;
	bool system_account = false;
	std::optional<Totp> totp;
};

struct UserPatch {
	std::optional<std::string> username, role;
	std::optional<bool> enabled;
};

// Error texts as the Go store returns them (they reach the API).
namespace errors {
inline constexpr const char *user_not_found = "user not found";
inline constexpr const char *user_exists = "username already exists";
inline constexpr const char *last_admin = "at least one enabled admin is required";
inline constexpr const char *self_modification = "administrators cannot disable or demote themselves";
inline constexpr const char *self_delete = "administrators cannot delete themselves";
inline constexpr const char *system_account = "the device owner account must remain an enabled administrator";
inline constexpr const char *system_account_rename = "only the device owner can rename the device owner account";
inline constexpr const char *session_revoked = "session revoked";
} // namespace errors

std::optional<std::string> validate_username(const std::string &username);  // nullopt: valid, else the message
std::optional<std::string> validate_password(const std::string &password);

class Store {
public:
	explicit Store(std::string path) : path_(std::move(path)) {}

	// error is set and nullopt returned on failure (file unreadable, user not found, ...).
	std::optional<std::vector<User>> list(std::string &error);
	std::optional<User> get(const std::string &username, std::string &error);
	// The user of a session token. Never hashes: a missing file has no valid session.
	std::optional<User> validate_token(const std::string &username, std::uint64_t token_version, std::string &error);

	// Slow. ok=false with no error: wrong password, unknown or disabled user. A correct password stored in an
	// older form (bcrypt) is rehashed with Argon2id.
	std::optional<User> authenticate(const std::string &username, const std::string &password, std::string &error);
	// Slow.
	bool create(const std::string &username, const std::string &password, const std::string &role, std::string &error);
	std::optional<User> update(const std::string &actor, const std::string &username, const UserPatch &patch,
	                           bool &changed, std::string &error);
	bool remove(const std::string &actor, const std::string &username, std::string &error);
	// Slow. expect (may be empty) is checked under the lock; after_commit runs after the write and, when it
	// fails, the previous accounts are restored.
	std::optional<User> set_password(const std::string &username, const std::string &password,
	                                 const std::function<std::optional<std::string>(const User &)> &expect,
	                                 const std::function<std::optional<std::string>()> &after_commit, std::string &error);
	std::optional<User> revoke(const std::string &username, std::string &error);
	// Changes a user under the lock; change sets save when the file must be written. An error from change is
	// returned as is (TOTP enable/disable/use, revocation).
	std::optional<User> modify(const std::string &username,
	                           const std::function<std::optional<std::string>(User &, bool &save)> &change, std::string &error);

private:
	struct Database {
		int version = 1;
		std::vector<User> users;
	};
	struct Stamp {
		std::int64_t size = 0, mtime_ns = 0, ctime_ns = 0;
		std::uint64_t dev = 0, ino = 0;
		bool operator==(const Stamp &) const = default;
	};

	// migrate: write a migrated or default database (needs a password hash for the default: slow).
	std::optional<Database> load(bool migrate, std::string &error);
	bool save(const Database &db, std::string &error);

	std::string path_;
	std::recursive_mutex mutex_;
	std::optional<Database> cache_;
	Stamp cache_stamp_;
	std::string default_hash_;  // admin's default password hash, made once
};

Store &store();

} // namespace nkos::auth
