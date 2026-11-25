#pragma once

#include "http/router.hpp"

namespace nkos::system {

// GET /api/vm/live-status.
void add_live_status_route(http::Router &router);

} // namespace nkos::system
