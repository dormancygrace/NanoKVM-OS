// The administrator's Wi-Fi access point and the Wi-Fi country, as the Go server's service/network wifi_ap.go and
// wifi_country.go: one file per setting in /etc/kvm (read by S30wifi), the stations from hostapd and the leases, the
// regulatory database and the channels the country allows.

#include "network/internal.hpp"

#include "common/log.hpp"

#include <openssl/sha.h>

#include <algorithm>

namespace nkos::network {

std::string wifi_ap_file(const std::string &name) { return paths.wifi_ap_prefix + "." + name; }

std::vector<std::string> wifi_ap_files()
{
	std::vector<std::string> out;
	for (const char *name : {"enabled", "ssid", "pass", "band", "channel", "hidden", "subnet", "internet", "dns"})
		out.push_back(wifi_ap_file(name));
	return out;
}

std::vector<std::string> wifi_ap_trial_files()
{
	auto out = wifi_ap_files();
	out.push_back(wifi_control.country_path());
	return out;
}

bool valid_wifi_ap_channel(const std::string &band, long long channel)
{
	if (band == "2.4") return channel >= 1 && channel <= 14;
	if (band == "5") return channel >= 32 && channel <= 177;
	return false;
}

std::string read_file_or_empty(const std::string &path) { return read_file(path).value_or(""); }

std::string read_wifi_ap_file(const std::string &name)
{
	auto data = read_file(wifi_ap_file(name));
	if (!data) return "";
	return trim_right(*data, "\n");
}

std::pair<WifiAP, bool> read_wifi_ap()
{
	WifiAP ap;
	ap.ssid = read_wifi_ap_file("ssid");
	ap.band = read_wifi_ap_file("band");
	ap.subnet = read_wifi_ap_file("subnet");
	ap.enabled = exists(wifi_ap_file("enabled"));
	ap.hidden = exists(wifi_ap_file("hidden"));
	ap.internet = exists(wifi_ap_file("internet"));
	ap.dns = fields(read_wifi_ap_file("dns"));
	ap.dns_nil = false;
	auto channel = network::atoi(read_wifi_ap_file("channel"));
	ap.channel = channel.value_or(0);
	if (ap.band != "5") ap.band = "2.4";
	if (ap.ssid.empty()) ap.ssid = "NanoKVM";
	return {ap, !read_wifi_ap_file("pass").empty()};
}

bool wifi_ap_active() { return exists(paths.wifi_ap_running); }

bool wifi_is_access_point() { return is_ap_mode() || wifi_ap_active(); }

bool valid_wifi_ap_password(const std::string &password)
{
	if (password.size() < 8 || password.size() > 63 || trim_space(password) != password) return false;
	for (char c : password)
		if (static_cast<unsigned char>(c) < 0x20 || static_cast<unsigned char>(c) > 0x7e) return false;
	return true;
}

Error valid_wifi_ap_subnet(const std::string &subnet)
{
	if (subnet.empty()) return {};
	auto prefix = parse_prefix(subnet + ".0/24");
	if (!prefix || std::count(subnet.begin(), subnet.end(), '.') != 2 || !prefix->addr.is4() || !prefix->addr.private_())
		return make_error("the network address must be three numbers of a private network, such as 192.168.50");
	bool ok = true;
	auto list = interfaces(&ok);
	if (ok) {
		for (const auto &iface : list) {
			if (iface.name == "wlan0") continue;
			for (const auto &address : iface.addrs) {
				Addr other;
				other.kind = 6;
				other.b = address.ip;
				if (prefix->contains(other.unmap()))
					return make_error("the network " + subnet + ".0/24 is already used by " + iface.name);
			}
		}
	}
	return {};
}

Error allowed_wifi_ap_channel(const WifiAP &ap, const std::optional<Channels> &allowed)
{
	if (!allowed || !ap.enabled) return {};
	auto it = allowed->find(ap.band);
	if (it == allowed->end() || it->second.empty()) {
		if (ap.band == "5") return {};
		return make_error("the Wi-Fi country allows no access point on " + ap.band + " GHz");
	}
	if (ap.channel == 0) return {};
	if (std::find(it->second.begin(), it->second.end(), ap.channel) != it->second.end()) return {};
	return make_error("the Wi-Fi country does not allow an access point on channel " + std::to_string(ap.channel));
}

std::pair<std::string, std::string> wifi_ap_band_fallback(const std::string &band, const std::string &running,
                                                          const std::string &country, const std::optional<Channels> &allowed)
{
	std::string in_use = band;
	auto no_five = [&] {
		if (!allowed) return false;
		auto it = allowed->find("5");
		return it == allowed->end() || it->second.empty();
	};
	if (running == "2.4" || running == "5") in_use = running;
	else if (band == "5" && (country == "00" || no_five())) in_use = "2.4";
	if (band != "5" || in_use != "2.4") return {in_use, ""};
	if (country == "00") return {in_use, "country_unset"};
	return {in_use, "country_disallows"};
}

Error validate_wifi_ap(const WifiAP &ap, bool keep_password)
{
	if (ap.ssid.empty() || ap.ssid.size() > 32 || !valid_utf8(ap.ssid) ||
	    ap.ssid.find_first_of(std::string("\0\r\n", 3)) != std::string::npos)
		return make_error("the network name must be 1 to 32 bytes");
	if (!keep_password && !valid_wifi_ap_password(ap.password))
		return make_error("the password must be 8 to 63 characters: letters, digits or symbols");
	if (ap.band != "2.4" && ap.band != "5") return make_error("the band must be 2.4 or 5 GHz");
	if (ap.channel != 0 && !valid_wifi_ap_channel(ap.band, ap.channel))
		return make_error("channel " + std::to_string(ap.channel) + " is not offered on " + ap.band + " GHz");
	if (ap.dns.size() > 3) return make_error("at most three DNS servers");
	for (const auto &server : ap.dns) {
		auto addr = parse_addr(server);
		if (!addr || !addr->is4() || addr->unspecified()) return make_error(go_quote(server) + " is not an IPv4 address");
	}
	// The derived network (10.x.y, from the chip ID) can clash with the Ethernet network too: check it as a chosen one.
	std::string subnet = ap.subnet;
	if (subnet.empty() && ap.enabled) {
		subnet = wifi_ap_address("");
		if (subnet.size() >= 2 && subnet.compare(subnet.size() - 2, 2, ".1") == 0) subnet.resize(subnet.size() - 2);
	}
	return valid_wifi_ap_subnet(subnet);
}

Error write_wifi_ap(const WifiAP &ap, bool keep_password)
{
	auto write = [](const std::string &name, const std::string &value) { return write_file_atomic(wifi_ap_file(name), value + "\n"); };
	auto flag = [](const std::string &name, bool on) -> Error {
		if (on) return write_private(wifi_ap_file(name), "");
		auto err = remove(wifi_ap_file(name));
		if (err && !err.not_exist()) return err;
		return {};
	};
	auto drop = [](const std::string &name) -> Error {
		auto err = remove(wifi_ap_file(name));
		if (err && !err.not_exist()) return err;
		return {};
	};
	if (auto err = mkdir_all(dir_name(paths.wifi_ap_prefix), 0755)) return err;
	if (auto err = write("ssid", ap.ssid)) return err;
	if (!keep_password)
		if (auto err = write("pass", ap.password)) return err;
	if (auto err = write("band", ap.band)) return err;
	if (auto err = write("channel", std::to_string(ap.channel))) return err;
	if (auto err = ap.subnet.empty() ? drop("subnet") : write("subnet", ap.subnet)) return err;
	if (auto err = flag("hidden", ap.hidden)) return err;
	if (auto err = flag("internet", ap.internet)) return err;
	if (auto err = ap.dns.empty() ? drop("dns") : write("dns", join(ap.dns, " "))) return err;
	return flag("enabled", ap.enabled);
}

std::string wifi_ap_address(const std::string &subnet)
{
	if (!subnet.empty()) return subnet + ".1";
	auto uid = read_file(paths.wifi_ap_uid_file);
	if (!uid) return "";
	unsigned char sum[SHA512_DIGEST_LENGTH];
	::SHA512(reinterpret_cast<const unsigned char *>(uid->data()), uid->size(), sum);
	int id2 = sum[0], id3 = sum[1];
	if (id2 == id3) id2++;
	if (id2 >= 255) id2 = 253;
	if (id3 >= 255) id3 = 254;
	return "10." + std::to_string(id3) + "." + std::to_string(id2) + ".1";
}

std::vector<WifiAPStation> parse_all_sta(const std::string &out)
{
	std::vector<WifiAPStation> stations;
	auto lower = [](std::string s) {
		for (auto &c : s)
			if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
		return s;
	};
	for (const auto &raw : split(out, "\n")) {
		std::string line = trim_space(raw);
		if (parse_mac_ok(line)) {
			WifiAPStation st;
			st.mac = lower(line);
			stations.push_back(st);
			continue;
		}
		auto eq = line.find('=');
		if (eq == std::string::npos || stations.empty()) continue;
		std::string key = line.substr(0, eq), value = line.substr(eq + 1);
		auto &st = stations.back();
		auto to_int = [](const std::string &v) {
			auto n = network::atoi(v);
			return n && *n >= INT32_MIN && *n <= INT32_MAX ? static_cast<int>(*n) : 0;
		};
		if (key == "signal") {
			st.signal = to_int(value);
		} else if (key == "connected_time") {
			st.connected = to_int(value);
		} else if (key == "tx_rate_info") {
			// In units of 100 kbit/s: "650 mcs 7".
			auto f = fields(value);
			if (!f.empty())
				if (auto rate = network::atoi(f[0]); rate && *rate >= INT32_MIN && *rate <= INT32_MAX)
					st.rate = static_cast<double>(*rate) / 10;
		}
	}
	return stations;
}

std::map<std::string, std::pair<std::string, std::string>> parse_dump_leases(const std::string &out)
{
	std::map<std::string, std::pair<std::string, std::string>> leases;
	for (const auto &line : split(out, "\n")) {
		auto f = fields(line);
		if (f.size() < 3) continue;
		if (!parse_mac_ok(f[0]) || !parse_ip(f[1])) continue;
		std::string mac = f[0];
		for (auto &c : mac)
			if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
		leases[mac] = {f[1], f.size() >= 4 ? f[2] : ""};
	}
	return leases;
}

std::vector<WifiAPStation> wifi_ap_stations()
{
	// hostapd, not iw: it counts the time since association, which the driver does not report.
	auto r = network::run({"hostapd_cli", "-p", "/var/run/hostapd", "-i", "wlan0", "all_sta"});
	if (!r.ok()) return {};
	auto stations = parse_all_sta(r.out);
	if (stations.empty()) return stations;
	std::map<std::string, std::pair<std::string, std::string>> leases;
	if (auto d = network::run({"dumpleases", "-f", paths.wifi_ap_leases}); d.ok()) leases = parse_dump_leases(d.out);
	if (auto n = network::run({"ip", "-4", "neigh", "show", "dev", "wlan0"}); n.ok()) {
		for (const auto &line : split(n.out, "\n")) {
			auto f = fields(line);
			// 10.1.2.100 lladdr aa:bb:cc:dd:ee:ff REACHABLE
			if (f.size() >= 3 && f[1] == "lladdr") {
				std::string mac = f[2];
				for (auto &c : mac)
					if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
				auto it = leases.find(mac);
				if (it == leases.end() || it->second.first.empty())
					leases[mac] = {f[0], it == leases.end() ? "" : it->second.second};
			}
		}
	}
	for (auto &st : stations) {
		auto it = leases.find(st.mac);
		if (it != leases.end()) st.ip = it->second.first, st.hostname = it->second.second;
	}
	return stations;
}

std::vector<std::string> wifi_ap_device_dns()
{
	std::vector<std::string> servers;
	auto data = read_file(paths.wifi_ap_resolv_conf);
	if (!data) return servers;
	for (const auto &line : split(*data, "\n")) {
		auto f = fields(line);
		if (f.size() != 2 || f[0] != "nameserver" || servers.size() == 3) continue;
		if (auto addr = parse_addr(f[1]); addr && addr->is4() && !addr->loopback()) servers.push_back(f[1]);
	}
	return servers;
}

std::string channels_json(const Channels &c)
{
	std::string out = "{";
	bool first = true;
	for (const auto &[band, list] : c) {
		out += (first ? "" : ",") + quote(band) + ":[";
		for (std::size_t i = 0; i < list.size(); i++) out += (i ? "," : "") + std::to_string(list[i]);
		out += "]";
		first = false;
	}
	return out + "}";
}

std::string wifi_ap_state_json(const WifiAPState &s)
{
	auto b = [](bool v) { return v ? "true" : "false"; };
	const auto &ap = s.ap;
	std::string out = std::string("{\"enabled\":") + b(ap.enabled) + ",\"ssid\":" + quote(ap.ssid);
	if (!ap.password.empty()) out += ",\"password\":" + quote(ap.password);
	out += ",\"band\":" + quote(ap.band) + ",\"channel\":" + std::to_string(ap.channel) + ",\"hidden\":" + b(ap.hidden) +
	       ",\"subnet\":" + quote(ap.subnet) + ",\"internet\":" + b(ap.internet) + ",\"dns\":" + json_strings(ap.dns, ap.dns_nil) +
	       ",\"passwordSet\":" + b(s.password_set) + ",\"running\":" + b(s.running) + ",\"address\":" + quote(s.address) +
	       ",\"stations\":[";
	for (std::size_t i = 0; i < s.stations.size(); i++) {
		const auto &st = s.stations[i];
		out += (i ? "," : "") + std::string("{\"mac\":") + quote(st.mac);
		if (!st.ip.empty()) out += ",\"ip\":" + quote(st.ip);
		if (!st.hostname.empty()) out += ",\"hostname\":" + quote(st.hostname);
		if (st.signal) out += ",\"signal\":" + std::to_string(st.signal);
		out += ",\"connected\":" + std::to_string(st.connected);
		if (st.rate != 0) out += ",\"rate\":" + json_float(st.rate);
		out += "}";
	}
	out += "],\"deviceDns\":" + json_strings(s.device_dns, false);
	if (s.channels && !s.channels->empty()) out += ",\"channels\":" + channels_json(*s.channels);
	out += ",\"bandInUse\":" + quote(s.band_in_use);
	if (!s.band_fallback.empty()) out += ",\"bandFallback\":" + quote(s.band_fallback);
	return out + "}";
}

Error restart_wifi_ap()
{
	if (!wifi_control.enabled()) return {};
	auto r = wifi_control.run(wifi_script, {"restart"});
	return r.ok() ? Error{} : make_error(r.error);
}

// --- the Wi-Fi country (wifi_country.go) -----------------------------------------------------------------------------

bool valid_wifi_country(const std::string &code)
{
	if (code == "00") return true;
	return code.size() == 2 && code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z';
}

std::string RadioControl::country_path() const { return path_join(etc, "wifi.country"); }

std::string RadioControl::country() const
{
	auto data = read_file(country_path());
	if (!data) return "00";
	std::string line = data->substr(0, data->find('\n'));
	if (!line.empty() && line.back() == '\r') line.pop_back();
	return valid_wifi_country(line) ? line : "00";
}

Error RadioControl::save_country(const std::string &code) const
{
	if (!valid_wifi_country(code)) return make_error("invalid country");
	return write_file_atomic(country_path(), code + "\n");
}

std::optional<std::vector<std::string>> parse_regulatory_db(std::string_view data, std::string &err)
{
	auto u16 = [&](std::size_t at) { return static_cast<unsigned>(static_cast<unsigned char>(data[at]) << 8 | static_cast<unsigned char>(data[at + 1])); };
	if (data.size() < 8 || data.substr(0, 4) != "RGDB") {
		err = "not a regulatory database";
		return std::nullopt;
	}
	unsigned long version = static_cast<unsigned long>(u16(4)) << 16 | u16(6);
	if (version != 20) {
		err = "regulatory database version " + std::to_string(version);
		return std::nullopt;
	}
	std::vector<std::string> codes;
	for (std::size_t offset = 8;; offset += 4) {
		if (offset + 4 > data.size()) {
			err = "regulatory database without the end of its country list";
			return std::nullopt;
		}
		if (u16(offset + 2) == 0) return codes;
		std::string code(data.substr(offset, 2));
		if (valid_wifi_country(code)) codes.push_back(code);
	}
}

std::optional<std::vector<std::string>> supported_countries()
{
	Error read_err;
	auto data = read_file(paths.regulatory_db, &read_err);
	if (!data) {
		log::warn("cannot read the regulatory database: {}", read_err.text);
		return std::nullopt;
	}
	std::string err;
	auto codes = parse_regulatory_db(*data, err);
	if (!codes) {
		log::warn("cannot read the regulatory database {}: {}", paths.regulatory_db, err);
		return std::nullopt;
	}
	// No country at all is Go's nil list: unknown.
	if (codes->empty()) return std::nullopt;
	return codes;
}

bool country_supported(const std::string &code, const std::optional<std::vector<std::string>> &supported)
{
	return code == "00" || !supported || std::find(supported->begin(), supported->end(), code) != supported->end();
}

std::string parse_reg_country(const std::string &output)
{
	auto reg_code = [](const std::string &c) {
		return c.size() == 2 && std::all_of(c.begin(), c.end(), [](char x) { return (x >= '0' && x <= '9') || (x >= 'A' && x <= 'Z'); });
	};
	for (const auto &line : split(output, "\n")) {
		if (line.rfind("phy#", 0) == 0) break;
		if (line.rfind("country ", 0) != 0) continue;
		std::string rest = line.substr(8);
		auto colon = rest.find(':');
		if (colon == std::string::npos) continue;
		std::string code = rest.substr(0, colon);
		if (reg_code(code)) return code;
	}
	return "";
}

std::string RadioControl::active_country() const
{
	auto r = run("iw", {"reg", "get"});
	if (!r.ok()) return "";
	return parse_reg_country(r.out);
}

namespace {

// `\*\s+(\d+)(?:\.\d+)?\s+MHz`, the leftmost match: the digits.
std::optional<std::string> match_ap_frequency(std::string_view line)
{
	auto space = [](char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'; };
	auto digit = [](char c) { return c >= '0' && c <= '9'; };
	for (std::size_t p = 0; p < line.size(); p++) {
		if (line[p] != '*') continue;
		std::size_t j = p + 1, s = j;
		while (j < line.size() && space(line[j])) j++;
		if (j == s) continue;
		std::size_t d = j;
		while (j < line.size() && digit(line[j])) j++;
		if (j == d) continue;
		std::string digits(line.substr(d, j - d));
		auto tail = [&](std::size_t k) {
			std::size_t w = k;
			while (w < line.size() && space(line[w])) w++;
			return w > k && line.substr(w, 3) == "MHz";
		};
		if (j < line.size() && line[j] == '.') {
			std::size_t k = j + 1;
			while (k < line.size() && digit(line[k])) k++;
			if (k > j + 1 && tail(k)) return digits;
		}
		if (tail(j)) return digits;
	}
	return std::nullopt;
}

} // namespace

std::optional<Channels> parse_ap_channels(const std::string &output)
{
	std::optional<Channels> result;
	for (const auto &line : split(output, "\n")) {
		auto m = match_ap_frequency(line);
		if (!m) continue;
		if (!result) result = Channels{};
		auto f = network::atoi(*m);
		int freq = f && *f >= INT32_MIN && *f <= INT32_MAX ? static_cast<int>(*f) : 0;
		std::string band = band_for(freq);
		int channel = channel_for(freq);
		// Channel 14 is 802.11b only, which hostapd's hw_mode=g cannot use.
		if (band.empty() || channel == 0 || channel == 14 || (band == "5" && freq < 5150)) continue;
		bool excluded = false;
		for (const char *flag : {"disabled", "no IR", "radar", "passive scanning", "no IBSS"})
			excluded = excluded || line.find(flag) != std::string::npos;
		if (!excluded) (*result)[band].push_back(channel);
	}
	return result;
}

std::optional<Channels> RadioControl::ap_channels() const
{
	std::string p = phy();
	if (p.empty()) return std::nullopt;
	auto read = [&]() -> std::optional<Channels> {
		auto r = run("iw", {"phy", p, "info"});
		if (!r.ok()) return std::nullopt;
		return parse_ap_channels(r.out);
	};
	if (!ap_bands) return read();
	std::lock_guard lock(ap_bands->mu);
	auto now = std::chrono::steady_clock::now();
	if (ap_bands->channels && ap_bands->phy == p && now >= ap_bands->at && now - ap_bands->at < ap_bands->ttl)
		return ap_bands->channels;
	ap_bands->channels = read();
	ap_bands->phy = p;
	ap_bands->at = now;
	return ap_bands->channels;
}

void RadioControl::country_changed() const
{
	if (bands) bands->reset();
	if (ap_bands) ap_bands->reset();
}

Error apply_wifi_country(const std::string &code)
{
	if (auto r = wifi_control.run("iw", {"reg", "set", code}); !r.ok()) log::warn("iw reg set {} failed: {}", code, r.error);
	auto r = wifi_control.run(wifi_script, {"restart"});
	wifi_control.country_changed();
	return r.ok() ? Error{} : make_error(r.error);
}

Error restart_wifi_country()
{
	if (!wifi_control.enabled()) {
		wifi_control.country_changed();
		return {};
	}
	return apply_wifi_country(wifi_control.country());
}

std::string wifi_country_json(const std::string &country, const std::string &active,
                              const std::optional<std::vector<std::string>> &supported, const std::string &trial_json)
{
	std::string out = "{\"country\":" + quote(country) + ",\"active\":" + quote(active);
	if (supported && !supported->empty()) out += ",\"supported\":" + json_strings(*supported, false);
	if (!trial_json.empty()) out += ",\"trial\":" + trial_json;
	return out + "}";
}

} // namespace nkos::network
