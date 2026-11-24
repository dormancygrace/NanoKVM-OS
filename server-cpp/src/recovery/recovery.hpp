#pragma once

// Settings recovery, as the Go server's recovery package: bounded, authenticated settings archives
// (NKOSBACKUP1, PBKDF2-SHA256 600000 rounds, AES-256-GCM) of a fixed list of settings files and trees, and the
// preview, staging and status of a restore or reset. A staged plan (/var/lib/nanokvm-recovery/pending.json) is
// applied at the next boot by nkos-update recovery-resume; the server never applies it. Every path is opened
// component by component without following symbolic links. Blocking: call off the event loop.

#include <cstdint>
#include <functional>
#include <optional>
#include <string>
#include <vector>

namespace nkos::recovery {

inline constexpr std::size_t max_archive = 4 << 20;
inline constexpr std::size_t max_data = 2 << 20;
inline constexpr std::size_t max_file = 512 << 10;
inline constexpr std::size_t max_files = 1024;
inline constexpr const char *pending_path = "var/lib/nanokvm-recovery/pending.json";

using Error = std::string;  // empty: no error

struct File {
	std::string path;
	std::optional<std::string> data;  // nullopt: Go's nil []byte (JSON null)
	std::uint32_t mode = 0;
};

struct Archive {
	std::int64_t format = 0;
	std::string version, board;
	std::string created;  // RFC 3339 as written
	std::optional<std::vector<File>> files;
	std::optional<std::vector<std::string>> packages;
	std::string root_hash;
	// The settings groups (groups in archive.cpp) the archive covers: those of which the device had state (see
	// covered_groups). Archives made before groups existed have none. JSON omitempty: none and empty alike.
	std::vector<std::string> covers;
};

struct Options {
	std::string action, mode;  // restore or reset; device or migrate
	bool keep_users = false, keep_apps = false;
};

struct Change {
	std::string path;
	std::optional<File> before, after;
};

struct Plan {
	bool rolling_back = false, started = false;
	Options options;
	std::optional<std::vector<Change>> changes;
	std::optional<std::vector<std::string>> remove_packages;
	// The settings groups the plan may change: those the restored archive covers, or all for a reset requested now.
	// A journal without them, as a restore or reset queued by a release before groups existed, changes no file of
	// any group: neither in the plan nor when the boot pass takes inventory again. JSON omitempty.
	std::vector<std::string> covers;
	// The source world is checked again at boot: do not execute a stale removal.
	std::optional<std::string> world;

	// Whether the plan may change a settings path: one outside every group, or one in a group the plan covers.
	bool covers_path(const std::string &name) const;
};

struct Preview {
	std::string token;
	std::vector<std::string> changed, skipped, remove_packages;
	std::optional<std::vector<std::string>> packages;
	std::string version;
};

struct Status {
	std::string state, message;
};

// JSON exactly as Go's encoding/json writes these types.
std::string to_json(const File &f);
std::string to_json(const Archive &a);
std::string to_json(const Plan &p);
std::string to_json(const Preview &v);
std::string to_json(const Status &s);

Error validate(const Archive &a);
// Seal: the encrypted archive (random salt and nonce).
std::optional<std::string> seal(const Archive &a, const std::string &password, Error &error);
std::optional<Archive> open(const std::string &data, const std::string &password, Error &error);
// Open's reading of the plaintext: Go's decoding with unknown fields refused and nothing but white space after it
// ("invalid archive manifest", "trailing archive data"); not validated.
std::optional<Archive> decode_manifest(const std::string &plain, Error &error);

// The POST body of /api/system/recovery/:action as Gin binds it (json.Decoder, first value); Options as its
// UnmarshalJSON reads them (a reset needs explicit keepUsers and keepApps). False when Go's decoding fails.
struct Request {
	std::string password, archive_password, token;
	std::optional<std::string> archive;
	Options options;
};
bool decode_request(const std::string &body, Request &r);
// json.Unmarshal into Options.
bool decode_options(const std::string &json, Options &o);

struct Engine {
	std::string root = "/";
	std::string version, board;

	std::optional<Archive> snapshot(Error &error) const;
	bool prepare(const Archive *archive, const Options &o, Plan &plan, Preview &preview, Error &error) const;
	bool pending() const;
	Status status() const;
	Error stage(const Plan &plan) const;
	// Resume: applies a staged plan at early boot with the services stopped (nkos-update recovery-resume). A power
	// loss leaves the durable plan for replay; a write failure restores all previous settings. remove deletes the
	// approved APK world roots of a factory package reset (they are not undone).
	Error resume(const std::function<Error(const std::vector<std::string> &)> &remove) const;
};

// Boot: nkos-update recovery-resume: resumes a pending recovery under the recovery lock, refused while
// NanoKVM-Server runs; a package reset runs apk del and removes picoclaw and the acme.sh program tree.
Error boot();

// Live: the device (root /, the installed version, the board of /etc/kvm/hw).
Engine live();
// Available: both halves of the upgrade (nanokvm and nanokvm-base, whose boot script resumes recovery).
bool available();
// Reboot: nkos-update recovery-reboot, detached.
Error reboot();

// A settings file under root, read or written (nullptr deletes) without following links, bounded to limit bytes.
std::optional<File> read(const std::string &root, const std::string &name, std::size_t limit, Error &error);
Error write(const std::string &root, const std::string &name, const File *value);

} // namespace nkos::recovery
