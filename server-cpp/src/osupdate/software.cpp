// The Software page and the scheduled index refresh, as the Go server's osupdate/software.go and index_schedule.go.

#include "common/process.hpp"
#include "osupdate/files.hpp"
#include "osupdate/godecode.hpp"
#include "osupdate/gostr.hpp"
#include "osupdate/internal.hpp"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <climits>
#include <cstdlib>
#include <ctime>
#include <dirent.h>
#include <fcntl.h>
#include <set>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::osupdate {

using gostr::has_prefix;
using gostr::split;
using gostr::trim_space;

const char *const search_superseded_error = "a newer package search replaced this one";

namespace {

constexpr std::size_t log_limit = 64 * 1024;
constexpr const char *software_index_path = "/var/cache/apk";
constexpr auto software_lock_wait = std::chrono::seconds(12);

const std::vector<std::string> protected_packages{
	"apk-tools", "busybox", "musl", "openrc", "nanokvm-app", "nanokvm", "nanokvm-web", "nanokvm-base", "nanokvm-release",
	"nanokvm-kernel", "nanokvm-kmod", "nanokvm-firmware", "nanokvm-zlib", "nanokvm-openssl", "nanokvm-openssl3", "nanokvm-bootloader", "nanokvm-tpu"};

bool is_protected(const std::string &name)
{
	return std::find(protected_packages.begin(), protected_packages.end(), name) != protected_packages.end();
}

std::string software_state_path() { return std::string(apk_state_dir) + "/software.json"; }
std::string software_log_path() { return std::string(apk_state_dir) + "/software.log"; }

std::string tail(std::string s, std::size_t limit)
{
	if (s.size() > limit) s = s.substr(s.size() - limit);
	return s;
}

// runAPKOutput: a Software page query, its output (the last 64 KiB).
std::string run_apk_output(std::chrono::seconds timeout, std::vector<std::string> args, bool &ok, std::string &error,
                           std::function<bool()> cancelled = {})
{
	apkrun::Cmd cmd;
	cmd.path = apk_executable;
	cmd.args = std::move(args);
	cmd.lock_wait = software_lock_wait;
	cmd.timeout = timeout;
	cmd.cancelled = std::move(cancelled);
	auto r = apkrun::combined_output(cmd);
	ok = r.ok;
	error = r.error;
	return tail(r.output, log_limit);
}

bool alpine() { return files::exists("/etc/alpine-release"); }

void decode_software_status(const std::string &data, SoftwareStatus &s)
{
	auto doc = godecode::parse_document(data);
	if (!doc || doc->kind != godecode::Value::Kind::object) return;
	for (const auto &[k, v] : doc->members) {
		if (godecode::key_matches(k, "state")) godecode::get(v, s.state);
		else if (godecode::key_matches(k, "action")) godecode::get(v, s.action);
		else if (godecode::key_matches(k, "package")) godecode::get(v, s.package);
		else if (godecode::key_matches(k, "message")) godecode::get(v, s.message);
		else if (godecode::key_matches(k, "log")) godecode::get(v, s.log);
	}
}

std::string read_tail(const std::string &name, std::size_t limit, bool &ok)
{
	ok = false;
	int fd = ::open(name.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return "";
	struct stat st{};
	if (::fstat(fd, &st) == 0 && static_cast<std::size_t>(st.st_size) > limit) ::lseek(fd, -static_cast<off_t>(limit), SEEK_END);
	std::string out;
	char buf[8192];
	while (out.size() < limit) {
		ssize_t n = ::read(fd, buf, std::min(sizeof buf, limit - out.size()));
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			::close(fd);
			return "";
		}
		if (n == 0) break;
		out.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	ok = true;
	return out;
}

// requiredSystemPackages: APK's solver resolves the installed system roots, never a newer repository.
std::optional<std::set<std::string>> required_system_packages(Error &error)
{
	auto installed = installed_versions(error);
	if (!installed) return std::nullopt;
	std::vector<std::string> args{"--no-network", "query", "--from", "installed", "--recursive", "--format", "json", "--fields", "name"};
	std::vector<std::string> roots;
	for (const auto &name : protected_packages) {
		auto it = installed->find(name);
		if (it != installed->end()) roots.push_back(name + "=" + it->second);
	}
	if (roots.empty()) return error = "no installed system package roots", std::nullopt;
	std::sort(roots.begin(), roots.end());
	args.insert(args.end(), roots.begin(), roots.end());
	bool ok = false;
	std::string run_error;
	std::string output = run_apk_output(std::chrono::seconds(30), args, ok, run_error);
	if (!ok) return error = "APK dependency query failed: " + run_error, std::nullopt;
	auto start = output.find('[');
	if (start == std::string::npos) return error = "APK dependency query returned no package list", std::nullopt;
	std::string json_error;
	auto doc = godecode::unmarshal(std::string_view(output).substr(start), json_error);
	if (!doc) return error = json_error, std::nullopt;
	const char *element = "struct { Name string \"json:\\\"name\\\"\" }";
	std::vector<std::string> names;
	std::string type_error;
	if (doc->kind == godecode::Value::Kind::array) {
		for (std::size_t i = 0; i < doc->items.size(); i++) {
			const auto &item = doc->items[i];
			std::string name, index = std::to_string(i);
			if (item.kind == godecode::Value::Kind::object) {
				for (const auto &[k, v] : item.members)
					if (godecode::key_matches(k, "name") && !godecode::get(v, name) && type_error.empty())
						type_error = "json: cannot unmarshal " + godecode::kind_name(v) + " into Go struct field ." + index + ".name of type string";
			} else if (item.kind != godecode::Value::Kind::null && type_error.empty()) {
				type_error = "json: cannot unmarshal " + godecode::kind_name(item) + " into ." + index + " of type " + element;
			}
			names.push_back(name);
		}
	} else if (doc->kind != godecode::Value::Kind::null) {
		type_error = "json: cannot unmarshal " + godecode::kind_name(*doc) + " into Go value of type []" + element;
	}
	if (!type_error.empty()) return error = type_error, std::nullopt;
	std::set<std::string> required;
	for (const auto &name : names) {
		if (!valid_package_name(name)) return error = "invalid package name", std::nullopt;
		required.insert(name);
	}
	for (const auto &name : protected_packages)
		if (installed->count(name) && !required.count(name))
			return error = "APK dependency query omitted system root " + name, std::nullopt;
	return required;
}

std::string package_json(const SoftwarePackage &p)
{
	std::string out = "{\"name\":" + json::str(p.name) + ",\"version\":" + json::str(p.version);
	if (!p.removal_blocked.empty()) out += ",\"removalBlocked\":" + json::str(p.removal_blocked);
	return out + "}";
}

// One package search at a time, the newest.
std::mutex search_mutex;
std::uint64_t search_generation = 0;
std::shared_ptr<std::atomic<bool>> search_cancel;

} // namespace

Error save_software_status(const SoftwareStatus &s)
{
	if (auto e = files::mkdir_all(apk_state_dir, 0700); !e.empty()) return e;
	return write_atomic(software_state_path(), to_json(s), 0600);
}

SoftwareStatus get_software_status()
{
	SoftwareStatus s;
	if (auto data = files::read(software_state_path())) decode_software_status(*data, s);
	bool ok = false;
	std::string log = read_tail(software_log_path(), log_limit, ok);
	if (ok) s.log = log;
	return s;
}

std::string to_json(const SoftwareStatus &s)
{
	std::string out = "{\"state\":" + json::str(s.state);
	if (!s.action.empty()) out += ",\"action\":" + json::str(s.action);
	if (!s.package.empty()) out += ",\"package\":" + json::str(s.package);
	if (!s.message.empty()) out += ",\"message\":" + json::str(s.message);
	if (!s.log.empty()) out += ",\"log\":" + json::str(s.log);
	return out + "}";
}

std::vector<SoftwarePackage> parse_matching_packages(const std::string &output, std::string query, std::size_t max)
{
	std::set<std::string> seen;
	std::vector<SoftwarePackage> packages;
	query = gostr::to_lower_ascii(query);
	for (const auto &raw : split(output, "\n")) {
		std::string line = raw;
		auto dash = line.find(" - ");
		if (dash != std::string::npos) line.resize(dash);
		line = trim_space(line);
		std::string name, version;
		if (!detail::apk_package_version(line, name, version) || !detail::apk_package_name(name) || seen.count(name)) continue;
		if (!query.empty() && name.find(query) == std::string::npos) continue;
		seen.insert(name);
		packages.push_back(SoftwarePackage{name, version, ""});
		if (packages.size() == max) break;
	}
	auto rank = [&](const std::string &name) { return name == query ? 0 : has_prefix(name, query) ? 1 : 2; };
	std::stable_sort(packages.begin(), packages.end(), [&](const SoftwarePackage &a, const SoftwarePackage &b) {
		if (!query.empty()) {
			int ra = rank(a.name), rb = rank(b.name);
			if (ra != rb) return ra < rb;
		}
		return a.name < b.name;
	});
	return packages;
}

std::vector<SoftwareUpdate> parse_software_updates(const std::string &output)
{
	std::vector<SoftwareUpdate> updates;
	for (const auto &line : split(output, "\n")) {
		auto f = gostr::fields(line);
		if (f.size() < 2) continue;
		std::string installed = f[0], available = f[1];
		if (f.size() >= 3 && f[1] == "<") available = f[2];
		std::string name, current;
		if (!detail::apk_package_version(installed, name, current) || !detail::apk_package_name(name)) continue;
		std::string version = available, cname, cversion;
		if (detail::apk_package_version(available, cname, cversion) && cname == name) version = cversion;
		else if (!detail::apk_bare_version(available)) continue;
		updates.push_back(SoftwareUpdate{name, current, version});
	}
	std::stable_sort(updates.begin(), updates.end(), [](const auto &a, const auto &b) { return a.name < b.name; });
	return updates;
}

SoftwareIndexStatus get_software_index_status()
{
	SoftwareIndexStatus status;
	if (auto data = files::read(repositories_path)) {
		std::string all = *data + "\n";
		// repositoryLists: the *.list files of /etc/apk/repositories.d in name order.
		std::vector<std::string> lists;
		if (DIR *d = ::opendir("/etc/apk/repositories.d")) {
			while (dirent *e = ::readdir(d)) {
				std::string name = e->d_name;
				if (name.size() >= 5 && name.compare(name.size() - 5, 5, ".list") == 0) lists.push_back(name);
			}
			::closedir(d);
		}
		std::sort(lists.begin(), lists.end());
		for (const auto &list : lists)
			if (auto more = files::read("/etc/apk/repositories.d/" + list)) all += *more + "\n";
		for (const auto &raw : split(all, "\n")) {
			std::string line = trim_space(raw);
			if (!line.empty() && !has_prefix(line, "#")) status.repositories++;
		}
	}
	DIR *d = ::opendir(software_index_path);
	if (!d) return status;
	std::int64_t oldest_ns = 0;
	bool any = false;
	while (dirent *e = ::readdir(d)) {
		std::string name = e->d_name;
		if (name == "." || name == "..") continue;
		struct stat st{};
		if (::fstatat(dirfd(d), name.c_str(), &st, AT_SYMLINK_NOFOLLOW) != 0) continue;
		if (S_ISDIR(st.st_mode) || !has_prefix(name, "APKINDEX.") || !gostr::has_suffix(name, ".tar.gz")) continue;
		status.cached++;
		std::int64_t m = static_cast<std::int64_t>(st.st_mtim.tv_sec) * 1000000000 + st.st_mtim.tv_nsec;
		if (!any || m < oldest_ns) oldest_ns = m;
		any = true;
	}
	::closedir(d);
	if (!any) return status;
	timespec now{};
	clock_gettime(CLOCK_REALTIME, &now);
	std::int64_t now_ns = static_cast<std::int64_t>(now.tv_sec) * 1000000000 + now.tv_nsec;
	// time.Unix() floors; Seconds() of the age truncates toward zero.
	status.updated_at = oldest_ns >= 0 ? oldest_ns / 1000000000 : (oldest_ns - 999999999) / 1000000000;
	status.age_seconds = (now_ns - oldest_ns) / 1000000000;
	if (status.age_seconds < 0) status.age_seconds = 0;
	if (status.repositories > status.cached || status.age_seconds > 24 * 3600) {
		status.state = "stale";
		return status;
	}
	status.state = "fresh";
	return status;
}

std::string to_json(const SoftwareIndexStatus &s)
{
	std::string out = "{\"state\":" + json::str(s.state);
	if (s.updated_at) out += ",\"updatedAt\":" + std::to_string(s.updated_at);
	if (s.age_seconds) out += ",\"ageSeconds\":" + std::to_string(s.age_seconds);
	return out + ",\"repositories\":" + std::to_string(s.repositories) + ",\"cached\":" + std::to_string(s.cached) + "}";
}

std::string to_json(const std::vector<SoftwarePackage> &packages)
{
	std::string out = "[";
	for (std::size_t i = 0; i < packages.size(); i++) out += (i ? "," : "") + package_json(packages[i]);
	return out + "]";
}

Error check_removable(const std::string &name)
{
	if (!valid_package_name(name)) return "invalid package name";
	if (is_protected(name)) return "this protected system package cannot be removed from the web interface";
	Error e;
	auto required = required_system_packages(e);
	if (!required) return "cannot verify system package dependencies: " + e;
	if (required->count(name)) return "required by NanoKVM OS; cannot be removed";
	return "";
}

std::optional<std::vector<SoftwarePackage>> list_installed_software(Error &error)
{
	if (!alpine()) return error = "native APK software management requires Alpine", std::nullopt;
	bool ok = false;
	std::string run_error;
	std::string output = run_apk_output(std::chrono::seconds(45), {"info", "--verbose"}, ok, run_error);
	if (!ok) return error = "cannot list installed packages: " + trim_space(output), std::nullopt;
	auto packages = parse_matching_packages(output, "", 512);
	Error protection;
	auto required = required_system_packages(protection);
	for (auto &p : packages) {
		if (is_protected(p.name)) p.removal_blocked = "Protected system package";
		else if (!required) p.removal_blocked = "Cannot verify system dependencies; refresh and try again";
		else if (required->count(p.name)) p.removal_blocked = "Required by NanoKVM OS";
	}
	return packages;
}

std::optional<std::vector<SoftwarePackage>> search_software(const std::string &raw_query, std::function<bool()> gone, Error &error)
{
	std::string query = gostr::to_lower_ascii(trim_space(raw_query));
	if (!valid_package_name(query)) return error = "enter a package name using letters, digits, +, _, . or -", std::nullopt;
	if (!alpine()) return error = "native APK software management requires Alpine", std::nullopt;
	auto mine = std::make_shared<std::atomic<bool>>(false);
	std::uint64_t generation;
	{
		std::lock_guard lock(search_mutex);
		if (search_cancel) search_cancel->store(true);
		generation = ++search_generation;
		search_cancel = mine;
	}
	bool ok = false;
	std::string run_error;
	std::string output = run_apk_output(std::chrono::seconds(45), {"search", "--all", "--verbose", "*" + query + "*"}, ok, run_error,
	                                    [mine, gone] { return mine->load() || (gone && gone()); });
	{
		std::lock_guard lock(search_mutex);
		if (search_generation == generation) search_cancel.reset();
	}
	if (mine->load()) return error = search_superseded_error, std::nullopt;
	if (!ok) return error = "package search failed: " + trim_space(output), std::nullopt;
	return parse_matching_packages(output, query, 50);
}

std::optional<std::vector<SoftwareUpdate>> list_software_updates(Error &error)
{
	if (!alpine()) return error = "native APK software management requires Alpine", std::nullopt;
	bool ok = false;
	std::string run_error;
	std::string output = run_apk_output(std::chrono::seconds(45), {"version", "-l", "<"}, ok, run_error);
	if (!ok) return error = "cannot check available package updates: " + trim_space(output), std::nullopt;
	return parse_software_updates(output);
}

std::optional<std::string> preview_software_removal(const std::string &name, Error &error)
{
	if (auto e = check_removable(name); !e.empty()) return error = e, std::nullopt;
	auto l = lock(error);
	if (!l) return std::nullopt;
	bool ok = false;
	std::string run_error;
	std::string output = run_apk_output(std::chrono::seconds(30), {"del", "--simulate", "--", name}, ok, run_error);
	if (!ok) return error = "cannot simulate package removal: " + trim_space(output), std::nullopt;
	return output;
}

Error start_software(const std::string &action, const std::string &name)
{
	if (action != "refresh" && action != "install" && action != "remove" && action != "upgrade") return "invalid software operation";
	if (action == "install") {
		if (!valid_package_name(name)) return "invalid package name";
	} else if (action == "remove") {
		if (auto e = check_removable(name); !e.empty()) return e;
	} else if (!name.empty()) {
		return "this software operation does not accept a package name";
	}
	if (!alpine()) return "native APK software management requires Alpine";
	Error e;
	auto l = lock(e);
	if (!l) return e;
	if (auto err = save_software_status(SoftwareStatus{"running", action, name, "", ""}); !err.empty()) return err;
	std::string log_path = software_log_path();
	int log = ::open(log_path.c_str(), O_CREAT | O_TRUNC | O_WRONLY | O_CLOEXEC, 0600);
	if (log < 0) return goerr::path_error("open", log_path, errno);
	bool started = start_detached({helper, "software-inherited", action, name}, log, l->fd(), e);
	::close(log);
	if (!started) {
		(void)save_software_status(SoftwareStatus{"failed", action, name, e, ""});
		return e;
	}
	return "";
}

Error run_software(const std::string &action, const std::string &name)
{
	auto fail = [&](const Error &e) {
		(void)save_software_status(SoftwareStatus{"failed", action, name, e, ""});
		return e;
	};
	if (action == "install") {
		if (!valid_package_name(name)) return fail("invalid package name");
	} else if (action == "remove") {
		if (auto e = check_removable(name); !e.empty()) return fail(e);
	} else if ((action != "refresh" && action != "upgrade") || !name.empty()) {
		return fail("invalid software operation");
	}
	auto deadline = apkrun::Clock::now() + std::chrono::minutes(30);
	auto run = [&](std::vector<std::string> args) {
		apkrun::Cmd cmd;
		cmd.path = apk_executable;
		cmd.args = std::move(args);
		cmd.deadline = deadline;
		auto r = apkrun::run(cmd);
		return r.ok ? Error() : r.error;
	};
	Error result;
	if (action == "refresh") {
		result = run({"update"});
	} else if (action == "install") {
		result = run({"update"});
		if (result.empty()) result = run({"add", "--", name});
	} else if (action == "remove") {
		result = run({"del", "--", name});
	} else {
		result = run({"update"});
		if (result.empty()) result = run({"upgrade"});
	}
	if (!result.empty()) return fail("APK " + action + " failed: " + result);
	(void)check_updates(action == "refresh");
	if (auto e = save_software_status(SoftwareStatus{"succeeded", action, name, "APK operation completed", ""}); !e.empty()) return fail(e);
	return "";
}

// ---- Index schedule (index_schedule.go) ----

namespace {

constexpr const char *index_marker = "# nanokvm-apk-index-refresh";
constexpr const char *index_command = "/usr/sbin/nkos-update refresh-indexes";
constexpr const char *index_crontab_path = "/etc/crontabs/root";

Error validate_index_schedule(const IndexSchedule &s, int &hour, int &minute)
{
	const std::string &t = s.time;
	if (t.size() != 5 || t[2] != ':') return "time must use HH:MM format";
	for (int i : {0, 1, 3, 4})
		if (t[static_cast<std::size_t>(i)] < '0' || t[static_cast<std::size_t>(i)] > '9') return "time must use HH:MM format";
	hour = (t[0] - '0') * 10 + (t[1] - '0');
	minute = (t[3] - '0') * 10 + (t[4] - '0');
	if (hour > 23 || minute > 59) return "time must be between 00:00 and 23:59";
	return "";
}

std::optional<std::string> read_index_crontab(Error &error)
{
	int err = 0;
	auto data = files::read(index_crontab_path, err);
	if (!data) {
		if (err == ENOENT) return std::string();
		return error = files::read_error(index_crontab_path, err), std::nullopt;
	}
	return data;
}

// exec.CommandContext(name, args) with a 15 s context and input on stdin: its error, the output in output.
Error exec_command(const std::string &name, std::vector<std::string> args, const std::string &input, std::string &output)
{
	// exec.LookPath of a bare name in PATH.
	std::string path;
	const char *env = std::getenv("PATH");
	for (const auto &dir : split(env ? env : "", ":")) {
		std::string candidate = (dir.empty() ? "." : dir) + "/" + name;
		struct stat st{};
		if (::stat(candidate.c_str(), &st) == 0 && S_ISREG(st.st_mode) && (st.st_mode & 0111)) {
			path = candidate;
			break;
		}
	}
	if (path.empty()) return "exec: \"" + name + "\": executable file not found in $PATH";
	args.insert(args.begin(), path);
	process::Options options;
	options.timeout = std::chrono::seconds(15);
	options.input = input;
	auto r = process::run(args, options);
	output = r.output;
	if (!r.started) return goerr::start_error(path, EACCES);
	if (r.timed_out) return "signal: killed";
	if (r.exit_code != 0) return goerr::exit_text(r.exit_code, r.signal);
	return "";
}

} // namespace

std::mutex &crontab_mutex()
{
	static std::mutex m;
	return m;
}

std::string to_json(const IndexSchedule &s) { return "{\"enabled\":" + json::boolean(s.enabled) + ",\"time\":" + json::str(s.time) + "}"; }

IndexSchedule parse_index_schedule(const std::string &data, Error &error)
{
	IndexSchedule s;
	std::string prefix = std::string(index_marker) + " ";
	for (const auto &line : split(data, "\n")) {
		if (!has_prefix(line, prefix)) continue;
		std::string json_error;
		auto doc = godecode::unmarshal(std::string_view(line).substr(prefix.size()), json_error);
		std::string type_error;
		if (doc && doc->kind == godecode::Value::Kind::object) {
			for (const auto &[k, v] : doc->members) {
				if (godecode::key_matches(k, "enabled")) {
					if (!godecode::get(v, s.enabled) && type_error.empty())
						type_error = "json: cannot unmarshal " + godecode::kind_name(v) + " into Go struct field IndexSchedule.enabled of type bool";
				} else if (godecode::key_matches(k, "time")) {
					if (!godecode::get(v, s.time) && type_error.empty())
						type_error = "json: cannot unmarshal " + godecode::kind_name(v) + " into Go struct field IndexSchedule.time of type string";
				}
			}
		} else if (doc && doc->kind != godecode::Value::Kind::null) {
			type_error = "json: cannot unmarshal " + godecode::kind_name(*doc) + " into Go value of type osupdate.IndexSchedule";
		}
		if (!doc) return error = "read index refresh schedule: " + json_error, s;
		if (!type_error.empty()) return error = "read index refresh schedule: " + type_error, s;
		int h = 0, m = 0;
		if (auto e = validate_index_schedule(s, h, m); !e.empty()) return error = e, s;
		break;
	}
	return s;
}

std::optional<std::string> render_index_schedule(const std::string &data, const IndexSchedule &s, Error &error)
{
	int hour = 0, minute = 0;
	if (auto e = validate_index_schedule(s, hour, minute); !e.empty()) return error = e, std::nullopt;
	std::string body = data;
	if (!body.empty() && body.back() == '\n') body.pop_back();
	std::vector<std::string> lines;
	std::string prefix = std::string(index_marker) + " ", suffix = " " + std::string(index_marker);
	for (const auto &line : split(body, "\n")) {
		if (has_prefix(line, prefix) || gostr::has_suffix(line, suffix)) continue;
		lines.push_back(line);
	}
	if (lines.size() == 1 && lines[0].empty()) lines.clear();
	lines.push_back(prefix + to_json(s));
	if (s.enabled)
		lines.push_back(std::to_string(minute) + " " + std::to_string(hour) + " * * * " + index_command + " >/dev/null 2>&1 " + index_marker);
	return gostr::join(lines, "\n") + "\n";
}

IndexSchedule get_index_schedule(Error &error)
{
	std::lock_guard lock(crontab_mutex());
	auto data = read_index_crontab(error);
	if (!data) return IndexSchedule{false, ""};
	return parse_index_schedule(*data, error);
}

Error set_index_schedule(const IndexSchedule &s)
{
	std::lock_guard lock(crontab_mutex());
	Error error;
	auto data = read_index_crontab(error);
	if (!data) return error;
	auto next = render_index_schedule(*data, s, error);
	if (!next) return error;
	std::string output;
	if (s.enabled) {
		if (auto e = exec_command("rc-update", {"add", "crond", "default"}, "", output); !e.empty()) return "enable cron: " + e;
		if (auto e = exec_command("rc-service", {"crond", "status"}, "", output); !e.empty())
			if (auto e2 = exec_command("rc-service", {"crond", "start"}, "", output); !e2.empty()) return "start cron: " + e2;
	}
	if (auto e = exec_command("crontab", {"-"}, *next, output); !e.empty())
		return "save index refresh schedule: " + e + ": " + trim_space(output);
	return "";
}

std::string index_schedule_timezone()
{
	if (auto data = files::read("/etc/timezone"); data && !trim_space(*data).empty()) return trim_space(*data);
	char resolved[PATH_MAX];
	if (::realpath("/etc/localtime", resolved)) {
		std::string name = resolved;
		auto at = name.find("/zoneinfo/");
		if (at != std::string::npos) return name.substr(at + 10);
	}
	return "UTC";
}

} // namespace nkos::osupdate
