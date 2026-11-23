#pragma once

// Address and network-name masking for files that leave the device (the developer archive), as the Go server's
// logs/mask.go: MAC, IPv4 and IPv6 addresses and Wi-Fi network names become placeholders; loopback, unspecified and
// netmask-shaped values stay so routes remain readable. The Go patterns (RE2, leftmost-first) are matched by hand.

#include <string>
#include <string_view>

namespace nkos::system::logs {

// MaskAddresses.
std::string mask_addresses(std::string_view text);

// SanitizeFile: a whole text file for the archive: the log viewer's sanitizer (system/log_sanitize, the last
// 20000 lines and 1 MiB), then mask_addresses, ending in a newline; "" when nothing is left.
std::string sanitize_file(std::string_view data);

} // namespace nkos::system::logs
