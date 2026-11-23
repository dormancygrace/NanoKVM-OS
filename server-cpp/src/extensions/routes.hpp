#pragma once

// /api/extensions (admin), as the Go server's router/extensions.go: VPN versions, OpenVPN and WireGuard profiles,
// Settings > Services (LLDP, SNMP, UPS), Tailscale, NetBird and ACME; and the ACME HTTP-01 tokens
// (/.well-known/acme-challenge/:token, no authentication).

#include "http/router.hpp"

namespace nkos::extensions {

void add_routes(http::Router &router);

// What the Go server starts with the routes and in main: the OpenVPN and WireGuard boot restore of the enabled
// profile, the APK world selection of wireguard-tools, and the ACME renewal schedule (threads).
void start();

} // namespace nkos::extensions
