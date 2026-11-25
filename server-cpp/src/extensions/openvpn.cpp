#include "extensions/openvpn.hpp"

#include "api/multipart.hpp"
#include "common/process.hpp"
#include "extensions/gopath.hpp"
#include "network/util.hpp"
#include "osupdate/godecode.hpp"

#include <cerrno>
#include <csignal>
#include <fcntl.h>
#include <mutex>
#include <set>
#include <sys/stat.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

namespace nkos::extensions::openvpn {

std::function<void(std::chrono::seconds)> restart_sleep = [](std::chrono::seconds d) { std::this_thread::sleep_for(d); };

namespace {

using network::trim_space;

// maxOpenVPNLine: the longest line OpenVPN 2 reads whole (OPTION_LINE_SIZE is 256 with the terminator).
constexpr std::size_t max_line = 255;
constexpr std::size_t max_profile = 256 * 1024;

bool is_space(char32_t r) { return network::is_space_rune(r); }

bool contains_any(std::string_view s, std::string_view chars) { return s.find_first_of(chars) != std::string_view::npos; }

const std::set<std::string> &permitted()
{
	static const std::set<std::string> p = [] {
		std::set<std::string> out;
		for (auto &f : network::fields(
			     "client tls-client dev dev-type proto remote remote-random remote-random-hostname resolv-retry nobind bind "
			     "persist-key persist-tun remote-cert-tls verify-x509-name peer-fingerprint cipher data-ciphers "
			     "data-ciphers-fallback auth auth-nocache auth-retry reneg-sec reneg-bytes reneg-pkts key-direction "
			     "tls-version-min tls-version-max tls-cipher tls-ciphersuites tls-cert-profile tls-timeout verb mute "
			     "mute-replay-warnings connect-retry connect-retry-max connect-timeout server-poll-timeout hand-window "
			     "tran-window ping ping-restart ping-exit keepalive sndbuf rcvbuf tun-mtu link-mtu mssfix fragment "
			     "explicit-exit-notify float fast-io route route-ipv6 route-gateway route-metric route-delay route-nopull "
			     "redirect-gateway redirect-private pull pull-filter dhcp-option block-outside-dns comp-lzo compress "
			     "allow-compression socket-flags tcp-nodelay disable-dco setenv setenv-safe push-peer-info keysize "
			     "ns-cert-type ignore-unknown-option tun-mtu-extra auth-user-pass ca cert key tls-auth tls-crypt tls-crypt-v2 "
			     "crl-verify extra-certs"))
			out.insert(f);
		return out;
	}();
	return p;
}

const std::set<std::string> inline_keys{"ca", "cert", "key", "tls-auth", "tls-crypt", "tls-crypt-v2", "crl-verify", "extra-certs"};

} // namespace

bool words(const std::string &line, std::vector<std::string> &out)
{
	out.clear();
	std::string value;
	char32_t quote = 0;
	bool escaped = false, started = false;
	for (std::size_t i = 0; i < line.size();) {
		auto [c, n] = decode_rune(line, i);
		i += n;
		if (escaped) {
			append_rune(value, c);
			escaped = false;
			started = true;
			continue;
		}
		if (c == '\\') {
			escaped = true;
			started = true;
			continue;
		}
		if (quote != 0) {
			if (c == quote) quote = 0;
			else append_rune(value, c);
			continue;
		}
		if (c == '\'' || c == '"') {
			quote = c;
			started = true;
			continue;
		}
		if (is_space(c)) {
			if (started) {
				out.push_back(value);
				value.clear();
				started = false;
			}
			continue;
		}
		if ((c == '#' || c == ';') && !started) break;
		append_rune(value, c);
		started = true;
	}
	if (escaped || quote != 0) return false;
	if (started) out.push_back(value);
	return true;
}

bool normalize(const std::string &data, const std::map<std::string, std::string> *assets, Parsed &result, std::string &error)
{
	result = Parsed{};
	if (data.size() > max_profile || data.find('\0') != std::string::npos) {
		error = "invalid or oversized OpenVPN profile";
		return false;
	}
	std::string section;
	std::vector<std::string> block, output;
	bool remote = false, trust = false, server_check = false, connection = false;
	std::string named, typed;
	auto line_error = [&](std::size_t n, const std::string &msg) {
		result = Parsed{};
		error = "line " + std::to_string(n) + ": " + msg;
		return false;
	};
	auto lines = network::split(data, "\n");
	for (std::size_t number = 0; number < lines.size(); number++) {
		std::string line = trim_space(lines[number]);
		if (!section.empty()) {
			if (line == "</" + section + ">") {
				output.push_back("<" + section + ">");
				output.push_back(network::join(block, "\n"));
				output.push_back(line);
				if (section == "ca") trust = true;
				section.clear();
				block.clear();
				continue;
			}
			// OpenVPN recognises a closing tag at the start of any 256-byte read chunk, so a tag anywhere in a long
			// line could end the block.
			if (contains_any(line, "<>")) return line_error(number + 1, "invalid credential block");
			block.push_back(line);
			continue;
		}
		if (line.empty() || line[0] == '#' || line[0] == ';') continue;
		if (line == "<connection>") {
			if (connection) return line_error(number + 1, "nested connection blocks are not supported");
			connection = true;
			output.push_back(line);
			continue;
		}
		if (line == "</connection>") {
			if (!connection) return line_error(number + 1, "unexpected connection block end");
			connection = false;
			output.push_back(line);
			continue;
		}
		if (line[0] == '<') {
			std::string name = line.substr(1);
			if (!name.empty() && name.back() == '>') name.pop_back();
			if (!inline_keys.count(name) || line != "<" + name + ">") return line_error(number + 1, "unsupported inline block");
			section = name;
			continue;
		}
		std::vector<std::string> args;
		if (!words(line, args) || args.empty()) return line_error(number + 1, "invalid quoting");
		std::string key = args[0].rfind("--", 0) == 0 ? args[0].substr(2) : args[0];
		args.erase(args.begin());
		if (!permitted().count(key)) return line_error(number + 1, "unsupported client option");
		if (inline_keys.count(key)) {
			if (args.size() < 1 || args.size() > 2 || args[0].empty() || contains_any(args[0], "/\\"))
				return line_error(number + 1, "upload the referenced credential file alongside the .ovpn file");
			const std::string *asset = nullptr;
			if (assets)
				if (auto it = assets->find(args[0]); it != assets->end()) asset = &it->second;
			if (!asset) return line_error(number + 1, "a referenced credential file is missing from the upload");
			if (asset->find('<') != std::string::npos || asset->find('\0') != std::string::npos)
				return line_error(number + 1, "invalid credential file");
			if (args.size() == 2) {
				if (key != "tls-auth" || (args[1] != "0" && args[1] != "1"))
					return line_error(number + 1, "unsupported credential option");
				output.push_back("key-direction " + args[1]);
			}
			output.push_back("<" + key + ">");
			output.push_back(trim_space(*asset));
			output.push_back("</" + key + ">");
			if (key == "ca") trust = true;
			continue;
		}
		if (key == "dev" || key == "dev-type") {
			std::string kind;
			if (args.size() == 1)
				for (const char *k : {"tun", "tap"})
					if ((key == "dev-type" && args[0] == k) || (key == "dev" && args[0].rfind(k, 0) == 0)) kind = k;
			if (kind.empty()) return line_error(number + 1, "use a TUN or TAP profile");
			(key == "dev" ? named : typed) = kind;
			continue;  // The service names the device.
		}
		if (key == "client" || key == "tls-client" || key == "verb" || key == "mute" || key == "auth-retry" ||
		    key == "ignore-unknown-option" || key == "keysize")
			continue;
		if (key == "block-outside-dns") continue;  // Windows-only; DNS is managed by the Linux resolver.
		if (key == "ns-cert-type") {
			if (args.size() != 1 || args[0] != "server") return line_error(number + 1, "unsupported certificate purpose");
			key = "remote-cert-tls";
			server_check = true;
		} else if (key == "remote-cert-tls") {
			if (args.size() != 1 || args[0] != "server") return line_error(number + 1, "a server certificate is required");
			server_check = true;
		} else if (key == "peer-fingerprint") {
			trust = true;
		} else if (key == "auth-user-pass") {
			if (!args.empty()) return line_error(number + 1, "enter the VPN username and password in the interface");
			result.needs_auth = true;
			continue;  // The service supplies the protected credentials file.
		} else if (key == "remote") {
			if (args.size() < 1 || args.size() > 3) return line_error(number + 1, "invalid remote endpoint");
			remote = true;
		} else if (key == "setenv" || key == "setenv-safe") {
			// These two compatibility hints are commonly emitted by OpenVPN Connect.
			if (args.empty() || (args[0] != "CLIENT_CERT" && args[0] != "opt"))
				return line_error(number + 1, "unsupported environment option");
			if (args[0] == "opt" && (args.size() != 2 || args[1] != "block-outside-dns"))
				return line_error(number + 1, "unsupported optional option");
			continue;
		} else if (key == "dhcp-option") {
			if (args.size() < 2 || (args[0] != "DNS" && args[0] != "DNS6" && args[0] != "DOMAIN" && args[0] != "DOMAIN-SEARCH"))
				return line_error(number + 1, "unsupported DNS option");
		}
		std::string encoded = key;
		for (const auto &a : args) encoded += " " + go_quote(a);
		output.push_back(encoded);
	}
	bool needs_auth = result.needs_auth;
	if (!section.empty() || connection) {
		error = "unclosed configuration block";
		return false;
	}
	if (!remote || !trust) {
		error = "profile requires a remote endpoint and a CA certificate or peer fingerprint";
		return false;
	}
	if (!server_check) output.push_back("remote-cert-tls server");
	if (typed == "tap" || (typed.empty() && named == "tap")) {
		result.tap = true;
		output.push_back("dev-type tap");
	}
	// The service supplies a unique device name for status and DNS cleanup.
	result.config = "client\n" + network::join(output, "\n") + "\n";
	result.needs_auth = needs_auth;
	if (result.config.size() > max_profile) {
		result = Parsed{};
		error = "combined profile and certificates exceed 256 KiB";
		return false;
	}
	// OpenVPN 2 reads configuration lines in 256-byte chunks and parses each chunk as its own line, so a longer
	// line here would smuggle options past the allowlist above. Check the exact text OpenVPN will read.
	auto out_lines = network::split(result.config, "\n");
	for (std::size_t n = 0; n < out_lines.size(); n++)
		if (out_lines[n].size() > max_line) {
			result = Parsed{};
			error = "profile line " + std::to_string(n + 1) + " exceeds the " + std::to_string(max_line) + "-byte OpenVPN line limit";
			return false;
		}
	return true;
}

bool dco_link_json(const std::string &data)
{
	using K = godecode::Value::Kind;
	auto doc = godecode::parse_document(data);
	if (!doc || doc->kind != K::array) return false;  // null: no links; anything else: a type error
	// []struct{LinkInfo struct{Kind string `json:"info_kind"`} `json:"linkinfo"`}: any value of another type fails
	// the whole decode; the kind of the first link counts.
	std::string first_kind;
	for (std::size_t i = 0; i < doc->items.size(); i++) {
		const auto &link = doc->items[i];
		if (link.kind == K::null) continue;
		if (link.kind != K::object) return false;
		for (const auto &[k, info] : link.members) {
			if (!godecode::key_matches(k, "linkinfo") || info.kind == K::null) continue;
			if (info.kind != K::object) return false;
			for (const auto &[k2, v] : info.members) {
				if (!godecode::key_matches(k2, "info_kind")) continue;
				if (v.kind != K::null && v.kind != K::string) return false;
				if (v.kind == K::string && i == 0) first_kind = v.text;
			}
		}
	}
	return doc->items.size() == 1 && (first_kind == "ovpn" || first_kind == "ovpn-dco");
}

std::chrono::seconds restart_delay(int n)
{
	std::chrono::seconds delay(5);
	for (int i = 1; i < n && delay < std::chrono::minutes(5); i++) delay *= 2;
	return std::min<std::chrono::seconds>(delay, std::chrono::minutes(5));
}

namespace {

struct Profile {
	std::string id, name;
	bool needs_auth = false, needs_passphrase = false;
};

struct LinkKind {
	int index = 0;
	bool dco = false;
};

struct Service {
	std::mutex mu;
	std::string dir = "/etc/kvm/openvpn", run = "/run/nkos-openvpn";
	std::map<std::string, LinkKind> links;
	std::map<std::string, std::uint64_t> generation;
};

Service &service()
{
	static Service s;
	return s;
}

bool valid_id(std::string_view id)
{
	if (id.size() != 12 || id.substr(0, 2) != "ov") return false;
	for (char c : id.substr(2))
		if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) return false;
	return true;
}

std::string path(const Service &s, const std::string &id, const char *suffix) { return gopath::join(s.dir, id + suffix); }
std::string runtime(const Service &s, const std::string &id, const char *suffix) { return gopath::join(s.run, id + suffix); }
std::string device_name(const std::string &id) { return "nv" + (id.rfind("ov", 0) == 0 ? id.substr(2) : id); }

// profiles.json: nullopt when it cannot be read or holds an invalid entry.
std::optional<std::vector<Profile>> profiles(const Service &s)
{
	std::vector<Profile> ps;
	auto file = gopath::join(s.dir, "profiles.json");
	int err = 0;
	auto data = gopath::read_file(file, err);
	if (!data) {
		if (err == ENOENT) return ps;
		return std::nullopt;
	}
	using K = godecode::Value::Kind;
	auto doc = godecode::parse_document(*data);
	if (!doc) return std::nullopt;
	if (doc->kind != K::null) {
		if (doc->kind != K::array) return std::nullopt;
		bool type_error = false;
		for (const auto &item : doc->items) {
			Profile p;
			if (item.kind == K::object) {
				for (const auto &[k, v] : item.members) {
					bool ok = true;
					if (godecode::key_matches(k, "id")) ok = godecode::get(v, p.id);
					else if (godecode::key_matches(k, "name")) ok = godecode::get(v, p.name);
					else if (godecode::key_matches(k, "needsAuth")) ok = godecode::get(v, p.needs_auth);
					else if (godecode::key_matches(k, "needsPassphrase")) ok = godecode::get(v, p.needs_passphrase);
					if (!ok) type_error = true;
				}
			} else if (item.kind != K::null) {
				type_error = true;
			}
			ps.push_back(p);
		}
		if (type_error) return std::nullopt;
	}
	for (const auto &p : ps)
		if (!valid_id(p.id)) return std::nullopt;
	return ps;
}

std::string profiles_json(const std::vector<Profile> &ps)
{
	std::string out = "[";
	for (std::size_t i = 0; i < ps.size(); i++) {
		const auto &p = ps[i];
		out += std::string(i ? "," : "") + "{\"id\":" + q(p.id) + ",\"name\":" + q(p.name) + ",\"needsAuth\":" + json_bool(p.needs_auth) +
		       ",\"needsPassphrase\":" + json_bool(p.needs_passphrase) + "}";
	}
	return out + "]";
}

std::string save(const Service &s, const std::vector<Profile> &ps)
{
	return write_private(gopath::join(s.dir, "profiles.json"), profiles_json(ps));
}

std::string desired(const Service &s)
{
	auto data = read_file(gopath::join(s.dir, "active"));
	std::string id = trim_space(data ? *data : "");
	return valid_id(id) ? id : "";
}

int pid_of(const Service &s, const std::string &id)
{
	auto data = read_file(runtime(s, id, ".pid"));
	auto pid = network::atoi(trim_space(data ? *data : ""));
	if (!pid || *pid < 2) return 0;
	auto cmd = read_file("/proc/" + std::to_string(*pid) + "/cmdline");
	if (!cmd) return 0;
	auto args = network::split(*cmd, std::string(1, '\0'));
	if (args.size() < 2 || gopath::base(args[0]) != "openvpn") return 0;
	for (std::size_t i = 0; i < args.size(); i++)
		if (args[i] == "--config" && i + 1 < args.size() && args[i + 1] == path(s, id, ".ovpn")) return static_cast<int>(*pid);
	return 0;
}

bool credentials(const Service &s, const Profile &p)
{
	if (p.needs_auth && !stat_ok(path(s, p.id, ".auth"))) return false;
	if (p.needs_passphrase && !stat_ok(path(s, p.id, ".pass"))) return false;
	return true;
}

std::uint64_t next_generation(Service &s, const std::string &id) { return ++s.generation[id]; }

// A command with a time limit and nothing captured (cmd.Run) or its combined output.
Result quick(std::vector<std::string> argv, int seconds, Command::Capture capture, std::optional<std::string> input = {})
{
	Command c;
	c.argv = std::move(argv);
	c.timeout = std::chrono::seconds(seconds);
	c.capture = capture;
	c.input = std::move(input);
	return run(c);
}

std::string link_detail(const std::string &name, bool &ok)
{
	auto r = quick({"ip", "-j", "-d", "link", "show", "dev", name}, 2, Command::Capture::output);
	ok = r.ok();
	return r.out;
}

// removeStaleDCO: the DCO device of an OpenVPN that was killed keeps its addresses and routes.
std::string remove_stale_dco(const std::string &name)
{
	if (!stat_ok("/sys/class/net/" + name)) return "";
	bool ok = false;
	auto detail = link_detail(name, ok);
	if (!ok || !dco_link_json(detail)) return "";
	auto r = quick({"ip", "link", "del", "dev", name}, 3, Command::Capture::combined);
	if (!r.ok()) return "cannot remove the stale DCO device: " + trim_space(r.out);
	return "";
}

// prepareTAP: the TAP device made before OpenVPN opens it, router advertisements and autoconfiguration off.
std::string prepare_tap(const std::string &name)
{
	auto r = quick({"ip", "tuntap", "add", "dev", name, "mode", "tap"}, 3, Command::Capture::combined);
	if (!r.ok()) return "cannot create the TAP device: " + trim_space(r.out);
	for (const char *key : {"accept_ra", "autoconf"}) {
		std::string file = gopath::join("/proc/sys/net/ipv6/conf/" + name, key);
		int fd = ::open(file.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0644);
		if (fd < 0) {
			if (errno == ENOENT) continue;  // a kernel without IPv6
			return "open " + file + ": " + network::errno_text(errno);
		}
		bool written = ::write(fd, "0\n", 2) == 2;
		int e = errno;
		::close(fd);
		if (!written) return "write " + file + ": " + network::errno_text(e);
	}
	return "";
}

// removeTAP: a TAP device prepareTAP made; a TUN device goes with its OpenVPN.
std::string remove_tap(const std::string &name)
{
	auto flags = read_file("/sys/class/net/" + name + "/tun_flags");
	if (!flags) return "";
	auto value = network::parse_uint(trim_space(*flags), 0, 32);
	if (!value || (*value & 0x0002) == 0) return "";  // IFF_TAP
	auto r = quick({"ip", "tuntap", "del", "dev", name, "mode", "tap"}, 3, Command::Capture::combined);
	if (!r.ok()) return "cannot remove the TAP device: " + trim_space(r.out);
	return "";
}

std::string clear_dns(const std::string &id)
{
	// OpenVPN's down script removes the record; one killed leaves it.
	std::string name = device_name(id);
	if (!resolvconf_has(name)) return "";
	return quick({"resolvconf", "-d", name, "-f"}, 3, Command::Capture::none).error;
}

std::string join_errors(std::initializer_list<std::string> errs)
{
	std::string out;
	for (const auto &e : errs)
		if (!e.empty()) out += (out.empty() ? "" : "\n") + e;
	return out;
}

// runtimeLog: the last 256 KiB of the log.
std::string runtime_log(const std::string &file)
{
	int fd = ::open(file.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return "";
	constexpr off_t limit = 256 * 1024;
	struct stat st{};
	if (::fstat(fd, &st) == 0 && st.st_size > limit && ::lseek(fd, st.st_size - limit, SEEK_SET) < 0) {
		::close(fd);
		return "";
	}
	std::string out;
	char buf[16384];
	while (out.size() < static_cast<std::size_t>(limit)) {
		ssize_t n = ::read(fd, buf, std::min(sizeof buf, static_cast<std::size_t>(limit) - out.size()));
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) break;
		out.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	return out;
}

std::uint64_t read_counter(const std::string &file)
{
	auto data = read_file(file);
	if (!data) return 0;
	return parse_uint_value(trim_space(*data));
}

void supervise(const std::string &id, std::uint64_t generation, int attempt, std::chrono::steady_clock::duration ran);

// startAttempt: the caller holds the lock and found no OpenVPN of the profile running.
std::string start_attempt(Service &s, const Profile &p, int attempt, std::uint64_t generation)
{
	if (!credentials(s, p)) return "credentials are required";
	if (auto e = gopath::mkdir_all(s.run, 0700); !e.empty()) return e;
	// Validate and canonicalize persisted profiles again before handing them to a root daemon.
	std::string profile = path(s, p.id, ".ovpn");
	int err = 0;
	auto data = gopath::read_file(profile, err);
	if (!data) return gopath::path_error("open", profile, err);
	Parsed normalized;
	std::string error;
	if (!normalize(*data, nullptr, normalized, error)) return error;
	if (normalized.config != *data)
		if (auto e = write_private(profile, normalized.config); !e.empty()) return e;
	if (!resolvconf_has("nkos.base")) {
		int rerr = 0;
		auto base = gopath::read_file("/etc/resolv.conf", rerr);
		if (!base) return gopath::path_error("open", "/etc/resolv.conf", rerr);
		auto r = quick({"resolvconf", "-a", "nkos.base"}, 5, Command::Capture::none, *base);
		if (!r.ok()) return r.error;
	}
	std::string name = device_name(p.id);
	if (auto e = remove_stale_dco(name); !e.empty()) return e;
	std::string dev_type = "tun";
	if (normalized.tap) {
		dev_type = "tap";
		// A new device for every start: one left by an OpenVPN that did not exit cleanly keeps its addresses and
		// routes, and a pushed default route there would catch the way to the server.
		auto e = remove_tap(name);
		if (e.empty()) e = prepare_tap(name);
		if (!e.empty()) return e;
	}
	std::vector<std::string> args{"--config", profile, "--dev", name, "--dev-type", dev_type, "--log", runtime(s, p.id, ".log"),
	                              "--script-security", "2", "--up", "/etc/openvpn/up.sh", "--down", "/etc/openvpn/down.sh",
	                              "--down-pre", "--setenv", "PEER_DNS", "yes", "--auth-nocache"};
	if (p.needs_auth) args.insert(args.end(), {"--auth-user-pass", path(s, p.id, ".auth")});
	if (p.needs_passphrase) args.insert(args.end(), {"--askpass", path(s, p.id, ".pass")});
	::unlink(runtime(s, p.id, ".log").c_str());
	::unlink(runtime(s, p.id, ".pid").c_str());
	auto program = look_path("openvpn");
	if (!program) return "OpenVPN could not start";
	auto argv = network::unprotected(*program, args);

	// cmd.Start with Setpgid; stdin, stdout and stderr on /dev/null.
	std::vector<char *> cargv;
	for (auto &a : argv) cargv.push_back(a.data());
	cargv.push_back(nullptr);
	int report[2];
	if (::pipe2(report, O_CLOEXEC) != 0) return "OpenVPN could not start";
	int null_fd = ::open("/dev/null", O_RDWR | O_CLOEXEC);
	pid_t pid = ::fork();
	if (pid == 0) {
		::setpgid(0, 0);
		for (int fd = 0; fd < 3; fd++) ::dup2(null_fd, fd);
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int sig : {SIGPIPE, SIGTERM, SIGINT, SIGUSR1, SIGUSR2, SIGCHLD}) ::sigaction(sig, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		::execv(cargv[0], cargv.data());
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	::close(report[1]);
	if (null_fd >= 0) ::close(null_fd);
	int exec_errno = 0;
	bool started = pid > 0 && ::read(report[0], &exec_errno, sizeof exec_errno) != sizeof exec_errno;
	::close(report[0]);
	if (!started) {
		if (pid > 0) ::waitpid(pid, nullptr, 0);
		return "OpenVPN could not start";
	}
	::setpgid(pid, pid);
	auto began = std::chrono::steady_clock::now();
	if (auto e = write_private(runtime(s, p.id, ".pid"), std::to_string(pid) + "\n"); !e.empty()) {
		::kill(pid, SIGKILL);
		::waitpid(pid, nullptr, 0);
		return e;
	}
	// Only a fully registered child takes over the retry chain.
	std::thread([pid, id = p.id, generation, attempt, began] {
		int status = 0;
		while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
		}
		supervise(id, generation, attempt, std::chrono::steady_clock::now() - began);
	}).detach();
	return "";
}

std::string start_profile(Service &s, const Profile &p) { return start_attempt(s, p, 0, next_generation(s, p.id)); }

// restartOnce: one restart attempt; whether to try again.
bool restart_once(Service &s, const std::string &id, std::uint64_t generation, int attempt)
{
	std::lock_guard lock(s.mu);
	// A manual start, stop or delete since this chain began owns the profile now.
	if (s.generation[id] != generation || desired(s) != id || pid_of(s, id) != 0) return false;
	if (runtime_log(runtime(s, id, ".log")).find("AUTH_FAILED") != std::string::npos) return false;
	auto ps = profiles(s);
	for (const auto &p : ps ? *ps : std::vector<Profile>{})
		if (p.id == id) {
			if (!credentials(s, p)) return false;
			return !start_attempt(s, p, attempt, generation).empty();
		}
	return false;
}

// supervise: restarts the profile after its OpenVPN exits by itself, for as long as it is the enabled profile.
void supervise(const std::string &id, std::uint64_t generation, int attempt, std::chrono::steady_clock::duration ran)
{
	if (ran > std::chrono::minutes(5)) attempt = 0;
	while (true) {
		attempt++;
		restart_sleep(restart_delay(attempt));
		if (!restart_once(service(), id, generation, attempt)) return;
	}
}

std::string stop(Service &s, const std::string &id)
{
	// No retry chain may restart what is being stopped.
	next_generation(s, id);
	auto finish = [&](std::string result) {
		std::string dns = clear_dns(id);
		result = join_errors({result, dns});
		if (pid_of(s, id) == 0) result = join_errors({result, remove_stale_dco(device_name(id)), remove_tap(device_name(id))});
		return result;
	};
	int pid = pid_of(s, id);
	if (pid == 0) return finish("");
	if (::kill(pid, SIGTERM) != 0 && errno != ESRCH) return finish(network::errno_text(errno));
	for (int n = 0; n < 50; n++) {
		if (pid_of(s, id) == 0) return finish("");
		std::this_thread::sleep_for(std::chrono::milliseconds(100));
	}
	// Check ownership again before escalation; never signal a reused PID.
	if (pid_of(s, id) == pid) ::kill(pid, SIGKILL);
	for (int n = 0; n < 10; n++) {
		if (pid_of(s, id) == 0) return finish("");
		std::this_thread::sleep_for(std::chrono::milliseconds(100));
	}
	return finish("OpenVPN has not stopped");
}

std::size_t last_index(const std::string &value, const std::string &pattern)
{
	auto i = value.rfind(pattern);
	return i;
}

struct Live {
	std::string state, address;
	std::uint64_t received = 0, sent = 0;
	bool dco = false;
};

Live live_status(Service &s, const std::string &id)
{
	Live l;
	std::string name = device_name(id);
	std::string log = runtime_log(runtime(s, id, ".log"));
	auto as_int = [](std::size_t i) { return i == std::string::npos ? -1L : static_cast<long>(i); };
	long initialized = as_int(last_index(log, "Initialization Sequence Completed"));
	long disconnected = -1;
	for (const char *pattern : {"SIGUSR1", "Restart pause", "TCP/UDP: Closing socket", "AUTH_FAILED"})
		disconnected = std::max(disconnected, as_int(last_index(log, pattern)));
	if (stat_ok("/sys/class/net/" + name) && initialized >= 0 && initialized > disconnected) l.state = "connected";
	else if (initialized >= 0) l.state = "reconnecting";
	else l.state = "connecting";
	std::string base = "/sys/class/net/" + name + "/statistics";
	l.received = read_counter(base + "/rx_bytes");
	l.sent = read_counter(base + "/tx_bytes");
	if (auto iface = network::interface_by_name(name)) {
		for (const auto &a : iface->addrs)
			if (a.v4) {
				l.address = network::ip_string(a.ip);
				break;
			}
		// The link kind is fixed for the life of an interface: ip runs once per tunnel interface.
		auto known = s.links.find(iface->name);
		if (known != s.links.end() && known->second.index == iface->index) {
			l.dco = known->second.dco;
		} else {
			bool ok = false;
			auto detail = link_detail(iface->name, ok);
			if (ok) {
				l.dco = dco_link_json(detail);
				s.links[iface->name] = LinkKind{iface->index, l.dco};
			}
		}
	}
	return l;
}

} // namespace

Reply get_status()
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	auto ps = profiles(s);
	if (!ps) return fail(-1, "cannot read OpenVPN profiles");
	std::string wanted = desired(s);
	bool available = look_path("openvpn").has_value();
	std::string list = "[";
	for (std::size_t i = 0; i < ps->size(); i++) {
		const auto &p = (*ps)[i];
		Live l{"off", "", 0, 0, false};
		bool enabled = wanted == p.id;
		std::string error;
		if (pid_of(s, p.id) > 0) {
			l = live_status(s, p.id);
		} else if (enabled) {
			l.state = "error";
			error = "OpenVPN stopped; check the profile and credentials";
		}
		list += std::string(i ? "," : "") + "{\"id\":" + q(p.id) + ",\"name\":" + q(p.name) + ",\"needsAuth\":" + json_bool(p.needs_auth) +
		        ",\"needsPassphrase\":" + json_bool(p.needs_passphrase) + ",\"state\":" + q(l.state) + ",\"enabled\":" + json_bool(enabled) +
		        ",\"credentialsSaved\":" + json_bool(credentials(s, p)) + ",\"address\":" + q(l.address) +
		        ",\"received\":" + std::to_string(l.received) + ",\"sent\":" + std::to_string(l.sent) + ",\"dco\":" + json_bool(l.dco) +
		        (error.empty() ? "" : ",\"error\":" + q(error)) + "}";
	}
	return ok("{\"available\":" + json_bool(available) + ",\"profiles\":" + list + "]}");
}

Reply import(const std::string &content_type, const std::string &body)
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	std::optional<std::vector<api::Part>> form;
	if (body.size() <= 5 * 1024 * 1024) form = api::multipart(content_type, body);
	if (!form) return fail(-1, "upload .ovpn profiles and their referenced certificate/key files");
	std::vector<const api::Part *> files;
	for (const auto &part : *form)
		if (part.name == "files" && part.filename) files.push_back(&part);
	if (files.empty() || files.size() > 64) return fail(-1, "upload 1 to 64 files");
	std::map<std::string, std::string> assets;
	std::vector<std::string> names;
	for (const auto *f : files) {
		std::string name = gopath::base(*f->filename);
		if (name.size() > 128) return fail(-1, "filename too long");
		if (assets.count(name)) return fail(-1, "duplicate filename in upload");
		if (f->data.size() > 256 * 1024) return fail(-1, "maximum file size is 256 KiB");
		assets[name] = std::string(f->data);
		std::string lower = name;
		for (auto &c : lower) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
		if (lower.size() >= 5 && lower.compare(lower.size() - 5, 5, ".ovpn") == 0) names.push_back(name);
	}
	auto ps = profiles(s);
	if (!ps || names.empty() || ps->size() + names.size() > 16) return fail(-1, "maximum 16 OpenVPN profiles");
	struct Pending {
		Profile p;
		std::string config;
	};
	std::vector<Pending> batch;
	for (const auto &name : names) {
		Parsed cfg;
		std::string error;
		if (!normalize(assets[name], &assets, cfg, error)) return fail(-1, error);
		unsigned char random[5];
		if (!random_bytes(random, sizeof random)) return fail(-1, "cannot create profile");
		Profile p;
		p.id = "ov" + hex_encode(std::string_view(reinterpret_cast<char *>(random), sizeof random));
		p.name = name.substr(0, name.size() - gopath::ext(name).size());
		p.needs_auth = cfg.needs_auth;
		p.needs_passphrase = cfg.config.find("ENCRYPTED PRIVATE KEY") != std::string::npos ||
		                     cfg.config.find("Proc-Type: 4,ENCRYPTED") != std::string::npos;
		batch.push_back({p, cfg.config});
	}
	auto rollback = [&] {
		for (const auto &p : batch) ::unlink(path(s, p.p.id, ".ovpn").c_str());
	};
	for (const auto &p : batch) {
		if (!write_private(path(s, p.p.id, ".ovpn"), p.config).empty()) {
			rollback();
			return fail(-1, "cannot save profile");
		}
		ps->push_back(p.p);
	}
	if (!save(s, *ps).empty()) {
		rollback();
		return fail(-1, "cannot save profile index");
	}
	return ok();
}

Reply change(const std::string &body)
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	// MaxBytesReader of 16 KiB: the decoder sees at most that much.
	network::RequestData data;
	data.method = "POST";
	data.body = body.size() > 16 * 1024 ? body.substr(0, 16 * 1024) : body;
	auto b = network::Binder::bind(data, true);
	std::string id, action, username, password, passphrase;
	b.str("id", "ID", id);
	b.str("action", "Action", action);
	b.str("username", "Username", username);
	b.str("password", "Password", password);
	b.str("passphrase", "Passphrase", passphrase);
	if (!b.ok() || !valid_id(id)) return fail(-1, "invalid profile");
	auto ps = profiles(s);
	if (!ps) return fail(-1, "cannot read profiles");
	int idx = -1;
	for (std::size_t i = 0; i < ps->size(); i++)
		if ((*ps)[i].id == id) idx = static_cast<int>(i);
	if (idx < 0) return fail(-1, "profile not found");
	Profile p = (*ps)[static_cast<std::size_t>(idx)];
	std::string e;
	if (action == "credentials") {
		for (const auto *v : {&username, &password, &passphrase})
			if (v->size() > 4096 || v->find_first_of(std::string_view("\r\n\0", 3)) != std::string::npos)
				return fail(-1, "invalid credentials");
		if (p.needs_passphrase && passphrase.empty()) return fail(-1, "private-key passphrase is required");
		if (p.needs_auth) {
			if (username.empty() || password.empty()) return fail(-1, "username and password are required");
			e = write_private(path(s, p.id, ".auth"), username + "\n" + password + "\n");
		}
		if (e.empty() && p.needs_passphrase) e = write_private(path(s, p.id, ".pass"), passphrase + "\n");
	} else if (action == "up") {
		for (const auto &other : *ps)
			if (other.id != p.id && (pid_of(s, other.id) > 0 || desired(s) == other.id))
				return fail(-1, "disable the active OpenVPN profile before switching");
		if (pid_of(s, p.id) == 0) e = start_profile(s, p);
		if (e.empty()) {
			e = write_private(gopath::join(s.dir, "active"), p.id + "\n");
			if (!e.empty()) (void)stop(s, p.id);
		}
	} else if (action == "down" || action == "delete") {
		if (desired(s) == p.id) {
			std::string active = gopath::join(s.dir, "active");
			if (int err = remove_file(active)) e = gopath::path_error("remove", active, err);
		}
		if (e.empty()) e = stop(s, p.id);
		if (e.empty() && action == "delete") {
			auto next = *ps;
			next.erase(next.begin() + idx);
			e = save(s, next);
			if (e.empty())
				for (const char *suffix : {".ovpn", ".auth", ".pass"}) (void)remove_file(path(s, p.id, suffix));
		}
	} else {
		return fail(-1, "invalid action");
	}
	if (!e.empty()) return fail(-1, e == "credentials are required" ? e : "OpenVPN operation failed");
	return ok();
}

void restore()
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	std::string id = desired(s);
	if (id.empty()) return;
	auto ps = profiles(s);
	for (const auto &p : ps ? *ps : std::vector<Profile>{})
		if (p.id == id && pid_of(s, id) == 0) {
			// Right after a power cut resolvconf can fail before OpenVPN runs: retry as for a tunnel that exited.
			auto generation = next_generation(s, id);
			if (!start_attempt(s, p, 0, generation).empty())
				std::thread([id, generation] { supervise(id, generation, 0, {}); }).detach();
		}
}

void start()
{
	std::thread(restore).detach();
}

} // namespace nkos::extensions::openvpn
