#pragma once

// The web terminal: GET /api/vm/terminal (admin, recent password confirmation) attaches the page to a login shell on
// a pty, a new one or with session=ID the user's shell that is still running; with port=... it is a serial terminal
// (system/serial_terminal.hpp). GET /api/vm/terminal/sessions lists the running shells, DELETE
// /api/vm/terminal/sessions/:id ends one (admin).
//
// A shell outlives its page: without one it keeps its output (the last 64 KiB) and waits 10 minutes for a page to
// attach again, then ends. It belongs to the sign-in that started it and ends when that sign-in is no longer valid.
// At most four run at once. Ending a shell ends every program of its session (SIGHUP, then SIGKILL).

#include "http/router.hpp"

namespace nkos::system::terminal {

void add_routes(http::Router &router);

} // namespace nkos::system::terminal
