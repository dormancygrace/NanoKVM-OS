#pragma once

// Tailscale, as the Go server's service/extensions/tailscale: the Alpine community packages installed and removed
// on request, tailscaled run through OpenRC (never beside NetBird), the CLI's up/down/login/logout one at a time with
// time limits and at most one waiting login, status from tailscaled's local API (the CLI only when it gives no
// usable answer).

#include "extensions/common.hpp"
#include "network/util.hpp"

#include <optional>
#include <string>

namespace nkos::extensions::tailscale {

Reply install();
Reply uninstall(const std::string &body);
// local: the address the request's connection arrived at (thisConnection); nullopt when unknown.
Reply get_status(const std::optional<network::IP16> &local);
Reply up();
Reply down();
Reply login();
Reply logout();
Reply start();
Reply stop();
Reply restart();

// For checks: tailscaled's local API socket.
extern std::string local_api_socket;

} // namespace nkos::extensions::tailscale
