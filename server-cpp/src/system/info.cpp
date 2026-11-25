// GET /api/vm/info (addresses, mDNS name, application version, device key) and GET /api/vm/hardware (board revision,
// ATX switch), as the Go server's service/vm/info.go.

#include "system/info.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "system/atx.hpp"
#include "system/hardware.hpp"

#include <glaze/glaze.hpp>

#include <arpa/inet.h>
#include <fstream>
#include <ifaddrs.h>
#include <net/if.h>
#include <netinet/in.h>
#include <optional>
#include <string>
#include <sys/stat.h>
#include <vector>

namespace nkos::system {

namespace {

struct Ip {
	std::string name, addr, version, type;
};
struct InfoJson {
	std::optional<std::vector<Ip>> ips;  // null without an address, as Go's nil slice
	std::string mdns, application, deviceKey;
};
struct HardwareJson {
	std::string version;
	bool atx = false;
};

std::string read_line(const char *path, const char *fallback = "")
{
	std::ifstream in(path);
	if (!in) return fallback;
	std::string v((std::istreambuf_iterator<char>(in)), {});
	std::erase(v, '\n');
	return v;
}

// The first address of every running wired or wireless interface, in interface order; only IPv4 ones are listed
// (GetInterfaceInfos then getIPs: an interface whose first address is IPv6 is left out).
std::optional<std::vector<Ip>> addresses()
{
	ifaddrs *list = nullptr;
	if (::getifaddrs(&list) != 0) return std::nullopt;
	std::vector<Ip> out;
	std::vector<std::string> seen;
	for (ifaddrs *a = list; a; a = a->ifa_next) {
		if (!a->ifa_addr || (a->ifa_addr->sa_family != AF_INET && a->ifa_addr->sa_family != AF_INET6)) continue;
		std::string name = a->ifa_name;
		if (std::find(seen.begin(), seen.end(), name) != seen.end()) continue;
		if (!(a->ifa_flags & IFF_UP) || !(a->ifa_flags & IFF_RUNNING)) continue;
		std::string type;
		if (name.starts_with("eth") || name.starts_with("en")) type = "Wired";
		else if (name.starts_with("wlan") || name.starts_with("wl")) type = "Wireless";
		else continue;
		seen.push_back(name);
		if (a->ifa_addr->sa_family != AF_INET) continue;
		char buf[INET_ADDRSTRLEN];
		inet_ntop(AF_INET, &reinterpret_cast<sockaddr_in *>(a->ifa_addr)->sin_addr, buf, sizeof buf);
		out.push_back({name, buf, "IPv4", type});
	}
	::freeifaddrs(list);
	if (out.empty()) return std::nullopt;
	return out;
}

std::string mdns()
{
	struct stat st{};
	if (::stat("/run/avahi-daemon/pid", &st) != 0 || read_line("/run/avahi-daemon/pid").empty()) return "";
	std::ifstream in("/etc/hostname");
	if (!in) return "";
	return read_line("/etc/hostname") + ".local";
}

} // namespace

void add_info_routes(http::Router &router)
{
	router.add("GET", "/api/vm/info", auth::guarded([](http::Request &, http::Response &res) {
		InfoJson info{addresses(), mdns(), read_line("/kvmapp/version", "1.0.0"), read_line("/device_key")};
		std::string out;
		(void)glz::write<glz::opts{.skip_null_members = false}>(info, out);
		api::send(res, 0, "success", out);
	}));
	router.add("GET", "/api/vm/hardware", auth::guarded([](http::Request &, http::Response &res) {
		std::string version = hardware().version.empty() ? "Unknown" : hardware().version;
		std::string out;
		(void)glz::write_json(HardwareJson{version, atx::enabled()}, out);
		api::send(res, 0, "success", out);
	}));
}

} // namespace nkos::system
