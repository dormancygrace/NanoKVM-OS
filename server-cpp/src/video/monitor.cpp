#include "video/monitor.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "config/config.hpp"
#include "notify/notify.hpp"
#include "video/screen.hpp"
#include "video/vision.hpp"

#include <kvm_vision.h>

#include <glaze/glaze.hpp>

#include <algorithm>
#include <cerrno>
#include <csignal>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <format>
#include <fstream>
#include <map>
#include <mutex>
#include <set>
#include <sstream>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::video::monitor {

namespace {

constexpr const char *edid_dir = "/usr/share/nanokvm/edid";
constexpr const char *resolution_file = "/etc/kvm/monitor_resolution";
constexpr const char *portrait_file = "/etc/kvm/monitor_portrait";
constexpr const char *portrait_resolution_file = "/etc/kvm/monitor_portrait_resolution";
constexpr const char *identity_file = "/etc/kvm/monitor_identity";
constexpr const char *hash_file = "/etc/kvm/monitor_edid_sha256";
constexpr const char *power_cycle_file = "/etc/kvm/monitor_power_cycle_pending";
constexpr const char *verified_file = "/etc/kvm/monitor_edid_verified";
constexpr const char *system_version_file = "/kvmapp/version";
// The written profile when an identity is set: nanokvm_update_edid takes a file.
constexpr const char *identity_profile_file = "/run/nanokvm-monitor-edid.bin";
constexpr const char *update_tool = "/usr/sbin/nanokvm_update_edid";
constexpr const char *write_skipped = "Requested EDID already installed; flash write skipped";
// A monitor update saved for the boot after a video memory switch (or by the retired portrait migration): the first
// start programs it.
constexpr const char *deferred_file = "/etc/kvm/monitor_edid_deferred";
// Set once a start found the receiver holding the recorded profile, or could not read it: later starts of the same
// boot do not reset the receiver again.
constexpr const char *receiver_checked_file = "/run/nanokvm-monitor-edid-checked";
// nanokvm_update_edid keeps it from before an LT6911D write on a Cube or Lite until the software replug after it.
constexpr const char *replug_pending_file = "/etc/kvm/monitor_edid_replug_pending";
// Set once the first start found a Cube receiver EDID within 720p/1080p at 60 Hz.
constexpr const char *edid_checked_file = "/etc/kvm/monitor_edid_checked";
// Set once the first start found a Cube or Lite receiver EDID declaring the RGB range selectable, or programmed a
// profile that does.
constexpr const char *rgb_range_checked_file = "/etc/kvm/monitor_rgb_range_checked";
// The retired Windows absolute-pointer profile: its marker and USB ContainerID.
constexpr const char *retired_pointer_marker = "/boot/usb.pointer_windows";
constexpr const char *retired_container_id_file = "/etc/kvm/usb_container_id";
// Set in the environment of the process that replaced itself after lowering the profile: it does not again.
constexpr const char *fit_restart_marker = "NKOS_MONITOR_FIT_RESTARTED";
// 1920x1080 and 1080x1920 at 100 Hz, which earlier releases programmed (now 90 Hz).
const std::set<std::string> retired_profiles = {
	"8e90f013b8d93be6936bbcdaa324640ad9de03dd877891732c9f4daa2b95ca8a",  // NanoKVM-monitor-1080-100.bin, also Auto
	"2d7fd4976ab726052bcb3a17b46951f8a5d0516e01816a0061a5d98d91087bb5",  // NanoKVM-portrait-1080x1920-100.bin
};

// The whole file, nullopt on any error (os.ReadFile: a directory is an error too).
std::optional<std::string> read(const std::string &path)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return std::nullopt;
	std::string data;
	char buf[4096];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			int e = errno;
			::close(fd);
			errno = e;
			return std::nullopt;
		}
		if (n == 0) break;
		data.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	return data;
}

std::string trim(const std::string &s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

bool regular(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode);
}

bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

// Read once per value the scripts publish before the server starts; anything unknown is read again.
std::string identity_value(const char *path, std::initializer_list<const char *> known)
{
	static std::map<std::string, std::string> cache;
	static std::mutex mutex;  // the device thread reads it too
	std::lock_guard lock(mutex);
	if (auto it = cache.find(path); it != cache.end()) return it->second;
	std::string v = trim(read(path).value_or(""));
	for (auto *k : known)
		if (v == k) cache[path] = v;
	return v;
}

edid::Hardware hardware() { return {board(), receiver_chip()}; }

std::optional<edid::Identity> saved_identity()
{
	auto data = read(identity_file);
	if (!data) return std::nullopt;
	edid::Identity id;
	if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(id, *data); e) {
		log::warn("ignoring the saved monitor identity: {}", glz::format_error(e, *data));
		return std::nullopt;
	}
	if (auto e = edid::validate(id)) {
		log::warn("ignoring the saved monitor identity: {}", *e);
		return std::nullopt;
	}
	return id;
}

// A profile as it is written: the file itself, or with the identity.
std::optional<std::string> profile_bytes(const std::string &path, const std::optional<edid::Identity> &id, std::string *error = nullptr)
{
	auto data = read(path);
	if (!data) {
		if (error) *error = "cannot read " + path;
		return std::nullopt;
	}
	if (!id) return data;
	return edid::with_identity(*data, *id, error);
}

std::optional<std::string> profile_sha256(const std::string &path, const std::optional<edid::Identity> &id)
{
	auto data = profile_bytes(path, id);
	if (!data) return std::nullopt;
	return edid::sha256_hex(*data);
}

bool programmed(const std::string &path, std::uint16_t height)
{
	auto id = saved_identity();
	auto want = profile_sha256(path, id);
	if (!want) return false;
	auto recorded = read(hash_file);
	if (!recorded) {
		// A Cube or Lite without a recorded write has an unknown EDID: its rate is not inferred.
		if (requires_power_cycle()) return false;
		auto legacy = profile_sha256(edid::profile_path(hardware(), edid_dir, height), id);
		return legacy && *legacy == *want;
	}
	return trim(*recorded) == *want;
}

std::string rate_profile_path(std::uint16_t id, int rate) { return edid::rate_profile_path(edid_dir, id, rate); }

// Writes a monitor file through a temporary file and a rename, owner-only as the Go server's temporary files left it.
bool write_file(const std::string &path, const std::string &value) { return config::atomic_write(path, value, 0600); }

// An OS error as Go words it ("no such file or directory").
std::string os_error(int e)
{
	std::string s = std::strerror(e);
	if (!s.empty()) s[0] = static_cast<char>(std::tolower(static_cast<unsigned char>(s[0])));
	return s;
}

// How a failed command ended, as Go's exec errors say it.
std::string exit_text(const process::Result &r)
{
	if (!r.started) return r.error;
	if (r.exit_code >= 0) return std::format("exit status {}", r.exit_code);
	return r.signal == SIGKILL ? "signal: killed" : std::format("signal: {}", strsignal(r.signal));
}

// errors.Join of two messages.
std::string join(const std::string &a, const std::string &b)
{
	if (a.empty()) return b;
	if (b.empty()) return a;
	return a + "\n" + b;
}

std::string verified_tag(const std::string &hash)
{
	return trim(hash + " " + trim(read(system_version_file).value_or("")));
}

// Records the hash of the EDID just written; nanokvm_update_edid reads the receiver back after writing it.
void record_profile(const std::string &data)
{
	std::string sum = edid::sha256_hex(data);
	if (!write_file(hash_file, sum + "\n")) {
		std::remove(hash_file);
		return;
	}
	if (!write_file(verified_file, verified_tag(sum) + "\n")) log::warn("cannot record the HDMI receiver EDID check");
}

// Writes a profile with an identity (nullopt: as shipped) and records it (applyMonitorProfileLocked). Device thread,
// the capture paused.
std::string apply_profile(const std::string &path, const std::optional<edid::Identity> &id)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0) return std::format("monitor profile is unavailable: stat {}: {}", path, os_error(errno));
	if (!S_ISREG(st.st_mode)) return "monitor profile is unavailable: " + path + " is not a regular file";
	std::string error;
	auto data = profile_bytes(path, id, &error);
	if (!data) return "monitor profile is unavailable: " + error;
	std::string target = path;
	if (id) {
		target = identity_profile_file;
		std::ofstream out(target, std::ios::binary | std::ios::trunc);
		if (!(out << *data)) return "preparing the monitor profile failed";
		::chmod(identity_profile_file, 0600);
	}
	struct Cleanup {
		bool active;
		~Cleanup()
		{
			if (active) std::remove(identity_profile_file);
		}
	} cleanup{id.has_value()};

	if (vision::closed()) return "video capture is closed";
	bool cube = requires_power_cycle();
	if (kvmv_edid_maintenance(1) != 0) return "cannot pause HDMI for monitor change";
	std::vector<std::string> argv = {update_tool, target};
	bool pending_before = false;
	std::string failure;
	if (cube) {
		pending_before = exists(power_cycle_file);
		// The marker is durable before writing: even a timeout or a crash can leave the receiver flash changed.
		if (!write_file(power_cycle_file, "1\n")) failure = "cannot record the power-cycle reminder";
		argv = {update_tool, "--accept-power-cycle", target};
	}
	if (failure.empty()) {
		auto r = process::run(argv, {.timeout = std::chrono::seconds(90)});
		if (!r.ok()) {
			log::error("monitor EDID programming failed: {}: {}", exit_text(r), r.output);
			failure = "monitor EDID programming failed";
		} else if (cube && !pending_before && r.output.find(write_skipped) != std::string::npos) {
			// The receiver already held this EDID: no power cycle is needed for it.
			if (std::remove(power_cycle_file) != 0 && errno != ENOENT)
				log::warn("cannot clear the power-cycle reminder: remove {}: {}", power_cycle_file, os_error(errno));
		}
	}
	if (kvmv_edid_maintenance(0) != 0) failure = join(failure, "cannot restore HDMI after monitor change");
	if (!failure.empty()) return failure;
	record_profile(*data);
	return "";
}

bool portrait_resolution_supported(std::uint16_t resolution, const VideoPool &pool)
{
	if (resolution == portrait_hd)
		return portrait_supported_for(pool) && regular(std::string(edid_dir) + "/NanoKVM-portrait-720x1280.bin");
	if (resolution == portrait_default) return portrait_supported_for(pool);
	if (resolution == portrait_max) return portrait_max_supported_for(pool);
	return false;
}

std::string persist_identity(const std::optional<edid::Identity> &id)
{
	if (!id) {
		if (std::remove(identity_file) != 0 && errno != ENOENT) return "clearing the monitor identity failed";
		return "";
	}
	std::string out;
	(void)glz::write<glz::opts{.skip_null_members = false}>(*id, out);
	if (!write_file(identity_file, out + "\n")) return "saving the monitor identity failed";
	return "";
}

// applyMonitorSettingsLocked: the final state from the update and the saved settings, one write at most, then the
// saved settings (only after a successful write).
std::string apply_locked(const Settings &update, int fps)
{
	bool explicit_change = update.resolution || update.portrait || update.portrait_resolution || update.set_identity;
	bool refresh = update.sync_refresh && refresh_follows_stream();
	if (!explicit_change && !refresh) return "";
	if (!profile_supported()) return "EDID programming is unsupported on this board/HDMI chip";
	auto hw = hardware();
	std::uint16_t height = selected_resolution();
	bool portrait = portrait_enabled();
	std::uint16_t resolution = portrait_resolution();
	if (update.resolution) {
		height = *update.resolution;
		if (!edid::resolution_map.count(height) && !edid::wider_profile(height)) return "unsupported monitor resolution";
		if (edid::wider_profile(height)) {
			if (edid::wider_rates_for(hw, height).empty())
				return "this HDMI receiver does not take that 16:10 or 21:9 monitor profile";
		} else if (!hw.high_refresh() && height != 0 && height != 720 && height != 1080) {
			return "HDMI receiver EDID profiles are limited to 720p/1080p at 60 Hz";
		}
	}
	if (update.portrait) portrait = *update.portrait;
	if (update.portrait_resolution) resolution = *update.portrait_resolution;
	if (portrait || update.portrait_resolution) {
		if (!portrait_resolution_supported(resolution, booted_video_pool())) return "portrait monitor profile is unavailable";
	}
	auto identity = saved_identity();
	if (update.set_identity) {
		identity = update.identity;
		if (identity)
			if (auto e = edid::validate(*identity)) return *e;
	}
	bool write = update.resolution || update.portrait || (portrait && update.portrait_resolution) || update.set_identity;
	if (write || refresh) {
		auto path = portrait ? edid::portrait_profile_path_at(hw, edid_dir, resolution, fps, regular)
		                     : edid::profile_path_at(hw, edid_dir, height, fps, regular);
		if (write || !programmed(path, height))
			if (auto e = apply_profile(path, identity); !e.empty()) return e;
	}
	if (update.set_identity)
		if (auto e = persist_identity(identity); !e.empty()) return e;
	if (update.resolution && !write_file(resolution_file, std::to_string(height)))
		return "saving monitor resolution failed";
	if (update.portrait_resolution && !write_file(portrait_resolution_file, std::format("{}\n", resolution)))
		return "saving the portrait resolution failed";
	if (update.portrait && !write_file(portrait_file, portrait ? "1\n" : "0\n")) return "saving the portrait setting failed";
	return "";
}

// --- the start-time upkeep (main.go's goroutine), device thread -----------------------------------------------

std::string base(const std::string &path) { return std::filesystem::path(path).filename().string(); }

// A stat that failed for another reason than a missing file.
bool unreadable(const char *path)
{
	struct stat st{};
	return ::stat(path, &st) == 0 || errno != ENOENT;
}

// The profile a saved 1296x2304 portrait becomes: 1080x1920 for a device that encodes H.264, which cannot encode a
// taller portrait, else 1440x2560.
std::uint16_t retired_portrait_replacement()
{
	std::string codec;
	for (const char *path : {"/etc/kvm/encoder_codec", "/kvmapp/kvm/type"}) {
		auto data = read(path);
		if (!data) continue;
		codec = trim(*data);
		for (auto &c : codec) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
		if (!codec.empty()) break;
	}
	return codec == "h264" ? portrait_default : portrait_max;
}

// The installed profiles (NanoKVM-*.bin), sorted.
std::vector<std::string> shipped_files()
{
	std::vector<std::string> files;
	std::error_code ec;
	for (auto &entry : std::filesystem::directory_iterator(edid_dir, ec)) {
		std::string name = entry.path().filename().string();
		if (name.size() >= 12 && name.starts_with("NanoKVM-") && name.ends_with(".bin")) files.push_back(entry.path().string());
	}
	std::sort(files.begin(), files.end());
	return files;
}

// The installed profile with this hash as written (with the saved identity), "" for none.
std::string shipped_profile(const std::string &hash)
{
	auto id = saved_identity();
	for (auto &path : shipped_files())
		if (auto sum = profile_sha256(path, id); sum && *sum == hash) return path;
	return "";
}

// The installed profile whose earlier version, without the RGB range block, has this hash: the receiver holds it as
// an earlier release programmed it. Same resolution and rate, so the current version replaces it.
std::string rgb_range_successor(const std::string &hash)
{
	if (!shipped_profile(hash).empty()) return "";  // a current profile, such as the stock one
	for (auto &path : shipped_files()) {
		auto data = read(path);
		if (!data) continue;
		if (auto old = edid::without_rgb_range(*data); old && edid::sha256_hex(*old) == hash) return path;
	}
	return "";
}

// A shipped profile with this hash for the log; several files can hold the same EDID, the monitor and portrait
// names say most.
std::string held_profile_name(const std::string &hash)
{
	auto id = saved_identity();
	std::vector<std::string> names;
	for (auto &path : shipped_files())
		if (auto sum = profile_sha256(path, id); sum && *sum == hash) names.push_back(base(path));
	if (names.empty()) return "an EDID that is not a NanoKVM profile";
	for (auto &name : names)
		if (name.starts_with("NanoKVM-monitor-") || name.starts_with("NanoKVM-portrait-")) return name;
	return names[0];
}

std::string mark(const char *path)
{
	return write_file(path, "1\n") ? "" : std::format("{}: {}", path, os_error(errno));
}

// The receiver's EDID (256 bytes), read by nanokvm_update_edid with the register sequence programming uses to
// compare the installed EDID. The capture is paused.
std::string read_receiver_edid(std::string &edid)
{
	edid.clear();
	if (vision::closed()) return "video capture is closed";
	if (kvmv_edid_maintenance(1) != 0) return "cannot pause HDMI to read the monitor EDID";
	std::string error;
	char name[] = "/run/nanokvm-edid-XXXXXX.bin";
	int fd = ::mkstemps(name, 4);
	if (fd < 0) {
		error = std::format("open /run/nanokvm-edid-*.bin: {}", os_error(errno));
	} else {
		::close(fd);
		auto r = process::run({update_tool, "--dump", name}, {.timeout = std::chrono::seconds(30)});
		if (!r.ok()) {
			log::error("reading the monitor EDID failed: {}: {}", exit_text(r), r.output);
			error = "reading the monitor EDID failed";
		} else if (auto data = read(name); !data) {
			error = std::format("open {}: cannot read", name);
		} else if (data->size() != 256) {
			error = std::format("monitor EDID copy has {} bytes", data->size());
		} else {
			edid = std::move(*data);
		}
		std::remove(name);
	}
	if (kvmv_edid_maintenance(0) != 0) {
		edid.clear();
		error = join(error, "cannot restore HDMI after reading the monitor EDID");
	}
	return error;
}

// The receiver holds the recorded profile under this system version ("<sha256> <version>").
bool receiver_verified(const std::string &hash)
{
	auto data = read(verified_file);
	return data && trim(*data) == verified_tag(hash);
}

// The recorded profile is only what this server last wrote; a tool or another system can rewrite the receiver's
// flash. Reads the receiver (about 1 s on PCIe, with a receiver reset around the read) on the first start after an
// installation or an update of the system, at most once per boot, and never while HDMI capture is disabled (the read
// would release the receiver from reset). False when it holds another EDID; a failed read changes nothing.
bool receiver_holds(bool read_at_start, const std::string &hash)
{
	if (!read_at_start || receiver_verified(hash)) return true;
	if (exists(receiver_checked_file)) return true;
	auto checked = [] {
		int fd = ::open(receiver_checked_file, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
		if (fd < 0) {
			log::warn("cannot record the HDMI receiver EDID check: open {}: {}", receiver_checked_file, os_error(errno));
			return;
		}
		::close(fd);
	};
	std::string edid;
	std::string error = read_receiver_edid(edid);
	if (error.empty() && !edid::edid_valid(edid)) error = "invalid EDID header or checksum";
	if (!error.empty()) {
		log::warn("cannot check the HDMI receiver EDID: {}", error);
		checked();
		return true;
	}
	// The copy is 256 bytes; a one-block profile file is 128.
	std::string held = edid::sha256_hex(edid.substr(0, (static_cast<std::size_t>(static_cast<unsigned char>(edid[126])) + 1) * 128));
	if (held == hash) {
		checked();
		if (!write_file(verified_file, verified_tag(hash) + "\n"))
			log::warn("cannot record the HDMI receiver EDID check: {}", os_error(errno));
		return true;
	}
	log::warn("the HDMI receiver holds {}, not the recorded profile; programming the saved one", held_profile_name(held));
	return false;
}

// A Cube or Lite receiver keeps its EDID in flash across reinstalls and needs a power cycle after a write, so the
// first start programs it only for a reason found by reading it once: an EDID advertising modes an LT6911C cannot
// capture (left by another system or by an image that took the board for PCIe), or one without the RGB range
// declaration (the source then sends limited-range RGB, which the receiver reads as full range: a washed-out
// picture). An LT6911C gets the saved Cube profile here; for an LT6911UXC, which takes every profile, rgb_missing
// asks the caller to program the saved profile and then to mark the range checked.
std::string check_cube(bool read_at_start, std::string &path, bool &rgb_missing)
{
	bool modes_checked = exists(edid_checked_file) || receiver_chip() == "ux";
	bool range_checked = exists(rgb_range_checked_file);
	// With HDMI capture disabled, a read would release the receiver from reset; the check waits for a start with it.
	if ((modes_checked && range_checked) || !read_at_start) return "";
	std::string edid;
	std::string error = read_receiver_edid(edid);
	if (error.empty() && !edid::edid_valid(edid)) error = "invalid EDID header or checksum";
	if (!error.empty()) return "checking the Cube receiver EDID: " + error;
	auto beyond = edid::beyond_cube(edid);
	std::string mode;
	if (!modes_checked && beyond) {
		mode = "advertises " + *beyond + ", beyond the Cube's 720p/1080p at 60 Hz";
	} else if (range_checked || edid::declares_rgb_range(edid)) {
		std::string e;
		if (!range_checked) e = mark(rgb_range_checked_file);
		if (!modes_checked) e = join(e, mark(edid_checked_file));
		return e;
	} else if (receiver_chip() == "ux") {
		log::warn("the HDMI receiver EDID does not declare the RGB range selectable, so sources send limited-range RGB "
		          "and the picture is washed out; the saved profile will be programmed and take effect after the next "
		          "power cycle");
		rgb_missing = true;
		return "";
	} else {
		mode = "does not declare the RGB range selectable, so sources send limited-range RGB and the picture is washed out";
	}
	std::uint16_t height = selected_resolution();
	if (height != 720 && height != 1080 && !edid::wider_profile(height)) height = 0;  // Auto: the Cube's 1080p profile
	// A 16:10 or 21:9 profile the receiver takes (saved only then) stays.
	std::string target = edid::wider_profile(height) ? edid::profile_path(hardware(), edid_dir, height)
	                                                 : edid::cube_profile_path(edid_dir, height);
	log::warn("the HDMI receiver EDID {}; programming {}, which takes effect after the next power cycle", mode, target);
	if (auto e = apply_profile(target, saved_identity()); !e.empty()) return e;
	path = target;
	if (height != selected_resolution() && !write_file(resolution_file, std::to_string(height)))
		return "saving monitor resolution failed: " + os_error(errno);
	// Every Cube profile declares the RGB range.
	return join(mark(edid_checked_file), mark(rgb_range_checked_file));
}

// The profile file the saved setting asks for at the stream rate.
std::string saved_profile_path(int fps)
{
	if (portrait_enabled()) return edid::portrait_profile_path_at(hardware(), edid_dir, portrait_resolution(), fps, regular);
	return edid::profile_path_at(hardware(), edid_dir, selected_resolution(), fps, regular);
}

// The old default stream rate of 100 fps becomes 90, with the retired 100 Hz profiles.
std::string retire_stream_rate(int &fps)
{
	if (fps != 100) return "";
	if (!config::atomic_write("/kvmapp/kvm/fps", "90", 0600)) return "saving the stream rate failed: " + os_error(errno);
	fps = 90;
	loop::post([] { set_screen("fps", 90); });
	return "";
}

// MigrateRetiredPortraitResolution: a saved 1296x2304 portrait becomes its successor, once. With portrait on the
// receiver still advertises 1296x2304, which the capture no longer takes: the request is recorded first (deferred
// monitor request), so the first-start step programs it and a failed write is retried at the next start.
bool migrate_retired_portrait(std::string &error)
{
	if (read_video_value(portrait_resolution_file) != edid::retired_portrait) return false;
	if (portrait_enabled() && !write_file(deferred_file, "1\n")) {
		error = "requesting the portrait profile: " + os_error(errno);
		return false;
	}
	std::uint16_t replacement = retired_portrait_replacement();
	if (!write_file(portrait_resolution_file, std::format("{}\n", replacement))) {
		error = "saving the portrait profile: " + os_error(errno);
		return false;
	}
	log::info("the 1296x2304 portrait profile is retired; the portrait profile is now {}x{}", portrait_width(replacement), replacement);
	return true;
}

// FitMonitorToVideoMemory: a saved profile the booted video memory cannot capture is lowered, so the device never
// advertises it (above 2560x1440 to 1440p, or to 1080p without QHD; above 1920x1088 to 1080p; an unsupported
// portrait monitor is turned off). True when the EDID was reprogrammed.
bool fit_to_video_memory(int fps, std::string &error)
{
	VideoPool pool = booted_video_pool();
	Settings update;
	std::string need = pool_need(selected_resolution());
	if ((need == "uhd" && !pool.uhd()) || (need == "qhd" && !pool.qhd()))
		update.resolution = static_cast<std::uint16_t>(need == "uhd" && pool.qhd() ? 1440 : 1080);
	if (portrait_enabled() && fits_video_pool(State{0, true, portrait_resolution()}, pool)) update.portrait = false;
	// Without programmable EDID the receiver never advertised the profile.
	if ((!update.resolution && !update.portrait) || !profile_supported()) return false;
	error = apply_locked(update, fps);
	return error.empty();
}

// RetireWindowsPointer: a device updated with the retired Windows absolute-pointer profile on still has its marker
// and USB ContainerID, and its receiver the recorded profile with a Microsoft block carrying that ContainerID. The
// recorded profile is programmed as shipped (only a PCIe board with an LT6911UXC carried the block), then the
// marker and the ContainerID go; the marker stays until the write succeeds.
std::string retire_windows_pointer()
{
	if (!unreadable(retired_pointer_marker)) return "";
	if (profile_supported() && refresh_follows_stream()) {
		std::string path;
		if (auto recorded = read(hash_file)) {
			std::string hash = trim(*recorded);
			path = shipped_profile(hash);
			// Written before the RGB range block: its current version, so the first start has nothing left to write.
			if (path.empty()) path = rgb_range_successor(hash);
		}
		if (path.empty()) {
			log::info("the recorded monitor profile is not shipped; the HDMI receiver keeps the retired Windows pointer "
			          "block until the next profile change");
		} else {
			if (auto e = apply_profile(path, saved_identity()); !e.empty()) return e;
			log::info("programmed {} without the retired Windows pointer block", base(path));
		}
	}
	for (const char *name : {retired_pointer_marker, retired_container_id_file})
		if (std::remove(name) != 0 && errno != ENOENT) return std::format("remove {}: {}", name, os_error(errno));
	return "";
}

// ProgramMonitorProfileOnFirstStart: the receiver keeps its EDID in its own flash, which survives a reinstall (one
// that advertised 2560x1440 kept doing so under a new system in the FHD video memory mode). Until a profile is
// recorded, which one the receiver holds is unknown: the saved one is programmed (nanokvm_update_edid skips the flash
// write when it is installed already). With a recorded profile, the first start after an installation or an update
// reads the receiver and programs the saved profile again only when it holds another EDID; a Cube or Lite is checked
// once instead (check_cube). A deferred request (a video memory switch, the retired portrait) is always programmed.
// path: the programmed profile, "" when nothing was written. fps: the stream rate, 90 after a retired 100 Hz profile.
std::string program_on_first_start(bool read_at_start, int &fps, std::string &path)
{
	path.clear();
	if (!profile_supported()) return "";
	bool deferred = false, rgb_range = false;
	if (auto data = read(deferred_file)) {
		if (trim(*data) != "1") return "invalid deferred monitor request";
		deferred = true;
	} else if (unreadable(deferred_file)) {
		return std::format("reading deferred monitor request: open {}: {}", deferred_file, os_error(errno ? errno : EACCES));
	}
	// A confirmed memory switch is an explicit request, unlike first-install maintenance: it finishes on a Cube or
	// Lite too. A write whose software replug did not complete (an LT6911D on a Cube or Lite) is applied again: the
	// EDID tool then replugs without rewriting flash.
	if (read_at_start && receiver_chip() == "d" && exists(replug_pending_file)) {
		log::info("the last HDMI reconnect after an EDID write did not complete; applying the profile again");
	} else if (!deferred && requires_power_cycle()) {
		// Before the recorded-profile check: an image that took a Cube for PCIe recorded the 100 Hz profile it wrote.
		bool missing = false;
		auto e = check_cube(read_at_start, path, missing);
		if (!path.empty() || !e.empty() || !missing) return e;
		rgb_range = true;
	} else if (!deferred) {
		if (auto recorded = read(hash_file)) {
			std::string hash = trim(*recorded);
			if (retired_profiles.count(hash)) {
				if (auto e = retire_stream_rate(fps); !e.empty()) return e;
				log::info("the HDMI receiver holds a retired 1080p 100 Hz profile; programming the current one");
			} else if (auto successor = rgb_range_successor(hash); !successor.empty()) {
				log::info("the HDMI receiver holds {} without the RGB range declaration; programming the current one", base(successor));
				if (auto e = apply_profile(successor, saved_identity()); !e.empty()) return e;
				path = successor;
				return "";
			} else if (receiver_holds(read_at_start, hash)) {
				// Enough only while that is still what the saved setting asks for (a release may map a setting to
				// another profile, such as a faster rate for an LT6911D).
				auto want = profile_sha256(saved_profile_path(fps), saved_identity());
				if (!want || *want == hash) return "";
				log::info("the HDMI receiver holds an earlier profile for this setting; programming the current one");
			}
		} else if (unreadable(hash_file)) {
			return "";
		}
	}
	// Fitting may have failed, or the saved state changed since: never advertise a profile this boot cannot capture.
	if (auto e = fits_video_pool(state_after({}), booted_video_pool())) return *e;
	if (portrait_enabled() && !portrait_resolution_supported(portrait_resolution(), booted_video_pool()))
		return "portrait monitor profile is unavailable";
	std::string target = saved_profile_path(fps);
	// On failure no profile is recorded, so the next start tries again.
	if (auto e = apply_profile(target, saved_identity()); !e.empty()) return e;
	path = target;
	if (deferred && std::remove(deferred_file) != 0 && errno != ENOENT)
		return std::format("clearing deferred monitor request: remove {}: {}", deferred_file, os_error(errno));
	if (rgb_range) return mark(rgb_range_checked_file);
	return "";
}

// The steps after the fit: they also run when the restart failed.
void finish_start(bool hdmi_disabled, int fps)
{
	if (auto e = retire_windows_pointer(); !e.empty()) log::error("failed to retire the Windows pointer profile: {}", e);
	// After a fit the profile is recorded and this does nothing.
	std::string path;
	if (auto e = program_on_first_start(!hdmi_disabled, fps, path); !e.empty()) {
		log::error("failed to program the monitor profile on the first start: {}", e);
	} else if (!path.empty()) {
		log::info("programmed the monitor profile {} on the first start", path);
		// Nobody watched this write: a Cube or Lite takes the new EDID, such as the RGB range that corrects pale
		// colours, only after a power cycle.
		if (power_cycle_pending())
			notify::emit({.kind = std::string(notify::kind_hdmi), .severity = notify::Severity::warning, .message = "hdmi.power-cycle"});
	}
}

} // namespace

std::string board() { return identity_value("/etc/kvm/hw", {"alpha", "beta", "pcie"}); }
std::string receiver_chip() { return identity_value("/etc/kvm/hdmi_version", {"c", "ux", "d", "ue"}); }

std::string receiver_model()
{
	std::string c = receiver_chip();
	return c == "c" ? "LT6911C" : c == "ux" ? "LT6911UXC" : c == "d" ? "LT6911D" : "";
}

bool profile_supported() { return hardware().profile_supported(); }
bool high_refresh_supported() { return hardware().high_refresh(); }
bool requires_power_cycle() { return hardware().power_cycle(); }
bool power_cycle_pending() { return exists(power_cycle_file) && requires_power_cycle(); }
bool refresh_follows_stream() { return hardware().follows_refresh(); }

std::pair<std::uint16_t, std::uint16_t> size(std::uint16_t id) { return edid::size(id); }

std::string aspect(std::uint16_t id)
{
	if (auto it = edid::wider.find(id); it != edid::wider.end()) return it->second.aspect;
	return "16:9";
}

bool valid_profile(std::uint16_t id) { return edid::valid_profile(id); }
bool wider_profile(std::uint16_t id) { return edid::wider_profile(id); }
bool wider_profile_offered(std::uint16_t id) { return !edid::wider_rates_for(hardware(), id).empty(); }
std::uint16_t auto_height() { return edid::auto_height(hardware()); }
std::vector<int> rates(std::uint16_t height) { return edid::monitor_rates(hardware(), height); }
std::string pool_need(std::uint16_t id) { return edid::pool_need(id); }

std::uint16_t selected_resolution()
{
	int value = read_video_value(resolution_file);
	if (value < 0 || value > 65535) return 0;
	auto h = static_cast<std::uint16_t>(value);
	if (!edid::resolution_map.count(h) && !wider_profile(h)) return 0;
	// A 16:10 or 21:9 id the receiver does not take means Auto (a moved card).
	if (wider_profile(h) && !wider_profile_offered(h)) return 0;
	return h;
}

bool portrait_enabled()
{
	std::string v = trim(read(portrait_file).value_or(""));
	for (auto &c : v) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return v == "1" || v == "true" || v == "yes" || v == "on";
}

bool portrait_supported_for(const VideoPool &p)
{
	bool stride = trim(read("/sys/module/cv181x_vi/parameters/yuv_bypass_aligned_stride").value_or("")) == "Y";
	return high_refresh_supported() && stride && profile_supported() && p.qhd() &&
	       regular(std::string(edid_dir) + "/NanoKVM-portrait-1080x1920.bin");
}

bool portrait_max_supported_for(const VideoPool &p)
{
	return high_refresh_supported() && profile_supported() && p.mib >= 54 &&
	       regular(std::string(edid_dir) + "/NanoKVM-portrait-1440x2560.bin");
}

bool portrait_supported() { return portrait_supported_for(booted_video_pool()); }
bool portrait_max_supported() { return portrait_max_supported_for(booted_video_pool()); }
bool portrait_supported_for_shared_pool() { return portrait_supported_for(VideoPool{144, true}); }
bool portrait_max_supported_for_shared_pool() { return portrait_max_supported_for(VideoPool{144, true}); }

std::uint16_t portrait_resolution()
{
	int value = read_video_value(portrait_resolution_file);
	if (value < 0 || value > 65535) return portrait_default;
	auto r = static_cast<std::uint16_t>(value);
	// Until the start has rewritten the file.
	if (r == edid::retired_portrait) return retired_portrait_replacement();
	if (r != portrait_hd && r != portrait_default && r != portrait_max) return portrait_default;
	return r;
}

std::uint16_t portrait_width(std::uint16_t resolution)
{
	auto it = edid::portrait_widths.find(resolution);
	return it == edid::portrait_widths.end() ? 0 : it->second;
}

std::vector<int> portrait_rates(std::uint16_t resolution)
{
	auto it = edid::portrait_rate_table.find(resolution);
	return it == edid::portrait_rate_table.end() ? std::vector<int>{} : it->second;
}

int refresh_hz()
{
	std::uint16_t height = selected_resolution();
	if (portrait_enabled()) {
		std::uint16_t r = portrait_resolution();
		for (int rate : portrait_rates(r))
			if (programmed(std::format("{}/NanoKVM-portrait-{}x{}-{}.bin", edid_dir, portrait_width(r), r, rate), height))
				return rate;
		return 0;
	}
	for (int rate : rates(height)) {
		std::uint16_t effective = height == 0 ? auto_height() : height;
		if (programmed(rate_profile_path(effective, rate), height)) return rate;
	}
	return 0;
}

bool stock_profile() { return requires_power_cycle() && !exists(hash_file); }

std::string identity_json()
{
	auto id = saved_identity();
	if (!id) return "null";
	std::string out;
	(void)glz::write<glz::opts{.skip_null_members = false}>(*id, out);
	return out;
}

State state_after(const Settings &update)
{
	State s{selected_resolution(), portrait_enabled(), portrait_resolution()};
	if (update.resolution) s.resolution = *update.resolution;
	if (update.portrait) s.portrait = *update.portrait;
	if (update.portrait_resolution) s.portrait_resolution = *update.portrait_resolution;
	return s;
}

std::optional<std::string> fits_video_pool(const State &s, const VideoPool &p)
{
	auto [w, h] = size(s.resolution);
	std::string need = pool_need(s.resolution);
	if (need == "uhd" && !p.uhd()) return std::format("the monitor resolution {}x{} needs the UHD video memory mode", w, h);
	if (need == "qhd" && !p.qhd()) return std::format("the monitor resolution {}x{} needs the QHD or UHD video memory mode", w, h);
	if (s.portrait) {
		if (s.portrait_resolution == portrait_max) {
			if (p.mib < 54) return std::string("the portrait monitor 1440x2560 needs the QHD or UHD video memory mode");
		} else if (!p.qhd()) {
			return std::string("the portrait monitor needs the QHD or UHD video memory mode");
		}
	}
	return std::nullopt;
}

void apply(Settings update, int fps, std::function<void(std::string)> done)
{
	bool explicit_change = update.resolution || update.portrait || update.portrait_resolution || update.set_identity;
	if (!explicit_change) {
		// Only the refresh can need a write: none when the monitor does not follow the stream rate or the profile
		// for this rate is the one programmed.
		bool due = update.sync_refresh && refresh_follows_stream() && profile_supported();
		if (due) {
			auto hw = hardware();
			std::uint16_t height = selected_resolution();
			auto path = portrait_enabled() ? edid::portrait_profile_path_at(hw, edid_dir, portrait_resolution(), fps, regular)
			                               : edid::profile_path_at(hw, edid_dir, height, fps, regular);
			due = !programmed(path, height);
		}
		if (!due) {
			loop::post([done = std::move(done)] { done(""); });
			return;
		}
	}
	vision::run_paused([update = std::move(update), fps, done = std::move(done)] {
		std::string error = apply_locked(update, fps);
		loop::post([done, error] { done(error); });
	});
}

std::string clear_power_cycle_pending()
{
	if (std::remove(power_cycle_file) != 0 && errno != ENOENT) return "cannot clear the power-cycle reminder";
	return "";
}

void start(bool hdmi_disabled, std::function<void()> restart)
{
	int fps = screen().fps;
	vision::run_paused([hdmi_disabled, fps, restart = std::move(restart)] {
		std::string error;
		bool migrated = migrate_retired_portrait(error);
		if (!error.empty()) log::error("failed to migrate the retired portrait profile: {}", error);
		else if (migrated) log::info("replaced the retired 1296x2304 portrait profile");
		error.clear();
		bool changed = fit_to_video_memory(fps, error);
		if (!error.empty()) {
			log::error("failed to fit the monitor profile to the video memory: {}", error);
			return;
		}
		if (changed) {
			log::info("lowered the saved monitor profile to the video memory of this boot");
			// The capture detects the input when it starts and keeps one this boot's video memory cannot hold (such
			// as 3840x2160 on a QHD pool, right after lowering) until the next start: neither a mode change of the
			// computer nor an HDMI reset recovers it. So restart once, when the computer has had time to switch.
			const char *marker = std::getenv(fit_restart_marker);
			if (!marker || !*marker) {
				loop::post([hdmi_disabled, fps, restart] {
					loop::after(std::chrono::seconds(20), [hdmi_disabled, fps, restart] {
						restart();
						vision::run_paused([hdmi_disabled, fps] { finish_start(hdmi_disabled, fps); });
					});
				});
				return;
			}
		}
		finish_start(hdmi_disabled, fps);
	});
}

} // namespace nkos::video::monitor
