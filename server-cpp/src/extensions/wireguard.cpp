#include "extensions/wireguard.hpp"

#include "api/multipart.hpp"
#include "common/base64.hpp"
#include "common/log.hpp"
#include "extensions/gopath.hpp"
#include "network/util.hpp"
#include "osupdate/apkpkg.hpp"
#include "osupdate/godecode.hpp"

#include <algorithm>
#include <cctype>
#include <ctime>
#include <map>
#include <mutex>
#include <set>
#include <sys/time.h>
#include <thread>

namespace nkos::extensions::wireguard {

std::function<std::string(const std::string &)> keep_package = [](const std::string &name) { return apkpkg::keep(name); };

namespace {

using network::trim_space;

bool valid_key(const std::string &s)
{
	auto b = base64::decode(s);
	return b && b->size() == 32;
}

bool valid_number(const std::string &s, std::uint64_t min, std::uint64_t max)
{
	if (s.empty() || s.size() > 20) return false;
	std::uint64_t n = 0;
	for (char c : s) {
		if (c < '0' || c > '9') return false;
		n = n * 10 + static_cast<std::uint64_t>(c - '0');
		if (n > 4294967295ull) return false;
	}
	return n >= min && n <= max;
}

bool valid_prefixes(const std::string &s)
{
	for (const auto &p : network::split(s, ","))
		if (!network::parse_prefix(trim_space(p))) return false;
	return true;
}

bool valid_host(const std::string &s)
{
	if (network::parse_addr(s)) return true;
	if (s.empty() || s.size() > 253) return false;
	std::string name = !s.empty() && s.back() == '.' ? s.substr(0, s.size() - 1) : s;
	for (const auto &label : network::split(name, ".")) {
		if (label.empty() || label.size() > 63 || label.front() == '-' || label.back() == '-') return false;
		for (char c : label)
			if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-')) return false;
	}
	return true;
}

} // namespace

bool validate(const std::string &data, std::string &out, std::string &error)
{
	if (data.size() > 64 * 1024 || data.find('\0') != std::string::npos) {
		error = "configuration is too large or contains invalid characters";
		return false;
	}
	std::string section;
	int peers = 0;
	bool seen_interface = false, private_key = false, address = false, peer_key = false, allowed = false;
	std::set<std::string> seen;
	std::vector<std::string> lines;
	auto fail_with = [&](std::string text) {
		error = std::move(text);
		return false;
	};
	for (const auto &raw : network::split(data, "\n")) {
		std::string line = trim_space(raw.substr(0, raw.find('#')));
		if (line.empty()) continue;
		if (line[0] == '[') {
			if (section == "Peer" && (!peer_key || !allowed)) return fail_with("each peer requires PublicKey and AllowedIPs");
			if (line == "[Interface]") {
				if (seen_interface || peers != 0) return fail_with("exactly one Interface must precede peers");
				seen_interface = true;
				section = "Interface";
			} else if (line == "[Peer]") {
				if (!seen_interface) return fail_with("Interface must precede peers");
				peers++;
				section = "Peer";
				peer_key = false;
				allowed = false;
			} else {
				return fail_with("unsupported configuration section");
			}
			seen.clear();
			lines.push_back(line);
			if (section == "Interface") lines.push_back("Table = off");
			continue;
		}
		auto eq = line.find('=');
		if (eq == std::string::npos || section.empty()) return fail_with("invalid configuration line");
		std::string key = trim_space(line.substr(0, eq)), value = trim_space(line.substr(eq + 1));
		if (key.size() > 32) return fail_with("invalid field name");
		if (seen.count(key) && key != "Address" && key != "DNS" && key != "AllowedIPs")
			return fail_with("invalid or unsupported field: " + key);
		seen.insert(key);
		bool valid = false;
		if (section == "Interface") {
			if (key == "PrivateKey") {
				valid = valid_key(value);
				private_key = valid;
			} else if (key == "Address") {
				valid = valid_prefixes(value);
				address = address || valid;
			} else if (key == "ListenPort") {
				valid = valid_number(value, 0, 65535);
			} else if (key == "MTU") {
				valid = valid_number(value, 1280, 9000);
			} else if (key == "DNS") {
				valid = true;
				for (const auto &v : network::split(value, ","))
					if (!network::parse_addr(trim_space(v))) valid = false;
			} else if (key == "Table") {
				valid = value == "auto" || value == "off" || valid_number(value, 1, 4294967295ull);
			}
		} else {
			if (key == "PublicKey") {
				valid = valid_key(value);
				peer_key = valid;
			} else if (key == "PresharedKey") {
				valid = valid_key(value);
			} else if (key == "AllowedIPs") {
				valid = valid_prefixes(value);
				allowed = allowed || valid;
			} else if (key == "PersistentKeepalive") {
				valid = valid_number(value, 0, 65535);
			} else if (key == "Endpoint") {
				std::string host, port;
				valid = network::split_host_port(value, host, port) && valid_number(port, 1, 65535) && valid_host(host);
			}
		}
		if (!valid) return fail_with("invalid or unsupported field: " + key);
		// Always keep the routing policy above, including for imported Table=auto or numeric tables.
		if (section == "Interface" && key == "Table") continue;
		lines.push_back(key + " = " + value);
	}
	if (!private_key || !address || peers == 0 || !peer_key || !allowed)
		return fail_with("Interface requires PrivateKey and Address; each Peer requires PublicKey and AllowedIPs");
	out = network::join(lines, "\n") + "\n";
	return true;
}

bool profile_config(const std::string &data, bool route_allowed_ips, std::string &out, std::string &error)
{
	if (!validate(data, out, error)) return false;
	if (route_allowed_ips)
		if (auto i = out.find("Table = off\n"); i != std::string::npos) out.replace(i, 12, "Table = auto\n");
	return true;
}

Dump parse_dump(const std::string &text)
{
	Dump d;
	for (const auto &line : network::split(text, "\n")) {
		auto f = network::split(line, "\t");
		if (f.size() != 8) continue;
		d.last_handshake = std::max<std::int64_t>(d.last_handshake, parse_int_value(f[4]));
		std::uint64_t rx = parse_uint_value(f[5]), tx = parse_uint_value(f[6]);
		d.received += rx;
		d.sent += tx;
	}
	return d;
}

std::chrono::seconds restore_delay(int attempt)
{
	std::chrono::seconds delay(15);
	for (int i = 1; i < attempt && delay < std::chrono::minutes(5); i++) delay *= 2;
	return std::min<std::chrono::seconds>(delay, std::chrono::minutes(5));
}

namespace {

constexpr const char *profile_dir = "/etc/kvm/wireguard";

struct Profile {
	std::string id, name;
	bool route_allowed_ips = false;
};

struct Service {
	std::mutex mu;
	std::string dir = profile_dir;
	std::map<std::string, std::string> failures;
};

Service &service()
{
	static Service s;
	return s;
}

bool valid_id(std::string_view id)
{
	if (id.size() != 12 || id.substr(0, 2) != "nk") return false;
	for (char c : id.substr(2))
		if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) return false;
	return true;
}

std::optional<std::vector<Profile>> profiles(const Service &s)
{
	std::vector<Profile> ps;
	int err = 0;
	auto data = gopath::read_file(gopath::join(s.dir, "profiles.json"), err);
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
					else if (godecode::key_matches(k, "routeAllowedIPs")) ok = godecode::get(v, p.route_allowed_ips);
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

std::string profile_json(const Profile &p)
{
	return "\"id\":" + q(p.id) + ",\"name\":" + q(p.name) + ",\"routeAllowedIPs\":" + json_bool(p.route_allowed_ips);
}

std::string save(const Service &s, const std::vector<Profile> &ps)
{
	std::string out = "[";
	for (std::size_t i = 0; i < ps.size(); i++) out += std::string(i ? "," : "") + "{" + profile_json(ps[i]) + "}";
	return write_private(gopath::join(s.dir, "profiles.json"), out + "]");
}

std::string config(const Service &s, const std::string &id) { return gopath::join(s.dir, id + ".conf"); }

std::string desired(const Service &s)
{
	auto b = read_file(gopath::join(s.dir, "active"));
	std::string id = trim_space(b ? *b : "");
	return valid_id(id) ? id : "";
}

bool exists_interface(const std::string &id) { return stat_ok("/sys/class/net/" + id); }

bool installed() { return look_path("wg-quick").has_value(); }

// command: the tool without the server's OOM protection, in a process group of its own, stdout only (stderr may
// hold endpoint details or key validation errors).
Result command(int seconds, std::optional<std::string> input, const std::string &name, std::vector<std::string> args)
{
	Command c;
	c.argv = network::unprotected(name, args);
	c.timeout = std::chrono::seconds(seconds);
	c.input = std::move(input);
	c.extra_env = {"WG_ENDPOINT_RESOLUTION_RETRIES=2"};
	c.group = true;
	c.wait_delay = std::chrono::seconds(1);
	return run(c);
}

std::string quick(Service &s, const std::string &action, const std::string &id)
{
	auto data = read_file(config(s, id));
	if (!data) return "cannot read configuration";
	auto ps = profiles(s);
	if (!ps) return "cannot read WireGuard profiles";
	bool route = false;
	for (const auto &p : *ps)
		if (p.id == id) {
			route = p.route_allowed_ips;
			break;
		}
	std::string normalized, error;
	if (!profile_config(*data, route, normalized, error)) return error;
	if (action == "up") {
		// Migrate saved profiles before activation too; keep the original Table on down so wg-quick can remove
		// rules from a previously active profile.
		if (normalized != *data) {
			if (!write_private(config(s, id), normalized).empty()) return "cannot save WireGuard routing policy";
			*data = normalized;
		}
		for (const auto &line : network::split(*data, "\n"))
			if ((line.rfind("Address = ", 0) == 0 || (route && line.rfind("AllowedIPs = ", 0) == 0)) &&
			    line.find(':') != std::string::npos) {
				auto disabled = read_file("/proc/sys/net/ipv6/conf/default/disable_ipv6");
				if (trim_space(disabled ? *disabled : "") == "1")
					return "WireGuard profile uses IPv6; enable IPv6 in Network settings first";
			}
		// Seed the existing LAN resolver on first use.
		if (data->find("DNS = ") != std::string::npos && !resolvconf_has("nkos.base")) {
			auto base = read_file("/etc/resolv.conf");
			if (!base) return "WireGuard cannot read the existing DNS configuration";
			if (!command(5, *base, "resolvconf", {"-a", "nkos.base"}).ok())
				return "WireGuard cannot preserve the existing DNS configuration";
		}
	}
	if (!command(25, std::nullopt, "wg-quick", {action, config(s, id)}).ok())
		return "WireGuard " + action + " failed; check the configuration, DNS and kernel support";
	return "";
}

} // namespace

void restore_with(const std::function<void(std::chrono::seconds)> &sleep)
{
	Service &s = service();
	for (int attempt = 0;; attempt++) {
		if (attempt > 0) sleep(restore_delay(attempt));
		std::lock_guard lock(s.mu);
		std::string id = desired(s);
		if (id.empty() || exists_interface(id)) return;
		auto e = quick(s, "up", id);
		if (e.empty()) {
			s.failures.erase(id);
			return;
		}
		s.failures[id] = e;
	}
}

void keep_tools(const std::function<void(std::chrono::seconds)> &sleep)
{
	Service &s = service();
	for (std::chrono::seconds delay = std::chrono::minutes(1);; delay = std::min<std::chrono::seconds>(2 * delay, std::chrono::minutes(30))) {
		std::optional<std::vector<Profile>> ps;
		{
			std::lock_guard lock(s.mu);
			ps = profiles(s);
		}
		if (!ps || ps->empty() || !installed()) return;
		auto err = keep_package("wireguard-tools");
		if (err.empty()) return;
		log::warn("WireGuard: cannot select wireguard-tools in the APK world, retrying in {}: {}", go_duration(delay.count()), err);
		sleep(delay);
	}
}

void start()
{
	auto sleep = [](std::chrono::seconds d) { std::this_thread::sleep_for(d); };
	std::thread([sleep] { restore_with(sleep); }).detach();
	std::thread([sleep] { keep_tools(sleep); }).detach();
}

Reply get_status()
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	auto ps = profiles(s);
	if (!ps) return fail(-1, "cannot read WireGuard profiles");
	std::string want = desired(s);
	bool available = look_path("wg").has_value() && look_path("wg-quick").has_value();
	std::string list = "[";
	for (std::size_t i = 0; i < ps->size(); i++) {
		const auto &p = (*ps)[i];
		std::string state = "off", address, error;
		if (auto it = s.failures.find(p.id); it != s.failures.end()) error = it->second;
		int mtu = 0;
		Dump d;
		auto data = read_file(config(s, p.id));
		for (const auto &line : network::split(data ? *data : "", "\n"))
			if (line.rfind("Address = ", 0) == 0) {
				if (!address.empty()) address += ", ";
				address += line.substr(10);
			}
		if (exists_interface(p.id)) {
			state = "waiting";
			if (network::interface_by_name(p.id))
				if (auto m = read_file("/sys/class/net/" + p.id + "/mtu"))
					if (auto v = network::atoi(trim_space(*m))) mtu = static_cast<int>(*v);
			// One wg run for handshakes and transfer: status is polled every 5 s.
			auto r = command(2, std::nullopt, "wg", {"show", p.id, "dump"});
			if (!r.ok()) {
				state = "error";
				error = "cannot read tunnel status";
			} else {
				d = parse_dump(r.out);
				if (d.last_handshake > 0) {
					state = "idle";
					if (static_cast<std::int64_t>(std::time(nullptr)) - d.last_handshake < 180) state = "connected";
				}
			}
		} else if (!error.empty()) {
			state = "error";
		}
		list += std::string(i ? "," : "") + "{" + profile_json(p) + ",\"state\":" + q(state) + ",\"enabled\":" + json_bool(want == p.id) +
		        ",\"address\":" + q(address) + (mtu ? ",\"mtu\":" + std::to_string(mtu) : "") +
		        ",\"lastHandshake\":" + std::to_string(d.last_handshake) + ",\"received\":" + std::to_string(d.received) +
		        ",\"sent\":" + std::to_string(d.sent) + (error.empty() ? "" : ",\"error\":" + q(error)) + "}";
	}
	timeval tv{};
	::gettimeofday(&tv, nullptr);
	std::int64_t now = static_cast<std::int64_t>(tv.tv_sec) * 1000 + tv.tv_usec / 1000;
	return ok("{\"available\":" + json_bool(available) + ",\"now\":" + std::to_string(now) + ",\"profiles\":" + list + "]}");
}

Reply import(const std::string &content_type, const std::string &body)
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	std::optional<std::vector<api::Part>> form;
	if (body.size() <= 1024 * 1024) form = api::multipart(content_type, body);
	if (!form) return fail(-1, "upload .conf files (maximum 64 KiB each)");
	std::vector<const api::Part *> files;
	for (const auto &part : *form)
		if (part.name == "files" && part.filename) files.push_back(&part);
	auto ps = profiles(s);
	if (!ps || files.empty() || ps->size() + files.size() > 16) return fail(-1, "upload between 1 and 16 profiles in total");
	struct Pending {
		Profile p;
		std::string data;
	};
	std::vector<Pending> batch;
	for (const auto *f : files) {
		std::string name = gopath::base(*f->filename);
		std::string lower = name;
		for (auto &c : lower) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
		if (lower.size() < 5 || lower.compare(lower.size() - 5, 5, ".conf") != 0 || name.size() > 128)
			return fail(-1, "use .conf filenames up to 128 bytes");
		std::string data(f->data.substr(0, 64 * 1024 + 1));
		std::string cfg, error;
		if (!validate(data, cfg, error)) return fail(-1, error);
		unsigned char random[5];
		if (!random_bytes(random, sizeof random)) return fail(-1, "cannot create profile ID");
		Profile p;
		p.id = "nk" + hex_encode(std::string_view(reinterpret_cast<char *>(random), sizeof random));
		p.name = name.substr(0, name.size() - gopath::ext(name).size());
		batch.push_back({p, cfg});
	}
	auto rollback = [&] {
		for (const auto &p : batch) (void)remove_file(config(s, p.p.id));
	};
	for (const auto &p : batch) {
		if (!write_private(config(s, p.p.id), p.data).empty()) {
			rollback();
			return fail(-1, "cannot save profile");
		}
		ps->push_back(p.p);
	}
	if (!save(s, *ps).empty()) {
		rollback();
		return fail(-1, "cannot save profile index");
	}
	std::thread([] { keep_tools([](std::chrono::seconds d) { std::this_thread::sleep_for(d); }); }).detach();
	return ok();
}

Reply change(const std::string &body)
{
	Service &s = service();
	std::unique_lock lock(s.mu, std::try_to_lock);
	if (!lock.owns_lock()) return fail(-1, "another VPN operation is in progress");
	network::RequestData data;
	data.method = "POST";
	data.body = body;
	auto b = network::Binder::bind(data, true);
	std::string id, action, name;
	bool route = false;
	b.str("id", "ID", id);
	b.str("action", "Action", action);
	bool has_name = b.str("name", "Name", name);
	bool has_route = b.boolean("routeAllowedIPs", "RouteAllowedIPs", route);
	if (!b.ok() || !valid_id(id)) return fail(-1, "invalid profile");
	auto ps = profiles(s);
	if (!ps) return fail(-1, "cannot read profiles");
	int idx = -1;
	for (std::size_t i = 0; i < ps->size(); i++)
		if ((*ps)[i].id == id) idx = static_cast<int>(i);
	if (idx < 0) return fail(-1, "profile not found");
	auto &profile = (*ps)[static_cast<std::size_t>(idx)];
	std::string e;
	if (action == "rename") {
		if (!has_name) return fail(-1, "profile name is required");
		std::string trimmed = trim_space(name);
		bool control = false;
		std::size_t runes = 0;
		for (std::size_t i = 0; i < trimmed.size(); runes++) {
			auto [r, n] = decode_rune(trimmed, i);
			if (r < 0x20 || (r >= 0x7f && r <= 0x9f)) control = true;
			i += n;
		}
		if (trimmed.empty() || runes > 128 || control)
			return fail(-1, "use a profile name of 1–128 characters without control characters");
		// A display name is metadata only: the interface ID, config file, restore marker and tunnel state stay.
		profile.name = trimmed;
		if (!save(s, *ps).empty()) return fail(-1, "cannot rename WireGuard profile");
		return ok();
	} else if (action == "routing") {
		if (!has_route) return fail(-1, "routeAllowedIPs is required");
		if (exists_interface(id) || desired(s) == id) return fail(-1, "disable this profile before changing routing");
		profile.route_allowed_ips = route;
		e = save(s, *ps);
	} else if (action == "up") {
		for (const auto &p : *ps)
			if (p.id != id && (exists_interface(p.id) || desired(s) == p.id)) return fail(-1, "disable the active profile before switching");
		if (!exists_interface(id)) e = quick(s, "up", id);
		if (e.empty()) {
			e = write_private(gopath::join(s.dir, "active"), id + "\n");
			if (!e.empty()) (void)quick(s, "down", id);
		}
	} else if (action == "down" || action == "delete") {
		// Disable boot restore first; failed teardown remains visible and retryable.
		if (desired(s) == id) {
			std::string active = gopath::join(s.dir, "active");
			if (int err = remove_file(active); err && err != ENOENT) e = gopath::path_error("remove", active, err);
		}
		if (e.empty() && exists_interface(id)) e = quick(s, "down", id);
		if (e.empty() && action == "delete") {
			auto next = *ps;
			next.erase(next.begin() + idx);
			e = save(s, next);
			if (e.empty())
				if (int err = remove_file(config(s, id))) e = gopath::path_error("remove", config(s, id), err);
		}
	} else {
		return fail(-1, "invalid action");
	}
	if (!e.empty()) {
		std::string message = e.rfind("WireGuard ", 0) == 0 ? e : "cannot save WireGuard state";
		s.failures[id] = message;
		return fail(-1, message);
	}
	s.failures.erase(id);
	return ok();
}

} // namespace nkos::extensions::wireguard
