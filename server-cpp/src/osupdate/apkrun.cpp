#include "osupdate/apkrun.hpp"

#include "common/process.hpp"
#include "osupdate/goerr.hpp"

#include <algorithm>
#include <cerrno>
#include <csignal>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <fstream>
#include <map>
#include <poll.h>
#include <set>
#include <sstream>
#include <sys/file.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

extern char **environ;

namespace nkos::apkrun {

const char *const busy_error = "another package operation is still running; try again when it has finished";

namespace {

constexpr const char *mem_info_path = "/proc/meminfo";
constexpr const char *apply_state_dir = "/run/nanokvm-apk";
constexpr const char *standby_marker = "/run/nkos-standby/armed";
constexpr std::int64_t required_kib = 32 * 1024;
constexpr auto lock_poll = std::chrono::milliseconds(200);
constexpr auto wait_delay = std::chrono::seconds(5);

enum class Class { installed_only, index_query, transaction };

Class classify(const std::vector<std::string> &args)
{
	std::string applet;
	std::set<std::string> options;
	for (const auto &a : args) {
		if (!a.empty() && a[0] == '-') options.insert(a);
		else if (applet.empty()) applet = a;
	}
	if (applet == "info") {
		if (options.count("-e") || options.count("--installed")) return Class::installed_only;
	} else if (applet == "add" || applet == "del" || applet == "upgrade" || applet == "fix") {
		if (!options.count("-s") && !options.count("--simulate")) return Class::transaction;
	}
	return Class::index_query;
}

bool cancelled(const Cmd &cmd) { return cmd.cancelled && cmd.cancelled(); }

struct Fd {
	int fd = -1;
	~Fd()
	{
		if (fd >= 0) ::close(fd);
	}
};

// acquire: the exclusive lock, polled so that both the deadline and the wait limit end the wait.
bool acquire(const Cmd &cmd, Fd &lock, std::string &error)
{
	int fd = ::open(lock_path, O_CREAT | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0600);
	if (fd < 0) {
		error = "cannot open APK lock: " + goerr::errno_text(errno);
		return false;
	}
	lock.fd = fd;
	struct stat st{};
	if (::fstat(fd, &st) != 0 || !S_ISREG(st.st_mode)) {
		error = "unsafe APK lock file";
		return false;
	}
	std::optional<Clock::time_point> expired;
	if (cmd.lock_wait.count() > 0) expired = Clock::now() + cmd.lock_wait;
	for (;;) {
		if (::flock(fd, LOCK_EX | LOCK_NB) == 0) return true;
		if (errno != EWOULDBLOCK && errno != EINTR) {
			error = "cannot lock APK: " + goerr::errno_text(errno);
			return false;
		}
		if (cancelled(cmd)) {
			error = "context canceled";
			return false;
		}
		auto now = Clock::now();
		if ((cmd.deadline && now >= *cmd.deadline) || (expired && now >= *expired)) {
			error = busy_error;
			return false;
		}
		std::this_thread::sleep_for(lock_poll);
	}
}

bool exists(const std::string &path, int &err)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) return true;
	err = errno;
	return false;
}

// waitForApply: the previous transaction's post-commit worker has finished (its pending marker is gone).
bool wait_for_apply(const Cmd &cmd, std::string &error)
{
	auto deadline = Clock::now() + std::chrono::minutes(2);
	if (cmd.deadline && *cmd.deadline < deadline) deadline = *cmd.deadline;
	const std::string pending_path = std::string(apply_state_dir) + "/pending";
	const std::string lock_file = std::string(apply_state_dir) + "/lock";
	for (;;) {
		int err = 0;
		bool pending = exists(pending_path, err);
		if (!pending && err != ENOENT) {
			error = "cannot inspect APK application: " + goerr::path_error("stat", pending_path, err);
			return false;
		}
		if (!pending) {
			int fd = ::open(lock_file.c_str(), O_RDWR | O_CLOEXEC | O_NOFOLLOW);
			if (fd < 0 && errno == ENOENT) return true;
			if (fd < 0) {
				error = "cannot open APK application lock: " + goerr::errno_text(errno);
				return false;
			}
			if (::flock(fd, LOCK_EX | LOCK_NB) == 0) {
				int stat_err = 0;
				bool still = exists(pending_path, stat_err);
				::close(fd);
				if (!still && stat_err == ENOENT) return true;
				if (!still) {
					error = "cannot inspect APK application: " + goerr::path_error("stat", pending_path, stat_err);
					return false;
				}
			} else {
				int e = errno;
				::close(fd);
				if (e != EWOULDBLOCK && e != EINTR) {
					error = "cannot lock APK application: " + goerr::errno_text(e);
					return false;
				}
			}
		}
		if (cancelled(cmd) || Clock::now() >= deadline) {
			error = "waiting for previous APK component application: " +
			        std::string(cancelled(cmd) ? "context canceled" : "context deadline exceeded") +
			        "; inspect nkos-apply-updates status";
			return false;
		}
		std::this_thread::sleep_for(lock_poll);
	}
}

std::map<std::string, std::int64_t> read_mem_info()
{
	std::map<std::string, std::int64_t> values;
	std::ifstream in(mem_info_path);
	for (std::string line; std::getline(in, line);) {
		auto colon = line.find(':');
		if (colon == std::string::npos) continue;
		std::istringstream rest(line.substr(colon + 1));
		std::string first;
		if (!(rest >> first)) continue;
		char *end = nullptr;
		errno = 0;
		long long v = std::strtoll(first.c_str(), &end, 10);
		if (errno == 0 && end && *end == 0 && !first.empty()) values[line.substr(0, colon)] = v;
	}
	return values;
}

bool check_memory(std::int64_t required, std::string &error)
{
	auto info = read_mem_info();
	auto available = info.find("MemAvailable");
	if (available == info.end()) return true;
	std::int64_t budget = available->second + std::min(info["SwapFree"], info["AnonPages"]) / 2;
	if (budget < required) {
		error = "not enough free memory to run APK safely: about " + std::to_string(budget / 1024) + " MiB available, " +
		        std::to_string(required / 1024) +
		        " MiB needed; close remote sessions or stop unused add-ons, or reboot, and try again";
		return false;
	}
	return true;
}

void set_oom_and_priority(pid_t pid, int priority)
{
	std::string path = "/proc/" + std::to_string(pid) + "/oom_score_adj";
	int fd = ::open(path.c_str(), O_WRONLY | O_CLOEXEC);
	if (fd >= 0) {
		(void)!::write(fd, "1000", 4);
		::close(fd);
	}
	::setpriority(PRIO_PROCESS, static_cast<id_t>(pid), priority);
}

enum class Output { capture, split, inherit, null };

Result execute(const Cmd &cmd, Output mode)
{
	bool capture = mode == Output::capture || mode == Output::split;
	bool split = mode == Output::split;
	Result r;
	Class kind = classify(cmd.args);
	Fd lock;
	if (kind != Class::installed_only) {
		std::string error;
		bool ok = acquire(cmd, lock, error) && (kind != Class::transaction || wait_for_apply(cmd, error));
		if (ok) {
			int err = 0;
			if (exists(standby_marker, err) || err != ENOENT) {
				error = "NanoKVM shutdown is in progress";
				ok = false;
			}
		}
		if (ok) ok = check_memory(cmd.memory_kib > 0 ? cmd.memory_kib : required_kib, error);
		if (!ok) {
			r.not_started = true;
			r.error = error;
			r.cancelled = cancelled(cmd);
			return r;
		}
	}
	std::optional<Clock::time_point> deadline = cmd.deadline;
	if (cmd.timeout.count() > 0) {
		auto t = Clock::now() + cmd.timeout;
		if (!deadline || t < *deadline) deadline = t;
	}

	int out[2] = {-1, -1}, err[2] = {-1, -1}, report[2] = {-1, -1};
	if ((capture && ::pipe2(out, O_CLOEXEC) != 0) || (split && ::pipe2(err, O_CLOEXEC) != 0) || ::pipe2(report, O_CLOEXEC) != 0) {
		r.error = goerr::errno_text(errno);
		for (int fd : {out[0], out[1], err[0], err[1], report[0], report[1]})
			if (fd >= 0) ::close(fd);
		return r;
	}
	std::vector<char *> argv;
	argv.push_back(const_cast<char *>(cmd.path.c_str()));
	for (const auto &a : cmd.args) argv.push_back(const_cast<char *>(a.c_str()));
	argv.push_back(nullptr);
	pid_t pid = ::fork();
	if (pid < 0) {
		r.error = goerr::start_error(cmd.path, errno);
		for (int fd : {out[0], out[1], err[0], err[1], report[0], report[1]})
			if (fd >= 0) ::close(fd);
		return r;
	}
	if (pid == 0) {
		if (kind != Class::transaction) ::setpgid(0, 0);
		int null = ::open("/dev/null", O_RDWR);
		::dup2(null, 0);
		if (capture) {
			::dup2(out[1], 1);
			::dup2(split ? err[1] : out[1], 2);
		} else if (mode == Output::null) {
			::dup2(null, 1);
			::dup2(null, 2);
		}
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int s : {SIGPIPE, SIGTERM, SIGINT, SIGHUP}) ::sigaction(s, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		::execve(argv[0], argv.data(), environ);
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	if (capture) ::close(out[1]);
	if (split) ::close(err[1]);
	::close(report[1]);
	int exec_errno = 0;
	bool failed = ::read(report[0], &exec_errno, sizeof exec_errno) == sizeof exec_errno;
	::close(report[0]);
	if (failed) {
		int status = 0;
		while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
		}
		if (capture) ::close(out[0]);
		if (split) ::close(err[0]);
		r.error = goerr::start_error(cmd.path, exec_errno);
		return r;
	}
	set_oom_and_priority(pid, kind == Class::index_query ? 19 : 0);

	auto kill_it = [&] {
		if (kind != Class::transaction) ::kill(-pid, SIGKILL);
		else ::kill(pid, SIGKILL);
	};
	bool killed = false;
	std::optional<Clock::time_point> drain_until;
	int status = 0;
	bool reaped = false;
	if (capture) {
		::fcntl(out[0], F_SETFL, O_NONBLOCK);
		if (split) ::fcntl(err[0], F_SETFL, O_NONBLOCK);
		bool open = true, err_open = split;
		while (open || err_open) {
			if (!killed && (cancelled(cmd) || (deadline && Clock::now() >= *deadline))) {
				kill_it();
				killed = true;
				r.cancelled = cancelled(cmd);
				// A read-only run's descendants may hold the pipe: Go's WaitDelay ends the wait after 5 s.
				if (kind != Class::transaction) drain_until = Clock::now() + wait_delay;
			}
			if (drain_until && Clock::now() >= *drain_until) break;
			pollfd p[2] = {{open ? out[0] : -1, POLLIN, 0}, {err_open ? err[0] : -1, POLLIN, 0}};
			if (::poll(p, 2, 100) < 0 && errno != EINTR) break;
			char buf[8192];
			auto drain = [&](int fd, std::string &into, bool &is_open) {
				while (is_open) {
					ssize_t got = ::read(fd, buf, sizeof buf);
					if (got > 0) {
						into.append(buf, static_cast<std::size_t>(got));
						continue;
					}
					if (got == 0) is_open = false;
					else if (errno != EAGAIN && errno != EINTR) is_open = false;
					break;
				}
			};
			drain(out[0], r.output, open);
			if (split) drain(err[0], r.stderr_output, err_open);
		}
		::close(out[0]);
		if (split) ::close(err[0]);
	} else {
		for (;;) {
			pid_t w = ::waitpid(pid, &status, WNOHANG);
			if (w == pid) {
				reaped = true;
				break;
			}
			if (w < 0 && errno != EINTR) break;
			if (!killed && (cancelled(cmd) || (deadline && Clock::now() >= *deadline))) {
				kill_it();
				killed = true;
				r.cancelled = cancelled(cmd);
			}
			std::this_thread::sleep_for(std::chrono::milliseconds(50));
		}
	}
	if (!reaped)
		while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
		}
	if (WIFEXITED(status) && WEXITSTATUS(status) == 0) {
		r.ok = true;
	} else if (WIFEXITED(status)) {
		r.error = goerr::exit_text(WEXITSTATUS(status), 0);
	} else if (WIFSIGNALED(status)) {
		r.error = goerr::exit_text(-1, WTERMSIG(status));
	}
	return r;
}

} // namespace

Result combined_output(const Cmd &cmd)
{
	Result r = execute(cmd, Output::capture);
	if (r.not_started) r.output = r.error;
	return r;
}

Result run(const Cmd &cmd)
{
	Result r = execute(cmd, Output::inherit);
	if (r.not_started) {
		std::string line = r.error + "\n";
		(void)!::write(2, line.data(), line.size());
	}
	return r;
}

Result run_quiet(const Cmd &cmd) { return execute(cmd, Output::null); }

Result output_split(const Cmd &cmd)
{
	Result r = execute(cmd, Output::split);
	if (r.not_started) r.stderr_output = r.error + "\n";
	return r;
}

} // namespace nkos::apkrun
