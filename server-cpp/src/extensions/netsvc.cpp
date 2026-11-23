#include "extensions/netsvc.hpp"

#include "common/log.hpp"
#include "extensions/gopath.hpp"
#include "osupdate/godecode.hpp"

#include <algorithm>
#include <cerrno>
#include <dirent.h>
#include <fcntl.h>
#include <functional>
#include <mutex>
#include <set>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::extensions::netsvc {

namespace {

using network::trim_space;
using K = godecode::Value::Kind;

constexpr const char *runlevel = "/etc/runlevels/default";
// The marker in the first line of a configuration file written here.
const std::string marker = "# Written by NanoKVM OS from Settings > Services; changes made here are replaced.\n";

Result output(int seconds, std::vector<std::string> argv)
{
	Command c;
	c.argv = std::move(argv);
	c.timeout = std::chrono::seconds(seconds);
	c.capture = Command::Capture::combined;
	return run(c);
}

bool rc(int seconds, std::vector<std::string> argv) { return output(seconds, std::move(argv)).ok(); }

std::string tail(const std::string &out)
{
	std::string text = trim_space(out);
	if (text.size() > 300) text = text.substr(text.size() - 300);
	return text;
}

bool has_prefix(std::string_view s, std::string_view p) { return s.substr(0, p.size()) == p; }

// strings.Cut.
bool cut(std::string_view s, std::string_view sep, std::string &before, std::string &after)
{
	auto i = s.find(sep);
	if (i == std::string_view::npos) {
		before = s;
		after.clear();
		return false;
	}
	before = s.substr(0, i);
	after = s.substr(i + sep.size());
	return true;
}

// stage: the new content of a configuration file written next to it; the name of that file, or "" with error.
std::string stage(const std::string &path, const std::string &data, mode_t mode, const std::string &group, std::string &error)
{
	std::string dir = gopath::dir(path);
	if (error = gopath::mkdir_all(dir, 0755); !error.empty()) return "";
	std::string name;
	int fd = -1;
	for (int attempt = 0; attempt < 10000 && fd < 0; attempt++) {
		name = dir + "/.pending-" + temp_suffix();
		fd = ::open(name.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd < 0 && errno != EEXIST) {
			error = gopath::path_error("open", name, errno);
			return "";
		}
	}
	for (std::size_t done = 0; done < data.size() && error.empty();) {
		ssize_t n = ::write(fd, data.data() + done, data.size() - done);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) error = gopath::path_error("write", name, errno);
		else done += static_cast<std::size_t>(n);
	}
	if (error.empty() && ::fchmod(fd, mode) != 0) error = gopath::path_error("chmod", name, errno);
	if (error.empty() && ::fsync(fd) != 0) error = gopath::path_error("sync", name, errno);
	if (::close(fd) != 0 && error.empty()) error = gopath::path_error("close", name, errno);
	if (error.empty() && !group.empty()) error = output(5, {"chgrp", group, name}).error;
	if (!error.empty()) {
		::unlink(name.c_str());
		return "";
	}
	return name;
}

// writeFiles: several configuration files, all of them or none; a file that already has its content stays.
std::string write_files(const std::map<std::string, std::string> &files, mode_t mode, const std::string &group)
{
	std::vector<std::string> paths;
	std::map<std::string, std::string> previous;
	for (const auto &[path, data] : files) {
		if (auto old = read_file(path)) {
			if (*old == data) continue;
			previous[path] = *old;
		}
		paths.push_back(path);
	}
	std::vector<std::string> staged;
	auto cleanup = [&] {
		for (const auto &name : staged) ::unlink(name.c_str());
	};
	for (const auto &path : paths) {
		std::string error;
		std::string name = stage(path, files.at(path), mode, group, error);
		if (name.empty()) {
			cleanup();
			return error;
		}
		staged.push_back(name);
	}
	for (std::size_t i = 0; i < paths.size(); i++) {
		if (::rename(staged[i].c_str(), paths[i].c_str()) == 0) continue;
		std::string error = "rename " + staged[i] + " " + paths[i] + ": " + network::errno_text(errno);
		for (std::size_t j = 0; j < i; j++) {
			auto was = previous.find(paths[j]);
			if (was == previous.end()) {
				(void)remove_file(paths[j]);
			} else {
				std::string e;
				std::string name = stage(paths[j], was->second, mode, group, e);
				if (!name.empty() && ::rename(name.c_str(), paths[j].c_str()) != 0) ::unlink(name.c_str());
			}
		}
		cleanup();
		return error;
	}
	cleanup();
	return "";
}

std::string write_file(const std::string &path, const std::string &data, mode_t mode, const std::string &group)
{
	return write_files({{path, data}}, mode, group);
}

// secret: n letters and digits nobody has to remember.
bool secret(std::size_t n, std::string &out)
{
	static const char alphabet[] = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
	std::string raw(n, '\0');
	if (!random_bytes(reinterpret_cast<unsigned char *>(raw.data()), n)) return false;
	for (auto &c : raw) c = alphabet[static_cast<unsigned char>(c) % (sizeof alphabet - 1)];
	out = raw;
	return true;
}

bool ours(const std::string &path)
{
	auto data = read_file(path);
	return data && has_prefix(*data, marker);
}

struct Unit {
	std::string name, binary, service, pidfile;
	std::vector<std::string> configs;
	std::function<void()> access;
	std::function<bool()> complete;
	std::function<std::string()> fresh;
	std::mutex mutex;

	bool installed() const { return stat_ok(binary); }
	bool enabled() const { return lstat_ok(std::string(runlevel) + "/" + service); }

	bool alive() const
	{
		if (auto data = read_file(pidfile))
			if (auto pid = network::atoi(trim_space(*data)); pid && *pid > 0)
				if (stat_ok("/proc/" + std::to_string(*pid))) return true;
		return rc(5, {"pidof", gopath::base(binary)});
	}

	bool running() const { return rc(5, {"rc-service", service, "status"}) && alive(); }

	// foreign: a configuration file neither written here nor the package's own (apk audit lists it).
	std::string foreign(bool &result) const
	{
		result = false;
		std::set<std::string> unknown, dirs;
		for (const auto &path : configs) {
			auto data = read_file(path);
			if (!data || has_prefix(*data, marker)) continue;
			unknown.insert(path.substr(path[0] == '/' ? 1 : 0));
			dirs.insert(gopath::dir(path));
		}
		for (const auto &dir : dirs) {
			auto r = output(30, {"/sbin/apk", "audit", dir});
			if (!r.ok()) return "apk audit " + dir + ": " + tail(r.out);
			for (const auto &line : network::split(r.out, "\n")) {
				auto fields = network::fields(line);
				if (fields.size() == 2 && unknown.count(fields[1])) {
					result = true;
					return "";
				}
			}
		}
		return "";
	}

	// foreignNow: a configuration that could not be examined is not offered for editing.
	bool foreign_now() const
	{
		bool f = false;
		auto err = foreign(f);
		if (!err.empty()) log::warn("{} configuration: {}", name, err);
		return f || !err.empty();
	}

	void state(bool light, bool &is_enabled, bool &is_running, bool &is_foreign) const
	{
		is_enabled = enabled();
		if (light && !is_enabled && !alive()) {
			is_running = is_foreign = false;
			return;
		}
		is_running = running();
		is_foreign = foreign_now();
	}

	std::string stop() const
	{
		auto r = output(30, {"rc-service", service, "stop"});
		if (r.ok()) return "";
		if (alive()) return name + " did not stop: " + tail(r.out);
		(void)rc(10, {"rc-service", service, "zap"});
		return "";
	}

	void start() const
	{
		auto r = output(60, {"rc-service", service, "start"});
		if (!r.ok()) log::warn("{} did not start: {}", name, tail(r.out));
	}

	std::string apply(const std::function<std::string()> &write) const
	{
		bool on = enabled() || alive();
		if (on)
			if (auto e = stop(); !e.empty()) return e;
		std::string err = write();
		if (on) start();
		return err;
	}

	std::string prepare() const
	{
		if (access) access();
		if (!complete || complete()) return "";
		bool f = false;
		if (auto err = foreign(f); f || !err.empty()) return err;
		return fresh();
	}

	std::string set_enabled(bool on) const
	{
		if (on) {
			if (auto e = prepare(); !e.empty()) return e;
			// The boot entry after the stop: a daemon that does not stop leaves the service as it was.
			if (auto e = stop(); !e.empty()) return e;
			if (auto r = output(10, {"rc-update", "add", service, "default"}); !r.ok()) return r.error;
			start();
			return "";
		}
		if (auto e = stop(); !e.empty()) return e;
		return output(10, {"rc-update", "del", service, "default"}).error;
	}
};

// --- LLDP -------------------------------------------------------------------------------------------------------

constexpr const char *lldp_conf_d = "/etc/conf.d/lldpd";

std::string write_lldp(bool cdp)
{
	return write_file(lldp_conf_d, marker + "LLDPD_OPTS=\"" + (cdp ? "-c" : "") + "\"\n", 0644, "");
}

bool read_lldp()
{
	auto data = read_file(lldp_conf_d);
	for (const auto &raw : network::split(data ? *data : "", "\n")) {
		std::string line = trim_space(raw);
		if (!has_prefix(line, "LLDPD_OPTS=")) continue;
		std::string value = network::trim_chars(line.substr(11), "\"'");
		for (const auto &option : network::fields(value))
			if (option == "-c") return true;
		return false;
	}
	return false;
}

Unit &lldp()
{
	static Unit u{"LLDP", "/usr/sbin/lldpd", "lldpd", "/run/lldpd/lldpd.pid", {lldp_conf_d}, nullptr, nullptr,
	              [] { return write_lldp(false); }, {}};
	return u;
}

std::string neighbor_json(const Neighbor &n)
{
	return "{\"interface\":" + q(n.interface) + ",\"protocol\":" + q(n.protocol) + ",\"name\":" + q(n.name) +
	       ",\"description\":" + q(n.description) + ",\"port\":" + q(n.port) + ",\"portDescription\":" + q(n.port_description) +
	       ",\"address\":" + q(n.address) + ",\"age\":" + q(n.age) + "}";
}

std::string lldp_state(bool light)
{
	Unit &u = lldp();
	bool installed = u.installed(), enabled = false, running = false, foreign = false, cdp = false;
	std::vector<Neighbor> neighbors;
	if (installed) {
		u.state(light, enabled, running, foreign);
		cdp = read_lldp();
		if (running)
			if (auto r = output(5, {"lldpcli", "-f", "json0", "show", "neighbors"}); r.ok()) neighbors = parse_lldp_neighbors(r.out);
	}
	std::string list = "[";
	for (std::size_t i = 0; i < neighbors.size(); i++) list += (i ? "," : "") + neighbor_json(neighbors[i]);
	return "{\"installed\":" + json_bool(installed) + ",\"enabled\":" + json_bool(enabled) + ",\"running\":" + json_bool(running) +
	       ",\"foreign\":" + json_bool(foreign) + ",\"settings\":{\"cdp\":" + json_bool(cdp) + "},\"neighbors\":" + list + "]}";
}

// --- SNMP -------------------------------------------------------------------------------------------------------

constexpr const char *snmp_conf = "/etc/snmp/snmpd.conf";

struct SnmpSettings {
	std::string community, network, location, contact;
};

std::string snmp_file(const SnmpSettings &s)
{
	std::string source = s.network.empty() ? "default" : s.network;
	std::string b = marker + "agentAddress udp:161\n";
	// What a monitoring system reads about a host; not the process table, which shows command lines.
	for (const char *oid : {".1.3.6.1.2.1.1", ".1.3.6.1.2.1.2", ".1.3.6.1.2.1.4", ".1.3.6.1.2.1.31", ".1.3.6.1.2.1.25.1",
	                        ".1.3.6.1.2.1.25.2", ".1.3.6.1.2.1.25.3", ".1.3.6.1.4.1.2021"})
		b += std::string("view nanokvm included ") + oid + "\n";
	b += "rocommunity " + s.community + " " + source + " -V nanokvm\n";
	// an empty line would leave the agent's own default in place
	if (!s.location.empty()) b += "sysLocation " + s.location + "\n";
	if (!s.contact.empty()) b += "sysContact " + s.contact + "\n";
	b += "sysServices 72\n";
	return b;
}

SnmpSettings read_snmp()
{
	SnmpSettings s;
	auto data = read_file(snmp_conf);
	if (!data || !has_prefix(*data, marker)) return s;
	for (const auto &line : network::split(*data, "\n")) {
		std::string key, value;
		cut(line, " ", key, value);
		if (key == "rocommunity") {
			auto fields = network::fields(value);
			if (fields.size() >= 2) {
				s.community = fields[0];
				if (fields[1] != "default") s.network = fields[1];
			}
		} else if (key == "sysLocation") {
			s.location = value;
		} else if (key == "sysContact") {
			s.contact = value;
		}
	}
	return s;
}

std::string fresh_snmp()
{
	SnmpSettings s;
	if (!secret(16, s.community)) return "crypto/rand: no randomness";
	return write_file(snmp_conf, snmp_file(s), 0600, "");
}

Unit &snmp()
{
	static Unit u{"SNMP", "/usr/sbin/snmpd", "snmpd", "/run/snmpd.pid", {snmp_conf}, nullptr, [] { return ours(snmp_conf); },
	              fresh_snmp, {}};
	return u;
}

bool snmp_community(const std::string &s)
{
	if (s.size() < 8 || s.size() > 32) return false;
	for (char c : s)
		if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-'))
			return false;
	return true;
}

// ^[^\x00-\x1f\x7f"\\]{0,64}$: up to 64 runes (an invalid byte is one), none of them a control, quote or backslash.
bool snmp_text(const std::string &s)
{
	std::size_t runes = 0;
	for (std::size_t i = 0; i < s.size(); runes++) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x20 || c == 0x7f || c == '"' || c == '\\') return false;
		i += decode_rune(s, i).second;
	}
	return runes <= 64;
}

std::string snmp_validate(const SnmpSettings &s)
{
	std::string ignored;
	if (!snmp_community(s.community)) return "the community is 8 to 32 letters, digits, dots, dashes or underscores";
	if (!snmp_source(s.network, ignored)) return "the network is an IPv4 address or a network such as 192.168.1.0/24";
	if (!snmp_text(s.location) || !snmp_text(s.contact)) return "location and contact are up to 64 characters of plain text";
	return "";
}

std::string snmp_state(bool light)
{
	Unit &u = snmp();
	bool installed = u.installed(), enabled = false, running = false, foreign = false;
	SnmpSettings s;
	if (installed) {
		u.state(light, enabled, running, foreign);
		s = read_snmp();
	}
	return "{\"installed\":" + json_bool(installed) + ",\"enabled\":" + json_bool(enabled) + ",\"running\":" + json_bool(running) +
	       ",\"foreign\":" + json_bool(foreign) + ",\"settings\":{\"community\":" + q(s.community) + ",\"network\":" + q(s.network) +
	       ",\"location\":" + q(s.location) + ",\"contact\":" + q(s.contact) + "}}";
}

// --- UPS --------------------------------------------------------------------------------------------------------

constexpr const char *nut_dir = "/etc/nut";
constexpr const char *nut_drivers = "/usr/lib/nut";
constexpr const char *nut_local = "127.0.0.1";
constexpr const char *nut_usb_port = "auto";

std::vector<std::string> nut_paths()
{
	std::vector<std::string> out;
	for (const char *name : {"nut.conf", "ups.conf", "upsd.conf", "upsd.users"}) out.push_back(gopath::join(nut_dir, name));
	return out;
}

struct UpsSettings {
	std::string driver = "usbhid-ups", port = nut_usb_port, usb_device;
	bool network = false;
	std::string user = "monitor", password;
};

bool nut_driver(const std::string &s)
{
	if (s.size() < 2 || s.size() > 32) return false;
	auto alnum = [](char c) { return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'); };
	if (!alnum(s[0])) return false;
	for (std::size_t i = 1; i < s.size(); i++)
		if (!alnum(s[i]) && s[i] != '_' && s[i] != '-') return false;
	return true;
}

bool nut_usb_id(const std::string &s)
{
	if (s.size() != 9 || s[4] != ':') return false;
	for (std::size_t i = 0; i < 9; i++)
		if (i != 4 && !((s[i] >= '0' && s[i] <= '9') || (s[i] >= 'a' && s[i] <= 'f'))) return false;
	return true;
}

std::vector<std::string> ups_drivers()
{
	std::vector<std::string> drivers;
	if (DIR *d = ::opendir(nut_drivers)) {
		while (dirent *e = ::readdir(d)) {
			std::string name = e->d_name;
			if (name == "." || name == "..") continue;
			bool dir = e->d_type == DT_DIR;
			if (e->d_type == DT_UNKNOWN) {
				struct stat st{};
				dir = ::lstat((std::string(nut_drivers) + "/" + name).c_str(), &st) == 0 && S_ISDIR(st.st_mode);
			}
			// skel is the package's template of a driver, not one
			if (dir || !nut_driver(name) || name == "skel") continue;
			drivers.push_back(name);
		}
		::closedir(d);
	}
	std::sort(drivers.begin(), drivers.end());
	return drivers;
}

std::string ups_validate(const UpsSettings &s)
{
	if (!nut_driver(s.driver) || !stat_ok(gopath::join(nut_drivers, s.driver))) return "unknown UPS driver";
	if (s.port == nut_usb_port) {
		if (!s.usb_device.empty() && !nut_usb_id(s.usb_device)) return "the USB device is a vendor and product ID such as 051d:0002";
		return "";
	}
	if (!s.usb_device.empty() || (s.port != "/dev/ttyS1" && s.port != "/dev/ttyS2")) return "the port is USB or one of the serial ports";
	return "";
}

std::map<std::string, std::string> ups_files(const UpsSettings &s)
{
	// instead of the local address, which it covers: upsd refuses to start when one of its addresses is taken
	std::string listen = s.network ? "0.0.0.0" : nut_local;
	std::string device = "[ups]\n\tdriver = " + s.driver + "\n\tport = " + s.port + "\n";
	std::string vendor, product;
	if (cut(s.usb_device, ":", vendor, product)) device += "\tvendorid = " + vendor + "\n\tproductid = " + product + "\n";
	return {{"nut.conf", marker + "MODE=netserver\n"},
	        {"ups.conf", marker + device},
	        {"upsd.conf", marker + "LISTEN " + listen + " 3493\n"},
	        {"upsd.users", marker + "[" + s.user + "]\n\tpassword = " + s.password + "\n\tupsmon secondary\n"}};
}

std::string write_ups(const UpsSettings &s)
{
	std::map<std::string, std::string> files;
	for (const auto &[name, content] : ups_files(s)) files[gopath::join(nut_dir, name)] = content;
	// NUT refuses configuration other users can read
	return write_files(files, 0640, "nut");
}

bool read_ups(UpsSettings &out)
{
	UpsSettings s;
	out = UpsSettings{};
	std::map<std::string, std::string> text;
	for (const auto &path : nut_paths()) {
		auto data = read_file(path);
		if (!data || !has_prefix(*data, marker)) return false;
		text[gopath::base(path)] = *data;
	}
	auto value = [&](const std::string &file, const std::string &key) {
		for (const auto &line : network::split(text[file], "\n")) {
			std::string name, rest;
			if (cut(trim_space(line), "=", name, rest) && trim_space(name) == key) return trim_space(rest);
		}
		return std::string();
	};
	s.driver = value("ups.conf", "driver");
	s.port = value("ups.conf", "port");
	std::string vendor = value("ups.conf", "vendorid"), product = value("ups.conf", "productid");
	if (!vendor.empty() && !product.empty()) s.usb_device = vendor + ":" + product;
	// sharing is what upsd does, not what another file says about it
	bool listens = false;
	for (const auto &line : network::split(text["upsd.conf"], "\n")) {
		auto fields = network::fields(line);
		if (fields.size() >= 2 && fields[0] == "LISTEN") {
			listens = true;
			s.network = s.network || fields[1] != nut_local;
		}
	}
	s.password = value("upsd.users", "password");
	if (!listens || s.password.empty() || !ups_validate(s).empty()) return false;
	out = s;
	return true;
}

// upsAccess: the driver (user nut) gets the USB device of an attached UPS and the serial ports.
void ups_access()
{
	(void)rc(10, {"udevadm", "trigger", "--subsystem-match=usb", "--action=change"});
	(void)rc(10, {"addgroup", "nut", "dialout"});
}

std::string fresh_ups()
{
	UpsSettings s;
	if (!secret(20, s.password)) return "crypto/rand: no randomness";
	return write_ups(s);
}

Unit &ups()
{
	static Unit u{"UPS", "/usr/sbin/upsd", "nut-upsd", "/run/nut/upsd.pid", nut_paths(), ups_access, [] {
		              UpsSettings s;
		              return read_ups(s);
	              },
	              fresh_ups, {}};
	return u;
}

std::string ups_state(bool light)
{
	Unit &u = ups();
	bool installed = u.installed(), enabled = false, running = false, foreign = false, usb_host = false;
	UpsSettings s;
	s.driver = s.port = s.user = "";  // the zero settings of a service that is not installed
	std::vector<std::string> drivers;
	std::map<std::string, std::string> values;
	if (installed) {
		u.state(light, enabled, running, foreign);
		read_ups(s);
		drivers = ups_drivers();
		usb_host = lstat_ok("/etc/kvm/usb/usb.host");
		if (running)
			if (auto r = output(5, {"upsc", "ups@127.0.0.1"}); r.ok()) values = parse_ups_values(r.out);
	}
	std::string v = "{";
	bool first = true;
	for (const auto &[key, value] : values) {
		v += (first ? "" : ",") + q(key) + ":" + q(value);
		first = false;
	}
	return "{\"installed\":" + json_bool(installed) + ",\"enabled\":" + json_bool(enabled) + ",\"running\":" + json_bool(running) +
	       ",\"foreign\":" + json_bool(foreign) + ",\"settings\":{\"driver\":" + q(s.driver) + ",\"port\":" + q(s.port) +
	       ",\"usbDevice\":" + q(s.usb_device) + ",\"network\":" + json_bool(s.network) + ",\"user\":" + q(s.user) +
	       ",\"password\":" + q(s.password) + "},\"drivers\":" + json_strings(drivers) + ",\"usbHost\":" + json_bool(usb_host) +
	       ",\"values\":" + v + "}}";
}

// --- the services -----------------------------------------------------------------------------------------------

struct Entry {
	Unit &(*unit)();
	std::string (*state)(bool light);
};

const Entry *find(const std::string &service)
{
	static const std::map<std::string, Entry> entries{{"lldp", {lldp, lldp_state}}, {"snmp", {snmp, snmp_state}}, {"ups", {ups, ups_state}}};
	auto it = entries.find(service);
	return it == entries.end() ? nullptr : &it->second;
}

// handleSettings: the settings a handler has parsed and checked, written when the configuration is ours.
Reply handle_settings(const Entry &e, const std::function<std::string()> &write)
{
	Unit &u = e.unit();
	std::lock_guard lock(u.mutex);
	if (!u.installed()) return fail(-2, "the service is not installed");
	bool f = false;
	std::string err = u.foreign(f);
	if (err.empty() && f) err = "the service has a configuration that was not written here";
	if (!err.empty()) {
		log::error("{} settings: {}", u.name, err);
		return fail(-5, "the service has a configuration that was not written here");
	}
	if (auto e2 = u.apply(write); !e2.empty()) {
		log::error("{} settings: {}", u.name, e2);
		return fail(-3, "could not save the settings");
	}
	return ok(e.state(false));
}

} // namespace

std::vector<Neighbor> parse_lldp_neighbors(const std::string &data)
{
	// lldpcli's json0 report decoded as encoding/json fills the Go structs: a value of another type fails the
	// whole decode; null leaves a string or struct as it was and clears a slice; an array decodes into the
	// elements a slice already has (a duplicate key merges) and sets its length; the last of duplicate keys wins.
	struct Chassis {
		std::vector<std::string> name, descr, mgmt;
	};
	struct Port {
		std::vector<std::string> id, descr;
	};
	struct Entry {
		std::string name, via, age;
		std::vector<Chassis> chassis;
		std::vector<Port> port;
	};
	struct Block {
		std::vector<Entry> entries;
	};
	auto doc = godecode::parse_document(data);
	if (!doc) return {};
	bool good = true;
	auto object = [&](const godecode::Value &v, const std::function<void(const std::string &, const godecode::Value &)> &member) {
		if (v.kind == K::null) return;
		if (v.kind != K::object) {
			good = false;
			return;
		}
		for (const auto &[k, m] : v.members) member(k, m);
	};
	auto slice = [&](const godecode::Value &v, auto &out, const auto &element) {
		if (v.kind == K::null) {
			out.clear();
			return;
		}
		if (v.kind != K::array) {
			good = false;
			return;
		}
		std::size_t i = 0;
		for (const auto &item : v.items) {
			if (i >= out.size()) out.emplace_back();
			element(item, out[i]);
			i++;
		}
		if (i < out.size()) out.resize(i);
	};
	auto str = [&](const godecode::Value &v, std::string &out) {
		if (!godecode::get(v, out)) good = false;
	};
	auto values = [&](const godecode::Value &v, std::vector<std::string> &out) {
		slice(v, out, [&](const godecode::Value &item, std::string &value) {
			object(item, [&](const std::string &k, const godecode::Value &m) {
				if (godecode::key_matches(k, "value")) str(m, value);
			});
		});
	};
	std::vector<Block> report;
	object(*doc, [&](const std::string &k, const godecode::Value &lldp) {
		if (!godecode::key_matches(k, "lldp")) return;
		slice(lldp, report, [&](const godecode::Value &block, Block &b) {
			object(block, [&](const std::string &k2, const godecode::Value &ifaces) {
				if (!godecode::key_matches(k2, "interface")) return;
				slice(ifaces, b.entries, [&](const godecode::Value &entry, Entry &e) {
					object(entry, [&](const std::string &k3, const godecode::Value &m) {
						if (godecode::key_matches(k3, "name")) str(m, e.name);
						else if (godecode::key_matches(k3, "via")) str(m, e.via);
						else if (godecode::key_matches(k3, "age")) str(m, e.age);
						else if (godecode::key_matches(k3, "chassis"))
							slice(m, e.chassis, [&](const godecode::Value &c, Chassis &ch) {
								object(c, [&](const std::string &k4, const godecode::Value &cv) {
									if (godecode::key_matches(k4, "name")) values(cv, ch.name);
									else if (godecode::key_matches(k4, "descr")) values(cv, ch.descr);
									else if (godecode::key_matches(k4, "mgmt-ip")) values(cv, ch.mgmt);
								});
							});
						else if (godecode::key_matches(k3, "port"))
							slice(m, e.port, [&](const godecode::Value &p, Port &po) {
								object(p, [&](const std::string &k4, const godecode::Value &pv) {
									if (godecode::key_matches(k4, "id")) values(pv, po.id);
									else if (godecode::key_matches(k4, "descr")) values(pv, po.descr);
								});
							});
					});
				});
			});
		});
	});
	if (!good) return {};
	auto first = [](const std::vector<std::string> &list) { return list.empty() ? std::string() : list[0]; };
	std::vector<Neighbor> neighbors;
	for (const auto &b : report)
		for (const auto &e : b.entries) {
			Neighbor n;
			n.interface = e.name;
			n.protocol = e.via;
			n.age = e.age;
			if (!e.chassis.empty()) {
				n.name = first(e.chassis[0].name);
				n.description = first(e.chassis[0].descr);
				n.address = first(e.chassis[0].mgmt);
			}
			if (!e.port.empty()) {
				n.port = first(e.port[0].id);
				n.port_description = first(e.port[0].descr);
			}
			neighbors.push_back(n);
		}
	return neighbors;
}

std::map<std::string, std::string> parse_ups_values(const std::string &out)
{
	std::map<std::string, std::string> values;
	for (const auto &line : network::split(out, "\n")) {
		std::string key, value;
		if (cut(line, ": ", key, value) && key.find(' ') == std::string::npos) values[key] = trim_space(value);
	}
	return values;
}

bool snmp_source(const std::string &text, std::string &out)
{
	out.clear();
	if (text.empty()) return true;
	if (auto ip = network::parse_ip(text)) {
		out = text;
		return network::ip_is_v4(*ip);
	}
	// net.ParseCIDR: an address without a zone, a decimal prefix length within its size.
	auto slash = text.find('/');
	if (slash == std::string::npos) return false;
	auto addr = network::parse_addr(text.substr(0, slash));
	if (!addr || !addr->zone.empty()) return false;
	std::string mask = text.substr(slash + 1);
	int n = 0;
	std::size_t i = 0;
	for (; i < mask.size() && mask[i] >= '0' && mask[i] <= '9'; i++) {
		n = n * 10 + (mask[i] - '0');
		if (n >= 0xffffff) return false;
	}
	if (i == 0 || i != mask.size() || n > addr->bit_len()) return false;
	network::IP16 ip{};
	if (addr->is4()) {
		ip[10] = ip[11] = 0xff;
		std::copy(addr->b.begin(), addr->b.begin() + 4, ip.begin() + 12);
	} else {
		ip = addr->b;
	}
	// IPNet.IP = ip.Mask(CIDRMask(n, bits)): an IPv4 mask applies to the last four bytes.
	int offset = addr->is4() ? 12 : 0;
	for (int bit = 0; bit < 128 - offset; bit++)
		if (bit >= n) ip[static_cast<std::size_t>(offset + bit / 8)] &= static_cast<std::uint8_t>(~(0x80 >> (bit % 8)));
	if (!network::ip_is_v4(ip)) return false;
	// IPNet.String: the IPv4 network with the mask's last four bytes.
	out = network::ip_string(ip) + "/" + std::to_string(addr->is4() ? n : n - 96);
	return true;
}

Reply status(const std::string &service, bool light)
{
	const Entry *e = find(service);
	return ok(e->state(light));
}

Reply set_enabled(const std::string &service, const network::RequestData &request)
{
	const Entry *e = find(service);
	auto b = network::Binder::bind(request, false);
	bool on = false;
	bool present = b.boolean("enabled", "Enabled", on);
	if (!b.ok() || !present) return fail(-1, "invalid arguments");
	Unit &u = e->unit();
	std::lock_guard lock(u.mutex);
	if (!u.installed()) return fail(-2, "the service is not installed");
	if (auto err = u.set_enabled(on); !err.empty()) {
		log::error("switch {}: {}", u.name, err);
		return fail(-3, "could not switch the service");
	}
	return ok(e->state(false));
}

Reply settings(const std::string &service, const network::RequestData &request)
{
	const Entry *e = find(service);
	auto b = network::Binder::bind(request, false);
	if (service == "lldp") {
		bool cdp = false;
		b.boolean("cdp", "CDP", cdp);
		if (!b.ok()) return fail(-1, "invalid arguments");
		return handle_settings(*e, [cdp] { return write_lldp(cdp); });
	}
	if (service == "snmp") {
		SnmpSettings s;
		b.str("community", "Community", s.community);
		b.str("network", "Network", s.network);
		b.str("location", "Location", s.location);
		b.str("contact", "Contact", s.contact);
		if (!b.ok()) return fail(-1, "invalid arguments");
		s.location = trim_space(s.location);
		s.contact = trim_space(s.contact);
		s.network = trim_space(s.network);
		if (auto err = snmp_validate(s); !err.empty()) return fail(-4, err);
		std::string source;
		snmp_source(s.network, source);
		s.network = source;
		return handle_settings(*e, [s] { return write_file(snmp_conf, snmp_file(s), 0600, ""); });
	}
	std::string driver, port, usb_device;
	bool network = false, new_password = false;
	b.str("driver", "Driver", driver);
	b.str("port", "Port", port);
	b.str("usbDevice", "USBDevice", usb_device);
	bool has_network = b.boolean("network", "Network", network);
	b.boolean("newPassword", "NewPassword", new_password);
	if (!b.ok() || driver.empty() || port.empty() || !has_network) return fail(-1, "invalid arguments");
	for (auto &c : usb_device) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	UpsSettings asked;
	asked.driver = driver;
	asked.port = port;
	asked.usb_device = usb_device;
	if (auto err = ups_validate(asked); !err.empty()) return fail(-4, err);
	return handle_settings(*e, [=] {
		UpsSettings next;
		read_ups(next);
		next.driver = driver;
		next.port = port;
		next.network = network;
		next.usb_device = usb_device;
		if (next.password.empty() || new_password)
			if (!secret(20, next.password)) return std::string("crypto/rand: no randomness");
		ups_access();
		return write_ups(next);
	});
}

Reply reset(const std::string &service)
{
	const Entry *e = find(service);
	Unit &u = e->unit();
	std::lock_guard lock(u.mutex);
	if (!u.installed()) return fail(-2, "the service is not installed");
	if (auto err = u.apply(u.fresh); !err.empty()) {
		log::error("reset {}: {}", u.name, err);
		return fail(-3, "could not replace the configuration");
	}
	return ok(e->state(false));
}

} // namespace nkos::extensions::netsvc
