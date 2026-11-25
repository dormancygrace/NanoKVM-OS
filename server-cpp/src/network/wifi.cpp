// Wi-Fi as the Go server's service/network wifi.go, wifi_control.go, wifi_saved.go and wpa_ctrl.go: the radio's
// state, scans, the joined and saved networks (the files S30wifi reads) and wpa_supplicant's control socket.

#include "network/internal.hpp"

#include "common/log.hpp"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <cstddef>
#include <cstring>
#include <glob.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/un.h>
#include <unistd.h>

namespace nkos::network {

Paths paths;

namespace {

std::mutex wifi_mu;
bool wifi_busy = false;
std::string wifi_error;

BandCache band_cache;
ApChannelCache ap_channel_cache;

RunResult run_radio_command(const std::string &name, const std::vector<std::string> &args)
{
	RunOptions options;
	options.timeout = std::chrono::seconds(20);
	options.wait_delay = std::chrono::seconds(2);
	return network::run(unprotected(name, args), options);
}

std::string upper_ascii(std::string s)
{
	for (auto &c : s)
		if (c >= 'a' && c <= 'z') c = static_cast<char>(c - 'a' + 'A');
	return s;
}

bool contains(std::string_view s, std::string_view part) { return s.find(part) != std::string_view::npos; }

// strings.Cut(s, sep)
bool cut(std::string_view s, char sep, std::string &before, std::string &after)
{
	auto at = s.find(sep);
	if (at == std::string_view::npos) return false;
	before = std::string(s.substr(0, at));
	after = std::string(s.substr(at + 1));
	return true;
}

int atoi_or_zero(std::string_view s)
{
	auto v = network::atoi(s);
	if (!v || *v < INT32_MIN || *v > INT32_MAX) return 0;
	return static_cast<int>(*v);
}

// `\*\s+(\d+)(?:\.0+)? MHz`, the leftmost match: the digits.
std::optional<std::string> match_frequency(std::string_view line)
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
		std::size_t k = j;
		if (k < line.size() && line[k] == '.') {
			std::size_t z = k + 1;
			while (z < line.size() && line[z] == '0') z++;
			if (z > k + 1 && line.substr(z, 4) == " MHz") return digits;
		}
		if (line.substr(j, 4) == " MHz") return digits;
	}
	return std::nullopt;
}

} // namespace

RadioControl wifi_control{"/etc/kvm", "/sys/class/net/wlan0", "/sys/bus/sdio/devices", "/run/nanokvm-wifi", run_radio_command,
                          wpa_control("/var/run/wpa_supplicant/wlan0"), &band_cache, &ap_channel_cache};

Error write_private(const std::string &path, std::string_view data) { return write_private_file(path, data); }

// --- caches ----------------------------------------------------------------------------------------------------------

Freqs BandCache::get(const std::string &p, std::chrono::steady_clock::time_point now, const std::function<Freqs()> &read)
{
	std::lock_guard lock(mu);
	if (freqs && phy == p && now >= at && now - at < ttl) return *freqs;
	Freqs value = read();
	freqs.reset();
	// A failed or empty answer is not kept; the next poll asks again.
	if (!value.empty()) {
		phy = p;
		at = now;
		freqs = value;
	}
	return value;
}

void BandCache::reset()
{
	std::lock_guard lock(mu);
	freqs.reset();
}

void ApChannelCache::reset()
{
	std::lock_guard lock(mu);
	cached = false;
	channels.reset();
}

// --- the radio -------------------------------------------------------------------------------------------------------

bool RadioControl::present() const { return exists(net); }

bool wifi_hardware_present() { return wifi_control.hardware(); }

std::string RadioControl::chip_alias() const
{
	if (sdio.empty()) return "";
	glob_t g{};
	std::string pattern = path_join(path_join(sdio, "*"), "modalias");
	std::string out;
	if (::glob(pattern.c_str(), 0, nullptr, &g) == 0) {
		for (std::size_t i = 0; i < g.gl_pathc; i++) {
			if (auto alias = read_file(g.gl_pathv[i])) {
				out = trim_space(*alias);
				break;
			}
		}
	}
	::globfree(&g);
	return out;
}

bool RadioControl::hardware() const { return present() || !chip_alias().empty(); }

bool RadioControl::enabled() const { return stat_path(path_join(etc, "wifi.disabled")).not_exist(); }

std::string RadioControl::model() const
{
	auto alias = read_file(path_join(net, "device/modalias"));
	std::string id = upper_ascii(alias ? *alias : chip_alias());
	static const std::pair<const char *, const char *> models[] = {
		{"V5449D0145", "AIC8801"}, {"V544AD0146", "AIC8801"}, {"VC8A1D0082", "AIC8800D80"},
		{"VC8A1DC08D", "AIC8800D80"}, {"V024CDB73A", "RTL8733BS"}, {"V024CDB733", "RTL8733BS"}};
	for (auto [key, name] : models)
		if (contains(id, key)) return name;
	if (auto driver = eval_symlinks(path_join(net, "device/driver"))) return base_name(*driver);
	return "";
}

std::string band_for(int freq)
{
	if (freq >= 2400 && freq < 2500) return "2.4";
	if (freq >= 4900 && freq < 5925) return "5";
	return "";
}

int channel_for(int freq)
{
	if (freq == 2484) return 14;
	if (freq >= 2412 && freq <= 2472) return (freq - 2407) / 5;
	if (freq >= 5160 && freq <= 5885) return (freq - 5000) / 5;
	if (freq >= 5955 && freq <= 7115) return (freq - 5950) / 5;
	return 0;
}

Freqs parse_frequencies(const std::string &output)
{
	Freqs result;
	for (const auto &line : split(output, "\n")) {
		auto m = match_frequency(line);
		if (!m || contains(line, "disabled")) continue;
		std::string band = band_for(atoi_or_zero(*m));
		if (!band.empty()) result[band].push_back(*m);
	}
	return result;
}

std::string RadioControl::phy() const
{
	auto phy = eval_symlinks(path_join(net, "phy80211"));
	return phy ? base_name(*phy) : "";
}

Freqs RadioControl::phy_frequencies(const std::string &p) const
{
	if (p.empty()) return {};
	auto r = run("iw", {"phy", p, "info"});
	if (!r.ok()) return {};
	return parse_frequencies(r.out);
}

Freqs RadioControl::frequencies() const { return phy_frequencies(phy()); }

Freqs RadioControl::status_frequencies() const
{
	std::string p = phy();
	if (!bands || p.empty()) return phy_frequencies(p);
	return bands->get(p, std::chrono::steady_clock::now(), [&] { return phy_frequencies(p); });
}

std::string RadioControl::preferred_band() const
{
	auto value = read_file(path_join(etc, "wifi.band_preference"));
	if (value && (*value == "2.4" || *value == "5")) return *value;
	return "5";
}

Error RadioControl::save_preferred_band(const std::string &band) const
{
	if (band != "2.4" && band != "5") return make_error("invalid preferred band");
	return write_private(path_join(etc, "wifi.band_preference"), band);
}

std::vector<std::string> RadioControl::band_frequencies(const std::string &band) const
{
	std::string filename;
	if (band == "2.4") filename = "wifi.freq_list_2.4";
	else if (band == "5") filename = "wifi.freq_list_5";
	else return {};
	if (auto value = read_file(path_join(etc, filename))) {
		auto list = fields(*value);
		for (const auto &field : list) {
			auto frequency = network::atoi(field);
			if (!frequency || *frequency < INT32_MIN || *frequency > INT32_MAX || band_for(static_cast<int>(*frequency)) != band)
				return {};
		}
		return list;
	}
	auto all = frequencies();
	return all[band];
}

Error RadioControl::apply_preferred_band(const std::string &band) const
{
	if (!request) return {};
	auto frequencies = band_frequencies(band);
	if (frequencies.empty()) return {};
	auto networks = request("LIST_NETWORKS");
	if (!networks) return make_error("wpa_supplicant did not answer");
	std::string preferred;
	bool preferred_current = false;
	auto lines = split(*networks, "\n");
	for (std::size_t i = 1; i < lines.size(); i++) {
		auto f = split(lines[i], "\t");
		const std::string &id = f[0];
		if (!network::atoi(id)) continue;
		auto priority = request("GET_NETWORK " + id + " priority");
		if (priority && trim_space(*priority) == "2") {
			preferred = id;
			preferred_current = f.size() >= 4 && contains(f.back(), "[CURRENT]");
			break;
		}
	}
	if (preferred.empty()) return {};
	auto reply = request("SET_NETWORK " + preferred + " freq_list " + join(frequencies, " "));
	if (!reply) return make_error("wpa_supplicant did not answer");
	if (trim_space(*reply) != "OK") return make_error("wpa_supplicant refused the channel list: " + trim_space(*reply));
	std::string selection = preferred_current ? "REASSOCIATE" : "SCAN";
	reply = request(selection);
	if (!reply) return make_error("wpa_supplicant did not answer");
	std::string text = trim_space(*reply);
	if (text != "OK" && !(selection == "SCAN" && text == "FAIL-BUSY"))
		return make_error("wpa_supplicant refused " + selection + ": " + text);
	return {};
}

std::string decode_ssid(const std::string &value)
{
	std::string out;
	for (std::size_t i = 0; i < value.size(); i++) {
		if (i + 3 < value.size() && value[i] == '\\' && value[i + 1] == 'x') {
			if (auto b = parse_uint(std::string_view(value).substr(i + 2, 2), 16, 8)) {
				out += static_cast<char>(*b);
				i += 3;
				continue;
			}
		}
		out += value[i];
	}
	return out;
}

std::vector<WifiNetwork> parse_scan(const std::string &output, const std::string &band)
{
	std::vector<WifiNetwork> result;
	std::optional<WifiNetwork> n;
	bool privacy = false, secured = false, wpa = false, psk = false, sae = false, unsupported = false;
	std::string section;
	auto flush = [&] {
		if (!n || n->band.empty() || (band != "all" && n->band != band) || n->ssid.empty() || !valid_utf8(n->ssid)) return;
		n->security = "open";
		if (privacy || secured) n->security = "unsupported";
		if (!unsupported) {
			if (psk && sae) n->security = "wpa2-wpa3";
			else if (sae) n->security = "wpa3";
			else if (psk && wpa) n->security = "wpa-wpa2";
			else if (psk) n->security = "wpa2";
			else if (wpa) n->security = "wpa";
		}
		result.push_back(*n);
	};
	for (const auto &line : split(output, "\n")) {
		std::string l = trim_space(line);
		if (l.rfind("BSS ", 0) == 0) {
			flush();
			auto f = fields(l);
			if (f.size() < 2) {
				n.reset();
				continue;
			}
			n = WifiNetwork{};
			n->bssid = split(f[1], "(")[0];
			n->signal = -100;
			privacy = secured = wpa = psk = sae = unsupported = false;
			section.clear();
		} else if (n) {
			std::string left = trim_left(line, "\t ");
			if (l.rfind("freq:", 0) == 0) {
				auto freq = parse_float(trim_space(l.substr(5)));
				n->band = band_for(freq ? static_cast<int>(*freq) : 0);
			} else if (l.rfind("signal:", 0) == 0) {
				auto f = fields(l.substr(7));
				if (!f.empty()) n->signal = parse_float(f[0]).value_or(0);
			} else if (left.rfind("SSID: ", 0) == 0) {
				n->ssid = decode_ssid(left.substr(6));
			} else if (l.rfind("capability:", 0) == 0) {
				privacy = contains(l, "Privacy");
			} else if (l.rfind("RSN:", 0) == 0) {
				secured = true;
				section = "rsn";
			} else if (l.rfind("WPA:", 0) == 0) {
				secured = true;
				section = "wpa";
			} else if (contains(l, "Authentication suites:")) {
				if (section == "rsn") {
					psk = psk || contains(l, "PSK");
					sae = sae || contains(l, "SAE");
				}
				if (section == "wpa") wpa = wpa || contains(l, "PSK");
				unsupported = unsupported || contains(l, "IEEE 802.1X") || contains(l, "OWE");
			}
		}
	}
	flush();
	std::stable_sort(result.begin(), result.end(), [](const WifiNetwork &a, const WifiNetwork &b) { return a.signal > b.signal; });
	std::set<std::string> seen;
	std::vector<WifiNetwork> unique;
	for (const auto &net : result) {
		std::string key = net.ssid + std::string(1, '\0') + net.security + std::string(1, '\0') + net.band;
		if (seen.insert(key).second) unique.push_back(net);
	}
	return unique;
}

std::string wifi_networks_json(const std::vector<WifiNetwork> &list)
{
	std::string out = "[";
	for (std::size_t i = 0; i < list.size(); i++) {
		const auto &n = list[i];
		out += (i ? "," : "") + std::string("{\"bssid\":") + quote(n.bssid) + ",\"ssid\":" + quote(n.ssid) +
		       ",\"band\":" + quote(n.band) + ",\"signal\":" + json_float(n.signal) + ",\"security\":" + quote(n.security) + "}";
	}
	return out + "]";
}

std::string wifi_status_json(const WifiStatus &s)
{
	auto b = [](bool v) { return v ? "true" : "false"; };
	std::string out = std::string("{\"enabled\":") + b(s.enabled) + ",\"model\":" + quote(s.model) +
	                  ",\"bands\":" + json_strings(s.bands, false) + ",\"band\":" + quote(s.band) + ",\"busy\":" + b(s.busy) +
	                  ",\"error\":" + quote(s.error) + ",\"supported\":" + b(s.supported) + ",\"apMode\":" + b(s.ap_mode) +
	                  ",\"accessPoint\":" + b(s.access_point) + ",\"apStations\":" + std::to_string(s.ap_stations) +
	                  ",\"connected\":" + b(s.connected) + ",\"ssid\":" + quote(s.ssid) + ",\"preferredBand\":" + quote(s.preferred_band);
	if (s.connection) {
		const auto &c = *s.connection;
		out += ",\"connection\":{\"frequency\":" + std::to_string(c.frequency);
		if (c.channel) out += ",\"channel\":" + std::to_string(c.channel);
		if (!c.width.empty()) out += ",\"width\":" + quote(c.width);
		if (c.signal) out += ",\"signal\":" + std::to_string(c.signal);
		if (c.link_speed) out += ",\"linkSpeed\":" + std::to_string(c.link_speed);
		if (c.generation) out += ",\"generation\":" + std::to_string(c.generation);
		if (!c.key_mgmt.empty()) out += ",\"keyMgmt\":" + quote(c.key_mgmt);
		if (!c.ip.empty()) out += ",\"ip\":" + quote(c.ip);
		out += "}";
	}
	out += std::string(",\"thisConnection\":") + b(s.this_connection) + "}";
	return out;
}

WifiStatus RadioControl::status() const
{
	// Without a radio Wi-Fi is not on, whatever the saved preference says.
	WifiStatus d;
	bool hw = hardware();
	d.supported = hw;
	d.enabled = hw && enabled();
	d.ap_mode = is_ap_mode();
	d.access_point = wifi_ap_active();
	d.preferred_band = preferred_band();
	{
		std::lock_guard lock(wifi_mu);
		d.busy = wifi_busy;
		d.error = wifi_error;
	}
	if (!d.supported) return d;
	d.model = model();
	// Off, the driver is not loaded: no bands or connection to read.
	if (!present()) return d;
	auto freqs = status_frequencies();
	for (const char *b : {"2.4", "5"})
		if (!freqs[b].empty()) d.bands.push_back(b);
	if (d.enabled && d.access_point) {
		auto [ap, _] = read_wifi_ap();
		d.ssid = ap.ssid;
		d.ap_stations = static_cast<int>(wifi_ap_stations().size());
	}
	if (!d.enabled || d.ap_mode || d.access_point) return d;
	if (!request) return d;
	auto out = request("STATUS");
	if (!out) return d;
	WifiConnection c;
	for (const auto &line : split(*out, "\n")) {
		std::string key, value;
		if (!cut(line, '=', key, value)) continue;
		if (key == "wpa_state") {
			d.connected = value == "COMPLETED";
		} else if (key == "ssid") {
			// wpa_supplicant escapes SSIDs its own way, unlike iw's scan.
			auto ssid = wpa_unescape(value);
			d.ssid = ssid ? *ssid : value;
		} else if (key == "freq") {
			c.frequency = atoi_or_zero(value);
			d.band = band_for(c.frequency);
		} else if (key == "wifi_generation") {
			c.generation = atoi_or_zero(value);
		} else if (key == "key_mgmt") {
			c.key_mgmt = value;
		} else if (key == "ip_address") {
			c.ip = value;
		}
	}
	if (!d.connected) {
		d.ssid.clear();
		d.band.clear();
		return d;
	}
	// Signal and rate are best effort: the connection is known without them.
	if (auto poll = request("SIGNAL_POLL")) {
		for (const auto &line : split(*poll, "\n")) {
			std::string key, value;
			if (!cut(trim_space(line), '=', key, value)) continue;
			if (key == "RSSI") c.signal = atoi_or_zero(value);
			else if (key == "LINKSPEED") c.link_speed = atoi_or_zero(value);
			else if (key == "WIDTH") c.width = value;
		}
	}
	c.channel = channel_for(c.frequency);
	d.connection = c;
	return d;
}

Error WifiProfile::validate() const
{
	auto valid_text = [](const std::string &s) { return valid_utf8(s) && s.find_first_of(std::string("\0\r\n", 3)) == std::string::npos; };
	if (ssid.empty() || ssid.size() > 32 || !valid_text(ssid)) return make_error("invalid SSID");
	if (!band.empty() && band != "2.4" && band != "5") return make_error("invalid band");
	if (!preferred_band.empty() && preferred_band != "2.4" && preferred_band != "5") return make_error("invalid preferred band");
	if (security == "open") {
		if (!password.empty()) return make_error("open network must not have a password");
	} else if (security == "personal" || security == "wpa" || security == "wpa-wpa2" || security == "wpa2" ||
	           security == "wpa3" || security == "wpa2-wpa3") {
		if (password.size() < 8 || password.size() > 63 || !valid_text(password)) return make_error("invalid password");
	} else {
		return make_error("unsupported security");
	}
	return {};
}

WifiProfile legacy_profile(const std::string &ssid, const std::string &password)
{
	WifiProfile p;
	p.ssid = ssid;
	p.password = password;
	p.security = "personal";
	return p;
}

Error RadioControl::save_profile(const WifiProfile &p, const Freqs &frequencies, const std::string &preferred) const
{
	auto get = [&](const char *band) {
		auto it = frequencies.find(band);
		return it == frequencies.end() ? std::vector<std::string>{} : it->second;
	};
	auto all = get("2.4");
	auto five = get("5");
	all.insert(all.end(), five.begin(), five.end());
	std::vector<std::pair<std::string, std::string>> values = {
		{"wifi.ssid", p.ssid}, {"wifi.pass", p.password}, {"wifi.security", p.security},
		{"wifi.hidden", p.hidden ? "true" : "false"}, {"wifi.band_preference", preferred},
		// The legacy aggregate list remains useful to previous startup scripts; new startup code uses the per-band
		// lists to create a preferred and a fallback network block.
		{"wifi.freq_list", join(all, " ")}, {"wifi.freq_list_2.4", join(get("2.4"), " ")}, {"wifi.freq_list_5", join(five, " ")}};
	std::map<std::string, std::optional<std::string>> before;
	for (const auto &[name, _] : values) {
		Error err;
		auto data = read_file(path_join(etc, name), &err);
		if (err && !err.not_exist()) return err;
		before[name] = data;
	}
	for (const auto &[name, value] : values) {
		if (auto err = write_private(path_join(etc, name), value)) {
			for (const auto &[old_name, old] : before) {
				std::string path = path_join(etc, old_name);
				if (old) (void)write_private(path, *old);
				else (void)remove(path);
			}
			return err;
		}
	}
	return {};
}

Error RadioControl::forget_profile() const
{
	for (const char *name : {"wifi.ssid", "wifi.pass", "wifi.security", "wifi.hidden", "wifi.freq_list", "wifi.freq_list_2.4",
	                         "wifi.freq_list_5"}) {
		auto err = remove(path_join(etc, name));
		if (err && !err.not_exist()) return err;
	}
	return {};
}

bool reserve_wifi()
{
	std::lock_guard lock(wifi_mu);
	if (wifi_busy) return false;
	wifi_busy = true;
	wifi_error.clear();
	return true;
}

void finish_wifi(const Error &err)
{
	std::lock_guard lock(wifi_mu);
	wifi_busy = false;
	if (err) wifi_error = "operationFailed";
}

bool is_supported() { return exists(paths.wlan0); }
bool is_ap_mode() { return exists(paths.ap_mode_file); }

std::string ap_password()
{
	auto data = read_file(paths.ap_pass_file);
	if (!data) return "";
	std::string out;
	for (char c : *data)
		if (c != '\n') out += c;
	return out;
}

std::pair<bool, bool> ApKeyGuard::allow(const std::string &given, const std::string &current)
{
	std::lock_guard lock(mu);
	if (current != key) {
		key = current;
		failures = 0;
	}
	if (failures >= ap_key_max_failures) return {false, true};
	if (!given.empty() && !current.empty() && given.size() == current.size()) {
		unsigned char diff = 0;
		for (std::size_t i = 0; i < given.size(); i++) diff |= static_cast<unsigned char>(given[i] ^ current[i]);
		if (diff == 0) return {true, false};
	}
	failures++;
	if (failures < ap_key_max_failures) return {false, false};
	// Once per key: refused requests after this are not logged.
	log::warn("Wi-Fi provisioning: {} wrong AP keys; refused until provisioning restarts", ap_key_max_failures);
	return {false, true};
}

ApKeyGuard ap_guard;

// --- saved networks (wifi_saved.go) ----------------------------------------------------------------------------------

std::string saved_name(const std::string &ssid) { return hex_encode(ssid); }

std::string RadioControl::saved_path(const std::string &ssid) const { return path_join(path_join(etc, "wifi.saved"), saved_name(ssid)); }

std::string RadioControl::selected_path() const { return run_dir.empty() ? "" : path_join(run_dir, "selected"); }

Error RadioControl::save_network(const WifiProfile &p, bool auto_connect) const
{
	std::string dir = path_join(etc, "wifi.saved");
	if (auto err = mkdir_all(dir, 0700)) return err;
	if (auto err = chmod_path(dir, 0700)) return err;
	std::string name = saved_name(p.ssid);
	std::string tmp = path_join(dir, "." + name + ".tmp");
	std::string old = path_join(dir, "." + name + ".old");
	std::string final_path = path_join(dir, name);
	(void)remove_all(tmp);
	if (auto err = mkdir(tmp, 0700)) return err;
	std::string security = p.security.empty() ? "personal" : p.security;
	for (const auto &[file, value] : std::vector<std::pair<std::string, std::string>>{
	         {"ssid", p.ssid}, {"pass", p.password}, {"security", security}, {"hidden", p.hidden ? "true" : "false"},
	         {"auto", auto_connect ? "1" : "0"}}) {
		if (auto err = write_private(path_join(tmp, file), value)) {
			(void)remove_all(tmp);
			return err;
		}
	}
	(void)remove_all(old);
	if (auto err = rename(final_path, old); err && !err.not_exist()) {
		(void)remove_all(tmp);
		return err;
	}
	if (auto err = rename(tmp, final_path)) {
		(void)rename(old, final_path);
		(void)remove_all(tmp);
		return err;
	}
	(void)remove_all(old);
	return trim_saved(name);
}

Error RadioControl::trim_saved(const std::string &keep) const
{
	Error err;
	auto networks = read_saved("", &err);
	if (err || networks.size() <= 32) return err;
	std::stable_sort(networks.begin(), networks.end(), [](const SavedNetwork &a, const SavedNetwork &b) { return a.used_ns < b.used_ns; });
	for (std::size_t i = 0; i < networks.size() - 32; i++) {
		if (saved_name(networks[i].ssid) == keep) continue;
		if (auto e = remove_all(saved_path(networks[i].ssid))) return e;
	}
	return {};
}

std::string read_trimmed(const std::string &path)
{
	auto data = read_file(path);
	if (!data) return "";
	return trim_right(*data, "\n");
}

std::vector<SavedNetwork> RadioControl::read_saved(const std::string &current, Error *err) const
{
	if (err) *err = {};
	std::string dir = path_join(etc, "wifi.saved");
	Error e;
	auto entries = read_dir(dir, &e);
	if (e) {
		if (!e.not_exist() && err) *err = e;
		return {};
	}
	std::vector<SavedNetwork> networks;
	for (const auto &entry : entries) {
		if (!entry.dir || entry.name.rfind(".", 0) == 0) continue;
		std::string path = path_join(dir, entry.name);
		auto ssid = read_file(path_join(path, "ssid"));
		if (!ssid || ssid->empty() || saved_name(*ssid) != entry.name) continue;
		SavedNetwork n;
		n.ssid = *ssid;
		n.security = read_trimmed(path_join(path, "security"));
		n.hidden = read_trimmed(path_join(path, "hidden")) == "true";
		n.auto_connect = read_trimmed(path_join(path, "auto")) == "1";
		n.current = !current.empty() && entry.name == current;
		if (n.security.empty()) n.security = "personal";
		if (entry.info_ok) n.used_ns = entry.mtime_ns;
		networks.push_back(n);
	}
	// The current network first, then the most recently joined.
	std::stable_sort(networks.begin(), networks.end(), [](const SavedNetwork &a, const SavedNetwork &b) {
		if (a.current != b.current) return a.current;
		return a.used_ns > b.used_ns;
	});
	return networks;
}

std::string saved_networks_json(const std::vector<SavedNetwork> &list)
{
	auto b = [](bool v) { return v ? "true" : "false"; };
	std::string out = "[";
	for (std::size_t i = 0; i < list.size(); i++) {
		const auto &n = list[i];
		out += (i ? "," : "") + std::string("{\"ssid\":") + quote(n.ssid) + ",\"security\":" + quote(n.security) +
		       ",\"hidden\":" + b(n.hidden) + ",\"autoConnect\":" + b(n.auto_connect) + ",\"current\":" + b(n.current) + "}";
	}
	return out + "]";
}

bool RadioControl::saved_profile(const std::string &ssid, WifiProfile &p, bool &auto_connect, Error &err) const
{
	std::string path = saved_path(ssid);
	auto stored = read_file(path_join(path, "ssid"), &err);
	if (!stored) return false;
	if (*stored != ssid) {
		err = Error{ENOENT, "file does not exist"};
		return false;
	}
	Error pass_err;
	auto pass = read_file(path_join(path, "pass"), &pass_err);
	if (pass_err && !pass_err.not_exist()) {
		err = pass_err;
		return false;
	}
	p = WifiProfile{};
	p.ssid = ssid;
	p.password = trim_right(pass.value_or(""), "\r\n");
	p.security = read_trimmed(path_join(path, "security"));
	p.hidden = read_trimmed(path_join(path, "hidden")) == "true";
	if (p.security.empty()) p.security = "personal";
	auto_connect = read_trimmed(path_join(path, "auto")) == "1";
	err = {};
	return true;
}

Error RadioControl::migrate_saved() const
{
	auto err = stat_path(path_join(etc, "wifi.saved"));
	if (!err.not_exist()) return err;
	if (auto p = configured_profile()) return save_network(*p, true);
	return {};
}

std::optional<WifiProfile> RadioControl::configured_profile() const
{
	std::string ssid = read_trimmed(path_join(etc, "wifi.ssid"));
	if (ssid.empty()) return std::nullopt;
	WifiProfile p;
	p.ssid = ssid;
	p.password = trim_right(read_trimmed(path_join(etc, "wifi.pass")), "\r");
	p.security = read_trimmed(path_join(etc, "wifi.security"));
	p.hidden = read_trimmed(path_join(etc, "wifi.hidden")) == "true";
	if (p.security.empty()) p.security = "personal";
	if (p.password.empty() && p.security != "open") return std::nullopt;
	return p;
}

Error RadioControl::set_auto_connect(const std::string &ssid, bool auto_connect) const
{
	std::string path = saved_path(ssid);
	if (auto err = stat_path(path)) return err;
	return write_private(path_join(path, "auto"), auto_connect ? "1" : "0");
}

Error RadioControl::select_network(const std::string &ssid) const
{
	std::string path = selected_path();
	if (path.empty()) return {};
	if (auto err = mkdir_all(dir_name(path), 0700)) return err;
	return write_private(path, saved_name(ssid));
}

Error RadioControl::clear_selected() const
{
	std::string path = selected_path();
	if (path.empty()) return {};
	auto err = remove(path);
	if (err && !err.not_exist()) return err;
	return {};
}

Error RadioControl::clear_selected_if(const std::string &name) const
{
	std::string path = selected_path();
	if (path.empty()) return {};
	auto data = read_file(path);
	if (!data || *data != name) return {};
	return clear_selected();
}

bool valid_saved_name(const std::string &name)
{
	auto b = hex_decode(name);
	return b && !b->empty() && saved_name(*b) == name;
}

std::optional<std::string> wpa_unescape(const std::string &value)
{
	std::string out;
	for (std::size_t i = 0; i < value.size(); i++) {
		if (value[i] != '\\') {
			out += value[i];
			continue;
		}
		if (i + 1 >= value.size()) return std::nullopt;
		i++;
		switch (value[i]) {
		case '\\': case '"': out += value[i]; break;
		case 'e': out += '\x1b'; break;
		case 'n': out += '\n'; break;
		case 'r': out += '\r'; break;
		case 't': out += '\t'; break;
		case 'x': {
			if (i + 2 >= value.size()) return std::nullopt;
			auto b = parse_uint(std::string_view(value).substr(i + 1, 2), 16, 8);
			if (!b) return std::nullopt;
			out += static_cast<char>(*b);
			i += 2;
			break;
		}
		default:
			return std::nullopt;
		}
	}
	return out;
}

std::optional<std::string> RadioControl::associated() const
{
	if (!request) return std::nullopt;
	auto out = request("STATUS");
	if (!out) return std::nullopt;
	std::string state, ssid, id_str;
	for (const auto &line : split(*out, "\n")) {
		std::string key, value;
		if (!cut(trim_right(line, "\r"), '=', key, value)) continue;
		if (key == "wpa_state") state = value;
		else if (key == "ssid") ssid = value;
		else if (key == "id_str") id_str = value;
	}
	if (state != "COMPLETED") return std::nullopt;
	if (valid_saved_name(id_str)) return id_str;
	if (auto decoded = wpa_unescape(ssid); decoded && !decoded->empty()) return saved_name(*decoded);
	return std::nullopt;
}

std::optional<std::string> RadioControl::running_name(const std::string &id, const std::string &listed) const
{
	if (auto reply = request("GET_NETWORK " + id + " id_str")) {
		std::string value = trim_space(*reply);
		if (value.size() >= 2 && value.front() == '"' && value.back() == '"') {
			std::string name = value.substr(1, value.size() - 2);
			if (valid_saved_name(name)) return name;
			return std::nullopt;
		}
	}
	if (auto decoded = wpa_unescape(listed); decoded && !decoded->empty()) return saved_name(*decoded);
	return std::nullopt;
}

void RadioControl::remove_running_network(const std::string &ssid) const
{
	if (!request) return;
	auto networks = request("LIST_NETWORKS");
	if (!networks) return;
	std::string name = saved_name(ssid);
	auto lines = split(*networks, "\n");
	for (std::size_t i = 1; i < lines.size(); i++) {
		auto f = split(trim_right(lines[i], "\r"), "\t");
		if (f.size() < 2) continue;
		if (!parse_uint(f[0], 10, 31)) continue;
		auto running = running_name(f[0], f[1]);
		if (!running || *running != name) continue;
		auto reply = request("REMOVE_NETWORK " + f[0]);
		if (!reply || trim_space(*reply) != "OK")
			log::warn("Wi-Fi: could not remove a forgotten network from wpa_supplicant: {}", reply ? *reply : std::string("no answer"));
	}
}

Error RadioControl::stop_joining_associated() const
{
	auto name = associated();
	if (!name) return {};
	if (auto err = clear_selected_if(*name)) return err;
	auto raw = hex_decode(*name).value_or("");
	auto err = set_auto_connect(raw, false);
	if (!err.not_exist()) return err;
	// Not saved yet: the network of a release with one network. Without an entry S30wifi would store it again as one
	// that connects automatically and rejoin it at once.
	if (auto p = configured_profile(); p && saved_name(p->ssid) == *name) return save_network(*p, false);
	return {};
}

Error RadioControl::remember_joined(const WifiProfile &p, bool auto_connect) const
{
	if (auto err = save_network(p, auto_connect)) return err;
	return select_network(p.ssid);
}

// --- wpa_supplicant's control socket (wpa_ctrl.go) -------------------------------------------------------------------

std::function<std::optional<std::string>(const std::string &)> wpa_control(const std::string &socket_path)
{
	return [socket_path](const std::string &command) -> std::optional<std::string> {
		static std::atomic<std::uint64_t> sequence{0};
		int fd = ::socket(AF_UNIX, SOCK_DGRAM | SOCK_CLOEXEC, 0);
		if (fd < 0) return std::nullopt;
		// wpa_supplicant replies to the sender's address, so the client socket needs a name; the abstract namespace
		// leaves no file behind.
		sockaddr_un local{};
		local.sun_family = AF_UNIX;
		std::string name = "nanokvm-wpa-" + std::to_string(::getpid()) + "-" + std::to_string(++sequence);
		std::memcpy(local.sun_path + 1, name.data(), std::min(name.size(), sizeof local.sun_path - 2));
		socklen_t local_len = static_cast<socklen_t>(offsetof(sockaddr_un, sun_path) + 1 + name.size());
		sockaddr_un remote{};
		remote.sun_family = AF_UNIX;
		std::strncpy(remote.sun_path, socket_path.c_str(), sizeof remote.sun_path - 1);
		socklen_t remote_len = static_cast<socklen_t>(offsetof(sockaddr_un, sun_path) + socket_path.size() + 1);
		if (::bind(fd, reinterpret_cast<sockaddr *>(&local), local_len) != 0 ||
		    ::connect(fd, reinterpret_cast<sockaddr *>(&remote), remote_len) != 0) {
			::close(fd);
			return std::nullopt;
		}
		// wpa_cli waits ten seconds for a reply and reads at most 4 KiB.
		auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(10);
		auto set_timeout = [&] {
			auto left = std::chrono::duration_cast<std::chrono::microseconds>(deadline - std::chrono::steady_clock::now()).count();
			if (left <= 0) return false;
			timeval tv{static_cast<time_t>(left / 1000000), static_cast<suseconds_t>(left % 1000000)};
			::setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
			::setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);
			return true;
		};
		if (!set_timeout() || ::send(fd, command.data(), command.size(), 0) < 0) {
			::close(fd);
			return std::nullopt;
		}
		char reply[4096];
		for (;;) {
			if (!set_timeout()) break;
			ssize_t n = ::recv(fd, reply, sizeof reply, 0);
			if (n < 0) {
				if (errno == EINTR) continue;
				break;
			}
			// Like wpa_ctrl_request, skip event messages ("<level>text"); they only reach monitors that attached,
			// which this client never does.
			if (n > 0 && reply[0] == '<') continue;
			::close(fd);
			return std::string(reply, static_cast<std::size_t>(n));
		}
		::close(fd);
		return std::nullopt;
	};
}

bool request_may_ride_wifi(const std::optional<IP16> &local)
{
	for (const auto &iface : interfaces()) {
		if (!local) continue;
		bool on = std::any_of(iface.addrs.begin(), iface.addrs.end(), [&](const IfAddr &a) { return a.ip == *local; });
		if (on) return !(iface.name.rfind("eth", 0) == 0 || iface.name.rfind("usb", 0) == 0);
	}
	return true;
}

} // namespace nkos::network
