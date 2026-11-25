#include "system/terminal.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "auth/store.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "http/server.hpp"
#include "http/websocket.hpp"
#include "system/serial_terminal.hpp"

#include <glaze/glaze.hpp>
#include <openssl/rand.h>

#include <algorithm>
#include <array>
#include <cerrno>
#include <chrono>
#include <csignal>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <fstream>
#include <map>
#include <memory>
#include <sstream>
#include <sys/ioctl.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <termios.h>
#include <thread>
#include <unistd.h>
#include <vector>

extern char **environ;

namespace nkos::system::terminal {

namespace {

using namespace std::chrono_literals;

constexpr auto detached_timeout = 10min;
constexpr std::size_t replay_size = 64 << 10;
constexpr std::size_t max_shells = 4;
// The sign-ins of the running shells are checked against the account store this often: it can change without the
// server (reset button, edited file).
constexpr auto account_recheck = 5s;
// How long the programs of an ending shell get after SIGHUP before SIGKILL, and how long SIGKILL may take.
constexpr auto hangup_wait = 500ms;
constexpr auto kill_wait = 2s;
// Output waiting for the page beyond this pauses the pty; a page that takes none of it for drain_timeout is
// dropped (the Go server's write deadline) and the shell waits detached.
constexpr std::size_t max_pending_output = 256 << 10;
constexpr auto drain_timeout = 10s;
// Page input waiting for the pty beyond this is dropped.
constexpr std::size_t max_pending_input = 1 << 20;
constexpr std::size_t read_size = 16 << 10;

// WebSocket close codes the terminal page acts on.
constexpr int not_found_code = 4404;    // the requested shell has ended, or is another sign-in's
constexpr int taken_over_code = 4409;   // another page attached to this shell
constexpr int closed_code = 4410;       // closed from the session list
constexpr int ended_code = 4411;        // the shell exited, or nobody attached in time
constexpr int limit_code = 4429;        // max_shells are running

std::int64_t now_ms()
{
	return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::system_clock::now().time_since_epoch())
		.count();
}

// One sign-in generation: the same user is not enough once their account changed or they signed in again after
// a revocation.
struct Account {
	std::string username;
	std::uint64_t token_version = 0;
	bool has_token = false;  // false with authentication disabled: nothing to lose

	bool same(const Account &o) const
	{
		return username == o.username && token_version == o.token_version && has_token == o.has_token;
	}
	// Whether the store still accepts this sign-in and the user is still an admin.
	bool valid() const
	{
		if (!has_token) return true;
		std::string error;
		auto user = auth::store().validate_token(username, token_version, error);
		return user && user->role == "admin";
	}
};

Account account_of(const http::Request &req)
{
	Account a;
	if (req.auth) {
		a.username = req.auth->username;
		a.token_version = req.auth->token_version;
		a.has_token = req.auth->has_token;
	}
	return a;
}

// The last replay_size bytes of a shell's output.
struct ReplayRing {
	std::vector<char> buf = std::vector<char>(replay_size);
	std::size_t start = 0, size = 0;

	void write(const char *data, std::size_t n)
	{
		if (n >= buf.size()) {
			std::memcpy(buf.data(), data + n - buf.size(), buf.size());
			start = 0;
			size = buf.size();
			return;
		}
		std::size_t end = (start + size) % buf.size();
		std::size_t first = std::min(n, buf.size() - end);
		std::memcpy(buf.data() + end, data, first);
		std::memcpy(buf.data(), data + first, n - first);
		size += n;
		if (size > buf.size()) {
			start = (start + size - buf.size()) % buf.size();
			size = buf.size();
		}
	}
	std::string bytes() const
	{
		std::string out;
		out.reserve(size);
		std::size_t first = std::min(size, buf.size() - start);
		out.append(buf.data() + start, first);
		out.append(buf.data(), size - first);
		return out;
	}
};

enum class State { running, closing, stuck, gone };

struct Page;
using Done = std::function<void(const std::string &error)>;

// A login shell on a pty, in its own session (setsid) with the jobs started from it. At most one page is attached.
struct Shell {
	std::string id;
	Account account;
	std::int64_t created = 0;
	pid_t pid = -1;  // also the session id
	int master = -1;
	http::FdWatch watch = 0;

	Page *page = nullptr;
	std::string client_ip, user_agent;  // of the page attached now, or last
	std::int64_t detached_at = 0;
	// Counts detachments and attachments: a timeout belongs to the detachment that started it.
	std::uint64_t detached = 0;
	ReplayRing replay;
	// Output bytes produced, and the offset up to which a page reported its terminal processed them (and sent the
	// answers to the queries in them, in the same message).
	std::uint64_t produced = 0, processed = 0;
	std::string input;
	bool paused = false;
	std::uint64_t pauses = 0;

	State state = State::running;
	std::vector<Done> waiters;  // while closing
};

std::map<std::string, std::shared_ptr<Shell>> shells;
bool watching = false;

void close_shell(const std::shared_ptr<Shell> &s, int code, const std::string &reason, Done done = {});
void detach(const std::shared_ptr<Shell> &s);

std::string new_id()
{
	unsigned char buf[16];
	RAND_bytes(buf, sizeof buf);
	static const char hex[] = "0123456789abcdef";
	std::string out;
	for (unsigned char c : buf) {
		out += hex[c >> 4];
		out += hex[c & 15];
	}
	return out;
}

// The session id of a live (not zombie) process: -1 when it is gone or a zombie, -2 on an unexpected stat.
int process_session(int pid)
{
	std::ifstream f("/proc/" + std::to_string(pid) + "/stat");
	std::string stat((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
	if (stat.empty()) return -1;
	// pid (comm) state ppid pgrp session ...; comm may contain anything.
	auto paren = stat.rfind(')');
	if (paren == std::string::npos) return -2;
	std::istringstream rest(stat.substr(paren + 1));
	std::string state, ppid, pgrp, session;
	if (!(rest >> state >> ppid >> pgrp >> session)) return -2;
	if (state == "Z" || state == "X") return -1;
	return std::atoi(session.c_str());
}

// The live processes of session sid; error set when /proc cannot be read.
std::vector<int> session_processes(int sid, std::string &error)
{
	std::vector<int> pids;
	DIR *dir = ::opendir("/proc");
	if (!dir) {
		error = std::strerror(errno);
		return pids;
	}
	while (dirent *e = ::readdir(dir)) {
		char *end = nullptr;
		long pid = std::strtol(e->d_name, &end, 10);
		if (*e->d_name == 0 || *end != 0) continue;
		int session = process_session(static_cast<int>(pid));
		if (session == -2) {
			error = "unexpected /proc/" + std::string(e->d_name) + "/stat";
			break;
		}
		if (session == sid) pids.push_back(static_cast<int>(pid));
	}
	::closedir(dir);
	return pids;
}

// Sends sig to the live processes of session sid. Each is held by a pidfd from before its session is checked until
// the signal, so a pid that a process frees and another one takes is never signalled.
void signal_session(int sid, int sig, std::string &error)
{
	for (int pid : session_processes(sid, error)) {
		int fd = static_cast<int>(::syscall(SYS_pidfd_open, pid, 0));
		if (fd < 0) continue;  // gone already
		// While the pidfd's process is alive its pid is not reused, so the session read now is its own.
		if (process_session(pid) == sid && ::syscall(SYS_pidfd_send_signal, fd, 0, nullptr, 0) == 0)
			::syscall(SYS_pidfd_send_signal, fd, sig, nullptr, 0);
		::close(fd);
	}
}

// Ends every process of session sid: the shell, its foreground program and its background jobs, each in its own
// process group. They get SIGHUP, as from a hung-up terminal, then SIGKILL. A program that left the session on
// purpose (setsid, a service it started) keeps running. Blocking; empty when nothing is left.
std::string end_session(int sid)
{
	std::string error;
	auto left = [&] {
		auto pids = session_processes(sid, error);
		return pids;
	};
	signal_session(sid, SIGHUP, error);
	if (!error.empty()) return error;
	signal_session(sid, SIGCONT, error);
	error.clear();
	for (auto deadline = std::chrono::steady_clock::now() + hangup_wait; std::chrono::steady_clock::now() < deadline;) {
		std::this_thread::sleep_for(50ms);
		if (left().empty() || !error.empty()) return error;
	}
	for (auto deadline = std::chrono::steady_clock::now() + kill_wait; std::chrono::steady_clock::now() < deadline;) {
		signal_session(sid, SIGKILL, error);
		if (!error.empty()) return error;
		std::this_thread::sleep_for(50ms);
		if (left().empty() || !error.empty()) return error;
	}
	auto pids = left();
	if (!error.empty() || pids.empty()) return error;
	std::string list;
	for (int pid : pids) list += (list.empty() ? "" : " ") + std::to_string(pid);
	return "programs of the terminal session still running: [" + list + "]";
}

// The command in the foreground of the shell's terminal: the shell itself at its prompt.
std::string foreground(const Shell &s)
{
	if (s.master < 0 || s.state != State::running) return "";
	pid_t pgrp = 0;
	if (::ioctl(s.master, TIOCGPGRP, &pgrp) != 0 || pgrp <= 0) return "";
	std::ifstream f("/proc/" + std::to_string(pgrp) + "/comm");
	std::string comm;
	std::getline(f, comm);
	return comm;
}

struct Page : http::WsHandler {
	std::shared_ptr<Shell> shell;  // attached; reset once the page is replaced or the shell ends
	std::string session;           // the shell asked for, or empty for a new one
	Account account;
	std::string client_ip, user_agent;
	http::WebSocket *ws = nullptr;
	std::string pending;

	void opened(http::WebSocket &socket) override;
	void received(http::WebSocket &socket, std::string_view data, bool binary) override;
	bool ready(http::WebSocket &) override { return !pending.empty(); }
	std::optional<http::Message> next(http::WebSocket &) override;
	void closed(http::WebSocket &socket) override;
};

void resume(Shell &s)
{
	if (!s.paused) return;
	s.paused = false;
	if (s.watch) http::pause_fd(s.watch, false);
}

void write_input(Shell &s)
{
	while (!s.input.empty() && s.master >= 0 && s.state == State::running) {
		ssize_t n = ::write(s.master, s.input.data(), s.input.size());
		if (n > 0) {
			s.input.erase(0, static_cast<std::size_t>(n));
			continue;
		}
		if (n < 0 && (errno == EAGAIN || errno == EINTR)) {
			if (s.watch) http::want_writable_fd(s.watch);
			return;
		}
		s.input.clear();  // the terminal is going; its end is seen on the read side
	}
}

void queue_input(Shell &s, std::string_view data)
{
	if (s.input.size() + data.size() > max_pending_input) {
		log::error("terminal session of {}: input dropped, the shell does not read it", s.account.username);
		return;
	}
	bool idle = s.input.empty();
	s.input.append(data);
	if (idle) write_input(s);
}

void read_output(const std::shared_ptr<Shell> &s)
{
	char buf[read_size];
	ssize_t n = ::read(s->master, buf, sizeof buf);
	if (n < 0 && (errno == EAGAIN || errno == EINTR)) return;
	if (n <= 0) {
		// Nothing holds the terminal open any more.
		close_shell(s, ended_code, "terminal session ended", [user = s->account.username](const std::string &error) {
			if (!error.empty()) log::error("terminal session of {}: {}", user, error);
		});
		return;
	}
	if (s->state != State::running) return;
	auto size = static_cast<std::size_t>(n);
	s->replay.write(buf, size);
	s->produced += size;
	Page *page = s->page;
	if (!page || !page->ws) return;
	page->pending.append(buf, size);
	page->ws->wake();
	if (page->pending.size() > max_pending_output && !s->paused) {
		s->paused = true;
		std::uint64_t pause = ++s->pauses;
		http::pause_fd(s->watch, true);
		loop::after(drain_timeout, [weak = std::weak_ptr<Shell>(s), pause] {
			auto s = weak.lock();
			if (!s || !s->paused || s->pauses != pause || !s->page || !s->page->ws) return;
			// The page takes nothing: it is gone without a close.
			s->page->ws->abort();
		});
	}
}

std::shared_ptr<Shell> start_shell(const Account &account, std::string &error)
{
	int master = ::posix_openpt(O_RDWR | O_NOCTTY | O_CLOEXEC | O_NONBLOCK);
	char name[64];
	if (master < 0 || ::grantpt(master) != 0 || ::unlockpt(master) != 0 || ::ptsname_r(master, name, sizeof name) != 0) {
		error = std::strerror(errno);
		if (master >= 0) ::close(master);
		return nullptr;
	}
	winsize size{};
	size.ws_row = 24;
	size.ws_col = 80;
	::ioctl(master, TIOCSWINSZ, &size);

	// Commands typed here, apk included, must not share the server's OOM protection or priority.
	std::vector<std::string> argv{"/bin/sh", "-c",
	                              "echo 0 >/proc/self/oom_score_adj; renice 0 \"$$\" >/dev/null; exec \"$0\" \"$@\"",
	                              "/bin/sh", "-l"};
	std::vector<char *> args;
	for (auto &a : argv) args.push_back(a.data());
	args.push_back(nullptr);
	// The browser's emulator; the installed rootfs has this terminfo.
	std::vector<std::string> extra{"HOME=/root", "TERM=xterm-256color"};
	std::vector<char *> env;
	for (char **e = environ; *e; e++)
		if (std::strncmp(*e, "HOME=", 5) != 0 && std::strncmp(*e, "TERM=", 5) != 0) env.push_back(*e);
	for (auto &e : extra) env.push_back(e.data());
	env.push_back(nullptr);

	pid_t pid = ::fork();
	if (pid < 0) {
		error = std::strerror(errno);
		::close(master);
		return nullptr;
	}
	if (pid == 0) {
		// A session leader without a terminal: the pty it opens becomes its controlling terminal.
		::setsid();
		int slave = ::open(name, O_RDWR);
		if (slave < 0) ::_exit(127);
		::ioctl(slave, TIOCSCTTY, 0);
		::dup2(slave, 0);
		::dup2(slave, 1);
		::dup2(slave, 2);
		if (slave > 2) ::close(slave);
		if (::chdir("/root") != 0) ::_exit(127);
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int sig : {SIGPIPE, SIGTERM, SIGINT, SIGHUP}) ::sigaction(sig, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		::execve(args[0], args.data(), env.data());
		::_exit(127);
	}
	std::ofstream("/proc/" + std::to_string(pid) + "/oom_score_adj") << 0;

	auto s = std::make_shared<Shell>();
	s->id = new_id();
	s->account = account;
	s->created = now_ms();
	s->pid = pid;
	s->master = master;
	std::weak_ptr<Shell> weak = s;
	s->watch = http::watch_fd(master, http::FdCallbacks{
		[weak] {
			if (auto s = weak.lock()) read_output(s);
		},
		[weak] {
			if (auto s = weak.lock()) write_input(*s);
		},
		[weak] {
			// lws closed the terminal on a hangup.
			auto s = weak.lock();
			if (!s) return;
			s->watch = 0;
			s->master = -1;
			close_shell(s, ended_code, "terminal session ended", [user = s->account.username](const std::string &error) {
				if (!error.empty()) log::error("terminal session of {}: {}", user, error);
			});
		},
	});
	if (!s->watch) {
		error = "cannot watch the terminal";
		::close(master);
		s->master = -1;
		// Nothing reads its terminal: end it the same way as any shell.
		shells[s->id] = s;
		close_shell(s, 0, "");
		return nullptr;
	}
	return s;
}

void recheck();

void watch_accounts()
{
	if (watching) return;
	watching = true;
	loop::after(account_recheck, recheck);
}

// Ends the shells whose sign-in the account store no longer accepts, attached or not, and tries again to end
// those whose programs outlived an attempt, until no shell is left.
void recheck()
{
	std::map<std::string, bool> valid;
	std::vector<std::shared_ptr<Shell>> list;
	for (auto &[id, s] : shells) list.push_back(s);
	for (auto &s : list) {
		std::string key = s->account.username + '\n' + std::to_string(s->account.token_version) +
		                  (s->account.has_token ? "t" : "");
		auto it = valid.find(key);
		if (it == valid.end()) it = valid.emplace(key, s->account.valid()).first;
		if (!it->second || s->state == State::stuck)
			close_shell(s, auth::session_revoked_close_code, "session expired or revoked",
			            [user = s->account.username](const std::string &error) {
				            if (!error.empty()) log::error("terminal session of {}: {}", user, error);
			            });
	}
	if (shells.empty()) {
		watching = false;
		return;
	}
	loop::after(account_recheck, recheck);
}

void hello(Page &page, Shell &s)
{
	std::string replay = s.replay.bytes();
	std::uint64_t offset = s.produced - replay.size();
	// The end of the replay that no page processed: its terminal queries are still unanswered, whether it was
	// output while detached or reached a page that went away before its terminal got to it.
	std::uint64_t pending = std::min<std::uint64_t>(s.produced - s.processed, replay.size());
	// Keys as the Go server's map (sorted).
	std::string json = "{\"offset\":" + std::to_string(offset) + ",\"pending\":" + std::to_string(pending) +
	                   ",\"replay\":" + std::to_string(replay.size()) + ",\"session\":\"" + s.id + "\"}";
	page.ws->send(http::make_message(json, false));
	if (!replay.empty()) page.ws->send(http::make_message(replay));
}

void Page::opened(http::WebSocket &socket)
{
	ws = &socket;
	std::shared_ptr<Shell> s;
	if (!session.empty()) {
		auto it = shells.find(session);
		// Only the sign-in that started a shell attaches to it.
		if (it == shells.end() || !it->second->account.same(account) || it->second->state != State::running)
			return ws->close(not_found_code, "terminal session not found");
		s = it->second;
	} else {
		if (shells.size() >= max_shells) return ws->close(limit_code, "too many terminal sessions");
		std::string error;
		s = start_shell(account, error);
		if (!s) {
			ws->send(http::make_message("Unable to start terminal session.\r\n"));
			ws->close(1000, "");
			log::error("terminal session failed: {}", error);
			return;
		}
		shells[s->id] = s;
		watch_accounts();
	}

	Page *previous = s->page;
	if (previous) {
		previous->shell.reset();
		previous->pending.clear();
		if (previous->ws) previous->ws->close(taken_over_code, "terminal session opened elsewhere");
	}
	shell = s;
	s->page = this;
	s->client_ip = client_ip;
	s->user_agent = user_agent;
	s->detached++;
	resume(*s);
	hello(*this, *s);
}

// pageMessage of the Go server: the window size, the output offset the page's terminal processed, or the
// terminal's answers to the queries in a chunk of output together with the offset of its end.
struct PageMessage {
	std::uint16_t rows = 0, cols = 0;
	std::optional<std::string> data;
	std::optional<std::uint64_t> ack;
};
constexpr glz::opts lenient{.error_on_unknown_keys = false};

void Page::received(http::WebSocket &, std::string_view data, bool binary)
{
	if (!shell || shell->page != this || shell->state != State::running) return;
	Shell &s = *shell;
	// Keyboard input comes as text; mouse reports in the X10 encoding, which are not text, as binary starting
	// with ESC.
	if (!binary || (!data.empty() && data[0] == 0x1b)) return queue_input(s, data);
	PageMessage message;
	if (glz::read<lenient>(message, std::string(data))) return;
	// Answers reach the shell together with the end of the output they answer: those of a page already replaced
	// were dropped above, and the next page's hello counts what was recorded here.
	if (message.ack) {
		if (*message.ack > s.processed && *message.ack <= s.produced) s.processed = *message.ack;
		if (message.data) queue_input(s, *message.data);
	}
	if (message.rows > 0 && message.cols > 0 && s.master >= 0) {
		winsize size{};
		size.ws_row = message.rows;
		size.ws_col = message.cols;
		::ioctl(s.master, TIOCSWINSZ, &size);
	}
}

std::optional<http::Message> Page::next(http::WebSocket &)
{
	if (pending.empty()) return std::nullopt;
	auto m = http::make_message(pending);
	pending.clear();
	if (shell && shell->page == this) resume(*shell);
	return m;
}

void Page::closed(http::WebSocket &socket)
{
	ws = nullptr;
	auto s = std::move(shell);
	shell.reset();
	if (!s || s->page != this) return;
	// The server closed it because the sign-in was revoked or expired: end the shell rather than keep it for the
	// next page.
	if (socket.closing_ && socket.close_code_ == auth::session_revoked_close_code) {
		s->page = nullptr;
		close_shell(s, 0, "", [user = s->account.username](const std::string &error) {
			if (!error.empty()) log::error("terminal session of {}: {}", user, error);
		});
		return;
	}
	detach(s);
}

// The shell waits without a page, and ends if none attaches in time.
void detach(const std::shared_ptr<Shell> &s)
{
	if (s->state != State::running) return;
	s->page = nullptr;
	s->detached_at = now_ms();
	std::uint64_t detachment = ++s->detached;
	resume(*s);
	loop::after(detached_timeout, [weak = std::weak_ptr<Shell>(s), detachment] {
		auto s = weak.lock();
		if (!s || s->state != State::running || s->page || s->detached != detachment) return;
		close_shell(s, ended_code, "terminal session ended", [user = s->account.username](const std::string &error) {
			if (!error.empty()) log::error("terminal session of {}: {}", user, error);
		});
	});
}

void finish(const std::shared_ptr<Shell> &s, const std::string &error, bool first)
{
	if (first && s->watch) {
		http::unwatch_fd(s->watch);
		s->watch = 0;
		s->master = -1;
	} else if (first && s->master >= 0) {
		::close(s->master);
		s->master = -1;
	}
	s->state = error.empty() ? State::gone : State::stuck;
	if (error.empty()) {
		shells.erase(s->id);
		log::debug("terminal session of {} ended", s->account.username);
	}
	auto waiters = std::move(s->waiters);
	s->waiters.clear();
	for (auto &w : waiters) w(error);
}

// Ends the shell and every program of its session. A non-zero code tells the attached page why. done gets the
// result: empty once the programs are gone, or the error naming those still running; the shell then stays listed
// and the next close (End again, or the recheck) tries again. Callers during an attempt share it.
void close_shell(const std::shared_ptr<Shell> &s, int code, const std::string &reason, Done done)
{
	if (s->state == State::gone) {
		if (done) done("");
		return;
	}
	if (done) s->waiters.push_back(std::move(done));
	if (s->state == State::closing) return;
	bool first = s->state == State::running;
	s->state = State::closing;
	if (Page *page = s->page) {
		s->page = nullptr;
		page->shell.reset();
		page->pending.clear();
		if (page->ws) {
			if (code) page->ws->close(code, reason);
			else page->ws->abort();
		}
	}
	if (first && s->watch) {
		s->paused = true;
		http::pause_fd(s->watch, true);
	}
	// The shell stays unreaped until no program of its session is left: its pid is the session id, and while it
	// is a zombie no new process or session can take that number, so a later attempt still signals this session.
	// Once an attempt succeeded the shell has exited (it is in the session) and waitpid returns at once.
	pid_t sid = s->pid;
	loop::blocking([s, sid, first] {
		std::string error = end_session(sid);
		if (error.empty()) {
			int status = 0;
			while (::waitpid(sid, &status, 0) < 0 && errno == EINTR) {
			}
		}
		loop::post([s, error, first] { finish(s, error, first); });
	});
}

// Ends the shells of a user whose sessions were revoked through the server (sign-out, password change, account
// removed or changed).
void close_user(const std::string &username)
{
	std::vector<std::shared_ptr<Shell>> list;
	for (auto &[id, s] : shells)
		if (s->account.username == username) list.push_back(s);
	for (auto &s : list)
		close_shell(s, auth::session_revoked_close_code, "session expired or revoked", [username](const std::string &error) {
			if (!error.empty()) log::error("terminal session of {}: {}", username, error);
		});
}

struct SessionInfo {
	std::string id;
	std::string username;
	// The command in the foreground: the shell itself at its prompt.
	std::string command;
	std::int64_t createdAt = 0;
	bool attached = false;
	// Without a page: when it lost it, and when it ends unless one attaches.
	std::optional<std::int64_t> detachedAt;
	std::optional<std::int64_t> endsAt;
	// The page attached now, or last.
	std::string clientIp;
	std::string userAgent;
	// The requesting user's own shell: only these can be opened.
	bool own = false;
};
struct SessionsRsp {
	std::vector<SessionInfo> sessions;
	int max = static_cast<int>(max_shells);
};

bool has_query_key(const std::string &query, std::string_view key)
{
	std::size_t pos = 0;
	while (pos <= query.size()) {
		std::size_t end = query.find('&', pos);
		if (end == std::string::npos) end = query.size();
		std::string_view part(query.data() + pos, end - pos);
		if (part.substr(0, part.find('=')) == key) return true;
		pos = end + 1;
	}
	return false;
}

} // namespace

void add_routes(http::Router &router)
{
	static bool hooked = false;
	if (!hooked) {
		hooked = true;
		auth::on_user_revoked(close_user);
	}
	router.ws("/api/vm/terminal", auth::guarded_ws([](http::Request &req, http::Response &res) -> std::shared_ptr<http::WsHandler> {
		if (has_query_key(req.query, "port")) {
			std::string error;
			auto settings = serial_terminal::settings_from_query(req, error);
			if (!settings) {
				res.text(400, error);
				return nullptr;
			}
			return serial_terminal::make_page(std::move(*settings));
		}
		auto page = std::make_shared<Page>();
		page->session = req.query_value("session");
		page->account = account_of(req);
		page->client_ip = auth::client_address(req);
		page->user_agent = req.header("user-agent");
		return page;
	}, auth::Guard{{"admin"}, true}));

	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/terminal/sessions", auth::guarded([](http::Request &req, http::Response &res) {
		Account me = account_of(req);
		std::vector<std::shared_ptr<Shell>> list;
		for (auto &[id, s] : shells) list.push_back(s);
		std::sort(list.begin(), list.end(), [](auto &a, auto &b) { return a->created < b->created; });
		SessionsRsp rsp;
		for (auto &s : list) {
			SessionInfo info;
			info.id = s->id;
			info.username = s->account.username;
			info.command = foreground(*s);
			info.createdAt = s->created;
			info.own = s->account.same(me);
			info.attached = s->page != nullptr;
			info.clientIp = s->client_ip;
			info.userAgent = s->user_agent;
			if (!info.attached) {
				info.detachedAt = s->detached_at;
				info.endsAt = s->detached_at + std::chrono::duration_cast<std::chrono::milliseconds>(detached_timeout).count();
			}
			rsp.sessions.push_back(std::move(info));
		}
		api::ok(res, rsp);
	}, admin));
	router.add("DELETE", "/api/vm/terminal/sessions/:id", auth::guarded([](http::Request &req, http::Response &res) {
		auto it = shells.find(req.param("id"));
		if (it == shells.end()) return api::err(res, -1, "terminal session not found");
		res.pending = true;
		close_shell(it->second, closed_code, "terminal session closed",
		            [id = req.id, user = it->second->account.username](const std::string &error) {
			            http::Response out;
			            if (!error.empty()) {
				            log::error("terminal session of {}: {}", user, error);
				            api::err(out, -1, "programs of the terminal session are still running");
			            } else {
				            api::ok(out);
			            }
			            http::complete(id, std::move(out));
		            });
	}, admin));
}

} // namespace nkos::system::terminal
