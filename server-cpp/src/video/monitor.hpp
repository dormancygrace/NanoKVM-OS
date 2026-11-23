#pragma once

// The HDMI monitor the device presents to the computer (the EDID programmed into the receiver), as the Go server's
// common/monitor*.go: profiles by resolution (16:9, 16:10, 21:9, 8:9), refresh rates, the portrait profiles, the
// identity the profiles carry, and what the board's receiver takes. The EDIDs are /usr/share/nanokvm/edid/*.bin;
// nanokvm_update_edid writes them. Reading side; loop thread.

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

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

inline constexpr std::uint16_t portrait_hd = 1280, portrait_default = 1920, portrait_max = 2560;

std::pair<std::uint16_t, std::uint16_t> size(std::uint16_t id);
std::string aspect(std::uint16_t id);
inline const std::vector<std::string> aspects{"16:9", "16:10", "21:9", "8:9"};
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

int refresh_hz();
bool stock_profile();
// The saved identity as JSON, "null" when none.
std::string identity_json();

} // namespace nkos::video::monitor
