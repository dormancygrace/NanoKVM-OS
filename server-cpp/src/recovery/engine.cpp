// Preview, staging and status of a settings restore or reset, as the Go server's recovery/engine.go and boot.go
// (the parts the server runs; Resume runs in nkos-update recovery-resume at boot).

#include "recovery/internal.hpp"
#include "recovery/yaml_v3.hpp"

#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "osupdate/files.hpp"
#include "osupdate/godecode.hpp"
#include "osupdate/gostr.hpp"
#include "osupdate/apkrun.hpp"
#include "osupdate/osupdate.hpp"

#include <dirent.h>

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
	"nanokvm-kmod", "nanokvm-firmware", "nanokvm-bootloader", "nanokvm-zlib", "nanokvm-openssl", "nanokvm-openssl3",
	"nanokvm-exfatprogs", "nanokvm-exfatprogs1", "nanokvm-i2c-tools", "nanokvm-tpu"};

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
	if (!valid_groups(p.covers).empty()) return "invalid recovery journal";
	std::set<std::string> seen;
	std::size_t total = 0;
	if (p.changes)
		for (const auto &c : *p.changes) {
			if (!p.covers_path(c.path)) return "recovery change outside the plan's settings groups";
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

bool Plan::covers_path(const std::string &name) const
{
	auto g = group(name);
	return g.empty() || std::find(covers.begin(), covers.end(), g) != covers.end();
}

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
	if (!p.covers.empty()) out += ",\"covers\":" + strings_json(p.covers);
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
	// A group the device had no state of is not covered, and its other files stay out: a backup made before ZeroTier
	// started says nothing about an identity made later, which no restore could bring back once deleted.
	a.covers = covered_groups(*a.files);
	std::erase_if(*a.files, [&](const File &f) {
		auto g = group(f.path);
		return !g.empty() && std::find(a.covers.begin(), a.covers.end(), g) == a.covers.end();
	});
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
		p.covers = a->covers;
	} else {
		p.covers = all_groups();
	}
	auto current = inventory(root, error);
	if (!current) return false;
	// desired: nullopt deletes the file.
	std::map<std::string, std::optional<File>> desired;
	for (const auto &f : *current) {
		if (o.action == "reset" && o.keep_users && (users(f.path) || f.path == "etc/kvm/ssh_stop" || f.path.rfind("etc/ssh/", 0) == 0)) continue;
		if (o.action == "restore" && o.mode == "migrate" && !portable(f.path)) continue;
		if (!p.covers_path(f.path)) continue;
		desired[f.path] = std::nullopt;
	}
	if (o.action == "restore" && a->files) {
		for (File f : *a->files) {
			if (retired(f.path)) continue;
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

namespace {

// json.Unmarshal into a Plan (a staged journal): Go's syntax error text, or a type error.
bool decode_file_value(const godecode::Value &v, std::optional<File> &out)
{
	using K = godecode::Value::Kind;
	if (v.kind == K::null) {
		out.reset();
		return true;
	}
	if (v.kind != K::object) return false;
	if (!out) out = File{};
	bool ok = true;
	for (const auto &[k, m] : v.members) {
		if (godecode::key_matches(k, "path")) ok = godecode::get(m, out->path) && ok;
		else if (godecode::key_matches(k, "data")) ok = godecode::get_bytes(m, out->data) && ok;
		else if (godecode::key_matches(k, "mode")) {
			std::uint64_t mode = out->mode;
			ok = godecode::get_uint(m, mode, UINT32_MAX) && ok;
			out->mode = static_cast<std::uint32_t>(mode);
		}
	}
	return ok;
}

bool decode_plan(const std::string &data, Plan &p, Error &error)
{
	auto doc = godecode::unmarshal(data, error);
	if (!doc) return false;
	using K = godecode::Value::Kind;
	if (doc->kind == K::null) return true;
	if (doc->kind != K::object) return error = "json: cannot unmarshal " + godecode::kind_name(*doc) + " into Go value of type recovery.Plan", false;
	bool ok = true;
	for (const auto &[k, m] : doc->members) {
		if (godecode::key_matches(k, "rollingBack")) ok = godecode::get(m, p.rolling_back) && ok;
		else if (godecode::key_matches(k, "started")) ok = godecode::get(m, p.started) && ok;
		else if (godecode::key_matches(k, "options")) {
			Options o = p.options;
			ok = decode_options_value(m, o) && ok;
			p.options = o;
		} else if (godecode::key_matches(k, "changes")) {
			if (m.kind == K::null) {
				p.changes.reset();
			} else if (m.kind != K::array) {
				ok = false;
			} else {
				std::vector<Change> list;
				for (std::size_t i = 0; i < m.items.size(); i++) {
					Change c = p.changes && i < p.changes->size() ? (*p.changes)[i] : Change{};
					const auto &item = m.items[i];
					if (item.kind == K::object) {
						for (const auto &[ck, cv] : item.members) {
							if (godecode::key_matches(ck, "path")) ok = godecode::get(cv, c.path) && ok;
							else if (godecode::key_matches(ck, "before")) ok = decode_file_value(cv, c.before) && ok;
							else if (godecode::key_matches(ck, "after")) ok = decode_file_value(cv, c.after) && ok;
						}
					} else if (item.kind != K::null) {
						ok = false;
					}
					list.push_back(std::move(c));
				}
				p.changes = std::move(list);
			}
		} else if (godecode::key_matches(k, "removePackages")) ok = godecode::get_strings(m, p.remove_packages) && ok;
		else if (godecode::key_matches(k, "covers")) ok = decode_groups(m, p.covers) && ok;
		else if (godecode::key_matches(k, "world")) ok = godecode::get_bytes(m, p.world) && ok;
	}
	if (!ok) error = "json: cannot unmarshal the recovery journal into recovery.Plan";
	return ok;
}

std::string truncate_3000(std::string s)
{
	if (s.size() > 3000) s.resize(3000);
	return s;
}

// remainingRemovals: a retry may find fewer optional roots, never new or changed ones.
std::optional<std::vector<std::string>> remaining_removals(const std::string &original, const std::string &current,
                                                           const std::vector<std::string> &approved, Error &error)
{
	if (!removable(current, error)) return std::nullopt;
	std::set<std::string> old;
	for (const auto &line : gostr::fields(original)) old.insert(line);
	std::set<std::string> present;
	for (const auto &line : gostr::fields(current)) {
		if (!old.count(line)) return error = "APK world changed after recovery was queued", std::nullopt;
		present.insert(gostr::fields_ascii(line, "<>=~@")[0]);
	}
	std::vector<std::string> result;
	for (const auto &name : approved)
		if (present.count(name)) result.push_back(name);
	return result;
}

} // namespace

Error Engine::resume(const std::function<Error(const std::vector<std::string> &)> &remove) const
{
	// Only a fully decoded, unstarted journal proves that settings are untouched; corrupt or started journals stay
	// pending (networking still permits repair).
	bool safe_to_abandon = false;
	auto finish = [&](Error result) -> Error {
		if (!result.empty() && safe_to_abandon) {
			std::string message = truncate_3000("Recovery cancelled before settings were changed: " + result);
			// abandon: a bounded failed journal replaces the previous one; it is never replayed.
			File status{"", to_json(Status{"failed", message}), 0600};
			Error e = write(root, status_path, &status);
			if (e.empty()) e = retire_pending(root);
			return e.empty() ? Error() : result + "\n" + e;
		}
		return result;
	};
	Error error;
	auto f = read(root, pending_path, 2 * max_archive, error);
	if (!error.empty()) return finish(error);
	if (!f) return "";
	Plan p;
	if (!decode_plan(f->data.value_or(""), p, error)) return finish(error);
	safe_to_abandon = !p.started && !p.rolling_back;
	if (auto e = validate_plan(p); !e.empty()) return finish(e);
	// Services may flush their settings while shutting down: that final state is captured once, before packages or
	// files change, and kept for a replay after a crash.
	if (!p.started) {
		if (p.options.action == "reset") {
			Plan next;
			Preview preview;
			if (!prepare(nullptr, p.options, next, preview, error)) return finish(error);
			// Prepare covers every group this release knows; a reset changes only those recorded when it was
			// queued. One queued before groups existed resets none of them.
			next.covers = p.covers;
			if (next.changes) std::erase_if(*next.changes, [&](const Change &c) { return !next.covers_path(c.path); });
			next.world = p.world;
			next.remove_packages = p.remove_packages;
			p = std::move(next);
		} else {
			std::set<std::string> seen;
			if (p.changes)
				for (auto &c : *p.changes) {
					seen.insert(c.path);
					c.before = read(root, c.path, max_file, error);
					if (!error.empty()) return finish(error);
				}
			auto current = inventory(root, error);
			if (!current) return finish(error);
			for (auto &cf : *current) {
				if (seen.count(cf.path) || (p.options.mode == "migrate" && !portable(cf.path)) || !p.covers_path(cf.path)) continue;
				if (!p.changes) p.changes = std::vector<Change>{};
				p.changes->push_back(Change{cf.path, cf, std::nullopt});
			}
		}
		if (p.options.action == "reset" && !p.options.keep_apps) {
			auto world = read(root, "etc/apk/world", max_file, error);
			if (!error.empty()) return finish(error);
			if (!world) return finish("APK world missing during recovery");
			if (world->data.value_or("") != p.world.value_or("")) return finish("APK world changed after recovery was queued");
		}
		p.started = true;
		if (auto e = validate_plan(p); !e.empty()) return finish(e);
		File journal{"", to_json(p), 0600};
		if (auto e = write(root, pending_path, &journal); !e.empty()) return finish(e);
	}
	safe_to_abandon = false;
	Error operation;
	if (p.rolling_back) operation = "resuming interrupted rollback";
	if (!p.rolling_back && p.options.action == "reset" && !p.options.keep_apps) {
		auto world = read(root, "etc/apk/world", max_file, error);
		if (!error.empty()) return error;
		// After an interrupted deletion APK may have removed some roots already: only approved names are replayed.
		if (!world) return "APK world missing during recovery";
		Error e;
		auto names = remaining_removals(p.world.value_or(""), world->data.value_or(""), p.remove_packages.value_or(std::vector<std::string>{}), e);
		operation = names ? remove(*names) : e;
	}
	if (operation.empty() && p.changes)
		for (const auto &c : *p.changes)
			if (auto e = write(root, c.path, c.after ? &*c.after : nullptr); !e.empty()) {
				operation = c.path + ": " + e;
				break;
			}
	if (!operation.empty()) {
		if (!p.rolling_back) {
			p.rolling_back = true;
			File journal{"", to_json(p), 0600};
			if (auto e = write(root, pending_path, &journal); !e.empty()) return e;
		}
		if (p.changes)
			for (const auto &c : *p.changes)
				if (auto e = write(root, c.path, c.before ? &*c.before : nullptr); !e.empty())
					return "recovery and rollback failed at " + c.path + ": " + e;
	}
	Status status{"complete", "Recovery completed."};
	if (!operation.empty())
		status = Status{"failed", "Settings restored after a recovery error: " + operation + ". Some optional packages may have been removed."};
	status.message = truncate_3000(status.message);
	File status_file{"", to_json(status), 0600};
	if (auto e = write(root, status_path, &status_file); !e.empty()) return e;
	return write(root, pending_path, nullptr);
}

Error boot()
{
	Engine e = live();
	if (!e.pending()) return "";
	// The pending marker deliberately blocks normal lock() callers.
	if (DIR *d = ::opendir("/proc")) {
		bool server = false;
		while (dirent *ent = ::readdir(d)) {
			std::string pid = ent->d_name;
			if (pid.empty() || pid.find_first_not_of("0123456789") != std::string::npos) continue;
			if (gostr::trim_space(osupdate::files::read("/proc/" + pid + "/comm").value_or("")) == "NanoKVM-Server") server = true;
		}
		::closedir(d);
		if (server) return "recovery-resume is only for early boot; reboot the device";
	}
	Error error;
	auto lock = osupdate::recovery_lock(error);
	if (!lock) return error;
	return e.resume([](const std::vector<std::string> &names) -> Error {
		if (!names.empty()) {
			apkrun::Cmd cmd;
			cmd.path = "/sbin/apk";
			cmd.args = {"del", "--no-network", "--no-progress"};
			cmd.args.insert(cmd.args.end(), names.begin(), names.end());
			cmd.deadline = apkrun::Clock::now() + std::chrono::minutes(5);
			auto r = apkrun::combined_output(cmd);
			if (!r.ok) return "APK reset failed: " + r.error + " (" + gostr::trim_space(r.output) + ")";
		}
		if (auto e2 = write("/", "usr/bin/picoclaw", nullptr); !e2.empty()) return e2;
		return remove_acme_binaries("/");
	});
}

} // namespace nkos::recovery
