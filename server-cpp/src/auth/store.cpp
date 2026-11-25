#include "auth/store.hpp"

#include "auth/password.hpp"
#include "config/config.hpp"

#include <glaze/glaze.hpp>
#include <openssl/rand.h>

#include <algorithm>
#include <cerrno>
#include <cstdio>
#include <cstring>
#include <ctime>
#include <regex>
#include <sys/stat.h>

namespace nkos::auth {

namespace {

// The file's JSON form; optional members are omitted when absent (Go's omitempty).
struct FileTotp {
	std::string secret;
	std::optional<std::int64_t> lastStep;
	std::optional<std::vector<std::string>> recoveryHashes;
};
struct FileUser {
	std::string username;
	std::string password;
	std::string role;
	bool enabled = false;
	std::uint64_t tokenVersion = 0;
	std::optional<bool> mustChangePassword;
	std::optional<bool> systemAccount;
	std::optional<FileTotp> totp;
};
struct FileDatabase {
	int version = 0;
	std::vector<FileUser> users;
	std::optional<std::string> username;  // legacy mirror of the device owner, for single-user releases
	std::optional<std::string> password;
};
struct Legacy {
	std::optional<std::string> username;
	std::optional<std::string> password;
};

constexpr glz::opts read_opts{.error_on_unknown_keys = false};
// Two spaces, as Go's json.MarshalIndent(db, "", "  ") wrote the file.
struct PwdOpts : glz::opts {
	std::uint8_t indentation_width = 2;
};
constexpr PwdOpts write_opts{{.skip_null_members = true, .prettify = true}};

constexpr std::int64_t settle_ns = 5'000'000'000;

User from_file(const FileUser &f)
{
	User u{f.username, f.password, f.role, f.enabled, f.tokenVersion, f.mustChangePassword.value_or(false),
	       f.systemAccount.value_or(false), std::nullopt};
	if (f.totp) u.totp = Totp{f.totp->secret, f.totp->lastStep.value_or(0), f.totp->recoveryHashes.value_or(std::vector<std::string>{})};
	return u;
}

FileUser to_file(const User &u)
{
	FileUser f{u.username, u.password_hash, u.role, u.enabled, u.token_version, std::nullopt, std::nullopt, std::nullopt};
	if (u.must_change_password) f.mustChangePassword = true;
	if (u.system_account) f.systemAccount = true;
	if (u.totp) {
		f.totp = FileTotp{u.totp->secret, std::nullopt, std::nullopt};
		if (u.totp->last_step) f.totp->lastStep = u.totp->last_step;
		if (!u.totp->recovery_hashes.empty()) f.totp->recoveryHashes = u.totp->recovery_hashes;
	}
	return f;
}

bool valid_role(const std::string &role) { return role == "admin" || role == "user"; }

int enabled_admins(const std::vector<User> &users)
{
	int n = 0;
	for (const auto &u : users)
		if (u.role == "admin" && u.enabled) n++;
	return n;
}

int index_of(const std::vector<User> &users, const std::string &username)
{
	for (size_t i = 0; i < users.size(); i++)
		if (users[i].username == username) return static_cast<int>(i);
	return -1;
}

// validateDatabase; also fills a zero token version with 1.
std::optional<std::string> validate(int version, std::vector<User> &users)
{
	if (version != 1) return "unsupported account file version: " + std::to_string(version);
	if (users.empty()) return std::string("account file contains no users");
	std::vector<std::string> seen;
	int system = 0;
	for (auto &u : users) {
		if (auto e = validate_username(u.username)) return e;
		if (u.password_hash.empty() || !valid_role(u.role)) return std::string("account file contains an invalid user");
		if (std::find(seen.begin(), seen.end(), u.username) != seen.end())
			return std::string("account file contains duplicate usernames");
		seen.push_back(u.username);
		if (u.token_version == 0) u.token_version = 1;
		if (u.system_account) {
			system++;
			if (u.role != "admin" || !u.enabled || system > 1) return std::string(errors::system_account);
		}
	}
	if (enabled_admins(users) == 0) return std::string(errors::last_admin);
	return std::nullopt;
}

std::uint64_t new_token_version()
{
	for (;;) {
		std::uint64_t v = 0;
		if (RAND_bytes(reinterpret_cast<unsigned char *>(&v), sizeof v) != 1) return 1;
		if (v != 0 && v != ~std::uint64_t{0}) return v;
	}
}

std::optional<std::string> read_file(const std::string &path, struct stat &st, int &err)
{
	FILE *f = std::fopen(path.c_str(), "rb");
	if (!f) {
		err = errno;
		return std::nullopt;
	}
	if (fstat(fileno(f), &st) != 0) {
		err = errno;
		std::fclose(f);
		return std::nullopt;
	}
	std::string data;
	char buf[4096];
	size_t n;
	while ((n = std::fread(buf, 1, sizeof buf, f)) > 0) data.append(buf, n);
	std::fclose(f);
	return data;
}

std::int64_t ns(const timespec &t) { return static_cast<std::int64_t>(t.tv_sec) * 1'000'000'000 + t.tv_nsec; }

std::int64_t now_ns()
{
	timespec t{};
	clock_gettime(CLOCK_REALTIME, &t);
	return ns(t);
}

} // namespace

std::optional<std::string> validate_username(const std::string &username)
{
	static const std::regex pattern("^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$");
	if (!std::regex_match(username, pattern))
		return std::string("username must be 1-32 characters and contain only letters, numbers, '.', '_' or '-'");
	return std::nullopt;
}

std::optional<std::string> validate_password(const std::string &password)
{
	if (password.size() < 8 || password.size() > 72) return std::string("password must be between 8 and 72 bytes");
	// Root's password is set from it through passwd, one line each: a line break would set another password.
	for (size_t i = 0; i < password.size();) {
		unsigned char c = static_cast<unsigned char>(password[i]);
		if (c < 0x20 || c == 0x7f) return std::string("password must not contain control characters");
		i++;
	}
	return std::nullopt;
}

std::optional<Store::Database> Store::load(bool migrate, std::string &error)
{
	struct stat st{};
	if (::stat(path_.c_str(), &st) != 0) {
		cache_.reset();
		if (errno != ENOENT) {
			error = std::strerror(errno);
			return std::nullopt;
		}
		// No file (a reset): a default owner admin/admin with a forced password change. Its hash is made only
		// when the database is written (login), never on a token check.
		if (!migrate) {
			error = "no account file";
			return std::nullopt;
		}
		if (default_hash_.empty()) {
			auto hash = hash_password("admin");
			if (!hash) {
				error = "cannot hash the default password";
				return std::nullopt;
			}
			default_hash_ = *hash;
		}
		Database db;
		db.users.push_back(User{"admin", default_hash_, "admin", true, new_token_version(), true, true, std::nullopt});
		if (!save(db, error)) return std::nullopt;
		return db;
	}
	Stamp stamp{st.st_size, ns(st.st_mtim), ns(st.st_ctim), static_cast<std::uint64_t>(st.st_dev),
	            static_cast<std::uint64_t>(st.st_ino)};
	if (cache_ && stamp == cache_stamp_ && now_ns() - stamp.mtime_ns >= settle_ns) return cache_;

	int err = 0;
	auto data = read_file(path_, st, err);
	if (!data) {
		cache_.reset();
		error = std::strerror(err);
		return std::nullopt;
	}
	stamp = {st.st_size, ns(st.st_mtim), ns(st.st_ctim), static_cast<std::uint64_t>(st.st_dev), static_cast<std::uint64_t>(st.st_ino)};
	FileDatabase file;
	if (!glz::read<read_opts>(file, *data) && file.version != 0) {
		Database db;
		db.version = file.version;
		for (const auto &u : file.users) db.users.push_back(from_file(u));
		if (auto e = validate(db.version, db.users)) {
			cache_.reset();
			error = *e;
			return std::nullopt;
		}
		if (now_ns() - stamp.mtime_ns >= settle_ns) {
			cache_ = db;
			cache_stamp_ = stamp;
		} else {
			cache_.reset();
		}
		return db;
	}
	cache_.reset();
	// The single-account file of early releases: its owner becomes the device owner.
	Legacy legacy;
	if (glz::read<read_opts>(legacy, *data) || !legacy.username || legacy.username->empty() || !legacy.password ||
	    legacy.password->empty()) {
		error = "invalid account file";
		return std::nullopt;
	}
	if (auto e = validate_username(*legacy.username)) {
		error = "invalid legacy username: " + *e;
		return std::nullopt;
	}
	Database db;
	bool default_password = migrate && verify_password(*legacy.password, "admin");
	db.users.push_back(User{*legacy.username, *legacy.password, "admin", true, new_token_version(), default_password, true, std::nullopt});
	if (migrate && !save(db, error)) return std::nullopt;
	return db;
}

bool Store::save(const Database &db, std::string &error)
{
	cache_.reset();
	Database checked = db;
	if (auto e = validate(checked.version, checked.users)) {
		error = *e;
		return false;
	}
	FileDatabase file;
	file.version = checked.version;
	for (const auto &u : checked.users) file.users.push_back(to_file(u));
	// syncLegacyAccount: the device owner, else the first enabled admin.
	const User *mirror = nullptr;
	for (const auto &u : checked.users)
		if (u.system_account) mirror = &u;
	if (!mirror)
		for (const auto &u : checked.users)
			if (u.role == "admin" && u.enabled) {
				mirror = &u;
				break;
			}
	if (mirror) {
		file.username = mirror->username;
		file.password = mirror->password_hash;
	}
	std::string json;
	if (glz::write<write_opts>(file, json)) {
		error = "cannot encode the account file";
		return false;
	}
	json += '\n';
	if (!config::atomic_write(path_, json, 0600)) {
		error = std::strerror(errno);
		return false;
	}
	return true;
}

std::optional<std::vector<User>> Store::list(std::string &error)
{
	std::lock_guard lock(mutex_);
	auto db = load(false, error);
	if (!db) return std::nullopt;
	return db->users;
}

std::optional<User> Store::get(const std::string &username, std::string &error)
{
	std::lock_guard lock(mutex_);
	auto db = load(false, error);
	if (!db) return std::nullopt;
	int i = index_of(db->users, username);
	if (i < 0) {
		error = errors::user_not_found;
		return std::nullopt;
	}
	return db->users[static_cast<size_t>(i)];
}

std::optional<User> Store::validate_token(const std::string &username, std::uint64_t token_version, std::string &error)
{
	auto user = get(username, error);
	if (!user) return std::nullopt;
	if (!user->enabled || user->token_version != token_version) {
		error = errors::session_revoked;
		return std::nullopt;
	}
	return user;
}

std::optional<User> Store::authenticate(const std::string &username, const std::string &password, std::string &error)
{
	std::optional<User> user;
	{
		std::lock_guard lock(mutex_);
		auto db = load(true, error);
		if (!db) return std::nullopt;
		int i = index_of(db->users, username);
		if (i < 0 || !db->users[static_cast<size_t>(i)].enabled) return std::nullopt;
		user = db->users[static_cast<size_t>(i)];
	}
	// The hash runs outside the lock: token checks must not wait behind a login.
	if (!verify_password(user->password_hash, password)) return std::nullopt;
	if (hash_is_current(user->password_hash)) return user;
	auto upgraded = hash_password(password);
	if (!upgraded) return user;  // still a valid login; the upgrade waits for the next one
	std::lock_guard lock(mutex_);
	auto db = load(true, error);
	if (!db) return std::nullopt;
	int i = index_of(db->users, username);
	// The account may have changed meanwhile: upgrade only the record that was checked.
	if (i < 0 || !db->users[static_cast<size_t>(i)].enabled || db->users[static_cast<size_t>(i)].password_hash != user->password_hash)
		return std::nullopt;
	User &u = db->users[static_cast<size_t>(i)];
	u.password_hash = *upgraded;
	if (u.token_version == 0) u.token_version = 1;
	if (!save(*db, error)) return std::nullopt;
	return u;
}

bool Store::create(const std::string &username, const std::string &password, const std::string &role, std::string &error)
{
	if (auto e = validate_username(username)) return error = *e, false;
	if (auto e = validate_password(password)) return error = *e, false;
	if (!valid_role(role)) return error = "invalid role", false;
	auto hash = hash_password(password);
	if (!hash) return error = "cannot hash the password", false;
	std::lock_guard lock(mutex_);
	auto db = load(true, error);
	if (!db) return false;
	if (index_of(db->users, username) >= 0) return error = errors::user_exists, false;
	db->users.push_back(User{username, *hash, role, true, new_token_version(), false, false, std::nullopt});
	return save(*db, error);
}

std::optional<User> Store::update(const std::string &actor, const std::string &username, const UserPatch &patch,
                                  bool &changed, std::string &error)
{
	changed = false;
	std::lock_guard lock(mutex_);
	auto db = load(true, error);
	if (!db) return std::nullopt;
	int i = index_of(db->users, username);
	if (i < 0) return error = errors::user_not_found, std::nullopt;
	User user = db->users[static_cast<size_t>(i)];
	if (patch.username) {
		if (auto e = validate_username(*patch.username)) return error = *e, std::nullopt;
		if (*patch.username != username && index_of(db->users, *patch.username) >= 0) return error = errors::user_exists, std::nullopt;
	}
	if (user.system_account && patch.username && *patch.username != username && actor != username)
		return error = errors::system_account_rename, std::nullopt;
	if (patch.role && !valid_role(*patch.role)) return error = "invalid role", std::nullopt;
	bool demote = (patch.role && *patch.role != "admin") || (patch.enabled && !*patch.enabled);
	if (actor == username && user.role == "admin" && demote) return error = errors::self_modification, std::nullopt;
	if (user.system_account && demote) return error = errors::system_account, std::nullopt;
	if (patch.username && user.username != *patch.username) user.username = *patch.username, changed = true;
	if (patch.role && user.role != *patch.role) user.role = *patch.role, changed = true;
	if (patch.enabled && user.enabled != *patch.enabled) user.enabled = *patch.enabled, changed = true;
	if (!changed) return user;
	db->users[static_cast<size_t>(i)] = user;
	if (enabled_admins(db->users) == 0) return changed = false, error = errors::last_admin, std::nullopt;
	db->users[static_cast<size_t>(i)].token_version++;
	if (!save(*db, error)) return changed = false, std::nullopt;
	return db->users[static_cast<size_t>(i)];
}

bool Store::remove(const std::string &actor, const std::string &username, std::string &error)
{
	if (actor == username) return error = errors::self_delete, false;
	std::lock_guard lock(mutex_);
	auto db = load(true, error);
	if (!db) return false;
	int i = index_of(db->users, username);
	if (i < 0) return error = errors::user_not_found, false;
	if (db->users[static_cast<size_t>(i)].system_account) return error = errors::system_account, false;
	auto users = db->users;
	users.erase(users.begin() + i);
	if (enabled_admins(users) == 0) return error = errors::last_admin, false;
	db->users = std::move(users);
	return save(*db, error);
}

std::optional<User> Store::set_password(const std::string &username, const std::string &password,
                                        const std::function<std::optional<std::string>(const User &)> &expect,
                                        const std::function<std::optional<std::string>()> &after_commit, std::string &error)
{
	if (auto e = validate_password(password)) return error = *e, std::nullopt;
	auto hash = hash_password(password);
	if (!hash) return error = "cannot hash the password", std::nullopt;
	std::lock_guard lock(mutex_);
	auto db = load(true, error);
	if (!db) return std::nullopt;
	int i = index_of(db->users, username);
	if (i < 0) return error = errors::user_not_found, std::nullopt;
	if (expect)
		if (auto e = expect(db->users[static_cast<size_t>(i)])) return error = *e, std::nullopt;
	Database previous = *db;
	User &u = db->users[static_cast<size_t>(i)];
	u.password_hash = *hash;
	u.must_change_password = false;
	u.token_version++;
	if (!save(*db, error)) return std::nullopt;
	if (after_commit) {
		if (auto e = after_commit()) {
			std::string rollback_error;
			if (!save(previous, rollback_error))
				return error = "system password update failed: " + *e + "; account rollback failed: " + rollback_error, std::nullopt;
			return error = *e, std::nullopt;
		}
	}
	return db->users[static_cast<size_t>(i)];
}

std::optional<User> Store::revoke(const std::string &username, std::string &error)
{
	return modify(username, [](User &u, bool &save) -> std::optional<std::string> {
		u.token_version++;
		save = true;
		return std::nullopt;
	}, error);
}

std::optional<User> Store::modify(const std::string &username,
                                  const std::function<std::optional<std::string>(User &, bool &save)> &change, std::string &error)
{
	std::lock_guard lock(mutex_);
	auto db = load(true, error);
	if (!db) return std::nullopt;
	int i = index_of(db->users, username);
	if (i < 0) return error = errors::user_not_found, std::nullopt;
	bool write = false;
	if (auto e = change(db->users[static_cast<size_t>(i)], write)) return error = *e, std::nullopt;
	if (write && !save(*db, error)) return std::nullopt;
	return db->users[static_cast<size_t>(i)];
}

Store &store()
{
	static Store instance(account_file);
	return instance;
}

} // namespace nkos::auth
