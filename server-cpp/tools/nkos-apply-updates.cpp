// nkos-apply-updates applies APK transactions to running services (the apk commit hooks): pre-commit records what
// every running service, legacy daemon, the managed OpenVPN profile and the boot components consist of; post-commit
// starts a detached worker; apply restarts what changed in OpenRC's order and defers what needs a reboot. The files
// in /run/nanokvm-apk are the Go helper's, byte for byte: an upgrade from it records with the old binary and applies
// with this one.

#include "common/gojson.hpp"
#include "common/time.hpp"

#include <glaze/glaze.hpp>

#include <algorithm>
#include <cerrno>
#include <chrono>
#include <csignal>
#include <cstdio>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <fnmatch.h>
#include <glob.h>
#include <map>
#include <optional>
#include <string>
#include <sys/file.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>
#include <vector>

namespace {

using namespace std::chrono_literals;
using nkos::gojson::quote;

const std::string state_dir = "/run/nanokvm-apk";

struct Stamp {
	std::uint64_t inode = 0, device = 0;
	std::int64_t size = 0, modified = 0;
	std::uint32_t mode = 0;
	bool operator==(const Stamp &) const = default;
};

struct Target {
	std::string name, kind;
	std::optional<std::vector<std::string>> paths, trees;  // nullopt: Go's nil slice (null)
	std::optional<std::map<std::string, Stamp>> before;
	std::optional<std::map<int, std::string>> pids;
};

struct Snapshot {
	std::optional<std::vector<Target>> targets;
	std::string created;
};

struct Report {
	std::string state;
	std::optional<std::vector<std::string>> restarted, deferred, failed;
	std::string finished = "0001-01-01T00:00:00Z";
};

struct Process {
	int pid = 0;
	std::string exe, arg0, name, identity;
	std::vector<std::string> maps;
};

void push(std::optional<std::vector<std::string>> &v, std::string s)
{
	if (!v) v.emplace();
	v->push_back(std::move(s));
}

bool exists(const std::string &p)
{
	struct stat st{};
	return ::stat(p.c_str(), &st) == 0;
}

std::optional<std::string> read_file(const std::string &p)
{
	FILE *f = std::fopen(p.c_str(), "rb");
	if (!f) return std::nullopt;
	std::string out;
	char buf[65536];
	std::size_t n;
	while ((n = std::fread(buf, 1, sizeof buf, f)) > 0) out.append(buf, n);
	std::fclose(f);
	return out;
}

std::string trim(std::string_view s)
{
	auto space = [](unsigned char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'; };
	while (!s.empty() && space(s.front())) s.remove_prefix(1);
	while (!s.empty() && space(s.back())) s.remove_suffix(1);
	return std::string(s);
}

std::vector<std::string> fields(std::string_view s)
{
	std::vector<std::string> out;
	std::size_t i = 0;
	auto space = [](unsigned char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'; };
	while (i < s.size()) {
		while (i < s.size() && space(s[i])) i++;
		std::size_t start = i;
		while (i < s.size() && !space(s[i])) i++;
		if (i > start) out.emplace_back(s.substr(start, i - start));
	}
	return out;
}

std::vector<std::string> split(std::string_view s, char sep)
{
	std::vector<std::string> out;
	std::size_t start = 0;
	for (;;) {
		auto at = s.find(sep, start);
		out.emplace_back(s.substr(start, at == std::string_view::npos ? std::string_view::npos : at - start));
		if (at == std::string_view::npos) return out;
		start = at + 1;
	}
}

// strconv.Atoi: optional sign and decimal digits only; 0 on anything else.
int atoi_go(const std::string &s)
{
	if (s.empty()) return 0;
	std::size_t i = (s[0] == '+' || s[0] == '-') ? 1 : 0;
	if (i == s.size()) return 0;
	long long v = 0;
	for (std::size_t j = i; j < s.size(); j++) {
		if (s[j] < '0' || s[j] > '9') return 0;
		v = v * 10 + (s[j] - '0');
		if (v > 0x7fffffffffffffffLL / 10) return 0;
	}
	return static_cast<int>(s[0] == '-' ? -v : v);
}

std::vector<std::string> glob_sorted(const std::string &pattern)
{
	std::vector<std::string> out;
	glob_t g{};
	if (::glob(pattern.c_str(), 0, nullptr, &g) == 0)
		for (std::size_t i = 0; i < g.gl_pathc; i++) out.emplace_back(g.gl_pathv[i]);
	globfree(&g);
	std::sort(out.begin(), out.end());
	return out;
}

std::string base_name(const std::string &p)
{
	if (p.empty()) return ".";
	std::string s = p;
	while (s.size() > 1 && s.back() == '/') s.pop_back();
	auto at = s.rfind('/');
	return at == std::string::npos ? s : s.substr(at + 1);
}

// Go's FileMode of a stat result (os.Stat follows links): permission bits plus Go's own type and special bits.
std::uint32_t go_mode(const struct stat &st)
{
	std::uint32_t m = st.st_mode & 0777;
	switch (st.st_mode & S_IFMT) {
	case S_IFBLK: m |= 1u << 26; break;                  // ModeDevice
	case S_IFCHR: m |= (1u << 26) | (1u << 21); break;   // ModeDevice | ModeCharDevice
	case S_IFDIR: m |= 1u << 31; break;
	case S_IFIFO: m |= 1u << 25; break;
	case S_IFLNK: m |= 1u << 27; break;
	case S_IFSOCK: m |= 1u << 24; break;
	}
	if (st.st_mode & S_ISUID) m |= 1u << 23;
	if (st.st_mode & S_ISGID) m |= 1u << 22;
	if (st.st_mode & S_ISVTX) m |= 1u << 20;
	return m;
}

void add_stamp(std::map<std::string, Stamp> &out, const std::string &p)
{
	struct stat st{};
	if (::stat(p.c_str(), &st) != 0 || S_ISDIR(st.st_mode)) return;
	out[p] = Stamp{static_cast<std::uint64_t>(st.st_ino), static_cast<std::uint64_t>(st.st_dev), static_cast<std::int64_t>(st.st_size),
	               static_cast<std::int64_t>(st.st_mtim.tv_sec) * 1'000'000'000 + st.st_mtim.tv_nsec, go_mode(st)};
}

// filepath.WalkDir: links are not followed; unreadable directories are skipped.
void walk(std::map<std::string, Stamp> &out, const std::string &dir)
{
	DIR *d = ::opendir(dir.c_str());
	if (!d) return;
	std::vector<std::string> names;
	while (auto *e = ::readdir(d)) {
		std::string n = e->d_name;
		if (n != "." && n != "..") names.push_back(n);
	}
	::closedir(d);
	std::sort(names.begin(), names.end());
	for (const auto &n : names) {
		std::string p = dir == "/" ? "/" + n : dir + "/" + n;
		struct stat st{};
		if (::lstat(p.c_str(), &st) != 0) continue;
		if (S_ISDIR(st.st_mode)) walk(out, p);
		else add_stamp(out, p);
	}
}

std::map<std::string, Stamp> fingerprint(const std::optional<std::vector<std::string>> &paths,
                                         const std::optional<std::vector<std::string>> &trees)
{
	std::map<std::string, Stamp> out;
	if (paths)
		for (const auto &p : *paths) add_stamp(out, p);
	if (trees)
		for (const auto &root : *trees) {
			struct stat st{};
			if (::lstat(root.c_str(), &st) != 0) continue;
			if (S_ISDIR(st.st_mode)) walk(out, root);
			else add_stamp(out, root);
		}
	return out;
}

std::string identity(int pid)
{
	auto b = read_file("/proc/" + std::to_string(pid) + "/stat");
	if (!b) return "";
	auto at = b->find(") ");
	if (at == std::string::npos) return "";
	auto f = fields(std::string_view(*b).substr(at + 2));
	if (f.size() < 20) return "";
	return f[19];
}

std::vector<Process> processes()
{
	std::vector<Process> result;
	for (const auto &d : glob_sorted("/proc/[0-9]*")) {
		int pid = atoi_go(base_name(d));
		char buf[4096];
		ssize_t n = ::readlink((d + "/exe").c_str(), buf, sizeof buf - 1);
		if (n < 0) continue;
		std::string exe(buf, static_cast<std::size_t>(n));
		if (exe.ends_with(" (deleted)")) exe.resize(exe.size() - 10);
		std::string cmd = read_file(d + "/cmdline").value_or("");
		std::string arg0 = split(cmd, '\0')[0];
		Process p{pid, exe, arg0, trim(read_file(d + "/comm").value_or("")), identity(pid), {}};
		for (const auto &line : split(read_file(d + "/maps").value_or(""), '\n')) {
			auto f = fields(line);
			if (f.size() >= 6 && f[5].starts_with("/") && (f[1].find('x') != std::string::npos || f[5].find(".so") != std::string::npos))
				p.maps.push_back(f[5]);
		}
		result.push_back(std::move(p));
	}
	return result;
}

void add_process(Target &t, const Process &p)
{
	push(t.paths, p.exe);
	for (const auto &m : p.maps) push(t.paths, m);
	if (!t.pids) t.pids.emplace();
	(*t.pids)[p.pid] = p.identity;
}

// Only the active profile owned by NanoKVM, never an arbitrary manually started OpenVPN process.
bool managed_vpn(int pid)
{
	auto b = read_file("/etc/kvm/openvpn/active");
	if (!b) return false;
	std::string id = trim(*b);
	if (id.empty() || id.find_first_of("/\\. \t\n") != std::string::npos) return false;
	b = read_file("/run/nkos-openvpn/" + id + ".pid");
	if (!b || atoi_go(trim(*b)) != pid) return false;
	b = read_file("/proc/" + std::to_string(pid) + "/cmdline");
	if (!b) return false;
	auto args = split(*b, '\0');
	for (std::size_t i = 0; i < args.size(); i++)
		if (args[i] == "--config" && i + 1 < args.size() && args[i + 1] == "/etc/kvm/openvpn/" + id + ".ovpn") return true;
	return false;
}

Snapshot gather()
{
	auto all = processes();
	Snapshot s;
	s.created = nkos::rfc3339nano(nkos::WallClock::now());
	auto add_target = [&](Target t) {
		if (!s.targets) s.targets.emplace();
		s.targets->push_back(std::move(t));
	};
	for (const auto &entry : glob_sorted("/run/openrc/started/*")) {
		std::string name = base_name(entry);
		Target t{name, "service", std::vector<std::string>{"/etc/init.d/" + name, "/etc/conf.d/" + name}, {}, {}, {}};
		auto records = glob_sorted("/run/openrc/daemons/" + name + "/*");
		for (const auto &record : records) {
			std::map<std::string, std::string> values;
			for (const auto &line : split(read_file(record).value_or(""), '\n')) {
				auto eq = line.find('=');
				if (eq == std::string::npos) continue;
				std::string k = line.substr(0, eq), v = line.substr(eq + 1);
				values[k] = v;
				if (k == "exec" || (k.starts_with("argv_") && v.starts_with("/etc/"))) push(t.paths, v);
			}
			int pid = atoi_go(trim(read_file(values["pidfile"]).value_or("")));
			for (const auto &p : all)
				if ((pid > 1 && p.pid == pid) || (pid == 0 && !values["exec"].empty() && (p.exe == values["exec"] || p.arg0 == values["exec"])))
					add_process(t, p);
		}
		if (name == "nanokvm-app") {
			push(t.paths, "/kvmapp/server/NanoKVM-Server");
			t.trees = std::vector<std::string>{"/kvmapp/server/dl_lib", "/kvmapp/kvm_system", "/kvmapp/system/init.d",
			                                   "/usr/share/nanokvm/edid"};
			push(t.paths, "/usr/libexec/nanokvm/legacy/S95nanokvm");
			push(t.paths, "/usr/sbin/nanokvm_update_edid");
			for (const auto &p : all)
				if (p.name == "NanoKVM-Server" || p.name == "kvm_system") add_process(t, p);
		} else if (name == "nanokvm-network") {
			for (const char *n : {"S25wifimod", "S30eth", "S30wifi"}) push(t.paths, std::string("/usr/libexec/nanokvm/legacy/") + n);
			for (const auto &p : all)
				if (p.name == "wpa_supplicant" || p.name == "udhcpc") add_process(t, p);
		} else if (name == "nanokvm-policy") {
			for (const char *n : {"S34mssclamp", "S38memory", "S94hwcrypto"}) push(t.paths, std::string("/usr/libexec/nanokvm/legacy/") + n);
		} else if (name == "nanokvm-policy-late") {
			push(t.paths, "/usr/libexec/nanokvm/legacy/S49persistent-cron");
		} else if (name == "nanokvm-watchdog") {
			push(t.paths, "/usr/libexec/nanokvm/legacy/S13nanokvm-watchdog");
			t.kind = "reboot";
		} else if (name == "procfs") {
			push(t.paths, "/etc/rc.conf.d/nanokvm-boot.conf");
			t.kind = "reboot";
		} else {
			static const std::vector<std::string> boot = {"nanokvm-board", "nanokvm-modules", "nanokvm-storage", "nanokvm-usb",
			                                              "dbus", "udev", "udev-trigger", "devfs", "sysfs", "root", "localmount",
			                                              "hwclock", "sysctl", "modules", "hwdrivers", "bootmisc", "hostname"};
			if (std::find(boot.begin(), boot.end(), name) != boot.end()) t.kind = "reboot";
		}
		if (records.empty() && !name.starts_with("nanokvm-")) {
			for (const auto &p : all)
				if (p.name == name || base_name(p.arg0) == name) add_process(t, p);
			if (!t.pids || t.pids->empty()) t.kind = "reboot";
		}
		t.before = fingerprint(t.paths, t.trees);
		add_target(std::move(t));
	}
	// Legacy optional services have no OpenRC daemon records.
	struct Legacy {
		const char *name, *comm, *script;
	};
	for (const Legacy &spec : {Legacy{"tailscaled", "tailscaled", "S98tailscaled"}, Legacy{"dnsmasq", "dnsmasq", "S80dnsmasq"},
	                           Legacy{"picoclaw", "picoclaw", "S96picoclaw"}}) {
		bool found = false;
		if (s.targets)
			for (const auto &t : *s.targets)
				if (t.name == spec.name) found = true;
		if (found) continue;
		Target t{spec.name, "legacy", std::vector<std::string>{std::string("/usr/libexec/nanokvm/legacy/") + spec.script}, {}, {}, {}};
		for (const auto &p : all)
			if (p.name == spec.comm) add_process(t, p);
		if (t.pids && !t.pids->empty()) {
			t.before = fingerprint(t.paths, std::nullopt);
			add_target(std::move(t));
		}
	}
	Target vpn{"openvpn", "openvpn", std::vector<std::string>{"/usr/sbin/openvpn"}, {}, {}, {}};
	for (const auto &p : all)
		if (p.name == "openvpn" && managed_vpn(p.pid)) add_process(vpn, p);
	if (vpn.pids && !vpn.pids->empty()) {
		vpn.before = fingerprint(vpn.paths, std::nullopt);
		add_target(std::move(vpn));
	}
	for (Target t : {Target{"kernel boot payload", "reboot", {}, std::vector<std::string>{"/usr/lib/nanokvm/boot"}, {}, {}},
	                 Target{"bootloader", "reboot", {}, std::vector<std::string>{"/usr/lib/nanokvm/bootloader"}, {}, {}},
	                 Target{"kernel modules and device firmware", "reboot", {},
	                        std::vector<std::string>{"/lib/modules", "/lib/firmware", "/usr/share/fw_vcodec"}, {}, {}},
	                 Target{"core runtime", "reboot",
	                        std::vector<std::string>{"/bin/busybox", "/sbin/init", "/sbin/openrc", "/sbin/openrc-run"}, {}, {}, {}}}) {
		t.before = fingerprint(t.paths, t.trees);
		add_target(std::move(t));
	}
	for (const auto &p : all)
		if (p.pid == 1) {
			Target t{"PID 1 libraries", "reboot", {}, {}, {}, {}};
			add_process(t, p);
			t.before = fingerprint(t.paths, std::nullopt);
			add_target(std::move(t));
		}
	return s;
}

// reflect.DeepEqual of the recorded stamps with fresh ones: a nil record never equals the (non-nil) fresh map.
bool changed(const Target &t) { return !t.before || *t.before != fingerprint(t.paths, t.trees); }

// json.MarshalIndent(v, "", "  ").
std::string strings_json(const std::optional<std::vector<std::string>> &v, const std::string &indent)
{
	if (!v) return "null";
	if (v->empty()) return "[]";
	std::string out = "[\n";
	for (std::size_t i = 0; i < v->size(); i++) out += indent + "  " + quote((*v)[i]) + (i + 1 < v->size() ? ",\n" : "\n");
	return out + indent + "]";
}

std::string snapshot_json(const Snapshot &s)
{
	std::string out = "{\n  \"Targets\": ";
	if (!s.targets) out += "null";
	else if (s.targets->empty()) out += "[]";
	else {
		out += "[\n";
		for (std::size_t i = 0; i < s.targets->size(); i++) {
			const auto &t = (*s.targets)[i];
			const std::string in = "      ";
			out += "    {\n";
			out += in + "\"Name\": " + quote(t.name) + ",\n";
			out += in + "\"Kind\": " + quote(t.kind) + ",\n";
			out += in + "\"Paths\": " + strings_json(t.paths, in) + ",\n";
			out += in + "\"Trees\": " + strings_json(t.trees, in) + ",\n";
			out += in + "\"Before\": ";
			if (!t.before) out += "null";
			else if (t.before->empty()) out += "{}";
			else {
				out += "{\n";
				std::size_t k = 0;
				for (const auto &[path, st] : *t.before) {
					const std::string i2 = in + "  ", i3 = in + "    ";
					out += i2 + quote(path) + ": {\n";
					out += i3 + "\"Inode\": " + std::to_string(st.inode) + ",\n";
					out += i3 + "\"Device\": " + std::to_string(st.device) + ",\n";
					out += i3 + "\"Size\": " + std::to_string(st.size) + ",\n";
					out += i3 + "\"Modified\": " + std::to_string(st.modified) + ",\n";
					out += i3 + "\"Mode\": " + std::to_string(st.mode) + "\n";
					out += i2 + "}" + (++k < t.before->size() ? ",\n" : "\n");
				}
				out += in + "}";
			}
			out += ",\n" + in + "\"PIDs\": ";
			if (!t.pids) out += "null";
			else if (t.pids->empty()) out += "{}";
			else {
				// encoding/json sorts map keys by their text.
				std::vector<std::pair<std::string, std::string>> keyed;
				for (const auto &[pid, id] : *t.pids) keyed.emplace_back(std::to_string(pid), id);
				std::sort(keyed.begin(), keyed.end());
				out += "{\n";
				for (std::size_t k = 0; k < keyed.size(); k++)
					out += in + "  " + quote(keyed[k].first) + ": " + quote(keyed[k].second) + (k + 1 < keyed.size() ? ",\n" : "\n");
				out += in + "}";
			}
			out += "\n    }";
			out += i + 1 < s.targets->size() ? ",\n" : "\n";
		}
		out += "  ]";
	}
	out += ",\n  \"Created\": " + quote(s.created) + "\n}";
	return out;
}

std::string report_json(const Report &r)
{
	return "{\n  \"state\": " + quote(r.state) + ",\n  \"restarted\": " + strings_json(r.restarted, "  ") +
	       ",\n  \"deferred\": " + strings_json(r.deferred, "  ") + ",\n  \"failed\": " + strings_json(r.failed, "  ") +
	       ",\n  \"finished\": " + quote(r.finished) + "\n}";
}

// The snapshot as the Go helper writes it (exact field names; integers read exactly).
struct StampJ {
	std::uint64_t Inode = 0, Device = 0;
	std::int64_t Size = 0, Modified = 0;
	std::uint32_t Mode = 0;
};
struct TargetJ {
	std::string Name, Kind;
	std::optional<std::vector<std::string>> Paths, Trees;
	std::optional<std::map<std::string, StampJ>> Before;
	std::optional<std::map<std::string, std::string>> PIDs;
};
struct SnapshotJ {
	std::optional<std::vector<TargetJ>> Targets;
	std::string Created;
};

std::optional<Snapshot> parse_snapshot(const std::string &text, std::string &error)
{
	SnapshotJ j;
	if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(j, text)) {
		error = glz::format_error(e, text);
		return std::nullopt;
	}
	Snapshot s;
	s.created = j.Created;
	if (j.Targets) {
		s.targets.emplace();
		for (auto &t : *j.Targets) {
			Target x{t.Name, t.Kind, t.Paths, t.Trees, {}, {}};
			if (t.Before) {
				x.before.emplace();
				for (const auto &[path, st] : *t.Before) (*x.before)[path] = Stamp{st.Inode, st.Device, st.Size, st.Modified, st.Mode};
			}
			if (t.PIDs) {
				x.pids.emplace();
				for (const auto &[k, id] : *t.PIDs) (*x.pids)[atoi_go(k)] = id;
			}
			s.targets->push_back(std::move(x));
		}
	}
	return s;
}

// exec.CommandContext with 90 s, output to ours; Go's error text.
std::string run(const std::vector<std::string> &args)
{
	pid_t pid = ::fork();
	if (pid < 0) return std::string("fork/exec ") + args[0] + ": " + std::strerror(errno);
	if (pid == 0) {
		std::vector<char *> argv;
		for (const auto &a : args) argv.push_back(const_cast<char *>(a.c_str()));
		argv.push_back(nullptr);
		::execvp(argv[0], argv.data());
		::_exit(127);
	}
	auto deadline = std::chrono::steady_clock::now() + 90s;
	int status = 0;
	bool killed = false;
	for (;;) {
		pid_t r = ::waitpid(pid, &status, WNOHANG);
		if (r == pid) break;
		if (r < 0 && errno != EINTR) return std::string("wait: ") + std::strerror(errno);
		if (!killed && std::chrono::steady_clock::now() >= deadline) {
			::kill(pid, SIGKILL);
			killed = true;
		}
		std::this_thread::sleep_for(20ms);
	}
	if (WIFEXITED(status)) {
		if (WEXITSTATUS(status) == 0) return "";
		return "exit status " + std::to_string(WEXITSTATUS(status));
	}
	if (WIFSIGNALED(status)) return std::string("signal: ") + (WTERMSIG(status) == SIGKILL ? "killed" : ::strsignal(WTERMSIG(status)));
	return "exit status -1";
}

bool running(const Target &t)
{
	if (t.kind == "service") return exists("/run/openrc/started/" + t.name);
	if (t.pids)
		for (const auto &[pid, id] : *t.pids)
			if (!id.empty() && identity(pid) == id) return true;
	return false;
}

std::string stop_vpn(const Target &t)
{
	if (t.pids)
		for (const auto &[pid, id] : *t.pids) {
			if (id.empty() || identity(pid) != id) continue;
			if (::kill(pid, SIGTERM) != 0 && errno != ESRCH) return std::strerror(errno);
		}
	auto deadline = std::chrono::steady_clock::now() + 8s;
	while (std::chrono::steady_clock::now() < deadline) {
		if (!running(t)) return "";
		std::this_thread::sleep_for(100ms);
	}
	return "OpenVPN did not exit; retained process, manual restart required";
}

std::pair<std::vector<Target>, std::optional<std::vector<std::string>>> plan(const Snapshot &s)
{
	std::optional<std::vector<std::string>> deferred;
	std::vector<Target> todo;
	bool app_needed = false, app_running = false;
	const std::vector<Target> none;
	const auto &targets = s.targets ? *s.targets : none;
	for (const auto &t : targets)
		if (t.name == "nanokvm-app" && running(t)) app_running = true;
	for (const auto &t : targets) {
		if (!changed(t)) continue;
		if (t.kind == "reboot" || t.kind == "firmware") {
			push(deferred, t.name);
			continue;
		}
		if (t.kind == "openvpn" && !app_running) {
			push(deferred, "OpenVPN: application stopped; reconnect profile manually");
			continue;
		}
		if (running(t)) {
			todo.push_back(t);
			if (t.kind == "openvpn") app_needed = true;
		}
	}
	if (app_needed && std::none_of(todo.begin(), todo.end(), [](const Target &t) { return t.name == "nanokvm-app"; }))
		for (const auto &t : targets)
			if (t.name == "nanokvm-app" && running(t)) todo.push_back(t);
	return {todo, deferred};
}

std::optional<std::map<std::string, int>> openrc_order(std::string &error)
{
	int fds[2];
	if (::pipe(fds) != 0) {
		error = "cannot obtain OpenRC dependency order: pipe failed";
		return std::nullopt;
	}
	pid_t pid = ::fork();
	if (pid == 0) {
		::dup2(fds[1], 1);
		int null = ::open("/dev/null", O_WRONLY);
		if (null >= 0) ::dup2(null, 2);
		::close(fds[0]);
		::close(fds[1]);
		// rc-status processes -s immediately, so formatting must precede it.
		::execlp("rc-status", "rc-status", "--nocolor", "--format", "ini", "--servicelist", static_cast<char *>(nullptr));
		::_exit(127);
	}
	::close(fds[1]);
	std::string out;
	char buf[4096];
	ssize_t n;
	auto deadline = std::chrono::steady_clock::now() + 15s;
	::fcntl(fds[0], F_SETFL, O_NONBLOCK);
	int status = 0;
	bool done = false, killed = false;
	while (!done) {
		while ((n = ::read(fds[0], buf, sizeof buf)) > 0) out.append(buf, static_cast<std::size_t>(n));
		if (::waitpid(pid, &status, WNOHANG) == pid) done = true;
		else {
			if (!killed && std::chrono::steady_clock::now() >= deadline) {
				::kill(pid, SIGKILL);
				killed = true;
			}
			std::this_thread::sleep_for(10ms);
		}
	}
	while ((n = ::read(fds[0], buf, sizeof buf)) > 0) out.append(buf, static_cast<std::size_t>(n));
	::close(fds[0]);
	if (pid < 0 || !WIFEXITED(status) || WEXITSTATUS(status) != 0) {
		std::string why = pid < 0 ? "fork failed"
		                  : WIFSIGNALED(status) ? std::string("signal: ") + (WTERMSIG(status) == SIGKILL ? "killed" : ::strsignal(WTERMSIG(status)))
		                  : WEXITSTATUS(status) == 127 ? "exec: \"rc-status\": executable file not found in $PATH"
		                                               : "exit status " + std::to_string(WEXITSTATUS(status));
		error = "cannot obtain OpenRC dependency order: " + why;
		return std::nullopt;
	}
	std::map<std::string, int> result;
	for (const auto &line : split(out, '\n')) {
		auto eq = line.find('=');
		if (eq == std::string::npos) continue;
		std::string name = trim(line.substr(0, eq));
		int rank = static_cast<int>(result.size()) + 1;
		result[name] = rank;
	}
	if (result.empty()) {
		error = "OpenRC returned an empty dependency order";
		return std::nullopt;
	}
	return result;
}

void order_targets(std::vector<Target> &todo, const std::map<std::string, int> &order)
{
	auto rank = [&](const Target &t) {
		if (t.kind == "openvpn") return 0;
		auto it = order.find(t.name);
		return it != order.end() ? it->second : static_cast<int>(order.size()) + 1;
	};
	std::stable_sort(todo.begin(), todo.end(), [&](const Target &a, const Target &b) { return rank(a) < rank(b); });
}

bool renewed_by_openrc(const std::string &name, const std::optional<std::map<int, std::string>> &before)
{
	if (!before || before->empty()) return false;
	for (const auto &[pid, id] : *before)
		if (!id.empty() && identity(pid) == id) return false;
	// The old daemon(s) exited. Confirm replacements exist rather than mistaking a crashed service or an in-progress
	// start for success.
	auto now = gather();
	if (now.targets)
		for (const auto &t : *now.targets)
			if (t.name == name && t.pids && !t.pids->empty()) return true;
	return false;
}

std::string wait_openrc(const std::string &name)
{
	auto deadline = std::chrono::steady_clock::now() + 90s;
	while (exists("/run/openrc/starting/" + name) || exists("/run/openrc/stopping/" + name)) {
		if (std::chrono::steady_clock::now() > deadline) return "OpenRC transition timed out: " + name;
		std::this_thread::sleep_for(200ms);
	}
	return "";
}

Report apply(const Snapshot &s)
{
	Report r{"applied", {}, {}, {}, {}};
	auto [todo, deferred] = plan(s);
	r.deferred = deferred;
	// OpenRC supplies the dependency order; do not duplicate its graph or disable its cascading restarts.
	std::string error;
	auto order = openrc_order(error);
	if (!order) {
		r.state = "failed";
		push(r.failed, error);
		r.finished = nkos::rfc3339nano(nkos::WallClock::now());
		return r;
	}
	order_targets(todo, *order);
	std::map<std::string, std::optional<std::map<int, std::string>>> baseline;
	auto now = gather();
	if (now.targets)
		for (const auto &t : *now.targets) baseline[t.name] = t.pids;
	for (const auto &t : todo) {
		if (t.kind == "service") {
			if (auto e = wait_openrc(t.name); !e.empty()) {
				push(r.failed, e);
				continue;
			}
		}
		if (!running(t)) continue;
		if (t.kind == "service" && renewed_by_openrc(t.name, baseline[t.name])) {
			std::printf("Already restarted by OpenRC dependency handling: %s\n", t.name.c_str());
			std::fflush(stdout);
			push(r.restarted, t.name);
			continue;
		}
		std::printf("Applying APK update: %s\n", t.name.c_str());
		std::fflush(stdout);
		std::string e;
		if (t.kind == "service") e = run({"rc-service", t.name, "restart"});
		else if (t.kind == "legacy") {
			e = run({(*t.paths)[0], "stop"});
			if (e.empty()) e = run({(*t.paths)[0], "start"});
		} else if (t.kind == "openvpn") e = stop_vpn(t);
		if (!e.empty()) push(r.failed, t.name + ": " + e);
		else push(r.restarted, t.name);
	}
	if (r.deferred && !r.deferred->empty()) {
		// APK activates the kernel before this worker runs; only reboot remains.
		int fd = ::open("/run/reboot-required.pkgs", O_APPEND | O_CREAT | O_WRONLY | O_CLOEXEC, 0644);
		if (fd >= 0) {
			for (const auto &n : *r.deferred) {
				std::string line = n + "\n";
				(void)!::write(fd, line.data(), line.size());
			}
			::close(fd);
			if (FILE *f = std::fopen("/run/reboot-required", "w")) {
				std::fputs("System components changed; see reboot-required.pkgs\n", f);
				std::fclose(f);
			}
		} else {
			push(r.failed, std::string("open /run/reboot-required.pkgs: ") + std::strerror(errno));
		}
	}
	if (r.failed && !r.failed->empty()) r.state = "failed";
	else if (r.deferred && !r.deferred->empty()) r.state = "reboot-required";
	r.finished = nkos::rfc3339nano(nkos::WallClock::now());
	return r;
}

// os.WriteFile(path+".tmp", 0600) then rename.
std::string write_json(const std::string &path, const std::string &json)
{
	std::string tmp = path + ".tmp", data = json + "\n";
	int fd = ::open(tmp.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
	if (fd < 0) return "open " + tmp + ": " + std::strerror(errno);
	bool ok = ::write(fd, data.data(), data.size()) == static_cast<ssize_t>(data.size());
	int saved = errno;
	::close(fd);
	if (!ok) return "write " + tmp + ": " + std::strerror(saved);
	if (::rename(tmp.c_str(), path.c_str()) != 0) return "rename " + tmp + " " + path + ": " + std::strerror(errno);
	return "";
}

std::string go_errno(const std::string &op, const std::string &path)
{
	return op + " " + path + ": " + std::strerror(errno);
}

// The worker did not start: the result says so, and pending stays for "nkos-apply-updates apply" to clear.
std::string start_failed(const char *exe, int error)
{
	Report failed{"failed", {}, {}, {}, "0001-01-01T00:00:00Z"};
	push(failed.failed, std::string("start ") + exe + " apply: " + std::strerror(error));
	(void)write_json(state_dir + "/result.json", report_json(failed));
	return std::string("fork/exec ") + exe + ": " + std::strerror(error) + "; run nkos-apply-updates apply";
}

std::string main_err(int argc, char **argv)
{
	// Early-boot factory reset may remove optional packages: no application services from the APK commit hook before
	// settings are applied.
	struct stat st{};
	if (::lstat("/var/lib/nanokvm-recovery/pending.json", &st) == 0) return "";
	if (argc != 2) return "usage: nkos-apply-updates pre-commit|post-commit|apply|status";
	std::string op = argv[1];
	if (op == "status") {
		auto b = read_file(state_dir + "/result.json");
		if (!b) return go_errno("open", state_dir + "/result.json");
		std::fwrite(b->data(), 1, b->size(), stdout);
		return "";
	}
	if (!exists("/run/openrc/softlevel")) return "";
	if (::mkdir(state_dir.c_str(), 0700) != 0 && errno != EEXIST) return go_errno("mkdir", state_dir);
	int lock = ::open((state_dir + "/lock").c_str(), O_CREAT | O_RDWR | O_CLOEXEC, 0600);
	if (lock < 0) return go_errno("open", state_dir + "/lock");
	if (::flock(lock, LOCK_EX | LOCK_NB) != 0)
		return std::string("previous APK service application is still running: ") + std::strerror(errno);
	std::string path = state_dir + "/snapshot.json";
	if (op == "pre-commit") {
		if (exists(state_dir + "/pending"))
			return "previous APK application pending; inspect " + state_dir + "/result.json and run nkos-apply-updates apply";
		return write_json(path, snapshot_json(gather()));
	}
	if (op == "post-commit") {
		if (!exists(path)) return "";  // first installation of this hook, or an offline root
		int pending = ::open((state_dir + "/pending").c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
		if (pending < 0) return go_errno("open", state_dir + "/pending");
		::close(pending);
		if (auto e = write_json(state_dir + "/result.json", report_json(Report{"pending", {}, {}, {}, "0001-01-01T00:00:00Z"})); !e.empty())
			return e;
		int log = ::open("/var/log/nanokvm-apk-apply.log", O_CREAT | O_APPEND | O_WRONLY | O_CLOEXEC, 0600);
		if (log < 0) return go_errno("open", "/var/log/nanokvm-apk-apply.log");
		char exe[4096];
		ssize_t n = ::readlink("/proc/self/exe", exe, sizeof exe - 1);
		if (n < 0) {
			::close(log);
			return go_errno("readlink", "/proc/self/exe");
		}
		exe[n] = 0;
		// Release before starting the worker; pending prevents a second transaction.
		::flock(lock, LOCK_UN);
		// The worker reports a failed exec through a pipe that a successful exec closes: the hook must not say the
		// application runs when nothing will clear pending.
		int started[2];
		if (::pipe2(started, O_CLOEXEC) != 0) {
			::close(log);
			return std::string("pipe: ") + std::strerror(errno);
		}
		pid_t pid = ::fork();
		if (pid < 0) {
			int saved = errno;
			for (int fd : {log, started[0], started[1]}) ::close(fd);
			return start_failed(exe, saved);
		}
		if (pid == 0) {
			::setsid();
			int null = ::open("/dev/null", O_RDONLY);
			if (null >= 0) ::dup2(null, 0);
			::dup2(log, 1);
			::dup2(log, 2);
			::execl(exe, exe, "apply", static_cast<char *>(nullptr));
			int saved = errno;
			(void)!::write(started[1], &saved, sizeof saved);
			::_exit(127);
		}
		::close(log);
		::close(started[1]);
		int exec_errno = 0;
		ssize_t got;
		while ((got = ::read(started[0], &exec_errno, sizeof exec_errno)) < 0 && errno == EINTR) {
		}
		::close(started[0]);
		if (got == static_cast<ssize_t>(sizeof exec_errno)) {
			while (::waitpid(pid, nullptr, 0) < 0 && errno == EINTR) {
			}
			return start_failed(exe, exec_errno);
		}
		std::puts("NanoKVM: applying changed components; nkos-apply-updates status shows the result");
		return "";
	}
	if (op == "apply") {
		// This worker descends from apk, the preferred OOM victim: the services it restarts must not inherit that or a
		// lowered priority. It runs while apk may still hold the runner's lock and so never runs apk itself.
		if (FILE *f = std::fopen("/proc/self/oom_score_adj", "w")) {
			std::fputs("0", f);
			std::fclose(f);
		}
		::setpriority(PRIO_PROCESS, 0, 0);
		auto b = read_file(path);
		if (!b) return go_errno("open", path);
		std::string error;
		auto s = parse_snapshot(*b, error);
		if (!s) return error;
		Report result = apply(*s);
		if (auto e = write_json(state_dir + "/result.json", report_json(result)); !e.empty()) return e;
		::unlink(path.c_str());
		::unlink((state_dir + "/pending").c_str());
		if (result.failed && !result.failed->empty()) {
			std::string joined;
			for (std::size_t i = 0; i < result.failed->size(); i++) joined += (i ? "; " : "") + (*result.failed)[i];
			return "component restart failed: " + joined;
		}
		return "";
	}
	return "unknown operation";
}

} // namespace

#ifndef NKOS_APPLY_UPDATES_NO_MAIN
int main(int argc, char **argv)
{
	if (auto e = main_err(argc, argv); !e.empty()) {
		std::fprintf(stderr, "NanoKVM APK apply: %s\n", e.c_str());
		return 1;
	}
	return 0;
}
#endif
