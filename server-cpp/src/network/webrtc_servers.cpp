// The STUN and TURN servers and the DTLS preference of the WebRTC connections, as the Go server's
// service/network/webrtc_servers.go and config/webrtc.go: kept in server.yaml (stun, turn.turnAddr/turnUser/turnCred,
// dtlsCipher, every other key and comment kept), read by each new connection.

#include "network/internal.hpp"
#include "network/network.hpp"

#include "common/log.hpp"
#include "config/config.hpp"
#include "config/yaml.hpp"

#include <algorithm>

namespace nkos::network {

namespace {

constexpr const char *dtls_chacha20 = "chacha20";
constexpr const char *dtls_aes_gcm = "aes-gcm";
constexpr std::size_t max_stun_servers = 4;

std::mutex webrtc_mu;  // the settings in use
std::optional<WebRTCSettings> webrtc_current;
std::mutex webrtc_update_mu;  // a read-modify-write of the stored TURN password

std::string dtls_cipher_or_default(const std::string &cipher) { return cipher == dtls_aes_gcm ? cipher : dtls_chacha20; }

std::string stun_or_default(const std::string &stun)
{
	if (stun.empty() || stun == "stun.l.google.com:19302") return config::default_stun;
	return stun;
}

// yaml.v3's choice for a !!str scalar written by the encoder: plain when it reads back as the same string, otherwise
// double-quoted.
std::string yaml_scalar(const std::string &value)
{
	if (value.empty()) return "\"\"";
	auto lower = value;
	for (auto &c : lower)
		if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
	static const char *const special[] = {"~", "null", "true", "false", "yes", "no", "on", "off", "y", "n", ".inf", "-.inf", "+.inf", ".nan"};
	bool reserved = std::any_of(std::begin(special), std::end(special), [&](const char *w) { return lower == w; });
	bool numeric = parse_float(value).has_value() || (value.size() > 2 && value[0] == '0' && (value[1] == 'x' || value[1] == 'o' || value[1] == 'b'));
	bool safe_start = std::isalnum(static_cast<unsigned char>(value[0])) || value[0] == '.' || value[0] == '/';
	bool safe = std::all_of(value.begin(), value.end(), [](char c) {
		return std::isalnum(static_cast<unsigned char>(c)) || c == '.' || c == ':' || c == ',' || c == '/' || c == '@' || c == '-' || c == '_' || c == '+' || c == '=';
	});
	bool colon_end = value.back() == ':';
	if (!reserved && !numeric && safe_start && safe && !colon_end) return value;
	std::string out = "\"";
	for (char c : value) {
		if (c == '"' || c == '\\') out += '\\';
		out += c;
	}
	return out + "\"";
}

// setScalar and mappingChild on server.yaml as Go writes it (yaml.v3, indent 4): top-level keys at the start of a line,
// the turn mapping's keys indented by four spaces. A key that is missing is added at the end of its mapping.
std::string edit_webrtc_yaml(const std::string &text, const WebRTCSettings &w)
{
	auto lines = split(text, "\n");
	bool trailing = !lines.empty() && lines.back().empty();
	if (trailing) lines.pop_back();
	auto top_key = [](const std::string &line) -> std::string {
		if (line.empty() || line[0] == ' ' || line[0] == '#' || line[0] == '-') return "";
		auto colon = line.find(':');
		return colon == std::string::npos ? "" : line.substr(0, colon);
	};
	auto set_top = [&](const std::string &key, const std::string &value) {
		for (auto &line : lines) {
			if (top_key(line) == key) {
				line = key + ": " + yaml_scalar(value);
				return;
			}
		}
		lines.push_back(key + ": " + yaml_scalar(value));
	};
	set_top("stun", w.stun);
	// The turn mapping: its line and the indented lines below it.
	std::size_t turn = lines.size();
	for (std::size_t i = 0; i < lines.size(); i++)
		if (top_key(lines[i]) == "turn") turn = i;
	if (turn == lines.size()) {
		lines.push_back("turn:");
	} else if (trim_space(lines[turn].substr(5)) != "") {
		lines[turn] = "turn:";  // a scalar becomes a mapping
	}
	std::size_t end = turn + 1;
	while (end < lines.size() && (lines[end].rfind("    ", 0) == 0 || lines[end].empty())) end++;
	for (const auto &[key, value] : {std::pair<std::string, std::string>{"turnAddr", w.turn.addr}, {"turnUser", w.turn.user}, {"turnCred", w.turn.cred}}) {
		bool found = false;
		for (std::size_t i = turn + 1; i < end; i++) {
			if (lines[i].rfind("    " + key + ":", 0) == 0) {
				lines[i] = "    " + key + ": " + yaml_scalar(value);
				found = true;
				break;
			}
		}
		if (!found) {
			lines.insert(lines.begin() + static_cast<std::ptrdiff_t>(end), "    " + key + ": " + yaml_scalar(value));
			end++;
		}
	}
	set_top("dtlsCipher", w.dtls_cipher);
	return join(lines, "\n") + "\n";
}

} // namespace

std::function<WebRTCSettings()> read_webrtc = [] {
	std::lock_guard lock(webrtc_mu);
	if (!webrtc_current) {
		const auto &conf = config::get();
		webrtc_current = WebRTCSettings{conf.stun, WebRTCTurn{conf.turn.turn_addr, conf.turn.turn_user, conf.turn.turn_cred}, conf.dtls_cipher};
	}
	WebRTCSettings s = *webrtc_current;
	s.dtls_cipher = dtls_cipher_or_default(s.dtls_cipher);
	return s;
};

std::function<Error(const WebRTCSettings &)> update_webrtc = [](const WebRTCSettings &in) -> Error {
	WebRTCSettings w = in;
	w.dtls_cipher = dtls_cipher_or_default(w.dtls_cipher);
	std::lock_guard lock(webrtc_mu);
	Error err;
	auto text = read_file(config::configuration_file, &err);
	if (!text) return err;
	std::string parse_error;
	if (!yaml::parse(*text, parse_error)) return make_error(std::string("parse ") + config::configuration_file + ": " + parse_error);
	if (auto e = atomicfile_write(config::configuration_file, edit_webrtc_yaml(*text, w), 0600)) return e;
	webrtc_current = WebRTCSettings{stun_or_default(w.stun), w.turn, w.dtls_cipher};
	return {};
};

WebRTCServers webrtc_servers()
{
	auto s = read_webrtc();
	return WebRTCServers{s.stun, s.turn.addr, s.turn.user, s.turn.cred, s.dtls_cipher};
}

std::vector<std::string> split_stun(const std::string &stun)
{
	std::vector<std::string> out;
	std::string current;
	for (char c : stun) {
		if (c == ',' || c == ' ' || c == '\t') {
			if (!current.empty()) out.push_back(current);
			current.clear();
		} else {
			current += c;
		}
	}
	if (!current.empty()) out.push_back(current);
	return out;
}

std::string webrtc_servers_state_json(const WebRTCSettings &settings)
{
	std::string mode = "custom";
	auto stun = split_stun(settings.stun);
	if (settings.stun == config::default_stun) mode = "default";
	else if (settings.stun == "disable") mode = "off", stun.clear();
	return "{\"stunMode\":" + quote(mode) + ",\"stun\":" + json_strings(stun, false) +
	       ",\"defaultStun\":" + json_strings(split_stun(config::default_stun), false) + ",\"turn\":{\"address\":" + quote(settings.turn.addr) +
	       ",\"username\":" + quote(settings.turn.user) + ",\"hasCredential\":" + (settings.turn.cred.empty() ? "false" : "true") +
	       "},\"dtlsCipher\":" + quote(settings.dtls_cipher) + "}";
}

bool valid_hostname(const std::string &host)
{
	if (host.empty() || host.size() > 253) return false;
	for (const auto &label : split(host, ".")) {
		if (label.empty() || label.size() > 63 || label.front() == '-' || label.back() == '-') return false;
		for (char c : label)
			if (!std::isalnum(static_cast<unsigned char>(c)) && c != '-') return false;
	}
	return true;
}

bool printable(const std::string &s, std::size_t max)
{
	if (s.empty() || s.size() > max) return false;
	return std::all_of(s.begin(), s.end(), [](char c) { return c > ' ' && c <= '~'; });
}

std::optional<std::string> ice_address(const std::string &input, std::string &err)
{
	std::string value = trim_space(input);
	for (const char *scheme : {"stun:", "turn:"})
		if (value.rfind(scheme, 0) == 0) value = value.substr(5);
	std::string host, port;
	if (!split_host_port(value, host, port)) {
		err = "server must be host:port";
		return std::nullopt;
	}
	auto number = network::atoi(port);
	if (!number || *number < 1 || *number > 65535) {
		err = "port must be between 1 and 65535";
		return std::nullopt;
	}
	if (!parse_ip(host) && !valid_hostname(host)) {
		err = "invalid server host";
		return std::nullopt;
	}
	return join_host_port(host, port);
}

std::optional<WebRTCSettings> webrtc_servers_config(WebRTCServersRequest req, const WebRTCTurn &current, std::string &err)
{
	if (req.dtls_cipher.empty()) req.dtls_cipher = dtls_chacha20;  // a page from before the choice
	if (req.dtls_cipher != dtls_chacha20 && req.dtls_cipher != dtls_aes_gcm) {
		err = "DTLS cipher must be chacha20 or aes-gcm";
		return std::nullopt;
	}
	WebRTCSettings out;
	out.dtls_cipher = req.dtls_cipher;
	if (req.stun_mode == "default") {
	} else if (req.stun_mode == "off") {
		out.stun = "disable";
	} else if (req.stun_mode == "custom") {
		std::vector<std::string> servers;
		for (const auto &value : req.stun) {
			if (trim_space(value).empty()) continue;
			auto address = ice_address(value, err);
			if (!address) return std::nullopt;
			if (std::find(servers.begin(), servers.end(), *address) == servers.end()) servers.push_back(*address);
		}
		if (servers.empty()) {
			err = "add a STUN server or turn STUN off";
			return std::nullopt;
		}
		if (servers.size() > max_stun_servers) {
			err = "at most 4 STUN servers";
			return std::nullopt;
		}
		out.stun = join(servers, ",");
	} else {
		err = "STUN mode must be default, custom or off";
		return std::nullopt;
	}
	if (trim_space(req.turn_address).empty()) return out;
	auto address = ice_address(req.turn_address, err);
	if (!address) return std::nullopt;
	if (!printable(req.turn_username, 128)) {
		err = "TURN needs a username of up to 128 characters without spaces";
		return std::nullopt;
	}
	std::string credential = current.cred;
	if (req.turn_credential && !req.turn_credential->empty()) credential = *req.turn_credential;
	if (!printable(credential, 256)) {
		err = "TURN needs a password of up to 256 characters without spaces";
		return std::nullopt;
	}
	out.turn = WebRTCTurn{*address, req.turn_username, credential};
	return out;
}

Decoded<WebRTCServersRequest> decode_set_webrtc_servers(const RequestData &data)
{
	Decoded<WebRTCServersRequest> d;
	auto b = Binder::bind(data, true);
	WebRTCServersRequest r;
	if (b.ok()) {
		b.str("stunMode", "", r.stun_mode);
		b.strings("stun", "", r.stun);
		b.str("dtlsCipher", "", r.dtls_cipher);
		if (auto turn = b.object("turn")) {
			turn->str("address", "", r.turn_address);
			turn->str("username", "", r.turn_username);
			std::string credential;
			if (turn->str("credential", "", credential)) r.turn_credential = credential;
			if (!turn->ok()) b.fail();
		}
	}
	if (!b.ok()) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	d.value = r;
	return d;
}

// SetWebRTCServers after decoding.
std::pair<int, std::string> set_webrtc_servers(const WebRTCServersRequest &req, std::string &state)
{
	// Retaining a password is a read-modify-write operation: do not restore an old password over another
	// administrator's concurrent update.
	std::lock_guard lock(webrtc_update_mu);
	std::string err;
	auto settings = webrtc_servers_config(req, read_webrtc().turn, err);
	if (!settings) return {-1, err};
	if (auto e = update_webrtc(*settings)) return {-2, e.text};
	log::info("WebRTC: STUN {}, TURN {}, DTLS {} first", req.stun_mode, !settings->turn.addr.empty(), settings->dtls_cipher);
	state = webrtc_servers_state_json(read_webrtc());
	return {0, "success"};
}

} // namespace nkos::network
