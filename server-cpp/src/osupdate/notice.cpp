// The update notice and the installed components, as the Go server's osupdate/notice.go: a light, periodic look at
// the NanoKVM repositories only (the official OS and add-on repositories, any untagged line of
// /etc/apk/repositories or repositories.d/*.list whose URL contains "nanokvm"), with an index cache of its own in
// /run, compared with the installed packages. The Alpine indexes are left to the Software page.

#include "common/log.hpp"
#include "osupdate/files.hpp"
#include "osupdate/godecode.hpp"
#include "osupdate/gostr.hpp"
#include "osupdate/internal.hpp"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <ctime>
#include <dirent.h>
#include <fcntl.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>

namespace nkos::osupdate {

using gostr::has_prefix;
using gostr::split;
using gostr::trim_space;

namespace {

constexpr const char *notice_disabled_path = "/etc/kvm/update-check.disabled";
constexpr const char *notice_cache_dir = "/run/nanokvm-update-check";
constexpr const char *repositories_dir = "/etc/apk/repositories.d";
constexpr std::int64_t notice_memory_kib = 12 * 1024;
constexpr std::size_t output_limit = 64 * 1024;

std::atomic<bool> notice_checking{false};

std::string notice_path() { return std::string(apk_state_dir) + "/notice.json"; }

std::int64_t now_ns()
{
	timespec ts{};
	clock_gettime(CLOCK_REALTIME, &ts);
	return static_cast<std::int64_t>(ts.tv_sec) * 1000000000 + ts.tv_nsec;
}

void decode_update(const godecode::Value &v, SoftwareUpdate &u)
{
	if (v.kind != godecode::Value::Kind::object) return;
	for (const auto &[k, m] : v.members) {
		if (godecode::key_matches(k, "name")) godecode::get(m, u.name);
		else if (godecode::key_matches(k, "installed")) godecode::get(m, u.installed);
		else if (godecode::key_matches(k, "available")) godecode::get(m, u.available);
	}
}

void decode_notice(const std::string &data, UpdateNotice &n)
{
	auto doc = godecode::parse_document(data);
	if (!doc || doc->kind != godecode::Value::Kind::object) return;
	for (const auto &[k, v] : doc->members) {
		if (godecode::key_matches(k, "enabled")) godecode::get(v, n.enabled);
		else if (godecode::key_matches(k, "state")) godecode::get(v, n.state);
		else if (godecode::key_matches(k, "checkedAt")) godecode::get_int(v, n.checked_at, INT64_MIN, INT64_MAX);
		else if (godecode::key_matches(k, "attemptAt")) godecode::get_int(v, n.attempt_at, INT64_MIN, INT64_MAX);
		else if (godecode::key_matches(k, "message")) godecode::get(v, n.message);
		else if (godecode::key_matches(k, "updates")) {
			if (v.kind == godecode::Value::Kind::null) {
				n.updates.reset();
			} else if (v.kind == godecode::Value::Kind::array) {
				// Go reuses the elements of an earlier array of the same key; a fresh list is the same for one key.
				std::vector<SoftwareUpdate> list;
				for (std::size_t i = 0; i < v.items.size(); i++) {
					SoftwareUpdate u;
					if (n.updates && i < n.updates->size()) u = (*n.updates)[i];
					if (v.items[i].kind != godecode::Value::Kind::null) decode_update(v.items[i], u);
					list.push_back(u);
				}
				n.updates = std::move(list);
			}
		}
	}
}

std::string update_json(const SoftwareUpdate &u)
{
	return "{\"name\":" + json::str(u.name) + ",\"installed\":" + json::str(u.installed) + ",\"available\":" + json::str(u.available) + "}";
}

// The *.list files of /etc/apk/repositories.d in name order.
std::string repository_lists()
{
	std::vector<std::string> lists;
	if (DIR *d = ::opendir(repositories_dir)) {
		while (dirent *e = ::readdir(d)) {
			std::string name = e->d_name;
			// filepath.Glob("*.list"): dot files too.
			if (name.size() >= 5 && name.compare(name.size() - 5, 5, ".list") == 0) lists.push_back(name);
		}
		::closedir(d);
	}
	std::sort(lists.begin(), lists.end());
	std::string data;
	for (const auto &list : lists)
		if (auto more = files::read(std::string(repositories_dir) + "/" + list)) data += *more + "\n";
	return data;
}

std::optional<std::vector<std::string>> nanokvm_repositories(Error &error)
{
	int err = 0;
	auto data = files::read(repositories_path, err);
	if (!data) return error = files::read_error(repositories_path, err), std::nullopt;
	std::string all = *data + "\n" + repository_lists();
	std::vector<std::string> repos;
	for (const auto &raw : split(all, "\n")) {
		std::string line = trim_space(raw);
		if (line.empty() || has_prefix(line, "#") || has_prefix(line, "@")) continue;
		if (gostr::to_lower_ascii(line).find("nanokvm") != std::string::npos) repos.push_back(line);
	}
	return repos;
}

std::vector<std::string> notice_args(const std::vector<std::string> &repos, std::vector<std::string> applet)
{
	std::vector<std::string> args{"--repositories-file=/dev/null", std::string("--cache-dir=") + notice_cache_dir};
	for (const auto &repo : repos) args.push_back("--repository=" + repo);
	args.insert(args.end(), applet.begin(), applet.end());
	return args;
}

std::string run_notice_apk(const std::vector<std::string> &args, apkrun::Clock::time_point deadline, bool &ok)
{
	apkrun::Cmd cmd;
	cmd.path = apk_executable;
	cmd.args = args;
	cmd.lock_wait = std::chrono::seconds(5);
	cmd.timeout = std::chrono::minutes(1);
	cmd.memory_kib = notice_memory_kib;
	cmd.deadline = deadline;
	auto r = apkrun::combined_output(cmd);
	ok = r.ok;
	if (r.output.size() > output_limit) r.output = r.output.substr(r.output.size() - output_limit);
	return r.output;
}

std::mutex components_mutex;
std::int64_t components_mtime = -1, components_size = -1;
std::optional<std::vector<Component>> components_cache;

const std::vector<std::string> component_order{"nanokvm",       "nanokvm-release", "nanokvm-app",
                                               "nanokvm-web",   "nanokvm-base",    "nanokvm-kernel",
                                               "nanokvm-kmod",  "nanokvm-firmware", "nanokvm-keys"};

} // namespace

bool notice_enabled()
{
	struct stat st{};
	return ::stat(notice_disabled_path, &st) != 0 && errno == ENOENT;
}

Error set_notice_enabled(bool enabled)
{
	if (enabled) {
		int e = files::remove(notice_disabled_path);
		if (e && e != ENOENT) return goerr::path_error("remove", notice_disabled_path, e);
		return "";
	}
	return write_atomic(notice_disabled_path, "", 0644);
}

UpdateNotice get_notice()
{
	UpdateNotice n;
	if (auto data = files::read(notice_path())) decode_notice(*data, n);
	Error e;
	if (auto installed = installed_versions(e)) {
		std::vector<SoftwareUpdate> current;
		if (n.updates)
			for (auto u : *n.updates) {
				auto it = installed->find(u.name);
				if (it != installed->end() && compare_apk_versions(u.available, it->second) > 0) {
					u.installed = it->second;
					current.push_back(u);
				}
			}
		n.updates = std::move(current);
	}
	if (!n.updates) n.updates = std::vector<SoftwareUpdate>{};
	n.enabled = notice_enabled();
	if (notice_checking.load()) n.state = "checking";
	return n;
}

std::string to_json(const UpdateNotice &n)
{
	std::string out = "{\"enabled\":" + json::boolean(n.enabled) + ",\"state\":" + json::str(n.state);
	if (n.checked_at) out += ",\"checkedAt\":" + std::to_string(n.checked_at);
	if (n.attempt_at) out += ",\"attemptAt\":" + std::to_string(n.attempt_at);
	if (!n.message.empty()) out += ",\"message\":" + json::str(n.message);
	out += ",\"updates\":";
	if (!n.updates) return out + "null}";
	out += "[";
	for (std::size_t i = 0; i < n.updates->size(); i++) out += (i ? "," : "") + update_json((*n.updates)[i]);
	return out + "]}";
}

std::string to_json(const std::vector<SoftwareUpdate> &updates)
{
	std::string out = "[";
	for (std::size_t i = 0; i < updates.size(); i++) out += (i ? "," : "") + update_json(updates[i]);
	return out + "]";
}

Error save_notice(const UpdateNotice &n)
{
	if (auto e = files::mkdir_all(apk_state_dir, 0700); !e.empty()) return e;
	return write_atomic(notice_path(), to_json(n), 0600);
}

std::optional<UpdateLock> lock_notice(bool wait, bool &busy, Error &error)
{
	busy = false;
	if (auto e = files::mkdir_all(apk_state_dir, 0700); !e.empty()) return error = e, std::nullopt;
	int fd = ::open((std::string(apk_state_dir) + "/notice.lock").c_str(), O_CREAT | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0600);
	if (fd < 0) return error = goerr::errno_text(errno), std::nullopt;
	UpdateLock lock(fd);
	struct stat st{};
	if (::fstat(fd, &st) != 0 || !S_ISREG(st.st_mode)) return error = "unsafe notice lock", std::nullopt;
	auto deadline = apkrun::Clock::now() + check_timeout;
	for (;;) {
		if (::flock(fd, LOCK_EX | LOCK_NB) == 0) return lock;
		if (errno != EWOULDBLOCK && errno != EINTR) return error = goerr::errno_text(errno), std::nullopt;
		if (!wait) {
			busy = true;
			return std::nullopt;
		}
		if (apkrun::Clock::now() >= deadline) return error = "context deadline exceeded", std::nullopt;
		std::this_thread::sleep_for(std::chrono::milliseconds(50));
	}
}

Error check_updates(bool network)
{
	auto deadline = apkrun::Clock::now() + check_timeout;
	if (network)
		if (auto e = recover_interrupted_rollback(); !e.empty()) return e;
	bool busy = false;
	Error error;
	auto lock = lock_notice(!network, busy, error);
	if (!lock) return error;
	struct Checking {
		bool on;
		explicit Checking(bool network) : on(network)
		{
			if (on) notice_checking.store(true);
		}
		~Checking()
		{
			if (on) notice_checking.store(false);
		}
	} checking(network);
	UpdateNotice n = get_notice();
	std::int64_t now = now_ns() / 1000000000;
	if (network) n.attempt_at = now;
	else if (n.checked_at == 0) return "";
	Error err;
	auto repos = nanokvm_repositories(err);
	if (repos && repos->empty()) {
		n.state = "unavailable";
		n.message = "no NanoKVM repository is configured";
		n.updates.reset();
		return save_notice(n);
	}
	if (err.empty()) err = files::mkdir_all(notice_cache_dir, 0755);
	if (err.empty() && network) {
		bool ok = false;
		std::string out = run_notice_apk(notice_args(*repos, {"update"}), deadline, ok);
		if (!ok) err = "index refresh failed: " + trim_space(out);
		else n.checked_at = now;
	}
	if (err.empty()) {
		bool ok = false;
		std::string out = run_notice_apk(notice_args(*repos, {"--no-network", "version", "-l", "<"}), deadline, ok);
		if (!ok) err = "version check failed: " + trim_space(out);
		else n.updates = parse_software_updates(out);
	}
	if (!err.empty()) {
		n.state = "failed";
		n.message = err;
	} else {
		n.state = "ok";
		n.message.clear();
	}
	if (auto save = save_notice(n); !save.empty()) return save;
	return err;
}

bool notice_due(const UpdateNotice &n, std::int64_t now)
{
	// now in nanoseconds; the stamps are seconds.
	if (!n.enabled) return false;
	if (n.attempt_at != 0 && now - n.attempt_at * 1000000000 < 3600LL * 1000000000) return false;
	return n.state == "failed" || n.checked_at == 0 || now - n.checked_at * 1000000000 >= 86400LL * 1000000000;
}

void start_notice_schedule()
{
	std::thread([] {
		std::this_thread::sleep_for(std::chrono::minutes(2));
		for (;;) {
			if (files::exists("/etc/alpine-release") && notice_due(get_notice(), now_ns())) (void)check_updates(true);
			std::this_thread::sleep_for(std::chrono::minutes(10));
		}
	}).detach();
}

std::vector<Component> installed_components()
{
	struct stat st{};
	if (::stat(installed_db_path, &st) != 0) return {};
	std::int64_t mtime = static_cast<std::int64_t>(st.st_mtim.tv_sec) * 1000000000 + st.st_mtim.tv_nsec;
	std::lock_guard lock(components_mutex);
	if (components_cache && mtime == components_mtime && st.st_size == components_size) return *components_cache;
	auto data = files::read(installed_db_path);
	if (!data) return {};
	std::vector<Component> list;
	std::string name;
	for (const auto &line : split(*data, "\n")) {
		if (line.empty()) name.clear();
		else if (has_prefix(line, "P:")) name = line.substr(2);
		else if (has_prefix(line, "V:") && (name == "nanokvm" || has_prefix(name, "nanokvm-")))
			list.push_back(Component{name, line.substr(2), ""});
	}
	auto rank = [](const std::string &n) {
		auto it = std::find(component_order.begin(), component_order.end(), n);
		return static_cast<std::size_t>(it - component_order.begin());
	};
	std::stable_sort(list.begin(), list.end(), [&](const Component &a, const Component &b) {
		auto ra = rank(a.name), rb = rank(b.name);
		if (ra != rb) return ra < rb;
		return a.name < b.name;
	});
	components_mtime = mtime;
	components_size = st.st_size;
	components_cache = list;
	return list;
}

Components get_components()
{
	std::map<std::string, std::string> available;
	for (const auto &u : *get_notice().updates) available[u.name] = u.available;
	Components c;
	for (auto comp : installed_components()) {
		auto it = available.find(comp.name);
		comp.available = it == available.end() ? "" : it->second;
		c.packages.push_back(comp);
	}
	c.alpine = trim_space(files::read("/etc/alpine-release").value_or(""));
	c.kernel = trim_space(files::read("/proc/sys/kernel/osrelease").value_or(""));
	c.boot_kernel = trim_space(files::read("/boot/kernel.release").value_or(""));
	return c;
}

std::string to_json(const Components &c)
{
	std::string out = "{\"packages\":[";
	for (std::size_t i = 0; i < c.packages.size(); i++) {
		const auto &p = c.packages[i];
		out += (i ? ",{" : "{") + std::string("\"name\":") + json::str(p.name) + ",\"installed\":" + json::str(p.installed);
		if (!p.available.empty()) out += ",\"available\":" + json::str(p.available);
		out += "}";
	}
	return out + "],\"alpine\":" + json::str(c.alpine) + ",\"kernel\":" + json::str(c.kernel) + ",\"bootKernel\":" +
	       json::str(c.boot_kernel) + "}";
}

} // namespace nkos::osupdate
