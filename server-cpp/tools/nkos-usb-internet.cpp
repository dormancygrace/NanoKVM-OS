// nkos-usb-internet, as the Go server's cmd/nkos-usb-internet: called only by the serialized S30usbnet lifecycle
// (apply PREFIX, stop, watch-needed), and run by the OpenRC service nanokvm-usb-internet as the watcher (watch),
// which reapplies that lifecycle without rebinding USB: every 5 s, and promptly on link, address, route, rule and
// firewall changes (ip monitor, nft monitor; the helper's own table's events ignored).

#include "common/process.hpp"
#include "usb/usbinternet.hpp"

#include <algorithm>
#include <chrono>
#include <condition_variable>
#include <csignal>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <mutex>
#include <string>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>
#include <vector>

extern char **environ;

namespace {

using namespace nkos::usb;

[[noreturn]] void fail(const std::string &error)
{
	std::fprintf(stderr, "%s\n", error.c_str());
	std::exit(1);
}

std::mutex mutex;
std::condition_variable wake;
bool event = false, stopping = false;
std::vector<pid_t> children;

pid_t spawn(const std::vector<std::string> &argv, int out_fd, bool watcher_env)
{
	std::vector<std::string> env;
	for (char **e = environ; *e; e++) env.emplace_back(*e);
	if (watcher_env) env.emplace_back("NANOKVM_USB_NET_WATCHER=1");
	std::vector<char *> args, envp;
	for (auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	for (auto &e : env) envp.push_back(e.data());
	envp.push_back(nullptr);
	pid_t pid = ::fork();
	if (pid == 0) {
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		int null = ::open("/dev/null", O_RDWR);
		if (null >= 0) {
			::dup2(null, 0);
			if (out_fd < 0) ::dup2(null, 1);
		}
		if (out_fd >= 0) ::dup2(out_fd, 1);
		nkos::process::cloexec_inherited();
		::execvpe(args[0], args.data(), envp.data());
		::_exit(127);
	}
	return pid;
}

// WNOWAIT reserves the child's PID until it has been removed from the shutdown signal list.
pid_t wait_child(pid_t pid, int &status, bool block)
{
	siginfo_t info{};
	int rc;
	do {
		rc = ::waitid(P_PID, static_cast<id_t>(pid), &info, WEXITED | WNOWAIT | (block ? 0 : WNOHANG));
	} while (rc < 0 && errno == EINTR);
	if (rc == 0 && info.si_pid == 0) return 0;
	{
		std::lock_guard lock(mutex);
		std::erase(children, pid);
	}
	if (rc < 0) return -1;
	pid_t reaped;
	do {
		reaped = ::waitpid(pid, &status, 0);
	} while (reaped < 0 && errno == EINTR);
	return reaped;
}

// A monitor: each line of its output is an event (nft's lines about the helper's own table excepted).
void monitor(std::vector<std::string> argv)
{
	int fds[2];
	if (::pipe2(fds, O_CLOEXEC) != 0) return;
	pid_t pid = spawn(argv, fds[1], false);
	::close(fds[1]);
	if (pid < 0) {
		::close(fds[0]);
		return;
	}
	{
		std::lock_guard lock(mutex);
		children.push_back(pid);
		if (stopping) ::kill(pid, SIGKILL);
	}
	bool nft = argv[0] == "nft";
	std::string pending;
	char buf[4096];
	for (;;) {
		ssize_t n = ::read(fds[0], buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) break;
		pending.append(buf, static_cast<std::size_t>(n));
		for (std::size_t nl; (nl = pending.find('\n')) != std::string::npos; pending.erase(0, nl + 1)) {
			if (nft && pending.substr(0, nl).find(usbinternet::table) != std::string::npos) continue;
			std::lock_guard lock(mutex);
			event = true;
			wake.notify_all();
		}
	}
	::close(fds[0]);
	int status;
	wait_child(pid, status, true);
}

int watch()
{
	// Our waiters own reaping. An inherited SIG_IGN would let the kernel recycle a PID first.
	struct sigaction reap{};
	reap.sa_handler = SIG_DFL;
	::sigemptyset(&reap.sa_mask);
	if (::sigaction(SIGCHLD, &reap, nullptr) != 0) fail(std::strerror(errno));
	// SIGTERM and SIGINT end the watcher: blocked here, taken by a thread of their own.
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGTERM);
	sigaddset(&set, SIGINT);
	::pthread_sigmask(SIG_BLOCK, &set, nullptr);
	std::thread signals([set] {
		int sig;
		::sigwait(&set, &sig);
		std::lock_guard lock(mutex);
		stopping = true;
		for (pid_t pid : children) ::kill(pid, SIGKILL);
		wake.notify_all();
	});
	signals.detach();
	std::thread ip(monitor, std::vector<std::string>{"ip", "monitor", "link", "address", "route", "rule"});
	std::thread nft(monitor, std::vector<std::string>{"nft", "monitor"});
	// A ticker of 5 s (a tick missed during a long run is delivered at once, as Go's buffered ticker).
	auto next = std::chrono::steady_clock::now() + std::chrono::seconds(5);
	for (;;) {
		// A reconciliation run by the watcher must never synchronously stop the watcher.
		pid_t pid = spawn({"sh", "/etc/init.d/S30usbnet", "start"}, -1, true);
		if (pid > 0) {
			{
				std::lock_guard lock(mutex);
				children.push_back(pid);
				if (stopping) ::kill(pid, SIGKILL);
			}
			auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(15);
			int status = 0;
			bool killed = false;
			pid_t reaped;
			while ((reaped = wait_child(pid, status, false)) == 0) {
				if (!killed && std::chrono::steady_clock::now() >= deadline) {
					::kill(pid, SIGKILL);
					killed = true;
				}
				std::this_thread::sleep_for(std::chrono::milliseconds(50));
			}
			std::lock_guard lock(mutex);
			bool ok = reaped == pid && WIFEXITED(status) && WEXITSTATUS(status) == 0;
			if (!ok && !stopping) std::fprintf(stderr, "USB network reconciliation failed\n");
		}
		std::unique_lock lock(mutex);
		wake.wait_until(lock, next, [] { return stopping || event; });
		if (stopping) break;
		if (event) {
			event = false;
		} else {
			for (auto now = std::chrono::steady_clock::now(); next <= now;) next += std::chrono::seconds(5);
		}
	}
	// Reap ip and nft before the watcher exits.
	ip.join();
	nft.join();
	return 0;
}

} // namespace

int main(int argc, char **argv)
{
	auto r = usbinternet::default_runtime();
	if (argc < 2) fail("usage: nkos-usb-internet {apply PREFIX|stop|watch|watch-needed}");
	std::string action = argv[1];
	if (action == "watch-needed") return usbinternet::watch_needed(r) ? 0 : 1;
	if (action == "apply") {
		if (argc != 3) fail("USB prefix required");
		if (auto e = usbinternet::apply(r, argv[2], false); !e.empty()) fail(e);
		return 0;
	}
	if (action == "stop") {
		if (auto e = usbinternet::apply(r, "", true); !e.empty()) fail(e);
		return 0;
	}
	if (action == "watch") return watch();
	fail("unknown action");
}
