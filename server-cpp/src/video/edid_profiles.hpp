#pragma once

// The monitor (EDID) profiles as plain functions of their inputs, as the Go server's common/monitor_*.go: which
// receiver takes which profile, the profile file for a monitor setting and stream rate, and the edits made to a
// profile's bytes (a monitor identity, the RGB range check, the Cube mode check). No file is read here except
// through the exists predicate, so the choices can be compared with the Go server's offline.

#include <cstdint>
#include <functional>
#include <map>
#include <optional>
#include <string>
#include <vector>

namespace nkos::video::edid {

inline constexpr std::uint16_t portrait_hd = 1280, portrait_default = 1920, portrait_max = 2560;
inline constexpr std::uint16_t retired_portrait = 2304;

// The board (/etc/kvm/hw: alpha, beta, pcie) and the HDMI receiver (/etc/kvm/hdmi_version: c, ux, d, ue).
struct Hardware {
	std::string board, chip;
	// EDID programming.
	bool profile_supported() const
	{
		if (board == "pcie") return chip == "ux" || chip == "d";
		if (board == "alpha" || board == "beta") return chip == "c" || chip == "ux" || chip == "d";
		return false;
	}
	// The full profile set (above 1080p60, QHD, UHD, portrait): the LT6911UXC and the LT6911D.
	bool high_refresh() const { return profile_supported() && (chip == "ux" || chip == "d"); }
	// A new EDID takes effect only after a power cycle (Cube, Lite; an LT6911D is replugged in software).
	bool power_cycle() const { return (board == "alpha" || board == "beta") && chip != "d"; }
	// Applying video settings may rewrite the EDID to follow the stream rate.
	bool follows_refresh() const { return high_refresh() && !power_cycle(); }
};

struct Size {
	std::uint16_t width, height;
	const char *aspect;
};
// The 16:10, 21:9 and 8:9 profiles by id (the height of a 16:10 one, the width of a 21:9 one and of 1280x1440, the
// height of 2560x2880).
inline const std::map<std::uint16_t, Size> wider = {
	{1200, {1920, 1200, "16:10"}}, {1600, {2560, 1600, "16:10"}}, {2560, {2560, 1080, "21:9"}},
	{3440, {3440, 1440, "21:9"}},  {1280, {1280, 1440, "8:9"}},   {2880, {2560, 2880, "8:9"}},
};
inline const std::map<std::uint16_t, std::vector<int>> wider_rates = {
	{1200, {60, 50, 30}}, {1600, {50, 30}}, {2560, {60, 50, 30}}, {3440, {40, 30}}, {1280, {60, 50, 30}}, {2880, {30}},
};
// The LT6911C: the two sizes near 1920x1080 within its pixel clock.
inline const std::map<std::uint16_t, std::vector<int>> slow_wider_rates = {{1200, {50, 30}}, {2560, {30}}};
inline const std::map<std::uint16_t, std::vector<int>> monitor_rate_table = {
	{720, {120, 60, 30}}, {1080, {90, 75, 60, 30}}, {1440, {60, 50, 40, 30}}, {2160, {30}}};
inline const std::map<std::uint16_t, std::vector<int>> portrait_rate_table = {
	{portrait_hd, {120, 60, 30}}, {portrait_default, {90, 75, 60, 30}}, {portrait_max, {60, 50, 30}}};
inline const std::map<std::uint16_t, std::uint16_t> portrait_widths = {
	{portrait_hd, 720}, {portrait_default, 1080}, {portrait_max, 1440}};

// The stream limits' heights and widths (ResolutionMap).
inline const std::map<std::uint16_t, std::uint16_t> resolution_map = {
	{2160, 3840}, {1440, 2560}, {1080, 1920}, {720, 1280}, {600, 800}, {0, 0}};

using Exists = std::function<bool(const std::string &path)>;

std::vector<int> wider_rates_for(const Hardware &hw, std::uint16_t id);
bool wider_profile(std::uint16_t id);
bool valid_profile(std::uint16_t id);
std::pair<std::uint16_t, std::uint16_t> size(std::uint16_t id);
// "" for up to 1920x1088 pixels, "qhd" up to 2560x1440, else "uhd".
std::string pool_need(std::uint16_t id);
std::uint16_t auto_height(const Hardware &hw);
// The refresh rates of a monitor setting (0 Auto), fastest first; empty when fixed.
std::vector<int> monitor_rates(const Hardware &hw, std::uint16_t height);
// The slowest rate not below fps (the fastest when fps exceeds them all); 0 for none.
int slowest_rate_for(const std::vector<int> &rates, int fps);
int profile_rate(const Hardware &hw, int fps);

std::string rate_profile_path(const std::string &dir, std::uint16_t id, int rate);
std::string cube_profile_path(const std::string &dir, std::uint16_t height);
std::string portrait_edid_path(const std::string &dir, std::uint16_t resolution);
// The single-rate profile of a setting (monitorProfilePath).
std::string profile_path(const Hardware &hw, const std::string &dir, std::uint16_t height);
// The profile for a setting at a stream rate, the single-rate one when no rate profile exists.
std::string profile_path_at(const Hardware &hw, const std::string &dir, std::uint16_t height, int fps, const Exists &regular);
std::string portrait_profile_path_at(const Hardware &hw, const std::string &dir, std::uint16_t resolution, int fps,
                                     const Exists &regular);

// The identity the profiles carry instead of NanoKVM's own.
struct Identity {
	std::string manufacturer;
	std::uint16_t productCode = 0;
	std::string name;
	std::optional<std::uint32_t> serialNumber;
	std::string serialText;
	std::uint8_t week = 0;
	std::uint16_t year = 0;
};
// The error message for an identity EDID cannot carry, nullopt when it can.
std::optional<std::string> validate(const Identity &id);
// A copy of a profile carrying the identity (checksum recomputed); nullopt with error set when it cannot.
std::optional<std::string> with_identity(const std::string &edid, const Identity &id, std::string *error = nullptr);

// The header and every block's checksum.
bool edid_valid(const std::string &edid);
// A CTA Video Capability Data Block with QS=1.
bool declares_rgb_range(const std::string &edid);
// The profile as earlier releases shipped it, without the RGB range block; nullopt when it has none.
std::optional<std::string> without_rgb_range(const std::string &edid);
// The first mode an LT6911C cannot capture, nullopt when there is none.
std::optional<std::string> beyond_cube(const std::string &edid);

std::string sha256_hex(const std::string &data);

} // namespace nkos::video::edid
