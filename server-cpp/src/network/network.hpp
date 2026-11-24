#pragma once

// The network settings of the Go server's service/network: Wi-Fi (scan, networks, saved networks, the access point,
// the country, the no-login setup), Ethernet IPv4 and VLAN, DNS, IPv6, the preferred gateway and the gateway check,
// network changes on trial, Wake-on-LAN and the NanoKVMs announced on the local network.

#include "http/router.hpp"

#include <optional>
#include <string>

namespace nkos::network {

// The routes of router/network.go.
void add_routes(http::Router &router);

// Start-up work, in the Go server's order (see main.cpp). Before the listeners: initialize_ipv6 and
// ensure_dhcp_integration (main.go initialize), publish_discovery (main.go), then, with the routes, restore_pending_trial
// and start_gateway_check (router/network.go). None needs the event loop.
// WiFiHardwarePresent: the SDIO radio, also detected when Wi-Fi is switched off.
bool wifi_hardware_present();
bool initialize_ipv6();          // logs its own failure
bool ensure_dhcp_integration();  // logs its own failure
void publish_discovery();
void restore_pending_trial();
void start_gateway_check();

// A Wake-on-LAN packet for the MCP tools (network.Wake): 0 on success, else the API code with message.
int wake(const std::string &user, const std::string &mac, const std::string &iface, std::string &message);

// The STUN and TURN servers and the DTLS cipher offered first (config.WebRTCSettings), as POST /api/network/webrtc
// last set them: for each new WebRTC connection.
struct WebRTCServers {
	std::string stun, turn_addr, turn_user, turn_cred, dtls_cipher;
};
WebRTCServers webrtc_servers();

// networktrial.CheckIdle: why shutdown or standby must wait for a network trial, or nullopt when none is pending.
std::optional<std::string> trial_check_idle();

} // namespace nkos::network
