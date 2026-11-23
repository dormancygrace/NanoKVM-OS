#pragma once

// Who may send input, as the Go server's controlmode and inputcontrol packages.
//
// The AI control mode (/etc/kvm/ai-control.mode: off, mcp or picoclaw; picoclaw when absent) says which AI client may
// drive the computer. A switch preempts the side giving up control, waits up to 30 s for the operations holding the
// mode to finish, cleans up (releases keys), then saves the new mode; while it runs the mode reads as transitioning
// and no new operation starts. Manual input (a browser page) always wins over an AI operation: it cancels one in
// flight and keeps AI input away for 2 s after the last manual report. A manual session holds the mode stable while
// its reports are in flight or keys and buttons are held, so the mode cannot change under them.
//
// The mode and the operation slot are guarded for any thread: the loop thread never waits on them (a busy mode
// refuses), switches run on blocking threads. Reports are written by the HID writer threads, which ask a
// reservation, at the moment of writing, whether it still holds: a revoked session's queued reports and a
// cancelled AI operation's reports must not reach the computer; that check reads atomics only.

#include "common/time.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <functional>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>

namespace nkos::input::control {

enum class Mode { off, mcp, picoclaw };
const char *mode_name(Mode mode);
std::optional<Mode> parse_mode(std::string_view name);

// The saved mode; a variable so that the differential check can point it elsewhere.
inline std::string mode_file = "/etc/kvm/ai-control.mode";

struct ModeStatus {
	Mode mode = Mode::off;
	bool transitioning = false;
	std::string last_error;
	WallClock::time_point changed_at{};
};
// Status(): the saved mode (cached by the file's size and time). A file that cannot be read gives mode off and
// error (when asked for) set.
ModeStatus mode_status(std::string *error = nullptr);
// The JSON of a Status: {"mode","transitioning","last_error" (when set),"changed_at"}.
std::string mode_status_json(const ModeStatus &status);
// The error text of a conflict: "AI control mode conflict: current=off expected=mcp".
std::string require_write(Mode expected);

// A hold on the current mode: a switch waits until every hold is released (or 30 s pass).
class Hold {
public:
	~Hold();
	Hold(const Hold &) = delete;
	Hold &operator=(const Hold &) = delete;
	void release();

private:
	friend std::unique_ptr<Hold> acquire_hold();
	Hold() = default;
	bool held_ = true;
};
// AcquireWrite: the mode is expected and stable, and held so; nullptr and the conflict text otherwise.
std::unique_ptr<Hold> acquire_write(Mode expected, std::string &error);
// AcquireStable: the mode held stable for manual input; nullptr while it changes.
std::unique_ptr<Hold> acquire_stable(ModeStatus &status, std::string &error);

// A step of a switch: "" on success, else the error.
using Hook = std::function<std::string()>;
// SwitchWithCleanup: preempt, wait for the holds, cleanup (a failure saves off), save next. "" on success. Blocking:
// never on the loop thread.
std::string switch_with_cleanup(Mode next, const Hook &preempt, const Hook &cleanup);
// SwitchIfWithCleanup: the same only while the mode is expected; switched tells whether it matched.
std::string switch_if_with_cleanup(Mode expected, Mode next, const Hook &preempt, const Hook &cleanup, bool &switched);
// How long a switch waits for the holds (30 s); a variable for the check.
inline std::chrono::milliseconds activity_wait_timeout{30000};
// Forgets the cached state (the check starts each case afresh).
void reset_state();

// A cancellable operation: the first cause given wins; waits end early when it is cancelled. Any thread.
class Cancel {
public:
	Cancel() = default;
	// With a deadline: past it the operation counts as cancelled with deadline_cause.
	Cancel(std::chrono::steady_clock::time_point deadline, std::string deadline_cause);
	void cancel(const std::string &cause);
	// "" while it runs.
	std::string cause() const;
	bool cancelled() const { return !cause().empty(); }
	// Sleeps for delay; false (cancelled) when it ended early.
	bool sleep(std::chrono::nanoseconds delay) const;

private:
	mutable std::mutex mutex_;
	mutable std::condition_variable cv_;
	std::string cause_;
	std::optional<std::chrono::steady_clock::time_point> deadline_;
	std::string deadline_cause_;
	std::atomic<bool> set_{false};
};

// ---- AI input operations (inputcontrol.Coordinator) ----

inline constexpr const char *err_mcp_busy = "MCP remote control is busy";
inline constexpr const char *err_manual_active = "manual control is active";
inline constexpr const char *err_manual_preempted = "MCP operation was preempted by manual input";
inline constexpr const char *err_mode_changed = "MCP control mode changed";

enum class OperationKind { hid, read_only };
// BeginMCP / BeginBackground: the single AI operation slot; nullptr with error when busy or manual input is active
// (for HID operations).
std::shared_ptr<Cancel> begin_operation(OperationKind kind, std::string &error);
// Frees the slot (once; later calls do nothing).
void end_operation(const std::shared_ptr<Cancel> &operation);
// CancelMCP: cancels the operation in flight with cause.
void cancel_operation(const std::string &cause = err_mode_changed);

// ---- Manual input ----

enum class ReportKind { keyboard = 0, relative_mouse = 1, absolute_mouse = 2 };

class ManualSession;

// One admitted report. complete() once, on the loop thread, after the write (or when it is dropped).
class Reservation {
public:
	// Writer threads: the session still holds this generation (or the AI operation still runs), and the deadline,
	// if any, has not passed.
	bool valid() const;
	void complete(bool success);
	// The gate of an AI operation's reports: valid while it is not cancelled.
	static std::shared_ptr<Reservation> for_operation(std::shared_ptr<Cancel> operation);
	// A report that must not reach the computer after this time (the RustDesk bridge answers its frame by then).
	// Set before the report is queued.
	void set_deadline(std::chrono::steady_clock::time_point deadline) { deadline_ = deadline; }

private:
	friend class ManualSession;
	std::shared_ptr<ManualSession> session_;
	std::shared_ptr<Cancel> operation_;
	std::uint64_t generation_ = 0;
	ReportKind kind_{};
	bool held_ = false, start_cooldown_ = false, done_ = false;
	std::chrono::steady_clock::time_point deadline_{};
};

class ManualSession : public std::enable_shared_from_this<ManualSession> {
public:
	static std::shared_ptr<ManualSession> create();
	~ManualSession();

	// Admits a report: held says it leaves a key or button down; start_cooldown that it ends a gesture. nullptr
	// when manual input is not allowed now (the mode is changing, or allow refuses the mode).
	std::shared_ptr<Reservation> reserve(ReportKind kind, bool held, bool start_cooldown,
	                                     const std::function<bool(Mode)> &allow = {});
	// The writer released this kind after a failure.
	void reset(ReportKind kind);
	// Queued reports become invalid and the lease goes, the session stays usable (control moved away).
	void revoke();
	void close();
	// The current generation, for writes that release this session's state while it holds the lane.
	std::uint64_t generation() const { return live_.load(); }
	bool active() const { return active_; }

private:
	friend class Reservation;
	void complete(std::uint64_t generation, ReportKind kind, bool held, bool success, bool start_cooldown);
	bool release_report(ReportKind kind, bool held) const;
	void finish_if_idle();
	void end(bool cooldown);

	bool active_ = false, closed_ = false, cooldown_on_idle_ = false;
	std::uint64_t generation_ = 0;
	int pending_ = 0;
	bool held_[3] = {false, false, false};
	std::unique_ptr<Hold> hold_;
	std::atomic<std::uint64_t> live_{0};  // generation_ while active and open, else 0
};

// The lane writers take around a gated write, so manual, AI and background input never interleave a report.
void lock_write_lane();
void unlock_write_lane();

// Manual input is active or cooling down: AI input (MCP, PicoClaw, the jiggler) waits.
bool manual_active();

// Whether a PicoClaw session holds the input (set by the AI routes): manual input then yields while the mode is
// picoclaw (the allow function of the browser's input, paste and key sequences).
inline std::function<bool()> ai_session_holds_input;
inline bool allow_manual(Mode mode) { return mode != Mode::picoclaw || !ai_session_holds_input || !ai_session_holds_input(); }

} // namespace nkos::input::control
