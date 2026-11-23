#pragma once

// OpenVPN client profiles, as the Go server's service/extensions/openvpn: imported .ovpn profiles normalized to an
// allowlist of client options (credential files inlined, scripts and plugins refused) in /etc/kvm/openvpn, one
// profile enabled at a time (the "active" marker, restored at start), OpenVPN started as a daemon of its own and
// restarted with a growing delay when it exits by itself, status from its log, sysfs and the link kind (DCO).

#include "extensions/common.hpp"

#include <chrono>
#include <functional>
#include <map>
#include <string>

namespace nkos::extensions::openvpn {

struct Parsed {
	std::string config;
	bool needs_auth = false;
	bool tap = false;
};
// Normalize: the self-contained profile, or the error text.
bool normalize(const std::string &data, const std::map<std::string, std::string> *assets, Parsed &out, std::string &error);
// words: the line split as OpenVPN splits it (quotes, backslashes, a comment after white space); false on bad quoting.
bool words(const std::string &line, std::vector<std::string> &out);
// dcoLinkJSON: ip -j -d link show of exactly one ovpn (DCO) link.
bool dco_link_json(const std::string &data);
// restartDelay: before restart n (n >= 1) of a tunnel that exited by itself.
std::chrono::seconds restart_delay(int n);

// The routes (under the service's lock).
Reply get_status();
// POST /openvpn/import: multipart "files" (.ovpn profiles and the files they reference).
Reply import(const std::string &content_type, const std::string &body);
// POST /openvpn/profile: {"id","action","username","password","passphrase"}.
Reply change(const std::string &body);

// NewService: brings the enabled profile up after a start (a thread).
void start();
// For checks: the restore of start() in the calling thread.
void restore();

// Replaceable by checks (restartSleep).
extern std::function<void(std::chrono::seconds)> restart_sleep;

} // namespace nkos::extensions::openvpn
