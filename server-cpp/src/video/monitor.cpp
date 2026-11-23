#include "video/monitor.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "config/config.hpp"
#include "video/screen.hpp"
#include "video/vision.hpp"

#include <kvm_vision.h>

#include <glaze/glaze.hpp>

#include <cstdio>
#include <format>
#include <fstream>
#include <map>
#include <mutex>
#include <sstream>
#include <sys/stat.h>

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

std::optional<std::string> read(const std::string &path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
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

// Writes a monitor file through a temporary file and a rename.
bool write_file(const std::string &path, const std::string &value) { return config::atomic_write(path, value, 0644); }

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
	if (!regular(path)) return "monitor profile is unavailable: " + path + " is not a regular file";
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
			log::error("monitor EDID programming failed: {}: {}", r.timed_out ? "timeout" : std::to_string(r.exit_code), r.output);
			failure = "monitor EDID programming failed";
		} else if (cube && !pending_before && r.output.find(write_skipped) != std::string::npos) {
			// The receiver already held this EDID: no power cycle is needed for it.
			std::remove(power_cycle_file);
		}
	}
	if (kvmv_edid_maintenance(0) != 0)
		failure += failure.empty() ? "cannot restore HDMI after monitor change" : "; cannot restore HDMI after monitor change";
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
	if (r == edid::retired_portrait) {
		// The retired 1296x2304 profile: 1080x1920 for an H.264 device, 1440x2560 otherwise.
		std::string codec;
		for (const char *path : {"/etc/kvm/encoder_codec", "/kvmapp/kvm/type"}) {
			codec = trim(read(path).value_or(""));
			for (auto &c : codec) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
			if (!codec.empty()) break;
		}
		return codec == "h264" ? portrait_default : portrait_max;
	}
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

} // namespace nkos::video::monitor
