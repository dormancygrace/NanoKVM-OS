#pragma once

// Scripts window and scheduled tasks, as the Go server's service/vm/script.go, autostart.go and schedule.go:
// GET/DELETE /api/vm/script, POST /api/vm/script/upload, /run and /autostart (recent password confirmation);
// GET/POST/DELETE /api/vm/schedule, POST /api/vm/schedule/preview and /run, GET /api/vm/schedule/log (admin).
//
// Scripts are .sh and .py files under /etc/kvm/scripts. Those marked "run at startup" run once per boot (start_
// autostart, after the listeners exist), each in a session of its own, with the first 64 KiB of their output and
// their exit status in /run/nanokvm/autostart. Scheduled tasks live in root's crontab: a "# nanokvm-task {json}"
// line per task and, for an enabled one, its job line; run-task keeps their output and status in /run/nanokvm/tasks.

#include "http/router.hpp"

#include <string>

namespace nkos::system::scripts {

void add_routes(http::Router &router);
// Runs the marked scripts once per boot (a marker in /run keeps server restarts from running them again).
void start_autostart();

// For other families: a script name (relative path below the script directory) that exists as a regular file,
// reached without symlinks; empty when it does not.
std::string script_path(const std::string &name);

} // namespace nkos::system::scripts
