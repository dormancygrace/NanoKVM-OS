#include "input/control.hpp"

#include "common/log.hpp"

#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <fstream>
#include <random>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::input::control {

namespace {

using Clock = std::chrono::steady_clock;

constexpr auto manual_cooldown = std::chrono::seconds(2);
constexpr Mode default_mode = Mode::picoclaw;

// controlmode.Manager.
std::mutex state_mu;
struct State {
	bool loaded = false, exists = false;
	off_t size = 0;
	struct timespec mtime{};
	Mode mode = Mode::off;
	bool transitioning = false;
	std::string last_error;
	WallClock::time_point changed_at{};
} st;
std::mutex transition_mu;

// The activity gate: holds share the mode, a switch takes it alone.
std::mutex gate_mu;
std::condition_variable gate_cv;
int gate_active = 0;
bool gate_exclusive = false;

// inputcontrol.Coordinator.
std::mutex coord_mu;
std::shared_ptr<Cancel> active_operation;
OperationKind active_kind = OperationKind::hid;
int manual_sessions = 0;
Clock::time_point manual_until{};

std::mutex write_lane;

// Go's text of an errno (syscall.Errno.Error()): glibc's with a lower-case first letter.
std::string errno_text(int e)
{
	std::string s = std::strerror(e);
	if (!s.empty() && s[0] >= 'A' && s[0] <= 'Z' && !(s.size() > 1 && s[1] >= 'A' && s[1] <= 'Z')) s[0] = static_cast<char>(s[0] - 'A' + 'a');
	return s;
}

std::string path_error(const char *op, const std::string &path, int e) { return std::string(op) + " " + path + ": " + errno_text(e); }

std::string trim_space(const std::string &s)
{
	auto a = s.find_first_not_of(" \t\r\n\v\f");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n\v\f") - a + 1);
}

// strconv.Quote of short ASCII-ish text (the mode file's content).
std::string go_quote(const std::string &s)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
	for (unsigned char c : s) {
		switch (c) {
		case '"': out += "\\\""; break;
		case '\\': out += "\\\\"; break;
		case '\n': out += "\\n"; break;
		case '\r': out += "\\r"; break;
		case '\t': out += "\\t"; break;
		case '\v': out += "\\v"; break;
		case '\f': out += "\\f"; break;
		case '\a': out += "\\a"; break;
		case '\b': out += "\\b"; break;
		default:
			if (c < 0x20 || c == 0x7f) {
				out += "\\x";
				out += hex[c >> 4];
				out += hex[c & 15];
			} else {
				out += static_cast<char>(c);
			}
		}
	}
	return out + "\"";
}

WallClock::time_point to_wall(const struct timespec &ts)
{
	return WallClock::time_point(std::chrono::duration_cast<WallClock::duration>(std::chrono::seconds(ts.tv_sec) +
	                                                                            std::chrono::nanoseconds(ts.tv_nsec)));
}

void cache_file_info(const struct stat &info)
{
	st.exists = true;
	st.size = info.st_size;
	st.mtime = info.st_mtim;
	auto mtime = to_wall(info.st_mtim);
	if (st.changed_at == WallClock::time_point{} || mtime > st.changed_at) st.changed_at = mtime;
}

std::string load_locked()
{
	if (st.loaded && st.transitioning) return {};
	struct stat info{};
	int r = ::stat(mode_file.c_str(), &info);
	int stat_errno = r == 0 ? 0 : errno;
	if (st.loaded && r == 0 && st.exists && info.st_size == st.size && info.st_mtim.tv_sec == st.mtime.tv_sec &&
	    info.st_mtim.tv_nsec == st.mtime.tv_nsec)
		return {};
	if (st.loaded && stat_errno == ENOENT && !st.exists) return {};
	if (r != 0 && stat_errno != ENOENT) return "stat AI control mode: " + path_error("stat", mode_file, stat_errno);

	int fd = ::open(mode_file.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) {
		int e = errno;
		if (e == ENOENT) {
			st.mode = default_mode;
			st.loaded = true;
			st.exists = false;
			st.size = 0;
			st.mtime = {};
			if (st.changed_at == WallClock::time_point{}) st.changed_at = WallClock::now();
			return {};
		}
		return "read AI control mode: " + path_error("open", mode_file, e);
	}
	std::string data;
	char buf[256];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) break;
		data.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	std::string text = trim_space(data);
	auto mode = parse_mode(text);
	if (r != 0) ::stat(mode_file.c_str(), &info);
	if (!mode) {
		st.mode = Mode::off;
		st.loaded = true;
		st.last_error = "invalid AI control mode " + go_quote(text);
		cache_file_info(info);
		return {};
	}
	st.mode = *mode;
	st.loaded = true;
	st.last_error.clear();
	cache_file_info(info);
	return {};
}

std::string save_locked(Mode mode)
{
	auto slash = mode_file.rfind('/');
	std::string dir = slash == std::string::npos ? "." : slash == 0 ? "/" : mode_file.substr(0, slash);
	// os.MkdirAll.
	for (std::size_t p = 1; p <= dir.size(); p++) {
		if (p != dir.size() && dir[p] != '/') continue;
		std::string part = dir.substr(0, p);
		if (::mkdir(part.c_str(), 0755) != 0 && errno != EEXIST)
			return "create AI control mode directory: " + path_error("mkdir", part, errno);
	}
	std::random_device rd;
	std::string tmp;
	int fd = -1;
	for (int attempt = 0; attempt < 10000 && fd < 0; attempt++) {
		tmp = dir + "/.ai-control.mode." + std::to_string(rd() % 4294967295u);
		fd = ::open(tmp.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd < 0 && errno != EEXIST) return "create temporary AI control mode: " + path_error("open", tmp, errno);
	}
	if (fd < 0) return "create temporary AI control mode: " + path_error("open", tmp, EEXIST);
	struct Remove {
		std::string path;
		~Remove() { ::unlink(path.c_str()); }
	} remove{tmp};
	if (::fchmod(fd, 0600) != 0) {
		int e = errno;
		::close(fd);
		return "set AI control mode permissions: " + path_error("chmod", tmp, e);
	}
	std::string content = std::string(mode_name(mode)) + "\n";
	if (::write(fd, content.data(), content.size()) != static_cast<ssize_t>(content.size())) {
		int e = errno;
		::close(fd);
		return "write AI control mode: " + path_error("write", tmp, e);
	}
	if (::fsync(fd) != 0) {
		int e = errno;
		::close(fd);
		return "sync AI control mode: " + path_error("sync", tmp, e);
	}
	::close(fd);
	if (::rename(tmp.c_str(), mode_file.c_str()) != 0)
		return "replace AI control mode: rename " + tmp + " " + mode_file + ": " + errno_text(errno);
	st.mode = mode;
	st.loaded = true;
	struct stat info{};
	if (::stat(mode_file.c_str(), &info) == 0) {
		cache_file_info(info);
	} else {
		st.exists = true;
		st.size = static_cast<off_t>(content.size());
		clock_gettime(CLOCK_REALTIME, &st.mtime);
	}
	st.changed_at = WallClock::now();
	int dfd = ::open(dir.c_str(), O_RDONLY | O_CLOEXEC);
	if (dfd >= 0) {
		if (::fsync(dfd) != 0) {
			int e = errno;
			::close(dfd);
			return "sync AI control mode directory: " + path_error("sync", dir, e);
		}
		::close(dfd);
	}
	return {};
}

std::string save(Mode mode)
{
	std::lock_guard lock(state_mu);
	return save_locked(mode);
}

void set_transitioning(bool v)
{
	std::lock_guard lock(state_mu);
	st.transitioning = v;
}

void set_last_error(const std::string &error)
{
	std::lock_guard lock(state_mu);
	st.last_error = error;
}

ModeStatus status_locked()
{
	return {st.mode, st.transitioning, st.last_error, st.changed_at};
}

// A shared hold of the gate; wait: block while a switch holds it (never on the loop thread).
bool gate_share(bool wait)
{
	std::unique_lock lock(gate_mu);
	if (gate_exclusive) {
		if (!wait) return false;
		gate_cv.wait(lock, [] { return !gate_exclusive; });
	}
	gate_active++;
	return true;
}

void gate_unshare()
{
	{
		std::lock_guard lock(gate_mu);
		if (gate_active > 0) gate_active--;
	}
	gate_cv.notify_all();
}

std::string gate_exclusive_acquire()
{
	auto timeout = activity_wait_timeout.count() > 0 ? activity_wait_timeout : std::chrono::milliseconds(30000);
	std::unique_lock lock(gate_mu);
	if (!gate_cv.wait_for(lock, timeout, [] { return !gate_exclusive && gate_active == 0; }))
		return "wait for active control operations: timed out waiting for active control operations";
	gate_exclusive = true;
	return {};
}

void gate_exclusive_release()
{
	{
		std::lock_guard lock(gate_mu);
		gate_exclusive = false;
	}
	gate_cv.notify_all();
}

struct Transitioning {
	Transitioning() { set_transitioning(true); }
	~Transitioning() { set_transitioning(false); }
};

// The steps of a switch once the transition has started.
std::string run_switch(Mode next, const Hook &preempt, const Hook &cleanup, const char *what)
{
	Transitioning transitioning;
	if (preempt) {
		if (std::string err = preempt(); !err.empty()) {
			set_last_error(err);
			log::warn("{} preempt failed: {}", what, err);
			return err;
		}
	}
	if (std::string err = gate_exclusive_acquire(); !err.empty()) {
		set_last_error(err);
		log::warn("{} activity wait failed: {}", what, err);
		return err;
	}
	struct Release {
		~Release() { gate_exclusive_release(); }
	} release;
	if (cleanup) {
		if (std::string err = cleanup(); !err.empty()) {
			if (std::string rollback = save(Mode::off); !rollback.empty()) err += "\n" + rollback;
			set_last_error(err);
			log::warn("{} cleanup failed: {}", what, err);
			return err;
		}
	}
	if (std::string err = save(next); !err.empty()) {
		set_last_error(err);
		log::warn("{} save failed: {}", what, err);
		return err;
	}
	set_last_error({});
	log::info("{} completed: to {}", what, mode_name(next));
	return {};
}

} // namespace

const char *mode_name(Mode mode)
{
	switch (mode) {
	case Mode::mcp: return "mcp";
	case Mode::picoclaw: return "picoclaw";
	default: return "off";
	}
}

std::optional<Mode> parse_mode(std::string_view name)
{
	if (name == "off") return Mode::off;
	if (name == "mcp") return Mode::mcp;
	if (name == "picoclaw") return Mode::picoclaw;
	return std::nullopt;
}

ModeStatus mode_status(std::string *error)
{
	std::lock_guard lock(state_mu);
	if (std::string err = load_locked(); !err.empty()) {
		if (error) *error = err;
		return {Mode::off, st.transitioning, err, st.changed_at};
	}
	if (error) error->clear();
	return status_locked();
}

std::string mode_status_json(const ModeStatus &s)
{
	std::string out = std::string("{\"mode\":\"") + mode_name(s.mode) + "\",\"transitioning\":" + (s.transitioning ? "true" : "false");
	if (!s.last_error.empty()) {
		// Go's encoding of the error text (HTML characters escaped).
		std::string q = "\"";
		static const char hex[] = "0123456789abcdef";
		for (unsigned char c : s.last_error) {
			switch (c) {
			case '"': q += "\\\""; break;
			case '\\': q += "\\\\"; break;
			case '\n': q += "\\n"; break;
			case '\r': q += "\\r"; break;
			case '\t': q += "\\t"; break;
			default:
				if (c < 0x20 || c == '<' || c == '>' || c == '&') {
					q += "\\u00";
					q += hex[c >> 4];
					q += hex[c & 15];
				} else {
					q += static_cast<char>(c);
				}
			}
		}
		out += ",\"last_error\":" + q + "\"";
	}
	return out + ",\"changed_at\":\"" + rfc3339nano(s.changed_at) + "\"}";
}

std::string require_write(Mode expected)
{
	std::string error;
	auto s = mode_status(&error);
	if (!error.empty()) return error;
	if (s.transitioning || s.mode != expected)
		return std::string("AI control mode conflict: current=") + mode_name(s.mode) + " expected=" + mode_name(expected);
	return {};
}

std::unique_ptr<Hold> acquire_hold() { return std::unique_ptr<Hold>(new Hold); }

Hold::~Hold() { release(); }

void Hold::release()
{
	if (!held_) return;
	held_ = false;
	gate_unshare();
}

std::unique_ptr<Hold> acquire_write(Mode expected, std::string &error)
{
	error = require_write(expected);
	if (!error.empty()) return nullptr;
	gate_share(true);
	auto hold = acquire_hold();
	error = require_write(expected);
	if (!error.empty()) return nullptr;
	return hold;
}

std::unique_ptr<Hold> acquire_stable(ModeStatus &status, std::string &error)
{
	status = mode_status(&error);
	if (!error.empty()) {
		status = {};
		return nullptr;
	}
	if (status.transitioning || !gate_share(false)) {
		error = "AI control mode conflict: control mode is transitioning";
		return nullptr;
	}
	auto hold = acquire_hold();
	status = mode_status(&error);
	if (!error.empty()) {
		status = {};
		return nullptr;
	}
	if (status.transitioning) {
		error = "AI control mode conflict: control mode is transitioning";
		return nullptr;
	}
	return hold;
}

std::string switch_with_cleanup(Mode next, const Hook &preempt, const Hook &cleanup)
{
	std::lock_guard transition(transition_mu);
	std::string error;
	auto s = mode_status(&error);
	if (!error.empty()) {
		log::warn("AI control mode switch failed while loading status: {}", error);
		return error;
	}
	if (s.mode == next) return {};
	return run_switch(next, preempt, cleanup, "AI control mode switch");
}

std::string switch_if_with_cleanup(Mode expected, Mode next, const Hook &preempt, const Hook &cleanup, bool &switched)
{
	switched = false;
	std::lock_guard transition(transition_mu);
	std::string error;
	auto s = mode_status(&error);
	if (!error.empty()) {
		log::warn("conditional AI control mode switch failed while loading status: {}", error);
		return error;
	}
	if (s.mode != expected) return {};
	if (s.mode == next) {
		switched = true;
		return {};
	}
	error = run_switch(next, preempt, cleanup, "conditional AI control mode switch");
	switched = error.empty();
	return error;
}

void reset_state()
{
	std::lock_guard lock(state_mu);
	st = {};
	std::lock_guard c(coord_mu);
	active_operation.reset();
	manual_sessions = 0;
	manual_until = {};
}

// ---- Cancel ----

Cancel::Cancel(std::chrono::steady_clock::time_point deadline, std::string deadline_cause)
    : deadline_(deadline), deadline_cause_(std::move(deadline_cause))
{
}

void Cancel::cancel(const std::string &cause)
{
	{
		std::lock_guard lock(mutex_);
		if (!cause_.empty()) return;
		cause_ = cause.empty() ? "context canceled" : cause;
		set_ = true;
	}
	cv_.notify_all();
}

std::string Cancel::cause() const
{
	std::lock_guard lock(mutex_);
	if (cause_.empty() && deadline_ && std::chrono::steady_clock::now() >= *deadline_) return deadline_cause_;
	return cause_;
}

bool Cancel::sleep(std::chrono::nanoseconds delay) const
{
	std::unique_lock lock(mutex_);
	auto until = std::chrono::steady_clock::now() + delay;
	if (deadline_ && *deadline_ < until) {
		cv_.wait_until(lock, *deadline_, [&] { return !cause_.empty(); });
		return false;
	}
	return !cv_.wait_until(lock, until, [&] { return !cause_.empty(); });
}

// ---- AI operations ----

std::shared_ptr<Cancel> begin_operation(OperationKind kind, std::string &error)
{
	std::lock_guard lock(coord_mu);
	if (active_operation) {
		error = err_mcp_busy;
		return nullptr;
	}
	if (kind == OperationKind::hid && (manual_sessions > 0 || Clock::now() < manual_until)) {
		error = err_manual_active;
		return nullptr;
	}
	active_operation = std::make_shared<Cancel>();
	active_kind = kind;
	error.clear();
	return active_operation;
}

void end_operation(const std::shared_ptr<Cancel> &operation)
{
	if (!operation) return;
	operation->cancel("context canceled");
	std::lock_guard lock(coord_mu);
	if (active_operation == operation) active_operation.reset();
}

void cancel_operation(const std::string &cause)
{
	std::shared_ptr<Cancel> operation;
	{
		std::lock_guard lock(coord_mu);
		operation = active_operation;
	}
	if (operation) operation->cancel(cause);
}

namespace {

// Manual input starts: an AI HID operation in flight is cancelled (its reports stop at the write lane).
void begin_manual()
{
	std::lock_guard lock(coord_mu);
	manual_sessions++;
	if (active_operation && active_kind == OperationKind::hid) active_operation->cancel(err_manual_preempted);
}

void end_manual(bool start_cooldown)
{
	std::lock_guard lock(coord_mu);
	if (manual_sessions > 0) manual_sessions--;
	if (start_cooldown && manual_sessions == 0) manual_until = Clock::now() + manual_cooldown;
}

} // namespace

// ---- Manual sessions ----

bool Reservation::valid() const
{
	if (deadline_ != Clock::time_point{} && Clock::now() >= deadline_) return false;
	if (operation_) return !operation_->cancelled();
	return session_ && generation_ != 0 && session_->live_.load() == generation_;
}

void Reservation::complete(bool success)
{
	if (done_ || !session_) return;
	done_ = true;
	session_->complete(generation_, kind_, held_, success, start_cooldown_);
}

std::shared_ptr<Reservation> Reservation::for_operation(std::shared_ptr<Cancel> operation)
{
	auto r = std::make_shared<Reservation>();
	r->operation_ = std::move(operation);
	return r;
}

std::shared_ptr<ManualSession> ManualSession::create() { return std::shared_ptr<ManualSession>(new ManualSession); }

ManualSession::~ManualSession()
{
	if (active_) end(true);
}

bool ManualSession::release_report(ReportKind kind, bool held) const
{
	if (held) return false;
	if (kind == ReportKind::keyboard) return held_[0];
	return held_[1] || held_[2];
}

std::shared_ptr<Reservation> ManualSession::reserve(ReportKind kind, bool held, bool start_cooldown,
                                                    const std::function<bool(Mode)> &allow)
{
	if (closed_) return nullptr;
	auto make = [&] {
		auto r = std::make_shared<Reservation>();
		r->session_ = shared_from_this();
		r->generation_ = generation_;
		r->kind_ = kind;
		r->held_ = held;
		r->start_cooldown_ = start_cooldown;
		return r;
	};
	if (active_) {
		std::string error;
		auto status = mode_status(&error);
		if (!error.empty()) return nullptr;
		bool allowed = !status.transitioning && (!allow || allow(status.mode));
		// A release always passes: keys must come up.
		if (!allowed && !release_report(kind, held)) return nullptr;
		pending_++;
		start_cooldown = start_cooldown || release_report(kind, held);
		return make();
	}
	ModeStatus status;
	std::string error;
	auto hold = acquire_stable(status, error);
	if (!hold) return nullptr;
	if (allow && !allow(status.mode)) return nullptr;
	begin_manual();
	hold_ = std::move(hold);
	active_ = true;
	generation_++;
	live_ = generation_;
	pending_ = 1;
	return make();
}

void ManualSession::complete(std::uint64_t generation, ReportKind kind, bool held, bool success, bool start_cooldown)
{
	if (!active_ || generation != generation_) return;
	if (pending_ > 0) pending_--;
	if (start_cooldown) cooldown_on_idle_ = true;
	if (success) held_[static_cast<int>(kind)] = held;
	else held_[0] = held_[1] = held_[2] = false;
	finish_if_idle();
}

void ManualSession::finish_if_idle()
{
	if (!active_ || pending_ > 0 || held_[0] || held_[1] || held_[2]) return;
	bool cooldown = cooldown_on_idle_;
	active_ = false;
	live_ = 0;
	cooldown_on_idle_ = false;
	end(cooldown);
}

void ManualSession::end(bool cooldown)
{
	hold_.reset();
	end_manual(cooldown);
}

void ManualSession::reset(ReportKind kind)
{
	if (!active_) return;
	held_[static_cast<int>(kind)] = false;
	if (!active_ || pending_ > 0 || held_[0] || held_[1] || held_[2]) return;
	active_ = false;
	live_ = 0;
	cooldown_on_idle_ = false;
	end(true);
}

void ManualSession::revoke()
{
	generation_++;
	if (!active_) return;
	pending_ = 0;
	held_[0] = held_[1] = held_[2] = false;
	cooldown_on_idle_ = false;
	active_ = false;
	live_ = 0;
	end(false);
}

void ManualSession::close()
{
	if (closed_) return;
	closed_ = true;
	pending_ = 0;
	held_[0] = held_[1] = held_[2] = false;
	cooldown_on_idle_ = false;
	bool was = active_;
	active_ = false;
	live_ = 0;
	generation_++;
	if (was) end(true);
}

void lock_write_lane() { write_lane.lock(); }
void unlock_write_lane() { write_lane.unlock(); }

bool manual_active()
{
	std::lock_guard lock(coord_mu);
	return manual_sessions > 0 || Clock::now() < manual_until;
}

} // namespace nkos::input::control
