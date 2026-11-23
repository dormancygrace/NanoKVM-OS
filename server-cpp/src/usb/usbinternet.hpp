#pragma once

// USB internet sharing and the usb0 input filter, as the Go server's usbinternet package: the policy of usb0 only,
// never the gadget or the device's uplink. The server reads the status and the flags; the helper nkos-usb-internet
// (called by S30usbnet) applies it: the USB address, DHCP/DNS (dnsmasq), forwarding and NAT to the uplink, the
// optional flow offload and the input filter that keeps the managed computer out of the NanoKVM's services.
// Commands run with the caller's PATH, as Go's exec does.

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <vector>

namespace nkos::usb::usbinternet {

inline constexpr const char *table = "nkos_usb_internet";
inline constexpr const char *host_access_flag = "usb.host_access";

// Boot: the USB flags (usb.internet, usb.host_access, rndis.*); Run: the runtime state; Server: server.yaml.
struct Runtime {
	std::string boot, run, gadget, net, proc, resolv, server;
};
Runtime default_runtime();

bool enabled(const Runtime &r);
bool host_access(const Runtime &r);
int netboot_port(const Runtime &r);
std::string set_netboot_port(const Runtime &r, int port);
bool watch_needed(const Runtime &r);

struct Status {
	bool enabled = false;
	std::string state, uplink, address;
	bool flow_offload = false;
	std::string offload_reason;
	bool ipv6 = false, host_access = false;
	long long updated_at = 0;
};
// ReadStatus; now is the Unix time.
Status read_status(const Runtime &r, long long now);
std::string status_json(const Status &s);

// Apply(preferred prefix, stop): reconciles usb0 with the flags; "" or Go's error text.
std::string apply(const Runtime &r, const std::string &preferred, bool stop);

// --- plan.go, exposed for the differential check ---

struct Prefix {
	std::uint32_t addr = 0;  // host order
	int bits = -1;           // -1: not an IPv4 prefix
};
std::optional<std::uint32_t> parse_ipv4(const std::string &s);
std::optional<Prefix> parse_prefix(const std::string &s);
std::string prefix_string(const Prefix &p);
bool choose_subnet(const std::string &preferred, bool custom, const std::vector<Prefix> &occupied, Prefix &out,
                   std::string &error);
std::string subnet_prefix(const Prefix &p);
bool inspect_rules(const std::string &data, bool &exists, bool &foreign, std::string &error);
bool default_policy(const std::string &data);
bool trusted_mss(const std::string &data);
struct HostInput {
	bool open = false, dhcp = false;
	long long web = 0, netboot = 0;
	std::string chain(bool dns) const;
};
long long web_port(const std::string &server_yaml);
std::string input_only(bool exists, const HostInput &in);
std::string ruleset(const Prefix &subnet, const std::string &uplink, bool flow, bool exists, const HostInput &in);
std::string dns_config(const std::string &resolv, const std::string &prefix, bool sharing, long long boot_port);

} // namespace nkos::usb::usbinternet
