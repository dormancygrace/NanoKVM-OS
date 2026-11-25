// DNS and the udhcpc hooks, as the Go server's service/network dns.go and udhcpc_hooks.go: manual or DHCP servers,
// /boot/resolv.conf, the DHCP cache /etc/resolv.conf.dhcp, the base resolver handed to openresolv (nkos.base) and the
// hooks Alpine's udhcpc script runs.

#include "network/internal.hpp"

#include "common/log.hpp"

#include <set>

namespace nkos::network {

namespace {

constexpr std::size_t max_dns_servers = 6;
constexpr std::size_t max_scanner_line = 64 * 1024;

std::string join_lines(const std::vector<std::string> &servers) { return join(servers, "\n") + "\n"; }

Error write_dns_mode(const std::string &mode)
{
	if (auto err = write_file(paths.dns_mode_file, mode + "\n", 0644)) return wrap("failed to write dns mode", err);
	return {};
}

std::string default_dns_mode() { return exists(paths.boot_resolv) ? "manual" : "dhcp"; }

std::vector<std::string> parse_plain_dns_servers(const std::string &path, Error &err)
{
	auto data = read_file(path, &err);
	if (!data) return {};
	auto out = normalize_dns_servers(split(*data, "\n"), err);
	return out ? *out : std::vector<std::string>{};
}

Error preserve_manual_dns_servers()
{
	if (!read_manual_dns_servers().empty()) return {};
	auto servers = parse_resolv_conf(paths.boot_resolv);
	if (servers.empty()) servers = parse_resolv_conf(paths.boot_resolv_backup);
	if (servers.empty()) return {};
	if (auto err = write_file(paths.dns_servers_file, join_lines(servers), 0644)) return wrap("failed to preserve manual dns servers", err);
	return {};
}

Error backup_and_remove_boot_resolv()
{
	Error err;
	auto data = read_file(paths.boot_resolv, &err);
	if (!data) {
		if (err.not_exist()) return {};
		return wrap("failed to read boot resolv", err);
	}
	if (auto e = write_file(paths.boot_resolv_backup, *data, 0644)) return wrap("failed to backup boot resolv", e);
	if (auto e = remove(paths.boot_resolv); e && !e.not_exist()) return wrap("failed to remove boot resolv", e);
	return {};
}

Error install_udhcpc_dns_hook()
{
	if (auto err = install_udhcpc_hook("99-nanokvm-dns", udhcpc_dns_hook)) return err;
	return ensure_udhcpc_leaves_resolv_conf();
}

std::pair<std::string, std::string> default_ipv4_route()
{
	auto data = read_file(paths.proc_net_route);
	if (!data) return {};
	for (const auto &line : split(*data, "\n")) {
		auto f = fields(line);
		if (f.size() < 8 || f[1] != "00000000") continue;
		return {f[0], parse_route_gateway(f[2])};
	}
	return {};
}

std::pair<std::string, std::string> ipv4_address_info(const Interface &iface)
{
	for (const auto &a : iface.addrs)
		if (a.v4) return {ip_string(a.ip) + "/" + std::to_string(a.prefix_len), a.mask_str()};
	return {};
}

std::string fallback_ipv4_interface()
{
	for (const auto &iface : interfaces()) {
		if (!(iface.flags & 1)) continue;  // IFF_UP
		if (dns_interface_type(iface.name).empty()) continue;
		if (!ipv4_address_info(iface).first.empty()) return iface.name;
	}
	return "";
}

} // namespace

std::string strip_inline_comment(const std::string &value)
{
	auto hash = value.find('#');
	return hash == std::string::npos ? value : value.substr(0, hash);
}

std::string normalize_dns_server(const std::string &server)
{
	auto f = fields(trim_space(strip_inline_comment(server)));
	return f.empty() ? "" : f[0];
}

std::optional<std::vector<std::string>> normalize_dns_servers(const std::vector<std::string> &servers, Error &err)
{
	std::set<std::string> seen;
	std::vector<std::string> normalized;
	for (const auto &raw : servers) {
		std::string server = normalize_dns_server(raw);
		if (server.empty()) continue;
		auto ip = parse_ip(server);
		if (!ip) {
			err = make_error("invalid dns server: " + server);
			return std::nullopt;
		}
		server = ip_string(*ip);
		if (!seen.insert(server).second) continue;
		normalized.push_back(server);
	}
	err = {};
	return normalized;
}

std::optional<std::vector<std::string>> validate_dns_servers(const std::vector<std::string> &servers, Error &err)
{
	auto normalized = normalize_dns_servers(servers, err);
	if (!normalized) return std::nullopt;
	if (normalized->size() > max_dns_servers) {
		err = make_error("too many dns servers");
		return std::nullopt;
	}
	return normalized;
}

std::vector<std::string> normalize_search_domains(const std::vector<std::string> &domains)
{
	std::set<std::string> seen;
	std::vector<std::string> normalized;
	for (const auto &raw : domains) {
		std::string domain = trim_space(raw);
		if (domain.empty() || !seen.insert(domain).second) continue;
		normalized.push_back(domain);
	}
	return normalized;
}

Error parse_resolv_config(const std::string &path, ResolvConfig &out)
{
	out = ResolvConfig{};
	Error err;
	auto data = read_file(path, &err);
	if (!data) return err;
	std::vector<std::string> servers, search;
	auto lines = split(*data, "\n");
	if (!lines.empty() && lines.back().empty()) lines.pop_back();
	for (auto line : lines) {
		if (line.size() > max_scanner_line) return make_error("bufio.Scanner: token too long");
		if (!line.empty() && line.back() == '\r') line.pop_back();
		auto f = fields(strip_inline_comment(line));
		if (f.size() < 2) continue;
		if (f[0] == "nameserver") servers.push_back(f[1]);
		else if (f[0] == "search") search.insert(search.end(), f.begin() + 1, f.end());
		else if (f[0] == "domain") search.push_back(f[1]);
	}
	auto normalized = normalize_dns_servers(servers, err);
	if (!normalized) return err;
	out.servers = *normalized;
	out.search_domains = normalize_search_domains(search);
	return {};
}

std::vector<std::string> parse_resolv_conf(const std::string &path)
{
	ResolvConfig config;
	if (parse_resolv_config(path, config)) return {};
	return config.servers;
}

Error render_resolv_config(const std::string &path, const ResolvConfig &config)
{
	Error err;
	auto normalized = normalize_dns_servers(config.servers, err);
	if (!normalized) return err;
	if (normalized->empty()) return make_error("dns servers are required");
	std::string text;
	auto search = normalize_search_domains(config.search_domains);
	if (!search.empty()) text += "search " + join(search, " ") + "\n";
	for (const auto &server : *normalized) text += "nameserver " + server + "\n";
	if (auto e = mkdir_all(dir_name(path), 0755)) return wrap("failed to create dns file directory", e);
	// Keep the base resolver separate from VPN DNS. Removing the tunnel then restores the latest network settings,
	// including DHCP renewals.
	if (path == paths.etc_resolv) {
		if (auto tool = look_path("resolvconf")) {
			if (auto e = publish_base_resolver(*tool, text)) return wrap("failed to update resolver", e);
			return {};
		}
	}
	if (auto e = write_file(path, text, 0644)) return wrap("failed to write " + path, e);
	return {};
}

std::string read_raw_dns_mode()
{
	auto data = read_file(paths.dns_mode_file);
	if (!data) return "";
	std::string mode = trim_space(*data);
	return mode == "manual" || mode == "dhcp" ? mode : "";
}

std::string current_dns_mode()
{
	std::string mode = read_raw_dns_mode();
	return mode.empty() ? default_dns_mode() : mode;
}

bool can_fallback_effective_for_dhcp()
{
	std::string mode = read_raw_dns_mode();
	if (mode == "dhcp") return true;
	if (mode == "manual") return false;
	return stat_path(paths.boot_resolv).not_exist();
}

std::vector<std::string> read_manual_dns_servers()
{
	Error err;
	auto servers = parse_plain_dns_servers(paths.dns_servers_file, err);
	if (err) return {};
	return servers;
}

Error read_dhcp_resolv_config(bool allow_effective_fallback, ResolvConfig &out)
{
	ResolvConfig config;
	Error err = parse_resolv_config(paths.dhcp_resolv, config);
	if (!err && !config.servers.empty()) {
		out = config;
		return {};
	}
	if (allow_effective_fallback) {
		ResolvConfig effective;
		if (!parse_resolv_config(paths.etc_resolv, effective) && !effective.servers.empty()) {
			out = effective;
			return {};
		}
	}
	out = ResolvConfig{};
	if (err && !err.not_exist()) return err;
	return {};
}

Error refresh_dhcp_resolv_cache_from_effective()
{
	ResolvConfig config;
	if (auto err = parse_resolv_config(paths.etc_resolv, config)) {
		if (err.not_exist()) return {};
		return err;
	}
	if (config.servers.empty()) return {};
	if (config.search_domains.empty()) {
		ResolvConfig cached;
		(void)parse_resolv_config(paths.dhcp_resolv, cached);
		config.search_domains = cached.search_domains;
	}
	return render_resolv_config(paths.dhcp_resolv, config);
}

Error set_manual_dns(const std::vector<std::string> &servers)
{
	Error err;
	auto normalized = validate_dns_servers(servers, err);
	if (!normalized) return err;
	if (normalized->empty()) return make_error("dns servers are required");
	if (current_dns_mode() == "dhcp")
		if (auto e = refresh_dhcp_resolv_cache_from_effective())
			log::warn("failed to refresh dhcp dns cache before switching to manual: {}", e.text);
	if (auto e = mkdir_all(paths.dns_config_dir, 0755)) return wrap("failed to create dns config directory", e);
	if (auto e = write_dns_mode("manual")) return e;
	if (auto e = write_file(paths.dns_servers_file, join_lines(*normalized), 0644)) return wrap("failed to write dns servers", e);
	if (auto e = render_resolv_config(paths.boot_resolv, ResolvConfig{*normalized, {}})) return e;
	if (auto e = clear_dhcpv6_resolvers()) return e;
	if (auto e = render_resolv_config(paths.etc_resolv, ResolvConfig{*normalized, {}})) return e;
	return install_udhcpc_dns_hook();
}

Error set_dhcp_dns()
{
	ResolvConfig config;
	if (auto err = read_dhcp_resolv_config(can_fallback_effective_for_dhcp(), config)) return wrap("failed to read dhcp dns", err);
	if (config.servers.empty()) return make_error("no dhcp dns is currently available");
	if (auto e = mkdir_all(paths.dns_config_dir, 0755)) return wrap("failed to create dns config directory", e);
	if (auto e = render_resolv_config(paths.dhcp_resolv, config)) return wrap("failed to write dhcp dns cache", e);
	if (auto e = write_dns_mode("dhcp")) return e;
	if (auto e = preserve_manual_dns_servers()) return e;
	if (auto e = backup_and_remove_boot_resolv()) return e;
	if (auto e = render_resolv_config(paths.etc_resolv, config)) return e;
	return install_udhcpc_dns_hook();
}

std::string parse_route_gateway(const std::string &value)
{
	auto gateway = parse_uint(value, 16, 32);
	if (!gateway || *gateway == 0) return "";
	auto g = *gateway;
	return addr4(static_cast<std::uint8_t>(g), static_cast<std::uint8_t>(g >> 8), static_cast<std::uint8_t>(g >> 16),
	             static_cast<std::uint8_t>(g >> 24)).str();
}

std::string dns_interface_type(const std::string &name)
{
	if (name.rfind("eth", 0) == 0 || name.rfind("en", 0) == 0) return "Wired";
	if (name.rfind("wlan", 0) == 0 || name.rfind("wl", 0) == 0) return "Wireless";
	return "";
}

DNSInfo dns_info()
{
	DNSInfo info;
	auto [name, gateway] = default_ipv4_route();
	if (name.empty()) name = fallback_ipv4_interface();
	info.interface = name;
	info.gateway = gateway;
	ResolvConfig dhcp;
	(void)read_dhcp_resolv_config(can_fallback_effective_for_dhcp(), dhcp);
	info.search_domains = dhcp.search_domains;
	if (name.empty()) return info;
	auto iface = interface_by_name(name);
	if (!iface) return info;
	info.type = dns_interface_type(iface->name);
	std::tie(info.address, info.subnet_mask) = ipv4_address_info(*iface);
	return info;
}

std::string dns_json(const std::string &mode, const std::vector<std::string> &servers, const std::vector<std::string> &effective,
                     const std::vector<std::string> &dhcp, const DNSInfo &info)
{
	return "{\"mode\":" + quote(mode) + ",\"servers\":" + json_strings(servers, true) + ",\"effective\":" + json_strings(effective, true) +
	       ",\"dhcp\":" + json_strings(dhcp, true) + ",\"info\":{\"interface\":" + quote(info.interface) + ",\"type\":" + quote(info.type) +
	       ",\"address\":" + quote(info.address) + ",\"subnetMask\":" + quote(info.subnet_mask) + ",\"gateway\":" + quote(info.gateway) +
	       ",\"searchDomains\":" + json_strings(info.search_domains, true) + "}}";
}

Decoded<std::pair<std::string, std::vector<std::string>>> decode_set_dns(const RequestData &data)
{
	Decoded<std::pair<std::string, std::vector<std::string>>> d;
	auto b = Binder::bind(data, false);
	std::string mode;
	std::vector<std::string> servers;
	if (b.ok()) {
		b.str("mode", "Mode", mode);
		b.strings("servers", "Servers", servers);
	}
	if (!b.ok() || (mode != "manual" && mode != "dhcp")) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	d.value = std::pair{mode, servers};
	return d;
}

// --- udhcpc hooks (udhcpc_hooks.go) ----------------------------------------------------------------------------------

const char *udhcpc_resolv_line = "RESOLV_CONF=\"no\" # NanoKVM: openresolv owns /etc/resolv.conf";

Error install_udhcpc_hook_in(const std::vector<std::string> &dirs, const std::string &name, std::string_view script)
{
	for (const auto &dir : dirs) {
		if (auto err = mkdir_all(dir, 0755)) return wrap("create DHCP hook directory", err);
		if (auto err = write_file(path_join(dir, name), script, 0755)) return wrap("install DHCP hook " + name, err);
	}
	if (auto err = remove(path_join(paths.legacy_hook_dir, name)); err && !err.not_exist())
		return wrap("remove legacy DHCP hook " + name, err);
	return {};
}

Error install_udhcpc_hook(const std::string &name, std::string_view script)
{
	return install_udhcpc_hook_in(paths.udhcpc_hook_dirs, name, script);
}

Error ensure_udhcpc_leaves_resolv_conf()
{
	Error err;
	auto data = read_file(paths.udhcpc_conf, &err);
	if (err && !err.not_exist()) return wrap("read udhcpc configuration", err);
	std::string text = data.value_or("");
	for (const auto &line : split(text, "\n"))
		if (trim_space(line) == udhcpc_resolv_line) return {};
	if (!text.empty() && text.back() != '\n') text += "\n";
	if (auto e = mkdir_all(dir_name(paths.udhcpc_conf), 0755)) return wrap("create udhcpc configuration directory", e);
	if (auto e = write_file(paths.udhcpc_conf, text + udhcpc_resolv_line + "\n", 0644)) return wrap("write udhcpc configuration", e);
	return {};
}

std::function<Error(const std::string &, const std::string &, const std::vector<std::string> &)> resolvconf_runner =
	[](const std::string &tool, const std::string &input, const std::vector<std::string> &args) -> Error {
	std::vector<std::string> argv{tool};
	argv.insert(argv.end(), args.begin(), args.end());
	RunOptions options;
	options.timeout = std::chrono::seconds(5);
	options.input = input;
	auto r = run(argv, options);
	if (!r.ok()) return make_error(r.error + ": " + trim_space(r.err_out));
	return {};
};

Error publish_base_resolver(const std::string &tool, const std::string &content)
{
	auto err = resolvconf_runner(tool, content, {"-a", "nkos.base"});
	if (!err || err.text.find("signature mismatch") == std::string::npos) return err;
	if (auto e = resolvconf_runner(tool, "", {"-u"})) return wrap("take over /etc/resolv.conf", e);
	return resolvconf_runner(tool, content, {"-a", "nkos.base"});
}

Error adopt_base_resolver(const std::string &tool, std::string base)
{
	bool has_settings = false, has_servers = false;
	for (const auto &raw : split(base, "\n")) {
		std::string line = raw.substr(0, raw.find('#'));
		line = line.substr(0, line.find(';'));
		auto f = fields(line);
		if (f.size() > 1) {
			has_settings = true;
			has_servers = has_servers || f[0] == "nameserver";
		}
	}
	if (has_settings && !has_servers) {
		RunOptions options;
		options.timeout = std::chrono::seconds(5);
		auto r = run({tool, "-l", "nkos.base"}, options);
		// openresolv returns 2 when the requested key does not exist yet.
		if (!r.ok() && r.exit_code != 2) return wrap("read existing base dns", make_error(r.error));
		base = r.out + "\n" + base;
	}
	if (auto e = resolvconf_runner(tool, "", {"-u"})) return wrap("take over " + paths.etc_resolv, e);
	if (has_settings)
		if (auto e = resolvconf_runner(tool, base, {"-a", "nkos.base"})) return wrap("publish base dns", e);
	return {};
}

Error adopt_resolv_conf()
{
	auto tool = look_path("resolvconf");
	if (!tool) return {};
	Error err;
	auto data = read_file(paths.etc_resolv, &err);
	if (!data) {
		if (err.not_exist()) return {};
		return wrap("read " + paths.etc_resolv, err);
	}
	if (data->find("# Generated by resolvconf") != std::string::npos) return {};
	std::string base = *data;
	if (current_dns_mode() == "manual") {
		if (auto servers = read_manual_dns_servers(); !servers.empty()) base = "nameserver " + join(servers, "\nnameserver ") + "\n";
	} else if (auto e = refresh_dhcp_resolv_cache_from_effective()) {
		log::warn("failed to cache dhcp dns before handing resolv.conf to openresolv: {}", e.text);
	}
	if (auto e = adopt_base_resolver(*tool, base)) return e;
	log::info("handed {} to openresolv", paths.etc_resolv);
	return {};
}

Error clear_dhcpv6_resolvers()
{
	auto tool = look_path("resolvconf");
	if (!tool) return {};
	for (const auto &name : ipv6_interfaces)
		if (auto err = resolvconf_runner(*tool, "", {"-d", "nkos.dhcpv6." + name, "-f"})) return wrap("remove DHCPv6 DNS for " + name, err);
	return {};
}

Error ensure_dhcp_integration_error()
{
	std::vector<Error> errs;
	errs.push_back(install_udhcpc_hook("99-nanokvm-dns", udhcpc_dns_hook));
	errs.push_back(install_udhcpc_hook("99-nanokvm-gateway", udhcpc_gateway_hook));
	// Also on deconfig: the rule goes with the lease.
	auto dirs = paths.udhcpc_hook_dirs;
	dirs.push_back(paths.udhcpc_deconfig_dir);
	errs.push_back(install_udhcpc_hook_in(dirs, "98-nanokvm-wifi-source", udhcpc_wifi_source_hook));
	errs.push_back(ensure_udhcpc_leaves_resolv_conf());
	errs.push_back(adopt_resolv_conf());
	return join_errors(errs);
}

} // namespace nkos::network
