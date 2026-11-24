#include "extensions/common.hpp"

#include "common/gojson.hpp"
#include "common/process.hpp"
#include "osupdate/goerr.hpp"

#include <algorithm>
#include <cctype>
#include <climits>
#include <cerrno>
#include <csignal>
#include <cstring>
#include <ctime>
#include <dirent.h>
#include <fcntl.h>
#include <poll.h>
#include <sys/random.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

extern char **environ;

namespace nkos::extensions {

namespace {

#include "extensions/isprint.inc"

} // namespace

// --- answers -------------------------------------------------------------------------------------------------------

std::string reply_json(const Reply &reply)
{
	return "{\"code\":" + std::to_string(reply.code) + ",\"msg\":" + q(reply.msg) + ",\"data\":" +
	       (reply.data.empty() ? std::string("null") : reply.data) + "}";
}

void send(http::Response &res, const Reply &reply) { res.json(200, reply_json(reply)); }

std::string q(std::string_view s) { return gojson::quote(s); }

std::string json_strings(const std::vector<std::string> &list)
{
	std::string out = "[";
	for (std::size_t i = 0; i < list.size(); i++) out += (i ? "," : "") + q(list[i]);
	return out + "]";
}

std::string json_bool(bool b) { return b ? "true" : "false"; }

void answer_later(http::Request &req, http::Response &res, std::function<Reply()> fn)
{
	res.pending = true;
	std::thread([id = req.id, headers = res.headers, fn = std::move(fn)] {
		http::Response out;
		out.headers = headers;
		send(out, fn());
		http::complete(id, std::move(out));
	}).detach();
}

// --- Go texts -------------------------------------------------------------------------------------------------------

std::pair<char32_t, std::size_t> decode_rune(std::string_view s, std::size_t i)
{
	unsigned char c = static_cast<unsigned char>(s[i]);
	if (c < 0x80) return {c, 1};
	int n = c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? (c >= 0xf0 ? 0 : 3) : c >= 0xc2 ? 2 : 0;
	if (n == 0 || i + static_cast<std::size_t>(n) > s.size()) return {0xfffd, 1};
	char32_t r = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
	for (int k = 1; k < n; k++) {
		unsigned char cc = static_cast<unsigned char>(s[i + static_cast<std::size_t>(k)]);
		if ((cc & 0xc0) != 0x80) return {0xfffd, 1};
		r = (r << 6) | (cc & 0x3f);
	}
	if ((n == 2 && r < 0x80) || (n == 3 && (r < 0x800 || (r >= 0xd800 && r <= 0xdfff))) || (n == 4 && (r < 0x10000 || r > 0x10ffff)))
		return {0xfffd, 1};
	return {r, static_cast<std::size_t>(n)};
}

void append_rune(std::string &out, char32_t r)
{
	if (r > 0x10ffff || (r >= 0xd800 && r <= 0xdfff)) r = 0xfffd;
	if (r < 0x80) {
		out += static_cast<char>(r);
	} else if (r < 0x800) {
		out += static_cast<char>(0xc0 | (r >> 6));
		out += static_cast<char>(0x80 | (r & 0x3f));
	} else if (r < 0x10000) {
		out += static_cast<char>(0xe0 | (r >> 12));
		out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
		out += static_cast<char>(0x80 | (r & 0x3f));
	} else {
		out += static_cast<char>(0xf0 | (r >> 18));
		out += static_cast<char>(0x80 | ((r >> 12) & 0x3f));
		out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
		out += static_cast<char>(0x80 | (r & 0x3f));
	}
}

bool is_print(char32_t r)
{
	if (r <= 0xff) {
		if (r >= 0x20 && r <= 0x7e) return true;
		if (r >= 0xa1 && r <= 0xff) return r != 0xad;
		return false;
	}
	// The first entry not below r: the start or end of a range that may hold it.
	auto in_ranges = [](const auto &table, auto x) {
		auto it = std::lower_bound(std::begin(table), std::end(table), x);
		std::size_t i = static_cast<std::size_t>(it - std::begin(table));
		std::size_t n = std::size(table);
		return i < n && table[i & ~std::size_t(1)] <= x && x <= table[i | 1];
	};
	auto listed = [](const auto &table, auto x) { return std::binary_search(std::begin(table), std::end(table), x); };
	if (r < 0x10000) {
		auto x = static_cast<std::uint16_t>(r);
		return in_ranges(is_print16, x) && !listed(is_not_print16, x);
	}
	auto x = static_cast<std::uint32_t>(r);
	if (!in_ranges(is_print32, x)) return false;
	if (r >= 0x20000) return true;
	return !listed(is_not_print32, static_cast<std::uint16_t>(r - 0x10000));
}

std::string go_quote(std::string_view s)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		if (n == 1 && r == 0xfffd) {
			unsigned char c = static_cast<unsigned char>(s[i]);
			out += "\\x";
			out += hex[c >> 4];
			out += hex[c & 15];
			i++;
			continue;
		}
		i += n;
		if (r == '"' || r == '\\') {
			out += '\\';
			out += static_cast<char>(r);
			continue;
		}
		if (is_print(r)) {
			append_rune(out, r);
			continue;
		}
		switch (r) {
		case '\a': out += "\\a"; continue;
		case '\b': out += "\\b"; continue;
		case '\f': out += "\\f"; continue;
		case '\n': out += "\\n"; continue;
		case '\r': out += "\\r"; continue;
		case '\t': out += "\\t"; continue;
		case '\v': out += "\\v"; continue;
		}
		if (r < ' ' || r == 0x7f) {
			out += "\\x";
			out += hex[(r >> 4) & 15];
			out += hex[r & 15];
		} else if (r < 0x10000) {
			out += "\\u";
			for (int sh = 12; sh >= 0; sh -= 4) out += hex[(r >> sh) & 15];
		} else {
			out += "\\U";
			for (int sh = 28; sh >= 0; sh -= 4) out += hex[(r >> sh) & 15];
		}
	}
	return out + "\"";
}

std::int64_t parse_int_value(std::string_view s)
{
	bool negative = false;
	if (!s.empty() && (s[0] == '+' || s[0] == '-')) {
		negative = s[0] == '-';
		s.remove_prefix(1);
	}
	if (s.empty()) return 0;
	std::uint64_t n = 0;
	bool over = false;
	for (char c : s) {
		if (c < '0' || c > '9') return 0;
		if (n > (UINT64_MAX - static_cast<std::uint64_t>(c - '0')) / 10) over = true;
		else n = n * 10 + static_cast<std::uint64_t>(c - '0');
	}
	if (negative) return over || n > static_cast<std::uint64_t>(INT64_MAX) + 1 ? INT64_MIN : -static_cast<std::int64_t>(n - 1) - 1;
	return over || n > static_cast<std::uint64_t>(INT64_MAX) ? INT64_MAX : static_cast<std::int64_t>(n);
}

std::uint64_t parse_uint_value(std::string_view s)
{
	if (s.empty()) return 0;
	std::uint64_t n = 0;
	bool over = false;
	for (char c : s) {
		if (c < '0' || c > '9') return 0;
		if (n > (UINT64_MAX - static_cast<std::uint64_t>(c - '0')) / 10) over = true;
		else n = n * 10 + static_cast<std::uint64_t>(c - '0');
	}
	return over ? UINT64_MAX : n;
}

std::string go_duration(std::int64_t seconds)
{
	if (seconds == 0) return "0s";
	std::int64_t h = seconds / 3600, m = seconds / 60 % 60, s = seconds % 60;
	if (h) return std::to_string(h) + "h" + std::to_string(m) + "m" + std::to_string(s) + "s";
	if (m) return std::to_string(m) + "m" + std::to_string(s) + "s";
	return std::to_string(s) + "s";
}

std::string rfc3339(std::int64_t unix_seconds)
{
	std::time_t t = static_cast<std::time_t>(unix_seconds);
	std::tm tm{};
	::gmtime_r(&t, &tm);
	char buf[32];
	std::strftime(buf, sizeof buf, "%Y-%m-%dT%H:%M:%SZ", &tm);
	return buf;
}

std::function<std::string()> now_rfc3339 = [] { return rfc3339(std::time(nullptr)); };

// --- commands -------------------------------------------------------------------------------------------------------

std::optional<std::string> look_path(const std::string &name)
{
	auto executable = [](const std::string &path) {
		struct stat st{};
		return ::stat(path.c_str(), &st) == 0 && !S_ISDIR(st.st_mode) && (st.st_mode & 0111) != 0;
	};
	if (name.find('/') != std::string::npos) return executable(name) ? std::optional(name) : std::nullopt;
	const char *path = ::getenv("PATH");
	std::string_view dirs = path ? path : "";
	while (true) {
		auto colon = dirs.find(':');
		std::string dir(dirs.substr(0, colon));
		std::string candidate = (dir.empty() ? std::string(".") : dir) + "/" + name;
		if (executable(candidate)) return candidate;
		if (colon == std::string_view::npos) break;
		dirs.remove_prefix(colon + 1);
	}
	return std::nullopt;
}

std::string not_found(const std::string &name) { return "exec: \"" + name + "\": executable file not found in $PATH"; }

Result run(const Command &cmd)
{
	Result r;
	if (cmd.argv.empty()) {
		r.error = "exec: no command";
		return r;
	}
	std::string program = cmd.argv[0];
	if (program.find('/') == std::string::npos) {
		auto found = look_path(program);
		if (!found) {
			r.error = not_found(program);
			return r;
		}
		program = *found;
	}
	int out[2] = {-1, -1}, err[2] = {-1, -1}, in[2] = {-1, -1}, report[2] = {-1, -1};
	bool capture = cmd.capture != Command::Capture::none;
	bool split = cmd.capture == Command::Capture::output;
	auto close_all = [&] {
		for (int fd : {out[0], out[1], err[0], err[1], in[0], in[1], report[0], report[1]})
			if (fd >= 0) ::close(fd);
	};
	if ((capture && ::pipe2(out, O_CLOEXEC)) || (split && ::pipe2(err, O_CLOEXEC)) || (cmd.input && ::pipe2(in, O_CLOEXEC)) ||
	    ::pipe2(report, O_CLOEXEC)) {
		r.error = goerr::errno_text(errno);
		close_all();
		return r;
	}
	std::vector<std::string> env_strings;
	if (cmd.env) {
		env_strings = *cmd.env;
	} else {
		for (char **e = environ; *e; e++) env_strings.emplace_back(*e);
		env_strings.insert(env_strings.end(), cmd.extra_env.begin(), cmd.extra_env.end());
	}
	std::vector<char *> args, env;
	for (const auto &a : cmd.argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	for (const auto &e : env_strings) env.push_back(const_cast<char *>(e.c_str()));
	env.push_back(nullptr);
	// os.StartProcess checks the directory first when no SysProcAttr is set (no process group).
	if (!cmd.dir.empty() && !cmd.group) {
		struct stat st{};
		if (::stat(cmd.dir.c_str(), &st) != 0) {
			r.error = goerr::path_error("chdir", cmd.dir, errno);
			close_all();
			return r;
		}
	}
	int null_fd = ::open("/dev/null", O_RDWR | O_CLOEXEC);

	pid_t pid = ::fork();
	if (pid < 0) {
		r.error = "fork/exec " + program + ": " + goerr::errno_text(errno);
		close_all();
		if (null_fd >= 0) ::close(null_fd);
		return r;
	}
	if (pid == 0) {
		if (cmd.group) ::setpgid(0, 0);
		::dup2(cmd.input ? in[0] : null_fd, 0);
		::dup2(capture ? out[1] : null_fd, 1);
		::dup2(split ? err[1] : capture ? out[1] : null_fd, 2);
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int s : {SIGPIPE, SIGTERM, SIGINT, SIGUSR1, SIGUSR2, SIGCHLD}) ::sigaction(s, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		if (!cmd.dir.empty() && ::chdir(cmd.dir.c_str()) != 0) {
			int e = errno;
			(void)!::write(report[1], &e, sizeof e);
			::_exit(127);
		}
		::execve(program.c_str(), args.data(), env.data());
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	if (cmd.group) ::setpgid(pid, pid);
	if (null_fd >= 0) ::close(null_fd);
	for (int *fd : {&out[1], &err[1], &in[0], &report[1]})
		if (*fd >= 0) ::close(*fd), *fd = -1;
	int exec_errno = 0;
	bool started = ::read(report[0], &exec_errno, sizeof exec_errno) != sizeof exec_errno;
	::close(report[0]);
	report[0] = -1;
	if (!started) {
		int status = 0;
		while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
		}
		close_all();
		r.error = "fork/exec " + program + ": " + goerr::errno_text(exec_errno);
		return r;
	}
	r.started = true;

	int in_fd = in[1];
	in[1] = -1;
	std::size_t written = 0;
	if (in_fd >= 0) {
		if (cmd.input->empty()) {
			::close(in_fd);
			in_fd = -1;
		} else {
			::fcntl(in_fd, F_SETFL, O_NONBLOCK);
		}
	}
	using clock = std::chrono::steady_clock;
	auto deadline = cmd.timeout ? clock::now() + *cmd.timeout : clock::time_point::max();
	bool out_open = out[0] >= 0, err_open = err[0] >= 0, killed = false, exited = false, delay_expired = false;
	int status = 0;
	clock::time_point ended{};
	auto kill_it = [&] {
		if (cmd.group) ::kill(-pid, SIGKILL);
		else ::kill(pid, SIGKILL);
		killed = true;
		r.deadline = true;
	};
	while (out_open || err_open || in_fd >= 0 || !exited) {
		auto now = clock::now();
		if (!exited) {
			pid_t w = ::waitpid(pid, &status, WNOHANG);
			if (w == pid) exited = true, ended = now;
		}
		if (!killed && !exited && now >= deadline) kill_it();
		if (exited && !out_open && !err_open && in_fd < 0) break;
		if (exited && cmd.wait_delay && now - ended >= *cmd.wait_delay) {
			delay_expired = true;
			break;
		}
		pollfd fds[3];
		int n = 0, oi = -1, ei = -1, ii = -1;
		if (out_open) oi = n, fds[n++] = {out[0], POLLIN, 0};
		if (err_open) ei = n, fds[n++] = {err[0], POLLIN, 0};
		if (in_fd >= 0) ii = n, fds[n++] = {in_fd, POLLOUT, 0};
		int wait = exited && !cmd.wait_delay ? -1 : 50;
		if (n == 0) {
			::usleep(10000);
			continue;
		}
		if (::poll(fds, static_cast<nfds_t>(n), wait) < 0) {
			if (errno == EINTR) continue;
			break;
		}
		char buf[8192];
		if (oi >= 0 && fds[oi].revents) {
			ssize_t got = ::read(out[0], buf, sizeof buf);
			if (got > 0) {
				if (cmd.sink) cmd.sink(std::string_view(buf, static_cast<std::size_t>(got)));
				else r.out.append(buf, static_cast<std::size_t>(got));
			} else if (got == 0 || (errno != EAGAIN && errno != EINTR)) {
				out_open = false;
			}
		}
		if (ei >= 0 && fds[ei].revents) {
			ssize_t got = ::read(err[0], buf, sizeof buf);
			if (got > 0) {
				if (cmd.keep_stderr) r.err_out.append(buf, static_cast<std::size_t>(got));
			} else if (got == 0 || (errno != EAGAIN && errno != EINTR)) {
				err_open = false;
			}
		}
		if (ii >= 0 && fds[ii].revents) {
			ssize_t put = ::write(in_fd, cmd.input->data() + written, cmd.input->size() - written);
			if (put > 0) written += static_cast<std::size_t>(put);
			if (put < 0 && errno != EAGAIN && errno != EINTR) written = cmd.input->size();
			if (written >= cmd.input->size()) {
				::close(in_fd);
				in_fd = -1;
			}
		}
	}
	if (in_fd >= 0) ::close(in_fd);
	close_all();
	while (!exited) {
		pid_t w = ::waitpid(pid, &status, 0);
		if (w == pid) exited = true;
		else if (w < 0 && errno != EINTR) break;
	}
	if (cmd.kill_group_after) ::kill(-pid, SIGKILL);
	if (WIFEXITED(status)) {
		r.exit_code = WEXITSTATUS(status);
		if (r.exit_code != 0) r.error = "exit status " + std::to_string(r.exit_code), r.exited = true;
	} else if (WIFSIGNALED(status)) {
		r.exit_code = -1;
		r.exited = true;
		r.error = "signal: " + goerr::signal_text(WTERMSIG(status));
	}
	if (r.error.empty() && killed) r.error = "context deadline exceeded";
	if (r.error.empty() && delay_expired) r.error = "exec: WaitDelay expired before I/O complete";
	return r;
}

Child spawn(const std::vector<std::string> &argv, bool group, bool pipe_out, bool pipe_err)
{
	Child c;
	std::string program = argv.at(0);
	if (program.find('/') == std::string::npos) {
		auto found = look_path(program);
		if (!found) {
			c.error = not_found(program);
			return c;
		}
		program = *found;
	}
	int out[2] = {-1, -1}, err[2] = {-1, -1}, report[2] = {-1, -1};
	if ((pipe_out && ::pipe2(out, O_CLOEXEC)) || (pipe_err && ::pipe2(err, O_CLOEXEC)) || ::pipe2(report, O_CLOEXEC)) {
		c.error = goerr::errno_text(errno);
		for (int fd : {out[0], out[1], err[0], err[1], report[0], report[1]})
			if (fd >= 0) ::close(fd);
		return c;
	}
	std::vector<char *> args;
	for (const auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	int null_fd = ::open("/dev/null", O_RDWR | O_CLOEXEC);
	pid_t pid = ::fork();
	if (pid == 0) {
		if (group) ::setpgid(0, 0);
		::dup2(null_fd, 0);
		::dup2(pipe_out ? out[1] : null_fd, 1);
		::dup2(pipe_err ? err[1] : null_fd, 2);
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int s : {SIGPIPE, SIGTERM, SIGINT, SIGUSR1, SIGUSR2, SIGCHLD}) ::sigaction(s, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		::execve(program.c_str(), args.data(), environ);
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	if (null_fd >= 0) ::close(null_fd);
	for (int fd : {out[1], err[1], report[1]})
		if (fd >= 0) ::close(fd);
	int exec_errno = 0;
	bool started = pid > 0 && ::read(report[0], &exec_errno, sizeof exec_errno) != sizeof exec_errno;
	::close(report[0]);
	if (!started) {
		if (pid > 0) ::waitpid(pid, nullptr, 0);
		for (int fd : {out[0], err[0]})
			if (fd >= 0) ::close(fd);
		c.error = "fork/exec " + program + ": " + goerr::errno_text(pid > 0 ? exec_errno : errno);
		return c;
	}
	if (group) ::setpgid(pid, pid);
	c.pid = pid;
	c.out = out[0];
	c.err = err[0];
	return c;
}

std::string find_url(std::string_view text)
{
	// [:space:] is ASCII white space: tab, newline, vertical tab, form feed, carriage return and space.
	auto space = [](char c) { return c == ' ' || (c >= '\t' && c <= '\r'); };
	for (std::size_t from = 0;;) {
		auto i = text.find("https://", from);
		if (i == std::string_view::npos) return "";
		std::size_t j = i + 8;
		while (j < text.size() && !space(text[j])) j++;
		if (j > i + 8) return std::string(text.substr(i, j - i));
		from = i + 1;
	}
}

// --- files ----------------------------------------------------------------------------------------------------------

namespace {

std::string parent(const std::string &path)
{
	auto slash = path.rfind('/');
	if (slash == std::string::npos) return ".";
	if (slash == 0) return "/";
	return path.substr(0, slash);
}

std::string mkdir_all(const std::string &path, mode_t mode)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) {
		if (S_ISDIR(st.st_mode)) return "";
		return goerr::path_error("mkdir", path, ENOTDIR);
	}
	std::string up = parent(path);
	if (up != path && up != "." && up != "/") {
		if (auto e = mkdir_all(up, mode); !e.empty()) return e;
	}
	if (::mkdir(path.c_str(), mode) != 0 && errno != EEXIST) return goerr::path_error("mkdir", path, errno);
	return "";
}

} // namespace

std::string write_private(const std::string &path, std::string_view data)
{
	std::string dir = parent(path);
	if (auto e = mkdir_all(dir, 0700); !e.empty()) return e;
	std::string temp;
	int fd = -1;
	for (int attempt = 0; attempt < 10000 && fd < 0; attempt++) {
		temp = dir + "/.pending-" + temp_suffix();
		fd = ::open(temp.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd < 0 && errno != EEXIST) return goerr::path_error("open", temp, errno);
	}
	if (fd < 0) return goerr::path_error("open", temp, EEXIST);
	std::string error;
	for (std::size_t done = 0; done < data.size() && error.empty();) {
		ssize_t n = ::write(fd, data.data() + done, data.size() - done);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) error = goerr::path_error("write", temp, errno);
		else done += static_cast<std::size_t>(n);
	}
	if (error.empty() && ::fsync(fd) != 0) error = goerr::path_error("sync", temp, errno);
	if (::close(fd) != 0 && error.empty()) error = goerr::path_error("close", temp, errno);
	if (error.empty() && ::rename(temp.c_str(), path.c_str()) != 0)
		error = "rename " + temp + " " + path + ": " + goerr::errno_text(errno);
	if (!error.empty()) {
		::unlink(temp.c_str());
		return error;
	}
	int d = ::open(dir.c_str(), O_RDONLY | O_CLOEXEC);
	if (d < 0) return goerr::path_error("open", dir, errno);
	if (::fsync(d) != 0) error = goerr::path_error("sync", dir, errno);
	::close(d);
	return error;
}

std::optional<std::string> read_file(const std::string &path)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return std::nullopt;
	std::string data;
	char buf[16384];
	while (true) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			::close(fd);
			return std::nullopt;
		}
		if (n == 0) break;
		data.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	return data;
}

bool stat_ok(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

bool lstat_ok(const std::string &path)
{
	struct stat st{};
	return ::lstat(path.c_str(), &st) == 0;
}

int remove_file(const std::string &path)
{
	if (::unlink(path.c_str()) == 0) return 0;
	int e = errno;
	if (::rmdir(path.c_str()) == 0) return 0;
	return errno == ENOTDIR ? e : errno;
}

std::string remove_all(const std::string &path)
{
	struct stat st{};
	if (::lstat(path.c_str(), &st) != 0) return errno == ENOENT ? "" : goerr::path_error("unlinkat", path, errno);
	if (!S_ISDIR(st.st_mode)) {
		if (::unlink(path.c_str()) != 0 && errno != ENOENT) return goerr::path_error("unlinkat", path, errno);
		return "";
	}
	std::string first;
	if (DIR *d = ::opendir(path.c_str())) {
		std::vector<std::string> names;
		while (dirent *e = ::readdir(d)) {
			std::string n = e->d_name;
			if (n != "." && n != "..") names.push_back(n);
		}
		::closedir(d);
		for (const auto &n : names)
			if (auto e = remove_all(path + "/" + n); !e.empty() && first.empty()) first = e;
	} else {
		return goerr::path_error("open", path, errno);
	}
	if (::rmdir(path.c_str()) != 0 && errno != ENOENT && first.empty()) first = goerr::path_error("unlinkat", path, errno);
	return first;
}

// --- randomness -----------------------------------------------------------------------------------------------------

std::string temp_suffix()
{
	std::uint32_t n = 0;
	while (::getrandom(&n, sizeof n, 0) < 0 && errno == EINTR) {
	}
	return std::to_string(n);
}

std::function<bool(unsigned char *, std::size_t)> random_bytes = [](unsigned char *p, std::size_t n) {
	while (n) {
		ssize_t got = ::getrandom(p, n, 0);
		if (got < 0 && errno == EINTR) continue;
		if (got <= 0) return false;
		p += got;
		n -= static_cast<std::size_t>(got);
	}
	return true;
};

std::string hex_encode(std::string_view bytes)
{
	static const char digits[] = "0123456789abcdef";
	std::string out;
	for (unsigned char c : bytes) {
		out += digits[c >> 4];
		out += digits[c & 15];
	}
	return out;
}

// --- internal/toolcache ---------------------------------------------------------------------------------------------

std::pair<std::string, std::string> ToolCache::output(const std::string &path, const std::vector<std::string> &args)
{
	std::string key = path;
	for (const auto &a : args) key += std::string(1, '\0') + a;
	std::string stamp;
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0)
		stamp = std::to_string(st.st_ino) + ":" + std::to_string(st.st_size) + ":" +
		        std::to_string(static_cast<std::int64_t>(st.st_mtim.tv_sec) * 1000000000 + st.st_mtim.tv_nsec);
	std::lock_guard lock(mutex_);
	auto now = std::chrono::steady_clock::now();
	if (auto it = entries_.find(key); it != entries_.end() && !stamp.empty() && it->second.stamp == stamp &&
	                                  (it->second.err.empty() || now - it->second.at < retry_))
		return {it->second.out, it->second.err};
	auto [out, err] = run_(path, args);
	entries_[key] = Entry{stamp, out, err, now};
	return {out, err};
}

bool resolvconf_has(const std::string &key)
{
	Command c;
	c.argv = {"resolvconf", "-i", key};
	c.timeout = std::chrono::seconds(3);
	auto r = run(c);
	if (!r.ok()) return false;
	std::string_view out = r.out;
	std::size_t i = 0;
	while (i < out.size()) {
		while (i < out.size() && std::isspace(static_cast<unsigned char>(out[i]))) i++;
		std::size_t j = i;
		while (j < out.size() && !std::isspace(static_cast<unsigned char>(out[j]))) j++;
		if (j > i && out.substr(i, j - i) == key) return true;
		i = j;
	}
	return false;
}

} // namespace nkos::extensions
