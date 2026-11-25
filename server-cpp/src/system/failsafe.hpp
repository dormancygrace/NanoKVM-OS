#pragma once

// Video recovery, as the Go server's service/failsafe. The supervisor (nanokvm-server-supervisor) restarts the
// server after a crash and keeps eight records of the exits (/run/nanokvm/recovery/exit-N.txt: uptime, status,
// the video phase at the time, then the crash report). Three crashes within ten minutes while the video was
// starting or running put the server into recovery: it starts without video, so settings, updates and input stay
// reachable, until an administrator retries.

#include "http/router.hpp"

#include <string>

namespace nkos::system::failsafe {

struct State {
	bool active = false;
	int crashes = 0;
	bool retrying = false;
};

// Reads the records; true when recovery is active. Before anything touches the video.
bool init();
State status();
std::string status_json();
// "initializing" / "ready": the supervisor writes the phase of a crash into its record.
void video_phase(const char *phase);
// The video is off for this run (recovery).
bool video_disabled();

// GET /api/vm/video/crashes, POST /api/vm/video/retry (administrators).
void add_routes(http::Router &router);

// A route guard: 503 while recovery keeps the video off (middleware.RequireVideo).
bool require_video(http::Response &response);

} // namespace nkos::system::failsafe
