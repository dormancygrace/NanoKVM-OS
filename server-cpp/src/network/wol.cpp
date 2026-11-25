// Wake-on-LAN, as the Go server's service/network/wol.go: ether-wake on a hardware interface, a rate limit per user
// and for all, and the history of woken addresses with their names in /etc/kvm/cache/wol.

#include "network/internal.hpp"
#include "network/network.hpp"

#include "common/log.hpp"

#include <algorithm>
#include <thread>

namespace nkos::network {

namespace {

constexpr std::size_t max_wol_history = 100;
constexpr auto wol_flush_interval = std::chrono::minutes(1);

// Serialises every read-modify-write of the history file.
std::mutex wol_mac_mutex;
std::vector<std::string> wol_pending;  // under wol_mac_mutex
bool wol_flush_armed = false;
std::uint64_t wol_flush_generation = 0;

// Any user may wake: each one a second on average, five at once, and all users together twice that.
WakeLimiter wol_limiter{10, std::chrono::milliseconds(500)};
std::mutex wol_user_mu;
std::map<std::string, std::unique_ptr<WakeLimiter>> wol_user_limiters;

WakeLimiter &wol_user_limiter(const std::string &user)
{
	std::lock_guard lock(wol_user_mu);
	auto &limiter = wol_user_limiters[user];
	if (!limiter) limiter = std::make_unique<WakeLimiter>(5, std::chrono::seconds(1));
	return *limiter;
}

bool wol_interface_is_hardware(const std::string &name)
{
	std::string base = path_join(paths.wol_sys_class_net, name);
	if (stat_path(path_join(base, "device"))) return false;
	auto uevent = read_file(path_join(base, "uevent"));
	if (!uevent) return false;
	for (const auto &line : split(*uevent, "\n"))
		if (line == "DEVTYPE=gadget") return false;
	return true;
}

bool is_mac_exist(const std::string &mac)
{
	auto macs = read_wol_macs(nullptr);
	if (!macs) return false;
	for (const auto &item : *macs) {
		std::string item_mac, name;
		if (!split_wol_mac_line(item, item_mac, name)) continue;
		auto normalized = parse_mac(item_mac);
		if (normalized && *normalized == mac) return true;
	}
	return false;
}

// Writes the addresses wakes added since the last write, in one rewrite of the file. Holds wol_mac_mutex.
void flush_wol_pending_locked()
{
	wol_flush_armed = false;
	wol_flush_generation++;
	if (wol_pending.empty()) return;
	auto pending = std::move(wol_pending);
	wol_pending.clear();
	if (auto err = mkdir_all(dir_name(paths.wol_mac_file), 0755)) {
		log::error("failed to create dir: {}", err.text);
		return;
	}
	Error err;
	auto macs = read_wol_macs(&err);
	if (err && !err.not_exist()) {
		log::error("failed to read {}: {}", paths.wol_mac_file, err.text);
		return;
	}
	std::vector<std::string> all = macs.value_or(std::vector<std::string>{});
	all.insert(all.end(), pending.begin(), pending.end());
	if (auto e = write_wol_macs(bound_wol_history(all))) log::error("failed to write {}: {}", paths.wol_mac_file, e.text);
}

// Wakes add new addresses in memory; the file is rewritten at most once a minute.
void save_mac(const std::string &mac)
{
	std::lock_guard lock(wol_mac_mutex);
	if (is_mac_exist(mac) || std::find(wol_pending.begin(), wol_pending.end(), mac) != wol_pending.end()) return;
	wol_pending.push_back(mac);
	if (!wol_flush_armed) {
		wol_flush_armed = true;
		std::uint64_t generation = wol_flush_generation;
		std::thread([generation] {
			std::this_thread::sleep_for(wol_flush_interval);
			std::lock_guard l(wol_mac_mutex);
			if (wol_flush_armed && wol_flush_generation == generation) flush_wol_pending_locked();
		}).detach();
	}
}

std::vector<std::string> rewrite_history(const std::vector<std::string> &macs, const std::string &mac, const std::string *name, bool &found)
{
	std::vector<std::string> lines;
	found = false;
	for (const auto &line : macs) {
		std::string item_mac, item_name;
		if (!split_wol_mac_line(line, item_mac, item_name)) continue;
		auto normalized = parse_mac(item_mac);
		if (!normalized) {
			lines.push_back(format_wol_mac_line(item_mac, item_name));
			continue;
		}
		if (*normalized != mac) {
			lines.push_back(format_wol_mac_line(*normalized, item_name));
			continue;
		}
		found = true;
		if (name) lines.push_back(format_wol_mac_line(*normalized, *name));
	}
	return lines;
}

} // namespace

bool WakeLimiter::allow(std::chrono::steady_clock::time_point now)
{
	std::lock_guard lock(mu);
	if (!last) tokens = burst;
	else tokens = std::min(burst, tokens + std::chrono::duration<double>(now - *last).count() / std::chrono::duration<double>(every).count());
	last = now;
	if (tokens < 1) return false;
	tokens--;
	return true;
}

std::function<std::vector<Interface>(bool *)> system_network_interfaces = [](bool *ok) { return interfaces(ok); };

std::optional<std::string> parse_mac(const std::string &input)
{
	std::string mac;
	for (char c : trim_space(input)) {
		if (c == '-' || c == ':' || c == '.') continue;
		mac += c >= 'a' && c <= 'z' ? static_cast<char>(c - 'a' + 'A') : c;
	}
	if (mac.size() != 12 || !std::all_of(mac.begin(), mac.end(), [](char c) { return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F'); }))
		return std::nullopt;
	std::string out;
	for (std::size_t i = 0; i < 12; i += 2) {
		if (i) out += ':';
		out += mac.substr(i, 2);
	}
	return out;
}

bool split_wol_mac_line(const std::string &raw, std::string &mac, std::string &name)
{
	std::string line = trim_space(raw);
	if (line.empty()) return false;
	auto f = fields(line);
	if (f.empty()) return false;
	mac = f[0];
	name = line.size() == mac.size() ? "" : trim_space(line.substr(mac.size()));
	return true;
}

std::string format_wol_mac_line(const std::string &mac, const std::string &name) { return name.empty() ? mac : mac + " " + name; }

std::string sanitize_wol_mac_name(const std::string &name) { return join(fields(name), " "); }

std::vector<std::string> bound_wol_history(std::vector<std::string> macs)
{
	while (macs.size() > max_wol_history) {
		std::size_t drop = 0;
		for (std::size_t i = 0; i < macs.size(); i++) {
			std::string mac, name;
			if (split_wol_mac_line(macs[i], mac, name) && name.empty()) {
				drop = i;
				break;
			}
		}
		macs.erase(macs.begin() + static_cast<std::ptrdiff_t>(drop));
	}
	return macs;
}

std::optional<std::vector<std::string>> read_wol_macs(Error *err)
{
	Error e;
	auto content = read_file(paths.wol_mac_file, &e);
	if (err) *err = e;
	if (!content) return std::nullopt;
	std::vector<std::string> macs;
	for (const auto &line : split(*content, "\n")) {
		std::string mac, name;
		if (split_wol_mac_line(line, mac, name)) macs.push_back(format_wol_mac_line(mac, name));
	}
	return macs;
}

Error write_wol_macs(const std::vector<std::string> &macs)
{
	std::string data = macs.empty() ? "" : join(macs, "\n") + "\n";
	return atomicfile_write(paths.wol_mac_file, data, 0644);
}

std::optional<std::vector<std::string>> wol_interfaces(Error *err)
{
	bool ok = true;
	auto list = system_network_interfaces(&ok);
	if (!ok) {
		if (err) *err = make_error("route ip+net: netlinkrib failed");
		return std::nullopt;
	}
	std::vector<std::string> names;
	for (const auto &item : list) {
		// IFF_UP, IFF_LOOPBACK, IFF_BROADCAST
		if (!(item.flags & 1) || (item.flags & 8) || !(item.flags & 2) || item.hwaddr.size() != 6 || !wol_interface_is_hardware(item.name))
			continue;
		names.push_back(item.name);
	}
	std::sort(names.begin(), names.end());
	return names;
}

std::optional<std::string> select_wol_interface(const std::string &requested_in)
{
	auto list = wol_interfaces(nullptr);
	if (!list) return std::nullopt;
	std::string requested = trim_space(requested_in);
	if (requested.empty()) {
		if (std::find(list->begin(), list->end(), "eth0") != list->end()) return std::string("eth0");
		if (!list->empty()) return list->front();
		return std::nullopt;
	}
	if (std::find(list->begin(), list->end(), requested) != list->end()) return requested;
	return std::nullopt;
}

int wake(const std::string &user, const std::string &mac_address, const std::string &iface, std::string &message)
{
	auto mac = parse_mac(mac_address);
	if (!mac) {
		message = "invalid MAC address";
		return -2;
	}
	auto name = select_wol_interface(iface);
	if (!name) {
		message = "invalid network interface";
		return -2;
	}
	auto now = std::chrono::steady_clock::now();
	if (!wol_user_limiter(user).allow(now) || !wol_limiter.allow(now)) {
		message = "too many wake requests, try again in a second";
		return -4;
	}
	RunOptions options;
	options.combined = true;
	auto r = run({"ether-wake", "-i", *name, "-b", *mac}, options);
	if (!r.ok()) {
		log::error("failed to wake on lan: {}", r.error);
		message = r.out;
		return -3;
	}
	save_mac(*mac);
	log::debug("wake on lan: {} via {}", *mac, *name);
	message.clear();
	return 0;
}

std::string wol_macs_json(int &code, std::string &msg)
{
	std::lock_guard lock(wol_mac_mutex);
	Error err;
	auto macs = read_wol_macs(&err);
	if (err.not_exist()) {
		macs = std::vector<std::string>{};
		err = {};
	}
	if (err) {
		code = -2;
		msg = "open file error";
		return "";
	}
	// The addresses not written yet are history too.
	auto all = *macs;
	all.insert(all.end(), wol_pending.begin(), wol_pending.end());
	code = 0;
	msg = "success";
	return "{\"macs\":" + json_strings(bound_wol_history(all), false) + "}";
}

std::pair<int, std::string> set_wol_mac_name(const std::string &mac_in, const std::string &name_in)
{
	auto mac = parse_mac(mac_in);
	if (!mac) return {-2, "invalid MAC address"};
	std::string name = sanitize_wol_mac_name(name_in);
	if (name.empty()) return {-1, "invalid arguments"};
	std::lock_guard lock(wol_mac_mutex);
	flush_wol_pending_locked();
	Error err;
	auto macs = read_wol_macs(&err);
	if (!macs) {
		log::error("failed to open {}: {}", paths.wol_mac_file, err.text);
		return {-2, "read failed"};
	}
	bool found = false;
	auto lines = rewrite_history(*macs, *mac, &name, found);
	if (!found) {
		log::error("failed to find mac {}", mac_in);
		return {-3, "write failed"};
	}
	if (auto e = write_wol_macs(lines)) {
		log::error("failed to write {}: {}", paths.wol_mac_file, e.text);
		return {-3, "write failed"};
	}
	return {0, "success"};
}

std::pair<int, std::string> delete_wol_mac(const std::string &mac_in)
{
	auto mac = parse_mac(mac_in);
	if (!mac) return {-2, "invalid MAC address"};
	std::lock_guard lock(wol_mac_mutex);
	flush_wol_pending_locked();
	Error err;
	auto macs = read_wol_macs(&err);
	if (!macs) {
		log::error("failed to open {}: {}", paths.wol_mac_file, err.text);
		return {-2, "read failed"};
	}
	bool found = false;
	auto lines = rewrite_history(*macs, *mac, nullptr, found);
	if (auto e = write_wol_macs(lines)) {
		log::error("failed to write {}: {}", paths.wol_mac_file, e.text);
		return {-3, "write failed"};
	}
	return {0, "success"};
}

Decoded<std::pair<std::string, std::string>> decode_wake(const RequestData &data)
{
	Decoded<std::pair<std::string, std::string>> d;
	auto b = Binder::bind(data, false);
	std::string mac, iface;
	if (b.ok()) {
		b.str("Mac", "mac", mac);
		b.str("Interface", "interface", iface);
	}
	if (!b.ok() || mac.empty()) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	d.value = std::pair{mac, iface};
	return d;
}

Decoded<std::pair<std::string, std::string>> decode_set_mac_name(const RequestData &data)
{
	Decoded<std::pair<std::string, std::string>> d;
	auto b = Binder::bind(data, false);
	std::string mac, name;
	if (b.ok()) {
		b.str("Mac", "mac", mac);
		b.str("Name", "name", name);
	}
	if (!b.ok() || mac.empty() || name.empty()) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	d.value = std::pair{mac, name};
	return d;
}

Decoded<std::string> decode_delete_mac(const RequestData &data)
{
	Decoded<std::string> d;
	auto b = Binder::bind(data, false);
	std::string mac;
	if (b.ok()) b.str("Mac", "mac", mac);
	if (!b.ok() || mac.empty()) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	d.value = mac;
	return d;
}

} // namespace nkos::network
