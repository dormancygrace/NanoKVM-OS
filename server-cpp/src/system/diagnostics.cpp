// The diagnostics snapshot and report (service/vm/diagnostics.go) and the routes of System > Diagnostics. The snapshot
// is a small allow-list of support data, not a general system inspector: it stays safe to attach to an issue (no
// configuration, credentials, network addresses or video data).

#include "system/diagnostics.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "common/tz.hpp"
#include "network/network.hpp"
#include "notify/json.hpp"
#include "osupdate/osupdate.hpp"
#include "storage/go.hpp"
#include "usb/gadget.hpp"
#include "video/hdmi.hpp"
#include "video/monitor.hpp"
#include "video/screen.hpp"
#include "video/vision.hpp"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <charconv>
#include <climits>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <map>
#include <mutex>
#include <poll.h>
#include <set>
#include <signal.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

extern char **environ;

namespace nkos::system::diagnostics {

namespace go = storage::go;
using Clock = std::chrono::steady_clock;

Paths &paths()
{
	static Paths p;
	return p;
}

namespace {

constexpr auto probe_timeout = std::chrono::seconds(2);
constexpr auto collection_budget = std::chrono::seconds(8);
constexpr std::size_t command_output = 64 * 1024;

// diagnosticText: the file's text without surrounding white space, "" when it cannot be read.
std::string text(const std::string &path)
{
	std::string data;
	if (go::read_file(path, data)) return "";
	return go::trim_space(data);
}

// os.Stat: 0, or the errno.
int stat_error(const std::string &path, struct stat *out = nullptr)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0) return errno;
	if (out) *out = st;
	return 0;
}

bool safe_char(unsigned char c)
{
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '.' || c == '_' ||
	       c == '+' || c == '-';
}

// ^[A-Za-z0-9._+-]{1,128}$ after TrimSpace.
std::string safe_scalar(std::string_view value)
{
	std::string v = go::trim_space(value);
	if (v.empty() || v.size() > 128) return "";
	for (unsigned char c : v)
		if (!safe_char(c)) return "";
	return v;
}

bool digits(std::string_view s, std::size_t max)
{
	if (s.empty() || s.size() > max) return false;
	for (char c : s)
		if (c < '0' || c > '9') return false;
	return true;
}

// ^[0-9]{1,5} × [0-9]{1,5}$
std::string safe_dimension(const std::string &value)
{
	static constexpr std::string_view sep = " \xc3\x97 ";
	auto at = value.find(sep);
	if (at == std::string::npos) return "";
	std::string_view v = value;
	return digits(v.substr(0, at), 5) && digits(v.substr(at + sep.size()), 5) ? value : "";
}

std::string safe_state(std::string_view value)
{
	static const std::set<std::string_view> known = {
		"ok",        "running",  "stopped",          "absent",        "unavailable",         "error",
		"disabled",  "configured", "no-signal",      "checking",      "installing",          "ready",
		"up-to-date", "installed", "idle",           "operation-failed", "saved-profile",    "power-cycle-pending",
		"pending-reboot"};
	return known.contains(value) ? std::string(value) : "unavailable";
}

std::string safe_desired(const std::string &value)
{
	return value == "enabled" || value == "disabled" || value == "on-demand" || value == "optional" ? value : "";
}

std::string safe_nft_hook(const std::string &value)
{
	static const std::set<std::string_view> known = {"input", "output", "forward", "prerouting", "postrouting", "ingress"};
	return known.contains(value) ? value : "unknown";
}

std::string safe_nft_policy(const std::string &value)
{
	static const std::set<std::string_view> known = {"accept", "drop", "queue", "continue", "return"};
	return known.contains(value) ? value : "";
}

// NanoKVM OS's own nftables tables, by what they do. Only these names leave the device.
const std::map<std::string, std::string> &nft_features()
{
	static const std::map<std::string, std::string> features = {{"nkos_mss", "mss-clamp"},
	                                                            {"nkos_usb_internet", "usb-internet"}};
	return features;
}

std::string safe_nft_feature(const std::string &value)
{
	for (const auto &[table, feature] : nft_features())
		if (value == feature) return value;
	return "";
}

const std::vector<std::string> &package_names()
{
	static const std::vector<std::string> names = {
		"nanokvm",        "nanokvm-web",      "nanokvm-app",        "nanokvm-base",  "nanokvm-release", "nanokvm-kernel",
		"nanokvm-kmod",   "nanokvm-firmware", "nanokvm-bootloader", "nanokvm-zlib",  "nanokvm-openssl3", "nanokvm-openssl", "nanokvm-tpu"};
	return names;
}

// ---- versions ----

// getImageVersion: /boot/image.json's image_version, else the legacy /boot/ver by its known names.
std::string image_version()
{
	std::string data;
	if (!go::read_file("/boot/image.json", data)) {
		notify::json::Value v;
		std::string version;
		bool ok = notify::json::unmarshal(data, v) &&
		          notify::json::members(v, {"image_version"}, [&](std::string_view, const notify::json::Value &m) {
			          return notify::json::get(m, version);
		          });
		if (ok && !go::trim_space(version).empty()) return version;
	}
	if (go::read_file("/boot/ver", data)) return "";
	std::erase(data, '\n');
	static const std::map<std::string, std::string> legacy = {
		{"2024-06-23-20-59-2d2bfb.img", "v1.0.0"}, {"2024-07-23-20-18-587710.img", "v1.1.0"},
		{"2024-08-08-19-44-bef2ca.img", "v1.2.0"}, {"2024-11-13-09-59-9c961a.img", "v1.3.0"},
		{"2025-02-17-19-08-3649fe.img", "v1.4.0"}, {"2025-04-17-14-21-98d17d.img", "v1.4.1"},
		{"2026-01-05-1_4_1.img", "v1.4.2"}};
	if (auto it = legacy.find(data); it != legacy.end()) return it->second;
	return data;
}

std::string application_version()
{
	std::string data;
	if (go::read_file("/kvmapp/version", data)) return "1.0.0";
	std::erase(data, '\n');
	return data;
}

std::vector<Package> installed_packages()
{
	std::vector<Package> result;
	std::map<std::string, std::string> versions;
	std::string data;
	bool readable = !go::read_file(paths().apk_db, data);
	if (readable) {
		std::string name;
		std::size_t start = 0;
		for (;;) {
			std::size_t nl = data.find('\n', start);
			std::string_view line = std::string_view(data).substr(start, nl == std::string::npos ? std::string::npos : nl - start);
			if (line.starts_with("P:")) name = std::string(line.substr(2));
			if (line.starts_with("V:") && !name.empty()) versions[name] = safe_scalar(line.substr(2));
			if (line.empty()) name.clear();
			if (nl == std::string::npos) break;
			start = nl + 1;
		}
	}
	for (const auto &name : package_names()) {
		Package p{name, "", "absent"};
		if (!readable) {
			p.state = "unavailable";
		} else if (auto it = versions.find(name); it != versions.end() && !it->second.empty()) {
			p.state = "installed";
			p.version = it->second;
		}
		result.push_back(std::move(p));
	}
	return result;
}

bool modules_metadata(const std::string &release)
{
	struct stat st{};
	return stat_error(go::join(go::join(paths().modules_dir, release), "modules.dep"), &st) == 0 && S_ISREG(st.st_mode) &&
	       st.st_size > 0;
}

Item kernel_modules(const std::string &kernel, const std::string &target, bool pending_reboot)
{
	if (kernel.empty()) return {"unavailable", "kernel release unavailable"};
	if (modules_metadata(kernel)) return {"ok", "module metadata matches running kernel; ABI not verified"};
	if (pending_reboot && !target.empty() && target != kernel && modules_metadata(target))
		return {"pending-reboot", "new kernel modules installed; reboot is pending"};
	if (stat_error(go::join(paths().modules_dir, kernel)) == ENOENT)
		return {"error", "no installed modules for running kernel"};
	return {"unavailable", "matching module directory lacks usable metadata"};
}

Item boot_status(const std::string &kernel, bool pending_reboot)
{
	if (int e = stat_error(paths().boot_dir)) {
		if (e == ENOENT) return {"error", "boot partition unavailable"};
		return {"unavailable", "cannot inspect boot partition"};
	}
	if (stat_error(go::join(paths().boot_dir, "boot.sd"))) return {"error", "boot FIT is unavailable"};
	std::string boot_kernel = text(go::join(paths().boot_dir, "kernel.release"));
	if (boot_kernel.empty()) return {"unavailable", "boot release marker is unavailable"};
	if (!kernel.empty() && boot_kernel != kernel) {
		if (pending_reboot) return {"pending-reboot", "boot marker differs; reboot is pending"};
		return {"error", "boot kernel does not match running kernel"};
	}
	return {"ok", "boot marker matches running kernel; FIT ABI not verified"};
}

// ---- services ----

Item service_status(const std::string &name, bool optional, Clock::time_point budget)
{
	Item item{"", "", optional};
	if (int e = stat_error(go::join(paths().init_dir, name))) {
		if (e == ENOENT) item.state = "absent", item.detail = "service is not installed";
		else item.state = "unavailable", item.detail = "cannot inspect service definition";
		return item;
	}
	if (!look_path("rc-service")) {
		item.state = "unavailable", item.detail = "OpenRC status command is unavailable";
		return item;
	}
	auto r = run_command({"rc-service", name, "status"}, command_output, std::min(Clock::now() + probe_timeout, budget));
	if (r.deadline_passed) item.state = "error", item.detail = "OpenRC status timed out";
	else if (r.error.empty()) item.state = "running", item.detail = "reported running by OpenRC";
	// OpenRC reserves exit status 3 for a stopped service.
	else if (r.exit_code == 3) item.state = "stopped", item.detail = "installed but not running";
	else item.state = "error", item.detail = "OpenRC status command failed";
	return item;
}

// openVPNPIDLive: the pid file names a running openvpn.
bool openvpn_pid_live(const std::string &path)
{
	std::string data;
	if (go::read_file(path, data)) return false;
	std::string s = go::trim_space(data);
	std::string_view v = s;
	if (!v.empty() && (v[0] == '+' || v[0] == '-')) v.remove_prefix(1);
	long long pid = 0;
	auto [end, ec] = std::from_chars(v.data(), v.data() + v.size(), pid);
	if (v.empty() || ec != std::errc{} || end != v.data() + v.size()) return false;
	if (!s.empty() && s[0] == '-') pid = -pid;
	if (pid < 2) return false;
	std::string cmd;
	if (go::read_file(go::join(go::join(paths().proc_dir, std::to_string(pid)), "cmdline"), cmd)) return false;
	return go::base(std::string_view(cmd).substr(0, cmd.find('\0'))) == "openvpn";
}

Item openvpn_status()
{
	Item item{"", "", true};
	if (!look_path("openvpn")) {
		item.state = "absent", item.detail = "OpenVPN client is not installed";
		return item;
	}
	item.state = "stopped", item.detail = "installed; no active profile reported";
	DIR *d = ::opendir(paths().openvpn_run.c_str());
	if (!d) return item;
	std::vector<std::string> names;
	while (auto *e = ::readdir(d)) {
		std::string n = e->d_name;
		if (n != "." && n != "..") names.push_back(n);
	}
	::closedir(d);
	std::sort(names.begin(), names.end());
	for (const auto &n : names)
		if (n.ends_with(".pid") && openvpn_pid_live(go::join(paths().openvpn_run, n))) {
			item.state = "running", item.detail = "an active profile is reported";
			break;
		}
	return item;
}

std::vector<Service> service_statuses(Clock::time_point budget)
{
	struct Spec {
		const char *name, *desired, *disabled_marker;
		bool optional;
	};
	static const Spec specs[] = {
		{"nanokvm-storage", "enabled", "", false}, {"nanokvm-modules", "enabled", "", false},
		{"nanokvm-board", "enabled", "", false},   {"nanokvm-network", "enabled", "", false},
		{"nanokvm-app", "enabled", "", false},     {"nanokvm-usb", "enabled", "", false},
		{"nanokvm-ssh", "enabled", "/etc/kvm/ssh_stop", false}, {"chronyd", "enabled", "", false},
		{"dnsmasq", "on-demand", "", true},        {"tailscaled", "optional", "", true},
		{"netbird", "optional", "", true},         {"zerotier-one", "optional", "", true},
	};
	constexpr std::size_t count = std::size(specs);
	std::vector<Service> results(count + 1);
	std::vector<std::size_t> probes;
	for (std::size_t i = 0; i < count; i++) {
		results[i].name = specs[i].name;
		results[i].desired = specs[i].desired;
		if (*specs[i].disabled_marker && stat_error(specs[i].disabled_marker) == 0) {
			results[i].desired = "disabled";
			results[i].item = {"disabled", "disabled by NanoKVM settings", specs[i].optional};
			continue;
		}
		probes.push_back(i);
	}
	// Three probes at a time; one that would start after the budget is not run.
	std::atomic<std::size_t> next{0};
	auto worker = [&] {
		for (std::size_t k; (k = next.fetch_add(1)) < probes.size();) {
			const Spec &spec = specs[probes[k]];
			Item &out = results[probes[k]].item;
			if (Clock::now() >= budget) out = {"unavailable", "collection budget expired", spec.optional};
			else out = service_status(spec.name, spec.optional, budget);
		}
	};
	{
		std::jthread a(worker), b(worker), c(worker);
	}
	results[count] = Service{"openvpn client", "optional", openvpn_status()};
	return results;
}

// ---- video, USB, firewall ----

std::string monitor_profile()
{
	std::string value = text("/etc/kvm/monitor_resolution");
	if (value == "0") return "automatic";
	if (value == "720" || value == "1080" || value == "1440" || value == "2160") return value;
	// 16:10 and 21:9 profiles by their size; an id is 16 bits.
	unsigned id = 0;
	auto [end, ec] = std::from_chars(value.data(), value.data() + value.size(), id);
	if (!value.empty() && ec == std::errc{} && end == value.data() + value.size() && id <= 0xffff &&
	    video::edid::valid_profile(static_cast<std::uint16_t>(id))) {
		auto [w, h] = video::edid::size(static_cast<std::uint16_t>(id));
		return std::to_string(w) + "x" + std::to_string(h);
	}
	return "";
}

std::string dimensions(const std::string &width, const std::string &height)
{
	if (width.empty() || height.empty()) return "";
	return width + " \xc3\x97 " + height;
}

std::string video_value(int value) { return value <= 0 ? "" : std::to_string(value); }

void usb_status(Snapshot &s)
{
	auto c = usb::read_composition(usb::flags_dir, usb::gadget_root);
	std::pair<const char *, bool> devices[] = {{"keyboard", c.keyboard}, {"relative mouse", c.relative},
	                                           {"absolute mouse", c.absolute}, {"network", c.network},
	                                           {"disk", c.disk},          {"serial", c.serial},
	                                           {"audio", c.audio}};
	for (auto [name, on] : devices)
		if (on) s.usb_selected.emplace_back(name);
	if (int e = stat_error(paths().gadget_dir)) {
		s.usb_binding = {"unavailable", e == ENOENT ? "USB gadget is not present" : "cannot inspect USB gadget"};
		return;
	}
	std::string udc = text(go::join(paths().gadget_dir, "UDC"));
	if (udc.empty()) {
		s.usb_binding = {"stopped", "gadget is not bound to a USB controller"};
		return;
	}
	std::string state = text(go::join(go::join("/sys/class/udc", udc), "state"));
	if (state.empty()) s.usb_binding = {"ok", "gadget bound; host connection state unavailable"};
	else s.usb_binding = {"ok", "gadget bound; controller " + state};
}

struct NftChain {
	std::string family, table, name, hook, policy;
};

// parseNFTHookChains: json.Unmarshal of nft --json into {nftables: [{chain: {...}}]}; false on an error.
bool parse_nft_hook_chains(std::string_view data, std::vector<HookChain> &chains)
{
	namespace j = notify::json;
	j::Value doc;
	if (!j::unmarshal(data, doc)) return false;
	bool ok = j::members(doc, {"nftables"}, [&](std::string_view, const j::Value &list) {
		if (list.is_null()) return true;
		if (list.kind != j::Value::Kind::array) return false;
		bool good = true;
		for (const auto &entry : list.items) {
			std::optional<NftChain> chain;
			bool entry_ok = j::members(entry, {"chain"}, [&](std::string_view, const j::Value &c) {
				if (c.is_null()) {
					chain.reset();
					return true;
				}
				NftChain value = chain.value_or(NftChain{});
				bool r = j::members(c, {"family", "table", "name", "hook", "policy"},
				                    [&](std::string_view k, const j::Value &f) {
					                    std::string &target = k == "family" ? value.family : k == "table" ? value.table
					                                        : k == "name"     ? value.name
					                                        : k == "hook"     ? value.hook
					                                                          : value.policy;
					                    return j::get(f, target);
				                    });
				chain = value;
				return r;
			});
			if (!entry_ok) good = false;
			if (!chain || chain->hook.empty()) continue;
			std::string feature;
			if (auto it = nft_features().find(chain->table); it != nft_features().end()) feature = it->second;
			chains.push_back({"chain-" + std::to_string(chains.size() + 1), safe_nft_hook(chain->hook),
			                  safe_nft_policy(chain->policy), feature});
		}
		return good;
	});
	return ok;
}

void firewall_status(Snapshot &s, Clock::time_point budget)
{
	if (!look_path("nft")) {
		s.firewall_state = "unavailable", s.firewall_detail = "nft command is unavailable";
		return;
	}
	auto r = run_command({"nft", "--json", "list", "ruleset"}, command_output, std::min(Clock::now() + probe_timeout, budget));
	if (r.deadline_passed) {
		s.firewall_state = "error", s.firewall_detail = "nftables query timed out";
		return;
	}
	if (!r.error.empty()) {
		s.firewall_state = "error", s.firewall_detail = "nftables query failed";
		return;
	}
	std::vector<HookChain> chains;
	if (!parse_nft_hook_chains(r.output, chains)) {
		s.firewall_state = "error", s.firewall_detail = "cannot parse nftables hook chains";
		return;
	}
	s.firewall_state = "ok";
	s.hook_chains = std::move(chains);
	s.firewall_detail = s.hook_chains.empty() ? "no active nftables hook chains"
	                                          : "active base chains shown; policies alone do not describe every rule";
}

// ---- JSON ----

using Out = notify::json::Out;

Out item_json(const Item &i)
{
	Out o = Out::object();
	o.set("state", Out::str(i.state));
	if (!i.detail.empty()) o.set("detail", Out::str(i.detail));
	if (i.optional) o.set("optional", Out::boolean(true));
	return o;
}

Out package_json(const Package &p)
{
	Out o = Out::object();
	o.set("name", Out::str(p.name));
	if (!p.version.empty()) o.set("version", Out::str(p.version));
	o.set("state", Out::str(p.state));
	return o;
}

Out chain_json(const HookChain &c)
{
	Out o = Out::object();
	o.set("label", Out::str(c.label));
	o.set("hook", Out::str(c.hook));
	if (!c.policy.empty()) o.set("policy", Out::str(c.policy));
	if (!c.feature.empty()) o.set("feature", Out::str(c.feature));
	return o;
}

// collectReportHardware: public hardware fields, a model or capability shared by devices.
Out report_hardware()
{
	Out o = Out::object();
	std::string board = text("/etc/kvm/board-profile");
	if (board == "alpha" || board == "beta" || board == "lite" || board == "pcie") o.set("boardProfile", Out::str(board));
	std::string receiver = video::monitor::receiver_model();
	if (!receiver.empty()) o.set("hdmiReceiver", Out::str(receiver));
	o.set("ethernetPresent", Out::boolean(stat_error("/sys/class/net/eth0") == 0));
	o.set("wifiPresent", Out::boolean(network::wifi_hardware_present()));
	const std::string card = "/sys/block/mmcblk0/";
	// The SD product name reported by the kernel is five ASCII bytes.
	std::string name = text(card + "device/name");
	bool name_ok = !name.empty() && name.size() <= 5 &&
	               std::all_of(name.begin(), name.end(), [](unsigned char c) {
		               return std::isalnum(c) || c == '.' || c == '_' || c == '+' || c == ' ' || c == '-';
	               });
	if (name_ok) o.set("sdCardModel", Out::str(name));
	// Linux reports block device size in 512-byte sectors (strconv.ParseUint(s, 10, 55)).
	std::string size = text(card + "size");
	std::uint64_t sectors = 0;
	auto [end, ec] = std::from_chars(size.data(), size.data() + size.size(), sectors);
	if (!size.empty() && ec == std::errc() && end == size.data() + size.size() && sectors < (std::uint64_t{1} << 55) &&
	    sectors != 0)
		o.set("sdCardCapacityBytes", Out::lit(std::to_string(sectors * 512)));
	return o;
}

Out report(const Snapshot &s)
{
	static const std::set<std::string_view> service_names = {
		"nanokvm-storage", "nanokvm-modules", "nanokvm-board", "nanokvm-network", "nanokvm-app", "nanokvm-usb",
		"nanokvm-ssh",     "chronyd",         "dnsmasq",       "tailscaled",      "netbird",     "zerotier-one",  "openvpn client"};
	static const std::set<std::string_view> usb_names = {"keyboard", "relative mouse", "absolute mouse", "network",
	                                                     "disk",     "serial",         "audio"};
	Out versions = Out::object();
	versions.set("Application", Out::str(safe_scalar(s.application)));
	versions.set("Image", Out::str(safe_scalar(s.image)));
	versions.set("Alpine", Out::str(safe_scalar(s.alpine)));
	versions.set("Kernel", Out::str(safe_scalar(s.kernel)));
	versions.set("Modules", Out::str(safe_state(s.modules.state)));
	versions.set("Boot", Out::str(safe_state(s.boot.state)));
	Out packages = Out::array();
	for (const auto &p : s.packages)
		if (std::find(package_names().begin(), package_names().end(), p.name) != package_names().end())
			packages.add(package_json({p.name, safe_scalar(p.version), safe_state(p.state)}));
	versions.set("Packages", std::move(packages));

	Out services = Out::array();
	for (const auto &svc : s.services) {
		if (!service_names.contains(svc.name)) continue;
		Out o = Out::object();
		o.set("name", Out::str(svc.name));
		o.set("state", Out::str(safe_state(svc.item.state)));
		if (svc.item.optional) o.set("optional", Out::boolean(true));
		if (auto d = safe_desired(svc.desired); !d.empty()) o.set("desired", Out::str(d));
		services.add(std::move(o));
	}
	Out video = Out::object();
	video.set("State", Out::str(safe_state(s.video.state)));
	video.set("EDIDState", Out::str(safe_state(s.video.edid_state)));
	video.set("Profile", Out::str(safe_scalar(s.video.profile)));
	video.set("Input", Out::str(safe_dimension(s.video.input)));
	video.set("Output", Out::str(safe_dimension(s.video.output)));
	video.set("MeasuredFPS", Out::str(safe_scalar(s.video.measured_fps)));
	Out selected = Out::array();
	for (const auto &u : s.usb_selected)
		if (usb_names.contains(u)) selected.add(Out::str(u));
	Out usb = Out::object();
	usb.set("Selected", std::move(selected));
	usb.set("Binding", Out::str(safe_state(s.usb_binding.state)));
	Out apk = Out::object();
	apk.set("State", Out::str(safe_state(s.apk_state)));
	apk.set("ErrorCategory", Out::str(safe_state(s.apk_error_category)));
	Out chains = Out::array();
	for (std::size_t i = 0; i < s.hook_chains.size(); i++) {
		const auto &c = s.hook_chains[i];
		chains.add(chain_json({"chain-" + std::to_string(i + 1), safe_nft_hook(c.hook), safe_nft_policy(c.policy),
		                       safe_nft_feature(c.feature)}));
	}
	Out firewall = Out::object();
	firewall.set("State", Out::str(safe_state(s.firewall_state)));
	firewall.set("HookChains", std::move(chains));

	Out o = Out::object();
	o.set("hardware", report_hardware());
	o.set("schema", Out::str("nanokvm-diagnostics/v1"));
	o.set("collectedAt", Out::number(s.collected_at));
	o.set("privacy", Out::str("Allow-listed status only; no credentials, configuration contents, network addresses, "
	                          "identifiers, screen, or video data."));
	o.set("versions", std::move(versions));
	o.set("services", std::move(services));
	o.set("video", std::move(video));
	o.set("usb", std::move(usb));
	o.set("apk", std::move(apk));
	o.set("firewall", std::move(firewall));
	return o;
}

// ---- the cache: one collection at a time, kept for 20 s (loop thread) ----

struct Cache {
	std::optional<Snapshot> snapshot;
	Clock::time_point at;
	bool collecting = false;
	std::vector<std::function<void(const Snapshot &)>> waiters;
};
Cache cache;

const Snapshot *fresh_snapshot()
{
	if (cache.snapshot && Clock::now() - cache.at < std::chrono::seconds(20)) return &*cache.snapshot;
	return nullptr;
}

VideoInputs video_inputs()
{
	VideoInputs v;
	v.enabled = !video::hdmi::disabled();
	v.signal = v.enabled && video::vision::hdmi_signal_active();
	v.hardware = {video::monitor::board(), video::monitor::receiver_chip()};
	v.power_cycle_pending = video::monitor::power_cycle_pending();
	return v;
}

// readDiagnostics: done runs on the loop thread with the snapshot.
void read_snapshot(std::function<void(const Snapshot &)> done)
{
	if (auto *s = fresh_snapshot()) return done(*s);
	cache.waiters.push_back(std::move(done));
	if (cache.collecting) return;
	cache.collecting = true;
	auto started = Clock::now();
	loop::blocking([inputs = video_inputs(), started] {
		Snapshot s = collect(inputs);
		loop::post([s = std::move(s), started] {
			cache.snapshot = s;
			cache.at = started;
			cache.collecting = false;
			auto waiters = std::move(cache.waiters);
			cache.waiters.clear();
			for (auto &w : waiters) w(s);
		});
	});
}

// ---- the developer archive route ----

std::atomic<bool> archive_busy{false};

std::string local_stamp(std::int64_t sec)
{
	auto t = tz::Zone::device().local(sec);
	char buf[32];
	std::snprintf(buf, sizeof buf, "%04d%02d%02d-%02d%02d%02d", t.year, t.month, t.day, t.hour, t.minute, t.second);
	return buf;
}

} // namespace

// ---- commands ----

namespace {

// exec.LookPath: the program's path, or "" when PATH has no executable of that name.
std::string find_program(const std::string &name)
{
	auto executable = [](const std::string &path) {
		struct stat st{};
		return ::stat(path.c_str(), &st) == 0 && !S_ISDIR(st.st_mode) && ::faccessat(AT_FDCWD, path.c_str(), X_OK, AT_EACCESS) == 0;
	};
	if (name.find('/') != std::string::npos) return executable(name) ? name : "";
	const char *env = std::getenv("PATH");
	std::string_view list = env ? env : "";
	for (;;) {
		auto colon = list.find(':');
		std::string dir(list.substr(0, colon));
		// A program found through a relative directory is refused (exec.ErrDot).
		if (!dir.empty() && dir[0] == '/' && executable(go::join(dir, name))) return go::join(dir, name);
		if (colon == std::string_view::npos) break;
		list.remove_prefix(colon + 1);
	}
	return "";
}

} // namespace

bool look_path(const std::string &name) { return !find_program(name).empty(); }

CommandResult run_command(const std::vector<std::string> &argv, std::size_t limit, Clock::time_point deadline)
{
	CommandResult r;
	auto finish = [&]() -> CommandResult & {
		r.deadline_passed = Clock::now() >= deadline;
		return r;
	};
	if (Clock::now() >= deadline) {
		r.error = "context deadline exceeded";
		return finish();
	}
	std::string path = argv.at(0);
	if (path.find('/') == std::string::npos) {
		path = find_program(path);
		if (path.empty()) {
			r.error = "exec: \"" + argv[0] + "\": executable file not found in $PATH";
			return finish();
		}
	}
	int out[2], report[2];
	if (::pipe2(out, O_CLOEXEC) != 0) {
		r.error = go::errno_text(errno);
		return finish();
	}
	if (::pipe2(report, O_CLOEXEC) != 0) {
		r.error = go::errno_text(errno);
		::close(out[0]), ::close(out[1]);
		return finish();
	}
	std::vector<char *> args;
	for (const auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	pid_t pid = ::fork();
	if (pid < 0) {
		r.error = "fork/exec " + path + ": " + go::errno_text(errno);
		for (int fd : {out[0], out[1], report[0], report[1]}) ::close(fd);
		return finish();
	}
	if (pid == 0) {
		::setpgid(0, 0);
		int null = ::open("/dev/null", O_RDONLY);
		::dup2(null, 0);
		::dup2(out[1], 1);
		::dup2(out[1], 2);
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int s : {SIGPIPE, SIGTERM, SIGINT, SIGCHLD}) ::sigaction(s, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		::execve(path.c_str(), args.data(), environ);
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	::close(out[1]);
	::close(report[1]);
	int exec_errno = 0;
	bool exec_failed = ::read(report[0], &exec_errno, sizeof exec_errno) == sizeof exec_errno;
	::close(report[0]);
	if (exec_failed) {
		::close(out[0]);
		while (::waitpid(pid, nullptr, 0) < 0 && errno == EINTR) {
		}
		r.error = "fork/exec " + path + ": " + go::errno_text(exec_errno);
		return finish();
	}
	int pidfd = static_cast<int>(::syscall(SYS_pidfd_open, pid, 0));
	int out_fd = out[0];
	bool exited = false, killed = false, limit_hit = false, wait_delay_hit = false;
	int status = 0, wait_errno = 0;
	// WaitDelay: the output is waited for at most 250 ms after the exit or the deadline, whichever comes first.
	std::optional<Clock::time_point> delay_end;
	auto reap = [&](int flags) {
		pid_t got;
		while ((got = ::waitpid(pid, &status, flags)) < 0 && errno == EINTR) {
		}
		// A wait that fails (no such child) ends the command too: nothing is left to wait for.
		if (got < 0) wait_errno = errno;
		if (got == pid || got < 0) {
			exited = true;
			if (!delay_end) delay_end = Clock::now() + std::chrono::milliseconds(250);
		}
	};
	for (;;) {
		auto now = Clock::now();
		if (!exited && !killed && now >= deadline) {
			::kill(-pid, SIGKILL);
			killed = true;
			if (!delay_end) delay_end = now + std::chrono::milliseconds(250);
		}
		bool delay_over = delay_end && now >= *delay_end;
		if (delay_over && out_fd >= 0) {
			::close(out_fd);
			out_fd = -1;
			wait_delay_hit = true;
		}
		if (exited && out_fd < 0) break;
		int wait_ms = -1;
		if (!exited && !killed) wait_ms = static_cast<int>(std::chrono::duration_cast<std::chrono::milliseconds>(deadline - now).count() + 1);
		else if (delay_end && !delay_over)
			wait_ms = static_cast<int>(std::chrono::duration_cast<std::chrono::milliseconds>(*delay_end - now).count() + 1);
		if (pidfd < 0 && !exited) wait_ms = wait_ms < 0 ? 10 : std::min(wait_ms, 10);
		pollfd fds[2];
		int n = 0, out_i = -1, pid_i = -1;
		if (out_fd >= 0) out_i = n, fds[n++] = {out_fd, POLLIN, 0};
		if (!exited && pidfd >= 0) pid_i = n, fds[n++] = {pidfd, POLLIN, 0};
		if (::poll(fds, static_cast<nfds_t>(n), wait_ms) < 0 && errno != EINTR) break;
		if (out_i >= 0 && fds[out_i].revents) {
			char buf[16384];
			ssize_t got = ::read(out_fd, buf, sizeof buf);
			if (got > 0) {
				std::size_t room = limit - r.output.size();
				if (static_cast<std::size_t>(got) > room) {
					// The writer refuses more: the copy ends and the pipe closes, as in Go.
					r.output.append(buf, room);
					limit_hit = true;
					::close(out_fd);
					out_fd = -1;
				} else {
					r.output.append(buf, static_cast<std::size_t>(got));
				}
			} else if (got == 0 || (errno != EAGAIN && errno != EINTR)) {
				::close(out_fd);
				out_fd = -1;
			}
		}
		if (!exited && (pidfd < 0 || (pid_i >= 0 && fds[pid_i].revents))) reap(WNOHANG);
	}
	if (out_fd >= 0) ::close(out_fd);
	if (pidfd >= 0) ::close(pidfd);
	if (!exited) reap(0);
	bool success = WIFEXITED(status) && WEXITSTATUS(status) == 0;
	if (WIFEXITED(status) && !wait_errno) r.exit_code = WEXITSTATUS(status);
	if (wait_errno) r.error = "wait: " + go::errno_text(wait_errno);
	else if (!success) r.error = go::exit_text(WIFEXITED(status) ? WEXITSTATUS(status) : -1, WIFSIGNALED(status) ? WTERMSIG(status) : 0);
	else if (killed) r.error = "context deadline exceeded";
	else if (limit_hit) r.error = "diagnostic command output limit exceeded";
	else if (wait_delay_hit) r.error = "exec: WaitDelay expired before I/O complete";
	return finish();
}

// ---- collection ----

Snapshot collect(const VideoInputs &video)
{
	auto budget = Clock::now() + collection_budget;
	Snapshot s;
	s.collected_at = std::chrono::duration_cast<std::chrono::milliseconds>(
	                     std::chrono::system_clock::now().time_since_epoch()).count();
	s.application = safe_scalar(application_version());
	s.image = safe_scalar(image_version());
	s.alpine = safe_scalar(text("/etc/alpine-release"));
	s.kernel = safe_scalar(text("/proc/sys/kernel/osrelease"));
	std::string boot_target = text(go::join(paths().boot_dir, "kernel.release"));
	bool pending = stat_error("/run/reboot-required") == 0;
	s.modules = kernel_modules(s.kernel, boot_target, pending);
	s.boot = boot_status(s.kernel, pending);
	s.packages = installed_packages();
	s.services = service_statuses(budget);

	s.video.enabled = video.enabled;
	s.video.signal = video.enabled && video.signal;
	s.video.profile = monitor_profile();
	s.video.input = dimensions(video_value(video::read_video_value("/run/nanokvm/width")),
	                           video_value(video::read_video_value("/run/nanokvm/height")));
	s.video.output = dimensions(video_value(video::read_video_value("/run/nanokvm/stream_width")),
	                            video_value(video::read_video_value("/run/nanokvm/stream_height")));
	s.video.measured_fps = video_value(video::read_video_value("/run/nanokvm/now_fps"));
	s.video.edid_state = "saved-profile";
	if (!video.hardware.profile_supported()) s.video.edid_state = "unavailable";
	else if (video.power_cycle_pending) s.video.edid_state = "power-cycle-pending";
	s.video.state = !s.video.enabled ? "disabled" : !s.video.signal ? "no-signal" : "ok";

	usb_status(s);
	auto apk = osupdate::get_apk_status();
	s.apk_state = safe_state(apk.state);
	if (apk.state == "failed") {
		s.apk_state = "error";
		s.apk_error_category = "operation-failed";
	}
	firewall_status(s, budget);
	return s;
}

std::string snapshot_json(const Snapshot &s)
{
	Out versions = Out::object();
	if (!s.application.empty()) versions.set("application", Out::str(s.application));
	if (!s.image.empty()) versions.set("image", Out::str(s.image));
	if (!s.alpine.empty()) versions.set("alpine", Out::str(s.alpine));
	if (!s.kernel.empty()) versions.set("kernel", Out::str(s.kernel));
	versions.set("modules", item_json(s.modules));
	versions.set("boot", item_json(s.boot));
	Out packages = Out::array();
	for (const auto &p : s.packages) packages.add(package_json(p));
	versions.set("packages", std::move(packages));
	Out services = Out::array();
	for (const auto &svc : s.services) {
		Out o = Out::object();
		o.set("name", Out::str(svc.name));
		if (!svc.desired.empty()) o.set("desired", Out::str(svc.desired));
		for (auto &m : item_json(svc.item).members) o.set(m.first, std::move(m.second));
		services.add(std::move(o));
	}
	Out video = Out::object();
	video.set("enabled", Out::boolean(s.video.enabled));
	video.set("signal", Out::boolean(s.video.signal));
	if (!s.video.profile.empty()) video.set("profile", Out::str(s.video.profile));
	if (!s.video.input.empty()) video.set("input", Out::str(s.video.input));
	if (!s.video.output.empty()) video.set("output", Out::str(s.video.output));
	if (!s.video.measured_fps.empty()) video.set("measuredFps", Out::str(s.video.measured_fps));
	video.set("edidState", Out::str(s.video.edid_state));
	video.set("state", Out::str(s.video.state));
	Out usb = Out::object();
	usb.set("selected", Out::strings(s.usb_selected));
	usb.set("binding", item_json(s.usb_binding));
	Out apk = Out::object();
	apk.set("state", Out::str(s.apk_state));
	if (!s.apk_error_category.empty()) apk.set("errorCategory", Out::str(s.apk_error_category));
	Out chains = Out::array();
	for (const auto &c : s.hook_chains) chains.add(chain_json(c));
	Out firewall = Out::object();
	firewall.set("state", Out::str(s.firewall_state));
	firewall.set("hookChains", std::move(chains));
	if (!s.firewall_detail.empty()) firewall.set("detail", Out::str(s.firewall_detail));

	Out o = Out::object();
	o.set("collectedAt", Out::number(s.collected_at));
	o.set("versions", std::move(versions));
	o.set("services", std::move(services));
	o.set("video", std::move(video));
	o.set("usb", std::move(usb));
	o.set("apk", std::move(apk));
	o.set("firewall", std::move(firewall));
	return notify::json::marshal(o);
}

std::string report_json(const Snapshot &s, bool indent)
{
	Out o = report(s);
	return indent ? notify::json::marshal_indent(o) : notify::json::marshal(o);
}

// ---- routes ----

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/diagnostics", auth::guarded([](http::Request &req, http::Response &res) {
		if (auto *s = fresh_snapshot()) return api::send(res, 0, "success", snapshot_json(*s));
		res.pending = true;
		read_snapshot([id = req.id, headers = res.headers](const Snapshot &s) {
			http::Response out;
			out.headers = headers;
			api::send(out, 0, "success", snapshot_json(s));
			http::complete(id, std::move(out));
		});
	}, admin));

	// An issue-ready report as an attachment: the user decides where it goes.
	router.add("GET", "/api/vm/diagnostics/report", auth::guarded([](http::Request &req, http::Response &res) {
		auto answer = [](http::Response &out, const Snapshot &s) {
			out.set_header("Content-Disposition", "attachment; filename=nanokvm-diagnostics.json");
			out.json(200, report_json(s, false));
		};
		if (auto *s = fresh_snapshot()) return answer(res, *s);
		res.pending = true;
		read_snapshot([id = req.id, headers = res.headers, answer](const Snapshot &s) {
			http::Response out;
			out.headers = headers;
			answer(out, s);
			http::complete(id, std::move(out));
		});
	}, admin));

	router.add("GET", "/api/vm/diagnostics/archive", auth::guarded([](http::Request &req, http::Response &res) {
		if (archive_busy.exchange(true)) return api::err(res, -1, "a developer archive is already being prepared");
		struct stat st{};
		if (::stat(paths().archive_script.c_str(), &st) != 0) {
			archive_busy = false;
			return api::err(res, -1, "update nanokvm-base to prepare a developer archive");
		}
		res.pending = true;
		auto headers = res.headers;
		auto fail = [id = req.id, headers](std::string msg) {
			http::Response out;
			out.headers = headers;
			api::err(out, -1, msg);
			archive_busy = false;
			http::complete(id, std::move(out));
		};
		// The collector (up to 90 s, on a blocking thread), then the snapshot (loop thread), then packing.
		loop::blocking([id = req.id, headers, fail] {
			// os.MkdirTemp("", "nanokvm-dev-archive-").
			const char *tmp = std::getenv("TMPDIR");
			std::string dir = go::join(tmp && *tmp ? tmp : "/tmp", "nanokvm-dev-archive-XXXXXX");
			if (!::mkdtemp(dir.data())) return fail("cannot prepare the developer archive");
			// A partial collection is still useful: pack what exists and say why.
			std::string note;
			auto r = run_command({paths().archive_script, "collect", dir}, 64 * 1024, Clock::now() + std::chrono::seconds(90));
			if (!r.error.empty()) note = "\nThe collection did not finish: " + r.error + "\n";
			loop::post([id, headers, fail, dir, note] {
				read_snapshot([id, headers, fail, dir, note](const Snapshot &s) {
					loop::blocking([id, headers, fail, dir, note, report = report_json(s, true)] {
						auto now = std::chrono::system_clock::now().time_since_epoch();
						auto sec = std::chrono::duration_cast<std::chrono::seconds>(now);
						auto nsec = std::chrono::duration_cast<std::chrono::nanoseconds>(now - sec);
						std::string error;
						auto data = pack(dir, report, note, sec.count(), static_cast<std::int32_t>(nsec.count()), error);
						go::remove_all(dir);
						if (!data) return fail(error);
						auto stamp = std::chrono::duration_cast<std::chrono::seconds>(
						                 std::chrono::system_clock::now().time_since_epoch()).count();
						http::Response out;
						out.headers = headers;
						out.set_header("Cache-Control", "no-store");
						out.set_header("Content-Disposition",
						               "attachment; filename=\"nanokvm-dev-archive-" + local_stamp(stamp) + ".tar.gz\"");
						out.status = 200;
						out.content_type = "application/gzip";
						out.body = std::move(*data);
						archive_busy = false;
						http::complete(id, std::move(out));
					});
				});
			});
		});
	}, admin));
}

} // namespace nkos::system::diagnostics
