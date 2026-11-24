#include "system/scripts.hpp"
#include "osupdate/apkpkg.hpp"
#include "osupdate/osupdate.hpp"

#include "api/multipart.hpp"
#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "common/tz.hpp"
#include "config/config.hpp"
#include "system/cron.hpp"

#include <glaze/glaze.hpp>
#include <openssl/rand.h>
#include <openssl/sha.h>

#include <algorithm>
#include <cerrno>
#include <chrono>
#include <csignal>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <fstream>
#include <functional>
#include <map>
#include <mutex>
#include <poll.h>
#include <regex>
#include <set>
#include <sstream>
#include <sys/stat.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>
#include <vector>

extern char **environ;

namespace nkos::system::scripts {

namespace {

using namespace std::chrono_literals;

const std::string script_directory = "/etc/kvm/scripts";
const std::string python_interpreter = "/usr/bin/python3";
const std::string autostart_list = "/etc/kvm/autostart.list";
const std::string autostart_run_dir = "/run/nanokvm/autostart";
constexpr std::size_t output_limit = 64 << 10;  // a Run's and a startup run's output
constexpr auto run_limit = 50s;                  // a Run answers before the browser stops waiting (60 s)
constexpr int code_python_missing = -3;
const char *python_missing = "Python runtime is not installed";
// A process left in the background may hold the output open: Run waits this long after the script exited.
constexpr auto wait_delay = 1s;

const std::string oom_reset = "echo 0 >/proc/self/oom_score_adj; renice 0 \"$$\" >/dev/null; ";

std::mutex autostart_mutex;

std::string read_file(const std::string &path, bool &ok)
{
	std::ifstream f(path, std::ios::binary);
	ok = static_cast<bool>(f);
	if (!f) return "";
	return std::string((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
}

bool lower_suffix(const std::string &name, const char *suffix)
{
	std::size_t n = std::strlen(suffix);
	if (name.size() < n) return false;
	for (std::size_t i = 0; i < n; i++)
		if (std::tolower(static_cast<unsigned char>(name[name.size() - n + i])) != suffix[i]) return false;
	return true;
}

bool is_script(const std::string &name) { return lower_suffix(name, ".sh") || lower_suffix(name, ".py"); }
bool is_python(const std::string &path)
{
	// filepath.Ext: the suffix from the last dot of the last element.
	auto slash = path.rfind('/');
	auto dot = path.rfind('.');
	if (dot == std::string::npos || (slash != std::string::npos && dot < slash)) return false;
	return lower_suffix(path.substr(dot), ".py");
}

// filepath.Clean(name) == name for a relative name: no empty, "." or ".." elements, no trailing slash.
bool clean_relative(const std::string &name)
{
	if (name.empty() || name.front() == '/') return false;
	std::size_t start = 0;
	while (true) {
		auto end = name.find('/', start);
		std::string part = name.substr(start, end == std::string::npos ? std::string::npos : end - start);
		if (part.empty() || part == "." || part == "..") return false;
		if (end == std::string::npos) return true;
		start = end + 1;
	}
}

bool valid_name(const std::string &name, bool nested)
{
	if (!clean_relative(name) || !is_script(name)) return false;
	if (name.find('\\') != std::string::npos || (!nested && name.find('/') != std::string::npos)) return false;
	for (unsigned char c : name)
		if (c < 0x20 || c == 0x7f) return false;
	return true;
}

// The script directory itself: a real directory. errno-like: 0 ok, ENOENT missing, EINVAL not a directory.
int root_state(const std::string &root)
{
	struct stat st{};
	if (::lstat(root.c_str(), &st) != 0) return errno;
	if (S_ISLNK(st.st_mode) || !S_ISDIR(st.st_mode)) return EINVAL;
	return 0;
}

// A regular file below root, reached without symlinks at any component.
std::string resolve(const std::string &root, const std::string &name)
{
	if (!valid_name(name, true) || root_state(root) != 0) return "";
	std::string current = root;
	std::size_t start = 0;
	while (true) {
		auto end = name.find('/', start);
		bool last = end == std::string::npos;
		current += "/" + name.substr(start, last ? std::string::npos : end - start);
		struct stat st{};
		if (::lstat(current.c_str(), &st) != 0 || S_ISLNK(st.st_mode)) return "";
		if (!last && !S_ISDIR(st.st_mode)) return "";
		if (last) return S_ISREG(st.st_mode) ? current : "";
		start = end + 1;
	}
}

// The scripts below root, sorted; nullopt when the walk fails.
std::optional<std::vector<std::string>> list_scripts(const std::string &root)
{
	int state = root_state(root);
	if (state == ENOENT) return std::vector<std::string>{};
	if (state != 0) return std::nullopt;
	std::vector<std::string> files;
	std::function<bool(const std::string &, const std::string &)> walk = [&](const std::string &dir,
	                                                                          const std::string &prefix) {
		DIR *d = ::opendir(dir.c_str());
		if (!d) return false;
		std::vector<std::string> names;
		while (dirent *e = ::readdir(d))
			if (std::strcmp(e->d_name, ".") != 0 && std::strcmp(e->d_name, "..") != 0) names.push_back(e->d_name);
		::closedir(d);
		std::sort(names.begin(), names.end());
		for (const auto &n : names) {
			std::string path = dir + "/" + n;
			struct stat st{};
			if (::lstat(path.c_str(), &st) != 0) return false;
			if (S_ISLNK(st.st_mode)) continue;
			std::string relative = prefix.empty() ? n : prefix + "/" + n;
			if (S_ISDIR(st.st_mode)) {
				if (!walk(path, relative)) return false;
			} else if (S_ISREG(st.st_mode) && is_script(relative)) {
				files.push_back(relative);
			}
		}
		return true;
	};
	if (!walk(root, "")) return std::nullopt;
	std::sort(files.begin(), files.end());
	return files;
}

bool mkdir_all(const std::string &path, mode_t mode)
{
	std::size_t pos = 0;
	while ((pos = path.find('/', pos + 1)) != std::string::npos) ::mkdir(path.substr(0, pos).c_str(), mode);
	return ::mkdir(path.c_str(), mode) == 0 || errno == EEXIST;
}

bool save_script(const std::string &root, const std::string &name, std::string_view data)
{
	if (!valid_name(name, false) || !mkdir_all(root, 0755) || root_state(root) != 0) return false;
	std::string tmp = root + "/.script-upload-XXXXXX";
	int fd = ::mkstemp(tmp.data());
	if (fd < 0) return false;
	bool ok = ::fchmod(fd, 0700) == 0;
	for (std::size_t off = 0; ok && off < data.size();) {
		ssize_t n = ::write(fd, data.data() + off, data.size() - off);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) ok = false;
		else off += static_cast<std::size_t>(n);
	}
	ok = ok && ::fsync(fd) == 0;
	ok = ::close(fd) == 0 && ok;
	ok = ok && ::rename(tmp.c_str(), (root + "/" + name).c_str()) == 0;
	if (!ok) ::unlink(tmp.c_str());
	return ok;
}

bool executable(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode) && ::access(path.c_str(), X_OK) == 0;
}

// The program and arguments of a script: a .py one through Python. error: python_missing or another message.
std::vector<std::string> script_command(const std::string &path, std::string &error)
{
	if (is_python(path)) {
		if (!executable(python_interpreter)) {
			error = python_missing;
			return {};
		}
		return {python_interpreter, path};
	}
	if (!executable(path)) {
		error = "exec: \"" + path + "\": permission denied";
		return {};
	}
	return {path};
}

// argv as oomscore.Unprotected: commands started here do not keep the server's OOM protection or priority.
std::vector<std::string> unprotected(const std::vector<std::string> &command)
{
	std::vector<std::string> argv{"/bin/sh", "-c", oom_reset + "exec \"$0\" \"$@\""};
	argv.insert(argv.end(), command.begin(), command.end());
	return argv;
}

struct Child {
	pid_t pid = -1;
	int output = -1;  // read end of stdout+stderr, or -1
};

// fork + exec of argv. session: a session of its own (setsid), else a process group of its own when group.
// capture: stdout and stderr to a pipe; otherwise to /dev/null.
Child spawn(const std::vector<std::string> &argv, const std::string &dir, bool session, bool group, bool capture)
{
	std::vector<char *> args;
	for (auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	int out[2] = {-1, -1};
	if (capture && ::pipe2(out, O_CLOEXEC) != 0) return {};
	pid_t pid = ::fork();
	if (pid < 0) {
		if (capture) {
			::close(out[0]);
			::close(out[1]);
		}
		return {};
	}
	if (pid == 0) {
		if (session) ::setsid();
		else if (group) ::setpgid(0, 0);
		int null = ::open("/dev/null", O_RDWR);
		::dup2(null, 0);
		::dup2(capture ? out[1] : null, 1);
		::dup2(capture ? out[1] : null, 2);
		if (!dir.empty() && ::chdir(dir.c_str()) != 0) ::_exit(127);
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int s : {SIGPIPE, SIGTERM, SIGINT, SIGHUP}) ::sigaction(s, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		::execve(args[0], args.data(), environ);
		::_exit(127);
	}
	if (capture) ::close(out[1]);
	return {pid, capture ? out[0] : -1};
}

// Reaps a child that runs on after its start (a background script, a startup script, a task run now).
void reap_later(pid_t pid)
{
	std::thread([pid] {
		int status = 0;
		while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
		}
	}).detach();
}

// A Run: the script's output (stdout and stderr together, the first output_limit bytes; the rest is read and
// dropped), killed with what it started (its process group) at run_limit. The output ends with a note when it was
// cut, the script was stopped, or it exited with a code other than 0. Blocking.
std::optional<std::string> run_foreground(const std::vector<std::string> &argv, std::string &error)
{
	Child child = spawn(argv, "", false, true, true);
	if (child.pid < 0) {
		error = std::strerror(errno);
		return std::nullopt;
	}
	::fcntl(child.output, F_SETFL, O_NONBLOCK);
	std::string output;
	bool cut = false, stopped = false, exited = false, open = true;
	int status = 0;
	auto start = std::chrono::steady_clock::now();
	std::chrono::steady_clock::time_point exited_at{};
	while (open) {
		auto now = std::chrono::steady_clock::now();
		if (!exited) {
			pid_t r = ::waitpid(child.pid, &status, WNOHANG);
			if (r == child.pid) {
				exited = true;
				exited_at = now;
			}
		}
		if (!exited && !stopped && now - start >= run_limit) {
			stopped = true;
			::kill(-child.pid, SIGKILL);
		}
		if (exited && now - exited_at >= wait_delay) break;
		pollfd p{child.output, POLLIN, 0};
		::poll(&p, 1, 100);
		char buf[16384];
		while (true) {
			ssize_t n = ::read(child.output, buf, sizeof buf);
			if (n > 0) {
				std::size_t room = output.size() < output_limit ? output_limit - output.size() : 0;
				if (static_cast<std::size_t>(n) > room) cut = true;
				output.append(buf, std::min<std::size_t>(room, static_cast<std::size_t>(n)));
				continue;
			}
			if (n == 0) open = false;
			break;
		}
	}
	::close(child.output);
	while (!exited) {
		pid_t r = ::waitpid(child.pid, &status, WNOHANG);
		if (r == child.pid || (r < 0 && errno != EINTR)) break;
		if (!stopped && std::chrono::steady_clock::now() - start >= run_limit) {
			stopped = true;
			::kill(-child.pid, SIGKILL);
		}
		std::this_thread::sleep_for(std::chrono::milliseconds(100));
	}
	if (cut) output += "\n[output cut after " + std::to_string(output_limit >> 10) + " KiB]";
	if (stopped) {
		output += "\n[stopped after " + std::to_string(run_limit.count()) + " s; use Run in background for longer scripts]";
	} else {
		int code = WIFEXITED(status) ? WEXITSTATUS(status) : -1;
		if (code != 0) output += "\n[exit code " + std::to_string(code) + "]";
	}
	return output;
}

// The scripts marked to run at startup.
std::optional<std::vector<std::string>> read_autostart()
{
	bool ok = false;
	std::string data = read_file(autostart_list, ok);
	std::vector<std::string> names;
	if (!ok) {
		if (errno == ENOENT) return names;
		return std::nullopt;
	}
	std::istringstream in(data);
	for (std::string line; std::getline(in, line);)
		if (valid_name(line, true)) names.push_back(line);
	return names;
}

bool write_autostart(std::vector<std::string> names)
{
	std::sort(names.begin(), names.end());
	names.erase(std::unique(names.begin(), names.end()), names.end());
	if (names.empty()) return ::unlink(autostart_list.c_str()) == 0 || errno == ENOENT;
	std::string data;
	for (auto &n : names) data += n + "\n";
	return config::atomic_write(autostart_list, data, 0644);
}

bool set_autostart(const std::string &name, bool enabled)
{
	std::lock_guard lock(autostart_mutex);
	if (enabled ? resolve(script_directory, name).empty() : !valid_name(name, true)) return false;
	auto names = read_autostart();
	if (!names) return false;
	std::vector<std::string> kept;
	for (auto &n : *names)
		if (n != name) kept.push_back(n);
	if (enabled) kept.push_back(name);
	return write_autostart(kept);
}

// The log and status files of a startup script: a digest of its name.
std::string autostart_base(const std::string &name)
{
	unsigned char digest[SHA256_DIGEST_LENGTH];
	SHA256(reinterpret_cast<const unsigned char *>(name.data()), name.size(), digest);
	static const char hex[] = "0123456789abcdef";
	std::string out = autostart_run_dir + "/";
	for (unsigned char c : digest) {
		out += hex[c >> 4];
		out += hex[c & 15];
	}
	return out;
}

std::string trim(std::string s)
{
	auto space = [](unsigned char c) { return std::isspace(c) != 0; };
	s.erase(s.begin(), std::find_if_not(s.begin(), s.end(), space));
	s.erase(std::find_if_not(s.rbegin(), s.rend(), space).base(), s.end());
	return s;
}

std::map<std::string, std::string> autostart_status(const std::vector<std::string> &names)
{
	std::map<std::string, std::string> status;
	for (auto &n : names) {
		bool ok = false;
		std::string data = read_file(autostart_base(n) + ".status", ok);
		if (ok) status[n] = trim(data);
	}
	return status;
}

void write_small(const std::string &path, const std::string &data)
{
	int fd = ::open(path.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
	if (fd < 0) return;
	(void)!::write(fd, data.data(), data.size());
	::close(fd);
}

std::string start_one(const std::string &name)
{
	std::string path = resolve(script_directory, name);
	if (path.empty()) return "invalid script";
	std::string error;
	auto command = script_command(path, error);
	if (command.empty()) return error;
	std::string base = autostart_base(name);
	write_small(base + ".status", "running\n");
	// $1 status file, $2 log file, then the command; output beyond the limit is read and dropped.
	std::string wrapper = oom_reset + "status=$1; log=$2; shift 2\n( \"$@\"; echo \"exited $?\" > \"$status\" ) 2>&1 | { head -c " +
	                      std::to_string(output_limit) + " > \"$log\"; cat > /dev/null; }";
	std::vector<std::string> argv{"/bin/sh", "-c", wrapper, "nanokvm-autostart", base + ".status", base + ".log"};
	argv.insert(argv.end(), command.begin(), command.end());
	auto slash = path.rfind('/');
	Child child = spawn(argv, path.substr(0, slash), true, false, false);
	if (child.pid < 0) return std::strerror(errno);
	reap_later(child.pid);
	return "";
}

struct ScriptsRsp {
	std::vector<std::string> files;
	std::vector<std::string> autostart;
	std::map<std::string, std::string> autostartStatus;
};
struct UploadRsp {
	std::string file;
	std::optional<bool> pythonMissing;
};
struct RunRsp {
	std::string log;
};

std::string param_string(const http::Request &req, const char *name, bool &valid)
{
	auto params = api::Params::bind(req);
	bool ok = true;
	std::optional<std::string> v;
	if (params) v = params->string(name, ok);
	valid = params && ok;
	return v.value_or("");
}

// ------------------------------------------------------------------ scheduled tasks

const std::string task_marker = "# nanokvm-task";
const std::string task_runner = "/usr/libexec/nanokvm/run-task";
const std::string task_run_dir = "/run/nanokvm/tasks";
const std::string crontab_path = "/etc/crontabs/root";
// Shared with the APK index refresh schedule (osupdate): one lock for every rewrite of root's crontab.
std::mutex &crontab_mutex = osupdate::crontab_mutex();

bool valid_task_id(const std::string &id)
{
	if (id.size() != 9 || id[0] != 't') return false;
	for (std::size_t i = 1; i < id.size(); i++)
		if (!std::isdigit(static_cast<unsigned char>(id[i])) && (id[i] < 'a' || id[i] > 'f')) return false;
	return true;
}

struct Task {
	std::string id;
	std::string name;
	bool enabled = false;
	// Five cron fields, in the device's time zone.
	std::string schedule;
	// "script": target is a script of the Scripts window; "command": a shell command line.
	std::string kind;
	std::string target;
};

struct TaskStatus {
	std::string id;
	std::string name;
	bool enabled = false;
	std::string schedule;
	std::string kind;
	std::string target;
	std::vector<std::int64_t> next;
	// running, exited N, or absent when it has not run since the boot.
	std::optional<std::string> state;
	std::optional<std::int64_t> started;
	std::optional<std::int64_t> ended;
};

std::string task_json(const Task &t)
{
	return "{\"id\":" + gojson::quote(t.id) + ",\"name\":" + gojson::quote(t.name) +
	       ",\"enabled\":" + (t.enabled ? "true" : "false") + ",\"schedule\":" + gojson::quote(t.schedule) +
	       ",\"kind\":" + gojson::quote(t.kind) + ",\"target\":" + gojson::quote(t.target) + "}";
}

std::string fields_joined(const std::string &s)
{
	std::istringstream in(s);
	std::string out;
	for (std::string f; in >> f;) out += (out.empty() ? "" : " ") + f;
	return out;
}

// Characters Go's strings.TrimSpace and unicode.IsControl see, for the ASCII range and the C1 controls.
std::size_t utf8_runes(const std::string &s)
{
	std::size_t n = 0;
	for (unsigned char c : s)
		if ((c & 0xc0) != 0x80) n++;
	return n;
}
bool has_control(const std::string &s)
{
	for (std::size_t i = 0; i < s.size(); i++) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x20 || c == 0x7f) return true;
		if (c == 0xc2 && i + 1 < s.size()) {
			unsigned char d = static_cast<unsigned char>(s[i + 1]);
			if (d >= 0x80 && d <= 0x9f) return true;
		}
	}
	return false;
}

std::optional<std::string> validate_task(const Task &t)
{
	if (!valid_task_id(t.id)) return "invalid task";
	std::string name = trim(t.name);
	if (name.empty() || utf8_runes(name) > 64 || has_control(name)) return "use a name of 1-64 characters";
	std::string error;
	if (!cron::parse(t.schedule, error)) return error;
	if (t.kind == "script") {
		if (resolve(script_directory, t.target).empty()) return "choose a script of the Scripts window";
	} else if (t.kind == "command") {
		if (trim(t.target).empty() || t.target.size() > 1024 || t.target.find_first_of(std::string("\r\n\0", 3)) != std::string::npos)
			return "use a command of one line, up to 1024 bytes";
	} else {
		return "a task runs a script or a command";
	}
	return std::nullopt;
}

std::string shell_quote(const std::string &s)
{
	std::string out = "'";
	for (char c : s) {
		if (c == '\'') out += "'\\''";
		else out += c;
	}
	return out + "'";
}

// What run-task runs: the working directory, then the program.
std::vector<std::string> task_args(const Task &t)
{
	if (t.kind == "command") return {"/root", "/bin/sh", "-c", t.target};
	std::string path = script_directory + "/" + t.target;
	std::string dir = path.substr(0, path.rfind('/'));
	if (is_python(path)) return {dir, python_interpreter, path};
	return {dir, path};
}

std::string task_job_line(const Task &t)
{
	std::string line = fields_joined(t.schedule) + " " + task_runner + " " + t.id;
	for (auto &a : task_args(t)) line += " " + shell_quote(a);
	return line + " " + task_marker + " " + t.id;
}

// Splits a crontab into our tasks and the other lines.
void parse_tasks(const std::string &data, std::vector<Task> &tasks, std::vector<std::string> &other)
{
	std::string text = data;
	if (!text.empty() && text.back() == '\n') text.pop_back();
	std::size_t start = 0;
	constexpr glz::opts lenient{.error_on_unknown_keys = false};
	while (true) {
		auto end = text.find('\n', start);
		std::string line = text.substr(start, end == std::string::npos ? std::string::npos : end - start);
		bool taken = false;
		std::string prefix = task_marker + " {";
		if (line.rfind(prefix, 0) == 0) {
			Task t;
			std::string json = line.substr(task_marker.size() + 1);
			if (!glz::read<lenient>(t, json) && valid_task_id(t.id)) {
				tasks.push_back(t);
				taken = true;
			}
		}
		if (!taken) {
			std::string mark = " " + task_marker + " ";
			auto i = line.rfind(mark);
			if (i != std::string::npos && valid_task_id(line.substr(i + mark.size()))) taken = true;  // a job line
		}
		if (!taken) other.push_back(line);
		if (end == std::string::npos) break;
		start = end + 1;
	}
	if (other.size() == 1 && other[0].empty()) other.clear();
}

std::string render_tasks(const std::vector<Task> &tasks, const std::vector<std::string> &other)
{
	std::vector<std::string> lines = other;
	for (auto &t : tasks) {
		lines.push_back(task_marker + " " + task_json(t));
		if (t.enabled) lines.push_back(task_job_line(t));
	}
	std::string out;
	for (std::size_t i = 0; i < lines.size(); i++) out += (i ? "\n" : "") + lines[i];
	return out + "\n";
}

std::optional<std::string> read_crontab(std::string &error)
{
	bool ok = false;
	std::string data = read_file(crontab_path, ok);
	if (!ok && errno != ENOENT) {
		error = std::strerror(errno);
		return std::nullopt;
	}
	return data;
}

bool crond_running()
{
	return process::run({"rc-service", "crond", "status"}).ok();
}

std::optional<std::string> ensure_crond()
{
	if (!process::run({"rc-update", "add", "crond", "default"}).ok()) return "enable cron: failed";
	if (!crond_running() && !process::run({"rc-service", "crond", "start"}).ok()) return "start cron: failed";
	return std::nullopt;
}

// Changes the task list in the crontab under its lock; crond is enabled and started when a task is enabled.
std::optional<std::string> edit_tasks(const std::function<std::optional<std::string>(std::vector<Task> &)> &change)
{
	std::lock_guard lock(crontab_mutex);
	std::string error;
	auto data = read_crontab(error);
	if (!data) return error;
	std::vector<Task> tasks;
	std::vector<std::string> other;
	parse_tasks(*data, tasks, other);
	if (auto e = change(tasks)) return e;
	for (auto &t : tasks) {
		if (t.enabled) {
			if (auto e = ensure_crond()) return e;
			break;
		}
	}
	process::Options options;
	options.input = render_tasks(tasks, other);
	auto r = process::run({"crontab", "-"}, options);
	if (!r.ok()) return "crontab: " + (r.started ? "exit status " + std::to_string(r.exit_code) : r.error) + ": " + trim(r.output);
	return std::nullopt;
}

void read_task_state(const std::string &id, TaskStatus &st)
{
	bool ok = false;
	std::string data = read_file(task_run_dir + "/" + id + ".status", ok);
	if (!ok) return;
	// "running START" or "exited CODE START END"
	std::istringstream in(data);
	std::vector<std::string> f;
	for (std::string w; in >> w;) f.push_back(w);
	auto num = [](const std::string &s) {
		try {
			std::size_t used = 0;
			long long v = std::stoll(s, &used);
			return used == s.size() ? v : 0LL;
		} catch (...) {
			return 0LL;
		}
	};
	if (f.size() >= 2 && f[0] == "running") {
		st.state = "running";
		st.started = num(f[1]);
		if (!*st.started) st.started.reset();
	} else if (f.size() == 4 && f[0] == "exited") {
		st.state = "exited " + f[1];
		st.started = num(f[2]);
		st.ended = num(f[3]);
		if (!*st.started) st.started.reset();
		if (!*st.ended) st.ended.reset();
	}
}

std::int64_t now_unix()
{
	return std::chrono::duration_cast<std::chrono::seconds>(std::chrono::system_clock::now().time_since_epoch()).count();
}

// gin.H: the keys in sorted order.
struct TasksRsp {
	bool crond = false;
	std::vector<std::string> other;
	std::vector<TaskStatus> tasks;
};
struct IdRsp {
	std::string id;
};
struct NextRsp {
	std::vector<std::int64_t> next;
};
struct LogRsp {
	std::string log;
};

struct IdReq {
	std::string id;
};
struct PreviewReq {
	std::string schedule;
};

std::optional<std::string> body_id(const http::Request &req)
{
	IdReq r;
	constexpr glz::opts lenient{.error_on_unknown_keys = false};
	if (glz::read<lenient>(r, req.body) || !valid_task_id(r.id)) return std::nullopt;
	return r.id;
}

} // namespace

std::string script_path(const std::string &name) { return resolve(script_directory, name); }

void start_autostart()
{
	loop::blocking([] {
		if (!mkdir_all(autostart_run_dir, 0700)) {
			log::error("autostart: {}", std::strerror(errno));
			return;
		}
		int marker = ::open((autostart_run_dir + "/.started").c_str(), O_CREAT | O_EXCL | O_WRONLY | O_CLOEXEC, 0600);
		if (marker < 0) {
			if (errno != EEXIST) log::error("autostart: {}", std::strerror(errno));
			return;
		}
		::close(marker);
		auto names = read_autostart();
		if (!names) {
			log::error("autostart: {}", std::strerror(errno));
			return;
		}
		for (auto &name : *names) {
			std::string error = start_one(name);
			if (!error.empty()) {
				log::error("autostart {}: {}", name, error);
				write_small(autostart_base(name) + ".status", "error: " + error + "\n");
				continue;
			}
			log::info("autostart: started {}", name);
		}
	});
}

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	auth::Guard admin_recent{{"admin"}, true};

	router.add("GET", "/api/vm/script", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			auto files = list_scripts(script_directory);
			if (!files) return api::err(out, -1, "get scripts failed");
			auto autostart = read_autostart();
			if (!autostart) {
				log::error("read autostart list: {}", std::strerror(errno));
				autostart = std::vector<std::string>{};
			}
			api::ok(out, ScriptsRsp{*files, *autostart, autostart_status(*autostart)});
		});
	}, admin));

	// InstallPython: Python 3 from the Alpine repositories for .py scripts (apkpkg.Run under the update lock; an
	// install can take minutes, so a thread of its own).
	router.add("POST", "/api/vm/script/python", auth::guarded([](http::Request &req, http::Response &res) {
		res.pending = true;
		std::thread([id = req.id, headers = res.headers] {
			http::Response out;
			out.headers = headers;
			std::string error;
			if (!executable(python_interpreter)) error = apkpkg::run("install", "python3");
			if (error.empty()) api::ok(out);
			else out.json(200, "{\"code\":-1,\"msg\":" + gojson::quote("Python installation failed: " + error) + ",\"data\":null}");
			http::complete(id, std::move(out));
		}).detach();
	}, admin));

	router.add("POST", "/api/vm/script/upload", auth::guarded([](http::Request &req, http::Response &res) {
		auto parts = api::multipart(req);
		const api::Part *file = parts ? api::form_file(*parts, "file") : nullptr;
		if (!file) return api::err(res, -1, "bad request");
		if (!valid_name(*file->filename, false)) return api::err(res, -2, "invalid arguments");
		http::defer(req, res, [name = *file->filename, data = std::string(file->data)](http::Response &out) {
			if (!save_script(script_directory, name, data)) return api::err(out, -2, "save failed");
			UploadRsp rsp{name, std::nullopt};
			if (is_python(name) && !executable(python_interpreter)) rsp.pythonMissing = true;
			api::ok(out, rsp);
		});
	}, admin_recent));

	router.add("POST", "/api/vm/script/run", auth::guarded([](http::Request &req, http::Response &res) {
		auto params = api::Params::bind(req);
		bool ok1 = true, ok2 = true;
		std::optional<std::string> name, type;
		if (params) {
			name = params->string("name", ok1);
			type = params->string("type", ok2);
		}
		if (!params || !ok1 || !ok2 || !name || name->empty() || !type || type->empty())
			return api::err(res, -1, "invalid arguments");
		std::string path = resolve(script_directory, *name);
		if (path.empty() || (*type != "foreground" && *type != "background")) return api::err(res, -2, "invalid arguments");
		http::defer(req, res, [path, name = *name, type = *type](http::Response &out) {
			std::string error;
			auto command = script_command(path, error);
			std::optional<std::string> output;
			if (!command.empty()) {
				auto argv = unprotected(command);
				if (type == "foreground") {
					output = run_foreground(argv, error);
				} else {
					Child child = spawn(argv, "", false, false, false);
					if (child.pid < 0) {
						error = std::strerror(errno);
					} else {
						// Reaped here, as the Go server's goroutine did; the script's failure is logged.
						std::thread([child, path] {
							int status = 0;
							while (::waitpid(child.pid, &status, 0) < 0 && errno == EINTR) {
							}
							if (!WIFEXITED(status) || WEXITSTATUS(status) != 0)
								log::error("background script \"{}\" failed: exit status {}", path,
								           WIFEXITED(status) ? WEXITSTATUS(status) : -1);
						}).detach();
						output = "";
					}
				}
			}
			if (!output) {
				log::error("run script \"{}\" failed: {}", name, error);
				if (error == python_missing) return api::err(out, code_python_missing, python_missing);
				return api::err(out, -2, "run script failed");
			}
			api::ok(out, RunRsp{*output});
		});
	}, admin_recent));

	router.add("DELETE", "/api/vm/script", auth::guarded([](http::Request &req, http::Response &res) {
		bool valid = false;
		std::string name = param_string(req, "name", valid);
		if (!valid || name.empty()) return api::err(res, -1, "invalid arguments");
		http::defer(req, res, [name](http::Response &out) {
			std::string path = resolve(script_directory, name);
			if (path.empty() || ::unlink(path.c_str()) != 0) {
				log::error("delete script \"{}\" failed", name);
				return api::err(out, -3, "delete failed");
			}
			if (!set_autostart(name, false)) log::error("unmark deleted script \"{}\"", name);
			api::ok(out);
		});
	}, admin));

	router.add("POST", "/api/vm/script/autostart", auth::guarded([](http::Request &req, http::Response &res) {
		auto params = api::Params::bind(req);
		bool ok1 = true, ok2 = true;
		std::optional<std::string> name;
		std::optional<bool> enabled;
		if (params) {
			name = params->string("name", ok1);
			enabled = params->boolean("enabled", ok2);
		}
		if (!params || !ok1 || !ok2 || !name || name->empty()) return api::err(res, -1, "invalid arguments");
		http::defer(req, res, [name = *name, enabled = enabled.value_or(false)](http::Response &out) {
			if (!set_autostart(name, enabled)) {
				log::error("autostart \"{}\": invalid script", name);
				return api::err(out, -2, "invalid script");
			}
			api::ok(out);
		});
	}, admin_recent));

	router.add("GET", "/api/vm/schedule", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::string error;
			std::optional<std::string> data;
			{
				std::lock_guard lock(crontab_mutex);
				data = read_crontab(error);
			}
			if (!data) return api::err(out, -1, "cannot read the crontab");
			std::vector<Task> tasks;
			std::vector<std::string> other;
			parse_tasks(*data, tasks, other);
			tz::Zone zone = tz::Zone::device();
			std::int64_t now = now_unix();
			TasksRsp rsp;
			for (auto &t : tasks) {
				TaskStatus st{t.id, t.name, t.enabled, t.schedule, t.kind, t.target, {}, {}, {}, {}};
				std::string e;
				if (auto s = cron::parse(t.schedule, e); s && t.enabled) st.next = s->next(now, 3, zone);
				read_task_state(t.id, st);
				rsp.tasks.push_back(std::move(st));
			}
			// Other jobs, shown read-only: comments and blank lines are left out.
			for (auto &line : other) {
				std::string l = trim(line);
				if (!l.empty() && l[0] != '#') rsp.other.push_back(l);
			}
			rsp.crond = crond_running();
			api::ok(out, rsp);
		});
	}, admin));

	router.add("POST", "/api/vm/schedule", auth::guarded([](http::Request &req, http::Response &res) {
		Task t;
		constexpr glz::opts lenient{.error_on_unknown_keys = false};
		if (glz::read<lenient>(t, req.body)) return api::err(res, -1, "invalid arguments");
		if (t.id.empty()) {
			unsigned char random[4];
			RAND_bytes(random, sizeof random);
			static const char hex[] = "0123456789abcdef";
			t.id = "t";
			for (unsigned char c : random) {
				t.id += hex[c >> 4];
				t.id += hex[c & 15];
			}
		}
		t.name = trim(t.name);
		t.schedule = fields_joined(t.schedule);
		http::defer(req, res, [t](http::Response &out) {
			if (auto e = validate_task(t)) return api::err(out, -2, *e);
			auto e = edit_tasks([&](std::vector<Task> &tasks) -> std::optional<std::string> {
				for (auto &x : tasks) {
					if (x.id == t.id) {
						x = t;
						return std::nullopt;
					}
				}
				if (tasks.size() >= 64) return std::string("at most 64 scheduled tasks");
				tasks.push_back(t);
				return std::nullopt;
			});
			if (e) return api::err(out, -1, *e);
			api::ok(out, IdRsp{t.id});
		});
	}, admin_recent));

	router.add("DELETE", "/api/vm/schedule", auth::guarded([](http::Request &req, http::Response &res) {
		auto id = body_id(req);
		if (!id) return api::err(res, -1, "invalid arguments");
		http::defer(req, res, [id = *id](http::Response &out) {
			auto e = edit_tasks([&](std::vector<Task> &tasks) -> std::optional<std::string> {
				tasks.erase(std::remove_if(tasks.begin(), tasks.end(), [&](const Task &t) { return t.id == id; }), tasks.end());
				return std::nullopt;
			});
			if (e) return api::err(out, -1, *e);
			::unlink((task_run_dir + "/" + id + ".status").c_str());
			::unlink((task_run_dir + "/" + id + ".log").c_str());
			api::ok(out);
		});
	}, admin));

	router.add("POST", "/api/vm/schedule/preview", auth::guarded([](http::Request &req, http::Response &res) {
		PreviewReq r;
		constexpr glz::opts lenient{.error_on_unknown_keys = false};
		if (glz::read<lenient>(r, req.body)) return api::err(res, -1, "invalid arguments");
		std::string error;
		auto s = cron::parse(r.schedule, error);
		if (!s) return api::err(res, -2, error);
		api::ok(res, NextRsp{s->next(now_unix(), 5, tz::Zone::device())});
	}, admin));

	router.add("POST", "/api/vm/schedule/run", auth::guarded([](http::Request &req, http::Response &res) {
		auto id = body_id(req);
		if (!id) return api::err(res, -1, "invalid arguments");
		http::defer(req, res, [id = *id](http::Response &out) {
			std::string error;
			std::optional<std::string> data;
			{
				std::lock_guard lock(crontab_mutex);
				data = read_crontab(error);
			}
			if (!data) return api::err(out, -1, "cannot read the crontab");
			std::vector<Task> tasks;
			std::vector<std::string> other;
			parse_tasks(*data, tasks, other);
			for (auto &t : tasks) {
				if (t.id != id) continue;
				std::vector<std::string> argv{task_runner, t.id};
				for (auto &a : task_args(t)) argv.push_back(a);
				// As cron would run it, in a session of its own.
				Child child = spawn(argv, "", true, false, false);
				if (child.pid < 0) return api::err(out, -1, "the task could not be started");
				reap_later(child.pid);
				return api::ok(out);
			}
			api::err(out, -1, "task not found");
		});
	}, admin_recent));

	router.add("GET", "/api/vm/schedule/log", auth::guarded([](http::Request &req, http::Response &res) {
		std::string id = req.query_value("id");
		if (!valid_task_id(id)) return api::err(res, -1, "invalid arguments");
		bool ok = false;
		std::string data = read_file(task_run_dir + "/" + id + ".log", ok);
		if (!ok && errno != ENOENT) return api::err(res, -1, "cannot read the task output");
		api::ok(res, LogRsp{data});
	}, admin));
}

} // namespace nkos::system::scripts
