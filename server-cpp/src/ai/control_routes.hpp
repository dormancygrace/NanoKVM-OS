#pragma once

// The AI control mode routes of router/control.go (administrators): GET /api/ai/control/status answers the mode,
// PUT /api/ai/control/mode switches it (off, mcp or picoclaw): the side giving up control is preempted (MCP's
// operation and PicoClaw's operations and sessions), its runtime stopped when MCP takes over, the HID state released.

#include "http/router.hpp"

#include <functional>
#include <string>

namespace nkos::ai::control_routes {

// hid.ReleaseAllHIDStateBestEffort; set by the glue.
extern std::function<std::string()> release_hid;

// The handlers (blocking: a switch waits for the operations holding the mode).
void status(http::Request &request, http::Response &response);
void set_mode(http::Request &request, http::Response &response);

void add_routes(http::Router &router);

} // namespace nkos::ai::control_routes
