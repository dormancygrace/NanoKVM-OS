// GET /api/vm/dashboard, as the Go server's service/vm/dashboard.go and dashboard package: read-only Linux telemetry
// without subprocesses (CPU, load, clock, temperature, storage, network interfaces), the memory status, the board
// revision and the application version.

#include "system/dashboard.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/loop.hpp"
#include "system/hardware.hpp"
#include "system/memory.hpp"

#include <glaze/glaze.hpp>

#include <arpa/inet.h>
#include <chrono>
#include <cmath>
#include <dirent.h>
#include <format>
#include <fstream>
#include <ifaddrs.h>
#include <linux/if_link.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <map>
#include <mutex>
#include <net/if.h>
#include <netinet/in.h>
#include <optional>
#include <sched.h>
#include <sstream>
#include <string>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/statvfs.h>
#include <unistd.h>
#include <vector>

namespace nkos::system {

namespace {

struct Cpu {
	std::uint64_t total = 0, idle = 0;
};
struct Storage {
	std::string path;
	bool available = false;
	std::uint64_t total = 0, free = 0, used = 0;
	bool readOnly = false;
};
struct Interface {
	std::string kind, name, mac;
	int mtu = 0;
	bool up = false, enabled = false, connected = false, wireless = false;
	std::vector<std::string> addresses;
	std::optional<std::uint64_t> received, sent;
};
struct Status {
	std::int64_t now = 0;
	std::string hostname, kernel, architecture = "riscv64";
	int cores = 1;
	std::optional<double> uptime, cpuUsage;
	std::optional<Cpu> cpu;
	std::optional<std::vector<std::string>> load;
	std::optional<int> cpuFrequency;
	std::optional<double> temperature;
	std::vector<Storage> storage;
	std::vector<Interface> interfaces;
};

std::string read(const std::string &path)
{
	std::ifstream in(path);
	if (!in) return "";
	std::string v((std::istreambuf_iterator<char>(in)), {});
	auto a = v.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return "";
	return v.substr(a, v.find_last_not_of(" \t\r\n") - a + 1);
}

std::vector<std::string> fields(const std::string &line)
{
	std::vector<std::string> out;
	std::istringstream in(line);
	for (std::string f; in >> f;) out.push_back(f);
	return out;
}

std::optional<std::uint64_t> counter(const std::string &path)
{
	std::string v = read(path);
	if (v.empty() || v.find_first_not_of("0123456789") != std::string::npos) return std::nullopt;
	try {
		return std::stoull(v);
	} catch (...) {
		return std::nullopt;
	}
}

// Guest time is already in user/nice: only the first eight fields count.
std::optional<Cpu> parse_cpu(const std::string &text)
{
	auto f = fields(text.substr(0, text.find('\n')));
	if (f.size() < 5 || f[0] != "cpu") return std::nullopt;
	Cpu c;
	for (std::size_t i = 1; i < std::min<std::size_t>(f.size(), 9); i++) {
		if (f[i].find_first_not_of("0123456789") != std::string::npos) return std::nullopt;
		std::uint64_t n = std::stoull(f[i]);
		c.total += n;
		if (i == 4 || i == 5) c.idle += n;
	}
	return c;
}

// Sampled once a second, independently of requests, so the first answer has a recent interval.
struct {
	std::mutex mutex;
	std::optional<Cpu> previous;
	std::optional<double> usage;
	std::chrono::steady_clock::time_point sampled;
} sampler;

void sample()
{
	auto next = parse_cpu(read("/proc/stat"));
	std::lock_guard lock(sampler.mutex);
	sampler.usage.reset();
	if (next && sampler.previous && next->total > sampler.previous->total && next->idle >= sampler.previous->idle) {
		std::uint64_t total = next->total - sampler.previous->total, idle = next->idle - sampler.previous->idle;
		if (idle <= total) sampler.usage = static_cast<double>(total - idle) * 100 / static_cast<double>(total);
	}
	sampler.previous = next;
	sampler.sampled = std::chrono::steady_clock::now();
}

void sample_every_second()
{
	sample();
	loop::after(std::chrono::seconds(1), sample_every_second);
}

std::optional<double> cpu_usage()
{
	std::lock_guard lock(sampler.mutex);
	if (!sampler.usage || std::chrono::steady_clock::now() - sampler.sampled > std::chrono::seconds(3)) return std::nullopt;
	return sampler.usage;
}

std::vector<std::string> glob(const std::string &dir, const std::string &prefix, const std::string &suffix = "")
{
	std::vector<std::string> out;
	if (DIR *d = ::opendir(dir.c_str())) {
		while (dirent *e = ::readdir(d)) {
			std::string name = e->d_name;
			if (name.starts_with(prefix) && name.ends_with(suffix) && name.size() >= prefix.size() + suffix.size())
				out.push_back(dir + "/" + name);
		}
		::closedir(d);
	}
	std::sort(out.begin(), out.end());
	return out;
}

// The board sensor over unrelated hwmon devices; unreadable or implausible readings are left out.
std::optional<double> temperature()
{
	std::vector<std::string> samples;
	for (const auto &dir : glob("/sys/class/hwmon", "hwmon")) {
		std::string name = read(dir + "/name");
		if (name != "nanokvm" && name != "sg2002") continue;
		for (const auto &p : glob(dir, "temp", "_input")) samples.push_back(p);
	}
	if (samples.empty())
		for (const auto &dir : glob("/sys/class/thermal", "thermal_zone")) {
			std::string name = read(dir + "/type");
			for (auto &c : name) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
			if (name.find("soc") != std::string::npos || name.find("cpu") != std::string::npos ||
			    name.find("nanokvm") != std::string::npos || name.find("sg2002") != std::string::npos)
				samples.push_back(dir + "/temp");
		}
	std::optional<double> result;
	for (const auto &path : samples) {
		std::string v = read(path);
		char *end = nullptr;
		long long n = std::strtoll(v.c_str(), &end, 10);
		if (v.empty() || *end || n < -40000 || n > 150000) continue;
		double t = static_cast<double>(n) / 1000;
		if (!result || t > *result) result = t;
	}
	return result;
}

std::optional<int> cpu_frequency()
{
	std::string v = read("/sys/devices/system/cpu/cpufreq/policy0/cpuinfo_cur_freq");
	char *end = nullptr;
	long khz = std::strtol(v.c_str(), &end, 10);
	if (v.empty() || *end || khz <= 0) return std::nullopt;
	return static_cast<int>(khz / 1000);
}

Storage storage(const std::string &path, bool mounted)
{
	Storage s{path};
	if (!mounted) return s;
	struct statvfs st{};
	if (::statvfs(path.c_str(), &st) != 0 || st.f_blocks == 0) return s;
	// Go's Statfs: blocks in units of f_bsize.
	std::uint64_t bsize = st.f_bsize;
	s.available = true;
	s.total = st.f_blocks * bsize;
	s.free = st.f_bavail * bsize;
	s.used = (st.f_blocks - std::min(st.f_blocks, st.f_bfree)) * bsize;
	s.readOnly = st.f_flag & ST_RDONLY;
	return s;
}

// The kernel link kind (IFLA_INFO_KIND) by interface index, from an RTM_GETLINK dump.
std::map<int, std::string> link_kinds()
{
	std::map<int, std::string> out;
	int fd = ::socket(AF_NETLINK, SOCK_RAW | SOCK_CLOEXEC, NETLINK_ROUTE);
	if (fd < 0) return out;
	struct {
		nlmsghdr h;
		ifinfomsg i;
	} req{};
	req.h.nlmsg_len = sizeof req;
	req.h.nlmsg_type = RTM_GETLINK;
	req.h.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP;
	req.i.ifi_family = AF_UNSPEC;
	if (::send(fd, &req, sizeof req, 0) < 0) {
		::close(fd);
		return out;
	}
	std::vector<char> buf(32768);
	for (bool done = false; !done;) {
		ssize_t n = ::recv(fd, buf.data(), buf.size(), 0);
		if (n <= 0) break;
		for (auto *h = reinterpret_cast<nlmsghdr *>(buf.data()); NLMSG_OK(h, static_cast<unsigned>(n)); h = NLMSG_NEXT(h, n)) {
			if (h->nlmsg_type == NLMSG_DONE || h->nlmsg_type == NLMSG_ERROR) {
				done = true;
				break;
			}
			if (h->nlmsg_type != RTM_NEWLINK) continue;
			auto *info = static_cast<ifinfomsg *>(NLMSG_DATA(h));
			int len = static_cast<int>(IFLA_PAYLOAD(h));
			for (auto *a = IFLA_RTA(info); RTA_OK(a, len); a = RTA_NEXT(a, len)) {
				if ((a->rta_type & NLA_TYPE_MASK) != IFLA_LINKINFO) continue;
				int ilen = static_cast<int>(RTA_PAYLOAD(a));
				for (auto *b = static_cast<rtattr *>(RTA_DATA(a)); RTA_OK(b, ilen); b = RTA_NEXT(b, ilen)) {
					if ((b->rta_type & NLA_TYPE_MASK) != IFLA_INFO_KIND) continue;
					std::string kind(static_cast<char *>(RTA_DATA(b)), RTA_PAYLOAD(b));
					while (!kind.empty() && kind.back() == '\0') kind.pop_back();
					if (!kind.empty()) out[info->ifi_index] = kind;
				}
			}
		}
	}
	::close(fd);
	return out;
}

int prefix_length(const sockaddr *mask)
{
	if (!mask) return 0;
	const unsigned char *b;
	std::size_t n;
	if (mask->sa_family == AF_INET) {
		b = reinterpret_cast<const unsigned char *>(&reinterpret_cast<const sockaddr_in *>(mask)->sin_addr);
		n = 4;
	} else {
		b = reinterpret_cast<const unsigned char *>(&reinterpret_cast<const sockaddr_in6 *>(mask)->sin6_addr);
		n = 16;
	}
	int bits = 0;
	for (std::size_t i = 0; i < n; i++) bits += __builtin_popcount(b[i]);
	return bits;
}

// Administrative preference, independent of carrier and link state.
bool interface_enabled(const std::string &name)
{
	struct stat st{};
	if (name == "eth0" || name.starts_with("eth0.")) return ::stat("/boot/eth.disabled", &st) != 0;
	if (name == "wlan0") return ::stat("/etc/kvm/wifi.disabled", &st) != 0;
	return true;
}

std::vector<Interface> interfaces()
{
	// Interfaces by index, as net.Interfaces lists them.
	std::vector<std::pair<int, std::string>> links;
	for (const auto &dir : glob("/sys/class/net", "")) {
		std::string name = dir.substr(dir.rfind('/') + 1);
		if (name == "." || name == "..") continue;
		links.push_back({std::atoi(read(dir + "/ifindex").c_str()), name});
	}
	std::sort(links.begin(), links.end());
	// Addresses per interface, in the kernel's order (IPv4, then IPv6).
	std::map<std::string, std::vector<std::string>> addresses;
	ifaddrs *list = nullptr;
	if (::getifaddrs(&list) == 0) {
		for (ifaddrs *a = list; a; a = a->ifa_next) {
			if (!a->ifa_addr || (a->ifa_addr->sa_family != AF_INET && a->ifa_addr->sa_family != AF_INET6)) continue;
			char text[INET6_ADDRSTRLEN];
			const void *raw = a->ifa_addr->sa_family == AF_INET
			                      ? static_cast<const void *>(&reinterpret_cast<sockaddr_in *>(a->ifa_addr)->sin_addr)
			                      : static_cast<const void *>(&reinterpret_cast<sockaddr_in6 *>(a->ifa_addr)->sin6_addr);
			inet_ntop(a->ifa_addr->sa_family, raw, text, sizeof text);
			addresses[a->ifa_name].push_back(std::string(text) + "/" + std::to_string(prefix_length(a->ifa_netmask)));
		}
		::freeifaddrs(list);
	}
	auto kinds = link_kinds();
	std::vector<Interface> out;
	for (const auto &[index, name] : links) {
		std::string base = "/sys/class/net/" + name;
		unsigned flags = static_cast<unsigned>(std::strtoul(read(base + "/flags").c_str(), nullptr, 16));
		if (flags & IFF_LOOPBACK) continue;
		struct stat st{};
		Interface i;
		i.kind = kinds.count(index) ? kinds[index] : "";
		i.name = name;
		i.enabled = interface_enabled(name);
		i.mac = read(base + "/address");
		i.mtu = std::atoi(read(base + "/mtu").c_str());
		i.up = flags & IFF_UP;
		// IFF_RUNNING comes from operstate, as Go's net package reads it.
		std::string operstate = read(base + "/operstate");
		i.connected = operstate == "up" || operstate == "unknown";
		i.wireless = ::stat((base + "/wireless").c_str(), &st) == 0 || name.starts_with("wl");
		i.addresses = addresses[name];
		i.received = counter(base + "/statistics/rx_bytes");
		i.sent = counter(base + "/statistics/tx_bytes");
		if (read(base + "/carrier") == "0") i.connected = false;
		out.push_back(std::move(i));
	}
	return out;
}

Status read_status()
{
	Status s;
	s.now = std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::system_clock::now().time_since_epoch()).count();
	s.hostname = read("/proc/sys/kernel/hostname");
	s.kernel = read("/proc/sys/kernel/osrelease");
	cpu_set_t set;
	if (::sched_getaffinity(0, sizeof set, &set) == 0) s.cores = CPU_COUNT(&set);
	s.cpu = parse_cpu(read("/proc/stat"));
	s.cpuUsage = cpu_usage();
	if (auto f = fields(read("/proc/uptime")); !f.empty()) {
		char *end = nullptr;
		double n = std::strtod(f[0].c_str(), &end);
		if (!*end && n >= 0) s.uptime = n;
	}
	if (auto f = fields(read("/proc/loadavg")); f.size() >= 3) s.load = std::vector<std::string>(f.begin(), f.begin() + 3);
	s.temperature = temperature();
	s.cpuFrequency = cpu_frequency();
	std::map<std::string, bool> mounted;
	std::istringstream mounts(read("/proc/mounts"));
	for (std::string line; std::getline(mounts, line);)
		if (auto f = fields(line); f.size() > 1) mounted[f[1]] = true;
	for (const char *path : {"/", "/boot", "/data"}) s.storage.push_back(storage(path, mounted[path]));
	s.interfaces = interfaces();
	return s;
}

} // namespace

void start_dashboard() { sample_every_second(); }

void add_dashboard_routes(http::Router &router)
{
	router.add("GET", "/api/vm/dashboard", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::string system_json;
			(void)glz::write<glz::opts{.skip_null_members = false}>(read_status(), system_json);
			std::string version = hardware().version.empty() ? "Unknown" : hardware().version;
			std::ifstream in("/kvmapp/version");
			std::string application = "1.0.0";
			if (in) {
				application.assign(std::istreambuf_iterator<char>(in), {});
				std::erase(application, '\n');
			}
			api::send(out, 0, "success",
			          std::format(R"({{"application":{},"hardware":{},"memory":{},"system":{}}})", api::quote(application),
			                      api::quote(version), memory_status_json().value_or("null"), system_json));
		});
	}));
}

} // namespace nkos::system
