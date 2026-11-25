// Settings archives and their policy, as the Go server's recovery/archive.go and policy.go.

#include "recovery/internal.hpp"
#include "recovery/yaml_v3.hpp"

#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "osupdate/godecode.hpp"
#include "osupdate/gostr.hpp"

#include <openssl/evp.h>
#include <openssl/rand.h>

#include <algorithm>
#include <cstring>
#include <set>

namespace nkos::recovery {

namespace {

constexpr std::string_view magic = "NKOSBACKUP1\n";
constexpr int iterations = 600000;

// Paths relative to the root. Hardware detection records, kernel and boot binaries, package databases, disks and
// arbitrary /etc are excluded.
const char *const settings_text = R"(
 etc/kvm/frame_detect etc/kvm/encoder_codec etc/kvm/input-region.json etc/kvm/leader-key
 etc/kvm/mouse-jiggler etc/kvm/oled_sleep etc/kvm/oled_layout etc/kvm/memory.conf
 etc/kvm/web-title etc/kvm/update-check.disabled etc/kvm/cpufreq etc/kvm/cpufreq.boot
 etc/kvm/monitor_resolution etc/kvm/monitor_portrait
 etc/kvm/monitor_portrait_resolution etc/kvm/monitor_edid_sha256 etc/kvm/monitor_identity
 etc/kvm/shortcuts.json etc/kvm/server.yaml etc/kvm/ssh_stop etc/kvm/mdns_disabled
 etc/kvm/hevc_vi_direct etc/kvm/watermark.json etc/kvm/hdmi_disable etc/kvm/hdmi_idle_timeout etc/kvm/atx etc/kvm/screen-text-off
 etc/kvm/rtsp.json
 etc/kvm/mcp.json etc/kvm/ai-control.mode etc/kvm/autostart.list etc/kvm/host-sensors
 etc/kvm/wifi.ssid etc/kvm/wifi.pass etc/kvm/wifi.disabled etc/kvm/wifi.band_preference etc/kvm/wifi.country
 etc/kvm/wifi.ap.enabled etc/kvm/wifi.ap.ssid etc/kvm/wifi.ap.pass etc/kvm/wifi.ap.band etc/kvm/wifi.ap.channel
 etc/kvm/wifi.ap.hidden etc/kvm/wifi.ap.subnet etc/kvm/wifi.ap.internet etc/kvm/wifi.ap.dns
 etc/kvm/usb-before-rustdesk.json
 etc/kvm/picoclaw-runtime.json etc/kvm/pwd
 kvmapp/kvm/fps kvmapp/kvm/qlty kvmapp/kvm/res kvmapp/kvm/type
 kvmapp/kvm/transport kvmapp/kvm/mjpeg_chroma kvmapp/kvm/ap.pass
 boot/hostname.prefix boot/eth.mac boot/eth.disabled boot/eth.nodhcp boot/eth.vlan boot/hostname
 boot/wifi.ssid boot/wifi.pass boot/wifi.disabled boot/resolv.conf
 boot/resolv.conf.manual.bak boot/sshd_config
 etc/kvm/cron/crontabs/root etc/kvm/cache/wol etc/hostname etc/hosts etc/timezone etc/localtime etc/chrony.conf etc/kvm/date-time.json
 root/.ssh/authorized_keys
 root/.picoclaw/config.json root/.picoclaw/.security.yml
 etc/ssh/ssh_host_ed25519_key etc/ssh/ssh_host_ed25519_key.pub
 etc/ssh/ssh_host_rsa_key etc/ssh/ssh_host_rsa_key.pub
 etc/ssh/ssh_host_ecdsa_key etc/ssh/ssh_host_ecdsa_key.pub
 etc/kvm/server.crt etc/kvm/server.key etc/kvm/bmc/config.json
 etc/kvm/acme/settings.json var/lib/tailscale/tailscaled.state etc/netbird/config.json
 var/lib/zerotier-one/identity.secret var/lib/zerotier-one/identity.public var/lib/zerotier-one/local.conf
 etc/kvm/notify/config.json etc/kvm/notify/addresses.json
)";

// Settings that a later release added to the backup, by group. An archive lists the groups it covers. Restoring one
// made before a group existed leaves that group's files on the device as they are: their absence from the archive
// does not mean they were absent from the device.
struct SettingsGroup {
	const char *name;
	std::vector<std::string> prefixes;
	// A backup covers the group only when the device had one of these files.
	std::vector<std::string> markers;
};

// Sorted by name (allGroups). Prefixes do not overlap, so the order of lookup does not matter.
const std::vector<SettingsGroup> &groups()
{
	static const std::vector<SettingsGroup> list{
		// The RTSP stream (on/off, port, password); a backup from before it leaves the device's as it is.
		{"rtsp", {"etc/kvm/rtsp.json"}, {"etc/kvm/rtsp.json"}},
		// The node identity and the networks joined with it. Without identity.secret ZeroTier had not started:
		// network files then are only preseeded join requests, and restoring them would delete the identity and
		// networks made since.
		{"zerotier", {"var/lib/zerotier-one/identity.", "var/lib/zerotier-one/networks.d/"}, {"var/lib/zerotier-one/identity.secret"}},
		// Configuration, which does not depend on the identity: covered once there is one, or once ZeroTier started
		// (it then had none).
		{"zerotier-config", {"var/lib/zerotier-one/local.conf"}, {"var/lib/zerotier-one/local.conf", "var/lib/zerotier-one/identity.secret"}},
	};
	return list;
}

const SettingsGroup *find_group(const std::string &name)
{
	for (const auto &g : groups())
		if (name == g.name) return &g;
	return nullptr;
}

std::string json_bytes(const std::optional<std::string> &data) { return data ? gojson::quote(base64::encode(*data)) : "null"; }

bool alnum(char c) { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'); }

// ^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$ (authn.ValidateUsername)
bool valid_username(const std::string &s)
{
	if (s.empty() || s.size() > 32 || !alnum(s[0])) return false;
	for (char c : s)
		if (!alnum(c) && c != '_' && c != '.' && c != '-') return false;
	return true;
}

// The first UnmarshalTypeError of a decode, as Go words it.
struct TypeErrors {
	std::string first;
	void add(const godecode::Value &v, const std::string &context, const std::string &type, bool number_literal = false)
	{
		if (!first.empty()) return;
		std::string value = godecode::kind_name(v);
		if (number_literal && v.kind == godecode::Value::Kind::number) value = "number " + v.text;
		first = "json: cannot unmarshal " + value + " into " + context + " of type " + type;
	}
};

// Go's UnmarshalTypeError place: the top-level struct and the path from it (array elements by index); an
// element itself is named without "Go struct field".
std::string field(const std::string &path) { return "Go struct field database." + path; }
std::string element(const std::string &path) { return "database." + path; }

// authn.ValidateRecoveryAccounts: json.Unmarshal into the account file's struct, then validateDatabase.
Error validate_accounts(const std::string &data)
{
	std::string error;
	auto doc = godecode::unmarshal(data, error);
	if (!doc) return error;
	TypeErrors te;
	std::int64_t version = 0;
	struct U {
		std::string username, password, role;
		bool enabled = false, system = false;
		std::uint64_t token = 0;
	};
	std::vector<U> list;
	using K = godecode::Value::Kind;
	if (doc->kind == K::object) {
		for (const auto &[k, v] : doc->members) {
			if (godecode::key_matches(k, "version")) {
				if (!godecode::get_int(v, version, INT64_MIN, INT64_MAX)) te.add(v, field("version"), "int", true);
			} else if (godecode::key_matches(k, "users")) {
				if (v.kind == K::null) {
					list.clear();
				} else if (v.kind != K::array) {
					te.add(v, field("users"), "[]authn.User");
				} else {
					std::vector<U> fresh;
					for (std::size_t i = 0; i < v.items.size(); i++) {
						U u = i < list.size() ? list[i] : U{};
						const auto &item = v.items[i];
						const std::string at = "users." + std::to_string(i);
						if (item.kind == K::object) {
							for (const auto &[uk, uv] : item.members) {
								auto str = [&](const char *name, std::string &out, const char *type) {
									if (!godecode::get(uv, out)) te.add(uv, field(at + "." + name), type);
								};
								if (godecode::key_matches(uk, "username")) str("username", u.username, "string");
								else if (godecode::key_matches(uk, "password")) str("password", u.password, "string");
								else if (godecode::key_matches(uk, "role")) str("role", u.role, "authn.Role");
								else if (godecode::key_matches(uk, "enabled")) {
									if (!godecode::get(uv, u.enabled)) te.add(uv, field(at + ".enabled"), "bool");
								} else if (godecode::key_matches(uk, "tokenVersion")) {
									if (!godecode::get_uint(uv, u.token, UINT64_MAX)) te.add(uv, field(at + ".tokenVersion"), "uint64", true);
								} else if (godecode::key_matches(uk, "mustChangePassword")) {
									bool b = false;
									if (!godecode::get(uv, b)) te.add(uv, field(at + ".mustChangePassword"), "bool");
								} else if (godecode::key_matches(uk, "systemAccount")) {
									if (!godecode::get(uv, u.system)) te.add(uv, field(at + ".systemAccount"), "bool");
								} else if (godecode::key_matches(uk, "totp")) {
									if (uv.kind == K::object) {
										for (const auto &[tk, tv] : uv.members) {
											if (godecode::key_matches(tk, "secret")) {
												std::string s;
												if (!godecode::get(tv, s)) te.add(tv, field(at + ".totp.secret"), "string");
											} else if (godecode::key_matches(tk, "lastStep")) {
												std::int64_t x = 0;
												if (!godecode::get_int(tv, x, INT64_MIN, INT64_MAX)) te.add(tv, field(at + ".totp.lastStep"), "int64", true);
											} else if (godecode::key_matches(tk, "recoveryHashes")) {
												std::optional<std::vector<std::string>> h;
												if (tv.kind != K::null && tv.kind != K::array) te.add(tv, field(at + ".totp.recoveryHashes"), "[]string");
												else if (!godecode::get_strings(tv, h)) {
													for (std::size_t x = 0; x < tv.items.size(); x++)
														if (tv.items[x].kind != K::string && tv.items[x].kind != K::null)
															te.add(tv.items[x], element(at + ".totp.recoveryHashes." + std::to_string(x)), "string");
												}
											}
										}
									} else if (uv.kind != K::null) {
										te.add(uv, field(at + ".totp"), "authn.TOTP");
									}
								}
							}
						} else if (item.kind != K::null) {
							te.add(item, element(at), "authn.User");
						}
						fresh.push_back(u);
					}
					list = std::move(fresh);
				}
			} else if (godecode::key_matches(k, "username") || godecode::key_matches(k, "password")) {
				std::string s;
				if (!godecode::get(v, s)) te.add(v, field(godecode::key_matches(k, "username") ? "username" : "password"), "string");
			}
		}
	} else if (doc->kind != K::null) {
		te.add(*doc, "Go value", "authn.database");
	}
	if (!te.first.empty()) return te.first;
	if (version != 1) return "unsupported account file version: " + std::to_string(version);
	if (list.empty()) return "account file contains no users";
	std::set<std::string> seen;
	int system = 0, admins = 0;
	for (const auto &u : list) {
		if (!valid_username(u.username)) return "username must be 1-32 characters and contain only letters, numbers, '.', '_' or '-'";
		if (u.password.empty() || (u.role != "admin" && u.role != "user")) return "account file contains an invalid user";
		if (!seen.insert(u.username).second) return "account file contains duplicate usernames";
		if (u.system) {
			system++;
			if (u.role != "admin" || !u.enabled || system > 1) return "the device owner account must remain an enabled administrator";
		}
		if (u.role == "admin" && u.enabled) admins++;
	}
	if (admins == 0) return "at least one enabled admin is required";
	return "";
}

std::string password_problem(const std::string &password)
{
	if (password.size() < 12 || password.size() > 256) return "backup password must be 12\xe2\x80\x93" "256 bytes";
	return "";
}

bool derive(const std::string &password, const unsigned char *salt, unsigned char key[32])
{
	return PKCS5_PBKDF2_HMAC(password.data(), static_cast<int>(password.size()), salt, 16, iterations, EVP_sha256(), 32, key) == 1;
}

// One file of an archive with DisallowUnknownFields; false on any decode error.
bool decode_file(const godecode::Value &v, File &f)
{
	using K = godecode::Value::Kind;
	if (v.kind == K::null) return true;
	if (v.kind != K::object) return false;
	bool ok = true;
	for (const auto &[k, m] : v.members) {
		if (godecode::key_matches(k, "path")) ok = godecode::get(m, f.path) && ok;
		else if (godecode::key_matches(k, "data")) ok = godecode::get_bytes(m, f.data) && ok;
		else if (godecode::key_matches(k, "mode")) {
			std::uint64_t mode = f.mode;
			ok = godecode::get_uint(m, mode, UINT32_MAX) && ok;
			f.mode = static_cast<std::uint32_t>(mode);
		} else return false;
	}
	return ok;
}

bool decode_archive(const godecode::Value &v, Archive &a)
{
	using K = godecode::Value::Kind;
	if (v.kind == K::null) return true;
	if (v.kind != K::object) return false;
	bool ok = true;
	for (const auto &[k, m] : v.members) {
		if (godecode::key_matches(k, "format")) ok = godecode::get_int(m, a.format, INT64_MIN, INT64_MAX) && ok;
		else if (godecode::key_matches(k, "version")) ok = godecode::get(m, a.version) && ok;
		else if (godecode::key_matches(k, "board")) ok = godecode::get(m, a.board) && ok;
		else if (godecode::key_matches(k, "created")) ok = godecode::get_time(m, a.created) && ok;
		else if (godecode::key_matches(k, "files")) {
			if (m.kind == K::null) {
				a.files.reset();
			} else if (m.kind != K::array) {
				ok = false;
			} else {
				std::vector<File> files;
				for (std::size_t i = 0; i < m.items.size(); i++) {
					File f = a.files && i < a.files->size() ? (*a.files)[i] : File{};
					ok = decode_file(m.items[i], f) && ok;
					files.push_back(std::move(f));
				}
				a.files = std::move(files);
			}
		} else if (godecode::key_matches(k, "packages")) ok = godecode::get_strings(m, a.packages) && ok;
		else if (godecode::key_matches(k, "rootHash")) ok = godecode::get(m, a.root_hash) && ok;
		else if (godecode::key_matches(k, "covers")) ok = decode_groups(m, a.covers) && ok;
		else return false;
	}
	return ok;
}

// Options.UnmarshalJSON: the plain fields, and for a reset explicit keepUsers and keepApps (exact keys, not null).
} // namespace

bool decode_options_value(const godecode::Value &v, Options &o)
{
	using K = godecode::Value::Kind;
	if (v.kind == K::null) return true;
	if (v.kind != K::object) return false;
	Options plain;
	bool ok = true;
	for (const auto &[k, m] : v.members) {
		if (godecode::key_matches(k, "action")) ok = godecode::get(m, plain.action) && ok;
		else if (godecode::key_matches(k, "mode")) ok = godecode::get(m, plain.mode) && ok;
		else if (godecode::key_matches(k, "keepUsers")) ok = godecode::get(m, plain.keep_users) && ok;
		else if (godecode::key_matches(k, "keepApps")) ok = godecode::get(m, plain.keep_apps) && ok;
	}
	if (!ok) return false;
	if (plain.action == "reset") {
		for (const char *key : {"keepUsers", "keepApps"}) {
			const godecode::Value *last = nullptr;
			for (const auto &[k, m] : v.members)
				if (k == key) last = &m;
			if (!last || last->kind == K::null) return false;
		}
	}
	o = plain;
	return true;
}

bool decode_request(const std::string &body, Request &r)
{
	std::string error;
	auto doc = godecode::decode_first(body, error);
	if (!doc) return false;
	using K = godecode::Value::Kind;
	if (doc->kind == K::null) return true;
	if (doc->kind != K::object) return false;
	bool ok = true;
	for (const auto &[k, m] : doc->members) {
		if (godecode::key_matches(k, "password")) ok = godecode::get(m, r.password) && ok;
		else if (godecode::key_matches(k, "archivePassword")) ok = godecode::get(m, r.archive_password) && ok;
		else if (godecode::key_matches(k, "archive")) ok = godecode::get_bytes(m, r.archive) && ok;
		else if (godecode::key_matches(k, "options")) ok = decode_options_value(m, r.options) && ok;
		else if (godecode::key_matches(k, "token")) ok = godecode::get(m, r.token) && ok;
	}
	return ok;
}

bool decode_options(const std::string &json, Options &o)
{
	auto doc = godecode::parse_document(json);
	return doc && decode_options_value(*doc, o);
}

std::optional<Archive> decode_manifest(const std::string &plain, Error &error)
{
	godecode::Value v;
	std::size_t end = 0;
	Archive a;
	if (!godecode::parse(plain, v, end) || !decode_archive(v, a)) return error = "invalid archive manifest", std::nullopt;
	if (!godecode::only_space(std::string_view(plain).substr(end))) return error = "trailing archive data", std::nullopt;
	return a;
}

const std::vector<std::string> &settings_list()
{
	static const std::vector<std::string> list = gostr::fields(settings_text);
	return list;
}

const std::vector<std::string> &settings_trees()
{
	static const std::vector<std::string> trees{"etc/kvm/network", "etc/kvm/wifi.saved", "etc/kvm/usb", "etc/kvm/branding",
	                                            "etc/kvm/scripts", "etc/kvm/wireguard", "etc/kvm/openvpn", "etc/kvm/tls",
	                                            "etc/kvm/acme/data", "etc/nanokvm-rustdesk", "var/lib/zerotier-one/networks.d"};
	return trees;
}

std::string group(const std::string &p)
{
	for (const auto &g : groups())
		for (const auto &prefix : g.prefixes)
			if (p.rfind(prefix, 0) == 0) return g.name;
	return "";
}

std::vector<std::string> covered_groups(const std::vector<File> &files)
{
	std::vector<std::string> names;
	for (const auto &name : all_groups()) {
		const auto &markers = find_group(name)->markers;
		if (std::any_of(files.begin(), files.end(),
		                [&](const File &f) { return std::find(markers.begin(), markers.end(), f.path) != markers.end(); }))
			names.push_back(name);
	}
	return names;
}

std::vector<std::string> all_groups()
{
	std::vector<std::string> names;
	for (const auto &g : groups()) names.push_back(g.name);
	std::sort(names.begin(), names.end());
	return names;
}

Error valid_groups(const std::vector<std::string> &names)
{
	std::set<std::string> seen;
	for (const auto &name : names) {
		// Groups are only added: an unknown one comes from a later release.
		if (!find_group(name))
			return "the backup holds settings of a later application version; install the same or a newer version before restoring";
		if (!seen.insert(name).second) return "invalid settings group";
	}
	return "";
}

bool decode_groups(const godecode::Value &v, std::vector<std::string> &out)
{
	using K = godecode::Value::Kind;
	if (v.kind == K::null) {
		out.clear();
		return true;
	}
	if (v.kind != K::array) return false;
	bool ok = true;
	std::vector<std::string> list;
	for (std::size_t i = 0; i < v.items.size(); i++) {
		std::string s = i < out.size() ? out[i] : std::string();
		ok = godecode::get(v.items[i], s) && ok;
		list.push_back(std::move(s));
	}
	out = std::move(list);
	return ok;
}

bool package_name(const std::string &s)
{
	if (s.empty() || s.size() > 128) return false;
	if (!(alnum(s[0]) || s[0] == '_' || s[0] == '.')) return false;
	for (char c : s)
		if (!alnum(c) && c != '+' && c != '_' && c != '.' && c != '-') return false;
	return true;
}

bool version_pattern(const std::string &s, std::string &major, std::string &minor, std::string &patch)
{
	std::size_t i = 0;
	if (i < s.size() && s[i] == 'v') i++;
	auto digits = [&](std::string &out) {
		std::size_t start = i;
		while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		out = s.substr(start, i - start);
		return i > start;
	};
	if (!digits(major) || i >= s.size() || s[i++] != '.' || !digits(minor) || i >= s.size() || s[i++] != '.' || !digits(patch))
		return false;
	if (i == s.size()) return true;
	if (s[i] != '-' && s[i] != '.' && s[i] != '_') return false;
	i++;
	if (i == s.size()) return false;
	for (; i < s.size(); i++)
		if (!alnum(s[i]) && s[i] != '.' && s[i] != '-') return false;
	return true;
}

bool retired(const std::string &p) { return p == "etc/kvm/GOMEMLIMIT"; }

bool allowed(const std::string &p)
{
	if (p.empty() || go_clean(p) != p || p[0] == '/' || p.find_first_of(std::string("\\\0\r\n", 4)) != std::string::npos) return false;
	if (retired(p)) return true;
	for (const auto &s : settings_list())
		if (p == s) return true;
	for (const auto &s : settings_trees())
		if (p.rfind(s + "/", 0) == 0) return true;
	return false;
}

bool users(const std::string &p) { return p == "etc/kvm/pwd" || p == "root/.ssh/authorized_keys"; }

bool portable(const std::string &p)
{
	auto prefix = [&](const char *x) { return p.rfind(x, 0) == 0; };
	// A clone keeps its own credentials, identity, network and hardware policy (ATX depends on the board).
	if (users(p) || p == "etc/kvm/atx" || prefix("etc/ssh/") || prefix("boot/") || prefix("etc/kvm/wifi") || prefix("etc/kvm/network/") ||
	    prefix("etc/kvm/monitor_"))
		return false;
	for (const char *x : {"etc/kvm/wireguard/", "etc/kvm/openvpn/", "etc/kvm/tls/", "etc/kvm/acme/", "etc/nanokvm-rustdesk/",
	                      "var/lib/zerotier-one/", "root/.picoclaw/", "etc/kvm/usb/"})
		if (prefix(x)) return false;
	static const std::set<std::string> local{
		"etc/kvm/bmc/config.json", "etc/kvm/cron/crontabs/root", "etc/kvm/cache/wol", "etc/netbird/config.json",
		"var/lib/tailscale/tailscaled.state", "etc/kvm/acme/settings.json", "etc/kvm/server.yaml", "etc/kvm/server.crt",
		"etc/kvm/server.key", "etc/hostname", "etc/hosts", "etc/kvm/ssh_stop", "etc/kvm/usb-before-rustdesk.json",
		"etc/kvm/mcp.json", "etc/kvm/autostart.list", "etc/kvm/picoclaw-runtime.json", "etc/kvm/rtsp.json",
		// Notification channels hold tokens and passwords; the addresses are this device's login history.
		"etc/kvm/notify/config.json", "etc/kvm/notify/addresses.json"};
	if (local.count(p)) return false;
	return !prefix("etc/kvm/scripts/");
}

Error validate(const Archive &a)
{
	std::string major, minor, patch;
	if (a.format != 1 || !version_pattern(a.version, major, minor, patch)) return "unsupported archive format or application version";
	std::size_t nfiles = a.files ? a.files->size() : 0, npackages = a.packages ? a.packages->size() : 0;
	if (nfiles > max_files || npackages > 2048 || a.board.size() > 128) return "archive inventory too large";
	if (a.root_hash.size() > 1024 || a.root_hash.find_first_of(std::string(":\r\n\0", 4)) != std::string::npos)
		return "invalid root password record";
	if (auto e = valid_groups(a.covers); !e.empty()) return e;
	std::size_t total = 0;
	std::set<std::string> seen;
	if (a.files)
		for (const auto &f : *a.files) {
			std::size_t size = f.data ? f.data->size() : 0;
			total += size;
			if (!allowed(f.path) || seen.count(f.path) || size > max_file || total > max_data || (f.mode & ~0777u) != 0)
				return "invalid or excessive settings entry: " + f.path;
			seen.insert(f.path);
			if (auto g = group(f.path); !g.empty() && std::find(a.covers.begin(), a.covers.end(), g) == a.covers.end())
				return "settings entry outside the archive's groups: " + f.path;
			if (f.path == "etc/kvm/pwd")
				if (auto e = validate_accounts(f.data.value_or("")); !e.empty()) return e;
			if (f.path == "etc/kvm/server.yaml" && !yamlv3::valid_ports(f.data.value_or(""))) return "invalid server configuration";
		}
	if (a.packages)
		for (const auto &s : *a.packages)
			if (!package_name(s)) return "invalid package inventory";
	return "";
}

std::string to_json(const File &f)
{
	return "{\"path\":" + gojson::quote(f.path) + ",\"data\":" + json_bytes(f.data) + ",\"mode\":" + std::to_string(f.mode) + "}";
}

std::string to_json(const Archive &a)
{
	std::string out = "{\"format\":" + std::to_string(a.format) + ",\"version\":" + gojson::quote(a.version) + ",\"board\":" +
	                  gojson::quote(a.board) + ",\"created\":" + gojson::quote(a.created) + ",\"files\":";
	if (!a.files) out += "null";
	else {
		out += "[";
		for (std::size_t i = 0; i < a.files->size(); i++) out += (i ? "," : "") + to_json((*a.files)[i]);
		out += "]";
	}
	out += ",\"packages\":";
	if (!a.packages) out += "null";
	else {
		out += "[";
		for (std::size_t i = 0; i < a.packages->size(); i++) out += (i ? "," : "") + gojson::quote((*a.packages)[i]);
		out += "]";
	}
	if (!a.root_hash.empty()) out += ",\"rootHash\":" + gojson::quote(a.root_hash);
	if (!a.covers.empty()) {
		out += ",\"covers\":[";
		for (std::size_t i = 0; i < a.covers.size(); i++) out += (i ? "," : "") + gojson::quote(a.covers[i]);
		out += "]";
	}
	return out + "}";
}

std::optional<std::string> seal(const Archive &a, const std::string &password, Error &error)
{
	if (auto e = password_problem(password); !e.empty()) return error = e, std::nullopt;
	if (auto e = validate(a); !e.empty()) return error = e, std::nullopt;
	std::string data = to_json(a);
	unsigned char salt[16], nonce[12], key[32];
	if (RAND_bytes(salt, sizeof salt) != 1 || RAND_bytes(nonce, sizeof nonce) != 1) return error = "random source failed", std::nullopt;
	std::string header(magic);
	header.append(reinterpret_cast<char *>(salt), sizeof salt);
	header.append(reinterpret_cast<char *>(nonce), sizeof nonce);
	if (data.size() + header.size() + 16 > max_archive) {
		OPENSSL_cleanse(data.data(), data.size());
		return error = "archive exceeds encrypted size limit", std::nullopt;
	}
	if (!derive(password, salt, key)) return error = "key derivation failed", std::nullopt;
	std::string out = header;
	out.resize(header.size() + data.size() + 16);
	EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
	int len = 0, total = 0;
	bool ok = ctx && EVP_EncryptInit_ex(ctx, EVP_aes_256_gcm(), nullptr, nullptr, nullptr) == 1 &&
	          EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_SET_IVLEN, 12, nullptr) == 1 &&
	          EVP_EncryptInit_ex(ctx, nullptr, nullptr, key, nonce) == 1 &&
	          EVP_EncryptUpdate(ctx, nullptr, &len, reinterpret_cast<const unsigned char *>(header.data()), static_cast<int>(header.size())) == 1 &&
	          EVP_EncryptUpdate(ctx, reinterpret_cast<unsigned char *>(out.data() + header.size()), &len,
	                            reinterpret_cast<const unsigned char *>(data.data()), static_cast<int>(data.size())) == 1;
	total = len;
	ok = ok && EVP_EncryptFinal_ex(ctx, reinterpret_cast<unsigned char *>(out.data() + header.size() + total), &len) == 1;
	ok = ok && EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_GET_TAG, 16, out.data() + header.size() + data.size()) == 1;
	EVP_CIPHER_CTX_free(ctx);
	OPENSSL_cleanse(key, sizeof key);
	OPENSSL_cleanse(data.data(), data.size());
	if (!ok) return error = "encryption failed", std::nullopt;
	return out;
}

std::optional<Archive> open(const std::string &data, const std::string &password, Error &error)
{
	if (auto e = password_problem(password); !e.empty()) return error = e, std::nullopt;
	const std::size_t n = magic.size() + 16 + 12;
	if (data.size() > max_archive || data.size() < n + 16 || data.compare(0, magic.size(), magic) != 0)
		return error = "not a supported NanoKVM settings archive", std::nullopt;
	unsigned char key[32];
	if (!derive(password, reinterpret_cast<const unsigned char *>(data.data() + magic.size()), key))
		return error = "key derivation failed", std::nullopt;
	std::size_t body = data.size() - n - 16;
	std::string plain(body, '\0');
	EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
	int len = 0;
	bool ok = ctx && EVP_DecryptInit_ex(ctx, EVP_aes_256_gcm(), nullptr, nullptr, nullptr) == 1 &&
	          EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_SET_IVLEN, 12, nullptr) == 1 &&
	          EVP_DecryptInit_ex(ctx, nullptr, nullptr, key, reinterpret_cast<const unsigned char *>(data.data() + n - 12)) == 1 &&
	          EVP_DecryptUpdate(ctx, nullptr, &len, reinterpret_cast<const unsigned char *>(data.data()), static_cast<int>(n)) == 1 &&
	          EVP_DecryptUpdate(ctx, reinterpret_cast<unsigned char *>(plain.data()), &len,
	                            reinterpret_cast<const unsigned char *>(data.data() + n), static_cast<int>(body)) == 1 &&
	          EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_SET_TAG, 16, const_cast<char *>(data.data() + n + body)) == 1 &&
	          EVP_DecryptFinal_ex(ctx, reinterpret_cast<unsigned char *>(plain.data() + len), &len) == 1;
	EVP_CIPHER_CTX_free(ctx);
	OPENSSL_cleanse(key, sizeof key);
	if (!ok) {
		OPENSSL_cleanse(plain.data(), plain.size());
		return error = "incorrect password or damaged archive", std::nullopt;
	}
	struct Wipe {
		std::string &s;
		~Wipe() { OPENSSL_cleanse(s.data(), s.size()); }
	} wipe{plain};
	if (plain.size() > max_archive) return error = "archive too large", std::nullopt;
	auto a = decode_manifest(plain, error);
	if (!a) return std::nullopt;
	if (auto e = validate(*a); !e.empty()) return error = e, std::nullopt;
	return a;
}

} // namespace nkos::recovery
