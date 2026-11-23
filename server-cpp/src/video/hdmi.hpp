#pragma once

// The HDMI capture's demand, as the Go server's service/vm/hdmi.go: viewers (per transport) and leases (a stream
// session, a screenshot) keep it running. Without demand the VPSS outputs park after 20 s (at once before the first
// viewer of this run), so the capture stops converting frames nobody reads; after the idle timeout
// (/etc/kvm/hdmi_idle_timeout, minutes, 0 = never) the capture stops altogether until demand comes back. An
// administrator can turn HDMI capture off (/etc/kvm/hdmi_disable). Loop thread only.

#include "http/router.hpp"

#include <chrono>
#include <functional>
#include <string>

namespace nkos::video::hdmi {

// EnableHdmiCapture or DisableHdmiCapture by the saved setting; at start, after vision::init.
void start();

// A transport's viewer count (its total; the transports add up).
void set_viewers(const std::string &source, int count);
int viewer_count();

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

bool disabled();

// The JSON of GET /api/vm/hdmi.
std::string state_json();

// GET /api/vm/hdmi, POST /api/vm/hdmi/{reset,enable,disable,timeout}.
void add_routes(http::Router &router);

} // namespace nkos::video::hdmi
