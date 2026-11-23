#pragma once

// The logs of the administrator's log viewer, as the Go server's logs package: the system log (/run/log/messages,
// rotated), the kernel ring and the application log (the server's own messages and the native libraries' stderr,
// kept in a 1 MiB ring in memory), each shown sanitized (system/log_sanitize). The last three boots are kept on
// the card (/var/log/nanokvm-boots/{current,previous,older}), saved once a minute, in the Go server's format.

#include "http/router.hpp"

#include <string_view>

namespace nkos::system::logs {

// The server's log lines go into the application ring from now on; the archive of this boot is opened (and the
// application lines saved earlier in this boot come back). Before the server runs.
void init();
// File descriptor 2 into the application ring (at most 20 lines a second; a full pipe drops, never blocks).
void capture_native_stderr();
// Descriptor 2 back as the service started it (before an exec: the pipe's reader does not survive it).
void restore_native_stderr();
// Saves the archive once a minute (loop thread, after the server runs).
void start();
// The last save, at shutdown.
void flush();

// GET /api/vm/logs, GET /api/vm/logs/boots (admin).
void add_routes(http::Router &router);

} // namespace nkos::system::logs
