// Preview, staging and status of a settings restore or reset, as the Go server's recovery/engine.go and boot.go
// (the parts the server runs; Resume runs in nkos-update recovery-resume at boot).

#include "recovery/internal.hpp"
#include "recovery/yaml_v3.hpp"

#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "osupdate/files.hpp"
#include "osupdate/godecode.hpp"
#include "osupdate/gostr.hpp"
#include "osupdate/osupdate.hpp"

#include <openssl/sha.h>
#include <sys/stat.h>

#include <algorithm>
#include <cerrno>
#include <climits>
#include <cstdio>
#include <ctime>
#include <map>
#include <set>

namespace nkos::recovery {

namespace {

constexpr const char *status_path = "var/lib/nanokvm-recovery/status.json";

const std::vector<std::string> core_packages{
	"nanokvm", "nanokvm-web", "nanokvm-app", "nanokvm-release", "nanokvm-base", "nanokvm-keys", "nanokvm-kernel",
	"nanokvm-kmod", "nanokvm-firmware", "nanokvm-bootloader", "nanokvm-zlib", "nanokvm-openssl", "nanokvm-exfatprogs",
	"nanokvm-i2c-tools", "nanokvm-tpu", "nanokvm-kernel-sg2002", "nanokvm-kmod-sg2002", "nanokvm-firmware-sg2002",
	"nanokvm-bootloader-sg2002"};

std::string strings_json(const std::optional<std::vector<std::string>> &list)
{
	if (!list) return "null";
	std::string out = "[";
	for (std::size_t i = 0; i < list->size(); i++) out += (i ? "," : "") + gojson::quote((*list)[i]);
	return out + "]";
}

std::string file_json(const std::optional<File> &f) { return f ? to_json(*f) : "null"; }

// time.Now().UTC() as encoding/json writes a time.Time (RFC 3339 with nanoseconds, trailing zeros dropped).
std::string now_rfc3339()
{
	timespec ts{};
	clock_gettime(CLOCK_REALTIME, &ts);
	std::tm tm{};
	gmtime_r(&ts.tv_sec, &tm);
	char buf[64];
	std::snprintf(buf, sizeof buf, "%04d-%02d-%02dT%02d:%02d:%02d", tm.tm_year + 1900, tm.tm_mon + 1, tm.tm_mday, tm.tm_hour,
	              tm.tm_min, tm.tm_sec);
	std::string out = buf;
	if (ts.tv_nsec) {
		char frac[16];
		std::snprintf(frac, sizeof frac, "%09ld", static_cast<long>(ts.tv_nsec));
		std::string f = frac;
		while (!f.empty() && f.back() == '0') f.pop_back();
		out += "." + f;
	}
	return out + "Z";
}

long long atoi_clamped(const std::string &digits)
{
	long long v = 0;
	for (char c : digits) {
		if (v > (LLONG_MAX - (c - '0')) / 10) return LLONG_MAX;
		v = v * 10 + (c - '0');
	}
	return v;
}

// The same or a newer version of the same major series.
bool compatible(const std::string &from, const std::string &to)
{
	std::string a1, a2, a3, b1, b2, b3;
	if (!version_pattern(from, a1, a2, a3) || !version_pattern(to, b1, b2, b3) || a1 != b1) return false;
	for (auto [x, y] : {std::pair{atoi_clamped(a2), atoi_clamped(b2)}, std::pair{atoi_clamped(a3), atoi_clamped(b3)}})
		if (x != y) return x < y;
	return true;
}

std::optional<std::vector<std::string>> removable(const std::string &world, Error &error)
{
	// nanokvm-release keeps the complete OS dependency closure installed. Never --recursive: removing optional world
	// roots must not remove dependents.
	std::set<std::string> protected_names{"nanokvm-release", "alpine-base", "alpine-baselayout", "alpine-keys", "apk-tools",
	                                      "busybox", "libc-utils"};
	protected_names.insert(core_packages.begin(), core_packages.end());
	std::vector<std::string> result;
	bool release = false;
	for (const auto &line : gostr::fields(world)) {
		auto parts = gostr::fields_ascii(line, "<>=~@");
		if (parts.empty() || !package_name(parts[0])) return error = "unsupported APK world entry", std::nullopt;
		const std::string &name = parts[0];
		if (name == "nanokvm-release") release = true;
		if (protected_names.count(name)) continue;
		result.push_back(name);
	}
	if (!release) return error = "factory package reset requires the nanokvm-release system package", std::nullopt;
	std::sort(result.begin(), result.end());
	return result;
}

bool same_file(const std::optional<File> &a, const std::optional<File> &b) { return file_json(a) == file_json(b); }

Error validate_plan(const Plan &p)
{
	if ((p.options.action != "restore" && p.options.action != "reset") || (p.rolling_back && !p.started)) return "invalid recovery journal";
	std::size_t n = p.changes ? p.changes->size() : 0;
	if (n > max_files + 8) return "excessive recovery changes";
	std::set<std::string> seen;
	std::size_t total = 0;
	if (p.changes)
		for (const auto &c : *p.changes) {
			if ((!allowed(c.path) && c.path != "etc/shadow" && c.path != "etc/kvm/.jwt_secret" && c.path != "etc/kvm/.picoclaw_internal_token") ||
			    seen.count(c.path))
				return "invalid recovery destination";
			seen.insert(c.path);
			for (const auto *f : {&c.before, &c.after})
				if (*f) {
					std::size_t size = (*f)->data ? (*f)->data->size() : 0;
					total += size;
					if ((*f)->path != c.path || size > max_file || ((*f)->mode & ~0777u) != 0) return "invalid recovery file";
				}
		}
	if (total > 2 * max_data + 2 * max_file) return "excessive recovery data";
	if (p.world && !p.world->empty()) {
		Error e;
		auto names = removable(*p.world, e);
		if (!names) return e;
		if (strings_json(*names) != strings_json(p.remove_packages) || p.options.action != "reset" || p.options.keep_apps)
			return "invalid package reset";
	} else if (p.remove_packages && !p.remove_packages->empty()) {
		return "missing package reset inventory";
	}
	return "";
}

} // namespace

std::string to_json(const Plan &p)
{
	std::string out = "{";
	if (p.rolling_back) out += "\"rollingBack\":true,";
	out += "\"started\":" + std::string(p.started ? "true" : "false");
	out += ",\"options\":{\"action\":" + gojson::quote(p.options.action) + ",\"mode\":" + gojson::quote(p.options.mode) +
	       ",\"keepUsers\":" + (p.options.keep_users ? "true" : "false") + ",\"keepApps\":" + (p.options.keep_apps ? "true" : "false") + "}";
	out += ",\"changes\":";
	if (!p.changes) out += "null";
	else {
		out += "[";
		for (std::size_t i = 0; i < p.changes->size(); i++) {
			const auto &c = (*p.changes)[i];
			out += (i ? ",{" : "{") + std::string("\"path\":") + gojson::quote(c.path) + ",\"before\":" + file_json(c.before) +
			       ",\"after\":" + file_json(c.after) + "}";
		}
		out += "]";
	}
	out += ",\"removePackages\":" + strings_json(p.remove_packages);
	out += ",\"world\":" + (p.world ? gojson::quote(base64::encode(*p.world)) : std::string("null"));
	return out + "}";
}

std::string to_json(const Preview &v)
{
	return "{\"token\":" + gojson::quote(v.token) + ",\"changed\":" + strings_json(v.changed) + ",\"skipped\":" + strings_json(v.skipped) +
	       ",\"removePackages\":" + strings_json(v.remove_packages) + ",\"packages\":" + strings_json(v.packages) +
	       ",\"version\":" + gojson::quote(v.version) + "}";
}

std::string to_json(const Status &s) { return "{\"state\":" + gojson::quote(s.state) + ",\"message\":" + gojson::quote(s.message) + "}"; }

std::optional<Archive> Engine::snapshot(Error &error) const
{
	Archive a;
	a.format = 1;
	a.version = version;
	a.board = board;
	a.created = now_rfc3339();
	auto files = inventory(root, error);
	if (!files) return std::nullopt;
	a.files = std::move(*files);
	auto world = read(root, "etc/apk/world", max_file, error);
	if (!error.empty()) return std::nullopt;
	if (world) {
		for (const auto &line : gostr::fields(world->data.value_or(""))) {
			auto parts = gostr::fields_ascii(line, "<>=~@");
			if (parts.empty()) return error = "invalid APK world", std::nullopt;
			if (!package_name(parts[0])) return error = "unsupported package name in world", std::nullopt;
			if (!a.packages) a.packages = std::vector<std::string>{};
			a.packages->push_back(parts[0]);
		}
	}
	auto shadow = read(root, "etc/shadow", max_file, error);
	if (!error.empty()) return std::nullopt;
	if (shadow)
		for (const auto &line : gostr::split(shadow->data.value_or(""), "\n")) {
			auto fields = gostr::split(line, ":");
			if (fields.size() > 1 && fields[0] == "root") a.root_hash = fields[1];
		}
	if (auto e = validate(a); !e.empty()) return error = e, std::nullopt;
	return a;
}

bool Engine::prepare(const Archive *a, const Options &o, Plan &p, Preview &v, Error &error) const
{
	p = Plan{};
	p.options = o;
	v = Preview{};
	v.packages = std::vector<std::string>{};
	if (o.action != "restore" && o.action != "reset") return error = "invalid action", false;
	if (o.action == "restore") {
		if (!a) return error = "archive required", false;
		if (auto e = validate(*a); !e.empty()) return error = e, false;
		if (!compatible(a->version, version))
			return error = "install the same or a newer application version in the same major series before restoring", false;
		if (o.mode != "device" && o.mode != "migrate") return error = "invalid restore mode", false;
		if (o.mode == "device" && (a->board.empty() || a->board != board))
			return error = "board profile differs or is unknown; use migration mode", false;
		v.version = a->version;
		v.packages = a->packages;
	}
	auto current = inventory(root, error);
	if (!current) return false;
	// desired: nullopt deletes the file.
	std::map<std::string, std::optional<File>> desired;
	for (const auto &f : *current) {
		if (o.action == "reset" && o.keep_users && (users(f.path) || f.path == "etc/kvm/ssh_stop" || f.path.rfind("etc/ssh/", 0) == 0)) continue;
		if (o.action == "restore" && o.mode == "migrate" && !portable(f.path)) continue;
		desired[f.path] = std::nullopt;
	}
	if (o.action == "restore" && a->files) {
		for (File f : *a->files) {
			if (o.mode == "migrate" && !portable(f.path)) {
				v.skipped.push_back(f.path);
				continue;
			}
			// Private files with broad permissions from FAT or old images are not copied as such.
			f.mode = 0600;
			if (f.path == "etc/hosts" || f.path == "etc/hostname" || f.path == "etc/timezone" || f.path == "etc/localtime" ||
			    f.path == "etc/chrony.conf")
				f.mode = 0644;
			if (f.path.rfind("etc/kvm/scripts/", 0) == 0) f.mode = 0700;
			desired[f.path] = f;
		}
	}
	auto put = [&](const std::string &name, const std::string &value, std::uint32_t mode) { desired[name] = File{name, value, mode}; };
	// The layout marker is an invariant, not a user setting.
	if (o.action == "reset" || o.mode == "device") put("etc/kvm/usb/.layout-v1", "1\n", 0600);
	// Every web session issued before is invalidated, custom JWT keys included.
	if (o.action == "restore" && o.mode == "migrate") {
		auto cfg = read(root, "etc/kvm/server.yaml", max_file, error);
		if (!error.empty()) return false;
		if (cfg) desired[cfg->path] = *cfg;
	}
	desired["etc/kvm/.jwt_secret"] = std::nullopt;
	if (o.action == "reset" || o.mode == "device") desired["etc/kvm/.picoclaw_internal_token"] = std::nullopt;
	if (auto it = desired.find("etc/kvm/server.yaml"); it != desired.end() && it->second) {
		bool panicked = false;
		auto out = yamlv3::reset_web_settings(it->second->data.value_or(""), error, panicked);
		if (!out) return false;
		it->second->data = *out;
	}
	if (o.action == "reset") {
		// Unrelated cron jobs stay; only the automatic index refresh goes.
		auto cron = read(root, "etc/kvm/cron/crontabs/root", max_file, error);
		if (!error.empty()) return false;
		if (cron) {
			std::vector<std::string> lines;
			for (const auto &line : gostr::split(cron->data.value_or(""), "\n")) {
				auto fields = gostr::fields(line);
				if (line.rfind("# nanokvm-apk-index-refresh ", 0) == 0 ||
				    (fields.size() == 7 && fields[5] == "/usr/sbin/nkos-update" && fields[6] == "refresh-indexes"))
					continue;
				lines.push_back(line);
			}
			put("etc/kvm/cron/crontabs/root", gostr::join(lines, "\n"), 0600);
		}
		// S10uuid needs these files to exist before it applies the chip hostname.
		put("etc/hostname", "nanokvm\n", 0644);
		put("etc/hosts", "127.0.0.1 localhost nanokvm\n::1 localhost\n", 0644);
		put("etc/timezone", "UTC\n", 0644);
		int err = 0;
		std::string tz_path = root + "/usr/share/zoneinfo/UTC";
		auto tz = osupdate::files::read(tz_path, err);
		if (!tz) return error = osupdate::files::read_error(tz_path, err), false;
		put("etc/localtime", *tz, 0644);
		put("etc/chrony.conf",
		    "pool pool.ntp.org iburst\ndriftfile /var/lib/chrony/drift\nmakestep 1.0 3\nrtcsync\nport 0\ncmdport 0\nbindcmdaddress /run/chrony/chronyd.sock\n",
		    0644);
		if (!o.keep_users) put("etc/kvm/ssh_stop", "", 0600);
	}
	// Root alone is merged into shadow; service accounts belong to APK.
	if ((o.action == "reset" && !o.keep_users) || (o.action == "restore" && o.mode == "device" && !a->root_hash.empty())) {
		auto shadow = read(root, "etc/shadow", max_file, error);
		if (!error.empty()) return false;
		if (!shadow) return error = "root password database missing", false;
		std::string hash = a ? a->root_hash : "!";
		auto lines = gostr::split(shadow->data.value_or(""), "\n");
		bool found = false;
		for (auto &line : lines) {
			auto fields = gostr::split(line, ":");
			if (fields.size() >= 2 && fields[0] == "root") {
				fields[1] = hash;
				line = gostr::join(fields, ":");
				found = true;
			}
		}
		if (!found) return error = "root account missing", false;
		put("etc/shadow", gostr::join(lines, "\n"), 0600);
	}
	if (o.action == "reset" && !o.keep_apps) {
		auto world = read(root, "etc/apk/world", max_file, error);
		if (!error.empty()) return false;
		if (!world) return error = "APK world missing", false;
		p.world = world->data;
		auto names = removable(world->data.value_or(""), error);
		if (!names) return false;
		p.remove_packages = *names;
		v.remove_packages = *names;
	}
	for (const auto &[name, after] : desired) {
		auto before = read(root, name, max_file, error);
		if (!error.empty()) return error = name + ": " + error, false;
		if (!before && !after) continue;
		if (!p.changes) p.changes = std::vector<Change>{};
		p.changes->push_back(Change{name, before, after});
		if (!before || !after || before->data.value_or("") != after->data.value_or("") || before->mode != after->mode)
			v.changed.push_back(name);
	}
	std::string data = to_json(p);
	unsigned char digest[SHA256_DIGEST_LENGTH];
	SHA256(reinterpret_cast<const unsigned char *>(data.data()), data.size(), digest);
	static const char hex[] = "0123456789abcdef";
	for (unsigned char c : digest) v.token += hex[c >> 4], v.token += hex[c & 15];
	return true;
}

bool Engine::pending() const
{
	struct stat st{};
	return ::lstat((root + "/" + pending_path).c_str(), &st) == 0 || errno != ENOENT;
}

Status Engine::status() const
{
	if (pending()) return Status{"pending", "Recovery is queued; reboot to apply it."};
	Error error;
	auto f = read(root, status_path, 4096, error);
	if (!error.empty()) return Status{"error", error};
	Status s{"idle", ""};
	if (f)
		if (auto doc = godecode::parse_document(f->data.value_or("")); doc && doc->kind == godecode::Value::Kind::object)
			for (const auto &[k, val] : doc->members) {
				if (godecode::key_matches(k, "state")) godecode::get(val, s.state);
				else if (godecode::key_matches(k, "message")) godecode::get(val, s.message);
			}
	return s;
}

Error Engine::stage(const Plan &p) const
{
	if (pending()) return "a recovery operation is already pending";
	if (auto e = validate_plan(p); !e.empty()) return e;
	if (p.changes)
		for (const auto &c : *p.changes) {
			Error error;
			auto f = read(root, c.path, max_file, error);
			if (!error.empty()) return error;
			if (!same_file(f, c.before)) return "settings changed since preview; preview again";
		}
	std::string data = to_json(p);
	if (data.size() > 2 * max_archive) return "recovery journal too large";
	File journal{"", data, 0600};
	return write(root, pending_path, &journal);
}

Engine live()
{
	Engine e;
	e.root = "/";
	e.version = osupdate::installed_version();
	e.board = gostr::trim_space(osupdate::files::read("/etc/kvm/hw").value_or(""));
	return e;
}

bool available()
{
	auto data = osupdate::files::read("/etc/init.d/nanokvm-board");
	return data && data->find("nkos-update recovery-resume") != std::string::npos;
}

Error reboot()
{
	Error e;
	if (!osupdate::start_detached({osupdate::helper, "recovery-reboot"}, -1, -1, e)) return e;
	return "";
}

} // namespace nkos::recovery
