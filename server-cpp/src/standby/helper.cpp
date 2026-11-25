// nkos-standby: the RAM helper of network standby, as the Go helper server/cmd/nkos-standby. Statically linked: it
// runs from the RAM root after the SD card is released, as init's shutdown and restart actions and finally as PID 1.
//   probe ROOT     check the staged root in a chroot (run by the server while it prepares)
//   acknowledge    init's respawn action: confirm the handoff, then ask init to restart into "takeover"
//   shutdown       init's shutdown action: OpenRC shutdown with the storage writeback checked
//   takeover       the new PID 1: release storage, protect the disks, serve the standby page, wake on request
//   bound|renew|deconfig|nak|leasefail   the staged udhcpc's hooks

#include "standby/core.hpp"
#include "storage/go.hpp"

#include <openssl/err.h>
#include <openssl/pem.h>
#include <openssl/ssl.h>

#include <algorithm>
#include <arpa/inet.h>
#include <atomic>
#include <cerrno>
#include <condition_variable>
#include <csignal>
#include <cstdio>
#include <cstring>
#include <ctime>
#include <dirent.h>
#include <fcntl.h>
#include <glob.h>
#include <map>
#include <mutex>
#include <linux/fs.h>
#include <linux/if_packet.h>
#include <linux/loop.h>
#include <linux/reboot.h>
#include <net/if.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/ioctl.h>
#include <sys/mount.h>
#include <sys/reboot.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/swap.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

extern char **environ;

namespace nkos::standby {
namespace {

namespace go = nkos::storage::go;

void sleep_for(std::chrono::milliseconds d) { std::this_thread::sleep_for(d); }

[[noreturn]] void sleep_forever()
{
	for (;;) ::pause();
}

std::string errno_text(int err) { return go::errno_text(err); }

// exec.Cmd with the helper's own stdio: argv, extra environment; Go's error text of Run.
struct Child {
	pid_t pid = -1;
	Error error;
};

Child start(const std::vector<std::string> &argv, const std::vector<std::string> &env_extra = {}, int out_fd = -1)
{
	std::vector<char *> args;
	for (const auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	std::vector<std::string> env_store;
	for (char **e = environ; e && *e; e++) env_store.emplace_back(*e);
	for (const auto &e : env_extra) env_store.push_back(e);
	std::vector<char *> env;
	for (auto &e : env_store) env.push_back(e.data());
	env.push_back(nullptr);
	int status_pipe[2];
	if (::pipe2(status_pipe, O_CLOEXEC) != 0) return {-1, "fork/exec " + argv[0] + ": " + errno_text(errno)};
	pid_t pid = ::fork();
	if (pid < 0) return {-1, "fork/exec " + argv[0] + ": " + errno_text(errno)};
	if (pid == 0) {
		if (out_fd >= 0) {
			::dup2(out_fd, 1);
			::dup2(out_fd, 2);
		}
		::execve(args[0], args.data(), env.data());
		int err = errno;
		(void)!::write(status_pipe[1], &err, sizeof err);
		::_exit(127);
	}
	::close(status_pipe[1]);
	int err = 0;
	ssize_t n = ::read(status_pipe[0], &err, sizeof err);
	::close(status_pipe[0]);
	if (n == sizeof err) {
		::waitpid(pid, nullptr, 0);
		return {-1, "fork/exec " + argv[0] + ": " + errno_text(err)};
	}
	return {pid, ""};
}

Error wait_child(pid_t pid)
{
	int status = 0;
	while (::waitpid(pid, &status, 0) < 0)
		if (errno != EINTR) return "wait: " + errno_text(errno);
	if (WIFEXITED(status)) return WEXITSTATUS(status) == 0 ? "" : go::exit_text(WEXITSTATUS(status), 0);
	return go::exit_text(-1, WTERMSIG(status));
}

Error run_inherit(const std::vector<std::string> &argv, const std::vector<std::string> &env = {})
{
	Child c = start(argv, env);
	if (!c.error.empty()) return c.error;
	return wait_child(c.pid);
}

// CombinedOutput.
Error run_combined(const std::vector<std::string> &argv, std::string &out)
{
	int p[2];
	if (::pipe2(p, O_CLOEXEC) != 0) return errno_text(errno);
	Child c = start(argv, {}, p[1]);
	::close(p[1]);
	char buf[4096];
	for (;;) {
		ssize_t n = ::read(p[0], buf, sizeof buf);
		if (n > 0) out.append(buf, static_cast<std::size_t>(n));
		else if (n < 0 && errno == EINTR) continue;
		else break;
	}
	::close(p[0]);
	if (!c.error.empty()) return c.error;
	return wait_child(c.pid);
}

Error busybox(const std::vector<std::string> &args)
{
	std::vector<std::string> argv{"/bin/busybox"};
	argv.insert(argv.end(), args.begin(), args.end());
	std::string out;
	if (Error e = run_combined(argv, out); !e.empty()) return "network " + args[0] + ": " + out + ": " + e;
	return "";
}

Error read_file(const std::string &path, std::string &data) { return go::read_file(path, data).text; }
Error write_file(const std::string &path, std::string_view data, unsigned mode) { return go::write_file(path, data, mode).text; }

std::vector<std::string> glob_paths(const std::string &pattern)
{
	std::vector<std::string> out;
	glob_t g{};
	if (::glob(pattern.c_str(), 0, nullptr, &g) == 0)
		for (std::size_t i = 0; i < g.gl_pathc; i++) out.emplace_back(g.gl_pathv[i]);
	::globfree(&g);
	return out;
}

std::vector<std::string> read_dir(const std::string &path, int &err)
{
	std::vector<std::string> names;
	err = 0;
	DIR *d = ::opendir(path.c_str());
	if (!d) {
		err = errno;
		return names;
	}
	while (dirent *e = ::readdir(d)) {
		std::string n = e->d_name;
		if (n != "." && n != "..") names.push_back(n);
	}
	::closedir(d);
	std::sort(names.begin(), names.end());
	return names;
}

// --- probe and acknowledge ----------------------------------------------------------------------------------------

Error probe(const std::string &dir)
{
	if (::chroot(dir.c_str()) != 0) return errno_text(errno);
	if (::chdir("/") != 0) return go::path_error("chdir", "/", errno);
	Config c;
	if (Error e = load_config("/config.json", c); !e.empty()) return e;
	if (Error e = run_inherit({"/bin/busybox", "true"}); !e.empty()) return e;
	if (c.wifi) return run_inherit({"/sbin/wpa_supplicant", "-v"});
	return "";
}

Error acknowledge()
{
	if (::getppid() != 1) return "acknowledgement must be started by init";
	Config c;
	if (Error e = load_config(std::string(root) + "/config.json", c); !e.empty()) return e;
	std::string dir = directory;
	if (Error e = write_file(dir + "/ack", c.token, 0600); !e.empty()) return e;
	for (;;) {
		struct stat st{};
		if (::stat((dir + "/commit").c_str(), &st) == 0) break;
		if (::stat(marker, &st) != 0) return "handoff cancelled";
		sleep_for(std::chrono::milliseconds(50));
	}
	sleep_for(std::chrono::seconds(2));
	if (::kill(1, SIGQUIT) != 0) return errno_text(errno);
	sleep_forever();
}

// --- shutdown ------------------------------------------------------------------------------------------------------

Error join(const std::vector<Error> &errs)
{
	Error out;
	for (const auto &e : errs) {
		if (e.empty()) continue;
		if (!out.empty()) out += "\n";
		out += e;
	}
	return out;
}

// init's shutdown action. A descriptor opened before the services stop for each persistent mount lets syncfs see
// writeback errors over the whole shutdown, and keeps OpenRC from unmounting storage before that check.
Error shutdown()
{
	if (::getppid() != 1) return "shutdown action must be started by init";
	std::vector<Mount> mounts;
	std::vector<Error> results;
	if (Error e = read_mounts(mounts); !e.empty()) results.push_back(e);
	std::vector<std::pair<int, std::string>> files;
	for (const auto &m : mounts) {
		if (!block_fs(m.type)) continue;
		int fd = ::open(m.point.c_str(), O_RDONLY | O_CLOEXEC);
		if (fd < 0) results.push_back(go::path_error("open", m.point, errno));
		else files.emplace_back(fd, m.point);
	}
	// OpenRC clears its failed directory before it returns and its exit status hides stop failures: watch events.
	std::unique_ptr<FailureWatch> watch;
	if (Error e = FailureWatch::open("/run/openrc/failed", "/run/openrc/deptree", watch); !e.empty()) results.push_back(e);
	// OpenRC's umount retry uses fuser -k, which would kill this supervisor for its descriptors: mounts stay for the
	// new PID 1, which releases them after every service stopped.
	if (Error e = run_inherit({"/sbin/openrc", "shutdown"}, {"RC_NO_UMOUNTS=/:/.+"}); !e.empty())
		results.push_back("OpenRC shutdown: " + e);
	for (const auto &[fd, name] : files) {
		if (::syncfs(fd) != 0) results.push_back("shutdown writeback " + name + ": " + errno_text(errno));
		if (::close(fd) != 0) results.push_back(go::path_error("close", name, errno));
	}
	if (watch)
		if (Error e = watch->result(); !e.empty()) results.push_back(e);
	watch.reset();
	Error err = join(results);
	if (Error e = write_file(std::string(directory) + "/shutdown-result", err.empty() ? "ok" : err, 0600); !e.empty()) return e;
	return err;
}

Error shutdown_result()
{
	std::string b;
	if (Error e = read_file(std::string(directory) + "/shutdown-result", b); !e.empty())
		return "shutdown completion was not recorded: " + e;
	if (b != "ok") return "shutdown did not complete cleanly: " + b;
	return "";
}

// --- storage release ------------------------------------------------------------------------------------------------

Error no_user_processes()
{
	int err = 0;
	auto entries = read_dir("/proc", err);
	if (err) return go::path_error("open", "/proc", err);
	for (const auto &name : entries) {
		Error pe;
		auto pid = parse_uint(name, 10, 63, pe);
		if (!pid || *pid == 1) continue;
		std::string stat;
		auto e = go::read_file("/proc/" + name + "/stat", stat);
		if (e.sys == ENOENT) continue;
		if (e) return e.text;
		bool stopped = false;
		if (Error x = process_released(stat, stopped); !x.empty()) return x;
		if (!stopped) return "process " + name + " did not stop; storage may still be in use";
	}
	return "";
}

Error detach_usb()
{
	for (const auto &p : glob_paths("/sys/kernel/config/usb_gadget/*/UDC")) {
		std::string b;
		if (Error e = read_file(p, b); !e.empty()) return e;
		if (!go::trim_space(b).empty())
			if (Error e = write_file(p, "\n", 0600); !e.empty()) return "USB detach: " + e;
	}
	for (const auto &p : glob_paths("/sys/kernel/config/usb_gadget/*/functions/mass_storage.*/lun.*/file")) {
		std::string b;
		if (Error e = read_file(p, b); !e.empty()) return e;
		if (!go::trim_space(b).empty()) {
			if (Error e = write_file(p, "\n", 0600); !e.empty()) return "USB backing file release: " + e;
			if (!read_file(p, b).empty() || !go::trim_space(b).empty()) return "USB storage is still attached";
		}
	}
	return "";
}

Error stop_swap()
{
	std::string b;
	if (Error e = read_file("/proc/swaps", b); !e.empty()) return e;
	std::vector<std::string> lines;
	{
		std::string t = go::trim_space(b);
		std::size_t start = 0;
		for (;;) {
			auto nl = t.find('\n', start);
			lines.push_back(t.substr(start, nl == std::string::npos ? std::string::npos : nl - start));
			if (nl == std::string::npos) break;
			start = nl + 1;
		}
	}
	for (std::size_t i = 1; i < lines.size(); i++) {
		auto f = go::fields(lines[i]);
		if (f.size() < 5) return "invalid swap table";
		if (::swapoff(unescape_mount(f[0]).c_str()) != 0) return "swapoff " + f[0] + ": " + errno_text(errno);
	}
	if (Error e = read_file("/proc/swaps", b); !e.empty()) return e;
	if (go::fields(b).size() != 5) return "swap remains active";
	for (const auto &p : glob_paths("/sys/block/zram*/reset"))
		if (Error e = write_file(p, "1\n", 0600); !e.empty()) return e;
	return "";
}

Error detach_loops(const std::string &prefix)
{
	for (const auto &p : glob_paths(prefix + "/sys/block/loop*/loop/backing_file")) {
		std::string name = go::base(go::dir(go::dir(p)));
		int fd = ::open((prefix + "/dev/" + name).c_str(), O_RDONLY | O_CLOEXEC);
		if (fd < 0) return errno_text(errno);
		int rc = ::ioctl(fd, LOOP_CLR_FD, 0);
		int err = errno;
		::close(fd);
		if (rc != 0) return "release " + name + ": " + errno_text(err);
	}
	return "";
}

// No lazy or forced unmount: busy mappings, loops, gadgets and tasks in D state fail closed.
Error leave_storage()
{
	auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(10);
	for (;;) {
		int status = 0;
		while (::waitpid(-1, &status, WNOHANG) > 0) {
		}
		Error e = no_user_processes();
		if (e.empty()) break;
		if (std::chrono::steady_clock::now() > deadline) return e;
		sleep_for(std::chrono::milliseconds(100));
	}
	if (Error e = detach_usb(); !e.empty()) return e;
	if (Error e = stop_swap(); !e.empty()) return e;
	std::vector<Mount> mounts;
	if (Error e = read_mounts(mounts); !e.empty()) return e;
	if (Error e = sync_filesystems(mounts); !e.empty()) return e;
	if (::mount("", "/", "", MS_REC | MS_PRIVATE, nullptr) != 0) return errno_text(errno);
	if (::mkdir("/run/nkos-oldroot", 0700) != 0) return go::path_error("mkdir", "/run/nkos-oldroot", errno);
	if (::syscall(SYS_pivot_root, "/run", "/run/nkos-oldroot") != 0) return errno_text(errno);
	if (::chdir("/") != 0) return go::path_error("chdir", "/", errno);
	for (std::string name : {"dev", "sys", "proc"})
		if (::mount(("/nkos-oldroot/" + name).c_str(), ("/nkos-standby/root/" + name).c_str(), "", MS_MOVE, nullptr) != 0)
			return errno_text(errno);
	if (Error e = read_mounts(mounts, "/nkos-standby/root/proc/self/mountinfo"); !e.empty()) return e;
	std::stable_sort(mounts.begin(), mounts.end(), [](const Mount &a, const Mount &b) { return a.point.size() > b.point.size(); });
	// LOOP_CLR_FD marks mounted loops autoclear; their backing files go when those mounts close.
	if (Error e = detach_loops("/nkos-standby/root"); !e.empty()) return e;
	std::vector<Mount> pending;
	for (const auto &m : mounts)
		if (m.point == "/nkos-oldroot" || m.point.starts_with("/nkos-oldroot/")) pending.push_back(m);
	// A loop mounted at /m may hold a backing file on /data: retry EBUSY only after another mount closed.
	while (!pending.empty()) {
		std::vector<Mount> busy;
		for (const auto &m : pending) {
			if (::umount2(m.point.c_str(), 0) == 0) continue;
			if (errno == EBUSY) {
				busy.push_back(m);
				continue;
			}
			return "unmount " + m.point + ": " + errno_text(errno);
		}
		if (busy.size() == pending.size()) return "unmount " + busy[0].point + ": " + errno_text(EBUSY);
		pending = busy;
	}
	// The whole namespace, before chroot hides mounts outside the helper's subtree.
	if (Error e = read_mounts(mounts, "/nkos-standby/root/proc/self/mountinfo"); !e.empty()) return e;
	for (const auto &m : mounts)
		if (!memory_fs(m.type)) return "persistent filesystem remains mounted at " + m.point;
	return "";
}

Error memory_mounts_only()
{
	std::vector<Mount> mounts;
	if (Error e = read_mounts(mounts); !e.empty()) return e;
	for (const auto &m : mounts)
		if (!memory_fs(m.type)) return "persistent filesystem remains mounted at " + m.point;
	return "";
}

struct Disk {
	std::string name;
	Counters baseline;
};

Error protect_disks(std::vector<Disk> &disks)
{
	// Never block writeback on a filesystem that was not released.
	if (Error e = memory_mounts_only(); !e.empty()) return e;
	int err = 0;
	auto entries = read_dir("/sys/block", err);
	if (err) return go::path_error("open", "/sys/block", err);
	for (const auto &name : entries) {
		if (name.starts_with("loop") || name.starts_with("ram") || name.starts_with("zram")) continue;
		if (name.starts_with("dm-")) return "device-mapper standby is unsupported";
		// Partitions too: raw partition descriptors must not bypass the whole-disk read-only flag.
		std::vector<std::string> nodes{name};
		for (const auto &p : glob_paths("/sys/block/" + name + "/" + name + "*")) {
			struct stat st{};
			if (::stat((p + "/partition").c_str(), &st) == 0) nodes.push_back(go::base(p));
		}
		for (const auto &node : nodes) {
			int fd = ::open(("/dev/" + node).c_str(), O_RDONLY | O_CLOEXEC);
			if (fd < 0) return errno_text(errno);
			if (::ioctl(fd, BLKFLSBUF, 0) != 0) {
				int e = errno;
				::close(fd);
				return "flush " + node + ": " + errno_text(e);
			}
			int one = 1, ro = 0;
			Error e;
			if (::ioctl(fd, BLKROSET, &one) != 0) e = errno_text(errno);
			else if (::ioctl(fd, BLKROGET, &ro) != 0) e = errno_text(errno);
			else if (ro != 1) e = "block device remains writable";
			::close(fd);
			if (!e.empty()) return e;
		}
		disks.push_back({name, {}});
	}
	if (disks.empty()) return "no physical storage found to verify";
	for (auto &d : disks) {
		std::string b;
		if (Error e = read_file("/sys/block/" + d.name + "/stat", b); !e.empty()) return e;
		if (Error e = parse_counters(b, d.baseline); !e.empty()) return e;
		if (d.baseline.in_flight != 0) return "block I/O remains in flight";
	}
	return "";
}

Error verify_storage(const std::vector<Disk> &disks)
{
	if (Error e = watchdogs_released("/sys/class/watchdog"); !e.empty()) return e;
	if (Error e = memory_mounts_only(); !e.empty()) return e;
	for (const auto &d : disks) {
		std::string b;
		if (Error e = read_file("/sys/block/" + d.name + "/ro", b); !e.empty()) return e;
		if (go::trim_space(b) != "1") return d.name + " is no longer read-only";
		if (Error e = read_file("/sys/block/" + d.name + "/stat", b); !e.empty()) return e;
		Counters c;
		if (Error e = parse_counters(b, c); !e.empty()) return e;
		if (!(c == d.baseline) || c.in_flight != 0) return "I/O changed after storage release on " + d.name;
	}
	return "";
}

// --- network --------------------------------------------------------------------------------------------------------

Error start_network(const Config &c, const std::function<void(const Error &)> &failed)
{
	if (Error e = busybox({"ip", "link", "set", "lo", "up"}); !e.empty()) return e;
	if (Error e = busybox({"ip", "link", "set", c.interface, "up"}); !e.empty()) return e;
	auto start_watched = [&failed](const std::vector<std::string> &argv) -> Error {
		Child child = start(argv);
		if (!child.error.empty()) return child.error;
		std::thread([pid = child.pid, path = argv[0], failed] {
			Error e = wait_child(pid);
			failed(path + " stopped: " + (e.empty() ? std::string("<nil>") : e));
		}).detach();
		return "";
	};
	if (c.wifi)
		if (Error e = start_watched({"/sbin/wpa_supplicant", "-Dnl80211", "-i" + c.interface, "-c/wpa.conf"}); !e.empty()) return e;
	if (!c.dhcp) {
		if (Error e = busybox({"ip", "addr", "replace", c.address + "/" + std::to_string(c.prefix), "dev", c.interface}); !e.empty())
			return e;
		if (!c.gateway.empty()) return busybox({"ip", "route", "replace", "default", "via", c.gateway, "dev", c.interface});
		return "";
	}
	std::vector<std::string> args{"/bin/busybox", "udhcpc", "-f", "-B", "-i", c.interface, "-r", c.address, "-s", "/init",
	                              "-p", "/run/udhcpc.pid", "-t", "5", "-T", "2", "-A", "5"};
	if (!c.hostname.empty()) {
		args.push_back("-x");
		args.push_back("hostname:" + c.hostname);
	}
	if (Error e = start_watched(args); !e.empty()) return e;
	auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(45);
	while (std::chrono::steady_clock::now() < deadline) {
		std::string b;
		if (read_file("/run/network.ready", b).empty() && b == c.address) return "";
		sleep_for(std::chrono::milliseconds(100));
	}
	return "DHCP did not restore the management address; use the router lease list and the saved restart key";
}

std::string env(const char *name)
{
	const char *v = ::getenv(name);
	return v ? v : "";
}

std::string ip4_text(const std::string &four)
{
	char buf[INET_ADDRSTRLEN];
	::inet_ntop(AF_INET, four.data(), buf, sizeof buf);
	return buf;
}

// The staged udhcpc's hook: only this client's interface, no lease or DNS state outside RAM.
Error dhcp_hook(const std::string &event)
{
	Config c;
	if (Error e = load_config("/config.json", c); !e.empty()) return e;
	if (env("interface") != c.interface) return "unexpected DHCP interface";
	if (event == "deconfig" || event == "nak" || event == "leasefail") {
		::unlink("/run/network.ready");
		return busybox({"ip", "-4", "addr", "flush", "dev", c.interface});
	}
	if (event != "bound" && event != "renew") return "";
	auto ip = parse_ip4(env("ip"));
	auto mask = parse_ip4(env("subnet"));
	if (!ip || !mask) return "invalid DHCP address";
	std::uint32_t m = ntohl(*reinterpret_cast<const std::uint32_t *>(mask->data()));
	int prefix = __builtin_popcount(m);
	// net.IPMask.Size: a non-canonical mask has no size.
	if (m != (prefix == 0 ? 0u : 0xffffffffu << (32 - prefix))) return "invalid DHCP subnet";
	if (Error e = busybox({"ip", "-4", "addr", "flush", "dev", c.interface}); !e.empty()) return e;
	if (Error e = busybox({"ip", "route", "flush", "dev", c.interface}); !e.empty()) return e;
	std::string address = ip4_text(*ip);
	if (Error e = busybox({"ip", "addr", "replace", address + "/" + std::to_string(prefix), "dev", c.interface}); !e.empty())
		return e;
	auto routers = go::fields(env("router"));
	if (!routers.empty()) {
		auto gw = parse_ip4(routers[0]);
		if (!gw) return "invalid DHCP gateway";
		if (Error e = busybox({"ip", "route", "replace", "default", "via", ip4_text(*gw), "dev", c.interface}); !e.empty())
			return e;
	}
	return write_file("/run/network.ready", address, 0600);
}

// --- wake packets ---------------------------------------------------------------------------------------------------

std::int64_t packet_time(msghdr &msg)
{
	for (cmsghdr *c = CMSG_FIRSTHDR(&msg); c; c = CMSG_NXTHDR(&msg, c)) {
		if (c->cmsg_level == SOL_SOCKET && c->cmsg_type == SCM_TIMESTAMPNS && c->cmsg_len >= CMSG_LEN(16)) {
			std::int64_t sec, nsec;
			std::memcpy(&sec, CMSG_DATA(c), 8);
			std::memcpy(&nsec, CMSG_DATA(c) + 8, 8);
			if (sec > 0 && nsec >= 0 && nsec < 1000000000) return sec * 1000000000 + nsec;
		}
	}
	return 0;
}

void read_magic(int fd, std::string mac, std::function<void(std::int64_t)> wake, std::function<void(const Error &)> failed)
{
	std::thread([=] {
		char b[2048];
		alignas(cmsghdr) char oob[128];
		for (;;) {
			iovec iov{b, sizeof b};
			msghdr msg{};
			msg.msg_iov = &iov;
			msg.msg_iovlen = 1;
			msg.msg_control = oob;
			msg.msg_controllen = sizeof oob;
			ssize_t n = ::recvmsg(fd, &msg, 0);
			if (n < 0) {
				if (errno == EINTR) continue;
				failed(errno_text(errno));
				return;
			}
			if (magic_packet(std::string_view(b, static_cast<std::size_t>(n)), mac)) wake(packet_time(msg));
		}
	}).detach();
}

// All sockets open before readiness; only this physical interface is listened to.
Error listen_magic(const Config &c, std::function<void(std::int64_t)> wake, std::function<void(const Error &)> failed)
{
	unsigned index = ::if_nametoindex(c.interface.c_str());
	if (index == 0) return "route ip+net: no such network interface";
	std::string mac;
	{
		unsigned v[6];
		char tail;
		if (c.mac.size() != 17 || std::sscanf(c.mac.c_str(), "%2x:%2x:%2x:%2x:%2x:%2x%c", &v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &tail) != 6)
			return "invalid wake MAC";
		for (unsigned x : v) mac += static_cast<char>(x);
	}
	std::vector<int> fds;
	auto close_all = [&fds] {
		for (int fd : fds) ::close(fd);
	};
	for (int port : {7, 9}) {
		int fd = ::socket(AF_INET, SOCK_DGRAM | SOCK_CLOEXEC, 0);
		if (fd < 0) {
			close_all();
			return "socket: " + errno_text(errno);
		}
		int one = 1;
		::setsockopt(fd, SOL_SOCKET, SO_BROADCAST, &one, sizeof one);
		sockaddr_in addr{};
		addr.sin_family = AF_INET;
		addr.sin_port = htons(static_cast<std::uint16_t>(port));
		if (::bind(fd, reinterpret_cast<sockaddr *>(&addr), sizeof addr) != 0) {
			Error e = "listen udp4 :" + std::to_string(port) + ": bind: " + errno_text(errno);
			::close(fd);
			close_all();
			return e;
		}
		if (::setsockopt(fd, SOL_SOCKET, SO_BINDTODEVICE, c.interface.c_str(), static_cast<socklen_t>(c.interface.size())) != 0 ||
		    ::setsockopt(fd, SOL_SOCKET, SO_TIMESTAMPNS, &one, sizeof one) != 0) {
			Error e = errno_text(errno);
			::close(fd);
			close_all();
			return e;
		}
		fds.push_back(fd);
	}
	// SOCK_DGRAM strips the Ethernet header; ETH_P_WOL is distinct from HTTP and UDP encapsulation.
	int fd = ::socket(AF_PACKET, SOCK_DGRAM | SOCK_CLOEXEC, htons(0x0842));
	if (fd < 0) {
		Error e = errno_text(errno);
		close_all();
		return e;
	}
	int one = 1;
	if (::setsockopt(fd, SOL_SOCKET, SO_TIMESTAMPNS, &one, sizeof one) != 0) {
		Error e = errno_text(errno);
		::close(fd);
		close_all();
		return e;
	}
	sockaddr_ll ll{};
	ll.sll_family = AF_PACKET;
	ll.sll_protocol = htons(0x0842);
	ll.sll_ifindex = static_cast<int>(index);
	if (::bind(fd, reinterpret_cast<sockaddr *>(&ll), sizeof ll) != 0) {
		Error e = errno_text(errno);
		::close(fd);
		close_all();
		return e;
	}
	fds.push_back(fd);
	for (int f : fds) read_magic(f, mac, wake, failed);
	return "";
}

// --- the standby page's HTTP server ----------------------------------------------------------------------------------

const char *status_text(int code)
{
	switch (code) {
	case 200: return "OK";
	case 202: return "Accepted";
	case 301: return "Moved Permanently";
	case 400: return "Bad Request";
	case 401: return "Unauthorized";
	case 403: return "Forbidden";
	case 404: return "Not Found";
	case 408: return "Request Timeout";
	case 409: return "Conflict";
	case 431: return "Request Header Fields Too Large";
	case 501: return "Not Implemented";
	}
	return "";
}

std::string http_date()
{
	char buf[64];
	std::time_t now = std::time(nullptr);
	std::tm tm{};
	::gmtime_r(&now, &tm);
	std::strftime(buf, sizeof buf, "%a, %d %b %Y %H:%M:%S GMT", &tm);
	return buf;
}

// The mux's cleanPath.
std::string clean_path(const std::string &p)
{
	if (p.empty()) return "/";
	std::string q = p[0] == '/' ? p : "/" + p;
	std::string np = go::clean(q);
	if (q.back() == '/' && np != "/") np += "/";
	return np;
}

struct Conn {
	int fd = -1;
	SSL *ssl = nullptr;
	std::string buffer;

	// Waits until deadline for the socket to be ready for reading or writing.
	bool wait(bool write, std::chrono::steady_clock::time_point deadline)
	{
		auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - std::chrono::steady_clock::now()).count();
		if (left <= 0) return false;
		pollfd p{fd, static_cast<short>(write ? POLLOUT : POLLIN), 0};
		return ::poll(&p, 1, static_cast<int>(left)) > 0;
	}
	// More input into buffer; false on end, error or timeout.
	bool fill(std::chrono::steady_clock::time_point deadline)
	{
		char buf[4096];
		for (;;) {
			if (ssl) {
				if (SSL_pending(ssl) == 0 && !wait(false, deadline)) return false;
				int n = SSL_read(ssl, buf, sizeof buf);
				if (n > 0) {
					buffer.append(buf, static_cast<std::size_t>(n));
					return true;
				}
				int e = SSL_get_error(ssl, n);
				if (e == SSL_ERROR_WANT_READ) continue;
				if (e == SSL_ERROR_WANT_WRITE) {
					if (!wait(true, deadline)) return false;
					continue;
				}
				return false;
			}
			if (!wait(false, deadline)) return false;
			ssize_t n = ::recv(fd, buf, sizeof buf, 0);
			if (n > 0) {
				buffer.append(buf, static_cast<std::size_t>(n));
				return true;
			}
			if (n < 0 && (errno == EINTR || errno == EAGAIN)) continue;
			return false;
		}
	}
	bool send_all(std::string_view data, std::chrono::steady_clock::time_point deadline)
	{
		while (!data.empty()) {
			if (ssl) {
				int n = SSL_write(ssl, data.data(), static_cast<int>(data.size()));
				if (n > 0) {
					data.remove_prefix(static_cast<std::size_t>(n));
					continue;
				}
				int e = SSL_get_error(ssl, n);
				if ((e == SSL_ERROR_WANT_WRITE || e == SSL_ERROR_WANT_READ) && wait(e == SSL_ERROR_WANT_WRITE, deadline)) continue;
				return false;
			}
			if (!wait(true, deadline)) return false;
			ssize_t n = ::send(fd, data.data(), data.size(), MSG_NOSIGNAL);
			if (n > 0) data.remove_prefix(static_cast<std::size_t>(n));
			else if (n < 0 && (errno == EINTR || errno == EAGAIN)) continue;
			else return false;
		}
		return true;
	}
	bool handshake(std::chrono::steady_clock::time_point deadline)
	{
		for (;;) {
			int n = SSL_accept(ssl);
			if (n == 1) return true;
			int e = SSL_get_error(ssl, n);
			if ((e == SSL_ERROR_WANT_READ || e == SSL_ERROR_WANT_WRITE) && wait(e == SSL_ERROR_WANT_WRITE, deadline)) continue;
			return false;
		}
	}
};

std::string lower(std::string s)
{
	for (auto &c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return s;
}

class PageServer {
public:
	PageServer(const Config &c, State &state, std::function<Error()> restart) : config_(c), state_(state), restart_(std::move(restart)) {}

	Error listen()
	{
		fd_ = ::socket(AF_INET, SOCK_STREAM | SOCK_CLOEXEC, 0);
		if (fd_ < 0) return "socket: " + errno_text(errno);
		int one = 1;
		::setsockopt(fd_, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
		if (::setsockopt(fd_, SOL_SOCKET, SO_BINDTODEVICE, config_.interface.c_str(), static_cast<socklen_t>(config_.interface.size())) != 0)
			return errno_text(errno);
		sockaddr_in addr{};
		addr.sin_family = AF_INET;
		addr.sin_port = htons(static_cast<std::uint16_t>(config_.port));
		if (::bind(fd_, reinterpret_cast<sockaddr *>(&addr), sizeof addr) != 0 || ::listen(fd_, 128) != 0)
			return "listen tcp4 :" + std::to_string(config_.port) + ": bind: " + errno_text(errno);
		if (config_.certificate && !config_.certificate->empty()) {
			ctx_ = SSL_CTX_new(TLS_server_method());
			SSL_CTX_set_min_proto_version(ctx_, TLS1_2_VERSION);
			if (Error e = load_pair(); !e.empty()) {
				::close(fd_);
				return e;
			}
		}
		return "";
	}

	// Serve: at most 32 connections at once (handshakes included), each on its own thread.
	Error serve()
	{
		for (;;) {
			{
				std::unique_lock lock(mu_);
				cv_.wait(lock, [this] { return active_ < 32; });
				active_++;
			}
			int c = ::accept4(fd_, nullptr, nullptr, SOCK_CLOEXEC);
			if (c < 0) {
				{
					std::lock_guard lock(mu_);
					active_--;
				}
				if (errno == EINTR || errno == ECONNABORTED || errno == EMFILE || errno == ENFILE || errno == ENOBUFS || errno == ENOMEM) {
					sleep_for(std::chrono::milliseconds(5));
					continue;
				}
				return "accept tcp4: " + errno_text(errno);
			}
			std::thread([this, c] {
				connection(c);
				::close(c);
				std::lock_guard lock(mu_);
				active_--;
				cv_.notify_one();
			}).detach();
		}
	}

private:
	Error load_pair()
	{
		BIO *cb = BIO_new_mem_buf(config_.certificate->data(), static_cast<int>(config_.certificate->size()));
		X509 *leaf = PEM_read_bio_X509(cb, nullptr, nullptr, nullptr);
		bool ok = leaf && SSL_CTX_use_certificate(ctx_, leaf) == 1;
		while (ok) {
			X509 *x = PEM_read_bio_X509(cb, nullptr, nullptr, nullptr);
			if (!x) break;
			SSL_CTX_add_extra_chain_cert(ctx_, x);
		}
		ERR_clear_error();
		BIO_free(cb);
		X509_free(leaf);
		std::string key = config_.private_key.value_or("");
		BIO *kb = BIO_new_mem_buf(key.data(), static_cast<int>(key.size()));
		EVP_PKEY *pkey = PEM_read_bio_PrivateKey(kb, nullptr, nullptr, nullptr);
		BIO_free(kb);
		ok = ok && pkey && SSL_CTX_use_PrivateKey(ctx_, pkey) == 1 && SSL_CTX_check_private_key(ctx_) == 1;
		EVP_PKEY_free(pkey);
		return ok ? "" : "tls: failed to load the certificate and key";
	}

	void connection(int fd)
	{
		Conn c;
		c.fd = fd;
		::fcntl(fd, F_SETFL, ::fcntl(fd, F_GETFL) | O_NONBLOCK);
		if (ctx_) {
			c.ssl = SSL_new(ctx_);
			SSL_set_fd(c.ssl, fd);
			// The handshake counts against the first request's read timeout.
			if (!c.handshake(std::chrono::steady_clock::now() + std::chrono::seconds(10))) {
				SSL_free(c.ssl);
				return;
			}
		}
		for (bool first = true;; first = false) {
			auto start = std::chrono::steady_clock::now();
			// Idle between requests: 15 s for the first byte of the next one.
			if (!first && c.buffer.empty() && !c.fill(start + std::chrono::seconds(15))) break;
			if (!request(c, std::chrono::steady_clock::now())) break;
		}
		if (c.ssl) {
			SSL_shutdown(c.ssl);
			SSL_free(c.ssl);
		}
	}

	// One request and its answer; false closes the connection.
	bool request(Conn &c, std::chrono::steady_clock::time_point start)
	{
		auto header_deadline = start + std::chrono::seconds(5);
		auto read_deadline = start + std::chrono::seconds(10);
		std::size_t end;
		while ((end = c.buffer.find("\r\n\r\n")) == std::string::npos) {
			if (c.buffer.size() > 8192 + 4096) {
				simple(c, 431, "431 Request Header Fields Too Large", start);
				return false;
			}
			if (!c.fill(header_deadline)) return false;
		}
		std::string head = c.buffer.substr(0, end);
		c.buffer.erase(0, end + 4);
		auto line_end = head.find("\r\n");
		std::string line = head.substr(0, line_end);
		auto s1 = line.find(' '), s2 = line.rfind(' ');
		if (s1 == std::string::npos || s1 == s2) {
			simple(c, 400, "400 Bad Request", start);
			return false;
		}
		std::string method = line.substr(0, s1), target = line.substr(s1 + 1, s2 - s1 - 1), version = line.substr(s2 + 1);
		if (version != "HTTP/1.1" && version != "HTTP/1.0") {
			simple(c, 400, "400 Bad Request", start);
			return false;
		}
		std::map<std::string, std::vector<std::string>> headers;
		for (std::size_t at = line_end == std::string::npos ? head.size() : line_end + 2; at < head.size();) {
			auto nl = head.find("\r\n", at);
			std::string h = head.substr(at, nl == std::string::npos ? std::string::npos : nl - at);
			at = nl == std::string::npos ? head.size() : nl + 2;
			auto colon = h.find(':');
			if (colon == std::string::npos || colon == 0) {
				simple(c, 400, "400 Bad Request: malformed MIME header line", start);
				return false;
			}
			headers[lower(h.substr(0, colon))].push_back(go::trim_space(h.substr(colon + 1)));
		}
		auto first = [&headers](const char *name) {
			auto it = headers.find(name);
			return it == headers.end() ? std::string() : it->second.front();
		};
		bool keep = version == "HTTP/1.1" ? lower(first("connection")) != "close" : lower(first("connection")) == "keep-alive";
		if (!first("transfer-encoding").empty()) keep = false;
		// The handler reads no body: discard what Content-Length announces.
		if (auto cl = first("content-length"); !cl.empty()) {
			Error e;
			auto n = parse_uint(cl, 10, 63, e);
			if (!n) {
				simple(c, 400, "400 Bad Request", start);
				return false;
			}
			std::uint64_t left = *n;
			while (left > 0) {
				if (c.buffer.empty() && !c.fill(read_deadline)) return false;
				std::size_t take = static_cast<std::size_t>(std::min<std::uint64_t>(left, c.buffer.size()));
				c.buffer.erase(0, take);
				left -= take;
			}
		}
		std::string raw_query;
		std::string path = target;
		if (auto q = path.find('?'); q != std::string::npos) {
			raw_query = path.substr(q + 1);
			path.resize(q);
		}
		auto url = go::parse_url(target);
		if (!url || target.empty() || target[0] != '/') {
			simple(c, 400, "400 Bad Request", start);
			return false;
		}
		path = url->path;
		Reply r;
		if (std::string clean = clean_path(path); clean != path && method != "CONNECT") {
			std::string location = clean + (raw_query.empty() ? "" : "?" + raw_query);
			r.status = 301;
			r.headers.emplace_back("Location", location);
			if (method == "GET" || method == "HEAD") r.headers.emplace_back("Content-Type", "text/html; charset=utf-8");
			if (method == "GET") r.body = "<a href=\"" + location + "\">Moved Permanently</a>.\n\n";
		} else {
			HttpIn in{method, path, first("authorization"), first("origin"), first("sec-fetch-site"), first("host"), c.ssl != nullptr};
			r = handle(config_, state_, in);
			if (r.restart_first) {
				std::thread([this] {
					// Let the reply leave the socket before restarting.
					sleep_for(std::chrono::milliseconds(250));
					if (Error e = restart_(); !e.empty()) state_.restart_failed(e);
				}).detach();
			}
		}
		return write(c, r, method == "HEAD", keep, start) && keep;
	}

	void simple(Conn &c, int code, const std::string &text, std::chrono::steady_clock::time_point start)
	{
		std::string out = "HTTP/1.1 " + std::to_string(code) + " " + status_text(code) +
		                  "\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\n\r\n" + text;
		c.send_all(out, start + std::chrono::seconds(10));
	}

	bool write(Conn &c, const Reply &r, bool head, bool keep, std::chrono::steady_clock::time_point start)
	{
		std::string out = "HTTP/1.1 " + std::to_string(r.status) + " " + status_text(r.status) + "\r\n";
		for (const auto &[k, v] : r.headers) out += k + ": " + v + "\r\n";
		out += "Date: " + http_date() + "\r\n";
		out += "Content-Length: " + std::to_string(r.body.size()) + "\r\n";
		if (!keep) out += "Connection: close\r\n";
		out += "\r\n";
		if (!head) out += r.body;
		return c.send_all(out, start + std::chrono::seconds(10));
	}

	Config config_;
	State &state_;
	std::function<Error()> restart_;
	int fd_ = -1;
	SSL_CTX *ctx_ = nullptr;
	std::mutex mu_;
	std::condition_variable cv_;
	int active_ = 0;
};

// --- takeover -------------------------------------------------------------------------------------------------------

Error reboot_now()
{
	::sync();
	if (::reboot(RB_AUTOBOOT) != 0) return errno_text(errno);
	return "";
}

// Takeover is entered only by BusyBox init's restart action, as the new PID 1.
Failure takeover()
{
	bool protected_ = false;
	auto result = [&protected_](const Error &e) {
		return Failure{protected_ ? recovery_text(e) : e, protected_};
	};
	if (::getpid() != 1) return {"takeover requires PID 1", false};
	Config c;
	if (Error e = load_config(std::string(root) + "/config.json", c); !e.empty()) return {e, false};
	static State state(c);
	if (Error e = shutdown_result(); !e.empty()) state.fail(e);
	for (int sig : {SIGTERM, SIGINT, SIGHUP, SIGQUIT}) ::signal(sig, SIG_IGN);
	Error err = leave_storage();
	if (!err.empty()) state.fail(err);
	// Even on failure the page is isolated from storage; a failed release never becomes ready later.
	std::string dir = root;
	struct stat st{};
	if (::stat("/nkos-standby/root/config.json", &st) == 0) dir = "/nkos-standby/root";
	// A partial transition may not have moved the virtual filesystems yet.
	if (!err.empty()) {
		for (auto [name, sentinel] : {std::pair<std::string, std::string>{"dev", "null"}, {"proc", "self/mountinfo"}, {"sys", "class/net"}}) {
			if (::stat((dir + "/" + name + "/" + sentinel).c_str(), &st) == 0) continue;
			std::string source = dir != root ? "/nkos-oldroot/" + name : "/" + name;
			if (::mount(source.c_str(), (dir + "/" + name).c_str(), "", MS_BIND | MS_REC, nullptr) != 0)
				state.fail("RAM " + name + ": " + errno_text(errno));
		}
	}
	if (::chroot(dir.c_str()) != 0) return result("RAM chroot: " + errno_text(errno));
	if (::chdir("/") != 0) return result(go::path_error("chdir", "/", errno));
	std::vector<Disk> disks;
	if (err.empty()) {
		err = protect_disks(disks);
		if (!err.empty()) state.fail("protect storage: " + err);
		else protected_ = true;
	}
	static PageServer *server = new PageServer(c, state, reboot_now);
	if (Error e = server->listen(); !e.empty()) return result(e);
	std::thread([] {
		if (Error e = server->serve(); !e.empty()) state.fail(e);
	}).detach();
	auto fail = [](const Error &e) { state.fail(e); };
	if (Error e = start_network(c, fail); !e.empty()) state.fail(e);
	if (c.magic_wake) {
		Error e = listen_magic(
		    c,
		    [](std::int64_t received) {
			    auto [first, err] = state.magic_restart(received);
			    if (err.empty() && first)
				    if (Error r = reboot_now(); !r.empty()) state.fail(r);
		    },
		    fail);
		if (!e.empty()) state.fail(e);
	}
	if (state.get().state == "preparing") {
		for (int i = 0; i < 3; i++) {
			sleep_for(std::chrono::seconds(1));
			if (Error e = verify_storage(disks); !e.empty()) {
				state.fail(e);
				break;
			}
		}
		state.ready();
	}
	// Observe the whole session, not only a point-in-time sync.
	for (;;) {
		sleep_for(std::chrono::seconds(1));
		if (state.get().state == "ready")
			if (Error e = verify_storage(disks); !e.empty()) state.fail(e);
	}
}

} // namespace
} // namespace nkos::standby

int main(int argc, char **argv)
{
	using namespace nkos::standby;
	Failure err;
	std::string op = argc >= 2 ? argv[1] : "";
	if (argc < 2) err.text = "usage: nkos-standby {probe ROOT|acknowledge|shutdown|takeover}";
	else if (op == "probe") err.text = argc != 3 ? "probe requires RAM root" : probe(argv[2]);
	else if (op == "acknowledge") err.text = acknowledge();
	else if (op == "shutdown") err.text = shutdown();
	else if (op == "takeover") err = takeover();
	else if (op == "bound" || op == "renew" || op == "deconfig" || op == "nak" || op == "leasefail") err.text = dhcp_hook(op);
	else err.text = "unknown operation";
	if (!err.text.empty()) std::fprintf(stderr, "%s\n", err.text.c_str());
	// Exiting PID 1 panics the kernel: keep console evidence of a failed setup. A verified storage release permits
	// the announced, delayed restart when the RAM listener could not start.
	if (::getpid() == 1) {
		if (err.recovery) {
			Failure r = recover_fatal(
			    err, [](std::chrono::seconds d) { std::this_thread::sleep_for(d); }, [] { return reboot_now(); });
			if (!r.text.empty()) std::fprintf(stderr, "%s\n", r.text.c_str());
		}
		for (;;) ::pause();
	}
	return err.text.empty() ? 0 : 1;
}
