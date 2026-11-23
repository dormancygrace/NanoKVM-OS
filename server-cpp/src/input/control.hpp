#pragma once

// Who may send input, as the Go server's controlmode and inputcontrol packages.
//
// The AI control mode (/etc/kvm/ai-control.mode: off, mcp or picoclaw; picoclaw when absent) says which AI client may
// drive the computer. Manual input (a browser page) always wins over an AI operation: it cancels one in flight and
// keeps AI input away for 2 s after the last manual report. A manual session holds a "stable mode" while its
// reports are in flight or keys and buttons are held, so the mode cannot change under them.
//
// State lives on the loop thread. Reports are written by the HID writer threads, which ask a reservation, at the
// moment of writing, whether it still belongs to the current generation of its session (a revoked session's queued
// reports must not reach the computer): that check reads atomics only.

#include <atomic>
#include <chrono>
#include <cstdint>
#include <functional>
#include <memory>
#include <string>

namespace nkos::input::control {

enum class Mode { off, mcp, picoclaw };
const char *mode_name(Mode mode);
struct ModeStatus {
	Mode mode = Mode::off;
	bool transitioning = false;
	std::string last_error;
};
// The saved mode (cached by the file's size and time).
ModeStatus mode_status();

enum class ReportKind { keyboard = 0, relative_mouse = 1, absolute_mouse = 2 };

class ManualSession;

// One admitted report. complete() once, on the loop thread, after the write (or when it is dropped).
class Reservation {
public:
	// Writer threads: the session still holds this generation (and the deadline, if any, has not passed).
	bool valid() const;
	void complete(bool success);
	// A report that must not reach the computer after this time (the RustDesk bridge answers its frame by then).
	// Set before the report is queued.
	void set_deadline(std::chrono::steady_clock::time_point deadline) { deadline_ = deadline; }

private:
	friend class ManualSession;
	std::shared_ptr<ManualSession> session_;
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
	std::atomic<std::uint64_t> live_{0};  // generation_ while active and open, else 0
};

// The lane writers take around a gated write, so manual, AI and background input never interleave a report.
void lock_write_lane();
void unlock_write_lane();

// Manual input is active or cooling down: AI input (MCP, PicoClaw, the jiggler) waits.
bool manual_active();

} // namespace nkos::input::control
