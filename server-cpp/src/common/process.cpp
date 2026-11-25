#include "common/process.hpp"

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <linux/close_range.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

extern char **environ;

namespace nkos::process {

void cloexec_inherited()
{
	::syscall(SYS_close_range, 3u, ~0u, CLOSE_RANGE_CLOEXEC);
}

Result run(const std::vector<std::string> &argv, const Options &options)
{
	Result r;
	if (argv.empty()) {
		r.error = "no program";
		return r;
	}
	int out[2] = {-1, -1}, in[2] = {-1, -1}, report[2] = {-1, -1};
	if (::pipe2(out, O_CLOEXEC) != 0 || ::pipe2(in, O_CLOEXEC) != 0 || ::pipe2(report, O_CLOEXEC) != 0) {
		r.error = std::strerror(errno);
		for (int fd : {out[0], out[1], in[0], in[1], report[0], report[1]})
			if (fd >= 0) ::close(fd);
		return r;
	}
	std::vector<char *> args;
	for (const auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	std::vector<std::string> env_storage;
	std::vector<char *> env;
	for (char **e = environ; *e; e++) env.push_back(*e);
	for (const auto &e : options.env) env.push_back(const_cast<char *>(e.c_str()));
	env.push_back(nullptr);

	pid_t pid = ::fork();
	if (pid < 0) {
		r.error = std::strerror(errno);
		for (int fd : {out[0], out[1], in[0], in[1], report[0], report[1]}) ::close(fd);
		return r;
	}
	if (pid == 0) {
		::setpgid(0, 0);
		::dup2(in[0], 0);
		int devnull = ::open("/dev/null", O_WRONLY);
		::dup2(options.capture ? out[1] : devnull, 1);
		::dup2(options.capture ? out[1] : devnull, 2);
		if (!options.dir.empty() && ::chdir(options.dir.c_str()) != 0) {
			int e = errno;
			(void)!::write(report[1], &e, sizeof e);
			::_exit(127);
		}
		// The child does not keep the server's signal setup.
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int s : {SIGPIPE, SIGTERM, SIGINT}) ::sigaction(s, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		cloexec_inherited();
		::execve(args[0], args.data(), env.data());
		if (std::strchr(args[0], '/') == nullptr) ::execvpe(args[0], args.data(), env.data());
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	::close(out[1]);
	::close(in[0]);
	::close(report[1]);
	int exec_errno = 0;
	if (::read(report[0], &exec_errno, sizeof exec_errno) == sizeof exec_errno) {
		r.error = std::strerror(exec_errno);
	} else {
		r.started = true;
	}
	::close(report[0]);

	// Input, the output until EOF, and the child's exit, all within the timeout: a child whose output goes to
	// /dev/null closes nothing the loop could wait on, so its exit is watched through a pidfd.
	std::size_t written = 0;
	int in_fd = in[1];
	if (options.input.empty()) {
		::close(in_fd);
		in_fd = -1;
	} else {
		::fcntl(in_fd, F_SETFL, O_NONBLOCK);
	}
	::fcntl(out[0], F_SETFL, O_NONBLOCK);
	auto deadline = std::chrono::steady_clock::now() + options.timeout;
	bool out_open = true;
	int pidfd = static_cast<int>(::syscall(SYS_pidfd_open, pid, 0));
	bool exited = pidfd < 0;
	while (out_open || in_fd >= 0 || !exited) {
		auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - std::chrono::steady_clock::now()).count();
		if (left <= 0) {
			r.timed_out = true;
			::kill(-pid, SIGKILL);
			break;
		}
		if (options.cancel) {
			if (options.cancel->load()) {
				r.cancelled = true;
				::kill(-pid, SIGKILL);
				break;
			}
			left = std::min(left, decltype(left){100});
		}
		pollfd fds[3];
		int n = 0, out_index = -1, in_index = -1, pid_index = -1;
		if (out_open) out_index = n, fds[n++] = {out[0], POLLIN, 0};
		if (in_fd >= 0) in_index = n, fds[n++] = {in_fd, POLLOUT, 0};
		if (!exited) pid_index = n, fds[n++] = {pidfd, POLLIN, 0};
		if (::poll(fds, static_cast<nfds_t>(n), static_cast<int>(left)) < 0) {
			if (errno == EINTR) continue;
			break;
		}
		if (pid_index >= 0 && fds[pid_index].revents) exited = true;
		if (out_index >= 0 && fds[out_index].revents) {
			char buf[4096];
			ssize_t got = ::read(out[0], buf, sizeof buf);
			if (got > 0) r.output.append(buf, static_cast<std::size_t>(got));
			else if (got == 0 || (errno != EAGAIN && errno != EINTR)) out_open = false;
		}
		if (in_index >= 0 && fds[in_index].revents) {
			ssize_t put = ::write(in_fd, options.input.data() + written, options.input.size() - written);
			if (put > 0) written += static_cast<std::size_t>(put);
			if (put < 0 && errno != EAGAIN && errno != EINTR) written = options.input.size();
			if (written >= options.input.size()) {
				::close(in_fd);
				in_fd = -1;
			}
		}
	}
	if (in_fd >= 0) ::close(in_fd);
	::close(out[0]);
	if (pidfd >= 0) ::close(pidfd);
	int status = 0;
	while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
	}
	if (WIFEXITED(status)) r.exit_code = WEXITSTATUS(status);
	else if (WIFSIGNALED(status)) r.signal = WTERMSIG(status);
	if (!r.started) r.exit_code = -1;
	return r;
}

} // namespace nkos::process
