#pragma once

// NetBird, as the Go server's service/extensions/netbird: the Alpine community packages installed and removed on
// request, the netbird service run through OpenRC (never beside Tailscale), login by "netbird up" with the URL it
// prints, status from "netbird status --json".

#include "extensions/common.hpp"

#include <string>

namespace nkos::extensions::netbird {

Reply install();
Reply uninstall(const std::string &body);
Reply get_status();
Reply login();
Reply down();
Reply start();
Reply stop();
Reply restart();

// For checks: getIPv4 of a status address ("100.1.2.3/16" or an address).
std::string ipv4_of(const std::string &value);

} // namespace nkos::extensions::netbird
