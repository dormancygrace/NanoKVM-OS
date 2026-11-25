#pragma once

// /api/os/update (admin), as the Go server's router/os_update.go: the update page (status, notice, channel, APK
// check/install/rollback/reboot in the helper), the Software page (installed, search, updates, removal preview,
// operations, index refresh schedule). Answers are {"code","msg","data"} with Go's error texts in msg (updateReply).

#include "http/router.hpp"

namespace nkos::osupdate {

void add_routes(http::Router &router);

// RunNoticeSchedule: the update notice is checked two minutes after the start, then when due (daily; failed
// attempts hourly). Starts a thread.
void start();

} // namespace nkos::osupdate
