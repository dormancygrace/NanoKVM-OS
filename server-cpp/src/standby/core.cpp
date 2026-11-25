#include "standby/core.hpp"

#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "storage/go.hpp"

#include <algorithm>
#include <arpa/inet.h>
#include <cerrno>
#include <chrono>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <sys/inotify.h>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::standby {

namespace go = nkos::storage::go;

namespace {

const unsigned char page_html_bytes[] = {
#embed "page.html"
};
const unsigned char page_js_bytes[] = {
#embed "page.js"
};

std::int64_t wall_ns()
{
	return std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::system_clock::now().time_since_epoch()).count();
}

std::vector<std::string> split(std::string_view s, std::string_view sep)
{
	std::vector<std::string> out;
	for (;;) {
		auto i = s.find(sep);
		if (i == std::string_view::npos) {
			out.emplace_back(s);
			return out;
		}
		out.emplace_back(s.substr(0, i));
		s.remove_prefix(i + sep.size());
	}
}

std::string replace_all(std::string_view s, std::string_view from, std::string_view to)
{
	std::string out;
	for (;;) {
		auto i = s.find(from);
		if (i == std::string_view::npos) {
			out += s;
			return out;
		}
		out += s.substr(0, i);
		out += to;
		s.remove_prefix(i + from.size());
	}
}

// --- a JSON reader for config.json, with encoding/json's rules for a flat struct ---

struct JsonReader {
	std::string_view s;
	std::size_t i = 0;
	bool type_error = false;

	void ws()
	{
		while (i < s.size() && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r')) i++;
	}
	bool lit(std::string_view w)
	{
		if (s.substr(i, w.size()) != w) return false;
		i += w.size();
		return true;
	}
	static void put_utf8(std::string &out, char32_t r)
	{
		if (r < 0x80) out += static_cast<char>(r);
		else if (r < 0x800) {
			out += static_cast<char>(0xc0 | (r >> 6));
			out += static_cast<char>(0x80 | (r & 0x3f));
		} else if (r < 0x10000) {
			out += static_cast<char>(0xe0 | (r >> 12));
			out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
			out += static_cast<char>(0x80 | (r & 0x3f));
		} else {
			out += static_cast<char>(0xf0 | (r >> 18));
			out += static_cast<char>(0x80 | ((r >> 12) & 0x3f));
			out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
			out += static_cast<char>(0x80 | (r & 0x3f));
		}
	}
	bool hex4(unsigned &v)
	{
		if (i + 4 > s.size()) return false;
		v = 0;
		for (int k = 0; k < 4; k++) {
			char c = s[i + k];
			v <<= 4;
			if (c >= '0' && c <= '9') v |= c - '0';
			else if (c >= 'a' && c <= 'f') v |= c - 'a' + 10;
			else if (c >= 'A' && c <= 'F') v |= c - 'A' + 10;
			else return false;
		}
		i += 4;
		return true;
	}
	bool string(std::string &out)
	{
		if (i >= s.size() || s[i] != '"') return false;
		i++;
		std::string raw;
		while (i < s.size()) {
			unsigned char c = static_cast<unsigned char>(s[i]);
			if (c == '"') {
				i++;
				// Invalid UTF-8 becomes U+FFFD, as encoding/json decodes it.
				out.clear();
				for (char32_t r : go::runes(raw)) put_utf8(out, r);
				return true;
			}
			if (c < 0x20) return false;
			if (c != '\\') {
				raw += static_cast<char>(c);
				i++;
				continue;
			}
			if (++i >= s.size()) return false;
			char e = s[i++];
			switch (e) {
			case '"': raw += '"'; break;
			case '\\': raw += '\\'; break;
			case '/': raw += '/'; break;
			case 'b': raw += '\b'; break;
			case 'f': raw += '\f'; break;
			case 'n': raw += '\n'; break;
			case 'r': raw += '\r'; break;
			case 't': raw += '\t'; break;
			case 'u': {
				unsigned v;
				if (!hex4(v)) return false;
				char32_t r = v;
				if (v >= 0xd800 && v < 0xdc00) {
					unsigned w;
					std::size_t save = i;
					if (s.substr(i, 2) == "\\u" && (i += 2, hex4(w)) && w >= 0xdc00 && w < 0xe000)
						r = 0x10000 + ((v - 0xd800) << 10) + (w - 0xdc00);
					else {
						i = save;
						r = 0xfffd;
					}
				} else if (v >= 0xdc00 && v < 0xe000) {
					r = 0xfffd;
				}
				put_utf8(raw, r);
				break;
			}
			default: return false;
			}
		}
		return false;
	}
	// A number literal; its text.
	bool number(std::string &out)
	{
		std::size_t start = i;
		if (i < s.size() && s[i] == '-') i++;
		if (i >= s.size()) return false;
		if (s[i] == '0') i++;
		else if (s[i] >= '1' && s[i] <= '9')
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		else return false;
		if (i < s.size() && s[i] == '.') {
			i++;
			if (i >= s.size() || s[i] < '0' || s[i] > '9') return false;
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		}
		if (i < s.size() && (s[i] == 'e' || s[i] == 'E')) {
			i++;
			if (i < s.size() && (s[i] == '+' || s[i] == '-')) i++;
			if (i >= s.size() || s[i] < '0' || s[i] > '9') return false;
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		}
		out = std::string(s.substr(start, i - start));
		return true;
	}
	bool skip_value()
	{
		ws();
		if (i >= s.size()) return false;
		std::string t;
		switch (s[i]) {
		case '"': return string(t);
		case '{': {
			i++;
			ws();
			if (i < s.size() && s[i] == '}') return ++i, true;
			for (;;) {
				ws();
				if (!string(t)) return false;
				ws();
				if (i >= s.size() || s[i] != ':') return false;
				i++;
				if (!skip_value()) return false;
				ws();
				if (i < s.size() && s[i] == ',') {
					i++;
					continue;
				}
				if (i < s.size() && s[i] == '}') return ++i, true;
				return false;
			}
		}
		case '[': {
			i++;
			ws();
			if (i < s.size() && s[i] == ']') return ++i, true;
			for (;;) {
				if (!skip_value()) return false;
				ws();
				if (i < s.size() && s[i] == ',') {
					i++;
					continue;
				}
				if (i < s.size() && s[i] == ']') return ++i, true;
				return false;
			}
		}
		case 't': return lit("true");
		case 'f': return lit("false");
		case 'n': return lit("null");
		default: return number(t);
		}
	}
};

// strconv.Quote (fmt %q): printable text as it is, Go escapes for quotes, backslashes, control characters and
// invalid UTF-8 (non-printable code points beyond Latin-1 are not met here).
std::string go_quote(std::string_view s)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
	auto byte_escape = [&](unsigned c) {
		out += "\\x";
		out += hex[c >> 4];
		out += hex[c & 15];
	};
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x80) {
			i++;
			switch (c) {
			case '"': out += "\\\""; break;
			case '\\': out += "\\\\"; break;
			case '\a': out += "\\a"; break;
			case '\b': out += "\\b"; break;
			case '\f': out += "\\f"; break;
			case '\n': out += "\\n"; break;
			case '\r': out += "\\r"; break;
			case '\t': out += "\\t"; break;
			case '\v': out += "\\v"; break;
			default:
				if (c < 0x20 || c == 0x7f) byte_escape(c);
				else out += static_cast<char>(c);
			}
			continue;
		}
		std::size_t n = c >= 0xf0 ? 4 : c >= 0xe0 ? 3 : 2;
		if (i + n > s.size() || !go::valid_utf8(s.substr(i, n))) {
			byte_escape(c);
			i++;
			continue;
		}
		char32_t r = go::runes(s.substr(i, n))[0];
		if (r >= 0x80 && r < 0xa0) {
			out += "\\u00";
			out += hex[(r >> 4) & 15];
			out += hex[r & 15];
		} else {
			out += s.substr(i, n);
		}
		i += n;
	}
	return out + "\"";
}

bool fold_equal(std::string_view a, std::string_view b) { return go::equal_fold(a, b); }

} // namespace

// --- config.json --------------------------------------------------------------------------------------------------

std::string config_json(const Config &c)
{
	auto bytes = [](const std::optional<std::string> &b) { return b ? gojson::quote(base64::encode(*b)) : std::string("null"); };
	auto flag = [](bool v) { return std::string(v ? "true" : "false"); };
	return "{\"Token\":" + gojson::quote(c.token) + ",\"Address\":" + gojson::quote(c.address) +
	       ",\"ClientAddress\":" + gojson::quote(c.client_address) + ",\"Prefix\":" + std::to_string(c.prefix) +
	       ",\"Gateway\":" + gojson::quote(c.gateway) + ",\"Interface\":" + gojson::quote(c.interface) +
	       ",\"MAC\":" + gojson::quote(c.mac) + ",\"DHCP\":" + flag(c.dhcp) + ",\"WiFi\":" + flag(c.wifi) +
	       ",\"Hostname\":" + gojson::quote(c.hostname) + ",\"Port\":" + std::to_string(c.port) +
	       ",\"Certificate\":" + bytes(c.certificate) + ",\"PrivateKey\":" + bytes(c.private_key) +
	       ",\"MagicWake\":" + flag(c.magic_wake) + "}";
}

Error parse_config(std::string_view data, Config &c)
{
	// Syntax first, as json.Unmarshal checks the whole text before it stores anything.
	{
		JsonReader check{data};
		if (!check.skip_value()) return "json: invalid JSON";
		check.ws();
		if (check.i != data.size()) return "json: invalid JSON";
	}
	JsonReader r{data};
	r.ws();
	if (r.lit("null")) return "";
	if (data[r.i] != '{') return "json: cannot unmarshal into Go value of type standby.Config";
	r.i++;
	bool type_error = false;
	static const char *names[] = {"Token", "Address", "ClientAddress", "Prefix", "Gateway", "Interface", "MAC",
	                              "DHCP", "WiFi", "Hostname", "Port", "Certificate", "PrivateKey", "MagicWake"};
	for (;;) {
		r.ws();
		if (data[r.i] == '}') break;
		std::string key;
		r.string(key);
		r.ws();
		r.i++;  // ':'
		r.ws();
		int field = -1;
		for (int k = 0; k < 14; k++)
			if (key == names[k]) field = k;
		if (field < 0)
			for (int k = 0; k < 14; k++)
				if (fold_equal(key, names[k])) {
					field = k;
					break;
				}
		char first = data[r.i];
		if (field < 0) {
			r.skip_value();
		} else if (first == 'n') {
			r.lit("null");
			if (field == 11) c.certificate.reset();
			if (field == 12) c.private_key.reset();
		} else {
			std::string *text = nullptr;
			switch (field) {
			case 0: text = &c.token; break;
			case 1: text = &c.address; break;
			case 2: text = &c.client_address; break;
			case 4: text = &c.gateway; break;
			case 5: text = &c.interface; break;
			case 6: text = &c.mac; break;
			case 9: text = &c.hostname; break;
			}
			if (text || field == 11 || field == 12) {
				if (first != '"') {
					type_error = true;
					r.skip_value();
				} else {
					std::string v;
					r.string(v);
					if (text) *text = v;
					else if (auto d = base64::decode(v)) (field == 11 ? c.certificate : c.private_key) = *d;
					else type_error = true;
				}
			} else if (field == 3 || field == 10) {
				std::string num;
				if (first == '"' || first == '{' || first == '[' || first == 't' || first == 'f' || !r.number(num)) {
					type_error = true;
					r.skip_value();
				} else {
					Error e;
					bool negative = num[0] == '-';
					auto v = num.find_first_of(".eE") == std::string::npos
					             ? parse_uint(std::string_view(num).substr(negative ? 1 : 0), 10, 64, e)
					             : std::nullopt;
					if (!v || *v > (negative ? 0x8000000000000000ull : 0x7fffffffffffffffull)) type_error = true;
					else (field == 3 ? c.prefix : c.port) = negative ? static_cast<long long>(0 - *v) : static_cast<long long>(*v);
				}
			} else {
				if (first == 't' || first == 'f') {
					bool v = first == 't';
					r.lit(v ? "true" : "false");
					(field == 7 ? c.dhcp : field == 8 ? c.wifi : c.magic_wake) = v;
				} else {
					type_error = true;
					r.skip_value();
				}
			}
		}
		r.ws();
		if (data[r.i] == ',') r.i++;
	}
	if (type_error) return "json: cannot unmarshal into Go struct field of type standby.Config";
	return "";
}

Error load_config(const std::string &path, Config &c)
{
	std::string data;
	if (auto e = go::read_file(path, data)) return e.text;
	if (Error e = parse_config(data, c); !e.empty()) return e;
	if (c.token.size() != 64 || !parse_ip4(c.address) || c.port < 1 || c.port > 65535) return "invalid standby configuration";
	return "";
}

// --- state and the page -----------------------------------------------------------------------------------------------

std::string status_json(const Status &s)
{
	std::string out = "{\"state\":" + gojson::quote(s.state) + ",\"safe\":" + (s.safe ? "true" : "false");
	if (!s.error.empty()) out += ",\"error\":" + gojson::quote(s.error);
	out += ",\"mac\":" + gojson::quote(s.mac) + ",\"magicWake\":" + (s.magic_wake ? "true" : "false") + "}\n";
	return out;
}

State::State(const Config &c)
{
	status_.state = "preparing";
	status_.mac = c.mac;
	status_.magic_wake = c.magic_wake;
}

Status State::get()
{
	std::lock_guard lock(mu_);
	return status_;
}

void State::fail(const std::string &error)
{
	std::lock_guard lock(mu_);
	if (status_.state == "failed" || status_.state == "restarting") return;
	status_.state = "failed";
	status_.safe = false;
	status_.error = error;
}

void State::restart_failed(const std::string &error)
{
	std::lock_guard lock(mu_);
	if (status_.state == "restarting") {
		status_.state = "failed";
		status_.safe = false;
		status_.error = error;
	}
}

void State::ready()
{
	std::lock_guard lock(mu_);
	if (status_.state == "preparing") {
		status_.state = "ready";
		status_.safe = true;
		ready_at_ns_ = wall_ns();
	}
}

std::pair<bool, Error> State::restart(bool magic)
{
	std::lock_guard lock(mu_);
	return restart_locked(magic);
}

std::pair<bool, Error> State::magic_restart(std::int64_t received_ns)
{
	std::lock_guard lock(mu_);
	if (received_ns == 0 || received_ns < ready_at_ns_) return {false, "packet predates readiness"};
	return restart_locked(true);
}

std::pair<bool, Error> State::restart_locked(bool magic)
{
	if (status_.state == "restarting") return {false, ""};
	if (status_.state != "ready" && (magic || status_.state != "failed")) return {false, "shutdown is still being checked"};
	status_.state = "restarting";
	status_.safe = false;
	return {true, ""};
}

std::string_view page_html() { return {reinterpret_cast<const char *>(page_html_bytes), sizeof page_html_bytes}; }
std::string_view page_js() { return {reinterpret_cast<const char *>(page_js_bytes), sizeof page_js_bytes}; }

void http_error(Reply &r, std::string_view text, int status)
{
	std::erase_if(r.headers, [](const auto &h) { return h.first == "Content-Type" || h.first == "X-Content-Type-Options"; });
	r.headers.emplace_back("Content-Type", "text/plain; charset=utf-8");
	r.headers.emplace_back("X-Content-Type-Options", "nosniff");
	r.status = status;
	r.body = std::string(text) + "\n";
}

Reply handle(const Config &c, State &state, const HttpIn &in)
{
	Reply r;
	r.headers = {{"Cache-Control", "no-store"},
	             {"Referrer-Policy", "no-referrer"},
	             {"X-Content-Type-Options", "nosniff"},
	             {"Content-Security-Policy",
	              "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; "
	              "frame-ancestors 'none'; base-uri 'none'; form-action 'none'"}};
	if (in.method == "GET" && in.path == "/standby.js") {
		r.headers.emplace_back("Content-Type", "text/javascript; charset=utf-8");
		r.body = std::string(page_js());
		return r;
	}
	if (in.method == "GET" && (in.path == "/" || in.path == "/standby")) {
		r.headers.emplace_back("Content-Type", "text/html; charset=utf-8");
		r.body = std::string(page_html());
		return r;
	}
	const std::string &header = in.authorization;
	std::string_view auth = header;
	bool bearer = auth.starts_with("Bearer ");
	if (bearer) auth.remove_prefix(7);
	bool equal = auth.size() == c.token.size();
	unsigned char diff = 0;
	if (equal)
		for (std::size_t k = 0; k < auth.size(); k++) diff |= static_cast<unsigned char>(auth[k] ^ c.token[k]);
	if (!bearer || c.token.size() != 64 || !equal || diff != 0) {
		http_error(r, "Use the shutdown browser's restart key.", 401);
		return r;
	}
	if (in.method == "GET" && in.path == "/standby/status") {
		r.headers.emplace_back("Content-Type", "application/json");
		r.body = status_json(state.get());
		return r;
	}
	if (in.method == "POST" && in.path == "/standby/start") {
		auto origin = go::parse_url(in.origin);
		std::string scheme = in.tls ? "https" : "http";
		if (!origin || origin->scheme != scheme || origin->host != in.host || origin->has_user || !origin->path.empty() ||
		    !origin->raw_query.empty() || !origin->fragment.empty() || in.sec_fetch_site == "cross-site") {
			http_error(r, "same-origin request required", 403);
			return r;
		}
		auto [first, err] = state.restart(false);
		if (!err.empty()) {
			http_error(r, err, 409);
			return r;
		}
		r.status = 202;
		r.restart_first = first;
		return r;
	}
	http_error(r, "404 page not found", 404);
	return r;
}

// --- mounts and block devices ---------------------------------------------------------------------------------------

bool has_option(std::string_view options, std::string_view option)
{
	for (const auto &s : split(options, ","))
		if (s == option) return true;
	return false;
}

std::string unescape_mount(std::string_view s)
{
	// strings.NewReplacer: at each position the first matching old string, scanning left to right.
	static const std::pair<std::string_view, std::string_view> table[] = {
		{"\\040", " "}, {"\\011", "\t"}, {"\\012", "\n"}, {"\\134", "\\"}};
	std::string out;
	for (std::size_t i = 0; i < s.size();) {
		bool hit = false;
		for (const auto &[from, to] : table)
			if (s.substr(i, from.size()) == from) {
				out += to;
				i += from.size();
				hit = true;
				break;
			}
		if (!hit) out += s[i++];
	}
	return out;
}

Error parse_mounts(std::string_view data, std::vector<Mount> &out)
{
	out.clear();
	for (const auto &line : split(go::trim_space(data), "\n")) {
		auto f = go::fields(line);
		int sep = -1;
		for (std::size_t i = 0; i < f.size(); i++)
			if (f[i] == "-") {
				sep = static_cast<int>(i);
				break;
			}
		if (f.size() < 10 || sep < 6 || f.size() < static_cast<std::size_t>(sep) + 4) {
			out.clear();
			return "invalid mountinfo";
		}
		out.push_back(Mount{unescape_mount(f[4]), unescape_mount(f[sep + 2]), f[sep + 1], f[5], f[sep + 3]});
	}
	return "";
}

Error read_mounts(std::vector<Mount> &out, const std::string &path)
{
	std::string data;
	if (auto e = go::read_file(path, data)) return e.text;
	return parse_mounts(data, out);
}

bool memory_fs(std::string_view t)
{
	for (std::string_view m : {"tmpfs", "proc", "sysfs", "devtmpfs", "devpts", "configfs", "cgroup", "cgroup2", "mqueue",
	                           "securityfs", "debugfs", "tracefs", "pstore", "bpf"})
		if (t == m) return true;
	return false;
}

bool block_fs(std::string_view t)
{
	for (std::string_view m : {"ext4", "f2fs", "vfat", "exfat", "ext2", "ext3", "squashfs", "iso9660"})
		if (t == m) return true;
	return false;
}

Error sync_filesystems(const std::vector<Mount> &mounts)
{
	for (const auto &m : mounts) {
		if (!block_fs(m.type)) continue;
		int fd = ::open(m.point.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
		if (fd < 0) return go::errno_text(errno);
		int rc = ::syncfs(fd);
		int err = errno;
		::close(fd);
		if (rc != 0) return "syncfs " + m.point + ": " + go::errno_text(err);
	}
	return "";
}

Error parse_counters(std::string_view data, Counters &c)
{
	auto f = go::fields(data);
	c = {};
	if (f.size() < 17) return "block statistics lack discard/flush accounting";
	// Go walks a map here: with more than one bad field the reported one is random; the first is reported.
	std::pair<int, std::uint64_t *> slots[] = {{4, &c.writes}, {6, &c.written}, {8, &c.in_flight},
	                                           {11, &c.discards}, {13, &c.discarded}, {15, &c.flushes}};
	for (auto [i, p] : slots) {
		Error e;
		auto v = parse_uint(f[i], 10, 64, e);
		if (!v) return e;
		*p = *v;
	}
	return "";
}

Error process_released(std::string_view stat, bool &released)
{
	released = false;
	auto end = stat.rfind(')');
	if (end == std::string_view::npos) return "invalid process stat";
	auto f = go::fields(stat.substr(end + 1));
	if (f.size() < 7) return "truncated process stat";
	Error e;
	auto flags = parse_uint(f[6], 10, 64, e);
	if (!flags) return e;
	released = f[0] == "Z" || f[0] == "X" || (*flags & 0x00200000) != 0;
	return "";
}

// --- the handoff's checks ---------------------------------------------------------------------------------------------

Error handoff_inittab(std::string_view table, std::string &out)
{
	const std::string_view shutdown = "::shutdown:/sbin/openrc shutdown";
	if (table.find(shutdown) == std::string_view::npos) return "custom init shutdown actions are unsupported";
	for (const auto &line : split(table, "\n")) {
		if (go::trim_space(line).starts_with("#")) continue;
		if (line.find(":shutdown:") != std::string::npos && line != shutdown) return "custom init shutdown actions are unsupported";
		if (line.find(":restart:") != std::string::npos) return "custom init restart action is unsupported";
	}
	out = replace_all(table, shutdown, std::string("::shutdown:") + root + "/init shutdown");
	out += std::string("\n::restart:") + root + "/init takeover\n::respawn:" + root + "/init acknowledge\n";
	return "";
}

Error ram_wpa(std::string_view data, std::string &out)
{
	static const char *allowed[] = {"ssid", "psk", "sae_password", "key_mgmt", "scan_ssid", "id_str", "priority",
	                                "disabled", "bgscan", "ieee80211w", "sae_pwe", "proto", "pairwise", "group",
	                                "freq_list", "country", "ap_scan"};
	std::vector<std::string> lines{"ctrl_interface=/run/wpa_supplicant", "update_config=0"};
	for (const auto &raw : split(data, "\n")) {
		std::string line = go::trim_space(raw);
		if (line.empty() || line.starts_with("#")) continue;
		if (line == "network={" || line == "}") {
			lines.push_back(line);
			continue;
		}
		auto eq = line.find('=');
		std::string key = line.substr(0, eq);
		if (key == "ctrl_interface" || key == "update_config") continue;
		bool ok = false;
		for (const char *a : allowed)
			if (key == a) ok = true;
		if (eq == std::string::npos || !ok) return "unsupported Wi-Fi standby setting " + go_quote(key);
		lines.push_back(line);
	}
	if (data.find("network={") == std::string_view::npos) return "no station Wi-Fi configuration";
	out.clear();
	for (std::size_t i = 0; i < lines.size(); i++) out += (i ? "\n" : "") + lines[i];
	out += "\n";
	return "";
}

namespace {

std::uint32_t le32(std::uint64_t v) { return static_cast<std::uint32_t>(v); }

std::string ip4_string(std::uint32_t le)
{
	return std::to_string(le & 255) + "." + std::to_string((le >> 8) & 255) + "." + std::to_string((le >> 16) & 255) +
	       "." + std::to_string(le >> 24);
}

// net.IPMask(4 bytes).Size(): the leading ones, or -1 for a non-canonical mask.
int mask_bits(std::uint32_t le)
{
	unsigned char b[4] = {static_cast<unsigned char>(le), static_cast<unsigned char>(le >> 8),
	                      static_cast<unsigned char>(le >> 16), static_cast<unsigned char>(le >> 24)};
	int ones = 0;
	bool zero = false;
	for (unsigned char x : b)
		for (int k = 7; k >= 0; k--) {
			bool bit = (x >> k) & 1;
			if (bit && zero) return -1;
			if (bit) ones++;
			else zero = true;
		}
	return ones;
}

std::uint32_t ip4_le(const std::string &four)
{
	return static_cast<unsigned char>(four[0]) | static_cast<unsigned char>(four[1]) << 8 |
	       static_cast<unsigned char>(four[2]) << 16 | static_cast<std::uint32_t>(static_cast<unsigned char>(four[3])) << 24;
}

} // namespace

Error client_route(std::string_view table, const std::string &address, std::string &iface)
{
	auto ip = parse_ip4(address);
	if (!ip || static_cast<unsigned char>((*ip)[0]) == 127) return "direct IPv4 management is required";
	std::uint32_t target = ip4_le(*ip);
	int best_bits = -1;
	std::uint64_t best_metric = 0xffffffffull;
	std::string best;
	for (const auto &line : split(table, "\n")) {
		auto f = go::fields(line);
		if (f.size() < 8 || f[0] == "Iface") continue;
		Error e;
		auto dest = parse_uint(f[1], 16, 32, e);
		auto mask = parse_uint(f[7], 16, 32, e);
		auto metric = parse_uint(f[6], 10, 32, e);
		auto flags = parse_uint(f[3], 16, 32, e);
		if (!dest || !mask || !metric || !flags) return "unrecognized routing table";
		if ((*flags & 1) == 0 || (target & *mask) != *dest) continue;
		int bits = mask_bits(le32(*mask));
		if (bits < 0) return "invalid route mask";
		if (bits > best_bits || (bits == best_bits && *metric < best_metric)) {
			best_bits = bits;
			best_metric = *metric;
			best = f[0];
		}
	}
	if (best.empty()) return "no return route for the management client";
	iface = best;
	return "";
}

Error simple_routes(std::string_view table, const Config &c)
{
	auto ip = parse_ip4(c.address);
	if (!ip || c.prefix < 0 || c.prefix > 32) return "invalid management subnet";
	std::uint32_t mask_be = c.prefix == 0 ? 0 : 0xffffffffu << (32 - c.prefix);
	std::uint32_t mask = __builtin_bswap32(mask_be);
	std::uint32_t subnet = ip4_le(*ip) & mask;
	for (const auto &line : split(table, "\n")) {
		auto f = go::fields(line);
		if (f.size() < 8 || f[0] != c.interface) continue;
		Error e;
		auto dest = parse_uint(f[1], 16, 32, e);
		auto gateway = parse_uint(f[2], 16, 32, e);
		auto route_mask = parse_uint(f[7], 16, 32, e);
		if (!dest || !gateway || !route_mask) return "invalid management route";
		if (*dest == 0 && *route_mask == 0) continue;
		if (le32(*dest) == subnet && le32(*route_mask) == mask && *gateway == 0) continue;
		return "custom/classless routes on the management interface are unsupported for standby";
	}
	return "";
}

Error default_gateway(std::string_view table, const std::string &iface, std::string &gateway)
{
	gateway.clear();
	for (const auto &line : split(table, "\n")) {
		auto f = go::fields(line);
		if (f.size() < 8 || f[0] != iface || f[1] != "00000000") continue;
		if (!gateway.empty()) {
			gateway.clear();
			return "multiple management default routes are unsupported";
		}
		Error e;
		auto n = parse_uint(f[2], 16, 32, e);
		if (!n) {
			gateway.clear();
			return e;
		}
		gateway = ip4_string(le32(*n));
	}
	if (gateway == "0.0.0.0") gateway.clear();
	return "";
}

Error check_ram_budget(std::int64_t payload, std::int64_t available)
{
	if (payload < 0 || payload > (48ll << 20) || available < payload + (24ll << 20))
		return "not enough RAM for network standby; stop video/add-ons and retry";
	return "";
}

Error skips_stop(const std::string &deptree, const std::string &service, bool &skipped)
{
	skipped = false;
	std::string data;
	if (auto e = go::read_file(deptree, data)) return "OpenRC deptree: " + e.text;
	auto lines = split(data, "\n");
	std::string prefix;
	std::string suffix = "_service='" + service + "'";
	for (const auto &line : lines)
		if (line.ends_with(suffix)) {
			prefix = line.substr(0, line.size() - suffix.size()) + "_keyword_";
			break;
		}
	if (prefix.empty()) return "";
	for (const auto &line : lines) {
		auto eq = line.find('=');
		if (eq == std::string::npos || !line.starts_with(prefix)) continue;
		if (line.substr(0, eq).size() < prefix.size()) continue;
		std::string_view value = std::string_view(line).substr(eq + 1);
		while (!value.empty() && value.front() == '\'') value.remove_prefix(1);
		while (!value.empty() && value.back() == '\'') value.remove_suffix(1);
		if (value == "-shutdown" || value == "noshutdown") {
			skipped = true;
			return "";
		}
	}
	return "";
}

Error watchdogs_released(const std::string &sysfs)
{
	DIR *d = ::opendir(sysfs.c_str());
	if (!d) {
		if (errno == ENOENT) return "";
		return go::path_error("open", sysfs, errno);
	}
	std::vector<std::string> names;
	while (dirent *e = ::readdir(d)) {
		std::string n = e->d_name;
		if (n != "." && n != "..") names.push_back(n);
	}
	::closedir(d);
	std::sort(names.begin(), names.end());
	for (const auto &name : names) {
		std::string data;
		if (auto e = go::read_file(sysfs + "/" + name + "/state", data)) {
			if (e.sys == ENOENT) continue;
			return e.text;
		}
		if (data != "inactive\n") return "watchdog " + name + " has not released userspace ownership";
	}
	return "";
}

FailureWatch::~FailureWatch()
{
	if (fd_ >= 0) ::close(fd_);
}

Error FailureWatch::open(const std::string &dir, const std::string &deptree, std::unique_ptr<FailureWatch> &out)
{
	if (Error e = mkdir_all(dir, 0755); !e.empty()) return e;
	int fd = ::inotify_init1(IN_CLOEXEC | IN_NONBLOCK);
	if (fd < 0) return go::errno_text(errno);
	if (::inotify_add_watch(fd, dir.c_str(), IN_ONLYDIR | IN_CREATE | IN_MOVED_TO | IN_DELETE_SELF | IN_MOVE_SELF) < 0) {
		int err = errno;
		::close(fd);
		return go::errno_text(err);
	}
	auto w = std::unique_ptr<FailureWatch>(new FailureWatch);
	w->fd_ = fd;
	w->deptree_ = deptree;
	DIR *d = ::opendir(dir.c_str());
	if (!d) return go::path_error("open", dir, errno);
	std::vector<std::string> names;
	while (dirent *e = ::readdir(d)) {
		std::string n = e->d_name;
		if (n != "." && n != "..") names.push_back(n);
	}
	::closedir(d);
	std::sort(names.begin(), names.end());
	if (!names.empty()) w->existing_ = "OpenRC has a failed service: " + names[0];
	out = std::move(w);
	return "";
}

Error FailureWatch::result()
{
	if (!existing_.empty()) return existing_;
	alignas(inotify_event) char buf[16384];
	for (;;) {
		ssize_t n = ::read(fd_, buf, sizeof buf);
		if (n < 0 && errno == EAGAIN) return "";
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) return go::errno_text(errno);
		if (n == 0) return "OpenRC failure watch ended unexpectedly";
		for (ssize_t offset = 0; offset < n;) {
			if (offset + 16 > n) return "truncated OpenRC failure event";
			std::uint32_t mask, length;
			std::memcpy(&mask, buf + offset + 4, 4);
			std::memcpy(&length, buf + offset + 12, 4);
			if (offset + 16 + static_cast<ssize_t>(length) > n) return "truncated OpenRC failure name";
			if (mask & (IN_Q_OVERFLOW | IN_IGNORED | IN_DELETE_SELF | IN_MOVE_SELF)) return "OpenRC failure monitoring was interrupted";
			if (mask & (IN_CREATE | IN_MOVED_TO)) {
				std::string_view name(buf + offset + 16, length);
				while (!name.empty() && name.back() == '\0') name.remove_suffix(1);
				bool skipped = false;
				if (Error e = skips_stop(deptree_, std::string(name), skipped); !e.empty()) return e;
				if (!skipped) return "OpenRC service failed or refused shutdown: " + std::string(name);
			}
			offset += 16 + length;
		}
	}
}

std::string recovery_text(const Error &cause)
{
	return "RAM service failed after storage release; keep power connected for automatic restart in two minutes: " + cause;
}

Failure recover_fatal(const Failure &err, const std::function<void(std::chrono::seconds)> &wait,
                      const std::function<Error()> &reboot)
{
	if (!err.recovery) return err;
	wait(std::chrono::minutes(2));
	if (Error e = reboot(); !e.empty()) return {err.text + "; automatic restart failed: " + e, true};
	return {};
}

bool magic_packet(std::string_view packet, std::string_view mac)
{
	if (mac.size() != 6) return false;
	std::string pattern(6, '\xff');
	for (int i = 0; i < 16; i++) pattern += mac;
	return packet.find(pattern) != std::string_view::npos;
}

// --- RAM root staging ----------------------------------------------------------------------------------------------

Error mkdir_all(const std::string &path, unsigned mode)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) {
		if (S_ISDIR(st.st_mode)) return "";
		return go::path_error("mkdir", path, ENOTDIR);
	}
	std::size_t end = path.size();
	while (end > 0 && path[end - 1] == '/') end--;
	std::size_t j = end;
	while (j > 0 && path[j - 1] != '/') j--;
	if (j > 1) {
		std::string parent = path.substr(0, j - 1);
		if (Error e = mkdir_all(parent, mode); !e.empty()) return e;
	}
	if (::mkdir(path.c_str(), mode) != 0) {
		int err = errno;
		if (::lstat(path.c_str(), &st) == 0 && S_ISDIR(st.st_mode)) return "";
		return go::path_error("mkdir", path, err);
	}
	return "";
}

Error copy_file(const std::string &src, const std::string &dst, unsigned mode)
{
	int in = ::open(src.c_str(), O_RDONLY | O_CLOEXEC);
	if (in < 0) return go::path_error("open", src, errno);
	struct stat st{};
	if (::fstat(in, &st) != 0) {
		int err = errno;
		::close(in);
		return go::path_error("stat", src, err);
	}
	if (!S_ISREG(st.st_mode) || st.st_size > (32 << 20)) {
		::close(in);
		return "unsupported RAM payload: " + src;
	}
	if (Error e = mkdir_all(go::dir(dst), 0700); !e.empty()) {
		::close(in);
		return e;
	}
	int out = ::open(dst.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, mode);
	if (out < 0) {
		int err = errno;
		::close(in);
		return go::path_error("open", dst, err);
	}
	Error result;
	char buf[65536];
	for (;;) {
		ssize_t n = ::read(in, buf, sizeof buf);
		if (n < 0) {
			if (errno == EINTR) continue;
			result = go::path_error("read", src, errno);
			break;
		}
		if (n == 0) break;
		for (ssize_t off = 0; off < n;) {
			ssize_t w = ::write(out, buf + off, static_cast<std::size_t>(n - off));
			if (w < 0) {
				if (errno == EINTR) continue;
				result = go::path_error("write", dst, errno);
				break;
			}
			off += w;
		}
		if (!result.empty()) break;
	}
	::close(in);
	if (::close(out) != 0 && result.empty()) result = go::path_error("close", dst, errno);
	return result;
}

namespace {

// The parts of an ELF64 little-endian file Go's debug/elf reads for the stager.
struct Elf {
	std::string data;
	std::vector<std::pair<std::uint32_t, std::string>> progs;  // type, contents
	std::optional<std::vector<std::string>> needed;          // DT_NEEDED in order
	Error needed_error;
};

template <typename T> T rd(const std::string &d, std::size_t off)
{
	T v{};
	if (off <= d.size() && sizeof(T) <= d.size() - off) std::memcpy(&v, d.data() + off, sizeof(T));
	return v;
}

// Whether size bytes at off lie within the file, without the sum that a crafted offset would wrap.
bool within(const std::string &d, std::uint64_t off, std::uint64_t size) { return off <= d.size() && size <= d.size() - off; }

Error open_elf(const std::string &path, Elf &elf)
{
	if (auto e = go::read_file(path, elf.data)) return e.text;
	const std::string &d = elf.data;
	if (d.size() < 16) return "cannot read ELF identifier 'EOF' in record at byte 0x0";
	if (d.compare(0, 4, "\x7f" "ELF") != 0) {
		std::string v = "[";
		for (int i = 0; i < 4; i++) v += (i ? " " : "") + std::to_string(static_cast<unsigned char>(d[i]));
		return "bad magic number '" + v + "]' in record at byte 0x0";
	}
	if (d[4] != 2 || d[5] != 1) return "unsupported ELF class or byte order";
	if (d.size() < 64) return "unexpected EOF";
	auto phoff = rd<std::uint64_t>(d, 32), shoff = rd<std::uint64_t>(d, 40);
	auto phentsize = rd<std::uint16_t>(d, 54), phnum = rd<std::uint16_t>(d, 56);
	auto shentsize = rd<std::uint16_t>(d, 58), shnum = rd<std::uint16_t>(d, 60);
	for (unsigned i = 0; i < phnum; i++) {
		std::uint64_t step = static_cast<std::uint64_t>(i) * phentsize;
		if (!within(d, phoff, step) || !within(d, phoff + step, 56)) return "unexpected EOF";
		std::size_t at = phoff + step;
		auto type = rd<std::uint32_t>(d, at);
		auto off = rd<std::uint64_t>(d, at + 8), filesz = rd<std::uint64_t>(d, at + 32);
		std::string contents;
		if (type == 3) {  // PT_INTERP
			if (!within(d, off, filesz)) return "unexpected EOF";
			contents = d.substr(off, filesz);
		}
		elf.progs.emplace_back(type, contents);
	}
	struct Sec {
		std::uint32_t type;
		std::uint32_t link;
		std::uint64_t off, size;
	};
	std::vector<Sec> secs;
	for (unsigned i = 0; i < shnum; i++) {
		std::uint64_t step = static_cast<std::uint64_t>(i) * shentsize;
		if (!within(d, shoff, step) || !within(d, shoff + step, 64)) return "unexpected EOF";
		std::size_t at = shoff + step;
		secs.push_back({rd<std::uint32_t>(d, at + 4), rd<std::uint32_t>(d, at + 40), rd<std::uint64_t>(d, at + 24),
		                rd<std::uint64_t>(d, at + 32)});
	}
	elf.needed.emplace();
	const Sec *dyn = nullptr;
	for (const auto &s : secs)
		if (s.type == 6) {  // SHT_DYNAMIC
			dyn = &s;
			break;
		}
	if (!dyn) return "";
	if (!within(d, dyn->off, dyn->size)) {
		elf.needed_error = "unexpected EOF";
		return "";
	}
	if (dyn->size % 16 != 0) {
		elf.needed_error = "length of dynamic section is not a multiple of dynamic entry size";
		return "";
	}
	if (dyn->link == 0 || dyn->link >= secs.size()) {
		elf.needed_error = "section has invalid string table link";
		return "";
	}
	const Sec &str = secs[dyn->link];
	if (!within(d, str.off, str.size)) {
		elf.needed_error = "unexpected EOF";
		return "";
	}
	std::string_view strtab(d.data() + str.off, str.size);
	for (std::uint64_t at = dyn->off; at < dyn->off + dyn->size; at += 16) {
		auto tag = rd<std::int64_t>(d, at);
		auto val = rd<std::uint64_t>(d, at + 8);
		if (tag != 1) continue;  // DT_NEEDED
		if (val >= strtab.size()) continue;
		auto end = strtab.find('\0', val);
		elf.needed->emplace_back(strtab.substr(val, end == std::string_view::npos ? std::string_view::npos : end - val));
	}
	return "";
}

bool musl_self_library(const std::string &interpreter, const std::string &name)
{
	std::string base = go::base(interpreter);
	if (go::dir(interpreter) != "/lib" || !base.starts_with("ld-musl-") || !base.ends_with(".so.1") ||
	    base.size() <= std::string_view("ld-musl-.so.1").size())
		return false;
	for (std::string_view prefix : {"libc.", "libpthread.", "librt.", "libm.", "libdl.", "libutil.", "libxnet."})
		if (name.starts_with(prefix)) return true;
	return false;
}

struct Stager {
	std::string source_root, target_root;
	std::map<std::string, bool> &files;

	Error walk(const std::string &name, std::string interpreter, std::map<std::string, bool> &visited)
	{
		if (name.empty() || name[0] != '/' || go::clean(name) != name) return "unsupported ELF path " + name;
		if (visited[name]) return "";
		visited[name] = true;
		std::string source = go::join(source_root, name);
		Elf f;
		if (Error e = open_elf(source, f); !e.empty()) return e;
		for (const auto &[type, contents] : f.progs) {
			if (type != 3) continue;
			std::string_view v = contents;
			while (!v.empty() && v.back() == '\0') v.remove_suffix(1);
			interpreter = std::string(v);
			if (Error e = walk(interpreter, interpreter, visited); !e.empty()) return e;
		}
		if (auto it = files.find(name); it == files.end() || !it->second) {
			if (!target_root.empty())
				if (Error e = copy_file(source, go::join(target_root, name), 0700); !e.empty()) return e;
			files[name] = true;
		}
		if (!f.needed_error.empty()) return f.needed_error;
		for (const auto &lib : *f.needed) {
			if (lib.find('/') != std::string::npos) return "unsupported ELF dependency " + lib;
			if (musl_self_library(interpreter, lib)) continue;
			std::string found;
			for (std::string base : {"/lib", "/usr/lib"}) {
				std::string candidate = base + "/" + lib;
				struct stat st{};
				if (::stat(go::join(source_root, candidate).c_str(), &st) == 0) {
					found = candidate;
					break;
				}
			}
			if (found.empty()) return "RAM dependency missing: " + lib;
			if (Error e = walk(found, interpreter, visited); !e.empty()) return e;
		}
		return "";
	}
};

} // namespace

Error static_elf(const std::string &name)
{
	Elf f;
	if (Error e = open_elf(name, f); !e.empty()) return e;
	for (const auto &p : f.progs)
		if (p.first == 3) return "standby helper must be statically linked";
	return "";
}

Error stage_elf(const std::string &name, const std::string &source_root, const std::string &target_root,
                std::map<std::string, bool> &files)
{
	Stager s{source_root, target_root, files};
	std::map<std::string, bool> visited;
	return s.walk(name, "", visited);
}

// --- Go texts ------------------------------------------------------------------------------------------------------

std::optional<std::uint64_t> parse_uint(std::string_view s, int base, int bits, Error &err)
{
	auto quoted = [&] {
		std::string q = "\"";
		for (char ch : s) {
			if (ch == '"' || ch == '\\') q += '\\';
			q += ch;
		}
		return q + "\"";
	};
	auto syntax = [&] {
		err = "strconv.ParseUint: parsing " + quoted() + ": invalid syntax";
		return std::nullopt;
	};
	auto range = [&] {
		err = "strconv.ParseUint: parsing " + quoted() + ": value out of range";
		return std::nullopt;
	};
	if (s.empty()) return syntax();
	std::uint64_t max = bits >= 64 ? ~0ull : (1ull << bits) - 1;
	std::uint64_t cutoff = ~0ull / static_cast<std::uint64_t>(base) + 1;
	std::uint64_t n = 0;
	for (char c : s) {
		int d;
		if (c >= '0' && c <= '9') d = c - '0';
		else if (c >= 'a' && c <= 'z') d = c - 'a' + 10;
		else if (c >= 'A' && c <= 'Z') d = c - 'A' + 10;
		else return syntax();
		if (d >= base) return syntax();
		if (n >= cutoff) return range();
		n *= static_cast<std::uint64_t>(base);
		std::uint64_t n1 = n + static_cast<std::uint64_t>(d);
		if (n1 < n || n1 > max) return range();
		n = n1;
	}
	return n;
}

std::optional<std::string> parse_ip4(std::string_view s)
{
	std::string text(s);
	unsigned char b[16];
	if (text.find(':') == std::string::npos) {
		if (::inet_pton(AF_INET, text.c_str(), b) != 1) return std::nullopt;
		return std::string(reinterpret_cast<char *>(b), 4);
	}
	if (::inet_pton(AF_INET6, text.c_str(), b) != 1) return std::nullopt;
	static const unsigned char mapped[12] = {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff};
	if (std::memcmp(b, mapped, 12) != 0) return std::nullopt;
	return std::string(reinterpret_cast<char *>(b + 12), 4);
}

} // namespace nkos::standby
