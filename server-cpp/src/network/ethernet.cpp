// Ethernet IPv4 and VLAN, as the Go server's service/network ethernet.go and ethernet_vlan.go: /boot/eth.nodhcp
// ("address/prefix gateway", S30eth's format), /boot/eth.disabled and /boot/eth.vlan.

#include "network/internal.hpp"

#include "common/log.hpp"

#include <cstdint>

namespace nkos::network {

std::chrono::milliseconds restart_delay{1000};

std::function<std::string(const std::string &)> ethernet_ipv4 = [](const std::string &name) -> std::string {
	auto iface = interface_by_name(name);
	if (!iface) return "";
	for (const auto &a : iface->addrs)
		if (a.v4) return a.str();
	return "";
};

std::function<Error()> restart_ethernet = [] {
	RunOptions options;
	options.combined = true;
	auto r = run(unprotected(ethernet_init_script, {"restart"}), options);
	if (!r.ok()) {
		log::error("failed to restart ethernet: {}: {}", r.error, trim_space(r.out));
		return make_error(r.error);
	}
	return Error{};
};

std::string ethernet_config_json(const EthernetConfig &c)
{
	auto b = [](bool v) { return v ? "true" : "false"; };
	std::string out = std::string("{\"enabled\":") + b(c.enabled) + ",\"adminUp\":" + b(c.admin_up) + ",\"linkUp\":" + b(c.link_up) +
	                  ",\"mode\":" + quote(c.mode) + ",\"interface\":" + quote(c.interface) + ",\"address\":" + quote(c.address) +
	                  ",\"subnetMask\":" + quote(c.subnet_mask) + ",\"gateway\":" + quote(c.gateway) +
	                  ",\"vlanEnabled\":" + b(c.vlan_enabled) + ",\"vlanId\":" + std::to_string(c.vlan_id);
	if (!c.current_address.empty()) out += ",\"currentAddress\":" + quote(c.current_address);
	return out + "}";
}

namespace {

std::uint32_t v4_value(const Addr &a)
{
	return static_cast<std::uint32_t>(a.b[0]) << 24 | static_cast<std::uint32_t>(a.b[1]) << 16 |
	       static_cast<std::uint32_t>(a.b[2]) << 8 | a.b[3];
}

Addr v4_from(std::uint32_t v)
{
	return addr4(static_cast<std::uint8_t>(v >> 24), static_cast<std::uint8_t>(v >> 16), static_cast<std::uint8_t>(v >> 8),
	             static_cast<std::uint8_t>(v));
}

} // namespace

std::optional<int> subnet_mask_to_prefix(const std::string &subnet_mask, Error &err)
{
	auto mask = parse_addr(trim_space(subnet_mask));
	if (!mask || !mask->is4()) {
		err = make_error("invalid subnet mask");
		return std::nullopt;
	}
	std::uint32_t value = v4_value(*mask);
	int prefix = 0;
	bool seen_zero = false;
	for (int bit = 31; bit >= 0; bit--) {
		if (value & (1u << bit)) {
			if (seen_zero) {
				err = make_error("subnet mask must be contiguous");
				return std::nullopt;
			}
			prefix++;
		} else {
			seen_zero = true;
		}
	}
	if (prefix < 1 || prefix > 30) {
		err = make_error("subnet mask must be between 255.0.0.0 and 255.255.255.252");
		return std::nullopt;
	}
	err = {};
	return prefix;
}

std::string prefix_to_subnet_mask(int prefix)
{
	std::uint32_t value = prefix <= 0 ? 0 : prefix >= 32 ? ~0u : ~0u << (32 - prefix);
	return v4_from(value).str();
}

Addr ipv4_broadcast(const Prefix &prefix)
{
	std::uint32_t value = v4_value(prefix.masked().addr);
	value |= prefix.bits >= 32 ? 0 : (1u << (32 - prefix.bits)) - 1;
	return v4_from(value);
}

Error validate_static_ethernet(const std::string &address_in, const std::string &subnet_mask, const std::string &gateway_in,
                               StaticEthernet &out)
{
	std::string address = trim_space(address_in);
	Addr ip;
	int bits = 0;
	if (address.find('/') != std::string::npos) {
		auto cidr = parse_prefix(address);
		if (!cidr) return make_error("invalid static IP address");
		ip = cidr->addr;
		bits = cidr->bits;
		if (!trim_space(subnet_mask).empty()) {
			Error err;
			auto mask_bits = subnet_mask_to_prefix(subnet_mask, err);
			if (!mask_bits) return err;
			if (*mask_bits != bits) return make_error("subnet mask does not match the prefix length");
		}
	} else {
		auto parsed = parse_addr(address);
		if (!parsed) return make_error("invalid static IP address");
		ip = *parsed;
		Error err;
		auto mask_bits = subnet_mask_to_prefix(subnet_mask, err);
		if (!mask_bits) return err;
		bits = *mask_bits;
	}
	if (!ip.is4() || !ip.valid() || ip.unspecified() || ip.loopback() || ip.multicast()) return make_error("invalid static IP address");
	if (bits < 1 || bits > 30) return make_error("static IPv4 prefix length must be between 1 and 30");
	Prefix prefix{ip, bits};
	if (ip == prefix.masked().addr || ip == ipv4_broadcast(prefix)) return make_error("invalid static IP address");
	out.prefix = prefix;
	out.gateway = Addr{};
	// No gateway: a link without a router; S30eth adds no default route.
	if (trim_space(gateway_in).empty()) return {};
	auto gw = parse_addr(trim_space(gateway_in));
	if (!gw || !gw->is4() || !gw->valid() || gw->unspecified() || gw->loopback() || gw->multicast()) return make_error("invalid gateway");
	if (!prefix.contains(*gw) || *gw == ip || *gw == prefix.masked().addr || *gw == ipv4_broadcast(prefix))
		return make_error("gateway must be in the same subnet as the static IP");
	out.gateway = *gw;
	return {};
}

Error read_ethernet_enabled(bool &enabled)
{
	auto err = stat_path(paths.ethernet_disabled);
	if (!err) {
		enabled = false;
		return {};
	}
	if (err.not_exist()) {
		enabled = true;
		return {};
	}
	enabled = false;
	return wrap("failed to read ethernet enabled state", err);
}

bool read_ethernet_carrier()
{
	auto data = read_file(paths.ethernet_carrier);
	return data && trim_space(*data) == "1";
}

bool read_ethernet_admin_up()
{
	auto data = read_file(paths.ethernet_flags);
	if (!data) return false;
	auto flags = parse_uint(trim_space(*data), 0, 32);
	return flags && (*flags & 1);  // IFF_UP
}

Error read_ethernet_vlan(bool &enabled, long long &id)
{
	enabled = false;
	id = 0;
	Error err;
	auto data = read_file(paths.ethernet_vlan, &err);
	if (err.not_exist()) return {};
	if (err) return err;
	std::string text = trim_space(*data);
	auto n = network::atoi(text);
	bool digits = !text.empty() && text.find_first_not_of("0123456789") == std::string::npos;
	if (!digits || !n || *n < 1 || *n > 4094) return make_error("invalid Ethernet VLAN ID");
	enabled = true;
	id = *n;
	return {};
}

Error write_ethernet_vlan(bool enabled, long long id)
{
	if (!enabled) {
		auto err = remove(paths.ethernet_vlan);
		if (err && !err.not_exist()) return wrap("failed to disable Ethernet VLAN", err);
		return {};
	}
	if (id < 1 || id > 4094) return make_error("VLAN ID must be between 1 and 4094");
	if (auto err = mkdir_all(dir_name(paths.ethernet_vlan), 0755)) return wrap("failed to create Ethernet configuration directory", err);
	std::string tmp = paths.ethernet_vlan + ".tmp";
	if (auto err = write_file(tmp, std::to_string(id) + "\n", 0600)) return wrap("failed to write Ethernet VLAN", err);
	if (auto err = rename(tmp, paths.ethernet_vlan)) return wrap("failed to save Ethernet VLAN", err);
	return {};
}

Error read_ethernet_config(EthernetConfig &config)
{
	bool enabled = false;
	if (auto err = read_ethernet_enabled(enabled)) {
		config = EthernetConfig{};
		return err;
	}
	config = EthernetConfig{};
	config.enabled = enabled;
	config.admin_up = read_ethernet_admin_up();
	config.link_up = read_ethernet_carrier();
	config.mode = "dhcp";
	config.interface = "eth0";
	if (auto err = read_ethernet_vlan(config.vlan_enabled, config.vlan_id)) return err;
	std::string name = "eth0";
	if (config.vlan_enabled) name = "eth0." + std::to_string(config.vlan_id);
	config.current_address = ethernet_ipv4(name);
	Error err;
	auto data = read_file(paths.ethernet_config, &err);
	if (err.not_exist()) return {};
	if (err) return err;
	// bufio.Scanner lines: split at \n, a \r before it dropped.
	auto lines = split(*data, "\n");
	if (!lines.empty() && lines.back().empty()) lines.pop_back();
	for (auto line : lines) {
		if (!line.empty() && line.back() == '\r') line.pop_back();
		if (!line.empty() && line.back() == '\r') line.pop_back();
		line = trim_space(line);
		if (auto comment = line.find('#'); comment != std::string::npos) line = trim_space(line.substr(0, comment));
		auto f = fields(line);
		if (f.empty()) continue;
		auto invalid = make_error("invalid ethernet configuration");
		if (f.size() > 2) return invalid;
		auto prefix = parse_prefix(f[0]);
		if (!prefix || !prefix->addr.is4()) return invalid;
		if (prefix->bits < 1 || prefix->bits > 30) return invalid;
		Addr gateway = prefix->masked().addr.next();
		if (f.size() == 2 && f[1] == "none") {
			gateway = Addr{};
		} else if (f.size() == 2) {
			auto gw = parse_addr(f[1]);
			if (!gw || !gw->is4()) return invalid;
			gateway = *gw;
		}
		config.mode = "static";
		config.address = prefix->addr.str();
		config.subnet_mask = prefix_to_subnet_mask(prefix->bits);
		if (gateway.valid()) config.gateway = gateway.str();
		return {};
	}
	return {};
}

Error write_ethernet_enabled(bool enabled)
{
	if (enabled) {
		auto err = remove(paths.ethernet_disabled);
		if (err && !err.not_exist()) return wrap("failed to enable ethernet", err);
		return {};
	}
	if (auto err = mkdir_all(dir_name(paths.ethernet_disabled), 0755)) return wrap("failed to create ethernet configuration directory", err);
	std::string tmp = paths.ethernet_disabled + ".tmp";
	if (auto err = write_file(tmp, "", 0600)) return wrap("failed to write ethernet enabled state", err);
	if (auto err = rename(tmp, paths.ethernet_disabled)) return wrap("failed to save ethernet enabled state", err);
	return {};
}

Error write_ethernet_config(const EthernetConfig &config)
{
	if (config.mode == "dhcp") {
		auto err = remove(paths.ethernet_config);
		if (err && !err.not_exist()) return wrap("failed to enable dhcp", err);
		return {};
	}
	if (config.mode == "static") {
		StaticEthernet s;
		if (auto err = validate_static_ethernet(config.address, config.subnet_mask, config.gateway, s)) return err;
		// A missing second field means network+1 to S30eth (the Sipeed format), so "none" records that there is no
		// gateway.
		std::string contents = s.prefix.str() + " " + (s.gateway.valid() ? s.gateway.str() : "none") + "\n";
		std::string tmp = paths.ethernet_config + ".tmp";
		if (auto err = mkdir_all(dir_name(paths.ethernet_config), 0755)) return wrap("failed to create ethernet configuration directory", err);
		if (auto err = write_file(tmp, contents, 0600)) return wrap("failed to write ethernet configuration", err);
		if (auto err = rename(tmp, paths.ethernet_config)) return wrap("failed to save ethernet configuration", err);
		return {};
	}
	return make_error("invalid ethernet mode");
}

Error write_ethernet(const EthernetConfig &config, bool vlan)
{
	if (auto err = write_ethernet_config(config)) return err;
	if (vlan)
		if (auto err = write_ethernet_vlan(config.vlan_enabled, config.vlan_id)) return err;
	return write_ethernet_enabled(config.enabled);
}

Decoded<EthernetRequest> decode_set_ethernet(const RequestData &data)
{
	Decoded<EthernetRequest> d;
	auto b = Binder::bind(data, false);
	EthernetRequest r;
	bool enabled = false, vlan_enabled = false;
	bool has_enabled = b.ok() && b.boolean("enabled", "Enabled", enabled);
	std::string mode, address, mask, gateway;
	long long vlan_id = 0;
	bool has_vlan = false;
	if (b.ok()) {
		b.str("mode", "Mode", mode);
		b.str("address", "Address", address);
		b.str("subnetMask", "SubnetMask", mask);
		b.str("gateway", "Gateway", gateway);
		has_vlan = b.boolean("vlanEnabled", "VLANEnabled", vlan_enabled);
		b.integer("vlanId", "VLANID", vlan_id);
		b.boolean("force", "Force", r.force);
	}
	// ParseFormRequest: binding, then validate: enabled required, mode one of dhcp and static.
	if (!b.ok() || !has_enabled || (mode != "dhcp" && mode != "static")) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	r.config.enabled = enabled;
	r.config.mode = mode;
	r.config.interface = "eth0";
	if (mode == "static") {
		StaticEthernet s;
		if (auto err = validate_static_ethernet(address, mask, gateway, s)) {
			d.refusal = {-1, err.text};
			return d;
		}
		r.config.address = s.prefix.addr.str();
		r.config.subnet_mask = prefix_to_subnet_mask(s.prefix.bits);
		if (s.gateway.valid()) r.config.gateway = s.gateway.str();
	}
	if (has_vlan) {
		r.vlan = true;
		r.config.vlan_enabled = vlan_enabled;
		r.config.vlan_id = vlan_id;
		if (vlan_enabled && (vlan_id < 1 || vlan_id > 4094)) {
			d.refusal = {-1, "VLAN ID must be between 1 and 4094"};
			return d;
		}
	}
	d.value = r;
	return d;
}

} // namespace nkos::network
