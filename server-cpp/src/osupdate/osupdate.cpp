// The update lock, versions and apk output, the APK update page, the channel, the community repository and the
// rollback recovery of the Go server's osupdate package (state.go, versions.go, apk.go, channel.go, community.go,
// rollback_recovery.go).

#include "osupdate/osupdate.hpp"

#include "common/gojson.hpp"
#include "common/process.hpp"
#include "osupdate/apkrun.hpp"
#include "osupdate/files.hpp"
#include "osupdate/godecode.hpp"
#include "osupdate/gostr.hpp"
#include "osupdate/internal.hpp"

#include <algorithm>
#include <cerrno>
#include <climits>
#include <csignal>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <set>
#include <sys/file.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

extern char **environ;

namespace nkos::osupdate {

using gostr::fields;
using gostr::has_prefix;
using gostr::has_suffix;
using gostr::split;
using gostr::trim_space;

const std::vector<std::string> core_packages{
	"nanokvm", "nanokvm-web", "nanokvm-app", "nanokvm-release", "nanokvm-base", "nanokvm-keys", "nanokvm-kernel",
	"nanokvm-kmod", "nanokvm-firmware", "nanokvm-bootloader", "nanokvm-zlib", "nanokvm-openssl", "nanokvm-openssl3",
	"nanokvm-exfatprogs", "nanokvm-exfatprogs1", "nanokvm-i2c-tools", "nanokvm-tpu"};

// ---- Lock and files ----

UpdateLock &UpdateLock::operator=(UpdateLock &&other) noexcept
{
	if (this != &other) {
		if (fd_ >= 0) ::close(fd_);
		fd_ = std::exchange(other.fd_, -1);
	}
	return *this;
}

UpdateLock::~UpdateLock()
{
	if (fd_ >= 0) ::close(fd_);
}

std::optional<UpdateLock> recovery_lock(Error &error)
{
	if (auto e = files::mkdir_all(base, 0700); !e.empty()) return error = e, std::nullopt;
	struct stat st{};
	if (::lstat(base, &st) != 0 || !S_ISDIR(st.st_mode)) return error = "unsafe update state directory", std::nullopt;
	int fd = ::open((std::string(base) + "/lock").c_str(), O_CREAT | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0600);
	if (fd < 0) return error = goerr::errno_text(errno), std::nullopt;
	UpdateLock lock(fd);
	if (::fstat(fd, &st) != 0 || !S_ISREG(st.st_mode)) return error = "unsafe update lock", std::nullopt;
	if (::flock(fd, LOCK_EX | LOCK_NB) != 0) return error = "another update operation is in progress", std::nullopt;
	if (::stat("/run/nkos-standby/armed", &st) == 0 || errno != ENOENT) return error = "NanoKVM shutdown is in progress", std::nullopt;
	return lock;
}

std::optional<UpdateLock> lock(Error &error)
{
	auto l = recovery_lock(error);
	if (!l) return std::nullopt;
	struct stat st{};
	if (::lstat("/var/lib/nanokvm-recovery/pending.json", &st) == 0 || errno != ENOENT)
		return error = "settings recovery pending; reboot before changing packages", std::nullopt;
	return l;
}

std::string installed_version() { return trim_space(files::read("/kvmapp/version").value_or("")); }

namespace {

std::string dir_of(const std::string &name)
{
	auto slash = name.find_last_of('/');
	if (slash == std::string::npos) return ".";
	if (slash == 0) return "/";
	return name.substr(0, slash);
}

Error sync_dir(const std::string &name)
{
	int fd = ::open(name.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return goerr::path_error("open", name, errno);
	Error e;
	if (::fsync(fd) != 0) e = goerr::path_error("sync", name, errno);
	::close(fd);
	return e;
}

// os.CreateTemp(dir, ".write-").
int create_temp(const std::string &dir, std::string &name, Error &error)
{
	for (int attempt = 0; attempt < 10000; attempt++) {
		unsigned r = 0;
		int u = ::open("/dev/urandom", O_RDONLY | O_CLOEXEC);
		if (u >= 0) {
			(void)!::read(u, &r, sizeof r);
			::close(u);
		}
		name = dir + "/.write-" + std::to_string(r);
		int fd = ::open(name.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd >= 0) return fd;
		if (errno != EEXIST) {
			error = goerr::path_error("open", dir + "/.write-*", errno);
			return -1;
		}
	}
	error = goerr::path_error("open", dir + "/.write-*", EEXIST);
	return -1;
}

Error write_temp_and_rename(const std::string &name, const std::string &data, unsigned mode, const struct stat *like)
{
	std::string dir = dir_of(name), tmp;
	Error error;
	int fd = create_temp(dir, tmp, error);
	if (fd < 0) return error;
	if (::fchmod(fd, like ? (like->st_mode & 0777) : mode) != 0) error = goerr::path_error("chmod", tmp, errno);
	if (error.empty() && like && ::fchown(fd, like->st_uid, like->st_gid) != 0) error = goerr::path_error("chown", tmp, errno);
	std::size_t off = 0;
	while (error.empty() && off < data.size()) {
		ssize_t n = ::write(fd, data.data() + off, data.size() - off);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) error = goerr::path_error("write", tmp, errno);
		else off += static_cast<std::size_t>(n);
	}
	if (error.empty() && ::fsync(fd) != 0) error = goerr::path_error("sync", tmp, errno);
	if (::close(fd) != 0 && error.empty()) error = goerr::path_error("close", tmp, errno);
	if (error.empty() && ::rename(tmp.c_str(), name.c_str()) != 0) {
		int e = errno;
		error = "rename " + tmp + " " + name + ": " + goerr::errno_text(e);
	}
	if (!error.empty()) {
		::unlink(tmp.c_str());
		return error;
	}
	return sync_dir(dir);
}

} // namespace

Error write_atomic(const std::string &name, const std::string &data, unsigned mode)
{
	return write_temp_and_rename(name, data, mode, nullptr);
}

Error write_repositories(const std::string &data)
{
	std::string name = repositories_path;
	char resolved[PATH_MAX];
	if (::realpath(repositories_path, resolved)) {
		name = resolved;
	} else if (errno != ENOENT) {
		return goerr::path_error("lstat", repositories_path, errno);
	}
	struct stat st{};
	if (::stat(name.c_str(), &st) != 0) {
		if (errno == ENOENT) return write_atomic(name, data, 0644);
		return goerr::path_error("stat", name, errno);
	}
	return write_temp_and_rename(name, data, 0, &st);
}

bool start_detached(const std::vector<std::string> &argv, int output_fd, int lock_fd, Error &error)
{
	int report[2];
	if (::pipe2(report, O_CLOEXEC) != 0) {
		error = goerr::errno_text(errno);
		return false;
	}
	std::vector<char *> args;
	for (const auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	pid_t pid = ::fork();
	if (pid < 0) {
		error = goerr::start_error(argv[0], errno);
		::close(report[0]);
		::close(report[1]);
		return false;
	}
	if (pid == 0) {
		::setsid();
		int null = ::open("/dev/null", O_RDWR | O_CLOEXEC);
		int out = output_fd >= 0 ? output_fd : null;
		// The lock may sit on 0-3 already: move it above them first.
		int held = lock_fd >= 0 ? ::fcntl(lock_fd, F_DUPFD_CLOEXEC, 10) : -1;
		int outcopy = ::fcntl(out, F_DUPFD_CLOEXEC, 10);
		::dup2(null, 0);
		::dup2(outcopy, 1);
		::dup2(outcopy, 2);
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int s : {SIGPIPE, SIGTERM, SIGINT, SIGHUP}) ::sigaction(s, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		// dup2 leaves the new descriptor without close-on-exec: the helper gets the lock as fd 3.
		if (held >= 0) ::dup2(held, 3);
		::execve(args[0], args.data(), environ);
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	::close(report[1]);
	int exec_errno = 0;
	bool failed = ::read(report[0], &exec_errno, sizeof exec_errno) == sizeof exec_errno;
	::close(report[0]);
	if (failed) {
		int status = 0;
		while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
		}
		error = goerr::start_error(argv[0], exec_errno);
		return false;
	}
	std::thread([pid] {
		int status = 0;
		while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
		}
	}).detach();
	return true;
}

// ---- Go JSON helpers ----

namespace json {

std::string str(std::string_view s) { return gojson::quote(s); }
std::string boolean(bool b) { return b ? "true" : "false"; }
std::string strings(const std::optional<std::vector<std::string>> &list)
{
	if (!list) return "null";
	std::string out = "[";
	for (std::size_t i = 0; i < list->size(); i++) out += (i ? "," : "") + str((*list)[i]);
	return out + "]";
}

} // namespace json

// ---- Versions (versions.go) ----

namespace {

struct ApkSuffix {
	int rank = 0;
	long long number = 0;
};
struct ApkVersion {
	std::vector<long long> numbers;
	char letter = 0;
	std::vector<ApkSuffix> suffixes;
	long long release = 0;
};

// strconv.Atoi on digits: out of range gives the largest int (Go returns it with ErrRange, ignored here).
long long atoi_clamped(std::string_view digits)
{
	long long v = 0;
	for (char c : digits) {
		if (v > (LLONG_MAX - (c - '0')) / 10) return LLONG_MAX;
		v = v * 10 + (c - '0');
	}
	return v;
}

bool parse_apk_version(std::string_view v, ApkVersion &p)
{
	// ^([0-9]+(?:\.[0-9]+)*)([a-z]?)((?:_[a-z]+[0-9]*)*)(?:-r([0-9]+))?$
	std::size_t i = 0;
	auto digits = [&] {
		std::size_t start = i;
		while (i < v.size() && v[i] >= '0' && v[i] <= '9') i++;
		return v.substr(start, i - start);
	};
	// A number part out of int's range fails the parse (strconv.Atoi's error is checked there).
	auto number = [](std::string_view d) -> long long {
		long long x = 0;
		for (char c : d) {
			if (x > (LLONG_MAX - (c - '0')) / 10) return -1;
			x = x * 10 + (c - '0');
		}
		return x;
	};
	auto first = digits();
	if (first.empty() || number(first) < 0) return false;
	p.numbers.push_back(number(first));
	while (i < v.size() && v[i] == '.') {
		std::size_t save = i++;
		auto d = digits();
		if (d.empty()) {
			i = save;
			break;
		}
		if (number(d) < 0) return false;
		p.numbers.push_back(number(d));
	}
	if (i < v.size() && v[i] >= 'a' && v[i] <= 'z') {
		// The letter is optional: it may also be the start of nothing else (a suffix starts with '_').
		p.letter = v[i++];
	}
	static const std::pair<const char *, int> ranks[] = {{"alpha", -4}, {"beta", -3}, {"pre", -2}, {"rc", -1},
	                                                      {"cvs", 1},    {"svn", 2},   {"git", 3},  {"hg", 4}, {"p", 5}};
	while (i < v.size() && v[i] == '_') {
		std::size_t start = ++i;
		while (i < v.size() && v[i] >= 'a' && v[i] <= 'z') i++;
		if (i == start) return false;
		std::string_view name = v.substr(start, i - start);
		auto num = digits();
		int rank = 0;
		bool known = false;
		for (auto &[n, r] : ranks)
			if (name == n) rank = r, known = true;
		if (!known) return false;
		p.suffixes.push_back({rank, num.empty() ? 0 : atoi_clamped(num)});
	}
	if (i < v.size()) {
		if (v.substr(i, 2) != "-r") return false;
		i += 2;
		auto r = digits();
		if (r.empty() || i != v.size()) return false;
		p.release = atoi_clamped(r);
	}
	return i == v.size();
}

int cmp(long long a, long long b) { return a < b ? -1 : a > b ? 1 : 0; }

} // namespace

int compare_apk_versions(const std::string &a, const std::string &b)
{
	ApkVersion pa, pb;
	if (!parse_apk_version(a, pa) || !parse_apk_version(b, pb)) return a < b ? -1 : a > b ? 1 : 0;
	for (std::size_t i = 0; i < pa.numbers.size() || i < pb.numbers.size(); i++) {
		if (i >= pa.numbers.size()) return -1;
		if (i >= pb.numbers.size()) return 1;
		if (int c = cmp(pa.numbers[i], pb.numbers[i])) return c;
	}
	if (pa.letter != pb.letter) return cmp(static_cast<unsigned char>(pa.letter), static_cast<unsigned char>(pb.letter));
	for (std::size_t i = 0; i < pa.suffixes.size() || i < pb.suffixes.size(); i++) {
		ApkSuffix sa, sb;
		if (i < pa.suffixes.size()) sa = pa.suffixes[i];
		if (i < pb.suffixes.size()) sb = pb.suffixes[i];
		if (int c = cmp(sa.rank, sb.rank)) return c;
		if (int c = cmp(sa.number, sb.number)) return c;
	}
	return cmp(pa.release, pb.release);
}

// ---- Package names and versions in apk output ----

namespace detail {

bool name_char(char c) { return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '_' || c == '.' || c == '-'; }
bool version_char(char c)
{
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '+' ||
	       c == '~' || c == ':' || c == '-';
}

// ^[a-z0-9][a-z0-9+_.-]{0,127}$
bool apk_package_name(std::string_view s)
{
	if (s.empty() || s.size() > 128) return false;
	if (!((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= '0' && s[0] <= '9'))) return false;
	for (char c : s)
		if (!name_char(c)) return false;
	return true;
}

// ^[0-9][A-Za-z0-9._+~:-]*$
bool apk_bare_version(std::string_view s)
{
	if (s.empty() || s[0] < '0' || s[0] > '9') return false;
	for (char c : s)
		if (!version_char(c)) return false;
	return true;
}

// ^(.+)-([0-9][A-Za-z0-9._+~:-]*)$ (greedy: the last '-' that works).
bool apk_package_version(std::string_view s, std::string &name, std::string &version)
{
	if (s.find('\n') != std::string_view::npos) return false;
	for (std::size_t i = s.size(); i-- > 1;) {
		if (s[i] != '-') continue;
		if (apk_bare_version(s.substr(i + 1))) {
			name = std::string(s.substr(0, i));
			version = std::string(s.substr(i + 1));
			return true;
		}
	}
	return false;
}

// ^(.+)-[0-9][^-]*-r[0-9]+$
bool apk_versioned_name(std::string_view s, std::string &name)
{
	if (s.find('\n') != std::string_view::npos) return false;
	auto last = s.rfind('-');
	if (last == std::string_view::npos || last + 2 >= s.size() || s[last + 1] != 'r') return false;
	for (std::size_t k = last + 2; k < s.size(); k++)
		if (s[k] < '0' || s[k] > '9') return false;
	auto prev = last == 0 ? std::string_view::npos : s.rfind('-', last - 1);
	if (prev == std::string_view::npos || prev == 0) return false;
	if (s[prev + 1] < '0' || s[prev + 1] > '9') return false;
	name = std::string(s.substr(0, prev));
	return true;
}

bool regex_space(char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'; }

// apkStepNumber: ^\((?:\s*[0-9]+/[0-9]+|\?)\)\s+ ; the length matched, or 0.
std::size_t step_number(std::string_view s)
{
	std::size_t i = 0;
	if (i >= s.size() || s[i] != '(') return 0;
	i++;
	if (i < s.size() && s[i] == '?') {
		i++;
	} else {
		while (i < s.size() && regex_space(s[i])) i++;
		std::size_t start = i;
		while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		if (i == start || i >= s.size() || s[i] != '/') return 0;
		i++;
		start = i;
		while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		if (i == start) return 0;
	}
	if (i >= s.size() || s[i] != ')') return 0;
	i++;
	std::size_t start = i;
	while (i < s.size() && regex_space(s[i])) i++;
	return i == start ? 0 : i;
}

bool parse_apk_step(const std::string &raw, std::string &action, std::string &name, std::string &detail)
{
	std::string line = trim_space(raw);
	std::string_view s = line;
	std::size_t i = step_number(s);
	if (!i) return false;
	static const char *const actions[] = {"Installing", "Purging", "Reinstalling", "[APK unavailable, skipped] Reinstalling",
	                                      "Updating pinning", "Downgrading", "Replacing", "Upgrading"};
	for (const char *a : actions) {
		std::string_view act = a;
		if (s.substr(i, act.size()) != act) continue;
		std::size_t p = i + act.size(), start = p;
		while (p < s.size() && regex_space(s[p])) p++;
		if (p == start) continue;
		start = p;
		while (p < s.size() && !regex_space(s[p]) && s[p] != '@') p++;
		if (p == start) continue;
		std::string n(s.substr(start, p - start));
		if (p < s.size() && s[p] == '@') {
			std::size_t t = ++p;
			while (p < s.size() && !regex_space(s[p])) p++;
			if (p == t) continue;
		}
		start = p;
		while (p < s.size() && regex_space(s[p])) p++;
		if (p == start || p >= s.size() || s[p] != '(') continue;
		if (s.back() != ')' || s.size() < p + 2) continue;
		std::string_view d = s.substr(p + 1, s.size() - p - 2);
		if (d.find('\n') != std::string_view::npos) continue;
		action = a;
		name = n;
		detail = std::string(d);
		return true;
	}
	return false;
}

} // namespace detail

using detail::apk_package_name;
using detail::apk_package_version;
using detail::apk_versioned_name;

bool valid_package_name(const std::string &name) { return apk_package_name(name); }

std::vector<std::string> other_install_changes(const std::string &simulation, const std::vector<std::string> &names)
{
	std::vector<std::string> changed;
	for (const auto &raw : split(simulation, "\n")) {
		std::string action, name, detail;
		if (!detail::parse_apk_step(raw, action, name, detail)) {
			std::string line = trim_space(raw);
			if (detail::step_number(line)) changed.push_back(line);
			continue;
		}
		if (action != "Installing" || std::find(names.begin(), names.end(), name) == names.end())
			changed.push_back(action + " " + name);
	}
	return changed;
}

std::vector<std::string> kept_by_removal(const std::string &simulation, const std::vector<std::string> &names)
{
	std::set<std::string> purged;
	for (const auto &raw : split(simulation, "\n")) {
		std::string action, name, detail;
		if (detail::parse_apk_step(raw, action, name, detail) && action == "Purging") purged.insert(name);
	}
	std::vector<std::string> kept;
	for (const auto &name : names)
		if (!purged.contains(name)) kept.push_back(name);
	return kept;
}

std::optional<std::vector<std::string>> installed(const std::vector<std::string> &names, Error &error)
{
	int err = 0;
	auto data = files::read(installed_db_path, err);
	if (!data) return error = files::read_error(installed_db_path, err), std::nullopt;
	auto versions = parse_installed_db(*data);
	if (versions.empty()) return error = "the APK installed database records no package", std::nullopt;
	std::vector<std::string> present;
	for (const auto &name : names)
		if (versions.contains(name)) present.push_back(name);
	return present;
}

std::vector<std::string> installed_changes(const std::string &simulation, const std::vector<std::string> &reinstall)
{
	std::vector<std::string> changed;
	for (const auto &raw : split(simulation, "\n")) {
		std::string action, name, detail;
		if (!detail::parse_apk_step(raw, action, name, detail)) {
			std::string line = trim_space(raw);
			if (detail::step_number(line)) changed.push_back(line);
			continue;
		}
		if (action != "Installing" &&
		    !(action == "Reinstalling" && std::find(reinstall.begin(), reinstall.end(), name) != reinstall.end()))
			changed.push_back(action + " " + name);
	}
	return changed;
}

std::string bounded_upgrades(const std::string &plan, const std::vector<std::string> &bounded)
{
	std::vector<std::string> lines;
	for (const auto &raw : split(plan, "\n")) {
		std::string action, name, detail;
		if (!detail::parse_apk_step(raw, action, name, detail) || action != "Upgrading" ||
		    std::find(bounded.begin(), bounded.end(), name) == bounded.end())
			continue;
		auto arrow = detail.find(" -> ");
		if (arrow == std::string::npos) continue;
		std::string from = detail.substr(0, arrow), to = detail.substr(arrow + 4);
		if ((from + to).find_first_of(" ()") != std::string::npos) continue;
		lines.push_back(name + "-" + from + " < " + to);
	}
	return gostr::join(lines, "\n");
}

namespace {

std::vector<std::string> world_entries()
{
	auto data = files::read(world_path);
	if (!data) return {};
	return fields(*data);
}

bool is_nanokvm_selection(const std::string &selection)
{
	std::string name = selection.substr(0, selection.find('@'));
	return name == "nanokvm" || has_prefix(name, "nanokvm-");
}

} // namespace

std::vector<std::string> nanokvm_file_pins()
{
	std::vector<std::string> names;
	for (const auto &entry : world_entries()) {
		auto at = entry.find("><");
		if (at != std::string::npos && is_nanokvm_selection(entry.substr(0, at))) names.push_back(entry.substr(0, at));
	}
	return names;
}

void upgrade_selections(std::vector<std::string> &skip, std::vector<std::string> &bounded)
{
	for (const auto &entry : world_entries()) {
		auto at = entry.find("><");
		if (at != std::string::npos) {
			std::string name = entry.substr(0, at);
			if (!is_nanokvm_selection(name)) skip.push_back(name.substr(0, name.find('@')));
			continue;
		}
		auto i = entry.find_first_of("=<>~");
		if (i == std::string::npos || entry[i] == '>') continue;
		std::string name = entry.substr(0, i);
		name = name.substr(0, name.find('@'));
		skip.push_back(name);
		bounded.push_back(name);
	}
}

std::string installable_upgrades(const std::string &listing, const std::vector<std::string> &skip)
{
	std::vector<std::string> lines;
	for (const auto &line : split(trim_space(listing), "\n")) {
		auto f = fields(line);
		if (f.empty() || f[0] == "Installed:") continue;
		std::string name;
		if (apk_versioned_name(f[0], name) && std::find(skip.begin(), skip.end(), name) != skip.end()) continue;
		lines.push_back(trim_space(line));
	}
	return gostr::join(lines, "\n");
}

std::string pending_upgrades(const std::string &listing, const std::map<std::string, std::string> &installed)
{
	std::vector<std::string> lines;
	for (auto line : split(listing, "\n")) {
		auto f = fields(line);
		if (f.size() < 2) continue;
		std::string name;
		if (!apk_versioned_name(f[0], name)) {
			lines.push_back(line);
			continue;
		}
		auto it = installed.find(name);
		if (it != installed.end() && compare_apk_versions(f.back(), it->second) > 0) {
			if (f[0] != name + "-" + it->second) {
				f[0] = name + "-" + it->second;
				line = gostr::join(f, " ");
			}
			lines.push_back(line);
		}
	}
	return gostr::join(lines, "\n");
}

std::map<std::string, std::string> parse_installed_db(const std::string &data)
{
	std::map<std::string, std::string> versions;
	std::string name;
	for (const auto &line : split(data, "\n")) {
		if (line.empty()) name.clear();
		else if (has_prefix(line, "P:")) name = line.substr(2);
		else if (has_prefix(line, "V:") && !name.empty()) versions[name] = line.substr(2);
	}
	return versions;
}

namespace {

struct DbStamp {
	std::int64_t mtime_ns = -1, size = -1;
	bool operator==(const DbStamp &) const = default;
};

bool db_stamp(DbStamp &stamp, int &err)
{
	struct stat st{};
	if (::stat(installed_db_path, &st) != 0) {
		err = errno;
		return false;
	}
	stamp.mtime_ns = static_cast<std::int64_t>(st.st_mtim.tv_sec) * 1000000000 + st.st_mtim.tv_nsec;
	stamp.size = st.st_size;
	return true;
}

std::mutex versions_mutex;
DbStamp versions_stamp;
std::optional<std::map<std::string, std::string>> versions_cache;

} // namespace

std::optional<std::map<std::string, std::string>> installed_versions(Error &error)
{
	DbStamp stamp;
	int err = 0;
	if (!db_stamp(stamp, err)) return error = goerr::path_error("stat", installed_db_path, err), std::nullopt;
	std::lock_guard lock(versions_mutex);
	if (!versions_cache || !(stamp == versions_stamp)) {
		auto data = files::read(installed_db_path, err);
		if (!data) return error = files::read_error(installed_db_path, err), std::nullopt;
		versions_cache = parse_installed_db(*data);
		versions_stamp = stamp;
	}
	return versions_cache;
}

namespace {

std::string dependency_name(std::string dep)
{
	auto i = dep.find_first_of("<>=~");
	if (i != std::string::npos) dep.resize(i);
	return dep;
}

struct PackageGraph {
	std::map<std::string, bool> healthy;
	std::map<std::string, std::vector<std::string>> depends, providers;
};

PackageGraph parse_package_graph(const std::string &data)
{
	PackageGraph db;
	std::string name;
	for (const auto &line : split(data, "\n")) {
		if (line.empty()) {
			name.clear();
		} else if (has_prefix(line, "P:")) {
			name = line.substr(2);
			db.healthy[name] = true;
			db.providers[name].push_back(name);
		} else if (name.empty()) {
		} else if (has_prefix(line, "p:")) {
			for (auto provided : fields(line.substr(2))) {
				provided = provided.substr(0, provided.find('='));
				db.providers[provided].push_back(name);
			}
		} else if (has_prefix(line, "D:")) {
			for (const auto &dep : fields(line.substr(2)))
				if (!has_prefix(dep, "!")) db.depends[name].push_back(dependency_name(dep));
		} else if (has_prefix(line, "f:")) {
			if (line.find_first_of("fs", 2) != std::string::npos) db.healthy[name] = false;
		}
	}
	return db;
}

} // namespace

bool package_state(const std::vector<std::string> &names, std::vector<std::string> &missing, std::vector<std::string> &broken,
                   Error &error)
{
	int err = 0;
	auto data = files::read(installed_db_path, err);
	if (!data) return error = files::read_error(installed_db_path, err), false;
	PackageGraph db = parse_package_graph(*data);
	std::set<std::string> seen;
	std::vector<std::string> queue;
	for (const auto &name : names) {
		if (!db.healthy.count(name)) missing.push_back(name);
		else if (!seen.count(name)) seen.insert(name), queue.push_back(name);
	}
	for (std::size_t q = 0; q < queue.size(); q++) {
		std::string name = queue[q];
		bool unprovided = false;
		for (const auto &dep : db.depends[name])
			if (db.providers[dep].empty()) unprovided = true;
		if (!db.healthy[name] || unprovided) broken.push_back(name);
		for (const auto &dep : db.depends[name])
			for (const auto &provider : db.providers[dep])
				if (!seen.count(provider)) seen.insert(provider), queue.push_back(provider);
	}
	return true;
}

// ---- APK update page (apk.go) ----

namespace {

void decode_apk_status(const std::string &data, ApkStatus &s)
{
	auto doc = godecode::parse_document(data);
	if (!doc || doc->kind != godecode::Value::Kind::object) return;
	for (const auto &[k, v] : doc->members) {
		if (godecode::key_matches(k, "state")) godecode::get(v, s.state);
		else if (godecode::key_matches(k, "message")) godecode::get(v, s.message);
		else if (godecode::key_matches(k, "upgrades")) godecode::get(v, s.upgrades);
		else if (godecode::key_matches(k, "log")) godecode::get(v, s.log);
		else if (godecode::key_matches(k, "reboot")) godecode::get(v, s.reboot);
		else if (godecode::key_matches(k, "kernel")) godecode::get(v, s.kernel);
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
		if (n <= 0) break;
		out.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	ok = true;
	return out;
}

} // namespace

Error save_apk_status(const ApkStatus &s)
{
	if (auto e = files::mkdir_all(apk_state_dir, 0700); !e.empty()) return e;
	std::string tmp = std::string(apk_state_dir) + "/state.tmp";
	int fd = ::open(tmp.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
	if (fd < 0) return goerr::path_error("open", tmp, errno);
	std::string data = to_json(s);
	Error e;
	if (::write(fd, data.data(), data.size()) != static_cast<ssize_t>(data.size())) e = goerr::path_error("write", tmp, errno);
	::close(fd);
	if (!e.empty()) return e;
	if (::rename(tmp.c_str(), (std::string(apk_state_dir) + "/state.json").c_str()) != 0)
		return "rename " + tmp + " " + apk_state_dir + "/state.json: " + goerr::errno_text(errno);
	return "";
}

Error save_apk_status_idle() { return save_apk_status(ApkStatus{}); }

std::string to_json(const ApkStatus &s)
{
	std::string out = "{\"state\":" + json::str(s.state);
	if (!s.message.empty()) out += ",\"message\":" + json::str(s.message);
	if (!s.upgrades.empty()) out += ",\"upgrades\":" + json::str(s.upgrades);
	if (!s.log.empty()) out += ",\"log\":" + json::str(s.log);
	return out + ",\"reboot\":" + json::boolean(s.reboot) + ",\"kernel\":" + json::str(s.kernel) + "}";
}

ApkStatus get_apk_status()
{
	ApkStatus s;
	if (auto data = files::read(std::string(apk_state_dir) + "/state.json")) decode_apk_status(*data, s);
	if (s.state == "ready") {
		Error e;
		if (auto installed = installed_versions(e)) {
			s.upgrades = pending_upgrades(s.upgrades, *installed);
			if (s.upgrades.empty()) s.state = "up-to-date";
		}
	}
	s.reboot = files::exists("/run/reboot-required");
	s.kernel = trim_space(files::read("/proc/sys/kernel/osrelease").value_or(""));
	bool ok = false;
	std::string log = read_tail(std::string(apk_state_dir) + "/operation.log", 65536, ok);
	if (ok) s.log = log;
	return s;
}

Error start_apk(const std::string &action)
{
	static const std::map<std::string, std::string> states{
		{"check", "checking"}, {"install", "installing"}, {"rollback", "installing"}, {"reboot", "rebooting"}};
	auto state = states.find(action);
	if (state == states.end()) return "invalid package operation";
	if (!files::exists("/etc/alpine-release")) return "native APK updates require Alpine";
	Error e;
	auto l = lock(e);
	if (!l) return e;
	ApkStatus s;
	s.state = state->second;
	if (auto err = save_apk_status(s); !err.empty()) return err;
	std::string log_path = std::string(apk_state_dir) + "/operation.log";
	int log = ::open(log_path.c_str(), O_CREAT | O_TRUNC | O_WRONLY | O_CLOEXEC, 0600);
	if (log < 0) return goerr::path_error("open", log_path, errno);
	bool started = start_detached({helper, "apk-inherited", action}, log, l->fd(), e);
	::close(log);
	if (!started) {
		ApkStatus failed;
		failed.state = "failed";
		failed.message = e;
		(void)save_apk_status(failed);
		return e;
	}
	return "";
}

// ---- Channel (channel.go) ----

bool is_core_package(const std::string &name) { return std::find(core_packages.begin(), core_packages.end(), name) != core_packages.end(); }

bool prerelease(const std::string &version)
{
	for (const char *s : {"_alpha", "_beta", "_pre", "_rc"})
		if (version.find(s) != std::string::npos) return true;
	return false;
}

namespace {

// ^https://dormancygrace\.github\.io/NanoKVM-OS/(beta/)?alpine/(v[0-9]+\.[0-9]+)/riscv64(?:/Packages\.adb)?/?$
bool channel_repository(std::string_view line, bool &beta, std::string &branch)
{
	std::string_view prefix = "https://dormancygrace.github.io/NanoKVM-OS/";
	if (!has_prefix(line, prefix)) return false;
	line.remove_prefix(prefix.size());
	beta = has_prefix(line, "beta/");
	if (beta) line.remove_prefix(5);
	if (!has_prefix(line, "alpine/v")) return false;
	line.remove_prefix(7);
	std::size_t i = 1;
	std::size_t start = i;
	while (i < line.size() && line[i] >= '0' && line[i] <= '9') i++;
	if (i == start || i >= line.size() || line[i] != '.') return false;
	i++;
	start = i;
	while (i < line.size() && line[i] >= '0' && line[i] <= '9') i++;
	if (i == start) return false;
	branch = std::string(line.substr(0, i));
	line.remove_prefix(i);
	if (!has_prefix(line, "/riscv64")) return false;
	line.remove_prefix(8);
	if (has_prefix(line, "/Packages.adb")) line.remove_prefix(13);
	return line.empty() || line == "/";
}

} // namespace

int channel_line(const std::string &data, std::string &branch, ChannelStatus &status)
{
	int found = -1;
	branch.clear();
	status = ChannelStatus{};
	auto lines = split(data, "\n");
	for (std::size_t i = 0; i < lines.size(); i++) {
		std::string line = trim_space(lines[i]);
		if (line.empty() || has_prefix(line, "#")) continue;
		bool beta = false;
		std::string b;
		if (!channel_repository(line, beta, b)) {
			if (gostr::to_lower_ascii(line).find("nanokvm") != std::string::npos) {
				branch.clear();
				status = ChannelStatus{};
				return -1;
			}
			continue;
		}
		if (found >= 0) {
			branch.clear();
			status = ChannelStatus{};
			return -1;
		}
		found = static_cast<int>(i);
		branch = b;
		status.channel = beta ? "beta" : "stable";
	}
	status.available = found >= 0;
	return found;
}

ChannelStatus get_channel()
{
	auto data = files::read(repositories_path);
	if (!data) return ChannelStatus{};
	std::string branch;
	ChannelStatus status;
	channel_line(*data, branch, status);
	if (status.available && status.channel == "stable") {
		for (const auto &c : installed_components())
			if (is_core_package(c.name) && prerelease(c.installed)) {
				status.rollback_available = true;
				break;
			}
	}
	return status;
}

std::string to_json(const ChannelStatus &c)
{
	return "{\"channel\":" + json::str(c.channel) + ",\"available\":" + json::boolean(c.available) +
	       ",\"rollbackAvailable\":" + json::boolean(c.rollback_available) + "}";
}

namespace {

// lockCommunityDB: apk's database lock, so that apk stays off the repository list while it changes.
std::optional<UpdateLock> lock_community_db(Error &error)
{
	int fd = ::open("/lib/apk/db/lock", O_RDWR | O_CREAT | O_CLOEXEC | O_NOFOLLOW, 0600);
	if (fd < 0) return error = goerr::errno_text(errno), std::nullopt;
	if (::flock(fd, LOCK_EX | LOCK_NB) != 0) {
		::close(fd);
		return error = "apk database is in use", std::nullopt;
	}
	return UpdateLock(fd);
}

} // namespace

Error set_channel(const std::string &channel)
{
	if (channel != "stable" && channel != "beta") return "invalid update channel";
	Error e;
	auto l = lock(e);
	if (!l) return e;
	bool busy = false;
	auto notice_lock = lock_notice(false, busy, e);
	if (!e.empty()) return e;
	if (!notice_lock) return "update check is running; try again when it finishes";
	auto db_lock = lock_community_db(e);
	if (!db_lock) return e;
	int err = 0;
	auto data = files::read(repositories_path, err);
	if (!data) return files::read_error(repositories_path, err);
	std::string branch;
	ChannelStatus status;
	int i = channel_line(*data, branch, status);
	if (!status.available) return "update channel requires one official NanoKVM OS repository";
	if (status.channel == channel) return "";
	auto lines = split(*data, "\n");
	lines[static_cast<std::size_t>(i)] = "https://dormancygrace.github.io/NanoKVM-OS/" + std::string(channel == "beta" ? "beta/" : "") +
	                                     "alpine/" + branch + "/riscv64/Packages.adb";
	UpdateNotice cleared;
	cleared.state = "never";
	cleared.updates = std::vector<SoftwareUpdate>{};
	if (auto err2 = save_notice(cleared); !err2.empty()) return err2;
	if (auto err2 = save_apk_status(ApkStatus{}); !err2.empty()) return err2;
	return write_repositories(gostr::join(lines, "\n"));
}

std::vector<std::string> stable_package_targets(const std::string &output, Error &error)
{
	std::map<std::string, std::string> found;
	for (const auto &line : fields(output)) {
		std::string name, version;
		if (!apk_package_version(line, name, version) || !is_core_package(name)) continue;
		if (prerelease(version)) return error = "stable repository contains prerelease packages", std::vector<std::string>{};
		if (!found[name].empty()) return error = "ambiguous stable package " + name, std::vector<std::string>{};
		found[name] = version;
	}
	auto release = found.find("nanokvm-release");
	if (release != found.end() && compare_apk_versions(release->second, "5.0.0_alpha0") < 0)
		return error = "to return to NanoKVM OS 4.x, write a 4.x image to the SD card", std::vector<std::string>{};
	std::vector<std::string> targets;
	for (const auto &name : core_packages) {
		if (found[name].empty()) {
			if (name == "nanokvm-app" && !found["nanokvm-release"].empty()) {
				targets.push_back(name + "=" + found["nanokvm-release"]);
				continue;
			}
			if (name == "nanokvm-openssl") {
				targets.insert(targets.end(), {"!nanokvm-openssl", "openssl"});
				continue;
			}
			if (name == "nanokvm-openssl3" || name == "nanokvm-exfatprogs1" || name == "nanokvm-tpu") {
				targets.push_back("!" + name);
				continue;
			}
			return error = "stable repository is missing " + name, std::vector<std::string>{};
		}
		targets.push_back(name + "=" + found[name]);
	}
	return targets;
}

std::vector<std::string> tpu_dependents(const std::vector<std::string> &targets, const std::string &installed_db)
{
	std::vector<std::string> names;
	if (std::find(targets.begin(), targets.end(), "!nanokvm-tpu") == targets.end()) return names;
	for (const auto &record : split(installed_db, "\n\n")) {
		std::string name;
		bool needs = false;
		for (const auto &line : split(record, "\n")) {
			if (has_prefix(line, "P:")) name = line.substr(2);
			if (has_prefix(line, "D:"))
				for (const auto &dep : fields(line.substr(2)))
					needs = needs || dep == "nanokvm-tpu-abi" || has_prefix(dep, "nanokvm-tpu-abi=");
		}
		if (!name.empty() && needs) names.push_back(name);
	}
	return names;
}

// ---- Community repository (community.go) ----

namespace {

std::vector<std::string> active_repository_lines(const std::string &repositories)
{
	std::vector<std::string> lines;
	for (const auto &raw : split(repositories, "\n")) {
		std::string line = trim_space(raw);
		if (!line.empty() && !has_prefix(line, "#")) lines.push_back(line);
	}
	return lines;
}

std::string trim_right_slash(std::string s)
{
	while (!s.empty() && s.back() == '/') s.pop_back();
	return s;
}

std::string trim_suffix(const std::string &s, std::string_view suffix)
{
	return has_suffix(s, suffix) ? s.substr(0, s.size() - suffix.size()) : s;
}

} // namespace

std::string community_url(const std::string &repositories)
{
	for (const auto &line : active_repository_lines(repositories)) {
		if (has_prefix(line, "@")) continue;
		std::string url = trim_right_slash(line);
		if (has_suffix(url, "/main") && url.find("alpine") != std::string::npos) return trim_suffix(url, "/main") + "/community";
	}
	return "";
}

bool has_community(const std::string &repositories, const std::string &url)
{
	std::string trimmed = trim_suffix(url, "/community");
	auto slash = trimmed.rfind('/');
	std::string branch = url.substr(slash == std::string::npos ? 0 : slash);
	for (const auto &line : active_repository_lines(repositories))
		if (!has_prefix(line, "@") && has_suffix(trim_right_slash(line), branch)) return true;
	return false;
}

std::string with_community(const std::string &repositories, const std::string &url)
{
	std::string main = trim_suffix(url, "/community") + "/main";
	std::vector<std::string> out;
	bool added = false;
	for (const auto &line : split(repositories, "\n")) {
		out.push_back(line);
		std::string trimmed = trim_space(line);
		if (!added && !has_prefix(trimmed, "@") && trim_right_slash(trimmed) == main) {
			out.push_back(url);
			added = true;
		}
	}
	return gostr::join(out, "\n");
}

Error enable_community_locked()
{
	Error e;
	auto db_lock = lock_community_db(e);
	if (!db_lock) return e;
	int err = 0;
	auto data = files::read(repositories_path, err);
	if (!data) return files::read_error(repositories_path, err);
	std::string url = community_url(*data);
	if (url.empty()) return "no Alpine main repository is listed; the community repository cannot be derived";
	data = files::read(repositories_path, err);
	if (!data) return files::read_error(repositories_path, err);
	if (has_community(*data, url)) return "";
	return write_repositories(with_community(*data, url));
}

// ---- Rollback recovery (rollback_recovery.go) ----

bool rollback_constraint_installed()
{
	auto data = files::read(installed_db_path);
	return data && ("\n" + *data).find("\nP:.nanokvm-stable-rollback\n") != std::string::npos;
}

Error recover_interrupted_rollback()
{
	if (!rollback_constraint_installed()) return "";
	Error e;
	auto l = lock(e);
	if (!l) return e;
	if (!rollback_constraint_installed()) return "";
	apkrun::Cmd cmd;
	cmd.path = apk_executable;
	cmd.args = {"--no-network", "del", ".nanokvm-stable-rollback"};
	cmd.deadline = apkrun::Clock::now() + check_timeout;
	auto r = run_quiet(cmd);
	if (!r.ok) return r.error;
	return remove_rollback_recovery();
}

} // namespace nkos::osupdate
