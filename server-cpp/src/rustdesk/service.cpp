// The RustDesk service, as the Go server's service/rustdesk/service.go: the settings file, the status, the service
// and runlevel commands and the USB composition when RustDesk turns on or off.

#include "rustdesk/internal.hpp"

#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "storage/go.hpp"

#include <cerrno>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::rustdesk {

Paths paths;

using network::JValue;
using Kind = network::JValue::Kind;

namespace {

bool exact_or_fold(const std::string &key, const char *name)
{
	std::string_view n(name);
	if (key.size() != n.size()) return false;
	for (std::size_t i = 0; i < key.size(); i++) {
		char a = key[i], b = n[i];
		if (a >= 'A' && a <= 'Z') a = static_cast<char>(a - 'A' + 'a');
		if (b >= 'A' && b <= 'Z') b = static_cast<char>(b - 'A' + 'a');
		if (a != b) return false;
	}
	return true;
}

// The JSON type of a value in an UnmarshalTypeError, as encoding/json names it.
std::string json_kind(const JValue &v, bool number_literal)
{
	switch (v.kind) {
	case Kind::boolean: return "bool";
	case Kind::string: return "string";
	case Kind::number: return number_literal ? "number " + v.text : "number";
	case Kind::array: return "array";
	case Kind::object: return "object";
	default: return "null";
	}
}

// A struct of a few fields decoded as encoding/json does: every member whose key names a field (exactly, else
// without regard to case) is assigned in document order; the first type error is kept, decoding goes on.
struct Decoder {
	std::string struct_name, error;
	std::string prefix;  // the field path of a nested struct ("features.")

	void type_error(const JValue &v, const std::string &field, const char *go_type, bool number_literal = false)
	{
		if (!error.empty()) return;
		error = "json: cannot unmarshal " + json_kind(v, number_literal) + " into Go struct field " + struct_name + "." +
		        prefix + field + " of type " + go_type;
	}
	void boolean(const JValue &v, const std::string &field, bool &out)
	{
		if (v.kind == Kind::null) return;
		if (v.kind != Kind::boolean) return type_error(v, field, "bool");
		out = v.flag;
	}
	void opt_boolean(const JValue &v, const std::string &field, std::optional<bool> &out)
	{
		if (v.kind == Kind::null) {
			out.reset();
			return;
		}
		if (v.kind != Kind::boolean) {
			// The pointer is allocated before the value is refused.
			if (!out) out = false;
			return type_error(v, field, "bool");
		}
		out = v.flag;
	}
	void string(const JValue &v, const std::string &field, std::string &out)
	{
		if (v.kind == Kind::null) return;
		if (v.kind != Kind::string) return type_error(v, field, "string");
		out = v.text;
	}
	void integer(const JValue &v, const std::string &field, long long &out)
	{
		if (v.kind == Kind::null) return;
		if (v.kind != Kind::number) return type_error(v, field, "int");
		auto n = v.text.find_first_of(".eE") == std::string::npos ? network::atoi(v.text) : std::nullopt;
		if (!n) return type_error(v, field, "int", true);
		out = *n;
	}
};

// The top-level value must be an object (or null, which changes nothing).
std::string top_level_error(const JValue &v, const std::string &go_type)
{
	if (v.kind == Kind::object || v.kind == Kind::null) return {};
	return "json: cannot unmarshal " + json_kind(v, false) + " into Go value of type " + go_type;
}

// The metadata of the installed daemon (/usr/share/nanokvm-rustdesk/upstream.json): the RustDesk version and the
// features. ok is json.Unmarshal's success for a struct of only the fields in mask (each caller in Go declares its
// own struct, so a bad value of another field does not matter).
struct Upstream {
	std::string version;
	bool transport = false, audio = false, audio_settings = false, pointer = false;
};
enum : unsigned { up_version = 1, up_transport = 2, up_audio = 4, up_audio_settings = 8, up_pointer = 16, up_all = 31 };
bool read_upstream(const std::string &data, unsigned mask, Upstream &u)
{
	auto v = network::parse_json_whole(data);
	if (!v || !top_level_error(*v, "struct").empty()) return false;
	if (v->kind == Kind::null) return true;
	Decoder d{"", {}, {}};
	for (const auto &[key, value] : v->members) {
		bool is_version = (mask & up_version) && (key == "rustdesk_version" || exact_or_fold(key, "rustdesk_version"));
		if (is_version) d.string(value, "rustdesk_version", u.version);
		else if (key == "features" || exact_or_fold(key, "features")) {
			if (value.kind == Kind::null) continue;
			if (value.kind != Kind::object) {
				d.type_error(value, "features", "struct");
				continue;
			}
			for (const auto &[k, f] : value.members) {
				if ((mask & up_transport) && exact_or_fold(k, "transport_settings")) d.boolean(f, "transport_settings", u.transport);
				else if ((mask & up_audio) && exact_or_fold(k, "audio")) d.boolean(f, "audio", u.audio);
				else if ((mask & up_audio_settings) && exact_or_fold(k, "audio_settings"))
					d.boolean(f, "audio_settings", u.audio_settings);
				else if ((mask & up_pointer) && exact_or_fold(k, "pointer_settings")) d.boolean(f, "pointer_settings", u.pointer);
			}
		}
	}
	return d.error.empty();
}

bool matches_version(const std::string &s)
{
	// ^[0-9]+\.[0-9]+\.[0-9]+$
	int parts = 0;
	std::size_t i = 0;
	while (true) {
		std::size_t start = i;
		while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		if (i == start) return false;
		parts++;
		if (parts == 3) return i == s.size();
		if (i >= s.size() || s[i] != '.') return false;
		i++;
	}
}

bool matches_password(const std::string &s)
{
	// ^[A-HJ-NP-Z2-9]{10}$
	if (s.size() != 10) return false;
	for (char c : s) {
		bool letter = c >= 'A' && c <= 'Z' && c != 'I' && c != 'O';
		bool digit = c >= '2' && c <= '9';
		if (!letter && !digit) return false;
	}
	return true;
}

bool all_digits(const std::string &s, std::size_t min, std::size_t max)
{
	if (s.size() < min || s.size() > max) return false;
	for (char c : s)
		if (c < '0' || c > '9') return false;
	return true;
}

// ^[a-zA-Z0-9][a-zA-Z0-9.-]*$
bool matches_hostname(const std::string &s)
{
	auto alnum = [](char c) { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'); };
	if (s.empty() || !alnum(s[0])) return false;
	for (char c : s)
		if (!alnum(c) && c != '.' && c != '-') return false;
	return true;
}

bool audio_requested(const Config &c) { return !c.audio_enabled || *c.audio_enabled; }

std::string quote(const std::string &s) { return gojson::quote(s); }

// fmt.Sscanf(p, "%d", &n) for one to five digits.
int scan_int(const std::string &p) { return std::stoi(p); }

std::string stat_error(const std::string &path)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) return {};
	return storage::go::path_error("stat", path, errno);
}

bool lstat_ok(const std::string &path, int &err)
{
	struct stat st{};
	if (::lstat(path.c_str(), &st) == 0) return true;
	err = errno;
	return false;
}

} // namespace

Config default_config()
{
	Config c;
	c.official = true;
	c.codec = "auto";
	c.max_clients = 1;
	c.password_mode = "temporary";
	return c;
}

std::string decode_config(const JValue &v, Config &c)
{
	if (auto e = top_level_error(v, "rustdesk.Config"); !e.empty()) return e;
	Decoder d{"Config", {}, {}};
	for (const auto &[key, value] : v.members) {
		// The field a key names: an exact match first, else one without regard to case.
		static const char *names[] = {"audio_enabled", "local_pointer", "listen_address", "webrtc_enabled", "service_enabled",
		                              "use_official_id_server", "rendezvous_server", "relay_server", "server_key", "password",
		                              "password_mode", "codec", "max_clients"};
		const char *name = nullptr;
		for (const char *n : names)
			if (key == n) name = n;
		if (!name)
			for (const char *n : names)
				if (exact_or_fold(key, n)) {
					name = n;
					break;
				}
		if (!name) continue;
		std::string f = name;
		if (f == "audio_enabled") d.opt_boolean(value, f, c.audio_enabled);
		else if (f == "local_pointer") d.opt_boolean(value, f, c.local_pointer);
		else if (f == "listen_address") d.string(value, f, c.listen);
		else if (f == "webrtc_enabled") d.boolean(value, f, c.webrtc);
		else if (f == "service_enabled") d.boolean(value, f, c.enabled);
		else if (f == "use_official_id_server") d.boolean(value, f, c.official);
		else if (f == "rendezvous_server") d.string(value, f, c.rendezvous);
		else if (f == "relay_server") d.string(value, f, c.relay);
		else if (f == "server_key") d.string(value, f, c.key);
		else if (f == "password") d.string(value, f, c.password);
		else if (f == "password_mode") d.string(value, f, c.password_mode);
		else if (f == "codec") d.string(value, f, c.codec);
		else if (f == "max_clients") d.integer(value, f, c.max_clients);
	}
	return d.error;
}

std::string read_config(Config &c)
{
	c = default_config();
	network::Error err;
	auto data = network::read_file(paths.config_file, &err);
	if (!data) {
		if (err.not_exist()) return {};
		return err.text;
	}
	auto v = network::parse_json_whole(*data);
	// A file that is not JSON: encoding/json's syntax error (its text is not reproduced).
	if (!v) return "invalid character in " + paths.config_file;
	if (auto e = decode_config(*v, c); !e.empty()) return e;
	// Existing installs retain their permanent password until the user changes mode.
	bool has_mode = false;
	if (v->kind == Kind::object)
		for (const auto &[key, value] : v->members)
			if (key == "password_mode") has_mode = true;
	if (!has_mode && !c.password.empty()) c.password_mode = "permanent";
	// An older codec preference never overrides the shared device codec.
	c.codec = "auto";
	return {};
}

std::string config_json(const Config &c, const std::string &indent)
{
	std::vector<std::pair<std::string, std::string>> f;
	if (c.audio_enabled) f.emplace_back("audio_enabled", *c.audio_enabled ? "true" : "false");
	if (c.local_pointer) f.emplace_back("local_pointer", *c.local_pointer ? "true" : "false");
	if (!c.listen.empty()) f.emplace_back("listen_address", quote(c.listen));
	if (c.webrtc) f.emplace_back("webrtc_enabled", "true");
	f.emplace_back("service_enabled", c.enabled ? "true" : "false");
	f.emplace_back("use_official_id_server", c.official ? "true" : "false");
	f.emplace_back("rendezvous_server", quote(c.rendezvous));
	f.emplace_back("relay_server", quote(c.relay));
	f.emplace_back("server_key", quote(c.key));
	if (!c.password.empty()) f.emplace_back("password", quote(c.password));
	f.emplace_back("password_mode", quote(c.password_mode));
	f.emplace_back("codec", quote(c.codec));
	f.emplace_back("max_clients", std::to_string(c.max_clients));
	std::string out = "{";
	for (std::size_t i = 0; i < f.size(); i++) {
		if (i) out += ",";
		if (!indent.empty()) out += "\n" + indent;
		out += quote(f[i].first) + (indent.empty() ? ":" : ": ") + f[i].second;
	}
	if (!indent.empty()) out += "\n";
	return out + "}";
}

std::string write_config(const Config &c)
{
	if (auto e = network::mkdir_all(paths.config_dir, 0700)) return e.text;
	std::string data = config_json(c, "  ") + "\n";
	int fd = -1;
	std::string name;
	if (auto e = storage::go::create_temp(paths.config_dir, ".config-*", fd, name)) return e.text;
	std::string err;
	if (::fchmod(fd, 0600) != 0) err = storage::go::path_error("chmod", name, errno);
	for (std::size_t off = 0; err.empty() && off < data.size();) {
		ssize_t n = ::write(fd, data.data() + off, data.size() - off);
		if (n < 0) {
			if (errno == EINTR) continue;
			err = storage::go::path_error("write", name, errno);
			break;
		}
		off += static_cast<std::size_t>(n);
	}
	if (err.empty() && ::fsync(fd) != 0) err = storage::go::path_error("sync", name, errno);
	bool close_failed = ::close(fd) != 0;
	int close_errno = errno;
	if (err.empty() && close_failed) err = storage::go::path_error("close", name, close_errno);
	if (err.empty()) {
		if (auto e = network::rename(name, paths.config_file)) err = e.text;
	}
	if (!err.empty()) {
		::unlink(name.c_str());
		return err;
	}
	int dir = ::open(paths.config_dir.c_str(), O_RDONLY | O_CLOEXEC);
	if (dir < 0) return storage::go::path_error("open", paths.config_dir, errno);
	std::string out;
	if (::fsync(dir) != 0) out = storage::go::path_error("sync", paths.config_dir, errno);
	::close(dir);
	return out;
}

std::string server_address(const std::string &raw, const std::string &port, std::string &out)
{
	out.clear();
	std::string value = network::trim_space(raw);
	if (value.empty()) return {};
	if (value.size() > 255 || value.find_first_of("/ \t\r\n@?#") != std::string::npos)
		return "server must be a hostname or IP with an optional port";
	if (auto ip = network::parse_ip(network::trim_chars(value, "[]"))) {
		out = network::join_host_port(network::ip_string(*ip), port);
		return {};
	}
	if (value.find(':') == std::string::npos) value = network::join_host_port(value, port);
	std::string host, p;
	if (!network::split_host_port(value, host, p) || host.empty() || !all_digits(p, 1, 5)) return "invalid server address";
	int n = scan_int(p);
	if (n < 1 || n > 65535) return "invalid server port";
	if (!network::parse_ip(host) && !matches_hostname(host)) return "invalid server hostname";
	out = value;
	return {};
}

std::string validate_config(Config &c)
{
	if (!c.codec.empty() && c.codec != "auto" && c.codec != "h264" && c.codec != "h265") return "codec must be auto, h264 or h265";
	c.codec = "auto";
	if (c.max_clients < 1 || c.max_clients > 8) return "client limit must be between 1 and 8";
	if (!c.listen.empty() && !network::parse_ip(c.listen)) return "listen address must be an IP address";
	if (c.password_mode != "temporary" && c.password_mode != "permanent") return "password mode must be temporary or permanent";
	if ((c.password_mode == "permanent" || !c.password.empty()) && (c.password.size() < 8 || c.password.size() > 64))
		return "password must contain 8 to 64 bytes";
	std::string addr;
	if (auto e = server_address(c.rendezvous, "21116", addr); !e.empty()) return e;
	c.rendezvous = addr;
	if (auto e = server_address(c.relay, "21117", addr); !e.empty()) return e;
	c.relay = addr;
	if (!c.official && c.rendezvous.empty()) return "custom ID server is required";
	if (!c.key.empty()) {
		auto key = base64::decode(c.key);
		if (!key || key->size() != 32) return "server key must be a base64-encoded 32-byte public key";
	}
	return {};
}

std::string compact_json(std::string_view text)
{
	static const char hex[] = "0123456789abcdef";
	std::string out;
	bool in_string = false;
	for (std::size_t i = 0; i < text.size(); i++) {
		unsigned char c = static_cast<unsigned char>(text[i]);
		if (!in_string) {
			if (c == ' ' || c == '\t' || c == '\n' || c == '\r') continue;
			if (c == '"') in_string = true;
			out += static_cast<char>(c);
			continue;
		}
		if (c == '\\') {
			out += text[i];
			if (i + 1 < text.size()) out += text[++i];
			continue;
		}
		if (c == '"') in_string = false;
		if (c == '<' || c == '>' || c == '&') {
			out += "\\u00";
			out += hex[c >> 4];
			out += hex[c & 15];
			continue;
		}
		// U+2028 and U+2029 (E2 80 A8/A9).
		if (c == 0xe2 && i + 2 < text.size() && static_cast<unsigned char>(text[i + 1]) == 0x80 &&
		    (static_cast<unsigned char>(text[i + 2]) & 0xfe) == 0xa8) {
			out += static_cast<unsigned char>(text[i + 2]) == 0xa8 ? "\\u2028" : "\\u2029";
			i += 2;
			continue;
		}
		out += static_cast<char>(c);
	}
	return out;
}

CommandResult run_command(const std::string &name, const std::vector<std::string> &args, std::chrono::milliseconds timeout)
{
	std::vector<std::string> argv{name};
	argv.insert(argv.end(), args.begin(), args.end());
	if (name == "rc-service" && args.size() > 1 && (args[1] == "start" || args[1] == "restart"))
		// The daemon must not inherit the server's OOM protection.
		argv = network::unprotected(name, args);
	network::RunOptions options;
	options.timeout = timeout;
	options.combined = true;
	auto r = network::run(argv, options);
	if (!r.ok()) {
		std::string data = r.out.substr(0, std::min<std::size_t>(r.out.size(), 2048));
		return {"", name + " failed: " + network::trim_space(data)};
	}
	return {r.out, ""};
}

namespace {

constexpr std::chrono::milliseconds long_timeout{std::chrono::minutes(3)};
constexpr std::chrono::milliseconds status_timeout{std::chrono::seconds(3)};

} // namespace

bool Service::daemon_running()
{
	// The default openrc-run status succeeds only in the started state, so a service without its started link is
	// stopped and needs no rc-service run.
	int err = 0;
	if (lstat_ok(network::dir_name(paths.started_file), err)) {
		if (!lstat_ok(paths.started_file, err) && err == ENOENT) return false;
	}
	return run("rc-service", {package, "status"}, status_timeout).error.empty();
}

std::pair<std::string, std::string> Service::source()
{
	auto data = network::read_file(paths.source_file);
	if (!data) return {"", ""};
	auto v = network::parse_json_whole(*data);
	if (!v || !top_level_error(*v, "struct").empty()) return {"", ""};
	std::string url, version;
	Decoder d{"", {}, {}};
	if (v->kind == Kind::object)
		for (const auto &[key, value] : v->members) {
			if (exact_or_fold(key, "url")) d.string(value, "url", url);
			else if (exact_or_fold(key, "package_version")) d.string(value, "package_version", version);
		}
	if (!d.error.empty()) return {"", ""};
	auto parsed = storage::go::parse_url(url);
	if (!parsed || parsed->scheme != "https" || parsed->hostname().empty() || parsed->has_user || parsed->path.empty())
		return {version, ""};
	return {version, url};
}

bool Service::wants_webrtc()
{
	auto data = network::read_file(paths.upstream_file);
	Upstream u;
	return data && read_upstream(*data, up_transport, u) && u.transport;
}

std::string Service::status(std::string &error)
{
	Config c;
	if (auto e = read_config(c); !e.empty()) {
		error = e;
		return {};
	}
	bool has_password = !c.password.empty();
	c.password.clear();
	auto [version, source_url] = source();
	Upstream u;
	std::string rustdesk_version;
	bool usb_audio = false;
	if (auto data = network::read_file(paths.upstream_file)) {
		if (read_upstream(*data, up_all, u) && matches_version(u.version)) {
			rustdesk_version = u.version;
			usb_audio = u.audio && bridge && bridge->audio_enabled();
		} else {
			u = Upstream{};
		}
	}
	bool running = daemon_running();
	std::string id;
	if (auto data = network::read_file(paths.config_dir + "/settings-output.json")) {
		// map[string]string: every value a string (null is an empty one), else nothing.
		auto v = network::parse_json_whole(*data);
		if (v && (v->kind == Kind::object || v->kind == Kind::null)) {
			bool good = true;
			std::string found;
			for (const auto &[key, value] : v->members) {
				if (value.kind != Kind::string && value.kind != Kind::null) good = false;
				else if (key == "rustdesk_id") found = value.kind == Kind::string ? value.text : "";
			}
			if (good) id = found;
		}
	}
	std::string temporary, runtime;
	if (running) {
		if (c.password_mode == "temporary") {
			if (auto p = network::read_file(paths.password_file); p && matches_password(*p)) temporary = *p;
		}
		if (auto data = network::read_file(paths.runtime_dir + "/status.json"); data && network::parse_json_whole(*data))
			runtime = compact_json(*data);
	}
	auto b = [](bool x) { return x ? "true" : "false"; };
	std::string out = "{\"supports_transport_settings\":" + std::string(b(u.transport)) + ",\"supports_audio\":" + b(u.audio) +
	                  ",\"supports_audio_settings\":" + b(u.audio_settings) + ",\"supports_pointer_settings\":" + b(u.pointer) +
	                  ",\"usb_audio_enabled\":" + b(usb_audio);
	if (!version.empty()) out += ",\"version\":" + quote(version);
	if (!rustdesk_version.empty()) out += ",\"rustdesk_version\":" + quote(rustdesk_version);
	if (!source_url.empty()) out += ",\"source_url\":" + quote(source_url);
	out += ",\"running\":" + std::string(b(running)) + ",\"config\":" + config_json(c, "") + ",\"has_password\":" + b(has_password);
	if (!temporary.empty()) out += ",\"temporary_password\":" + quote(temporary);
	out += ",\"id\":" + quote(id);
	if (!runtime.empty()) out += ",\"runtime\":" + runtime;
	return out + "}";
}

std::string Service::prepare_usb()
{
	Config c;
	if (auto e = read_config(c); !e.empty()) return e;
	if (!c.enabled) return "RustDesk remote access is disabled";
	return stat_error(paths.binary);
}

std::string Service::configure(Config candidate)
{
	std::lock_guard lock(mu);
	return configure_locked(std::move(candidate));
}

std::string Service::configure_locked(Config candidate)
{
	Config current;
	if (auto e = read_config(current); !e.empty()) return e;
	if (candidate.password_mode.empty()) candidate.password_mode = current.password_mode;
	if (!candidate.audio_enabled) candidate.audio_enabled = current.audio_enabled;
	if (!candidate.local_pointer) candidate.local_pointer = current.local_pointer;
	if (candidate.listen.empty()) candidate.listen = current.listen;
	if (candidate.password_mode == "temporary" || candidate.password.empty()) candidate.password = current.password;
	if (auto e = validate_config(candidate); !e.empty()) return e;
	// WebRTC is not a setting: the daemon accepts it whenever it can.
	candidate.webrtc = wants_webrtc();
	auto supports = [](unsigned field) {
		Upstream u;
		auto data = network::read_file(paths.upstream_file);
		return data && read_upstream(*data, field, u) && (field == up_pointer ? u.pointer : u.audio_settings);
	};
	if (candidate.local_pointer && !supports(up_pointer)) return "update RustDesk before configuring the client pointer";
	if (candidate.audio_enabled && !supports(up_audio_settings)) return "update RustDesk before configuring sound transmission";
	// Only turning RustDesk on or off, or its sound on, changes USB; saving other settings keeps what the user chose
	// in USB settings.
	bool turning_on = candidate.enabled && !current.enabled;
	bool sound_on = candidate.enabled && current.enabled && audio_requested(candidate) && !audio_requested(current);
	bool turning_off = !candidate.enabled && current.enabled;
	std::string err;
	if (turning_on) err = usb.enable ? usb.enable(audio_requested(candidate)) : "USB for RustDesk is unavailable";
	else if (sound_on) err = usb.ensure ? usb.ensure(true) : "USB for RustDesk is unavailable";
	if (!err.empty()) return "prepare USB for RustDesk: " + err;
	if (run("rc-service", {package, "status"}, long_timeout).error.empty()) {
		if (auto r = run("rc-service", {package, "stop"}, long_timeout); !r.error.empty()) return r.error;
	}
	if (auto e = write_config(candidate); !e.empty()) return e;
	if (candidate.enabled) {
		if (auto e = bridge ? bridge->start() : std::string("RustDesk bridge is unavailable"); !e.empty()) return e;
		if (auto r = run("rc-update", {"add", package, "default"}, long_timeout); !r.error.empty()) return r.error;
		return run("rc-service", {package, "start"}, long_timeout).error;
	}
	// rc-update del fails if the disabled service has never been enabled.
	auto shown = run("rc-update", {"show", "default"}, long_timeout);
	if (!shown.error.empty()) return shown.error;
	for (const auto &field : network::fields(shown.output)) {
		if (field == package) {
			err = run("rc-update", {"del", package, "default"}, long_timeout).error;
			break;
		}
	}
	// RustDesk is off whatever happens to USB.
	if (err.empty() && turning_off) {
		std::string restore = usb.restore ? usb.restore() : "USB for RustDesk is unavailable";
		if (!restore.empty()) err = "RustDesk is off; restore USB: " + restore;
	}
	return err;
}

std::string Service::action(const std::string &action)
{
	std::lock_guard lock(mu);
	if (action != "regenerate-password") return "unknown RustDesk action";
	Config c;
	if (auto e = read_config(c); !e.empty()) return e;
	if (c.password_mode != "temporary" || !c.enabled) return "enable temporary password mode first";
	if (!run("rc-service", {package, "status"}, long_timeout).error.empty()) return "start RustDesk before generating a new password";
	return run("rc-service", {package, "restart"}, long_timeout).error;
}

void Service::accept_webrtc()
{
	std::lock_guard lock(mu);
	// A daemon that was never configured is off; Configure sets the option when the user sets RustDesk up.
	if (!stat_error(paths.config_file).empty()) return;
	Config c;
	if (!read_config(c).empty() || c.webrtc == wants_webrtc()) return;
	if (auto e = configure_locked(c); !e.empty()) log::error("RustDesk: accept WebRTC: {}", e);
}

Service &service()
{
	static Service s;
	return s;
}

bool world_selects(const std::string &world, const std::string &name)
{
	for (auto entry : network::fields(world)) {
		if (auto i = entry.find_first_of("=<>~@"); i != std::string::npos) entry.resize(i);
		if (entry == name) return true;
	}
	return false;
}

bool apk_selected(const std::string &name, std::string &error)
{
	network::Error err;
	auto world = network::read_file(paths.world, &err);
	if (!world) {
		error = err.text;
		return false;
	}
	return world_selects(*world, name);
}

std::string switch_off_stale(bool selected)
{
	if (struct stat st{}; ::stat(paths.config_file.c_str(), &st) != 0 && errno == ENOENT) return {};
	Config c;
	if (auto e = read_config(c); !e.empty() || !c.enabled) return e;
	int err = 0;
	bool linked = lstat_ok(paths.runlevel_link, err);
	if (linked && selected) return {};
	if (!linked && err != ENOENT) return storage::go::path_error("lstat", paths.runlevel_link, err);
	c.enabled = false;
	log::info("RustDesk: switched off the setting of the removed add-on");
	return write_config(c);
}

} // namespace nkos::rustdesk
