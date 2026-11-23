#include "usb/usbinternet.hpp"

#include "common/process.hpp"
#include "config/yaml.hpp"
#include "usb/gojson_read.hpp"
#include "usb/gotext.hpp"

#include <algorithm>
#include <array>
#include <cerrno>
#include <chrono>
#include <csignal>
#include <cstdlib>
#include <cstring>
#include <ctime>
#include <fcntl.h>
#include <format>
#include <functional>
#include <sys/file.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>

extern char **environ;

namespace nkos::usb::usbinternet {

namespace {

constexpr const char *owner = "NanoKVM USB internet";

std::string path_error(const char *op, const std::string &path, int err)
{
	return std::string(op) + " " + path + ": " + gotext::errno_text(err);
}

bool exists(const std::string &p)
{
	struct stat st{};
	return ::stat(p.c_str(), &st) == 0;
}

// os.ReadFile: "" or the error.
std::string read_file(const std::string &path, std::string &out)
{
	out.clear();
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return path_error("open", path, errno);
	char buf[4096];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			int e = errno;
			::close(fd);
			return path_error("read", path, e);
		}
		if (n == 0) break;
		out.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	return {};
}

// os.WriteFile(path, data, 0600).
std::string write_file(const std::string &path, const std::string &data)
{
	int fd = ::open(path.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
	if (fd < 0) return path_error("open", path, errno);
	std::string error;
	if (::write(fd, data.data(), data.size()) != static_cast<ssize_t>(data.size())) error = path_error("write", path, errno);
	if (::close(fd) != 0 && error.empty()) error = path_error("close", path, errno);
	return error;
}

std::string mkdir_all(const std::string &dir, mode_t mode)
{
	struct stat st{};
	if (::stat(dir.c_str(), &st) == 0) return S_ISDIR(st.st_mode) ? "" : "mkdir " + dir + ": not a directory";
	auto slash = dir.rfind('/');
	if (slash != std::string::npos && slash > 0)
		if (auto e = mkdir_all(dir.substr(0, slash), mode); !e.empty()) return e;
	if (::mkdir(dir.c_str(), mode) != 0 && errno != EEXIST) return path_error("mkdir", dir, errno);
	return {};
}

// atomicFile: a temporary file in the directory, synced, renamed over path (mode 0600, as os.CreateTemp).
std::string atomic_file(const std::string &path, const std::string &data)
{
	std::string tmp = path.substr(0, path.rfind('/')) + "/.usb-internet-XXXXXX";
	int fd = ::mkstemp(tmp.data());
	if (fd < 0) return path_error("open", tmp, errno);
	std::string error;
	if (::write(fd, data.data(), data.size()) != static_cast<ssize_t>(data.size())) error = path_error("write", tmp, errno);
	else if (::fsync(fd) != 0) error = path_error("sync", tmp, errno);
	if (::close(fd) != 0 && error.empty()) error = path_error("close", tmp, errno);
	if (error.empty() && ::rename(tmp.c_str(), path.c_str()) != 0)
		error = "rename " + tmp + " " + path + ": " + gotext::errno_text(errno);
	::unlink(tmp.c_str());
	return error;
}

std::string exit_text(const process::Result &r, const std::string &name)
{
	if (!r.started) return "exec: \"" + name + "\": executable file not found in $PATH";
	if (r.timed_out) return "signal: killed";
	if (r.signal != 0) {
		switch (r.signal) {
		case SIGKILL: return "signal: killed";
		case SIGTERM: return "signal: terminated";
		case SIGINT: return "signal: interrupt";
		case SIGSEGV: return "signal: segmentation fault";
		case SIGABRT: return "signal: aborted";
		case SIGPIPE: return "signal: broken pipe";
		default: return "signal: " + std::to_string(r.signal);
		}
	}
	return "exit status " + std::to_string(r.exit_code);
}

// Runtime.command: combined output, 4 s.
bool command(std::string &out, std::string &error, const std::string &input, const std::vector<std::string> &argv)
{
	process::Options options;
	options.timeout = std::chrono::seconds(4);
	options.input = input;
	auto r = process::run(argv, options);
	if (!r.ok()) {
		error = argv[0] + " failed: " + exit_text(r, argv[0]);
		out.clear();
		return false;
	}
	out = std::move(r.output);
	return true;
}

std::string run(const std::string &input, const std::vector<std::string> &argv)
{
	std::string out, error;
	command(out, error, input, argv);
	return error;
}

const char *b(bool v) { return v ? "true" : "false"; }

struct Applied {
	bool flow_unsupported = false, address_owned = false;
	std::string prefix, address, uplink, config, rules, routing;
	std::optional<std::map<std::string, std::string>> forward;
};

std::string applied_json(const Applied &a)
{
	using gotext::json_string;
	std::string forward = "null";
	if (a.forward) {
		forward = "{";
		bool first = true;
		for (auto &[k, v] : *a.forward) {
			forward += (first ? "" : ",") + json_string(k) + ":" + json_string(v);
			first = false;
		}
		forward += "}";
	}
	return std::format(R"({{"FlowUnsupported":{},"AddressOwned":{},"Prefix":{},"Address":{},"Uplink":{},"Config":{},)"
	                   R"("Rules":{},"Routing":{},"Forward":{}}})",
	                   b(a.flow_unsupported), b(a.address_owned), json_string(a.prefix), json_string(a.address),
	                   json_string(a.uplink), json_string(a.config), json_string(a.rules), json_string(a.routing), forward);
}

std::string save(const Runtime &r, const std::string &name, const std::string &json)
{
	return atomic_file(r.run + "/" + name, json + "\n");
}

std::string load(const Runtime &r, Applied &a)
{
	std::string data;
	std::string path = r.run + "/applied.json";
	if (auto e = read_file(path, data); !e.empty()) {
		if (errno == ENOENT || e.ends_with(gotext::errno_text(ENOENT))) return {};
		return e;
	}
	std::string error;
	goj::unmarshal(data, "applied", [&](goj::Reader &rd) {
		rd.object("applied", "usbinternet.applied", {
			{"FlowUnsupported", [&](goj::Reader &x) { x.boolean(a.flow_unsupported); }},
			{"AddressOwned", [&](goj::Reader &x) { x.boolean(a.address_owned); }},
			{"Prefix", [&](goj::Reader &x) { x.string(a.prefix); }},
			{"Address", [&](goj::Reader &x) { x.string(a.address); }},
			{"Uplink", [&](goj::Reader &x) { x.string(a.uplink); }},
			{"Config", [&](goj::Reader &x) { x.string(a.config); }},
			{"Rules", [&](goj::Reader &x) { x.string(a.rules); }},
			{"Routing", [&](goj::Reader &x) { x.string(a.routing); }},
			{"Forward", [&](goj::Reader &x) {
				if (x.is_null()) a.forward.reset();
				x.map("map[string]string", [&] { if (!a.forward) a.forward.emplace(); },
				      [&](const std::string &k, goj::Reader &y) {
					      std::string v;
					      y.string(v);
					      (*a.forward)[k] = v;
				      });
			}},
		});
	}, error);
	return error;
}

struct Route {
	std::string dst, dev, gateway, type;
};

bool parse_routes(const std::string &data, std::vector<Route> &routes, std::string &error)
{
	routes.clear();
	std::vector<Route> elements;
	std::size_t length = 0;
	bool ok = goj::unmarshal(data, "", [&](goj::Reader &rd) {
		rd.slice("[]usbinternet.route", [&] { elements.clear(), length = 0; },
		         [&](std::size_t i, goj::Reader &x) {
			         if (i >= elements.size()) elements.emplace_back();
			         Route &rt = elements[i];
			         x.object("route", "usbinternet.route", {
				         {"dst", [&](goj::Reader &y) { y.string(rt.dst); }},
				         {"dev", [&](goj::Reader &y) { y.string(rt.dev); }},
				         {"gateway", [&](goj::Reader &y) { y.string(rt.gateway); }},
				         {"type", [&](goj::Reader &y) { y.string(rt.type); }},
			         });
		         },
		         [&](std::size_t n) {
			         if (n == 0) elements.clear();
			         length = n;
		         });
	}, error);
	if (ok) routes.assign(elements.begin(), elements.begin() + static_cast<long>(length));
	return ok;
}

// The process of a running dnsmasq the helper started, and whether it is ours.
bool owned_daemon(const Runtime &r, long long &pid)
{
	pid = 0;
	std::string data;
	if (!read_file(r.run + "/dnsmasq.pid", data).empty()) return false;
	std::string s = gotext::trim_space(data);
	// strconv.Atoi
	std::size_t i = !s.empty() && (s[0] == '+' || s[0] == '-') ? 1 : 0;
	if (i == s.size() || s.size() > 19 || s.find_first_not_of("0123456789", i) != std::string::npos) return false;
	long long v = std::strtoll(s.c_str(), nullptr, 10);
	if (v <= 1) return false;
	pid = v;
	std::string path = r.proc + "/" + std::to_string(v);
	std::string comm;
	if (!read_file(path + "/comm", comm).empty() || gotext::trim_space(comm) != "dnsmasq") return false;
	std::string args;
	if (!read_file(path + "/cmdline", args).empty()) return false;
	std::string want = "--conf-file=" + r.run + "/dnsmasq.conf";
	for (std::size_t start = 0;;) {
		auto end = args.find('\0', start);
		if (args.substr(start, end == std::string::npos ? std::string::npos : end - start) == want) return true;
		if (end == std::string::npos) break;
		start = end + 1;
	}
	return false;
}

std::string stop_daemon(const Runtime &r)
{
	long long pid;
	if (owned_daemon(r, pid)) {
		if (::kill(static_cast<pid_t>(pid), SIGTERM) != 0 && errno != ESRCH) return gotext::errno_text(errno);
		for (int n = 0; n < 20; n++) {
			if (!owned_daemon(r, pid)) break;
			std::this_thread::sleep_for(std::chrono::milliseconds(50));
		}
		if (owned_daemon(r, pid)) return "USB DNS/DHCP did not stop";
	}
	std::string path = r.run + "/dnsmasq.pid";
	if (::unlink(path.c_str()) != 0 && errno != ENOENT) return path_error("remove", path, errno);
	return {};
}

std::string daemon(const Runtime &r, const std::string &config)
{
	std::string path = r.run + "/dnsmasq.conf";
	std::string old;
	(void)read_file(path, old);
	long long pid;
	if (owned_daemon(r, pid) && old == config) return {};
	if (auto e = stop_daemon(r); !e.empty()) return e;
	if (auto e = atomic_file(path, config); !e.empty()) return e;
	if (auto e = run("", {"dnsmasq", "--test", "--conf-file=" + path}); !e.empty()) return e;
	std::string log_path = r.run + "/dnsmasq.log";
	int log = ::open(log_path.c_str(), O_CREAT | O_WRONLY | O_TRUNC | O_CLOEXEC, 0600);
	if (log < 0) return path_error("open", log_path, errno);
	std::string conf_arg = "--conf-file=" + path;
	pid_t child = ::fork();
	if (child == 0) {
		::setsid();
		int null = ::open("/dev/null", O_RDONLY);
		if (null >= 0) ::dup2(null, 0);
		::dup2(log, 1);
		::dup2(log, 2);
		process::cloexec_inherited();
		const char *argv[] = {"dnsmasq", "--keep-in-foreground", conf_arg.c_str(), nullptr};
		::execvp("dnsmasq", const_cast<char *const *>(argv));
		::_exit(127);
	}
	int err = errno;
	::close(log);
	if (child < 0) return "fork/exec dnsmasq: " + gotext::errno_text(err);
	if (auto e = atomic_file(r.run + "/dnsmasq.pid", std::to_string(child) + "\n"); !e.empty()) {
		::kill(child, SIGKILL);
		return e;
	}
	// Bind and privilege errors come after the exec: a running process of ours is required.
	std::this_thread::sleep_for(std::chrono::milliseconds(100));
	if (!owned_daemon(r, pid)) return "USB DNS/DHCP failed to start";
	return {};
}

std::string sysctl_path(const Runtime &r, const std::string &dev) { return r.proc + "/sys/net/ipv4/conf/" + dev + "/forwarding"; }

std::string ensure_forward(const Runtime &r, Applied &a, const std::string &dev)
{
	std::string data;
	if (auto e = read_file(sysctl_path(r, dev), data); !e.empty()) return e;
	if (gotext::trim_space(data) == "1") return {};
	if (!a.forward) a.forward.emplace();
	if (!a.forward->contains(dev)) (*a.forward)[dev] = data;
	if (auto e = save(r, "applied.json", applied_json(a)); !e.empty()) return e;
	return write_file(sysctl_path(r, dev), "1\n");
}

// The Wi-Fi access point's internet forwards through the same uplinks and finds their forwarding already on when
// sharing came first: its table keeps it.
std::string forwarding(const Runtime &r, Applied &a, const std::string &uplink, bool foreign)
{
	std::vector<std::string> wanted;
	if (!uplink.empty()) wanted = {"usb0", uplink};
	if (!a.forward) a.forward.emplace();
	bool keep = foreign;
	std::vector<std::string> devs;
	for (auto &[dev, original] : *a.forward) devs.push_back(dev);
	for (auto &dev : devs) {
		if (std::find(wanted.begin(), wanted.end(), dev) != wanted.end()) continue;
		if (!keep && run("", {"nft", "list", "table", "inet", "nkos_wifi_ap"}).empty()) keep = true;
		// Never toggle global ip_forward (it resets sysctls); another forwarding policy keeps its forwarding.
		std::string current;
		if (read_file(sysctl_path(r, dev), current).empty() && gotext::trim_space(current) == "1" && !keep)
			if (auto e = write_file(sysctl_path(r, dev), (*a.forward)[dev]); !e.empty()) return e;
		a.forward->erase(dev);
	}
	std::sort(wanted.begin(), wanted.end());
	wanted.erase(std::unique(wanted.begin(), wanted.end()), wanted.end());
	for (auto &dev : wanted)
		if (auto e = ensure_forward(r, a, dev); !e.empty()) return e;
	return {};
}

bool bound(const Runtime &r)
{
	std::string udc;
	if (!read_file(r.gadget + "/UDC", udc).empty() || gotext::trim_space(udc).empty()) return false;
	for (const char *name : {"ncm.usb0", "rndis.usb0"}) {
		struct stat st{};
		if (::lstat((r.gadget + "/configs/c.1/" + name).c_str(), &st) == 0 && S_ISLNK(st.st_mode)) return exists(r.net + "/usb0");
	}
	return false;
}

HostInput host_input(const Runtime &r)
{
	std::string data;
	(void)read_file(r.server, data);
	return {host_access(r), false, web_port(data), netboot_port(r)};
}

bool occupied(std::vector<Prefix> &out, std::string &error)
{
	std::string data;
	if (!command(data, error, "", {"ip", "-j", "-4", "route", "show", "table", "all"})) return false;
	std::vector<Route> routes;
	if (!parse_routes(data, routes, error)) return false;
	out.clear();
	for (auto &rt : routes) {
		if (rt.dev == "usb0" || rt.dst == "default") continue;
		if (auto p = parse_prefix(rt.dst)) out.push_back(*p);
		else if (auto ip = parse_ipv4(rt.dst)) out.push_back({*ip, 32});
	}
	return true;
}

void uplink_of(const Runtime &r, const Prefix &subnet, std::string &dev, std::string &routing)
{
	dev.clear();
	routing.clear();
	std::string data, error;
	// Ingress and source in the lookup, so that policy routing applies to the managed host.
	if (!command(data, error, "", {"ip", "-j", "-4", "route", "get", "1.1.1.1", "from", subnet_prefix(subnet) + ".100", "iif", "usb0"}))
		return;
	std::vector<Route> routes;
	if (!parse_routes(data, routes, error) || routes.size() != 1) return;
	const Route &rt = routes[0];
	if (rt.dev != "eth0" && rt.dev != "wlan0") return;
	if (!rt.type.empty() && rt.type != "unicast") return;
	// A route on an unplugged cable is not a usable uplink.
	std::string carrier;
	if (!read_file(r.net + "/" + rt.dev + "/carrier", carrier).empty() || gotext::trim_space(carrier) != "1") return;
	dev = rt.dev;
	routing = data;
}

} // namespace

Runtime default_runtime()
{
	// Base helpers can be upgraded before the application that understands the new layout.
	bool layout = exists("/usr/share/nanokvm/usb-flags-layout") && exists("/etc/kvm/usb/.layout-v1");
	return {layout ? "/etc/kvm/usb" : "/boot", "/run/nanokvm-usbnet", "/sys/kernel/config/usb_gadget/g0", "/sys/class/net",
	        "/proc", "/etc/resolv.conf", "/etc/kvm/server.yaml"};
}

bool enabled(const Runtime &r) { return exists(r.boot + "/usb.internet"); }
bool host_access(const Runtime &r) { return exists(r.boot + "/" + host_access_flag); }
bool watch_needed(const Runtime &r) { return bound(r) && !exists(r.boot + "/rndis.nodhcpd"); }

int netboot_port(const Runtime &r)
{
	std::string data;
	if (!read_file(r.run + "/netboot.port", data).empty()) return 0;
	std::string s = gotext::trim_space(data);
	std::size_t i = !s.empty() && (s[0] == '+' || s[0] == '-') ? 1 : 0;
	if (i == s.size() || s.size() > 19 || s.find_first_not_of("0123456789", i) != std::string::npos) return 0;
	long long port = std::strtoll(s.c_str(), nullptr, 10);
	return port < 1024 || port > 65535 ? 0 : static_cast<int>(port);
}

std::string set_netboot_port(const Runtime &r, int port)
{
	std::string path = r.run + "/netboot.port";
	if (port == 0) {
		if (::unlink(path.c_str()) != 0 && errno != ENOENT) return path_error("remove", path, errno);
		return {};
	}
	if (port < 1024 || port > 65535) return std::format("invalid network boot port {}", port);
	if (auto e = mkdir_all(r.run, 0700); !e.empty()) return e;
	return atomic_file(path, std::to_string(port) + "\n");
}

Status read_status(const Runtime &r, long long now)
{
	bool on = enabled(r);
	Status saved;
	std::string data;
	bool valid = false;
	if (read_file(r.run + "/status.json", data).empty()) {
		std::string error;
		valid = goj::unmarshal(data, "Status", [&](goj::Reader &rd) {
			rd.object("Status", "usbinternet.Status", {
				{"enabled", [&](goj::Reader &x) { x.boolean(saved.enabled); }},
				{"state", [&](goj::Reader &x) { x.string(saved.state); }},
				{"uplink", [&](goj::Reader &x) { x.string(saved.uplink); }},
				{"address", [&](goj::Reader &x) { x.string(saved.address); }},
				{"flowOffload", [&](goj::Reader &x) { x.boolean(saved.flow_offload); }},
				{"offloadReason", [&](goj::Reader &x) { x.string(saved.offload_reason); }},
				{"ipv6", [&](goj::Reader &x) { x.boolean(saved.ipv6); }},
				{"hostAccess", [&](goj::Reader &x) { x.boolean(saved.host_access); }},
				{"updatedAt", [&](goj::Reader &x) { x.integer(saved.updated_at, "int64"); }},
			});
		}, error);
	}
	Status s;
	if (!watch_needed(r)) {
		// Without a watcher an idle status does not expire; a cleanup failure stays visible until an apply works.
		if (valid && saved.state == "error" && saved.enabled == on) {
			s = saved;
			s.ipv6 = false;
		} else {
			s.enabled = on;
			s.state = "disabled";
			if (on) s.state = exists(r.boot + "/rndis.nodhcpd") ? "dhcp-disabled" : "waiting-ncm";
		}
	} else {
		s = saved;
		// int64 arithmetic wraps in Go.
		auto age = static_cast<long long>(static_cast<unsigned long long>(now) - static_cast<unsigned long long>(saved.updated_at));
		if (!valid || age > 20) {
			s = Status{};
			s.state = "unavailable";
		}
		s.enabled = on;
		s.ipv6 = false;
	}
	s.host_access = host_access(r);
	return s;
}

std::string status_json(const Status &s)
{
	using gotext::json_string;
	std::string j = std::format(R"({{"enabled":{},"state":{})", b(s.enabled), json_string(s.state));
	if (!s.uplink.empty()) j += ",\"uplink\":" + json_string(s.uplink);
	if (!s.address.empty()) j += ",\"address\":" + json_string(s.address);
	j += std::format(R"(,"flowOffload":{})", b(s.flow_offload));
	if (!s.offload_reason.empty()) j += ",\"offloadReason\":" + json_string(s.offload_reason);
	return j + std::format(R"(,"ipv6":{},"hostAccess":{},"updatedAt":{}}})", b(s.ipv6), b(s.host_access), s.updated_at);
}

// --- plan.go ---

std::optional<std::uint32_t> parse_ipv4(const std::string &s)
{
	std::uint32_t v = 0;
	int parts = 0;
	std::size_t i = 0;
	while (true) {
		std::size_t start = i;
		while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		std::size_t len = i - start;
		if (len == 0 || len > 3 || (len > 1 && s[start] == '0')) return std::nullopt;
		int octet = std::stoi(s.substr(start, len));
		if (octet > 255) return std::nullopt;
		v = v << 8 | static_cast<std::uint32_t>(octet);
		parts++;
		if (i == s.size()) break;
		if (s[i] != '.' || parts == 4) return std::nullopt;
		i++;
	}
	if (parts != 4) return std::nullopt;
	return v;
}

std::optional<Prefix> parse_prefix(const std::string &s)
{
	auto slash = s.find('/');
	if (slash == std::string::npos) return std::nullopt;
	auto addr = parse_ipv4(s.substr(0, slash));
	std::string bits = s.substr(slash + 1);
	if (!addr || bits.empty() || bits.size() > 2 || bits.find_first_not_of("0123456789") != std::string::npos ||
	    (bits.size() > 1 && bits[0] == '0'))
		return std::nullopt;
	int n = std::stoi(bits);
	if (n > 32) return std::nullopt;
	return Prefix{*addr, n};
}

std::string prefix_string(const Prefix &p)
{
	return std::format("{}.{}.{}.{}/{}", p.addr >> 24, (p.addr >> 16) & 255, (p.addr >> 8) & 255, p.addr & 255, p.bits);
}

namespace {
bool contains(const Prefix &p, std::uint32_t ip)
{
	std::uint32_t mask = p.bits == 0 ? 0 : ~0u << (32 - p.bits);
	return (p.addr & mask) == (ip & mask);
}
} // namespace

bool choose_subnet(const std::string &preferred, bool custom, const std::vector<Prefix> &occupied, Prefix &out,
                   std::string &error)
{
	auto p = parse_prefix(preferred + ".0/24");
	if (!p || (p->addr >> 24) == 127 || (p->addr >> 28) == 0xe || p->addr == 0) {
		error = "invalid USB prefix";
		return false;
	}
	auto free = [&](const Prefix &c) {
		for (auto &o : occupied)
			if (o.bits > 0 && (contains(c, o.addr) || contains(o, c.addr))) return false;
		return true;
	};
	if (free(*p)) {
		out = *p;
		return true;
	}
	if (custom) {
		error = "custom USB subnet overlaps another network";
		return false;
	}
	// Bounded, deterministic alternatives.
	for (std::uint32_t base : {172u << 24 | 31u << 16, 192u << 24 | 168u << 16, 10u << 24 | 254u << 16, 172u << 24 | 30u << 16})
		for (std::uint32_t n = 0; n < 256; n++) {
			Prefix c{base | n << 8, 24};
			if (free(c)) {
				out = c;
				return true;
			}
		}
	error = "no unused USB subnet";
	return false;
}

std::string subnet_prefix(const Prefix &p)
{
	std::string s = prefix_string(p);
	s.resize(s.find('/'));
	return s.substr(0, s.rfind('.'));
}

bool trusted_mss(const std::string &data)
{
	std::vector<std::vector<std::pair<std::string, std::string>>> objects;
	std::string error;
	if (!goj::unmarshal(data, "", [&](goj::Reader &rd) {
		    rd.object("", "struct { NFT []map[string]json.RawMessage \"json:\\\"nftables\\\"\" }", {
			    {"nftables", [&](goj::Reader &x) {
				    x.slice("[]map[string]json.RawMessage", [&] { objects.clear(); },
				            [&](std::size_t i, goj::Reader &y) {
					            if (i >= objects.size()) objects.emplace_back();
					            auto &o = objects[i];
					            y.map("map[string]json.RawMessage", [] {}, [&](const std::string &k, goj::Reader &z) {
						            std::string raw;
						            z.raw(raw);
						            auto it = std::find_if(o.begin(), o.end(), [&](auto &m) { return m.first == k; });
						            if (it != o.end()) it->second = raw;
						            else o.emplace_back(k, raw);
					            });
				            },
				            [&](std::size_t n) { objects.resize(n); });
			    }},
		    });
	    }, error))
		return false;
	goj::Any expected;
	{
		std::string e;
		goj::unmarshal(R"([{"match":{"op":"!=","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"match":{"op":"==","left":{"&":[{"payload":{"protocol":"tcp","field":"flags"}},{"|":["syn","rst"]}]},"right":"syn"}},{"mangle":{"key":{"tcp option":{"name":"maxseg","field":"size"}},"value":{"rt":{"key":"mtu"}}}}])",
		               "", [&](goj::Reader &rd) { rd.any(expected); }, e);
	}
	int chains = 0, entries = 0;
	for (auto &object : objects)
		for (auto &[kind, raw] : object) {
			std::string family, tbl, name, type, hook, policy, chain;
			long long prio = 0;
			std::optional<goj::Any> expr;
			std::string e;
			bool ok = goj::unmarshal(raw, "", [&](goj::Reader &rd) {
				rd.object("", "struct", {
					{"Family", [&](goj::Reader &x) { x.string(family); }}, {"Table", [&](goj::Reader &x) { x.string(tbl); }},
					{"Name", [&](goj::Reader &x) { x.string(name); }}, {"Type", [&](goj::Reader &x) { x.string(type); }},
					{"Hook", [&](goj::Reader &x) { x.string(hook); }}, {"Policy", [&](goj::Reader &x) { x.string(policy); }},
					{"Chain", [&](goj::Reader &x) { x.string(chain); }}, {"Prio", [&](goj::Reader &x) { x.integer(prio); }},
					{"Expr", [&](goj::Reader &x) {
						goj::Any v;
						x.any(v);
						if (v.kind == goj::Any::array) expr = std::move(v);
						else if (v.kind == goj::Any::null) expr.reset();
						else x.fail_type(v.kind == goj::Any::object ? "object" : v.kind == goj::Any::string ? "string" : v.kind == goj::Any::boolean ? "bool" : "number", "[]interface {}");
					}},
				});
			}, e);
			if (!ok) return false;
			if (family != "inet" || (tbl != "nkos_mss" && !(kind == "table" && name == "nkos_mss"))) continue;
			if (kind == "table") {
			} else if (kind == "chain") {
				chains++;
				if (name != "postrouting" || type != "filter" || hook != "postrouting" || prio != -150 || policy != "accept") return false;
			} else if (kind == "rule") {
				entries++;
				if (chain != "postrouting") return false;
				goj::Any expression;
				bool any_expression = false;
				int counters = 0;
				if (expr)
					for (auto &e2 : expr->items) {
						if (e2.kind == goj::Any::object && e2.members.size() == 1 && e2.members[0].first == "counter" &&
						    e2.members[0].second.kind != goj::Any::null) {
							counters++;
							continue;
						}
						any_expression = true;
						expression.kind = goj::Any::array;
						expression.items.push_back(e2);
					}
				if (counters != 1 || !any_expression || !(expression == expected)) return false;
			} else {
				return false;
			}
		}
	return chains == 1 && entries == 1;
}

bool inspect_rules(const std::string &data, bool &exists_out, bool &foreign, std::string &error)
{
	exists_out = foreign = false;
	struct Item {
		std::optional<std::array<std::string, 3>> table, chain;  // family,name,comment / family,table,hook
	};
	std::vector<Item> items;
	std::size_t length = 0;
	static const char *type = "struct { NFT []struct { Table *struct { Family string; Name string; Comment string } "
	                          "\"json:\\\"table\\\"\"; Chain *struct { Family string; Table string; Hook string } "
	                          "\"json:\\\"chain\\\"\" } \"json:\\\"nftables\\\"\" }";
	if (!goj::unmarshal(data, "", [&](goj::Reader &rd) {
		    rd.object("", type, {
			    {"nftables", [&](goj::Reader &x) {
				    x.slice("[]struct { Table *struct { Family string; Name string; Comment string } \"json:\\\"table\\\"\"; Chain *struct { Family string; Table string; Hook string } \"json:\\\"chain\\\"\" }", [&] { items.clear(), length = 0; },
				            [&](std::size_t i, goj::Reader &y) {
					            if (i >= items.size()) items.emplace_back();
					            Item &it = items[i];
					            y.object("", "struct { Table *struct { Family string; Name string; Comment string } \"json:\\\"table\\\"\"; Chain *struct { Family string; Table string; Hook string } \"json:\\\"chain\\\"\" }", {
						            {"table", [&](goj::Reader &z) {
							            z.pointer_object("", "struct { Family string; Name string; Comment string }", [&] {
								            if (!it.table) it.table.emplace();
								            auto *t = &*it.table;
								            return std::vector<goj::Field>{
									            {"Family", [t](goj::Reader &w) { w.string((*t)[0]); }},
									            {"Name", [t](goj::Reader &w) { w.string((*t)[1]); }},
									            {"Comment", [t](goj::Reader &w) { w.string((*t)[2]); }}};
							            }, [&] { it.table.reset(); });
						            }},
						            {"chain", [&](goj::Reader &z) {
							            z.pointer_object("", "struct { Family string; Table string; Hook string }", [&] {
								            if (!it.chain) it.chain.emplace();
								            auto *c = &*it.chain;
								            return std::vector<goj::Field>{
									            {"Family", [c](goj::Reader &w) { w.string((*c)[0]); }},
									            {"Table", [c](goj::Reader &w) { w.string((*c)[1]); }},
									            {"Hook", [c](goj::Reader &w) { w.string((*c)[2]); }}};
							            }, [&] { it.chain.reset(); });
						            }},
					            });
				            },
				            [&](std::size_t n) {
					            if (n == 0) items.clear();
					            length = n;
				            });
			    }},
		    });
	    }, error))
		return false;
	for (std::size_t i = 0; i < length; i++) {
		auto &r = items[i];
		if (r.table && (*r.table)[0] == "inet" && (*r.table)[1] == table) {
			if ((*r.table)[2] != owner) {
				error = "nft table name is already owned by another service";
				return false;
			}
			exists_out = true;
		}
		if (r.chain && !((*r.chain)[0] == "inet" && (*r.chain)[1] == table)) {
			const std::string &hook = (*r.chain)[2];
			if (hook == "ingress" || hook == "prerouting" || hook == "forward" || hook == "postrouting") {
				// The Wi-Fi access point's table (S30wifi) matches only wlan0 traffic.
				bool mss = (*r.chain)[0] == "inet" && (*r.chain)[1] == "nkos_mss" && trusted_mss(data);
				bool ap = (*r.chain)[0] == "inet" && (*r.chain)[1] == "nkos_wifi_ap";
				if (!mss && !ap) foreign = true;
			}
		}
	}
	return true;
}

bool default_policy(const std::string &data)
{
	std::vector<std::vector<std::pair<std::string, std::string>>> rules;
	std::string error;
	if (!goj::unmarshal(data, "", [&](goj::Reader &rd) {
		    rd.slice("[]map[string]json.RawMessage", [&] { rules.clear(); },
		             [&](std::size_t i, goj::Reader &x) {
			             if (i >= rules.size()) rules.emplace_back();
			             auto &o = rules[i];
			             x.map("map[string]json.RawMessage", [] {}, [&](const std::string &k, goj::Reader &y) {
				             std::string raw;
				             y.raw(raw);
				             auto it = std::find_if(o.begin(), o.end(), [&](auto &m) { return m.first == k; });
				             if (it != o.end()) it->second = raw;
				             else o.emplace_back(k, raw);
			             });
		             },
		             [&](std::size_t n) { rules.resize(n); });
	    }, error))
		return false;
	if (rules.size() != 3) return false;
	std::vector<long long> seen;
	for (auto &r : rules) {
		auto get = [&](const char *k) -> const std::string * {
			for (auto &[name, raw] : r)
				if (name == k) return &raw;
			return nullptr;
		};
		long long priority = 0;
		goj::Any tbl;
		std::string e;
		auto *pr = get("priority");
		auto *tr = get("table");
		if (!pr || !goj::unmarshal(*pr, "", [&](goj::Reader &x) { x.integer(priority); }, e)) return false;
		if (!tr || !goj::unmarshal(*tr, "", [&](goj::Reader &x) { x.any(tbl); }, e)) return false;
		for (auto &[key, value] : r) {
			if (key == "priority" || key == "table") continue;
			if (key == "src" || key == "dst") {
				if (value != "\"all\"") return false;
				continue;
			}
			return false;
		}
		// fmt.Sprint(table): a name, or a number (float64) such as 255.
		auto is = [&](const char *word, double number) {
			return (tbl.kind == goj::Any::string && tbl.text == word) || (tbl.kind == goj::Any::number && tbl.value == number) ||
			       (tbl.kind == goj::Any::string && tbl.text == std::format("{}", static_cast<int>(number)));
		};
		bool ok = (priority == 0 && is("local", 255)) || (priority == 32766 && is("main", 254)) ||
		          (priority == 32767 && is("default", 253));
		if (std::find(seen.begin(), seen.end(), priority) != seen.end() || !ok) return false;
		seen.push_back(priority);
	}
	return true;
}

std::string HostInput::chain(bool dns) const
{
	if (open) return {};
	std::string s = " chain input { type filter hook input priority filter; policy accept;\n";
	s += "  iifname \"usb0\" meta nfproto ipv6 counter drop\n";
	s += "  iifname \"usb0\" ct state { established, related } accept\n";
	if (dhcp) s += "  iifname \"usb0\" udp dport 67 accept\n";
	if (dns) s += "  iifname \"usb0\" meta l4proto { tcp, udp } th dport 53 accept\n";
	if (web > 0) s += std::format("  iifname \"usb0\" tcp dport {} accept\n", web);
	if (netboot > 0) s += std::format("  iifname \"usb0\" tcp dport {} accept\n", netboot);
	s += "  iifname \"usb0\" counter drop\n }\n";
	return s;
}

long long web_port(const std::string &server_yaml)
{
	std::string error, proto;
	long long http = 0, https = 0;
	bool ok = false;
	if (auto values = yaml::parse(server_yaml, error)) {
		if (auto p = yaml::as_string(values->get("proto"))) proto = *p;
		const yaml::Value *port = values->get("port");
		if (!port) {
			ok = true;  // decodes as zero ports
		} else if (port->kind == yaml::Value::Kind::map) {
			auto h = port->get("http") ? yaml::as_int(port->get("http")) : std::optional<std::int64_t>(0);
			auto hs = port->get("https") ? yaml::as_int(port->get("https")) : std::optional<std::int64_t>(0);
			if (h && hs) {
				ok = true;
				http = *h;
				https = *hs;
			}
		}
	}
	if (!ok || http < 1 || https < 1 || http > 65535 || https > 65535) {
		http = 80;
		https = 443;
		proto = "https";
	}
	return proto == "https" ? https : http;
}

std::string input_only(bool exists_table, const HostInput &in)
{
	std::string chain = in.chain(false);
	if (chain.empty()) return {};
	std::string s;
	if (exists_table) s += std::format("delete table inet {}\n", table);
	s += std::format("table inet {} {{\n comment {}\n{}}}\n", table, gotext::quote(owner), chain);
	return s;
}

std::string ruleset(const Prefix &subnet, const std::string &uplink, bool flow, bool exists_table, const HostInput &in)
{
	std::string net = prefix_string(subnet), up = gotext::quote(uplink), s;
	if (exists_table) s += std::format("delete table inet {}\n", table);
	s += std::format("table inet {} {{\n comment {}\n", table, gotext::quote(owner));
	// dnsmasq answers DNS only while sharing.
	s += in.chain(!uplink.empty());
	if (flow) s += std::format(" flowtable usb_fast {{ hook ingress priority 0; devices = {{ usb0, {} }}; }}\n", up);
	// A local-only guard also prevents accidental USB routing when a VPN enabled forwarding globally.
	s += " chain forward { type filter hook forward priority 30000; policy accept;\n";
	s += "  iifname \"usb0\" meta nfproto ipv6 counter drop\n  oifname \"usb0\" meta nfproto ipv6 counter drop\n";
	if (!uplink.empty()) {
		s += "  iifname \"usb0\" ct state invalid counter drop\n";
		if (flow)
			s += std::format("  iifname {} oifname \"usb0\" ip daddr {} ct state established ct mark 0 meta l4proto {{ tcp, udp }} flow add @usb_fast\n", up, net);
		s += std::format("  iifname \"usb0\" oifname {} ip saddr {} tcp flags syn tcp option maxseg size set rt mtu\n", up, net);
		s += std::format("  iifname \"usb0\" oifname {} ip saddr {} ct state {{ new, established, related }} counter accept\n", up, net);
		s += std::format("  iifname {} oifname \"usb0\" ip daddr {} ct state {{ established, related }} counter accept\n", up, net);
	}
	s += "  iifname \"usb0\" counter drop\n  oifname \"usb0\" counter drop\n }\n";
	if (!uplink.empty())
		s += std::format(" chain nat {{ type nat hook postrouting priority srcnat; policy accept; iifname \"usb0\" oifname {} ip saddr {} counter masquerade; }}\n", up, net);
	return s + "}\n";
}

std::string dns_config(const std::string &resolv, const std::string &prefix, bool sharing, long long boot_port)
{
	std::string cfg = std::format("interface=usb0\nexcept-interface=lo\nbind-interfaces\nlisten-address={0}.1\nno-hosts\n"
	                              "log-facility=-\ndomain-needed\nbogus-priv\nuser=nobody\npid-file=\nleasefile-ro\nquiet-dhcp\n"
	                              "dhcp-authoritative\ndhcp-range={0}.100,{0}.200,255.255.255.0,2m\n",
	                              prefix);
	if (sharing) cfg += std::format("resolv-file={}\ndhcp-option=3,{}.1\ndhcp-option=6,{}.1\n", resolv, prefix, prefix);
	else cfg += "port=0\ndhcp-option=3\ndhcp-option=6\n";
	if (boot_port > 0) cfg += std::format("dhcp-match=set:ipxe,175\ndhcp-boot=tag:ipxe,http://{}.1:{}/boot.ipxe\n", prefix, boot_port);
	return cfg;
}

// --- Apply ---

std::string apply(const Runtime &r, const std::string &preferred_in, bool stop)
{
	std::string preferred = preferred_in;
	if (auto e = mkdir_all(r.run, 0700); !e.empty()) return e;
	// The API, gadget and watcher entry points share this lock.
	std::string lock_path = r.run + "/internet.lock";
	int lock = ::open(lock_path.c_str(), O_CREAT | O_RDWR | O_CLOEXEC, 0600);
	if (lock < 0) return path_error("open", lock_path, errno);
	struct Close {
		int fd;
		~Close() { ::close(fd); }
	} close_lock{lock};
	while (::flock(lock, LOCK_EX) != 0)
		if (errno != EINTR) return gotext::errno_text(errno);
	Applied a;
	if (auto e = load(r, a); !e.empty()) return e;
	Status s;
	s.enabled = enabled(r);
	s.state = "disabled";
	HostInput in, served;
	bool cleanup_registered = false;

	std::string err = [&]() -> std::string {
		std::string data, e;
		if (!command(data, e, "", {"nft", "-j", "list", "ruleset"})) return e;
		bool table_exists, foreign;
		if (!inspect_rules(data, table_exists, foreign, e)) return e;
		// The input filter of a bound usb0, with DHCP open while dnsmasq serves it.
		in = host_input(r);
		served = in;
		served.dhcp = true;
		cleanup_registered = true;

		// keep: usb0 is bound, so it keeps the input filter alone (without DHCP, which stops here).
		auto cleanup = [&](bool keep) -> std::string {
			std::string rules = keep ? input_only(table_exists, in) : "";
			if (!rules.empty()) {
				if (auto x = run(rules, {"nft", "-f", "-"}); !x.empty()) return x;
				table_exists = true;
			} else if (table_exists) {
				// Only the table with our verified owner comment; never a flush of the ruleset.
				if (auto x = run("", {"nft", "delete", "table", "inet", table}); !x.empty()) return x;
				table_exists = false;
			}
			if (auto x = stop_daemon(r); !x.empty()) return x;
			if (auto x = forwarding(r, a, "", foreign); !x.empty()) return x;
			if (!a.address.empty() && a.address_owned && exists(r.net + "/usb0")) {
				if (auto x = run("", {"ip", "address", "del", a.address, "dev", "usb0"}); !x.empty()) {
					// A missing address after a gadget rebind is harmless; other failures are not.
					std::string addresses, q;
					bool queried = command(addresses, q, "", {"ip", "-j", "-4", "address", "show", "dev", "usb0"});
					if (!queried || addresses.find(a.address.substr(0, a.address.find('/'))) != std::string::npos) return x;
				}
			}
			a.address.clear();
			a.address_owned = false;
			a.uplink.clear();
			a.routing.clear();
			a.config.clear();
			a.rules.clear();
			return save(r, "applied.json", applied_json(a));
		};
		bool is_bound = !stop && bound(r);
		if (!is_bound || exists(r.boot + "/rndis.nodhcpd")) {
			if (s.enabled) s.state = exists(r.boot + "/rndis.nodhcpd") ? "dhcp-disabled" : "waiting-ncm";
			return cleanup(is_bound);
		}
		std::vector<Prefix> occupied_nets;
		if (!occupied(occupied_nets, e)) return e;
		bool custom = exists(r.boot + "/rndis.ipv4_prefix");
		if (!a.prefix.empty() && !custom) preferred = a.prefix;
		Prefix subnet;
		if (!choose_subnet(preferred, custom, occupied_nets, subnet, e)) {
			s.state = "subnet-conflict";
			// Local access stays; only sharing and its gateway advertisement are retired.
			if (a.address.empty()) return cleanup(true);
			auto old = parse_prefix(a.prefix + ".0/24");
			if (!old) return "netip.ParsePrefix(" + gotext::quote(a.prefix + ".0/24") + "): invalid prefix";
			if (auto x = run(ruleset(*old, "", false, table_exists, served), {"nft", "-f", "-"}); !x.empty()) return x;
			if (auto x = forwarding(r, a, "", foreign); !x.empty()) return x;
			if (auto x = daemon(r, dns_config(r.resolv, a.prefix, false, netboot_port(r))); !x.empty()) return x;
			a.uplink.clear();
			a.rules.clear();
			return save(r, "applied.json", applied_json(a));
		}
		std::string prefix = subnet_prefix(subnet);
		std::string addresses;
		if (!command(addresses, e, "", {"ip", "-j", "address", "show", "dev", "usb0"})) return e;
		struct Link {
			std::vector<std::string> flags;
			std::vector<std::pair<std::string, long long>> addrs;
		};
		std::vector<Link> links;
		std::size_t link_count = 0;
		bool parsed = goj::unmarshal(addresses, "", [&](goj::Reader &rd) {
			rd.slice("[]struct", [&] { links.clear(), link_count = 0; },
			         [&](std::size_t i, goj::Reader &x) {
				         if (i >= links.size()) links.emplace_back();
				         Link &l = links[i];
				         x.object("", "struct", {
					         {"flags", [&](goj::Reader &y) {
						         std::size_t n = 0;
						         y.slice("[]string", [&] { l.flags.clear(); },
						                 [&](std::size_t k, goj::Reader &z) {
							                 if (k >= l.flags.size()) l.flags.emplace_back();
							                 z.string(l.flags[k]);
						                 },
						                 [&](std::size_t c) { n = c; l.flags.resize(n); });
					         }},
					         {"addr_info", [&](goj::Reader &y) {
						         y.slice("[]struct", [&] { l.addrs.clear(); },
						                 [&](std::size_t k, goj::Reader &z) {
							                 if (k >= l.addrs.size()) l.addrs.emplace_back();
							                 auto &ad = l.addrs[k];
							                 z.object("", "struct", {
								                 {"local", [&](goj::Reader &w) { w.string(ad.first); }},
								                 {"prefixlen", [&](goj::Reader &w) { w.integer(ad.second); }},
							                 });
						                 },
						                 [&](std::size_t c) { l.addrs.resize(c); });
					         }},
				         });
			         },
			         [&](std::size_t n) {
				         if (n == 0) links.clear();
				         link_count = n;
			         });
		}, e);
		if (!parsed || link_count != 1) return "cannot inspect USB interface";
		const Link &link = links[0];
		if (std::find(link.flags.begin(), link.flags.end(), "UP") == link.flags.end())
			if (auto x = run("", {"ip", "link", "set", "dev", "usb0", "up"}); !x.empty()) return x;
		std::string address = prefix + ".1/24";
		if (!a.address.empty() && a.address != address && a.address_owned) {
			if (auto x = stop_daemon(r); !x.empty()) return x;
			if (auto x = run("", {"ip", "address", "del", a.address, "dev", "usb0"}); !x.empty()) return x;
		}
		bool present = false;
		for (auto &[local, len] : link.addrs)
			if (local == prefix + ".1" && len == 24) present = true;
		bool owned = a.address == address && a.address_owned;
		if (!present) owned = true;
		a.prefix = prefix;
		a.address = address;
		a.address_owned = owned;
		if (auto x = save(r, "applied.json", applied_json(a)); !x.empty()) return x;
		if (!present)
			if (auto x = run("", {"ip", "address", "replace", address, "dev", "usb0"}); !x.empty()) return x;
		s.address = address;
		std::string uplink, routing;
		bool flow = false;
		if (s.enabled) {
			// The kernel rejects a forwarded route lookup on an interface with forwarding off: the guard first.
			if (!table_exists) {
				if (auto x = run(ruleset(subnet, "", false, false, served), {"nft", "-f", "-"}); !x.empty()) return x;
				table_exists = true;
			}
			if (auto x = ensure_forward(r, a, "usb0"); !x.empty()) return x;
			uplink_of(r, subnet, uplink, routing);
			if (uplink.empty()) {
				s.state = "waiting-uplink";
			} else {
				s.state = "active";
				s.uplink = uplink;
				std::string policy, pe;
				bool got = command(policy, pe, "", {"ip", "-j", "-4", "rule", "show"});
				flow = got && !foreign && default_policy(policy);
				if (!flow) s.offload_reason = "network-policy";
				if (flow && a.flow_unsupported && a.routing == routing) {
					flow = false;
					s.offload_reason = "unsupported";
				}
			}
		}
		// The cache holds the canonical table text, without the optional delete line.
		std::string canonical = ruleset(subnet, uplink, flow, false, served);
		if (canonical != a.rules || routing != a.routing || !table_exists) {
			if (auto x = run(ruleset(subnet, uplink, flow, table_exists, served), {"nft", "-f", "-"}); !x.empty()) {
				if (!flow) return x;
				// Old kernels or uplinks may lack flowtables: NAT must still work.
				flow = false;
				a.flow_unsupported = true;
				s.offload_reason = "unsupported";
				canonical = ruleset(subnet, uplink, false, false, served);
				if (auto y = run(ruleset(subnet, uplink, false, table_exists, served), {"nft", "-f", "-"}); !y.empty()) return y;
			}
		}
		if (flow) a.flow_unsupported = false;
		a.rules = canonical;
		a.routing = routing;
		a.uplink = uplink;
		if (auto x = forwarding(r, a, uplink, foreign); !x.empty()) return x;
		std::string config = dns_config(r.resolv, prefix, !uplink.empty(), netboot_port(r));
		if (auto x = daemon(r, config); !x.empty()) return x;
		a.config = config;
		s.flow_offload = flow;
		return save(r, "applied.json", applied_json(a));
	}();

	if (!err.empty() && cleanup_registered) [&] {
		// On a runtime error the stale gateway and NAT go while the USB address stays; ownership is checked again.
		std::string latest, e;
		bool listed = command(latest, e, "", {"nft", "-j", "list", "ruleset"});
		bool owned = false, transit = false;
		std::string check;
		bool checked = inspect_rules(latest, owned, transit, check);
		if (a.address.empty()) {
			// No gateway to retire, but a bound usb0 keeps its input filter.
			std::string rules = input_only(owned, in);
			if (listed && checked && !rules.empty() && !stop && bound(r)) run(rules, {"nft", "-f", "-"});
			return;
		}
		if (listed && checked)
			if (auto p = parse_prefix(a.prefix + ".0/24")) {
				if (run(ruleset(*p, "", false, owned, served), {"nft", "-f", "-"}).empty()) {
					(void)forwarding(r, a, "", transit);
					(void)daemon(r, dns_config(r.resolv, a.prefix, false, netboot_port(r)));
					a.rules = ruleset(*p, "", false, false, served);
					a.uplink.clear();
					a.routing.clear();
					(void)save(r, "applied.json", applied_json(a));
					return;
				}
			}
		(void)stop_daemon(r);
	}();
	if (!err.empty()) s.state = "error";
	s.updated_at = static_cast<long long>(std::time(nullptr));
	if (auto e = save(r, "status.json", status_json(s)); err.empty()) err = e;
	return err;
}

} // namespace nkos::usb::usbinternet
