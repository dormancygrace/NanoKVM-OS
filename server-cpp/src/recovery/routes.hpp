#pragma once

// /api/system/recovery (admin), as the Go server's router/recovery.go: GET the availability and status; POST
// export (an encrypted settings archive), preview and apply of a restore or reset (staged for the next boot, then
// a reboot), and reboot of a pending recovery. Every POST needs the current administrator password and holds the
// update lock; one at a time (each runs a 600000-round PBKDF2 and may hold a 4 MiB archive).

#include "http/router.hpp"

namespace nkos::recovery {

void add_routes(http::Router &router);

} // namespace nkos::recovery
