#pragma once

// GET /api/ws: the remote desktop page's socket, as the Go server's service/ws. The page sends keyboard and mouse
// reports and control requests; the server sends heartbeats, which tab has input control (with that tab's input
// lease), capture status and keyboard LED events. One tab at a time controls input; the others watch.
//
// From the page, binary: 0 heartbeat; 1 + 8-byte keyboard report; 2 + mouse report (relative 4/5, absolute 6/7
// bytes); 3 + 0/1 give up / take control; 4 held-input keepalive (opts in to releasing held keys 300 ms after the
// last keepalive); 5 + 0/1 page hidden / visible. To the page, text: {"type": ..., "data": "<json or empty>"}.

#include "http/router.hpp"

#include <chrono>
#include <functional>
#include <string>

namespace nkos::input::ws {

void add_routes(http::Router &router);

// An HTTP input request (paste, ATX) may act: no tab holds input control, or it presents the controlling tab's lease.
bool allows_input_lease(const std::string &lease);

// An add-on reserves manual input; browser tabs stay view-only until one takes control, which calls release.
bool acquire_external_input(const std::string &lease, std::function<void()> release);
void release_external_input(const std::string &lease);

// The last manual input (for the mouse jiggler).
std::chrono::steady_clock::time_point last_manual_input();

} // namespace nkos::input::ws
