#pragma once

// WireGuard profiles, as the Go server's service/extensions/wireguard: declarative wg-quick profiles (never hooks)
// in /etc/kvm/wireguard, one enabled at a time (the "active" marker, restored after a start with a growing delay),
// brought up and down with wg-quick, status from wg show dump; wireguard-tools kept selected in the APK world while
// there are profiles.

#include "extensions/common.hpp"

#include <chrono>
#include <cstdint>
#include <functional>
#include <string>

namespace nkos::extensions::wireguard {

// Validate: the normalized profile (Table = off after [Interface]), or false with the error text.
bool validate(const std::string &data, std::string &out, std::string &error);
// profileConfig: Validate with the routing choice applied.
bool profile_config(const std::string &data, bool route_allowed_ips, std::string &out, std::string &error);
struct Dump {
	std::int64_t last_handshake = 0;
	std::uint64_t received = 0, sent = 0;
};
// parseDump: wg show <interface> dump; the newest handshake and the summed transfer of the peers.
Dump parse_dump(const std::string &text);
std::chrono::seconds restore_delay(int attempt);

Reply get_status();
// POST /wireguard/import: multipart "files" (.conf).
Reply import(const std::string &content_type, const std::string &body);
// POST /wireguard/profile: {"id","action","name","routeAllowedIPs"}.
Reply change(const std::string &body);

// NewService: the boot restore of the enabled profile and the APK world selection (threads).
void start();
// For checks: one run of each in the calling thread, with the given sleep.
void restore_with(const std::function<void(std::chrono::seconds)> &sleep);
void keep_tools(const std::function<void(std::chrono::seconds)> &sleep);

// Replaceable by checks: apkpkg.Keep ("" or the error).
extern std::function<std::string(const std::string &)> keep_package;

} // namespace nkos::extensions::wireguard
