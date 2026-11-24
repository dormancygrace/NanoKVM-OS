#pragma once

// The HDMI monitor the device presents to the computer (the EDID programmed into the receiver), as the Go server's
// common/monitor*.go: profiles by resolution (16:9, 16:10, 21:9, 8:9), refresh rates, the portrait profiles, the
// identity the profiles carry, and what the board's receiver takes. The EDIDs are /usr/share/nanokvm/edid/*.bin;
// nanokvm_update_edid writes them. The choices themselves are video/edid_profiles. Loop thread, except apply().

#include "video/edid_profiles.hpp"

#include <cstdint>
#include <functional>
#include <optional>
#include <string>
#include <vector>

namespace nkos::video {
struct VideoPool;
}

namespace nkos::video::monitor {

// The board and receiver (/etc/kvm/hw, /etc/kvm/hdmi_version).
std::string board();
std::string receiver_chip();
std::string receiver_model();  // LT6911C, LT6911UXC, LT6911D or ""
bool profile_supported();       // EDID programming
bool high_refresh_supported();  // the full profile set (above 1080p60, QHD, UHD, portrait)
bool requires_power_cycle();    // a new EDID takes effect only after a power cycle (Cube, Lite)
bool power_cycle_pending();
bool refresh_follows_stream();

inline constexpr std::uint16_t portrait_hd = edid::portrait_hd, portrait_default = edid::portrait_default,
                               portrait_max = edid::portrait_max;

std::pair<std::uint16_t, std::uint16_t> size(std::uint16_t id);
std::string aspect(std::uint16_t id);
inline const std::vector<std::string> aspects{"16:9", "16:10", "21:9", "8:9"};
bool valid_profile(std::uint16_t id);
bool wider_profile(std::uint16_t id);
bool wider_profile_offered(std::uint16_t id);
std::vector<int> rates(std::uint16_t height);
std::uint16_t auto_height();
// "uhd", "qhd" or "" (the video memory a profile needs).
std::string pool_need(std::uint16_t id);

std::uint16_t selected_resolution();
bool portrait_enabled();
bool portrait_supported();
bool portrait_max_supported();
std::uint16_t portrait_resolution();
std::uint16_t portrait_width(std::uint16_t resolution);
std::vector<int> portrait_rates(std::uint16_t resolution);
// The pool the current boot images get (shared 144 MiB reusable CMA).
bool portrait_supported_for_shared_pool();
bool portrait_max_supported_for_shared_pool();
bool portrait_supported_for(const VideoPool &pool);
bool portrait_max_supported_for(const VideoPool &pool);

int refresh_hz();
bool stock_profile();
// The saved identity as JSON, "null" when none.
std::string identity_json();

// One final monitor state; unset fields keep their saved values. sync_refresh follows the saved stream rate.
struct Settings {
	std::optional<std::uint16_t> resolution;
	std::optional<bool> portrait;
	std::optional<std::uint16_t> portrait_resolution;
	bool sync_refresh = false;
	// Replaces the saved identity with identity (nullopt: NanoKVM's own) and writes the profile with it.
	bool set_identity = false;
	std::optional<edid::Identity> identity;
};

struct State {
	std::uint16_t resolution = 0;  // 0 Auto
	bool portrait = false;
	std::uint16_t portrait_resolution = portrait_default;
};
// The monitor profile an update leads to: its fields, the saved settings for the others.
State state_after(const Settings &update);
// Why a video pool cannot capture the profile, nullopt when it can.
std::optional<std::string> fits_video_pool(const State &state, const VideoPool &pool);

// Programs the receiver for the update and saves it (ApplyMonitorSettings), on the device thread with the capture
// paused: the EDID tool takes about a second, up to 90 s. fps is the saved stream rate. done runs on the loop
// thread with "" or the error. Nothing is written when the update changes nothing the receiver shows.
void apply(Settings update, int fps, std::function<void(std::string error)> done);

// The user confirmed the power cycle (the reminder goes); "" or the error.
std::string clear_power_cycle_pending();

// The upkeep of the saved profile once the capture runs (the Go server's start goroutine). The receiver keeps its
// EDID in its own flash across restarts and reinstalls, so in order: a saved 1296x2304 portrait profile becomes its
// successor; a profile the video memory of this boot cannot capture is lowered (then, 20 s later, restart replaces
// the process once, so the capture detects the input again); a receiver left with the retired Windows pointer
// block gets its profile as shipped; after an installation the saved profile is programmed (a Cube or Lite only
// when its EDID advertises modes it cannot capture or lacks the RGB range declaration), and a write that needs a
// power cycle raises the hdmi.power-cycle notification. Each step runs on the device thread, the EDID tool with
// the capture paused. hdmi_disabled: HDMI capture is off, the receiver is not read. restart runs on the loop
// thread and returns only when the process could not be replaced. Loop thread.
void start(bool hdmi_disabled, std::function<void()> restart);

} // namespace nkos::video::monitor
