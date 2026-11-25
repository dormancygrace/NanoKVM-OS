#pragma once

// The mouse jiggler, as the Go server's service/vm/jiggler: while on (/etc/kvm/mouse-jiggler holds the mode,
// "relative" or "absolute"), every 15 s without manual input it moves the pointer a little and back. It is
// background input: it skips a turn while manual input is active or the control mode is changing. Loop thread.

#include "http/router.hpp"

namespace nkos::input::jiggler {

// Reads the saved state and starts the timer when on (after hid::start).
void start();
void stop();

// Manual input was queued: the next move waits a full interval.
void note_input();

// GET /api/vm/mouse-jiggler, POST /api/vm/mouse-jiggler/ (admin).
void add_routes(http::Router &router);

} // namespace nkos::input::jiggler
