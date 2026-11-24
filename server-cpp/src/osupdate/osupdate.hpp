#pragma once

// NanoKVM OS updates and software packages, as the Go server's osupdate package: the update lock shared by every
// package and image operation (/kvmapp/.os-update/lock, flock), the APK update page (status, check/install/rollback
// started in the detached nkos-update helper, which inherits the lock as fd 3), the update notice (a light check of
// the NanoKVM repositories only, with an index cache of its own), the update channel (stable or beta, the one
// official repository line), the Alpine community repository, the Software page (installed packages, search,
// updates, removal preview, operations in the helper) and the scheduled index refresh in root's crontab.
// The functions block (files, apk runs): call them off the event loop. Errors are Go's texts.

#include <cstdint>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace nkos::osupdate {

inline constexpr const char *base = "/kvmapp/.os-update";
inline constexpr const char *helper = "/usr/sbin/nkos-update";
inline constexpr const char *apk_executable = "/sbin/apk";
inline constexpr const char *apk_state_dir = "/run/nanokvm-apk-ui";
inline constexpr const char *repositories_path = "/etc/apk/repositories";
inline constexpr const char *installed_db_path = "/lib/apk/db/installed";
inline constexpr const char *world_path = "/etc/apk/world";

// An error as Go returns it: empty when there is none.
using Error = std::string;

// The update lock: held while the object lives (its descriptor), released when it is destroyed.
class UpdateLock {
public:
	UpdateLock() = default;
	explicit UpdateLock(int fd) : fd_(fd) {}
	UpdateLock(UpdateLock &&other) noexcept : fd_(std::exchange(other.fd_, -1)) {}
	UpdateLock &operator=(UpdateLock &&other) noexcept;
	UpdateLock(const UpdateLock &) = delete;
	UpdateLock &operator=(const UpdateLock &) = delete;
	~UpdateLock();
	int fd() const { return fd_; }

private:
	int fd_ = -1;
};

// Lock: the exclusive, non-blocking update lock; refused while a settings recovery is pending.
std::optional<UpdateLock> lock(Error &error);
// RecoveryLock: the same lock without the pending-recovery check (the early-boot worker).
std::optional<UpdateLock> recovery_lock(Error &error);

// The application release recorded by nanokvm (/kvmapp/version).
std::string installed_version();

// Starts a detached process in a session of its own (setsid): stdin /dev/null, stdout and stderr to output_fd
// (or /dev/null when -1), the update lock as descriptor 3 when lock_fd >= 0. Reaped in the background. The error
// is Go's exec.Cmd.Start error.
bool start_detached(const std::vector<std::string> &argv, int output_fd, int lock_fd, Error &error);

// Writes a file atomically (temporary file in the directory, mode, fsync, rename, directory fsync).
Error write_atomic(const std::string &name, const std::string &data, unsigned mode);

// ---- APK update page ----

struct ApkStatus {
	std::string state = "idle";
	std::string message, upgrades, log;
	bool reboot = false;
	std::string kernel;
};
ApkStatus get_apk_status();
std::string to_json(const ApkStatus &s);
Error start_apk(const std::string &action);

// ---- Versions and apk output ----

int compare_apk_versions(const std::string &a, const std::string &b);

struct SoftwareUpdate {
	std::string name, installed, available;
};
struct SoftwarePackage {
	std::string name, version, removal_blocked;
};
std::vector<SoftwareUpdate> parse_software_updates(const std::string &output);
std::vector<SoftwarePackage> parse_matching_packages(const std::string &output, std::string query, std::size_t max);
std::string installable_upgrades(const std::string &listing, const std::vector<std::string> &skip);
std::string pending_upgrades(const std::string &listing, const std::map<std::string, std::string> &installed);
std::string bounded_upgrades(const std::string &plan, const std::vector<std::string> &bounded);
std::vector<std::string> other_install_changes(const std::string &simulation, const std::vector<std::string> &names);
std::vector<std::string> installed_changes(const std::string &simulation, const std::vector<std::string> &reinstall);
std::map<std::string, std::string> parse_installed_db(const std::string &data);
// KeptByRemoval: the packages of names that an `apk del --simulate` does not purge (apk exits 0 and keeps a package
// that another selected package still needs, after taking it out of the world).
std::vector<std::string> kept_by_removal(const std::string &simulation, const std::vector<std::string> &names);
// Installed: the named packages that the installed database records. A database that cannot be read, or that records
// no package at all, proves nothing about the names: nullopt and error.
std::optional<std::vector<std::string>> installed(const std::vector<std::string> &names, Error &error);
std::optional<std::map<std::string, std::string>> installed_versions(Error &error);
// PackageState: the named packages that are not installed, and the broken ones among them and their dependencies.
bool package_state(const std::vector<std::string> &names, std::vector<std::string> &missing, std::vector<std::string> &broken,
                   Error &error);
std::vector<std::string> stable_package_targets(const std::string &output, Error &error);
std::vector<std::string> tpu_dependents(const std::vector<std::string> &targets, const std::string &installed_db);
void upgrade_selections(std::vector<std::string> &skip, std::vector<std::string> &bounded);
std::vector<std::string> nanokvm_file_pins();

// ---- Notice ----

struct UpdateNotice {
	bool enabled = false;
	std::string state = "never";
	std::int64_t checked_at = 0, attempt_at = 0;
	std::string message;
	std::optional<std::vector<SoftwareUpdate>> updates;  // nullopt: Go's nil slice
};
UpdateNotice get_notice();
std::string to_json(const UpdateNotice &n);
bool notice_enabled();
Error set_notice_enabled(bool enabled);
// CheckUpdates: refresh the NanoKVM index when network, then compare with the installed packages.
Error check_updates(bool network);
bool notice_due(const UpdateNotice &n, std::int64_t now);
// RunNoticeSchedule: two minutes after the start, then every ten minutes, a check when one is due. Starts a thread.
void start_notice_schedule();

struct Component {
	std::string name, installed, available;
};
struct Components {
	std::vector<Component> packages;
	std::string alpine, kernel, boot_kernel;
};
Components get_components();
std::string to_json(const Components &c);
std::vector<Component> installed_components();

// ---- Channel ----

struct ChannelStatus {
	std::string channel = "custom";
	bool available = false, rollback_available = false;
};
ChannelStatus get_channel();
std::string to_json(const ChannelStatus &c);
Error set_channel(const std::string &channel);
// channelLine: the index of the official repository line (-1 when none) and its Alpine branch.
int channel_line(const std::string &data, std::string &branch, ChannelStatus &status);

// ---- Community repository ----

std::string community_url(const std::string &repositories);
bool has_community(const std::string &repositories, const std::string &url);
std::string with_community(const std::string &repositories, const std::string &url);
// EnableCommunityLocked: the caller holds lock().
Error enable_community_locked();

// ---- Software page ----

struct SoftwareStatus {
	std::string state = "idle";
	std::string action, package, message, log;
};
struct SoftwareIndexStatus {
	std::string state = "missing";
	std::int64_t updated_at = 0, age_seconds = 0;
	int repositories = 0, cached = 0;
};
SoftwareStatus get_software_status();
std::string to_json(const SoftwareStatus &s);
SoftwareIndexStatus get_software_index_status();
std::string to_json(const SoftwareIndexStatus &s);
std::optional<std::vector<SoftwarePackage>> list_installed_software(Error &error);
std::string to_json(const std::vector<SoftwarePackage> &packages);
// SearchSoftware: one search at a time, the newest (a newer search ends this one with search_superseded_error);
// gone (may be empty) is polled and ends the search when the request went away.
std::optional<std::vector<SoftwarePackage>> search_software(const std::string &query, std::function<bool()> gone,
                                                            Error &error);
extern const char *const search_superseded_error;
std::optional<std::vector<SoftwareUpdate>> list_software_updates(Error &error);
std::string to_json(const std::vector<SoftwareUpdate> &updates);
std::optional<std::string> preview_software_removal(const std::string &name, Error &error);
Error start_software(const std::string &action, const std::string &name);
// RunSoftware in the server (apkpkg.Run): apk runs with the server's stdout and stderr; the caller holds lock().
Error run_software(const std::string &action, const std::string &name);
Error check_removable(const std::string &name);
bool valid_package_name(const std::string &name);

// ---- Index schedule ----

struct IndexSchedule {
	bool enabled = false;
	std::string time = "03:00";
};
std::string to_json(const IndexSchedule &s);
// GetIndexSchedule: the schedule of root's crontab (03:00, off, when it has none); with error set, what Go returns
// along with the error (the zero schedule, or the part read before a bad entry).
IndexSchedule get_index_schedule(Error &error);
Error set_index_schedule(const IndexSchedule &s);
std::string index_schedule_timezone();
IndexSchedule parse_index_schedule(const std::string &data, Error &error);
std::optional<std::string> render_index_schedule(const std::string &data, const IndexSchedule &s, Error &error);
// root's crontab is shared with Scheduled tasks (system/scripts.cpp): both must hold this lock for a
// read-modify-write of /etc/crontabs/root.
std::mutex &crontab_mutex();

// ---- The nkos-update helper's work ----

// RunAPK: check, install or rollback (or reboot) with the update lock already held; the state is saved for the
// page, a failure as "failed" with its message.
Error run_apk(const std::string &action);
// RunScheduledIndexRefresh: the cron job's index refresh when the schedule is on, logged like a manual one.
Error run_scheduled_index_refresh();

// ---- Rollback recovery ----

bool rollback_constraint_installed();
Error recover_interrupted_rollback();

} // namespace nkos::osupdate
