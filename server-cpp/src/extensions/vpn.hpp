#pragma once

// The VPN pages' shared pieces, as the Go server's service/extensions/vpn: the installed versions of the VPN
// programs (GET /api/extensions/vpn/versions), the purge flag of an uninstall and the removal of a VPN's state.

#include "extensions/common.hpp"

#include <string>
#include <string_view>
#include <vector>

namespace nkos::extensions::vpn {

// GET /api/extensions/vpn/versions: {"openvpn":"2.6.12",...} for the programs found in $PATH.
Reply versions();

// vpn.Purge: the uninstall request's body is {"purge": true} (ShouldBindJSON).
bool purge(const std::string &body);

// vpn.RemoveState: os.RemoveAll of each path, the errors joined ("" when none).
std::string remove_state(const std::vector<std::string> &paths);

// The first version number in text: [0-9]+\.[0-9]+(\.[0-9]+)?([-+][a-zA-Z0-9.-]+)* (three_parts: the third part
// required, as NetBird's pattern). "" when there is none.
std::string find_version(std::string_view text, bool three_parts);

} // namespace nkos::extensions::vpn
