#pragma once

// The HDMI capture's demand, as the Go server's service/vm/hdmi.go: viewers (per transport) and leases (a stream
// session, a screenshot) keep it running. Without demand the VPSS outputs park after 20 s (at once before the first
// viewer of this run), so the capture stops converting frames nobody reads; after the idle timeout
// (/etc/kvm/hdmi_idle_timeout, minutes, 0 = never) the capture stops altogether until demand comes back. An
// administrator can turn HDMI capture off (/etc/kvm/hdmi_disable). Loop thread only.

#include "http/router.hpp"

#include <chrono>
#include <functional>
#include <memory>
#include <string>

namespace nkos::video::hdmi {

// EnableHdmiCapture or DisableHdmiCapture by the saved setting; at start, after vision::init.
void start();

// A transport's viewer count (its total; the transports add up). While there are viewers the video gate is held:
// taken before the update, so before a viewer wakes the capture, with the background OCR already ended; released
// when none are left. Updates that come while the gate waits for that OCR are applied in order once it has ended.
void set_viewers(const std::string &source, int count);
// One transport's viewer count (0 when it has none).
int viewers_of(const std::string &source);
int viewer_count();

// A capture that came on gives a reader the picture from before first: the first successful reader after it
// claims (and drops) that one. Failed reads do not claim it.
bool needs_fresh_frame();
bool claim_fresh_frame();
// Counts the capture being switched on or off: a picture from before the last switch may be of a screen gone.
std::uint64_t capture_switches();

// Keeps the capture running until released (once; the destructor releases too).
class Lease {
public:
	Lease();
	~Lease();
	Lease(const Lease &) = delete;
	Lease &operator=(const Lease &) = delete;
	void release();
	// When a capture resumed from idle has had time to give a fresh frame.
	std::chrono::steady_clock::time_point ready_at() const { return ready_at_; }

private:
	bool held_ = true;
	std::chrono::steady_clock::time_point ready_at_;
};

// A read nobody watches (the screen watch): not demand, so a read every few seconds does not restart the park timer
// and the capture still parks without viewers. Acquiring starts a capture stopped for idle again and resumes parked
// outputs; releasing (once; the destructor too) parks again what it resumed.
class BackgroundRead {
public:
	// nullptr while HDMI capture is disabled.
	static std::unique_ptr<BackgroundRead> acquire();
	~BackgroundRead();
	BackgroundRead(const BackgroundRead &) = delete;
	BackgroundRead &operator=(const BackgroundRead &) = delete;
	void release();
	// When a capture resumed from idle has had time to give a fresh frame.
	std::chrono::steady_clock::time_point ready_at() const { return ready_at_; }

private:
	BackgroundRead() = default;
	bool held_ = true;
	std::chrono::steady_clock::time_point ready_at_;
};

// A watch that needs the screen while nobody views it (the screen watch): the idle timeout then leaves the capture
// running, parked between its reads. Stopping and restarting it for each read would reset the receiver of a PCIe
// board, which the target sees as a monitor unplugged and plugged in again.
void set_keep_capture(std::function<bool()> keep);

bool disabled();
// The capture is stopped because nobody watched for the idle timeout.
bool idle_stopped();

// The JSON of GET /api/vm/hdmi.
std::string state_json();

// GET /api/vm/hdmi, POST /api/vm/hdmi/{reset,enable,disable,timeout}.
void add_routes(http::Router &router);

} // namespace nkos::video::hdmi
