// IPv6 per interface, as the Go server's service/network/ipv6.go: /etc/kvm/network/ipv6 ("eth0=static
// eth0.address=2001:db8::10/64 eth0.gateway=2001:db8::1 wlan0=1"), applied by the embedded S02ipv6 script, which is
// also installed as /etc/init.d/S02ipv6 for the next boot.

#include "network/internal.hpp"
#include "network/network.hpp"

#include "common/log.hpp"

#include <algorithm>
#include <cerrno>
#include <cstdlib>
#include <fcntl.h>
#include <unistd.h>

namespace nkos::network {

const std::vector<std::string> ipv6_interfaces = {"eth0", "wlan0"};

namespace {

std::mutex ipv6_mutation;

bool managed(const std::string &name) { return std::find(ipv6_interfaces.begin(), ipv6_interfaces.end(), name) != ipv6_interfaces.end(); }

std::optional<Addr> recorded_ipv6(const std::string &file)
{
	auto data = read_file(path_join(paths.ipv6_run_dir, file));
	if (!data) return std::nullopt;
	auto f = fields(*data);
	if (f.empty()) return std::nullopt;
	auto prefix = parse_prefix(f[0]);
	if (!prefix) return std::nullopt;
	return prefix->addr;
}

// os.CreateTemp(dir, prefix + "*"): a new file of mode 0600 with a random name.
int create_temp(const std::string &dir, const std::string &prefix, std::string &name)
{
	for (int attempt = 0; attempt < 10000; attempt++) {
		unsigned value = 0;
		(void)!::getentropy(&value, sizeof value);
		name = path_join(dir, prefix + std::to_string(value));
		int fd = ::open(name.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd >= 0 || errno != EEXIST) return fd;
	}
	return -1;
}

Error write_fd(int fd, const std::string &data, const std::string &name)
{
	std::string_view rest = data;
	while (!rest.empty()) {
		ssize_t n = ::write(fd, rest.data(), rest.size());
		if (n < 0) {
			if (errno == EINTR) continue;
			return path_error("write", name, errno);
		}
		rest.remove_prefix(static_cast<std::size_t>(n));
	}
	return {};
}

} // namespace

Error IPv6Setting::normalized(IPv6Setting &out) const
{
	out = *this;
	if (!address.empty()) {
		auto prefix = parse_prefix(address);
		if (!prefix || !prefix->addr.is6() || prefix->addr.is4in6() || !prefix->addr.zone.empty() || prefix->addr.unspecified() ||
		    prefix->addr.multicast() || prefix->addr.loopback())
			return make_error("invalid IPv6 address");
		// Keeps the host bits: 2001:db8::10/64 stays itself, not 2001:db8::/64.
		out.address = prefix->str();
	}
	if (!gateway.empty()) {
		auto gw = parse_addr(gateway);
		if (!gw || !gw->is6() || gw->is4in6() || !gw->zone.empty() || gw->unspecified() || gw->multicast() || gw->loopback())
			return make_error("invalid IPv6 gateway");
		out.gateway = gw->str();
	}
	if (mode == "static" && out.address.empty()) return make_error("static IPv6 needs an address");
	return {};
}

std::optional<IPv6Policy> parse_ipv6_policy(const std::string &text)
{
	IPv6Policy policy;
	auto mode_of = [](const std::string &value) -> std::optional<std::string> {
		if (value == "0") return std::string();
		if (value == "1") return std::string("dhcpv6");
		if (value == "static") return std::string("static");
		return std::nullopt;
	};
	for (const auto &field : fields(text)) {
		if (field == "0" || field == "1") {
			for (const auto &name : ipv6_interfaces) policy[name].mode = *mode_of(field);
			continue;
		}
		auto eq = field.find('=');
		std::string key = field.substr(0, eq);
		std::string value = eq == std::string::npos ? "" : field.substr(eq + 1);
		auto dot = key.find('.');
		std::string name = key.substr(0, dot);
		std::string attribute = dot == std::string::npos ? "" : key.substr(dot + 1);
		if (eq == std::string::npos || !managed(name)) return std::nullopt;
		auto &setting = policy[name];
		if (attribute.empty()) {
			auto mode = mode_of(value);
			if (!mode) return std::nullopt;
			setting.mode = *mode;
		} else if (attribute == "address") {
			setting.address = value;
		} else if (attribute == "gateway") {
			setting.gateway = value;
		} else {
			return std::nullopt;
		}
	}
	for (auto &[name, setting] : policy) {
		IPv6Setting normalized;
		if (setting.normalized(normalized)) return std::nullopt;
		setting = normalized;
	}
	return policy;
}

bool ipv6_policy_any(const IPv6Policy &p)
{
	for (const auto &name : ipv6_interfaces)
		if (auto it = p.find(name); it != p.end() && !it->second.mode.empty()) return true;
	return false;
}

std::vector<std::string> ipv6_policy_words(const IPv6Policy &p)
{
	std::vector<std::string> words;
	for (const auto &name : ipv6_interfaces) {
		IPv6Setting setting;
		if (auto it = p.find(name); it != p.end()) setting = it->second;
		std::string value = setting.mode == "dhcpv6" ? "1" : setting.mode == "static" ? "static" : "0";
		words.push_back(name + "=" + value);
		if (!setting.address.empty()) words.push_back(name + ".address=" + setting.address);
		if (!setting.gateway.empty()) words.push_back(name + ".gateway=" + setting.gateway);
	}
	return words;
}

Error read_ipv6_policy(IPv6Policy &out)
{
	out.clear();
	Error err;
	auto data = read_file(paths.ipv6_config, &err);
	if (err.not_exist()) return {};
	if (err) return err;
	auto policy = parse_ipv6_policy(*data);
	if (!policy) return make_error("invalid IPv6 configuration");
	out = *policy;
	return {};
}

Error apply_ipv6(const IPv6Policy &p)
{
	std::string name;
	int fd = create_temp(paths.temp_dir, "nanokvm-ipv6-", name);
	if (fd < 0) return path_error("open", name, errno);
	Error err = write_fd(fd, join(ipv6_policy_words(p), "\n") + "\n", name);
	if (::close(fd) != 0 && !err) err = path_error("close", name, errno);
	if (!err) {
		RunOptions options;
		options.timeout = std::chrono::seconds(5);
		options.combined = true;
		options.input = std::string(ipv6_boot_script_text);
		options.env = {"NANOKVM_IPV6_SYSCTL=" + paths.ipv6_sysctl_root, "NANOKVM_IPV6_CONF=" + name};
		auto r = run(unprotected("/bin/sh", {"-s", "--", "start"}), options);
		if (!r.ok()) err = make_error("apply IPv6: " + r.error + ": " + trim_space(r.out));
	}
	::unlink(name.c_str());
	return err;
}

Error write_ipv6_policy(const IPv6Policy &p)
{
	std::string dir = dir_name(paths.ipv6_config);
	if (auto err = mkdir_all(dir, 0755)) return err;
	std::string name;
	int fd = create_temp(dir, ".ipv6-", name);
	if (fd < 0) return path_error("open", name, errno);
	Error err = write_fd(fd, join(ipv6_policy_words(p), "\n") + "\n", name);
	if (!err && ::fsync(fd) != 0) err = path_error("sync", name, errno);
	if (::close(fd) != 0 && !err) err = path_error("close", name, errno);
	if (!err) err = rename(name, paths.ipv6_config);
	if (err) ::unlink(name.c_str());
	return err;
}

std::pair<bool, Error> apply_ipv6_off()
{
	for (const auto &name : ipv6_interfaces) {
		for (const auto &file : {"udhcpc6." + name + ".pid", "udhcpc6." + name + ".addr", "ipv6-static." + name}) {
			// A stopped client can leave its address and DNS without a PID.
			if (!lstat_path(path_join(paths.ipv6_run_dir, file)).not_exist()) return {false, {}};
		}
	}
	Error err;
	auto entries = read_dir(paths.ipv6_sysctl_root, &err);
	// A kernel without IPv6 already satisfies a policy with IPv6 off.
	if (err.not_exist()) return {true, {}};
	if (err) return {false, err};
	// As in the script: only an existing value, and only when it differs.
	auto set = [](const std::string &name, const std::string &key, const std::string &value) -> Error {
		std::string path = path_join(path_join(paths.ipv6_sysctl_root, name), key);
		Error e;
		auto data = read_file(path, &e);
		if (e.not_exist()) return {};
		if (e) return e;
		if (trim_space(*data) == value) return {};
		return write_file(path, value + "\n", 0644);
	};
	// "all" rewrites every interface, so it comes first.
	for (const char *name : {"all", "default"})
		if (auto e = set(name, "disable_ipv6", "1")) return {true, e};
	for (const auto &entry : entries) {
		if (!entry.dir || entry.name == "all" || entry.name == "default") continue;
		if (auto e = set(entry.name, "disable_ipv6", entry.name == "lo" ? "0" : "1")) return {true, e};
		if (managed(entry.name)) {
			if (auto e = set(entry.name, "keep_addr_on_down", "0")) return {true, e};
			if (auto e = set(entry.name, "autoconf", "1")) return {true, e};
		}
	}
	return {true, {}};
}

Error initialize_ipv6_policy()
{
	std::lock_guard lock(ipv6_mutation);
	IPv6Policy policy;
	if (auto err = read_ipv6_policy(policy)) return err;
	const std::string &path = paths.ipv6_boot_script;
	auto data = read_file(path);
	if (!data || *data != ipv6_boot_script_text) {
		if (auto err = write_file(path + ".new", ipv6_boot_script_text, 0755)) return err;
		if (auto err = rename(path + ".new", path)) return err;
	}
	if (!ipv6_policy_any(policy)) {
		auto [done, err] = apply_ipv6_off();
		if (done || err) return err;
	}
	return apply_ipv6(policy);
}

std::string ipv6_source(const std::string &iface, const Addr &ip)
{
	if (!managed(iface) || ip.link_local_unicast()) return "";
	if (auto dhcp = recorded_ipv6("udhcpc6." + iface + ".addr"); dhcp && *dhcp == ip) return "dhcpv6";
	if (auto fixed = recorded_ipv6("ipv6-static." + iface); fixed && *fixed == ip) return "static";
	return "slaac";
}

Error ipv6_status_json(std::string &out)
{
	auto b = [](bool v) { return v ? "true" : "false"; };
	IPv6Policy policy;
	bool enabled = false, active = false, supported = false;
	std::string addresses, ifaces;
	auto finish = [&] {
		out = std::string("{\"enabled\":") + b(enabled) + ",\"active\":" + b(active) + ",\"supported\":" + b(supported) +
		      ",\"addresses\":[" + addresses + "],\"interfaces\":[" + ifaces + "]}";
	};
	if (auto err = read_ipv6_policy(policy)) {
		finish();
		return err;
	}
	enabled = ipv6_policy_any(policy);
	for (const auto &name : ipv6_interfaces) {
		Error e;
		auto state = read_file(path_join(path_join(paths.ipv6_sysctl_root, name), "disable_ipv6"), &e);
		IPv6Setting setting;
		if (auto it = policy.find(name); it != policy.end()) setting = it->second;
		if (!ifaces.empty()) ifaces += ",";
		ifaces += "{\"name\":" + quote(name) + ",\"enabled\":" + b(!setting.mode.empty()) + ",\"mode\":" + quote(setting.mode) +
		          ",\"address\":" + quote(setting.address) + ",\"gateway\":" + quote(setting.gateway) +
		          ",\"active\":" + b(state && trim_space(*state) == "0") + ",\"present\":" + b(state.has_value()) + "}";
	}
	Error e;
	auto state = read_file(path_join(paths.ipv6_sysctl_root, "default/disable_ipv6"), &e);
	if (e.not_exist()) {
		finish();
		return {};
	}
	if (e) {
		finish();
		return e;
	}
	supported = true;
	active = trim_space(*state) == "0";
	bool ok = true;
	auto list = interfaces(&ok);
	if (!ok) {
		finish();
		return make_error("route ip+net: netlinkrib failed");
	}
	for (const auto &iface : list) {
		if ((iface.flags & 8) || !(iface.flags & 1)) continue;  // IFF_LOOPBACK, IFF_UP
		auto mtu_data = read_file(path_join(path_join(paths.ipv6_sysctl_root, iface.name), "mtu"));
		auto mtu_value = network::atoi(trim_space(mtu_data.value_or("")));
		long long mtu = mtu_value.value_or(0);
		for (const auto &a : iface.addrs) {
			if (ip_is_v4(a.ip)) continue;
			Addr ip;
			ip.kind = 6;
			ip.b = a.ip;
			if (ip.unspecified() || ip.b[0] == 0xff) continue;
			std::string scope = "global";
			if (ip.b[0] == 0xfe && (ip.b[1] & 0xc0) == 0x80) scope = "linkLocal";
			else if ((ip.b[0] & 0xfe) == 0xfc) scope = "private";
			std::string source = ipv6_source(iface.name, ip);
			if (!addresses.empty()) addresses += ",";
			addresses += "{\"interface\":" + quote(iface.name) + ",\"address\":" + quote(a.str()) + ",\"scope\":" + quote(scope) +
			             ",\"mtu\":" + std::to_string(mtu);
			if (!source.empty()) addresses += ",\"source\":" + quote(source);
			addresses += "}";
		}
	}
	finish();
	return {};
}

Decoded<IPv6Request> decode_set_ipv6(const RequestData &data)
{
	Decoded<IPv6Request> d;
	auto b = Binder::bind(data, false);
	IPv6Request r;
	bool has_enabled = false;
	if (b.ok()) {
		has_enabled = b.boolean("enabled", "Enabled", r.enabled);
		b.str("interface", "Interface", r.interface);
		b.str("mode", "Mode", r.mode);
		std::string value;
		if (b.str("address", "Address", value)) r.address = value;
		value.clear();
		if (b.str("gateway", "Gateway", value)) r.gateway = value;
	}
	if (!b.ok() || !has_enabled) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	if ((!r.interface.empty() && !managed(r.interface)) || (!r.mode.empty() && r.mode != "dhcpv6" && r.mode != "static") ||
	    (r.interface.empty() && (!r.mode.empty() || r.address || r.gateway))) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	d.value = r;
	return d;
}

// The settings a request asks for, from the saved ones: nullopt with the refusal when one is invalid.
std::optional<IPv6Policy> ipv6_next_policy(const IPv6Request &req, const IPv6Policy &previous, Refusal &refusal)
{
	IPv6Policy next;
	for (const auto &name : ipv6_interfaces) {
		IPv6Setting setting;
		if (auto it = previous.find(name); it != previous.end()) setting = it->second;
		if (req.interface.empty() || req.interface == name) {
			if (req.address) setting.address = trim_space(*req.address);
			if (req.gateway) setting.gateway = trim_space(*req.gateway);
			if (!req.enabled) setting.mode = "";
			else if (!req.mode.empty()) setting.mode = req.mode;
			else if (setting.mode.empty()) setting.mode = "dhcpv6";
			IPv6Setting normalized;
			if (auto err = setting.normalized(normalized)) {
				refusal = {-1, err.text};
				return std::nullopt;
			}
			setting = normalized;
		}
		next[name] = setting;
	}
	return next;
}

// SetIPv6 after decoding: apply, save, and on failure apply the previous settings again.
std::pair<int, std::string> set_ipv6(const IPv6Request &req)
{
	std::lock_guard lock(ipv6_mutation);
	IPv6Policy previous;
	if (read_ipv6_policy(previous)) return {-1, "failed to read IPv6 configuration"};
	Refusal refusal{0, ""};
	auto next = ipv6_next_policy(req, previous, refusal);
	if (!next) return {refusal.code, refusal.msg};
	Error err = apply_ipv6(*next);
	if (!err) err = write_ipv6_policy(*next);
	if (err) {
		if (apply_ipv6(previous)) return {-2, "IPv6 update and recovery failed"};
		return {-2, "failed to apply IPv6 configuration"};
	}
	return {0, "success"};
}

std::pair<int, std::string> get_ipv6(std::string &json)
{
	std::lock_guard lock(ipv6_mutation);
	if (ipv6_status_json(json)) return {-1, "failed to read IPv6 status"};
	return {0, "success"};
}

bool initialize_ipv6_logged()
{
	if (auto err = initialize_ipv6_policy()) {
		log::error("failed to initialize IPv6 policy: {}", err.text);
		return false;
	}
	return true;
}

} // namespace nkos::network
