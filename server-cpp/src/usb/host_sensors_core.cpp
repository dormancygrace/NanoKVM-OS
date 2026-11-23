#include "usb/host_sensors_core.hpp"

#include "usb/gojson_read.hpp"
#include "usb/gotext.hpp"

#include <arpa/inet.h>

#include "common/log.hpp"

#include <algorithm>
#include <cerrno>
#include <cmath>
#include <format>
#include <fstream>
#include <sys/stat.h>

namespace nkos::usb::sensors {

namespace {

constexpr std::size_t max_text = 96;
constexpr long long max_interval = 3600;
constexpr Nanos min_stale_after = 15'000'000'000;
// 4 * maxText * utf8.UTFMax + 3
constexpr std::size_t max_view_key = 4 * max_text * 4 + 3;

const std::map<std::string, std::string> kinds = {
	{"temperature", "\xc2\xb0" "C"}, {"voltage", "V"}, {"current", "A"}, {"power", "W"}, {"fan", "RPM"},
	{"control", "%"}, {"load", "%"}, {"level", "%"}, {"clock", "MHz"}, {"frequency", "Hz"}, {"data", "GB"},
	{"smalldata", "MB"}, {"throughput", "B/s"}, {"energy", "mWh"}, {"humidity", "%"}, {"flow", "L/h"},
	{"noise", "dBA"}, {"factor", ""},
};

bool hardware_type(const std::string &t)
{
	return t == "cpu" || t == "gpu" || t == "motherboard" || t == "memory" || t == "storage" || t == "network" ||
	       t == "cooler" || t == "psu" || t == "battery" || t == "other";
}

// clean: a short printable text, trimmed (decoded JSON strings are valid UTF-8).
bool clean(const char *field, std::string &s, std::string &error)
{
	if (!gotext::valid_utf8(s)) {
		error = std::string(field) + ": not UTF-8";
		return false;
	}
	s = gotext::trim_space(s);
	if (gotext::rune_count(s) > max_text) {
		error = std::format("{}: longer than {} characters", field, max_text);
		return false;
	}
	for (std::size_t i = 0; i < s.size();) {
		std::size_t n;
		char32_t r = gotext::decode_rune(std::string_view(s).substr(i), n);
		if (!gotext::is_print(r)) {
			error = std::string(field) + ": a character that cannot be shown";
			return false;
		}
		i += n;
	}
	return true;
}

const char *b(bool v) { return v ? "true" : "false"; }

} // namespace

std::optional<std::string> unit(const std::string &kind)
{
	auto it = kinds.find(kind);
	if (it == kinds.end()) return std::nullopt;
	return it->second;
}

bool parse(std::string_view data, Report &r, std::string &error)
{
	r = {};
	if (data.size() > max_report_bytes) {
		error = "the report is too long";
		return false;
	}
	// The slice reuses its elements within its capacity, as Go's does for a repeated "sensors".
	std::vector<Sensor> elements;
	std::size_t length = 0;
	std::size_t end = 0;
	std::string decode_error;
	bool ok = goj::decode(data, "Report", [&](goj::Reader &rd) {
		rd.object("Report", "hostsensors.Report", {
			{"v", [&](goj::Reader &x) { x.integer(r.v); }},
			{"host", [&](goj::Reader &x) { x.string(r.host); }},
			{"os", [&](goj::Reader &x) { x.string(r.os); }},
			{"agent", [&](goj::Reader &x) { x.string(r.agent); }},
			{"interval", [&](goj::Reader &x) { x.integer(r.interval); }},
			{"limited", [&](goj::Reader &x) { x.boolean(r.limited); }},
			{"sensors", [&](goj::Reader &x) {
				x.slice("[]hostsensors.Sensor", [&] { elements.clear(), length = 0; },
				        [&](std::size_t i, goj::Reader &y) {
					        if (i >= elements.size()) elements.emplace_back();
					        Sensor &s = elements[i];
					        y.object("Sensor", "hostsensors.Sensor", {
						        {"hw", [&](goj::Reader &z) { z.string(s.hw); }},
						        {"type", [&](goj::Reader &z) { z.string(s.type); }},
						        {"name", [&](goj::Reader &z) { z.string(s.name); }},
						        {"kind", [&](goj::Reader &z) { z.string(s.kind); }},
						        {"value", [&](goj::Reader &z) { z.float64(s.value); }},
					        });
				        },
				        [&](std::size_t n) {
					        // An empty array is a new empty slice (no capacity left to reuse).
					        if (n == 0) elements.clear();
					        length = n;
				        });
			}},
		});
	}, end, decode_error);
	if (!ok) {
		error = "not a report: " + decode_error;
		return false;
	}
	if (goj::more(data, end)) {
		error = "text after the report";
		return false;
	}
	if (r.v != version) {
		error = std::format("report version {}, this server reads {}", r.v, version);
		return false;
	}
	if (length > max_sensors) {
		error = std::format("more than {} sensors", max_sensors);
		return false;
	}
	if (r.interval < 1 || r.interval > max_interval) {
		error = "interval out of range";
		return false;
	}
	if (!clean("host", r.host, error) || !clean("os", r.os, error) || !clean("agent", r.agent, error)) return false;
	for (std::size_t i = 0; i < length; i++) {
		Sensor s = elements[i];
		if (!kinds.contains(s.kind)) continue;
		if (std::isnan(s.value) || std::isinf(s.value)) {
			error = "a value that is not a number";
			return false;
		}
		if (!clean("hw", s.hw, error) || !clean("name", s.name, error)) return false;
		if (s.name.empty()) {
			error = "a sensor without a name";
			return false;
		}
		if (!hardware_type(s.type)) s.type = "other";
		r.sensors.push_back(std::move(s));
	}
	return true;
}

std::string report_json(const Report &r)
{
	using gotext::json_string;
	std::string j = std::format(R"({{"v":{},"host":{},"os":{},"agent":{},"interval":{})", r.v, json_string(r.host),
	                            json_string(r.os), json_string(r.agent), r.interval);
	if (r.limited) j += ",\"limited\":true";
	j += ",\"sensors\":[";
	for (std::size_t i = 0; i < r.sensors.size(); i++) {
		auto &s = r.sensors[i];
		j += std::format(R"({}{{"hw":{},"type":{},"name":{},"kind":{},"value":{}}})", i ? "," : "", json_string(s.hw),
		                 json_string(s.type), json_string(s.name), json_string(s.kind), gotext::json_float(s.value));
	}
	return j + "]}";
}

std::string Store::accept(std::string_view data, const std::string &transport, const From &from, Nanos now)
{
	Report r;
	std::string error;
	bool ok = parse(data, r, error);
	std::lock_guard lock(mutex_);
	if (!ok) {
		rejected_++;
		last_reject_ = error;
		return error;
	}
	report_ = std::move(r);
	transport_ = transport;
	at_ = now;
	from_ = from;
	return {};
}

void Store::clear()
{
	std::lock_guard lock(mutex_);
	report_.reset();
	transport_.clear();
	at_ = 0;
	from_ = {};
}

void Store::last(Nanos &at, From &from) const
{
	std::lock_guard lock(mutex_);
	at = at_;
	from = from_;
}

std::string Store::status_json(Nanos now) const
{
	std::lock_guard lock(mutex_);
	long long age = 0;
	bool stale = false;
	if (report_) {
		Nanos d = std::max<Nanos>(now - at_, 0);
		age = d / 1'000'000;
		Nanos limit = std::max<Nanos>(3 * report_->interval * 1'000'000'000LL, min_stale_after);
		stale = d > limit;
	}
	return std::format(R"({{"report":{},"transport":{},"age":{},"stale":{},"rejected":{},"lastReject":{}}})",
	                   report_ ? report_json(*report_) : "null", gotext::json_string(transport_), age, b(stale), rejected_,
	                   gotext::json_string(last_reject_));
}

void LineSplitter::feed(std::string_view data, const std::function<void(std::optional<std::string_view>)> &accept)
{
	while (!data.empty()) {
		auto end = data.find('\n');
		std::string_view part = end == std::string_view::npos ? data : data.substr(0, end);
		if (!overflow) {
			if (line.size() + part.size() > max_report_bytes) {
				overflow = true;
				line.clear();
			} else {
				line.append(part);
			}
		}
		if (end == std::string_view::npos) break;
		if (!overflow) {
			std::string text = gotext::trim_space(line);
			if (!text.empty()) accept(std::string_view(text));
		} else {
			accept(std::nullopt);  // counted as a rejected report
		}
		line.clear();
		overflow = false;
		data = data.substr(end + 1);
	}
}

bool login_console(std::string_view received)
{
	// bytes.ToLower: the runes that lower into ASCII letters are the dotted capital I and the Kelvin sign.
	std::string text;
	for (std::size_t i = 0; i < received.size();) {
		unsigned char c = static_cast<unsigned char>(received[i]);
		if (c < 0x80) {
			text += static_cast<char>(c >= 'A' && c <= 'Z' ? c + 32 : c);
			i++;
			continue;
		}
		std::size_t n;
		char32_t r = gotext::decode_rune(received.substr(i), n);
		text += r == 0x130 ? 'i' : r == 0x212a ? 'k' : '\x80';
		i += n;
	}
	return text.find("login:") != std::string::npos || text.find("password:") != std::string::npos;
}

bool from_network(const std::string &peer, const std::vector<Network> &networks)
{
	in_addr v4{};
	std::uint32_t ip;
	if (inet_pton(AF_INET, peer.c_str(), &v4) == 1) {
		ip = ntohl(v4.s_addr);
	} else {
		in6_addr v6{};
		if (inet_pton(AF_INET6, peer.c_str(), &v6) != 1 || !IN6_IS_ADDR_V4MAPPED(&v6)) return false;
		ip = static_cast<std::uint32_t>(v6.s6_addr[12]) << 24 | static_cast<std::uint32_t>(v6.s6_addr[13]) << 16 |
		     static_cast<std::uint32_t>(v6.s6_addr[14]) << 8 | v6.s6_addr[15];
	}
	for (auto &n : networks)
		if ((ip & n.mask) == (n.address & n.mask) && ip != n.address) return true;
	return false;
}

std::string key(const Sensor &s) { return s.type + "\t" + s.hw + "\t" + s.kind + "\t" + s.name; }

bool parse_view(std::string_view data, View &v, std::string &error)
{
	v = {};
	std::string decode_error;
	bool ok = goj::unmarshal(data, "View", [&](goj::Reader &rd) {
		rd.object("View", "hostsensors.View", {
			{"overrides", [&](goj::Reader &x) {
				x.map("map[string]bool", [] {}, [&](const std::string &k, goj::Reader &y) {
					bool value = false;
					y.boolean(value);
					v.overrides[k] = value;
				});
			}},
		});
	}, decode_error);
	if (!ok) {
		v = {};
		error = "not a view: " + decode_error;
		return false;
	}
	if (v.overrides.size() > max_view_entries) {
		error = std::format("more than {} readings", max_view_entries);
		return false;
	}
	for (auto &[k, value] : v.overrides) {
		(void)value;
		if (k.size() > max_view_key || !gotext::valid_utf8(k) || std::count(k.begin(), k.end(), '\t') != 3) {
			error = "a key that is not a reading's";
			return false;
		}
	}
	return true;
}

namespace {

bool read_file(const std::string &path, std::string &out, int &err)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) {
		err = errno;
		return false;
	}
	out.assign(std::istreambuf_iterator<char>(in), std::istreambuf_iterator<char>());
	return true;
}

} // namespace

View read_view(const std::string &path)
{
	std::string data;
	int err = 0;
	if (read_file(path, data, err)) {
		View v;
		std::string error;
		if (parse_view(data, v, error)) return v;
		log::error("host sensors view: {}", error);
	} else if (err != ENOENT) {
		log::error("host sensors view: open {}: {}", path, gotext::errno_text(err));
	}
	return {};
}

std::string read_mode(const std::string &path)
{
	std::string data;
	int err = 0;
	if (!read_file(path, data, err) && err != ENOENT) log::error("host sensors setting: open {}: {}", path, gotext::errno_text(err));
	std::string mode = gotext::trim_space(data);
	return mode == "serial" || mode == "network" ? mode : "off";
}

std::string addon_version(const std::string &source_json)
{
	std::string data;
	int err = 0;
	if (!read_file(source_json, data, err)) return {};
	std::string version, error;
	if (!goj::unmarshal(data, "", [&](goj::Reader &r) {
		    r.object("", "struct { PackageVersion string \"json:\\\"package_version\\\"\" }",
		             {{"package_version", [&](goj::Reader &x) { x.string(version); }}});
	    }, error))
		return {};
	return version.substr(0, version.find('-'));
}

std::string agent_file_json(const std::string &url_path, const std::string &file)
{
	struct stat st{};
	bool available = ::stat(file.c_str(), &st) == 0 && S_ISREG(st.st_mode);
	std::string j = "{\"path\":" + gotext::json_string(url_path) + ",\"available\":" + b(available);
	if (available && st.st_size != 0) j += std::format(",\"size\":{}", static_cast<long long>(st.st_size));
	return j + "}";
}

void helper_disk_lun(const std::string &lun_file, const std::string &helper_file, const std::string &console_disk,
                     bool &attached, bool &stale)
{
	attached = stale = false;
	std::string data;
	int err = 0;
	if (!read_file(lun_file, data, err)) return;
	std::string lun = gotext::trim_space(data);
	std::string path = lun;
	if (path.ends_with(" (deleted)")) path.resize(path.size() - 10);
	if (path != helper_file && path != console_disk) return;
	struct stat st{};
	std::string image = ::stat(helper_file.c_str(), &st) == 0 && S_ISREG(st.st_mode) ? helper_file : console_disk;
	attached = true;
	stale = path != lun || path != image;
}

std::string state_json(const StateInputs &in)
{
	using gotext::json_string;
	std::string addresses = "[";
	for (std::size_t i = 0; i < in.addresses.size(); i++) addresses += (i ? "," : "") + json_string(in.addresses[i]);
	addresses += "]";
	return std::format(R"({{"agent":{},"agentVersion":{},"disk":{{"attached":{},"stale":{},"wanted":{}}},"linuxAgent":{},)"
	                   R"("mode":{},"network":{{"addresses":{},"interface":"usb0","path":"/api/host-sensors"}},)"
	                   R"("serial":{{"port":{},"state":{}}},"status":{},"view":{},)"
	                   R"("watchdog":{{"action":{},"armed":{},"last":{},"minutes":{}}}}})",
	                   in.agent_json, json_string(in.agent_version), b(in.disk_attached), b(in.disk_stale),
	                   b(in.mode != "off"), in.linux_agent_json, json_string(in.mode), addresses,
	                   json_string(in.serial_port), json_string(in.serial_state), in.status_json, in.view_json,
	                   json_string(in.watchdog_action), b(in.watchdog_armed), json_string(in.watchdog_last),
	                   in.watchdog_minutes);
}

std::string view_json(const View &v)
{
	std::string j = "{\"overrides\":{";
	bool first = true;
	for (auto &[k, value] : v.overrides) {
		j += (first ? "" : ",") + gotext::json_string(k) + ":" + b(value);
		first = false;
	}
	return j + "}}";
}

} // namespace nkos::usb::sensors
