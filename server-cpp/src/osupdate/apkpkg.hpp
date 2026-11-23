#pragma once

// APK package operations of the add-ons and services, as the Go server's service/extensions/apkpkg: each holds the
// update lock (osupdate::lock) shared with firmware and Software page updates. Blocking (an apk transaction can
// take minutes): run it on a thread of its own or the blocking pool, never on the event loop. Errors are Go's
// texts; empty means success.

#include <string>
#include <vector>

namespace nkos::apkpkg {

// Run: one Software page operation (refresh, install, remove, upgrade) in the server, apk's output in the server's.
std::string run(const std::string &action, const std::string &name);

// InstallCommunity: packages of Alpine's community repository, together, after listing the repository (for a list
// that lost the line) and refreshing the indexes.
std::string install_community(const std::vector<std::string> &names);

// InstallOnly: packages of the NanoKVM repositories, refused when apk would do anything but install them.
std::string install_only(const std::vector<std::string> &names);

// Remove: packages in one transaction.
std::string remove(const std::vector<std::string> &names);

// Keep: selects an installed package in the APK world (apk add --no-network).
std::string keep(const std::string &name);

// Selected: whether the APK world names the package (with a constraint, tag or file pin too). error is set when the
// world cannot be read.
bool selected(const std::string &name, std::string &error);
// selected: whether a world text names the package.
bool world_selected(const std::string &world, const std::string &name);

// EnsureBasePackages: installs the network service packages (lldpd, net-snmp, nut and their OpenRC scripts,
// openvpn) a device updated from an older release lacks, in the background until it succeeds; their services stay
// off. Starts a thread; nothing when /etc/kvm/base-packages exists.
void start();

// The pieces of EnsureBasePackages, for the differential check.
std::string install_base();
void base_plan(const std::vector<std::string> &missing, const std::vector<std::string> &broken, const std::string &world,
               std::vector<std::string> &fix, std::vector<std::string> &add);
inline constexpr const char *changes_installed = "would change installed packages";

} // namespace nkos::apkpkg
